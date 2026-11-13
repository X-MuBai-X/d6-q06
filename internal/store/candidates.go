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

// InsertCandidate 新增候选。
func (s *Store) InsertCandidate(ctx context.Context, c *domain.Candidate) error {
	return s.InsertCandidateTx(ctx, s.db, c)
}

// GetCandidate 按 ID 读取候选。
func (s *Store) GetCandidate(ctx context.Context, id string) (*domain.Candidate, error) {
	return s.GetCandidateTx(ctx, s.db, id)
}

// GetCurrentCandidate 读取需求的当前候选。
func (s *Store) GetCurrentCandidate(ctx context.Context, requirementID string) (*domain.Candidate, error) {
	return s.GetCurrentCandidateTx(ctx, s.db, requirementID)
}

// ListCandidates 按需求列出全部候选（含历史），按尝试序号升序。
func (s *Store) ListCandidates(ctx context.Context, requirementID string) ([]domain.Candidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, task_id, requirement_id, attempt_no, image_url, review_status,
		review_reason, reviewer_id, reviewed_at, is_current, created_at FROM candidates
		WHERE requirement_id = ? ORDER BY attempt_no ASC`, requirementID)
	if err != nil {
		return nil, fmt.Errorf("查询候选列表失败: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Candidate{}
	for rows.Next() {
		c, err := scanCandidate(rows)
		if err != nil {
			return nil, fmt.Errorf("读取候选列表失败: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// UpdateCandidateReview 写回复核结论。
func (s *Store) UpdateCandidateReview(ctx context.Context, c *domain.Candidate) error {
	return s.UpdateCandidateReviewTx(ctx, s.db, c)
}

// ClearCurrentCandidates 把需求的全部候选标记为历史。
func (s *Store) ClearCurrentCandidates(ctx context.Context, requirementID string) error {
	return s.ClearCurrentCandidatesTx(ctx, s.db, requirementID)
}

func scanCandidate(sc rowScanner) (*domain.Candidate, error) {
	var (
		c          domain.Candidate
		status     string
		reviewedAt string
		createdAt  string
		isCurrent  int
	)
	if err := sc.Scan(&c.ID, &c.TaskID, &c.RequirementID, &c.AttemptNo, &c.ImageURL, &status,
		&c.ReviewReason, &c.ReviewerID, &reviewedAt, &isCurrent, &createdAt); err != nil {
		return nil, err
	}
	c.ReviewStatus = domain.ReviewStatus(status)
	c.ReviewedAt = parseTime(reviewedAt)
	c.CreatedAt = parseTime(createdAt)
	c.IsCurrent = isCurrent == 1
	return &c, nil
}

// InsertEvent 写入审计事件。
func (s *Store) InsertEvent(ctx context.Context, e *domain.Event) error {
	return s.InsertEventTx(ctx, s.db, e)
}

// ListEvents 按需求列出审计事件，按时间升序。
func (s *Store) ListEvents(ctx context.Context, requirementID string) ([]domain.Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, requirement_id, task_id, actor_id, actor_role, kind, detail, created_at
		FROM events WHERE requirement_id = ? ORDER BY id ASC`, requirementID)
	if err != nil {
		return nil, fmt.Errorf("查询审计事件失败: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("读取审计事件失败: %w", err)
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// ListAnomalyEvents 列出运营需要跟进的异常事件，按时间倒序。
// kinds 是异常种类清单，由业务层给出（service.AnomalyKinds）；存储层不再自带重复字面量。
func (s *Store) ListAnomalyEvents(ctx context.Context, kinds []string, limit int) ([]domain.Event, error) {
	if len(kinds) == 0 {
		return []domain.Event{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(kinds)), ", ")
	query := fmt.Sprintf(`SELECT id, requirement_id, task_id, actor_id, actor_role, kind, detail, created_at
		FROM events WHERE kind IN (%s) ORDER BY id DESC`, placeholders)
	args := make([]any, 0, len(kinds))
	for _, k := range kinds {
		args = append(args, k)
	}
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询异常事件失败: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("读取异常事件失败: %w", err)
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// ListRecentEvents 列出最近的审计事件（全部类型），用于运营总览。
func (s *Store) ListRecentEvents(ctx context.Context, limit int) ([]domain.Event, error) {
	query := `SELECT id, requirement_id, task_id, actor_id, actor_role, kind, detail, created_at FROM events ORDER BY id DESC`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("查询最近事件失败: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Event{}
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("读取最近事件失败: %w", err)
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

func scanEvent(sc rowScanner) (*domain.Event, error) {
	var (
		e         domain.Event
		role      string
		createdAt string
	)
	if err := sc.Scan(&e.ID, &e.RequirementID, &e.TaskID, &e.ActorID, &role, &e.Kind, &e.Detail, &createdAt); err != nil {
		return nil, err
	}
	e.ActorRole = domain.Role(role)
	e.CreatedAt = parseTime(createdAt)
	return &e, nil
}

// InsertUser 新增账号。
func (s *Store) InsertUser(ctx context.Context, u *domain.User) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO users (id, name, role, password_hash, salt) VALUES (?, ?, ?, ?, ?)`,
		u.ID, u.Name, string(u.Role), u.PasswordHash, u.Salt)
	if err != nil {
		return fmt.Errorf("写入账号失败: %w", err)
	}
	return nil
}

// GetUser 按账号 ID 读取。
func (s *Store) GetUser(ctx context.Context, id string) (*domain.User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, role, password_hash, salt FROM users WHERE id = ?`, id)
	var (
		u    domain.User
		role string
	)
	err := row.Scan(&u.ID, &u.Name, &role, &u.PasswordHash, &u.Salt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读取账号失败: %w", err)
	}
	u.Role = domain.Role(role)
	return &u, nil
}

// InsertSession 写入会话令牌。
func (s *Store) InsertSession(ctx context.Context, token, userID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token, user_id, created_at) VALUES (?, ?, ?)`,
		token, userID, formatTime(now))
	if err != nil {
		return fmt.Errorf("写入会话失败: %w", err)
	}
	return nil
}

// GetSessionUser 依据会话令牌读取账号；令牌无效或过期返回 ErrUnauthorized。
func (s *Store) GetSessionUser(ctx context.Context, token string) (*domain.User, error) {
	if token == "" {
		return nil, domain.ErrUnauthorized
	}
	row := s.db.QueryRowContext(ctx, `SELECT u.id, u.name, u.role, u.password_hash, u.salt
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token = ?`, token)
	var (
		u    domain.User
		role string
	)
	err := row.Scan(&u.ID, &u.Name, &role, &u.PasswordHash, &u.Salt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrUnauthorized
	}
	if err != nil {
		return nil, fmt.Errorf("读取会话失败: %w", err)
	}
	u.Role = domain.Role(role)
	return &u, nil
}

// DeleteSession 注销会话。
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token); err != nil {
		return fmt.Errorf("注销会话失败: %w", err)
	}
	return nil
}
