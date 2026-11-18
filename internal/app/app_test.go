package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kayungou/huahuamao-auto-clear/internal/api"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	a, err := New(DefaultConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestNewAndAppID(t *testing.T) {
	a := newTestApp(t)
	if a.AppID() != MiniProgramAppID {
		t.Fatalf("appid = %q", a.AppID())
	}
}

func TestOpenIDKeyStable(t *testing.T) {
	a := openIDKey("openid-abc")
	if a == 0 {
		t.Fatal("会话键不应为 0")
	}
	if a != openIDKey("openid-abc") {
		t.Fatal("同一 openid 应得到稳定键")
	}
	if a == openIDKey("openid-def") {
		t.Fatal("不同 openid 不应共享会话")
	}
}

func newTestJob(t *testing.T, a *App, owner string) *Job {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.createJobLocked(owner)
}

func TestJobLifecycle(t *testing.T) {
	a := newTestApp(t)
	j := newTestJob(t, a, tokenFingerprint("tok"))
	a.jobLog(j.ID, "开始")
	a.jobSet(j.ID, func(x *Job) { x.Phase = "跑关"; x.Cur, x.Total = 3, 10 })
	a.jobDone(j.ID, map[string]any{"ok": true})

	got, ok := a.GetJob(j.ID, "tok")
	if !ok {
		t.Fatal("job not found")
	}
	if got.Status != "done" || got.Phase != "完成" || got.Cur != 3 || got.Total != 10 {
		t.Fatalf("unexpected job: %+v", got)
	}
	if len(got.Logs) != 1 || got.Result["ok"] != true {
		t.Fatalf("unexpected logs/result: %+v", got)
	}
	if _, ok := a.GetJob("nope", "tok"); ok {
		t.Fatal("unknown job should not exist")
	}
	if _, ok := a.GetJob(j.ID, "wrong-token"); ok {
		t.Fatal("其他 Token 不应读到任务")
	}
	if _, ok := a.GetJob(j.ID, ""); ok {
		t.Fatal("缺少 Token 不应读到任务")
	}
}

func TestJobFailure(t *testing.T) {
	a := newTestApp(t)
	j := newTestJob(t, a, tokenFingerprint("tok"))
	a.jobFail(j.ID, errors.New("boom"))
	got, _ := a.GetJob(j.ID, "tok")
	if got.Status != "error" || got.Error != "boom" {
		t.Fatalf("unexpected job: %+v", got)
	}
}

func TestServiceErrorMapping(t *testing.T) {
	svc := NewService(newTestApp(t))
	ctx := context.Background()

	_, err := svc.PollQR(ctx, "missing")
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Status != 410 {
		t.Fatalf("PollQR 期望 410，实际 %#v", err)
	}

	if _, err := svc.Me(ctx, ""); !errors.As(err, &apiErr) || apiErr.Status != 401 {
		t.Fatalf("Me 期望 401，实际 %#v", err)
	}

	if _, err := svc.StartAction(ctx, "", "clear", nil); !errors.As(err, &apiErr) || apiErr.Status != 401 {
		t.Fatalf("StartAction 期望 401，实际 %#v", err)
	}
}

func TestStartActionUnknownActionFailsJob(t *testing.T) {
	a := newTestApp(t)
	id, err := a.StartAction(context.Background(), "token-x", "nope", nil)
	if err != nil {
		t.Fatalf("StartAction: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		j, ok := a.GetJob(id, "token-x")
		if ok && j.Status == "error" {
			if j.Error == "" {
				t.Fatal("失败任务应带错误信息")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("未知 action 未在预期时间内失败")
}
