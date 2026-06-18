package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"callme/internal/model"
	"callme/internal/repo"

	"github.com/google/uuid"
)

var (
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	ErrUsernameTaken      = errors.New("用户名已存在")
	ErrUnauthorized       = errors.New("请先登录")
	ErrForbidden          = errors.New("无权限访问")
	ErrCannotDeleteSelf   = errors.New("不能删除当前登录用户")
	ErrLastAdmin          = errors.New("不能删除最后一个管理员")
)

type Service struct {
	store    *repo.Store
	tokenTTL time.Duration
}

func NewService(store *repo.Store, tokenTTL time.Duration) *Service {
	return &Service{store: store, tokenTTL: tokenTTL}
}

type LoginResult struct {
	Token     string      `json:"token"`
	ExpiresAt time.Time   `json:"expiresAt"`
	User      *model.User `json:"user"`
}

func (s *Service) Register(ctx context.Context, username, password string) (*LoginResult, error) {
	username = strings.TrimSpace(username)
	if username == "" || len(password) < 4 {
		return nil, errors.New("用户名不能为空，密码至少 4 位")
	}
	if _, err := s.store.GetUserByUsername(ctx, username); err == nil {
		return nil, ErrUsernameTaken
	} else if err != sql.ErrNoRows {
		return nil, err
	}

	users, err := s.store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	role := model.UserRoleNormal
	if len(users) == 0 {
		role = model.UserRoleAdmin
	}

	now := time.Now()
	u := &model.User{
		ID:           uuid.New().String(),
		Username:     username,
		PasswordHash: hashPassword(password),
		Role:         role,
		Roles:        []model.UserRole{role},
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.store.CreateUser(ctx, u); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrUsernameTaken
		}
		return nil, err
	}
	return s.issue(ctx, u)
}

func (s *Service) Login(ctx context.Context, username, password string) (*LoginResult, error) {
	u, err := s.store.GetUserByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if !verifyPassword(password, u.PasswordHash) {
		return nil, ErrInvalidCredentials
	}
	return s.issue(ctx, u)
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.DeleteAuthToken(ctx, token)
}

func (s *Service) UserByToken(ctx context.Context, token string) (*model.User, error) {
	if token == "" {
		return nil, ErrUnauthorized
	}
	tok, err := s.store.GetAuthToken(ctx, token)
	if err != nil {
		return nil, ErrUnauthorized
	}
	if time.Now().After(tok.ExpiresAt) {
		_ = s.store.DeleteAuthToken(ctx, token)
		return nil, ErrUnauthorized
	}
	return s.store.GetUser(ctx, tok.UserID)
}

func (s *Service) ListUsers(ctx context.Context) ([]*model.User, error) {
	return s.store.ListUsers(ctx)
}

func (s *Service) UpdateRole(ctx context.Context, id string, role model.UserRole) error {
	return s.UpdateRoles(ctx, id, []model.UserRole{role}, 0)
}

func (s *Service) UpdateRoles(ctx context.Context, id string, roles []model.UserRole, maxSessions int) error {
	for _, role := range roles {
		if !model.IsValidUserRole(role) {
			return errors.New("角色无效")
		}
	}
	roles = model.NormalizeRoles(roles)
	if !containsRole(roles, model.UserRoleAdmin) {
		u, err := s.store.GetUser(ctx, id)
		if err != nil {
			return err
		}
		if u.HasRole(model.UserRoleAdmin) {
			adminCount, err := s.store.CountUsersByRole(ctx, model.UserRoleAdmin)
			if err != nil {
				return err
			}
			if adminCount <= 1 {
				return ErrLastAdmin
			}
		}
	}
	if maxSessions <= 0 {
		maxSessions = model.DefaultMaxSessionsForRoles(roles)
	}
	if maxSessions > 50 {
		return errors.New("个人并发上限不能超过 50")
	}
	return s.store.UpdateUserRolesAndLimit(ctx, id, roles, maxSessions)
}

func containsRole(roles []model.UserRole, target model.UserRole) bool {
	for _, role := range roles {
		if role == target {
			return true
		}
	}
	return false
}

func (s *Service) DeleteUser(ctx context.Context, actorID, id string) error {
	if id == "" {
		return errors.New("用户 ID 不能为空")
	}
	if actorID == id {
		return ErrCannotDeleteSelf
	}
	u, err := s.store.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if u.HasRole(model.UserRoleAdmin) {
		adminCount, err := s.store.CountUsersByRole(ctx, model.UserRoleAdmin)
		if err != nil {
			return err
		}
		if adminCount <= 1 {
			return ErrLastAdmin
		}
	}
	return s.store.DeleteUser(ctx, id)
}

func (s *Service) issue(ctx context.Context, u *model.User) (*LoginResult, error) {
	now := time.Now()
	token, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	expiresAt := now.Add(s.tokenTTL)
	if err := s.store.SaveAuthToken(ctx, &model.AuthToken{
		Token:     token,
		UserID:    u.ID,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}); err != nil {
		return nil, err
	}
	_ = s.store.DeleteExpiredAuthTokens(ctx, now)
	return &LoginResult{Token: token, ExpiresAt: expiresAt, User: u}, nil
}

func hashPassword(password string) string {
	salt, err := randomHex(16)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256([]byte(salt + ":" + password))
	return fmt.Sprintf("sha256$%s$%s", salt, hex.EncodeToString(sum[:]))
}

func verifyPassword(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 3 || parts[0] != "sha256" {
		return false
	}
	sum := sha256.Sum256([]byte(parts[1] + ":" + password))
	expected := []byte(parts[2])
	actual := []byte(hex.EncodeToString(sum[:]))
	return subtle.ConstantTimeCompare(expected, actual) == 1
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
