package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"creative-studio/internal/domain"
)

// recorder 是简化版的响应记录器。
type recorder struct {
	header http.Header
	code   int
	body   strings.Builder
}

// newRecorder 创建响应记录器。
func newRecorder() *recorder {
	return &recorder{header: http.Header{}, code: http.StatusOK}
}

// Header 返回响应头。
func (r *recorder) Header() http.Header { return r.header }

// Write 写入响应内容。
func (r *recorder) Write(data []byte) (int, error) { return r.body.Write(data) }

// WriteHeader 写入响应状态码。
func (r *recorder) WriteHeader(code int) { r.code = code }

// TestLabelHelpers 覆盖全部中文标签映射，确保页面上的状态说明没有遗漏分支。
func TestLabelHelpers(t *testing.T) {
	eventKinds := []string{
		domain.EventRequirementCreated, domain.EventDescriptionUpdated, domain.EventGenerationStarted,
		domain.EventGenerationRejected, domain.EventGenerationFailed, domain.EventGenerationTimeout,
		domain.EventCallbackAccepted, domain.EventCallbackIgnored, domain.EventCallbackUnknown,
		domain.EventCandidateCreated, domain.EventCandidateReviewed, domain.EventReworkRequested,
		domain.EventAdopted, domain.EventWithdrawn, domain.EventOperatorNote,
		domain.EventCallbackDuplicate, domain.EventCallbackConflict, domain.EventLateResultIgnored,
		domain.EventSignatureInvalid, "自定义事件",
	}
	for _, kind := range eventKinds {
		if eventLabel(kind) == "" {
			t.Fatalf("事件 %q 缺少中文标签", kind)
		}
	}
	taskStatuses := []domain.TaskStatus{
		domain.TaskProcessing, domain.TaskSucceeded, domain.TaskFailed,
		domain.TaskRejected, domain.TaskTimeout, domain.TaskStatus("自定义"),
	}
	for _, status := range taskStatuses {
		if taskLabel(status) == "" {
			t.Fatalf("任务状态 %q 缺少中文标签", status)
		}
	}
	reviewStatuses := []domain.ReviewStatus{
		domain.ReviewPending, domain.ReviewApproved, domain.ReviewRejected, domain.ReviewStatus("自定义"),
	}
	for _, status := range reviewStatuses {
		if reviewLabel(status) == "" {
			t.Fatalf("复核状态 %q 缺少中文标签", status)
		}
	}
	for _, role := range []domain.Role{domain.RoleRequester, domain.RoleReviewer, domain.RoleOperator, domain.Role("其他")} {
		if roleLabel(role) == "" {
			t.Fatalf("角色 %q 缺少中文标签", role)
		}
	}
}

// TestRecoverMiddlewareReturns500 覆盖未预期异常的兜底处理。
func TestRecoverMiddlewareReturns500(t *testing.T) {
	// 直接构造一个会 panic 的处理器，验证兜底中间件不会把内部细节透出去。
	handler := (&Server{}).recoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("内部细节不应透出")
	}))
	req, _ := http.NewRequest(http.MethodGet, "/panic", nil)
	rec := newRecorder()
	handler.ServeHTTP(rec, req)
	if rec.code != http.StatusInternalServerError {
		t.Fatalf("panic 应返回 500，实际 %d", rec.code)
	}
	if strings.Contains(rec.body.String(), "内部细节") {
		t.Fatalf("500 响应不应透出内部细节: %s", rec.body.String())
	}
}

// TestDecodeJSONEmptyBody 覆盖空请求体的解析分支。
func TestDecodeJSONEmptyBody(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "/api/x", strings.NewReader(""))
	var target struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(req, &target); err != nil {
		t.Fatalf("空请求体不应报错: %v", err)
	}
	if target.Name != "" {
		t.Fatalf("空请求体解析结果应为空: %+v", target)
	}
	req2, _ := http.NewRequest(http.MethodPost, "/api/x", strings.NewReader("not-json"))
	if err := decodeJSON(req2, &target); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

// TestSupplierCallbackBadJSON 覆盖回调内容非法时的分支。
func TestSupplierCallbackBadJSON(t *testing.T) {
	h := newHarness(t)
	body := "not-json"
	req, _ := http.NewRequest(http.MethodPost, h.server.URL+"/api/supplier/callback", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Supplier-Signature", signOf(h.secret, body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("回调请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法回调内容应返回 400，实际 %d", resp.StatusCode)
	}
}

// TestSupplierCallbackInvalidPayload 覆盖回调字段缺失时的分支。
func TestSupplierCallbackInvalidPayload(t *testing.T) {
	h := newHarness(t)
	body := `{"task_id":"","status":"weird"}`
	req, _ := http.NewRequest(http.MethodPost, h.server.URL+"/api/supplier/callback", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Supplier-Signature", signOf(h.secret, body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("回调请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("非法回调字段应返回 400，实际 %d", resp.StatusCode)
	}
}

// TestNewRequestIDFormat 覆盖请求编号生成。
func TestNewRequestIDFormat(t *testing.T) {
	id := newRequestID()
	if !strings.HasPrefix(id, "req-") || len(id) < 8 {
		t.Fatalf("请求编号格式不正确: %q", id)
	}
	if id == newRequestID() {
		t.Fatal("两次生成的请求编号不应相同")
	}
}
