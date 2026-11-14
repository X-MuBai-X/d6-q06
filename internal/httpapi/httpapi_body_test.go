package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"creative-studio/internal/domain"
)

// countingReader 统计被读取的字节数，用于断言处理器确实把请求体读干净。
type countingReader struct {
	r    io.Reader
	read int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += n
	return n, err
}

// newCountingRequest 构造一个带计数请求体的请求。
func newCountingRequest(method, target, body string) (*http.Request, *countingReader) {
	counter := &countingReader{r: strings.NewReader(body)}
	req := httptest.NewRequest(method, target, counter)
	return req, counter
}

// newRequirement 创建一个属于 account 的需求，返回需求编号。
func newRequirement(t *testing.T, h *harness, account, name string) string {
	t.Helper()
	status, body := h.do(t, http.MethodPost, "/api/requirements", account, map[string]any{
		"activity_name": name, "purpose": "回归测试", "description": "用于请求体读取回归测试的描述文本",
	})
	if status != http.StatusCreated {
		t.Fatalf("创建需求失败: %d %s", status, body)
	}
	var created struct {
		Requirement domain.Requirement `json:"requirement"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("解析需求失败: %v", err)
	}
	return created.Requirement.ID
}

// TestAPIDrawerHandlerConsumesRequestBody 断言撤销接口自己就把请求体读干净。
// 修复前 apiWithdraw 从不读取请求体，服务端关闭连接时客户端会偶发收到 TCP 复位。
func TestAPIDrawerHandlerConsumesRequestBody(t *testing.T) {
	h := newHarness(t)
	rid := newRequirement(t, h, "demand-a", "撤销接口请求体读取")
	srv, err := New(h.svc, h.secret)
	if err != nil {
		t.Fatalf("创建 HTTP 服务失败: %v", err)
	}
	user, err := h.svc.Authenticate(t.Context(), h.tokens["demand-a"])
	if err != nil {
		t.Fatalf("取当前身份失败: %v", err)
	}
	req, counter := newCountingRequest(http.MethodPost, "/api/requirements/"+rid+"/withdraw", "{}")
	req.SetPathValue("id", rid)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, user))
	rec := httptest.NewRecorder()
	srv.apiWithdraw(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("撤销应返回 200，实际 %d（%s）", rec.Code, rec.Body.String())
	}
	if counter.read != len("{}") {
		t.Fatalf("撤销接口必须读干请求体，实际读取 %d 字节", counter.read)
	}
}

// TestFormWithdrawHandlerConsumesRequestBody 断言页面撤销表单会解析（读干）请求体。
func TestFormWithdrawHandlerConsumesRequestBody(t *testing.T) {
	h := newHarness(t)
	rid := newRequirement(t, h, "demand-a", "表单撤销请求体读取")
	srv, err := New(h.svc, h.secret)
	if err != nil {
		t.Fatalf("创建 HTTP 服务失败: %v", err)
	}
	user, err := h.svc.Authenticate(t.Context(), h.tokens["demand-a"])
	if err != nil {
		t.Fatalf("取当前身份失败: %v", err)
	}
	req, counter := newCountingRequest(http.MethodPost, "/requirements/"+rid+"/withdraw", "note=%E5%B7%B2%E6%92%A4%E9%94%80")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", rid)
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, user))
	rec := httptest.NewRecorder()
	srv.formWithdraw(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("表单撤销应重定向，实际 %d（%s）", rec.Code, rec.Body.String())
	}
	if counter.read == 0 {
		t.Fatal("表单撤销必须解析请求体")
	}
}

// TestWriteHandlersConsumeRequestBody 覆盖全部写接口：请求体必须被读完，
// 否则 net/http 关闭连接时会向客户端发送 TCP 复位（evidence/independent-test-report.md D-03）。
func TestWriteHandlersConsumeRequestBody(t *testing.T) {
	h := newHarness(t)
	rid := newRequirement(t, h, "demand-a", "写接口请求体读取")
	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"创建需求", http.MethodPost, "/api/requirements", `{"activity_name":"写接口用例","purpose":"用途","description":"写接口用例的描述文本"}`},
		{"修改描述", http.MethodPost, "/api/requirements/" + rid + "/description", `{"description":"写接口用例改过的描述文本"}`},
		{"发起生成", http.MethodPost, "/api/requirements/" + rid + "/generations", `{"request_no":"body-drain-1"}`},
		{"发起生成（空体）", http.MethodPost, "/api/requirements/" + rid + "/generations", `{}`},
		{"复核", http.MethodPost, "/api/requirements/" + rid + "/review", `{"approved":false,"reason":"复核请求体用例"}`},
		{"确认采用", http.MethodPost, "/api/requirements/" + rid + "/adopt", `{}`},
		{"提出重做", http.MethodPost, "/api/requirements/" + rid + "/rework", `{"reason":"重做请求体用例"}`},
		{"撤销", http.MethodPost, "/api/requirements/" + rid + "/withdraw", `{}`},
		{"运营意见", http.MethodPost, "/api/requirements/" + rid + "/operator-note", `{"note":"运营意见请求体用例"}`},
		{"登录", http.MethodPost, "/api/login", `{"login":"demand-a","password":"` + testPassword + `"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, counter := newCountingRequest(c.method, c.path, c.body)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+h.tokens["demand-a"])
			rec := httptest.NewRecorder()
			h.server.Config.Handler.ServeHTTP(rec, req)
			if rec.Code == 0 {
				t.Fatalf("写接口未返回状态码")
			}
			if counter.read != len(c.body) {
				t.Fatalf("写接口必须读干请求体，实际读取 %d 字节，期望 %d 字节", counter.read, len(c.body))
			}
		})
	}
}

// TestWithdrawWithRequestBodyOverHTTPIsStable 连续 40 次带空请求体调用撤销接口，
// 每次都必须稳定拿到 200 或 4xx，不得出现连接错误（TCP 复位）。
func TestWithdrawWithRequestBodyOverHTTPIsStable(t *testing.T) {
	h := newHarness(t)
	rid := newRequirement(t, h, "demand-a", "撤销接口稳定性")
	client := &http.Client{Timeout: 10 * time.Second}
	for i := 0; i < 40; i++ {
		req, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/requirements/"+rid+"/withdraw",
			strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("构造请求失败: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+h.tokens["demand-a"])
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("第 %d 次带体撤销出现连接错误: %v", i+1, err)
		}
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusConflict {
			t.Fatalf("第 %d 次带体撤销应返回 200 或 409，实际 %d（%s）", i+1, resp.StatusCode, raw)
		}
	}
	got, err := h.store.GetRequirement(t.Context(), rid)
	if err != nil {
		t.Fatalf("查询需求失败: %v", err)
	}
	if got.Status != domain.ReqWithdrawn {
		t.Fatalf("撤销后状态应为 withdrawn，实际 %q", got.Status)
	}
}

// TestWriteEndpointsWithRequestBodyOverHTTPIsStable 对其它写接口做同类断言：
// 带请求体调用不得出现连接错误。
func TestWriteEndpointsWithRequestBodyOverHTTPIsStable(t *testing.T) {
	h := newHarness(t)
	rid := newRequirement(t, h, "demand-a", "其它写接口稳定性")
	client := &http.Client{Timeout: 10 * time.Second}
	paths := []string{
		"/api/requirements/" + rid + "/description",
		"/api/requirements/" + rid + "/generations",
		"/api/requirements/" + rid + "/review",
		"/api/requirements/" + rid + "/adopt",
		"/api/requirements/" + rid + "/rework",
		"/api/requirements/" + rid + "/operator-note",
		"/api/requirements/" + rid + "/withdraw",
	}
	for i := 0; i < 5; i++ {
		for _, p := range paths {
			req, err := http.NewRequest(http.MethodPost, h.server.URL+p, strings.NewReader("{}"))
			if err != nil {
				t.Fatalf("构造请求失败: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+h.tokens["demand-a"])
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s 第 %d 轮出现连接错误: %v", p, i+1, err)
			}
			raw, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 500 {
				t.Fatalf("%s 第 %d 轮应返回 2xx 或 4xx，实际 %d（%s）", p, i+1, resp.StatusCode, raw)
			}
		}
	}
}
