package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"creative-studio/internal/domain"
	"creative-studio/internal/service"
)

// readBody 读取请求体（有大小上限）。
func readBody(r *http.Request) ([]byte, error) {
	defer func() { _ = r.Body.Close() }()
	return io.ReadAll(io.LimitReader(r.Body, 1<<20))
}

// decodeJSON 解析 JSON 请求体。
func decodeJSON(r *http.Request, target any) error {
	body, err := readBody(r)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	return json.Unmarshal(body, target)
}

// apiLogin 是 JSON 登录接口。
func (s *Server) apiLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求内容不是合法 JSON")
		return
	}
	token, user, err := s.svc.Login(r.Context(), in.Login, in.Password)
	if err != nil {
		writeJSONError(w, statusForError(err), errorMessage(err))
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "studio_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": token, "user": userView(user)})
}

// apiListRequirements 返回当前身份可见的需求列表。
func (s *Server) apiListRequirements(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	reqs, err := s.svc.ListRequirements(r.Context(), user, nil)
	if err != nil {
		writeJSONError(w, statusForError(err), errorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": reqs})
}

// apiCreateRequirement 创建需求。
func (s *Server) apiCreateRequirement(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ActivityName string `json:"activity_name"`
		Purpose      string `json:"purpose"`
		Description  string `json:"description"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求内容不是合法 JSON")
		return
	}
	req, err := s.svc.CreateRequirement(r.Context(), currentUser(r), service.CreateRequirementInput{
		ActivityName: in.ActivityName, Purpose: in.Purpose, Description: in.Description,
	})
	if err != nil {
		writeJSONError(w, statusForError(err), errorMessage(err))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "requirement": req})
}

// apiRequirementDetail 返回需求完整视图。
func (s *Server) apiRequirementDetail(w http.ResponseWriter, r *http.Request) {
	view, err := s.svc.RequirementDetail(r.Context(), currentUser(r), r.PathValue("id"))
	if err != nil {
		writeJSONError(w, statusForError(err), errorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "detail": view})
}

// apiUpdateDescription 修改图片描述。
func (s *Server) apiUpdateDescription(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Description string `json:"description"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求内容不是合法 JSON")
		return
	}
	if err := s.svc.UpdateDescription(r.Context(), currentUser(r), r.PathValue("id"), in.Description); err != nil {
		writeJSONError(w, statusForError(err), errorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// apiStartGeneration 发起生成。
func (s *Server) apiStartGeneration(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RequestNo string `json:"request_no"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求内容不是合法 JSON")
		return
	}
	if in.RequestNo == "" {
		in.RequestNo = newRequestID()
	}
	task, err := s.svc.StartGeneration(r.Context(), currentUser(r), r.PathValue("id"), in.RequestNo)
	if err != nil {
		writeJSONError(w, statusForError(err), errorMessage(err))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "task": task})
}

// apiReview 复核当前候选。
func (s *Server) apiReview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CandidateID string `json:"candidate_id"`
		Approved    bool   `json:"approved"`
		Reason      string `json:"reason"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求内容不是合法 JSON")
		return
	}
	cand, err := s.svc.ReviewCandidate(r.Context(), currentUser(r), r.PathValue("id"), in.CandidateID, in.Approved, in.Reason)
	if err != nil {
		writeJSONError(w, statusForError(err), errorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "candidate": cand})
}

// apiAdopt 确认采用。
func (s *Server) apiAdopt(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CandidateID string `json:"candidate_id"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求内容不是合法 JSON")
		return
	}
	req, err := s.svc.Adopt(r.Context(), currentUser(r), r.PathValue("id"), in.CandidateID)
	if err != nil {
		writeJSONError(w, statusForError(err), errorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "requirement": req})
}

// apiRework 提出重做原因。
func (s *Server) apiRework(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求内容不是合法 JSON")
		return
	}
	if err := s.svc.RequestRework(r.Context(), currentUser(r), r.PathValue("id"), in.Reason); err != nil {
		writeJSONError(w, statusForError(err), errorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// apiWithdraw 撤销需求。
func (s *Server) apiWithdraw(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Withdraw(r.Context(), currentUser(r), r.PathValue("id")); err != nil {
		writeJSONError(w, statusForError(err), errorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// apiOperatorNote 运营记录跟进意见。
func (s *Server) apiOperatorNote(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Note string `json:"note"`
	}
	if err := decodeJSON(r, &in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求内容不是合法 JSON")
		return
	}
	if err := s.svc.AddOperatorNote(r.Context(), currentUser(r), r.PathValue("id"), in.Note); err != nil {
		writeJSONError(w, statusForError(err), errorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// apiReviewQueue 返回待复核队列。
func (s *Server) apiReviewQueue(w http.ResponseWriter, r *http.Request) {
	items, err := s.svc.ReviewQueue(r.Context(), currentUser(r))
	if err != nil {
		writeJSONError(w, statusForError(err), errorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": items})
}

// apiAnomalies 返回异常记录。
func (s *Server) apiAnomalies(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := s.svc.ListAnomalies(r.Context(), currentUser(r), limit)
	if err != nil {
		writeJSONError(w, statusForError(err), errorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": items})
}

// apiOverview 返回运营总览。
func (s *Server) apiOverview(w http.ResponseWriter, r *http.Request) {
	ov, err := s.svc.Overview(r.Context(), currentUser(r))
	if err != nil {
		writeJSONError(w, statusForError(err), errorMessage(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "overview": ov})
}

// userView 返回对外可见的账号信息（不含口令摘要）。
func userView(u *domain.User) map[string]any {
	return map[string]any{"id": u.ID, "name": u.Name, "role": string(u.Role), "role_label": roleLabel(u.Role)}
}
