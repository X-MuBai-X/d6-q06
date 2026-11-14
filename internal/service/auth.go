package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"creative-studio/internal/domain"
)

// Login 用账号与口令换取会话令牌。
func (s *Service) Login(ctx context.Context, login, password string) (string, *domain.User, error) {
	if login == "" || password == "" {
		return "", nil, fmt.Errorf("%w: 账号与口令不能为空", domain.ErrInvalidInput)
	}
	user, err := s.store.GetUser(ctx, login)
	if err != nil {
		if err == domain.ErrNotFound {
			return "", nil, fmt.Errorf("%w: 账号或口令不正确", domain.ErrUnauthorized)
		}
		return "", nil, err
	}
	if hashPassword(user.Salt, password) != user.PasswordHash {
		return "", nil, fmt.Errorf("%w: 账号或口令不正确", domain.ErrUnauthorized)
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("生成会话令牌失败: %w", err)
	}
	token := hex.EncodeToString(buf)
	if err := s.store.InsertSession(ctx, token, user.ID, s.now()); err != nil {
		return "", nil, err
	}
	return token, user, nil
}

// Logout 注销会话。
func (s *Service) Logout(ctx context.Context, token string) error {
	return s.store.DeleteSession(ctx, token)
}

// Authenticate 依据会话令牌识别当前身份。
func (s *Service) Authenticate(ctx context.Context, token string) (*domain.User, error) {
	user, err := s.store.GetSessionUser(ctx, token)
	if err != nil {
		return nil, domain.ErrUnauthorized
	}
	return user, nil
}

// SeedUser 预置账号（仅在初始化时调用）；已存在则保持原样，保证重启可重复。
func (s *Service) SeedUser(ctx context.Context, id, name string, role domain.Role, password string) error {
	if _, err := s.store.GetUser(ctx, id); err == nil {
		return nil
	}
	salt := "studio-" + id
	u := &domain.User{
		ID:           id,
		Name:         name,
		Role:         role,
		Salt:         salt,
		PasswordHash: hashPassword(salt, password),
	}
	return s.store.InsertUser(ctx, u)
}
