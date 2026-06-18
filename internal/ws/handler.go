// Package ws WebSocket 推送：流式回答、排队位置、会话状态
// 参考 Colink internal/ws，按"一个连接绑定一个会话"简化
package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"callme/internal/model"
	"callme/internal/repo"
	"callme/internal/service/auth"
	"callme/internal/service/session"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// 云化产品同源部署 + 无登录匿名会话，会话 ID 即凭据
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Handler WebSocket 处理器
type Handler struct {
	manager *session.Manager
	auth    *auth.Service
	store   *repo.Store
	logger  *zap.Logger
}

// NewHandler 创建处理器
func NewHandler(manager *session.Manager, authSvc *auth.Service, store *repo.Store, logger *zap.Logger) *Handler {
	return &Handler{manager: manager, auth: authSvc, store: store, logger: logger}
}

// clientMessage 客户端上行消息
type clientMessage struct {
	Type    string               `json:"type"` // user_message | ping | close | stop
	Content string               `json:"content,omitempty"`
	Images  []model.ImageContent `json:"images,omitempty"`
}

// HandleWebSocket 处理 /ws/:sessionId 连接
func (h *Handler) HandleWebSocket(c *gin.Context) {
	sessionID := c.Param("sessionId")
	user, err := h.auth.UserByToken(c.Request.Context(), c.Query("token"))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return
	}
	sess, err := h.store.GetSession(c.Request.Context(), sessionID)
	if err != nil || (sess.UserID != "" && sess.UserID != user.ID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权限访问该会话"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Error("ws upgrade failed", zap.Error(err))
		return
	}

	var writeMu sync.Mutex
	send := func(ev session.Event) {
		writeMu.Lock()
		defer writeMu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteJSON(ev); err != nil {
			h.logger.Debug("ws write failed", zap.String("sessionID", sessionID), zap.Error(err))
		}
	}

	view, err := h.manager.Subscribe(sessionID, send)
	if err != nil {
		send(session.Event{Type: session.EventError, SessionID: sessionID, Error: err.Error()})
		conn.Close()
		return
	}
	defer func() {
		h.manager.Unsubscribe(sessionID)
		conn.Close()
	}()

	// 连接建立即推送当前状态（含排队位置）
	send(session.Event{Type: session.EventState, SessionID: sessionID, Session: view, Position: view.Position})

	conn.SetReadLimit(48 * 1024 * 1024)
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			h.logger.Debug("ws read closed", zap.String("sessionID", sessionID), zap.Error(err))
			return
		}

		var msg clientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "user_message":
			if msg.Content == "" && len(msg.Images) == 0 {
				continue
			}
			// 异步处理：一轮回答耗时较长，不能阻塞读循环（否则 ping/close 收不到）；
			// 管理器内部 busy 标记保证同会话不会并发执行两轮
			go func(content string, images []model.ImageContent) {
				if err := h.manager.HandleUserMessage(context.Background(), sessionID, content, images); err != nil {
					send(session.Event{Type: session.EventError, SessionID: sessionID, Error: err.Error()})
				}
			}(msg.Content, msg.Images)
		case "ping":
			h.manager.Touch(sessionID)
		case "stop":
			if err := h.manager.StopCurrentTurn(sessionID); err != nil {
				send(session.Event{Type: session.EventError, SessionID: sessionID, Error: err.Error()})
			}
		case "close":
			h.manager.CloseSession(context.Background(), sessionID, model.CloseReasonUser)
			return
		}
	}
}
