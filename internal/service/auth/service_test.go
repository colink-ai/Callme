package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"callme/internal/model"
	"callme/internal/repo"
)

func newAuthService(t *testing.T, ttl time.Duration) (*Service, *repo.Store) {
	t.Helper()
	db, err := repo.Open("sqlite", filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := repo.NewStore(db)
	return NewService(store, ttl), store
}

func TestRegisterLoginTokenLifecycle(t *testing.T) {
	ctx := context.Background()
	svc, _ := newAuthService(t, time.Hour)

	first, err := svc.Register(ctx, " admin ", "pass1234")
	if err != nil {
		t.Fatalf("register first: %v", err)
	}
	if first.User.Role != model.UserRoleAdmin || !first.User.HasRole(model.UserRoleAdmin) {
		t.Fatalf("first user should be admin, got %+v", first.User)
	}
	if _, err := svc.Register(ctx, "admin", "pass1234"); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("duplicate username error = %v", err)
	}
	if _, err := svc.Login(ctx, "admin", "bad-pass"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("invalid login error = %v", err)
	}
	login, err := svc.Login(ctx, "admin", "pass1234")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if login.Token == "" {
		t.Fatal("login token should not be empty")
	}
	user, err := svc.UserByToken(ctx, login.Token)
	if err != nil || user.Username != "admin" {
		t.Fatalf("UserByToken = %+v, %v", user, err)
	}
	if err := svc.Logout(ctx, login.Token); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := svc.UserByToken(ctx, login.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("token after logout error = %v", err)
	}
}

func TestExpiredTokenIsRejected(t *testing.T) {
	ctx := context.Background()
	svc, _ := newAuthService(t, time.Nanosecond)
	result, err := svc.Register(ctx, "user", "pass1234")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := svc.UserByToken(ctx, result.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expired token error = %v", err)
	}
}

func TestUpdateRolesAndDeleteUserGuards(t *testing.T) {
	ctx := context.Background()
	svc, store := newAuthService(t, time.Hour)
	admin, _ := svc.Register(ctx, "admin", "pass1234")
	staff, _ := svc.Register(ctx, "staff", "pass1234")

	if err := svc.UpdateRoles(ctx, staff.User.ID, []model.UserRole{model.UserRoleVIP, model.UserRoleKnowledgeStaff}, 0); err != nil {
		t.Fatalf("update staff roles: %v", err)
	}
	updated, err := store.GetUser(ctx, staff.User.ID)
	if err != nil {
		t.Fatalf("get updated staff: %v", err)
	}
	if !updated.HasRole(model.UserRoleVIP) || !updated.HasRole(model.UserRoleKnowledgeStaff) || updated.MaxSessions != 2 {
		t.Fatalf("unexpected updated user: %+v", updated)
	}
	if err := svc.UpdateRoles(ctx, staff.User.ID, []model.UserRole{"bad"}, 0); err == nil {
		t.Fatal("invalid role should fail")
	}
	if err := svc.UpdateRoles(ctx, admin.User.ID, []model.UserRole{model.UserRoleNormal}, 0); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("removing last admin error = %v", err)
	}
	if err := svc.DeleteUser(ctx, admin.User.ID, admin.User.ID); !errors.Is(err, ErrCannotDeleteSelf) {
		t.Fatalf("delete self error = %v", err)
	}
	if err := svc.DeleteUser(ctx, admin.User.ID, staff.User.ID); err != nil {
		t.Fatalf("delete staff: %v", err)
	}
	if users, err := svc.ListUsers(ctx); err != nil || len(users) != 1 {
		t.Fatalf("users after delete len=%d err=%v", len(users), err)
	}
}
