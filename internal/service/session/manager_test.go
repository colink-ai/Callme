package session

import (
	"context"
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
	if _, err := m.CreateSession(ctx, testUser("client-a", model.UserRoleNormal)); err != ErrClientBusy {
		t.Fatalf("expect ErrClientBusy, got %v", err)
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
