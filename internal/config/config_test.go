package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAgentPromptTimeoutDefault(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  host: 127.0.0.1
  port: 8090
database:
  driver: sqlite
  dsn: data/callme.db
`)
	if cfg.Agent.PromptTimeout != 30*time.Minute {
		t.Fatalf("prompt timeout = %s, want 30m", cfg.Agent.PromptTimeout)
	}
}

func TestLoadAgentPromptTimeoutDisabled(t *testing.T) {
	cfg := loadTestConfig(t, `
agent:
  prompt_timeout: -1s
`)
	if cfg.Agent.PromptTimeout != -time.Second {
		t.Fatalf("prompt timeout = %s, want -1s", cfg.Agent.PromptTimeout)
	}
}

func TestApplyDefaultsAndAddr(t *testing.T) {
	cfg := loadTestConfig(t, `
server:
  host: 127.0.0.1
  port: 18090
agent:
  type: ""
  cli_path: ""
  hermes_home: ""
  work_dir: ""
auth:
  token_ttl: 0s
session:
  max_active: 0
  max_queue: -1
  max_per_client: 0
  idle_warn_after: 0s
  idle_close_after: 1s
  max_duration: 0s
  queue_poll_seconds: 0
feedback:
  distill_interval: 0s
  audit_interval: 0s
  notes_max_entries: 0
log:
  max_size: 0
  max_backups: 0
  max_age: 0
`)
	if got := cfg.Server.Addr(); got != "127.0.0.1:18090" {
		t.Fatalf("Addr=%q", got)
	}
	if cfg.Agent.Type != "hermes" || cfg.Agent.CliPath != "hermes" {
		t.Fatalf("agent defaults not applied: %+v", cfg.Agent)
	}
	if cfg.Agent.HermesHome != "data/hermes-home" || cfg.Agent.WorkDir != "data/workdir" {
		t.Fatalf("agent paths not defaulted: %+v", cfg.Agent)
	}
	if cfg.Auth.TokenTTL != 7*24*time.Hour {
		t.Fatalf("token ttl default=%s", cfg.Auth.TokenTTL)
	}
	if cfg.Session.MaxActive != 5 || cfg.Session.MaxQueue != 50 || cfg.Session.MaxPerClient != 1 {
		t.Fatalf("session capacity defaults not applied: %+v", cfg.Session)
	}
	if cfg.Session.IdleWarnAfter != 5*time.Minute || cfg.Session.IdleCloseAfter != 10*time.Minute {
		t.Fatalf("idle defaults not applied: %+v", cfg.Session)
	}
	if cfg.Session.MaxDuration != 2*time.Hour || cfg.Session.QueuePollSeconds != 5 {
		t.Fatalf("session duration defaults not applied: %+v", cfg.Session)
	}
	if cfg.Feedback.DistillCron != "0 * * * *" || cfg.Feedback.AuditCron != "*/10 * * * *" ||
		cfg.Feedback.DistillInterval != time.Hour || cfg.Feedback.AuditInterval != 10*time.Minute || cfg.Feedback.NotesMaxEntries != 200 {
		t.Fatalf("feedback defaults not applied: %+v", cfg.Feedback)
	}
	if cfg.Log.MaxSize != 100 || cfg.Log.MaxBackups != 3 || cfg.Log.MaxAge != 7 {
		t.Fatalf("log defaults not applied: %+v", cfg.Log)
	}
}

func TestAgentRuntimeRootDerivesDefaultDomainPaths(t *testing.T) {
	cfg := loadTestConfig(t, `
agent:
  runtime_root: data/agent-runtime
`)
	if cfg.Agent.HermesHome != "data/agent-runtime/domain-default/hermes/home" {
		t.Fatalf("default runtime home = %q", cfg.Agent.HermesHome)
	}
	if cfg.Agent.WorkDir != "data/agent-runtime/domain-default/hermes/workdir" {
		t.Fatalf("default work dir = %q", cfg.Agent.WorkDir)
	}
	if got := cfg.Agent.RuntimeHomeForDomain("Ops Team!"); got != "data/agent-runtime/opsteam/hermes/home" {
		t.Fatalf("sanitized domain runtime home = %q", got)
	}
	if got := cfg.Agent.RuntimeHomeForDomainAgent("Ops Team!", "Open Code"); got != "data/agent-runtime/opsteam/opencode/home" {
		t.Fatalf("agent runtime home = %q", got)
	}
}

func TestAgentRuntimeDirsAndFallbacks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runtime")
	agent := AgentConfig{Type: "Hermes", RuntimeRoot: root}
	if got := agent.DomainRuntimeDir(" !!! "); got != filepath.Join(root, DefaultDomainID) {
		t.Fatalf("empty sanitized domain dir = %q", got)
	}
	if got := agent.AgentRuntimeDirForDomain("Ops_01", " !!! "); got != filepath.Join(root, "ops_01", "hermes") {
		t.Fatalf("fallback agent runtime dir = %q", got)
	}
	if err := agent.EnsureDomainRuntimeDirs("Ops_01", "Claude Code"); err != nil {
		t.Fatalf("ensure runtime dirs: %v", err)
	}
	for _, dir := range []string{
		filepath.Join(root, "ops_01", "claudecode", "home"),
		filepath.Join(root, "ops_01", "claudecode", "workdir"),
	} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("runtime dir missing %s info=%+v err=%v", dir, info, err)
		}
	}

	legacy := AgentConfig{HermesHome: "legacy-home", WorkDir: "legacy-work"}
	if legacy.DomainRuntimeDir("ops") != "" {
		t.Fatal("legacy config without runtime root should not expose domain dir")
	}
	if err := legacy.EnsureDomainRuntimeDirs("ops", "hermes"); err != nil {
		t.Fatalf("legacy ensure should be no-op: %v", err)
	}
	if legacy.RuntimeHomeForDomain("ops") != "legacy-home" || legacy.WorkDirForDomain("ops") != "legacy-work" {
		t.Fatalf("legacy paths not preserved: %+v", legacy)
	}
}

func TestLoadMissingAndInvalidConfig(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("missing config should fail")
	}
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(path, []byte("server:\n  port: [bad"), 0o644); err != nil {
		t.Fatalf("write bad config: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("invalid yaml should fail")
	}
}

func loadTestConfig(t *testing.T, body string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg
}
