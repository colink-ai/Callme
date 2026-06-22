package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"callme/internal/config"
	"callme/internal/model"
	"callme/internal/repo"
	"callme/internal/service/agent"
	"callme/internal/service/auth"
	"callme/internal/service/session"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

type wsFakeAdapter struct{}

func (wsFakeAdapter) StartSession(ctx context.Context, sessionID string, req *agent.SessionRequest) error {
	return nil
}

func (wsFakeAdapter) Prompt(ctx context.Context, sessionID string, input string, images []agent.ImageContent, onChunk func(agent.Chunk)) error {
	onChunk(agent.Chunk{Type: agent.ChunkTypeText, Content: "ws answer: " + input})
	onChunk(agent.Chunk{Type: agent.ChunkTypeDone})
	return nil
}

func (wsFakeAdapter) StopSession(sessionID string) error { return nil }

func (wsFakeAdapter) GetSessionStatus(sessionID string) agent.SessionStatus {
	return agent.SessionStatusRunning
}

func (wsFakeAdapter) CheckHealth(ctx context.Context, spec agent.AgentSpec) error { return nil }

type wsSettings struct{}

func (wsSettings) AgentSpec() agent.AgentSpec {
	return agent.AgentSpec{Type: "ws_fake", CliPath: "ws-fake", DefaultModel: "ws-model"}
}

func (wsSettings) PoolSettings() model.PoolSettings {
	return model.PoolSettings{MaxActive: 2, MaxQueue: 2}
}

var registerWSFakeOnce sync.Once

func registerWSFakeAgent() {
	registerWSFakeOnce.Do(func() {
		agent.RegisterPlugin(agent.PluginMeta{
			Type:    "ws_fake",
			Name:    "WS Fake",
			Factory: func() agent.Adapter { return wsFakeAdapter{} },
		})
	})
}

func newWSTestHarness(t *testing.T) (*gin.Engine, *session.Manager, *auth.Service, *repo.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	registerWSFakeAgent()

	dir := t.TempDir()
	db, err := repo.Open("sqlite", filepath.Join(dir, "ws.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	store := repo.NewStore(db)
	authSvc := auth.NewService(store, time.Hour)
	manager := session.NewManager(
		config.SessionConfig{
			MaxActive:      2,
			MaxQueue:       2,
			IdleWarnAfter:  time.Minute,
			IdleCloseAfter: 2 * time.Minute,
			MaxDuration:    time.Hour,
		},
		config.AgentConfig{WorkDir: filepath.Join(dir, "workdir"), HermesHome: filepath.Join(dir, "home")},
		store,
		wsSettings{},
		func() []agent.MCPServerSpec { return nil },
		zap.NewNop(),
	)
	t.Cleanup(func() {
		manager.Shutdown()
		_ = db.Close()
	})

	handler := NewHandler(manager, authSvc, store, zap.NewNop())
	router := gin.New()
	router.GET("/ws/:sessionId", handler.HandleWebSocket)
	return router, manager, authSvc, store
}

func TestHandleWebSocketFullMessageFlow(t *testing.T) {
	router, manager, authSvc, _ := newWSTestHarness(t)
	ctx := context.Background()

	result, err := authSvc.Register(ctx, "ws-user", "pass1234")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	view, err := manager.CreateSession(ctx, result.User)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	server := httptest.NewServer(router)
	defer server.Close()
	url := "ws" + server.URL[len("http"):] + "/ws/" + view.ID + "?token=" + result.Token
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	var ev session.Event
	if err := conn.ReadJSON(&ev); err != nil {
		t.Fatalf("read initial state: %v", err)
	}
	if ev.Type != session.EventState || ev.SessionID != view.ID || ev.Session == nil {
		t.Fatalf("unexpected initial event: %+v", ev)
	}

	if err := conn.WriteJSON(clientMessage{Type: "ping"}); err != nil {
		t.Fatalf("write ping: %v", err)
	}
	if err := conn.WriteJSON(clientMessage{Type: "user_message", Content: "hello"}); err != nil {
		t.Fatalf("write user message: %v", err)
	}

	var sawUserMessage, sawAssistantMessage, sawText, sawDone bool
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var next session.Event
		if err := conn.ReadJSON(&next); err != nil {
			t.Fatalf("read event: %v", err)
		}
		switch next.Type {
		case session.EventMessage:
			if next.Message != nil && next.Message.Role == model.MessageRoleUser {
				sawUserMessage = true
			}
			if next.Message != nil && next.Message.Role == model.MessageRoleAssistant {
				sawAssistantMessage = true
			}
		case session.EventChunk:
			if next.Chunk != nil && next.Chunk.Type == agent.ChunkTypeText && next.Chunk.Content == "ws answer: hello" {
				sawText = true
			}
			if next.Chunk != nil && next.Chunk.Type == agent.ChunkTypeDone {
				sawDone = true
			}
		}
		if sawUserMessage && sawAssistantMessage && sawText && sawDone {
			break
		}
	}
	if !sawUserMessage || !sawAssistantMessage || !sawText || !sawDone {
		t.Fatalf("missing websocket events: user=%v assistant=%v text=%v done=%v", sawUserMessage, sawAssistantMessage, sawText, sawDone)
	}

	if err := conn.WriteJSON(clientMessage{Type: "close"}); err != nil {
		t.Fatalf("write close: %v", err)
	}
}

func TestHandleWebSocketAuthBoundaries(t *testing.T) {
	router, manager, authSvc, _ := newWSTestHarness(t)
	ctx := context.Background()
	owner, err := authSvc.Register(ctx, "ws-owner", "pass1234")
	if err != nil {
		t.Fatalf("register owner: %v", err)
	}
	other, err := authSvc.Register(ctx, "ws-other", "pass1234")
	if err != nil {
		t.Fatalf("register other: %v", err)
	}
	view, err := manager.CreateSession(ctx, owner.User)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ws/"+view.ID, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("missing token should be unauthorized, got %d %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/ws/"+view.ID+"?token="+other.Token, nil)
	rr = httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("other user should be forbidden, got %d %s", rr.Code, rr.Body.String())
	}
}
