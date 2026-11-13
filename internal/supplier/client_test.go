package supplier

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSubmitAccepted(t *testing.T) {
	var received SubmitRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("解析请求失败: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"accepted":true,"task_id":"sup-1"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "secret", 2*time.Second)
	resp, err := c.Submit(context.Background(), SubmitRequest{Ref: "task-1", Prompt: "描述"})
	if err != nil {
		t.Fatalf("提交不应出错: %v", err)
	}
	if !resp.Accepted || resp.TaskID != "sup-1" {
		t.Fatalf("提交结果不正确: %+v", resp)
	}
	if received.Ref != "task-1" || received.Prompt != "描述" {
		t.Fatalf("供应商收到的请求不正确: %+v", received)
	}
}

func TestSubmitRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"accepted":false,"reason":"配额不足"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "secret", 2*time.Second)
	resp, err := c.Submit(context.Background(), SubmitRequest{Ref: "task-1", Prompt: "描述"})
	if err != nil {
		t.Fatalf("提交不应出错: %v", err)
	}
	if resp.Accepted {
		t.Fatal("供应商明确拒绝时 Accepted 应为 false")
	}
	if resp.Reason != "配额不足" {
		t.Fatalf("拒绝原因不正确: %q", resp.Reason)
	}
}

func TestSubmitHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("内部错误"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "secret", 2*time.Second)
	if _, err := c.Submit(context.Background(), SubmitRequest{}); err == nil {
		t.Fatal("供应商返回 5xx 时应报错")
	}
}

func TestSubmitBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not-json"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "secret", 2*time.Second)
	if _, err := c.Submit(context.Background(), SubmitRequest{}); err == nil {
		t.Fatal("响应不是合法 JSON 时应报错")
	}
}

func TestSubmitNetworkError(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "secret", 500*time.Millisecond)
	if _, err := c.Submit(context.Background(), SubmitRequest{}); err == nil {
		t.Fatal("供应商不可达时应报错")
	}
}

func TestSignatureAndVerify(t *testing.T) {
	body := []byte(`{"task_id":"sup-1","status":"succeeded"}`)
	sig := Signature("secret", body)
	if sig == "" {
		t.Fatal("签名不应为空")
	}
	if !VerifySignature("secret", body, sig) {
		t.Fatal("正确签名应校验通过")
	}
	if VerifySignature("secret", body, sig+"00") {
		t.Fatal("被篡改的签名不应通过")
	}
	if VerifySignature("other", body, sig) {
		t.Fatal("错误密钥不应通过")
	}
	if VerifySignature("secret", body, "") {
		t.Fatal("空签名不应通过")
	}
	if !VerifySignature("secret", body, strings.ToUpper(sig)) {
		t.Fatal("大写十六进制签名应可通过")
	}
}
