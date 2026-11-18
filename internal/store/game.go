package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// gameSchema 保存「微信账号 → 闯关助手游戏账号（X-Auth-Token）」的绑定关系。
const gameSchema = `
CREATE TABLE IF NOT EXISTS game_accounts (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    wechat_account_id INTEGER REFERENCES wechat_accounts(id) ON DELETE SET NULL,
    appid             TEXT    NOT NULL DEFAULT '',
    openid            TEXT    NOT NULL DEFAULT '',
    token             TEXT    NOT NULL UNIQUE,
    nickname          TEXT,
    avatar            TEXT,
    user_json         TEXT,
    status            TEXT,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_game_acc_wx ON game_accounts(wechat_account_id);
`

// GameAccount 是 hhmapi 侧的游戏账号：token 换取后长期有效（无过期时间）。
type GameAccount struct {
	ID              int64          `json:"id"`
	WechatAccountID *int64         `json:"wechat_account_id,omitempty"`
	AppID           string         `json:"appid"`
	OpenID          string         `json:"openid"`
	Token           string         `json:"token"`
	Nickname        *string        `json:"nickname,omitempty"`
	Avatar          *string        `json:"avatar,omitempty"`
	User            map[string]any `json:"user,omitempty"`
	Status          *string        `json:"status,omitempty"`
	CreatedAt       int64          `json:"created_at"`
	UpdatedAt       int64          `json:"updated_at"`
}

// GameAccountPublic 是给前端的脱敏视图（不含 token，token 只在服务端使用）。
type GameAccountPublic struct {
	ID       int64   `json:"id"`
	WXRef    string  `json:"wx_ref"`
	AppID    string  `json:"appid"`
	OpenID   string  `json:"openid"`
	Nickname *string `json:"nickname"`
	Avatar   *string `json:"avatar"`
	Status   *string `json:"status"`
	Stars    int     `json:"stars"`
	Cleared  int     `json:"cleared"`
	Three    int     `json:"three"`
	Updated  int64   `json:"updated_at"`
}

func (g *GameAccount) Public(openidToRef map[string]string) GameAccountPublic {
	p := GameAccountPublic{
		ID:       g.ID,
		AppID:    g.AppID,
		OpenID:   g.OpenID,
		Nickname: g.Nickname,
		Avatar:   g.Avatar,
		Status:   g.Status,
		Updated:  g.UpdatedAt,
	}
	if u := g.User; u != nil {
		p.Stars = intFromMapStats(u["stars"])
		p.Cleared = intFromMapStats(u["cleared"])
		p.Three = intFromMapStats(u["three"])
	}
	if g.OpenID != "" {
		if ref, ok := openidToRef[g.OpenID]; ok {
			p.WXRef = ref
		}
	}
	return p
}

func intFromMapStats(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	case json.Number:
		i, _ := x.Int64()
		return int(i)
	}
	return 0
}

// EnsureGameSchema 建表；由装配层在 store.Open 之后调用。
func (db *DB) EnsureGameSchema(ctx context.Context) error {
	_, err := db.sql.ExecContext(ctx, gameSchema)
	return err
}

const selectGameAccountSQL = `SELECT id, wechat_account_id, appid, openid, token, nickname, avatar, user_json, status, created_at, updated_at FROM game_accounts`

// UpsertGameAccount 以 token 为唯一键写入/更新游戏账号。
func (db *DB) UpsertGameAccount(ctx context.Context, wxID *int64, appid, openid, token string, nickname, avatar *string, user map[string]any) (*GameAccount, error) {
	now := time.Now().Unix()
	userJSON, err := marshalNullable(user)
	if err != nil {
		return nil, err
	}
	var wx sql.NullInt64
	if wxID != nil {
		wx = sql.NullInt64{Int64: *wxID, Valid: true}
	}
	_, err = db.sql.ExecContext(ctx,
		`INSERT INTO game_accounts
		(wechat_account_id, appid, openid, token, nickname, avatar, user_json, status, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(token) DO UPDATE SET
		wechat_account_id=excluded.wechat_account_id, appid=excluded.appid, openid=excluded.openid,
		nickname=excluded.nickname, avatar=excluded.avatar, user_json=excluded.user_json,
		status=excluded.status, updated_at=excluded.updated_at`,
		wx, appid, openid, token, nullableString(nickname), nullableString(avatar), userJSON,
		sql.NullString{String: "ok", Valid: true}, now, now,
	)
	if err != nil {
		return nil, err
	}
	return db.GetGameAccountByToken(ctx, token)
}

func (db *DB) scanGameAccount(row accountScanner) (*GameAccount, error) {
	var (
		g                        GameAccount
		wx                       sql.NullInt64
		nickname, avatar, status sql.NullString
		userJSON                 sql.NullString
	)
	if err := row.Scan(&g.ID, &wx, &g.AppID, &g.OpenID, &g.Token, &nickname, &avatar, &userJSON, &status, &g.CreatedAt, &g.UpdatedAt); err != nil {
		return nil, err
	}
	if wx.Valid {
		v := wx.Int64
		g.WechatAccountID = &v
	}
	if nickname.Valid {
		g.Nickname = &nickname.String
	}
	if avatar.Valid {
		g.Avatar = &avatar.String
	}
	if status.Valid {
		g.Status = &status.String
	}
	if userJSON.Valid && userJSON.String != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(userJSON.String), &m); err == nil {
			g.User = m
		}
	}
	return &g, nil
}

func (db *DB) GetGameAccount(ctx context.Context, id int64) (*GameAccount, error) {
	return db.scanGameAccount(db.sql.QueryRowContext(ctx, selectGameAccountSQL+" WHERE id=?", id))
}

func (db *DB) GetGameAccountByToken(ctx context.Context, token string) (*GameAccount, error) {
	return db.scanGameAccount(db.sql.QueryRowContext(ctx, selectGameAccountSQL+" WHERE token=?", token))
}

func (db *DB) GetGameAccountByOpenID(ctx context.Context, openid string) (*GameAccount, error) {
	return db.scanGameAccount(db.sql.QueryRowContext(ctx, selectGameAccountSQL+" WHERE openid=?", openid))
}

// ResolveGameAccount 支持数字 id、openid、以及 "g_<id>" 形式的引用。
func (db *DB) ResolveGameAccount(ctx context.Context, ref string) (*GameAccount, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, sql.ErrNoRows
	}
	if strings.HasPrefix(ref, "g_") {
		ref = strings.TrimPrefix(ref, "g_")
	}
	if isDigits(ref) {
		n, _ := strconv.ParseInt(ref, 10, 64)
		return db.GetGameAccount(ctx, n)
	}
	return db.GetGameAccountByOpenID(ctx, ref)
}

func (db *DB) ListGameAccounts(ctx context.Context) ([]*GameAccount, error) {
	rows, err := db.sql.QueryContext(ctx, selectGameAccountSQL+" ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*GameAccount
	for rows.Next() {
		g, err := db.scanGameAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (db *DB) UpdateGameAccountUser(ctx context.Context, id int64, nickname, avatar *string, user map[string]any) error {
	userJSON, err := marshalNullable(user)
	if err != nil {
		return err
	}
	_, err = db.sql.ExecContext(ctx,
		"UPDATE game_accounts SET nickname=?, avatar=?, user_json=?, updated_at=? WHERE id=?",
		nullableString(nickname), nullableString(avatar), userJSON, time.Now().Unix(), id,
	)
	return err
}

func (db *DB) DeleteGameAccount(ctx context.Context, id int64) error {
	_, err := db.sql.ExecContext(ctx, "DELETE FROM game_accounts WHERE id=?", id)
	return err
}

// ErrNoGameAccount 由装配层在未绑定游戏账号时返回。
var ErrNoGameAccount = errors.New("尚未绑定游戏账号，请先完成微信扫码登录")
