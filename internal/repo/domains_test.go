package repo

import (
	"context"
	"path/filepath"
	"testing"

	"callme/internal/model"
)

func TestDomainAndKnowledgeSourceLifecycle(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "domains.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	store := NewStore(db)
	ctx := context.Background()
	if err := store.EnsureDefaultDomain(ctx); err != nil {
		t.Fatalf("ensure default domain: %v", err)
	}
	if err := store.UpsertDomain(ctx, &model.Domain{
		ID:             "ops",
		Name:           "Ops",
		Description:    "operations knowledge",
		DefaultAgentID: "agent-ops",
		Enabled:        true,
	}); err != nil {
		t.Fatalf("upsert ops domain: %v", err)
	}
	if err := store.UpsertDomain(ctx, &model.Domain{ID: "disabled", Name: "Disabled", Enabled: false}); err != nil {
		t.Fatalf("upsert disabled domain: %v", err)
	}
	if err := store.UpsertKnowledgeSource(ctx, &model.KnowledgeSource{
		ID:       "kb-http",
		DomainID: "ops",
		Name:     "HTTP KB",
		Type:     "http",
		URL:      "http://127.0.0.1:9100/mcp",
		Headers:  map[string]string{"Authorization": "Bearer test"},
		Args:     []string{"--port", "9100"},
		Env:      map[string]string{"MODE": "test"},
		Enabled:  true,
	}); err != nil {
		t.Fatalf("upsert source: %v", err)
	}
	if err := store.UpsertKnowledgeSource(ctx, &model.KnowledgeSource{ID: "kb-disabled", DomainID: "ops", Name: "Disabled KB", Enabled: false}); err != nil {
		t.Fatalf("upsert disabled source: %v", err)
	}

	enabled, err := store.ListDomains(ctx, false)
	if err != nil {
		t.Fatalf("list enabled domains: %v", err)
	}
	if len(enabled) != 2 || enabled[0].ID != model.DefaultDomainID || enabled[1].ID != "ops" {
		t.Fatalf("enabled domains = %+v", enabled)
	}
	all, err := store.ListDomains(ctx, true)
	if err != nil {
		t.Fatalf("list all domains: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("all domains len = %d, want 3", len(all))
	}

	ops, err := store.GetDomain(ctx, "ops")
	if err != nil {
		t.Fatalf("get ops domain: %v", err)
	}
	if ops.DefaultAgentID != "agent-ops" || len(ops.KnowledgeSources) != 2 {
		t.Fatalf("ops domain not hydrated: %+v", ops)
	}
	if ops.KnowledgeSources[0].Headers["Authorization"] == "" || ops.KnowledgeSources[0].Env["MODE"] != "test" {
		t.Fatalf("source maps not decoded: %+v", ops.KnowledgeSources[0])
	}

	activeSources, err := store.ListKnowledgeSources(ctx, "ops", false)
	if err != nil {
		t.Fatalf("list active sources: %v", err)
	}
	if len(activeSources) != 1 || activeSources[0].ID != "kb-http" {
		t.Fatalf("active sources = %+v", activeSources)
	}
	if err := store.DeleteKnowledgeSource(ctx, "kb-http"); err != nil {
		t.Fatalf("delete source: %v", err)
	}
	activeSources, err = store.ListKnowledgeSources(ctx, "ops", true)
	if err != nil {
		t.Fatalf("list sources after delete: %v", err)
	}
	if len(activeSources) != 1 || activeSources[0].ID != "kb-disabled" {
		t.Fatalf("sources after delete = %+v", activeSources)
	}
}

func TestDomainsForUserRespectGrantsAndDisabledDomains(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "domain-grants.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	store := NewStore(db)
	ctx := context.Background()
	if err := store.EnsureDefaultDomain(ctx); err != nil {
		t.Fatalf("ensure default: %v", err)
	}
	for _, d := range []*model.Domain{
		{ID: "ops", Name: "Ops", Enabled: true},
		{ID: "archive", Name: "Archive", Enabled: false},
	} {
		if err := store.UpsertDomain(ctx, d); err != nil {
			t.Fatalf("upsert %s: %v", d.ID, err)
		}
	}
	user := &model.User{ID: "u1", Username: "user", Role: model.UserRoleNormal, Roles: []model.UserRole{model.UserRoleNormal}}
	admin := &model.User{ID: "a1", Username: "admin", Role: model.UserRoleAdmin, Roles: []model.UserRole{model.UserRoleAdmin}}
	if err := store.SetUserDomains(ctx, user.ID, []string{"ops", "archive"}); err != nil {
		t.Fatalf("set grants: %v", err)
	}

	userDomains, err := store.ListDomainsForUser(ctx, user, false)
	if err != nil {
		t.Fatalf("list user domains: %v", err)
	}
	if len(userDomains) != 2 || userDomains[0].ID != model.DefaultDomainID || userDomains[1].ID != "ops" {
		t.Fatalf("user enabled domains = %+v", userDomains)
	}
	userAll, err := store.ListDomainsForUser(ctx, user, true)
	if err != nil {
		t.Fatalf("list user all domains: %v", err)
	}
	if len(userAll) != 3 {
		t.Fatalf("user all domains len = %d, want 3", len(userAll))
	}
	adminDomains, err := store.ListDomainsForUser(ctx, admin, true)
	if err != nil {
		t.Fatalf("list admin domains: %v", err)
	}
	if len(adminDomains) != 3 {
		t.Fatalf("admin domains len = %d, want 3", len(adminDomains))
	}
}
