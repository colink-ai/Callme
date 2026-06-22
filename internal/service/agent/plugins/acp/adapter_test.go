package acp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"callme/internal/service/agent"
)

func TestBuildContentBlocks(t *testing.T) {
	blocks := buildContentBlocks("看图回答", []agent.ImageContent{
		{MimeType: "image/png", Data: "aW1n"},
		{MimeType: "image/jpeg", Data: "anBn"},
	})
	if len(blocks) != 3 {
		t.Fatalf("expected text plus 2 image blocks, got %d", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].Text != "看图回答" {
		t.Fatalf("unexpected text block: %+v", blocks[0])
	}
	if blocks[1].Type != "image" || blocks[1].MimeType != "image/png" || blocks[1].Data != "aW1n" {
		t.Fatalf("unexpected first image block: %+v", blocks[1])
	}
}

func TestPromptContext(t *testing.T) {
	ctx, cancel := promptContext(context.Background(), -1)
	defer cancel()
	select {
	case <-ctx.Done():
		t.Fatal("negative timeout should not set an immediate deadline")
	default:
	}

	ctx, cancel = promptContext(context.Background(), time.Nanosecond)
	defer cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("positive timeout should expire")
	}
}

func TestSupportsSessionResume(t *testing.T) {
	cases := []struct {
		name string
		caps map[string]any
		want bool
	}{
		{name: "empty", caps: nil, want: false},
		{name: "camel true", caps: map[string]any{"sessionCapabilities": map[string]any{"resume": true}}, want: true},
		{name: "snake truthy", caps: map[string]any{"session_capabilities": map[string]any{"resume": map[string]any{}}}, want: true},
		{name: "explicit false", caps: map[string]any{"sessionCapabilities": map[string]any{"resume": false}}, want: false},
		{name: "bad shape", caps: map[string]any{"sessionCapabilities": "yes"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := supportsSessionResume(tc.caps); got != tc.want {
				t.Fatalf("supportsSessionResume=%v want %v", got, tc.want)
			}
		})
	}
}

func TestMergeEnvOverridesAndIgnoresInvalidEntries(t *testing.T) {
	t.Setenv("CALLME_ACP_TEST_ENV", "old")
	env := mergeEnv([]string{"CALLME_ACP_TEST_ENV=new", "CALLME_ACP_EXTRA=value", "BROKEN"})
	joined := "\n" + strings.Join(env, "\n") + "\n"
	if !strings.Contains(joined, "\nCALLME_ACP_TEST_ENV=new\n") {
		t.Fatalf("expected override in env: %v", env)
	}
	if !strings.Contains(joined, "\nCALLME_ACP_EXTRA=value\n") {
		t.Fatalf("expected extra env: %v", env)
	}
	if strings.Contains(joined, "\nBROKEN\n") {
		t.Fatalf("invalid env entry should be ignored: %v", env)
	}
	_ = os.Getenv("CALLME_ACP_TEST_ENV")
}

func TestAdapterSessionIntrospectionAndNotification(t *testing.T) {
	adapter := NewBaseAdapter(AdapterConfig{})
	session := &acpSession{id: "native-1", status: agent.SessionStatusRunning, nativeResume: true}
	adapter.sessions["business-1"] = session

	if got := adapter.GetSessionStatus("business-1"); got != agent.SessionStatusRunning {
		t.Fatalf("status=%s", got)
	}
	if got := adapter.GetSessionStatus("missing"); got != agent.SessionStatusIdle {
		t.Fatalf("missing status=%s", got)
	}
	if got := adapter.AgentSessionID("business-1"); got != "native-1" {
		t.Fatalf("agent session id=%q", got)
	}
	if !adapter.UsedNativeResume("business-1") {
		t.Fatal("native resume should be reported")
	}

	var chunks []agent.Chunk
	session.onChunk = func(chunk agent.Chunk) { chunks = append(chunks, chunk) }
	update := map[string]any{
		"sessionUpdate": "agent_message_chunk",
		"content": map[string]any{
			"type": "text",
			"text": "hello",
		},
	}
	payload, _ := json.Marshal(acpSessionUpdateParams{Update: mustMarshalRaw(t, update)})
	adapter.handleNotification(session, "session/update", payload)
	adapter.handleNotification(session, "unknown", nil)

	if len(chunks) != 1 || chunks[0].Content != "hello" {
		t.Fatalf("unexpected chunks: %+v", chunks)
	}
}

func TestBaseAdapterLifecycleWithMockACP(t *testing.T) {
	cliPath := writeMockACPAgent(t)
	var starts, ends int
	adapter := NewBaseAdapter(AdapterConfig{
		BuildArgs: func(req *agent.SessionRequest) []string { return nil },
		BuildEnv:  func(req *agent.SessionRequest) []string { return []string{"CALLME_TEST_ENV=1"} },
		OnSessionStart: func(req *agent.SessionRequest) {
			starts++
		},
		OnSessionEnd: func(req *agent.SessionRequest) {
			ends++
		},
		ConfigureModelViaACP: true,
	})

	req := &agent.SessionRequest{
		Spec: agent.AgentSpec{
			CliPath:       cliPath,
			DefaultModel:  "model-a",
			PromptTimeout: time.Second,
		},
		WorkDir: t.TempDir(),
	}
	if err := adapter.StartSession(context.Background(), "session-a", req); err != nil {
		t.Fatalf("start session: %v", err)
	}
	if starts != 1 {
		t.Fatalf("start hook count=%d", starts)
	}
	if got := adapter.GetSessionStatus("session-a"); got != agent.SessionStatusRunning {
		t.Fatalf("status=%s", got)
	}
	if got := adapter.AgentSessionID("session-a"); got != "mock-new-session" {
		t.Fatalf("agent session id=%q", got)
	}
	if adapter.UsedNativeResume("session-a") {
		t.Fatal("fresh session should not report native resume")
	}

	var chunks []agent.Chunk
	if err := adapter.Prompt(context.Background(), "session-a", "hello", []agent.ImageContent{{MimeType: "image/png", Data: "aW1n"}}, func(chunk agent.Chunk) {
		chunks = append(chunks, chunk)
	}); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if len(chunks) != 1 || chunks[0].Content != "mock: hello" {
		t.Fatalf("unexpected chunks: %+v", chunks)
	}
	if err := adapter.StopSession("session-a"); err != nil {
		t.Fatalf("stop session: %v", err)
	}
	if ends != 1 {
		t.Fatalf("end hook count=%d", ends)
	}
	if got := adapter.GetSessionStatus("session-a"); got != agent.SessionStatusIdle {
		t.Fatalf("stopped session should be idle, got %s", got)
	}
}

func TestBaseAdapterResumeWithMockACP(t *testing.T) {
	cliPath := writeMockACPAgent(t)
	adapter := NewBaseAdapter(AdapterConfig{
		BuildArgs:            func(req *agent.SessionRequest) []string { return nil },
		BuildEnv:             func(req *agent.SessionRequest) []string { return nil },
		ConfigureModelViaACP: true,
		OnSessionStart:       nil,
		OnSessionEnd:         nil,
	})
	req := &agent.SessionRequest{
		Spec: agent.AgentSpec{
			CliPath:       cliPath,
			DefaultModel:  "model-b",
			PromptTimeout: time.Second,
		},
		WorkDir:         t.TempDir(),
		ResumeSessionID: "native-resume-id",
	}
	if err := adapter.StartSession(context.Background(), "session-b", req); err != nil {
		t.Fatalf("start resumed session: %v", err)
	}
	defer adapter.StopSession("session-b")
	if got := adapter.AgentSessionID("session-b"); got != "native-resume-id" {
		t.Fatalf("agent session id=%q", got)
	}
	if !adapter.UsedNativeResume("session-b") {
		t.Fatal("resumed session should report native resume")
	}
}

func mustMarshalRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}
	return data
}

func writeMockACPAgent(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mock-acp-agent.py")
	source := `#!/usr/bin/env python3
import json
import sys

def send(payload):
    sys.stdout.write(json.dumps(payload, ensure_ascii=False) + "\n")
    sys.stdout.flush()

for line in sys.stdin:
    req = json.loads(line)
    method = req.get("method")
    req_id = req.get("id")
    if method == "initialize":
        send({"jsonrpc": "2.0", "id": req_id, "result": {"protocolVersion": 2025, "agentCapabilities": {"sessionCapabilities": {"resume": True}}}})
    elif method == "session/new":
        send({"jsonrpc": "2.0", "id": req_id, "result": {"sessionId": "mock-new-session", "configOptions": [{"id": "model", "name": "Model", "type": "string"}]}})
    elif method == "session/resume":
        send({"jsonrpc": "2.0", "id": req_id, "result": {"configOptions": [{"id": "model", "name": "Model", "type": "string"}]}})
    elif method == "session/set_config_option":
        send({"jsonrpc": "2.0", "id": req_id, "result": {"ok": True}})
    elif method == "session/prompt":
        prompt = req.get("params", {}).get("prompt", [])
        text = ""
        for block in prompt:
            if block.get("type") == "text":
                text = block.get("text", "")
                break
        send({"jsonrpc": "2.0", "method": "session/update", "params": {"sessionId": req.get("params", {}).get("sessionId"), "update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "mock: " + text}}}})
        send({"jsonrpc": "2.0", "id": req_id, "result": {"stopReason": "end_turn"}})
    else:
        send({"jsonrpc": "2.0", "id": req_id, "result": {}})
`
	if err := os.WriteFile(path, []byte(source), 0o755); err != nil {
		t.Fatalf("write mock acp agent: %v", err)
	}
	return path
}
