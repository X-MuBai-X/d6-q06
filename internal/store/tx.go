package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"creative-studio/internal/domain"
)

// Tx 在一个「立即加写锁」的事务中执行 fn。
// BEGIN IMMEDIATE 把并发写操作串行化，避免两个并发事务同时通过前置检查。
func (s *Store) Tx(ctx context.Context, fn func(tx Execer) error) error {
	return s.withImmediateTx(ctx, fn)
}

// GetRequirementTx 在事务上下文内读取需求。
func (s *Store) GetRequirementTx(ctx context.Context, q Execer, id string) (*domain.Requirement, error) {
	row := q.QueryRowContext(ctx, `SELECT id, owner_id, activity_name, purpose, description, status,
		current_task_id, current_candidate, attempt_count, created_at, updated_at FROM requirements WHERE id = ?`, id)
	r, err := scanRequirement(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取需求失败: %w", err)
	}
	return r, nil
}

// InsertTaskTx 在事务上下文内写入生成任务。
func (s *Store) InsertTaskTx(ctx context.Context, q Execer, t *domain.GenerationTask) error {
	_, err := q.ExecContext(ctx, `INSERT INTO generation_tasks
		(id, requirement_id, request_no, attempt_no, description_snapshot, status, supplier_task_id,
		image_url, failure_reason, created_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.RequirementID, t.RequestNo, t.AttemptNo, t.DescriptionSnapshot, string(t.Status),
		t.SupplierTaskID, t.ImageURL, t.FailureReason, formatTime(t.CreatedAt), formatTime(t.FinishedAt))
	if err != nil {
		return fmt.Errorf("写入生成任务失败: %w", err)
	}
	return nil
}

// GetTaskTx 在事务上下文内按任务 ID 读取。
func (s *Store) GetTaskTx(ctx context.Context, q Execer, id string) (*domain.GenerationTask, error) {
	row := q.QueryRowContext(ctx, `SELECT id, requirement_id, request_no, attempt_no, description_snapshot, status,
		supplier_task_id, image_url, failure_reason, created_at, finished_at FROM generation_tasks WHERE id = ?`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取生成任务失败: %w", err)
	}
	return t, nil
}

// GetTaskByRequestNoTx 在事务上下文内按请求编号读取。
func (s *Store) GetTaskByRequestNoTx(ctx context.Context, q Execer, requestNo string) (*domain.GenerationTask, error) {
	row := q.QueryRowContext(ctx, `SELECT id, requirement_id, request_no, attempt_no, description_snapshot, status,
		supplier_task_id, image_url, failure_reason, created_at, finished_at FROM generation_tasks WHERE request_no = ?`, requestNo)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取生成任务失败: %w", err)
	}
	return t, nil
}

// GetTaskByRequirementRequestNoTx 在事务上下文内按需求与请求编号组合读取。
// 题面 R2：幂等键按需求隔离，跨需求复用同一请求编号不会被这里命中。
func (s *Store) GetTaskByRequirementRequestNoTx(ctx context.Context, q Execer, requirementID, requestNo string) (*domain.GenerationTask, error) {
	return s.getTaskByRequirementRequestNo(ctx, q, requirementID, requestNo)
}

// GetTaskBySupplierTaskIDTx 在事务上下文内按供应商任务号读取。
func (s *Store) GetTaskBySupplierTaskIDTx(ctx context.Context, q Execer, supplierTaskID string) (*domain.GenerationTask, error) {
	row := q.QueryRowContext(ctx, `SELECT id, requirement_id, request_no, attempt_no, description_snapshot, status,
		supplier_task_id, image_url, failure_reason, created_at, finished_at FROM generation_tasks WHERE supplier_task_id = ?`, supplierTaskID)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取生成任务失败: %w", err)
	}
	return t, nil
}

// FindProcessingTaskTx 在事务上下文内查找需求当前处理中的任务。
func (s *Store) FindProcessingTaskTx(ctx context.Context, q Execer, requirementID string) (*domain.GenerationTask, error) {
	row := q.QueryRowContext(ctx, `SELECT id, requirement_id, request_no, attempt_no, description_snapshot, status,
		supplier_task_id, image_url, failure_reason, created_at, finished_at FROM generation_tasks
		WHERE requirement_id = ? AND status = 'processing'`, requirementID)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取生成任务失败: %w", err)
	}
	return t, nil
}

// UpdateTaskTx 在事务上下文内写回任务状态。
func (s *Store) UpdateTaskTx(ctx context.Context, q Execer, t *domain.GenerationTask) error {
	_, err := q.ExecContext(ctx, `UPDATE generation_tasks SET status = ?, supplier_task_id = ?,
		image_url = ?, failure_reason = ?, finished_at = ? WHERE id = ?`,
		string(t.Status), t.SupplierTaskID, t.ImageURL, t.FailureReason, formatTime(t.FinishedAt), t.ID)
	if err != nil {
		return fmt.Errorf("更新生成任务失败: %w", err)
	}
	return nil
}

// UpdateRequirementStatusTx 在事务上下文内更新需求状态与当前关联对象。
func (s *Store) UpdateRequirementStatusTx(ctx context.Context, q Execer, id string, status domain.RequirementStatus,
	currentTaskID, currentCandidate string, attemptCount int, now time.Time) error {
	res, err := q.ExecContext(ctx, `UPDATE requirements SET status = ?, current_task_id = ?,
		current_candidate = ?, attempt_count = ?, updated_at = ? WHERE id = ?`,
		string(status), currentTaskID, currentCandidate, attemptCount, formatTime(now), id)
	if err != nil {
		return fmt.Errorf("更新需求状态失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// UpdateRequirementDescriptionTx 在事务上下文内更新当前描述。
func (s *Store) UpdateRequirementDescriptionTx(ctx context.Context, q Execer, id, description string, now time.Time) error {
	res, err := q.ExecContext(ctx, `UPDATE requirements SET description = ?, updated_at = ? WHERE id = ?`,
		description, formatTime(now), id)
	if err != nil {
		return fmt.Errorf("更新描述失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// InsertCandidateTx 在事务上下文内写入候选。
func (s *Store) InsertCandidateTx(ctx context.Context, q Execer, c *domain.Candidate) error {
	_, err := q.ExecContext(ctx, `INSERT INTO candidates
		(id, task_id, requirement_id, attempt_no, image_url, review_status, review_reason, reviewer_id, reviewed_at, is_current, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.TaskID, c.RequirementID, c.AttemptNo, c.ImageURL, string(c.ReviewStatus),
		c.ReviewReason, c.ReviewerID, formatTime(c.ReviewedAt), boolToInt(c.IsCurrent), formatTime(c.CreatedAt))
	if err != nil {
		return fmt.Errorf("写入候选失败: %w", err)
	}
	return nil
}

// ClearCurrentCandidatesTx 在事务上下文内把需求的全部候选标记为历史。
func (s *Store) ClearCurrentCandidatesTx(ctx context.Context, q Execer, requirementID string) error {
	_, err := q.ExecContext(ctx, `UPDATE candidates SET is_current = 0 WHERE requirement_id = ?`, requirementID)
	if err != nil {
		return fmt.Errorf("更新候选历史标记失败: %w", err)
	}
	return nil
}

// UpdateCandidateReviewTx 在事务上下文内写回复核结论。
func (s *Store) UpdateCandidateReviewTx(ctx context.Context, q Execer, c *domain.Candidate) error {
	_, err := q.ExecContext(ctx, `UPDATE candidates SET review_status = ?, review_reason = ?, reviewer_id = ?,
		reviewed_at = ? WHERE id = ?`,
		string(c.ReviewStatus), c.ReviewReason, c.ReviewerID, formatTime(c.ReviewedAt), c.ID)
	if err != nil {
		return fmt.Errorf("更新复核结论失败: %w", err)
	}
	return nil
}

// GetCandidateTx 在事务上下文内按 ID 读取候选。
func (s *Store) GetCandidateTx(ctx context.Context, q Execer, id string) (*domain.Candidate, error) {
	row := q.QueryRowContext(ctx, `SELECT id, task_id, requirement_id, attempt_no, image_url, review_status,
		review_reason, reviewer_id, reviewed_at, is_current, created_at FROM candidates WHERE id = ?`, id)
	c, err := scanCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取候选失败: %w", err)
	}
	return c, nil
}

// GetCurrentCandidateTx 在事务上下文内读取需求当前候选。
func (s *Store) GetCurrentCandidateTx(ctx context.Context, q Execer, requirementID string) (*domain.Candidate, error) {
	row := q.QueryRowContext(ctx, `SELECT id, task_id, requirement_id, attempt_no, image_url, review_status,
		review_reason, reviewer_id, reviewed_at, is_current, created_at FROM candidates
		WHERE requirement_id = ? AND is_current = 1`, requirementID)
	c, err := scanCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取当前候选失败: %w", err)
	}
	return c, nil
}

// InsertEventTx 在事务上下文内写入审计事件。
func (s *Store) InsertEventTx(ctx context.Context, q Execer, e *domain.Event) error {
	_, err := q.ExecContext(ctx, `INSERT INTO events
		(requirement_id, task_id, actor_id, actor_role, kind, detail, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.RequirementID, e.TaskID, e.ActorID, string(e.ActorRole), e.Kind, e.Detail, formatTime(e.CreatedAt))
	if err != nil {
		return fmt.Errorf("写入审计事件失败: %w", err)
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
