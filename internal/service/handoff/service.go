// Package handoff 人工接管/工单转交
// AI 答不了或用户主动转人工时：生成带会话上下文包的工单，落库并 webhook 外发给下游工单系统。
package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"callme/internal/config"
	"callme/internal/model"
	"callme/internal/repo"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Service 工单服务
type Service struct {
	store  *repo.Store
	cfg    config.HandoffConfig
	logger *zap.Logger
	client *http.Client
}

// NewService 创建工单服务
func NewService(store *repo.Store, cfg config.HandoffConfig, logger *zap.Logger) *Service {
	return &Service{store: store, cfg: cfg, logger: logger, client: &http.Client{Timeout: 15 * time.Second}}
}

// CreateTicket 创建转人工工单：打包会话上下文，落库并异步外发
func (s *Service) CreateTicket(ctx context.Context, sessionID, reason string) (*model.Ticket, error) {
	sess, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("会话不存在: %w", err)
	}

	transcript, err := s.buildTranscript(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	ticket := &model.Ticket{
		ID:         uuid.New().String(),
		SessionID:  sessionID,
		Reason:     strings.TrimSpace(reason),
		Transcript: transcript,
		Status:     model.TicketStatusOpen,
		CreatedAt:  time.Now(),
	}
	if err := s.store.CreateTicket(ctx, ticket); err != nil {
		return nil, err
	}

	// webhook 异步外发，不阻塞用户侧响应
	go s.notifyWebhook(ticket, sess)
	return ticket, nil
}

// ListTickets 工单列表
func (s *Service) ListTickets(ctx context.Context, limit int) ([]*model.Ticket, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.store.ListTickets(ctx, limit)
}

// buildTranscript 打包会话上下文（外发给人工客服）
func (s *Service) buildTranscript(ctx context.Context, sessionID string) (string, error) {
	msgs, err := s.store.ListMessages(ctx, sessionID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, m := range msgs {
		role := "用户"
		if m.Role == model.MessageRoleAssistant {
			role = "AI客服"
		}
		fmt.Fprintf(&b, "[%s] %s:\n%s\n\n", m.CreatedAt.Format("15:04:05"), role, m.Content)
	}
	return b.String(), nil
}

// notifyWebhook 外发工单到下游系统（通用 JSON webhook）
func (s *Service) notifyWebhook(ticket *model.Ticket, sess *model.Session) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if s.cfg.WebhookURL == "" {
		s.logger.Info("handoff webhook not configured, ticket kept local", zap.String("ticketID", ticket.ID))
		return
	}

	payload := map[string]any{
		"ticketId":   ticket.ID,
		"sessionId":  ticket.SessionID,
		"reason":     ticket.Reason,
		"transcript": ticket.Transcript,
		"clientId":   sess.ClientID,
		"createdAt":  ticket.CreatedAt.Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.WebhookURL, bytes.NewReader(body))
	if err != nil {
		s.markStatus(ticket.ID, model.TicketStatusFailed)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range s.cfg.WebhookHeaders {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		s.logger.Error("handoff webhook failed", zap.String("ticketID", ticket.ID), zap.Error(err))
		s.markStatus(ticket.ID, model.TicketStatusFailed)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		s.markStatus(ticket.ID, model.TicketStatusNotified)
	} else {
		s.logger.Error("handoff webhook non-2xx", zap.String("ticketID", ticket.ID), zap.Int("status", resp.StatusCode))
		s.markStatus(ticket.ID, model.TicketStatusFailed)
	}
}

func (s *Service) markStatus(ticketID string, status model.TicketStatus) {
	if err := s.store.UpdateTicketStatus(context.Background(), ticketID, status); err != nil {
		s.logger.Error("update ticket status failed", zap.Error(err))
	}
}
