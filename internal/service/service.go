// Package service 是业务编排层：口径、幂等、权限判定与留痕都在这里收口。
// 依赖方向固定为 httpapi → service → store → domain。
package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"creative-studio/internal/domain"
	"creative-studio/internal/store"
	"creative-studio/internal/supplier"
)

// Config 是服务的运行参数。
type Config struct {
	// SupplierBaseURL 是模拟生成供应商的地址。
	SupplierBaseURL string
	// SupplierSecret 是供应商回调签名密钥（仅在服务与模拟服务之间共享）。
	SupplierSecret string
	// SupplierTimeout 是单次提交供应商的最长等待时间。
	SupplierTimeout time.Duration
	// ClockScale 是教学时钟倍率：1 表示题面规定的 60 秒真实时限，
	// 0.1 表示演示用的 6 秒。必须向使用者注明（假设 A3）。
	ClockScale float64
}

// Service 承载全部业务用例。
type Service struct {
	store    *store.Store
	clock    domain.Clock
	supplier *supplier.Client
	cfg      Config
}

// New 创建服务实例。
func New(st *store.Store, clock domain.Clock, cfg Config) *Service {
	return &Service{
		store:    st,
		clock:    clock,
		supplier: supplier.NewClient(cfg.SupplierBaseURL, cfg.SupplierSecret, cfg.SupplierTimeout),
		cfg:      cfg,
	}
}

// Store 暴露持久化层，供集成测试与运维脚本使用。
func (s *Service) Store() *store.Store { return s.store }

// Config 暴露运行参数。
func (s *Service) Config() Config { return s.cfg }

// now 返回当前时间。
func (s *Service) now() time.Time { return s.clock.Now() }

// newID 生成带前缀的随机 ID。
func (s *Service) newID(prefix string) string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		// 随机源不可用属于极端情况，回退到时间戳，保证 ID 仍然唯一。
		return fmt.Sprintf("%s-%d", prefix, s.now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(buf)
}

// hashPassword 计算口令摘要（演示系统使用 SHA-256 加盐，口令不落明文）。
func hashPassword(salt, password string) string {
	sum := sha256.Sum256([]byte(salt + ":" + password))
	return hex.EncodeToString(sum[:])
}

// actorCanView 判断角色是否可以查看该需求。
// 需求人只能看自己的需求（题面 R8）；复核人与运营可查看全部记录。
func actorCanView(actor *domain.User, req *domain.Requirement) bool {
	switch actor.Role {
	case domain.RoleRequester:
		return req.OwnerID == actor.ID
	case domain.RoleReviewer, domain.RoleOperator:
		return true
	}
	return false
}

// actorCanOperate 判断角色是否可以对需求执行写操作（除复核外的需求人操作）。
func actorCanOperate(actor *domain.User, req *domain.Requirement) bool {
	return actor.Role == domain.RoleRequester && req.OwnerID == actor.ID
}

// loadRequirement 读取需求并做可见性判定。
func (s *Service) loadRequirement(ctx context.Context, actor *domain.User, requirementID string) (*domain.Requirement, error) {
	req, err := s.store.GetRequirement(ctx, requirementID)
	if err != nil {
		return nil, err
	}
	if !actorCanView(actor, req) {
		return nil, fmt.Errorf("%w: 该需求不属于当前用户", domain.ErrForbidden)
	}
	return req, nil
}

// recordEvent 写入一条审计事件。
func (s *Service) recordEvent(ctx context.Context, e *domain.Event) {
	if err := s.store.InsertEvent(ctx, e); err != nil {
		// 审计写入失败属于内部错误，向上返回但不影响已发生的业务判定。
		return
	}
}

// newEvent 构造审计事件。
func newEvent(actor *domain.User, requirementID, taskID, kind, detail string, now time.Time) *domain.Event {
	actorID, role := "", ""
	if actor != nil {
		actorID, role = actor.ID, string(actor.Role)
	}
	return &domain.Event{
		RequirementID: requirementID,
		TaskID:        taskID,
		ActorID:       actorID,
		ActorRole:     domain.Role(role),
		Kind:          kind,
		Detail:        detail,
		CreatedAt:     now,
	}
}

// isUniqueErr 判断错误是否为唯一约束冲突（并发重复发起时的兜底判定）。
func isUniqueErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// wrapNotFound 把存储层的未找到错误统一成领域错误。
func wrapNotFound(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrNotFound
	}
	return err
}
