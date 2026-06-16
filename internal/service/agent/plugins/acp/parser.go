// ACP session/update 事件解析，移植自 Colink internal/service/agent/plugins/acp/event_parser.go
// 裁剪：去掉 AskUserQuestion / plan 等客服场景用不到的分支
package acp

import (
	"encoding/json"
	"fmt"
	"strings"

	"callme/internal/service/agent"

	"go.uber.org/zap"
)

func parseACPSessionUpdate(raw json.RawMessage) ([]agent.Chunk, error) {
	var header acpSessionUpdateHeader
	if err := json.Unmarshal(raw, &header); err != nil {
		return nil, fmt.Errorf("ACP: parse session update header: %w", err)
	}

	switch header.SessionUpdate {
	case "agent_message_chunk":
		return parseAgentMessageChunk(raw, agent.ChunkTypeText)
	case "agent_thought_chunk":
		return parseAgentMessageChunk(raw, agent.ChunkTypeThinking)
	case "tool_call":
		return parseToolCall(raw)
	case "tool_call_update":
		return parseToolCallUpdate(raw)
	case "usage_update":
		return parseUsageUpdate(raw)
	default:
		LogDebug("ACP: skip unknown session update", zap.String("sessionUpdate", header.SessionUpdate))
		return nil, nil
	}
}

func parseAgentMessageChunk(raw json.RawMessage, typ agent.ChunkType) ([]agent.Chunk, error) {
	var msg acpAgentMessageChunk
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, fmt.Errorf("ACP: parse message chunk: %w", err)
	}
	return []agent.Chunk{{Type: typ, Content: extractContentBlockText(msg.Content)}}, nil
}

func parseToolCall(raw json.RawMessage) ([]agent.Chunk, error) {
	var tc acpToolCall
	if err := json.Unmarshal(raw, &tc); err != nil {
		return nil, fmt.Errorf("ACP: parse tool_call: %w", err)
	}

	var toolInput map[string]any
	if m, ok := tc.RawInput.(map[string]any); ok {
		toolInput = m
	}

	return []agent.Chunk{{
		Type:      agent.ChunkTypeToolUse,
		ToolName:  tc.Title,
		ToolID:    tc.ToolCallID,
		ToolInput: toolInput,
	}}, nil
}

func parseToolCallUpdate(raw json.RawMessage) ([]agent.Chunk, error) {
	var update acpToolCallUpdate
	if err := json.Unmarshal(raw, &update); err != nil {
		return nil, fmt.Errorf("ACP: parse tool_call_update: %w", err)
	}

	status := strings.ToLower(update.Status)

	// in_progress/pending：初始 tool_call 可能没有 input，这里补发更新
	if status == "in_progress" || status == "pending" {
		var toolInput map[string]any
		if m, ok := update.RawInput.(map[string]any); ok {
			toolInput = m
		}
		if len(toolInput) > 0 {
			return []agent.Chunk{{
				Type:      agent.ChunkTypeToolUse,
				ToolName:  update.Title,
				ToolID:    update.ToolCallID,
				ToolInput: toolInput,
			}}, nil
		}
		return nil, nil
	}

	// completed/failed：发送 tool_result
	return []agent.Chunk{{
		Type:    agent.ChunkTypeToolResult,
		Content: extractToolCallContent(update.Content),
		ToolID:  update.ToolCallID,
		IsError: status == "failed",
	}}, nil
}

// extractToolCallContent 从 content 数组提取文本，兼容标准与嵌套格式
func extractToolCallContent(blocks []acpContentBlock) string {
	for _, block := range blocks {
		if text := extractContentBlockText(block); text != "" {
			return text
		}
	}
	return ""
}

func extractContentBlockText(block acpContentBlock) string {
	if block.Text != "" {
		return block.Text
	}
	// 嵌套格式: {"type":"content","content":{"type":"text","text":"..."}}
	if block.Type == "content" && len(block.Content) > 0 {
		var nested acpContentBlock
		if err := json.Unmarshal(block.Content, &nested); err == nil && nested.Text != "" {
			return nested.Text
		}
	}
	return ""
}

func parseUsageUpdate(raw json.RawMessage) ([]agent.Chunk, error) {
	var usage acpUsageUpdate
	if err := json.Unmarshal(raw, &usage); err != nil {
		return nil, fmt.Errorf("ACP: parse usage_update: %w", err)
	}
	return []agent.Chunk{{
		Type:  agent.ChunkTypeUsage,
		Usage: &agent.TokenUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens},
	}}, nil
}
