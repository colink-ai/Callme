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
		Type:               "hermes",
		CliPath:            "hermes-custom",
		DefaultModel:       "glm-6",
		APIURL:             "https://new.example/v1",
		APIToken:           "****",
		SystemPrompt:       "new prompt",
		SupportsMultimodal: true,
	})
	if err != nil {
		t.Fatalf("update agent settings: %v", err)
	}
	spec := svc.AgentSpec()
	if spec.APIToken != "secret-token" || spec.DefaultModel != "glm-6" || spec.CliPath != "hermes-custom" || !spec.SupportsMultimodal {
		t.Fatalf("token should be retained and settings updated, spec=%+v", spec)
	}
	if caps := svc.GetAgentCapabilities(); !caps.SupportsMultimodal || caps.DefaultModel != "glm-6" {
		t.Fatalf("capabilities should expose active model flags, got %+v", caps)
	}

	reloaded := NewService(store, config.AgentConfig{Type: "mock", CliPath: "mock"}, config.SessionConfig{}, zap.NewNop())
	if reloaded.AgentSpec().DefaultModel != "glm-6" {
		t.Fatalf("settings should reload from db, got %+v", reloaded.AgentSpec())
	}
}

func TestAgentProfilesSwitchAndTokenRetention(t *testing.T) {
	ctx := context.Background()
	svc, store := newSettingsService(t)

	initial := svc.GetAgentProfiles()
	if len(initial.Profiles) != 1 || initial.ActiveProfileID != "default" {
		t.Fatalf("initial profiles = %+v", initial)
	}
	if initial.Profiles[0].Settings.APIToken != "secr****oken" {
		t.Fatalf("profile token should be masked, got %q", initial.Profiles[0].Settings.APIToken)
	}

	next := model.AgentProfilesSettings{
		ActiveProfileID: "backup",
		Profiles: []model.AgentProfile{
			{
				ID:   "default",
				Name: "默认配置",
				Settings: model.AgentSettings{
					Type:         "hermes",
					CliPath:      "hermes",
					DefaultModel: "glm-5",
					APIURL:       "https://example.test/v1",
					APIToken:     "****",
				},
			},
			{
				ID:   "backup",
				Name: "备用配置",
				Settings: model.AgentSettings{
					Type:         "mock",
					CliPath:      "mock-agent",
					DefaultModel: "mock-model",
					APIURL:       "https://backup.example/v1",
					APIToken:     "backup-token",
				},
			},
		},
	}
	if err := svc.UpdateAgentProfiles(ctx, next); err != nil {
		t.Fatalf("update agent profiles: %v", err)
	}
	if spec := svc.AgentSpec(); spec.Type != "mock" || spec.DefaultModel != "mock-model" || spec.APIToken != "backup-token" {
		t.Fatalf("active profile spec = %+v", spec)
	}
	got := svc.GetAgentProfiles()
	if got.Profiles[1].Settings.APIToken != "back****oken" {
		t.Fatalf("backup token should be masked, got %+v", got.Profiles[1].Settings)
	}

	reloaded := NewService(store, config.AgentConfig{Type: "hermes", CliPath: "hermes"}, config.SessionConfig{}, zap.NewNop())
	if spec := reloaded.AgentSpec(); spec.Type != "mock" || spec.DefaultModel != "mock-model" {
		t.Fatalf("reloaded active profile spec = %+v", spec)
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

func TestAgentSpecUsesRuntimeRootDefaultDomain(t *testing.T) {
	db, err := repo.Open("sqlite", filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := repo.NewStore(db)
	agentCfg := config.AgentConfig{
		Type:        "hermes",
		CliPath:     "hermes",
		RuntimeRoot: filepath.Join(t.TempDir(), "runtime"),
	}
	agentCfg.HermesHome = agentCfg.RuntimeHomeForDomain(config.DefaultDomainID)
	agentCfg.WorkDir = agentCfg.WorkDirForDomain(config.DefaultDomainID)
	svc := NewService(store, agentCfg, config.SessionConfig{}, zap.NewNop())
	spec := svc.AgentSpec()
	if spec.RuntimeHome == "" || spec.RuntimeHome != spec.HermesHome {
		t.Fatalf("runtime home should be mirrored for compatibility, spec=%+v", spec)
	}
	if want := filepath.Join(agentCfg.RuntimeRoot, model.DefaultDomainID, "hermes", "home"); spec.RuntimeHome != want {
		t.Fatalf("runtime home = %q, want %q", spec.RuntimeHome, want)
	}
}

func TestAgentProfilesNormalizationEdges(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSettingsService(t)
	if err := svc.UpdateAgentProfiles(ctx, model.AgentProfilesSettings{}); err != nil {
		t.Fatalf("empty profiles should normalize to current default: %v", err)
	}
	if profiles := svc.GetAgentProfiles(); len(profiles.Profiles) != 1 || profiles.ActiveProfileID == "" {
		t.Fatalf("empty profiles not normalized: %+v", profiles)
	}

	err := svc.UpdateAgentProfiles(ctx, model.AgentProfilesSettings{
		ActiveProfileID: "missing",
		Profiles: []model.AgentProfile{{
			ID:   "",
			Name: "",
			Settings: model.AgentSettings{
				Type:         "",
				CliPath:      "",
				DefaultModel: "edge-model",
				APIURL:       "https://edge.example/v1",
				APIToken:     "tiny",
			},
		}},
	})
	if err != nil {
		t.Fatalf("normalize edge profile: %v", err)
	}
	profiles := svc.GetAgentProfiles()
	if profiles.ActiveProfileID != profiles.Profiles[0].ID {
		t.Fatalf("missing active id should fall back to first profile: %+v", profiles)
	}
	if profiles.Profiles[0].Name == "" || profiles.Profiles[0].Settings.Type != "hermes" || profiles.Profiles[0].Settings.CliPath != "hermes" {
		t.Fatalf("profile defaults not filled: %+v", profiles.Profiles[0])
	}
	if profiles.Profiles[0].Settings.APIToken != "****" {
		t.Fatalf("short token should be fully masked, got %q", profiles.Profiles[0].Settings.APIToken)
	}
}

func TestUpdateAgentSettingsDefaultPathAndMaskedToken(t *testing.T) {
	ctx := context.Background()
	svc, _ := newSettingsService(t)
	if err := svc.UpdateAgentSettings(ctx, model.AgentSettings{
		Type:         "hermes",
		DefaultModel: "no-cli-model",
		APIURL:       "https://example.test/v1",
		APIToken:     "****",
	}); err != nil {
		t.Fatalf("update default cli: %v", err)
	}
	spec := svc.AgentSpec()
	if spec.CliPath != "hermes" || spec.APIToken != "secret-token" || spec.DefaultModel != "no-cli-model" {
		t.Fatalf("default cli or token retention failed: %+v", spec)
	}
	if err := svc.UpdateAgentSettings(ctx, model.AgentSettings{
		Type:         "custom",
		DefaultModel: "custom-model",
		APIURL:       "https://custom.example/v1",
		APIToken:     "",
	}); err != nil {
		t.Fatalf("update custom type: %v", err)
	}
	if spec := svc.AgentSpec(); spec.Type != "custom" || spec.CliPath != "" || spec.APIToken != "secret-token" {
		t.Fatalf("custom type should keep empty cli and retained token: %+v", spec)
	}
}
