package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"creative-studio/internal/domain"
)

// InsertTask 新增生成任务。
func (s *Store) InsertTask(ctx context.Context, t *domain.GenerationTask) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO generation_tasks
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

// GetTask 按任务 ID 读取。
func (s *Store) GetTask(ctx context.Context, id string) (*domain.GenerationTask, error) {
	return s.getTask(ctx, s.db, `SELECT id, requirement_id, request_no, attempt_no, description_snapshot, status,
		supplier_task_id, image_url, failure_reason, created_at, finished_at FROM generation_tasks WHERE id = ?`, id)
}

// GetTaskByRequestNo 按请求编号读取（幂等键）。
func (s *Store) GetTaskByRequestNo(ctx context.Context, requestNo string) (*domain.GenerationTask, error) {
	return s.getTask(ctx, s.db, `SELECT id, requirement_id, request_no, attempt_no, description_snapshot, status,
		supplier_task_id, image_url, failure_reason, created_at, finished_at FROM generation_tasks WHERE request_no = ?`, requestNo)
}

// GetTaskByRequirementRequestNo 按需求与请求编号读取（幂等键按需求隔离）。
// 题面 R2 的幂等作用域是「同一需求」：请求编号只在同一需求内唯一。
func (s *Store) GetTaskByRequirementRequestNo(ctx context.Context, requirementID, requestNo string) (*domain.GenerationTask, error) {
	return s.getTaskByRequirementRequestNo(ctx, s.db, requirementID, requestNo)
}

// GetTaskBySupplierTaskID 按供应商任务号读取（回调关联键）。
func (s *Store) GetTaskBySupplierTaskID(ctx context.Context, supplierTaskID string) (*domain.GenerationTask, error) {
	return s.getTask(ctx, s.db, `SELECT id, requirement_id, request_no, attempt_no, description_snapshot, status,
		supplier_task_id, image_url, failure_reason, created_at, finished_at FROM generation_tasks WHERE supplier_task_id = ?`, supplierTaskID)
}

// ListTasks 按需求列出全部生成任务，按尝试序号升序。
func (s *Store) ListTasks(ctx context.Context, requirementID string) ([]domain.GenerationTask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, requirement_id, request_no, attempt_no, description_snapshot, status,
		supplier_task_id, image_url, failure_reason, created_at, finished_at FROM generation_tasks
		WHERE requirement_id = ? ORDER BY attempt_no ASC`, requirementID)
	if err != nil {
		return nil, fmt.Errorf("查询生成任务失败: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []domain.GenerationTask{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("读取生成任务失败: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// FindProcessingTask 查找需求当前处理中的生成任务。
func (s *Store) FindProcessingTask(ctx context.Context, requirementID string) (*domain.GenerationTask, error) {
	return s.getTask(ctx, s.db, `SELECT id, requirement_id, request_no, attempt_no, description_snapshot, status,
		supplier_task_id, image_url, failure_reason, created_at, finished_at FROM generation_tasks
		WHERE requirement_id = ? AND status = 'processing'`, requirementID)
}

// ListProcessingTasksBefore 列出早于截止时间仍在处理中的任务（超时判定用）。
func (s *Store) ListProcessingTasksBefore(ctx context.Context, cutoff time.Time) ([]domain.GenerationTask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, requirement_id, request_no, attempt_no, description_snapshot, status,
		supplier_task_id, image_url, failure_reason, created_at, finished_at FROM generation_tasks
		WHERE status = 'processing' AND created_at <= ? ORDER BY created_at ASC`, formatTime(cutoff))
	if err != nil {
		return nil, fmt.Errorf("查询超时任务失败: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []domain.GenerationTask{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("读取超时任务失败: %w", err)
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// UpdateTask 写回任务状态与结果字段。
func (s *Store) UpdateTask(ctx context.Context, t *domain.GenerationTask) error {
	res, err := s.db.ExecContext(ctx, `UPDATE generation_tasks SET status = ?, supplier_task_id = ?,
		image_url = ?, failure_reason = ?, finished_at = ? WHERE id = ?`,
		string(t.Status), t.SupplierTaskID, t.ImageURL, t.FailureReason, formatTime(t.FinishedAt), t.ID)
	if err != nil {
		return fmt.Errorf("更新生成任务失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) getTask(ctx context.Context, q Execer, query, arg string) (*domain.GenerationTask, error) {
	row := q.QueryRowContext(ctx, query, arg)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取生成任务失败: %w", err)
	}
	return t, nil
}

// getTaskByRequirementRequestNo 按需求与请求编号组合读取任务。
func (s *Store) getTaskByRequirementRequestNo(ctx context.Context, q Execer, requirementID, requestNo string) (*domain.GenerationTask, error) {
	row := q.QueryRowContext(ctx, `SELECT id, requirement_id, request_no, attempt_no, description_snapshot, status,
		supplier_task_id, image_url, failure_reason, created_at, finished_at FROM generation_tasks
		WHERE requirement_id = ? AND request_no = ?`, requirementID, requestNo)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取生成任务失败: %w", err)
	}
	return t, nil
}

func scanTask(sc rowScanner) (*domain.GenerationTask, error) {
	var (
		t         domain.GenerationTask
		status    string
		createdAt string
		finished  string
	)
	if err := sc.Scan(&t.ID, &t.RequirementID, &t.RequestNo, &t.AttemptNo, &t.DescriptionSnapshot, &status,
		&t.SupplierTaskID, &t.ImageURL, &t.FailureReason, &createdAt, &finished); err != nil {
		return nil, err
	}
	t.Status = domain.TaskStatus(status)
	t.CreatedAt = parseTime(createdAt)
	t.FinishedAt = parseTime(finished)
	return &t, nil
}

// isUniqueViolation 判断错误是否为唯一约束冲突。
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
