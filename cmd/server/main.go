// Command server 启动「闯关助手」Web 端。
//
// 用法：
//
//	go run ./cmd/server -host 127.0.0.1 -port 8902
//
// 打开 http://127.0.0.1:8902/ 后：微信扫码 → 服务端向微信申请小程序 code →
// 用 code 调 hhmapi 的 a=wxlogin 换 X-Auth-Token → Token 交给浏览器 Cookie。
// 服务端不保存账号和 Token，全程不读取任何进程内存。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/kayungou/huahuamao-auto-clear/internal/api"
	"github.com/kayungou/huahuamao-auto-clear/internal/app"
)

func main() {
	host := flag.String("host", "127.0.0.1", "监听地址")
	port := flag.Int("port", 8902, "监听端口")
	dbPath := flag.String("db", "", "已弃用：账号与 Token 不再落盘（仅为兼容旧启动参数保留）")
	tcpProxy := flag.String("tcp-proxy", "", "可选 TCP 代理：socks5://host:port 或 http-connect://host:port")
	openBrowser := flag.Bool("open", true, "启动后自动打开浏览器")
	gameSecret := flag.String("secret", "", "hhmapi 签名密钥（默认内置；也可用 HHMAPI_SECRET 覆盖）")
	gameBaseURL := flag.String("base-url", "", "hhmapi 接口地址（默认内置；也可用 HHMAPI_BASE_URL 覆盖）")
	flag.Parse()

	if *gameSecret == "" {
		*gameSecret = os.Getenv("HHMAPI_SECRET")
	}
	if *gameBaseURL == "" {
		*gameBaseURL = os.Getenv("HHMAPI_BASE_URL")
	}

	cfg := app.DefaultConfig()
	cfg.TCPProxy = *tcpProxy
	cfg.GameSecret = *gameSecret
	cfg.GameBaseURL = *gameBaseURL

	a, err := app.New(cfg)
	if err != nil {
		log.Fatalf("初始化失败: %v", err)
	}
	defer func() { _ = a.Close() }()

	addr := fmt.Sprintf("%s:%d", *host, *port)
	url := fmt.Sprintf("http://%s/", addr)
	if *host == "0.0.0.0" || *host == "::" {
		url = fmt.Sprintf("http://127.0.0.1:%d/", *port)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           api.New(app.NewService(a)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// 扫码轮询会等待上游最长 35s，写超时需留出余量。
		WriteTimeout:   90 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	log.Printf("闯关助手 Web 端已启动: %s", url)
	if *dbPath != "" {
		log.Printf("提示: -db 已弃用，账号与 Token 现在只保存在浏览器 Cookie 中，不写服务器磁盘")
	}
	log.Printf("模式: 无数据库（服务端不保存微信会话、账号或 Token）")
	if *tcpProxy != "" {
		log.Printf("TCP 代理: %s", *tcpProxy)
	}

	if *openBrowser {
		go func() {
			time.Sleep(300 * time.Millisecond)
			openURL(url)
		}()
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		log.Fatalf("HTTP 服务异常: %v", err)
	case sig := <-stop:
		log.Printf("收到 %s，正在退出…", sig)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// openURL 在 macOS / Linux / Windows 上尽力打开默认浏览器。
func openURL(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("无法自动打开浏览器，请手动访问 %s（%v）", url, err)
	}
}
