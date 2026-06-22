package acp

import (
	"encoding/json"
	"testing"

	"callme/internal/service/agent"
)

func TestParseACPSessionUpdate(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want agent.Chunk
	}{
		{
			name: "agent text",
			raw:  `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}`,
			want: agent.Chunk{Type: agent.ChunkTypeText, Content: "hello"},
		},
		{
			name: "agent thought nested content",
			raw:  `{"sessionUpdate":"agent_thought_chunk","content":{"type":"content","content":{"type":"text","text":"thinking"}}}`,
			want: agent.Chunk{Type: agent.ChunkTypeThinking, Content: "thinking"},
		},
		{
			name: "tool call",
			raw:  `{"sessionUpdate":"tool_call","toolCallId":"tc1","title":"mcp_code_graph_query","rawInput":{"query":"deploy"}}`,
			want: agent.Chunk{Type: agent.ChunkTypeToolUse, ToolName: "mcp_code_graph_query", ToolID: "tc1", ToolInput: map[string]any{"query": "deploy"}},
		},
		{
			name: "tool result failed",
			raw:  `{"sessionUpdate":"tool_call_update","toolCallId":"tc1","status":"failed","content":[{"type":"text","text":"boom"}]}`,
			want: agent.Chunk{Type: agent.ChunkTypeToolResult, ToolID: "tc1", Content: "boom", IsError: true},
		},
		{
			name: "usage",
			raw:  `{"sessionUpdate":"usage_update","inputTokens":11,"outputTokens":22}`,
			want: agent.Chunk{Type: agent.ChunkTypeUsage, Usage: &agent.TokenUsage{InputTokens: 11, OutputTokens: 22}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunks, err := parseACPSessionUpdate(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatalf("parseACPSessionUpdate: %v", err)
			}
			if len(chunks) != 1 {
				t.Fatalf("chunks len = %d", len(chunks))
			}
			got := chunks[0]
			if got.Type != tc.want.Type || got.Content != tc.want.Content || got.ToolName != tc.want.ToolName || got.ToolID != tc.want.ToolID || got.IsError != tc.want.IsError {
				t.Fatalf("chunk = %+v, want %+v", got, tc.want)
			}
			if tc.want.ToolInput != nil && got.ToolInput["query"] != tc.want.ToolInput["query"] {
				t.Fatalf("tool input = %+v", got.ToolInput)
			}
			if tc.want.Usage != nil && (got.Usage == nil || got.Usage.InputTokens != tc.want.Usage.InputTokens || got.Usage.OutputTokens != tc.want.Usage.OutputTokens) {
				t.Fatalf("usage = %+v", got.Usage)
			}
		})
	}
}

func TestParseACPIgnoresUnknownAndPendingWithoutInput(t *testing.T) {
	for _, raw := range []string{
		`{"sessionUpdate":"unknown"}`,
		`{"sessionUpdate":"tool_call_update","toolCallId":"tc1","status":"pending"}`,
	} {
		chunks, err := parseACPSessionUpdate(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("parse %s: %v", raw, err)
		}
		if len(chunks) != 0 {
			t.Fatalf("expected no chunks for %s, got %+v", raw, chunks)
		}
	}
}

func TestParseACPBadJSONReturnsError(t *testing.T) {
	if _, err := parseACPSessionUpdate(json.RawMessage(`{"sessionUpdate":`)); err == nil {
		t.Fatal("bad json should fail")
	}
}
