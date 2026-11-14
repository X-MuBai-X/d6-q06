package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"creative-studio/internal/domain"
)

// TestRegressionStartGenerationAfterWithdraw 覆盖 D-01：已撤销的需求不得再次发起生成。
// 期望：服务层返回状态错误（HTTP 409），需求保持 withdrawn，不新增任务、不调用供应商。
func TestRegressionStartGenerationAfterWithdraw(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")

	if err := env.svc.Withdraw(ctx, env.requester, req.ID); err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	got, _ := env.store.GetRequirement(ctx, req.ID)
	if got.Status != domain.ReqWithdrawn {
		t.Fatalf("撤销后状态应为 withdrawn，实际 %q", got.Status)
	}

	// 撤销后用新请求编号发起生成：必须被明确拒绝
	task, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "after-withdraw-1")
	if !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("撤销后发起生成应返回 ErrInvalidState（映射 409），实际 %v", err)
	}
	if task != nil {
		t.Fatalf("撤销后发起生成不应返回任务对象，实际 %+v", task)
	}
	// 需求状态不得被复活
	got, _ = env.store.GetRequirement(ctx, req.ID)
	if got.Status != domain.ReqWithdrawn {
		t.Fatalf("撤销后的需求不应被复活，实际 %q", got.Status)
	}
	// 不得新增生成任务
	tasks, _ := env.store.ListTasks(ctx, req.ID)
	if len(tasks) != 0 {
		t.Fatalf("撤销后发起不应新增生成任务，实际 %d 条", len(tasks))
	}
	// 不得向供应商提交
	if env.supplier.callCount() != 0 {
		t.Fatalf("撤销后发起不应调用供应商，实际调用 %d 次", env.supplier.callCount())
	}
}

// TestRegressionStartGenerationAfterWithdrawWhileGenerating 覆盖 D-01 的生成中撤销场景：
// 撤销时仍有处理中任务，撤销后再次发起同样必须被拒绝。
func TestRegressionStartGenerationAfterWithdrawWhileGenerating(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "gen-1"); err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	if err := env.svc.Withdraw(ctx, env.requester, req.ID); err != nil {
		t.Fatalf("撤销失败: %v", err)
	}

	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "gen-2"); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("撤销后再次发起应返回 ErrInvalidState，实际 %v", err)
	}
	got, _ := env.store.GetRequirement(ctx, req.ID)
	if got.Status != domain.ReqWithdrawn {
		t.Fatalf("撤销后的需求不应被复活，实际 %q", got.Status)
	}
	tasks, _ := env.store.ListTasks(ctx, req.ID)
	if len(tasks) != 1 {
		t.Fatalf("撤销后再次发起不应新增生成任务，实际 %d 条", len(tasks))
	}
	if env.supplier.callCount() != 1 {
		t.Fatalf("撤销后再次发起不应调用供应商，实际调用 %d 次", env.supplier.callCount())
	}
}

// TestRegressionSweepTimeoutsKeepsWithdrawnRequirement 覆盖 D-02：
// 生成中撤销后，超时巡检不得把已撤销的需求改回 failed／exhausted。
func TestRegressionSweepTimeoutsKeepsWithdrawnRequirement(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	task, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "sweep-2")
	if err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	if err := env.svc.Withdraw(ctx, env.requester, req.ID); err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	// 把任务创建时间提前，使其已超过超时上限
	if _, err := env.store.DB().Exec("UPDATE generation_tasks SET created_at = ?",
		time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("调整时间失败: %v", err)
	}

	timedOut, err := env.svc.SweepTimeouts(ctx)
	if err != nil {
		t.Fatalf("巡检失败: %v", err)
	}
	if len(timedOut) != 1 || timedOut[0] != task.ID {
		t.Fatalf("应判定 1 个任务超时，实际 %v", timedOut)
	}
	// 需求必须保持撤销
	got, _ := env.store.GetRequirement(ctx, req.ID)
	if got.Status != domain.ReqWithdrawn {
		t.Fatalf("撤销后的需求不得被巡检复活，实际 %q", got.Status)
	}
	// 任务本身判定为超时
	stored, err := env.store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if stored.Status != domain.TaskTimeout {
		t.Fatalf("任务状态应为 timeout，实际 %q", stored.Status)
	}
	// 事件：留「迟到结果被忽略」痕迹，且不得写 generation_timeout
	events, _ := env.store.ListEvents(ctx, req.ID)
	lateIgnored := false
	for _, e := range events {
		if e.Kind == domain.EventGenerationTimeout {
			t.Fatalf("撤销后的巡检不应写 generation_timeout 事件: %+v", events)
		}
		if e.Kind == domain.EventLateResultIgnored {
			lateIgnored = true
		}
	}
	if !lateIgnored {
		t.Fatalf("撤销后的巡检应留痕 late_result_ignored: %+v", events)
	}
}

// TestRegressionStartGenerationCrossRequirementRequestNo 覆盖 D-04：
// 跨需求复用同一请求编号必须被明确拒绝，不得返回别的需求的任务对象。
func TestRegressionStartGenerationCrossRequirementRequestNo(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	first := env.newRequirement(t, "第一个需求的描述文本")
	second := env.newRequirement(t, "第二个需求的描述文本")

	task, err := env.svc.StartGeneration(ctx, env.requester, first.ID, "shared-request-no")
	if err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}

	reused, err := env.svc.StartGeneration(ctx, env.requester, second.ID, "shared-request-no")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("跨需求复用请求编号应返回 ErrConflict（映射 409），实际 %v", err)
	}
	if reused != nil {
		t.Fatalf("跨需求复用请求编号不应返回任务对象，实际 %+v", reused)
	}
	// 第二个需求不得被静默执行
	got, _ := env.store.GetRequirement(ctx, second.ID)
	if got.Status != domain.ReqDraft {
		t.Fatalf("第二个需求状态不应变化，实际 %q", got.Status)
	}
	tasks, _ := env.store.ListTasks(ctx, second.ID)
	if len(tasks) != 0 {
		t.Fatalf("第二个需求不应创建生成任务，实际 %d 条", len(tasks))
	}
	if env.supplier.callCount() != 1 {
		t.Fatalf("跨需求复用不应再次调用供应商，实际调用 %d 次", env.supplier.callCount())
	}
	// 第一个需求的任务不受影响
	kept, err := env.store.GetTask(ctx, task.ID)
	if err != nil || kept.RequirementID != first.ID {
		t.Fatalf("原需求任务不应受影响: %+v（%v）", kept, err)
	}
}

// TestRegressionWithdrawnRequirementRejectsFurtherEdits 覆盖 D-01 的延伸：
// 已撤销的需求不得再被修改描述，也不得被别的需求人发起生成。
func TestRegressionWithdrawnRequirementRejectsFurtherEdits(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	other := seedUser(t, env.svc, "demand-b", "需求人乙", domain.RoleRequester)

	if err := env.svc.Withdraw(ctx, env.requester, req.ID); err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	if err := env.svc.UpdateDescription(ctx, env.requester, req.ID, "撤销后想改的描述"); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("撤销后修改描述应返回 ErrInvalidState，实际 %v", err)
	}
	if _, err := env.svc.StartGeneration(ctx, other, req.ID, "other-1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("他人对已撤销需求发起生成应返回 ErrForbidden，实际 %v", err)
	}
	got, _ := env.store.GetRequirement(ctx, req.ID)
	if got.Status != domain.ReqWithdrawn || got.Description != "初始描述文本" {
		t.Fatalf("已撤销需求不应被改写: %+v", got)
	}
	if env.supplier.callCount() != 0 {
		t.Fatalf("不应调用供应商，实际调用 %d 次", env.supplier.callCount())
	}
}

// TestRegressionDetailViewExposesCurrentCandidate 覆盖详情视图与异常查询：
// 当前候选必须在详情里可见，撤销后巡检留下的迟到结果也必须能被运营查到。
func TestRegressionDetailViewExposesCurrentCandidate(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "detail-1"); err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	if _, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: env.supplier.taskID(), Status: "succeeded", ImageURL: "/static/img/a.svg",
	}); err != nil {
		t.Fatalf("回调失败: %v", err)
	}
	view, err := env.svc.RequirementDetail(ctx, env.requester, req.ID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if view.CurrentCandidate == nil || view.CurrentCandidate.AttemptNo != 1 {
		t.Fatalf("详情应给出当前候选: %+v", view.CurrentCandidate)
	}

	// 另一个需求：撤销后巡检留下的迟到结果必须能被运营在异常记录里查到
	other := env.newRequirement(t, "另一个需求的描述文本")
	if _, err := env.svc.StartGeneration(ctx, env.requester, other.ID, "anomaly-1"); err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	if err := env.svc.Withdraw(ctx, env.requester, other.ID); err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	if _, err := env.store.DB().Exec("UPDATE generation_tasks SET created_at = ?",
		time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("调整时间失败: %v", err)
	}
	if _, err := env.svc.SweepTimeouts(ctx); err != nil {
		t.Fatalf("巡检失败: %v", err)
	}
	anomalies, err := env.svc.ListAnomalies(ctx, env.operator, 0)
	if err != nil {
		t.Fatalf("查询异常记录失败: %v", err)
	}
	found := false
	for _, e := range anomalies {
		if e.Kind == domain.EventLateResultIgnored && e.RequirementID == other.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("撤销后的迟到结果应出现在运营异常记录中: %+v", anomalies)
	}
}

// TestRegressionSeedUserIsIdempotent 覆盖预置账号的幂等性：重复预置保持原样。
func TestRegressionSeedUserIsIdempotent(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	if err := env.svc.SeedUser(ctx, "demand-a", "需求人甲", domain.RoleRequester, testPassword); err != nil {
		t.Fatalf("重复预置账号不应报错: %v", err)
	}
	user, err := env.store.GetUser(ctx, "demand-a")
	if err != nil || user.Name != "需求人甲" {
		t.Fatalf("重复预置不应改写账号: %+v（%v）", user, err)
	}
}

// TestRegressionStartGenerationIdempotencyWithinRequirement 确认同一需求内的幂等重放仍然有效：
// 同一个请求编号重复发起返回同一任务，不重复调用供应商。
func TestRegressionStartGenerationIdempotencyWithinRequirement(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")

	first, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "same-request-no")
	if err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	again, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "same-request-no")
	if err != nil {
		t.Fatalf("同一需求内幂等重放不应报错: %v", err)
	}
	if again == nil || again.ID != first.ID {
		t.Fatalf("幂等重放应返回同一任务，实际 %+v / %+v", first, again)
	}
	if env.supplier.callCount() != 1 {
		t.Fatalf("幂等重放不应再次调用供应商，实际调用 %d 次", env.supplier.callCount())
	}
}
