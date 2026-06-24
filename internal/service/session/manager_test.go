package session

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"callme/internal/config"
	"callme/internal/model"
	"callme/internal/repo"
	"callme/internal/service/agent"

	"go.uber.org/zap"
)

// fakeAdapter 测试用 Agent 适配器：Prompt 回显输入
type fakeAdapter struct {
	mu           sync.Mutex
	sessions     map[string]bool
	agentIDs     map[string]string
	nativeResume map[string]bool
}

func (f *fakeAdapter) StartSession(ctx context.Context, sessionID string, req *agent.SessionRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[sessionID] = true
	if req.ResumeSessionID != "" {
		f.agentIDs[sessionID] = req.ResumeSessionID
		f.nativeResume[sessionID] = true
	} else {
		f.agentIDs[sessionID] = "agent-" + sessionID
	}
	return nil
}

func (f *fakeAdapter) Prompt(ctx context.Context, sessionID string, input string, images []agent.ImageContent, onChunk func(agent.Chunk)) error {
	onChunk(agent.Chunk{Type: agent.ChunkTypeToolUse, ToolName: "code-graph", ToolID: "t1"})
	onChunk(agent.Chunk{Type: agent.ChunkTypeText, Content: "echo: " + input})
	return nil
}

func (f *fakeAdapter) StopSession(sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, sessionID)
	return nil
}

func (f *fakeAdapter) GetSessionStatus(sessionID string) agent.SessionStatus {
	return agent.SessionStatusRunning
}

func (f *fakeAdapter) CheckHealth(ctx context.Context, spec agent.AgentSpec) error { return nil }

func (f *fakeAdapter) AgentSessionID(sessionID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.agentIDs[sessionID]
}

func (f *fakeAdapter) UsedNativeResume(sessionID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nativeResume[sessionID]
}

var registerFakeOnce sync.Once

func registerFake() {
	registerFakeOnce.Do(func() {
		agent.RegisterPlugin(agent.PluginMeta{
			Type: "fake",
			Name: "Fake",
			Factory: func() agent.Adapter {
				return &fakeAdapter{
					sessions:     make(map[string]bool),
					agentIDs:     make(map[string]string),
					nativeResume: make(map[string]bool),
				}
			},
		})
	})
}

func testUser(id string, role model.UserRole) *model.User {
	return &model.User{ID: id, Username: id, Role: role}
}

// stubSettings 测试用设置：坐席数 1，队列 5
type stubSettings struct{}

func (stubSettings) AgentSpec() agent.AgentSpec {
	return agent.AgentSpec{Type: "fake", CliPath: "fake", DefaultModel: "test-model"}
}
func (stubSettings) PoolSettings() model.PoolSettings {
	return model.PoolSettings{MaxActive: 1, MaxQueue: 5}
}

func newTestManager(t *testing.T) (*Manager, *repo.Store) {
	t.Helper()
	registerFake()

	dir := t.TempDir()
	db, err := repo.Open("sqlite", filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store := repo.NewStore(db)

	cfg := config.SessionConfig{
		MaxActive:      1,
		MaxQueue:       5,
		IdleWarnAfter:  time.Minute,
		IdleCloseAfter: 2 * time.Minute,
		MaxDuration:    time.Hour,
		MaxPerClient:   1,
	}
	agentCfg := config.AgentConfig{WorkDir: filepath.Join(dir, "workdir"), HermesHome: filepath.Join(dir, "home")}
	m := NewManager(cfg, agentCfg, store, stubSettings{}, func() []agent.MCPServerSpec { return nil }, zap.NewNop())
	t.Cleanup(m.Shutdown)
	return m, store
}

func TestSeatLimitAndQueue(t *testing.T) {
	m, _ := newTestManager(t)
	ctx := context.Background()

	// 第 1 个会话：占座激活
	v1, err := m.CreateSession(ctx, testUser("client-a", model.UserRoleNormal))
	if err != nil {
		t.Fatalf("create session 1: %v", err)
	}
	if v1.Status != model.SessionStatusActive {
		t.Fatalf("session 1 should be active, got %s", v1.Status)
	}
	if v1.StartedAt == nil {
		t.Fatal("active session must have startedAt")
	}

	// 第 2 个会话：坐席满，排队位置 1
	v2, err := m.CreateSession(ctx, testUser("client-b", model.UserRoleNormal))
	if err != nil {
		t.Fatalf("create session 2: %v", err)
	}
	if v2.Status != model.SessionStatusQueued || v2.Position != 1 {
		t.Fatalf("session 2 should be queued at position 1, got %s pos=%d", v2.Status, v2.Position)
	}

	// 同客户端再开会话：拒绝
	var limitErr *UserConcurrencyError
	if _, err := m.CreateSession(ctx, testUser("client-a", model.UserRoleNormal)); !errors.As(err, &limitErr) || limitErr.MaxSessions != 1 {
		t.Fatalf("expect UserConcurrencyError max=1, got %v", err)
	}

	// 排队会话发消息：拒绝
	if err := m.HandleUserMessage(ctx, v2.ID, "hi", nil); err != ErrSessionQueued {
		t.Fatalf("expect ErrSessionQueued, got %v", err)
	}

	// 关闭会话 1：会话 2 应被放行激活
	if err := m.CloseSession(ctx, v1.ID, model.CloseReasonUser); err != nil {
		t.Fatalf("close session 1: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		active, queued := m.Counts()
		if active == 1 && queued == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("session 2 not admitted: active=%d queued=%d", active, queued)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestMessageFlowAndPersistence(t *testing.T) {
	m, store := newTestManager(t)
	ctx := context.Background()

	v, err := m.CreateSession(ctx, testUser("client-x", model.UserRoleNormal))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	var mu sync.Mutex
	var events []Event
	if _, err := m.Subscribe(v.ID, func(ev Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := m.HandleUserMessage(ctx, v.ID, "如何部署？", nil); err != nil {
		t.Fatalf("handle message: %v", err)
	}

	// 校验消息落库：user + assistant（含工具调用记录）
	msgs, err := store.ListMessages(ctx, v.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expect 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != model.MessageRoleUser || msgs[1].Role != model.MessageRoleAssistant {
		t.Fatalf("unexpected roles: %s, %s", msgs[0].Role, msgs[1].Role)
	}
	if msgs[1].Content != "echo: 如何部署？" {
		t.Fatalf("unexpected assistant content: %q", msgs[1].Content)
	}
	if msgs[1].ToolCalls == "" {
		t.Fatal("assistant message should record tool calls (knowledge citations)")
	}

	// 校验事件流包含 chunk 与 done
	mu.Lock()
	defer mu.Unlock()
	var sawText, sawDone, sawToolUse bool
	for _, ev := range events {
		if ev.Type == EventChunk && ev.Chunk != nil {
			switch ev.Chunk.Type {
			case agent.ChunkTypeText:
				sawText = true
			case agent.ChunkTypeDone:
				sawDone = true
			case agent.ChunkTypeToolUse:
				sawToolUse = true
			}
		}
	}
	if !sawText || !sawDone || !sawToolUse {
		t.Fatalf("missing chunk events: text=%v done=%v toolUse=%v", sawText, sawDone, sawToolUse)
	}

	// 会话标题取自首问
	sess, err := store.GetSession(ctx, v.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.Title != "如何部署？" {
		t.Fatalf("unexpected title: %q", sess.Title)
	}
	if sess.AgentSessionID == "" {
		t.Fatal("agent session id should be persisted after activation")
	}
}

func TestContinueSessionReusesClosedSession(t *testing.T) {
	m, store := newTestManager(t)
	ctx := context.Background()
	user := testUser("client-resume", model.UserRoleNormal)

	v, err := m.CreateSession(ctx, user)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := m.HandleUserMessage(ctx, v.ID, "现在知识库中有什么内容", nil); err != nil {
		t.Fatalf("handle first message: %v", err)
	}
	if err := m.CloseSession(ctx, v.ID, model.CloseReasonUser); err != nil {
		t.Fatalf("close session: %v", err)
	}

	closed, err := store.GetSession(ctx, v.ID)
	if err != nil {
		t.Fatalf("get closed session: %v", err)
	}
	resumed, err := m.ContinueSession(ctx, user, closed)
	if err != nil {
		t.Fatalf("continue session: %v", err)
	}
	if resumed.ID != v.ID {
		t.Fatalf("continued session should reuse original id: got %s want %s", resumed.ID, v.ID)
	}
	if resumed.Status != model.SessionStatusActive {
		t.Fatalf("continued session should be active, got %s", resumed.Status)
	}

	msgs, err := store.ListMessages(ctx, v.ID)
	if err != nil {
		t.Fatalf("list messages before resumed prompt: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("continue should not copy messages, got %d", len(msgs))
	}
	if err := m.HandleUserMessage(ctx, resumed.ID, "继续说明一下", nil); err != nil {
		t.Fatalf("handle resumed message: %v", err)
	}
	msgs, err = store.ListMessages(ctx, v.ID)
	if err != nil {
		t.Fatalf("list messages after resumed prompt: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("resumed prompt should append to original session, got %d messages", len(msgs))
	}
	if strings.Contains(msgs[3].Content, "用户继续提问：") {
		t.Fatalf("native resume should not inject transcript prompt, got %q", msgs[3].Content)
	}
	resumedSess, err := store.GetSession(ctx, v.ID)
	if err != nil {
		t.Fatalf("get resumed session: %v", err)
	}
	if resumedSess.AgentSessionID != closed.AgentSessionID {
		t.Fatalf("agent session id should be reused: got %q want %q", resumedSess.AgentSessionID, closed.AgentSessionID)
	}
}

func TestManagerViewsAndLifecycleHelpers(t *testing.T) {
	m, _ := newTestManager(t)
	ctx := context.Background()

	v1, err := m.CreateSession(ctx, testUser("viewer-a", model.UserRoleNormal))
	if err != nil {
		t.Fatalf("create active session: %v", err)
	}
	v2, err := m.CreateSession(ctx, testUser("viewer-b", model.UserRoleNormal))
	if err != nil {
		t.Fatalf("create queued session: %v", err)
	}

	active, queued := m.Snapshot()
	if len(active) != 1 || len(queued) != 1 || queued[0].Position != 1 {
		t.Fatalf("unexpected snapshot: active=%d queued=%d pos=%d", len(active), len(queued), queued[0].Position)
	}
	if current := m.CurrentByUser("viewer-a"); current == nil || current.ID != v1.ID || current.Position != 0 {
		t.Fatalf("unexpected active current view: %+v", current)
	}
	if current := m.CurrentByUser("viewer-b"); current == nil || current.ID != v2.ID || current.Position != 1 {
		t.Fatalf("unexpected queued current view: %+v", current)
	}
	if current := m.CurrentByUser("missing"); current != nil {
		t.Fatalf("missing user should not have current session: %+v", current)
	}

	var sawState bool
	if view, err := m.Subscribe(v1.ID, func(ev Event) {
		if ev.Type == EventState {
			sawState = true
		}
	}); err != nil || view.ID != v1.ID {
		t.Fatalf("subscribe active: view=%+v err=%v", view, err)
	}
	m.Touch(v1.ID)
	m.notify(m.find(v1.ID), Event{Type: EventState, SessionID: v1.ID})
	if !sawState {
		t.Fatal("subscriber should receive events")
	}
	m.Unsubscribe(v1.ID)
	sawState = false
	m.notify(m.find(v1.ID), Event{Type: EventState, SessionID: v1.ID})
	if sawState {
		t.Fatal("unsubscribed listener should not receive events")
	}

	if _, err := m.Subscribe("missing", func(Event) {}); err != ErrSessionGone {
		t.Fatalf("subscribe missing should return ErrSessionGone, got %v", err)
	}
	if activeCount, queuedCount := m.Counts(); activeCount != 1 || queuedCount != 1 {
		t.Fatalf("unexpected counts: active=%d queued=%d", activeCount, queuedCount)
	}
}

func TestValidationAndStopBoundaries(t *testing.T) {
	m, _ := newTestManager(t)
	ctx := context.Background()

	if err := validateImages([]model.ImageContent{{MimeType: "text/plain", Data: "aGVsbG8="}}); err == nil || !strings.Contains(err.Error(), "仅支持图片文件") {
		t.Fatalf("expected mime validation error, got %v", err)
	}
	if err := validateImages([]model.ImageContent{{MimeType: "image/png", Data: "not-base64"}}); err == nil || !strings.Contains(err.Error(), "图片数据无效") {
		t.Fatalf("expected base64 validation error, got %v", err)
	}
	many := make([]model.ImageContent, maxUserImages+1)
	for i := range many {
		many[i] = model.ImageContent{MimeType: "image/png", Data: "aA=="}
	}
	if err := validateImages(many); err == nil || !strings.Contains(err.Error(), "最多支持") {
		t.Fatalf("expected image count validation error, got %v", err)
	}
	if err := validateImages([]model.ImageContent{{MimeType: "image/png", Data: "aA=="}}); err != nil {
		t.Fatalf("valid image rejected: %v", err)
	}

	v, err := m.CreateSession(ctx, testUser("stopper", model.UserRoleNormal))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := m.StopCurrentTurn(v.ID); err != nil {
		t.Fatalf("stop idle turn should be a no-op: %v", err)
	}
	if err := m.StopCurrentTurn("missing"); err != ErrSessionGone {
		t.Fatalf("stop missing should return ErrSessionGone, got %v", err)
	}
	if err := m.HandleUserMessage(ctx, "missing", "hi", nil); err != ErrSessionGone {
		t.Fatalf("message to missing session should return ErrSessionGone, got %v", err)
	}
	if err := m.HandleUserMessage(ctx, v.ID, "看图", []model.ImageContent{{MimeType: "image/png", Data: "aA=="}}); err == nil || !strings.Contains(err.Error(), "不支持图片输入") {
		t.Fatalf("non-multimodal model should reject images, got %v", err)
	}
}

func TestInternalErrorAndCleanupHelpers(t *testing.T) {
	m, _ := newTestManager(t)
	errText := (&UserConcurrencyError{MaxSessions: 2, CurrentSessions: 3}).Error()
	if !strings.Contains(errText, "最多允许 2 个并发会话") || !strings.Contains(errText, "已有 3 个") {
		t.Fatalf("unexpected concurrency error text: %s", errText)
	}

	l := &live{sess: &model.Session{ID: "closed", Status: model.SessionStatusClosed}}
	if !m.isPromptInterruptedByClose("closed", l, context.Canceled) {
		t.Fatal("context canceled should be treated as close interruption")
	}
	if !m.isPromptInterruptedByClose("closed", l, errors.New("agent aborted")) {
		t.Fatal("closed live should be treated as close interruption")
	}
	l.sess.Status = model.SessionStatusActive
	if m.isPromptInterruptedByClose("closed", l, errors.New("ordinary failure")) {
		t.Fatal("ordinary active prompt failure should not be treated as close interruption")
	}

	active := &live{sess: &model.Session{ID: "active"}}
	queued := &live{sess: &model.Session{ID: "queued"}}
	m.mu.Lock()
	m.active["active"] = active
	m.queue = append(m.queue, queued)
	m.mu.Unlock()
	m.evict("active")
	m.evict("queued")
	if activeCount, queuedCount := m.Counts(); activeCount != 0 || queuedCount != 0 {
		t.Fatalf("evict should remove active and queued sessions, active=%d queued=%d", activeCount, queuedCount)
	}
}
