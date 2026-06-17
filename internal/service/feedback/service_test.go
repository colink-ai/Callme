package feedback

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"callme/internal/config"
	"callme/internal/model"
	"callme/internal/repo"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

func newTestService(t *testing.T) (*Service, *repo.Store, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := repo.Open("sqlite", filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store := repo.NewStore(db)
	home := filepath.Join(dir, "home")
	// 用很长的蒸馏间隔，避免后台 loop 干扰，手动调用 distillOnce
	s := &Service{store: store, cfg: config.FeedbackConfig{DistillInterval: time.Hour, AuditInterval: time.Hour, NotesMaxEntries: 100}, hermesHome: home, logger: zap.NewNop()}
	return s, store, home
}

// 点踩+纠错蒸馏后应进入候选池(pending)，且不写任何生产可读文件
func TestDistillProducesPendingCandidate(t *testing.T) {
	s, store, home := newTestService(t)
	ctx := context.Background()

	sess := &model.Session{ID: "s1", ClientID: "c1", UserID: "u1", Status: model.SessionStatusClosed, CreatedAt: time.Now()}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	store.CreateMessage(ctx, &model.Message{ID: "mu", SessionID: "s1", Role: model.MessageRoleUser, Content: "如何重置密码", CreatedAt: time.Now()})
	store.CreateMessage(ctx, &model.Message{ID: "ma", SessionID: "s1", Role: model.MessageRoleAssistant, Content: "错误答案", CreatedAt: time.Now().Add(time.Second)})
	if err := store.CreateFeedback(ctx, &model.Feedback{ID: uuid.New().String(), SessionID: "s1", MessageID: "ma", Rating: model.FeedbackDown, Correction: "在设置页点击重置密码", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("create feedback: %v", err)
	}

	if err := s.distillOnce(ctx); err != nil {
		t.Fatalf("distill: %v", err)
	}

	cands, err := store.ListCandidates(ctx, model.CandidateStatusPending, 100)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("expect 1 pending candidate, got %d", len(cands))
	}
	if cands[0].Content != "在设置页点击重置密码" || cands[0].AssetType != model.CandidateAssetFAQ {
		t.Fatalf("unexpected candidate: %+v", cands[0])
	}
	// 关键：蒸馏不得写正式知识文件（通过前不进回答链路）
	if _, err := os.Stat(filepath.Join(home, ApprovedFileName)); !os.IsNotExist(err) {
		t.Fatal("approved_knowledge.md must NOT exist before approval")
	}
}

// 审批通过才发布到正式知识；拒绝不发布
func TestReviewApprovePublishes(t *testing.T) {
	s, store, home := newTestService(t)
	ctx := context.Background()

	now := time.Now()
	c := &model.CandidateAsset{ID: "cand1", AssetType: model.CandidateAssetFAQ, Title: "重置密码", Question: "如何重置密码", Content: "去设置页重置", Status: model.CandidateStatusPending, CreatedAt: now, UpdatedAt: now}
	if err := store.CreateCandidate(ctx, c); err != nil {
		t.Fatalf("create candidate: %v", err)
	}

	if _, err := s.ReviewCandidate(ctx, "cand1", true, "admin", "ok"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	got, _ := store.GetCandidate(ctx, "cand1")
	if got.Status != model.CandidateStatusApproved {
		t.Fatalf("status = %s, want approved", got.Status)
	}
	data, err := os.ReadFile(filepath.Join(home, ApprovedFileName))
	if err != nil {
		t.Fatalf("approved file should exist after approve: %v", err)
	}
	if !contains(string(data), "重置密码") {
		t.Fatalf("approved file missing content: %s", data)
	}

	// 已审批的不能重复审批
	if _, err := s.ReviewCandidate(ctx, "cand1", false, "admin", ""); err == nil {
		t.Fatal("expect error re-reviewing approved candidate")
	}
}

func TestAuditIncludesQuarantinedHermesAssets(t *testing.T) {
	s, store, home := newTestService(t)
	ctx := context.Background()
	qdir := filepath.Join(home, "_quarantine", "legacy", "memories")
	if err := os.MkdirAll(qdir, 0o755); err != nil {
		t.Fatalf("mkdir quarantine: %v", err)
	}
	if err := os.WriteFile(filepath.Join(qdir, "MEMORY.md"), []byte("token expired 通常与 SSO 配置有关"), 0o644); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	if err := s.auditHermesLearning(ctx); err != nil {
		t.Fatalf("audit: %v", err)
	}
	assets, err := store.ListHermesLearningAssets(ctx, model.HermesLearningStatusPendingReview, 100)
	if err != nil {
		t.Fatalf("list assets: %v", err)
	}
	if len(assets) != 1 {
		t.Fatalf("assets len = %d, want 1", len(assets))
	}
	if !contains(assets[0].RiskFlags, "quarantined") || !contains(assets[0].RiskFlags, "diagnostic_claim") {
		t.Fatalf("unexpected risk flags: %s", assets[0].RiskFlags)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
