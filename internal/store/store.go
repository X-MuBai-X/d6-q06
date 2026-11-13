// Package store 提供基于 SQLite 的持久化实现。
// 本层只做数据存取与结构性约束，不含业务口径判断；口径全部在 service 层收口。
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Execer 抽象可执行 SQL 的对象，*sql.DB、*sql.Tx、*sql.Conn 均满足。
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store 封装数据库连接。
type Store struct {
	db *sql.DB
}

// Open 打开（并在需要时创建）数据库文件，执行建表语句并设置忙等待等参数。
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// SQLite 为单写模型，限制连接数可以避免写锁竞争演变为锁错误。
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("初始化表结构失败: %w", err)
	}
	return &Store{db: db}, nil
}

// Close 关闭数据库连接。
func (s *Store) Close() error { return s.db.Close() }

// DB 暴露底层连接，供集成测试使用。
func (s *Store) DB() *sql.DB { return s.db }

// withImmediateTx 在一个「立即加写锁」的事务中执行 fn。
// SQLite 的延迟事务会让两个并发事务都通过前置检查再在写入时冲突，
// BEGIN IMMEDIATE 可以把并发发起生成任务的判定串行化。
func (s *Store) withImmediateTx(ctx context.Context, fn func(tx Execer) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("获取数据库连接失败: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	if err := fn(conn); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

// formatTime 统一时间落库格式。
func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// parseTime 解析落库时间。
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
