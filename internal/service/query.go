package service

import (
	"context"
	"fmt"

	"creative-studio/internal/domain"
	"creative-studio/internal/store"
)

// DetailView 是按需求查清的完整视图（题面 R8）。
type DetailView struct {
	Requirement      *domain.Requirement     `json:"requirement"`
	Tasks            []domain.GenerationTask `json:"tasks"`
	Candidates       []domain.Candidate      `json:"candidates"`
	Events           []domain.Event          `json:"events"`
	CurrentCandidate *domain.Candidate       `json:"current_candidate"`
}

// ListRequirements 按角色返回可见的需求列表。
func (s *Service) ListRequirements(ctx context.Context, actor *domain.User, statuses []domain.RequirementStatus) ([]domain.Requirement, error) {
	filter := store.RequirementFilter{Statuses: statuses, Limit: 200}
	switch actor.Role {
	case domain.RoleRequester:
		// 题面 R8：需求人仅访问自己的需求。
		filter.OwnerID = actor.ID
	case domain.RoleReviewer, domain.RoleOperator:
	default:
		return nil, fmt.Errorf("%w: 未知角色", domain.ErrForbidden)
	}
	return s.store.ListRequirements(ctx, filter)
}

// RequirementDetail 返回需求的完整视图：每次描述快照、供应商任务、结果、复核与最终处置。
func (s *Service) RequirementDetail(ctx context.Context, actor *domain.User, requirementID string) (*DetailView, error) {
	req, err := s.loadRequirement(ctx, actor, requirementID)
	if err != nil {
		return nil, err
	}
	tasks, err := s.store.ListTasks(ctx, requirementID)
	if err != nil {
		return nil, err
	}
	cands, err := s.store.ListCandidates(ctx, requirementID)
	if err != nil {
		return nil, err
	}
	events, err := s.store.ListEvents(ctx, requirementID)
	if err != nil {
		return nil, err
	}
	view := &DetailView{Requirement: req, Tasks: tasks, Candidates: cands, Events: events}
	for i := range cands {
		if cands[i].IsCurrent {
			view.CurrentCandidate = &cands[i]
			break
		}
	}
	return view, nil
}

// ListAnomalies 返回运营需要跟进的异常记录（题面 R4：异常须可查询）。
// 查询种类取自异常注册表 AnomalyKinds，并按注册表二次过滤，避免非异常事件混入。
func (s *Service) ListAnomalies(ctx context.Context, actor *domain.User, limit int) ([]domain.Event, error) {
	if actor.Role != domain.RoleOperator && actor.Role != domain.RoleReviewer {
		return nil, fmt.Errorf("%w: 只有运营与设计复核人可以查看异常记录", domain.ErrForbidden)
	}
	if limit <= 0 {
		limit = 50
	}
	events, err := s.store.ListAnomalyEvents(ctx, AnomalyKinds, limit)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Event, 0, len(events))
	for _, e := range events {
		if IsAnomalyKind(e.Kind) {
			out = append(out, e)
		}
	}
	return out, nil
}

// OperatorOverview 是运营总览：需求状态分布、生成任务状态分布与异常计数。
type OperatorOverview struct {
	RequirementCounts map[domain.RequirementStatus]int `json:"requirement_counts"`
	TaskCounts        map[domain.TaskStatus]int        `json:"task_counts"`
	AnomalyCount      int                              `json:"anomaly_count"`
	RecentEvents      []domain.Event                   `json:"recent_events"`
}

// Overview 汇总运营视角的数据。
func (s *Service) Overview(ctx context.Context, actor *domain.User) (*OperatorOverview, error) {
	if actor.Role != domain.RoleOperator {
		return nil, fmt.Errorf("%w: 只有运营可以查看总览", domain.ErrForbidden)
	}
	all, err := s.store.ListRequirements(ctx, store.RequirementFilter{Limit: 1000})
	if err != nil {
		return nil, err
	}
	counts := map[domain.RequirementStatus]int{}
	for _, r := range all {
		counts[r.Status]++
	}
	taskCounts := map[domain.TaskStatus]int{}
	for _, r := range all {
		tasks, err := s.store.ListTasks(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		for _, t := range tasks {
			taskCounts[t.Status]++
		}
	}
	anomalies, err := s.store.ListAnomalyEvents(ctx, AnomalyKinds, 0)
	if err != nil {
		return nil, err
	}
	recent, err := s.store.ListRecentEvents(ctx, 20)
	if err != nil {
		return nil, err
	}
	return &OperatorOverview{
		RequirementCounts: counts,
		TaskCounts:        taskCounts,
		AnomalyCount:      len(anomalies),
		RecentEvents:      recent,
	}, nil
}

// ReviewQueue 返回设计复核人待处理的需求（题面 R8：复核人可审核待处理需求）。
func (s *Service) ReviewQueue(ctx context.Context, actor *domain.User) ([]domain.Requirement, error) {
	if actor.Role != domain.RoleReviewer {
		return nil, fmt.Errorf("%w: 只有设计复核人可以查看待复核队列", domain.ErrForbidden)
	}
	return s.store.ListRequirements(ctx, store.RequirementFilter{
		Statuses: []domain.RequirementStatus{domain.ReqCandidatePending},
		Limit:    200,
	})
}
