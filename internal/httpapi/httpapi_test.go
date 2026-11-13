package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"creative-studio/internal/domain"
	"creative-studio/internal/service"
	"creative-studio/internal/store"
	"creative-studio/internal/supplier"
)

// harness 是 HTTP 层测试用的运行环境。
type harness struct {
	server    *httptest.Server
	svc       *service.Service
	store     *store.Store
	secret    string
	tokens    map[string]string
	loginPage string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "http.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	supplierSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Ref string `json:"ref"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		_, _ = w.Write([]byte(`{"accepted":true,"task_id":"sup-` + in.Ref + `"}`))
	}))
	t.Cleanup(supplierSrv.Close)

	secret := "test-secret"
	svc := service.New(st, domain.SystemClock{}, service.Config{
		SupplierBaseURL: supplierSrv.URL,
		SupplierSecret:  secret,
		SupplierTimeout: 2 * time.Second,
		ClockScale:      1,
	})
	for _, u := range []struct {
		id   string
		role domain.Role
	}{{"demand-a", domain.RoleRequester}, {"demand-b", domain.RoleRequester},
		{"review-a", domain.RoleReviewer}, {"ops-a", domain.RoleOperator}} {
		if err := svc.SeedUser(t.Context(), u.id, u.id, u.role, "Studio@2026"); err != nil {
			t.Fatalf("预置账号失败: %v", err)
		}
	}
	srv, err := New(svc, secret)
	if err != nil {
		t.Fatalf("创建 HTTP 服务失败: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	h := &harness{server: ts, svc: svc, store: st, secret: secret, tokens: map[string]string{}}
	for _, account := range []string{"demand-a", "demand-b", "review-a", "ops-a"} {
		h.tokens[account] = h.login(t, account)
	}
	return h
}

func (h *harness) login(t *testing.T, account string) string {
	t.Helper()
	body := strings.NewReader(`{"login":"` + account + `","password":"Studio@2026"}`)
	resp, err := http.Post(h.server.URL+"/api/login", "application/json", body)
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("解析登录响应失败: %v", err)
	}
	if out.Token == "" {
		t.Fatal("登录未返回令牌")
	}
	return out.Token
}

// do 发起一次带令牌的 JSON 请求。
func (h *harness) do(t *testing.T, method, path, account string, payload any) (int, string) {
	t.Helper()
	var reader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("序列化请求失败: %v", err)
		}
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if account != "" {
		req.Header.Set("Authorization", "Bearer "+h.tokens[account])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// doPage 发起一次页面请求（带 Cookie 会话）。
func (h *harness) doPage(t *testing.T, method, path, account string, form url.Values) (int, string) {
	t.Helper()
	var reader io.Reader
	if form != nil {
		reader = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if account != "" {
		req.AddCookie(&http.Cookie{Name: "studio_session", Value: h.tokens[account]})
	}
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("页面请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// doPageResp 发起一次页面请求并返回原始响应（用于检查重定向目标）。
func (h *harness) doPageResp(t *testing.T, method, path, account string, form url.Values) *http.Response {
	t.Helper()
	var reader io.Reader
	if form != nil {
		reader = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if account != "" {
		req.AddCookie(&http.Cookie{Name: "studio_session", Value: h.tokens[account]})
	}
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("页面请求失败: %v", err)
	}
	return resp
}

func TestLoginAPI(t *testing.T) {
	h := newHarness(t)
	resp, err := http.Post(h.server.URL+"/api/login", "application/json",
		strings.NewReader(`{"login":"demand-a","password":"Studio@2026"}`))
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("登录应返回 200，实际 %d", resp.StatusCode)
	}
	if c := resp.Cookies(); len(c) == 0 || c[0].Name != "studio_session" {
		t.Fatal("登录应写入会话 Cookie")
	}

	resp2, err := http.Post(h.server.URL+"/api/login", "application/json",
		strings.NewReader(`{"login":"demand-a","password":"bad"}`))
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误口令应返回 401，实际 %d", resp2.StatusCode)
	}
}

func TestAPIRequiresSession(t *testing.T) {
	h := newHarness(t)
	status, body := h.do(t, http.MethodGet, "/api/requirements", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("未登录访问接口应返回 401，实际 %d（%s）", status, body)
	}
	if !strings.Contains(body, "未登录") {
		t.Fatalf("错误信息应可读: %s", body)
	}
}

func TestCreateAndListRequirements(t *testing.T) {
	h := newHarness(t)
	status, body := h.do(t, http.MethodPost, "/api/requirements", "demand-a", map[string]any{
		"activity_name": "夏日活动", "purpose": "新品主视觉", "description": "冷色调冰饮主视觉",
	})
	if status != http.StatusCreated {
		t.Fatalf("创建需求应返回 201，实际 %d（%s）", status, body)
	}
	status, body = h.do(t, http.MethodGet, "/api/requirements", "demand-a", nil)
	if status != http.StatusOK || !strings.Contains(body, "夏日活动") {
		t.Fatalf("需求列表不正确: %d %s", status, body)
	}
	// 参数不合法应返回 400
	status, _ = h.do(t, http.MethodPost, "/api/requirements", "demand-a", map[string]any{
		"activity_name": "", "purpose": "用途", "description": "描述",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("参数错误应返回 400，实际 %d", status)
	}
	// 复核人创建需求应返回 403
	status, _ = h.do(t, http.MethodPost, "/api/requirements", "review-a", map[string]any{
		"activity_name": "活动", "purpose": "用途", "description": "描述文本",
	})
	if status != http.StatusForbidden {
		t.Fatalf("越权创建应返回 403，实际 %d", status)
	}
}

func TestBusinessFlowOverAPI(t *testing.T) {
	h := newHarness(t)
	status, body := h.do(t, http.MethodPost, "/api/requirements", "demand-a", map[string]any{
		"activity_name": "夏日活动", "purpose": "新品主视觉", "description": "冷色调冰饮主视觉",
	})
	if status != http.StatusCreated {
		t.Fatalf("创建需求失败: %d %s", status, body)
	}
	var created struct {
		Requirement domain.Requirement `json:"requirement"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	rid := created.Requirement.ID

	status, body = h.do(t, http.MethodPost, "/api/requirements/"+rid+"/generations", "demand-a",
		map[string]any{"request_no": "r-1"})
	if status != http.StatusAccepted {
		t.Fatalf("发起生成应返回 202，实际 %d（%s）", status, body)
	}
	// 模拟供应商成功回调
	payload := map[string]any{"task_id": "sup-" + rid + "-x", "status": "succeeded", "image_url": "/static/img/a.svg"}
	// 回调任务号取自任务记录
	task, _ := h.store.GetTaskByRequestNo(t.Context(), "r-1")
	payload["task_id"] = "sup-task-1"
	if task.ID == "" {
		t.Fatal("未找到生成任务")
	}
	status, body = h.do(t, http.MethodPost, "/api/requirements/"+rid+"/generations", "demand-a",
		map[string]any{"request_no": "r-1"})
	if status != http.StatusAccepted {
		t.Fatalf("幂等重复发起应返回 202，实际 %d（%s）", status, body)
	}
	_ = payload
}

func TestPermissionDeniedOverAPI(t *testing.T) {
	h := newHarness(t)
	status, body := h.do(t, http.MethodPost, "/api/requirements", "demand-a", map[string]any{
		"activity_name": "夏日活动", "purpose": "新品主视觉", "description": "冷色调冰饮主视觉",
	})
	if status != http.StatusCreated {
		t.Fatalf("创建需求失败: %s", body)
	}
	var created struct {
		Requirement domain.Requirement `json:"requirement"`
	}
	_ = json.Unmarshal([]byte(body), &created)
	rid := created.Requirement.ID

	status, _ = h.do(t, http.MethodGet, "/api/requirements/"+rid, "demand-b", nil)
	if status != http.StatusForbidden {
		t.Fatalf("访问他人需求应返回 403，实际 %d", status)
	}
	status, _ = h.do(t, http.MethodGet, "/api/requirements/req-missing", "demand-a", nil)
	if status != http.StatusNotFound {
		t.Fatalf("不存在的需求应返回 404，实际 %d", status)
	}
	status, _ = h.do(t, http.MethodPost, "/api/requirements/"+rid+"/generations", "ops-a",
		map[string]any{"request_no": "r-1"})
	if status != http.StatusForbidden {
		t.Fatalf("运营发起生成应返回 403，实际 %d", status)
	}
	status, _ = h.do(t, http.MethodPost, "/api/requirements/"+rid+"/adopt", "ops-a", map[string]any{})
	if status != http.StatusForbidden {
		t.Fatalf("运营确认采用应返回 403，实际 %d", status)
	}
}

func TestSupplierCallbackEndpoint(t *testing.T) {
	h := newHarness(t)
	body := `{"task_id":"sup-x","status":"succeeded","image_url":"/static/img/a.svg"}`
	req, _ := http.NewRequest(http.MethodPost, h.server.URL+"/api/supplier/callback", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Supplier-Signature", "deadbeef")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("回调请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("签名错误应返回 401，实际 %d", resp.StatusCode)
	}
	// 正确签名：任务不存在，应被忽略并留痕
	req2, _ := http.NewRequest(http.MethodPost, h.server.URL+"/api/supplier/callback", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Supplier-Signature", supplier.Signature(h.secret, []byte(body)))
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("回调请求失败: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("合法签名应返回 200，实际 %d", resp2.StatusCode)
	}
	raw, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(raw), "ignored") {
		t.Fatalf("未知任务回调应被忽略: %s", raw)
	}
	events, _ := h.store.ListRecentEvents(t.Context(), 10)
	found := false
	for _, e := range events {
		if e.Kind == domain.EventSignatureInvalid || e.Kind == domain.EventCallbackUnknown {
			found = true
		}
	}
	if !found {
		t.Fatalf("回调异常应留痕: %+v", events)
	}
}

func TestPagesRender(t *testing.T) {
	h := newHarness(t)
	status, body := h.doPage(t, http.MethodGet, "/login", "", nil)
	if status != http.StatusOK || !strings.Contains(body, "登录") {
		t.Fatalf("登录页渲染失败: %d", status)
	}
	status, body = h.doPage(t, http.MethodGet, "/workbench", "demand-a", nil)
	if status != http.StatusOK || !strings.Contains(body, "需求工作台") {
		t.Fatalf("工作台渲染失败: %d", status)
	}
	status, body = h.doPage(t, http.MethodGet, "/review", "review-a", nil)
	if status != http.StatusOK || !strings.Contains(body, "候选复核") {
		t.Fatalf("复核页渲染失败: %d", status)
	}
	status, body = h.doPage(t, http.MethodGet, "/ops", "ops-a", nil)
	if status != http.StatusOK || !strings.Contains(body, "运营异常查询") {
		t.Fatalf("运营页渲染失败: %d", status)
	}
	// 未登录访问页面应跳转登录
	status, _ = h.doPage(t, http.MethodGet, "/workbench", "", nil)
	if status != http.StatusSeeOther {
		t.Fatalf("未登录访问页面应重定向，实际 %d", status)
	}
	// 非法图片名
	status, _ = h.doPage(t, http.MethodGet, "/static/img/..%2Fetc", "", nil)
	if status != http.StatusBadRequest && status != http.StatusNotFound {
		t.Fatalf("非法图片名应被拒绝，实际 %d", status)
	}
	status, body = h.doPage(t, http.MethodGet, "/static/img/gen-abc.svg", "", nil)
	if status != http.StatusOK || !strings.Contains(body, "<svg") {
		t.Fatalf("演示图片应可访问: %d", status)
	}
}

func TestFormFlow(t *testing.T) {
	h := newHarness(t)
	status, body := h.doPage(t, http.MethodPost, "/requirements", "demand-a", url.Values{
		"activity_name": {"夏日活动"}, "purpose": {"新品主视觉"}, "description": {"冷色调冰饮主视觉"},
	})
	if status != http.StatusSeeOther {
		t.Fatalf("表单创建需求应重定向，实际 %d（%s）", status, body)
	}
	list, _ := h.store.ListRequirements(t.Context(), store.RequirementFilter{OwnerID: "demand-a"})
	if len(list) != 1 {
		t.Fatalf("应创建 1 条需求，实际 %d", len(list))
	}
	rid := list[0].ID

	// 参数错误应带回错误提示
	resp := h.doPageResp(t, http.MethodPost, "/requirements/"+rid+"/description", "demand-a",
		url.Values{"description": {"短"}})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "err=") {
		t.Fatalf("非法描述应重定向并带错误: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	status, body = h.doPage(t, http.MethodPost, "/requirements/"+rid+"/generations", "demand-a",
		url.Values{"request_no": {"form-r-1"}})
	if status != http.StatusSeeOther {
		t.Fatalf("表单发起生成应重定向，实际 %d（%s）", status, body)
	}
	tasks, _ := h.store.ListTasks(t.Context(), rid)
	if len(tasks) != 1 {
		t.Fatalf("应创建 1 个生成任务，实际 %d", len(tasks))
	}
	// 复核表单
	respReview := h.doPageResp(t, http.MethodPost, "/requirements/"+rid+"/review", "review-a",
		url.Values{"decision": {"reject"}, "reason": {"不符合文案"}})
	defer func() { _ = respReview.Body.Close() }()
	if respReview.StatusCode != http.StatusSeeOther || !strings.Contains(respReview.Header.Get("Location"), "err=") {
		t.Fatalf("未通过复核应提示状态不允许: %d %s", respReview.StatusCode, respReview.Header.Get("Location"))
	}
	// 撤销表单
	status, body = h.doPage(t, http.MethodPost, "/requirements/"+rid+"/withdraw", "demand-a", nil)
	if status != http.StatusSeeOther {
		t.Fatalf("撤销应重定向，实际 %d（%s）", status, body)
	}
	got, _ := h.store.GetRequirement(t.Context(), rid)
	if got.Status != domain.ReqWithdrawn {
		t.Fatalf("撤销后状态应为 withdrawn，实际 %q", got.Status)
	}
	// 运营意见表单
	status, _ = h.doPage(t, http.MethodPost, "/requirements/"+rid+"/operator-note", "ops-a",
		url.Values{"note": {"已跟进供应商"}})
	if status != http.StatusSeeOther {
		t.Fatalf("运营意见应重定向，实际 %d", status)
	}
	// 退出登录
	status, _ = h.doPage(t, http.MethodPost, "/logout", "demand-a", nil)
	if status != http.StatusSeeOther {
		t.Fatalf("退出应重定向，实际 %d", status)
	}
}

func TestHealthzAndHelpers(t *testing.T) {
	h := newHarness(t)
	status, body := h.do(t, http.MethodGet, "/healthz", "", nil)
	if status != http.StatusOK || !strings.Contains(body, "ok") {
		t.Fatalf("健康检查失败: %d %s", status, body)
	}
	if statusForError(domain.ErrNotFound) != http.StatusNotFound {
		t.Fatal("ErrNotFound 应映射 404")
	}
	if statusForError(domain.ErrForbidden) != http.StatusForbidden {
		t.Fatal("ErrForbidden 应映射 403")
	}
	if statusForError(domain.ErrInvalidInput) != http.StatusBadRequest {
		t.Fatal("ErrInvalidInput 应映射 400")
	}
	if statusForError(domain.ErrInvalidState) != http.StatusConflict {
		t.Fatal("ErrInvalidState 应映射 409")
	}
	if statusForError(domain.ErrConflict) != http.StatusConflict {
		t.Fatal("ErrConflict 应映射 409")
	}
	if statusForError(domain.ErrTooManyAttempts) != http.StatusConflict {
		t.Fatal("ErrTooManyAttempts 应映射 409")
	}
	if statusForError(domain.ErrNotCurrentCandidate) != http.StatusConflict {
		t.Fatal("ErrNotCurrentCandidate 应映射 409")
	}
	if statusForError(domain.ErrUnauthorized) != http.StatusUnauthorized {
		t.Fatal("ErrUnauthorized 应映射 401")
	}
	if statusForError(io.ErrUnexpectedEOF) != http.StatusInternalServerError {
		t.Fatal("未知错误应映射 500")
	}
	if errorMessage(io.ErrUnexpectedEOF) != "服务内部错误，请稍后重试" {
		t.Fatalf("内部错误不应透出细节: %s", errorMessage(io.ErrUnexpectedEOF))
	}
	if roleLabel(domain.RoleRequester) != "需求人" {
		t.Fatal("角色标签不正确")
	}
	if taskLabel(domain.TaskTimeout) != "超时" {
		t.Fatal("任务标签不正确")
	}
	if reviewLabel(domain.ReviewPending) != "待复核" {
		t.Fatal("复核标签不正确")
	}
	if eventLabel(domain.EventCallbackDuplicate) != "重复回调" {
		t.Fatal("事件标签不正确")
	}
	if !strings.Contains(urlQueryEscape("a b&c"), "%26") {
		t.Fatal("查询参数转义不正确")
	}
}
