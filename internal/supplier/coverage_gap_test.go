package supplier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 本文件补齐 internal/supplier 中尚未被覆盖的代码块：客户端参数兜底、
// 非法请求地址与响应体不完整三类真实失败场景。

// TestGapNewClientFallsBackToDefaultTimeout 覆盖超时参数非法时的兜底。
// 业务含义：调用方未给出有效超时（<=0）时，客户端必须回落到 10 秒，
// 否则一次提交可能无限等待。
func TestGapNewClientFallsBackToDefaultTimeout(t *testing.T) {
	c := NewClient("http://supplier.example", "secret", 0)
	if c.http.Timeout != 10*time.Second {
		t.Fatalf("非正超时应回落到 10 秒，实际 %v", c.http.Timeout)
	}
	negative := NewClient("http://supplier.example", "secret", -time.Second)
	if negative.http.Timeout != 10*time.Second {
		t.Fatalf("负超时应回落到 10 秒，实际 %v", negative.http.Timeout)
	}
}

// TestGapSubmitWithInvalidBaseURL 覆盖请求地址非法时的失败。
// 业务含义：配置错误必须在提交前被识别并给出「构造提交请求失败」的明确原因，
// 而不是把脏地址发到网络上。
func TestGapSubmitWithInvalidBaseURL(t *testing.T) {
	c := NewClient("http://exa mple.test", "secret", time.Second)
	_, err := c.Submit(context.Background(), SubmitRequest{Ref: "task-1", Prompt: "描述"})
	if err == nil {
		t.Fatal("非法基础地址应返回错误")
	}
	if !strings.Contains(err.Error(), "构造提交请求失败") {
		t.Fatalf("错误信息应说明是构造请求失败，实际 %v", err)
	}
}

// TestGapSubmitWithTruncatedBody 覆盖响应体不完整时的失败。
// 业务含义：供应商中途断开连接时，本次提交结果属于「未知」，
// 客户端必须以「读取供应商响应失败」返回，由业务层按提交失败收口。
func TestGapSubmitWithTruncatedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("测试服务器不支持连接劫持")
			return
		}
		conn, buf, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("劫持连接失败: %v", err)
			return
		}
		// 声明 64 字节长度但只写一部分，然后直接断开连接。
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 64\r\n\r\n{\"accepted\":true")
		_ = buf.Flush()
		_ = conn.Close()
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "secret", 2*time.Second)
	_, err := c.Submit(context.Background(), SubmitRequest{Ref: "task-1", Prompt: "描述"})
	if err == nil {
		t.Fatal("响应体不完整时应返回错误")
	}
	if !strings.Contains(err.Error(), "读取供应商响应失败") {
		t.Fatalf("错误信息应说明是读取响应失败，实际 %v", err)
	}
}
