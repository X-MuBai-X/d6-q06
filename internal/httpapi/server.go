// Package httpapi 提供 JSON 接口与可操作的最小业务页面。
// 身份一律来自服务端签发的会话令牌，请求参数中的用户编号不被采信。
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"creative-studio/internal/domain"
	"creative-studio/internal/service"
	"creative-studio/internal/supplier"
)

// Server 是 HTTP 层。
type Server struct {
	svc       *service.Service
	templates *template.Template
	secret    string
}

// New 创建 HTTP 服务。
func New(svc *service.Service, supplierSecret string) (*Server, error) {
	tpl, err := template.New("").Funcs(template.FuncMap{
		"statusLabel": service.DescribeStatus,
		"roleLabel":   roleLabel,
		"eventLabel":  eventLabel,
		"taskLabel":   taskLabel,
		"reviewLabel": reviewLabel,
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("解析页面模板失败: %w", err)
	}
	return &Server{svc: svc, templates: tpl, secret: supplierSecret}, nil
}

// Handler 返回带全部路由的处理器。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// 页面路由
	mux.HandleFunc("GET /login", s.pageLogin)
	mux.HandleFunc("POST /login", s.pageLoginSubmit)
	mux.Handle("POST /logout", s.withUser(s.pageLogout))
	mux.Handle("GET /", s.withUser(s.pageWorkbench))
	mux.Handle("GET /workbench", s.withUser(s.pageWorkbench))
	mux.Handle("GET /requirements/{id}", s.withUser(s.pageRequirement))
	mux.Handle("GET /review", s.withUser(s.pageReview))
	mux.Handle("GET /ops", s.withUser(s.pageOps))
	mux.HandleFunc("GET /static/img/{name}", s.demoImage)

	// 表单提交（页面通道）
	mux.Handle("POST /requirements", s.withUser(s.formCreateRequirement))
	mux.Handle("POST /requirements/{id}/description", s.withUser(s.formUpdateDescription))
	mux.Handle("POST /requirements/{id}/generations", s.withUser(s.formStartGeneration))
	mux.Handle("POST /requirements/{id}/review", s.withUser(s.formReview))
	mux.Handle("POST /requirements/{id}/adopt", s.withUser(s.formAdopt))
	mux.Handle("POST /requirements/{id}/rework", s.withUser(s.formRework))
	mux.Handle("POST /requirements/{id}/withdraw", s.withUser(s.formWithdraw))
	mux.Handle("POST /requirements/{id}/operator-note", s.withUser(s.formOperatorNote))

	// JSON 接口（与表单走同一业务逻辑）
	mux.HandleFunc("POST /api/login", s.apiLogin)
	mux.Handle("GET /api/requirements", s.withUser(s.apiListRequirements))
	mux.Handle("POST /api/requirements", s.withUser(s.apiCreateRequirement))
	mux.Handle("GET /api/requirements/{id}", s.withUser(s.apiRequirementDetail))
	mux.Handle("POST /api/requirements/{id}/description", s.withUser(s.apiUpdateDescription))
	mux.Handle("POST /api/requirements/{id}/generations", s.withUser(s.apiStartGeneration))
	mux.Handle("POST /api/requirements/{id}/review", s.withUser(s.apiReview))
	mux.Handle("POST /api/requirements/{id}/adopt", s.withUser(s.apiAdopt))
	mux.Handle("POST /api/requirements/{id}/rework", s.withUser(s.apiRework))
	mux.Handle("POST /api/requirements/{id}/withdraw", s.withUser(s.apiWithdraw))
	mux.Handle("POST /api/requirements/{id}/operator-note", s.withUser(s.apiOperatorNote))
	mux.Handle("GET /api/review-queue", s.withUser(s.apiReviewQueue))
	mux.Handle("GET /api/anomalies", s.withUser(s.apiAnomalies))
	mux.Handle("GET /api/overview", s.withUser(s.apiOverview))

	// 供应商回调（独立鉴权：签名 + 任务关联）
	mux.HandleFunc("POST /api/supplier/callback", s.apiSupplierCallback)
	mux.HandleFunc("GET /healthz", s.healthz)

	return s.recoverMiddleware(s.bodyDrainMiddleware(mux))
}

// bodyDrainMiddleware 在处理完请求后把请求体读干净。
// 兜底措施：任何写接口如果没有消费请求体，net/http 会在关闭连接时向客户端发送 TCP 复位，
// 客户端可能因此丢失已经生成的响应（见 evidence/independent-test-report.md D-03）。
func (s *Server) bodyDrainMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer drainBody(r)
		next.ServeHTTP(w, r)
	})
}

// recoverMiddleware 把未预期异常转换成 500，且不向调用方透出内部细节。
func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("请求 %s %s 发生未预期异常: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				http.Error(w, "服务内部错误，请稍后重试", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// contextKey 是请求上下文的键类型。
type contextKey string

const userContextKey contextKey = "studio-user"

// currentUser 取出请求中的当前身份。
func currentUser(r *http.Request) *domain.User {
	u, _ := r.Context().Value(userContextKey).(*domain.User)
	return u
}

// withUser 要求有效会话，并把身份放入请求上下文。
func (s *Server) withUser(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.sessionToken(r)
		user, err := s.svc.Authenticate(r.Context(), token)
		if err != nil {
			if isAPIPath(r.URL.Path) {
				writeJSONError(w, http.StatusUnauthorized, "未登录或会话已失效")
				return
			}
			http.Redirect(w, r, "/login?next="+urlQueryEscape(r.URL.Path), http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, user)
		next(w, r.WithContext(ctx))
	})
}

// sessionToken 从 Cookie 或 Authorization 头取会话令牌。
func (s *Server) sessionToken(r *http.Request) string {
	if c, err := r.Cookie("studio_session"); err == nil && c.Value != "" {
		return c.Value
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// isAPIPath 判断是否为 JSON 接口路径。
func isAPIPath(path string) bool {
	return strings.HasPrefix(path, "/api/")
}

// writeJSON 输出 JSON。
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeJSONError 输出 JSON 错误。
func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": message})
}

// statusForError 把领域错误映射为 HTTP 状态码。
func statusForError(err error) int {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, domain.ErrInvalidInput):
		return http.StatusBadRequest
	case errors.Is(err, domain.ErrInvalidState):
		return http.StatusConflict
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, domain.ErrTooManyAttempts):
		return http.StatusConflict
	case errors.Is(err, domain.ErrNotCurrentCandidate):
		return http.StatusConflict
	case errors.Is(err, domain.ErrUnknownTask):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrTaskAlreadyFinished):
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// errorMessage 返回可安全对外展示的错误说明。
func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrForbidden),
		errors.Is(err, domain.ErrUnauthorized), errors.Is(err, domain.ErrInvalidInput),
		errors.Is(err, domain.ErrInvalidState), errors.Is(err, domain.ErrConflict),
		errors.Is(err, domain.ErrTooManyAttempts), errors.Is(err, domain.ErrNotCurrentCandidate),
		errors.Is(err, domain.ErrUnknownTask), errors.Is(err, domain.ErrTaskAlreadyFinished):
		return err.Error()
	}
	return "服务内部错误，请稍后重试"
}

// healthz 是健康检查。
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "time": time.Now().UTC().Format(time.RFC3339)})
}

// apiSupplierCallback 处理供应商回调：先验签，再交给业务层。
func (s *Server) apiSupplierCallback(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "无法读取回调内容")
		return
	}
	if !supplier.VerifySignature(s.secret, body, r.Header.Get("X-Supplier-Signature")) {
		_ = s.svc.RecordAnomaly(r.Context(), domain.EventSignatureInvalid, "", "",
			"回调签名校验失败，已拒绝处理")
		writeJSONError(w, http.StatusUnauthorized, "回调签名校验失败")
		return
	}
	var payload struct {
		TaskID   string `json:"task_id"`
		Status   string `json:"status"`
		ImageURL string `json:"image_url"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeJSONError(w, http.StatusBadRequest, "回调内容不是合法 JSON")
		return
	}
	result, err := s.svc.HandleCallback(r.Context(), service.CallbackPayload{
		SupplierTaskID: payload.TaskID,
		Status:         payload.Status,
		ImageURL:       payload.ImageURL,
		Reason:         payload.Reason,
	})
	if err != nil {
		writeJSONError(w, statusForError(err), errorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "applied": result.Applied, "ignored": result.Ignored,
		"task_id": result.TaskID, "message": result.Message,
	})
}

// newRequestID 生成一个请求编号，供页面发起生成时使用（幂等键）。
func newRequestID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	return "req-" + hex.EncodeToString(buf)
}

// roleLabel 返回角色的中文名。
func roleLabel(r domain.Role) string {
	switch r {
	case domain.RoleRequester:
		return "需求人"
	case domain.RoleReviewer:
		return "设计复核人"
	case domain.RoleOperator:
		return "运营"
	}
	return string(r)
}

// eventLabel 返回审计事件的中文名。
func eventLabel(kind string) string {
	switch kind {
	case domain.EventRequirementCreated:
		return "创建需求"
	case domain.EventDescriptionUpdated:
		return "修改描述"
	case domain.EventGenerationStarted:
		return "发起生成"
	case domain.EventGenerationRejected:
		return "生成未受理"
	case domain.EventGenerationFailed:
		return "生成失败"
	case domain.EventGenerationTimeout:
		return "生成超时"
	case domain.EventCallbackAccepted:
		return "供应商回调已受理"
	case domain.EventCallbackIgnored:
		return "回调被忽略"
	case domain.EventCallbackUnknown:
		return "回调关联不到任务"
	case domain.EventCandidateCreated:
		return "生成候选"
	case domain.EventCandidateReviewed:
		return "候选复核"
	case domain.EventReworkRequested:
		return "提出重做"
	case domain.EventAdopted:
		return "确认采用"
	case domain.EventWithdrawn:
		return "撤销需求"
	case domain.EventOperatorNote:
		return "运营跟进"
	case domain.EventCallbackDuplicate:
		return "重复回调"
	case domain.EventCallbackConflict:
		return "冲突回调"
	case domain.EventLateResultIgnored:
		return "迟到结果被忽略"
	case domain.EventSignatureInvalid:
		return "回调签名无效"
	}
	return kind
}

// taskLabel 返回生成任务状态的中文名。
func taskLabel(s domain.TaskStatus) string {
	switch s {
	case domain.TaskProcessing:
		return "处理中"
	case domain.TaskSucceeded:
		return "成功"
	case domain.TaskFailed:
		return "失败"
	case domain.TaskRejected:
		return "被拒绝"
	case domain.TaskTimeout:
		return "超时"
	}
	return string(s)
}

// reviewLabel 返回复核状态的中文名。
func reviewLabel(s domain.ReviewStatus) string {
	switch s {
	case domain.ReviewPending:
		return "待复核"
	case domain.ReviewApproved:
		return "复核通过"
	case domain.ReviewRejected:
		return "复核未通过"
	}
	return string(s)
}

// urlQueryEscape 转义查询参数。
func urlQueryEscape(s string) string {
	replacer := strings.NewReplacer("&", "%26", "?", "%3F", "#", "%23", " ", "%20")
	return replacer.Replace(s)
}
