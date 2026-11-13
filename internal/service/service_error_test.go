package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"creative-studio/internal/domain"
)

// TestClosedDatabaseErrorPaths 覆盖数据库不可用时的错误分支。
// 关闭底层连接后，业务调用必须返回错误而不是 panic。
func TestClosedDatabaseErrorPaths(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	if err := env.store.Close(); err != nil {
		t.Fatalf("关闭数据库失败: %v", err)
	}

	if _, err := env.svc.RequirementDetail(ctx, env.requester, req.ID); err == nil {
		t.Fatal("数据库关闭后读取详情应报错")
	}
	if _, err := env.svc.ListRequirements(ctx, env.requester, nil); err == nil {
		t.Fatal("数据库关闭后列出需求应报错")
	}
	if _, err := env.svc.Overview(ctx, env.operator); err == nil {
		t.Fatal("数据库关闭后查看总览应报错")
	}
	if _, err := env.svc.ListAnomalies(ctx, env.operator, 10); err == nil {
		t.Fatal("数据库关闭后查看异常应报错")
	}
	if _, err := env.svc.ReviewQueue(ctx, env.reviewer); err == nil {
		t.Fatal("数据库关闭后查看复核队列应报错")
	}
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-closed"); err == nil {
		t.Fatal("数据库关闭后发起生成应报错")
	}
	if _, err := env.svc.SweepTimeouts(ctx); err == nil {
		t.Fatal("数据库关闭后巡检应报错")
	}
	if err := env.svc.AddOperatorNote(ctx, env.operator, req.ID, "跟进"); err == nil {
		t.Fatal("数据库关闭后记录意见应报错")
	}
	if err := env.svc.Withdraw(ctx, env.requester, req.ID); err == nil {
		t.Fatal("数据库关闭后撤销应报错")
	}
	if err := env.svc.RequestRework(ctx, env.requester, req.ID, "重做原因"); err == nil {
		t.Fatal("数据库关闭后提出重做应报错")
	}
	if _, err := env.svc.Adopt(ctx, env.requester, req.ID, ""); err == nil {
		t.Fatal("数据库关闭后确认采用应报错")
	}
	if err := env.svc.RecordAnomaly(ctx, domain.EventSignatureInvalid, "", "", "签名错误"); err == nil {
		t.Fatal("数据库关闭后记录异常应报错")
	}
}

// TestListRequirementsUnknownRole 覆盖未知角色的分支。
func TestListRequirementsUnknownRole(t *testing.T) {
	env := newTestEnv(t, "accept")
	unknown := &domain.User{ID: "x", Role: domain.Role("unknown")}
	if _, err := env.svc.ListRequirements(context.Background(), unknown, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("未知角色应返回 ErrForbidden，实际 %v", err)
	}
}

// TestListRequirementsByStatus 覆盖按状态过滤的查询分支。
func TestListRequirementsByStatus(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	env.newRequirement(t, "初始描述文本")
	items, err := env.svc.ListRequirements(ctx, env.operator,
		[]domain.RequirementStatus{domain.ReqDraft})
	if err != nil || len(items) != 1 {
		t.Fatalf("按状态过滤应返回 1 条，实际 %d（%v）", len(items), err)
	}
	items, err = env.svc.ListRequirements(ctx, env.operator,
		[]domain.RequirementStatus{domain.ReqDelivered})
	if err != nil || len(items) != 0 {
		t.Fatalf("不匹配的状态应返回 0 条，实际 %d（%v）", len(items), err)
	}
}

// TestAdoptWithCurrentCandidateID 覆盖带候选编号确认采用的正常路径。
func TestAdoptWithCurrentCandidateID(t *testing.T) {
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
	cand, err := env.store.GetCurrentCandidate(ctx, req.ID)
	if err != nil {
		t.Fatalf("读取候选失败: %v", err)
	}
	if _, err := env.svc.ReviewCandidate(ctx, env.reviewer, req.ID, cand.ID, true, "通过"); err != nil {
		t.Fatalf("复核失败: %v", err)
	}
	delivered, err := env.svc.Adopt(ctx, env.requester, req.ID, cand.ID)
	if err != nil {
		t.Fatalf("确认采用失败: %v", err)
	}
	if delivered.Status != domain.ReqDelivered {
		t.Fatalf("确认采用后状态应为 delivered，实际 %q", delivered.Status)
	}
}

// TestHandleCallbackFailureAfterWithdraw 覆盖撤销后收到失败回调的分支。
func TestHandleCallbackFailureAfterWithdraw(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-1"); err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	if err := env.svc.Withdraw(ctx, env.requester, req.ID); err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	result, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: env.supplier.taskID(), Status: "failed", Reason: "供应商失败",
	})
	if err != nil {
		t.Fatalf("失败回调不应报错: %v", err)
	}
	if !result.Applied {
		t.Fatalf("失败回调应被记录: %+v", result)
	}
	got, _ := env.store.GetRequirement(ctx, req.ID)
	if got.Status != domain.ReqWithdrawn {
		t.Fatalf("撤销后的需求状态不应被改写，实际 %q", got.Status)
	}
}
func TestSweepTimeoutsWithMissingRequirement(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-1"); err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	if _, err := env.store.DB().Exec("UPDATE generation_tasks SET created_at = ?",
		time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("调整时间失败: %v", err)
	}
	timedOut, err := env.svc.SweepTimeouts(ctx)
	if err != nil {
		t.Fatalf("巡检失败: %v", err)
	}
	if len(timedOut) != 1 {
		t.Fatalf("应判定 1 个任务超时，实际 %d", len(timedOut))
	}
}
