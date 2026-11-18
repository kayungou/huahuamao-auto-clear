package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kayungou/huahuamao-auto-clear/internal/qr"
)

// qrSession 是一次扫码登录的内存态；成功后立即丢弃微信凭证。
type qrSession struct {
	session   *qr.Session
	createdAt time.Time
	lastPoll  time.Time

	polling   bool
	result    *LoginSession
	delivered bool
	failure   string
}

// QRInfo 返回给前端：会话 ID + 二维码图片 data URI。
type QRInfo struct {
	ID        string `json:"id"`
	Image     string `json:"image"`
	ExpiresIn int    `json:"expires_in"`
}

// QRLoginAccount 是扫码后展示的微信资料（只含公开字段）。
type QRLoginAccount struct {
	OpenID   string `json:"openid"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar,omitempty"`
}

// LoginSession 是扫码成功后一次性下发给浏览器的登录态。
//
// Token 只在这里出现一次，由前端写入本机 Cookie；服务端不留存。
type LoginSession struct {
	Token   string         `json:"token"`
	Account QRLoginAccount `json:"account"`
	User    any            `json:"user,omitempty"`
}

// QRStatus 是扫码状态。
type QRStatus struct {
	Status  string        `json:"status"`
	Session *LoginSession `json:"session,omitempty"`
	Message string        `json:"message,omitempty"`
}

// NewQR 新建扫码会话并取回二维码图片。
func (a *App) NewQR(ctx context.Context) (*QRInfo, error) {
	res, err := a.qrc.GetQRCodeImage(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取微信二维码失败: %w", err)
	}
	s := &qrSession{session: res.Session, createdAt: time.Now(), lastPoll: time.Now()}
	a.mu.Lock()
	if len(a.qrSess) >= maxQRSessions {
		a.mu.Unlock()
		return nil, ErrTooManySessions
	}
	a.qrSess[res.Session.ID] = s
	a.mu.Unlock()
	return &QRInfo{
		ID:        res.Session.ID,
		Image:     qr.DataURIJPEG(res.ImageBytes),
		ExpiresIn: int(a.cfg.QRSessionTTL.Seconds()),
	}, nil
}

// PollQR 轮询扫码状态；扫码确认后立即换游戏 Token 并返回给前端。
func (a *App) PollQR(ctx context.Context, id string) (*QRStatus, error) {
	a.mu.Lock()
	s := a.qrSess[id]
	if s != nil {
		if time.Since(s.createdAt) > a.cfg.QRSessionTTL {
			delete(a.qrSess, id)
			s = nil
		} else {
			s.lastPoll = time.Now()
		}
	}
	if s == nil {
		a.mu.Unlock()
		return nil, fmt.Errorf("%w，请刷新二维码", ErrQRSessionExpired)
	}
	// Token 只下发一次：浏览器拿到后写入本机 Cookie，服务端立即丢弃。
	if s.delivered {
		a.mu.Unlock()
		return nil, ErrLoginDelivered
	}
	if s.result != nil {
		res := s.result
		s.result = nil
		s.delivered = true
		a.mu.Unlock()
		return &QRStatus{Status: "confirmed", Session: res}, nil
	}
	if s.failure != "" {
		msg := s.failure
		a.mu.Unlock()
		return &QRStatus{Status: "error", Message: msg}, nil
	}
	a.mu.Unlock()

	a.mu.Lock()
	if s.polling || s.session == nil {
		a.mu.Unlock()
		return &QRStatus{Status: "pending"}, nil
	}
	s.polling = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		s.polling = false
		a.mu.Unlock()
	}()

	res, err := a.qrc.PollQRCode(ctx, s.session)
	if err != nil {
		return &QRStatus{Status: "pending", Message: err.Error()}, nil
	}
	switch res.Status {
	case "authorized", "confirmed":
		session, err := a.finishQRLogin(ctx, s)
		if err != nil {
			a.mu.Lock()
			s.failure = err.Error()
			a.mu.Unlock()
			a.logf("扫码登录失败: %v", err)
			return &QRStatus{Status: "error", Message: err.Error()}, nil
		}
		a.mu.Lock()
		s.result = session
		a.mu.Unlock()
		a.logf("扫码登录完成（Token 已交给浏览器 Cookie，服务端不留存）")
		return &QRStatus{Status: "confirmed", Session: session}, nil
	default:
		return &QRStatus{Status: res.Status, Message: res.Message}, nil
	}
}

// finishQRLogin 用授权码换 login_buffer → 小程序 code → 游戏 Token。
// 微信凭证只在本次调用内使用，返回后不写入数据库、不保留在会话里。
func (a *App) finishQRLogin(ctx context.Context, s *qrSession) (*LoginSession, error) {
	lr, err := a.qrc.GetLoginBuffer(ctx, s.session)
	if err != nil {
		return nil, fmt.Errorf("获取微信登录凭证失败: %w", err)
	}
	creds := lr.Credentials
	if creds.OpenID == "" {
		return nil, errors.New("微信回调未返回 openid，请重试")
	}
	nickname, avatar := "", ""
	if info, err := a.qrc.LoginBuffers().FetchUserInfo(ctx, creds); err == nil && info != nil {
		nickname = firstString(info, "nick_name", "nickname", "nickName")
		avatar = firstString(info, "head_img_url", "headimgurl", "avatar")
	}

	codeRes, err := a.pool.GetCode(ctx, lr.LoginBuffer, a.AppID(), openIDKey(creds.OpenID), a.cfg.TCPProxy)
	if err != nil {
		return nil, fmt.Errorf("向微信申请小程序 code 失败: %w", err)
	}
	code := firstString(codeRes, "code")
	if code == "" {
		return nil, errors.New("微信未返回小程序 code，请重新扫码")
	}
	login, err := a.game.Login(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("换取游戏 Token 失败: %w", err)
	}
	if login == nil || login.Token == "" {
		return nil, errors.New("游戏服务未返回 Token，请重新扫码")
	}

	// 微信侧凭证用完即弃：清空 login_buffer 与会话引用，进程内缓存也会自然过期。
	lr.LoginBuffer = ""
	a.mu.Lock()
	s.session = nil
	a.mu.Unlock()

	return &LoginSession{
		Token:   login.Token,
		Account: QRLoginAccount{OpenID: creds.OpenID, Nickname: nickname, Avatar: avatar},
		User:    login.User,
	}, nil
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}
