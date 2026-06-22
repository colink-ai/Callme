package hermes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"callme/internal/service/agent"

	"gopkg.in/yaml.v3"
)

func TestGenerateHermesConfig(t *testing.T) {
	home := t.TempDir()
	spec := agent.AgentSpec{
		HermesHome:   home,
		DefaultModel: "glm-test",
		APIURL:       "https://example.test/v1",
		APIToken:     "secret-token",
	}
	mcpServers := []agent.MCPServerSpec{
		{Name: "wiki", Type: "http", URL: "http://127.0.0.1:3000/mcp", Headers: map[string]string{"Authorization": "Bearer token"}},
		{Name: "code", Type: "stdio", Command: "node", Args: []string{"server.js"}, Env: map[string]string{"A": "B"}},
		{Name: "missing-name", Type: "stdio"},
		{Name: "bad", Type: "tcp", URL: "tcp://example"},
	}

	generateHermesConfig(spec, mcpServers)

	data, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config should be yaml: %v\n%s", err, data)
	}
	model := cfg["model"].(map[string]any)
	if model["default"] != "glm-test" || model["provider"] != "custom" || model["base_url"] != "https://example.test/v1" {
		t.Fatalf("unexpected model config: %+v", model)
	}
	servers := cfg["mcp_servers"].(map[string]any)
	if len(servers) != 2 {
		t.Fatalf("unexpected mcp servers: %+v", servers)
	}
	if servers["wiki"].(map[string]any)["url"] != "http://127.0.0.1:3000/mcp" {
		t.Fatalf("http server not rendered: %+v", servers["wiki"])
	}
	if servers["code"].(map[string]any)["command"] != "node" {
		t.Fatalf("stdio server not rendered: %+v", servers["code"])
	}
	if cfg["memory"].(map[string]any)["memory_enabled"] != false {
		t.Fatalf("memory guard missing: %+v", cfg["memory"])
	}
	if cfg["skills"].(map[string]any)["guard_agent_created"] != true {
		t.Fatalf("skills guard missing: %+v", cfg["skills"])
	}
}

func TestGenerateHermesConfigPreservesExistingKeysAndHandlesEmptyHome(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(configPath, []byte("existing:\n  enabled: true\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	generateHermesConfig(agent.AgentSpec{HermesHome: home, DefaultModel: "glm-test"}, nil)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "existing:") || !strings.Contains(string(data), "glm-test") {
		t.Fatalf("existing config should be preserved and model added:\n%s", data)
	}

	generateHermesConfig(agent.AgentSpec{}, nil)
}

func TestBuildHermesEnvAndHelpers(t *testing.T) {
	home := filepath.Join(t.TempDir(), "hermes")
	env := buildHermesEnv(agent.AgentSpec{
		HermesHome:   home,
		APIURL:       "https://example.test/v1",
		APIToken:     "secret-token",
		DefaultModel: "glm-test",
	})
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, want := range []string{
		"\nNO_PROXY=127.0.0.1,localhost,::1\n",
		"\nHERMES_INFERENCE_PROVIDER=custom\n",
		"\nCUSTOM_BASE_URL=https://example.test/v1\n",
		"\nOPENAI_API_KEY=secret-token\n",
		"\nHERMES_HOME=" + home + "\n",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("env missing %q in %v", want, env)
		}
	}

	if got := absPath(""); got != "" {
		t.Fatalf("empty path should stay empty, got %q", got)
	}
	if got := absPath(home); got != home {
		t.Fatalf("absolute path changed: %q", got)
	}
	if got := maskSecret(""); got != "<empty>" {
		t.Fatalf("empty secret mask=%q", got)
	}
	if got := maskSecret("short"); got != "****" {
		t.Fatalf("short secret mask=%q", got)
	}
	if got := maskSecret("abcdef123456"); got != "abcd****3456" {
		t.Fatalf("long secret mask=%q", got)
	}
}

func TestBuildHermesMCPServersSkipsInvalidSpecs(t *testing.T) {
	servers := buildHermesMCPServers([]agent.MCPServerSpec{
		{Name: "", Type: "http", URL: "http://x"},
		{Name: "http-missing-url", Type: "http"},
		{Name: "stdio-missing-command", Type: "stdio"},
		{Name: "unsupported", Type: "sse", URL: "http://x"},
	})
	if len(servers) != 0 {
		t.Fatalf("invalid specs should be skipped: %+v", servers)
	}
}
