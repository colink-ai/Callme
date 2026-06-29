package runtime

import (
	"context"
	"testing"

	"callme/internal/service/agent"

	helioscontracts "github.com/colink-ai/helios/contracts"
	helios "github.com/colink-ai/helios/runtime"
	"go.uber.org/zap"
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
	if err := svc.CheckHealth(context.Background(), agent.AgentSpec{Type: "custom"}); err == nil {
		t.Fatalf("custom agent health check should be rejected")
	}
}

func TestServiceUsesSharedAdapter(t *testing.T) {
	svc := NewService(zap.NewNop())
	first, err := svc.NewAdapter(agent.AgentSpec{Type: TypeHermes})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	second, err := svc.NewAdapter(agent.AgentSpec{})
	if err != nil {
		t.Fatalf("new default adapter: %v", err)
	}
	if first != second {
		t.Fatalf("runtime service should share one Helios adapter per process")
	}
}

func TestApplyCallmeHermesConfig(t *testing.T) {
	cfg := map[string]any{
		"skills": map[string]any{"existing": true},
	}
	applyCallmeHermesConfig(cfg)

	if cfg["memory"].(map[string]any)["memory_enabled"] != false {
		t.Fatalf("memory guard missing: %+v", cfg["memory"])
	}
	if cfg["curator"].(map[string]any)["enabled"] != false {
		t.Fatalf("curator guard missing: %+v", cfg["curator"])
	}
	skills := cfg["skills"].(map[string]any)
	if skills["guard_agent_created"] != true || skills["existing"] != true {
		t.Fatalf("skills config should be merged, got %+v", skills)
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
	if got.RuntimeConfigMode != helios.RuntimeConfigIsolated {
		t.Fatalf("Callme runtime should use isolated config mode, got %q", got.RuntimeConfigMode)
	}

	servers := toHeliosMCPServers([]agent.MCPServerSpec{{
		Name: "kb", Type: "http", URL: "http://127.0.0.1/mcp",
		Headers: map[string]string{"Authorization": "Bearer x"},
	}})
	if len(servers) != 1 || servers[0].Name != "kb" || servers[0].Headers["Authorization"] == "" {
		t.Fatalf("unexpected helios mcp servers: %+v", servers)
	}

	images := toHeliosImages([]agent.ImageContent{{MimeType: "image/png", Data: "aW1n", URL: "https://example.test/image.png"}})
	if len(images) != 1 || images[0].MimeType != "image/png" || images[0].Data != "aW1n" || images[0].URL == "" {
		t.Fatalf("unexpected helios images: %+v", images)
	}
	if runtimeHome(agent.AgentSpec{RuntimeHome: "/runtime", HermesHome: "/hermes"}) != "/runtime" {
		t.Fatal("RuntimeHome should take precedence")
	}
	if runtimeHome(agent.AgentSpec{HermesHome: "/hermes"}) != "/hermes" {
		t.Fatal("HermesHome should be fallback runtime home")
	}
}

func TestFromHeliosChunkTypesAndUsageNil(t *testing.T) {
	cases := []struct {
		in   helioscontracts.ChunkType
		want agent.ChunkType
	}{
		{helioscontracts.ChunkText, agent.ChunkTypeText},
		{helioscontracts.ChunkError, agent.ChunkTypeError},
		{helioscontracts.ChunkThinking, agent.ChunkTypeThinking},
		{helioscontracts.ChunkToolUse, agent.ChunkTypeToolUse},
		{helioscontracts.ChunkToolResult, agent.ChunkTypeToolResult},
		{helioscontracts.ChunkInputJSONDelta, agent.ChunkTypeInputJSONDelta},
		{helioscontracts.ChunkUsage, agent.ChunkTypeUsage},
		{helioscontracts.ChunkQuestion, agent.ChunkTypeQuestion},
		{helioscontracts.ChunkPermission, agent.ChunkTypePermission},
		{helioscontracts.ChunkArtifact, agent.ChunkTypeArtifact},
		{helioscontracts.ChunkHandoff, agent.ChunkTypeHandoff},
		{helioscontracts.ChunkDone, agent.ChunkTypeDone},
		{helioscontracts.ChunkType("unknown"), agent.ChunkTypeStatus},
	}
	for _, tc := range cases {
		if got := fromHeliosChunkType(tc.in); got != tc.want {
			t.Fatalf("chunk type %q = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := fromHeliosUsage(nil); got != nil {
		t.Fatalf("nil usage should remain nil: %+v", got)
	}
	chunk := fromHeliosChunk(helioscontracts.Chunk{Type: helioscontracts.ChunkError, Content: "boom", IsError: true})
	if chunk.Type != agent.ChunkTypeError || !chunk.IsError || chunk.Content != "boom" {
		t.Fatalf("error chunk mapping failed: %+v", chunk)
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
		ToolResultBlocks: []helioscontracts.ContentBlock{{
			Type: "text",
			Text: "rich result",
			Metadata: map[string]any{
				"source": "kb",
			},
		}},
		Metadata: map[string]any{"phase": "call"},
	})
	if chunk.Type != agent.ChunkTypeToolUse || chunk.ToolName != "knowledge.search" || chunk.ToolInput["query"] != "refund" {
		t.Fatalf("unexpected chunk mapping: %+v", chunk)
	}
	if chunk.Usage == nil || chunk.Usage.InputTokens != 12 || chunk.Usage.OutputTokens != 34 {
		t.Fatalf("usage not mapped: %+v", chunk.Usage)
	}
	if len(chunk.ToolResultBlocks) != 1 || chunk.ToolResultBlocks[0].Text != "rich result" {
		t.Fatalf("tool result blocks not mapped: %+v", chunk.ToolResultBlocks)
	}
	if chunk.Metadata["phase"] != "call" {
		t.Fatalf("metadata not mapped: %+v", chunk.Metadata)
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

func TestHeliosAdapterRoutesUsageEventsToSessionCallback(t *testing.T) {
	adapter := newHeliosAdapter(zap.NewNop())
	var got []agent.Chunk
	adapter.mu.Lock()
	adapter.callbacks["s1"] = func(chunk agent.Chunk) {
		got = append(got, chunk)
	}
	adapter.mu.Unlock()

	err := adapter.onRunEvent(context.Background(), helioscontracts.RunEvent{
		SessionID: "s1",
		Type:      helioscontracts.EventUsageReported,
		Usage:     &helioscontracts.TokenUsage{InputTokens: 7, OutputTokens: 9},
	})
	if err != nil {
		t.Fatalf("onRunEvent failed: %v", err)
	}
	if len(got) != 1 || got[0].Type != agent.ChunkTypeUsage || got[0].Usage.OutputTokens != 9 {
		t.Fatalf("callback did not receive usage chunk: %+v", got)
	}
}

func TestHeliosAdapterSessionStateAccessors(t *testing.T) {
	adapter := newHeliosAdapter(zap.NewNop())
	if got := adapter.GetSessionStatus("missing"); got != agent.SessionStatusIdle {
		t.Fatalf("missing session status = %s", got)
	}
	adapter.mu.Lock()
	adapter.sessions["s1"] = sessionInfo{
		status:           agent.SessionStatusRunning,
		agentSessionID:   "native-1",
		usedNativeResume: true,
	}
	adapter.callbacks["s1"] = func(agent.Chunk) {}
	adapter.mu.Unlock()

	if got := adapter.GetSessionStatus("s1"); got != agent.SessionStatusRunning {
		t.Fatalf("running status = %s", got)
	}
	if got := adapter.AgentSessionID("s1"); got != "native-1" {
		t.Fatalf("agent session id = %q", got)
	}
	if !adapter.UsedNativeResume("s1") {
		t.Fatal("native resume flag should be true")
	}
	if err := adapter.StopSession("missing"); err == nil {
		t.Fatal("stopping a missing Helios session should surface adapter error")
	}
	if _, ok := adapter.callbacks["s1"]; !ok {
		t.Fatal("unrelated callback should remain after stopping missing session")
	}
}

func TestHeliosAdapterErrorPaths(t *testing.T) {
	adapter := newHeliosAdapter(zap.NewNop())
	if err := adapter.StartSession(context.Background(), "s-nil", nil); err == nil {
		t.Fatal("nil session request should fail")
	}
	if err := adapter.Prompt(context.Background(), "missing", "hi", nil, func(agent.Chunk) {}); err == nil {
		t.Fatal("prompting missing session should fail")
	}
	if err := adapter.CheckHealth(context.Background(), agent.AgentSpec{Type: TypeHermes, CliPath: "/path/that/does/not/exist"}); err == nil {
		t.Fatal("health check with missing cli should fail")
	}
}
