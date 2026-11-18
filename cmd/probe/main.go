// Command probe 是「扫码 → login_buffer → 小程序 code → hhmapi token」链路的自检 CLI。
//
// 只依赖标准库 flag；四个子命令：
//
//	probe qr     创建微信开放平台扫码会话、保存二维码 PNG、轮询状态（无需真人扫码即可验证 pending）
//	probe code   用已有 login_buffer 走 mmtls + shortlink 调 js-login 换取任意小程序的 wx.login code
//	probe hhmapi 只读 GET hhmapi a=me，验证 X-Auth-Token 是否可用
//	probe flow   交互式全链路：生成二维码 → 等扫码 → login_buffer → code → hhmapi wxlogin 换 token
//
// 只读原则：本 CLI 不调用 hhmapi 的任何写接口（submit/modes/items/adlog/profile/reset）；
// 唯一的非 GET 请求是 a=wxlogin（扫码登录后换 token 本身）。
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg" // 注册 JPEG 解码器：二维码接口返回 image/jpeg
	"image/png"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kayungou/huahuamao-auto-clear/internal/protocol"
	"github.com/kayungou/huahuamao-auto-clear/internal/qr"
	"github.com/kayungou/huahuamao-auto-clear/internal/store"
)

const (
	// defaultAppID 是目标小程序的 appid（苏杰云闯关助手）。
	defaultAppID = "wx0a7720b2b180dd29"
	// defaultHHMAPI 是游戏后端接口基址。
	defaultHHMAPI = "https://hhmapi.sujieyun.net/api/index.php"
	// modulePath 用于从任意子目录定位 web/ 模块根。
	modulePath = "github.com/kayungou/huahuamao-auto-clear"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "qr":
		err = runQR(os.Args[2:])
	case "code":
		err = runCode(os.Args[2:])
	case "hhmapi":
		err = runHHMAPI(os.Args[2:])
	case "flow":
		err = runFlow(os.Args[2:])
	case "help", "-h", "--help":
		usage()
		return
	default:
		err = fmt.Errorf("未知子命令 %q（可用：qr / code / hhmapi / flow）", os.Args[1])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n[FAIL] %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stdout, `probe — 微信 code 获取链路自检 CLI（只读，不调用游戏写接口）

用法:
  go run ./cmd/probe qr      [选项]   生成二维码 PNG 并轮询状态（无需扫码）
  go run ./cmd/probe code    [选项]   用 login_buffer 申请小程序 code
  go run ./cmd/probe hhmapi  [选项]   只读 GET a=me 验证 token
  go run ./cmd/probe flow    [选项]   交互式全链路（需真人扫码）

公共选项:
  -db string         sqlite 路径（默认 <web>/data/yyb.db）
  -tcp-proxy string  socks5://host:port 或 http-connect://host:port（空=直连）
  -timeout duration  单步网络超时（默认 45s）

示例:
  go run ./cmd/probe qr
  go run ./cmd/probe code -login-buffer "<base64>" -app-id %s
  go run ./cmd/probe hhmapi -token <48hex>
  go run ./cmd/probe flow
`, defaultAppID)
}

// ---------------------------------------------------------------------------
// 公共基础设施
// ---------------------------------------------------------------------------

// commonFlags 是所有子命令共享的存储/代理参数。
type commonFlags struct {
	db       string
	tcpProxy string
	timeout  time.Duration
}

func addCommonFlags(fs *flag.FlagSet, c *commonFlags) {
	fs.StringVar(&c.db, "db", "", "sqlite 路径（默认 <web>/data/yyb.db）")
	fs.StringVar(&c.tcpProxy, "tcp-proxy", "", "socks5://host:port 或 http-connect://host:port（空=直连）")
	fs.DurationVar(&c.timeout, "timeout", 45*time.Second, "单步网络超时")
}

func (c *commonFlags) dbPath() string {
	if c.db != "" {
		return c.db
	}
	return filepath.Join(moduleRoot(), "data", "yyb.db")
}

// signalContext 让 Ctrl+C 能中断长时间的轮询/握手。
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// moduleRoot 向上查找含 `module github.com/kayungou/huahuamao-auto-clear` 的 go.mod，返回 web/ 绝对路径。
func moduleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(b), "module "+modulePath) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

func defaultOutDir() string { return filepath.Join(moduleRoot(), "cmd", "probe", "out") }

// logf / stepf 是带「步骤」前缀的进度日志。
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
}

func stepf(idx, total int, format string, args ...any) {
	fmt.Fprintf(os.Stdout, "\n== [%d/%d] %s\n", idx, total, fmt.Sprintf(format, args...))
}

func detailf(format string, args ...any) {
	fmt.Fprintf(os.Stdout, "   "+format+"\n", args...)
}

// truncate 只用于日志里展示敏感串（login_buffer / token）的前缀。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// mask 把敏感串保留首尾各小段，避免整段落进终端 scrollback。
func mask(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:6] + "…" + s[len(s)-4:]
}

// toPrettyJSON 把任意值格式化成缩进 JSON（失败则退回 %v）。
func toPrettyJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// childMap 沿 keys 逐层取 map[string]any。
func childMap(m map[string]any, keys ...string) map[string]any {
	cur := m
	for _, k := range keys {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

func strAt(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func numAt(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	f, _ := m[key].(float64)
	return f
}

// ---------------------------------------------------------------------------
// 二维码图片落盘：接口返回 JPEG，统一转成 PNG
// ---------------------------------------------------------------------------

// saveQRCodePNG 把二维码图片按 PNG 写入 path。
// 返回值 written 为最终文件字节数，note 描述源格式（例如 "jpeg→png"）。
func saveQRCodePNG(path string, raw []byte) (written int, note string, err error) {
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, "", err
	}
	// 已经是 PNG 就原样落盘。
	if len(raw) >= 8 && raw[0] == 0x89 && raw[1] == 'P' && raw[2] == 'N' && raw[3] == 'G' {
		if err = os.WriteFile(path, raw, 0o644); err != nil {
			return 0, "", err
		}
		return len(raw), "png", nil
	}
	img, format, decErr := image.Decode(bytes.NewReader(raw))
	if decErr != nil {
		// 解码失败时原样保存，至少不丢数据（真实错误在 note 里体现）。
		if err = os.WriteFile(path, raw, 0o644); err != nil {
			return 0, "", err
		}
		return len(raw), "unknown(原样保存): " + decErr.Error(), nil
	}
	var buf bytes.Buffer
	if err = png.Encode(&buf, img); err != nil {
		return 0, "", fmt.Errorf("编码 PNG: %w", err)
	}
	if err = os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return 0, "", err
	}
	return buf.Len(), format + "→png", nil
}

// ---------------------------------------------------------------------------
// probe qr
// ---------------------------------------------------------------------------

func runQR(args []string) error {
	fs := flag.NewFlagSet("probe qr", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	var (
		outDir       string
		polls        int
		pollInterval time.Duration
		timeout      time.Duration
	)
	fs.StringVar(&outDir, "out", "", "二维码输出目录（默认 <web>/cmd/probe/out）")
	fs.IntVar(&polls, "polls", 2, "生成后的轮询次数（长轮询每次约 15s）")
	fs.DurationVar(&pollInterval, "poll-interval", 500*time.Millisecond, "两次轮询之间的间隔")
	fs.DurationVar(&timeout, "timeout", 45*time.Second, "HTTP 超时（须大于长轮询 15s）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if outDir == "" {
		outDir = defaultOutDir()
	}
	ctx, cancel := signalContext()
	defer cancel()

	client := qr.NewClient(timeout)
	stepf(1, 3, "创建微信开放平台扫码会话（open.weixin.qq.com）")
	img, err := client.GetQRCodeImage(ctx)
	if err != nil {
		return fmt.Errorf("创建二维码失败: %w", err)
	}
	sess := img.Session
	detailf("session id : %s", sess.ID)
	detailf("wx uuid    : %s", sess.WXUUID)
	detailf("QRCodeURL  : %s", sess.QRCodeURL)

	imgPath := filepath.Join(outDir, "qr.png")
	written, note, err := saveQRCodePNG(imgPath, img.ImageBytes)
	if err != nil {
		return fmt.Errorf("保存二维码失败: %w", err)
	}
	detailf("二维码图片 : %s（%d bytes，%s）", imgPath, written, note)

	stepf(2, 3, "轮询扫码状态（无真人扫码时应为 pending）")
	successes := 0
	lastStatus := ""
	for i := 0; i < polls; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(pollInterval):
			}
		}
		res, perr := client.PollQRCode(ctx, sess)
		if perr != nil {
			detailf("#%d 轮询失败（可重试）: %v", i+1, perr)
			continue
		}
		successes++
		lastStatus = res.Status
		extra := ""
		if res.ErrCode != nil {
			extra = fmt.Sprintf(" errcode=%d", *res.ErrCode)
		}
		if res.Code != "" {
			extra += " code=" + mask(res.Code)
		}
		if res.Message != "" {
			extra += fmt.Sprintf(" msg=%q", truncate(res.Message, 120))
		}
		detailf("#%d 状态: %s%s", i+1, res.Status, extra)
	}

	stepf(3, 3, "结果")
	if polls > 0 && successes == 0 {
		return fmt.Errorf("全部 %d 次轮询都失败，二维码会话可能不可用", polls)
	}
	detailf("二维码已生成: %s", imgPath)
	detailf("最近状态: %s", lastStatus)
	if polls == 0 {
		detailf("已跳过轮询（-polls 0）")
		return nil
	}
	switch lastStatus {
	case "pending":
		logf("   [OK] 会话有效且等待扫码（pending）。真人用微信扫 %s 即可继续。", imgPath)
	case "scanned":
		logf("   [OK] 已扫码，等待手机端确认（scanned）。")
	default:
		logf("   [WARN] 当前状态 %q，未观察到 pending（可能已过期/已被扫）。", lastStatus)
	}
	return nil
}

// ---------------------------------------------------------------------------
// probe code
// ---------------------------------------------------------------------------

func runCode(args []string) error {
	fs := flag.NewFlagSet("probe code", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	var (
		loginBuffer     string
		loginBufferFile string
		appID           string
		openid          string
		accountID       int64
		verbose         bool
		c               commonFlags
	)
	fs.StringVar(&loginBuffer, "login-buffer", "", "login_buffer（base64，扫码后由 pcyyb_get_wx_login_buffer_auth 返回）")
	fs.StringVar(&loginBufferFile, "login-buffer-file", "", "从文件读取 login_buffer（与 -login-buffer 二选一）")
	fs.StringVar(&appID, "app-id", defaultAppID, "目标小程序 appid")
	fs.StringVar(&openid, "openid", "", "探测账号的 openid（默认按 login_buffer 指纹生成，避免复用陈旧 mmtls 会话）")
	fs.Int64Var(&accountID, "account-id", 0, "复用数据库已有账号 id（login_buffer 默认取该行）")
	fs.BoolVar(&verbose, "v", true, "打印阶段日志")
	addCommonFlags(fs, &c)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if loginBuffer == "" && loginBufferFile != "" {
		raw, err := os.ReadFile(loginBufferFile)
		if err != nil {
			return fmt.Errorf("读取 login-buffer-file: %w", err)
		}
		loginBuffer = strings.TrimSpace(string(raw))
	}
	ctx, cancel := signalContext()
	defer cancel()

	db, err := store.Open(c.dbPath())
	if err != nil {
		return fmt.Errorf("打开 sqlite %s: %w", c.dbPath(), err)
	}
	defer db.Close()
	if verbose {
		detailf("sqlite    : %s", c.dbPath())
	}

	// 账号来源：-account-id 复用已有行（login_buffer 默认取该行）；否则按 buffer 指纹新建探测账号。
	var (
		acc        *store.WechatAccount
		forceFresh bool
	)
	switch {
	case accountID > 0:
		if acc, err = db.GetAccount(ctx, accountID); err != nil {
			return fmt.Errorf("读取数据库账号 #%d: %w", accountID, err)
		}
		if loginBuffer == "" {
			loginBuffer = acc.LoginBuffer
			if verbose {
				detailf("login_buffer : 取自数据库账号 #%d（长度 %d）", acc.ID, len(loginBuffer))
			}
		} else if loginBuffer != acc.LoginBuffer {
			forceFresh = true
			logf("[WARN] 传入的 login_buffer 与数据库账号 #%d 的不一致，将清理该账号缓存的 mmtls 会话", acc.ID)
		}
	case loginBuffer == "":
		return errors.New("缺少 -login-buffer（或 -login-buffer-file；也可用 -account-id 从数据库取）")
	default:
		if acc, err = ensureProbeAccount(ctx, db, openid, loginBuffer); err != nil {
			return fmt.Errorf("准备探测账号: %w", err)
		}
	}
	if loginBuffer == "" {
		return fmt.Errorf("账号 #%d 的 login_buffer 为空", acc.ID)
	}
	if _, err := base64.StdEncoding.DecodeString(loginBuffer); err != nil {
		return fmt.Errorf("login_buffer 不是合法 base64: %w", err)
	}
	if verbose {
		detailf("账号 id   : %d（openid=%s）", acc.ID, acc.OpenID)
		detailf("app id    : %s", appID)
		detailf("tcp proxy : %q", c.tcpProxy)
	}

	cfg := protocol.DefaultConfig()
	cfg.LoginTimeout = c.timeout
	cfg.ShortlinkTimeout = c.timeout
	cfg.TCPProxy = c.tcpProxy
	pool := protocol.NewPool(cfg, db)
	if forceFresh {
		if err := pool.Invalidate(ctx, acc.ID, c.tcpProxy); err != nil {
			detailf("[WARN] 清理旧会话失败（继续）: %v", err)
		}
	}

	stepf(1, 1, "mmtls 登录 + shortlink 调 /cgi-bin/mmbiz-bin/js-login（appid=%s）", appID)
	start := time.Now()
	res, err := pool.GetCode(ctx, loginBuffer, appID, acc.ID, c.tcpProxy)
	cost := time.Since(start).Round(time.Millisecond)
	if err != nil {
		return fmt.Errorf("取 code 失败（耗时 %s）: %w", cost, err)
	}
	code, _ := res["code"].(string)
	errMsg, _ := res["errMsg"].(string)
	detailf("耗时      : %s", cost)
	detailf("errMsg    : %s", errMsg)
	detailf("code      : %s", code)
	detailf("code 长度 : %d", len(code))
	if code == "" {
		return fmt.Errorf("协议层返回成功但 code 为空: %s", toPrettyJSON(res))
	}
	logf("\n[OK] 已签发 appid=%s 的 wx.login code", appID)
	logf("     下一步：POST %s?a=wxlogin  body {\"code\":\"%s\"}", defaultHHMAPI, code)
	return nil
}

// ensureProbeAccount 返回可用于 protocol.Pool 的账号行。
//
// 关键点：pool 的 mmtls 会话按 (account_id, proxy) 缓存。若换了一份 login_buffer 却复用同一
// account_id，会命中上一次登录的旧会话，可能给错人的 code。因此新建探测账号时默认用
// login_buffer 的指纹做 openid：同一份 buffer → 复用会话（正确），不同 buffer → 新账号（不会串）。
func ensureProbeAccount(ctx context.Context, db *store.DB, openid, loginBuffer string) (*store.WechatAccount, error) {
	if openid == "" {
		sum := sha256.Sum256([]byte(loginBuffer))
		openid = "probe_" + hex.EncodeToString(sum[:8])
	}
	return db.UpsertAccount(ctx, openid, loginBuffer, nil, nil, nil, nil, nil, nil)
}

// ---------------------------------------------------------------------------
// probe hhmapi（只读 a=me）
// ---------------------------------------------------------------------------

func runHHMAPI(args []string) error {
	fs := flag.NewFlagSet("probe hhmapi", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	var (
		token   string
		base    string
		timeout time.Duration
		rawOnly bool
	)
	fs.StringVar(&token, "token", "", "X-Auth-Token（48 hex）")
	fs.StringVar(&base, "base", defaultHHMAPI, "接口基址（默认官方地址）")
	fs.DurationVar(&timeout, "timeout", 20*time.Second, "HTTP 超时")
	fs.BoolVar(&rawOnly, "raw", false, "只打印原始响应体，不格式化")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if token == "" {
		return errors.New("缺少 -token")
	}
	if len(token) != 48 {
		logf("[WARN] token 长度 %d，预期 48 hex，继续尝试…", len(token))
	}
	ctx, cancel := signalContext()
	defer cancel()

	hc := &http.Client{Timeout: timeout}
	url := base + "?a=me"
	body, status, header, err := hhmapiGet(ctx, hc, url, token)
	if err != nil {
		return fmt.Errorf("GET a=me 失败: %w", err)
	}
	logf("GET %s", url)
	detailf("HTTP %d  content-type=%s  bytes=%d", status, header.Get("Content-Type"), len(body))
	if rawOnly {
		logf("%s", string(body))
		return nil
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		logf("\n[WARN] 响应不是 JSON（%v），原文如下：\n%s", err, string(body))
		if status < 200 || status >= 300 {
			return fmt.Errorf("HTTP %d 且响应非 JSON", status)
		}
		return nil
	}
	logf("\n%s", toPrettyJSON(parsed))

	ok, _ := parsed["ok"].(bool)
	if !ok {
		msg := strAt(parsed, "msg")
		if msg == "" {
			msg = strAt(parsed, "error")
		}
		if msg == "" {
			msg = "服务端返回 ok=false"
		}
		return fmt.Errorf("a=me 被拒绝: %s", msg)
	}
	user := childMap(parsed, "data", "user")
	if user == nil {
		user = childMap(parsed, "user")
	}
	logf("\n[OK] token 可用: user.id=%.0f nickname=%q stars=%.0f cleared=%.0f top=%.0f three=%.0f",
		numAt(user, "id"), strAt(user, "nickname"), numAt(user, "stars"),
		numAt(user, "cleared"), numAt(user, "top"), numAt(user, "three"))
	return nil
}

// hhmapiGet 发起一个带 X-Auth-Token 的只读 GET。
func hhmapiGet(ctx context.Context, hc *http.Client, url, token string) ([]byte, int, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Client", "mg")
	req.Header.Set("X-Auth-Token", token)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, resp.Header, err
	}
	return body, resp.StatusCode, resp.Header, nil
}

// ---------------------------------------------------------------------------
// probe flow（交互式全链路）
// ---------------------------------------------------------------------------

func runFlow(args []string) error {
	fs := flag.NewFlagSet("probe flow", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	var (
		appID        string
		outDir       string
		wait         time.Duration
		pollInterval time.Duration
		hhBase       string
		showSecret   bool
		c            commonFlags
	)
	fs.StringVar(&appID, "app-id", defaultAppID, "目标小程序 appid")
	fs.StringVar(&outDir, "out", "", "二维码/中间产物目录（默认 <web>/cmd/probe/out）")
	fs.DurationVar(&wait, "wait", 5*time.Minute, "等待真人扫码的最长时间")
	fs.DurationVar(&pollInterval, "poll-interval", 300*time.Millisecond, "两次轮询间隔")
	fs.StringVar(&hhBase, "hhmapi", defaultHHMAPI, "hhmapi 基址")
	fs.BoolVar(&showSecret, "show-secret", false, "在日志中打印完整 login_buffer（默认只打印前缀）")
	addCommonFlags(fs, &c)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if outDir == "" {
		outDir = defaultOutDir()
	}
	ctx, cancel := signalContext()
	defer cancel()

	const steps = 6
	client := qr.NewClient(c.timeout)

	// ---- 步骤 1：创建扫码会话 ----
	stepf(1, steps, "创建微信开放平台扫码会话（open.weixin.qq.com）")
	img, err := client.GetQRCodeImage(ctx)
	if err != nil {
		return fmt.Errorf("步骤 1 创建二维码失败: %w", err)
	}
	sess := img.Session
	imgPath := filepath.Join(outDir, "qr.png")
	written, note, err := saveQRCodePNG(imgPath, img.ImageBytes)
	if err != nil {
		return fmt.Errorf("步骤 1 保存二维码失败: %w", err)
	}
	detailf("session id : %s", sess.ID)
	detailf("wx uuid    : %s", sess.WXUUID)
	detailf("QRCodeURL  : %s", sess.QRCodeURL)
	detailf("二维码文件 : %s（%d bytes，%s）", imgPath, written, note)

	// ---- 步骤 2：等待扫码 ----
	stepf(2, steps, "等待真人用微信扫码（最长 %s；现在去看二维码 %s）", wait, imgPath)
	status, err := waitForScan(ctx, client, sess, wait, pollInterval)
	if err != nil {
		return fmt.Errorf("步骤 2 等待扫码失败: %w", err)
	}
	detailf("扫码完成，最终状态: %s", status)

	// ---- 步骤 3：换 login_buffer ----
	stepf(3, steps, "用 wx_code 走应用宝 OAuth 换 login_buffer（pcyyb_get_wx_login_buffer_auth）")
	lbRes, err := client.GetLoginBuffer(ctx, sess)
	if err != nil {
		return fmt.Errorf("步骤 3 获取 login_buffer 失败: %w", err)
	}
	creds := lbRes.Credentials
	detailf("openid        : %s", creds.OpenID)
	detailf("nickname      : %s", creds.Nickname)
	detailf("accesstoken   : %s", mask(creds.AccessToken))
	detailf("logintype     : %s", creds.LoginType)
	detailf("login_buffer  : 长度 %d，前缀 %s", len(lbRes.LoginBuffer), truncate(lbRes.LoginBuffer, 40))
	if showSecret {
		detailf("login_buffer 全文: %s", lbRes.LoginBuffer)
	}
	lbPath := filepath.Join(outDir, "login_buffer.txt")
	if err := os.MkdirAll(filepath.Dir(lbPath), 0o755); err != nil {
		return fmt.Errorf("步骤 3 写 login_buffer 文件失败: %w", err)
	}
	if err := os.WriteFile(lbPath, []byte(lbRes.LoginBuffer+"\n"), 0o600); err != nil {
		return fmt.Errorf("步骤 3 写 login_buffer 文件失败: %w", err)
	}
	detailf("已保存      : %s（可复用：probe code -login-buffer-file %s）", lbPath, lbPath)

	// ---- 步骤 4：账号落库（供 server/app 复用） ----
	stepf(4, steps, "微信账号落库（store.UpsertAccount）")
	db, err := store.Open(c.dbPath())
	if err != nil {
		return fmt.Errorf("步骤 4 打开 sqlite %s 失败: %w", c.dbPath(), err)
	}
	defer db.Close()
	statusAuthorized := "authorized"
	nickname := creds.Nickname
	acc, err := db.UpsertAccount(ctx, creds.OpenID, lbRes.LoginBuffer, nil, &nickname, nil, nil, creds.ToMap(), &statusAuthorized)
	if err != nil {
		return fmt.Errorf("步骤 4 账号落库失败: %w", err)
	}
	detailf("sqlite      : %s", c.dbPath())
	detailf("账号 id     : %d（openid=%s）", acc.ID, acc.OpenID)

	// ---- 步骤 5：申请小程序 code ----
	stepf(5, steps, "申请小程序 code（mmtls + shortlink → /cgi-bin/mmbiz-bin/js-login, appid=%s）", appID)
	cfg := protocol.DefaultConfig()
	cfg.LoginTimeout = c.timeout
	cfg.ShortlinkTimeout = c.timeout
	cfg.TCPProxy = c.tcpProxy
	pool := protocol.NewPool(cfg, db)
	// login_buffer 是刚换到的：清掉该账号可能遗留的旧 mmtls 会话，强制用新 buffer 登录。
	if err := pool.Invalidate(ctx, acc.ID, c.tcpProxy); err != nil {
		detailf("[WARN] 清理旧会话失败（继续）: %v", err)
	}
	start := time.Now()
	codeRes, err := pool.GetCode(ctx, lbRes.LoginBuffer, appID, acc.ID, c.tcpProxy)
	if err != nil {
		return fmt.Errorf("步骤 5 取 code 失败（耗时 %s）: %w", time.Since(start).Round(time.Millisecond), err)
	}
	code, _ := codeRes["code"].(string)
	detailf("耗时        : %s", time.Since(start).Round(time.Millisecond))
	detailf("code        : %s（长度 %d）", code, len(code))
	if code == "" {
		return fmt.Errorf("步骤 5 协议层返回成功但 code 为空: %s", toPrettyJSON(codeRes))
	}

	// ---- 步骤 6：hhmapi wxlogin 换 token ----
	stepf(6, steps, "POST hhmapi a=wxlogin 用 code 换 X-Auth-Token（唯一的登录写操作）")
	token, user, err := hhmapiWxLogin(ctx, hhBase, c.timeout, code)
	if err != nil {
		return fmt.Errorf("步骤 6 wxlogin 失败: %w", err)
	}
	detailf("token       : %s", token)
	if len(token) != 48 {
		detailf("[WARN] token 长度 %d（预期 48 hex）", len(token))
	}
	if user != nil {
		detailf("user.id     : %.0f", numAt(user, "id"))
		detailf("user.nickname: %s", strAt(user, "nickname"))
		detailf("user.stars  : %.0f / cleared=%.0f / top=%.0f / three=%.0f",
			numAt(user, "stars"), numAt(user, "cleared"), numAt(user, "top"), numAt(user, "three"))
	}
	logf("\n[OK] 全链路打通：扫码 → login_buffer → code → X-Auth-Token")
	logf("     X-Auth-Token = %s", token)
	logf("     现在可以：go run ./cmd/probe hhmapi -token %s", token)
	logf("     注意：游戏写接口（submit/modes/items/adlog/profile/reset）本 probe 一律不调用。")
	return nil
}

// waitForScan 轮询直到 authorized（返回最终状态）；pending/scanned 继续等，expired/cancelled 直接失败。
func waitForScan(ctx context.Context, client *qr.Client, sess *qr.Session, wait, pollInterval time.Duration) (string, error) {
	deadline := time.Now().Add(wait)
	last := ""
	attempt := 0
	for {
		if err := ctx.Err(); err != nil {
			return last, fmt.Errorf("被中断: %w", err)
		}
		if time.Now().After(deadline) {
			if last == "" {
				last = "unknown"
			}
			return last, fmt.Errorf("等待扫码超时（%s），最后状态 %s", wait, last)
		}
		attempt++
		res, err := client.PollQRCode(ctx, sess)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return last, ctxErr
			}
			detailf("第 %d 次轮询出错（可重试）: %v", attempt, err)
			select {
			case <-ctx.Done():
				return last, ctx.Err()
			case <-time.After(pollInterval):
			}
			continue
		}
		if res.Status != last {
			detailf("状态变化: %s", res.Status)
			last = res.Status
		}
		switch res.Status {
		case "authorized", "confirmed":
			return res.Status, nil
		case "pending", "scanned":
			// 长轮询本身会阻塞约 15s，无需额外 sleep；这里只在极快返回时稍作退避。
			continue
		case "expired":
			return res.Status, errors.New("二维码已过期（errcode 402），请重新运行 probe flow")
		case "cancelled":
			return res.Status, errors.New("手机端取消了登录（errcode 403）")
		default:
			// unknown：保留原始响应便于排查，重试几次仍无进展则报错。
			if res.Message != "" {
				detailf("服务端返回未知响应: %q", truncate(res.Message, 200))
			}
			if attempt >= 5 && last == "unknown" {
				return res.Status, fmt.Errorf("连续 %d 次收到 unknown 状态，放弃；最后响应: %q", attempt, truncate(res.Message, 200))
			}
			select {
			case <-ctx.Done():
				return last, ctx.Err()
			case <-time.After(pollInterval):
			}
		}
	}
}

// hhmapiWxLogin 用 wx.login code 换 X-Auth-Token（登录本身，不是游戏写操作）。
// 该接口不需要签名头（只有 submit/modes/items/adlog 才需要 X-Ts/X-Nonce/X-Sign）。
func hhmapiWxLogin(ctx context.Context, base string, timeout time.Duration, code string) (string, map[string]any, error) {
	payload, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		return "", nil, err
	}
	hc := &http.Client{Timeout: timeout}
	url := base + "?a=wxlogin"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Client", "mg")
	resp, err := hc.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", nil, err
	}
	detailf("HTTP %d，响应 %d bytes", resp.StatusCode, len(body))
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		// 生产环境 display_errors 可能把 PHP Warning 混进 JSON，原样展示前 800 字节。
		return "", nil, fmt.Errorf("响应不是合法 JSON（HTTP %d）: %s", resp.StatusCode, truncate(string(body), 800))
	}
	ok, _ := parsed["ok"].(bool)
	if !ok {
		msg := strAt(parsed, "msg")
		if msg == "" {
			msg = strAt(parsed, "error")
		}
		if msg == "" {
			msg = "服务端返回 ok=false"
		}
		return "", nil, fmt.Errorf("wxlogin 被拒绝（HTTP %d）: %s；响应=%s", resp.StatusCode, msg, truncate(string(body), 500))
	}
	data := childMap(parsed, "data")
	token := strAt(data, "token")
	if token == "" {
		token = strAt(parsed, "token") // 兼容 token 在顶层的情况
	}
	if token == "" {
		return "", nil, fmt.Errorf("wxlogin 返回 ok=true 但没有 token；响应=%s", truncate(string(body), 800))
	}
	user := childMap(data, "user")
	if user == nil {
		user = childMap(parsed, "user")
	}
	return token, user, nil
}
