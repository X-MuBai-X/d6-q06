package httpapi

import (
	"fmt"
	"html/template"
	"net/http"
	"regexp"
	"strings"

	"creative-studio/internal/domain"
	"creative-studio/internal/service"
)

// pageData 是页面渲染的统一数据。
type pageData struct {
	Title string
	User  *domain.User
	Flash string
	Error string
	Data  any
}

// loginData 是登录页数据。
type loginData struct {
	Next string
}

// workbenchData 是需求工作台数据。
type workbenchData struct {
	Requirements []domain.Requirement
	Owner        string
	NewRequestNo string
}

// requirementData 是需求详情页数据。
type requirementData struct {
	View           *service.DetailView
	CanEdit        bool
	CanGenerate    bool
	CanReview      bool
	CanAdopt       bool
	CanWithdraw    bool
	CanOperateNote bool
	NewRequestNo   string
}

// reviewData 是复核队列页数据。
type reviewData struct {
	Items []domain.Requirement
}

// opsData 是运营异常查询页数据。
type opsData struct {
	Overview *service.OperatorOverview
	Anomaly  []domain.Event
	All      []domain.Requirement
}

// pageLogin 渲染登录页。
func (s *Server) pageLogin(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "login.html", "登录", &pageData{
		Data: &loginData{Next: r.URL.Query().Get("next")},
	}, http.StatusOK)
}

// pageLoginSubmit 处理登录表单。
func (s *Server) pageLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	token, _, err := s.svc.Login(r.Context(), r.PostFormValue("login"), r.PostFormValue("password"))
	if err != nil {
		s.render(w, r, "login.html", "登录", &pageData{
			Error: errorMessage(err),
			Data:  &loginData{Next: r.PostFormValue("next")},
		}, http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "studio_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	next := r.PostFormValue("next")
	if next == "" {
		next = "/workbench"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// pageLogout 注销并回到登录页。
func (s *Server) pageLogout(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	if c, err := r.Cookie("studio_session"); err == nil {
		_ = s.svc.Logout(r.Context(), c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "studio_session", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// pageWorkbench 渲染需求工作台。
func (s *Server) pageWorkbench(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	reqs, err := s.svc.ListRequirements(r.Context(), user, nil)
	if err != nil {
		s.render(w, r, "workbench.html", "需求工作台", &pageData{User: user, Error: errorMessage(err)}, http.StatusForbidden)
		return
	}
	s.render(w, r, "workbench.html", "需求工作台", &pageData{
		User:  user,
		Flash: r.URL.Query().Get("ok"),
		Error: r.URL.Query().Get("err"),
		Data: &workbenchData{
			Requirements: reqs,
			Owner:        user.Name,
			NewRequestNo: newRequestID(),
		},
	}, http.StatusOK)
}

// pageRequirement 渲染需求详情页（当前候选与历史候选分开展示）。
func (s *Server) pageRequirement(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	view, err := s.svc.RequirementDetail(r.Context(), user, r.PathValue("id"))
	if err != nil {
		s.render(w, r, "requirement.html", "需求详情", &pageData{User: user, Error: errorMessage(err)}, statusForError(err))
		return
	}
	req := view.Requirement
	owner := user.Role == domain.RoleRequester && req.OwnerID == user.ID
	data := &requirementData{
		View:           view,
		CanEdit:        owner && domain.CanStartGeneration(req.Status),
		CanGenerate:    owner && domain.CanStartGeneration(req.Status),
		CanReview:      user.Role == domain.RoleReviewer && domain.CanReview(req.Status),
		CanAdopt:       owner && domain.CanAdopt(req.Status),
		CanWithdraw:    owner && domain.CanWithdraw(req.Status),
		CanOperateNote: user.Role == domain.RoleOperator,
		NewRequestNo:   newRequestID(),
	}
	s.render(w, r, "requirement.html", fmt.Sprintf("需求详情 · %s", req.ActivityName), &pageData{
		User:  user,
		Flash: r.URL.Query().Get("ok"),
		Error: r.URL.Query().Get("err"),
		Data:  data,
	}, http.StatusOK)
}

// pageReview 渲染设计复核队列。
func (s *Server) pageReview(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	items, err := s.svc.ReviewQueue(r.Context(), user)
	if err != nil {
		s.render(w, r, "review.html", "候选复核", &pageData{User: user, Error: errorMessage(err)}, statusForError(err))
		return
	}
	s.render(w, r, "review.html", "候选复核", &pageData{User: user, Data: &reviewData{Items: items}}, http.StatusOK)
}

// pageOps 渲染运营异常查询页。
func (s *Server) pageOps(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	ov, err := s.svc.Overview(r.Context(), user)
	if err != nil {
		s.render(w, r, "ops.html", "运营异常查询", &pageData{User: user, Error: errorMessage(err)}, statusForError(err))
		return
	}
	anomalies, err := s.svc.ListAnomalies(r.Context(), user, 100)
	if err != nil {
		s.render(w, r, "ops.html", "运营异常查询", &pageData{User: user, Error: errorMessage(err)}, statusForError(err))
		return
	}
	all, err := s.svc.ListRequirements(r.Context(), user, nil)
	if err != nil {
		s.render(w, r, "ops.html", "运营异常查询", &pageData{User: user, Error: errorMessage(err)}, statusForError(err))
		return
	}
	s.render(w, r, "ops.html", "运营异常查询", &pageData{
		User: user, Data: &opsData{Overview: ov, Anomaly: anomalies, All: all},
	}, http.StatusOK)
}

// formCreateRequirement 处理工作台的创建需求表单。
func (s *Server) formCreateRequirement(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	_, err := s.svc.CreateRequirement(r.Context(), currentUser(r), service.CreateRequirementInput{
		ActivityName: r.PostFormValue("activity_name"),
		Purpose:      r.PostFormValue("purpose"),
		Description:  r.PostFormValue("description"),
	})
	if err != nil {
		http.Redirect(w, r, "/workbench?err="+urlQueryEscape(errorMessage(err)), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/workbench?ok="+urlQueryEscape("需求已创建，请确认描述后发起生成"), http.StatusSeeOther)
}

// formUpdateDescription 处理修改描述表单。
func (s *Server) formUpdateDescription(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	s.redirectWithResult(w, r, func() error {
		return s.svc.UpdateDescription(r.Context(), currentUser(r), r.PathValue("id"), r.PostFormValue("description"))
	}, "描述已更新")
}

// formStartGeneration 处理发起生成表单。
func (s *Server) formStartGeneration(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	requestNo := r.PostFormValue("request_no")
	if requestNo == "" {
		requestNo = newRequestID()
	}
	s.redirectWithResult(w, r, func() error {
		_, err := s.svc.StartGeneration(r.Context(), currentUser(r), r.PathValue("id"), requestNo)
		return err
	}, "已提交生成，等待供应商回调")
}

// formReview 处理复核表单。
func (s *Server) formReview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	approved := r.PostFormValue("decision") == "approve"
	s.redirectWithResult(w, r, func() error {
		_, err := s.svc.ReviewCandidate(r.Context(), currentUser(r), r.PathValue("id"),
			r.PostFormValue("candidate_id"), approved, r.PostFormValue("reason"))
		return err
	}, "复核结论已记录")
}

// formAdopt 处理确认采用表单。
func (s *Server) formAdopt(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	s.redirectWithResult(w, r, func() error {
		_, err := s.svc.Adopt(r.Context(), currentUser(r), r.PathValue("id"), r.PostFormValue("candidate_id"))
		return err
	}, "已确认采用，交付完成")
}

// formRework 处理提出重做表单。
func (s *Server) formRework(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	s.redirectWithResult(w, r, func() error {
		return s.svc.RequestRework(r.Context(), currentUser(r), r.PathValue("id"), r.PostFormValue("reason"))
	}, "已记录重做原因，可修改描述后再次发起生成")
}

// formWithdraw 处理撤销表单。
// 表单不消费请求体时必须显式解析，否则请求体残留会导致客户端收到 TCP 复位。
func (s *Server) formWithdraw(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	s.redirectWithResult(w, r, func() error {
		return s.svc.Withdraw(r.Context(), currentUser(r), r.PathValue("id"))
	}, "需求已撤销")
}

// formOperatorNote 处理运营跟进意见表单。
func (s *Server) formOperatorNote(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "表单解析失败", http.StatusBadRequest)
		return
	}
	s.redirectWithResult(w, r, func() error {
		return s.svc.AddOperatorNote(r.Context(), currentUser(r), r.PathValue("id"), r.PostFormValue("note"))
	}, "已记录运营跟进意见")
}

// redirectWithResult 执行操作并回到详情页，失败原因通过查询参数回显。
func (s *Server) redirectWithResult(w http.ResponseWriter, r *http.Request, fn func() error, okMessage string) {
	id := r.PathValue("id")
	if err := fn(); err != nil {
		http.Redirect(w, r, "/requirements/"+id+"?err="+urlQueryEscape(errorMessage(err)), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/requirements/"+id+"?ok="+urlQueryEscape(okMessage), http.StatusSeeOther)
}

// render 渲染页面模板。
func (s *Server) render(w http.ResponseWriter, r *http.Request, name, title string, data *pageData, status int) {
	var buf strings.Builder
	if err := s.templates.ExecuteTemplate(&buf, name, data); err != nil {
		http.Error(w, "页面渲染失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(buf.String()))
}

// safeName 只允许字母、数字、下划线与短横线，用于演示图片地址。
var safeName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// demoImage 返回演示用占位图片（纯本地生成，不依赖外部网络）。
func (s *Server) demoImage(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSuffix(r.PathValue("name"), ".svg")
	if !safeName.MatchString(name) {
		http.Error(w, "非法图片名", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="640" height="400" viewBox="0 0 640 400">
<rect width="640" height="400" fill="#1f2937"/>
<circle cx="180" cy="140" r="70" fill="#f59e0b"/>
<rect x="300" y="90" width="260" height="180" rx="16" fill="#4b5563"/>
<text x="320" y="320" fill="#f9fafb" font-size="26" font-family="sans-serif">AI 生成演示图 %s</text>
</svg>`, template.HTMLEscapeString(name))
}
