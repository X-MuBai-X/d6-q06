// Command mockgen 是可控的 HTTP 模拟生成供应商。
// 它用于触发题面要求的成功、失败、拒绝、超时与重复回调场景；
// 它是教学用的模拟服务，不代表真实第三方接入成果。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"creative-studio/internal/supplier"
)

// mode 是模拟供应商对下一次提交的行为。
type mode string

const (
	modeSuccess mode = "success"
	modeFailure mode = "failure"
	modeReject  mode = "reject"
	modeTimeout mode = "timeout"
)

// mockTask 是模拟供应商保存的一条任务。
type mockTask struct {
	Ref     string
	Prompt  string
	TaskID  string
	Mode    mode
	Created time.Time
}

// mock 是模拟供应商的运行时状态。
type mock struct {
	mu        sync.Mutex
	tasks     map[string]*mockTask
	mode      mode
	delay     time.Duration
	duplicate bool
	callback  string
	secret    string
}

// newMock 创建模拟供应商实例。
func newMock(callbackURL, secret string) *mock {
	return &mock{
		tasks:    map[string]*mockTask{},
		mode:     modeSuccess,
		delay:    300 * time.Millisecond,
		callback: callbackURL,
		secret:   secret,
	}
}

// config 是模拟供应商的运行参数。
type config struct {
	addr        string
	callbackURL string
	secret      string
}

func main() {
	var cfg config
	flag.StringVar(&cfg.addr, "addr", ":8090", "监听地址")
	flag.StringVar(&cfg.callbackURL, "callback-url", "http://127.0.0.1:8080/api/supplier/callback", "回调平台地址")
	flag.StringVar(&cfg.secret, "secret", "demo-supplier-secret", "回调签名密钥")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		log.Fatalf("模拟供应商退出: %v", err)
	}
}

// run 启动模拟供应商并阻塞直到上下文结束。
func run(ctx context.Context, cfg config) error {
	m := newMock(cfg.callbackURL, cfg.secret)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/tasks", m.handleSubmit)
	mux.HandleFunc("POST /control/mode", m.handleMode)
	mux.HandleFunc("POST /control/redeliver", m.handleRedeliver)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	server := &http.Server{Addr: cfg.addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("模拟生成供应商已启动，地址 http://127.0.0.1%s，回调目标 %s", cfg.addr, cfg.callbackURL)
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// setMode 设置下一次提交的行为模式。
func (m *mock) setMode(next mode) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode = next
}

// handleSubmit 受理生成任务并按当前模式决定后续行为。
func (m *mock) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Ref    string `json:"ref"`
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"accepted": false, "reason": "请求内容不合法"})
		return
	}
	m.mu.Lock()
	current := m.mode
	delay := m.delay
	duplicate := m.duplicate
	taskID := fmt.Sprintf("sup-%d", time.Now().UnixNano())
	m.tasks[in.Ref] = &mockTask{Ref: in.Ref, Prompt: in.Prompt, TaskID: taskID, Mode: current, Created: time.Now()}
	m.mu.Unlock()

	if current == modeReject {
		log.Printf("受理被拒绝：ref=%s", in.Ref)
		writeJSON(w, http.StatusOK, map[string]any{"accepted": false, "reason": "模拟供应商本次拒绝受理"})
		return
	}
	log.Printf("已受理：ref=%s mode=%s task=%s", in.Ref, current, taskID)
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "task_id": taskID})

	if current == modeTimeout {
		// 超时场景：故意不回调，由平台侧的超时巡检判定。
		return
	}
	go func() {
		time.Sleep(delay)
		status := "succeeded"
		reason := ""
		if current == modeFailure {
			status = "failed"
			reason = "模拟供应商生成失败"
		}
		imageURL := "/static/img/gen-" + sanitize(in.Ref) + ".svg"
		m.deliver(taskID, status, imageURL, reason)
		if duplicate {
			// 重复通知场景：同一结果再发一次，平台必须忽略。
			time.Sleep(200 * time.Millisecond)
			m.deliver(taskID, status, imageURL, reason)
		}
	}()
}

// handleMode 设置下一次提交的行为模式。
func (m *mock) handleMode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Mode      string `json:"mode"`
		DelayMS   int    `json:"delay_ms"`
		Duplicate bool   `json:"duplicate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "请求内容不合法"})
		return
	}
	m.mu.Lock()
	m.mode = mode(in.Mode)
	if in.DelayMS > 0 {
		m.delay = time.Duration(in.DelayMS) * time.Millisecond
	}
	m.duplicate = in.Duplicate
	current := m.mode
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": current})
}

// handleRedeliver 重新投递一次回调，用于构造「迟到回调」场景。
func (m *mock) handleRedeliver(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Ref      string `json:"ref"`
		Status   string `json:"status"`
		ImageURL string `json:"image_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "请求内容不合法"})
		return
	}
	m.mu.Lock()
	task, ok := m.tasks[in.Ref]
	m.mu.Unlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"ok": false, "error": "未知任务"})
		return
	}
	status := in.Status
	if status == "" {
		status = "succeeded"
	}
	imageURL := in.ImageURL
	if imageURL == "" {
		imageURL = "/static/img/gen-" + sanitize(task.Ref) + ".svg"
	}
	m.deliver(task.TaskID, status, imageURL, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// deliver 投递回调（带 HMAC 签名）。
func (m *mock) deliver(taskID, status, imageURL, reason string) {
	payload := map[string]any{
		"task_id":   taskID,
		"status":    status,
		"image_url": imageURL,
		"reason":    reason,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("序列化回调失败: %v", err)
		return
	}
	signature := supplier.Signature(m.secret, body)
	// 平台可能尚未就绪，最多重试若干次。
	for attempt := 0; attempt < 10; attempt++ {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, m.callback, bytes.NewReader(body))
		if err != nil {
			log.Printf("构造回调请求失败: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Supplier-Signature", signature)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			log.Printf("回调已投递：task=%s status=%s", taskID, status)
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	log.Printf("回调投递失败（平台不可达）：task=%s", taskID)
}

// sanitize 把平台任务 ID 转成安全的文件名片段。
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// writeJSON 输出 JSON。
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
