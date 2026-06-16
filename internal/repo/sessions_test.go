package repo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"callme/internal/model"
)

func TestListClosedSessionsFiltersAndPaginates(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	store := NewStore(db)
	ctx := context.Background()
	base := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	for i, id := range []string{"s1", "s2", "s3"} {
		created := base.Add(time.Duration(i) * time.Hour)
		closed := created.Add(10 * time.Minute)
		if err := store.CreateSession(ctx, &model.Session{
			ID:          id,
			ClientID:    "client-" + id,
			UserID:      "user-" + id,
			Status:      model.SessionStatusClosed,
			CreatedAt:   created,
			StartedAt:   &created,
			ClosedAt:    &closed,
			CloseReason: model.CloseReasonUser,
			Title:       "title-" + id,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	start := base.Add(30 * time.Minute)
	end := base.Add(3 * time.Hour)
	sessions, total, err := store.ListClosedSessions(ctx, &start, &end, "", 1, 2)
	if err != nil {
		t.Fatalf("list closed: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	if len(sessions) != 2 {
		t.Fatalf("len = %d, want 2", len(sessions))
	}
	if sessions[0].ID != "s3" || sessions[1].ID != "s2" {
		t.Fatalf("order = [%s %s], want [s3 s2]", sessions[0].ID, sessions[1].ID)
	}

	sessions, total, err = store.ListClosedSessions(ctx, &start, &end, "", 2, 1)
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if total != 2 {
		t.Fatalf("page 2 total = %d, want 2", total)
	}
	if len(sessions) != 1 || sessions[0].ID != "s2" {
		t.Fatalf("page 2 sessions = %+v, want s2", sessions)
	}

	sessions, total, err = store.ListClosedSessions(ctx, nil, nil, "user-s1", 1, 10)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if total != 1 || len(sessions) != 1 || sessions[0].ID != "s1" {
		t.Fatalf("user filtered sessions total=%d sessions=%+v, want s1", total, sessions)
	}
}
