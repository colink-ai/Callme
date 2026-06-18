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
	if cands[0].Content != "在设置页点击重置密码" || cands[0].AssetType != model.CandidateAssetKnowledge {
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
	c := &model.CandidateAsset{ID: "cand1", AssetType: model.CandidateAssetKnowledge, PublishTargets: []model.KnowledgePublishTarget{model.KnowledgePublishLocal}, Title: "重置密码", Question: "如何重置密码", Content: "去设置页重置", Status: model.CandidateStatusPending, CreatedAt: now, UpdatedAt: now}
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

func TestReviewApprovePublishesSkill(t *testing.T) {
	s, store, home := newTestService(t)
	ctx := context.Background()

	now := time.Now()
	c := &model.CandidateAsset{
		ID:             "cand-skill",
		AssetType:      model.CandidateAssetKnowledge,
		PublishTargets: []model.KnowledgePublishTarget{model.KnowledgePublishSkill},
		Title:          "Agent 超时排查",
		Question:       "Agent 回答为什么中断",
		Content:        "检查 agent.prompt_timeout 和代理超时。",
		Status:         model.CandidateStatusPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := store.CreateCandidate(ctx, c); err != nil {
		t.Fatalf("create candidate: %v", err)
	}

	if _, err := s.ReviewCandidate(ctx, "cand-skill", true, "admin", "ok"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, "skills", "callme-approved", "agent-超时排查", "SKILL.md"))
	if err != nil {
		t.Fatalf("skill should exist after approve: %v", err)
	}
	if !contains(string(data), "检查 agent.prompt_timeout") {
		t.Fatalf("skill missing content: %s", data)
	}
}

func TestAuditIgnoresQuarantineAndSkillMetadata(t *testing.T) {
	s, store, home := newTestService(t)
	ctx := context.Background()

	mustWriteFeedbackTest(t, filepath.Join(home, "skills", ".usage.json"), "{}")
	mustWriteFeedbackTest(t, filepath.Join(home, "skills", "support", "callme", "SKILL.md"), "# Callme Skill")
	mustWriteFeedbackTest(t, filepath.Join(home, "skills", "support", "callme", "references", "notes.md"), "reference")
	mustWriteFeedbackTest(t, filepath.Join(home, "_quarantine", "legacy", "memories", "MEMORY.md"), "token expired 通常与 SSO 配置有关")

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
	if assets[0].AssetType != model.HermesLearningAssetSkill || filepath.Base(assets[0].Path) != "SKILL.md" {
		t.Fatalf("unexpected asset: %+v", assets[0])
	}
}

func TestAuditPreservesHermesAssetFormatting(t *testing.T) {
	s, store, home := newTestService(t)
	ctx := context.Background()
	body := "---\nname: demo\n---\n\n# Demo Skill\n\n- step one\n- step two\n"
	mustWriteFeedbackTest(t, filepath.Join(home, "skills", "demo", "SKILL.md"), body)

	if err := s.auditHermesLearning(ctx); err != nil {
		t.Fatalf("audit: %v", err)
	}
	raw, err := store.LatestHermesLearningAssetByPath(ctx, filepath.Join(home, "skills", "demo", "SKILL.md"))
	if err != nil {
		t.Fatalf("latest asset: %v", err)
	}
	if raw.Content != "" {
		t.Fatalf("audit record should not persist file content, got %q", raw.Content)
	}
	assets, err := store.ListHermesLearningAssets(ctx, model.HermesLearningStatusPendingReview, 100)
	if err != nil {
		t.Fatalf("list assets: %v", err)
	}
	if len(assets) != 1 {
		t.Fatalf("assets len = %d, want 1", len(assets))
	}
	if assets[0].Content != body {
		t.Fatalf("content formatting lost:\n%s", assets[0].Content)
	}
}

func TestReviewHermesLearningAssetDeletesFile(t *testing.T) {
	s, store, home := newTestService(t)
	ctx := context.Background()
	path := filepath.Join(home, "skills", "demo", "SKILL.md")
	mustWriteFeedbackTest(t, path, "# Demo Skill")

	if err := s.auditHermesLearning(ctx); err != nil {
		t.Fatalf("audit: %v", err)
	}
	assets, err := store.ListHermesLearningAssets(ctx, model.HermesLearningStatusPendingReview, 100)
	if err != nil || len(assets) != 1 {
		t.Fatalf("list assets: len=%d err=%v", len(assets), err)
	}
	reviewed, err := s.ReviewHermesLearningAsset(ctx, assets[0].ID, ReviewHermesLearningRequest{Action: "delete"}, "admin")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if reviewed.Status != model.HermesLearningStatusDeleted {
		t.Fatalf("status = %s, want deleted", reviewed.Status)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file should be removed, stat err=%v", err)
	}
}

func TestReviewHermesLearningAssetModifiesFile(t *testing.T) {
	s, store, home := newTestService(t)
	ctx := context.Background()
	path := filepath.Join(home, "skills", "demo", "SKILL.md")
	mustWriteFeedbackTest(t, path, "# Demo Skill\n\nold content\n")

	if err := s.auditHermesLearning(ctx); err != nil {
		t.Fatalf("audit: %v", err)
	}
	assets, err := store.ListHermesLearningAssets(ctx, model.HermesLearningStatusPendingReview, 100)
	if err != nil || len(assets) != 1 {
		t.Fatalf("list assets: len=%d err=%v", len(assets), err)
	}
	reviewed, err := s.ReviewHermesLearningAsset(ctx, assets[0].ID, ReviewHermesLearningRequest{
		Action:  "modify",
		Content: "# Demo Skill\n\nnew content",
	}, "admin")
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if reviewed.Status != model.HermesLearningStatusModified {
		t.Fatalf("status = %s, want modified", reviewed.Status)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read modified file: %v", err)
	}
	if string(data) != "# Demo Skill\n\nnew content\n" {
		t.Fatalf("unexpected file content: %q", string(data))
	}
}

func mustWriteFeedbackTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
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
