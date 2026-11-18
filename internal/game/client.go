// Package game 提供 hhmapi（闯关助手后端）的 HTTP 客户端与一键操作封装。
//
// 本包只依赖标准库；除 hhmapi.sujieyun.net 外不访问其他站点，也不读取任何进程内存。
// 签名规则来自对小程序客户端请求的静态分析：
//
//	X-Sign = hex(HMAC-SHA256(secret, X-Ts+"."+X-Nonce+"."+bodyJSON))
//
// 请求体必须先序列化成字节，再用同一份字节签名并发送，否则服务端会返回 bad_sign。
package game

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// 接口与签名常量（密钥来自小程序包；可用 Client.Secret 覆盖）。
const (
	// DefaultBaseURL 是 hhmapi 接口地址。
	DefaultBaseURL = "https://hhmapi.sujieyun.net/api/index.php"
	// DefaultSecret 是小程序内置的 HMAC 签名密钥。
	DefaultSecret = "e77aeb44faf254417787a5ef386fa329"
	// DefaultTimeout 是单次 HTTP 请求超时。
	DefaultTimeout = 25 * time.Second
	// DefaultThrottle 是默认请求节流间隔。
	DefaultThrottle = 200 * time.Millisecond
	// DefaultRetries 是单次请求的最大尝试次数（含首次）。
	DefaultRetries = 4

	// MaxLevel 是主关卡上限（实测关卡表）。
	MaxLevel = 220
	// SudokuLevels 是数独喵关卡数。
	SudokuLevels = 200
	// FishLevels 是小鱼收集关卡数。
	FishLevels = 40
	// LevelWin 是关卡结果标记。
	LevelWin = "win"

	// clientID 是固定的客户端标识。
	clientID = "mg"
	// nonceLen 是 X-Nonce 长度。
	nonceLen = 10
	// maxRespBytes 是响应体读取上限。
	maxRespBytes = 8 << 20
	// codeBadResponse 表示响应不是合法 JSON。
	codeBadResponse = "bad_response"
)

const nonceChars = "abcdefghijklmnopqrstuvwxyz0123456789"

var defaultHTTPClient = &http.Client{Timeout: DefaultTimeout}

// Client 是 hhmapi 客户端。请用 New 创建；零值字段会回退到默认值。
type Client struct {
	// BaseURL 是接口脚本地址，不含查询串。
	BaseURL string
	// Secret 是 HMAC 签名密钥。
	Secret string
	// HTTP 是底层 HTTP 客户端；为 nil 时使用 25s 超时的默认客户端。
	HTTP *http.Client
	// Throttle 是请求之间的节流间隔；New 默认为 200ms，设为 0 表示不节流（测试用）。
	Throttle time.Duration
	// Retries 是单次请求的最大尝试次数（含首次）；New 默认为 4，<=0 时按 1 次处理。
	Retries int
}

// New 返回带默认地址、密钥、25s 超时与 200ms 节流的客户端。
func New() *Client {
	return &Client{
		BaseURL:  DefaultBaseURL,
		Secret:   DefaultSecret,
		HTTP:     &http.Client{Timeout: DefaultTimeout},
		Throttle: DefaultThrottle,
		Retries:  DefaultRetries,
	}
}

func (c *Client) baseURL() string {
	if strings.TrimSpace(c.BaseURL) == "" {
		return DefaultBaseURL
	}
	return strings.TrimRight(c.BaseURL, "/")
}

func (c *Client) secretKey() string {
	if c.Secret == "" {
		return DefaultSecret
	}
	return c.Secret
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return defaultHTTPClient
}

// attemptCount 返回单次请求的最大尝试次数。
func (c *Client) attemptCount() int {
	if c.Retries <= 0 {
		return 1
	}
	return c.Retries
}

// sleep 按 d 休眠；Throttle 更小时以 Throttle 为准（测试里把它设为 0 即可关闭节流）。
func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Throttle < d {
		d = c.Throttle
	}
	return sleepCtx(ctx, d)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Sign 计算请求签名：hex(HMAC-SHA256(secret, ts+"."+nonce+"."+body))。
func Sign(secret, ts, nonce, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write([]byte(nonce))
	mac.Write([]byte("."))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

// newNonce 生成 10 位 [a-z0-9] 随机串。
func newNonce() string {
	b := make([]byte, nonceLen)
	if _, err := rand.Read(b); err != nil {
		// 熵源异常时退化为时间戳派生，仍保证长度与字符集合法。
		n := time.Now().UnixNano()
		for i := range b {
			b[i] = nonceChars[int(n>>(uint(i)*6))%len(nonceChars)]
		}
		return string(b)
	}
	for i := range b {
		b[i] = nonceChars[int(b[i])%len(nonceChars)]
	}
	return string(b)
}

func nowTs() string {
	return strconv.FormatInt(time.Now().Unix(), 10)
}

// APIError 是服务端返回 ok=false、HTTP 错误或响应无法解析时的错误。
type APIError struct {
	// Code 是服务端的错误码（如 bad_sign、wx_login_failed），可能为空。
	Code string
	// Message 是服务端的错误描述或本地诊断信息。
	Message string
	// Status 是 HTTP 状态码；0 表示请求未得到响应。
	Status int
}

// Error 实现 error 接口。
func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = "接口请求失败"
	}
	if e.Code != "" {
		return fmt.Sprintf("%s（%s）", msg, e.Code)
	}
	return msg
}

func newAPIError(j map[string]any, status int) *APIError {
	code := strOf(j["code"])
	msg := strOf(j["error"])
	if msg == "" {
		msg = strOf(j["msg"])
	}
	if msg == "" {
		msg = "请求被服务器拒绝"
	}
	return &APIError{Code: code, Message: msg, Status: status}
}

// retryable 判断错误是否值得重试：网络错误、5xx、响应不可解析时重试；
// 参数/签名类 4xx 直接返回，避免无意义请求。
func retryable(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		if ae.Code == codeBadResponse {
			return true
		}
		return ae.Status >= 500
	}
	return true
}

// call 执行一次接口调用（带重试）。body 为 nil 表示 GET，否则 POST 并签名。
func (c *Client) call(ctx context.Context, action, token string, query url.Values, body []byte) (map[string]any, error) {
	return c.callAttempts(ctx, action, token, query, body, c.attemptCount())
}

// callAttempts 以固定尝试次数执行接口调用。
func (c *Client) callAttempts(ctx context.Context, action, token string, query url.Values, body []byte, attempts int) (map[string]any, error) {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 1; i <= attempts; i++ {
		j, err := c.callOnce(ctx, action, token, query, body)
		if err == nil {
			return j, nil
		}
		lastErr = err
		if !retryable(err) || i == attempts {
			break
		}
		if err := sleepCtx(ctx, c.Throttle); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

// callOnce 发送单次请求。HTTP 非 200 也会尝试解析 JSON。
func (c *Client) callOnce(ctx context.Context, action, token string, query url.Values, body []byte) (map[string]any, error) {
	u := c.baseURL() + "?a=" + url.QueryEscape(action)
	if len(query) > 0 {
		u += "&" + query.Encode()
	}
	method := http.MethodGet
	var reader io.Reader
	if body != nil {
		method = http.MethodPost
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, fmt.Errorf("构建请求失败：%w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Client", clientID)
	if token != "" {
		req.Header.Set("X-Auth-Token", token)
	}
	if body != nil {
		// 签名与发送必须使用同一份字节。
		ts, nonce := nowTs(), newNonce()
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Ts", ts)
		req.Header.Set("X-Nonce", nonce)
		req.Header.Set("X-Sign", Sign(c.secretKey(), ts, nonce, string(body)))
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("网络错误：%w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return nil, &APIError{Code: codeBadResponse, Message: fmt.Sprintf("读取响应失败：%v", err), Status: resp.StatusCode}
	}
	var j map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&j); err != nil {
		return nil, &APIError{
			Code:    codeBadResponse,
			Message: fmt.Sprintf("服务器响应解析失败（HTTP %d）", resp.StatusCode),
			Status:  resp.StatusCode,
		}
	}
	if ok, _ := j["ok"].(bool); !ok {
		return nil, newAPIError(j, resp.StatusCode)
	}
	return j, nil
}

// payloadOf 取出响应中的 data 对象；没有 data 时退回整个响应，兼容旧格式。
func payloadOf(j map[string]any) map[string]any {
	if d, ok := j["data"].(map[string]any); ok && d != nil {
		return d
	}
	return j
}

// LevelEntry 是一条关卡成绩（a=submit 的 entries 元素）。
type LevelEntry struct {
	Level  int    `json:"level"`
	Result string `json:"result"`
	Steps  int    `json:"steps"`
	T      int64  `json:"t"`
	// ResetAt 是服务端进度重置时间；旧格式可省略。
	ResetAt int64 `json:"resetAt,omitempty"`
}

// LevelRecord 是 submit 回执里的单关记录。
type LevelRecord struct {
	Level     int `json:"level"`
	Stars     int `json:"stars"`
	BestSteps int `json:"best_steps"`
	Clears    int `json:"clears"`
	Fails     int `json:"fails"`
}

// ModeEntry 是一条模式成绩（a=modes 的 entries 元素）。
//
// 不同 t 使用不同字段，直接以 JSON 对象表示：
//
//	fish    {t, level, steps, at}
//	endless {t, floor, week, at}
//	sudoku  {t, level, stars, at}
//	match   {t, stage, at}
//	daily   {t, day, steps, at}
//
// 之所以用 map 而不是结构体，是为了保证 fish level=0 这类零值不会被 omitempty 丢掉。
type ModeEntry map[string]any

// FishEntry 构造小鱼收集条目。
func FishEntry(level, steps int) ModeEntry {
	return ModeEntry{"t": "fish", "level": level, "steps": steps, "at": time.Now().UnixMilli()}
}

// EndlessEntry 构造无尽模式条目；week 形如 2026-11-12。
func EndlessEntry(floor int, week string) ModeEntry {
	e := ModeEntry{"t": "endless", "floor": floor, "at": time.Now().UnixMilli()}
	if week != "" {
		e["week"] = week
	}
	return e
}

// SudokuEntry 构造数独喵条目。
func SudokuEntry(level, stars int) ModeEntry {
	return ModeEntry{"t": "sudoku", "level": level, "stars": stars, "at": time.Now().UnixMilli()}
}

// MatchEntry 构造消消喵条目。
func MatchEntry(stage int) ModeEntry {
	return ModeEntry{"t": "match", "stage": stage, "at": time.Now().UnixMilli()}
}

// SubmitResult 是 a=submit / a=modes 的结果摘要。
type SubmitResult struct {
	Accepted int            `json:"accepted"`
	Rejected int            `json:"rejected"`
	User     *User          `json:"user,omitempty"`
	Records  []LevelRecord  `json:"records,omitempty"`
	Modes    map[string]any `json:"modes,omitempty"`
	// Raw 是服务端 data 原始对象。
	Raw map[string]any `json:"-"`
}

// LoginResult 是 a=wxlogin 的结果。
type LoginResult struct {
	// Token 是长期有效的 X-Auth-Token（48 hex）。
	Token string
	// User 是登录后返回的账号信息，可能为空。
	User *User
	// IsNewAccount 表示服务端判定为新账号。
	IsNewAccount bool
	// Raw 是服务端 data 原始对象。
	Raw map[string]any
}

// submitResultOf 把响应转换成 SubmitResult。
func submitResultOf(j map[string]any) SubmitResult {
	p := payloadOf(j)
	res := SubmitResult{
		Accepted: numOf(p["accepted"]),
		Rejected: numOf(p["rejected"]),
		Raw:      p,
	}
	if u := mapOf(p["user"]); u != nil {
		res.User = parseUser(u, mapOf(p["modes"]), mapOf(p["items"]))
	}
	if recs, ok := p["records"].([]any); ok {
		for _, item := range recs {
			m := mapOf(item)
			if m == nil {
				continue
			}
			res.Records = append(res.Records, LevelRecord{
				Level:     numOf(m["level"]),
				Stars:     numOf(m["stars"]),
				BestSteps: numOf(m["best_steps"]),
				Clears:    numOf(m["clears"]),
				Fails:     numOf(m["fails"]),
			})
		}
	}
	if modes := mapOf(p["modes"]); modes != nil {
		res.Modes = modes
	}
	return res
}

// Login 用微信 wx.login 的 code 换取 X-Auth-Token（POST ?a=wxlogin）。
// 登录失败不做重试，避免消耗微信接口配额。
func (c *Client) Login(ctx context.Context, code string) (*LoginResult, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, errors.New("code 不能为空")
	}
	body, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		return nil, err
	}
	j, err := c.callAttempts(ctx, "wxlogin", "", nil, body, 1)
	if err != nil {
		return nil, err
	}
	p := payloadOf(j)
	token := strOf(p["token"])
	if token == "" {
		token = strOf(j["token"])
	}
	if token == "" {
		return nil, &APIError{Code: codeBadResponse, Message: "wxlogin 未返回 token"}
	}
	res := &LoginResult{
		Token:        token,
		IsNewAccount: boolOf(p["isNewAccount"]),
		Raw:          p,
	}
	if u := mapOf(p["user"]); u != nil {
		res.User = parseUser(u, mapOf(p["modes"]), mapOf(p["items"]))
	}
	return res, nil
}

// Me 读取账号信息（GET ?a=me），包含 user / modes / items。
func (c *Client) Me(ctx context.Context, token string) (*User, error) {
	j, err := c.call(ctx, "me", token, nil, nil)
	if err != nil {
		return nil, err
	}
	u := parseMe(payloadOf(j))
	if u == nil {
		return nil, &APIError{Code: "empty_user", Message: "账号信息为空，Token 可能已失效"}
	}
	return u, nil
}

// Config 读取游戏配置（GET ?a=config），返回 data 对象。
func (c *Client) Config(ctx context.Context) (map[string]any, error) {
	j, err := c.call(ctx, "config", "", nil, nil)
	if err != nil {
		return nil, err
	}
	return payloadOf(j), nil
}

// Rank 读取主榜（GET ?a=rank&type=...），typ 为空时按 stars。
func (c *Client) Rank(ctx context.Context, typ string) (map[string]any, error) {
	if typ == "" {
		typ = "stars"
	}
	return c.leaderboard(ctx, "rank", typ)
}

// MRank 读取模式榜（GET ?a=mrank&type=...），typ 为空时按 sudoku。
func (c *Client) MRank(ctx context.Context, typ string) (map[string]any, error) {
	if typ == "" {
		typ = "sudoku"
	}
	return c.leaderboard(ctx, "mrank", typ)
}

func (c *Client) leaderboard(ctx context.Context, action, typ string) (map[string]any, error) {
	j, err := c.call(ctx, action, "", url.Values{"type": {typ}}, nil)
	if err != nil {
		return nil, err
	}
	return payloadOf(j), nil
}

// SubmitLevels 提交关卡成绩（POST ?a=submit）。
func (c *Client) SubmitLevels(ctx context.Context, token string, entries []LevelEntry) (SubmitResult, error) {
	if len(entries) == 0 {
		return SubmitResult{}, errors.New("entries 不能为空")
	}
	body, err := json.Marshal(map[string]any{"entries": entries})
	if err != nil {
		return SubmitResult{}, err
	}
	j, err := c.call(ctx, "submit", token, nil, body)
	if err != nil {
		return SubmitResult{}, err
	}
	return submitResultOf(j), nil
}

// SubmitModes 提交模式成绩（POST ?a=modes）。
func (c *Client) SubmitModes(ctx context.Context, token string, entries []ModeEntry) (SubmitResult, error) {
	if len(entries) == 0 {
		return SubmitResult{}, errors.New("entries 不能为空")
	}
	body, err := json.Marshal(map[string]any{"entries": entries})
	if err != nil {
		return SubmitResult{}, err
	}
	j, err := c.call(ctx, "modes", token, nil, body)
	if err != nil {
		return SubmitResult{}, err
	}
	return submitResultOf(j), nil
}

// SubmitItems 同步道具/钱包（POST ?a=items）；服务端整包接受客户端值。
func (c *Client) SubmitItems(ctx context.Context, token string, payload map[string]any) (map[string]any, error) {
	if payload == nil {
		return nil, errors.New("payload 不能为空")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	j, err := c.call(ctx, "items", token, nil, body)
	if err != nil {
		return nil, err
	}
	return payloadOf(j), nil
}

// AdLog 上报广告观看记录（POST ?a=adlog），返回服务端接受条数。
func (c *Client) AdLog(ctx context.Context, token string, entries []any) (int, error) {
	if len(entries) == 0 {
		return 0, errors.New("entries 不能为空")
	}
	body, err := json.Marshal(map[string]any{"entries": entries})
	if err != nil {
		return 0, err
	}
	j, err := c.call(ctx, "adlog", token, nil, body)
	if err != nil {
		return 0, err
	}
	return numOf(payloadOf(j)["accepted"]), nil
}
