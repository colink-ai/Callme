// Package feedback 反馈与自学习沙箱
//
// 链路（Phase 1：沙箱 + 审批闸门）：
//
//	用户点踩/纠错 -> 反馈入库 -> 定时蒸馏(单写者) -> 候选知识池(pending)
//	  -> 管理员审批 -> 按发布目标写入本地知识 / Skill / 外部知识库
//
// 关键原则：任何会话提炼内容都不会自动进入生产回答链路；只有审批通过的资产才发布为正式知识，
// 杜绝"错误知识自增强"。生产 Agent 仅参考审批发布后的知识，不读候选池。
package feedback

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"callme/internal/config"
	"callme/internal/model"
	"callme/internal/repo"
	"callme/internal/service/agent"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
)

// ApprovedFileName 正式知识文件名（位于当前 Agent Runtime 工作目录下）
const ApprovedFileName = "approved_knowledge.md"

// Service 反馈与自学习沙箱服务
type Service struct {
	store       *repo.Store
	cfg         config.FeedbackConfig
	runtimeHome string
	agentSpec   func() agent.AgentSpec
	logger      *zap.Logger
	stop        chan struct{}
}

// NewService 创建服务并启动蒸馏任务
func NewService(store *repo.Store, cfg config.FeedbackConfig, runtimeHome string, agentSpec func() agent.AgentSpec, logger *zap.Logger) *Service {
	s := &Service{store: store, cfg: cfg, runtimeHome: runtimeHome, agentSpec: agentSpec, logger: logger, stop: make(chan struct{})}
	go s.distillLoop()
	go s.auditLoop()
	return s
}

// Shutdown 停止蒸馏任务
func (s *Service) Shutdown() { close(s.stop) }

// SubmitRequest 提交反馈请求
type SubmitRequest struct {
	SessionID  string               `json:"sessionId"`
	MessageID  string               `json:"messageId"`
	Rating     model.FeedbackRating `json:"rating"`
	Correction string               `json:"correction"`
}

// ManualDraftRequest 管理员手动录入知识，由 AI 整理成候选资产。
type ManualDraftRequest struct {
	PublishTargets []model.KnowledgePublishTarget `json:"publishTargets,omitempty"`
	Description    string                         `json:"description"`
	Images         []model.ImageContent           `json:"images,omitempty"`
}

// ReviewRuntimeLearningRequest 处理 Agent Runtime 自学习审计记录。
type ReviewRuntimeLearningRequest struct {
	Action  string `json:"action"`
	Note    string `json:"note"`
	Content string `json:"content"`
}

// AssistRuntimeLearningEditRequest 用 AI 辅助修改 Agent Runtime 自学习内容。
type AssistRuntimeLearningEditRequest struct {
	Instruction string `json:"instruction"`
	Content     string `json:"content"`
}

type ReviewHermesLearningRequest = ReviewRuntimeLearningRequest
type AssistHermesLearningEditRequest = AssistRuntimeLearningEditRequest

// ManualDraftStreamEvent 人工录入知识流式生成事件。
type ManualDraftStreamEvent struct {
	Type      string                `json:"type"`
	Delta     string                `json:"delta,omitempty"`
	Content   string                `json:"content,omitempty"`
	Candidate *model.CandidateAsset `json:"candidate,omitempty"`
	Error     string                `json:"error,omitempty"`
}

// Submit 提交消息级反馈
func (s *Service) Submit(ctx context.Context, req SubmitRequest) (*model.Feedback, error) {
	if req.Rating != model.FeedbackUp && req.Rating != model.FeedbackDown {
		return nil, fmt.Errorf("无效的评价类型: %s", req.Rating)
	}
	if _, err := s.store.GetMessage(ctx, req.MessageID); err != nil {
		return nil, fmt.Errorf("消息不存在: %w", err)
	}
	f := &model.Feedback{
		ID:         uuid.New().String(),
		SessionID:  req.SessionID,
		MessageID:  req.MessageID,
		Rating:     req.Rating,
		Correction: strings.TrimSpace(req.Correction),
		CreatedAt:  time.Now(),
	}
	if err := s.store.CreateFeedback(ctx, f); err != nil {
		return nil, err
	}
	return f, nil
}

// distillLoop 定时蒸馏（单写者：避免并发写候选池冲突）
func (s *Service) distillLoop() {
	s.runScheduled("feedback distill", s.cfg.DistillCron, s.cfg.DistillInterval, func() {
		if err := s.distillOnce(context.Background()); err != nil {
			s.logger.Error("feedback distill failed", zap.Error(err))
		}
		if err := s.aiLearnFromHistory(context.Background()); err != nil {
			s.logger.Warn("AI learning job failed", zap.Error(err))
		}
	})
}

func (s *Service) auditLoop() {
	if s.runtimeHome == "" {
		return
	}
	if err := s.auditRuntimeLearning(context.Background()); err != nil {
		s.logger.Warn("runtime learning audit failed", zap.Error(err))
	}
	s.runScheduled("runtime learning audit", s.cfg.AuditCron, s.cfg.AuditInterval, func() {
		if err := s.auditRuntimeLearning(context.Background()); err != nil {
			s.logger.Warn("runtime learning audit failed", zap.Error(err))
		}
	})
}

func (s *Service) runScheduled(name string, cronExpr string, interval time.Duration, run func()) {
	if strings.TrimSpace(cronExpr) != "" {
		s.runCronScheduled(name, cronExpr, interval, run)
		return
	}
	s.runIntervalScheduled(interval, run)
}

func (s *Service) runIntervalScheduled(interval time.Duration, run func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			run()
		}
	}
}

func (s *Service) runCronScheduled(name string, cronExpr string, fallbackInterval time.Duration, run func()) {
	schedule, err := cron.ParseStandard(cronExpr)
	if err != nil {
		s.logger.Warn("invalid cron schedule, fallback to interval",
			zap.String("task", name),
			zap.String("cron", cronExpr),
			zap.Duration("fallbackInterval", fallbackInterval),
			zap.Error(err),
		)
		s.runIntervalScheduled(fallbackInterval, run)
		return
	}
	for {
		next := schedule.Next(time.Now())
		timer := time.NewTimer(time.Until(next))
		select {
		case <-s.stop:
			timer.Stop()
			return
		case <-timer.C:
			run()
		}
	}
}

// auditRuntimeLearning 扫描当前 Agent Runtime 自学习资产并记录 diff。
// 它只创建审计记录，不直接发布正式知识；有价值内容后续可人工转入候选资产池。
func (s *Service) auditRuntimeLearning(ctx context.Context) error {
	provider := s.runtimeLearningProvider()
	if provider == nil {
		return nil
	}
	current, err := provider.Scan(s.runtimeHome)
	if err != nil {
		return err
	}
	latest, err := s.store.ListLatestRuntimeLearningAssets(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	created := 0
	for path, file := range current {
		prev, ok := latest[path]
		if ok && prev.ContentHash == file.hash && prev.ChangeType != model.RuntimeLearningChangeDeleted {
			continue
		}
		changeType := model.RuntimeLearningChangeNew
		if ok {
			changeType = model.RuntimeLearningChangeModified
		}
		if err := s.store.CreateRuntimeLearningAsset(ctx, &model.RuntimeLearningAsset{
			ID:          uuid.New().String(),
			AgentType:   provider.AgentType(),
			AssetType:   file.assetType,
			Path:        path,
			ContentHash: file.hash,
			Content:     "",
			ChangeType:  changeType,
			RiskFlags:   "[]",
			Status:      model.RuntimeLearningStatusPendingReview,
			CreatedAt:   now,
			UpdatedAt:   now,
		}); err != nil {
			return err
		}
		created++
	}

	for path, prev := range latest {
		if prev.ChangeType == model.RuntimeLearningChangeDeleted {
			continue
		}
		if _, ok := current[path]; ok {
			continue
		}
		if err := s.store.CreateRuntimeLearningAsset(ctx, &model.RuntimeLearningAsset{
			ID:          uuid.New().String(),
			AgentType:   provider.AgentType(),
			AssetType:   prev.AssetType,
			Path:        path,
			ContentHash: "",
			Content:     "",
			ChangeType:  model.RuntimeLearningChangeDeleted,
			RiskFlags:   "[]",
			Status:      model.RuntimeLearningStatusPendingReview,
			CreatedAt:   now,
			UpdatedAt:   now,
		}); err != nil {
			return err
		}
		created++
	}

	if created > 0 {
		s.logger.Info("runtime learning audit records created",
			zap.String("agentType", provider.AgentType()),
			zap.Int("records", created),
		)
	}
	return nil
}

func (s *Service) auditHermesLearning(ctx context.Context) error {
	return s.auditRuntimeLearning(ctx)
}

// distillOnce 把未处理的高价值反馈蒸馏为「候选资产(pending)」——不直接进回答链路
func (s *Service) distillOnce(ctx context.Context) error {
	pending, err := s.store.ListUndistilledFeedback(ctx, 100)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}

	created := 0
	processed := make([]string, 0, len(pending))
	for _, f := range pending {
		processed = append(processed, f.ID)
		// 仅蒸馏带纠错内容的负反馈：最高价值且有明确"正确答案"的学习信号
		if f.Rating != model.FeedbackDown || f.Correction == "" {
			continue
		}
		msg, err := s.store.GetMessage(ctx, f.MessageID)
		if err != nil {
			continue
		}
		question := s.findUserQuestion(ctx, msg)

		// 来源证据：可追溯到会话/消息/原错误回答/人工纠错
		evidence, _ := json.Marshal(map[string]any{
			"question":    truncate(question, 400),
			"wrongAnswer": truncate(msg.Content, 400),
			"correction":  f.Correction,
			"messageId":   f.MessageID,
		})

		title := truncate(question, 60)
		if title == "" {
			title = "未命名候选知识"
		}
		now := time.Now()
		cand := &model.CandidateAsset{
			ID:               uuid.New().String(),
			AssetType:        model.CandidateAssetKnowledge,
			PublishTargets:   []model.KnowledgePublishTarget{model.KnowledgePublishLocal},
			Title:            title,
			Question:         question,
			Content:          f.Correction,
			Evidence:         string(evidence),
			SourceSessionID:  f.SessionID,
			SourceFeedbackID: f.ID,
			Confidence:       0.5, // 单条反馈初始置信度，质检/聚类在 Phase 2 提升
			Status:           model.CandidateStatusPending,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if err := s.store.CreateCandidate(ctx, cand); err != nil {
			s.logger.Error("create candidate failed", zap.Error(err))
			continue
		}
		created++
	}

	if created > 0 {
		s.logger.Info("feedback distilled into candidate pool (pending review)",
			zap.Int("candidates", created), zap.Int("processed", len(processed)))
	}
	return s.store.MarkFeedbackDistilled(ctx, processed)
}

func (s *Service) aiLearnFromHistory(ctx context.Context) error {
	job, err := s.createLearningJob(ctx)
	if err != nil {
		return err
	}
	_, err = s.runLearningJob(ctx, job)
	return err
}

func (s *Service) createLearningJob(ctx context.Context) (*model.LearningJob, error) {
	job := &model.LearningJob{
		ID:        uuid.New().String(),
		Source:    "history",
		Status:    model.LearningJobStatusRunning,
		StartedAt: time.Now(),
	}
	if err := s.store.CreateLearningJob(ctx, job); err != nil {
		return nil, err
	}
	return job, nil
}

func (s *Service) runLearningJob(ctx context.Context, job *model.LearningJob) (*model.LearningJob, error) {
	finish := func(status model.LearningJobStatus, errText string) error {
		done := time.Now()
		job.Status = status
		job.Error = errText
		job.FinishedAt = &done
		updateCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.store.UpdateLearningJob(updateCtx, job)
	}

	if s.agentSpec == nil {
		return job, finish(model.LearningJobStatusSkipped, "未配置 AI 模型")
	}
	spec := s.agentSpec()
	if spec.APIURL == "" || spec.APIToken == "" || spec.DefaultModel == "" {
		return job, finish(model.LearningJobStatusSkipped, "未配置 API Base URL、Token 或模型")
	}

	now := time.Now()
	start := now.Add(-24 * time.Hour)
	sessions, _, err := s.store.ListClosedSessions(ctx, &start, &now, "", 1, 20)
	if err != nil {
		_ = finish(model.LearningJobStatusFailed, err.Error())
		return job, err
	}
	job.InputSessions = len(sessions)
	if len(sessions) == 0 {
		return job, finish(model.LearningJobStatusSkipped, "最近 24 小时没有可挖掘的历史会话")
	}

	transcript, sourceIDs := s.buildLearningTranscript(ctx, sessions)
	if transcript == "" {
		return job, finish(model.LearningJobStatusSkipped, "历史会话没有有效消息")
	}

	candidates, err := runAILearning(ctx, spec, transcript)
	if err != nil {
		_ = finish(model.LearningJobStatusFailed, err.Error())
		return job, err
	}
	for _, c := range candidates {
		now := time.Now()
		if c.Title == "" || c.Content == "" {
			continue
		}
		evidence, _ := json.Marshal(map[string]any{
			"source":       "ai_history_learning",
			"learningJob":  job.ID,
			"sessions":     sourceIDs,
			"reason":       c.Reason,
			"sourceWindow": "最近 24 小时已结束会话",
		})
		if err := s.store.CreateCandidate(ctx, &model.CandidateAsset{
			ID:              uuid.New().String(),
			AssetType:       model.CandidateAssetKnowledge,
			PublishTargets:  []model.KnowledgePublishTarget{model.KnowledgePublishLocal},
			Title:           truncate(c.Title, 120),
			Question:        truncate(c.Question, 500),
			Content:         c.Content,
			Evidence:        string(evidence),
			SourceSessionID: firstString(sourceIDs),
			Confidence:      c.Confidence,
			Status:          model.CandidateStatusPending,
			CreatedAt:       now,
			UpdatedAt:       now,
		}); err != nil {
			_ = finish(model.LearningJobStatusFailed, err.Error())
			return job, err
		}
		job.OutputAssets++
	}
	return job, finish(model.LearningJobStatusSucceeded, "")
}

func (s *Service) buildLearningTranscript(ctx context.Context, sessions []*model.Session) (string, []string) {
	var b strings.Builder
	sourceIDs := make([]string, 0, len(sessions))
	for _, sess := range sessions {
		msgs, err := s.store.ListMessages(ctx, sess.ID)
		if err != nil || len(msgs) == 0 {
			continue
		}
		sourceIDs = append(sourceIDs, sess.ID)
		b.WriteString("会话 ")
		b.WriteString(sess.ID)
		if sess.Title != "" {
			b.WriteString(" / ")
			b.WriteString(sess.Title)
		}
		b.WriteString("\n")
		for _, msg := range msgs {
			if msg.Role == model.MessageRoleSystem {
				continue
			}
			b.WriteString(string(msg.Role))
			b.WriteString(": ")
			b.WriteString(truncate(msg.Content, 600))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return truncate(b.String(), 20000), sourceIDs
}

// ---------- 候选资产审批 ----------

// ListCandidates 列出候选资产（status 为空则全部）
func (s *Service) ListCandidates(ctx context.Context, status model.CandidateAssetStatus) ([]*model.CandidateAsset, error) {
	return s.store.ListCandidates(ctx, status, 200)
}

// ListRuntimeLearningAssets 列出 Agent Runtime 自学习审计记录。
func (s *Service) ListRuntimeLearningAssets(ctx context.Context, status model.RuntimeLearningStatus) ([]*model.RuntimeLearningAsset, error) {
	return s.store.ListRuntimeLearningAssets(ctx, status, 200)
}

// ReviewRuntimeLearningAsset 处理 Agent Runtime 自学习审计记录。
func (s *Service) ReviewRuntimeLearningAsset(ctx context.Context, id string, req ReviewRuntimeLearningRequest, reviewer string) (*model.RuntimeLearningAsset, error) {
	asset, err := s.store.GetRuntimeLearningAsset(ctx, id)
	if err != nil {
		return nil, err
	}
	req.Action = strings.TrimSpace(req.Action)
	req.Note = strings.TrimSpace(req.Note)

	status := model.RuntimeLearningStatus("")
	switch req.Action {
	case "keep":
		status = model.RuntimeLearningStatusKept
	case "delete":
		if asset.ChangeType != model.RuntimeLearningChangeDeleted && asset.Path != "" {
			if err := os.Remove(asset.Path); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("删除 Runtime 文件失败: %w", err)
			}
		}
		status = model.RuntimeLearningStatusDeleted
	case "modify":
		content := strings.TrimSpace(req.Content)
		if content == "" {
			return nil, fmt.Errorf("修改内容不能为空")
		}
		if asset.Path == "" || asset.ChangeType == model.RuntimeLearningChangeDeleted {
			return nil, fmt.Errorf("删除记录无法修改内容")
		}
		if err := os.WriteFile(asset.Path, []byte(content+"\n"), 0o644); err != nil {
			return nil, fmt.Errorf("写入 Runtime 文件失败: %w", err)
		}
		sum := sha256.Sum256([]byte(content + "\n"))
		hash := hex.EncodeToString(sum[:])
		if err := s.store.UpdateRuntimeLearningAssetReviewWithHash(ctx, id, model.RuntimeLearningStatusModified, hash, reviewer, req.Note); err != nil {
			return nil, err
		}
		asset.Status = model.RuntimeLearningStatusModified
		asset.ContentHash = hash
		asset.Content = content + "\n"
		asset.Reviewer = reviewer
		asset.ReviewNote = req.Note
		asset.UpdatedAt = time.Now()
		return asset, nil
	default:
		return nil, fmt.Errorf("不支持的审计动作")
	}

	if err := s.store.UpdateRuntimeLearningAssetReview(ctx, id, status, reviewer, req.Note); err != nil {
		return nil, err
	}
	asset.Status = status
	asset.Reviewer = reviewer
	asset.ReviewNote = req.Note
	asset.UpdatedAt = time.Now()
	return asset, nil
}

func (s *Service) AssistRuntimeLearningEdit(ctx context.Context, id string, req AssistRuntimeLearningEditRequest) (string, error) {
	asset, err := s.store.GetRuntimeLearningAsset(ctx, id)
	if err != nil {
		return "", err
	}
	req.Instruction = strings.TrimSpace(req.Instruction)
	if req.Instruction == "" {
		return "", fmt.Errorf("请先输入修改要求")
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		content = strings.TrimSpace(asset.Content)
	}
	if content == "" {
		return "", fmt.Errorf("内容为空，无法辅助修改")
	}
	if s.agentSpec == nil {
		return "", fmt.Errorf("未配置 AI 模型")
	}
	spec := s.agentSpec()
	if spec.APIURL == "" || spec.APIToken == "" || spec.DefaultModel == "" {
		return "", fmt.Errorf("未配置 API Base URL、Token 或模型")
	}
	return runAIRuntimeLearningEdit(ctx, spec, asset, content, req.Instruction)
}

func (s *Service) AssistRuntimeLearningEditStream(ctx context.Context, id string, req AssistRuntimeLearningEditRequest, emit func(ManualDraftStreamEvent) error) error {
	asset, err := s.store.GetRuntimeLearningAsset(ctx, id)
	if err != nil {
		return err
	}
	req.Instruction = strings.TrimSpace(req.Instruction)
	if req.Instruction == "" {
		return fmt.Errorf("请先输入修改要求")
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		content = strings.TrimSpace(asset.Content)
	}
	if content == "" {
		return fmt.Errorf("内容为空，无法辅助修改")
	}
	if s.agentSpec == nil {
		return fmt.Errorf("未配置 AI 模型")
	}
	spec := s.agentSpec()
	if spec.APIURL == "" || spec.APIToken == "" || spec.DefaultModel == "" {
		return fmt.Errorf("未配置 API Base URL、Token 或模型")
	}
	raw := ""
	result, err := runAIRuntimeLearningEditStream(ctx, spec, asset, content, req.Instruction, func(delta string) error {
		if delta == "" {
			return nil
		}
		raw += delta
		return emit(ManualDraftStreamEvent{Type: "delta", Delta: delta, Content: raw})
	})
	if err != nil {
		if strings.TrimSpace(raw) != "" {
			return err
		}
		if emitErr := emit(ManualDraftStreamEvent{Type: "status", Content: "流式连接中断，已切换为普通生成模式，请稍候。"}); emitErr != nil {
			return emitErr
		}
		result, err = runAIRuntimeLearningEdit(ctx, spec, asset, content, req.Instruction)
		if err != nil {
			return err
		}
	}
	return emit(ManualDraftStreamEvent{Type: "done", Content: result})
}

func (s *Service) ListHermesLearningAssets(ctx context.Context, status model.HermesLearningStatus) ([]*model.HermesLearningAsset, error) {
	return s.ListRuntimeLearningAssets(ctx, status)
}

func (s *Service) ReviewHermesLearningAsset(ctx context.Context, id string, req ReviewHermesLearningRequest, reviewer string) (*model.HermesLearningAsset, error) {
	return s.ReviewRuntimeLearningAsset(ctx, id, req, reviewer)
}

func (s *Service) AssistHermesLearningEdit(ctx context.Context, id string, req AssistHermesLearningEditRequest) (string, error) {
	return s.AssistRuntimeLearningEdit(ctx, id, req)
}

func (s *Service) AssistHermesLearningEditStream(ctx context.Context, id string, req AssistHermesLearningEditRequest, emit func(ManualDraftStreamEvent) error) error {
	return s.AssistRuntimeLearningEditStream(ctx, id, req, emit)
}

// ListLearningJobs 列出 AI 学习任务执行历史。
func (s *Service) ListLearningJobs(ctx context.Context) ([]*model.LearningJob, error) {
	jobs, err := s.store.ListLearningJobs(ctx, 100)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	for _, job := range jobs {
		if job.Status != model.LearningJobStatusRunning || now.Sub(job.StartedAt) <= 5*time.Minute {
			continue
		}
		finished := now
		job.Status = model.LearningJobStatusFailed
		job.Error = "任务异常中断，请重新触发"
		job.FinishedAt = &finished
		if err := s.store.UpdateLearningJob(ctx, job); err != nil {
			s.logger.Warn("failed to mark stale learning job", zap.String("jobID", job.ID), zap.Error(err))
		}
	}
	return jobs, nil
}

// RunLearningJobNow 立即执行一次 AI 历史会话学习任务。
func (s *Service) RunLearningJobNow(ctx context.Context) error {
	job, err := s.createLearningJob(ctx)
	if err != nil {
		return err
	}
	_, err = s.runLearningJob(ctx, job)
	return err
}

// StartLearningJobNow 异步启动一次 AI 历史会话学习任务，立即返回任务记录。
func (s *Service) StartLearningJobNow(ctx context.Context) (*model.LearningJob, error) {
	job, err := s.createLearningJob(ctx)
	if err != nil {
		return nil, err
	}
	go func(job *model.LearningJob) {
		runCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := s.runLearningJob(runCtx, job); err != nil {
			s.logger.Warn("manual AI learning job failed", zap.String("jobID", job.ID), zap.Error(err))
		}
	}(job)
	return job, nil
}

// CreateManualDraft 根据管理员描述生成一条待审批候选知识。
func (s *Service) CreateManualDraft(ctx context.Context, req ManualDraftRequest) (*model.CandidateAsset, error) {
	req.Description = strings.TrimSpace(req.Description)
	if req.Description == "" && len(req.Images) == 0 {
		return nil, fmt.Errorf("请先输入知识描述或上传图片")
	}
	req.PublishTargets = model.NormalizeKnowledgePublishTargets(req.PublishTargets)
	if len(req.Images) > 5 {
		return nil, fmt.Errorf("最多上传 5 张图片")
	}
	for _, img := range req.Images {
		if !strings.HasPrefix(img.MimeType, "image/") || img.Data == "" {
			return nil, fmt.Errorf("图片参数无效")
		}
	}
	if s.agentSpec == nil {
		return nil, fmt.Errorf("未配置 AI 模型")
	}
	spec := s.agentSpec()
	if len(req.Images) > 0 && !spec.SupportsMultimodal {
		return nil, fmt.Errorf("当前启用的模型不支持图片输入，请切换到支持多模态的模型后再上传图片证据")
	}
	if spec.APIURL == "" || spec.APIToken == "" || spec.DefaultModel == "" {
		return nil, fmt.Errorf("未配置 API Base URL、Token 或模型")
	}

	draft, err := runAIManualDraft(ctx, spec, req)
	if err != nil {
		return nil, err
	}
	return s.createCandidateFromManualDraft(ctx, req, draft)
}

func (s *Service) createCandidateFromManualDraft(ctx context.Context, req ManualDraftRequest, draft *aiLearningCandidate) (*model.CandidateAsset, error) {
	now := time.Now()
	evidence, _ := json.Marshal(map[string]any{
		"source":      "manual_ai_draft",
		"description": truncate(req.Description, 1000),
		"imageCount":  len(req.Images),
		"reason":      draft.Reason,
	})
	cand := &model.CandidateAsset{
		ID:             uuid.New().String(),
		AssetType:      model.CandidateAssetKnowledge,
		PublishTargets: req.PublishTargets,
		Title:          truncate(draft.Title, 120),
		Question:       truncate(draft.Question, 500),
		Content:        strings.TrimSpace(draft.Content),
		Evidence:       string(evidence),
		Confidence:     draft.Confidence,
		Status:         model.CandidateStatusPending,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if cand.Title == "" {
		cand.Title = "人工录入知识"
	}
	if cand.Content == "" {
		return nil, fmt.Errorf("AI 未生成有效知识内容")
	}
	if cand.Confidence <= 0 {
		cand.Confidence = 0.7
	}
	if cand.Confidence > 1 {
		cand.Confidence = 1
	}
	if err := s.store.CreateCandidate(ctx, cand); err != nil {
		return nil, err
	}
	return cand, nil
}

// CreateManualDraftStream 流式生成候选知识，并在完成后写入候选池。
func (s *Service) CreateManualDraftStream(ctx context.Context, req ManualDraftRequest, emit func(ManualDraftStreamEvent) error) error {
	req.Description = strings.TrimSpace(req.Description)
	if req.Description == "" && len(req.Images) == 0 {
		return fmt.Errorf("请先输入知识描述或上传图片")
	}
	req.PublishTargets = model.NormalizeKnowledgePublishTargets(req.PublishTargets)
	if len(req.Images) > 5 {
		return fmt.Errorf("最多上传 5 张图片")
	}
	for _, img := range req.Images {
		if !strings.HasPrefix(img.MimeType, "image/") || img.Data == "" {
			return fmt.Errorf("图片参数无效")
		}
	}
	if s.agentSpec == nil {
		return fmt.Errorf("未配置 AI 模型")
	}
	spec := s.agentSpec()
	if len(req.Images) > 0 && !spec.SupportsMultimodal {
		return fmt.Errorf("当前启用的模型不支持图片输入，请切换到支持多模态的模型后再上传图片证据")
	}
	if spec.APIURL == "" || spec.APIToken == "" || spec.DefaultModel == "" {
		return fmt.Errorf("未配置 API Base URL、Token 或模型")
	}
	raw := ""
	draft, raw, err := runAIManualDraftStream(ctx, spec, req, func(delta string) error {
		if delta == "" {
			return nil
		}
		raw += delta
		return emit(ManualDraftStreamEvent{Type: "delta", Delta: delta, Content: raw})
	})
	if err != nil {
		if strings.TrimSpace(raw) != "" {
			return err
		}
		if emitErr := emit(ManualDraftStreamEvent{Type: "status", Content: "流式连接中断，已切换为普通生成模式，请稍候。"}); emitErr != nil {
			return emitErr
		}
		draft, err = runAIManualDraft(ctx, spec, req)
		if err != nil {
			return err
		}
		raw = aiManualDraftDisplayContent(draft)
	}
	cand, err := s.createCandidateFromManualDraft(ctx, req, draft)
	if err != nil {
		return err
	}
	return emit(ManualDraftStreamEvent{Type: "done", Content: raw, Candidate: cand})
}

// UpdateCandidate 编辑待审批候选资产的内容
func (s *Service) UpdateCandidate(ctx context.Context, id string, in *model.CandidateAsset) (*model.CandidateAsset, error) {
	cand, err := s.store.GetCandidate(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("候选资产不存在: %w", err)
	}
	if cand.Status != model.CandidateStatusPending {
		return nil, fmt.Errorf("只能编辑待审批的候选资产")
	}
	if in.AssetType != "" {
		cand.AssetType = in.AssetType
	}
	if len(in.PublishTargets) > 0 {
		cand.PublishTargets = model.NormalizeKnowledgePublishTargets(in.PublishTargets)
	}
	if in.Title != "" {
		cand.Title = in.Title
	}
	cand.Question = in.Question
	cand.Content = in.Content
	if err := s.store.UpdateCandidate(ctx, cand); err != nil {
		return nil, err
	}
	return cand, nil
}

// ReviewCandidate 审批：approve 通过并发布到正式知识；reject 拒绝
func (s *Service) ReviewCandidate(ctx context.Context, id string, approve bool, reviewer, note string) (*model.CandidateAsset, error) {
	cand, err := s.store.GetCandidate(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("候选资产不存在: %w", err)
	}
	if cand.Status != model.CandidateStatusPending {
		return nil, fmt.Errorf("该候选资产已审批")
	}
	cand.Reviewer = reviewer
	cand.ReviewNote = strings.TrimSpace(note)
	if approve {
		cand.Status = model.CandidateStatusApproved
		// 仅在审批通过时由发布动作写入正式知识（生产 Agent 只读发布后的知识）
		if err := s.publishApproved(cand); err != nil {
			return nil, fmt.Errorf("发布正式知识失败: %w", err)
		}
	} else {
		cand.Status = model.CandidateStatusRejected
	}
	if err := s.store.UpdateCandidate(ctx, cand); err != nil {
		return nil, err
	}
	return cand, nil
}

// publishApproved 把审批通过的资产追加到 Runtime 工作目录。
func (s *Service) publishApproved(a *model.CandidateAsset) error {
	targets := model.NormalizeKnowledgePublishTargets(a.PublishTargets)
	for _, target := range targets {
		switch target {
		case model.KnowledgePublishLocal:
			if err := s.publishApprovedLocal(a); err != nil {
				return err
			}
		case model.KnowledgePublishSkill:
			if err := s.publishApprovedSkill(a); err != nil {
				return err
			}
		case model.KnowledgePublishKnowledgeBase:
			return fmt.Errorf("外部知识库写入器暂未配置")
		}
	}
	return nil
}

func (s *Service) publishApprovedLocal(a *model.CandidateAsset) error {
	if err := os.MkdirAll(s.runtimeHome, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.runtimeHome, ApprovedFileName)
	existing, _ := os.ReadFile(path)
	content := string(existing)
	if content == "" {
		content = "# 正式知识（经人工审批发布，回答相关问题时可参考；每条均有来源依据）\n\n"
	}
	entry := fmt.Sprintf("## %s\n", a.Title)
	if a.Question != "" {
		entry += fmt.Sprintf("- 问题：%s\n", a.Question)
	}
	entry += fmt.Sprintf("- 答案：%s\n- 审批：%s @ %s\n",
		a.Content, a.Reviewer, time.Now().Format("2006-01-02 15:04"))
	content += "\n" + entry
	return os.WriteFile(path, []byte(content), 0o644)
}

func (s *Service) publishApprovedSkill(a *model.CandidateAsset) error {
	name := knowledgeSlug(a.Title)
	if name == "" {
		name = a.ID
	}
	dir := filepath.Join(s.runtimeHome, "skills", "callme-approved", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf(`# %s

Use this skill when the user asks about this approved Callme knowledge.

## Approved Knowledge

`, a.Title)
	if a.Question != "" {
		body += fmt.Sprintf("Question: %s\n\n", a.Question)
	}
	body += strings.TrimSpace(a.Content)
	body += fmt.Sprintf("\n\n---\nApproved by %s at %s.\n", a.Reviewer, time.Now().Format("2006-01-02 15:04"))
	return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644)
}

// findUserQuestion 找到该助手回答对应的上一条用户提问
func (s *Service) findUserQuestion(ctx context.Context, assistantMsg *model.Message) string {
	msgs, err := s.store.ListMessages(ctx, assistantMsg.SessionID)
	if err != nil {
		return ""
	}
	question := ""
	for _, m := range msgs {
		if m.ID == assistantMsg.ID {
			break
		}
		if m.Role == model.MessageRoleUser {
			question = m.Content
		}
	}
	return question
}

// ApprovedPath 正式知识文件路径（注入系统提示词用）
func (s *Service) ApprovedPath() string {
	return filepath.Join(s.runtimeHome, ApprovedFileName)
}

// ReadApproved 读取当前正式知识内容
func (s *Service) ReadApproved() string {
	data, err := os.ReadFile(s.ApprovedPath())
	if err != nil {
		return ""
	}
	return string(data)
}

func truncate(s string, n int) string {
	r := []rune(strings.ReplaceAll(s, "\n", " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

func knowledgeSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r >= '\u4e00' && r <= '\u9fff':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(truncate(b.String(), 80), "-")
}

type runtimeLearningFile struct {
	assetType model.RuntimeLearningAssetType
	hash      string
	content   string
}

type runtimeLearningProvider interface {
	AgentType() string
	Scan(home string) (map[string]runtimeLearningFile, error)
}

func (s *Service) runtimeLearningProvider() runtimeLearningProvider {
	spec := agent.AgentSpec{Type: "hermes"}
	if s.agentSpec != nil {
		spec = s.agentSpec()
	}
	switch strings.TrimSpace(spec.Type) {
	case "", "hermes":
		return hermesRuntimeLearningProvider{}
	default:
		return nil
	}
}

type hermesRuntimeLearningProvider struct{}

func (hermesRuntimeLearningProvider) AgentType() string { return "hermes" }

func (hermesRuntimeLearningProvider) Scan(home string) (map[string]runtimeLearningFile, error) {
	result := map[string]runtimeLearningFile{}
	roots := []struct {
		root      string
		assetType model.RuntimeLearningAssetType
	}{
		{root: filepath.Join(home, "skills"), assetType: model.RuntimeLearningAssetSkill},
		{root: filepath.Join(home, "memories"), assetType: model.RuntimeLearningAssetMemory},
	}
	for _, spec := range roots {
		info, err := os.Stat(spec.root)
		if err != nil || !info.IsDir() {
			continue
		}
		if err := filepath.WalkDir(spec.root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() || !isRuntimeLearningAssetFile(spec.root, path, spec.assetType) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			sum := sha256.Sum256(data)
			abs, err := filepath.Abs(path)
			if err != nil {
				abs = path
			}
			result[abs] = runtimeLearningFile{
				assetType: spec.assetType,
				hash:      hex.EncodeToString(sum[:]),
				content:   string(data),
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func scanHermesLearningFiles(home string) (map[string]runtimeLearningFile, error) {
	return hermesRuntimeLearningProvider{}.Scan(home)
}

func isRuntimeLearningAssetFile(root, path string, assetType model.RuntimeLearningAssetType) bool {
	if strings.HasSuffix(path, ".lock") {
		return false
	}
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") {
		return false
	}
	switch assetType {
	case model.RuntimeLearningAssetSkill:
		// Hermes skill 是一个目录资产，只有根 SKILL.md 才代表这个 skill。
		return base == "SKILL.md"
	case model.RuntimeLearningAssetMemory:
		if filepath.Ext(base) != ".md" {
			return false
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return false
		}
		return !strings.Contains(rel, string(filepath.Separator))
	default:
		return false
	}
}

type aiLearningCandidate struct {
	Title      string  `json:"title"`
	Question   string  `json:"question"`
	Content    string  `json:"content"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

type aiLearningResponse struct {
	Candidates []aiLearningCandidate `json:"candidates"`
}

func runAILearning(ctx context.Context, spec agent.AgentSpec, transcript string) ([]aiLearningCandidate, error) {
	baseURL := strings.TrimRight(spec.APIURL, "/")
	reqBody := map[string]any{
		"model": spec.DefaultModel,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": "你是 Callme 的客服知识沉淀助手。只能基于给定历史会话提出候选知识建议，不要编造事实。输出严格 JSON，格式为 {\"candidates\":[{\"title\":\"...\",\"question\":\"...\",\"content\":\"...\",\"confidence\":0.0,\"reason\":\"...\"}]}。只输出证据充分、可复用的候选，最多 5 条。",
			},
			{
				"role":    "user",
				"content": "请从以下历史会话中挖掘可进入候选知识池的知识。所有建议仍需人工审批。\n\n" + transcript,
			},
		},
		"temperature": 0.2,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+spec.APIToken)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("AI 学习请求失败: HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("AI 学习没有返回候选")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	var out aiLearningResponse
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return nil, fmt.Errorf("解析 AI 学习结果失败: %w", err)
	}
	if len(out.Candidates) > 5 {
		out.Candidates = out.Candidates[:5]
	}
	for i := range out.Candidates {
		if out.Candidates[i].Confidence <= 0 {
			out.Candidates[i].Confidence = 0.5
		}
		if out.Candidates[i].Confidence > 1 {
			out.Candidates[i].Confidence = 1
		}
	}
	return out.Candidates, nil
}

func runAIManualDraft(ctx context.Context, spec agent.AgentSpec, req ManualDraftRequest) (*aiLearningCandidate, error) {
	baseURL := strings.TrimRight(spec.APIURL, "/")
	messages := buildManualDraftMessages(req)
	reqBody := map[string]any{
		"model":       spec.DefaultModel,
		"messages":    messages,
		"temperature": 0.2,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+spec.APIToken)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("人工知识整理请求失败: HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("人工知识整理没有返回内容")
	}
	out, err := parseAIManualDraftContent(parsed.Choices[0].Message.Content)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func buildManualDraftMessages(req ManualDraftRequest) []map[string]any {
	content := []map[string]any{
		{
			"type": "text",
			"text": "请把管理员提供的原始描述整理成一篇完整、可审批的客服知识。只基于输入内容和图片，不要编造事实。输出严格 JSON，格式为 {\"title\":\"...\",\"question\":\"...\",\"content\":\"...\",\"confidence\":0.0,\"reason\":\"...\"}。content 使用 Markdown，结构清晰，包含适用场景、处理步骤、注意事项；如果证据不足，要在内容里标注待确认点。",
		},
		{
			"type": "text",
			"text": fmt.Sprintf("发布目标：%v\n原始描述：\n%s", req.PublishTargets, req.Description),
		},
	}
	for _, img := range req.Images {
		content = append(content, map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": fmt.Sprintf("data:%s;base64,%s", img.MimeType, img.Data),
			},
		})
	}
	return []map[string]any{
		{
			"role":    "system",
			"content": "你是 Callme 的人工知识录入助手，负责把管理员的零散描述和图片整理为待审批知识草稿。",
		},
		{
			"role":    "user",
			"content": content,
		},
	}
}

func parseAIManualDraftContent(contentText string) (*aiLearningCandidate, error) {
	contentText = strings.TrimSpace(contentText)
	contentText = strings.TrimPrefix(contentText, "```json")
	contentText = strings.TrimPrefix(contentText, "```")
	contentText = strings.TrimSuffix(contentText, "```")
	contentText = strings.TrimSpace(contentText)
	var out aiLearningCandidate
	if err := json.Unmarshal([]byte(contentText), &out); err != nil {
		return nil, fmt.Errorf("解析人工知识整理结果失败: %w", err)
	}
	return &out, nil
}

func aiManualDraftDisplayContent(draft *aiLearningCandidate) string {
	if draft == nil {
		return ""
	}
	var b strings.Builder
	if strings.TrimSpace(draft.Title) != "" {
		b.WriteString("# ")
		b.WriteString(strings.TrimSpace(draft.Title))
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(draft.Question) != "" {
		b.WriteString("**建议问题：** ")
		b.WriteString(strings.TrimSpace(draft.Question))
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(draft.Content) != "" {
		b.WriteString(strings.TrimSpace(draft.Content))
		b.WriteString("\n\n")
	}
	if draft.Confidence > 0 {
		b.WriteString("**置信度：** ")
		b.WriteString(fmt.Sprintf("%.0f%%", draft.Confidence*100))
		b.WriteString("\n\n")
	}
	if strings.TrimSpace(draft.Reason) != "" {
		b.WriteString("**生成依据：** ")
		b.WriteString(strings.TrimSpace(draft.Reason))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func runAIManualDraftStream(ctx context.Context, spec agent.AgentSpec, req ManualDraftRequest, onDelta func(string) error) (*aiLearningCandidate, string, error) {
	baseURL := strings.TrimRight(spec.APIURL, "/")
	reqBody := map[string]any{
		"model":       spec.DefaultModel,
		"messages":    buildManualDraftMessages(req),
		"temperature": 0.2,
		"stream":      true,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+spec.APIToken)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("人工知识整理请求失败: HTTP %d", resp.StatusCode)
	}

	var raw strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		delta := parseOpenAIStreamDelta(payload)
		if delta == "" {
			continue
		}
		raw.WriteString(delta)
		if onDelta != nil {
			if err := onDelta(delta); err != nil {
				return nil, raw.String(), err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, raw.String(), err
	}
	content := raw.String()
	draft, err := parseAIManualDraftContent(content)
	if err != nil {
		return nil, content, err
	}
	return draft, content, nil
}

func parseOpenAIStreamDelta(payload string) string {
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content any `json:"content"`
			} `json:"delta"`
			Message struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil || len(chunk.Choices) == 0 {
		return ""
	}
	content := chunk.Choices[0].Delta.Content
	if content == nil {
		content = chunk.Choices[0].Message.Content
	}
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if text, ok := m["text"].(string); ok {
					b.WriteString(text)
				}
			}
		}
		return b.String()
	default:
		return ""
	}
}

func runAIRuntimeLearningEdit(ctx context.Context, spec agent.AgentSpec, asset *model.RuntimeLearningAsset, content, instruction string) (string, error) {
	baseURL := strings.TrimRight(spec.APIURL, "/")
	reqBody := map[string]any{
		"model":       spec.DefaultModel,
		"messages":    buildRuntimeLearningEditMessages(asset, content, instruction),
		"temperature": 0.2,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+spec.APIToken)

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Runtime 自学习内容辅助修改失败: HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("AI 没有返回修订内容")
	}
	out := strings.TrimSpace(parsed.Choices[0].Message.Content)
	out = strings.TrimPrefix(out, "```markdown")
	out = strings.TrimPrefix(out, "```md")
	out = strings.TrimPrefix(out, "```")
	out = strings.TrimSuffix(out, "```")
	out = strings.TrimSpace(out)
	if out == "" {
		return "", fmt.Errorf("AI 返回内容为空")
	}
	return out, nil
}

func buildRuntimeLearningEditMessages(asset *model.RuntimeLearningAsset, content, instruction string) []map[string]string {
	agentType := ""
	assetType := model.RuntimeLearningAssetType("")
	if asset != nil {
		agentType = asset.AgentType
		assetType = asset.AssetType
	}
	if agentType == "" {
		agentType = "unknown"
	}
	return []map[string]string{
		{
			"role":    "system",
			"content": "你是 Callme 的 Agent Runtime 自学习审计助手。你只负责根据人工修改要求修订给定 Markdown 文件，不能编造事实，不能添加未提供的业务结论。只输出修订后的 Markdown 原文，不要输出解释、不要包裹代码块。",
		},
		{
			"role": "user",
			"content": fmt.Sprintf(`Agent 类型：%s
资产类型：%s

人工修改要求：
%s

原始 Markdown：
%s`, agentType, assetType, instruction, content),
		},
	}
}

func runAIRuntimeLearningEditStream(ctx context.Context, spec agent.AgentSpec, asset *model.RuntimeLearningAsset, content, instruction string, onDelta func(string) error) (string, error) {
	baseURL := strings.TrimRight(spec.APIURL, "/")
	reqBody := map[string]any{
		"model":       spec.DefaultModel,
		"messages":    buildRuntimeLearningEditMessages(asset, content, instruction),
		"temperature": 0.2,
		"stream":      true,
	}
	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+spec.APIToken)
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("Runtime 自学习内容辅助修改失败: HTTP %d", resp.StatusCode)
	}
	var raw strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		delta := parseOpenAIStreamDelta(payload)
		if delta == "" {
			continue
		}
		raw.WriteString(delta)
		if onDelta != nil {
			if err := onDelta(delta); err != nil {
				return raw.String(), err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return raw.String(), err
	}
	out := strings.TrimSpace(raw.String())
	out = strings.TrimPrefix(out, "```markdown")
	out = strings.TrimPrefix(out, "```md")
	out = strings.TrimPrefix(out, "```")
	out = strings.TrimSuffix(out, "```")
	out = strings.TrimSpace(out)
	if out == "" {
		return "", fmt.Errorf("AI 返回内容为空")
	}
	return out, nil
}

func runAIHermesLearningEdit(ctx context.Context, spec agent.AgentSpec, assetType model.HermesLearningAssetType, content, instruction string) (string, error) {
	return runAIRuntimeLearningEdit(ctx, spec, &model.RuntimeLearningAsset{AgentType: "hermes", AssetType: assetType}, content, instruction)
}

func buildHermesLearningEditMessages(assetType model.HermesLearningAssetType, content, instruction string) []map[string]string {
	return buildRuntimeLearningEditMessages(&model.RuntimeLearningAsset{AgentType: "hermes", AssetType: assetType}, content, instruction)
}

func runAIHermesLearningEditStream(ctx context.Context, spec agent.AgentSpec, assetType model.HermesLearningAssetType, content, instruction string, onDelta func(string) error) (string, error) {
	return runAIRuntimeLearningEditStream(ctx, spec, &model.RuntimeLearningAsset{AgentType: "hermes", AssetType: assetType}, content, instruction, onDelta)
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
