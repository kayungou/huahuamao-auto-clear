package game

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

var (
	noncePattern = regexp.MustCompile(`^[a-z0-9]{10}$`)
	signPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	datePattern  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// recordedCall 是一条被假服务器捕获的请求。
type recordedCall struct {
	Action string
	Method string
	Token  string
	Header http.Header
	Body   []byte
	Query  url.Values
}

// fakeAPI 是 httptest 假服务端：校验签名头后按 action 交给测试提供的 handle。
type fakeAPI struct {
	t      *testing.T
	secret string

	mu    sync.Mutex
	calls []recordedCall

	handle func(call recordedCall) (status int, payload any)
}

// newFakeAPI 启动假服务端；handle 返回 (HTTP 状态码, JSON 体)。
// payload 可以是 map、string（原样写入）或 nil。
func newFakeAPI(t *testing.T, handle func(call recordedCall) (int, any)) (*fakeAPI, *httptest.Server) {
	t.Helper()
	f := &fakeAPI{t: t, secret: DefaultSecret, handle: handle}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Errorf("读取请求体失败：%v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	action := r.URL.Query().Get("a")
	if action == "" {
		f.t.Errorf("请求缺少 a 参数：%s", r.URL.String())
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodPost {
		f.verifySignature(action, r, body)
	} else if got := r.Header.Get("X-Sign"); got != "" {
		f.t.Errorf("%s 的 GET 请求不应带 X-Sign，实际 %q", action, got)
	}
	if got := r.Header.Get("X-Client"); got != clientID {
		f.t.Errorf("%s 的 X-Client = %q，应为 %q", action, got, clientID)
	}
	call := recordedCall{
		Action: action,
		Method: r.Method,
		Token:  r.Header.Get("X-Auth-Token"),
		Header: r.Header.Clone(),
		Body:   body,
		Query:  r.URL.Query(),
	}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()

	status, payload := f.handle(call)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	switch v := payload.(type) {
	case nil:
		_, _ = w.Write([]byte("{}"))
	case string:
		_, _ = io.WriteString(w, v)
	default:
		raw, merr := json.Marshal(v)
		if merr != nil {
			f.t.Errorf("序列化假响应失败：%v", merr)
			return
		}
		_, _ = w.Write(raw)
	}
}

// verifySignature 用服务端密钥重算 HMAC，验证签名头与请求体逐字节一致。
func (f *fakeAPI) verifySignature(action string, r *http.Request, body []byte) {
	ts := r.Header.Get("X-Ts")
	nonce := r.Header.Get("X-Nonce")
	sig := r.Header.Get("X-Sign")
	if ts == "" || nonce == "" || sig == "" {
		f.t.Errorf("%s 缺少签名头：X-Ts=%q X-Nonce=%q X-Sign=%q", action, ts, nonce, sig)
		return
	}
	if _, err := strconv.ParseInt(ts, 10, 64); err != nil {
		f.t.Errorf("%s 的 X-Ts 不是 Unix 秒：%q", action, ts)
	} else if len(ts) != 10 {
		f.t.Errorf("%s 的 X-Ts 长度 = %d，应为 10：%q", action, len(ts), ts)
	}
	if !noncePattern.MatchString(nonce) {
		f.t.Errorf("%s 的 X-Nonce = %q，应匹配 [a-z0-9]{10}", action, nonce)
	}
	if !signPattern.MatchString(sig) {
		f.t.Errorf("%s 的 X-Sign = %q，应为 64 位小写 hex", action, sig)
	}
	mac := hmac.New(sha256.New, []byte(f.secret))
	mac.Write([]byte(ts + "." + nonce + "." + string(body)))
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		f.t.Errorf("%s 签名不匹配：服务端重算 %s，客户端 %s，body=%s", action, want, sig, body)
	}
}

// callsFor 返回某个 action 的请求（按时间顺序）。
func (f *fakeAPI) callsFor(action string) []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedCall, 0, len(f.calls))
	for _, c := range f.calls {
		if c.Action == action {
			out = append(out, c)
		}
	}
	return out
}

// totalCalls 返回请求总数。
func (f *fakeAPI) totalCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// newTestClient 返回指向假服务器的客户端（关闭节流、关闭重试）。
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c := New()
	c.BaseURL = srv.URL
	c.Throttle = 0
	c.Retries = 1
	return c
}

func okData(data map[string]any) map[string]any {
	return map[string]any{"ok": true, "data": data}
}

// bodyObject 解析请求体；失败只报错并返回 nil（避免在 HTTP 协程里 Fatal）。
func bodyObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Errorf("请求体不是合法 JSON：%v（%s）", err, body)
		return nil
	}
	return m
}

// bodyEntries 解析请求体里的 entries 数组。
func bodyEntries(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	m := bodyObject(t, body)
	if m == nil {
		return nil
	}
	raw, _ := m["entries"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if em, ok := item.(map[string]any); ok {
			out = append(out, em)
		}
	}
	if len(out) != len(raw) {
		t.Errorf("entries 含非对象元素：%s", body)
	}
	return out
}

// asInt 从解码后的 JSON 数值取 int。
func asInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case json.Number:
		i, _ := x.Int64()
		return int(i)
	case int:
		return x
	case int64:
		return int(x)
	}
	return 0
}

// sampleMeData 是 a=me 的典型 data 对象。
func sampleMeData() map[string]any {
	return map[string]any{
		"user": map[string]any{
			"id": 32, "nickname": "示例玩家",
			"avatar": 5, "wechatAvatar": "",
			"stars": 660, "cleared": 220, "top": 220, "three": 220,
			"resetAt": 1712345678, "skin": 19,
			"items": map[string]any{
				"hint": 999, "can": 3, "map": 2, "pass": 0,
				"spent": 645, "at": 1793651367227,
				"sk": 0, "skin": 18, "seen": 5, "ul": 0,
			},
		},
		"records": []any{
			map[string]any{"level": 1, "stars": 3, "best_steps": 2, "clears": 4, "fails": 0},
		},
		"modes": map[string]any{
			"fish":    map[string]any{"0": 3, "1": 2},
			"endless": map[string]any{"best": 200, "week": "2026-11-12", "weekBest": 200},
			"sudoku":  map[string]any{"best": 2, "stars": map[string]any{"1": 3, "2": 2}},
			"match":   map[string]any{"best": 5},
		},
	}
}

func TestNewDefaults(t *testing.T) {
	c := New()
	if c.BaseURL != DefaultBaseURL {
		t.Errorf("BaseURL = %q，应为 %q", c.BaseURL, DefaultBaseURL)
	}
	if c.Secret != DefaultSecret {
		t.Errorf("Secret = %q，应为 %q", c.Secret, DefaultSecret)
	}
	if c.Throttle != DefaultThrottle {
		t.Errorf("Throttle = %v，应为 %v", c.Throttle, DefaultThrottle)
	}
	if c.Retries != DefaultRetries {
		t.Errorf("Retries = %d，应为 %d", c.Retries, DefaultRetries)
	}
	if c.HTTP == nil || c.HTTP.Timeout != DefaultTimeout {
		t.Errorf("HTTP 超时应为 %v，实际 %+v", DefaultTimeout, c.HTTP)
	}
}

// TestSignKnownVector 用实际请求中的签名做已知向量校验。
func TestSignKnownVector(t *testing.T) {
	const body = `{"entries":[{"level":1,"result":"win","steps":2,"t":1793607826749}]}`
	const want = "af5dfcb005129cd0dd885bf4d459978f0a97135d099e333dab9a43704e148fb3"
	if got := Sign(DefaultSecret, "1793607827", "8metl7ljeb", body); got != want {
		t.Fatalf("Sign = %s，应为抓包值 %s", got, want)
	}
}

func TestNonceFormat(t *testing.T) {
	for i := 0; i < 200; i++ {
		n := newNonce()
		if len(n) != nonceLen {
			t.Fatalf("nonce 长度 = %d：%q", len(n), n)
		}
		if !noncePattern.MatchString(n) {
			t.Fatalf("nonce 字符集非法：%q", n)
		}
	}
}

// TestSigningExactBody 验证「签名用的字节」与「发送的字节」完全一致。
func TestSigningExactBody(t *testing.T) {
	f, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		return http.StatusOK, okData(map[string]any{"user": map[string]any{"id": 32}})
	})
	c := newTestClient(t, srv)

	if _, err := c.SubmitItems(context.Background(), "tok-1", map[string]any{"hint": 999, "resetAt": 0}); err != nil {
		t.Fatalf("SubmitItems 失败：%v", err)
	}
	calls := f.callsFor("items")
	if len(calls) != 1 {
		t.Fatalf("items 请求数 = %d，应为 1", len(calls))
	}
	if calls[0].Method != http.MethodPost {
		t.Errorf("方法 = %s，应为 POST", calls[0].Method)
	}
	if got, want := string(calls[0].Body), `{"hint":999,"resetAt":0}`; got != want {
		t.Errorf("请求体 = %s，应为 %s", got, want)
	}
	if calls[0].Token != "tok-1" {
		t.Errorf("X-Auth-Token = %q，应为 tok-1", calls[0].Token)
	}
	if ct := calls[0].Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q，应为 application/json", ct)
	}
}

func TestLogin(t *testing.T) {
	_, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		if call.Action != "wxlogin" {
			t.Errorf("action = %q，应为 wxlogin", call.Action)
		}
		if call.Method != http.MethodPost {
			t.Errorf("wxlogin 方法 = %s，应为 POST", call.Method)
		}
		body := bodyObject(t, call.Body)
		if body == nil {
			return http.StatusBadRequest, map[string]any{"ok": false}
		}
		if body["code"] != "code-abc" {
			t.Errorf("code = %v，应为 code-abc", body["code"])
		}
		return http.StatusOK, okData(map[string]any{
			"token":        "tok-1",
			"isNewAccount": true,
			"user":         map[string]any{"id": 9, "nickname": "玩家", "stars": 3},
		})
	})
	c := newTestClient(t, srv)

	res, err := c.Login(context.Background(), "  code-abc  ")
	if err != nil {
		t.Fatalf("Login 失败：%v", err)
	}
	if res.Token != "tok-1" {
		t.Errorf("Token = %q，应为 tok-1", res.Token)
	}
	if !res.IsNewAccount {
		t.Error("IsNewAccount 应为 true")
	}
	if res.User == nil || res.User.ID != 9 || res.User.Nickname != "玩家" || res.User.Stars != 3 {
		t.Errorf("User = %+v，字段不符", res.User)
	}
	if res.Raw["token"] != "tok-1" {
		t.Errorf("Raw 未保留 data：%+v", res.Raw)
	}
}

func TestLoginEmptyCode(t *testing.T) {
	f, srv := newFakeAPI(t, func(recordedCall) (int, any) {
		t.Error("空 code 不应发请求")
		return http.StatusOK, okData(nil)
	})
	c := newTestClient(t, srv)
	if _, err := c.Login(context.Background(), "   "); err == nil {
		t.Fatal("空 code 应返回错误")
	}
	if f.totalCalls() != 0 {
		t.Errorf("空 code 发出了 %d 个请求", f.totalCalls())
	}
}

func TestMe(t *testing.T) {
	f, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		if call.Method != http.MethodGet {
			t.Errorf("me 方法 = %s，应为 GET", call.Method)
		}
		return http.StatusOK, okData(sampleMeData())
	})
	c := newTestClient(t, srv)

	u, err := c.Me(context.Background(), "tok-me")
	if err != nil {
		t.Fatalf("Me 失败：%v", err)
	}
	if u.ID != 32 || u.Nickname != "示例玩家" || u.Stars != 660 {
		t.Errorf("基础字段不符：%+v", u)
	}
	if u.Cleared != 220 || u.Top != 220 || u.Three != 220 {
		t.Errorf("进度字段不符：%+v", u)
	}
	if u.Fish[0] != 3 || u.Fish[1] != 2 || len(u.Fish) != 2 {
		t.Errorf("Fish 不符：%+v", u.Fish)
	}
	if u.Endless != 200 {
		t.Errorf("Endless = %d，应为 200", u.Endless)
	}
	if u.SudokuBest != 2 || u.SudokuStars[1] != 3 || u.SudokuStars[2] != 2 {
		t.Errorf("数独字段不符：best=%d stars=%+v", u.SudokuBest, u.SudokuStars)
	}
	if u.MatchBest != 5 {
		t.Errorf("MatchBest = %d，应为 5", u.MatchBest)
	}
	if u.Items["hint"] != 999 || u.Items["spent"] != 645 || u.Items["skin"] != 18 {
		t.Errorf("Items 不符：%+v", u.Items)
	}
	if u.ResetAt != 1712345678 {
		t.Errorf("ResetAt = %d，应为 1712345678", u.ResetAt)
	}
	if u.Skin != "19" {
		t.Errorf("Skin = %q，应为 \"19\"", u.Skin)
	}
	if u.Avatar != 5 || u.WechatAvatar != "" {
		t.Errorf("Avatar/WechatAvatar = %d/%q", u.Avatar, u.WechatAvatar)
	}
	if len(u.Records) != 1 || u.Records[0] != (Record{Level: 1, Stars: 3, BestSteps: 2, Clears: 4}) {
		t.Errorf("Records 不符：%+v", u.Records)
	}
	if u.Modes == nil || u.Modes.Fish[0] != 3 || u.Modes.Sudoku.Stars[1] != 3 || u.Modes.Match.Best != 5 {
		t.Errorf("Modes 不符：%+v", u.Modes)
	}
	if calls := f.callsFor("me"); len(calls) != 1 || calls[0].Token != "tok-me" {
		t.Errorf("me 请求头不符：%+v", calls)
	}
}

func TestSubmitLevels(t *testing.T) {
	_, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		entries := bodyEntries(t, call.Body)
		if len(entries) != 1 {
			t.Errorf("entries 条数 = %d，应为 1", len(entries))
		}
		return http.StatusOK, okData(map[string]any{
			"accepted": 1, "rejected": 0,
			"user":    map[string]any{"id": 32, "stars": 3, "cleared": 1},
			"records": []any{map[string]any{"level": 7, "stars": 3, "best_steps": 12, "clears": 1, "fails": 0}},
		})
	})
	c := newTestClient(t, srv)

	res, err := c.SubmitLevels(context.Background(), "tok", []LevelEntry{
		{Level: 7, Result: LevelWin, Steps: 12, T: 123},
	})
	if err != nil {
		t.Fatalf("SubmitLevels 失败：%v", err)
	}
	if res.Accepted != 1 || res.Rejected != 0 {
		t.Errorf("accepted/rejected = %d/%d", res.Accepted, res.Rejected)
	}
	if res.User == nil || res.User.Stars != 3 {
		t.Errorf("User 不符：%+v", res.User)
	}
	if len(res.Records) != 1 || res.Records[0].Level != 7 || res.Records[0].BestSteps != 12 {
		t.Errorf("Records 不符：%+v", res.Records)
	}
	if len(res.Raw) == 0 {
		t.Error("Raw 应保留服务端 data")
	}
}

func TestSubmitLevelsExactBody(t *testing.T) {
	f, srv := newFakeAPI(t, func(recordedCall) (int, any) {
		return http.StatusOK, okData(map[string]any{"accepted": 1})
	})
	c := newTestClient(t, srv)
	if _, err := c.SubmitLevels(context.Background(), "tok", []LevelEntry{{Level: 7, Result: LevelWin, Steps: 12, T: 123}}); err != nil {
		t.Fatalf("SubmitLevels 失败：%v", err)
	}
	calls := f.callsFor("submit")
	if len(calls) != 1 {
		t.Fatalf("submit 请求数 = %d", len(calls))
	}
	want := `{"entries":[{"level":7,"result":"win","steps":12,"t":123}]}`
	if got := string(calls[0].Body); got != want {
		t.Errorf("请求体 = %s，应为 %s", got, want)
	}
}

func TestSubmitModes(t *testing.T) {
	f, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		entries := bodyEntries(t, call.Body)
		if len(entries) != 4 {
			t.Errorf("entries 条数 = %d，应为 4", len(entries))
		}
		return http.StatusOK, okData(map[string]any{
			"accepted": 4, "rejected": 0,
			"modes": map[string]any{"match": map[string]any{"best": 200}},
		})
	})
	c := newTestClient(t, srv)

	res, err := c.SubmitModes(context.Background(), "tok", []ModeEntry{
		FishEntry(0, 12),
		SudokuEntry(5, 3),
		EndlessEntry(200, "2026-11-12"),
		MatchEntry(200),
	})
	if err != nil {
		t.Fatalf("SubmitModes 失败：%v", err)
	}
	if res.Accepted != 4 {
		t.Errorf("accepted = %d，应为 4", res.Accepted)
	}
	if res.Modes == nil {
		t.Error("Modes 应保留服务端回执")
	}
	calls := f.callsFor("modes")
	if len(calls) != 1 {
		t.Fatalf("modes 请求数 = %d", len(calls))
	}
	entries := bodyEntries(t, calls[0].Body)
	// fish 的 level=0 是有效值，不能被省略。
	if v, ok := entries[0]["level"]; !ok || asInt(v) != 0 {
		t.Errorf("fish 条目缺少 level=0：%v", entries[0])
	}
	if entries[0]["t"] != "fish" || asInt(entries[0]["steps"]) != 12 {
		t.Errorf("fish 条目不符：%v", entries[0])
	}
	if asInt(entries[1]["stars"]) != 3 || asInt(entries[1]["level"]) != 5 {
		t.Errorf("sudoku 条目不符：%v", entries[1])
	}
	if asInt(entries[2]["floor"]) != 200 || entries[2]["week"] != "2026-11-12" {
		t.Errorf("endless 条目不符：%v", entries[2])
	}
	if asInt(entries[3]["stage"]) != 200 {
		t.Errorf("match 条目不符：%v", entries[3])
	}
	for i, e := range entries {
		if _, ok := e["at"]; !ok {
			t.Errorf("entries[%d] 缺少 at", i)
		}
	}
}

func TestSubmitItems(t *testing.T) {
	_, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		return http.StatusOK, okData(map[string]any{
			"user": map[string]any{"id": 32, "items": map[string]any{"pass": 999}},
		})
	})
	c := newTestClient(t, srv)

	data, err := c.SubmitItems(context.Background(), "tok", map[string]any{"pass": 999})
	if err != nil {
		t.Fatalf("SubmitItems 失败：%v", err)
	}
	u, _ := data["user"].(map[string]any)
	if u == nil {
		t.Fatalf("data 缺少 user：%+v", data)
	}
}

func TestAdLog(t *testing.T) {
	f, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		entries := bodyEntries(t, call.Body)
		if len(entries) != 2 {
			t.Errorf("广告 entries 条数 = %d，应为 2", len(entries))
		}
		return http.StatusOK, okData(map[string]any{"accepted": 2})
	})
	c := newTestClient(t, srv)

	n, err := c.AdLog(context.Background(), "tok", []any{
		map[string]any{"scene": "undo", "level": 1, "result": "ok", "t": 1},
		map[string]any{"scene": "inter", "level": 1, "result": "ok", "t": 1},
	})
	if err != nil {
		t.Fatalf("AdLog 失败：%v", err)
	}
	if n != 2 {
		t.Errorf("accepted = %d，应为 2", n)
	}
	if len(f.callsFor("adlog")) != 1 {
		t.Errorf("adlog 请求数 = %d，应为 1", len(f.callsFor("adlog")))
	}
}

// TestAPIErrorOKFalse 覆盖 ok=false：错误里要带 code/error。
func TestAPIErrorOKFalse(t *testing.T) {
	f, srv := newFakeAPI(t, func(recordedCall) (int, any) {
		return http.StatusOK, `{"ok":false,"code":"bad_sign","error":"请求已过期，请检查手机时间"}`
	})
	c := newTestClient(t, srv)

	_, err := c.SubmitItems(context.Background(), "tok", map[string]any{"pass": 1})
	if err == nil {
		t.Fatal("ok=false 应返回错误")
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("错误类型 = %T，应为 *APIError", err)
	}
	if ae.Code != "bad_sign" || ae.Status != http.StatusOK {
		t.Errorf("APIError = %+v", ae)
	}
	if !strings.Contains(err.Error(), "请求已过期") || !strings.Contains(err.Error(), "bad_sign") {
		t.Errorf("错误信息应含 code 与 error：%v", err)
	}
	if got := f.totalCalls(); got != 1 {
		t.Errorf("4xx/ok=false 不应重试，实际请求 %d 次", got)
	}
}

// TestHTTP500ParsedJSON 覆盖 HTTP 500 + JSON 错误体，并按 Retries 重试。
func TestHTTP500ParsedJSON(t *testing.T) {
	f, srv := newFakeAPI(t, func(recordedCall) (int, any) {
		return http.StatusInternalServerError, `{"ok":false,"code":"internal","error":"boom"}`
	})
	c := newTestClient(t, srv)
	c.Retries = DefaultRetries

	_, err := c.SubmitItems(context.Background(), "tok", map[string]any{"pass": 1})
	if err == nil {
		t.Fatal("HTTP 500 应返回错误")
	}
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusInternalServerError {
		t.Fatalf("APIError = %+v", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("错误信息应含 error 字段：%v", err)
	}
	if got := f.totalCalls(); got != DefaultRetries {
		t.Errorf("5xx 应重试 %d 次，实际请求 %d 次", DefaultRetries, got)
	}
}

// TestNon200WithOKBody 验证「HTTP 非 200 也解析 JSON」。
func TestNon200WithOKBody(t *testing.T) {
	_, srv := newFakeAPI(t, func(recordedCall) (int, any) {
		return http.StatusBadGateway, `{"ok":true,"data":{"token":"tok-502","user":{"id":7,"nickname":"x"}}}`
	})
	c := newTestClient(t, srv)

	res, err := c.Login(context.Background(), "code")
	if err != nil {
		t.Fatalf("HTTP 502 但 JSON ok=true 时不应报错：%v", err)
	}
	if res.Token != "tok-502" || res.User == nil || res.User.ID != 7 {
		t.Errorf("结果不符：%+v", res)
	}
}

// TestBadJSONResponse 覆盖非 JSON 响应：错误可重试且带 bad_response。
func TestBadJSONResponse(t *testing.T) {
	f, srv := newFakeAPI(t, func(recordedCall) (int, any) {
		return http.StatusOK, "<html>gateway error</html>"
	})
	c := newTestClient(t, srv)
	c.Retries = 2

	_, err := c.SubmitItems(context.Background(), "tok", map[string]any{"pass": 1})
	if err == nil {
		t.Fatal("非 JSON 响应应返回错误")
	}
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != codeBadResponse {
		t.Fatalf("APIError = %+v", err)
	}
	if got := f.totalCalls(); got != 2 {
		t.Errorf("响应不可解析应重试，实际请求 %d 次", got)
	}
}

func TestRankAndConfig(t *testing.T) {
	_, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		switch call.Action {
		case "rank", "mrank":
			return http.StatusOK, okData(map[string]any{"total": 12, "list": []any{}})
		case "config":
			if call.Method != http.MethodGet {
				t.Errorf("config 方法 = %s，应为 GET", call.Method)
			}
			return http.StatusOK, okData(map[string]any{"adsOn": false, "adDaily": 3})
		}
		return http.StatusOK, okData(nil)
	})
	c := newTestClient(t, srv)

	rank, err := c.Rank(context.Background(), "")
	if err != nil {
		t.Fatalf("Rank 失败：%v", err)
	}
	if asInt(rank["total"]) != 12 {
		t.Errorf("rank.total = %v，应为 12", rank["total"])
	}
	mrank, err := c.MRank(context.Background(), "sudoku")
	if err != nil {
		t.Fatalf("MRank 失败：%v", err)
	}
	if asInt(mrank["total"]) != 12 {
		t.Errorf("mrank.total = %v，应为 12", mrank["total"])
	}
	cfg, err := c.Config(context.Background())
	if err != nil {
		t.Fatalf("Config 失败：%v", err)
	}
	if cfg["adsOn"] != false {
		t.Errorf("config.adsOn = %v，应为 false", cfg["adsOn"])
	}
}

// minStepsFor 是假服务端每关的最小可接受步数。
func minStepsFor(level int) int { return 6 + level%4 }

func TestClearLevels(t *testing.T) {
	lastSteps := map[int]int{}
	f, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		switch call.Action {
		case "me":
			// 没有 records：全部关卡都走探测。
			return http.StatusOK, okData(map[string]any{"user": map[string]any{"id": 32}})
		case "submit":
			entries := bodyEntries(t, call.Body)
			records := make([]any, 0, len(entries))
			accepted, rejected := 0, 0
			for _, e := range entries {
				level := asInt(e["level"])
				steps := asInt(e["steps"])
				lastSteps[level] = steps
				if steps >= minStepsFor(level) && steps <= maxLevelSteps {
					accepted++
					records = append(records, map[string]any{
						"level": level, "stars": 3, "best_steps": steps, "clears": 1, "fails": 0,
					})
				} else {
					rejected++
				}
			}
			return http.StatusOK, okData(map[string]any{
				"accepted": accepted, "rejected": rejected, "records": records,
			})
		}
		return http.StatusOK, okData(nil)
	})
	c := newTestClient(t, srv)

	var phases []string
	var lastCur, lastTotal, finalCur, finalTotal int
	res, err := c.ClearLevels(context.Background(), "tok", 1, 4, Options{
		Progress: func(phase string, cur, total int) bool {
			phases = append(phases, phase)
			lastCur, lastTotal = cur, total
			return true
		},
		Log: func(string) {},
	})
	if err != nil {
		t.Fatalf("ClearLevels 失败：%v", err)
	}
	finalCur, finalTotal = lastCur, lastTotal
	if finalCur != 4 || finalTotal != 4 {
		t.Errorf("最终进度 = %d/%d，应为 4/4", finalCur, finalTotal)
	}
	if len(phases) == 0 || phases[0] != PhaseLevels {
		t.Errorf("阶段名 = %v，应包含 %q", phases, PhaseLevels)
	}
	if asInt(res["completed"]) != 4 || asInt(res["skipped"]) != 0 {
		t.Errorf("结果 = %+v，应为 completed=4 skipped=0", res)
	}
	for level := 1; level <= 4; level++ {
		if got, want := lastSteps[level], minStepsFor(level); got != want {
			t.Errorf("第 %d 关最终步数 = %d，应为最小可接受步数 %d", level, got, want)
		}
	}
	if submits := len(f.callsFor("submit")); submits == 0 || submits > 4*40 {
		t.Errorf("submit 请求数 = %d，超出预期范围", submits)
	}
}

func TestClearLevelsCancel(t *testing.T) {
	f, srv := newFakeAPI(t, func(recordedCall) (int, any) {
		t.Error("取消后不应再发提交")
		return http.StatusOK, okData(map[string]any{"accepted": 1})
	})
	c := newTestClient(t, srv)

	_, err := c.ClearLevels(context.Background(), "tok", 1, 4, Options{
		Progress: func(string, int, int) bool { return false },
	})
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v，应为 ErrCancelled", err)
	}
	if f.totalCalls() != 0 {
		t.Errorf("取消后发出了 %d 个请求", f.totalCalls())
	}
}

func TestClearLevelsInvalidRange(t *testing.T) {
	f, srv := newFakeAPI(t, func(recordedCall) (int, any) {
		return http.StatusOK, okData(nil)
	})
	c := newTestClient(t, srv)
	if _, err := c.ClearLevels(context.Background(), "tok", 10, 5, Options{}); err == nil {
		t.Fatal("from>to 应返回错误")
	}
	if f.totalCalls() != 0 {
		t.Errorf("非法区间不应发请求，实际 %d 次", f.totalCalls())
	}
}

func TestClearSudoku(t *testing.T) {
	var levels []int
	_, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		switch call.Action {
		case "me":
			return http.StatusOK, okData(sampleMeData())
		case "modes":
			entries := bodyEntries(t, call.Body)
			if len(entries) != 1 {
				t.Errorf("modes 条目数 = %d，应为 1", len(entries))
			}
			for _, e := range entries {
				if e["t"] != "sudoku" {
					t.Errorf("t = %v，应为 sudoku", e["t"])
				}
				if asInt(e["stars"]) != 3 {
					t.Errorf("stars = %v，应为 3", e["stars"])
				}
				levels = append(levels, asInt(e["level"]))
			}
			return http.StatusOK, okData(map[string]any{"accepted": 1})
		}
		return http.StatusOK, okData(nil)
	})
	c := newTestClient(t, srv)

	var lastCur, lastTotal int
	res, err := c.ClearSudoku(context.Background(), "tok", Options{
		Progress: func(_ string, cur, total int) bool { lastCur, lastTotal = cur, total; return true },
		Log:      func(string) {},
	})
	if err != nil {
		t.Fatalf("ClearSudoku 失败：%v", err)
	}
	// sampleMeData 里 1 关已三星，2..200 关待刷 = 199 关。
	if lastTotal != 199 || lastCur != 199 {
		t.Errorf("进度 = %d/%d，应为 199/199", lastCur, lastTotal)
	}
	if asInt(res["cleared"]) != 199 || asInt(res["total"]) != 199 {
		t.Errorf("结果 = %+v，应为 cleared=199 total=199", res)
	}
	if len(levels) != 199 {
		t.Errorf("提交关卡数 = %d，应为 199", len(levels))
	}
	for _, lv := range levels {
		if lv == 1 {
			t.Error("第 1 关已三星，不应再提交")
		}
		if lv < 2 || lv > SudokuLevels {
			t.Errorf("关卡越界：%d", lv)
		}
	}
}

func TestClearFish(t *testing.T) {
	var levels []int
	var stepsSeen []int
	_, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		switch call.Action {
		case "me":
			return http.StatusOK, okData(sampleMeData())
		case "modes":
			entries := bodyEntries(t, call.Body)
			for _, e := range entries {
				if e["t"] != "fish" {
					t.Errorf("t = %v，应为 fish", e["t"])
				}
				levels = append(levels, asInt(e["level"]))
				stepsSeen = append(stepsSeen, asInt(e["steps"]))
			}
			return http.StatusOK, okData(map[string]any{"accepted": 1})
		}
		return http.StatusOK, okData(nil)
	})
	c := newTestClient(t, srv)

	var lastCur, lastTotal int
	res, err := c.ClearFish(context.Background(), "tok", Options{
		Progress: func(_ string, cur, total int) bool { lastCur, lastTotal = cur, total; return true },
		Log:      func(string) {},
	})
	if err != nil {
		t.Fatalf("ClearFish 失败：%v", err)
	}
	// sampleMeData 里 0 关已 3 条鱼，1..39 关待刷 = 39 关。
	if lastTotal != 39 || lastCur != 39 {
		t.Errorf("进度 = %d/%d，应为 39/39", lastCur, lastTotal)
	}
	if asInt(res["cleared"]) != 39 {
		t.Errorf("结果 = %+v，应为 cleared=39", res)
	}
	if len(levels) != 39 {
		t.Errorf("提交关卡数 = %d，应为 39", len(levels))
	}
	for i, lv := range levels {
		if lv == 0 {
			t.Error("第 0 关已完成，不应再提交")
		}
		if stepsSeen[i] != 12 {
			t.Errorf("第 %d 关首次步数 = %d，应为 12（首个候选即被接受）", lv, stepsSeen[i])
		}
	}
}

func TestSetEndless(t *testing.T) {
	_, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		entries := bodyEntries(t, call.Body)
		if len(entries) != 1 {
			t.Fatalf("entries 条数 = %d", len(entries))
		}
		e := entries[0]
		if e["t"] != "endless" || asInt(e["floor"]) != 321 {
			t.Errorf("endless 条目不符：%v", e)
		}
		week, _ := e["week"].(string)
		if !datePattern.MatchString(week) {
			t.Errorf("week = %q，应为 YYYY-MM-DD", week)
		}
		if asInt(e["at"]) <= 0 {
			t.Errorf("at 应大于 0：%v", e["at"])
		}
		return http.StatusOK, okData(map[string]any{"accepted": 1})
	})
	c := newTestClient(t, srv)

	res, err := c.SetEndless(context.Background(), "tok", 321, Options{Log: func(string) {}})
	if err != nil {
		t.Fatalf("SetEndless 失败：%v", err)
	}
	if asInt(res["floor"]) != 321 {
		t.Errorf("结果 = %+v，应为 floor=321", res)
	}
}

func TestSetEndlessRejected(t *testing.T) {
	_, srv := newFakeAPI(t, func(recordedCall) (int, any) {
		return http.StatusOK, okData(map[string]any{"accepted": 0, "rejected": 1})
	})
	c := newTestClient(t, srv)
	if _, err := c.SetEndless(context.Background(), "tok", 1, Options{}); !errors.Is(err, ErrRejected) {
		t.Fatalf("err = %v，应为 ErrRejected", err)
	}
}

func TestSetMatch(t *testing.T) {
	// 服务端 2026-11-12 起要求 stage 逐关 +1；同一批 entries 按序逐条校验。
	cur := 5
	f, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		switch call.Action {
		case "me":
			return http.StatusOK, okData(sampleMeData())
		case "modes":
			entries := bodyEntries(t, call.Body)
			if len(entries) == 0 {
				t.Fatalf("modes 请求没有 entries")
			}
			for i, e := range entries {
				if e["t"] != "match" {
					t.Fatalf("条目 %d t=%v，应为 match", i, e["t"])
				}
				want := cur + 1 + i
				if got := asInt(e["stage"]); got != want {
					t.Fatalf("条目 %d stage=%d，应为 %d（必须逐关 +1）", i, got, want)
				}
			}
			cur += len(entries)
			return http.StatusOK, okData(map[string]any{"accepted": len(entries)})
		}
		return http.StatusOK, okData(nil)
	})
	c := newTestClient(t, srv)

	res, err := c.SetMatch(context.Background(), "tok", 200, Options{Log: func(string) {}})
	if err != nil {
		t.Fatalf("SetMatch 失败：%v", err)
	}
	if asInt(res["best"]) != 200 || asInt(res["from"]) != 5 || asInt(res["completed"]) != 195 {
		t.Errorf("结果 = %+v，应为 best=200 from=5 completed=195", res)
	}
	if cur != 200 {
		t.Errorf("服务端最终关数 = %d，应为 200", cur)
	}
	// 195 关 / 每批 20 关 = 10 批。
	if got := len(f.callsFor("modes")); got != 10 {
		t.Errorf("modes 请求数 = %d，应为 10", got)
	}
}

func TestSetMatchAlreadyDone(t *testing.T) {
	f, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		if call.Action == "me" {
			return http.StatusOK, okData(sampleMeData())
		}
		return http.StatusOK, okData(nil)
	})
	c := newTestClient(t, srv)

	res, err := c.SetMatch(context.Background(), "tok", 5, Options{Log: func(string) {}})
	if err != nil {
		t.Fatalf("SetMatch 失败：%v", err)
	}
	if asInt(res["best"]) != 5 || asInt(res["completed"]) != 0 {
		t.Errorf("结果 = %+v，应为 best=5 completed=0", res)
	}
	if got := len(f.callsFor("modes")); got != 0 {
		t.Errorf("已达标时不应提交 modes，实际 %d 次", got)
	}
}

func TestSetMatchResyncOnReject(t *testing.T) {
	// 第一次提交被拒，Me 显示服务端已到 40，之后从 41 继续。
	cur := 40
	calls := 0
	f, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		if call.Action == "me" {
			me := sampleMeData()
			me["modes"].(map[string]any)["match"] = map[string]any{"best": cur}
			return http.StatusOK, okData(me)
		}
		calls++
		if calls == 1 {
			return http.StatusOK, okData(map[string]any{"accepted": 0, "rejected": 1})
		}
		entries := bodyEntries(t, call.Body)
		cur += len(entries)
		return http.StatusOK, okData(map[string]any{"accepted": len(entries)})
	})
	c := newTestClient(t, srv)

	if _, err := c.SetMatch(context.Background(), "tok", 100, Options{Log: func(string) {}}); err != nil {
		t.Fatalf("SetMatch 失败：%v", err)
	}
	if cur != 100 {
		t.Errorf("最终关数 = %d，应为 100", cur)
	}
	if got := len(f.callsFor("modes")); got != 4 {
		t.Errorf("modes 请求数 = %d，应为 4（1 次被拒 + 60/25=3 批）", got)
	}
}

func TestSetMatchRetriesTransientReject(t *testing.T) {
	// 模拟服务端抖动：前 2 次整批 accepted=0，第 3 次开始正常。
	cur := 5
	modesCalls := 0
	_, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		switch call.Action {
		case "me":
			return http.StatusOK, okData(sampleMeData())
		case "modes":
			modesCalls++
			if modesCalls <= 2 {
				return http.StatusOK, okData(map[string]any{"accepted": 0, "rejected": 1})
			}
			entries := bodyEntries(t, call.Body)
			cur += len(entries)
			return http.StatusOK, okData(map[string]any{"accepted": len(entries)})
		}
		return http.StatusOK, okData(nil)
	})
	c := newTestClient(t, srv)

	res, err := c.SetMatch(context.Background(), "tok", 30, Options{Log: func(string) {}})
	if err != nil {
		t.Fatalf("瞬时拒绝应重试成功，实际：%v", err)
	}
	if asInt(res["best"]) != 30 || cur != 30 {
		t.Errorf("结果 = %+v，服务端 cur=%d，应为 best=30", res, cur)
	}
	// 2 次失败 + 25 关/20 = 2 批。
	if modesCalls != 4 {
		t.Errorf("modes 调用 = %d，应为 4", modesCalls)
	}
}

func TestSetMatchGivesUpAfterRetries(t *testing.T) {
	_, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		if call.Action == "me" {
			return http.StatusOK, okData(sampleMeData())
		}
		return http.StatusOK, okData(map[string]any{"accepted": 0, "rejected": 1})
	})
	c := newTestClient(t, srv)

	_, err := c.SetMatch(context.Background(), "tok", 30, Options{})
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("err = %v，应为 ErrRejected", err)
	}
	if !strings.Contains(err.Error(), "已重试") {
		t.Errorf("错误信息应包含重试次数：%v", err)
	}
}

func TestFillItemsDefault(t *testing.T) {
	var payload map[string]any
	_, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		switch call.Action {
		case "me":
			return http.StatusOK, okData(sampleMeData())
		case "items":
			payload = bodyObject(t, call.Body)
			return http.StatusOK, okData(map[string]any{"user": map[string]any{"id": 32}})
		}
		return http.StatusOK, okData(nil)
	})
	c := newTestClient(t, srv)

	if _, err := c.FillItems(context.Background(), "tok", nil, Options{Log: func(string) {}}); err != nil {
		t.Fatalf("FillItems 失败：%v", err)
	}
	if payload == nil {
		t.Fatal("未收到 items 请求")
	}
	for _, key := range []string{"hint", "can", "map", "pass"} {
		if asInt(payload[key]) != DefaultItemFill {
			t.Errorf("%s = %v，应为 %d", key, payload[key], DefaultItemFill)
		}
	}
	if asInt(payload["resetAt"]) != 1712345678 {
		t.Errorf("resetAt = %v，应为 1712345678", payload["resetAt"])
	}
	if asInt(payload["spent"]) != 645 || asInt(payload["skin"]) != 18 || asInt(payload["seen"]) != 5 {
		t.Errorf("钱包字段未沿用服务端值：%+v", payload)
	}
	if asInt(payload["sk"]) != 1 {
		t.Errorf("sk = %v，0 时应补为 1", payload["sk"])
	}
	if asInt(payload["at"]) <= 0 {
		t.Errorf("at 应大于 0：%v", payload["at"])
	}
}

func TestFillItemsExplicit(t *testing.T) {
	var payload map[string]any
	_, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		if call.Action == "items" {
			payload = bodyObject(t, call.Body)
		}
		return http.StatusOK, okData(map[string]any{})
	})
	c := newTestClient(t, srv)

	if _, err := c.FillItems(context.Background(), "tok", map[string]any{"pass": 5}, Options{}); err != nil {
		t.Fatalf("FillItems 失败：%v", err)
	}
	if len(payload) != 1 || asInt(payload["pass"]) != 5 {
		t.Errorf("payload 应原样提交：%+v", payload)
	}
}

func TestRunAdLogDefault(t *testing.T) {
	var entries []map[string]any
	_, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		entries = bodyEntries(t, call.Body)
		return http.StatusOK, okData(map[string]any{"accepted": len(entries)})
	})
	c := newTestClient(t, srv)

	res, err := c.RunAdLog(context.Background(), "tok", 1, Options{Log: func(string) {}})
	if err != nil {
		t.Fatalf("RunAdLog 失败：%v", err)
	}
	// count=1 → 每个场景 1 条，共 6 条。
	if len(entries) != 6 {
		t.Fatalf("广告条数 = %d，应为 6", len(entries))
	}
	scenes := map[string]bool{}
	for _, e := range entries {
		scene, _ := e["scene"].(string)
		scenes[scene] = true
		if e["result"] != "ok" || asInt(e["level"]) != 1 {
			t.Errorf("广告条目不符：%v", e)
		}
		if asInt(e["t"]) <= 0 {
			t.Errorf("广告条目缺少 t：%v", e)
		}
	}
	for _, want := range adScenes {
		if !scenes[want] {
			t.Errorf("缺少场景 %q", want)
		}
	}
	if asInt(res["entries"]) != 6 || asInt(res["accepted"]) != 6 {
		t.Errorf("结果 = %+v，应为 entries=6 accepted=6", res)
	}
}

func TestRunAdLogCount(t *testing.T) {
	var entries []map[string]any
	_, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		entries = bodyEntries(t, call.Body)
		return http.StatusOK, okData(map[string]any{"accepted": len(entries)})
	})
	c := newTestClient(t, srv)

	if _, err := c.RunAdLog(context.Background(), "tok", 3, Options{Log: func(string) {}}); err != nil {
		t.Fatalf("RunAdLog 失败：%v", err)
	}
	if len(entries) != 18 {
		t.Fatalf("广告条数 = %d，应为 18", len(entries))
	}
}

// TestFatalUnauthorized 验证 401 会终止整个操作而不是逐关重试。
func TestFatalUnauthorized(t *testing.T) {
	f, srv := newFakeAPI(t, func(recordedCall) (int, any) {
		return http.StatusUnauthorized, `{"ok":false,"code":"bad_token","error":"登录已失效"}`
	})
	c := newTestClient(t, srv)

	_, err := c.ClearLevels(context.Background(), "tok", 1, 5, Options{})
	if err == nil {
		t.Fatal("401 应返回错误")
	}
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusUnauthorized {
		t.Fatalf("APIError = %+v", err)
	}
	if got := len(f.callsFor("me")); got != 1 {
		t.Errorf("me 请求数 = %d，应为 1", got)
	}
	if got := len(f.callsFor("submit")); got != 0 {
		t.Errorf("401 后应立即中止，实际提交 %d 次", got)
	}
}

// TestContextCanceled 验证 context 取消会中止操作。
func TestContextCanceled(t *testing.T) {
	_, srv := newFakeAPI(t, func(recordedCall) (int, any) {
		return http.StatusOK, okData(map[string]any{"accepted": 1})
	})
	c := newTestClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ClearLevels(ctx, "tok", 1, 5, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v，应为 context.Canceled", err)
	}
}

// TestDefaultAdEntriesCap 验证广告条数上限。
func TestDefaultAdEntriesCap(t *testing.T) {
	if got := len(DefaultAdEntries(100)); got != len(adScenes)*maxAdPerScene {
		t.Errorf("广告条数 = %d，应为 %d", got, len(adScenes)*maxAdPerScene)
	}
	if got := len(DefaultAdEntries(0)); got != len(adScenes)*2 {
		t.Errorf("count=0 时广告条数 = %d，应为 %d", got, len(adScenes)*2)
	}
}

// TestDefaultsForZeroClient 验证零值 Client 会回退到默认地址与密钥。
func TestDefaultsForZeroClient(t *testing.T) {
	c := &Client{}
	if c.baseURL() != DefaultBaseURL {
		t.Errorf("零值 baseURL = %q，应为默认地址", c.baseURL())
	}
	if c.secretKey() != DefaultSecret {
		t.Errorf("零值 secret = %q，应为默认密钥", c.secretKey())
	}
	if c.attemptCount() != 1 {
		t.Errorf("零值 attemptCount = %d，应为 1", c.attemptCount())
	}
	if c.httpClient() == nil {
		t.Error("零值 httpClient 不应为 nil")
	}
}

// TestRunAdLogEntries 验证可直接提交指定广告记录。
func TestRunAdLogEntries(t *testing.T) {
	var entries []map[string]any
	_, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		entries = bodyEntries(t, call.Body)
		return http.StatusOK, okData(map[string]any{"accepted": 1})
	})
	c := newTestClient(t, srv)

	custom := []any{map[string]any{"scene": "undo", "level": 3, "result": "ok", "t": 99}}
	res, err := c.RunAdLogEntries(context.Background(), "tok", custom, Options{Log: func(string) {}})
	if err != nil {
		t.Fatalf("RunAdLogEntries 失败：%v", err)
	}
	if len(entries) != 1 || entries[0]["scene"] != "undo" || asInt(entries[0]["level"]) != 3 {
		t.Errorf("entries 未原样提交：%+v", entries)
	}
	if asInt(res["accepted"]) != 1 {
		t.Errorf("结果 = %+v，应为 accepted=1", res)
	}
	if _, err := c.RunAdLogEntries(context.Background(), "tok", nil, Options{}); err == nil {
		t.Error("空 entries 应返回错误")
	}
}

// TestClearLevelsReuseAll 验证已三星关卡全部复用 best_steps：只发 me + 一次批量提交。
func TestClearLevelsReuseAll(t *testing.T) {
	f, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		switch call.Action {
		case "me":
			records := make([]any, 0, 5)
			for level := 1; level <= 5; level++ {
				records = append(records, map[string]any{
					"level": level, "stars": 3, "best_steps": minStepsFor(level), "clears": 1, "fails": 0,
				})
			}
			return http.StatusOK, okData(map[string]any{"user": map[string]any{"id": 32}, "records": records})
		case "submit":
			entries := bodyEntries(t, call.Body)
			records := make([]any, 0, len(entries))
			for _, e := range entries {
				level := asInt(e["level"])
				steps := asInt(e["steps"])
				if steps < minStepsFor(level) {
					continue
				}
				records = append(records, map[string]any{
					"level": level, "stars": 3, "best_steps": steps, "clears": 1, "fails": 0,
				})
			}
			return http.StatusOK, okData(map[string]any{
				"accepted": len(records), "rejected": len(entries) - len(records), "records": records,
			})
		}
		return http.StatusOK, okData(nil)
	})
	c := newTestClient(t, srv)

	var lastCur, lastTotal int
	res, err := c.ClearLevels(context.Background(), "tok", 1, 5, Options{
		Progress: func(_ string, cur, total int) bool { lastCur, lastTotal = cur, total; return true },
		Log:      func(string) {},
	})
	if err != nil {
		t.Fatalf("ClearLevels 失败：%v", err)
	}
	if asInt(res["completed"]) != 5 || asInt(res["reused"]) != 5 || asInt(res["skipped"]) != 0 {
		t.Errorf("结果 = %+v，应为 completed=5 reused=5 skipped=0", res)
	}
	if lastCur != 5 || lastTotal != 5 {
		t.Errorf("进度 = %d/%d，应为 5/5", lastCur, lastTotal)
	}
	if got := len(f.callsFor("me")); got != 1 {
		t.Errorf("me 请求数 = %d，应为 1", got)
	}
	submits := f.callsFor("submit")
	if len(submits) != 1 {
		t.Fatalf("复用路径应只有 1 次批量提交，实际 %d 次", len(submits))
	}
	entries := bodyEntries(t, submits[0].Body)
	if len(entries) != 5 {
		t.Errorf("批量条目数 = %d，应为 5", len(entries))
	}
	for _, e := range entries {
		level := asInt(e["level"])
		if got, want := asInt(e["steps"]), minStepsFor(level); got != want {
			t.Errorf("第 %d 关复用步数 = %d，应为 %d", level, got, want)
		}
	}
}

// TestClearLevelsReusePartial 验证部分复用：已三星关卡直接提交 best_steps，缺失关卡才探测。
func TestClearLevelsReusePartial(t *testing.T) {
	lastSteps := map[int]int{}
	known := map[int]int{1: minStepsFor(1), 2: minStepsFor(2), 3: minStepsFor(3)}
	f, srv := newFakeAPI(t, func(call recordedCall) (int, any) {
		switch call.Action {
		case "me":
			records := make([]any, 0, len(known))
			for level := 1; level <= 3; level++ {
				records = append(records, map[string]any{
					"level": level, "stars": 3, "best_steps": known[level], "clears": 1, "fails": 0,
				})
			}
			return http.StatusOK, okData(map[string]any{"user": map[string]any{"id": 32}, "records": records})
		case "submit":
			entries := bodyEntries(t, call.Body)
			records := make([]any, 0, len(entries))
			for _, e := range entries {
				level := asInt(e["level"])
				steps := asInt(e["steps"])
				lastSteps[level] = steps
				if steps >= minStepsFor(level) && steps <= maxLevelSteps {
					records = append(records, map[string]any{
						"level": level, "stars": 3, "best_steps": steps, "clears": 1, "fails": 0,
					})
				}
			}
			return http.StatusOK, okData(map[string]any{
				"accepted": len(records), "rejected": len(entries) - len(records), "records": records,
			})
		}
		return http.StatusOK, okData(nil)
	})
	c := newTestClient(t, srv)

	res, err := c.ClearLevels(context.Background(), "tok", 1, 5, Options{Log: func(string) {}})
	if err != nil {
		t.Fatalf("ClearLevels 失败：%v", err)
	}
	if asInt(res["completed"]) != 5 || asInt(res["reused"]) != 3 || asInt(res["skipped"]) != 0 {
		t.Errorf("结果 = %+v，应为 completed=5 reused=3 skipped=0", res)
	}
	submits := f.callsFor("submit")
	if len(submits) == 0 {
		t.Fatal("没有 submit 请求")
	}
	first := bodyEntries(t, submits[0].Body)
	if len(first) != 3 {
		t.Errorf("首批应复用 3 关，实际 %d 关", len(first))
	}
	for _, e := range first {
		level := asInt(e["level"])
		if want, ok := known[level]; ok && asInt(e["steps"]) != want {
			t.Errorf("第 %d 关复用步数 = %d，应为 %d", level, asInt(e["steps"]), want)
		}
	}
	for level := 4; level <= 5; level++ {
		if got, want := lastSteps[level], minStepsFor(level); got != want {
			t.Errorf("第 %d 关最终步数 = %d，应为 %d", level, got, want)
		}
	}
	for level, want := range known {
		if lastSteps[level] != want {
			t.Errorf("第 %d 关复用后又被改写：%d，原值 %d", level, lastSteps[level], want)
		}
	}
}

// TestMeRealFixture 用真实抓包响应（testdata/hhm_me.json）校验解析。
func TestMeRealFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/hhm_me.json")
	if err != nil {
		t.Fatalf("读取夹具失败：%v", err)
	}
	_, srv := newFakeAPI(t, func(recordedCall) (int, any) {
		return http.StatusOK, string(raw)
	})
	c := newTestClient(t, srv)

	u, err := c.Me(context.Background(), "tok")
	if err != nil {
		t.Fatalf("Me 失败：%v", err)
	}
	if u.ID != 32 || u.Nickname != "示例玩家" || u.Avatar != 5 || u.WechatAvatar != "" {
		t.Errorf("基础字段不符：%+v", u)
	}
	if u.Stars != 660 || u.Cleared != 220 || u.Top != 220 || u.Three != 220 {
		t.Errorf("进度字段不符：%+v", u)
	}
	if len(u.Records) != 220 {
		t.Fatalf("records 条数 = %d，应为 220", len(u.Records))
	}
	if want := (Record{Level: 1, Stars: 3, BestSteps: 2, Clears: 4}); u.Records[0] != want {
		t.Errorf("records[0] = %+v，应为 %+v", u.Records[0], want)
	}
	if last := u.Records[219]; last.Level != 220 || last.BestSteps != 24 || last.Stars != 3 {
		t.Errorf("records[219] = %+v", last)
	}
	if len(u.Fish) != 40 || u.Fish[0] != 3 || u.Fish[39] != 3 {
		t.Errorf("Fish 不符（%d 条）：%+v", len(u.Fish), u.Fish)
	}
	if u.Endless != 9999 || u.MatchBest != 5 || u.SudokuBest != 200 {
		t.Errorf("模式字段不符：endless=%d match=%d sudoku=%d", u.Endless, u.MatchBest, u.SudokuBest)
	}
	if len(u.SudokuStars) != 200 || u.SudokuStars[200] != 3 {
		t.Errorf("SudokuStars 不符（%d 条）：%+v", len(u.SudokuStars), u.SudokuStars)
	}
	if u.Items["at"] != 1793651367227 || u.Items["hint"] != 999 || u.Items["sk"] != 1968143 {
		t.Errorf("Items 不符：%+v", u.Items)
	}
	if u.ResetAt != 0 || u.Skin != "" {
		t.Errorf("ResetAt/Skin = %d/%q", u.ResetAt, u.Skin)
	}
	if u.Modes == nil {
		t.Fatal("Modes 未解析")
	}
	if u.Modes.Fish[0] != 3 || u.Modes.Sudoku.Stars[200] != 3 || u.Modes.Sudoku.Best != 200 {
		t.Errorf("Modes 数值键解析不符：%+v", u.Modes)
	}
	if u.Modes.Endless.Week != "2026-11-09" || u.Modes.Endless.WeekBest != 9999 || u.Modes.Match.Best != 5 {
		t.Errorf("Modes 模式字段不符：%+v", u.Modes)
	}
	if len(u.Modes.Daily) != 3 || u.Modes.Daily["2026-11-02"] != (DailyRecord{St: 3, Steps: 7}) {
		t.Errorf("Modes.Daily 不符：%+v", u.Modes.Daily)
	}
	if _, err := json.Marshal(u); err != nil {
		t.Errorf("User 序列化失败：%v", err)
	}
}

// TestModesNumericKeys 验证 fish / sudoku.stars 的数字字符串键解析，非数字键忽略。
func TestModesNumericKeys(t *testing.T) {
	const raw = `{"fish":{"0":3,"39":2,"x":9},"sudoku":{"best":2,"stars":{"10":3}},"endless":{"best":5,"week":"2026-11-09","weekBest":6},"match":{"best":4},"daily":{"2026-11-02":{"st":3,"steps":7}}}`
	var m Modes
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("Modes 解析失败：%v", err)
	}
	if m.Fish[0] != 3 || m.Fish[39] != 2 || len(m.Fish) != 2 {
		t.Errorf("Fish = %+v，应跳过非数字键", m.Fish)
	}
	if m.Sudoku.Stars[10] != 3 || m.Sudoku.Best != 2 {
		t.Errorf("Sudoku = %+v", m.Sudoku)
	}
	if m.Endless.Best != 5 || m.Endless.Week != "2026-11-09" || m.Endless.WeekBest != 6 {
		t.Errorf("Endless = %+v", m.Endless)
	}
	if m.Match.Best != 4 {
		t.Errorf("Match = %+v", m.Match)
	}
	if m.Daily["2026-11-02"] != (DailyRecord{St: 3, Steps: 7}) {
		t.Errorf("Daily = %+v", m.Daily)
	}
}
