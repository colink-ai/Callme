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
