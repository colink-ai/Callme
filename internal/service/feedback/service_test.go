package feedback

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"callme/internal/config"
	"callme/internal/model"
	"callme/internal/repo"
	"callme/internal/service/agent"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

type feedbackRoundTripFunc func(*http.Request) (*http.Response, error)

func (f feedbackRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

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

func TestManualDraftAndAIHelpers(t *testing.T) {
	s, _, home := newTestService(t)
	ctx := context.Background()
	spec := agent.AgentSpec{DefaultModel: "test-model", APIURL: "http://callme-ai.test", APIToken: "token"}
	s.agentSpec = func() agent.AgentSpec { return spec }

	restore := mockFeedbackAI(t, func(req *http.Request, body string) string {
		if strings.Contains(body, `"stream":true`) {
			return "data: {\"choices\":[{\"delta\":{\"content\":\"{\\\"title\\\":\\\"流式知识\\\",\\\"question\\\":\\\"Q\\\",\\\"content\\\":\\\"内容\\\",\\\"confidence\\\":0.7,\\\"reason\\\":\\\"ok\\\"}\"}}]}\n\ndata: [DONE]\n\n"
		}
		return `{"choices":[{"message":{"content":"{\"title\":\"人工知识\",\"question\":\"如何录入\",\"content\":\"## 步骤\\n\\n整理后审批。\",\"confidence\":0.8,\"reason\":\"测试\"}"}}]}`
	})
	defer restore()

	cand, err := s.CreateManualDraft(ctx, ManualDraftRequest{
		Description:    "人工描述",
		PublishTargets: []model.KnowledgePublishTarget{model.KnowledgePublishLocal},
		Images:         []model.ImageContent{{MimeType: "image/png", Data: "abcd"}},
	})
	if err != nil {
		t.Fatalf("CreateManualDraft: %v", err)
	}
	if cand.Title != "人工知识" || cand.Status != model.CandidateStatusPending || cand.PublishTargets[0] != model.KnowledgePublishLocal {
		t.Fatalf("unexpected candidate: %+v", cand)
	}

	var events []ManualDraftStreamEvent
	if err := s.CreateManualDraftStream(ctx, ManualDraftRequest{Description: "流式描述"}, func(ev ManualDraftStreamEvent) error {
		events = append(events, ev)
		return nil
	}); err != nil {
		t.Fatalf("CreateManualDraftStream: %v", err)
	}
	if len(events) < 2 || events[len(events)-1].Type != "done" || events[len(events)-1].Candidate == nil {
		t.Fatalf("unexpected stream events: %+v", events)
	}
	if cands, err := s.ListCandidates(ctx, model.CandidateStatusPending); err != nil || len(cands) != 2 {
		t.Fatalf("ListCandidates len=%d err=%v", len(cands), err)
	}

	cand.Content = "更新内容"
	if updated, err := s.UpdateCandidate(ctx, cand.ID, cand); err != nil || updated.Content != "更新内容" {
		t.Fatalf("UpdateCandidate = %+v err=%v", updated, err)
	}
	if s.ApprovedPath() != filepath.Join(home, ApprovedFileName) {
		t.Fatalf("ApprovedPath = %s", s.ApprovedPath())
	}
	if s.ReadApproved() != "" {
		t.Fatal("ReadApproved should be empty before approval")
	}
}

func TestAILearningAndHermesEditHelpers(t *testing.T) {
	ctx := context.Background()
	spec := agent.AgentSpec{DefaultModel: "test-model", APIURL: "http://callme-ai.test", APIToken: "token"}
	restore := mockFeedbackAI(t, func(req *http.Request, body string) string {
		if strings.Contains(body, "客服知识沉淀助手") {
			return "{\"choices\":[{\"message\":{\"content\":\"```json\\n{\\\"candidates\\\":[{\\\"title\\\":\\\"A\\\",\\\"question\\\":\\\"Q\\\",\\\"content\\\":\\\"C\\\",\\\"confidence\\\":0,\\\"reason\\\":\\\"R\\\"},{\\\"title\\\":\\\"B\\\",\\\"question\\\":\\\"Q\\\",\\\"content\\\":\\\"C\\\",\\\"confidence\\\":2,\\\"reason\\\":\\\"R\\\"}]}\\n```\"}}]}"
		}
		if strings.Contains(body, `"stream":true`) {
			return "data: {\"choices\":[{\"delta\":{\"content\":\"```markdown\\n# New\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\" Skill\\n```\"}}]}\n\ndata: [DONE]\n\n"
		}
		return "{\"choices\":[{\"message\":{\"content\":\"```markdown\\n# Revised Skill\\n```\"}}]}"
	})
	defer restore()

	learned, err := runAILearning(ctx, spec, "user: question\nassistant: answer")
	if err != nil {
		t.Fatalf("runAILearning: %v", err)
	}
	if len(learned) != 2 || learned[0].Confidence != 0.5 || learned[1].Confidence != 1 {
		t.Fatalf("normalized learning candidates = %+v", learned)
	}
	revised, err := runAIHermesLearningEdit(ctx, spec, model.HermesLearningAssetSkill, "# Old", "改好")
	if err != nil || revised != "# Revised Skill" {
		t.Fatalf("runAIHermesLearningEdit = %q err=%v", revised, err)
	}
	var streamed strings.Builder
	out, err := runAIHermesLearningEditStream(ctx, spec, model.HermesLearningAssetSkill, "# Old", "改好", func(delta string) error {
		streamed.WriteString(delta)
		return nil
	})
	if err != nil || out != "# New Skill" || !strings.Contains(streamed.String(), "# New") {
		t.Fatalf("runAIHermesLearningEditStream out=%q streamed=%q err=%v", out, streamed.String(), err)
	}
}

func TestParseHelpersAndValidationErrors(t *testing.T) {
	if delta := parseOpenAIStreamDelta(`{"choices":[{"message":{"content":[{"type":"text","text":"hello"}]}}]}`); delta != "hello" {
		t.Fatalf("array content delta = %q", delta)
	}
	if delta := parseOpenAIStreamDelta(`bad`); delta != "" {
		t.Fatalf("bad payload delta = %q", delta)
	}
	if _, err := parseAIManualDraftContent("not-json"); err == nil {
		t.Fatal("invalid manual draft json should fail")
	}
	if got := aiManualDraftDisplayContent(&aiLearningCandidate{Title: "T", Content: "C"}); !strings.Contains(got, `"title": "T"`) {
		t.Fatalf("display content = %s", got)
	}
	if aiManualDraftDisplayContent(nil) != "" {
		t.Fatal("nil draft display should be empty")
	}
	if firstString(nil) != "" || firstString([]string{"a", "b"}) != "a" {
		t.Fatal("firstString returned unexpected value")
	}

	s, _, _ := newTestService(t)
	if _, err := s.CreateManualDraft(context.Background(), ManualDraftRequest{}); err == nil {
		t.Fatal("empty manual draft should fail")
	}
	if _, err := s.CreateManualDraft(context.Background(), ManualDraftRequest{Description: "x", Images: []model.ImageContent{{MimeType: "text/plain", Data: "bad"}}}); err == nil {
		t.Fatal("invalid image should fail")
	}
	if _, err := s.AssistHermesLearningEdit(context.Background(), "missing", AssistHermesLearningEditRequest{}); err == nil {
		t.Fatal("missing asset should fail")
	}
}

func TestSubmitAndServiceLifecycle(t *testing.T) {
	s, store, home := newTestService(t)
	ctx := context.Background()

	started := NewService(store, config.FeedbackConfig{
		DistillInterval: time.Hour,
		AuditInterval:   time.Hour,
		NotesMaxEntries: 100,
	}, home, nil, zap.NewNop())
	started.Shutdown()

	sess := &model.Session{ID: "submit-session", ClientID: "u1", UserID: "u1", Status: model.SessionStatusClosed, CreatedAt: time.Now()}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	msg := &model.Message{ID: "submit-message", SessionID: sess.ID, Role: model.MessageRoleAssistant, Content: "answer", CreatedAt: time.Now()}
	if err := store.CreateMessage(ctx, msg); err != nil {
		t.Fatalf("create message: %v", err)
	}

	if _, err := s.Submit(ctx, SubmitRequest{SessionID: sess.ID, MessageID: msg.ID, Rating: "bad"}); err == nil {
		t.Fatal("invalid rating should fail")
	}
	if _, err := s.Submit(ctx, SubmitRequest{SessionID: sess.ID, MessageID: "missing", Rating: model.FeedbackDown}); err == nil {
		t.Fatal("missing message should fail")
	}
	fb, err := s.Submit(ctx, SubmitRequest{SessionID: sess.ID, MessageID: msg.ID, Rating: model.FeedbackDown, Correction: "  corrected answer  "})
	if err != nil {
		t.Fatalf("submit feedback: %v", err)
	}
	if fb.Correction != "corrected answer" || fb.Rating != model.FeedbackDown {
		t.Fatalf("unexpected feedback: %+v", fb)
	}
}

func TestRunLearningJobFromClosedHistory(t *testing.T) {
	s, store, _ := newTestService(t)
	ctx := context.Background()
	spec := agent.AgentSpec{DefaultModel: "test-model", APIURL: "http://callme-ai.test", APIToken: "token"}
	s.agentSpec = func() agent.AgentSpec { return spec }

	now := time.Now()
	sess := &model.Session{
		ID:        "history-session",
		ClientID:  "u1",
		UserID:    "u1",
		Title:     "部署问题",
		Status:    model.SessionStatusClosed,
		CreatedAt: now.Add(-time.Hour),
		ClosedAt:  &now,
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	for _, msg := range []*model.Message{
		{ID: "h-system", SessionID: sess.ID, Role: model.MessageRoleSystem, Content: "hidden", CreatedAt: now},
		{ID: "h-user", SessionID: sess.ID, Role: model.MessageRoleUser, Content: "如何部署 Callme？", CreatedAt: now.Add(time.Second)},
		{ID: "h-assistant", SessionID: sess.ID, Role: model.MessageRoleAssistant, Content: "使用 start.sh 启动。", CreatedAt: now.Add(2 * time.Second)},
	} {
		if err := store.CreateMessage(ctx, msg); err != nil {
			t.Fatalf("create message %s: %v", msg.ID, err)
		}
	}

	restore := mockFeedbackAI(t, func(req *http.Request, body string) string {
		if !strings.Contains(body, "如何部署 Callme") || strings.Contains(body, "hidden") {
			t.Fatalf("unexpected learning request body: %s", body)
		}
		return `{"choices":[{"message":{"content":"{\"candidates\":[{\"title\":\"部署 Callme\",\"question\":\"如何部署 Callme？\",\"content\":\"使用 start.sh 启动服务。\",\"confidence\":0.8,\"reason\":\"来自历史会话\"}]}"}}]}`
	})
	defer restore()

	if err := s.RunLearningJobNow(ctx); err != nil {
		t.Fatalf("run learning job: %v", err)
	}
	jobs, err := s.ListLearningJobs(ctx)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Status != model.LearningJobStatusSucceeded || jobs[0].OutputAssets != 1 || jobs[0].InputSessions != 1 {
		t.Fatalf("unexpected learning job: %+v", jobs)
	}
	cands, err := s.ListCandidates(ctx, model.CandidateStatusPending)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(cands) != 1 || cands[0].Title != "部署 Callme" || cands[0].SourceSessionID != sess.ID {
		t.Fatalf("unexpected learned candidate: %+v", cands)
	}

	transcript, sourceIDs := s.buildLearningTranscript(ctx, []*model.Session{sess})
	if !strings.Contains(transcript, "部署问题") || strings.Contains(transcript, "system:") || len(sourceIDs) != 1 {
		t.Fatalf("unexpected transcript=%q sourceIDs=%v", transcript, sourceIDs)
	}
}

func TestRunLearningJobSkippedAndFailed(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()
	if err := s.RunLearningJobNow(ctx); err != nil {
		t.Fatalf("missing agent spec should be recorded as skipped, got error: %v", err)
	}
	jobs, err := s.ListLearningJobs(ctx)
	if err != nil {
		t.Fatalf("list skipped jobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].Status != model.LearningJobStatusSkipped {
		t.Fatalf("expected skipped job, got %+v", jobs)
	}

	s.agentSpec = func() agent.AgentSpec {
		return agent.AgentSpec{DefaultModel: "test-model", APIURL: "http://callme-ai.test", APIToken: "token"}
	}
	restore := mockFeedbackAI(t, func(req *http.Request, body string) string {
		return `{"choices":[{"message":{"content":"not-json"}}]}`
	})
	defer restore()
	// 没有历史会话时仍然是 skipped，不会调用 AI。
	if err := s.RunLearningJobNow(ctx); err != nil {
		t.Fatalf("empty history should be skipped, got error: %v", err)
	}
}

func TestHermesLearningAssetReviewBoundaries(t *testing.T) {
	s, store, home := newTestService(t)
	ctx := context.Background()
	path := filepath.Join(home, "skills", "review", "SKILL.md")
	mustWriteFeedbackTest(t, path, "# Review Skill\n")
	if err := s.auditHermesLearning(ctx); err != nil {
		t.Fatalf("audit: %v", err)
	}
	assets, err := s.ListHermesLearningAssets(ctx, model.HermesLearningStatusPendingReview)
	if err != nil || len(assets) != 1 {
		t.Fatalf("list hermes assets len=%d err=%v", len(assets), err)
	}
	if _, err := s.ReviewHermesLearningAsset(ctx, assets[0].ID, ReviewHermesLearningRequest{Action: "unknown"}, "admin"); err == nil {
		t.Fatal("unknown review action should fail")
	}
	if _, err := s.ReviewHermesLearningAsset(ctx, assets[0].ID, ReviewHermesLearningRequest{Action: "modify"}, "admin"); err == nil {
		t.Fatal("empty modify content should fail")
	}
	kept, err := s.ReviewHermesLearningAsset(ctx, assets[0].ID, ReviewHermesLearningRequest{Action: "keep", Note: "保留"}, "admin")
	if err != nil {
		t.Fatalf("keep asset: %v", err)
	}
	if kept.Status != model.HermesLearningStatusKept || kept.ReviewNote != "保留" {
		t.Fatalf("unexpected kept asset: %+v", kept)
	}

	// 已删除记录不能再修改。
	deleted := &model.HermesLearningAsset{
		ID:          "deleted-asset",
		AssetType:   model.HermesLearningAssetSkill,
		Path:        filepath.Join(home, "missing", "SKILL.md"),
		ChangeType:  model.HermesLearningChangeDeleted,
		Status:      model.HermesLearningStatusPendingReview,
		RiskFlags:   "[]",
		ContentHash: "",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if err := store.CreateHermesLearningAsset(ctx, deleted); err != nil {
		t.Fatalf("create deleted asset: %v", err)
	}
	if _, err := s.ReviewHermesLearningAsset(ctx, deleted.ID, ReviewHermesLearningRequest{Action: "modify", Content: "# x"}, "admin"); err == nil {
		t.Fatal("deleted record should not be modifiable")
	}
}

func mockFeedbackAI(t *testing.T, responder func(*http.Request, string) string) func() {
	t.Helper()
	original := http.DefaultTransport
	http.DefaultTransport = feedbackRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		data, _ := io.ReadAll(req.Body)
		body := responder(req, string(data))
		contentType := "application/json"
		if strings.HasPrefix(body, "data:") {
			contentType = "text/event-stream"
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{contentType}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	return func() { http.DefaultTransport = original }
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
