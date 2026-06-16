// Package feedback 反馈与自学习闭环
//
// 链路：用户点赞/点踩/纠错 -> 入库 -> 定时蒸馏任务（单写者，避免并发写记忆冲突）
// 把高价值纠错蒸馏为"学习笔记"写入共享 HERMES_HOME/learning_notes.md。
// Hermes 会话工作目录下的记忆与该笔记共同构成跨会话累积的知识，使系统越用越聪明。
package feedback

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"callme/internal/config"
	"callme/internal/model"
	"callme/internal/repo"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// NotesFileName 学习笔记文件名（位于 HERMES_HOME 下）
const NotesFileName = "learning_notes.md"

// Service 反馈服务
type Service struct {
	store      *repo.Store
	cfg        config.FeedbackConfig
	hermesHome string
	logger     *zap.Logger
	stop       chan struct{}
}

// NewService 创建反馈服务并启动蒸馏任务
func NewService(store *repo.Store, cfg config.FeedbackConfig, hermesHome string, logger *zap.Logger) *Service {
	s := &Service{store: store, cfg: cfg, hermesHome: hermesHome, logger: logger, stop: make(chan struct{})}
	go s.distillLoop()
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

// distillLoop 定时蒸馏（单写者：全系统只有这个 goroutine 写学习笔记）
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
		}
	}
}

// distillOnce 把未处理反馈蒸馏进学习笔记
func (s *Service) distillOnce(ctx context.Context) error {
	pending, err := s.store.ListUndistilledFeedback(ctx, 100)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}

	var entries []string
	processed := make([]string, 0, len(pending))
	for _, f := range pending {
		processed = append(processed, f.ID)
		// 仅蒸馏带纠错内容的负反馈：这是最高价值的学习信号
		if f.Rating != model.FeedbackDown || f.Correction == "" {
			continue
		}
		msg, err := s.store.GetMessage(ctx, f.MessageID)
		if err != nil {
			continue
		}
		question := s.findUserQuestion(ctx, msg)
		entry := fmt.Sprintf("## %s\n- 用户问题：%s\n- 错误回答摘要：%s\n- 正确做法/答案：%s\n",
			f.CreatedAt.Format("2006-01-02 15:04"),
			truncate(question, 200),
			truncate(msg.Content, 200),
			f.Correction)
		entries = append(entries, entry)
	}

	if len(entries) > 0 {
		if err := s.appendNotes(entries); err != nil {
			return err
		}
		s.logger.Info("feedback distilled into learning notes",
			zap.Int("newEntries", len(entries)), zap.Int("processed", len(processed)))
	}

	return s.store.MarkFeedbackDistilled(ctx, processed)
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

// appendNotes 追加学习笔记并裁剪到最大条数
func (s *Service) appendNotes(entries []string) error {
	if err := os.MkdirAll(s.hermesHome, 0o755); err != nil {
		return err
	}
	notesPath := filepath.Join(s.hermesHome, NotesFileName)

	existing, _ := os.ReadFile(notesPath)
	content := string(existing)
	if content == "" {
		content = "# 客服学习笔记（由用户反馈自动蒸馏，请在回答相关问题时参考）\n\n"
	}
	content += strings.Join(entries, "\n")

	// 按条目数裁剪：保留最新 NotesMaxEntries 条
	parts := strings.Split(content, "\n## ")
	if len(parts)-1 > s.cfg.NotesMaxEntries {
		header := parts[0]
		keep := parts[len(parts)-s.cfg.NotesMaxEntries:]
		content = header + "\n## " + strings.Join(keep, "\n## ")
	}

	return os.WriteFile(notesPath, []byte(content), 0o644)
}

// NotesPath 学习笔记路径（注入系统提示词用）
func (s *Service) NotesPath() string {
	return filepath.Join(s.hermesHome, NotesFileName)
}

// ReadNotes 读取当前学习笔记内容
func (s *Service) ReadNotes() string {
	data, err := os.ReadFile(s.NotesPath())
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
