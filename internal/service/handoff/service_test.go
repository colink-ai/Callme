package handoff

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"callme/internal/config"
	"callme/internal/model"
	"callme/internal/repo"

	"go.uber.org/zap"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func newHandoffStore(t *testing.T) *repo.Store {
	t.Helper()
	db, err := repo.Open("sqlite", filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return repo.NewStore(db)
}

func seedHandoffSession(t *testing.T, store *repo.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	started := now.Add(-2 * time.Minute)
	sess := &model.Session{ID: "s1", ClientID: "client", UserID: "u1", Status: model.SessionStatusActive, CreatedAt: started, StartedAt: &started}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	for _, msg := range []*model.Message{
		{ID: "m1", SessionID: "s1", Role: model.MessageRoleUser, Content: "需要人工帮助", CreatedAt: started},
		{ID: "m2", SessionID: "s1", Role: model.MessageRoleAssistant, Content: "建议转人工", CreatedAt: started.Add(time.Second)},
	} {
		if err := store.CreateMessage(ctx, msg); err != nil {
			t.Fatalf("create message: %v", err)
		}
	}
}

func TestCreateTicketLocalOnly(t *testing.T) {
	store := newHandoffStore(t)
	seedHandoffSession(t, store)
	svc := NewService(store, config.HandoffConfig{}, zap.NewNop())
	ticket, err := svc.CreateTicket(context.Background(), "s1", "  需要专家  ")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if ticket.Reason != "需要专家" || ticket.Status != model.TicketStatusOpen {
		t.Fatalf("unexpected ticket: %+v", ticket)
	}
	if !strings.Contains(ticket.Transcript, "用户") || !strings.Contains(ticket.Transcript, "AI客服") {
		t.Fatalf("transcript should include roles, got %q", ticket.Transcript)
	}
	list, err := svc.ListTickets(context.Background(), 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListTickets len=%d err=%v", len(list), err)
	}
}

func TestNotifyWebhookMarksStatus(t *testing.T) {
	store := newHandoffStore(t)
	seedHandoffSession(t, store)
	var mu sync.Mutex
	var sawHeader string
	svc := NewService(store, config.HandoffConfig{
		WebhookURL:     "http://callme-webhook.test/ticket",
		WebhookHeaders: map[string]string{"X-Test": "ok"},
	}, zap.NewNop())
	svc.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		sawHeader = req.Header.Get("X-Test")
		mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    req,
		}, nil
	})}
	ticket, err := svc.CreateTicket(context.Background(), "s1", "webhook")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		tickets, _ := store.ListTickets(context.Background(), 10)
		if len(tickets) == 1 && tickets[0].Status == model.TicketStatusNotified {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ticket %s was not marked notified, tickets=%+v", ticket.ID, tickets)
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if sawHeader != "ok" {
		t.Fatalf("webhook header not sent: %q", sawHeader)
	}
}

func TestCreateTicketMissingSession(t *testing.T) {
	svc := NewService(newHandoffStore(t), config.HandoffConfig{}, zap.NewNop())
	if _, err := svc.CreateTicket(context.Background(), "missing", "reason"); err == nil {
		t.Fatal("missing session should fail")
	}
}

func TestNotifyWebhookFailureStatuses(t *testing.T) {
	for name, transport := range map[string]http.RoundTripper{
		"network": roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("network down")
		}),
		"status": roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader(`bad`)),
				Request:    req,
			}, nil
		}),
	} {
		t.Run(name, func(t *testing.T) {
			store := newHandoffStore(t)
			seedHandoffSession(t, store)
			svc := NewService(store, config.HandoffConfig{WebhookURL: "http://callme-webhook.test/ticket"}, zap.NewNop())
			svc.client = &http.Client{Transport: transport}
			ticket, err := svc.CreateTicket(context.Background(), "s1", "webhook")
			if err != nil {
				t.Fatalf("CreateTicket: %v", err)
			}
			deadline := time.Now().Add(time.Second)
			for {
				tickets, _ := store.ListTickets(context.Background(), 10)
				if len(tickets) == 1 && tickets[0].Status == model.TicketStatusFailed {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("ticket %s was not marked failed, tickets=%+v", ticket.ID, tickets)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestListTicketsDefaultLimitAndTranscriptRoles(t *testing.T) {
	store := newHandoffStore(t)
	ctx := context.Background()
	now := time.Now()
	sess := &model.Session{ID: "s-system", ClientID: "client", UserID: "u1", Status: model.SessionStatusActive, CreatedAt: now}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := store.CreateMessage(ctx, &model.Message{ID: "sys", SessionID: sess.ID, Role: model.MessageRoleSystem, Content: "system note", CreatedAt: now}); err != nil {
		t.Fatalf("create system message: %v", err)
	}
	svc := NewService(store, config.HandoffConfig{}, zap.NewNop())
	ticket, err := svc.CreateTicket(ctx, sess.ID, "")
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	if !strings.Contains(ticket.Transcript, "用户") || strings.Contains(ticket.Transcript, "AI客服") {
		t.Fatalf("system/other roles should be labeled as user by default: %q", ticket.Transcript)
	}
	tickets, err := svc.ListTickets(ctx, -1)
	if err != nil || len(tickets) != 1 {
		t.Fatalf("default list tickets len=%d err=%v", len(tickets), err)
	}
}
