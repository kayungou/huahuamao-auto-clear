package app

import (
	"context"
	"errors"
	"strings"

	"github.com/kayungou/huahuamao-auto-clear/internal/api"
	"github.com/kayungou/huahuamao-auto-clear/internal/game"
)

// Service 把 App 适配成 api.Service（HTTP 层只认这个接口）。
type Service struct {
	app *App
}

// NewService 构造适配层。
func NewService(a *App) *Service { return &Service{app: a} }

var _ api.Service = (*Service)(nil)

// NewQR 新建扫码会话。
func (s *Service) NewQR(ctx context.Context) (api.QRInfo, error) {
	info, err := s.app.NewQR(ctx)
	if err != nil {
		return api.QRInfo{}, err
	}
	return api.QRInfo{ID: info.ID, Image: info.Image, ExpiresIn: info.ExpiresIn}, nil
}

// PollQR 轮询扫码状态；confirmed 时带上一次性 Token 与微信资料。
func (s *Service) PollQR(ctx context.Context, id string) (api.QRStatus, error) {
	st, err := s.app.PollQR(ctx, id)
	if err != nil {
		return api.QRStatus{}, wrapError(err)
	}
	out := api.QRStatus{Status: st.Status, Message: st.Message}
	if st.Session != nil {
		out.Session = &api.LoginSession{
			Token: st.Session.Token,
			Account: api.Account{
				OpenID:   st.Session.Account.OpenID,
				Nickname: st.Session.Account.Nickname,
				Avatar:   st.Session.Account.Avatar,
			},
		}
		if u, ok := st.Session.User.(*game.User); ok {
			out.Session.User = gameUserToAPI(u)
		}
	}
	return out, nil
}

// Me 用请求携带的 Token 查询账号数据。
func (s *Service) Me(ctx context.Context, token string) (api.MeResponse, error) {
	u, err := s.app.Me(ctx, token)
	if err != nil {
		return api.MeResponse{}, wrapError(err)
	}
	apiUser := gameUserToAPI(u)
	items := apiUser.Items
	if items == nil {
		items = map[string]int64{}
	}
	return api.MeResponse{
		User: apiUser,
		Modes: api.Modes{
			Fish:        apiUser.Fish,
			Endless:     apiUser.Endless,
			SudokuBest:  apiUser.SudokuBest,
			SudokuStars: apiUser.SudokuStars,
			MatchBest:   apiUser.MatchBest,
		},
		Items: items,
	}, nil
}

// StartAction 启动异步操作。
func (s *Service) StartAction(ctx context.Context, token, action string, params map[string]any) (string, error) {
	id, err := s.app.StartAction(ctx, token, action, params)
	if err != nil {
		return "", wrapError(err)
	}
	return id, nil
}

// GetJob 读取任务；任务只对提交它的 Token 可见。
func (s *Service) GetJob(id, token string) (api.Job, bool) {
	j, ok := s.app.GetJob(id, token)
	if !ok {
		return api.Job{}, false
	}
	return api.Job{
		ID:        j.ID,
		Status:    j.Status,
		Phase:     j.Phase,
		Cur:       j.Cur,
		Total:     j.Total,
		Logs:      j.Logs,
		Result:    j.Result,
		Error:     j.Error,
		UpdatedAt: j.UpdatedAt,
	}, true
}

func gameUserToAPI(u *game.User) *api.User {
	if u == nil {
		return nil
	}
	return &api.User{
		ID:          u.ID,
		Nickname:    u.Nickname,
		Stars:       u.Stars,
		Cleared:     u.Cleared,
		Top:         u.Top,
		Three:       u.Three,
		Fish:        u.Fish,
		Endless:     u.Endless,
		SudokuBest:  u.SudokuBest,
		SudokuStars: u.SudokuStars,
		MatchBest:   u.MatchBest,
		Items:       u.Items,
		ResetAt:     u.ResetAt,
		Skin:        u.Skin,
	}
}

// wrapError 把业务错误映射为 HTTP 语义错误。
func wrapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrQRSessionExpired) {
		return api.Errorf(410, err.Error())
	}
	if errors.Is(err, ErrMissingToken) {
		return api.Errorf(401, err.Error())
	}
	if errors.Is(err, ErrTooManySessions) || errors.Is(err, ErrBusy) {
		return api.Errorf(429, err.Error())
	}
	if errors.Is(err, ErrLoginDelivered) {
		return api.Errorf(410, err.Error())
	}
	var apiErr *game.APIError
	if errors.As(err, &apiErr) {
		switch strings.ToLower(apiErr.Code) {
		case "empty_user", "unauthorized", "bad_token", "invalid_token", "stale_session":
			return api.Errorf(401, "游戏 Token 已失效，请重新扫码登录")
		}
		if apiErr.Status == 401 {
			return api.Errorf(401, "游戏 Token 已失效，请重新扫码登录")
		}
		msg := strings.ToLower(apiErr.Message)
		if strings.Contains(msg, "token") || strings.Contains(msg, "登录已失效") {
			return api.Errorf(401, "游戏 Token 已失效，请重新扫码登录")
		}
	}
	return err
}
