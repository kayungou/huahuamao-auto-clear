package app

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/kayungou/huahuamao-auto-clear/internal/game"
	"github.com/kayungou/huahuamao-auto-clear/internal/protocol"
	"github.com/kayungou/huahuamao-auto-clear/internal/qr"
)

// MiniProgramAppID 是「闯关助手」小程序的 appid（wxapkg 目录名 wx0a7720b2b180dd29）。
const MiniProgramAppID = "wx0a7720b2b180dd29"

// 常见可映射为 HTTP 状态码的业务错误。
var (
	// ErrQRSessionExpired 表示扫码会话不存在或已过期。
	ErrQRSessionExpired = errors.New("扫码会话不存在或已过期")
	// ErrMissingToken 表示请求没带游戏 Token。
	ErrMissingToken = errors.New("缺少游戏 Token，请重新扫码登录")
	// ErrTooManySessions 表示并发的扫码会话过多（防止资源被刷爆）。
	ErrTooManySessions = errors.New("扫码会话过多，请稍后再试")
	// ErrBusy 表示同时在执行的任务过多。
	ErrBusy = errors.New("当前任务过多，请等待已有任务完成")
	// ErrLoginDelivered 表示登录态已经下发过一次，不再重复返回 Token。
	ErrLoginDelivered = errors.New("登录态已下发，请刷新页面重新扫码")
)

// 进程内资源上限：防止公开部署时被单个客户端刷爆内存或上游请求。
const (
	maxQRSessions = 32
	maxActiveJobs = 8
	maxJobs       = 256
)

// Config 是 Web 服务的运行时参数。
//
// 服务不落盘保存任何账号或 Token：扫码时向微信换一次 code、向 hhmapi 换一次 Token，
// 然后立即把 Token 交给浏览器 Cookie，服务端只保留进程内的短期任务状态。
type Config struct {
	TCPProxy     string
	AppID        string
	QRSessionTTL time.Duration
	ScanTimeout  time.Duration
	SessionTTL   time.Duration
	// GameBaseURL / GameSecret 允许覆盖内置的 hhmapi 地址与签名密钥（默认值来自小程序包）。
	GameBaseURL string
	GameSecret  string
}

// DefaultConfig 给出本机默认值。
func DefaultConfig() Config {
	return Config{
		AppID:        MiniProgramAppID,
		QRSessionTTL: 5 * time.Minute,
		ScanTimeout:  35 * time.Second,
		SessionTTL:   30 * time.Minute,
	}
}

// App 装配扫码登录与后台任务。
type App struct {
	cfg  Config
	qrc  *qr.Client
	pool *protocol.Pool
	game *game.Client

	mu       sync.Mutex
	qrSess   map[string]*qrSession
	jobs     map[string]*Job
	jobOrder []string
	jobSeq   int64
}

// New 装配依赖；不打开数据库、不写任何凭证到磁盘。
func New(cfg Config) (*App, error) {
	if cfg.AppID == "" {
		cfg.AppID = MiniProgramAppID
	}
	if cfg.QRSessionTTL <= 0 {
		cfg.QRSessionTTL = time.Minute * 5
	}
	if cfg.ScanTimeout <= 0 {
		cfg.ScanTimeout = 35 * time.Second
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 30 * time.Minute
	}
	pc := protocol.DefaultConfig()
	pc.TCPProxy = cfg.TCPProxy
	pc.SessionTTL = cfg.SessionTTL

	gc := game.New()
	if cfg.GameBaseURL != "" {
		gc.BaseURL = cfg.GameBaseURL
	}
	if cfg.GameSecret != "" {
		gc.Secret = cfg.GameSecret
	}

	a := &App{
		cfg:    cfg,
		qrc:    qr.NewClient(cfg.ScanTimeout),
		pool:   protocol.NewMemoryPool(pc),
		game:   gc,
		qrSess: map[string]*qrSession{},
		jobs:   map[string]*Job{},
	}
	go a.reaper()
	return a, nil
}

// Close 兼容旧调用；纯内存实现无需释放资源。
func (a *App) Close() error { return nil }

// AppID 返回当前使用的小程序 appid。
func (a *App) AppID() string {
	if a.cfg.AppID == "" {
		return MiniProgramAppID
	}
	return a.cfg.AppID
}

// openIDKey 把 openid 映射成会话缓存用的稳定数字键（不参与任何持久化）。
func openIDKey(openid string) int64 {
	sum := sha256.Sum256([]byte(openid))
	v := int64(binary.BigEndian.Uint64(sum[:8]) & 0x7fffffffffffffff)
	if v == 0 {
		v = 1
	}
	return v
}

// reaper 定期清理过期的扫码会话与已完成任务。
func (a *App) reaper() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		a.mu.Lock()
		now := time.Now()
		for id, s := range a.qrSess {
			if now.Sub(s.createdAt) > a.cfg.QRSessionTTL+2*time.Minute {
				delete(a.qrSess, id)
			}
		}
		for id, j := range a.jobs {
			if j.finishedAt() && now.Sub(j.updated) > 30*time.Minute {
				delete(a.jobs, id)
			}
		}
		a.mu.Unlock()
	}
}

// logf 统一日志前缀。
func (a *App) logf(format string, args ...any) {
	log.Printf("[web] "+format, args...)
}
