package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"creative-studio/internal/domain"
	"creative-studio/internal/store"
)

// fakeSupplier 是可控的模拟供应商，用于驱动业务层测试。
type fakeSupplier struct {
	mu       sync.Mutex
	mode     string // accept / reject / error
	calls    int
	prompts  []string
	server   *httptest.Server
	lastTask string
}

func newFakeSupplier(t *testing.T, mode string) *fakeSupplier {
	t.Helper()
	f := &fakeSupplier{mode: mode}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Ref    string `json:"ref"`
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.calls++
		f.prompts = append(f.prompts, in.Prompt)
		mode := f.mode
		f.mu.Unlock()
		switch mode {
		case "reject":
			_, _ = w.Write([]byte(`{"accepted":false,"reason":"模拟供应商拒绝受理"}`))
		case "error":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("内部错误"))
		default:
			taskID := "sup-" + in.Ref
			f.mu.Lock()
			f.lastTask = taskID
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"accepted":true,"task_id":"` + taskID + `"}`))
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeSupplier) setMode(mode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = mode
}

func (f *fakeSupplier) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeSupplier) taskID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastTask
}

// testEnv 是测试用的服务环境。
type testEnv struct {
	svc       *Service
	store     *store.Store
	supplier  *fakeSupplier
	clock     domain.Clock
	requester *domain.User
	reviewer  *domain.User
	operator  *domain.User
}

func newTestEnv(t *testing.T, supplierMode string) *testEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	fs := newFakeSupplier(t, supplierMode)
	svc := New(st, domain.SystemClock{}, Config{
		SupplierBaseURL: fs.server.URL,
		SupplierSecret:  "secret",
		SupplierTimeout: 2 * time.Second,
		ClockScale:      1,
	})
	ctx := context.Background()
	env := &testEnv{svc: svc, store: st, supplier: fs, clock: domain.SystemClock{}}
	env.requester = seedUser(t, svc, "demand-a", "需求人甲", domain.RoleRequester)
	env.reviewer = seedUser(t, svc, "review-a", "设计复核人甲", domain.RoleReviewer)
	env.operator = seedUser(t, svc, "ops-a", "运营甲", domain.RoleOperator)
	_ = ctx
	return env
}

func seedUser(t *testing.T, svc *Service, id, name string, role domain.Role) *domain.User {
	t.Helper()
	if err := svc.SeedUser(context.Background(), id, name, role, "Studio@2026"); err != nil {
		t.Fatalf("预置账号失败: %v", err)
	}
	user, err := svc.store.GetUser(context.Background(), id)
	if err != nil {
		t.Fatalf("读取账号失败: %v", err)
	}
	return user
}

// newRequirement 创建一个测试需求。
func (e *testEnv) newRequirement(t *testing.T, description string) *domain.Requirement {
	t.Helper()
	req, err := e.svc.CreateRequirement(context.Background(), e.requester, CreateRequirementInput{
		ActivityName: "夏日活动", Purpose: "新品主视觉", Description: description,
	})
	if err != nil {
		t.Fatalf("创建需求失败: %v", err)
	}
	return req
}

func TestLoginLogoutAndAuthenticate(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()

	token, user, err := env.svc.Login(ctx, "demand-a", "Studio@2026")
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	if token == "" || user.Role != domain.RoleRequester {
		t.Fatalf("登录结果不正确: %+v", user)
	}
	if _, _, err := env.svc.Login(ctx, "demand-a", "wrong"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("错误口令应返回 ErrUnauthorized，实际 %v", err)
	}
	if _, _, err := env.svc.Login(ctx, "nobody", "Studio@2026"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("未知账号应返回 ErrUnauthorized，实际 %v", err)
	}
	if _, _, err := env.svc.Login(ctx, "", ""); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("空参数应返回 ErrInvalidInput，实际 %v", err)
	}
	got, err := env.svc.Authenticate(ctx, token)
	if err != nil || got.ID != "demand-a" {
		t.Fatalf("会话识别失败: %+v（%v）", got, err)
	}
	if _, err := env.svc.Authenticate(ctx, "bad"); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("无效令牌应返回 ErrUnauthorized，实际 %v", err)
	}
	if err := env.svc.Logout(ctx, token); err != nil {
		t.Fatalf("注销失败: %v", err)
	}
	if _, err := env.svc.Authenticate(ctx, token); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("注销后令牌应失效，实际 %v", err)
	}
}

func TestCreateRequirementValidation(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()

	if _, err := env.svc.CreateRequirement(ctx, env.reviewer, CreateRequirementInput{
		ActivityName: "活动", Purpose: "用途", Description: "描述文本",
	}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("复核人创建需求应被拒绝，实际 %v", err)
	}
	if _, err := env.svc.CreateRequirement(ctx, env.requester, CreateRequirementInput{
		ActivityName: "", Purpose: "用途", Description: "描述文本",
	}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("空活动名称应被拒绝，实际 %v", err)
	}
	if _, err := env.svc.CreateRequirement(ctx, env.requester, CreateRequirementInput{
		ActivityName: "活动", Purpose: "", Description: "描述文本",
	}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("空用途应被拒绝，实际 %v", err)
	}
	if _, err := env.svc.CreateRequirement(ctx, env.requester, CreateRequirementInput{
		ActivityName: "活动", Purpose: "用途", Description: "短",
	}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("过短描述应被拒绝，实际 %v", err)
	}
	req, err := env.svc.CreateRequirement(ctx, env.requester, CreateRequirementInput{
		ActivityName: "夏日活动", Purpose: "新品主视觉", Description: "冷色调冰饮主视觉",
	})
	if err != nil {
		t.Fatalf("创建需求失败: %v", err)
	}
	if req.Status != domain.ReqDraft {
		t.Fatalf("新需求状态应为 draft，实际 %q", req.Status)
	}
	events, _ := env.store.ListEvents(ctx, req.ID)
	if len(events) == 0 || events[0].Kind != domain.EventRequirementCreated {
		t.Fatalf("创建需求应留痕，实际 %+v", events)
	}
}

func TestUpdateDescription(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")

	if err := env.svc.UpdateDescription(ctx, env.reviewer, req.ID, "改写"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("复核人改描述应被拒绝，实际 %v", err)
	}
	if err := env.svc.UpdateDescription(ctx, env.requester, req.ID, "短"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("非法描述应被拒绝，实际 %v", err)
	}
	if err := env.svc.UpdateDescription(ctx, env.requester, req.ID, "新的描述文本"); err != nil {
		t.Fatalf("修改描述失败: %v", err)
	}
	got, _ := env.store.GetRequirement(ctx, req.ID)
	if got.Description != "新的描述文本" {
		t.Fatalf("描述未更新: %+v", got)
	}
	if err := env.svc.UpdateDescription(ctx, env.requester, "missing", "新的描述文本"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("不存在的需求应返回 ErrNotFound，实际 %v", err)
	}
}

func TestStartGenerationIdempotencyAndConflict(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")

	if _, err := env.svc.StartGeneration(ctx, env.reviewer, req.ID, "r-1"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("复核人发起生成应被拒绝，实际 %v", err)
	}
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, ""); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("空请求编号应被拒绝，实际 %v", err)
	}
	task, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-1")
	if err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	if task.Status != domain.TaskProcessing || task.AttemptNo != 1 {
		t.Fatalf("任务状态不正确: %+v", task)
	}
	if task.DescriptionSnapshot != "初始描述文本" {
		t.Fatalf("描述快照不正确: %q", task.DescriptionSnapshot)
	}
	if env.supplier.callCount() != 1 {
		t.Fatalf("供应商应被调用 1 次，实际 %d", env.supplier.callCount())
	}
	// 同请求编号重复发起：不创建第二个供应商任务
	again, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-1")
	if err != nil {
		t.Fatalf("幂等重复发起不应报错: %v", err)
	}
	if again.ID != task.ID {
		t.Fatalf("幂等重复发起应返回同一任务，实际 %s 与 %s", again.ID, task.ID)
	}
	if env.supplier.callCount() != 1 {
		t.Fatalf("幂等重复发起不应再次调用供应商，实际调用 %d 次", env.supplier.callCount())
	}
	// 不同请求编号：已有处理中任务，明确冲突
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-2"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("并发发起应返回 ErrConflict，实际 %v", err)
	}
	// 需求存在即视为处理中，不能再次发起
	got, _ := env.store.GetRequirement(ctx, req.ID)
	if got.Status != domain.ReqGenerating || got.AttemptCount != 1 {
		t.Fatalf("需求状态不正确: %+v", got)
	}
}

func TestStartGenerationSupplierRejected(t *testing.T) {
	env := newTestEnv(t, "reject")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")

	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-1"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("供应商拒绝时应返回 ErrConflict，实际 %v", err)
	}
	tasks, _ := env.store.ListTasks(ctx, req.ID)
	if len(tasks) != 1 || tasks[0].Status != domain.TaskRejected {
		t.Fatalf("任务应记为被拒绝: %+v", tasks)
	}
	got, _ := env.store.GetRequirement(ctx, req.ID)
	if got.Status != domain.ReqFailed {
		t.Fatalf("需求状态应为 failed，实际 %q", got.Status)
	}
}

func TestStartGenerationSupplierError(t *testing.T) {
	env := newTestEnv(t, "error")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")

	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-1"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("供应商异常时应返回 ErrConflict，实际 %v", err)
	}
	tasks, _ := env.store.ListTasks(ctx, req.ID)
	if len(tasks) != 1 || tasks[0].Status != domain.TaskRejected {
		t.Fatalf("任务应记为被拒绝: %+v", tasks)
	}
}

func TestMaxAttempts(t *testing.T) {
	env := newTestEnv(t, "timeout")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")

	for i := 1; i <= domain.MaxAttempts; i++ {
		if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "req-"+string(rune('0'+i))); err != nil {
			t.Fatalf("第 %d 次发起失败: %v", i, err)
		}
		// 让任务落到失败状态以便再次发起
		task, _ := env.store.GetTaskByRequestNo(ctx, "req-"+string(rune('0'+i)))
		task.Status = domain.TaskFailed
		task.FinishedAt = time.Now()
		_ = env.store.UpdateTask(ctx, task)
		got, _ := env.store.GetRequirement(ctx, req.ID)
		_ = env.store.UpdateRequirementStatus(ctx, req.ID, domain.ReqFailed, task.ID, "", got.AttemptCount, time.Now())
	}
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "req-4"); !errors.Is(err, domain.ErrTooManyAttempts) {
		t.Fatalf("超过上限应返回 ErrTooManyAttempts，实际 %v", err)
	}
}

func TestHandleCallbackSuccessAndDuplicate(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	task, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-1")
	if err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	supplierTask := env.supplier.taskID()

	result, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "succeeded", ImageURL: "/static/img/a.svg",
	})
	if err != nil {
		t.Fatalf("处理回调失败: %v", err)
	}
	if !result.Applied {
		t.Fatalf("成功回调应被应用: %+v", result)
	}
	got, _ := env.store.GetRequirement(ctx, req.ID)
	if got.Status != domain.ReqCandidatePending {
		t.Fatalf("需求状态应为 candidate_pending，实际 %q", got.Status)
	}
	cand, err := env.store.GetCurrentCandidate(ctx, req.ID)
	if err != nil {
		t.Fatalf("应生成当前候选: %v", err)
	}
	if cand.AttemptNo != 1 || cand.ImageURL != "/static/img/a.svg" {
		t.Fatalf("候选内容不正确: %+v", cand)
	}
	// 重复回调不重复创建候选
	dup, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "succeeded", ImageURL: "/static/img/a.svg",
	})
	if err != nil {
		t.Fatalf("重复回调不应报错: %v", err)
	}
	if !dup.Ignored || dup.Applied {
		t.Fatalf("重复回调应被忽略: %+v", dup)
	}
	cands, _ := env.store.ListCandidates(ctx, req.ID)
	if len(cands) != 1 {
		t.Fatalf("重复回调不应产生第二个候选，实际 %d", len(cands))
	}
	events, _ := env.store.ListEvents(ctx, req.ID)
	found := false
	for _, e := range events {
		if e.Kind == domain.EventCallbackDuplicate {
			found = true
		}
	}
	if !found {
		t.Fatalf("重复回调应留痕: %+v", events)
	}
	_ = task
}

func TestHandleCallbackInvalidInput(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	if _, err := env.svc.HandleCallback(ctx, CallbackPayload{Status: "succeeded"}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("缺少任务号应报参数错误，实际 %v", err)
	}
	if _, err := env.svc.HandleCallback(ctx, CallbackPayload{SupplierTaskID: "x", Status: "weird"}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("非法状态应报参数错误，实际 %v", err)
	}
	if _, err := env.svc.HandleCallback(ctx, CallbackPayload{SupplierTaskID: "x", Status: "succeeded"}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("成功回调缺少图片地址应报参数错误，实际 %v", err)
	}
}

func TestHandleCallbackUnknownTask(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	result, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: "sup-unknown", Status: "succeeded", ImageURL: "/static/img/a.svg",
	})
	if err != nil {
		t.Fatalf("未知任务回调不应报错: %v", err)
	}
	if !result.Ignored {
		t.Fatalf("未知任务回调应被忽略: %+v", result)
	}
	events, _ := env.store.ListEvents(ctx, "")
	if len(events) == 0 {
		t.Fatal("未知任务回调应留痕")
	}
}

func TestHandleCallbackFailure(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-1"); err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	result, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: env.supplier.taskID(), Status: "failed", Reason: "供应商内部错误",
	})
	if err != nil {
		t.Fatalf("处理失败回调出错: %v", err)
	}
	if !result.Applied {
		t.Fatalf("失败回调应被应用: %+v", result)
	}
	got, _ := env.store.GetRequirement(ctx, req.ID)
	if got.Status != domain.ReqFailed {
		t.Fatalf("需求状态应为 failed，实际 %q", got.Status)
	}
	cands, _ := env.store.ListCandidates(ctx, req.ID)
	if len(cands) != 0 {
		t.Fatalf("失败回调不应产生候选，实际 %d", len(cands))
	}
}

func TestLateCallbackAfterWithdraw(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-1"); err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	supplierTask := env.supplier.taskID()
	if err := env.svc.Withdraw(ctx, env.requester, req.ID); err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	result, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: supplierTask, Status: "succeeded", ImageURL: "/static/img/a.svg",
	})
	if err != nil {
		t.Fatalf("迟到回调不应报错: %v", err)
	}
	if !result.Ignored {
		t.Fatalf("迟到回调应被忽略: %+v", result)
	}
	got, _ := env.store.GetRequirement(ctx, req.ID)
	if got.Status != domain.ReqWithdrawn {
		t.Fatalf("撤销后的需求不应被恢复，实际 %q", got.Status)
	}
	cands, _ := env.store.ListCandidates(ctx, req.ID)
	if len(cands) != 0 {
		t.Fatalf("迟到回调不应产生候选，实际 %d", len(cands))
	}
	events, _ := env.store.ListEvents(ctx, req.ID)
	found := false
	for _, e := range events {
		if e.Kind == domain.EventLateResultIgnored {
			found = true
		}
	}
	if !found {
		t.Fatalf("迟到结果应留痕: %+v", events)
	}
}

func TestReviewCandidate(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-1"); err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	if _, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: env.supplier.taskID(), Status: "succeeded", ImageURL: "/static/img/a.svg",
	}); err != nil {
		t.Fatalf("回调处理失败: %v", err)
	}
	cand, _ := env.store.GetCurrentCandidate(ctx, req.ID)

	if _, err := env.svc.ReviewCandidate(ctx, env.requester, req.ID, "", true, ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("需求人复核应被拒绝，实际 %v", err)
	}
	if _, err := env.svc.ReviewCandidate(ctx, env.reviewer, req.ID, "", false, ""); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("复核不通过缺原因应报参数错误，实际 %v", err)
	}
	// 先不通过
	if _, err := env.svc.ReviewCandidate(ctx, env.reviewer, req.ID, "", false, "图片不符合文案"); err != nil {
		t.Fatalf("复核不通过失败: %v", err)
	}
	got, _ := env.store.GetRequirement(ctx, req.ID)
	if got.Status != domain.ReqRejected {
		t.Fatalf("复核不通过后状态应为 rejected，实际 %q", got.Status)
	}
	// 重复复核（当前状态已不是待复核）
	if _, err := env.svc.ReviewCandidate(ctx, env.reviewer, req.ID, "", true, "再复核"); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("重复复核应返回 ErrInvalidState，实际 %v", err)
	}
	// 重新生成后旧候选成为历史
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-2"); err != nil {
		t.Fatalf("再次发起生成失败: %v", err)
	}
	if _, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: env.supplier.taskID(), Status: "succeeded", ImageURL: "/static/img/b.svg",
	}); err != nil {
		t.Fatalf("回调处理失败: %v", err)
	}
	if _, err := env.svc.ReviewCandidate(ctx, env.reviewer, req.ID, cand.ID, true, "复核历史版本"); !errors.Is(err, domain.ErrNotCurrentCandidate) {
		t.Fatalf("复核历史候选应返回 ErrNotCurrentCandidate，实际 %v", err)
	}
	if _, err := env.svc.ReviewCandidate(ctx, env.reviewer, req.ID, "cand-missing", true, "不存在的候选"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("不存在的候选应返回 ErrNotFound，实际 %v", err)
	}
}

func TestAdoptAndRework(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-1"); err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	if _, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: env.supplier.taskID(), Status: "succeeded", ImageURL: "/static/img/a.svg",
	}); err != nil {
		t.Fatalf("回调处理失败: %v", err)
	}
	// 未复核前不能确认采用
	if _, err := env.svc.Adopt(ctx, env.requester, req.ID, ""); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("未复核时确认采用应返回 ErrInvalidState，实际 %v", err)
	}
	if _, err := env.svc.ReviewCandidate(ctx, env.reviewer, req.ID, "", true, "通过"); err != nil {
		t.Fatalf("复核失败: %v", err)
	}
	if _, err := env.svc.Adopt(ctx, env.operator, req.ID, ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("运营确认采用应被拒绝，实际 %v", err)
	}
	if err := env.svc.RequestRework(ctx, env.requester, req.ID, ""); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("空重做原因应报参数错误，实际 %v", err)
	}
	if err := env.svc.RequestRework(ctx, env.requester, req.ID, "图片不符合文案"); err != nil {
		t.Fatalf("提出重做失败: %v", err)
	}
	got, _ := env.store.GetRequirement(ctx, req.ID)
	if got.Status != domain.ReqRejected {
		t.Fatalf("提出重做后状态应为 rejected，实际 %q", got.Status)
	}
	// 重新生成并复核通过后确认采用
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-2"); err != nil {
		t.Fatalf("再次发起生成失败: %v", err)
	}
	if _, err := env.svc.HandleCallback(ctx, CallbackPayload{
		SupplierTaskID: env.supplier.taskID(), Status: "succeeded", ImageURL: "/static/img/b.svg",
	}); err != nil {
		t.Fatalf("回调处理失败: %v", err)
	}
	if _, err := env.svc.ReviewCandidate(ctx, env.reviewer, req.ID, "", true, "第二版通过"); err != nil {
		t.Fatalf("复核失败: %v", err)
	}
	delivered, err := env.svc.Adopt(ctx, env.requester, req.ID, "")
	if err != nil {
		t.Fatalf("确认采用失败: %v", err)
	}
	if delivered.Status != domain.ReqDelivered {
		t.Fatalf("确认采用后状态应为 delivered，实际 %q", delivered.Status)
	}
	// 已确认采用不可撤销
	if err := env.svc.Withdraw(ctx, env.requester, req.ID); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("已交付需求撤销应返回 ErrInvalidState，实际 %v", err)
	}
}

func TestWithdrawAndOperatorNote(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")

	if err := env.svc.Withdraw(ctx, env.reviewer, req.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("复核人撤销应被拒绝，实际 %v", err)
	}
	if err := env.svc.Withdraw(ctx, env.requester, req.ID); err != nil {
		t.Fatalf("撤销失败: %v", err)
	}
	got, _ := env.store.GetRequirement(ctx, req.ID)
	if got.Status != domain.ReqWithdrawn {
		t.Fatalf("撤销后状态应为 withdrawn，实际 %q", got.Status)
	}
	if err := env.svc.Withdraw(ctx, env.requester, req.ID); !errors.Is(err, domain.ErrInvalidState) {
		t.Fatalf("重复撤销应返回 ErrInvalidState，实际 %v", err)
	}
	if err := env.svc.AddOperatorNote(ctx, env.requester, req.ID, "跟进"); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("需求人记录运营意见应被拒绝，实际 %v", err)
	}
	if err := env.svc.AddOperatorNote(ctx, env.operator, req.ID, ""); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("空意见应报参数错误，实际 %v", err)
	}
	if err := env.svc.AddOperatorNote(ctx, env.operator, req.ID, "已联系供应商跟进"); err != nil {
		t.Fatalf("记录运营意见失败: %v", err)
	}
}

func TestSweepTimeouts(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	if _, err := env.svc.StartGeneration(ctx, env.requester, req.ID, "r-1"); err != nil {
		t.Fatalf("发起生成失败: %v", err)
	}
	// 未到时限不应判定超时
	timedOut, err := env.svc.SweepTimeouts(ctx)
	if err != nil {
		t.Fatalf("巡检失败: %v", err)
	}
	if len(timedOut) != 0 {
		t.Fatalf("未到时限不应超时，实际 %v", timedOut)
	}
	// 把任务创建时间改到很久以前
	if _, err := env.store.DB().Exec("UPDATE generation_tasks SET created_at = ?",
		time.Now().Add(-2*time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("调整时间失败: %v", err)
	}
	timedOut, err = env.svc.SweepTimeouts(ctx)
	if err != nil {
		t.Fatalf("巡检失败: %v", err)
	}
	if len(timedOut) != 1 {
		t.Fatalf("应判定 1 个任务超时，实际 %d", len(timedOut))
	}
	got, _ := env.store.GetRequirement(ctx, req.ID)
	if got.Status != domain.ReqFailed {
		t.Fatalf("超时后需求状态应为 failed，实际 %q", got.Status)
	}
	cands, _ := env.store.ListCandidates(ctx, req.ID)
	if len(cands) != 0 {
		t.Fatalf("超时不产生候选，实际 %d", len(cands))
	}
	// 再次巡检不应重复判定
	timedOut, _ = env.svc.SweepTimeouts(ctx)
	if len(timedOut) != 0 {
		t.Fatalf("已终结任务不应重复判定超时，实际 %v", timedOut)
	}
}

func TestQueriesAndPermissions(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	req := env.newRequirement(t, "初始描述文本")
	other := &domain.User{ID: "demand-b", Name: "需求人乙", Role: domain.RoleRequester}
	_ = other

	// 需求人只能看到自己的需求
	own, err := env.svc.ListRequirements(ctx, env.requester, nil)
	if err != nil || len(own) != 1 {
		t.Fatalf("需求人应看到 1 条需求，实际 %d（%v）", len(own), err)
	}
	// 复核人与运营可看到全部
	if items, err := env.svc.ListRequirements(ctx, env.reviewer, nil); err != nil || len(items) != 1 {
		t.Fatalf("复核人应看到 1 条需求，实际 %d（%v）", len(items), err)
	}
	if items, err := env.svc.ListRequirements(ctx, env.operator, nil); err != nil || len(items) != 1 {
		t.Fatalf("运营应看到 1 条需求，实际 %d（%v）", len(items), err)
	}

	// 他人需求不可读
	if _, err := env.svc.RequirementDetail(ctx, other, req.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("访问他人需求应返回 ErrForbidden，实际 %v", err)
	}
	view, err := env.svc.RequirementDetail(ctx, env.requester, req.ID)
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if view.Requirement == nil || view.Tasks == nil || view.Candidates == nil || view.Events == nil {
		t.Fatalf("详情视图字段缺失: %+v", view)
	}
	// 异常查询与总览的权限
	if _, err := env.svc.ListAnomalies(ctx, env.requester, 10); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("需求人查看异常应被拒绝，实际 %v", err)
	}
	if _, err := env.svc.ListAnomalies(ctx, env.operator, 10); err != nil {
		t.Fatalf("运营查看异常失败: %v", err)
	}
	if _, err := env.svc.Overview(ctx, env.requester); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("需求人查看总览应被拒绝，实际 %v", err)
	}
	overview, err := env.svc.Overview(ctx, env.operator)
	if err != nil {
		t.Fatalf("运营查看总览失败: %v", err)
	}
	if overview.RequirementCounts[domain.ReqDraft] != 1 {
		t.Fatalf("总览统计不正确: %+v", overview.RequirementCounts)
	}
	// 复核队列
	if _, err := env.svc.ReviewQueue(ctx, env.operator); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("运营查看复核队列应被拒绝，实际 %v", err)
	}
	queue, err := env.svc.ReviewQueue(ctx, env.reviewer)
	if err != nil || len(queue) != 0 {
		t.Fatalf("当前无待复核需求，实际 %d（%v）", len(queue), err)
	}
}

func TestRecordAnomalyAndDescribeStatus(t *testing.T) {
	env := newTestEnv(t, "accept")
	ctx := context.Background()
	if err := env.svc.RecordAnomaly(ctx, domain.EventSignatureInvalid, "", "", "签名错误"); err != nil {
		t.Fatalf("记录异常失败: %v", err)
	}
	events, err := env.store.ListRecentEvents(ctx, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("异常事件应入库，实际 %d（%v）", len(events), err)
	}
	if DescribeStatus(domain.ReqDelivered) == "" {
		t.Fatal("状态说明不应为空")
	}
	if DescribeStatus(domain.RequirementStatus("unknown")) == "" {
		t.Fatal("未知状态也应有兜底说明")
	}
}
