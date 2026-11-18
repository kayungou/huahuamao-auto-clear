package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	uiassets "github.com/kayungou/huahuamao-auto-clear/assets"
	api "github.com/kayungou/huahuamao-auto-clear/internal/api"
)

// ---------- stub Service ----------

type stubService struct {
	newQR  func(context.Context) (api.QRInfo, error)
	pollQR func(context.Context, string) (api.QRStatus, error)
	me     func(context.Context, string) (api.MeResponse, error)
	start  func(context.Context, string, string, map[string]any) (string, error)
	getJob func(string, string) (api.Job, bool)
}

func (s *stubService) NewQR(ctx context.Context) (api.QRInfo, error) {
	if s.newQR == nil {
		return api.QRInfo{}, errors.New("stub: NewQR 未配置")
	}
	return s.newQR(ctx)
}

func (s *stubService) PollQR(ctx context.Context, id string) (api.QRStatus, error) {
	if s.pollQR == nil {
		return api.QRStatus{}, errors.New("stub: PollQR 未配置")
	}
	return s.pollQR(ctx, id)
}

func (s *stubService) Me(ctx context.Context, token string) (api.MeResponse, error) {
	if s.me == nil {
		return api.MeResponse{}, errors.New("stub: Me 未配置")
	}
	return s.me(ctx, token)
}

func (s *stubService) StartAction(ctx context.Context, token, action string, params map[string]any) (string, error) {
	if s.start == nil {
		return "", errors.New("stub: StartAction 未配置")
	}
	return s.start(ctx, token, action, params)
}

func (s *stubService) GetJob(id, token string) (api.Job, bool) {
	if s.getJob == nil {
		return api.Job{}, false
	}
	return s.getJob(id, token)
}

// ---------- 测试辅助 ----------

func newTestServer(t *testing.T, svc api.Service) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(api.New(svc))
	t.Cleanup(srv.Close)
	return srv
}

func request(t *testing.T, srv *httptest.Server, method, path string, body any) (int, []byte, http.Header) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求体失败: %v", err)
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return doRequest(t, srv, req)
}

func requestWithToken(t *testing.T, srv *httptest.Server, method, path, token string, body any) (int, []byte, http.Header) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求体失败: %v", err)
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return doRequest(t, srv, req)
}

func doRequest(t *testing.T, srv *httptest.Server, req *http.Request) (int, []byte, http.Header) {
	t.Helper()
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("请求 %s %s 失败: %v", req.Method, req.URL.Path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}
	return resp.StatusCode, data, resp.Header
}

func requestRaw(t *testing.T, srv *httptest.Server, method, path, raw string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(raw))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	status, data, _ := doRequest(t, srv, req)
	return status, data
}

func decodeMap(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("响应不是 JSON 对象: %v\n%s", err, data)
	}
	return out
}

func mustStatus(t *testing.T, got, want int, data []byte) {
	t.Helper()
	if got != want {
		t.Fatalf("状态码 = %d，期望 %d；响应: %s", got, want, data)
	}
}

func mustError(t *testing.T, data []byte) string {
	t.Helper()
	m := decodeMap(t, data)
	msg, ok := m["error"].(string)
	if !ok || msg == "" {
		t.Fatalf("错误响应缺少非空 error 字段: %s", data)
	}
	for key := range m {
		if key != "error" {
			t.Fatalf("错误响应含多余字段 %q: %s", key, data)
		}
	}
	return msg
}

func wantString(t *testing.T, m map[string]any, key, want string) {
	t.Helper()
	got, _ := m[key].(string)
	if got != want {
		t.Fatalf("%s = %q，期望 %q；响应: %v", key, got, want, m)
	}
}

func findAsset(t *testing.T, prefix, suffix string) string {
	t.Helper()
	entries, err := fs.ReadDir(uiassets.FS, "dist/assets")
	if err != nil {
		t.Fatalf("读取 dist/assets 失败（请先构建前端）: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
			return name
		}
	}
	t.Fatalf("dist/assets 中找不到 %s*%s", prefix, suffix)
	return ""
}

// ---------- 系统 / 页面 ----------

func TestHealthAndAppInfo(t *testing.T) {
	srv := newTestServer(t, &stubService{})

	status, data, _ := request(t, srv, http.MethodGet, "/api/health", nil)
	mustStatus(t, status, http.StatusOK, data)
	if !strings.Contains(string(data), `"ok":true`) {
		t.Fatalf("health 响应异常: %s", data)
	}

	status, data, _ = request(t, srv, http.MethodGet, "/api/app", nil)
	mustStatus(t, status, http.StatusOK, data)
	m := decodeMap(t, data)
	wantString(t, m, "app_id", api.DefaultAppID)
	wantString(t, m, "version", api.Version)
}

func TestIndexAndPages(t *testing.T) {
	srv := newTestServer(t, &stubService{})

	status, index, header := request(t, srv, http.MethodGet, "/", nil)
	mustStatus(t, status, http.StatusOK, index)
	if ct := header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("首页 Content-Type = %q，期望 text/html", ct)
	}
	if cc := header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("首页 Cache-Control = %q，期望 no-store", cc)
	}
	page := string(index)
	for _, want := range []string{`<div id="app">`, `/assets/`, "闯关助手"} {
		if !strings.Contains(page, want) {
			t.Fatalf("首页缺少 %q", want)
		}
	}

	for _, path := range []string{"/scan", "/account", "/ops", "/jobs", "/settings"} {
		st, data, hdr := request(t, srv, http.MethodGet, path, nil)
		mustStatus(t, st, http.StatusOK, data)
		if ct := hdr.Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Fatalf("%s Content-Type = %q，期望 text/html", path, ct)
		}
		if string(data) != page {
			t.Fatalf("%s 未返回同一前端外壳", path)
		}
	}
}

func TestStaticAssetsAndFavicon(t *testing.T) {
	srv := newTestServer(t, &stubService{})

	entry := findAsset(t, "index-", ".js")
	status, data, header := request(t, srv, http.MethodGet, "/assets/"+entry, nil)
	mustStatus(t, status, http.StatusOK, data)
	if ct := header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("%s Content-Type = %q，期望 javascript", entry, ct)
	}
	if cc := header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("%s Cache-Control = %q，期望 immutable", entry, cc)
	}

	css := findAsset(t, "index-", ".css")
	status, data, header = request(t, srv, http.MethodGet, "/assets/"+css, nil)
	mustStatus(t, status, http.StatusOK, data)
	if ct := header.Get("Content-Type"); !strings.Contains(ct, "css") {
		t.Fatalf("%s Content-Type = %q，期望 css", css, ct)
	}

	status, _, header = request(t, srv, http.MethodGet, "/favicon.svg", nil)
	mustStatus(t, status, http.StatusOK, nil)
	if ct := header.Get("Content-Type"); !strings.Contains(ct, "svg") {
		t.Fatalf("favicon Content-Type = %q，期望 svg", ct)
	}

	status, _, _ = request(t, srv, http.MethodGet, "/assets/does-not-exist.js", nil)
	mustStatus(t, status, http.StatusNotFound, nil)
}

func TestFrontendHasNoExternalDependencies(t *testing.T) {
	srv := newTestServer(t, &stubService{})

	_, index, _ := request(t, srv, http.MethodGet, "/", nil)
	page := strings.ToLower(string(index))
	for _, bad := range []string{`src="http`, `href="http`, "//cdn", "unpkg", "jsdelivr", "cdnjs"} {
		if strings.Contains(page, bad) {
			t.Fatalf("index.html 含外部依赖 %q（要求离线可用）", bad)
		}
	}

	entries, err := fs.ReadDir(uiassets.FS, "dist/assets")
	if err != nil {
		t.Fatalf("读取 dist/assets 失败: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		isCSS := strings.HasSuffix(name, ".css")
		isJS := strings.HasSuffix(name, ".js")
		if !isCSS && !isJS {
			continue
		}
		_, data, _ := request(t, srv, http.MethodGet, "/assets/"+name, nil)
		text := strings.ToLower(string(data))
		for _, bad := range []string{"unpkg", "jsdelivr", "cdnjs"} {
			if strings.Contains(text, bad) {
				t.Fatalf("%s 含外部依赖 %q（要求离线可用）", name, bad)
			}
		}
		if isCSS {
			for _, bad := range []string{"url(http", "url(//", "@import"} {
				if strings.Contains(text, bad) {
					t.Fatalf("%s 含外部资源 %q（要求离线可用）", name, bad)
				}
			}
		}
	}
}

func TestNilService(t *testing.T) {
	srv := newTestServer(t, nil)
	status, data, _ := request(t, srv, http.MethodPost, "/api/qr/new", nil)
	mustStatus(t, status, http.StatusServiceUnavailable, data)
	mustError(t, data)
}

// ---------- 扫码 ----------

func TestNewQR(t *testing.T) {
	srv := newTestServer(t, &stubService{
		newQR: func(context.Context) (api.QRInfo, error) {
			return api.QRInfo{ID: "qr-1", Image: "data:image/jpeg;base64,AAAA", ExpiresIn: 300}, nil
		},
	})
	status, data, _ := request(t, srv, http.MethodPost, "/api/qr/new", nil)
	mustStatus(t, status, http.StatusOK, data)
	m := decodeMap(t, data)
	wantString(t, m, "id", "qr-1")
	if m["expires_in"].(float64) != 300 {
		t.Fatalf("expires_in = %v", m["expires_in"])
	}
}

func TestNewQRError(t *testing.T) {
	srv := newTestServer(t, &stubService{
		newQR: func(context.Context) (api.QRInfo, error) {
			return api.QRInfo{}, errors.New("微信连接失败")
		},
	})
	status, data, _ := request(t, srv, http.MethodPost, "/api/qr/new", nil)
	mustStatus(t, status, http.StatusInternalServerError, data)
	if msg := mustError(t, data); !strings.Contains(msg, "微信连接失败") {
		t.Fatalf("错误信息 = %q", msg)
	}
}

func TestPollQR(t *testing.T) {
	var gotID string
	srv := newTestServer(t, &stubService{
		pollQR: func(_ context.Context, id string) (api.QRStatus, error) {
			gotID = id
			return api.QRStatus{
				Status: "confirmed",
				Session: &api.LoginSession{
					Token:   "abcdef0123456789",
					Account: api.Account{OpenID: "openid-1", Nickname: "示例玩家"},
					User:    &api.User{ID: 32, Nickname: "示例玩家"},
				},
			}, nil
		},
	})
	status, data, _ := request(t, srv, http.MethodGet, "/api/qr/poll?id=qr-1", nil)
	mustStatus(t, status, http.StatusOK, data)
	if gotID != "qr-1" {
		t.Fatalf("id = %q", gotID)
	}
	m := decodeMap(t, data)
	wantString(t, m, "status", "confirmed")
	session, ok := m["session"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 session: %s", data)
	}
	wantString(t, session, "token", "abcdef0123456789")
}

func TestPollQRErrorMapping(t *testing.T) {
	srv := newTestServer(t, &stubService{
		pollQR: func(context.Context, string) (api.QRStatus, error) {
			return api.QRStatus{}, api.Errorf(410, "扫码会话不存在或已过期")
		},
	})
	status, data, _ := request(t, srv, http.MethodGet, "/api/qr/poll?id=nope", nil)
	mustStatus(t, status, http.StatusGone, data)
	mustError(t, data)
}

// ---------- 账号 / 任务 ----------

func TestGameMeRequiresToken(t *testing.T) {
	srv := newTestServer(t, &stubService{})
	status, data, _ := request(t, srv, http.MethodGet, "/api/game/me", nil)
	mustStatus(t, status, http.StatusUnauthorized, data)
	mustError(t, data)
}

func TestGameMe(t *testing.T) {
	var gotToken string
	srv := newTestServer(t, &stubService{
		me: func(_ context.Context, token string) (api.MeResponse, error) {
			gotToken = token
			u := &api.User{ID: 32, Nickname: "示例玩家", Stars: 660, Cleared: 220, Three: 220}
			return api.MeResponse{
				User:  u,
				Modes: api.Modes{Endless: 9999, MatchBest: 1100},
				Items: map[string]int64{"hint": 999},
			}, nil
		},
	})
	status, data, _ := requestWithToken(t, srv, http.MethodGet, "/api/game/me", "tok-1", nil)
	mustStatus(t, status, http.StatusOK, data)
	if gotToken != "tok-1" {
		t.Fatalf("token = %q", gotToken)
	}
	m := decodeMap(t, data)
	user := m["user"].(map[string]any)
	if user["stars"].(float64) != 660 {
		t.Fatalf("user = %v", user)
	}
}

func TestGameMeAcceptsXAuthTokenHeader(t *testing.T) {
	var gotToken string
	srv := newTestServer(t, &stubService{
		me: func(_ context.Context, token string) (api.MeResponse, error) {
			gotToken = token
			return api.MeResponse{User: &api.User{}}, nil
		},
	})
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/game/me", nil)
	req.Header.Set("X-Auth-Token", "legacy-token")
	status, data, _ := doRequest(t, srv, req)
	mustStatus(t, status, http.StatusOK, data)
	if gotToken != "legacy-token" {
		t.Fatalf("token = %q", gotToken)
	}
}

func TestGameAction(t *testing.T) {
	var got struct {
		token, action string
		params        map[string]any
	}
	srv := newTestServer(t, &stubService{
		start: func(_ context.Context, token, action string, params map[string]any) (string, error) {
			got.token, got.action, got.params = token, action, params
			return "j7", nil
		},
	})
	status, data, _ := requestWithToken(t, srv, http.MethodPost, "/api/game/action", "tok-1",
		map[string]any{"action": "clear", "params": map[string]any{"from": 1, "to": 5}})
	mustStatus(t, status, http.StatusOK, data)
	wantString(t, decodeMap(t, data), "job", "j7")
	if got.token != "tok-1" || got.action != "clear" || got.params["to"].(float64) != 5 {
		t.Fatalf("stub 收到 %+v", got)
	}
}

func TestGameActionValidation(t *testing.T) {
	srv := newTestServer(t, &stubService{start: func(context.Context, string, string, map[string]any) (string, error) {
		t.Fatal("不应调用 StartAction")
		return "", nil
	}})
	status, data, _ := requestWithToken(t, srv, http.MethodPost, "/api/game/action", "tok", map[string]any{"action": "nope"})
	mustStatus(t, status, http.StatusBadRequest, data)
	mustError(t, data)

	status, data, _ = requestWithToken(t, srv, http.MethodPost, "/api/game/action", "tok", map[string]any{})
	mustStatus(t, status, http.StatusBadRequest, data)

	status, data, _ = requestWithToken(t, srv, http.MethodPost, "/api/game/action", "", map[string]any{"action": "clear"})
	mustStatus(t, status, http.StatusUnauthorized, data)

	status, data = requestRaw(t, srv, http.MethodPost, "/api/game/action", "{bad json")
	_ = status
	_ = data
}

func TestJob(t *testing.T) {
	srv := newTestServer(t, &stubService{
		getJob: func(id, token string) (api.Job, bool) {
			if id != "j1" || token != "tok" {
				return api.Job{}, false
			}
			return api.Job{ID: "j1", Status: "done", Phase: "完成", Cur: 10, Total: 10}, true
		},
	})
	status, data, _ := requestWithToken(t, srv, http.MethodGet, "/api/jobs/j1", "tok", nil)
	mustStatus(t, status, http.StatusOK, data)
	wantString(t, decodeMap(t, data), "status", "done")

	// 任务结果只回给同一 Token：没带 Token → 401，Token 不匹配 → 404。
	status, data, _ = request(t, srv, http.MethodGet, "/api/jobs/j1", nil)
	mustStatus(t, status, http.StatusUnauthorized, data)
	mustError(t, data)

	status, data, _ = requestWithToken(t, srv, http.MethodGet, "/api/jobs/j1", "other", nil)
	mustStatus(t, status, http.StatusNotFound, data)
	mustError(t, data)

	status, data, _ = requestWithToken(t, srv, http.MethodGet, "/api/jobs/nope", "tok", nil)
	mustStatus(t, status, http.StatusNotFound, data)
	mustError(t, data)

	status, _, _ = requestWithToken(t, srv, http.MethodPost, "/api/jobs/j1", "tok", nil)
	mustStatus(t, status, http.StatusMethodNotAllowed, nil)
}

func TestUnknownRoutes(t *testing.T) {
	srv := newTestServer(t, &stubService{})

	status, data, _ := request(t, srv, http.MethodGet, "/api/does-not-exist", nil)
	mustStatus(t, status, http.StatusNotFound, data)
	mustError(t, data)

	status, data, header := request(t, srv, http.MethodGet, "/nope", nil)
	mustStatus(t, status, http.StatusOK, data)
	if !strings.Contains(string(data), `<div id="app">`) {
		t.Fatalf("未回落到前端外壳: %s", data)
	}
	_ = header

	status, data, _ = request(t, srv, http.MethodGet, "/nope.js", nil)
	mustStatus(t, status, http.StatusNotFound, data)
	mustError(t, data)

	status, data, _ = request(t, srv, http.MethodPost, "/nope", nil)
	mustStatus(t, status, http.StatusNotFound, data)
	mustError(t, data)

	status, _, header = request(t, srv, http.MethodPost, "/api/health", nil)
	mustStatus(t, status, http.StatusMethodNotAllowed, nil)
	if allow := header.Get("Allow"); allow != http.MethodGet {
		t.Fatalf("Allow = %q，期望 GET", allow)
	}
}

func TestValidAction(t *testing.T) {
	for _, name := range api.Actions() {
		if !api.ValidAction(name) {
			t.Fatalf("%q 应为合法 action", name)
		}
	}
	if api.ValidAction("drop-database") {
		t.Fatal("未知 action 不应合法")
	}
}

func TestPanicRecovery(t *testing.T) {
	srv := newTestServer(t, &stubService{newQR: func(context.Context) (api.QRInfo, error) {
		panic("boom")
	}})
	status, data, _ := request(t, srv, http.MethodPost, "/api/qr/new", nil)
	mustStatus(t, status, http.StatusInternalServerError, data)
	mustError(t, data)
}

func TestErrorType(t *testing.T) {
	err := api.Errorf(418, "teapot")
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Status != 418 || apiErr.Error() != "teapot" {
		t.Fatalf("unexpected error: %#v", err)
	}
	var nilErr *api.Error
	if nilErr.Error() != "" {
		t.Fatalf("nil error 应返回空串")
	}
}

func TestSecurityHeaders(t *testing.T) {
	srv := newTestServer(t, &stubService{})
	status, _, header := request(t, srv, http.MethodGet, "/", nil)
	mustStatus(t, status, http.StatusOK, nil)

	if got := header.Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") || !strings.Contains(got, "frame-ancestors 'none'") {
		t.Fatalf("CSP = %q", got)
	}
	if got := header.Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q", got)
	}
	if got := header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
	if got := header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q", got)
	}
}
