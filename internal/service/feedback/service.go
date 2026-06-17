// Package feedback 反馈与自学习沙箱
//
// 链路（Phase 1：沙箱 + 审批闸门）：
//
//	用户点踩/纠错 -> 反馈入库 -> 定时蒸馏(单写者) -> 候选资产池(pending)
//	  -> 管理员审批 -> 通过后发布到 HERMES_HOME/approved_knowledge.md(正式知识，生产 Agent 只读)
//
// 关键原则：任何会话提炼内容都不会自动进入生产回答链路；只有审批通过的资产才发布为正式知识，
// 杜绝"错误知识自增强"。生产 Agent 仅参考 approved_knowledge.md，不读候选池。
package feedback

import (
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
	"go.uber.org/zap"
)

// ApprovedFileName 正式知识文件名（位于 HERMES_HOME 下，生产 Agent 只读此文件）
const ApprovedFileName = "approved_knowledge.md"

// Service 反馈与自学习沙箱服务
type Service struct {
	store      *repo.Store
	cfg        config.FeedbackConfig
	hermesHome string
	agentSpec  func() agent.AgentSpec
	logger     *zap.Logger
	stop       chan struct{}
}

// NewService 创建服务并启动蒸馏任务
func NewService(store *repo.Store, cfg config.FeedbackConfig, hermesHome string, agentSpec func() agent.AgentSpec, logger *zap.Logger) *Service {
	s := &Service{store: store, cfg: cfg, hermesHome: hermesHome, agentSpec: agentSpec, logger: logger, stop: make(chan struct{})}
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
	AssetType   model.CandidateAssetType `json:"assetType"`
	Description string                   `json:"description"`
	Images      []model.ImageContent     `json:"images,omitempty"`
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
	ticker := time.NewTicker(s.cfg.DistillInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			if err := s.distillOnce(context.Background()); err != nil {
				s.logger.Error("feedback distill failed", zap.Error(err))
			}
			if err := s.aiLearnFromHistory(context.Background()); err != nil {
				s.logger.Warn("AI learning job failed", zap.Error(err))
			}
		}
	}
}

func (s *Service) auditLoop() {
	if s.hermesHome == "" {
		return
	}
	if err := s.auditHermesLearning(context.Background()); err != nil {
		s.logger.Warn("Hermes learning audit failed", zap.Error(err))
	}
	ticker := time.NewTicker(s.cfg.AuditInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			if err := s.auditHermesLearning(context.Background()); err != nil {
				s.logger.Warn("Hermes learning audit failed", zap.Error(err))
			}
		}
	}
}

// auditHermesLearning 扫描 Hermes 自学习资产（skills / memories）并记录 diff。
// 它只创建审计记录，不直接发布正式知识；有价值内容后续可人工转入候选资产池。
func (s *Service) auditHermesLearning(ctx context.Context) error {
	current, err := scanHermesLearningFiles(s.hermesHome)
	if err != nil {
		return err
	}
	latest, err := s.store.ListLatestHermesLearningAssets(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	created := 0
	for path, file := range current {
		prev, ok := latest[path]
		if ok && prev.ContentHash == file.hash && prev.ChangeType != model.HermesLearningChangeDeleted {
			continue
		}
		changeType := model.HermesLearningChangeNew
		if ok {
			changeType = model.HermesLearningChangeModified
		}
		riskFlags := classifyHermesLearningRisk(file.content)
		if strings.Contains(path, string(filepath.Separator)+"_quarantine"+string(filepath.Separator)) {
			riskFlags = append(riskFlags, "quarantined")
		}
		if err := s.store.CreateHermesLearningAsset(ctx, &model.HermesLearningAsset{
			ID:          uuid.New().String(),
			AssetType:   file.assetType,
			Path:        path,
			ContentHash: file.hash,
			Content:     truncate(file.content, 20000),
			ChangeType:  changeType,
			RiskFlags:   encodeRiskFlags(riskFlags),
			Status:      model.HermesLearningStatusPendingReview,
			CreatedAt:   now,
			UpdatedAt:   now,
		}); err != nil {
			return err
		}
		created++
	}

	for path, prev := range latest {
		if prev.ChangeType == model.HermesLearningChangeDeleted {
			continue
		}
		if _, ok := current[path]; ok {
			continue
		}
		if err := s.store.CreateHermesLearningAsset(ctx, &model.HermesLearningAsset{
			ID:          uuid.New().String(),
			AssetType:   prev.AssetType,
			Path:        path,
			ContentHash: "",
			Content:     "",
			ChangeType:  model.HermesLearningChangeDeleted,
			RiskFlags:   encodeRiskFlags([]string{"deleted"}),
			Status:      model.HermesLearningStatusPendingReview,
			CreatedAt:   now,
			UpdatedAt:   now,
		}); err != nil {
			return err
		}
		created++
	}

	if created > 0 {
		s.logger.Info("Hermes learning audit records created", zap.Int("records", created))
	}
	return nil
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
			title = "未命名候选 FAQ"
		}
		now := time.Now()
		cand := &model.CandidateAsset{
			ID:               uuid.New().String(),
			AssetType:        model.CandidateAssetFAQ, // 点踩纠错 → 标准问答候选
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
	now := time.Now()
	job := &model.LearningJob{
		ID:        uuid.New().String(),
		Source:    "history",
		Status:    model.LearningJobStatusRunning,
		StartedAt: now,
	}
	if err := s.store.CreateLearningJob(ctx, job); err != nil {
		return err
	}
	finish := func(status model.LearningJobStatus, errText string) error {
		done := time.Now()
		job.Status = status
		job.Error = errText
		job.FinishedAt = &done
		return s.store.UpdateLearningJob(ctx, job)
	}

	if s.agentSpec == nil {
		return finish(model.LearningJobStatusSkipped, "未配置 AI 模型")
	}
	spec := s.agentSpec()
	if spec.APIURL == "" || spec.APIToken == "" || spec.DefaultModel == "" {
		return finish(model.LearningJobStatusSkipped, "未配置 API Base URL、Token 或模型")
	}

	start := now.Add(-24 * time.Hour)
	sessions, _, err := s.store.ListClosedSessions(ctx, &start, &now, "", 1, 20)
	if err != nil {
		_ = finish(model.LearningJobStatusFailed, err.Error())
		return err
	}
	job.InputSessions = len(sessions)
	if len(sessions) == 0 {
		return finish(model.LearningJobStatusSkipped, "最近 24 小时没有可挖掘的历史会话")
	}

	transcript, sourceIDs := s.buildLearningTranscript(ctx, sessions)
	if transcript == "" {
		return finish(model.LearningJobStatusSkipped, "历史会话没有有效消息")
	}

	candidates, err := runAILearning(ctx, spec, transcript)
	if err != nil {
		_ = finish(model.LearningJobStatusFailed, err.Error())
		return err
	}
	for _, c := range candidates {
		now := time.Now()
		if c.Title == "" || c.Content == "" {
			continue
		}
		assetType := model.CandidateAssetFAQ
		if c.AssetType == string(model.CandidateAssetWiki) {
			assetType = model.CandidateAssetWiki
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
			AssetType:       assetType,
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
			return err
		}
		job.OutputAssets++
	}
	return finish(model.LearningJobStatusSucceeded, "")
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

// ListHermesLearningAssets 列出 Hermes 自学习审计记录。
func (s *Service) ListHermesLearningAssets(ctx context.Context, status model.HermesLearningStatus) ([]*model.HermesLearningAsset, error) {
	return s.store.ListHermesLearningAssets(ctx, status, 200)
}

// ListLearningJobs 列出 AI 学习任务执行历史。
func (s *Service) ListLearningJobs(ctx context.Context) ([]*model.LearningJob, error) {
	return s.store.ListLearningJobs(ctx, 100)
}

// RunLearningJobNow 立即执行一次 AI 历史会话学习任务。
func (s *Service) RunLearningJobNow(ctx context.Context) error {
	return s.aiLearnFromHistory(ctx)
}

// CreateManualDraft 根据管理员描述生成一条待审批候选知识。
func (s *Service) CreateManualDraft(ctx context.Context, req ManualDraftRequest) (*model.CandidateAsset, error) {
	req.Description = strings.TrimSpace(req.Description)
	if req.Description == "" && len(req.Images) == 0 {
		return nil, fmt.Errorf("请先输入知识描述或上传图片")
	}
	if req.AssetType != model.CandidateAssetFAQ && req.AssetType != model.CandidateAssetWiki {
		req.AssetType = model.CandidateAssetWiki
	}
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
	if spec.APIURL == "" || spec.APIToken == "" || spec.DefaultModel == "" {
		return nil, fmt.Errorf("未配置 API Base URL、Token 或模型")
	}

	draft, err := runAIManualDraft(ctx, spec, req)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	evidence, _ := json.Marshal(map[string]any{
		"source":      "manual_ai_draft",
		"description": truncate(req.Description, 1000),
		"imageCount":  len(req.Images),
		"reason":      draft.Reason,
	})
	cand := &model.CandidateAsset{
		ID:         uuid.New().String(),
		AssetType:  req.AssetType,
		Title:      truncate(draft.Title, 120),
		Question:   truncate(draft.Question, 500),
		Content:    strings.TrimSpace(draft.Content),
		Evidence:   string(evidence),
		Confidence: draft.Confidence,
		Status:     model.CandidateStatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
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
		// 仅在审批通过时由发布动作写入正式知识（生产 Agent 只读此文件）
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

// publishApproved 把审批通过的资产追加到 HERMES_HOME/approved_knowledge.md
func (s *Service) publishApproved(a *model.CandidateAsset) error {
	if err := os.MkdirAll(s.hermesHome, 0o755); err != nil {
		return err
	}
	path := filepath.Join(s.hermesHome, ApprovedFileName)
	existing, _ := os.ReadFile(path)
	content := string(existing)
	if content == "" {
		content = "# 正式知识（经人工审批发布，回答相关问题时可参考；每条均有来源依据）\n\n"
	}
	kind := "FAQ"
	if a.AssetType == model.CandidateAssetWiki {
		kind = "Wiki"
	}
	entry := fmt.Sprintf("## [%s] %s\n", kind, a.Title)
	if a.Question != "" {
		entry += fmt.Sprintf("- 问题：%s\n", a.Question)
	}
	entry += fmt.Sprintf("- 答案：%s\n- 审批：%s @ %s\n",
		a.Content, a.Reviewer, time.Now().Format("2006-01-02 15:04"))
	content += "\n" + entry
	return os.WriteFile(path, []byte(content), 0o644)
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
	return filepath.Join(s.hermesHome, ApprovedFileName)
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

type hermesLearningFile struct {
	assetType model.HermesLearningAssetType
	hash      string
	content   string
}

func scanHermesLearningFiles(home string) (map[string]hermesLearningFile, error) {
	result := map[string]hermesLearningFile{}
	for _, spec := range []struct {
		dir       string
		assetType model.HermesLearningAssetType
	}{
		{dir: "skills", assetType: model.HermesLearningAssetSkill},
		{dir: "memories", assetType: model.HermesLearningAssetMemory},
	} {
		for _, root := range hermesLearningRoots(home, spec.dir) {
			info, err := os.Stat(root)
			if err != nil || !info.IsDir() {
				continue
			}
			if err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil || d.IsDir() || strings.HasSuffix(path, ".lock") {
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
				result[abs] = hermesLearningFile{
					assetType: spec.assetType,
					hash:      hex.EncodeToString(sum[:]),
					content:   string(data),
				}
				return nil
			}); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func hermesLearningRoots(home, dir string) []string {
	roots := []string{filepath.Join(home, dir)}
	quarantine := filepath.Join(home, "_quarantine")
	entries, err := os.ReadDir(quarantine)
	if err != nil {
		return roots
	}
	for _, entry := range entries {
		if entry.IsDir() {
			roots = append(roots, filepath.Join(quarantine, entry.Name(), dir))
		}
	}
	return roots
}

func classifyHermesLearningRisk(content string) []string {
	lower := strings.ToLower(content)
	flags := map[string]struct{}{}
	add := func(flag string) { flags[flag] = struct{}{} }

	for _, marker := range []string{"api_key", "apikey", "token", "password", "secret", "bearer ", "authorization"} {
		if strings.Contains(lower, marker) {
			add("contains_sensitive_data")
			break
		}
	}
	for _, marker := range []string{"客户", "用户", "手机号", "邮箱", "工单", "tenant", "customer"} {
		if strings.Contains(lower, strings.ToLower(marker)) {
			add("contains_customer_context")
			break
		}
	}
	for _, marker := range []string{"价格", "计费", "权限", "合规", "sla", "版本", "配置", "必须", "不能"} {
		if strings.Contains(lower, strings.ToLower(marker)) {
			add("contains_business_fact")
			break
		}
	}
	for _, marker := range []string{"根因", "原因", "导致", "报错", "故障", "token expired", "timeout"} {
		if strings.Contains(lower, strings.ToLower(marker)) {
			add("diagnostic_claim")
			break
		}
	}
	for _, marker := range []string{"通常", "一定", "总是", "所有", "必然", "never", "always"} {
		if strings.Contains(lower, strings.ToLower(marker)) {
			add("over_generalized")
			break
		}
	}

	if len(flags) == 0 {
		return []string{"behavior_only"}
	}
	out := make([]string, 0, len(flags))
	for flag := range flags {
		out = append(out, flag)
	}
	return out
}

func encodeRiskFlags(flags []string) string {
	data, err := json.Marshal(flags)
	if err != nil {
		return "[]"
	}
	return string(data)
}

type aiLearningCandidate struct {
	AssetType  string  `json:"assetType"`
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
				"content": "你是 Callme 的客服知识沉淀助手。只能基于给定历史会话提出候选知识建议，不要编造事实。输出严格 JSON，格式为 {\"candidates\":[{\"assetType\":\"faq|wiki\",\"title\":\"...\",\"question\":\"...\",\"content\":\"...\",\"confidence\":0.0,\"reason\":\"...\"}]}。只输出证据充分、可复用的候选，最多 5 条。",
			},
			{
				"role":    "user",
				"content": "请从以下历史会话中挖掘可进入候选知识池的 FAQ 或 Wiki。所有建议仍需人工审批。\n\n" + transcript,
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
	content := []map[string]any{
		{
			"type": "text",
			"text": "请把管理员提供的原始描述整理成一篇完整、可审批的客服知识。只基于输入内容和图片，不要编造事实。输出严格 JSON，格式为 {\"assetType\":\"faq|wiki\",\"title\":\"...\",\"question\":\"...\",\"content\":\"...\",\"confidence\":0.0,\"reason\":\"...\"}。content 使用 Markdown，结构清晰，包含适用场景、处理步骤、注意事项；如果证据不足，要在内容里标注待确认点。",
		},
		{
			"type": "text",
			"text": fmt.Sprintf("目标类型：%s\n原始描述：\n%s", req.AssetType, req.Description),
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
	reqBody := map[string]any{
		"model": spec.DefaultModel,
		"messages": []map[string]any{
			{
				"role":    "system",
				"content": "你是 Callme 的人工知识录入助手，负责把管理员的零散描述和图片整理为待审批知识草稿。",
			},
			{
				"role":    "user",
				"content": content,
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
	contentText := strings.TrimSpace(parsed.Choices[0].Message.Content)
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

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
