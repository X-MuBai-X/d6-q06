package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestMock 创建一个指向测试平台的模拟供应商。
func newTestMock(t *testing.T, callbackURL string) *mock {
	t.Helper()
	return newMock(callbackURL, "test-secret")
}

func TestHandleSubmitAccepts(t *testing.T) {
	m := newTestMock(t, "http://127.0.0.1:1/callback")
	body := bytes.NewReader([]byte(`{"ref":"task-1","prompt":"冷色调冰饮"}`))
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", body)
	rec := httptest.NewRecorder()
	m.handleSubmit(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("受理应返回 202，实际 %d", rec.Code)
	}
	var out struct {
		Accepted bool   `json:"accepted"`
		TaskID   string `json:"task_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if !out.Accepted || out.TaskID == "" {
		t.Fatalf("受理结果不正确: %+v", out)
	}
	m.mu.Lock()
	task, ok := m.tasks["task-1"]
	m.mu.Unlock()
	if !ok || task.Prompt != "冷色调冰饮" {
		t.Fatalf("任务未记录: %+v", task)
	}
}

func TestHandleSubmitReject(t *testing.T) {
	m := newTestMock(t, "http://127.0.0.1:1/callback")
	m.setMode(modeReject)
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader([]byte(`{"ref":"task-2","prompt":"x"}`)))
	rec := httptest.NewRecorder()
	m.handleSubmit(rec, req)

	var out struct {
		Accepted bool   `json:"accepted"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if out.Accepted || out.Reason == "" {
		t.Fatalf("拒绝结果不正确: %+v", out)
	}
}

func TestHandleSubmitBadBody(t *testing.T) {
	m := newTestMock(t, "http://127.0.0.1:1/callback")
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader([]byte("not-json")))
	rec := httptest.NewRecorder()
	m.handleSubmit(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法请求应返回 400，实际 %d", rec.Code)
	}
}

func TestHandleSubmitTimeoutModeDoesNotCallBack(t *testing.T) {
	m := newTestMock(t, "http://127.0.0.1:1/callback")
	m.setMode(modeTimeout)
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader([]byte(`{"ref":"task-3","prompt":"x"}`)))
	rec := httptest.NewRecorder()
	m.handleSubmit(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("超时模式仍应受理，实际 %d", rec.Code)
	}
}

func TestHandleMode(t *testing.T) {
	m := newTestMock(t, "http://127.0.0.1:1/callback")
	req := httptest.NewRequest(http.MethodPost, "/control/mode", bytes.NewReader([]byte(`{"mode":"failure","delay_ms":50,"duplicate":true}`)))
	rec := httptest.NewRecorder()
	m.handleMode(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("设置模式应返回 200，实际 %d", rec.Code)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mode != modeFailure || !m.duplicate || m.delay != 50*time.Millisecond {
		t.Fatalf("模式未生效: %+v", m)
	}
}

func TestHandleModeBadBody(t *testing.T) {
	m := newTestMock(t, "http://127.0.0.1:1/callback")
	req := httptest.NewRequest(http.MethodPost, "/control/mode", bytes.NewReader([]byte("not-json")))
	rec := httptest.NewRecorder()
	m.handleMode(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法请求应返回 400，实际 %d", rec.Code)
	}
}

func TestHandleRedeliver(t *testing.T) {
	received := make(chan string, 4)
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Get("X-Supplier-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer platform.Close()

	m := newTestMock(t, platform.URL)
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", bytes.NewReader([]byte(`{"ref":"task-4","prompt":"x"}`)))
	m.handleSubmit(httptest.NewRecorder(), req)

	rec := httptest.NewRecorder()
	m.handleRedeliver(rec, httptest.NewRequest(http.MethodPost, "/control/redeliver",
		bytes.NewReader([]byte(`{"ref":"task-4","status":"succeeded"}`))))
	if rec.Code != http.StatusOK {
		t.Fatalf("重新投递应返回 200，实际 %d", rec.Code)
	}
	select {
	case sig := <-received:
		if sig == "" {
			t.Fatal("回调应带签名")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未收到回调")
	}
}

func TestHandleRedeliverUnknownTask(t *testing.T) {
	m := newTestMock(t, "http://127.0.0.1:1/callback")
	rec := httptest.NewRecorder()
	m.handleRedeliver(rec, httptest.NewRequest(http.MethodPost, "/control/redeliver",
		bytes.NewReader([]byte(`{"ref":"missing"}`))))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知任务应返回 404，实际 %d", rec.Code)
	}
}

func TestHandleRedeliverBadBody(t *testing.T) {
	m := newTestMock(t, "http://127.0.0.1:1/callback")
	rec := httptest.NewRecorder()
	m.handleRedeliver(rec, httptest.NewRequest(http.MethodPost, "/control/redeliver",
		bytes.NewReader([]byte("not-json"))))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法请求应返回 400，实际 %d", rec.Code)
	}
}

func TestDeliverSignsCallback(t *testing.T) {
	var body []byte
	var signature string
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = readAll(r)
		signature = r.Header.Get("X-Supplier-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer platform.Close()

	m := newTestMock(t, platform.URL)
	m.deliver("sup-1", "succeeded", "/static/img/a.svg", "")
	if len(body) == 0 || signature == "" {
		t.Fatalf("回调应带内容与签名: %q %q", body, signature)
	}
	if !strings.Contains(string(body), "sup-1") {
		t.Fatalf("回调内容不正确: %s", body)
	}
}

func TestRunStartsAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, config{addr: "127.0.0.1:18099", callbackURL: "http://127.0.0.1:1/callback", secret: "s"})
	}()
	time.Sleep(300 * time.Millisecond)
	resp, err := http.Get("http://127.0.0.1:18099/healthz")
	if err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("健康检查应返回 200，实际 %d", resp.StatusCode)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("正常退出不应报错: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("服务未在预期时间内退出")
	}
}

// readAll 读取请求体。
func readAll(r *http.Request) ([]byte, error) {
	defer func() { _ = r.Body.Close() }()
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}
