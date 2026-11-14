package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"creative-studio/internal/domain"
	"creative-studio/internal/store"
)

// 本文件补齐 internal/service 中尚未被覆盖的代码块。
// 覆盖对象是「真实失败分支」：数据库不可用、表被破坏、写入被拒绝、
// 供应商异常、以及并发/幂等边界。每个用例都断言可观察的业务结果
// （返回值、错误类型、落库内容、审计事件类型），不使用无意义断言。

// gapInject 注入一条数据库故障（触发器或结构性破坏），用于制造真实的读写失败。
func gapInject(t *testing.T, env *testEnv, ddl string) {
	t.Helper()
	if _, err := env.store.DB().Exec(ddl); err != nil {
		t.Fatalf("注入数据库故障失败: %v", err)
	}
}

// gapFailInsert 生成「该表插入失败」的触发器语句。
func gapFailInsert(table string) string {
	return fmt.Sprintf("CREATE TRIGGER gap_fail_%s_insert BEFORE INSERT ON %s BEGIN SELECT RAISE(ABORT, '注入的写库故障'); END", table, table)
}

// gapFailUpdate 生成「该表更新失败」的触发器语句。
func gapFailUpdate(table string) string {
	return fmt.Sprintf("CREATE TRIGGER gap_fail_%s_update BEFORE UPDATE ON %s BEGIN SELECT RAISE(ABORT, '注入的写库故障'); END", table, table)
}

// gapFailEventOnKind 生成「写入指定种类审计事件失败」的触发器语句。
func gapFailEventOnKind(kind string) string {
	return fmt.Sprintf(
		"CREATE TRIGGER gap_fail_event_%s BEFORE INSERT ON events WHEN NEW.kind = '%s' BEGIN SELECT RAISE(ABORT, '注入的写库故障'); END",
		kind, kind)
}

// gapEnvWithSupplier 构造一个使用指定模拟供应商处理器的测试环境。
func gapEnvWithSupplier(t *testing.T, handler http.HandlerFunc) *testEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "gap.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	svc := New(st, domain.SystemClock{}, Config{
		SupplierBaseURL: srv.URL,
		SupplierSecret:  "secret",
		SupplierTimeout: 2 * time.Second,
		ClockScale:      1,
	})
	env := &testEnv{svc: svc, store: st, clock: domain.SystemClock{}}
	env.requester = seedUser(t, svc, "demand-a", "需求人甲", domain.RoleRequester)
	env.reviewer = seedUser(t, svc, "review-a", "设计复核人甲", domain.RoleReviewer)
	env.operator = seedUser(t, svc, "ops-a", "运营甲", domain.RoleOperator)
	return env
}

// gapSeedProcessingTask 创建一个带「处理中」生成任务的需求，返回需求与供应商任务号。
func gapSeedProcessingTask(t *testing.T, env *testEnv) (*domain.Requirement, string) {
	t.Helper()
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "gap-"+req.ID); err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	return req, env.supplier.taskID()
}

// gapSeedReviewable 构造一个「待设计复核」的需求（含当前候选）。
func gapSeedReviewable(t *testing.T, env *testEnv) (*domain.Requirement, *domain.Candidate) {
	t.Helper()
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "gap-"+req.ID); err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	if _, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: env.supplier.taskID(), Status: "succeeded", ImageURL: "/static/img/a.svg",
	}); err != nil {
		t.Fatalf("处理成功回调失败: %v", err)
	}
	cand, err := env.store.GetCurrentCandidate(ctx, req.ID)
	if err != nil {
		t.Fatalf("读取当前候选失败: %v", err)
	}
	return req, cand
}

// gapSeedApproved 构造一个「复核通过、等待需求人确认采用」的需求。
func gapSeedApproved(t *testing.T, env *testEnv) (*domain.Requirement, *domain.Candidate) {
	t.Helper()
	ctx := context.Background()
	req, cand := gapSeedReviewable(t, env)
	if _, err := env.svc.ReviewCandidate(ctx, env.reviewer, req.ID, cand.ID, true, "复核通过"); err != nil {
		t.Fatalf("复核失败: %v", err)
	}
	return req, cand
}

// gapSeedOldProcessingTask 构造一个「早已创建、仍在处理中」的生成任务，用于超时巡检。
func gapSeedOldProcessingTask(t *testing.T, env *testEnv) (*domain.Requirement, string) {
	t.Helper()
	req, supplierTask := gapSeedProcessingTask(t, env)
	if _, err := env.store.DB().Exec("UPDATE generation_tasks SET created_at = ? WHERE requirement_id = ?",
		time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339Nano), req.ID); err != nil {
		t.Fatalf("调整任务时间失败: %v", err)
	}
	return req, supplierTask
}

// gapSeedSecondGeneration 构造「已有上一版候选、再次发起生成」的场景。
func gapSeedSecondGeneration(t *testing.T, env *testEnv) (*domain.Requirement, string) {
	t.Helper()
	ctx := context.Background()
	req, cand := gapSeedReviewable(t, env)
	if _, err := env.svc.ReviewCandidate(ctx, env.reviewer, req.ID, cand.ID, true, "复核通过"); err != nil {
		t.Fatalf("复核失败: %v", err)
	}
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "gap-second-"+req.ID); err != nil {
		t.Fatalf("再次发起生成失败: %v", err)
	}
	return req, env.supplier.taskID()
}

// TestGapLoginWhenAccountTableUnavailable 覆盖账号不可读时的登录失败。
// 业务含义：账号数据不可读属于系统故障，必须原样上抛，
// 不能被误报成「账号或口令不正确」而让用户反复重试。
func TestGapLoginWhenAccountTableUnavailable(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	gapInject(t, env, "DROP TABLE users")

	_, _, err := env.svc.Login(ctx, "demand-a", testPassword)
	if err == nil {
		t.Fatal("账号不可读时登录必须失败")
	}
	if errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("账号不可读属于系统故障，不应报成未授权，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "读取账号失败") {
		t.Fatalf("错误信息应说明是读取账号失败，实际 %v", err)
	}
}

// TestGapLoginWhenSessionCannotBeStored 覆盖会话写入失败时的登录失败。
// 业务含义：会话令牌落库失败时不得返回登录成功，否则会出现「拿到令牌却无法使用」的假会话。
func TestGapLoginWhenSessionCannotBeStored(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	gapInject(t, env, gapFailInsert("sessions"))

	token, _, err := env.svc.Login(ctx, "demand-a", testPassword)
	if err == nil {
		t.Fatal("会话写入失败时登录必须失败")
	}
	if token != "" {
		t.Fatalf("登录失败时不应返回令牌，实际 %q", token)
	}
	if !strings.Contains(err.Error(), "写入会话失败") {
		t.Fatalf("错误信息应说明是写入会话失败，实际 %v", err)
	}
	if _, err := env.store.GetSessionUser(ctx, "any-token"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("登录失败后不应存在可用会话，实际 %v", err)
	}
}

// TestGapCreateRequirementWhenStoreRejects 覆盖需求写入被拒绝时的失败。
// 业务含义：需求未落库时必须报错，不能返回一个并不存在的需求对象。
func TestGapCreateRequirementWhenStoreRejects(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	gapInject(t, env, gapFailInsert("requirements"))

	req, err := env.svc.CreateRequirement(ctx, env.requester, CreateRequirementInput{
		ActivityName: "夏日活动", Purpose: "新品主视觉", Description: "冷色调冰饮主视觉",
	})
	if err == nil {
		t.Fatal("需求写入被拒绝时必须报错")
	}
	if req != nil {
		t.Fatalf("写入失败时不应返回需求对象，实际 %+v", req)
	}
	items, listErr := env.store.ListRequirements(ctx, store.RequirementFilter{})
	if listErr != nil {
		t.Fatalf("查询需求列表失败: %v", listErr)
	}
	if len(items) != 0 {
		t.Fatalf("写入失败后不应残留需求，实际 %d 条", len(items))
	}
}

// TestGapAuditFailureDoesNotBreakRequirementCreation 覆盖审计写入失败时的创建流程。
// 业务含义：审计写入失败属于内部错误，不应影响已发生的业务判定——
// 需求依然创建成功，但该条审计流水确实缺失（可事后核对）。
func TestGapAuditFailureDoesNotBreakRequirementCreation(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	gapInject(t, env, gapFailInsert("events"))

	req, err := env.svc.CreateRequirement(ctx, env.requester, CreateRequirementInput{
		ActivityName: "夏日活动", Purpose: "新品主视觉", Description: "冷色调冰饮主视觉",
	})
	if err != nil {
		t.Fatalf("审计写入失败不应影响需求创建，实际 %v", err)
	}
	if req == nil || req.ID == "" {
		t.Fatalf("需求应创建成功，实际 %+v", req)
	}
	got, err := env.store.GetRequirement(ctx, req.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqDraft {
		t.Fatalf("新需求状态应为 draft，实际 %q", got.Status)
	}
	events, err := env.store.ListEvents(ctx, req.ID)
	if err != nil {
		t.Fatalf("查询审计事件失败: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("审计写入被拒绝后不应有事件，实际 %d 条", len(events))
	}
}

// TestGapUpdateDescriptionWhenStoreRejects 覆盖描述更新被拒绝时的失败。
// 业务含义：描述未更新成功时必须报错，且原描述必须保持不变（事务回滚）。
func TestGapUpdateDescriptionWhenStoreRejects(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	gapInject(t, env, gapFailUpdate("requirements"))

	err := env.svc.UpdateDescription(ctx, env.requester, req.ID, "新的描述文本")
	if err == nil {
		t.Fatal("描述更新被拒绝时必须报错")
	}
	got, err := env.store.GetRequirement(ctx, req.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Description != "初始描述文本" {
		t.Fatalf("更新失败后描述不应改变，实际 %q", got.Description)
	}
}

// TestGapStartGenerationWhenTaskTableUnavailable 覆盖任务表不可读时的发起失败。
// 业务含义：幂等键校验依赖任务表，任务表不可读时必须报错，
// 不能误判为「没有历史任务」而重复提交供应商。
func TestGapStartGenerationWhenTaskTableUnavailable(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	gapInject(t, env, "DROP TABLE generation_tasks")

	_, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-drop")
	if err == nil {
		t.Fatal("任务表不可读时发起生成必须失败")
	}
	if !strings.Contains(err.Error(), "读取生成任务失败") {
		t.Fatalf("错误信息应说明是读取生成任务失败，实际 %v", err)
	}
	if env.supplier.callCount() != 0 {
		t.Fatalf("任务表不可读时不应提交供应商，实际调用 %d 次", env.supplier.callCount())
	}
}

// TestGapStartGenerationOnUniqueConflict 覆盖任务写入被唯一约束拒绝时的冲突收口。
// 业务含义：并发重复发起命中唯一索引时，必须明确返回冲突（409 语义），
// 而不是把数据库错误直接抛给用户。
func TestGapStartGenerationOnUniqueConflict(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	gapInject(t, env, "CREATE TRIGGER gap_fail_task_insert_unique BEFORE INSERT ON generation_tasks "+
		"BEGIN SELECT RAISE(ABORT, 'UNIQUE constraint failed: generation_tasks.requirement_id'); END")

	_, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-unique")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("唯一约束冲突应返回 ErrConflict，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "该请求编号已存在") {
		t.Fatalf("错误信息应说明请求编号冲突，实际 %v", err)
	}
	got, err := env.store.GetRequirement(ctx, req.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqDraft {
		t.Fatalf("冲突后需求状态应回滚为 draft，实际 %q", got.Status)
	}
	if env.supplier.callCount() != 0 {
		t.Fatalf("冲突后不应提交供应商，实际调用 %d 次", env.supplier.callCount())
	}
}

// TestGapStartGenerationWhenTaskInsertFails 覆盖任务写入失败（非唯一约束）时的失败。
// 业务含义：写库失败必须报错，且整笔事务回滚——需求仍是草稿，供应商也没有被调用。
func TestGapStartGenerationWhenTaskInsertFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	gapInject(t, env, gapFailInsert("generation_tasks"))

	_, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-insert")
	if err == nil {
		t.Fatal("任务写入失败时必须报错")
	}
	if errors.Is(err, domain.ErrConflict) {
		t.Fatalf("非唯一约束的写库失败不应报成冲突，实际 %v", err)
	}
	tasks, err := env.store.ListTasks(ctx, req.ID)
	if err != nil {
		t.Fatalf("查询任务列表失败: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("回滚后不应残留任务，实际 %d 条", len(tasks))
	}
	got, err := env.store.GetRequirement(ctx, req.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqDraft {
		t.Fatalf("回滚后需求状态应为 draft，实际 %q", got.Status)
	}
}

// TestGapStartGenerationWhenStatusUpdateFails 覆盖需求状态更新失败时的失败。
// 业务含义：状态写不进去时整笔发起必须回滚，不得留下「有任务但需求还是草稿」的脏状态。
func TestGapStartGenerationWhenStatusUpdateFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	gapInject(t, env, gapFailUpdate("requirements"))

	_, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-status")
	if err == nil {
		t.Fatal("需求状态更新失败时必须报错")
	}
	tasks, err := env.store.ListTasks(ctx, req.ID)
	if err != nil {
		t.Fatalf("查询任务列表失败: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("回滚后不应残留任务，实际 %d 条", len(tasks))
	}
	got, err := env.store.GetRequirement(ctx, req.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqDraft {
		t.Fatalf("回滚后需求状态应为 draft，实际 %q", got.Status)
	}
}

// TestGapStartGenerationWhenAuditFails 覆盖发起生成的审计写入失败。
// 业务含义：审计写入失败必须让整笔发起回滚，不得出现「任务已提交供应商但需求仍是草稿」。
func TestGapStartGenerationWhenAuditFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	gapInject(t, env, gapFailInsert("events"))

	_, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-audit")
	if err == nil {
		t.Fatal("审计写入失败时必须报错")
	}
	tasks, err := env.store.ListTasks(ctx, req.ID)
	if err != nil {
		t.Fatalf("查询任务列表失败: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("回滚后不应残留任务，实际 %d 条", len(tasks))
	}
	got, err := env.store.GetRequirement(ctx, req.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqDraft {
		t.Fatalf("回滚后需求状态应为 draft，实际 %q", got.Status)
	}
	if env.supplier.callCount() != 0 {
		t.Fatalf("回滚后不应提交供应商，实际调用 %d 次", env.supplier.callCount())
	}
}

// TestGapSupplierRejectedWithoutReasonUsesDefault 覆盖供应商拒绝且未给出原因时的默认文案。
// 业务含义：供应商只说「不接受」而没说原因时，系统必须给出可读的默认原因，
// 否则运营看到的是一条没有原因的空异常。
func TestGapSupplierRejectedWithoutReasonUsesDefault(t *testing.T) {
	env := gapEnvWithSupplier(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"accepted":false}`))
	})
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")

	_, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-noreason")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("供应商拒绝时应返回 ErrConflict，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "供应商拒绝受理本次生成") {
		t.Fatalf("拒绝原因应回落为默认文案，实际 %v", err)
	}
	tasks, err := env.store.ListTasks(ctx, req.ID)
	if err != nil {
		t.Fatalf("查询任务列表失败: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != domain.TaskRejected {
		t.Fatalf("任务应记为被拒绝，实际 %+v", tasks)
	}
	got, err := env.store.GetRequirement(ctx, req.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqFailed {
		t.Fatalf("被拒绝后需求状态应为 failed，实际 %q", got.Status)
	}
}

// TestGapSubmitToSupplierWhenTaskUpdateFails 覆盖供应商受理后任务写回失败。
// 业务含义：供应商已经受理，但本地任务写不回供应商任务号时必须报错，
// 否则本地永远等不到回调（任务停留在 processing 且缺少关联键）。
func TestGapSubmitToSupplierWhenTaskUpdateFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	gapInject(t, env, gapFailUpdate("generation_tasks"))

	_, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-update")
	if err == nil {
		t.Fatal("任务写回失败时必须报错")
	}
	if !strings.Contains(err.Error(), "更新生成任务失败") {
		t.Fatalf("错误信息应说明是更新生成任务失败，实际 %v", err)
	}
	tasks, err := env.store.ListTasks(ctx, req.ID)
	if err != nil {
		t.Fatalf("查询任务列表失败: %v", err)
	}
	if len(tasks) != 1 || tasks[0].SupplierTaskID != "" {
		t.Fatalf("供应商任务号不应被写入，实际 %+v", tasks)
	}
}

// TestGapFinalizeRejectedWhenTaskUpdateFails 覆盖被拒收任务写回失败。
// 业务含义：供应商拒绝后任务状态写不回去时，仍必须以「提交失败」收口并返回冲突，
// 不能把失败吞掉当成提交成功。
func TestGapFinalizeRejectedWhenTaskUpdateFails(t *testing.T) {
	env := gapEnvWithSupplier(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"accepted":false,"reason":"供应商配额不足"}`))
	})
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	gapInject(t, env, gapFailUpdate("generation_tasks"))

	_, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-reject")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("供应商拒绝时应返回 ErrConflict，实际 %v", err)
	}
	tasks, err := env.store.ListTasks(ctx, req.ID)
	if err != nil {
		t.Fatalf("查询任务列表失败: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != domain.TaskProcessing {
		t.Fatalf("任务状态写回失败时应保持处理中，实际 %+v", tasks)
	}
}

// TestGapFinalizeRejectedWhenRequirementUnavailable 覆盖被拒收时需求不可读。
// 业务含义：供应商拒绝且本地需求记录已不可读时，仍必须以「提交失败」收口（冲突），
// 不能把系统故障伪装成提交成功。
func TestGapFinalizeRejectedWhenRequirementUnavailable(t *testing.T) {
	var st *store.Store
	env := gapEnvWithSupplier(t, func(w http.ResponseWriter, r *http.Request) {
		if _, err := st.DB().Exec("DROP TABLE requirements"); err != nil {
			t.Errorf("破坏需求表失败: %v", err)
		}
		_, _ = w.Write([]byte(`{"accepted":false,"reason":"供应商配额不足"}`))
	})
	st = env.store
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")

	_, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-noreq")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("提交失败应收口为 ErrConflict，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "供应商配额不足") {
		t.Fatalf("错误信息应保留供应商给出的原因，实际 %v", err)
	}
}

// TestGapFinalizeRejectedWhenStatusUpdateFails 覆盖被拒收后需求状态写回失败。
// 业务含义：拒绝收口写不回需求状态时必须报错，且需求状态保持原样（事务回滚）。
func TestGapFinalizeRejectedWhenStatusUpdateFails(t *testing.T) {
	var st *store.Store
	env := gapEnvWithSupplier(t, func(w http.ResponseWriter, r *http.Request) {
		if _, err := st.DB().Exec(gapFailUpdate("requirements")); err != nil {
			t.Errorf("注入需求状态写回故障失败: %v", err)
		}
		_, _ = w.Write([]byte(`{"accepted":false,"reason":"供应商配额不足"}`))
	})
	st = env.store
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")

	_, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-reqstatus")
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("提交失败应收口为 ErrConflict，实际 %v", err)
	}
	if _, err := st.GetRequirement(ctx, req.ID); err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	got, err := st.GetRequirement(ctx, req.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqGenerating {
		t.Fatalf("状态写回失败后需求状态不应改变，实际 %q", got.Status)
	}
}

// TestGapHandleCallbackUnknownTaskWhenAuditFails 覆盖未知任务回调的审计写入失败。
// 业务含义：回调关联不到已知任务时必须留下可查的异常流水；
// 连流水都写不进去时必须报错，不能静默丢弃这次回调。
func TestGapHandleCallbackUnknownTaskWhenAuditFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	gapInject(t, env, "DROP TABLE events")

	result, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: "sup-unknown", Status: "succeeded", ImageURL: "/static/img/a.svg",
	})
	if err == nil {
		t.Fatal("异常流水写入失败时必须报错")
	}
	if result != nil {
		t.Fatalf("处理失败时不应返回结果对象，实际 %+v", result)
	}
}

// TestGapHandleCallbackWhenTaskTableUnavailable 覆盖任务表不可读时的回调处理。
// 业务含义：回调关联任务失败属于系统故障，必须报错并让调用方感知，
// 不能当成「关联不到已知任务」静默忽略。
func TestGapHandleCallbackWhenTaskTableUnavailable(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	gapInject(t, env, "DROP TABLE generation_tasks")

	_, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: "sup-x", Status: "succeeded", ImageURL: "/static/img/a.svg",
	})
	if err == nil {
		t.Fatal("任务表不可读时回调处理必须失败")
	}
	if !strings.Contains(err.Error(), "读取生成任务失败") {
		t.Fatalf("错误信息应说明是读取生成任务失败，实际 %v", err)
	}
}

// TestGapHandleCallbackOnTerminalTaskWhenAuditFails 覆盖终态任务回调的审计写入失败。
// 业务含义：终态任务收到冲突/重复回调必须留痕；留痕失败必须报错，
// 不能让「重复回调已忽略」在没有痕迹的情况下发生。
func TestGapHandleCallbackOnTerminalTaskWhenAuditFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, supplierTask := gapSeedProcessingTask(t, env)
	if _, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "failed", Reason: "供应商失败",
	}); err != nil {
		t.Fatalf("处理失败回调失败: %v", err)
	}
	gapInject(t, env, gapFailInsert("events"))

	_, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "succeeded", ImageURL: "/static/img/a.svg",
	})
	if err == nil {
		t.Fatal("冲突回调留痕失败时必须报错")
	}
	tasks, err := env.store.ListTasks(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("查询任务列表失败: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != domain.TaskFailed {
		t.Fatalf("冲突回调不应改写终态任务，实际 %+v", tasks)
	}
}

// TestGapHandleCallbackWhenRequirementUnavailable 覆盖回调时需求不可读。
// 业务含义：回调已定位到任务、但需求不可读时必须报错，
// 不能在没有需求上下文的情况下改写任务状态。
func TestGapHandleCallbackWhenRequirementUnavailable(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	_, supplierTask := gapSeedProcessingTask(t, env)
	gapInject(t, env, "DROP TABLE requirements")

	_, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "succeeded", ImageURL: "/static/img/a.svg",
	})
	if err == nil {
		t.Fatal("需求不可读时回调处理必须失败")
	}
	if !strings.Contains(err.Error(), "读取需求失败") {
		t.Fatalf("错误信息应说明是读取需求失败，实际 %v", err)
	}
}

// TestGapHandleCallbackFailureWhenTaskUpdateFails 覆盖失败回调中的任务写回失败。
// 业务含义：失败结果写不回任务时必须报错，且需求状态不得被改写（事务回滚）。
func TestGapHandleCallbackFailureWhenTaskUpdateFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, supplierTask := gapSeedProcessingTask(t, env)
	gapInject(t, env, gapFailUpdate("generation_tasks"))

	_, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "failed", Reason: "供应商失败",
	})
	if err == nil {
		t.Fatal("任务写回失败时回调处理必须失败")
	}
	got, err := env.store.GetRequirement(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqGenerating {
		t.Fatalf("回滚后需求状态不应改变，实际 %q", got.Status)
	}
}

// TestGapHandleCallbackFailureWhenRequirementUpdateFails 覆盖失败回调中的需求状态写回失败。
// 业务含义：失败结果无法写回需求状态时必须报错，任务状态也不得被改写（事务回滚）。
func TestGapHandleCallbackFailureWhenRequirementUpdateFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, supplierTask := gapSeedProcessingTask(t, env)
	gapInject(t, env, gapFailUpdate("requirements"))

	_, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "failed", Reason: "供应商失败",
	})
	if err == nil {
		t.Fatal("需求状态写回失败时回调处理必须失败")
	}
	tasks, err := env.store.ListTasks(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("查询任务列表失败: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != domain.TaskProcessing {
		t.Fatalf("回滚后任务状态不应改变，实际 %+v", tasks)
	}
}

// TestGapHandleCallbackFailureWhenAuditFails 覆盖失败回调中的审计写入失败。
// 业务含义：失败结果必须留痕（生成失败事件），留痕失败时整笔处理必须回滚。
func TestGapHandleCallbackFailureWhenAuditFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, supplierTask := gapSeedProcessingTask(t, env)
	gapInject(t, env, gapFailEventOnKind(domain.EventGenerationFailed))

	_, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "failed", Reason: "供应商失败",
	})
	if err == nil {
		t.Fatal("失败留痕写入失败时回调处理必须失败")
	}
	got, err := env.store.GetRequirement(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqGenerating {
		t.Fatalf("回滚后需求状态不应改变，实际 %q", got.Status)
	}
}

// TestGapLateFailureResultWhenAuditFails 覆盖撤销后迟到失败结果的留痕失败。
// 业务含义：撤销后的迟到失败结果只保留审计；连审计都写不进去时必须报错，
// 且撤销状态绝不能被改回 failed。
func TestGapLateFailureResultWhenAuditFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, supplierTask := gapSeedProcessingTask(t, env)
	if err := env.svc.Withdraw(ctx, env.requester, requirement.ID); err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	gapInject(t, env, gapFailEventOnKind(domain.EventLateResultIgnored))

	_, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "failed", Reason: "供应商失败",
	})
	if err == nil {
		t.Fatal("迟到结果留痕失败时回调处理必须失败")
	}
	got, err := env.store.GetRequirement(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqWithdrawn {
		t.Fatalf("撤销状态不得被改写，实际 %q", got.Status)
	}
}

// TestGapHandleCallbackSuccessWhenTaskUpdateFails 覆盖成功回调中的任务写回失败。
// 业务含义：成功结果写不回任务时必须报错，需求不得进入待复核、也不得产生候选。
func TestGapHandleCallbackSuccessWhenTaskUpdateFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, supplierTask := gapSeedProcessingTask(t, env)
	gapInject(t, env, gapFailUpdate("generation_tasks"))

	_, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "succeeded", ImageURL: "/static/img/a.svg",
	})
	if err == nil {
		t.Fatal("任务写回失败时回调处理必须失败")
	}
	got, err := env.store.GetRequirement(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqGenerating {
		t.Fatalf("回滚后需求状态不应改变，实际 %q", got.Status)
	}
	cands, err := env.store.ListCandidates(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("查询候选列表失败: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("回滚后不应产生候选，实际 %d 个", len(cands))
	}
}

// TestGapLateSuccessResultWhenAuditFails 覆盖撤销后迟到成功结果的留痕失败。
// 业务含义：撤销后的迟到成功结果只保留审计、不形成交付；
// 留痕失败时必须报错，且撤销状态与候选列表都不受影响。
func TestGapLateSuccessResultWhenAuditFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, supplierTask := gapSeedProcessingTask(t, env)
	if err := env.svc.Withdraw(ctx, env.requester, requirement.ID); err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	gapInject(t, env, gapFailEventOnKind(domain.EventLateResultIgnored))

	_, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "succeeded", ImageURL: "/static/img/a.svg",
	})
	if err == nil {
		t.Fatal("迟到结果留痕失败时回调处理必须失败")
	}
	got, err := env.store.GetRequirement(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqWithdrawn {
		t.Fatalf("撤销状态不得被改写，实际 %q", got.Status)
	}
	cands, err := env.store.ListCandidates(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("查询候选列表失败: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("撤销后的迟到结果不应产生候选，实际 %d 个", len(cands))
	}
}

// TestGapHandleCallbackSuccessWhenCandidatesUpdateFails 覆盖成功回调中的旧候选作废失败。
// 业务含义：上一版候选必须作废才能写入新候选；作废失败时必须报错，
// 不能在保留旧「当前候选」的情况下再插入新候选。
func TestGapHandleCallbackSuccessWhenCandidatesUpdateFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, supplierTask := gapSeedSecondGeneration(t, env)
	gapInject(t, env, gapFailUpdate("candidates"))

	_, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "succeeded", ImageURL: "/static/img/b.svg",
	})
	if err == nil {
		t.Fatal("旧候选作废失败时回调处理必须失败")
	}
	cands, err := env.store.ListCandidates(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("查询候选列表失败: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("回滚后候选数量不应改变，实际 %d 个", len(cands))
	}
	if !cands[0].IsCurrent {
		t.Fatal("回滚后上一版候选应仍为当前候选")
	}
}

// TestGapHandleCallbackSuccessWhenCandidateInsertFails 覆盖成功回调中的候选写入失败。
// 业务含义：候选写不进去时必须报错，需求不得进入待复核状态（事务回滚）。
func TestGapHandleCallbackSuccessWhenCandidateInsertFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, supplierTask := gapSeedProcessingTask(t, env)
	gapInject(t, env, gapFailInsert("candidates"))

	_, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "succeeded", ImageURL: "/static/img/a.svg",
	})
	if err == nil {
		t.Fatal("候选写入失败时回调处理必须失败")
	}
	got, err := env.store.GetRequirement(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqGenerating {
		t.Fatalf("回滚后需求状态不应改变，实际 %q", got.Status)
	}
}

// TestGapHandleCallbackSuccessWhenRequirementUpdateFails 覆盖成功回调中的需求状态写回失败。
// 业务含义：需求状态写不回去时必须报错，且候选不得残留（事务回滚）。
func TestGapHandleCallbackSuccessWhenRequirementUpdateFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, supplierTask := gapSeedProcessingTask(t, env)
	gapInject(t, env, gapFailUpdate("requirements"))

	_, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "succeeded", ImageURL: "/static/img/a.svg",
	})
	if err == nil {
		t.Fatal("需求状态写回失败时回调处理必须失败")
	}
	cands, err := env.store.ListCandidates(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("查询候选列表失败: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("回滚后不应残留候选，实际 %d 个", len(cands))
	}
}

// TestGapHandleCallbackSuccessWhenAuditFails 覆盖成功回调中的「候选已生成」留痕失败。
// 业务含义：候选生成必须留痕，留痕失败时整笔处理回滚，不得留下无痕候选。
func TestGapHandleCallbackSuccessWhenAuditFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, supplierTask := gapSeedProcessingTask(t, env)
	gapInject(t, env, gapFailEventOnKind(domain.EventCandidateCreated))

	_, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "succeeded", ImageURL: "/static/img/a.svg",
	})
	if err == nil {
		t.Fatal("候选留痕失败时回调处理必须失败")
	}
	cands, err := env.store.ListCandidates(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("查询候选列表失败: %v", err)
	}
	if len(cands) != 0 {
		t.Fatalf("回滚后不应残留候选，实际 %d 个", len(cands))
	}
}

// TestGapHandleCallbackSuccessWhenAcceptedAuditFails 覆盖成功回调中的「回调已受理」留痕失败。
// 业务含义：成功回调必须留「已受理」流水；该流水写不进去时必须报错并回滚。
func TestGapHandleCallbackSuccessWhenAcceptedAuditFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, supplierTask := gapSeedProcessingTask(t, env)
	gapInject(t, env, gapFailEventOnKind(domain.EventCallbackAccepted))

	_, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "succeeded", ImageURL: "/static/img/a.svg",
	})
	if err == nil {
		t.Fatal("受理留痕失败时回调处理必须失败")
	}
	got, err := env.store.GetRequirement(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqGenerating {
		t.Fatalf("回滚后需求状态不应改变，实际 %q", got.Status)
	}
}

// TestGapSweepTimeoutWhenTaskUpdateFails 覆盖超时判定中的任务写回失败。
// 业务含义：超时判定写不回任务时必须报错，且任务不得被判为超时（事务回滚）。
func TestGapSweepTimeoutWhenTaskUpdateFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, _ := gapSeedOldProcessingTask(t, env)
	gapInject(t, env, gapFailUpdate("generation_tasks"))

	timedOut, err := env.svc.SweepTimeouts(ctx)
	if err == nil {
		t.Fatal("任务写回失败时巡检必须报错")
	}
	if len(timedOut) != 0 {
		t.Fatalf("判定失败时不应返回超时任务，实际 %v", timedOut)
	}
	tasks, err := env.store.ListTasks(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("查询任务列表失败: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != domain.TaskProcessing {
		t.Fatalf("回滚后任务状态不应改变，实际 %+v", tasks)
	}
}

// TestGapSweepTimeoutWhenRequirementUnavailable 覆盖超时判定中的需求不可读。
// 业务含义：超时判定必须结合需求状态，需求不可读时必须报错，
// 不得把任务直接判为超时。
func TestGapSweepTimeoutWhenRequirementUnavailable(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	gapSeedOldProcessingTask(t, env)
	gapInject(t, env, "DROP TABLE requirements")

	timedOut, err := env.svc.SweepTimeouts(ctx)
	if err == nil {
		t.Fatal("需求不可读时巡检必须报错")
	}
	if len(timedOut) != 0 {
		t.Fatalf("判定失败时不应返回超时任务，实际 %v", timedOut)
	}
}

// TestGapSweepTimeoutWhenRequirementUpdateFails 覆盖超时判定中的需求状态写回失败。
// 业务含义：超时后需求状态写不回去时必须报错，任务状态也不得被判为超时（事务回滚）。
func TestGapSweepTimeoutWhenRequirementUpdateFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, _ := gapSeedOldProcessingTask(t, env)
	gapInject(t, env, gapFailUpdate("requirements"))

	timedOut, err := env.svc.SweepTimeouts(ctx)
	if err == nil {
		t.Fatal("需求状态写回失败时巡检必须报错")
	}
	if len(timedOut) != 0 {
		t.Fatalf("判定失败时不应返回超时任务，实际 %v", timedOut)
	}
	tasks, err := env.store.ListTasks(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("查询任务列表失败: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != domain.TaskProcessing {
		t.Fatalf("回滚后任务状态不应改变，实际 %+v", tasks)
	}
}

// TestGapSweepTimeoutWhenTimeoutAuditFails 覆盖超时判定中的超时留痕失败。
// 业务含义：超时必须留痕，留痕失败时必须报错，且不得把任务判为超时（事务回滚）。
func TestGapSweepTimeoutWhenTimeoutAuditFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, _ := gapSeedOldProcessingTask(t, env)
	gapInject(t, env, gapFailEventOnKind(domain.EventGenerationTimeout))

	timedOut, err := env.svc.SweepTimeouts(ctx)
	if err == nil {
		t.Fatal("超时留痕失败时巡检必须报错")
	}
	if len(timedOut) != 0 {
		t.Fatalf("判定失败时不应返回超时任务，实际 %v", timedOut)
	}
	tasks, err := env.store.ListTasks(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("查询任务列表失败: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != domain.TaskProcessing {
		t.Fatalf("回滚后任务状态不应改变，实际 %+v", tasks)
	}
}

// TestGapSweepTimeoutOnWithdrawnRequirementWhenAuditFails 覆盖撤销需求的超时留痕失败。
// 业务含义：撤销需求的迟到超时只保留审计、绝不恢复需求；
// 审计写不进去时必须报错，且撤销状态不得被改写。
func TestGapSweepTimeoutOnWithdrawnRequirementWhenAuditFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, _ := gapSeedOldProcessingTask(t, env)
	if err := env.svc.Withdraw(ctx, env.requester, requirement.ID); err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	gapInject(t, env, gapFailEventOnKind(domain.EventLateResultIgnored))

	timedOut, err := env.svc.SweepTimeouts(ctx)
	if err == nil {
		t.Fatal("迟到超时留痕失败时巡检必须报错")
	}
	if len(timedOut) != 0 {
		t.Fatalf("判定失败时不应返回超时任务，实际 %v", timedOut)
	}
	got, err := env.store.GetRequirement(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqWithdrawn {
		t.Fatalf("撤销状态不得被改写，实际 %q", got.Status)
	}
}

// TestGapStartTimeoutSweeperWithInvalidInterval 覆盖巡检间隔非法时不启动后台协程。
// 业务含义：间隔 <= 0 时不得启动巡检协程，否则会变成空转的忙循环。
func TestGapStartTimeoutSweeperWithInvalidInterval(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requirement, _ := gapSeedOldProcessingTask(t, env)

	env.svc.StartTimeoutSweeper(ctx, 0)
	time.Sleep(200 * time.Millisecond)

	tasks, err := env.store.ListTasks(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("查询任务列表失败: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Status != domain.TaskProcessing {
		t.Fatalf("非正间隔不应启动巡检，任务状态不应改变，实际 %+v", tasks)
	}
	got, err := env.store.GetRequirement(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqGenerating {
		t.Fatalf("非正间隔不应启动巡检，需求状态不应改变，实际 %q", got.Status)
	}
}

// TestGapReviewCandidateWhenRequirementUnavailable 覆盖复核时需求不可读。
// 业务含义：复核必须基于需求状态判定，需求不可读时必须报错。
func TestGapReviewCandidateWhenRequirementUnavailable(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, cand := gapSeedReviewable(t, env)
	gapInject(t, env, "DROP TABLE requirements")

	_, err := env.svc.ReviewCandidate(ctx, env.reviewer, requirement.ID, cand.ID, true, "复核通过")
	if err == nil {
		t.Fatal("需求不可读时复核必须失败")
	}
	if !strings.Contains(err.Error(), "读取需求失败") {
		t.Fatalf("错误信息应说明是读取需求失败，实际 %v", err)
	}
}

// TestGapReviewCandidateWhenCandidateTableUnavailable 覆盖复核时候选表不可读。
// 业务含义：指定候选复核时读不到候选属于系统故障，必须报错，
// 不能当成「候选不存在」返回 404。
func TestGapReviewCandidateWhenCandidateTableUnavailable(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, cand := gapSeedReviewable(t, env)
	gapInject(t, env, "DROP TABLE candidates")

	_, err := env.svc.ReviewCandidate(ctx, env.reviewer, requirement.ID, cand.ID, true, "复核通过")
	if err == nil {
		t.Fatal("候选表不可读时复核必须失败")
	}
	if errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("候选表不可读属于系统故障，不应报成候选不存在，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "读取候选失败") {
		t.Fatalf("错误信息应说明是读取候选失败，实际 %v", err)
	}
}

// TestGapReviewCurrentCandidateWhenCandidateTableUnavailable 覆盖复核当前候选时候选表不可读。
// 业务含义：不指定候选编号时读不到当前候选属于系统故障，必须报错。
func TestGapReviewCurrentCandidateWhenCandidateTableUnavailable(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, _ := gapSeedReviewable(t, env)
	gapInject(t, env, "DROP TABLE candidates")

	_, err := env.svc.ReviewCandidate(ctx, env.reviewer, requirement.ID, "", true, "复核通过")
	if err == nil {
		t.Fatal("候选表不可读时复核必须失败")
	}
	if errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("候选表不可读属于系统故障，不应报成没有待复核候选，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "读取当前候选失败") {
		t.Fatalf("错误信息应说明是读取当前候选失败，实际 %v", err)
	}
}

// TestGapReviewCandidateWhenReviewWriteFails 覆盖复核结论写入失败。
// 业务含义：复核结论写不回去时必须报错，候选必须仍是待复核（事务回滚）。
func TestGapReviewCandidateWhenReviewWriteFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, cand := gapSeedReviewable(t, env)
	gapInject(t, env, gapFailUpdate("candidates"))

	_, err := env.svc.ReviewCandidate(ctx, env.reviewer, requirement.ID, cand.ID, true, "复核通过")
	if err == nil {
		t.Fatal("复核结论写入失败时必须报错")
	}
	got, err := env.store.GetCandidate(ctx, cand.ID)
	if err != nil {
		t.Fatalf("读取候选失败: %v", err)
	}
	if got.ReviewStatus != domain.ReviewPending {
		t.Fatalf("回滚后候选应仍待复核，实际 %q", got.ReviewStatus)
	}
	gotReq, err := env.store.GetRequirement(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if gotReq.Status != domain.ReqCandidatePending {
		t.Fatalf("回滚后需求状态不应改变，实际 %q", gotReq.Status)
	}
}

// TestGapReviewCandidateWhenRequirementUpdateFails 覆盖复核后的需求状态写回失败。
// 业务含义：需求状态写不回去时必须报错，候选复核结论也不得残留（事务回滚）。
func TestGapReviewCandidateWhenRequirementUpdateFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, cand := gapSeedReviewable(t, env)
	gapInject(t, env, gapFailUpdate("requirements"))

	_, err := env.svc.ReviewCandidate(ctx, env.reviewer, requirement.ID, cand.ID, true, "复核通过")
	if err == nil {
		t.Fatal("需求状态写回失败时必须报错")
	}
	got, err := env.store.GetCandidate(ctx, cand.ID)
	if err != nil {
		t.Fatalf("读取候选失败: %v", err)
	}
	if got.ReviewStatus != domain.ReviewPending {
		t.Fatalf("回滚后候选应仍待复核，实际 %q", got.ReviewStatus)
	}
}

// TestGapReviewCandidateWhenAuditFails 覆盖复核留痕失败。
// 业务含义：复核必须留痕，留痕失败时必须报错，候选结论不得残留（事务回滚）。
func TestGapReviewCandidateWhenAuditFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, cand := gapSeedReviewable(t, env)
	gapInject(t, env, gapFailEventOnKind(domain.EventCandidateReviewed))

	_, err := env.svc.ReviewCandidate(ctx, env.reviewer, requirement.ID, cand.ID, true, "复核通过")
	if err == nil {
		t.Fatal("复核留痕失败时必须报错")
	}
	got, err := env.store.GetCandidate(ctx, cand.ID)
	if err != nil {
		t.Fatalf("读取候选失败: %v", err)
	}
	if got.ReviewStatus != domain.ReviewPending {
		t.Fatalf("回滚后候选应仍待复核，实际 %q", got.ReviewStatus)
	}
}

// TestGapAdoptWithHistoricalCandidate 覆盖确认采用历史候选的拒绝。
// 业务含义：题面 R6 规定上一版候选只作历史，不能被确认采用，
// 必须明确返回 ErrNotCurrentCandidate。
func TestGapAdoptWithHistoricalCandidate(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, firstCand := gapSeedApproved(t, env)
	if _, err := env.svc.StartGeneration(ctx, env.requester, requirement.ID, "gap-second"); err != nil {
		t.Fatalf("再次发起生成失败: %v", err)
	}
	if _, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: env.supplier.taskID(), Status: "succeeded", ImageURL: "/static/img/b.svg",
	}); err != nil {
		t.Fatalf("处理成功回调失败: %v", err)
	}
	stale, err := env.store.GetCandidate(ctx, firstCand.ID)
	if err != nil {
		t.Fatalf("读取历史候选失败: %v", err)
	}
	if stale.IsCurrent {
		t.Fatal("上一版候选应已作废")
	}

	_, err = env.svc.Adopt(ctx, env.requester, requirement.ID, firstCand.ID)
	if !errors.Is(err, domain.ErrNotCurrentCandidate) {
		t.Fatalf("确认采用历史候选应返回 ErrNotCurrentCandidate，实际 %v", err)
	}
}

// TestGapAdoptWhenCandidateTableUnavailable 覆盖确认采用时候选表不可读。
// 业务含义：指定候选确认采用时读不到候选属于系统故障，必须报错。
func TestGapAdoptWhenCandidateTableUnavailable(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, cand := gapSeedApproved(t, env)
	gapInject(t, env, "DROP TABLE candidates")

	_, err := env.svc.Adopt(ctx, env.requester, requirement.ID, cand.ID)
	if err == nil {
		t.Fatal("候选表不可读时确认采用必须失败")
	}
	if errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("候选表不可读属于系统故障，不应报成候选不存在，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "读取候选失败") {
		t.Fatalf("错误信息应说明是读取候选失败，实际 %v", err)
	}
}

// TestGapAdoptWhenRequirementUpdateFails 覆盖确认采用时的需求状态写回失败。
// 业务含义：交付状态写不回去时必须报错，需求不得变成已交付（事务回滚）。
func TestGapAdoptWhenRequirementUpdateFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, cand := gapSeedApproved(t, env)
	gapInject(t, env, gapFailUpdate("requirements"))

	_, err := env.svc.Adopt(ctx, env.requester, requirement.ID, cand.ID)
	if err == nil {
		t.Fatal("交付状态写回失败时必须报错")
	}
	got, err := env.store.GetRequirement(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqApproved {
		t.Fatalf("回滚后需求状态不应改变，实际 %q", got.Status)
	}
}

// TestGapAdoptWhenAuditFails 覆盖确认采用的留痕失败。
// 业务含义：确认采用必须留痕，留痕失败时必须报错，需求不得变成已交付（事务回滚）。
func TestGapAdoptWhenAuditFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, cand := gapSeedApproved(t, env)
	gapInject(t, env, gapFailEventOnKind(domain.EventAdopted))

	_, err := env.svc.Adopt(ctx, env.requester, requirement.ID, cand.ID)
	if err == nil {
		t.Fatal("交付留痕失败时必须报错")
	}
	got, err := env.store.GetRequirement(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqApproved {
		t.Fatalf("回滚后需求状态不应改变，实际 %q", got.Status)
	}
}

// TestGapRequestReworkWhenRequirementUpdateFails 覆盖提出重做时的状态写回失败。
// 业务含义：重做状态写不回去时必须报错，需求仍应是复核通过（事务回滚）。
func TestGapRequestReworkWhenRequirementUpdateFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement, _ := gapSeedApproved(t, env)
	gapInject(t, env, gapFailUpdate("requirements"))

	err := env.svc.RequestRework(ctx, env.requester, requirement.ID, "图片不符合文案")
	if err == nil {
		t.Fatal("重做状态写回失败时必须报错")
	}
	got, err := env.store.GetRequirement(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqApproved {
		t.Fatalf("回滚后需求状态不应改变，实际 %q", got.Status)
	}
}

// TestGapWithdrawWhenRequirementUpdateFails 覆盖撤销时的状态写回失败。
// 业务含义：撤销状态写不回去时必须报错，需求不得变成已撤销（事务回滚）。
func TestGapWithdrawWhenRequirementUpdateFails(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement := env.newRequirement(t, "初始描述文本")
	gapInject(t, env, gapFailUpdate("requirements"))

	err := env.svc.Withdraw(ctx, env.requester, requirement.ID)
	if err == nil {
		t.Fatal("撤销状态写回失败时必须报错")
	}
	got, err := env.store.GetRequirement(ctx, requirement.ID)
	if err != nil {
		t.Fatalf("读取需求失败: %v", err)
	}
	if got.Status != domain.ReqDraft {
		t.Fatalf("回滚后需求状态不应改变，实际 %q", got.Status)
	}
}

// TestGapRequirementDetailWhenTaskTableUnavailable 覆盖需求详情中的任务读取失败。
// 业务含义：需求详情必须完整，任务读不出来时必须报错，不能给出一份缺任务的不完整视图。
func TestGapRequirementDetailWhenTaskTableUnavailable(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement := env.newRequirement(t, "初始描述文本")
	gapInject(t, env, "DROP TABLE generation_tasks")

	_, err := env.svc.RequirementDetail(ctx, env.requester, requirement.ID)
	if err == nil {
		t.Fatal("任务不可读时需求详情必须失败")
	}
	if !strings.Contains(err.Error(), "查询生成任务失败") {
		t.Fatalf("错误信息应说明是查询生成任务失败，实际 %v", err)
	}
}

// TestGapRequirementDetailWhenCandidateTableUnavailable 覆盖需求详情中的候选读取失败。
// 业务含义：需求详情必须包含候选，候选读不出来时必须报错。
func TestGapRequirementDetailWhenCandidateTableUnavailable(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement := env.newRequirement(t, "初始描述文本")
	gapInject(t, env, "DROP TABLE candidates")

	_, err := env.svc.RequirementDetail(ctx, env.requester, requirement.ID)
	if err == nil {
		t.Fatal("候选不可读时需求详情必须失败")
	}
	if !strings.Contains(err.Error(), "查询候选列表失败") {
		t.Fatalf("错误信息应说明是查询候选列表失败，实际 %v", err)
	}
}

// TestGapRequirementDetailWhenEventTableUnavailable 覆盖需求详情中的审计读取失败。
// 业务含义：需求详情必须包含审计流水，审计读不出来时必须报错。
func TestGapRequirementDetailWhenEventTableUnavailable(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	requirement := env.newRequirement(t, "初始描述文本")
	gapInject(t, env, "DROP TABLE events")

	_, err := env.svc.RequirementDetail(ctx, env.requester, requirement.ID)
	if err == nil {
		t.Fatal("审计不可读时需求详情必须失败")
	}
	if !strings.Contains(err.Error(), "查询审计事件失败") {
		t.Fatalf("错误信息应说明是查询审计事件失败，实际 %v", err)
	}
}

// TestGapOverviewCountsTasks 覆盖运营总览中的任务状态统计。
// 业务含义：总览必须按任务状态给出计数，供运营判断积压量。
func TestGapOverviewCountsTasks(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	gapSeedProcessingTask(t, env)

	overview, err := env.svc.Overview(ctx, env.operator)
	if err != nil {
		t.Fatalf("查询运营总览失败: %v", err)
	}
	if overview.TaskCounts[domain.TaskProcessing] != 1 {
		t.Fatalf("处理中任务计数应为 1，实际 %+v", overview.TaskCounts)
	}
	if overview.RequirementCounts[domain.ReqGenerating] != 1 {
		t.Fatalf("生成中需求计数应为 1，实际 %+v", overview.RequirementCounts)
	}
}

// TestGapOverviewWhenTaskTableUnavailable 覆盖运营总览中的任务读取失败。
// 业务含义：总览的任务统计读不出来时必须报错，不能给出残缺的总览。
func TestGapOverviewWhenTaskTableUnavailable(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	env.newRequirement(t, "初始描述文本")
	gapInject(t, env, "DROP TABLE generation_tasks")

	_, err := env.svc.Overview(ctx, env.operator)
	if err == nil {
		t.Fatal("任务不可读时运营总览必须失败")
	}
	if !strings.Contains(err.Error(), "查询生成任务失败") {
		t.Fatalf("错误信息应说明是查询生成任务失败，实际 %v", err)
	}
}

// TestGapOverviewWhenEventTableUnavailable 覆盖运营总览中的异常统计失败。
// 业务含义：总览必须给出异常计数，异常读不出来时必须报错。
func TestGapOverviewWhenEventTableUnavailable(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	env.newRequirement(t, "初始描述文本")
	gapInject(t, env, "DROP TABLE events")

	_, err := env.svc.Overview(ctx, env.operator)
	if err == nil {
		t.Fatal("审计不可读时运营总览必须失败")
	}
	if !strings.Contains(err.Error(), "查询异常事件失败") {
		t.Fatalf("错误信息应说明是查询异常事件失败，实际 %v", err)
	}
}
