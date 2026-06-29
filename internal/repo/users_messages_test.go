package repo

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"callme/internal/model"
)

func TestUserDomainAndDeleteLifecycle(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "users.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	store := NewStore(db)
	ctx := context.Background()
	if err := store.EnsureDefaultDomain(ctx); err != nil {
		t.Fatalf("ensure default domain: %v", err)
	}
	if err := store.UpsertDomain(ctx, &model.Domain{ID: "ops", Name: "Ops", Enabled: true}); err != nil {
		t.Fatalf("upsert domain: %v", err)
	}
	user := &model.User{ID: "u1", Username: "user", Role: model.UserRoleVIP, Roles: []model.UserRole{model.UserRoleVIP}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if user.Role != model.UserRoleVIP || user.MaxSessions != model.DefaultMaxSessionsForRoles(user.Roles) {
		t.Fatalf("create user should normalize role and max sessions: %+v", user)
	}
	if err := store.SetUserDomains(ctx, "", []string{"ops"}); err == nil {
		t.Fatal("empty user id should fail")
	}
	if err := store.SetUserDomains(ctx, user.ID, []string{"ops", "ops", "", "missing"}); err != nil {
		t.Fatalf("set user domains: %v", err)
	}
	domains, err := store.ListUserDomainIDs(ctx, user.ID)
	if err != nil {
		t.Fatalf("list user domains: %v", err)
	}
	if len(domains) != 1 || domains[0] != "ops" {
		t.Fatalf("domains = %+v", domains)
	}

	start := time.Now()
	sess := &model.Session{ID: "s1", ClientID: user.ID, UserID: user.ID, Status: model.SessionStatusClosed, CreatedAt: start}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.DeleteUser(ctx, user.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if _, err := store.GetUser(ctx, user.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted user lookup error = %v", err)
	}
	deletedSession, err := store.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("get session after user delete: %v", err)
	}
	if deletedSession.UserID != "" {
		t.Fatalf("deleted user's sessions should be anonymized: %+v", deletedSession)
	}
	if err := store.DeleteUser(ctx, user.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("delete missing user error = %v", err)
	}
}

func TestSettingsAndMessageCopyBoundaries(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "settings-messages.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	store := NewStore(db)
	ctx := context.Background()

	if err := store.PutSetting(ctx, "bad", func() {}); err == nil {
		t.Fatal("unmarshalable setting should fail")
	}
	var out struct{ Name string }
	ok, err := store.GetSetting(ctx, "missing", &out)
	if err != nil || ok {
		t.Fatalf("missing setting ok=%v err=%v", ok, err)
	}
	if err := store.PutSetting(ctx, "good", map[string]string{"name": "callme"}); err != nil {
		t.Fatalf("put setting: %v", err)
	}
	ok, err = store.GetSetting(ctx, "good", &out)
	if err != nil || !ok || out.Name != "callme" {
		t.Fatalf("get setting ok=%v out=%+v err=%v", ok, out, err)
	}

	created := time.Now()
	for _, sess := range []*model.Session{
		{ID: "source", ClientID: "u1", UserID: "u1", Status: model.SessionStatusClosed, CreatedAt: created},
		{ID: "target", ClientID: "u1", UserID: "u1", Status: model.SessionStatusActive, CreatedAt: created},
	} {
		if err := store.CreateSession(ctx, sess); err != nil {
			t.Fatalf("create session %s: %v", sess.ID, err)
		}
	}
	if err := store.CreateMessage(ctx, &model.Message{ID: "m1", SessionID: "source", Role: model.MessageRoleUser, Content: "hello", CreatedAt: created}); err != nil {
		t.Fatalf("create message: %v", err)
	}
	if err := store.CopyMessagesToSession(ctx, "source", "target", created.Add(time.Minute)); err != nil {
		t.Fatalf("copy messages: %v", err)
	}
	msgs, err := store.ListMessages(ctx, "target")
	if err != nil {
		t.Fatalf("list target messages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].SessionID != "target" || msgs[0].Content != "hello" {
		t.Fatalf("copied messages = %+v", msgs)
	}
	if _, err := store.GetMessage(ctx, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing message error = %v", err)
	}
}
