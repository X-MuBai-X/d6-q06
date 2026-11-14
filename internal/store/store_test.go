package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"creative-studio/internal/domain"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func seedRequirement(t *testing.T, st *Store, id string) *domain.Requirement {
	t.Helper()
	now := time.Now()
	req := &domain.Requirement{
		ID: id, OwnerID: "demand-a", ActivityName: "活动", Purpose: "用途",
		Description: "描述文本", Status: domain.ReqDraft, CreatedAt: now, UpdatedAt: now,
	}
	if err := st.InsertRequirement(context.Background(), req); err != nil {
		t.Fatalf("写入需求失败: %v", err)
	}
	return req
}

func seedTask(t *testing.T, st *Store, id, reqID, requestNo string, attempt int, status domain.TaskStatus) *domain.GenerationTask {
	t.Helper()
	task := &domain.GenerationTask{
		ID: id, RequirementID: reqID, RequestNo: requestNo, AttemptNo: attempt,
		DescriptionSnapshot: "描述快照", Status: status, CreatedAt: time.Now(),
	}
	if err := st.InsertTask(context.Background(), task); err != nil {
		t.Fatalf("写入任务失败: %v", err)
	}
	return task
}

func TestOpenAndMigrate(t *testing.T) {
	st := newTestStore(t)
	var count int
	err := st.DB().QueryRow("SELECT COUNT(*) FROM requirements").Scan(&count)
	if err != nil {
		t.Fatalf("查询需求数量失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("新库需求数量应为 0，实际 %d", count)
	}
	// 重复打开不应报错（建表语句幂等）
	again, err := Open(filepath.Join(t.TempDir(), "again.db"))
	if err != nil {
		t.Fatalf("建表应幂等: %v", err)
	}
	_ = again.Close()
}

func TestRequirementCRUD(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedRequirement(t, st, "req-1")

	got, err := st.GetRequirement(ctx, "req-1")
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.ActivityName != "活动" || got.Status != domain.ReqDraft {
		t.Fatalf("需求内容不正确: %+v", got)
	}
	if _, err := st.GetRequirement(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("不存在的需求应返回 ErrNotFound，实际 %v", err)
	}

	if err := st.UpdateDescription(ctx, "req-1", "新的描述", time.Now()); err != nil {
		t.Fatalf("更新描述失败: %v", err)
	}
	got, _ = st.GetRequirement(ctx, "req-1")
	if got.Description != "新的描述" {
		t.Fatalf("描述未更新: %+v", got)
	}
	if err := st.UpdateDescription(ctx, "missing", "x", time.Now()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("更新不存在的需求应返回 ErrNotFound，实际 %v", err)
	}

	if err := st.UpdateRequirementStatus(ctx, "req-1", domain.ReqGenerating, "task-1", "", 1, time.Now()); err != nil {
		t.Fatalf("更新状态失败: %v", err)
	}
	got, _ = st.GetRequirement(ctx, "req-1")
	if got.Status != domain.ReqGenerating || got.CurrentTaskID != "task-1" || got.AttemptCount != 1 {
		t.Fatalf("状态更新不正确: %+v", got)
	}
	if err := st.UpdateRequirementStatus(ctx, "missing", domain.ReqDraft, "", "", 0, time.Now()); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("更新不存在的需求应返回 ErrNotFound，实际 %v", err)
	}

	items, err := st.ListRequirements(ctx, RequirementFilter{OwnerID: "demand-a"})
	if err != nil || len(items) != 1 {
		t.Fatalf("按需求人过滤应返回 1 条，实际 %d（%v）", len(items), err)
	}
	items, _ = st.ListRequirements(ctx, RequirementFilter{OwnerID: "other"})
	if len(items) != 0 {
		t.Fatalf("他人需求不应可见，实际 %d 条", len(items))
	}
	items, _ = st.ListRequirements(ctx, RequirementFilter{Statuses: []domain.RequirementStatus{domain.ReqGenerating}})
	if len(items) != 1 {
		t.Fatalf("按状态过滤应返回 1 条，实际 %d 条", len(items))
	}
	items, _ = st.ListRequirements(ctx, RequirementFilter{Statuses: []domain.RequirementStatus{domain.ReqDelivered}})
	if len(items) != 0 {
		t.Fatalf("不匹配的状态应返回 0 条，实际 %d 条", len(items))
	}
}

func TestTaskUniqueness(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedRequirement(t, st, "req-1")
	seedTask(t, st, "task-1", "req-1", "req-no-1", 1, domain.TaskProcessing)

	// 同一需求不允许同时存在两个处理中任务（题面 R2）
	err := st.InsertTask(ctx, &domain.GenerationTask{
		ID: "task-2", RequirementID: "req-1", RequestNo: "req-no-2", AttemptNo: 2,
		DescriptionSnapshot: "x", Status: domain.TaskProcessing, CreatedAt: time.Now(),
	})
	if err == nil {
		t.Fatal("同一需求第二个处理中任务应被唯一索引拒绝")
	}
	if !isUniqueViolation(err) {
		t.Fatalf("错误应为唯一约束冲突，实际 %v", err)
	}

	// 同一请求编号不允许重复（幂等键）
	err = st.InsertTask(ctx, &domain.GenerationTask{
		ID: "task-3", RequirementID: "req-1", RequestNo: "req-no-1", AttemptNo: 2,
		DescriptionSnapshot: "x", Status: domain.TaskFailed, CreatedAt: time.Now(),
	})
	if err == nil || !isUniqueViolation(err) {
		t.Fatalf("重复请求编号应被唯一索引拒绝，实际 %v", err)
	}

	byNo, err := st.GetTaskByRequestNo(ctx, "req-no-1")
	if err != nil || byNo.ID != "task-1" {
		t.Fatalf("按请求编号读取失败: %+v（%v）", byNo, err)
	}
	bySupplier, err := st.GetTaskBySupplierTaskID(ctx, "sup-none")
	if err == nil {
		t.Fatal("未知的供应商任务号不应命中任务")
	}
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实际 %v", err)
	}
	_ = bySupplier
}

// TestGetTaskByRequirementRequestNo 覆盖按需求+请求编号读取（幂等键按需求隔离）。
func TestGetTaskByRequirementRequestNo(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedRequirement(t, st, "req-1")
	seedRequirement(t, st, "req-2")
	seedTask(t, st, "task-1", "req-1", "req-no-1", 1, domain.TaskProcessing)
	seedTask(t, st, "task-2", "req-2", "req-no-1", 1, domain.TaskProcessing)

	got, err := st.GetTaskByRequirementRequestNo(ctx, "req-1", "req-no-1")
	if err != nil || got.ID != "task-1" {
		t.Fatalf("按需求与请求编号读取失败: %+v（%v）", got, err)
	}
	// 需求不匹配时不得命中别的需求的任务
	if _, err := st.GetTaskByRequirementRequestNo(ctx, "req-none", "req-no-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("需求不匹配应返回 ErrNotFound，实际 %v", err)
	}
	if _, err := st.GetTaskByRequirementRequestNo(ctx, "req-1", "req-no-none"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("请求编号不存在应返回 ErrNotFound，实际 %v", err)
	}
}

// TestListRequirementsLimitAndAnomalyLimit 覆盖列表查询的数量上限分支。
func TestListRequirementsLimitAndAnomalyLimit(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedRequirement(t, st, "req-1")
	seedRequirement(t, st, "req-2")

	items, err := st.ListRequirements(ctx, RequirementFilter{Limit: 1})
	if err != nil || len(items) != 1 {
		t.Fatalf("带上限的需求列表应返回 1 条，实际 %d（%v）", len(items), err)
	}

	kinds := []string{domain.EventLateResultIgnored, domain.EventGenerationTimeout}
	if err := st.InsertEvent(ctx, &domain.Event{
		RequirementID: "req-1", Kind: domain.EventLateResultIgnored, Detail: "迟到结果被忽略", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("写入审计事件失败: %v", err)
	}
	anomalies, err := st.ListAnomalyEvents(ctx, kinds, 5)
	if err != nil || len(anomalies) != 1 {
		t.Fatalf("异常列表应返回 1 条，实际 %d（%v）", len(anomalies), err)
	}
}

func TestTaskQueriesAndUpdate(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedRequirement(t, st, "req-1")
	seedTask(t, st, "task-1", "req-1", "req-no-1", 1, domain.TaskProcessing)

	processing, err := st.FindProcessingTask(ctx, "req-1")
	if err != nil || processing.ID != "task-1" {
		t.Fatalf("查找处理中任务失败: %+v（%v）", processing, err)
	}
	if _, err := st.FindProcessingTask(ctx, "req-none"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实际 %v", err)
	}

	task, _ := st.GetTask(ctx, "task-1")
	task.Status = domain.TaskSucceeded
	task.SupplierTaskID = "sup-1"
	task.ImageURL = "/static/img/a.svg"
	task.FinishedAt = time.Now()
	if err := st.UpdateTask(ctx, task); err != nil {
		t.Fatalf("更新任务失败: %v", err)
	}
	got, _ := st.GetTask(ctx, "task-1")
	if got.Status != domain.TaskSucceeded || got.SupplierTaskID != "sup-1" {
		t.Fatalf("任务更新不正确: %+v", got)
	}
	bySupplier, err := st.GetTaskBySupplierTaskID(ctx, "sup-1")
	if err != nil || bySupplier.ID != "task-1" {
		t.Fatalf("按供应商任务号读取失败: %+v（%v）", bySupplier, err)
	}

	old := time.Now().Add(-10 * time.Minute)
	seedTask(t, st, "task-old", "req-1", "req-no-old", 2, domain.TaskProcessing)
	if _, err := st.DB().Exec("UPDATE generation_tasks SET created_at = ? WHERE id = ?",
		old.UTC().Format(time.RFC3339Nano), "task-old"); err != nil {
		t.Fatalf("调整时间失败: %v", err)
	}
	list, err := st.ListProcessingTasksBefore(ctx, time.Now().Add(-time.Minute))
	if err != nil || len(list) != 1 || list[0].ID != "task-old" {
		t.Fatalf("超时任务筛选不正确: %+v（%v）", list, err)
	}
	tasks, err := st.ListTasks(ctx, "req-1")
	if err != nil || len(tasks) != 2 {
		t.Fatalf("任务列表应返回 2 条，实际 %d（%v）", len(tasks), err)
	}
}

func TestCandidateLifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedRequirement(t, st, "req-1")
	seedTask(t, st, "task-1", "req-1", "req-no-1", 1, domain.TaskSucceeded)

	cand := &domain.Candidate{
		ID: "cand-1", TaskID: "task-1", RequirementID: "req-1", AttemptNo: 1,
		ImageURL: "/static/img/a.svg", ReviewStatus: domain.ReviewPending, IsCurrent: true, CreatedAt: time.Now(),
	}
	if err := st.InsertCandidate(ctx, cand); err != nil {
		t.Fatalf("写入候选失败: %v", err)
	}
	current, err := st.GetCurrentCandidate(ctx, "req-1")
	if err != nil || current.ID != "cand-1" {
		t.Fatalf("读取当前候选失败: %+v（%v）", current, err)
	}
	cand.ReviewStatus = domain.ReviewApproved
	cand.ReviewReason = "通过"
	cand.ReviewerID = "review-a"
	cand.ReviewedAt = time.Now()
	if err := st.UpdateCandidateReview(ctx, cand); err != nil {
		t.Fatalf("更新复核结论失败: %v", err)
	}
	got, _ := st.GetCandidate(ctx, "cand-1")
	if got.ReviewStatus != domain.ReviewApproved || got.ReviewerID != "review-a" {
		t.Fatalf("复核结论未写入: %+v", got)
	}
	if err := st.ClearCurrentCandidates(ctx, "req-1"); err != nil {
		t.Fatalf("清除当前标记失败: %v", err)
	}
	if _, err := st.GetCurrentCandidate(ctx, "req-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("清除后不应有当前候选，实际 %v", err)
	}
	list, err := st.ListCandidates(ctx, "req-1")
	if err != nil || len(list) != 1 {
		t.Fatalf("候选列表应返回 1 条，实际 %d（%v）", len(list), err)
	}
}

func TestEventsAndSessions(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedRequirement(t, st, "req-1")

	evt := &domain.Event{
		RequirementID: "req-1", Kind: domain.EventGenerationStarted, Detail: "开始", CreatedAt: time.Now(),
	}
	if err := st.InsertEvent(ctx, evt); err != nil {
		t.Fatalf("写入事件失败: %v", err)
	}
	events, err := st.ListEvents(ctx, "req-1")
	if err != nil || len(events) != 1 {
		t.Fatalf("事件列表应返回 1 条，实际 %d（%v）", len(events), err)
	}
	anomalyKinds := []string{
		domain.EventCallbackDuplicate, domain.EventCallbackConflict, domain.EventLateResultIgnored,
		domain.EventSignatureInvalid, domain.EventCallbackUnknown, domain.EventGenerationRejected,
		domain.EventGenerationFailed, domain.EventGenerationTimeout,
	}
	anomalies, err := st.ListAnomalyEvents(ctx, anomalyKinds, 0)
	if err != nil || len(anomalies) != 0 {
		t.Fatalf("普通事件不应出现在异常列表，实际 %d（%v）", len(anomalies), err)
	}
	if err := st.InsertEvent(ctx, &domain.Event{
		RequirementID: "req-1", Kind: domain.EventCallbackDuplicate, Detail: "重复", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("写入异常事件失败: %v", err)
	}
	anomalies, _ = st.ListAnomalyEvents(ctx, anomalyKinds, 0)
	if len(anomalies) != 1 {
		t.Fatalf("异常列表应返回 1 条，实际 %d", len(anomalies))
	}
	empty, err := st.ListAnomalyEvents(ctx, nil, 0)
	if err != nil || len(empty) != 0 {
		t.Fatalf("空异常清单应返回空列表，实际 %d（%v）", len(empty), err)
	}
	recent, err := st.ListRecentEvents(ctx, 10)
	if err != nil || len(recent) != 2 {
		t.Fatalf("最近事件应返回 2 条，实际 %d（%v）", len(recent), err)
	}

	user := &domain.User{ID: "demand-a", Name: "需求人甲", Role: domain.RoleRequester, PasswordHash: "h", Salt: "s"}
	if err := st.InsertUser(ctx, user); err != nil {
		t.Fatalf("写入账号失败: %v", err)
	}
	if err := st.InsertUser(ctx, user); err == nil {
		t.Fatal("重复账号应被主键约束拒绝")
	}
	got, err := st.GetUser(ctx, "demand-a")
	if err != nil || got.Role != domain.RoleRequester {
		t.Fatalf("读取账号失败: %+v（%v）", got, err)
	}
	if _, err := st.GetUser(ctx, "nobody"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实际 %v", err)
	}

	if err := st.InsertSession(ctx, "token-1", "demand-a", time.Now()); err != nil {
		t.Fatalf("写入会话失败: %v", err)
	}
	sessionUser, err := st.GetSessionUser(ctx, "token-1")
	if err != nil || sessionUser.ID != "demand-a" {
		t.Fatalf("会话读取失败: %+v（%v）", sessionUser, err)
	}
	if _, err := st.GetSessionUser(ctx, "bad-token"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("无效令牌应返回 ErrUnauthorized，实际 %v", err)
	}
	if _, err := st.GetSessionUser(ctx, ""); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("空令牌应返回 ErrUnauthorized，实际 %v", err)
	}
	if err := st.DeleteSession(ctx, "token-1"); err != nil {
		t.Fatalf("注销会话失败: %v", err)
	}
	if _, err := st.GetSessionUser(ctx, "token-1"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("注销后令牌应失效，实际 %v", err)
	}
}

func TestTxOperations(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	seedRequirement(t, st, "req-1")

	err := st.Tx(ctx, func(tx Execer) error {
		req, err := st.GetRequirementTx(ctx, tx, "req-1")
		if err != nil {
			return err
		}
		if err := st.UpdateRequirementDescriptionTx(ctx, tx, req.ID, "事务内描述", time.Now()); err != nil {
			return err
		}
		return st.InsertEventTx(ctx, tx, &domain.Event{
			RequirementID: req.ID, Kind: domain.EventDescriptionUpdated, Detail: "事务内", CreatedAt: time.Now(),
		})
	})
	if err != nil {
		t.Fatalf("事务执行失败: %v", err)
	}
	got, _ := st.GetRequirement(ctx, "req-1")
	if got.Description != "事务内描述" {
		t.Fatalf("事务内更新未生效: %+v", got)
	}

	// 事务回滚：写入应被撤销
	err = st.Tx(ctx, func(tx Execer) error {
		if err := st.UpdateRequirementDescriptionTx(ctx, tx, "req-1", "将被回滚", time.Now()); err != nil {
			return err
		}
		return errors.New("故意失败")
	})
	if err == nil {
		t.Fatal("事务应返回错误")
	}
	got, _ = st.GetRequirement(ctx, "req-1")
	if got.Description != "事务内描述" {
		t.Fatalf("回滚未生效: %+v", got)
	}

	if _, err := st.GetRequirementTx(ctx, st.DB(), "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实际 %v", err)
	}
	if _, err := st.GetCandidateTx(ctx, st.DB(), "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实际 %v", err)
	}
	if _, err := st.GetTaskTx(ctx, st.DB(), "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实际 %v", err)
	}
	if _, err := st.GetTaskByRequestNoTx(ctx, st.DB(), "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实际 %v", err)
	}
	if _, err := st.GetTaskBySupplierTaskIDTx(ctx, st.DB(), "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实际 %v", err)
	}
	if _, err := st.FindProcessingTaskTx(ctx, st.DB(), "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("应返回 ErrNotFound，实际 %v", err)
	}
}
