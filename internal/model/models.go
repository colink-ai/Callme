// Package model Callme 领域模型
package model

import "time"

// UserRole 用户角色：普通用户 / VIP / 管理员
type UserRole string

const (
	UserRoleNormal UserRole = "normal"
	UserRoleVIP    UserRole = "vip"
	UserRoleAdmin  UserRole = "admin"
)

// User 登录用户
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Role         UserRole  `json:"role"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// AuthToken 服务端登录态
type AuthToken struct {
	Token     string    `json:"-"`
	UserID    string    `json:"userId"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
}

// SessionStatus 会话状态
type SessionStatus string

const (
	SessionStatusQueued SessionStatus = "queued" // 排队中
	SessionStatusActive SessionStatus = "active" // 活跃（占用坐席）
	SessionStatusClosed SessionStatus = "closed" // 已结束
)

// CloseReason 会话结束原因
type CloseReason string

const (
	CloseReasonUser       CloseReason = "user"        // 用户主动结束
	CloseReasonIdle       CloseReason = "idle"        // 空闲超时
	CloseReasonMaxTime    CloseReason = "max_time"    // 超出最大时长
	CloseReasonAdmin      CloseReason = "admin"       // 运营强制结束
	CloseReasonError      CloseReason = "error"       // Agent 异常
	CloseReasonQueueLeave CloseReason = "queue_leave" // 排队中离开
)

// Session 客服会话（坐席占用单位）
type Session struct {
	ID             string        `json:"id"`
	ClientID       string        `json:"clientId"` // 浏览器指纹/匿名客户端标识
	UserID         string        `json:"userId,omitempty"`
	Status         SessionStatus `json:"status"`
	CreatedAt      time.Time     `json:"createdAt"`           // 进入系统（含排队）时间
	StartedAt      *time.Time    `json:"startedAt,omitempty"` // 占用坐席（开始服务）时间
	ClosedAt       *time.Time    `json:"closedAt,omitempty"`
	CloseReason    CloseReason   `json:"closeReason,omitempty"`
	Title          string        `json:"title"`                    // 首问摘要，便于监控页识别
	AgentSessionID string        `json:"agentSessionId,omitempty"` // 底层 Agent/ACP 会话 ID，用于原生恢复
}

// DurationSeconds 会话已服务时长（秒）
func (s *Session) DurationSeconds(now time.Time) int64 {
	if s.StartedAt == nil {
		return 0
	}
	end := now
	if s.ClosedAt != nil {
		end = *s.ClosedAt
	}
	return int64(end.Sub(*s.StartedAt).Seconds())
}

// MessageRole 消息角色
type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleSystem    MessageRole = "system"
)

// Message 会话消息
type Message struct {
	ID        string      `json:"id"`
	SessionID string      `json:"sessionId"`
	Role      MessageRole `json:"role"`
	Content   string      `json:"content"`
	// ToolCalls 本条助手消息中发生的知识检索等工具调用（JSON 数组文本，用于引用展示与命中率统计）
	ToolCalls string `json:"toolCalls,omitempty"`
	// Model 生成本条助手消息时使用的模型（消息卡片展示）
	Model string `json:"model,omitempty"`
	// AgentType 生成本条助手消息时使用的 Agent 类型（消息卡片展示）
	AgentType string    `json:"agentType,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// ImageContent 图片内容（用于多模态输入）
type ImageContent struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
	Filename string `json:"filename,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
}

// FeedbackRating 反馈评价
type FeedbackRating string

const (
	FeedbackUp   FeedbackRating = "up"
	FeedbackDown FeedbackRating = "down"
)

// Feedback 消息级用户反馈（自学习闭环输入）
type Feedback struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"sessionId"`
	MessageID  string         `json:"messageId"`
	Rating     FeedbackRating `json:"rating"`
	Correction string         `json:"correction,omitempty"` // 用户提供的纠错/期望答案
	Distilled  bool           `json:"distilled"`            // 是否已被学习蒸馏任务处理
	CreatedAt  time.Time      `json:"createdAt"`
}

// TicketStatus 工单状态
type TicketStatus string

const (
	TicketStatusOpen     TicketStatus = "open"
	TicketStatusNotified TicketStatus = "notified" // webhook 已外发
	TicketStatusFailed   TicketStatus = "failed"   // webhook 外发失败
)

// Ticket 转人工工单
type Ticket struct {
	ID         string       `json:"id"`
	SessionID  string       `json:"sessionId"`
	Reason     string       `json:"reason"`     // 转人工原因（用户填写或 Agent 升级说明）
	Transcript string       `json:"transcript"` // 会话上下文包（外发给下游）
	Status     TicketStatus `json:"status"`
	CreatedAt  time.Time    `json:"createdAt"`
}

// AgentSettings 运行时 Agent 配置（持久化在 DB，可在 Settings 页修改，覆盖 config.yaml 默认值）
type AgentSettings struct {
	Type         string    `json:"type"`
	CliPath      string    `json:"cliPath"`
	DefaultModel string    `json:"defaultModel"`
	APIURL       string    `json:"apiUrl"`
	APIToken     string    `json:"apiToken"`
	SystemPrompt string    `json:"systemPrompt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// PoolSettings 坐席池运行时设置（持久化在 DB）
type PoolSettings struct {
	MaxActive int       `json:"maxActive"`
	MaxQueue  int       `json:"maxQueue"`
	UpdatedAt time.Time `json:"updatedAt"`
}
