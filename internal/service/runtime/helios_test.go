package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"callme/internal/service/agent"

	helioscontracts "github.com/colink-ai/helios/contracts"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

func TestServiceTypesAndDefaultPath(t *testing.T) {
	svc := NewService(zap.NewNop())
	types := svc.Types()
	if len(types) != 1 || types[0].Type != TypeHermes || types[0].DefaultPath != "hermes" {
		t.Fatalf("unexpected runtime types: %+v", types)
	}
	if got := svc.DefaultPathFor(TypeHermes); got != "hermes" {
		t.Fatalf("unexpected default path: %q", got)
	}
	if _, err := svc.NewAdapter(agent.AgentSpec{Type: "custom"}); err == nil {
		t.Fatalf("custom agent should be rejected by Callme runtime; add it as a Helios adapter instead")
	}
}

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

	generateHermesConfig(zap.NewNop(), spec, mcpServers)

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
}

func TestGenerateHermesConfigPreservesExistingKeysAndHandlesEmptyHome(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(configPath, []byte("existing:\n  enabled: true\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	generateHermesConfig(zap.NewNop(), agent.AgentSpec{HermesHome: home, DefaultModel: "glm-test"}, nil)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "existing:") || !strings.Contains(string(data), "glm-test") {
		t.Fatalf("existing config should be preserved and model added:\n%s", data)
	}

	generateHermesConfig(zap.NewNop(), agent.AgentSpec{}, nil)
}

func TestBuildHermesEnvAndHelpers(t *testing.T) {
	home := filepath.Join(t.TempDir(), "hermes")
	env := buildHermesEnv(zap.NewNop(), agent.AgentSpec{
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
	if got := maskSecret("abcdef123456"); got != "abcd****3456" {
		t.Fatalf("long secret mask=%q", got)
	}
}

func TestHeliosSpecConversions(t *testing.T) {
	spec := agent.AgentSpec{
		Type:               "ignored",
		CliPath:            "/usr/local/bin/hermes",
		DefaultModel:       "glm-test",
		APIURL:             "https://example.test/v1",
		APIToken:           "secret-token",
		RuntimeHome:        "/tmp/runtime",
		HermesHome:         "/tmp/hermes",
		SystemPrompt:       "be helpful",
		SupportsMultimodal: true,
	}
	got := toHeliosAgentSpec(spec)
	if got.Type != TypeHermes || got.CLIPath != spec.CliPath || got.RuntimeHome != spec.RuntimeHome {
		t.Fatalf("unexpected helios agent spec: %+v", got)
	}

	servers := toHeliosMCPServers([]agent.MCPServerSpec{{
		Name: "kb", Type: "http", URL: "http://127.0.0.1/mcp",
		Headers: map[string]string{"Authorization": "Bearer x"},
	}})
	if len(servers) != 1 || servers[0].Name != "kb" || servers[0].Headers["Authorization"] == "" {
		t.Fatalf("unexpected helios mcp servers: %+v", servers)
	}

	images := toHeliosImages([]agent.ImageContent{{MimeType: "image/png", Data: "aW1n"}})
	if len(images) != 1 || images[0].MimeType != "image/png" || images[0].Data != "aW1n" {
		t.Fatalf("unexpected helios images: %+v", images)
	}
}

func TestFromHeliosChunk(t *testing.T) {
	chunk := fromHeliosChunk(helioscontracts.Chunk{
		Type:      helioscontracts.ChunkToolUse,
		Content:   "calling",
		ToolID:    "tool-1",
		ToolName:  "knowledge.search",
		ToolInput: map[string]any{"query": "refund"},
		Usage: &helioscontracts.TokenUsage{
			InputTokens:  12,
			OutputTokens: 34,
		},
	})
	if chunk.Type != agent.ChunkTypeToolUse || chunk.ToolName != "knowledge.search" || chunk.ToolInput["query"] != "refund" {
		t.Fatalf("unexpected chunk mapping: %+v", chunk)
	}
	if chunk.Usage == nil || chunk.Usage.InputTokens != 12 || chunk.Usage.OutputTokens != 34 {
		t.Fatalf("usage not mapped: %+v", chunk.Usage)
	}
}

func TestHeliosAdapterRoutesHeliosEventsToSessionCallback(t *testing.T) {
	adapter := newHeliosAdapter(zap.NewNop())
	var got []agent.Chunk
	adapter.mu.Lock()
	adapter.callbacks["s1"] = func(chunk agent.Chunk) {
		got = append(got, chunk)
	}
	adapter.mu.Unlock()

	err := adapter.onRunEvent(context.Background(), helioscontracts.RunEvent{
		SessionID: "s1",
		Chunk:     &helioscontracts.Chunk{Type: helioscontracts.ChunkText, Content: "hello"},
	})
	if err != nil {
		t.Fatalf("onRunEvent failed: %v", err)
	}
	if len(got) != 1 || got[0].Type != agent.ChunkTypeText || got[0].Content != "hello" {
		t.Fatalf("callback did not receive mapped chunk: %+v", got)
	}
}
