// Package api 提供闯关助手 Web 控制台的 HTTP 接口层。
//
// 设计约束：
//   - 只依赖标准库 net/http + embed，不引入 gin 等框架，也不引用任何外部 CDN；
//   - 由本包定义 Service 契约与全部 JSON 结构，internal/app 负责实现；
//   - 本包不 import internal/app（也不 import internal/game），因此可独立构建与测试，
//     internal/app 只需把 game.User 适配成 api.User；
//   - 错误响应统一为 {"error":"..."}，配合 4xx/5xx 状态码。
//
// 路由（DESIGN.md §3）：
//
//	POST   /api/qr/new           新建扫码会话
//	GET    /api/qr/poll?id=      轮询扫码状态（Token 只下发一次）
//	GET    /api/game/me          刷新并返回账号数据（需 Token）
//	POST   /api/game/action      启动异步操作，返回任务 id（需 Token）
//	GET    /api/jobs/{id}        轮询任务进度与日志（需同一 Token）
//	GET    /api/app              前端展示用的应用信息
//	GET    /api/health           健康检查
//	GET    /                     前端单页（概览）
//	GET    /scan /account /ops /jobs /settings  前端页面（history 路由，返回同一入口）
//	GET    /assets/*             前端构建产物（文件名带内容哈希）
//	GET    /favicon.svg          站点图标
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"

	uiassets "github.com/kayungou/huahuamao-auto-clear/assets"
)

// DefaultAppID 是「滑滑猫」小程序的 appid，前端展示与取 code 时默认使用。
const DefaultAppID = "wx0a7720b2b180dd29"

// Version 是 Web 控制台版本号（前端页脚展示）。
const Version = "1.0.0"

// User 是 hhmapi `a=me` 返回的游戏账号数据（对应 DESIGN.md §4 的 game.User）。
//
// internal/app 负责把 game.User 转换成本类型；字段语义保持一致。
type User struct {
	ID          int              `json:"id"`
	Nickname    string           `json:"nickname"`
	Stars       int              `json:"stars"`
	Cleared     int              `json:"cleared"`
	Top         int              `json:"top"`
	Three       int              `json:"three"`
	Fish        map[int]int      `json:"fish,omitempty"`
	Endless     int              `json:"endless"`
	SudokuBest  int              `json:"sudoku_best"`
	SudokuStars map[int]int      `json:"sudoku_stars,omitempty"`
	MatchBest   int              `json:"match_best"`
	Items       map[string]int64 `json:"items,omitempty"`
	ResetAt     int              `json:"reset_at"`
	Skin        string           `json:"skin"`
}

// QRInfo 是新建扫码会话的返回体：图片为 data URI，可直接放进 <img src>。
type QRInfo struct {
	ID        string `json:"id"`
	Image     string `json:"image"`
	ExpiresIn int    `json:"expires_in"`
}

// QRStatus 是扫码会话的当前状态。
//
// Status 取值：pending | scanned | authorized | confirmed | expired | cancelled | error。
// confirmed 时 Session 携带一次性 Token（前端写入本机 Cookie，服务端不留存）。
type QRStatus struct {
	Status  string        `json:"status"`
	Session *LoginSession `json:"session,omitempty"`
	Message string        `json:"message,omitempty"`
}

// Account 是扫码后展示的微信公开资料（不含任何凭证）。
type Account struct {
	OpenID   string `json:"openid"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar,omitempty"`
}

// LoginSession 是扫码成功后一次性下发给浏览器的登录态。
type LoginSession struct {
	Token   string  `json:"token"`
	Account Account `json:"account"`
	User    *User   `json:"user,omitempty"`
}

// Modes 汇总各玩法进度，供前端展示；由 User 字段推导。
type Modes struct {
	Fish        map[int]int `json:"fish,omitempty"`
	Endless     int         `json:"endless"`
	SudokuBest  int         `json:"sudoku_best"`
	SudokuStars map[int]int `json:"sudoku_stars,omitempty"`
	MatchBest   int         `json:"match_best"`
}

// MeResponse 是 GET /api/game/me 的返回体。
type MeResponse struct {
	User  *User            `json:"user"`
	Modes Modes            `json:"modes"`
	Items map[string]int64 `json:"items"`
}

// Job 是一次异步操作（一键通关 / 数独 / 摸鱼 ...）的进度快照。
//
// Status 取值：running | done | error；Logs 为增量日志，前端按索引追加。
type Job struct {
	ID        string         `json:"id,omitempty"`
	Status    string         `json:"status"`
	Phase     string         `json:"phase"`
	Cur       int            `json:"cur"`
	Total     int            `json:"total"`
	Logs      []string       `json:"logs"`
	Result    map[string]any `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	UpdatedAt int64          `json:"updated_at,omitempty"`
}

// Job 状态取值。
const (
	JobRunning = "running"
	JobDone    = "done"
	JobError   = "error"
)

// 请求体。

// ActionRequest 是 POST /api/game/action 的请求体；Token 由请求头携带。
type ActionRequest struct {
	Action string         `json:"action"`
	Params map[string]any `json:"params"`
}

// ActionResponse 是 POST /api/game/action 的返回体。
type ActionResponse struct {
	Job string `json:"job"`
}

// AppInfo 是 GET /api/app 的返回体，供前端展示小程序与版本信息。
type AppInfo struct {
	AppID   string `json:"app_id"`
	Version string `json:"version"`
}

// 允许的 action 取值（DESIGN.md §3）。未知 action 直接 400。
var actionNames = []string{
	"clear",   // 120 关一键通关，params: from,to
	"sudoku",  // 数独喵 200 关
	"fish",    // 摸鱼 / 小鱼收集
	"endless", // 无尽模式，params: floor
	"match",   // 消消喵，params: best
	"items",   // 道具补满，params: payload
	"adlog",   // 广告观看记录，params: entries,count
	"rank",    // 排行榜
	"config",  // 服务端配置
}

// ValidAction 报告 action 是否在允许列表内。
func ValidAction(name string) bool {
	for _, n := range actionNames {
		if n == name {
			return true
		}
	}
	return false
}

// Actions 返回允许的 action 列表副本。
func Actions() []string {
	out := make([]string, len(actionNames))
	copy(out, actionNames)
	return out
}

// Error 是带 HTTP 状态码的业务错误。
//
// internal/app 用 Errorf 返回带语义状态码的错误（如 404 账号不存在、502 上游失败），
// 其余 error 一律按 500 处理；错误文本原样返回给前端，不做改写。
type Error struct {
	Status int
	Msg    string
}

// Error 实现 error 接口。
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Msg
}

// Errorf 构造一个带 HTTP 状态码的错误。
func Errorf(status int, msg string) error {
	return &Error{Status: status, Msg: msg}
}

// Service 是 internal/app 需要实现的用例接口。
//
// 约定：
//   - 所有方法都接收请求 context，内部应尊重取消与超时；
//   - GetJob 会被前端约 1s 一次轮询，必须是只读的；
//   - 返回 error 时，若为 *Error 则使用其状态码，否则使用 500。
type Service interface {
	// NewQR 新建微信扫码会话，返回二维码 data URI 与有效期（秒）。
	NewQR(ctx context.Context) (QRInfo, error)
	// PollQR 轮询扫码状态；confirmed 时返回一次性 Token 与微信资料。
	PollQR(ctx context.Context, id string) (QRStatus, error)
	// Me 用请求头里的游戏 Token 查询账号数据。
	Me(ctx context.Context, token string) (MeResponse, error)
	// StartAction 启动异步操作并返回任务 id。
	StartAction(ctx context.Context, token, action string, params map[string]any) (string, error)
	// GetJob 读取任务进度快照；任务不存在或不属于该 Token 时返回 false。
	GetJob(id, token string) (Job, bool)
}

// API 是 Service 之上的 HTTP 适配层。
type API struct {
	svc     Service
	index   []byte
	favicon []byte
	static  http.Handler
	mux     *http.ServeMux
}

// pageRoutes 是前端的真实页面路径（Vue Router history 模式）；
// 直接访问或刷新这些地址时，服务端统一返回 index.html 由前端路由接管。
var pageRoutes = []string{"/scan", "/account", "/ops", "/jobs", "/settings"}

// New 根据 Service 构造完整的 HTTP 处理器（API 路由 + 前端页面 + 静态资源）。
func New(svc Service) http.Handler {
	index, err := uiassets.FS.ReadFile("dist/index.html")
	if err != nil {
		panic("api: 读取内嵌 dist/index.html 失败（请先构建前端）: " + err.Error())
	}
	dist, err := fs.Sub(uiassets.FS, "dist")
	if err != nil {
		panic("api: 定位内嵌 dist 目录失败: " + err.Error())
	}
	favicon, _ := uiassets.FS.ReadFile("dist/favicon.svg")
	a := &API{
		svc:     svc,
		index:   index,
		favicon: favicon,
		static: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 请求路径 /assets/xxx 直接对应 dist/assets/xxx，文件名带内容哈希，可长期缓存。
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			http.FileServer(http.FS(dist)).ServeHTTP(w, r)
		}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/qr/new", a.handleNewQR)
	mux.HandleFunc("GET /api/qr/poll", a.handlePollQR)
	mux.HandleFunc("GET /api/game/me", a.handleMe)
	mux.HandleFunc("POST /api/game/action", a.handleAction)
	mux.HandleFunc("GET /api/jobs/{id}", a.handleJob)
	mux.HandleFunc("GET /api/app", a.handleAppInfo)
	mux.HandleFunc("GET /api/health", a.handleHealth)
	mux.HandleFunc("GET /{$}", a.handleIndex)
	for _, path := range pageRoutes {
		mux.HandleFunc("GET "+path, a.handleIndex)
	}
	mux.HandleFunc("GET /favicon.svg", a.handleFavicon)
	mux.Handle("GET /assets/", a.static)
	mux.HandleFunc("/", a.handleFallback)
	a.mux = mux
	return a
}

// ServeHTTP 实现 http.Handler，附加通用响应头与 panic 兜底。
func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 基础安全响应头：Token 存在浏览器 Cookie 中，用 CSP 收紧脚本来源，降低 XSS 窃取风险。
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; "+
			"script-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("api: panic serving %s %s: %v", r.Method, r.URL.Path, rec)
			writeError(w, http.StatusInternalServerError, "服务内部错误")
		}
	}()
	a.mux.ServeHTTP(w, r)
}

// ---------- 微信扫码 ----------

func (a *API) handleNewQR(w http.ResponseWriter, r *http.Request) {
	if !a.requireService(w) {
		return
	}
	info, err := a.svc.NewQR(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (a *API) handlePollQR(w http.ResponseWriter, r *http.Request) {
	if !a.requireService(w) {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "缺少 id 参数")
		return
	}
	status, err := a.svc.PollQR(r.Context(), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *API) handleMe(w http.ResponseWriter, r *http.Request) {
	if !a.requireService(w) {
		return
	}
	token := requestToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "缺少游戏 Token，请重新扫码登录")
		return
	}
	resp, err := a.svc.Me(r.Context(), token)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) handleAction(w http.ResponseWriter, r *http.Request) {
	if !a.requireService(w) {
		return
	}
	token := requestToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "缺少游戏 Token，请重新扫码登录")
		return
	}
	var req ActionRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	if req.Action == "" {
		writeError(w, http.StatusBadRequest, "缺少 action 参数")
		return
	}
	if !ValidAction(req.Action) {
		writeError(w, http.StatusBadRequest, "未知 action: "+req.Action)
		return
	}
	params := req.Params
	if params == nil {
		params = map[string]any{}
	}
	jobID, err := a.svc.StartAction(r.Context(), token, req.Action, params)
	if err != nil {
		a.fail(w, err)
		return
	}
	if jobID == "" {
		writeError(w, http.StatusInternalServerError, "服务端未返回任务 id")
		return
	}
	writeJSON(w, http.StatusOK, ActionResponse{Job: jobID})
}

// requestToken 从 Authorization: Bearer 或 X-Auth-Token 读取浏览器带来的游戏 Token。
func requestToken(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("Authorization")); v != "" {
		if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
			return strings.TrimSpace(v[7:])
		}
		return v
	}
	return strings.TrimSpace(r.Header.Get("X-Auth-Token"))
}

// ---------- 任务 / 系统 ----------

func (a *API) handleJob(w http.ResponseWriter, r *http.Request) {
	if !a.requireService(w) {
		return
	}
	token := requestToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "缺少游戏 Token，请重新扫码登录")
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "缺少任务 id")
		return
	}
	job, ok := a.svc.GetJob(id, token)
	if !ok {
		writeError(w, http.StatusNotFound, "任务不存在: "+id)
		return
	}
	if job.ID == "" {
		job.ID = id
	}
	if job.Logs == nil {
		job.Logs = []string{}
	}
	if job.Status == "" {
		job.Status = JobRunning
	}
	writeJSON(w, http.StatusOK, job)
}

func (a *API) handleAppInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, AppInfo{AppID: DefaultAppID, Version: Version})
}

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- 前端页面 / 静态资源 / 404 ----------

func (a *API) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(a.index)
}

func (a *API) handleFavicon(w http.ResponseWriter, r *http.Request) {
	if len(a.favicon) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(a.favicon)
}

// handleFallback 处理未命中路由：
//   - 已知 API 路径但方法不匹配 → 405；
//   - 无扩展名的 GET/HEAD（前端 history 路由）→ 返回 index.html；
//   - 其余 → 404 JSON。
func (a *API) handleFallback(w http.ResponseWriter, r *http.Request) {
	if allow, ok := allowedMethods(r.URL.Path); ok {
		w.Header().Set("Allow", allow)
		writeError(w, http.StatusMethodNotAllowed, "不支持的请求方法: "+r.Method)
		return
	}
	if isPageRequest(r) {
		a.handleIndex(w, r)
		return
	}
	writeError(w, http.StatusNotFound, "接口不存在: "+r.URL.Path)
}

// isPageRequest 判断请求是否应回落到前端页面（SPA fallback）。
//
// 只对 GET/HEAD、且不以 /api/ 或 /assets/ 开头、最后一段不含 "." 的路径生效，
// 避免把缺失的静态资源误当页面返回。
func isPageRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/assets/") {
		return false
	}
	last := path[strings.LastIndexByte(path, '/')+1:]
	return !strings.Contains(last, ".")
}

// allowedMethods 返回已知 API 路径允许的方法，用于区分 404 / 405。
func allowedMethods(path string) (string, bool) {
	if m, ok := map[string]string{
		"/api/qr/new":      http.MethodPost,
		"/api/qr/poll":     http.MethodGet,
		"/api/game/me":     http.MethodGet,
		"/api/game/action": http.MethodPost,
		"/api/app":         http.MethodGet,
		"/api/health":      http.MethodGet,
	}[path]; ok {
		return m, true
	}
	if strings.HasPrefix(path, "/api/jobs/") {
		return http.MethodGet, true
	}
	return "", false
}

// ---------- 公共辅助 ----------

const maxBodyBytes = 1 << 20 // 1 MiB

func (a *API) requireService(w http.ResponseWriter) bool {
	if a.svc == nil {
		writeError(w, http.StatusServiceUnavailable, "服务尚未就绪")
		return false
	}
	return true
}

// fail 把 Service 错误映射成 HTTP 状态码并输出 {"error":...}。
func (a *API) fail(w http.ResponseWriter, err error) {
	status := statusOf(err)
	msg := err.Error()
	if strings.TrimSpace(msg) == "" {
		msg = http.StatusText(status)
	}
	writeError(w, status, msg)
}

// statusOf 解析错误应使用的 HTTP 状态码。
func statusOf(err error) int {
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.Status >= 400 && apiErr.Status <= 599 {
		return apiErr.Status
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	return http.StatusInternalServerError
}

// decodeJSONBody 解析请求体；空 body 视为零值请求。
// 解析失败时已写出 400 响应并返回 false。
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		writeError(w, http.StatusBadRequest, "请求体不是合法 JSON: "+err.Error())
		return false
	}
	return true
}

// writeJSON 输出 JSON 响应（关闭 HTML 转义，保持中文可读）。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		log.Printf("api: 写响应失败: %v", err)
	}
}

type errorBody struct {
	Error string `json:"error"`
}

// writeError 输出统一的错误响应 {"error":"..."}。
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}
