// ACP 协议类型定义，移植自 Colink internal/service/agent/plugins/acp/types.go
package acp

import "encoding/json"

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonrpcError   `json:"error"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

type acpInitializeParams struct {
	ProtocolVersion    int                   `json:"protocolVersion"`
	ClientCapabilities acpClientCapabilities `json:"clientCapabilities"`
}

type acpClientCapabilities struct {
	PromptCapabilities acpPromptCapabilities `json:"promptCapabilities"`
}

type acpPromptCapabilities struct {
	Image           bool `json:"image"`
	EmbeddedContext bool `json:"embeddedContext"`
}

type acpInitializeResult struct {
	ProtocolVersion   int            `json:"protocolVersion"`
	AgentCapabilities map[string]any `json:"agentCapabilities"`
}

type acpNewSessionParams struct {
	CWD        string `json:"cwd"`
	MCPServers []any  `json:"mcpServers"`
}

type acpResumeSessionParams struct {
	CWD        string `json:"cwd"`
	SessionID  string `json:"sessionId"`
	MCPServers []any  `json:"mcpServers"`
}

type acpNewSessionResult struct {
	SessionID     string                `json:"sessionId"`
	ConfigOptions []acpSessionConfigOpt `json:"configOptions,omitempty"`
}

type acpResumeSessionResult struct {
	ConfigOptions []acpSessionConfigOpt `json:"configOptions,omitempty"`
}

type acpSessionConfigOpt struct {
	ConfigID     string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	CurrentValue string `json:"currentValue,omitempty"`
}

// session/set_model（legacy，广泛支持）
type acpSetModelParams struct {
	SessionID string `json:"sessionId"`
	ModelID   string `json:"modelId"`
}

// session/set_config_option（较新 API）
type acpSetConfigOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}

type acpPermissionResponse struct {
	Allow string `json:"allow"`
}

type acpPromptParams struct {
	SessionID string            `json:"sessionId"`
	Prompt    []acpContentBlock `json:"prompt"`
}

// acpContentBlock ACP prompt 内容块。
// 图片块按 ACP 规范用平铺字段 {type:"image", data, mimeType}（非 Anthropic 的 source 包裹）。
type acpContentBlock struct {
	Type     string          `json:"type"` // "text" | "image" | "content"（嵌套）
	Text     string          `json:"text,omitempty"`
	Data     string          `json:"data,omitempty"`     // image 块的 base64 数据
	MimeType string          `json:"mimeType,omitempty"` // image 块的 MIME 类型
	Content  json.RawMessage `json:"content,omitempty"`
}

type acpPromptResult struct {
	StopReason string `json:"stopReason"` // end_turn | cancelled | max_tokens | refusal
}

type acpSessionUpdateParams struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

type acpSessionUpdateHeader struct {
	SessionUpdate string `json:"sessionUpdate"`
}

type acpAgentMessageChunk struct {
	SessionUpdate string          `json:"sessionUpdate"`
	Content       acpContentBlock `json:"content"`
}

type acpToolCall struct {
	SessionUpdate string            `json:"sessionUpdate"`
	ToolCallID    string            `json:"toolCallId"`
	Status        string            `json:"status"`
	Title         string            `json:"title"`
	RawInput      any               `json:"rawInput,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	Content       []acpContentBlock `json:"content,omitempty"`
}

type acpToolCallUpdate struct {
	SessionUpdate string            `json:"sessionUpdate"`
	ToolCallID    string            `json:"toolCallId"`
	Status        string            `json:"status"`
	Title         string            `json:"title,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	RawInput      any               `json:"rawInput,omitempty"`
	Content       []acpContentBlock `json:"content,omitempty"`
}

type acpUsageUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	InputTokens   int64  `json:"inputTokens,omitempty"`
	OutputTokens  int64  `json:"outputTokens,omitempty"`
}
