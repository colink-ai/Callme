package repo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"callme/internal/model"
)

func newLifecycleStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db)
}

func TestStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newLifecycleStore(t)
	now := time.Now()

	user := &model.User{
		ID:           "u1",
		Username:     "alice",
		PasswordHash: "hash",
		Role:         model.UserRoleNormal,
		Roles:        []model.UserRole{model.UserRoleNormal, model.UserRoleVIP},
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := store.CreateUser(ctx, user); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if got, err := store.GetUser(ctx, "u1"); err != nil || !got.HasRole(model.UserRoleVIP) || got.MaxSessions != 2 {
		t.Fatalf("GetUser = %+v err=%v", got, err)
	}
	if domains, err := store.ListUserDomainIDs(ctx, "u1"); err != nil || len(domains) != 1 || domains[0] != "default" {
		t.Fatalf("default user domains = %+v err=%v", domains, err)
	}
	if got, err := store.GetUserByUsername(ctx, "alice"); err != nil || got.ID != "u1" {
		t.Fatalf("GetUserByUsername = %+v err=%v", got, err)
	}
	if users, err := store.ListUsers(ctx); err != nil || len(users) != 1 || len(users[0].DomainIDs) != 1 || users[0].DomainIDs[0] != "default" {
		t.Fatalf("ListUsers users=%+v err=%v", users, err)
	}
	if names, err := store.UsernamesByIDs(ctx, []string{"u1", "missing"}); err != nil || names["u1"] != "alice" {
		t.Fatalf("UsernamesByIDs = %+v err=%v", names, err)
	}
	if names, err := store.UsernamesByIDs(ctx, nil); err != nil || len(names) != 0 {
		t.Fatalf("empty UsernamesByIDs = %+v err=%v", names, err)
	}
	if err := store.UpdateUserRole(ctx, "u1", model.UserRoleKnowledgeStaff); err != nil {
		t.Fatalf("UpdateUserRole: %v", err)
	}
	if err := store.UpdateUserRolesAndLimit(ctx, "u1", []model.UserRole{model.UserRoleAdmin}, 12); err != nil {
		t.Fatalf("UpdateUserRolesAndLimit: %v", err)
	}
	if n, err := store.CountUsersByRole(ctx, model.UserRoleAdmin); err != nil || n != 1 {
		t.Fatalf("CountUsersByRole = %d err=%v", n, err)
	}

	if err := store.UpsertDomain(ctx, &model.Domain{ID: "ops", Name: "Ops", Enabled: true}); err != nil {
		t.Fatalf("UpsertDomain ops: %v", err)
	}
	if ok, err := store.UserCanUseDomain(ctx, user, "default"); err != nil || !ok {
		t.Fatalf("default domain access = %v err=%v", ok, err)
	}
	if ok, err := store.UserCanUseDomain(ctx, user, "ops"); err != nil || ok {
		t.Fatalf("ops access before grant = %v err=%v", ok, err)
	}
	if err := store.SetUserDomains(ctx, "u1", []string{"ops"}); err != nil {
		t.Fatalf("SetUserDomains: %v", err)
	}
	if ok, err := store.UserCanUseDomain(ctx, user, "ops"); err != nil || !ok {
		t.Fatalf("ops access after grant = %v err=%v", ok, err)
	}

	tok := &model.AuthToken{Token: "tok", UserID: "u1", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := store.SaveAuthToken(ctx, tok); err != nil {
		t.Fatalf("SaveAuthToken: %v", err)
	}
	if got, err := store.GetAuthToken(ctx, "tok"); err != nil || got.UserID != "u1" {
		t.Fatalf("GetAuthToken = %+v err=%v", got, err)
	}
	if err := store.DeleteExpiredAuthTokens(ctx, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("DeleteExpiredAuthTokens: %v", err)
	}
	if _, err := store.GetAuthToken(ctx, "tok"); err == nil {
		t.Fatal("expired token should be deleted")
	}
	tok2 := &model.AuthToken{Token: "tok2", UserID: "u1", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := store.SaveAuthToken(ctx, tok2); err != nil {
		t.Fatalf("SaveAuthToken tok2: %v", err)
	}
	if err := store.DeleteAuthToken(ctx, "tok2"); err != nil {
		t.Fatalf("DeleteAuthToken: %v", err)
	}
	if _, err := store.GetAuthToken(ctx, "tok2"); err == nil {
		t.Fatal("deleted token should not be found")
	}

	started := now.Add(-time.Hour)
	closed := now.Add(-time.Minute)
	sess := &model.Session{ID: "s1", ClientID: "u1", UserID: "u1", Status: model.SessionStatusActive, CreatedAt: started, StartedAt: &started, Title: "hello"}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sess.Status = model.SessionStatusClosed
	sess.ClosedAt = &closed
	sess.CloseReason = model.CloseReasonUser
	sess.AgentSessionID = "agent-s1"
	if err := store.UpdateSession(ctx, sess); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	if got, err := store.GetSession(ctx, "s1"); err != nil || got.Status != model.SessionStatusClosed || got.AgentSessionID != "agent-s1" {
		t.Fatalf("GetSession = %+v err=%v", got, err)
	}
	sess.Status = model.SessionStatusQueued
	sess.CreatedAt = now
	sess.StartedAt = nil
	sess.ClosedAt = nil
	sess.CloseReason = ""
	if err := store.ReopenSession(ctx, sess); err != nil {
		t.Fatalf("ReopenSession: %v", err)
	}
	if queued, err := store.ListSessionsByStatus(ctx, []model.SessionStatus{model.SessionStatusQueued}, 10); err != nil || len(queued) != 1 {
		t.Fatalf("ListSessionsByStatus queued=%+v err=%v", queued, err)
	}
	if empty, err := store.ListSessionsByStatus(ctx, nil, 10); err != nil || empty != nil {
		t.Fatalf("empty ListSessionsByStatus=%+v err=%v", empty, err)
	}
	if err := store.CloseUnfinishedSessions(ctx, model.CloseReasonAdmin); err != nil {
		t.Fatalf("CloseUnfinishedSessions: %v", err)
	}
	if got, err := store.GetSession(ctx, "s1"); err != nil || got.Status != model.SessionStatusClosed || got.CloseReason != model.CloseReasonAdmin {
		t.Fatalf("closed unfinished session = %+v err=%v", got, err)
	}
	sess.Status = model.SessionStatusClosed
	sess.ClosedAt = &closed
	sess.CloseReason = model.CloseReasonUser
	if err := store.UpdateSession(ctx, sess); err != nil {
		t.Fatalf("restore closed session: %v", err)
	}
	if closedSessions, total, err := store.ListClosedSessions(ctx, nil, nil, "u1", 1, 10); err != nil || total != 1 || len(closedSessions) != 1 {
		t.Fatalf("ListClosedSessions len=%d total=%d err=%v", len(closedSessions), total, err)
	}
	if byUser, err := store.ListSessionsByUser(ctx, "u1", 10); err != nil || len(byUser) != 1 {
		t.Fatalf("ListSessionsByUser len=%d err=%v", len(byUser), err)
	}
	if n, err := store.CountSessionsSince(ctx, now.Add(-24*time.Hour)); err != nil || n != 1 {
		t.Fatalf("CountSessionsSince = %d err=%v", n, err)
	}
	if daily, err := store.DailySessionCounts(ctx, 2); err != nil || daily[now.Format("2006-01-02")] != 1 {
		t.Fatalf("DailySessionCounts = %+v err=%v", daily, err)
	}

	msgs := []*model.Message{
		{ID: "m1", SessionID: "s1", Role: model.MessageRoleUser, Content: "hello", CreatedAt: now.Add(-4 * time.Minute)},
		{ID: "m2", SessionID: "s1", Role: model.MessageRoleAssistant, Content: "world", ToolCalls: `[{"toolName":"mcp_code_graph_query"}]`, Model: "glm", AgentType: "hermes", CreatedAt: now.Add(-3 * time.Minute)},
	}
	for _, msg := range msgs {
		if err := store.CreateMessage(ctx, msg); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
	}
	if listed, err := store.ListMessages(ctx, "s1"); err != nil || len(listed) != 2 || listed[1].ToolCalls == "" {
		t.Fatalf("ListMessages = %+v err=%v", listed, err)
	}
	if got, err := store.GetMessage(ctx, "m2"); err != nil || got.Model != "glm" || got.AgentType != "hermes" {
		t.Fatalf("GetMessage = %+v err=%v", got, err)
	}
	if n, err := store.CountMessagesSince(ctx, model.MessageRoleUser, now.Add(-24*time.Hour)); err != nil || n != 1 {
		t.Fatalf("CountMessagesSince = %d err=%v", n, err)
	}
	if n, err := store.CountKnowledgeHitsSince(ctx, now.Add(-24*time.Hour)); err != nil || n != 1 {
		t.Fatalf("CountKnowledgeHitsSince = %d err=%v", n, err)
	}
	if qs, err := store.RecentUserQuestions(ctx, now.Add(-24*time.Hour), 10); err != nil || len(qs) != 1 || qs[0] != "hello" {
		t.Fatalf("RecentUserQuestions = %+v err=%v", qs, err)
	}
	if err := store.CopyMessagesToSession(ctx, "s1", "s2", now); err != nil {
		t.Fatalf("CopyMessagesToSession: %v", err)
	}
	if copied, err := store.ListMessages(ctx, "s2"); err != nil || len(copied) != 2 {
		t.Fatalf("copied messages = %+v err=%v", copied, err)
	}

	for _, f := range []*model.Feedback{
		{ID: "f1", SessionID: "s1", MessageID: "m2", Rating: model.FeedbackUp, CreatedAt: now},
		{ID: "f2", SessionID: "s1", MessageID: "m2", Rating: model.FeedbackDown, Correction: "fix", CreatedAt: now},
	} {
		if err := store.CreateFeedback(ctx, f); err != nil {
			t.Fatalf("CreateFeedback: %v", err)
		}
	}
	if pending, err := store.ListUndistilledFeedback(ctx, 10); err != nil || len(pending) != 2 {
		t.Fatalf("ListUndistilledFeedback len=%d err=%v", len(pending), err)
	}
	if err := store.MarkFeedbackDistilled(ctx, []string{"f1"}); err != nil {
		t.Fatalf("MarkFeedbackDistilled: %v", err)
	}
	if up, down, err := store.FeedbackCountsSince(ctx, now.Add(-time.Hour)); err != nil || up != 1 || down != 1 {
		t.Fatalf("FeedbackCountsSince up=%d down=%d err=%v", up, down, err)
	}
	if daily, err := store.DailyFeedbackCounts(ctx, 2); err != nil || daily[now.Format("2006-01-02")] != [2]int64{1, 1} {
		t.Fatalf("DailyFeedbackCounts = %+v err=%v", daily, err)
	}

	ticket := &model.Ticket{ID: "t1", SessionID: "s1", Reason: "help", Transcript: "transcript", Status: model.TicketStatusOpen, CreatedAt: now}
	if err := store.CreateTicket(ctx, ticket); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if err := store.UpdateTicketStatus(ctx, "t1", model.TicketStatusNotified); err != nil {
		t.Fatalf("UpdateTicketStatus: %v", err)
	}
	if tickets, err := store.ListTickets(ctx, 10); err != nil || len(tickets) != 1 || tickets[0].Status != model.TicketStatusNotified {
		t.Fatalf("ListTickets = %+v err=%v", tickets, err)
	}
	if n, err := store.CountTicketsSince(ctx, now.Add(-time.Hour)); err != nil || n != 1 {
		t.Fatalf("CountTicketsSince = %d err=%v", n, err)
	}
	if err := store.DeleteClosedSessionCascade(ctx, "s1"); err != nil {
		t.Fatalf("DeleteClosedSessionCascade: %v", err)
	}
	if _, err := store.GetSession(ctx, "s1"); err == nil {
		t.Fatal("deleted session should not be found")
	}
	if listed, err := store.ListMessages(ctx, "s1"); err != nil || len(listed) != 0 {
		t.Fatalf("deleted session messages = %+v err=%v", listed, err)
	}
	if err := store.DeleteUser(ctx, "u1"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := store.GetUser(ctx, "u1"); err == nil {
		t.Fatal("deleted user should not be found")
	}

	var setting model.PoolSettings
	if ok, err := store.GetSetting(ctx, "pool", &setting); err != nil || ok {
		t.Fatalf("missing GetSetting ok=%v err=%v", ok, err)
	}
	if err := store.PutSetting(ctx, "pool", model.PoolSettings{MaxActive: 3, MaxQueue: 4}); err != nil {
		t.Fatalf("PutSetting: %v", err)
	}
	if ok, err := store.GetSetting(ctx, "pool", &setting); err != nil || !ok || setting.MaxActive != 3 {
		t.Fatalf("GetSetting ok=%v setting=%+v err=%v", ok, setting, err)
	}
}

func TestStoreAssetsAndLearningJobs(t *testing.T) {
	ctx := context.Background()
	store := newLifecycleStore(t)
	now := time.Now()

	cand := &model.CandidateAsset{
		ID:             "c1",
		AssetType:      model.CandidateAssetKnowledge,
		PublishTargets: []model.KnowledgePublishTarget{model.KnowledgePublishSkill},
		Title:          "知识",
		Question:       "问题",
		Content:        "内容",
		Confidence:     0.8,
		Status:         model.CandidateStatusPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := store.CreateCandidate(ctx, cand); err != nil {
		t.Fatalf("CreateCandidate: %v", err)
	}
	if got, err := store.GetCandidate(ctx, "c1"); err != nil || got.PublishTargets[0] != model.KnowledgePublishSkill {
		t.Fatalf("GetCandidate = %+v err=%v", got, err)
	}
	cand.Status = model.CandidateStatusApproved
	cand.Reviewer = "expert"
	cand.ReviewNote = "ok"
	if err := store.UpdateCandidate(ctx, cand); err != nil {
		t.Fatalf("UpdateCandidate: %v", err)
	}
	if list, err := store.ListCandidates(ctx, model.CandidateStatusApproved, 10); err != nil || len(list) != 1 {
		t.Fatalf("ListCandidates = %+v err=%v", list, err)
	}
	if n, err := store.CountCandidatesByStatus(ctx, model.CandidateStatusApproved); err != nil || n != 1 {
		t.Fatalf("CountCandidatesByStatus = %d err=%v", n, err)
	}

	skillPath := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("# skill"), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	asset := &model.HermesLearningAsset{
		ID:          "h1",
		AssetType:   model.HermesLearningAssetSkill,
		Path:        skillPath,
		ContentHash: "hash",
		Content:     "",
		ChangeType:  model.HermesLearningChangeNew,
		RiskFlags:   "[]",
		Status:      model.HermesLearningStatusPendingReview,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.CreateHermesLearningAsset(ctx, asset); err != nil {
		t.Fatalf("CreateHermesLearningAsset: %v", err)
	}
	if got, err := store.GetHermesLearningAsset(ctx, "h1"); err != nil || got.Content != "# skill" {
		t.Fatalf("GetHermesLearningAsset = %+v err=%v", got, err)
	}
	if got, err := store.LatestHermesLearningAssetByPath(ctx, skillPath); err != nil || got.ID != "h1" {
		t.Fatalf("LatestHermesLearningAssetByPath = %+v err=%v", got, err)
	}
	if latest, err := store.ListLatestHermesLearningAssets(ctx); err != nil || latest[skillPath].ID != "h1" {
		t.Fatalf("ListLatestHermesLearningAssets = %+v err=%v", latest, err)
	}
	if list, err := store.ListHermesLearningAssets(ctx, model.HermesLearningStatusPendingReview, 10); err != nil || len(list) != 1 || list[0].Content != "# skill" {
		t.Fatalf("ListHermesLearningAssets = %+v err=%v", list, err)
	}
	if err := store.UpdateHermesLearningAssetReview(ctx, "h1", model.HermesLearningStatusKept, "admin", "ok"); err != nil {
		t.Fatalf("UpdateHermesLearningAssetReview: %v", err)
	}
	if err := store.UpdateHermesLearningAssetReviewWithHash(ctx, "h1", model.HermesLearningStatusModified, "hash2", "admin", "changed"); err != nil {
		t.Fatalf("UpdateHermesLearningAssetReviewWithHash: %v", err)
	}

	job := &model.LearningJob{ID: "j1", Source: "history", Status: model.LearningJobStatusRunning, StartedAt: now}
	if err := store.CreateLearningJob(ctx, job); err != nil {
		t.Fatalf("CreateLearningJob: %v", err)
	}
	finished := now.Add(time.Minute)
	job.Status = model.LearningJobStatusSucceeded
	job.InputSessions = 2
	job.OutputAssets = 1
	job.FinishedAt = &finished
	if err := store.UpdateLearningJob(ctx, job); err != nil {
		t.Fatalf("UpdateLearningJob: %v", err)
	}
	if jobs, err := store.ListLearningJobs(ctx, 0); err != nil || len(jobs) != 1 || jobs[0].Status != model.LearningJobStatusSucceeded || jobs[0].FinishedAt == nil {
		t.Fatalf("ListLearningJobs = %+v err=%v", jobs, err)
	}
}
