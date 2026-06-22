package open_code

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"callme/internal/service/agent"
)

func TestBuildOpenCodeConfigContent(t *testing.T) {
	spec := agent.AgentSpec{
		APIURL:       "https://example.test/v1",
		APIToken:     "token-123",
		DefaultModel: "glm-test",
	}

	content := buildOpenCodeConfigContent(spec)
	if content == "" {
		t.Fatal("expected config content")
	}

	var cfg openCodeConfig
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatalf("config should be valid json: %v", err)
	}
	provider := cfg.Provider["callme"]
	if cfg.Model != "callme/glm-test" {
		t.Fatalf("unexpected model: %q", cfg.Model)
	}
	if provider.Options.APIKey != "token-123" || provider.Options.BaseURL != "https://example.test/v1" {
		t.Fatalf("unexpected provider options: %+v", provider.Options)
	}
	if provider.Models["glm-test"].ID != "glm-test" {
		t.Fatalf("model entry not populated: %+v", provider.Models)
	}
}

func TestBuildOpenCodeConfigContentEmpty(t *testing.T) {
	if got := buildOpenCodeConfigContent(agent.AgentSpec{}); got != "" {
		t.Fatalf("empty spec should not generate config, got %q", got)
	}
}

func TestBuildOpenCodeEnv(t *testing.T) {
	workDir := t.TempDir()
	req := &agent.SessionRequest{
		WorkDir: workDir,
		Spec: agent.AgentSpec{
			APIURL:       "https://example.test/v1",
			APIToken:     "secret-token",
			DefaultModel: "glm-test",
		},
	}

	env := buildOpenCodeEnv(req)
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "OPENCODE_PURE=1") {
		t.Fatalf("pure mode missing from env: %v", env)
	}
	configDir := filepath.Join(workDir, ".opencode")
	if !strings.Contains(joined, "OPENCODE_CONFIG_DIR="+configDir) {
		t.Fatalf("config dir missing from env: %v", env)
	}
	if _, err := os.Stat(configDir); err != nil {
		t.Fatalf("config dir should be created: %v", err)
	}
	if !strings.Contains(joined, "OPENCODE_CONFIG_CONTENT=") {
		t.Fatalf("config content missing from env: %v", env)
	}
}

func TestBuildOpenCodeEnvNilRequest(t *testing.T) {
	env := buildOpenCodeEnv(nil)
	if len(env) != 1 || env[0] != "OPENCODE_PURE=1" {
		t.Fatalf("nil request should only enable pure mode, got %v", env)
	}
}

func TestMaskSecret(t *testing.T) {
	cases := map[string]string{
		"":             "<empty>",
		"short":        "****",
		"12345678":     "****",
		"123456789":    "1234****6789",
		"abcdef123456": "abcd****3456",
	}
	for input, want := range cases {
		if got := maskSecret(input); got != want {
			t.Fatalf("maskSecret(%q)=%q want %q", input, got, want)
		}
	}
}
