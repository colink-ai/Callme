// Package agent defines Callme's application-facing runtime DTOs.
//
// Concrete agent protocol adapters are owned by Helios. Callme keeps these
// small types so its session, WebSocket, settings, and persistence layers do
// not depend on Helios contracts directly.
package agent

import (
	"context"
	"time"
)

// ImageContent 图片内容（用于多模态输入）
type ImageContent struct {
	MimeType string
	Data     string
	URL      string
}

// ChunkType 流式输出块类型
type ChunkType string

const (
	ChunkTypeText           ChunkType = "text"
	ChunkTypeError          ChunkType = "error"
	ChunkTypeStatus         ChunkType = "status"
	ChunkTypeThinking       ChunkType = "thinking"    // 思考过程
	ChunkTypeToolUse        ChunkType = "tool_use"    // 工具调用开始（知识检索引用展示来源）
	ChunkTypeToolResult     ChunkType = "tool_result" // 工具调用结果
	ChunkTypeInputJSONDelta ChunkType = "input_json_delta"
	ChunkTypeUsage          ChunkType = "usage" // Token 使用更新
	ChunkTypeQuestion       ChunkType = "question"
	ChunkTypePermission     ChunkType = "permission"
	ChunkTypeArtifact       ChunkType = "artifact"
	ChunkTypeHandoff        ChunkType = "handoff"
	ChunkTypeDone           ChunkType = "done" // 单轮回答结束
)

// TokenUsage Token 使用统计
type TokenUsage struct {
	InputTokens  int64 `json:"inputTokens,omitempty"`
	OutputTokens int64 `json:"outputTokens,omitempty"`
}

// ContentBlock preserves rich tool output for future UI surfaces without
// making session persistence depend on Helios contracts directly.
type ContentBlock struct {
	Type     string         `json:"type,omitempty"`
	Text     string         `json:"text,omitempty"`
	MimeType string         `json:"mimeType,omitempty"`
	Data     string         `json:"data,omitempty"`
	URL      string         `json:"url,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Chunk 流式输出块
type Chunk struct {
	Type             ChunkType      `json:"type"`
	Content          string         `json:"content,omitempty"`
	ToolName         string         `json:"toolName,omitempty"`
	ToolID           string         `json:"toolId,omitempty"`
	ToolInput        map[string]any `json:"toolInput,omitempty"`
	IsError          bool           `json:"isError,omitempty"`
	Usage            *TokenUsage    `json:"usage,omitempty"`
	ToolResultBlocks []ContentBlock `json:"toolResultBlocks,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}

// AgentSpec Agent 运行配置（模型切换的载体：改这里 + 重启会话即可换模型）
type AgentSpec struct {
	Type               string        // Agent 类型：hermes
	CliPath            string        // CLI 路径
	DefaultModel       string        // 模型 ID
	APIURL             string        // 自定义 provider base_url
	APIToken           string        // 自定义 provider token
	RuntimeHome        string        // 通用 Agent Runtime 工作目录（按领域隔离）
	HermesHome         string        // 共享持久化配置/记忆目录（自学习）
	SystemPrompt       string        // 客服系统提示词（注入首轮）
	SupportsMultimodal bool          // 当前模型是否允许图片等多模态输入
	PromptTimeout      time.Duration // 单轮回答最长等待时间；负数表示不主动超时
}

// MCPServerSpec 注入会话的 MCP server（知识图谱检索工具）
type MCPServerSpec struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"` // stdio | http
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// SessionRequest 启动会话的请求
type SessionRequest struct {
	Spec            AgentSpec
	WorkDir         string          // 会话工作目录
	MCPServers      []MCPServerSpec // 知识源工具
	ResumeSessionID string          // 底层 Agent 会话 ID；支持原生恢复时传入
}

// SessionStatus 底层 Agent 会话状态
type SessionStatus string

const (
	SessionStatusIdle    SessionStatus = "idle"
	SessionStatusRunning SessionStatus = "running"
	SessionStatusStopped SessionStatus = "stopped"
)

// Adapter is the narrow application-facing runtime interface consumed by
// Callme. Production implementations are backed by Helios.
type Adapter interface {
	// StartSession 启动常驻会话（拉起 Agent 进程、握手、建会话）
	StartSession(ctx context.Context, sessionID string, req *SessionRequest) error

	// Prompt 在已有会话上发送一轮输入，流式回调输出，阻塞至本轮结束
	Prompt(ctx context.Context, sessionID string, input string, images []ImageContent, onChunk func(Chunk)) error

	// StopSession 停止会话并回收进程
	StopSession(sessionID string) error

	// GetSessionStatus 查询会话状态
	GetSessionStatus(sessionID string) SessionStatus

	// CheckHealth 检查 CLI 可用性（拉起进程完成 initialize 握手）
	CheckHealth(ctx context.Context, spec AgentSpec) error
}

// SessionIntrospector 可选接口：暴露底层 Agent 会话状态给上层持久化/恢复。
type SessionIntrospector interface {
	AgentSessionID(sessionID string) string
	UsedNativeResume(sessionID string) bool
}
