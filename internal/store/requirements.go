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

// RequirementFilter 是需求列表的过滤条件。
type RequirementFilter struct {
	OwnerID  string
	Statuses []domain.RequirementStatus
	Limit    int
}

// InsertRequirement 新增需求。
func (s *Store) InsertRequirement(ctx context.Context, r *domain.Requirement) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO requirements
		(id, owner_id, activity_name, purpose, description, status, current_task_id, current_candidate, attempt_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.OwnerID, r.ActivityName, r.Purpose, r.Description, string(r.Status),
		r.CurrentTaskID, r.CurrentCandidate, r.AttemptCount, formatTime(r.CreatedAt), formatTime(r.UpdatedAt))
	if err != nil {
		return fmt.Errorf("写入需求失败: %w", err)
	}
	return nil
}

// GetRequirement 读取单个需求。
func (s *Store) GetRequirement(ctx context.Context, id string) (*domain.Requirement, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, owner_id, activity_name, purpose, description, status,
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

// ListRequirements 按过滤条件列出需求，按创建时间倒序。
func (s *Store) ListRequirements(ctx context.Context, f RequirementFilter) ([]domain.Requirement, error) {
	query := `SELECT id, owner_id, activity_name, purpose, description, status, current_task_id,
		current_candidate, attempt_count, created_at, updated_at FROM requirements`
	var args []any
	var where []string
	if f.OwnerID != "" {
		where = append(where, "owner_id = ?")
		args = append(args, f.OwnerID)
	}
	if len(f.Statuses) > 0 {
		parts := make([]string, 0, len(f.Statuses))
		for _, st := range f.Statuses {
			parts = append(parts, "?")
			args = append(args, string(st))
		}
		where = append(where, "status IN ("+strings.Join(parts, ",")+")")
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at DESC"
	if f.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询需求列表失败: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Requirement{}
	for rows.Next() {
		r, err := scanRequirement(rows)
		if err != nil {
			return nil, fmt.Errorf("读取需求列表失败: %w", err)
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// UpdateDescription 更新需求当前的图片描述（历史生成任务里的描述快照不受影响）。
func (s *Store) UpdateDescription(ctx context.Context, id, description string, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE requirements SET description = ?, updated_at = ? WHERE id = ?`,
		description, formatTime(now), id)
	if err != nil {
		return fmt.Errorf("更新描述失败: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// UpdateRequirementStatus 更新需求状态与当前关联对象。
func (s *Store) UpdateRequirementStatus(ctx context.Context, id string, status domain.RequirementStatus,
	currentTaskID, currentCandidate string, attemptCount int, now time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE requirements SET status = ?, current_task_id = ?,
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

// rowScanner 兼容 *sql.Row 与 *sql.Rows。
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRequirement(sc rowScanner) (*domain.Requirement, error) {
	var (
		r         domain.Requirement
		status    string
		createdAt string
		updatedAt string
	)
	if err := sc.Scan(&r.ID, &r.OwnerID, &r.ActivityName, &r.Purpose, &r.Description, &status,
		&r.CurrentTaskID, &r.CurrentCandidate, &r.AttemptCount, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	r.Status = domain.RequirementStatus(status)
	r.CreatedAt = parseTime(createdAt)
	r.UpdatedAt = parseTime(updatedAt)
	return &r, nil
}
