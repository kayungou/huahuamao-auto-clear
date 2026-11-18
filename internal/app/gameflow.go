package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kayungou/huahuamao-auto-clear/internal/game"
)

// Me 用浏览器带来的 Token 拉取账号信息（服务端不保存 token）。
func (a *App) Me(ctx context.Context, token string) (*game.User, error) {
	token = trimToken(token)
	if token == "" {
		return nil, ErrMissingToken
	}
	u, err := a.game.Me(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("读取账号信息失败: %w", err)
	}
	if u == nil {
		return nil, errors.New("游戏服务未返回账号信息，Token 可能已失效")
	}
	return u, nil
}

// Job 是一次后台操作的可观测状态。
type Job struct {
	ID        string         `json:"id"`
	Status    string         `json:"status"`
	Phase     string         `json:"phase"`
	Cur       int            `json:"cur"`
	Total     int            `json:"total"`
	Logs      []string       `json:"logs"`
	Result    map[string]any `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	UpdatedAt int64          `json:"updated_at"`

	// owner 是提交任务所用 Token 的 SHA-256 指纹：任务结果只回给同一 Token 的持有者。
	owner string

	created time.Time
	updated time.Time
}

func (j *Job) finishedAt() bool { return j.Status == "done" || j.Status == "error" }

func (j *Job) snapshot() Job {
	cp := Job{
		ID:        j.ID,
		Status:    j.Status,
		Phase:     j.Phase,
		Cur:       j.Cur,
		Total:     j.Total,
		Error:     j.Error,
		UpdatedAt: j.UpdatedAt,
	}
	if len(j.Logs) > 0 {
		cp.Logs = append([]string(nil), j.Logs...)
	}
	if j.Result != nil {
		cp.Result = j.Result
	}
	return cp
}

// tokenFingerprint 返回 Token 的单向指纹，用于任务归属校验（不保存 Token 原文）。
func tokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// StartAction 启动一个后台操作任务，返回任务 ID。
func (a *App) StartAction(ctx context.Context, token, action string, params map[string]any) (string, error) {
	token = trimToken(token)
	if token == "" {
		return "", ErrMissingToken
	}
	if action == "" {
		return "", errors.New("action 不能为空")
	}

	a.mu.Lock()
	running := 0
	for _, j := range a.jobs {
		if j.Status == "running" {
			running++
		}
	}
	if running >= maxActiveJobs {
		a.mu.Unlock()
		return "", ErrBusy
	}
	j := a.createJobLocked(tokenFingerprint(token))
	a.mu.Unlock()

	go a.runAction(j.ID, token, action, params)
	return j.ID, nil
}

// GetJob 读取任务状态快照；token 必须与提交任务时一致，否则视为不存在。
func (a *App) GetJob(id, token string) (Job, bool) {
	token = trimToken(token)
	if token == "" {
		return Job{}, false
	}
	fp := tokenFingerprint(token)

	a.mu.Lock()
	defer a.mu.Unlock()
	j, ok := a.jobs[id]
	if !ok {
		return Job{}, false
	}
	if subtle.ConstantTimeCompare([]byte(j.owner), []byte(fp)) != 1 {
		return Job{}, false
	}
	cp := j.snapshot()
	cp.UpdatedAt = j.updated.Unix()
	return cp, true
}

// createJobLocked 在持有 a.mu 时创建任务，并在任务表满时淘汰最老的已完成任务。
func (a *App) createJobLocked(owner string) *Job {
	if len(a.jobs) >= maxJobs {
		a.evictJobLocked()
	}
	a.jobSeq++
	now := time.Now()
	j := &Job{
		ID:      fmt.Sprintf("j%d", a.jobSeq),
		Status:  "running",
		Phase:   "准备中",
		owner:   owner,
		created: now,
		updated: now,
	}
	a.jobs[j.ID] = j
	a.jobOrder = append(a.jobOrder, j.ID)
	return j
}

// evictJobLocked 优先删除最早的已完成任务；若都在运行则删除最老的任务。
func (a *App) evictJobLocked() {
	victim := ""
	for _, id := range a.jobOrder {
		if j, ok := a.jobs[id]; ok && j.finishedAt() {
			victim = id
			break
		}
	}
	if victim == "" && len(a.jobOrder) > 0 {
		victim = a.jobOrder[0]
	}
	if victim != "" {
		delete(a.jobs, victim)
	}
	kept := a.jobOrder[:0]
	for _, id := range a.jobOrder {
		if _, ok := a.jobs[id]; ok {
			kept = append(kept, id)
		}
	}
	a.jobOrder = kept
}

func (a *App) jobSet(id string, mut func(*Job)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	j, ok := a.jobs[id]
	if !ok {
		return
	}
	mut(j)
	j.updated = time.Now()
}

func (a *App) jobLog(id, line string) {
	a.jobSet(id, func(j *Job) {
		j.Logs = append(j.Logs, line)
		if len(j.Logs) > 400 {
			j.Logs = j.Logs[len(j.Logs)-400:]
		}
	})
}

func (a *App) jobDone(id string, result map[string]any) {
	a.jobSet(id, func(j *Job) {
		j.Status = "done"
		j.Phase = "完成"
		j.Result = result
		j.Error = ""
	})
}

func (a *App) jobFail(id string, err error) {
	a.jobSet(id, func(j *Job) {
		j.Status = "error"
		j.Error = err.Error()
	})
}

// runAction 在后台执行具体操作。
func (a *App) runAction(id, token, action string, params map[string]any) {
	ctx := context.Background()
	defer func() {
		if r := recover(); r != nil {
			a.jobFail(id, fmt.Errorf("内部错误: %v", r))
		}
	}()
	opts := game.Options{
		Progress: func(phase string, cur, total int) bool {
			a.jobSet(id, func(j *Job) {
				j.Phase = phase
				j.Cur = cur
				j.Total = total
			})
			return ctx.Err() == nil
		},
		Log: func(line string) { a.jobLog(id, line) },
	}
	var (
		res map[string]any
		err error
	)
	switch action {
	case "clear":
		from := intParam(params, "from", 1)
		to := intParam(params, "to", 120)
		res, err = a.game.ClearLevels(ctx, token, from, to, opts)
	case "sudoku":
		res, err = a.game.ClearSudoku(ctx, token, opts)
	case "fish":
		res, err = a.game.ClearFish(ctx, token, opts)
	case "endless":
		res, err = a.game.SetEndless(ctx, token, intParam(params, "floor", 200), opts)
	case "match":
		res, err = a.game.SetMatch(ctx, token, intParam(params, "best", 5), opts)
	case "items":
		payload, _ := params["payload"].(map[string]any)
		res, err = a.game.FillItems(ctx, token, payload, opts)
	case "adlog":
		res, err = a.game.RunAdLog(ctx, token, intParam(params, "count", 1), opts)
	case "rank":
		res, err = a.game.Rank(ctx, strParam(params, "type", "stars"))
	case "config":
		res, err = a.game.Config(ctx)
	default:
		err = fmt.Errorf("未知操作: %s", action)
	}
	if err != nil {
		a.jobLog(id, "失败: "+err.Error())
		a.jobFail(id, err)
		return
	}
	a.jobDone(id, res)
}

func trimToken(token string) string {
	return strings.TrimSpace(token)
}

func intParam(params map[string]any, key string, def int) int {
	if params == nil {
		return def
	}
	switch v := params[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func strParam(params map[string]any, key, def string) string {
	if params == nil {
		return def
	}
	if v, ok := params[key].(string); ok && v != "" {
		return v
	}
	return def
}
