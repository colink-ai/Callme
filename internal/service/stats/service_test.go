package stats

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"callme/internal/model"
	"callme/internal/repo"
)

func newStatsStore(t *testing.T) *repo.Store {
	t.Helper()
	db, err := repo.Open("sqlite", filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return repo.NewStore(db)
}

func seedStatsData(t *testing.T, store *repo.Store) string {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	started := now.Add(-10 * time.Minute)
	closed := now.Add(-5 * time.Minute)
	sess := &model.Session{
		ID:          "s1",
		ClientID:    "u1",
		UserID:      "u1",
		Status:      model.SessionStatusClosed,
		CreatedAt:   started,
		StartedAt:   &started,
		ClosedAt:    &closed,
		CloseReason: model.CloseReasonUser,
		Title:       "Callme 部署失败 怎么处理",
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	msgs := []*model.Message{
		{ID: "m1", SessionID: "s1", Role: model.MessageRoleUser, Content: "Callme 部署失败 怎么处理", CreatedAt: now.Add(-4 * time.Minute)},
		{ID: "m2", SessionID: "s1", Role: model.MessageRoleUser, Content: "Callme 部署失败 怎么排查", CreatedAt: now.Add(-3 * time.Minute)},
		{ID: "m3", SessionID: "s1", Role: model.MessageRoleAssistant, Content: "参考知识", ToolCalls: `[{"toolName":"mcp_code_graph_query"}]`, CreatedAt: now.Add(-2 * time.Minute)},
	}
	for _, msg := range msgs {
		if err := store.CreateMessage(ctx, msg); err != nil {
			t.Fatalf("create message %s: %v", msg.ID, err)
		}
	}
	for _, fb := range []*model.Feedback{
		{ID: "f1", SessionID: "s1", MessageID: "m3", Rating: model.FeedbackUp, CreatedAt: now.Add(-time.Minute)},
		{ID: "f2", SessionID: "s1", MessageID: "m3", Rating: model.FeedbackDown, CreatedAt: now.Add(-time.Minute)},
	} {
		if err := store.CreateFeedback(ctx, fb); err != nil {
			t.Fatalf("create feedback: %v", err)
		}
	}
	if err := store.CreateTicket(ctx, &model.Ticket{ID: "t1", SessionID: "s1", Reason: "help", Transcript: "transcript", Status: model.TicketStatusOpen, CreatedAt: now}); err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	return sess.ID
}

func TestGetOverview(t *testing.T) {
	store := newStatsStore(t)
	seedStatsData(t, store)
	svc := NewService(store, func() (int, int) { return 2, 1 })
	overview, err := svc.GetOverview(context.Background())
	if err != nil {
		t.Fatalf("GetOverview: %v", err)
	}
	if overview.ActiveSessions != 2 || overview.QueuedSessions != 1 {
		t.Fatalf("live counts not included: %+v", overview)
	}
	if overview.SessionsToday != 1 || overview.Sessions7d != 1 || overview.UserMessages7d != 2 {
		t.Fatalf("unexpected session/message counters: %+v", overview)
	}
	if overview.KnowledgeHits7d != 1 || overview.KnowledgeHitRate != 1 {
		t.Fatalf("unexpected knowledge counters: %+v", overview)
	}
	if overview.FeedbackUp7d != 1 || overview.FeedbackDown7d != 1 || overview.SatisfactionRate != 0.5 {
		t.Fatalf("unexpected feedback counters: %+v", overview)
	}
	if overview.Tickets7d != 1 || overview.HandoffRate != 1 {
		t.Fatalf("unexpected handoff counters: %+v", overview)
	}
}

func TestDailyAndHotQuestions(t *testing.T) {
	store := newStatsStore(t)
	seedStatsData(t, store)
	svc := NewService(store, func() (int, int) { return 0, 0 })
	daily, err := svc.GetDaily(context.Background(), -1)
	if err != nil {
		t.Fatalf("GetDaily: %v", err)
	}
	if len(daily) != 14 {
		t.Fatalf("default daily length = %d", len(daily))
	}
	last := daily[len(daily)-1]
	if last.Sessions != 1 || last.Up != 1 || last.Down != 1 {
		t.Fatalf("today point = %+v", last)
	}
	hot, err := svc.GetHotQuestions(context.Background(), 5)
	if err != nil {
		t.Fatalf("GetHotQuestions: %v", err)
	}
	var sawCallme bool
	for _, item := range hot {
		if item.Keyword == "callme" && item.Count == 2 {
			sawCallme = true
		}
	}
	if !sawCallme {
		t.Fatalf("unexpected hot questions: %+v", hot)
	}
}

func TestStatsEmptyAndLimits(t *testing.T) {
	store := newStatsStore(t)
	svc := NewService(store, func() (int, int) { return 0, 0 })
	overview, err := svc.GetOverview(context.Background())
	if err != nil {
		t.Fatalf("empty overview: %v", err)
	}
	if overview.KnowledgeHitRate != 0 || overview.SatisfactionRate != 0 || overview.HandoffRate != 0 {
		t.Fatalf("empty rates should be zero: %+v", overview)
	}
	daily, err := svc.GetDaily(context.Background(), 91)
	if err != nil {
		t.Fatalf("daily with large days: %v", err)
	}
	if len(daily) != 14 {
		t.Fatalf("large days should fall back to 14, got %d", len(daily))
	}
	hot, err := svc.GetHotQuestions(context.Background(), 0)
	if err != nil {
		t.Fatalf("empty hot questions: %v", err)
	}
	if len(hot) != 0 {
		t.Fatalf("empty hot questions should be empty: %+v", hot)
	}
	if got := tokenize("how to deploy callme, callme? 的 a x"); len(got) != 3 || got[0] != "deploy" || got[1] != "callme" || got[2] != "callme" {
		t.Fatalf("tokenize filtered words = %+v", got)
	}
}
