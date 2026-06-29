package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"callme/internal/api"
	"callme/internal/config"
	"callme/internal/model"
	"callme/internal/repo"
	"callme/internal/service/agent"
	"callme/internal/service/auth"
	"callme/internal/service/feedback"
	"callme/internal/service/handoff"
	runtimeSvc "callme/internal/service/runtime"
	"callme/internal/service/session"
	"callme/internal/service/settings"
	"callme/internal/service/stats"
	"callme/internal/ws"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type apiFakeAdapter struct {
	mu       sync.Mutex
	agentIDs map[string]string
}

func (a *apiFakeAdapter) StartSession(ctx context.Context, sessionID string, req *agent.SessionRequest) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.agentIDs[sessionID] = "api-fake-" + sessionID
	return nil
}

func (a *apiFakeAdapter) Prompt(ctx context.Context, sessionID string, input string, images []agent.ImageContent, onChunk func(agent.Chunk)) error {
	onChunk(agent.Chunk{Type: agent.ChunkTypeText, Content: "mock answer: " + input})
	return nil
}

func (a *apiFakeAdapter) StopSession(sessionID string) error { return nil }

func (a *apiFakeAdapter) GetSessionStatus(sessionID string) agent.SessionStatus {
	return agent.SessionStatusRunning
}

func (a *apiFakeAdapter) CheckHealth(ctx context.Context, spec agent.AgentSpec) error { return nil }

func (a *apiFakeAdapter) AgentSessionID(sessionID string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.agentIDs[sessionID]
}

type apiFakeRuntime struct{}

func (apiFakeRuntime) Types() []runtimeSvc.AgentType {
	return []runtimeSvc.AgentType{{
		Type:        runtimeSvc.TypeHermes,
		Name:        "Hermes",
		Description: "test runtime",
		DefaultPath: "hermes",
	}}
}

func (apiFakeRuntime) CheckHealth(context.Context, agent.AgentSpec) error {
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type apiHarness struct {
	t      *testing.T
	router http.Handler
	store  *repo.Store
	db     interface{ Close() error }
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	originalTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "callme-ai.test" && req.URL.Path == "/chat/completions" {
			body := `{"choices":[{"message":{"content":"{\"title\":\"测试知识\",\"question\":\"如何验证知识沉淀？\",\"content\":\"## 处理步骤\\n\\n1. 使用候选知识流程。\\n2. 审批后发布正式知识。\",\"confidence\":0.9,\"reason\":\"集成测试\"}"}}]}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Status:     "200 OK",
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		}
		return originalTransport.RoundTrip(req)
	})
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	db, err := repo.Open("sqlite", filepath.Join(dir, "callme-test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	store := repo.NewStore(db)

	sessionCfg := config.SessionConfig{
		MaxActive:        5,
		MaxQueue:         5,
		IdleWarnAfter:    time.Minute,
		IdleCloseAfter:   2 * time.Minute,
		MaxDuration:      time.Hour,
		MaxPerClient:     5,
		QueuePollSeconds: 1,
	}
	agentCfg := config.AgentConfig{
		Type:         "api_fake",
		CliPath:      "api-fake",
		DefaultModel: "test-model",
		APIURL:       "http://callme-ai.test",
		APIToken:     "test-token",
		HermesHome:   filepath.Join(dir, "hermes-home"),
		WorkDir:      filepath.Join(dir, "workdir"),
	}

	logger := zap.NewNop()
	settingsSvc := settings.NewService(store, agentCfg, sessionCfg, logger)
	sessionMgr := session.NewManager(sessionCfg, agentCfg, store, settingsSvc, apiFakeAdapterFactory, func(domainID string) []agent.MCPServerSpec { return nil }, logger)
	authSvc := auth.NewService(store, time.Hour)
	feedbackSvc := feedback.NewService(store, config.FeedbackConfig{
		DistillInterval: time.Hour,
		AuditInterval:   time.Hour,
		NotesMaxEntries: 100,
	}, agentCfg.HermesHome, settingsSvc.AgentSpec, logger)
	handoffSvc := handoff.NewService(store, config.HandoffConfig{}, logger)
	statsSvc := stats.NewService(store, sessionMgr.Counts)
	wsHandler := ws.NewHandler(sessionMgr, authSvc, store, logger)

	t.Cleanup(func() {
		feedbackSvc.Shutdown()
		sessionMgr.Shutdown()
		_ = db.Close()
	})

	router := api.NewRouter(&api.Deps{
		Store:    store,
		Sessions: sessionMgr,
		Settings: settingsSvc,
		Runtime:  apiFakeRuntime{},
		Auth:     authSvc,
		Feedback: feedbackSvc,
		Handoff:  handoffSvc,
		Stats:    statsSvc,
		WS:       wsHandler,
		Logger:   logger,
		Version:  "test",
	})
	return &apiHarness{t: t, router: router, store: store, db: db}
}

func apiFakeAdapterFactory(agent.AgentSpec) (agent.Adapter, error) {
	return &apiFakeAdapter{agentIDs: map[string]string{}}, nil
}

func (h *apiHarness) do(method, path, token, activeRole string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if activeRole != "" {
		req.Header.Set("X-Callme-Active-Role", activeRole)
	}
	rr := httptest.NewRecorder()
	h.router.ServeHTTP(rr, req)
	return rr
}

func (h *apiHarness) register(username string) (string, model.User) {
	h.t.Helper()
	rr := h.do(http.MethodPost, "/api/v1/auth/register", "", "", map[string]string{
		"username": username,
		"password": "pass1234",
	})
	if rr.Code != http.StatusOK {
		h.t.Fatalf("register %s status=%d body=%s", username, rr.Code, rr.Body.String())
	}
	var resp struct {
		Token string     `json:"token"`
		User  model.User `json:"user"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		h.t.Fatalf("decode register response: %v", err)
	}
	if resp.Token == "" || resp.User.ID == "" {
		h.t.Fatalf("invalid register response: %+v", resp)
	}
	return resp.Token, resp.User
}

func decodeJSON[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode json status=%d body=%s: %v", rr.Code, rr.Body.String(), err)
	}
	return out
}

func TestAuthAndKnowledgeRoleBoundaries(t *testing.T) {
	h := newAPIHarness(t)

	adminToken, admin := h.register("admin")
	if admin.Role != model.UserRoleAdmin || !admin.HasRole(model.UserRoleAdmin) {
		t.Fatalf("first registered user should be admin, got role=%s roles=%v", admin.Role, admin.Roles)
	}
	staffToken, staff := h.register("staff")
	expertToken, expert := h.register("expert")

	rr := h.do(http.MethodGet, "/api/v1/learning/candidates", staffToken, string(model.UserRoleNormal), nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("normal user should not access curation candidates, got %d %s", rr.Code, rr.Body.String())
	}

	rr = h.do(http.MethodPut, "/api/v1/users/"+staff.ID+"/role", adminToken, string(model.UserRoleAdmin), map[string]any{
		"roles":       []model.UserRole{model.UserRoleNormal, model.UserRoleKnowledgeStaff},
		"maxSessions": 2,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("update staff roles status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodPut, "/api/v1/users/"+expert.ID+"/role", adminToken, string(model.UserRoleAdmin), map[string]any{
		"roles": []model.UserRole{model.UserRoleNormal, model.UserRoleKnowledgeExpert},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("update expert roles status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = h.do(http.MethodPost, "/api/v1/learning/manual-drafts", staffToken, string(model.UserRoleKnowledgeStaff), map[string]any{
		"description":    "知识沉淀需要先生成候选，再由知识专家审批。",
		"publishTargets": []model.KnowledgePublishTarget{model.KnowledgePublishLocal},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("knowledge staff should create manual draft, got %d %s", rr.Code, rr.Body.String())
	}
	candidate := decodeJSON[model.CandidateAsset](t, rr)
	if candidate.AssetType != model.CandidateAssetKnowledge || candidate.Status != model.CandidateStatusPending {
		t.Fatalf("unexpected candidate: type=%s status=%s", candidate.AssetType, candidate.Status)
	}

	rr = h.do(http.MethodPost, "/api/v1/learning/candidates/"+candidate.ID+"/review", staffToken, string(model.UserRoleKnowledgeStaff), map[string]any{
		"approve": true,
	})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("knowledge staff should not approve candidates, got %d %s", rr.Code, rr.Body.String())
	}

	rr = h.do(http.MethodPost, "/api/v1/learning/candidates/"+candidate.ID+"/review", expertToken, string(model.UserRoleKnowledgeExpert), map[string]any{
		"approve": true,
		"note":    "测试通过",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("knowledge expert should approve candidates, got %d %s", rr.Code, rr.Body.String())
	}
	approved := decodeJSON[model.CandidateAsset](t, rr)
	if approved.Status != model.CandidateStatusApproved || approved.Reviewer != "expert" {
		t.Fatalf("unexpected approved candidate: status=%s reviewer=%s", approved.Status, approved.Reviewer)
	}

	rr = h.do(http.MethodGet, "/api/v1/learning/notes", expertToken, string(model.UserRoleKnowledgeExpert), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("expert should read approved notes, got %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "测试知识") {
		t.Fatalf("approved notes should contain published candidate, body=%s", rr.Body.String())
	}
}

func TestSessionAPIReportsUserConcurrencyLimit(t *testing.T) {
	h := newAPIHarness(t)
	_, _ = h.register("admin")
	userToken, _ := h.register("solo")

	rr := h.do(http.MethodPost, "/api/v1/sessions", userToken, "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("create first session status=%d body=%s", rr.Code, rr.Body.String())
	}
	var first struct {
		ID     string              `json:"id"`
		Status model.SessionStatus `json:"status"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first session: %v", err)
	}
	if first.ID == "" || first.Status != model.SessionStatusActive {
		t.Fatalf("first session should be active, got %+v", first)
	}

	rr = h.do(http.MethodPost, "/api/v1/sessions", userToken, "", nil)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second session should hit user limit, got %d %s", rr.Code, rr.Body.String())
	}
	var limitResp struct {
		Code            string `json:"code"`
		MaxSessions     int    `json:"maxSessions"`
		CurrentSessions int    `json:"currentSessions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &limitResp); err != nil {
		t.Fatalf("decode limit response: %v", err)
	}
	if limitResp.Code != "user_concurrency_limit" || limitResp.MaxSessions != 1 || limitResp.CurrentSessions != 1 {
		t.Fatalf("unexpected limit response: %+v", limitResp)
	}
}

func TestSessionSettingsStatsAndWSRoutes(t *testing.T) {
	h := newAPIHarness(t)
	adminToken, _ := h.register("admin-routes")
	userToken, user := h.register("route-user")
	otherToken, _ := h.register("route-other")

	rr := h.do(http.MethodGet, "/api/v1/sessions/current", userToken, string(model.UserRoleAdmin), nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("user should not activate admin role, got %d %s", rr.Code, rr.Body.String())
	}

	rr = h.do(http.MethodPost, "/api/v1/auth/login", "", "", map[string]string{"username": "route-user", "password": "pass1234"})
	if rr.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodGet, "/api/v1/auth/me", userToken, "", nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"version":"test"`) {
		t.Fatalf("me status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = h.do(http.MethodGet, "/api/v1/settings/agent", adminToken, string(model.UserRoleAdmin), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get agent settings status=%d body=%s", rr.Code, rr.Body.String())
	}
	var agentProfiles model.AgentProfilesSettings
	if err := json.Unmarshal(rr.Body.Bytes(), &agentProfiles); err != nil {
		t.Fatalf("decode agent profiles: %v", err)
	}
	if agentProfiles.ActiveProfileID == "" || len(agentProfiles.Profiles) == 0 {
		t.Fatalf("invalid agent profiles response: %+v", agentProfiles)
	}
	rr = h.do(http.MethodPut, "/api/v1/settings/pool", adminToken, string(model.UserRoleAdmin), map[string]any{
		"maxActive": 3,
		"maxQueue":  7,
	})
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"maxActive":3`) {
		t.Fatalf("update pool status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodGet, "/api/v1/agent/types", adminToken, string(model.UserRoleAdmin), nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "hermes") {
		t.Fatalf("agent types status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodPost, "/api/v1/agent/health", adminToken, string(model.UserRoleAdmin), nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"healthy":true`) {
		t.Fatalf("agent health status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = h.do(http.MethodPost, "/api/v1/sessions", userToken, "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("create session status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created model.Session
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created session: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("session id missing: %+v", created)
	}

	rr = h.do(http.MethodGet, "/api/v1/sessions/current", userToken, "", nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), created.ID) {
		t.Fatalf("current session status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodGet, "/api/v1/sessions/history?limit=5", userToken, "", nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), created.ID) {
		t.Fatalf("history status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodGet, "/api/v1/sessions/"+created.ID, otherToken, "", nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("other user should not access session, got %d %s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodGet, "/api/v1/sessions/"+created.ID+"/messages", userToken, "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list messages status=%d body=%s", rr.Code, rr.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/ws/"+created.ID, nil)
	wsRR := httptest.NewRecorder()
	h.router.ServeHTTP(wsRR, req)
	if wsRR.Code != http.StatusUnauthorized {
		t.Fatalf("ws without token should be unauthorized, got %d %s", wsRR.Code, wsRR.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/ws/"+created.ID+"?token="+otherToken, nil)
	wsRR = httptest.NewRecorder()
	h.router.ServeHTTP(wsRR, req)
	if wsRR.Code != http.StatusForbidden {
		t.Fatalf("ws by other user should be forbidden, got %d %s", wsRR.Code, wsRR.Body.String())
	}

	rr = h.do(http.MethodGet, "/api/v1/sessions?include=closed", adminToken, string(model.UserRoleAdmin), nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "active") {
		t.Fatalf("live sessions status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodDelete, "/api/v1/sessions/"+created.ID+"?by=admin", userToken, "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("close session status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodPost, "/api/v1/sessions/"+created.ID+"/continue", userToken, "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("continue closed session status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodDelete, "/api/v1/sessions/"+created.ID, userToken, "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("close continued session status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodGet, "/api/v1/admin/sessions/closed?page=1&pageSize=5&userId="+user.ID, adminToken, string(model.UserRoleAdmin), nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), created.ID) || !strings.Contains(rr.Body.String(), "route-user") {
		t.Fatalf("closed sessions status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodGet, "/api/v1/admin/sessions/closed?start=bad", adminToken, string(model.UserRoleAdmin), nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad time should be rejected, got %d %s", rr.Code, rr.Body.String())
	}

	rr = h.do(http.MethodGet, "/api/v1/stats/overview", adminToken, string(model.UserRoleAdmin), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("stats overview status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodGet, "/api/v1/stats/daily?days=3", adminToken, string(model.UserRoleAdmin), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("stats daily status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodGet, "/api/v1/stats/hot-questions?limit=3", adminToken, string(model.UserRoleAdmin), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("hot questions status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = h.do(http.MethodPost, "/api/v1/auth/logout", userToken, "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("logout status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestKnowledgeFeedbackHandoffAndUserRoutes(t *testing.T) {
	h := newAPIHarness(t)
	adminToken, admin := h.register("admin-ops")
	userToken, user := h.register("ops-user")
	expertToken, expert := h.register("ops-expert")

	rr := h.do(http.MethodPut, "/api/v1/users/"+expert.ID+"/role", adminToken, string(model.UserRoleAdmin), map[string]any{
		"roles": []model.UserRole{model.UserRoleNormal, model.UserRoleKnowledgeExpert},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("grant expert role status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodGet, "/api/v1/users", adminToken, string(model.UserRoleAdmin), nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "ops-user") {
		t.Fatalf("list users status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = h.do(http.MethodPost, "/api/v1/sessions", userToken, "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("create session status=%d body=%s", rr.Code, rr.Body.String())
	}
	var sess model.Session
	if err := json.Unmarshal(rr.Body.Bytes(), &sess); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	userMsg := &model.Message{ID: "api-user-msg", SessionID: sess.ID, Role: model.MessageRoleUser, Content: "如何转人工", CreatedAt: time.Now()}
	assistantMsg := &model.Message{ID: "api-assistant-msg", SessionID: sess.ID, Role: model.MessageRoleAssistant, Content: "请点击转人工", CreatedAt: time.Now().Add(time.Second)}
	if err := h.store.CreateMessage(context.Background(), userMsg); err != nil {
		t.Fatalf("create user message: %v", err)
	}
	if err := h.store.CreateMessage(context.Background(), assistantMsg); err != nil {
		t.Fatalf("create assistant message: %v", err)
	}

	rr = h.do(http.MethodPost, "/api/v1/feedback", userToken, "", map[string]any{
		"sessionId":  sess.ID,
		"messageId":  assistantMsg.ID,
		"rating":     model.FeedbackDown,
		"correction": "应说明转人工入口",
	})
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "应说明转人工入口") {
		t.Fatalf("submit feedback status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodPost, "/api/v1/sessions/"+sess.ID+"/handoff", userToken, "", map[string]string{"reason": "需要人工协助"})
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "需要人工协助") {
		t.Fatalf("create handoff status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodGet, "/api/v1/tickets?limit=10", adminToken, string(model.UserRoleAdmin), nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "需要人工协助") {
		t.Fatalf("list tickets status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = h.do(http.MethodPost, "/api/v1/learning/manual-drafts/stream", expertToken, string(model.UserRoleKnowledgeExpert), map[string]any{
		"description": "整理一条流式知识",
	})
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "done") {
		t.Fatalf("manual draft stream status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodGet, "/api/v1/learning/candidates?status=pending", expertToken, string(model.UserRoleKnowledgeExpert), nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "测试知识") {
		t.Fatalf("list candidates status=%d body=%s", rr.Code, rr.Body.String())
	}
	var listResp struct {
		Candidates []model.CandidateAsset `json:"candidates"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode candidates: %v", err)
	}
	if len(listResp.Candidates) == 0 {
		t.Fatal("expected at least one candidate")
	}
	candidate := listResp.Candidates[0]
	candidate.Title = "更新后的候选知识"
	candidate.Content = "更新后的内容"
	rr = h.do(http.MethodPut, "/api/v1/learning/candidates/"+candidate.ID, expertToken, string(model.UserRoleKnowledgeExpert), candidate)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "更新后的内容") {
		t.Fatalf("update candidate status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = h.do(http.MethodPost, "/api/v1/learning/jobs/run", expertToken, string(model.UserRoleKnowledgeExpert), nil)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("run learning job status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodGet, "/api/v1/learning/jobs", expertToken, string(model.UserRoleKnowledgeExpert), nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "history") {
		t.Fatalf("list learning jobs status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodGet, "/api/v1/learning/hermes-assets", expertToken, string(model.UserRoleKnowledgeExpert), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list hermes assets status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodPost, "/api/v1/learning/hermes-assets/missing/review", expertToken, string(model.UserRoleKnowledgeExpert), map[string]string{"action": "keep"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("review missing hermes asset should fail, got %d %s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodPost, "/api/v1/learning/hermes-assets/missing/assist-edit", expertToken, string(model.UserRoleKnowledgeExpert), map[string]string{"instruction": "改写"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("assist missing hermes asset should fail, got %d %s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodPost, "/api/v1/learning/hermes-assets/missing/assist-edit/stream", expertToken, string(model.UserRoleKnowledgeExpert), map[string]string{"instruction": "改写"})
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "error") {
		t.Fatalf("stream assist missing hermes asset should emit error, got %d %s", rr.Code, rr.Body.String())
	}

	rr = h.do(http.MethodGet, "/api/v1/settings/pool", adminToken, string(model.UserRoleAdmin), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get pool status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodPut, "/api/v1/settings/agent", adminToken, string(model.UserRoleAdmin), map[string]any{
		"type":         "api_fake",
		"cliPath":      "api-fake",
		"defaultModel": "updated-model",
		"apiUrl":       "http://callme-ai.test",
		"apiToken":     "updated-token",
	})
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "updated-model") {
		t.Fatalf("update agent settings status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"profiles"`) || !strings.Contains(rr.Body.String(), `"activeProfileId"`) {
		t.Fatalf("update agent settings should return profiles, body=%s", rr.Body.String())
	}

	rr = h.do(http.MethodDelete, "/api/v1/users/"+admin.ID, adminToken, string(model.UserRoleAdmin), nil)
	if rr.Code != http.StatusConflict {
		t.Fatalf("admin should not delete self, got %d %s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodDelete, "/api/v1/users/"+user.ID, adminToken, string(model.UserRoleAdmin), nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete user status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = h.do(http.MethodPost, "/api/v1/auth/register", "", "", map[string]string{"username": "", "password": ""})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad register should fail, got %d %s", rr.Code, rr.Body.String())
	}
	rr = h.do(http.MethodPost, "/api/v1/auth/login", "", "", map[string]string{"username": "ops-user", "password": "wrong"})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("bad login should fail, got %d %s", rr.Code, rr.Body.String())
	}
}
