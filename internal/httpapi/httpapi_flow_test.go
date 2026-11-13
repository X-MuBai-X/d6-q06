package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"creative-studio/internal/domain"
	"creative-studio/internal/store"
	"creative-studio/internal/supplier"
)

// signOf 计算回调签名。
func signOf(secret, payload string) string {
	return supplier.Signature(secret, []byte(payload))
}

// storeFilter 构造按需求人过滤的查询条件。
func storeFilter(owner string) store.RequirementFilter {
	return store.RequirementFilter{OwnerID: owner}
}

// readBodyString 读取响应体文本。
func readBodyString(t *testing.T, resp *http.Response) string {
	t.Helper()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体失败: %v", err)
	}
	return string(raw)
}

// TestFullBusinessFlowOverAPI 覆盖「创建 → 生成 → 回调 → 复核 → 采用」全链路的接口分支。
func TestFullBusinessFlowOverAPI(t *testing.T) {
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
	_ = json.Unmarshal([]byte(body), &created)
	rid := created.Requirement.ID

	// 修改描述
	status, body = h.do(t, http.MethodPost, "/api/requirements/"+rid+"/description", "demand-a",
		map[string]any{"description": "改成冷色金属质感"})
	if status != http.StatusOK {
		t.Fatalf("修改描述失败: %d %s", status, body)
	}
	// 发起生成
	status, body = h.do(t, http.MethodPost, "/api/requirements/"+rid+"/generations", "demand-a",
		map[string]any{"request_no": "r-1"})
	if status != http.StatusAccepted {
		t.Fatalf("发起生成失败: %d %s", status, body)
	}
	// 供应商回调（正确签名）
	payload := callbackPayload(t, h, rid, "r-1")
	req, _ := http.NewRequest(http.MethodPost, h.server.URL+"/api/supplier/callback", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Supplier-Signature", signOf(h.secret, payload))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("回调失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("回调应返回 200，实际 %d", resp.StatusCode)
	}

	// 复核队列（复核前需求处于待复核状态）
	status, body = h.do(t, http.MethodGet, "/api/review-queue", "review-a", nil)
	if status != http.StatusOK || !strings.Contains(body, rid) {
		t.Fatalf("复核队列不正确: %d %s", status, body)
	}
	// 需求人无权查看复核队列
	status, _ = h.do(t, http.MethodGet, "/api/review-queue", "demand-a", nil)
	if status != http.StatusForbidden {
		t.Fatalf("需求人查看复核队列应 403，实际 %d", status)
	}
	// 复核通过
	status, body = h.do(t, http.MethodPost, "/api/requirements/"+rid+"/review", "review-a",
		map[string]any{"approved": true, "reason": "构图符合文案"})
	if status != http.StatusOK {
		t.Fatalf("复核失败: %d %s", status, body)
	}
	// 确认采用
	status, body = h.do(t, http.MethodPost, "/api/requirements/"+rid+"/adopt", "demand-a", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("确认采用失败: %d %s", status, body)
	}
	// 运营总览与异常查询
	status, body = h.do(t, http.MethodGet, "/api/overview", "ops-a", nil)
	if status != http.StatusOK || !strings.Contains(body, "delivered") {
		t.Fatalf("运营总览不正确: %d %s", status, body)
	}
	status, body = h.do(t, http.MethodGet, "/api/anomalies", "ops-a", nil)
	if status != http.StatusOK {
		t.Fatalf("异常查询失败: %d %s", status, body)
	}
	status, _ = h.do(t, http.MethodGet, "/api/anomalies", "demand-a", nil)
	if status != http.StatusForbidden {
		t.Fatalf("需求人查看异常应 403，实际 %d", status)
	}
	status, _ = h.do(t, http.MethodGet, "/api/overview", "demand-a", nil)
	if status != http.StatusForbidden {
		t.Fatalf("需求人查看总览应 403，实际 %d", status)
	}
	// 已交付后不可撤销
	status, _ = h.do(t, http.MethodPost, "/api/requirements/"+rid+"/withdraw", "demand-a", map[string]any{})
	if status != http.StatusConflict {
		t.Fatalf("已交付撤销应 409，实际 %d", status)
	}
	// 运营意见
	status, body = h.do(t, http.MethodPost, "/api/requirements/"+rid+"/operator-note", "ops-a",
		map[string]any{"note": "已跟进供应商"})
	if status != http.StatusOK {
		t.Fatalf("运营意见失败: %d %s", status, body)
	}
	status, _ = h.do(t, http.MethodPost, "/api/requirements/"+rid+"/operator-note", "demand-a",
		map[string]any{"note": "需求人不该能记录"})
	if status != http.StatusForbidden {
		t.Fatalf("需求人记录运营意见应 403，实际 %d", status)
	}
}

// TestReworkFlowOverAPI 覆盖「复核通过但需求人不采用 → 提出重做原因」。
func TestReworkFlowOverAPI(t *testing.T) {
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
	_ = json.Unmarshal([]byte(body), &created)
	rid := created.Requirement.ID

	h.do(t, http.MethodPost, "/api/requirements/"+rid+"/generations", "demand-a", map[string]any{"request_no": "r-1"})
	payload := callbackPayload(t, h, rid, "r-1")
	sendCallback(t, h, payload)

	status, body = h.do(t, http.MethodPost, "/api/requirements/"+rid+"/review", "review-a",
		map[string]any{"approved": true, "reason": "通过"})
	if status != http.StatusOK {
		t.Fatalf("复核失败: %d %s", status, body)
	}
	// 缺少原因的重做请求
	status, _ = h.do(t, http.MethodPost, "/api/requirements/"+rid+"/rework", "demand-a", map[string]any{"reason": ""})
	if status != http.StatusBadRequest {
		t.Fatalf("空重做原因应 400，实际 %d", status)
	}
	status, body = h.do(t, http.MethodPost, "/api/requirements/"+rid+"/rework", "demand-a",
		map[string]any{"reason": "图片不符合文案"})
	if status != http.StatusOK {
		t.Fatalf("提出重做失败: %d %s", status, body)
	}
	// 复核人无权提出重做
	status, _ = h.do(t, http.MethodPost, "/api/requirements/"+rid+"/rework", "review-a",
		map[string]any{"reason": "复核人不该能重做"})
	if status != http.StatusForbidden {
		t.Fatalf("复核人提出重做应 403，实际 %d", status)
	}
	// 页面表单通道
	status, body = h.doPage(t, http.MethodPost, "/requirements/"+rid+"/rework", "demand-a",
		url.Values{"reason": {"页面通道提出的重做原因"}})
	if status != http.StatusSeeOther {
		t.Fatalf("表单重做应重定向，实际 %d %s", status, body)
	}
}

// TestPageLoginAndRequirementRendering 覆盖登录页提交与需求详情页渲染。
func TestPageLoginAndRequirementRendering(t *testing.T) {
	h := newHarness(t)
	resp := h.doPageResp(t, http.MethodPost, "/login", "", url.Values{
		"login": {"demand-a"}, "password": {"Studio@2026"}, "next": {"/workbench"},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("登录应重定向，实际 %d", resp.StatusCode)
	}
	if len(resp.Cookies()) == 0 {
		t.Fatal("登录应写入会话 Cookie")
	}
	// 登录失败
	respBad := h.doPageResp(t, http.MethodPost, "/login", "", url.Values{
		"login": {"demand-a"}, "password": {"bad"},
	})
	defer func() { _ = respBad.Body.Close() }()
	if respBad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误口令应返回 401，实际 %d", respBad.StatusCode)
	}
	if !strings.Contains(readBodyString(t, respBad), "账号或口令不正确") {
		t.Fatalf("登录失败应展示原因")
	}

	// 创建需求后打开详情页
	status, body := h.doPage(t, http.MethodPost, "/requirements", "demand-a", url.Values{
		"activity_name": {"夏日活动"}, "purpose": {"新品主视觉"}, "description": {"冷色调冰饮主视觉"},
	})
	if status != http.StatusSeeOther {
		t.Fatalf("创建需求应重定向，实际 %d %s", status, body)
	}
	reqs, _ := h.store.ListRequirements(t.Context(), storeFilter("demand-a"))
	if len(reqs) != 1 {
		t.Fatalf("应创建 1 条需求，实际 %d", len(reqs))
	}
	status, body = h.doPage(t, http.MethodGet, "/requirements/"+reqs[0].ID, "demand-a", nil)
	if status != http.StatusOK {
		t.Fatalf("详情页渲染失败: %d %s", status, body)
	}
	for _, want := range []string{"当前候选", "历史候选", "生成历史", "审计时间线"} {
		if !strings.Contains(body, want) {
			t.Fatalf("详情页缺少区块：%s", want)
		}
	}
	// 他人访问详情页
	status, _ = h.doPage(t, http.MethodGet, "/requirements/"+reqs[0].ID, "demand-b", nil)
	if status != http.StatusForbidden {
		t.Fatalf("他人访问详情页应 403，实际 %d", status)
	}
	// 不存在的需求
	status, _ = h.doPage(t, http.MethodGet, "/requirements/req-missing", "demand-a", nil)
	if status != http.StatusNotFound {
		t.Fatalf("不存在的需求应 404，实际 %d", status)
	}
	// 复核页无权限
	status, _ = h.doPage(t, http.MethodGet, "/review", "demand-a", nil)
	if status != http.StatusForbidden {
		t.Fatalf("需求人访问复核页应 403，实际 %d", status)
	}
	// 运营页无权限
	status, _ = h.doPage(t, http.MethodGet, "/ops", "demand-a", nil)
	if status != http.StatusForbidden {
		t.Fatalf("需求人访问运营页应 403，实际 %d", status)
	}
	// 演示图片
	status, body = h.doPage(t, http.MethodGet, "/static/img/gen-x.svg", "", nil)
	if status != http.StatusOK || !strings.Contains(body, "svg") {
		t.Fatalf("演示图片应可访问: %d", status)
	}
	status, _ = h.doPage(t, http.MethodGet, "/static/img/bad name.svg", "", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("非法图片名应 400，实际 %d", status)
	}
}

// TestFormAdoptAndWithdraw 覆盖页面通道的确认采用与撤销。
func TestFormAdoptAndWithdraw(t *testing.T) {
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
	_ = json.Unmarshal([]byte(body), &created)
	rid := created.Requirement.ID

	h.do(t, http.MethodPost, "/api/requirements/"+rid+"/generations", "demand-a", map[string]any{"request_no": "r-1"})
	sendCallback(t, h, callbackPayload(t, h, rid, "r-1"))
	h.do(t, http.MethodPost, "/api/requirements/"+rid+"/review", "review-a", map[string]any{"approved": true, "reason": "通过"})

	resp := h.doPageResp(t, http.MethodPost, "/requirements/"+rid+"/adopt", "demand-a", url.Values{})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("确认采用应重定向，实际 %d", resp.StatusCode)
	}
	got, _ := h.store.GetRequirement(t.Context(), rid)
	if got.Status != domain.ReqDelivered {
		t.Fatalf("确认采用后状态应为 delivered，实际 %q", got.Status)
	}

	// 另一个需求走撤销通道
	status, body = h.do(t, http.MethodPost, "/api/requirements", "demand-a", map[string]any{
		"activity_name": "另一个活动", "purpose": "用途", "description": "另一个描述文本",
	})
	if status != http.StatusCreated {
		t.Fatalf("创建需求失败: %d %s", status, body)
	}
	_ = json.Unmarshal([]byte(body), &created)
	resp2 := h.doPageResp(t, http.MethodPost, "/requirements/"+created.Requirement.ID+"/withdraw", "demand-a", url.Values{})
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusSeeOther {
		t.Fatalf("撤销应重定向，实际 %d", resp2.StatusCode)
	}
	got2, _ := h.store.GetRequirement(t.Context(), created.Requirement.ID)
	if got2.Status != domain.ReqWithdrawn {
		t.Fatalf("撤销后状态应为 withdrawn，实际 %q", got2.Status)
	}
}

// callbackPayload 依据已生成的供应商任务号构造回调内容。
func callbackPayload(t *testing.T, h *harness, rid, requestNo string) string {
	t.Helper()
	task, err := h.store.GetTaskByRequestNo(t.Context(), requestNo)
	if err != nil {
		t.Fatalf("读取生成任务失败: %v", err)
	}
	return `{"task_id":"` + task.SupplierTaskID + `","status":"succeeded","image_url":"/static/img/a.svg"}`
}

// sendCallback 发送一条合法签名的供应商回调。
func sendCallback(t *testing.T, h *harness, payload string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, h.server.URL+"/api/supplier/callback", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Supplier-Signature", signOf(h.secret, payload))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("回调失败: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("回调应返回 200，实际 %d", resp.StatusCode)
	}
}
