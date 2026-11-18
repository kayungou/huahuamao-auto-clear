package game

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// 操作阶段名（前端直接展示）。
const (
	PhaseLevels  = "主关卡"
	PhaseSudoku  = "数独喵"
	PhaseFish    = "小鱼收集"
	PhaseEndless = "无尽模式"
	PhaseMatch   = "消消喵"
	PhaseItems   = "道具补满"
	PhaseAdLog   = "广告记录"
)

// 操作默认值。
const (
	// DefaultEndlessFloor 是无尽模式默认推进层数。
	DefaultEndlessFloor = 200
	// DefaultMatchStage 是消消喵默认推进关卡。
	DefaultMatchStage = 200
	// DefaultItemFill 是消耗类道具补满后的数量。
	DefaultItemFill = 999
	// maxLevelSteps 是单关步数探测上限（与参考实现一致）。
	maxLevelSteps = 400
	// levelBatchSize 是一次 a=submit 最多携带的关卡数（服务端接受数组批量提交）。
	levelBatchSize = 100
	// maxAdPerScene 是每个广告场景最多伪造的条数。
	maxAdPerScene = 20
	// matchBatchSize 是一次 a=modes 最多携带的消消喵连续关数。
	// 服务端 2026-11-12 起要求 stage 严格 +1 递增，但同一批 entries 会按序逐条校验，
	// 实测 50 关一批可全部接受；这里取 20 并用 250ms 间隔，避免服务端抖动。
	matchBatchSize = 20
	// matchMaxAttempts 是连续被拒后的最大重试次数（指数退避）。
	matchMaxAttempts = 12
)

// 各操作内部的建议节流间隔；Client.Throttle 更小时以 Throttle 为准。
const (
	levelStepDelay = 200 * time.Millisecond
	sudokuDelay    = 55 * time.Millisecond
	fishDelay      = 60 * time.Millisecond
	matchDelay     = 250 * time.Millisecond
	// matchMaxBackoff 是消消喵被拒重试的退避上限。
	matchMaxBackoff = 5 * time.Second
)

// fishStepCandidates 是小鱼收集的步数尝试顺序。
var fishStepCandidates = []int{12, 11, 10, 9, 8, 7, 6, 5, 4, 13, 14, 15}

// adScenes 是广告观看记录覆盖的场景。
var adScenes = []string{"undo", "inter", "match", "sudoku", "sudoku_revive", "revive"}

// ErrCancelled 表示调用方通过 Progress 回调取消了操作。
var ErrCancelled = errors.New("操作已取消")

// ErrRejected 表示服务端 ok=true 但没有接受提交（例如成绩不在允许区间）。
var ErrRejected = errors.New("服务端未接受提交")

// Progress 是操作进度回调。phase 为阶段名，cur/total 为已完成/总数；
// 返回 false 表示调用方要求取消，操作会尽快返回 ErrCancelled。
type Progress func(phase string, cur, total int) bool

// Options 是操作类方法的公共选项，零值表示不回调。
type Options struct {
	// Progress 是进度回调，可为 nil。
	Progress Progress
	// Log 接收一行人类可读日志，可为 nil。
	Log func(string)
}

// report 上报进度；返回 false 表示调用方要求取消。
func (o Options) report(phase string, cur, total int) bool {
	if o.Progress == nil {
		return true
	}
	return o.Progress(phase, cur, total)
}

// logf 输出一行日志。
func (o Options) logf(format string, args ...any) {
	if o.Log == nil {
		return
	}
	o.Log(fmt.Sprintf(format, args...))
}

// isFatal 判断错误是否应中止整个操作（Token 失效、签名/参数错误、context 取消等）。
func isFatal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ae *APIError
	if errors.As(err, &ae) {
		switch ae.Status {
		case 401, 403:
			return true
		}
		switch ae.Code {
		case "bad_token", "invalid_token", "unauthorized", "stale_session",
			"bad_sign", "bad_ts", "bad_nonce":
			return true
		}
	}
	return false
}

// ClearLevels 把 [from,to] 关卡刷成三星并返回统计。
//
// 先用 Me 拉取 records：已三星的关卡直接复用 best_steps（该关三星最小步数）批量提交；
// 缺失或未满三星的关卡才逐关粗探 + 二分逼近最小步数，最终成绩同样按批提交。
// 每次提交间隔 200ms（Client.Throttle 更小时以 Throttle 为准），单次请求失败重试 Client.Retries 次。
func (c *Client) ClearLevels(ctx context.Context, token string, from, to int, opts Options) (map[string]any, error) {
	if from <= 0 {
		from = 1
	}
	if to <= 0 {
		to = MaxLevel
	}
	if to > MaxLevel {
		to = MaxLevel
	}
	if from > to {
		return nil, fmt.Errorf("关卡区间非法：from=%d > to=%d", from, to)
	}
	total := to - from + 1
	if !opts.report(PhaseLevels, 0, total) {
		return nil, ErrCancelled
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	me, err := c.Me(ctx, token)
	if err != nil {
		return nil, err
	}
	// known：已三星关卡 → best_steps；anchors：未满三星但有历史步数（可做二分上界）。
	known := make(map[int]int, len(me.Records))
	anchors := make(map[int]int, len(me.Records))
	for _, rec := range me.Records {
		if rec.Level < from || rec.Level > to || rec.BestSteps < 2 {
			continue
		}
		if rec.Stars >= 3 {
			known[rec.Level] = rec.BestSteps
		} else {
			anchors[rec.Level] = rec.BestSteps
		}
	}

	accepted, reused, done := 0, 0, 0

	// 1) 已三星关卡：复用 best_steps 批量提交。
	reuseEntries := make([]LevelEntry, 0, len(known))
	for level, steps := range known {
		reuseEntries = append(reuseEntries, newLevelEntry(level, steps))
	}
	sort.Slice(reuseEntries, func(i, j int) bool { return reuseEntries[i].Level < reuseEntries[j].Level })
	var reuseFailed []int
	for start := 0; start < len(reuseEntries); start += levelBatchSize {
		end := start + levelBatchSize
		if end > len(reuseEntries) {
			end = len(reuseEntries)
		}
		batch := reuseEntries[start:end]
		acc, rejected, err := c.submitLevelBatch(ctx, token, batch)
		if err != nil {
			if isFatal(err) {
				return nil, err
			}
			opts.logf("复用步数提交失败（%d 关跳过）：%v", len(batch), err)
			done += len(batch)
			if !opts.report(PhaseLevels, done, total) {
				return nil, ErrCancelled
			}
			continue
		}
		for _, entry := range batch {
			if acc[entry.Level] {
				accepted++
				reused++
			}
		}
		if len(rejected) > 0 {
			reuseFailed = append(reuseFailed, rejected...)
			opts.logf("复用步数被拒绝 %d 关，改为重新探测", len(rejected))
		}
		done += len(batch)
		if !opts.report(PhaseLevels, done, total) {
			return nil, ErrCancelled
		}
		if err := c.sleep(ctx, levelStepDelay); err != nil {
			return nil, err
		}
	}
	if reused > 0 {
		opts.logf("已复用 %d 关的 best_steps", reused)
	}

	// 2) 缺失或复用失败的关卡：逐关探测最小步数。
	pending := make([]int, 0, total-len(known)+len(reuseFailed))
	for level := from; level <= to; level++ {
		if _, ok := known[level]; !ok {
			pending = append(pending, level)
		}
	}
	pending = append(pending, reuseFailed...)
	sort.Ints(pending)

	prev := 4
	minEntries := make([]LevelEntry, 0, levelBatchSize)
	flushMins := func() error {
		if len(minEntries) == 0 {
			return nil
		}
		acc, _, err := c.submitLevelBatch(ctx, token, minEntries)
		if err != nil {
			if isFatal(err) {
				return err
			}
			opts.logf("最小步数批量提交失败（%d 关）：%v", len(minEntries), err)
			minEntries = minEntries[:0]
			return nil
		}
		for _, entry := range minEntries {
			if acc[entry.Level] {
				accepted++
				continue
			}
			opts.logf("第 %d 关最小步数 %d 未被接受", entry.Level, entry.Steps)
		}
		minEntries = minEntries[:0]
		return nil
	}

	for _, level := range pending {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		minSteps, ok, err := c.tuneLevel(ctx, token, level, anchors[level], prev, opts)
		if err != nil {
			return nil, err
		}
		done++
		if ok {
			prev = minSteps
			minEntries = append(minEntries, newLevelEntry(level, minSteps))
			opts.logf("第 %d 关最小步数 %d", level, minSteps)
			if len(minEntries) >= levelBatchSize {
				if err := flushMins(); err != nil {
					return nil, err
				}
			}
		} else {
			opts.logf("第 %d 关没有找到可用步数，跳过", level)
		}
		if !opts.report(PhaseLevels, done, total) {
			return nil, ErrCancelled
		}
	}
	if err := flushMins(); err != nil {
		return nil, err
	}
	opts.report(PhaseLevels, total, total)
	skipped := total - accepted
	opts.logf("主关卡完成（%d 关，复用 %d 关，跳过 %d 关）", accepted, reused, skipped)
	return map[string]any{
		"from": from, "to": to, "completed": accepted, "reused": reused, "skipped": skipped,
	}, nil
}

// submitLevelBatch 批量提交关卡成绩，返回被接受的关卡集合与被拒绝/未确定的关卡。
// 调用失败时返回 error，由调用方决定是否跳过。
func (c *Client) submitLevelBatch(ctx context.Context, token string, entries []LevelEntry) (map[int]bool, []int, error) {
	acc := make(map[int]bool, len(entries))
	res, err := c.SubmitLevels(ctx, token, entries)
	if err != nil {
		if isFatal(err) {
			return nil, nil, err
		}
		return nil, nil, fmt.Errorf("批量提交 %d 关失败：%w", len(entries), err)
	}
	for _, rec := range res.Records {
		acc[rec.Level] = true
	}
	if len(acc) == 0 && res.Accepted >= len(entries) {
		// 回执没有 records 时按整体 accepted 判定。
		for _, entry := range entries {
			acc[entry.Level] = true
		}
	}
	rejected := make([]int, 0, len(entries))
	for _, entry := range entries {
		if !acc[entry.Level] {
			rejected = append(rejected, entry.Level)
		}
	}
	return acc, rejected, nil
}

// newLevelEntry 构造一条关卡成绩。
func newLevelEntry(level, steps int) LevelEntry {
	return LevelEntry{Level: level, Result: LevelWin, Steps: steps, T: time.Now().UnixMilli()}
}

// tuneLevel 探测某关被接受的最小步数；anchor>=2 时直接以它为二分上界。
// ok=false 表示本关没有任何被接受的提交。
func (c *Client) tuneLevel(ctx context.Context, token string, level, anchor, prev int, opts Options) (int, bool, error) {
	if anchor < 2 {
		var err error
		anchor, err = c.anchorSteps(ctx, token, level, prev, opts)
		if err != nil {
			return 0, false, err
		}
		if anchor <= 0 {
			return 0, false, nil
		}
	}
	lo, hi := 2, anchor
	for lo < hi {
		if err := ctx.Err(); err != nil {
			return 0, false, err
		}
		mid := lo + (hi-lo)/2
		acc, err := c.submitLevelSteps(ctx, token, level, mid)
		switch {
		case err != nil && isFatal(err):
			return 0, false, err
		case err != nil:
			opts.logf("第 %d 关步数 %d 探测失败：%v", level, mid, err)
			lo = mid + 1
		case acc:
			hi = mid
		default:
			lo = mid + 1
		}
		if err := c.sleep(ctx, levelStepDelay); err != nil {
			return 0, false, err
		}
	}
	return lo, true, nil
}

// anchorSteps 从上一关步数附近向上粗探一个被接受的步数（参考 autoclear.js）。
func (c *Client) anchorSteps(ctx context.Context, token string, level, prev int, opts Options) (int, error) {
	base := prev - 4
	if base < 2 {
		base = 2
	}
	limit := base + 80
	if limit > maxLevelSteps {
		limit = maxLevelSteps
	}
	for s := base; s <= limit; s++ {
		acc, err := c.probeSubmit(ctx, token, level, s, opts)
		if err != nil {
			return 0, err
		}
		if acc {
			return s, nil
		}
		if err := c.sleep(ctx, levelStepDelay); err != nil {
			return 0, err
		}
	}
	// 上一关步数偏高时，从 2 开始重新找。
	for s := 2; s < base; s++ {
		acc, err := c.probeSubmit(ctx, token, level, s, opts)
		if err != nil {
			return 0, err
		}
		if acc {
			return s, nil
		}
		if err := c.sleep(ctx, levelStepDelay); err != nil {
			return 0, err
		}
	}
	return 0, nil
}

// probeSubmit 提交一次探测；非致命错误记日志后按“未接受”处理。
func (c *Client) probeSubmit(ctx context.Context, token string, level, steps int, opts Options) (bool, error) {
	acc, err := c.submitLevelSteps(ctx, token, level, steps)
	if err == nil {
		return acc, nil
	}
	if isFatal(err) {
		return false, err
	}
	opts.logf("第 %d 关步数 %d 提交失败：%v", level, steps, err)
	return false, nil
}

// submitLevelSteps 提交单关成绩，返回是否被接受。
func (c *Client) submitLevelSteps(ctx context.Context, token string, level, steps int) (bool, error) {
	res, err := c.SubmitLevels(ctx, token, []LevelEntry{newLevelEntry(level, steps)})
	if err != nil {
		return false, err
	}
	return res.Accepted > 0, nil
}

// ClearSudoku 把数独喵 1..200 关刷成三星（服务端只增不减）。
func (c *Client) ClearSudoku(ctx context.Context, token string, opts Options) (map[string]any, error) {
	me, err := c.Me(ctx, token)
	if err != nil {
		return nil, err
	}
	pending := make([]int, 0, SudokuLevels)
	for level := 1; level <= SudokuLevels; level++ {
		if me.SudokuStars[level] < 3 {
			pending = append(pending, level)
		}
	}
	if len(pending) == 0 {
		opts.logf("数独喵已全部三星（%d/%d）", SudokuLevels, SudokuLevels)
		return map[string]any{"total": 0, "cleared": 0}, nil
	}
	total := len(pending)
	cleared := 0
	if !opts.report(PhaseSudoku, 0, total) {
		return nil, ErrCancelled
	}
	for i, level := range pending {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		res, err := c.SubmitModes(ctx, token, []ModeEntry{SudokuEntry(level, 3)})
		switch {
		case err != nil && isFatal(err):
			return nil, err
		case err != nil:
			opts.logf("数独第 %d 关提交失败：%v", level, err)
		case res.Accepted > 0:
			cleared++
		default:
			opts.logf("数独第 %d 关未被接受", level)
		}
		done := i + 1
		if done%20 == 0 || done == total {
			opts.logf("数独喵 %d/%d 关已三星", done, total)
		}
		if !opts.report(PhaseSudoku, done, total) {
			return nil, ErrCancelled
		}
		if err := c.sleep(ctx, sudokuDelay); err != nil {
			return nil, err
		}
	}
	opts.logf("数独喵完成（%d 关）", total)
	return map[string]any{"total": total, "cleared": cleared}, nil
}

// ClearFish 把小鱼收集 0..39 关刷成 3 条鱼。
func (c *Client) ClearFish(ctx context.Context, token string, opts Options) (map[string]any, error) {
	me, err := c.Me(ctx, token)
	if err != nil {
		return nil, err
	}
	pending := make([]int, 0, FishLevels)
	for level := 0; level < FishLevels; level++ {
		if me.Fish[level] < 3 {
			pending = append(pending, level)
		}
	}
	if len(pending) == 0 {
		opts.logf("小鱼收集已全部完成（%d/%d）", FishLevels*3, FishLevels*3)
		return map[string]any{"total": 0, "cleared": 0}, nil
	}
	total := len(pending)
	cleared := 0
	if !opts.report(PhaseFish, 0, total) {
		return nil, ErrCancelled
	}
	for i, level := range pending {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		accepted := false
		for _, steps := range fishStepCandidates {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			res, err := c.SubmitModes(ctx, token, []ModeEntry{FishEntry(level, steps)})
			if err == nil && res.Accepted > 0 {
				accepted = true
				break
			}
			if err != nil {
				if isFatal(err) {
					return nil, err
				}
				opts.logf("小鱼第 %d 关步数 %d 提交失败：%v", level, steps, err)
			}
			if err := c.sleep(ctx, fishDelay); err != nil {
				return nil, err
			}
		}
		if accepted {
			cleared++
			opts.logf("小鱼第 %d 关完成", level)
		}
		done := i + 1
		if !opts.report(PhaseFish, done, total) {
			return nil, ErrCancelled
		}
		if err := c.sleep(ctx, fishDelay); err != nil {
			return nil, err
		}
	}
	opts.logf("小鱼收集完成")
	return map[string]any{"total": total, "cleared": cleared}, nil
}

// SetEndless 把无尽模式推到 floor 层，返回 {"floor": floor}。
func (c *Client) SetEndless(ctx context.Context, token string, floor int, opts Options) (map[string]any, error) {
	if floor <= 0 {
		floor = DefaultEndlessFloor
	}
	if !opts.report(PhaseEndless, 0, 1) {
		return nil, ErrCancelled
	}
	week := time.Now().Format("2006-01-02")
	res, err := c.SubmitModes(ctx, token, []ModeEntry{EndlessEntry(floor, week)})
	if err != nil {
		opts.logf("无尽模式提交失败：%v", err)
		return nil, err
	}
	if res.Accepted <= 0 {
		opts.logf("无尽模式提交未被接受")
		return nil, fmt.Errorf("%w：无尽模式 floor=%d", ErrRejected, floor)
	}
	opts.logf("无尽模式已推进到 %d 层", floor)
	opts.report(PhaseEndless, 1, 1)
	return map[string]any{"floor": floor}, nil
}

// SetMatch 把消消喵推到 best 关，返回 {"best","from","completed"}。
//
// 服务端 2026-11-12 起收紧了校验：stage 必须严格逐关 +1，单条跳关会 accepted=0。
// 但同一个 a=modes 请求里的 entries 是按顺序逐条校验的，因此按 matchBatchSize
// 把连续的若干关放进同一批，一次请求连推多关。
//
// 注意：服务端偶发会整批 accepted=0（更新/抖动），此时不能直接判失败；
// 这里会重新 a=me 对齐进度并以指数退避重试，最多 matchMaxAttempts 次。
func (c *Client) SetMatch(ctx context.Context, token string, best int, opts Options) (map[string]any, error) {
	if best <= 0 {
		best = DefaultMatchStage
	}
	me, err := c.Me(ctx, token)
	if err != nil {
		return nil, err
	}
	from := me.MatchBest
	if from >= best {
		opts.logf("消消喵已是第 %d 关（目标 %d），无需推进", from, best)
		return map[string]any{"best": from, "from": from, "completed": 0}, nil
	}
	total := best - from
	done := 0
	if !opts.report(PhaseMatch, 0, total) {
		return nil, ErrCancelled
	}
	stage := from + 1
	knownBest := from
	backoff := matchDelay
	attempts := 0
	for stage <= best {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := stage + matchBatchSize - 1
		if end > best {
			end = best
		}
		entries := make([]ModeEntry, 0, end-stage+1)
		for s := stage; s <= end; s++ {
			entries = append(entries, MatchEntry(s))
		}
		res, err := c.SubmitModes(ctx, token, entries)
		if err != nil {
			return nil, err
		}
		if res.Accepted > 0 {
			attempts = 0
			backoff = matchDelay
			stage = end + 1
			knownBest = end
			done += res.Accepted
			if done > total {
				done = total
			}
			opts.logf("消消喵已推进到第 %d 关（%d/%d）", end, done, total)
			if !opts.report(PhaseMatch, done, total) {
				return nil, ErrCancelled
			}
			if err := c.sleep(ctx, matchDelay); err != nil {
				return nil, err
			}
			continue
		}
		// 整批被拒：先重新对齐服务端进度，再指数退避重试。
		attempts++
		if attempts > matchMaxAttempts {
			return nil, fmt.Errorf("%w：消消喵 stage=%d（已重试 %d 次，服务端当前第 %d 关）",
				ErrRejected, stage, attempts-1, knownBest)
		}
		cur, merr := c.Me(ctx, token)
		if merr != nil {
			return nil, merr
		}
		if cur.MatchBest+1 > stage {
			stage = cur.MatchBest + 1
			knownBest = cur.MatchBest
			done = cur.MatchBest - from
			opts.logf("服务端当前第 %d 关，继续推进", cur.MatchBest)
			backoff = matchDelay
			continue
		}
		opts.logf("第 %d 关被拒，%s 后重试（%d/%d）", stage, backoff, attempts, matchMaxAttempts)
		if err := c.sleep(ctx, backoff); err != nil {
			return nil, err
		}
		backoff *= 2
		if backoff > matchMaxBackoff {
			backoff = matchMaxBackoff
		}
	}
	opts.report(PhaseMatch, total, total)
	opts.logf("消消喵完成：第 %d 关", best)
	return map[string]any{"best": best, "from": from, "completed": done}, nil
}

// FillItems 同步道具/钱包（POST ?a=items）。
//
// payload 为 nil 时先拉取账号并生成默认补满 payload（提示/撤回/地图/跳关 = 999，
// 其余钱包字段沿用服务端当前值）；非 nil 时原样提交。
func (c *Client) FillItems(ctx context.Context, token string, payload map[string]any, opts Options) (map[string]any, error) {
	if !opts.report(PhaseItems, 0, 1) {
		return nil, ErrCancelled
	}
	if payload == nil {
		me, err := c.Me(ctx, token)
		if err != nil {
			return nil, err
		}
		payload = DefaultItemsPayload(me)
	}
	data, err := c.SubmitItems(ctx, token, payload)
	if err != nil {
		opts.logf("道具补满失败：%v", err)
		return nil, err
	}
	opts.logf("道具已补满：提示/撤回/地图/跳关 = %d", DefaultItemFill)
	opts.report(PhaseItems, 1, 1)
	return data, nil
}

// DefaultItemsPayload 按上游接口要求的形状生成补满 payload：
// 消耗类道具设为 999，其余钱包字段沿用账号当前值。
func DefaultItemsPayload(me *User) map[string]any {
	if me == nil {
		me = &User{Items: map[string]int64{}}
	}
	payload := map[string]any{"resetAt": me.ResetAt}
	for _, key := range []string{"hint", "can", "map", "pass"} {
		payload[key] = DefaultItemFill
	}
	for _, key := range []string{"spent", "sk", "skin", "seen", "ul"} {
		payload[key] = me.Items[key]
	}
	payload["at"] = time.Now().UnixMilli()
	if int64Of(payload["sk"]) == 0 {
		payload["sk"] = 1
	}
	return payload
}

// RunAdLog 伪造 count 个/场景的广告观看记录并返回接受条数。
// count<=0 时按 2 处理，最大 20/场景；返回 {"entries": 提交条数, "accepted": 接受条数}。
func (c *Client) RunAdLog(ctx context.Context, token string, count int, opts Options) (map[string]any, error) {
	return c.RunAdLogEntries(ctx, token, DefaultAdEntries(count), opts)
}

// RunAdLogEntries 提交指定的广告观看记录（entries 原样上传），返回接受条数。
func (c *Client) RunAdLogEntries(ctx context.Context, token string, entries []any, opts Options) (map[string]any, error) {
	if len(entries) == 0 {
		return nil, errors.New("entries 不能为空")
	}
	if !opts.report(PhaseAdLog, 0, len(entries)) {
		return nil, ErrCancelled
	}
	n, err := c.AdLog(ctx, token, entries)
	if err != nil {
		opts.logf("广告记录提交失败：%v", err)
		return nil, err
	}
	opts.logf("广告记录已提交 %d 条（服务端接受 %d 条）", len(entries), n)
	opts.report(PhaseAdLog, len(entries), len(entries))
	return map[string]any{"entries": len(entries), "accepted": n}, nil
}

// DefaultAdEntries 生成覆盖全部广告场景的记录：每个场景 count 条。
func DefaultAdEntries(count int) []any {
	if count <= 0 {
		count = 2
	}
	if count > maxAdPerScene {
		count = maxAdPerScene
	}
	now := time.Now().UnixMilli()
	entries := make([]any, 0, len(adScenes)*count)
	for _, scene := range adScenes {
		for i := 0; i < count; i++ {
			entries = append(entries, map[string]any{
				"scene": scene, "level": 1, "result": "ok", "t": now,
			})
		}
	}
	return entries
}
