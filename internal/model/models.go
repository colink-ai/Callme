// Package model Callme 领域模型
package model

import "time"

// UserRole 用户角色：普通用户 / VIP / 知识专员 / 知识专家 / 管理员
type UserRole string

const (
	UserRoleNormal          UserRole = "normal"
	UserRoleVIP             UserRole = "vip"
	UserRoleKnowledgeStaff  UserRole = "knowledge_staff"
	UserRoleKnowledgeExpert UserRole = "knowledge_expert"
	UserRoleAdmin           UserRole = "admin"
)

// User 登录用户
type User struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"`
	Role         UserRole   `json:"role"`  // 兼容旧前端/旧接口的主角色
	Roles        []UserRole `json:"roles"` // 新权限模型：一个用户可拥有多个角色
	MaxSessions  int        `json:"maxSessions"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

// HasRole 判断用户是否具备某角色；兼容只有 Role 的旧数据。
func (u *User) HasRole(role UserRole) bool {
	if u == nil {
		return false
	}
	if u.Role == role {
		return true
	}
	for _, r := range u.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// NormalizeRoles 去重并保证至少有普通用户角色。
func NormalizeRoles(roles []UserRole) []UserRole {
	seen := map[UserRole]struct{}{}
	out := make([]UserRole, 0, len(roles))
	for _, role := range roles {
		if !IsValidUserRole(role) {
			continue
		}
		if _, ok := seen[role]; ok {
			continue
		}
		seen[role] = struct{}{}
		out = append(out, role)
	}
	if len(out) == 0 {
		out = append(out, UserRoleNormal)
	}
	return out
}

// PrimaryRole 返回用于兼容旧 role 字段的主角色。
func PrimaryRole(roles []UserRole) UserRole {
	roles = NormalizeRoles(roles)
	for _, preferred := range []UserRole{UserRoleAdmin, UserRoleKnowledgeExpert, UserRoleKnowledgeStaff, UserRoleVIP, UserRoleNormal} {
		for _, role := range roles {
			if role == preferred {
				return role
			}
		}
	}
	return UserRoleNormal
}

func IsValidUserRole(role UserRole) bool {
	return role == UserRoleNormal ||
		role == UserRoleVIP ||
		role == UserRoleKnowledgeStaff ||
		role == UserRoleKnowledgeExpert ||
		role == UserRoleAdmin
}

// DefaultMaxSessionsForRoles 返回角色默认个人并发上限。
func DefaultMaxSessionsForRoles(roles []UserRole) int {
	roles = NormalizeRoles(roles)
	max := 1
	for _, role := range roles {
		switch role {
		case UserRoleAdmin:
			if max < 10 {
				max = 10
			}
		case UserRoleVIP:
			if max < 2 {
				max = 2
			}
		}
	}
	return max
}

func (u *User) MaxConcurrentSessions() int {
	if u == nil {
		return 1
	}
	if u.MaxSessions > 0 {
		return u.MaxSessions
	}
	return DefaultMaxSessionsForRoles(append(u.Roles, u.Role))
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
	Username       string        `json:"username,omitempty"` // 监控视图展示字段，不持久化到 sessions 表
	DomainID       string        `json:"domainId"`
	DomainName     string        `json:"domainName,omitempty"`
	Status         SessionStatus `json:"status"`
	CreatedAt      time.Time     `json:"createdAt"`           // 进入系统（含排队）时间
	StartedAt      *time.Time    `json:"startedAt,omitempty"` // 占用坐席（开始服务）时间
	ClosedAt       *time.Time    `json:"closedAt,omitempty"`
	CloseReason    CloseReason   `json:"closeReason,omitempty"`
	Title          string        `json:"title"`                    // 首问摘要，便于监控页识别
	AgentSessionID string        `json:"agentSessionId,omitempty"` // 底层 Agent/ACP 会话 ID，用于原生恢复
}

// Domain 业务领域。领域是知识、Runtime、会话入口的隔离单位。
type Domain struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	DefaultAgentID   string            `json:"defaultAgentId,omitempty"`
	Enabled          bool              `json:"enabled"`
	CreatedAt        time.Time         `json:"createdAt"`
	UpdatedAt        time.Time         `json:"updatedAt"`
	KnowledgeSources []KnowledgeSource `json:"knowledgeSources,omitempty"`
}

// KnowledgeSource 领域知识源，当前主要承载 MCP server 配置。
type KnowledgeSource struct {
	ID        string            `json:"id"`
	DomainID  string            `json:"domainId"`
	Name      string            `json:"name"`
	Type      string            `json:"type"` // stdio | http
	URL       string            `json:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Command   string            `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Enabled   bool              `json:"enabled"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
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

// CandidateAssetType 候选资产类型。历史上曾区分 faq/wiki；新候选统一使用 knowledge。
type CandidateAssetType string

const (
	CandidateAssetKnowledge CandidateAssetType = "knowledge"
	CandidateAssetFAQ       CandidateAssetType = "faq"  // 兼容旧数据
	CandidateAssetWiki      CandidateAssetType = "wiki" // 兼容旧数据
)

type KnowledgePublishTarget string

const (
	KnowledgePublishLocal         KnowledgePublishTarget = "local"
	KnowledgePublishSkill         KnowledgePublishTarget = "skill"
	KnowledgePublishKnowledgeBase KnowledgePublishTarget = "knowledge_base"
)

func NormalizeKnowledgePublishTargets(targets []KnowledgePublishTarget) []KnowledgePublishTarget {
	seen := map[KnowledgePublishTarget]struct{}{}
	out := make([]KnowledgePublishTarget, 0, len(targets))
	for _, target := range targets {
		switch target {
		case KnowledgePublishLocal, KnowledgePublishSkill, KnowledgePublishKnowledgeBase:
		default:
			continue
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		out = append(out, target)
	}
	if len(out) == 0 {
		out = append(out, KnowledgePublishLocal)
	}
	return out
}

// CandidateAssetStatus 候选资产审批状态
type CandidateAssetStatus string

const (
	CandidateStatusPending  CandidateAssetStatus = "pending"  // 待审批
	CandidateStatusApproved CandidateAssetStatus = "approved" // 已通过并发布
	CandidateStatusRejected CandidateAssetStatus = "rejected" // 已拒绝
)

// CandidateAsset 候选知识资产（自学习沙箱产物，审批后才进入正式知识）
type CandidateAsset struct {
	ID               string                   `json:"id"`
	AssetType        CandidateAssetType       `json:"assetType"`
	PublishTargets   []KnowledgePublishTarget `json:"publishTargets,omitempty"`
	Title            string                   `json:"title"`
	Question         string                   `json:"question,omitempty"`
	Content          string                   `json:"content"`
	Evidence         string                   `json:"evidence,omitempty"` // JSON：来源会话/消息摘要/纠错
	SourceSessionID  string                   `json:"sourceSessionId,omitempty"`
	SourceFeedbackID string                   `json:"sourceFeedbackId,omitempty"`
	Confidence       float64                  `json:"confidence"`
	Status           CandidateAssetStatus     `json:"status"`
	Reviewer         string                   `json:"reviewer,omitempty"`
	ReviewNote       string                   `json:"reviewNote,omitempty"`
	CreatedAt        time.Time                `json:"createdAt"`
	UpdatedAt        time.Time                `json:"updatedAt"`
}

// RuntimeLearningAssetType Agent Runtime 自学习资产类型。
type RuntimeLearningAssetType string

const (
	RuntimeLearningAssetSkill  RuntimeLearningAssetType = "skill"
	RuntimeLearningAssetMemory RuntimeLearningAssetType = "memory"
)

// RuntimeLearningChangeType Agent Runtime 自学习资产变更类型。
type RuntimeLearningChangeType string

const (
	RuntimeLearningChangeNew      RuntimeLearningChangeType = "new"
	RuntimeLearningChangeModified RuntimeLearningChangeType = "modified"
	RuntimeLearningChangeDeleted  RuntimeLearningChangeType = "deleted"
)

// RuntimeLearningStatus Agent Runtime 自学习审计状态。
type RuntimeLearningStatus string

const (
	RuntimeLearningStatusPendingReview        RuntimeLearningStatus = "pending_review"
	RuntimeLearningStatusKept                 RuntimeLearningStatus = "kept"
	RuntimeLearningStatusModified             RuntimeLearningStatus = "modified"
	RuntimeLearningStatusDeleted              RuntimeLearningStatus = "deleted"
	RuntimeLearningStatusConverted            RuntimeLearningStatus = "converted"
	RuntimeLearningStatusProhibitedAsEvidence RuntimeLearningStatus = "prohibited_as_evidence"
)

// RuntimeLearningAsset Agent Runtime 自学习审计记录。
// 记录具体 Agent Runtime 在自身资产目录中新增、修改、删除的文件，供人工审计。
type RuntimeLearningAsset struct {
	ID          string                    `json:"id"`
	AgentType   string                    `json:"agentType"`
	AssetType   RuntimeLearningAssetType  `json:"assetType"`
	Path        string                    `json:"path"`
	ContentHash string                    `json:"contentHash"`
	Content     string                    `json:"content,omitempty"`
	ChangeType  RuntimeLearningChangeType `json:"changeType"`
	RiskFlags   string                    `json:"riskFlags,omitempty"` // JSON array
	Status      RuntimeLearningStatus     `json:"status"`
	Reviewer    string                    `json:"reviewer,omitempty"`
	ReviewNote  string                    `json:"reviewNote,omitempty"`
	CreatedAt   time.Time                 `json:"createdAt"`
	UpdatedAt   time.Time                 `json:"updatedAt"`
}

// Backward-compatible aliases. The storage table is still hermes_learning_assets
// until a later migration can rename it safely.
type HermesLearningAssetType = RuntimeLearningAssetType
type HermesLearningChangeType = RuntimeLearningChangeType
type HermesLearningStatus = RuntimeLearningStatus
type HermesLearningAsset = RuntimeLearningAsset

const (
	HermesLearningAssetSkill  = RuntimeLearningAssetSkill
	HermesLearningAssetMemory = RuntimeLearningAssetMemory

	HermesLearningChangeNew      = RuntimeLearningChangeNew
	HermesLearningChangeModified = RuntimeLearningChangeModified
	HermesLearningChangeDeleted  = RuntimeLearningChangeDeleted

	HermesLearningStatusPendingReview        = RuntimeLearningStatusPendingReview
	HermesLearningStatusKept                 = RuntimeLearningStatusKept
	HermesLearningStatusModified             = RuntimeLearningStatusModified
	HermesLearningStatusDeleted              = RuntimeLearningStatusDeleted
	HermesLearningStatusConverted            = RuntimeLearningStatusConverted
	HermesLearningStatusProhibitedAsEvidence = RuntimeLearningStatusProhibitedAsEvidence
)

// LearningJobStatus AI 学习任务状态
type LearningJobStatus string

const (
	LearningJobStatusRunning   LearningJobStatus = "running"
	LearningJobStatusSucceeded LearningJobStatus = "succeeded"
	LearningJobStatusFailed    LearningJobStatus = "failed"
	LearningJobStatusSkipped   LearningJobStatus = "skipped"
)

// LearningJob AI 自动挖掘历史会话并提出候选知识建议的执行记录。
type LearningJob struct {
	ID            string            `json:"id"`
	Source        string            `json:"source"`
	Status        LearningJobStatus `json:"status"`
	InputSessions int               `json:"inputSessions"`
	OutputAssets  int               `json:"outputAssets"`
	Error         string            `json:"error,omitempty"`
	StartedAt     time.Time         `json:"startedAt"`
	FinishedAt    *time.Time        `json:"finishedAt,omitempty"`
}

// AgentSettings 运行时 Agent 配置（持久化在 DB，可在 Settings 页修改，覆盖 config.yaml 默认值）
type AgentSettings struct {
	Type               string    `json:"type"`
	CliPath            string    `json:"cliPath"`
	DefaultModel       string    `json:"defaultModel"`
	APIURL             string    `json:"apiUrl"`
	APIToken           string    `json:"apiToken"`
	SystemPrompt       string    `json:"systemPrompt"`
	SupportsMultimodal bool      `json:"supportsMultimodal"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// AgentProfile 一套可快速切换的 Agent / 模型配置。
type AgentProfile struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Settings AgentSettings `json:"settings"`
}

// AgentProfilesSettings Agent 多配置档案。
type AgentProfilesSettings struct {
	ActiveProfileID string         `json:"activeProfileId"`
	Profiles        []AgentProfile `json:"profiles"`
	UpdatedAt       time.Time      `json:"updatedAt"`
}

// AgentCapabilities 当前启用 Agent / 模型可安全暴露给前端的能力信息。
type AgentCapabilities struct {
	Type               string `json:"type"`
	DefaultModel       string `json:"defaultModel"`
	SupportsMultimodal bool   `json:"supportsMultimodal"`
}

// PoolSettings 坐席池运行时设置（持久化在 DB）
type PoolSettings struct {
	MaxActive int       `json:"maxActive"`
	MaxQueue  int       `json:"maxQueue"`
	UpdatedAt time.Time `json:"updatedAt"`
}
