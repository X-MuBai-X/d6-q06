// Package supplier 封装与「模拟生成供应商」的 HTTP 交互与回调签名。
// 供应商是可控的 HTTP 模拟服务，不代表真实第三方接入成果。
package supplier

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SubmitRequest 是提交给供应商的生成请求。
type SubmitRequest struct {
	// Ref 是平台侧的生成任务 ID，供应商在回调中原样带回。
	Ref string `json:"ref"`
	// Prompt 是发起当时的图片描述快照。
	Prompt string `json:"prompt"`
}

// SubmitResponse 是供应商对提交的明确答复（接受或拒绝）。
type SubmitResponse struct {
	Accepted bool   `json:"accepted"`
	TaskID   string `json:"task_id"`
	Reason   string `json:"reason"`
}

// Client 是供应商 HTTP 客户端。
type Client struct {
	baseURL string
	secret  string
	http    *http.Client
}

// NewClient 创建供应商客户端。timeout 控制单次提交的最长等待时间。
func NewClient(baseURL, secret string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		secret:  secret,
		http:    &http.Client{Timeout: timeout},
	}
}

// Submit 向供应商提交一次生成任务。
// 供应商明确返回接受或拒绝；网络异常以 error 返回，由业务层按「提交失败」记录。
func (c *Client) Submit(ctx context.Context, req SubmitRequest) (SubmitResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return SubmitResponse{}, fmt.Errorf("序列化提交请求失败: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/tasks", bytes.NewReader(body))
	if err != nil {
		return SubmitResponse{}, fmt.Errorf("构造提交请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return SubmitResponse{}, fmt.Errorf("调用供应商失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return SubmitResponse{}, fmt.Errorf("读取供应商响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return SubmitResponse{}, fmt.Errorf("供应商返回状态码 %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out SubmitResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return SubmitResponse{}, fmt.Errorf("解析供应商响应失败: %w", err)
	}
	return out, nil
}

// Signature 计算回调签名：HMAC-SHA256(secret, body) 的十六进制小写形式。
func Signature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature 校验回调签名。
func VerifySignature(secret string, body []byte, got string) bool {
	if got == "" {
		return false
	}
	expected := Signature(secret, body)
	return hmac.Equal([]byte(expected), []byte(strings.ToLower(got)))
}
