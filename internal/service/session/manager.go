// Package session 坐席制会话管理
//
// 并发控制模型：像人工客服一样只有有限"坐席"（max_active 个并发 Hermes 进程）。
// 新会话先尝试占座，满了进入 FIFO 队列；坐席释放时按序放行。
// 每个会话显式记录开始时间与持续时长，供用户端与运营监控页展示。
package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"callme/internal/config"
	"callme/internal/model"
	"callme/internal/repo"
	"callme/internal/service/agent"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const maxUserImages = 4
const maxImageBytes = 10 * 1024 * 1024

// 错误定义
var (
	ErrQueueFull     = errors.New("排队人数已满，请稍后再试")
	ErrClientBusy    = errors.New("您已有进行中的会话")
	ErrSessionGone   = errors.New("会话不存在或已结束")
	ErrSessionBusy   = errors.New("当前回答尚未完成，请稍候")
	ErrSessionQueued = errors.New("会话排队中，请等待接入")
)

type UserConcurrencyError struct {
	MaxSessions     int
	CurrentSessions int
}

func (e *UserConcurrencyError) Error() string {
	return fmt.Sprintf("由于算力资源有限，当前账号最多允许 %d 个并发会话。您已有 %d 个进行中的会话，请结束已有会话后再新建。", e.MaxSessions, e.CurrentSessions)
}

// EventType 推送给前端的事件类型
type EventType string

const (
	EventChunk       EventType = "chunk"        // Agent 流式输出块
	EventQueue       EventType = "queue"        // 排队位置更新
	EventState       EventType = "state"        // 会话状态变更
	EventIdleWarning EventType = "idle_warning" // 空闲提醒
	EventClosed      EventType = "closed"       // 会话结束
	EventMessage     EventType = "message"      // 完整消息落库通知（含 messageId，反馈用）
	EventError       EventType = "error"        // 错误
)

// Event 推送事件
type Event struct {
	Type      EventType      `json:"type"`
	SessionID string         `json:"sessionId"`
	Chunk     *agent.Chunk   `json:"chunk,omitempty"`
	Position  int            `json:"position,omitempty"` // 排队位置（1 起）
	QueueLen  int            `json:"queueLen,omitempty"`
	Active    int            `json:"active,omitempty"`
	MaxActive int            `json:"maxActive,omitempty"`
	Session   *SessionView   `json:"session,omitempty"`
	Message   *model.Message `json:"message,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	Error     string         `json:"error,omitempty"`
}

// SessionView 会话视图（带计算的时长）
type SessionView struct {
	*model.Session
	DurationSeconds int64  `json:"durationSeconds"`
	WaitingSeconds  int64  `json:"waitingSeconds"` // 排队等待时长
	Position        int    `json:"position,omitempty"`
	QueueLen        int    `json:"queueLen"`
	Active          int    `json:"active"`
	MaxActive       int    `json:"maxActive"`
	Model           string `json:"model,omitempty"`     // 当前生效模型（消息卡片流式期间展示）
	AgentType       string `json:"agentType,omitempty"` // 当前生效 Agent 类型（消息卡片流式期间展示）
}

// Subscriber 事件订阅回调（WS 推送）
type Subscriber func(Event)

// live 内存中的活跃/排队会话
type live struct {
	sess         *model.Session
	adapter      agent.Adapter // 占座后才创建
	subscriber   Subscriber
	lastActivity time.Time
	busy         bool // 一轮 Prompt 进行中
	cancelPrompt context.CancelFunc
	turnCount    int
	mu           sync.Mutex
}

// SettingsProvider 提供运行时 Agent/坐席配置（DB 覆盖 config 默认）
type SettingsProvider interface {
	AgentSpec() agent.AgentSpec
	PoolSettings() model.PoolSettings
}

// AdapterFactory creates a runtime adapter for the active AgentSpec. Concrete
// agent adapters are owned by Helios; Callme injects this factory from its
// runtime integration layer.
type AdapterFactory func(agent.AgentSpec) (agent.Adapter, error)

// Manager 会话管理器
type Manager struct {
	cfg      config.SessionConfig
	agentCfg config.AgentConfig
	store    *repo.Store
	settings SettingsProvider
	adapters AdapterFactory
	mcpSpecs func(domainID string) []agent.MCPServerSpec
	logger   *zap.Logger

	active map[string]*live
	queue  []*live // FIFO
	mu     sync.Mutex

	stopJanitor chan struct{}
}

// NewManager 创建会话管理器
func NewManager(
	cfg config.SessionConfig,
	agentCfg config.AgentConfig,
	store *repo.Store,
	settings SettingsProvider,
	adapters AdapterFactory,
	mcpSpecs func(domainID string) []agent.MCPServerSpec,
	logger *zap.Logger,
) *Manager {
	m := &Manager{
		cfg:         cfg,
		agentCfg:    agentCfg,
		store:       store,
		settings:    settings,
		adapters:    adapters,
		mcpSpecs:    mcpSpecs,
		logger:      logger,
		active:      make(map[string]*live),
		stopJanitor: make(chan struct{}),
	}
	go m.janitor()
	return m
}

// Shutdown 停止管理器并结束所有会话
func (m *Manager) Shutdown() {
	close(m.stopJanitor)
	m.mu.Lock()
	ids := make([]string, 0, len(m.active))
	for id := range m.active {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.CloseSession(context.Background(), id, model.CloseReasonAdmin)
	}
}

// CreateSession 创建会话：占座成功直接激活，否则普通用户排队；VIP 不受排队约束。
func (m *Manager) CreateSession(ctx context.Context, user *model.User) (*SessionView, error) {
	return m.CreateSessionInDomain(ctx, user, config.DefaultDomainID)
}

func (m *Manager) CreateSessionInDomain(ctx context.Context, user *model.User, domainID string) (*SessionView, error) {
	if domainID == "" {
		domainID = config.DefaultDomainID
	}
	return m.createSession(ctx, user, domainID, "")
}

// ContinueSession 复用已结束的历史会话记录。历史上下文只通过 Agent 原生 resume 恢复，不注入 Prompt。
func (m *Manager) ContinueSession(ctx context.Context, user *model.User, source *model.Session) (*SessionView, error) {
	pool := m.settings.PoolSettings()
	userID := user.ID

	m.mu.Lock()
	if _, exists := m.active[source.ID]; exists {
		m.mu.Unlock()
		return nil, ErrClientBusy
	}
	for _, q := range m.queue {
		if q.sess.ID == source.ID {
			m.mu.Unlock()
			return nil, ErrClientBusy
		}
	}
	userCount := 0
	for _, l := range m.active {
		if l.sess.UserID == userID {
			userCount++
		}
	}
	for _, l := range m.queue {
		if l.sess.UserID == userID {
			userCount++
		}
	}
	maxSessions := user.MaxConcurrentSessions()
	if userCount >= maxSessions {
		m.mu.Unlock()
		return nil, &UserConcurrencyError{MaxSessions: maxSessions, CurrentSessions: userCount}
	}
	isVIP := user.HasRole(model.UserRoleVIP)
	if !isVIP && len(m.queue) >= pool.MaxQueue {
		m.mu.Unlock()
		return nil, ErrQueueFull
	}

	now := time.Now()
	source.Status = model.SessionStatusQueued
	source.CreatedAt = now
	source.StartedAt = nil
	source.ClosedAt = nil
	source.CloseReason = ""
	l := &live{sess: source, lastActivity: now}

	canActivate := isVIP || len(m.active) < pool.MaxActive
	if canActivate {
		m.active[source.ID] = l
	} else {
		m.queue = append(m.queue, l)
	}
	position := len(m.queue)
	m.mu.Unlock()

	if err := m.store.ReopenSession(ctx, source); err != nil {
		m.evict(source.ID)
		return nil, fmt.Errorf("复用历史会话失败: %w", err)
	}

	if canActivate {
		if err := m.activate(l); err != nil {
			now := time.Now()
			source.Status = model.SessionStatusClosed
			source.CloseReason = model.CloseReasonError
			source.ClosedAt = &now
			if updateErr := m.store.UpdateSession(context.Background(), source); updateErr != nil {
				m.logger.Error("persist failed resumed session close failed", zap.String("sessionID", source.ID), zap.Error(updateErr))
			}
			return nil, err
		}
		return m.view(l), nil
	}

	m.logger.Info("session resumed and queued", zap.String("sessionID", source.ID), zap.Int("position", position))
	v := m.view(l)
	v.Position = position
	return v, nil
}

func (m *Manager) createSession(ctx context.Context, user *model.User, domainID, title string) (*SessionView, error) {
	pool := m.settings.PoolSettings()
	userID := user.ID

	m.mu.Lock()
	// 单用户会话数限制
	userCount := 0
	for _, l := range m.active {
		if l.sess.UserID == userID {
			userCount++
		}
	}
	for _, l := range m.queue {
		if l.sess.UserID == userID {
			userCount++
		}
	}
	maxSessions := user.MaxConcurrentSessions()
	if userCount >= maxSessions {
		m.mu.Unlock()
		return nil, &UserConcurrencyError{MaxSessions: maxSessions, CurrentSessions: userCount}
	}
	isVIP := user.HasRole(model.UserRoleVIP)
	if !isVIP && len(m.queue) >= pool.MaxQueue {
		m.mu.Unlock()
		return nil, ErrQueueFull
	}

	now := time.Now()
	sess := &model.Session{
		ID:        uuid.New().String(),
		ClientID:  userID,
		UserID:    userID,
		DomainID:  domainID,
		Status:    model.SessionStatusQueued,
		CreatedAt: now,
		Title:     title,
	}
	l := &live{sess: sess, lastActivity: now}

	canActivate := isVIP || len(m.active) < pool.MaxActive
	if canActivate {
		// 先占座，随后同步拉起 Agent；启动失败会释放坐席。
		m.active[sess.ID] = l
	} else {
		m.queue = append(m.queue, l)
	}
	position := len(m.queue)
	m.mu.Unlock()

	if err := m.store.CreateSession(ctx, sess); err != nil {
		m.evict(sess.ID)
		return nil, fmt.Errorf("持久化会话失败: %w", err)
	}

	if canActivate {
		if err := m.activate(l); err != nil {
			now := time.Now()
			sess.Status = model.SessionStatusClosed
			sess.CloseReason = model.CloseReasonError
			sess.ClosedAt = &now
			if updateErr := m.store.UpdateSession(context.Background(), sess); updateErr != nil {
				m.logger.Error("persist failed session close failed", zap.String("sessionID", sess.ID), zap.Error(updateErr))
			}
			return nil, err
		}
		return m.view(l), nil
	}

	m.logger.Info("session queued", zap.String("sessionID", sess.ID), zap.Int("position", position))
	v := m.view(l)
	v.Position = position
	return v, nil
}

// activate 占座会话：启动 Hermes 进程并标记 active
func (m *Manager) activate(l *live) error {
	spec := m.settings.AgentSpec()
	domainID := l.sess.DomainID
	if domainID == "" {
		domainID = config.DefaultDomainID
		l.sess.DomainID = domainID
	}
	runtimeHome := m.agentCfg.RuntimeHomeForDomain(domainID)
	spec.RuntimeHome = runtimeHome
	spec.HermesHome = runtimeHome
	if spec.DefaultModel == "" {
		m.evict(l.sess.ID)
		return fmt.Errorf("请先在设置中配置 Agent 模型")
	}
	adapter, err := m.adapters(spec)
	if err != nil {
		m.evict(l.sess.ID)
		return err
	}

	workDir := filepath.Join(m.agentCfg.WorkDirForDomain(domainID), l.sess.ID)
	if absWorkDir, err := filepath.Abs(workDir); err == nil {
		workDir = absWorkDir
	}

	req := &agent.SessionRequest{
		Spec:            spec,
		WorkDir:         workDir,
		MCPServers:      m.mcpSpecs(domainID),
		ResumeSessionID: l.sess.AgentSessionID,
	}

	if err := adapter.StartSession(context.Background(), l.sess.ID, req); err != nil {
		m.evict(l.sess.ID)
		m.logger.Error("agent session start failed", zap.String("sessionID", l.sess.ID), zap.Error(err))
		return fmt.Errorf("客服 Agent 启动失败: %w", err)
	}

	now := time.Now()
	l.mu.Lock()
	if inspector, ok := adapter.(agent.SessionIntrospector); ok {
		if agentSessionID := inspector.AgentSessionID(l.sess.ID); agentSessionID != "" {
			l.sess.AgentSessionID = agentSessionID
		}
	}
	l.adapter = adapter
	l.lastActivity = now
	l.mu.Unlock()

	l.sess.Status = model.SessionStatusActive
	l.sess.StartedAt = &now
	if err := m.store.UpdateSession(context.Background(), l.sess); err != nil {
		m.logger.Error("persist session activation failed", zap.Error(err))
	}

	m.logger.Info("session activated",
		zap.String("sessionID", l.sess.ID),
		zap.String("agentSessionID", l.sess.AgentSessionID),
		zap.String("model", spec.DefaultModel))
	m.notify(l, Event{Type: EventState, SessionID: l.sess.ID, Session: m.view(l)})
	return nil
}

// HandleUserMessage 处理一轮用户输入：落库、调用 Agent、流式回调、助手消息落库
func (m *Manager) HandleUserMessage(ctx context.Context, sessionID, content string, images []model.ImageContent) error {
	if err := validateImages(images); err != nil {
		return err
	}
	spec := m.settings.AgentSpec()
	if len(images) > 0 && !spec.SupportsMultimodal {
		return fmt.Errorf("当前启用的模型不支持图片输入，请切换到支持多模态的模型后再发送图片")
	}
	m.mu.Lock()
	l, ok := m.active[sessionID]
	m.mu.Unlock()
	if !ok {
		if m.queuePosition(sessionID) > 0 {
			return ErrSessionQueued
		}
		return ErrSessionGone
	}

	l.mu.Lock()
	if l.busy {
		l.mu.Unlock()
		return ErrSessionBusy
	}
	turnCtx, cancel := context.WithCancel(ctx)
	l.busy = true
	l.cancelPrompt = cancel
	l.turnCount++
	turn := l.turnCount
	adapter := l.adapter
	l.lastActivity = time.Now()
	l.mu.Unlock()

	defer func() {
		cancel()
		l.mu.Lock()
		l.busy = false
		l.cancelPrompt = nil
		l.lastActivity = time.Now()
		l.mu.Unlock()
	}()

	imageJSON := ""
	if len(images) > 0 {
		if data, err := json.Marshal(images); err == nil {
			imageJSON = string(data)
		}
	}

	// 用户消息落库
	userMsg := &model.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Role:      model.MessageRoleUser,
		Content:   content,
		ToolCalls: imageJSON,
		CreatedAt: time.Now(),
	}
	if err := m.store.CreateMessage(ctx, userMsg); err != nil {
		return fmt.Errorf("持久化用户消息失败: %w", err)
	}
	m.notify(l, Event{Type: EventMessage, SessionID: sessionID, Message: userMsg})

	// 首问作为会话标题（监控页识别）
	if turn == 1 && l.sess.Title == "" {
		title := content
		if title == "" && len(images) > 0 {
			title = "图片提问"
		}
		l.sess.Title = truncate(title, 80)
		m.store.UpdateSession(ctx, l.sess)
	}

	// 首轮注入客服系统提示词
	input := content
	if input == "" && len(images) > 0 {
		input = "请根据图片回答用户问题。"
	}
	if turn == 1 && spec.SystemPrompt != "" {
		input = spec.SystemPrompt + "\n\n---\n\n" + input
	}
	agentImages := make([]agent.ImageContent, 0, len(images))
	for _, img := range images {
		agentImages = append(agentImages, agent.ImageContent{MimeType: img.MimeType, Data: img.Data})
	}

	// 调用 Agent，流式回调推送 + 累积助手回答与工具调用（知识引用）
	var answer strings.Builder
	var toolCalls []map[string]any
	err := adapter.Prompt(turnCtx, sessionID, input, agentImages, func(chunk agent.Chunk) {
		switch chunk.Type {
		case agent.ChunkTypeText:
			answer.WriteString(chunk.Content)
		case agent.ChunkTypeToolUse:
			toolCalls = append(toolCalls, map[string]any{
				"toolId":   chunk.ToolID,
				"toolName": chunk.ToolName,
				"input":    chunk.ToolInput,
			})
		}
		m.notify(l, Event{Type: EventChunk, SessionID: sessionID, Chunk: &chunk})
	})

	if err != nil {
		if errors.Is(turnCtx.Err(), context.Canceled) {
			m.logger.Info("agent prompt stopped by user", zap.String("sessionID", sessionID))
			if answer.Len() > 0 {
				m.persistAssistantMessage(context.Background(), sessionID, spec, answer.String()+"\n\n（已停止生成）", toolCalls)
			}
			m.notify(l, Event{Type: EventChunk, SessionID: sessionID, Chunk: &agent.Chunk{Type: agent.ChunkTypeDone}})
			return nil
		}
		if m.isPromptInterruptedByClose(sessionID, l, err) {
			m.logger.Info("agent prompt interrupted by session close", zap.String("sessionID", sessionID), zap.Error(err))
			return nil
		}
		m.logger.Error("agent prompt failed", zap.String("sessionID", sessionID), zap.Error(err))
		m.notify(l, Event{Type: EventError, SessionID: sessionID, Error: "回答生成失败，请重试或转人工"})
		return err
	}

	assistantMsg := m.persistAssistantMessage(ctx, sessionID, spec, answer.String(), toolCalls)
	m.notify(l, Event{Type: EventChunk, SessionID: sessionID, Chunk: &agent.Chunk{Type: agent.ChunkTypeDone}})
	if assistantMsg != nil {
		m.notify(l, Event{Type: EventMessage, SessionID: sessionID, Message: assistantMsg})
	}
	return nil
}

func (m *Manager) persistAssistantMessage(ctx context.Context, sessionID string, spec agent.AgentSpec, content string, toolCalls []map[string]any) *model.Message {
	toolCallsJSON := ""
	if len(toolCalls) > 0 {
		if data, err := json.Marshal(toolCalls); err == nil {
			toolCallsJSON = string(data)
		}
	}
	assistantMsg := &model.Message{
		ID:        uuid.New().String(),
		SessionID: sessionID,
		Role:      model.MessageRoleAssistant,
		Content:   content,
		ToolCalls: toolCallsJSON,
		Model:     spec.DefaultModel,
		AgentType: spec.Type,
		CreatedAt: time.Now(),
	}
	if err := m.store.CreateMessage(ctx, assistantMsg); err != nil {
		m.logger.Error("persist assistant message failed", zap.Error(err))
		return nil
	}
	return assistantMsg
}

func validateImages(images []model.ImageContent) error {
	if len(images) > maxUserImages {
		return fmt.Errorf("最多支持 %d 张图片", maxUserImages)
	}
	for _, img := range images {
		if !strings.HasPrefix(img.MimeType, "image/") {
			return fmt.Errorf("仅支持图片文件")
		}
		data, err := base64.StdEncoding.DecodeString(img.Data)
		if err != nil {
			return fmt.Errorf("图片数据无效")
		}
		if len(data) > maxImageBytes {
			return fmt.Errorf("单张图片不能超过 10MB")
		}
	}
	return nil
}

func (m *Manager) isPromptInterruptedByClose(sessionID string, l *live, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}

	l.mu.Lock()
	closed := l.sess.Status == model.SessionStatusClosed
	l.mu.Unlock()
	if closed {
		return true
	}

	m.mu.Lock()
	_, stillActive := m.active[sessionID]
	m.mu.Unlock()
	if !stillActive {
		text := strings.ToLower(err.Error())
		return strings.Contains(text, "context canceled") || strings.Contains(text, "aborted")
	}
	return false
}

// StopCurrentTurn 只停止当前一轮回答，不关闭会话。
func (m *Manager) StopCurrentTurn(sessionID string) error {
	m.mu.Lock()
	l, ok := m.active[sessionID]
	m.mu.Unlock()
	if !ok {
		return ErrSessionGone
	}
	l.mu.Lock()
	cancel := l.cancelPrompt
	busy := l.busy
	l.mu.Unlock()
	if !busy || cancel == nil {
		return nil
	}
	cancel()
	m.notify(l, Event{Type: EventChunk, SessionID: sessionID, Chunk: &agent.Chunk{Type: agent.ChunkTypeDone}})
	return nil
}

// CloseSession 结束会话并释放坐席，按序放行队首
func (m *Manager) CloseSession(ctx context.Context, sessionID string, reason model.CloseReason) error {
	m.mu.Lock()
	l, wasActive := m.active[sessionID]
	if wasActive {
		delete(m.active, sessionID)
	} else {
		for i, q := range m.queue {
			if q.sess.ID == sessionID {
				l = q
				m.queue = append(m.queue[:i], m.queue[i+1:]...)
				if reason == model.CloseReasonUser {
					reason = model.CloseReasonQueueLeave
				}
				break
			}
		}
	}
	m.mu.Unlock()

	if l == nil {
		return ErrSessionGone
	}

	l.mu.Lock()
	adapter := l.adapter
	l.mu.Unlock()
	if adapter != nil {
		adapter.StopSession(sessionID)
	}

	now := time.Now()
	l.sess.Status = model.SessionStatusClosed
	l.sess.ClosedAt = &now
	l.sess.CloseReason = reason
	if err := m.store.UpdateSession(ctx, l.sess); err != nil {
		m.logger.Error("persist session close failed", zap.Error(err))
	}

	m.notify(l, Event{Type: EventClosed, SessionID: sessionID, Reason: string(reason), Session: m.view(l)})
	m.logger.Info("session closed", zap.String("sessionID", sessionID), zap.String("reason", string(reason)))

	if wasActive {
		m.admitNext()
	}
	m.broadcastQueuePositions()
	return nil
}

// admitNext 坐席释放后放行队首
func (m *Manager) admitNext() {
	pool := m.settings.PoolSettings()

	m.mu.Lock()
	if len(m.queue) == 0 || len(m.active) >= pool.MaxActive {
		m.mu.Unlock()
		return
	}
	next := m.queue[0]
	m.queue = m.queue[1:]
	m.active[next.sess.ID] = next
	m.mu.Unlock()

	go func() {
		if err := m.activate(next); err != nil {
			m.notify(next, Event{Type: EventError, SessionID: next.sess.ID, Error: err.Error()})
			return
		}
	}()
}

// broadcastQueuePositions 推送最新排队位置
func (m *Manager) broadcastQueuePositions() {
	m.mu.Lock()
	snapshot := make([]*live, len(m.queue))
	copy(snapshot, m.queue)
	m.mu.Unlock()

	for i, l := range snapshot {
		pool := m.settings.PoolSettings()
		m.mu.Lock()
		active := len(m.active)
		m.mu.Unlock()
		m.notify(l, Event{
			Type:      EventQueue,
			SessionID: l.sess.ID,
			Position:  i + 1,
			QueueLen:  len(snapshot),
			Active:    active,
			MaxActive: pool.MaxActive,
		})
	}
}

// Subscribe 绑定事件订阅（WS 连接建立时调用），返回当前会话视图
func (m *Manager) Subscribe(sessionID string, fn Subscriber) (*SessionView, error) {
	l := m.find(sessionID)
	if l == nil {
		return nil, ErrSessionGone
	}
	l.mu.Lock()
	l.subscriber = fn
	l.mu.Unlock()

	v := m.view(l)
	if pos := m.queuePosition(sessionID); pos > 0 {
		v.Position = pos
	}
	return v, nil
}

// Unsubscribe 解绑订阅（WS 断开）
func (m *Manager) Unsubscribe(sessionID string) {
	if l := m.find(sessionID); l != nil {
		l.mu.Lock()
		l.subscriber = nil
		l.mu.Unlock()
	}
}

// Touch 记录用户活动（防空闲回收）
func (m *Manager) Touch(sessionID string) {
	if l := m.find(sessionID); l != nil {
		l.mu.Lock()
		l.lastActivity = time.Now()
		l.mu.Unlock()
	}
}

// Snapshot 运营监控视图：活跃会话（含开始时间/时长）与排队队列
func (m *Manager) Snapshot() (activeViews, queuedViews []*SessionView) {
	m.mu.Lock()
	actives := make([]*live, 0, len(m.active))
	for _, l := range m.active {
		actives = append(actives, l)
	}
	queued := make([]*live, len(m.queue))
	copy(queued, m.queue)
	m.mu.Unlock()

	for _, l := range actives {
		activeViews = append(activeViews, m.view(l))
	}
	for i, l := range queued {
		v := m.view(l)
		v.Position = i + 1
		queuedViews = append(queuedViews, v)
	}
	return activeViews, queuedViews
}

// CurrentByUser 查询某个登录用户当前活跃/排队会话。
func (m *Manager) CurrentByUser(userID string) *SessionView {
	m.mu.Lock()
	for _, l := range m.active {
		if l.sess.UserID == userID {
			m.mu.Unlock()
			return m.view(l)
		}
	}
	for i, l := range m.queue {
		if l.sess.UserID == userID {
			m.mu.Unlock()
			v := m.view(l)
			v.Position = i + 1
			return v
		}
	}
	m.mu.Unlock()
	return nil
}

// Counts 当前活跃数与排队数
func (m *Manager) Counts() (active, queued int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.active), len(m.queue)
}

// ---------- 内部辅助 ----------

func (m *Manager) find(sessionID string) *live {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.active[sessionID]; ok {
		return l
	}
	for _, l := range m.queue {
		if l.sess.ID == sessionID {
			return l
		}
	}
	return nil
}

func (m *Manager) queuePosition(sessionID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, l := range m.queue {
		if l.sess.ID == sessionID {
			return i + 1
		}
	}
	return 0
}

func (m *Manager) evict(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.active, sessionID)
	for i, q := range m.queue {
		if q.sess.ID == sessionID {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			break
		}
	}
}

func (m *Manager) notify(l *live, ev Event) {
	l.mu.Lock()
	fn := l.subscriber
	l.mu.Unlock()
	if fn != nil {
		fn(ev)
	}
}

func (m *Manager) view(l *live) *SessionView {
	now := time.Now()
	pool := m.settings.PoolSettings()
	m.mu.Lock()
	active := len(m.active)
	queueLen := len(m.queue)
	m.mu.Unlock()
	v := &SessionView{
		Session:         l.sess,
		DurationSeconds: l.sess.DurationSeconds(now),
		QueueLen:        queueLen,
		Active:          active,
		MaxActive:       pool.MaxActive,
	}
	spec := m.settings.AgentSpec()
	v.Model = spec.DefaultModel
	v.AgentType = spec.Type
	if l.sess.StartedAt == nil {
		v.WaitingSeconds = int64(now.Sub(l.sess.CreatedAt).Seconds())
	}
	return v
}

// janitor 周期回收：空闲提醒 / 空闲关闭 / 最大时长
func (m *Manager) janitor() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopJanitor:
			return
		case <-ticker.C:
			m.reapIdle()
		}
	}
}

func (m *Manager) reapIdle() {
	now := time.Now()
	m.mu.Lock()
	actives := make([]*live, 0, len(m.active))
	for _, l := range m.active {
		actives = append(actives, l)
	}
	m.mu.Unlock()

	for _, l := range actives {
		l.mu.Lock()
		idle := now.Sub(l.lastActivity)
		busy := l.busy
		started := l.sess.StartedAt
		l.mu.Unlock()
		if busy {
			continue
		}

		switch {
		case started != nil && now.Sub(*started) > m.cfg.MaxDuration:
			m.CloseSession(context.Background(), l.sess.ID, model.CloseReasonMaxTime)
		case idle > m.cfg.IdleCloseAfter:
			m.CloseSession(context.Background(), l.sess.ID, model.CloseReasonIdle)
		case idle > m.cfg.IdleWarnAfter:
			m.notify(l, Event{Type: EventIdleWarning, SessionID: l.sess.ID})
		}
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
