package protocol

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kayungou/huahuamao-auto-clear/internal/store"
)

// SessionCache 抽象 mmtls 会话缓存。
//
// Web 服务用内存实现（NewMemorySessionCache）：微信会话只活在进程内，
// 进程退出即消失，不写磁盘；cmd/probe 仍可传 SQLite 实现方便调试。
type SessionCache interface {
	Get(ctx context.Context, accountID int64, tcpProxy string) (blob map[string]any, expiresAt int64, ok bool, err error)
	Put(ctx context.Context, accountID int64, uin *int64, blob map[string]any, expiresAt int64, tcpProxy string) error
	Invalidate(ctx context.Context, accountID int64, tcpProxy string) error
}

func cacheKey(accountID int64, tcpProxy string) string {
	return fmt.Sprintf("%d\x00%s", accountID, tcpProxy)
}

// NewMemorySessionCache 返回进程内会话缓存（不落盘）。
func NewMemorySessionCache() SessionCache {
	return &memorySessionCache{m: map[string]memSession{}}
}

type memSession struct {
	blob      map[string]any
	expiresAt int64
}

type memorySessionCache struct {
	mu sync.Mutex
	m  map[string]memSession
}

func (c *memorySessionCache) Get(_ context.Context, accountID int64, tcpProxy string) (map[string]any, int64, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := cacheKey(accountID, tcpProxy)
	v, ok := c.m[key]
	if !ok {
		return nil, 0, false, nil
	}
	if v.expiresAt > 0 && v.expiresAt <= time.Now().Unix() {
		delete(c.m, key)
		return nil, 0, false, nil
	}
	return v.blob, v.expiresAt, true, nil
}

func (c *memorySessionCache) Put(_ context.Context, accountID int64, _ *int64, blob map[string]any, expiresAt int64, tcpProxy string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[cacheKey(accountID, tcpProxy)] = memSession{blob: blob, expiresAt: expiresAt}
	return nil
}

func (c *memorySessionCache) Invalidate(_ context.Context, accountID int64, tcpProxy string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, cacheKey(accountID, tcpProxy))
	return nil
}

// NewSQLiteSessionCache 用 SQLite 存 mmtls 会话，仅用于本地调试工具。
func NewSQLiteSessionCache(db *store.DB) SessionCache { return sqliteSessionCache{db: db} }

type sqliteSessionCache struct {
	db *store.DB
}

func (c sqliteSessionCache) Get(ctx context.Context, accountID int64, tcpProxy string) (map[string]any, int64, bool, error) {
	row, err := c.db.GetSession(ctx, accountID, tcpProxy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	return row.SessionBlob, row.ExpiresAt, true, nil
}

func (c sqliteSessionCache) Put(ctx context.Context, accountID int64, uin *int64, blob map[string]any, expiresAt int64, tcpProxy string) error {
	return c.db.PutSession(ctx, accountID, uin, blob, expiresAt, tcpProxy)
}

func (c sqliteSessionCache) Invalidate(ctx context.Context, accountID int64, tcpProxy string) error {
	return c.db.InvalidateSession(ctx, accountID, tcpProxy)
}
