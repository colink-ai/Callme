package settings

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"callme/internal/config"
	"callme/internal/model"
	"callme/internal/repo"

	"go.uber.org/zap"
)

func newSettingsService(t *testing.T) (*Service, *repo.Store) {
	t.Helper()
	db, err := repo.Open("sqlite", filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := repo.NewStore(db)
	agentCfg := config.AgentConfig{
		Type:          "hermes",
		CliPath:       "hermes",
		DefaultModel:  "glm-5",
		APIURL:        "https://example.test/v1",
		APIToken:      "secret-token",
		HermesHome:    filepath.Join(t.TempDir(), "home"),
		SystemPrompt:  "system",
		PromptTimeout: time.Minute,
	}
	sessionCfg := config.SessionConfig{MaxActive: 2, MaxQueue: 3}
	return NewService(store, agentCfg, sessionCfg, zap.NewNop()), store
}

func TestAgentSettingsMaskingAndTokenRetention(t *testing.T) {
	ctx := context.Background()
	svc, store := newSettingsService(t)

	got := svc.GetAgentSettings()
	if got.APIToken != "secr****oken" {
		t.Fatalf("masked token = %q", got.APIToken)
	}
	if spec := svc.AgentSpec(); spec.APIToken != "secret-token" || spec.DefaultModel != "glm-5" {
		t.Fatalf("initial spec = %+v", spec)
	}

	err := svc.UpdateAgentSettings(ctx, model.AgentSettings{
		Type:         "hermes",
		CliPath:      "hermes-custom",
		DefaultModel: "glm-6",
		APIURL:       "https://new.example/v1",
		APIToken:     "****",
		SystemPrompt: "new prompt",
	})
	if err != nil {
		t.Fatalf("update agent settings: %v", err)
	}
	spec := svc.AgentSpec()
	if spec.APIToken != "secret-token" || spec.DefaultModel != "glm-6" || spec.CliPath != "hermes-custom" {
		t.Fatalf("token should be retained and settings updated, spec=%+v", spec)
	}

	reloaded := NewService(store, config.AgentConfig{Type: "mock", CliPath: "mock"}, config.SessionConfig{}, zap.NewNop())
	if reloaded.AgentSpec().DefaultModel != "glm-6" {
		t.Fatalf("settings should reload from db, got %+v", reloaded.AgentSpec())
	}
}

func TestPoolSettingsUpdateAndDefaults(t *testing.T) {
	ctx := context.Background()
	svc, store := newSettingsService(t)
	if pool := svc.PoolSettings(); pool.MaxActive != 2 || pool.MaxQueue != 3 {
		t.Fatalf("initial pool = %+v", pool)
	}
	if err := svc.UpdatePoolSettings(ctx, model.PoolSettings{MaxActive: 0, MaxQueue: -1}); err != nil {
		t.Fatalf("update pool: %v", err)
	}
	if pool := svc.PoolSettings(); pool.MaxActive != 1 || pool.MaxQueue != 0 {
		t.Fatalf("normalized pool = %+v", pool)
	}
	reloaded := NewService(store, config.AgentConfig{}, config.SessionConfig{MaxActive: 9, MaxQueue: 9}, zap.NewNop())
	if pool := reloaded.PoolSettings(); pool.MaxActive != 1 || pool.MaxQueue != 0 {
		t.Fatalf("reloaded pool = %+v", pool)
	}
}
