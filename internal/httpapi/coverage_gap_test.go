package httpapi

import (
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"creative-studio/internal/domain"
)

// failingBody 是一个读取必然失败的请求体，用于覆盖「请求体读不出来」的分支。
type failingBody struct{}

// Read 返回读取错误。
func (failingBody) Read(p []byte) (int, error) { return 0, errors.New("请求体读取失败") }

// Close 关闭请求体。
func (failingBody) Close() error { return nil }

// doRaw 发起一次带原始请求体的请求，返回状态码、Location 与响应体。
// account 非空时同时带上会话 Cookie 与 Bearer 令牌，页面通道与 JSON 通道都适用。
func (h *harness) doRaw(t *testing.T, method, path, account, contentType, raw string) (int, string, string) {
	t.Helper()
	req, err := http.NewRequest(method, h.server.URL+path, strings.NewReader(raw))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if account != "" {
		req.AddCookie(&http.Cookie{Name: "studio_session", Value: h.tokens[account]})
		req.Header.Set("Authorization", "Bearer "+h.tokens[account])
	}
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Location"), string(body)
}

// TestJSONEndpointsRejectInvalidJSON 覆盖全部 JSON 写接口的「请求体不是合法 JSON」分支。
// 业务口径：请求体解析失败必须返回 400 并说明原因，且不得产生任何业务数据。
func TestJSONEndpointsRejectInvalidJSON(t *testing.T) {
	h := newHarness(t)
	rid := newRequirement(t, h, "demand-a", "非法 JSON 用例")
	cases := []struct {
		name    string
		path    string
		account string
	}{
		{"登录", "/api/login", ""},
		{"创建需求", "/api/requirements", "demand-a"},
		{"修改描述", "/api/requirements/" + rid + "/description", "demand-a"},
		{"发起生成", "/api/requirements/" + rid + "/generations", "demand-a"},
		{"复核", "/api/requirements/" + rid + "/review", "review-a"},
		{"确认采用", "/api/requirements/" + rid + "/adopt", "demand-a"},
		{"提出重做", "/api/requirements/" + rid + "/rework", "demand-a"},
		{"运营意见", "/api/requirements/" + rid + "/operator-note", "ops-a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, _, body := h.doRaw(t, http.MethodPost, c.path, c.account, "application/json", "{这不是合法JSON")
			if status != http.StatusBadRequest {
				t.Fatalf("非法 JSON 应返回 400，实际 %d（%s）", status, body)
			}
			if !strings.Contains(body, "请求内容不是合法 JSON") {
				t.Fatalf("错误信息应说明 JSON 非法: %s", body)
			}
			if !strings.Contains(body, "\"ok\":false") {
				t.Fatalf("错误响应应标记 ok=false: %s", body)
			}
		})
	}
	// 非法 JSON 一律不得落库：需求数量不增加，也没有生成任务。
	reqs, _ := h.store.ListRequirements(t.Context(), storeFilter("demand-a"))
	if len(reqs) != 1 {
		t.Fatalf("非法 JSON 不得创建需求，实际 %d 条", len(reqs))
	}
	tasks, _ := h.store.ListTasks(t.Context(), rid)
	if len(tasks) != 0 {
		t.Fatalf("非法 JSON 不得创建生成任务，实际 %d 个", len(tasks))
	}
}

// TestRequirementDetailAPIReturnsFullView 覆盖需求详情接口的成功分支。
// 业务口径：需求人查看自己的需求应拿到需求、任务、候选与审计事件。
func TestRequirementDetailAPIReturnsFullView(t *testing.T) {
	h := newHarness(t)
	rid := newRequirement(t, h, "demand-a", "详情接口用例")
	status, _, body := h.doRaw(t, http.MethodGet, "/api/requirements/"+rid, "demand-a", "application/json", "")
	if status != http.StatusOK {
		t.Fatalf("查看需求详情应返回 200，实际 %d（%s）", status, body)
	}
	var out struct {
		OK     bool `json:"ok"`
		Detail struct {
			Requirement domain.Requirement `json:"requirement"`
			Events      []domain.Event     `json:"events"`
		} `json:"detail"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("解析详情响应失败: %v（%s）", err, body)
	}
	if !out.OK {
		t.Fatalf("详情响应应标记 ok=true: %s", body)
	}
	if out.Detail.Requirement.ID != rid {
		t.Fatalf("详情应返回需求 %s，实际 %s", rid, out.Detail.Requirement.ID)
	}
	if out.Detail.Requirement.ActivityName != "详情接口用例" {
		t.Fatalf("详情应返回活动名称，实际 %q", out.Detail.Requirement.ActivityName)
	}
	if len(out.Detail.Events) == 0 {
		t.Fatalf("详情应包含审计事件: %s", body)
	}
}

// TestUnknownRoleAccountCannotListRequirements 覆盖「角色不属于题面三类主体」的拒绝分支。
// 业务口径：需求列表按角色收口，未知角色既不能调接口，也不能进工作台页面。
func TestUnknownRoleAccountCannotListRequirements(t *testing.T) {
	h := newHarness(t)
	if err := h.svc.SeedUser(t.Context(), "auditor", "审计员", domain.Role("auditor"), testPassword); err != nil {
		t.Fatalf("预置未知角色账号失败: %v", err)
	}
	h.tokens["auditor"] = h.login(t, "auditor")

	status, _, body := h.doRaw(t, http.MethodGet, "/api/requirements", "auditor", "application/json", "")
	if status != http.StatusForbidden {
		t.Fatalf("未知角色列需求应返回 403，实际 %d（%s）", status, body)
	}
	if !strings.Contains(body, "无权执行该操作") {
		t.Fatalf("错误信息应说明无权: %s", body)
	}

	status, _, body = h.doRaw(t, http.MethodGet, "/workbench", "auditor", "application/json", "")
	if status != http.StatusForbidden {
		t.Fatalf("未知角色访问工作台应返回 403，实际 %d（%s）", status, body)
	}
	if !strings.Contains(body, "未知角色") {
		t.Fatalf("工作台应回显拒绝原因: %s", body)
	}
}

// TestAnomaliesAPIAppliesLimit 覆盖异常查询接口的 limit 参数分支。
// 业务口径：limit 必须真正生效，只返回指定条数的异常记录。
func TestAnomaliesAPIAppliesLimit(t *testing.T) {
	h := newHarness(t)
	// 制造两条签名非法的回调异常。
	for i := 0; i < 2; i++ {
		status, _, body := h.doRaw(t, http.MethodPost, "/api/supplier/callback", "", "application/json",
			`{"task_id":"sup-x","status":"succeeded"}`)
		if status != http.StatusUnauthorized {
			t.Fatalf("签名非法的回调应返回 401，实际 %d（%s）", status, body)
		}
	}
	status, _, body := h.doRaw(t, http.MethodGet, "/api/anomalies?limit=1", "ops-a", "application/json", "")
	if status != http.StatusOK {
		t.Fatalf("运营查询异常应返回 200，实际 %d（%s）", status, body)
	}
	var out struct {
		OK    bool           `json:"ok"`
		Items []domain.Event `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("解析异常响应失败: %v（%s）", err, body)
	}
	if len(out.Items) != 1 {
		t.Fatalf("limit=1 应只返回 1 条异常，实际 %d 条", len(out.Items))
	}
	if out.Items[0].Kind != domain.EventSignatureInvalid {
		t.Fatalf("异常种类不正确，实际 %q", out.Items[0].Kind)
	}
}

// TestSupplierCallbackRejectsUnreadableBody 覆盖回调接口读取请求体失败的分支。
// 业务口径：回调内容读不出来时必须返回 400，且不得进入验签与业务处理。
func TestSupplierCallbackRejectsUnreadableBody(t *testing.T) {
	h := newHarness(t)
	srv, err := New(h.svc, h.secret)
	if err != nil {
		t.Fatalf("创建 HTTP 服务失败: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/supplier/callback", failingBody{})
	rec := newRecorder()
	srv.apiSupplierCallback(rec, req)
	if rec.code != http.StatusBadRequest {
		t.Fatalf("回调请求体读取失败应返回 400，实际 %d（%s）", rec.code, rec.body.String())
	}
	if !strings.Contains(rec.body.String(), "无法读取回调内容") {
		t.Fatalf("应提示无法读取回调内容: %s", rec.body.String())
	}
}

// TestDecodeJSONReportsUnreadableBody 覆盖 JSON 解析时请求体读取失败的分支。
func TestDecodeJSONReportsUnreadableBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/requirements", failingBody{})
	var target struct {
		ActivityName string `json:"activity_name"`
	}
	if err := decodeJSON(req, &target); err == nil {
		t.Fatal("请求体读取失败时应返回错误")
	}
	if target.ActivityName != "" {
		t.Fatalf("解析失败不应写入字段: %+v", target)
	}
}

// TestDrainBodyToleratesMissingBody 覆盖读干请求体的空请求分支。
// 业务口径：没有请求体的请求（如 GET）读干时不能 panic，有请求体时必须读干净。
func TestDrainBodyToleratesMissingBody(t *testing.T) {
	drainBody(nil)
	drainBody(&http.Request{})

	req := httptest.NewRequest(http.MethodPost, "/api/requirements", strings.NewReader("payload"))
	drainBody(req)
	rest, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("读取剩余请求体失败: %v", err)
	}
	if len(rest) != 0 {
		t.Fatalf("读干后不应残留内容，实际 %q", rest)
	}
}

// TestStatusMappingCoversCallbackTaskErrors 覆盖供应商任务相关的状态码与文案映射。
// 业务口径：未知任务按 404、已终结任务按 409 对外，没有错误时不产生文案。
func TestStatusMappingCoversCallbackTaskErrors(t *testing.T) {
	if statusForError(domain.ErrUnknownTask) != http.StatusNotFound {
		t.Fatal("未知供应商任务应映射 404")
	}
	if statusForError(domain.ErrTaskAlreadyFinished) != http.StatusConflict {
		t.Fatal("已终结的生成任务应映射 409")
	}
	if msg := errorMessage(nil); msg != "" {
		t.Fatalf("没有错误时不应产生对外文案，实际 %q", msg)
	}
}

// TestPageLoginSubmitRejectsMalformedForm 覆盖登录表单解析失败的分支。
func TestPageLoginSubmitRejectsMalformedForm(t *testing.T) {
	h := newHarness(t)
	status, _, body := h.doRaw(t, http.MethodPost, "/login", "", "application/x-www-form-urlencoded", "login=%zz")
	if status != http.StatusBadRequest {
		t.Fatalf("表单解析失败应返回 400，实际 %d（%s）", status, body)
	}
	if !strings.Contains(body, "表单解析失败") {
		t.Fatalf("应提示表单解析失败: %s", body)
	}
}

// TestPageLoginRedirectsToWorkbenchWithoutNext 覆盖登录成功但未指定 next 的分支。
// 业务口径：没有指定跳转目标时回到需求工作台。
func TestPageLoginRedirectsToWorkbenchWithoutNext(t *testing.T) {
	h := newHarness(t)
	form := url.Values{"login": {"demand-a"}, "password": {testPassword}}
	status, location, body := h.doRaw(t, http.MethodPost, "/login", "", "application/x-www-form-urlencoded", form.Encode())
	if status != http.StatusSeeOther {
		t.Fatalf("登录成功应重定向，实际 %d（%s）", status, body)
	}
	if location != "/workbench" {
		t.Fatalf("未指定 next 时应跳转到工作台，实际 %q", location)
	}
}

// TestPageLogoutRejectsMalformedForm 覆盖退出登录时表单解析失败的分支。
func TestPageLogoutRejectsMalformedForm(t *testing.T) {
	h := newHarness(t)
	status, _, body := h.doRaw(t, http.MethodPost, "/logout", "demand-a", "application/x-www-form-urlencoded", "next=%zz")
	if status != http.StatusBadRequest {
		t.Fatalf("表单解析失败应返回 400，实际 %d（%s）", status, body)
	}
	if !strings.Contains(body, "表单解析失败") {
		t.Fatalf("应提示表单解析失败: %s", body)
	}
	// 会话仍然有效：表单解析失败不应提前注销会话。
	status, _, _ = h.doRaw(t, http.MethodGet, "/workbench", "demand-a", "application/json", "")
	if status != http.StatusOK {
		t.Fatalf("退出表单解析失败不应注销会话，实际 %d", status)
	}
}

// TestFormHandlersRejectMalformedForm 覆盖全部页面表单的「表单解析失败」分支。
// 业务口径：表单无法解析时返回 400，且不得改变需求状态。
func TestFormHandlersRejectMalformedForm(t *testing.T) {
	h := newHarness(t)
	rid := newRequirement(t, h, "demand-a", "表单解析失败用例")
	cases := []struct {
		name    string
		path    string
		account string
	}{
		{"创建需求", "/requirements", "demand-a"},
		{"修改描述", "/requirements/" + rid + "/description", "demand-a"},
		{"发起生成", "/requirements/" + rid + "/generations", "demand-a"},
		{"复核", "/requirements/" + rid + "/review", "review-a"},
		{"确认采用", "/requirements/" + rid + "/adopt", "demand-a"},
		{"提出重做", "/requirements/" + rid + "/rework", "demand-a"},
		{"撤销", "/requirements/" + rid + "/withdraw", "demand-a"},
		{"运营意见", "/requirements/" + rid + "/operator-note", "ops-a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, _, body := h.doRaw(t, http.MethodPost, c.path, c.account, "application/x-www-form-urlencoded", "field=%zz")
			if status != http.StatusBadRequest {
				t.Fatalf("表单解析失败应返回 400，实际 %d（%s）", status, body)
			}
			if !strings.Contains(body, "表单解析失败") {
				t.Fatalf("应提示表单解析失败: %s", body)
			}
		})
	}
	got, err := h.store.GetRequirement(t.Context(), rid)
	if err != nil {
		t.Fatalf("查询需求失败: %v", err)
	}
	if got.Status != domain.ReqDraft {
		t.Fatalf("表单解析失败不应改变需求状态，实际 %q", got.Status)
	}
}

// TestFormCreateRequirementRedirectsBusinessError 覆盖页面创建需求时的业务失败分支。
// 业务口径：参数不合法时跳回工作台并回显原因，且不得落库。
func TestFormCreateRequirementRedirectsBusinessError(t *testing.T) {
	h := newHarness(t)
	form := url.Values{"activity_name": {""}, "purpose": {"用途"}, "description": {"描述文本"}}
	status, location, body := h.doRaw(t, http.MethodPost, "/requirements", "demand-a",
		"application/x-www-form-urlencoded", form.Encode())
	if status != http.StatusSeeOther {
		t.Fatalf("创建失败应重定向，实际 %d（%s）", status, body)
	}
	target, err := url.Parse(location)
	if err != nil {
		t.Fatalf("解析重定向地址失败: %v（%q）", err, location)
	}
	if !strings.Contains(target.Query().Get("err"), "活动名称不能为空") {
		t.Fatalf("重定向应回显失败原因，实际 %q", target.Query().Get("err"))
	}
	reqs, _ := h.store.ListRequirements(t.Context(), storeFilter("demand-a"))
	if len(reqs) != 0 {
		t.Fatalf("创建失败不得落库，实际 %d 条", len(reqs))
	}
}

// TestFormStartGenerationFillsRequestNo 覆盖页面发起生成时未填请求编号的分支。
// 业务口径：请求编号缺失时由服务端生成，幂等键仍然落库且以 req- 开头。
func TestFormStartGenerationFillsRequestNo(t *testing.T) {
	h := newHarness(t)
	rid := newRequirement(t, h, "demand-a", "请求编号兜底用例")
	status, location, body := h.doRaw(t, http.MethodPost, "/requirements/"+rid+"/generations", "demand-a",
		"application/x-www-form-urlencoded", url.Values{}.Encode())
	if status != http.StatusSeeOther {
		t.Fatalf("表单发起生成应重定向，实际 %d（%s）", status, body)
	}
	if !strings.Contains(location, "ok=") {
		t.Fatalf("发起成功应带回成功提示，实际 %q", location)
	}
	tasks, err := h.store.ListTasks(t.Context(), rid)
	if err != nil {
		t.Fatalf("查询生成任务失败: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("应创建 1 个生成任务，实际 %d 个", len(tasks))
	}
	if !strings.HasPrefix(tasks[0].RequestNo, "req-") {
		t.Fatalf("未填请求编号时服务端应生成 req- 前缀编号，实际 %q", tasks[0].RequestNo)
	}
}

// TestRenderReturns500WhenTemplateFails 覆盖页面渲染失败的分支。
// 业务口径：模板渲染异常时返回 500 且不把内部细节透给页面。
func TestRenderReturns500WhenTemplateFails(t *testing.T) {
	tpl := template.Must(template.New("broken.html").Funcs(template.FuncMap{
		"explode": func() (string, error) { return "", errors.New("模板数据不可用") },
	}).Parse(`{{explode}}`))
	srv := &Server{templates: tpl}
	rec := newRecorder()
	req := httptest.NewRequest(http.MethodGet, "/broken", nil)
	srv.render(rec, req, "broken.html", "坏模板", &pageData{}, http.StatusOK)
	if rec.code != http.StatusInternalServerError {
		t.Fatalf("模板渲染失败应返回 500，实际 %d（%s）", rec.code, rec.body.String())
	}
	if !strings.Contains(rec.body.String(), "页面渲染失败") {
		t.Fatalf("应提示页面渲染失败: %s", rec.body.String())
	}
	if strings.Contains(rec.body.String(), "模板数据不可用") {
		t.Fatalf("不应透出模板内部细节: %s", rec.body.String())
	}
}
