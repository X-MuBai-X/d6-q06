package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"creative-studio/internal/domain"
)

// TestStartTimeoutSweeper 覆盖后台超时巡检协程。
func TestStartTimeoutSweeper(t *testing.T) {
	env := newTestEnv(t, "timeout")
	ctx, cancel := context.WithCancel(context.Background())
	env.svc.StartTimeoutSweeper(ctx, 50*time.Millisecond)
	req := env.newRequirement(t, "初始描述文本")
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "sweep-1"); err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	// 把任务时间提前，使其很快超时
	if _, err := env.store.DB().Exec("UPDATE generation_tasks SET created_at = ?",
		time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("调整时间失败: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := env.store.GetRequirement(context.Background(), req.ID)
		if got.Status == domain.ReqFailed {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	got, _ := env.store.GetRequirement(context.Background(), req.ID)
	if got.Status != domain.ReqFailed {
		t.Fatalf("后台巡检应把任务判定为超时，实际状态 %q", got.Status)
	}
	cancel()
}

// TestAccessorsAndHelpers 覆盖服务层的访问器与内部辅助函数。
func TestAccessorsAndHelpers(t *testing.T) {
	env := newTestEnv(t, "accept")
	if env.svc.Store() == nil {
		t.Fatal("Store 访问器不应返回 nil")
	}
	if env.svc.Config().ClockScale != 1 {
		t.Fatalf("Config 访问器返回值不正确: %+v", env.svc.Config())
	}
	if !isUniqueErr(errors.New("UNIQUE constraint failed: generation_tasks.request_no")) {
		t.Fatal("唯一约束错误应被识别")
	}
	if isUniqueErr(errors.New("其它错误")) {
		t.Fatal("非唯一约束错误不应被识别")
	}
	if isUniqueErr(nil) {
		t.Fatal("空错误不应被识别为唯一约束冲突")
	}
	if !errors.Is(wrapNotFound(domain.ErrNotFound), domain.ErrNotFound) {
		t.Fatal("ErrNotFound 应原样返回")
	}
	other := errors.New("其它错误")
	if wrapNotFound(other) != other {
		t.Fatal("其它错误应原样返回")
	}
}

// TestActorPermissions 覆盖角色可见范围判定。
func TestActorPermissions(t *testing.T) {
	env := newTestEnv(t, "accept")
	req := env.newRequirement(t, "初始描述文本")
	if !actorCanView(env.requester, req) {
		t.Fatal("需求人应能看自己的需求")
	}
	other := &domain.Requirement{OwnerID: "demand-b"}
	if actorCanView(env.requester, other) {
		t.Fatal("需求人不应能看他人需求")
	}
	if !actorCanView(env.reviewer, other) {
		t.Fatal("复核人应能查看需求")
	}
	if !actorCanView(env.operator, other) {
		t.Fatal("运营应能查看需求")
	}
	unknown := &domain.User{Role: domain.Role("unknown")}
	if actorCanView(unknown, req) {
		t.Fatal("未知角色不应有可见范围")
	}
	if actorCanOperate(unknown, req) {
		t.Fatal("未知角色不应能操作需求")
	}
}

// TestAdoptWithMismatchedCandidate 覆盖确认采用时的候选归属校验。
func TestAdoptWithMismatchedCandidate(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-1"); err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	if _, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: env.supplier.taskID(), Status: "succeeded", ImageURL: "/static/img/a.svg",
	}); err != nil {
		t.Fatalf("回调失败: %v", err)
	}
	if _, err := env.svc.ReviewCandidate(ctx, env.reviewer, req.ID, "", true, "通过"); err != nil {
		t.Fatalf("复核失败: %v", err)
	}
	if _, err := env.svc.Adopt(ctx, env.requester, req.ID, "cand-missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("不存在的候选应返回 ErrNotFound，实际 %v", err)
	}
	// 另一个需求的候选：用于覆盖「候选不属于该需求」分支
	otherReq := env.newRequirement(t, "另一个需求的描述文本")
	if _, err := env.svc.StartGeneration(ctx, env.requester, otherReq.ID, "r-other"); err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	if _, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: env.supplier.taskID(), Status: "succeeded", ImageURL: "/static/img/b.svg",
	}); err != nil {
		t.Fatalf("回调失败: %v", err)
	}
	otherCand, err := env.store.GetCurrentCandidate(ctx, otherReq.ID)
	if err != nil {
		t.Fatalf("读取他需求候选失败: %v", err)
	}
	if _, err := env.svc.Adopt(ctx, env.requester, req.ID, otherCand.ID); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("不属于该需求的候选应返回 ErrInvalidInput，实际 %v", err)
	}
	if _, err := env.svc.ReviewCandidate(ctx, env.reviewer, req.ID, otherCand.ID, true, "他需求候选"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("复核他需求候选应返回 ErrInvalidInput，实际 %v", err)
	}
}

// TestRequestReworkWrongState 覆盖非「复核通过」状态下提出重做。
func TestRequestReworkWrongState(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	if err := env.svc.RequestRework(ctx, env.requester, req.ID, "图片不符合文案"); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("草稿状态提出重做应返回 ErrInvalidState，实际 %v", err)
	}
	if err := env.svc.RequestRework(ctx, env.reviewer, req.ID, "复核人无权"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("复核人提出重做应返回 ErrForbidden，实际 %v", err)
	}
}

// TestHandleCallbackConflictOnFinishedTask 覆盖终态任务收到冲突回调的分支。
func TestHandleCallbackConflictOnFinishedTask(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-1"); err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	supplierTask := env.supplier.taskID()
	if _, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "failed", Reason: "供应商失败",
	}); err != nil {
		t.Fatalf("失败回调处理失败: %v", err)
	}
	// 任务已是终态，再收到成功回调属于冲突
	result, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "succeeded", ImageURL: "/static/img/a.svg",
	})
	if err != nil {
		t.Fatalf("冲突回调不应报错: %v", err)
	}
	if !result.Ignored {
		t.Fatalf("冲突回调应被忽略: %+v", result)
	}
	events, _ := env.store.ListEvents(ctx, req.ID)
	found := false
	for _, e := range events {
		if e.Kind == domain.EventCallbackConflict {
			found = true
		}
	}
	if !found {
		t.Fatalf("冲突回调应留痕: %+v", events)
	}
	cands, _ := env.store.ListCandidates(ctx, req.ID)
	if len(cands) != 0 {
		t.Fatalf("冲突回调不应产生候选，实际 %d", len(cands))
	}
}

// TestReviewWithoutCandidate 覆盖没有候选时的复核请求。
func TestReviewWithoutCandidate(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	if _, err := env.svc.ReviewCandidate(ctx, env.reviewer, req.ID, "", true, "通过"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("无候选时复核应返回 ErrNotFound，实际 %v", err)
	}
}

// TestQueriesOnMissingObjects 覆盖查询不存在对象的分支。
func TestQueriesOnMissingObjects(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	if _, err := env.svc.RequirementDetail(ctx, env.requester, "req-missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("读取不存在需求应返回 ErrNotFound，实际 %v", err)
	}
	if _, err := env.svc.StartGeneration(ctx, env.requester, "req-missing", "r-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("对不存在需求发起生成应返回 ErrNotFound，实际 %v", err)
	}
	if err := env.svc.UpdateDescription(ctx, env.requester, "req-missing", "新的描述文本"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("修改不存在需求应返回 ErrNotFound，实际 %v", err)
	}
	if err := env.svc.Withdraw(ctx, env.requester, "req-missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("撤销不存在需求应返回 ErrNotFound，实际 %v", err)
	}
	if err := env.svc.AddOperatorNote(ctx, env.operator, "req-missing", "跟进"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("对不存在需求记录意见应返回 ErrNotFound，实际 %v", err)
	}
	if _, err := env.svc.Adopt(ctx, env.requester, "req-missing", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("确认不存在需求应返回 ErrNotFound，实际 %v", err)
	}
	if err := env.svc.RequestRework(ctx, env.requester, "req-missing", "重做原因"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("对不存在需求提出重做应返回 ErrNotFound，实际 %v", err)
	}
}
