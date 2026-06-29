package repo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"callme/internal/model"
)

func TestCandidateAssetLifecycleAndCounts(t *testing.T) {
	db, err := Open("sqlite", filepath.Join(t.TempDir(), "candidates.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	store := NewStore(db)
	ctx := context.Background()
	now := time.Now()
	cand := &model.CandidateAsset{
		ID:             "cand",
		AssetType:      model.CandidateAssetKnowledge,
		PublishTargets: []model.KnowledgePublishTarget{model.KnowledgePublishSkill},
		Title:          "Title",
		Question:       "Question",
		Content:        "Content",
		Evidence:       "Evidence",
		Confidence:     0.8,
		Status:         model.CandidateStatusPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := store.CreateCandidate(ctx, cand); err != nil {
		t.Fatalf("create candidate: %v", err)
	}
	got, err := store.GetCandidate(ctx, cand.ID)
	if err != nil {
		t.Fatalf("get candidate: %v", err)
	}
	if got.PublishTargets[0] != model.KnowledgePublishSkill || got.Question != "Question" || got.Evidence != "Evidence" {
		t.Fatalf("candidate not decoded: %+v", got)
	}
	all, err := store.ListCandidates(ctx, "", 0)
	if err != nil || len(all) != 1 {
		t.Fatalf("list all candidates len=%d err=%v", len(all), err)
	}
	pending, err := store.ListCandidates(ctx, model.CandidateStatusPending, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("list pending candidates len=%d err=%v", len(pending), err)
	}
	cand.Status = model.CandidateStatusApproved
	cand.Reviewer = "admin"
	cand.ReviewNote = "ok"
	if err := store.UpdateCandidate(ctx, cand); err != nil {
		t.Fatalf("update candidate: %v", err)
	}
	n, err := store.CountCandidatesByStatus(ctx, model.CandidateStatusApproved)
	if err != nil || n != 1 {
		t.Fatalf("approved count=%d err=%v", n, err)
	}
}

func TestRuntimeLearningAssetsFilterHydrateAndJobs(t *testing.T) {
	dir := t.TempDir()
	db, err := Open("sqlite", filepath.Join(dir, "runtime-assets.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	store := NewStore(db)
	ctx := context.Background()
	now := time.Now()
	skillPath := filepath.Join(dir, "skills", "demo", "SKILL.md")
	mustWriteRepoTest(t, skillPath, "# Demo Skill\n")
	skillCopyPath := filepath.Join(dir, "skills", "copy", "SKILL.md")
	mustWriteRepoTest(t, skillCopyPath, "# Demo Skill Copy\n")
	memoryPath := filepath.Join(dir, "memories", "MEMORY.md")
	mustWriteRepoTest(t, memoryPath, "# Memory\n")
	ignoredPath := filepath.Join(dir, "_quarantine", "skills", "old", "SKILL.md")
	mustWriteRepoTest(t, ignoredPath, "# Ignored\n")
	assets := []*model.RuntimeLearningAsset{
		{ID: "skill-1", AssetType: model.RuntimeLearningAssetSkill, Path: skillPath, ContentHash: "same", ChangeType: model.RuntimeLearningChangeNew, RiskFlags: "[]", Status: model.RuntimeLearningStatusPendingReview, CreatedAt: now, UpdatedAt: now},
		{ID: "skill-dup", AssetType: model.RuntimeLearningAssetSkill, Path: skillCopyPath, ContentHash: "same", ChangeType: model.RuntimeLearningChangeNew, RiskFlags: "[]", Status: model.RuntimeLearningStatusPendingReview, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)},
		{ID: "memory-1", AssetType: model.RuntimeLearningAssetMemory, Path: memoryPath, ContentHash: "memory", ChangeType: model.RuntimeLearningChangeModified, RiskFlags: "[]", Status: model.RuntimeLearningStatusPendingReview, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)},
		{ID: "ignored", AssetType: model.RuntimeLearningAssetSkill, Path: ignoredPath, ContentHash: "ignored", ChangeType: model.RuntimeLearningChangeNew, RiskFlags: "[]", Status: model.RuntimeLearningStatusPendingReview, CreatedAt: now.Add(3 * time.Second), UpdatedAt: now.Add(3 * time.Second)},
		{ID: "deleted", AssetType: model.RuntimeLearningAssetMemory, Path: filepath.Join(dir, "missing.md"), ContentHash: "", ChangeType: model.RuntimeLearningChangeDeleted, RiskFlags: "[]", Status: model.RuntimeLearningStatusPendingReview, CreatedAt: now.Add(4 * time.Second), UpdatedAt: now.Add(4 * time.Second)},
	}
	for _, asset := range assets {
		if err := store.CreateRuntimeLearningAsset(ctx, asset); err != nil {
			t.Fatalf("create asset %s: %v", asset.ID, err)
		}
	}
	latest, err := store.ListLatestRuntimeLearningAssets(ctx)
	if err != nil {
		t.Fatalf("list latest: %v", err)
	}
	if len(latest) != 5 {
		t.Fatalf("latest len=%d", len(latest))
	}
	list, err := store.ListRuntimeLearningAssets(ctx, model.RuntimeLearningStatusPendingReview, 10)
	if err != nil {
		t.Fatalf("list runtime assets: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("filtered/deduped runtime assets len=%d assets=%+v", len(list), list)
	}
	var hydratedSkill bool
	for _, asset := range list {
		if asset.AssetType == model.RuntimeLearningAssetSkill && strings.Contains(asset.Content, "Demo Skill") {
			hydratedSkill = true
		}
	}
	if !hydratedSkill {
		t.Fatalf("skill content not hydrated: %+v", list)
	}
	if err := store.UpdateRuntimeLearningAssetReview(ctx, "skill-1", model.RuntimeLearningStatusKept, "admin", "ok"); err != nil {
		t.Fatalf("update review: %v", err)
	}
	if err := store.UpdateRuntimeLearningAssetReviewWithHash(ctx, "memory-1", model.RuntimeLearningStatusModified, "new-hash", "admin", "mod"); err != nil {
		t.Fatalf("update review hash: %v", err)
	}
	modified, err := store.GetRuntimeLearningAsset(ctx, "memory-1")
	if err != nil {
		t.Fatalf("get modified asset: %v", err)
	}
	if modified.ContentHash != "new-hash" || modified.ReviewNote != "mod" || modified.Content != "# Memory\n" {
		t.Fatalf("modified asset = %+v", modified)
	}

	finished := now.Add(time.Minute)
	job := &model.LearningJob{ID: "job", Source: "history", Status: model.LearningJobStatusRunning, StartedAt: now}
	if err := store.CreateLearningJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	job.Status = model.LearningJobStatusSucceeded
	job.InputSessions = 2
	job.OutputAssets = 1
	job.FinishedAt = &finished
	if err := store.UpdateLearningJob(ctx, job); err != nil {
		t.Fatalf("update job: %v", err)
	}
	jobs, err := store.ListLearningJobs(ctx, 0)
	if err != nil || len(jobs) != 1 || jobs[0].Status != model.LearningJobStatusSucceeded || jobs[0].FinishedAt == nil {
		t.Fatalf("jobs=%+v err=%v", jobs, err)
	}
}

func mustWriteRepoTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
