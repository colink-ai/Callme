// Package api HTTP API 路由与 handler
package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"callme/internal/model"
	"callme/internal/repo"
	"callme/internal/service/agent"
	"callme/internal/service/auth"
	"callme/internal/service/feedback"
	"callme/internal/service/handoff"
	"callme/internal/service/session"
	"callme/internal/service/settings"
	"callme/internal/service/stats"
	"callme/internal/ws"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Deps API 依赖集合
type Deps struct {
	Store    *repo.Store
	Sessions *session.Manager
	Settings *settings.Service
	Auth     *auth.Service
	Feedback *feedback.Service
	Handoff  *handoff.Service
	Stats    *stats.Service
	WS       *ws.Handler
	Logger   *zap.Logger
	WebDist  string // 前端构建产物目录（为空则不挂载）
}

// NewRouter 构建 Gin 路由
func NewRouter(d *Deps) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/ws/:sessionId", d.WS.HandleWebSocket)

	v1 := r.Group("/api/v1")
	{
		v1.POST("/auth/register", d.register)
		v1.POST("/auth/login", d.login)
		v1.POST("/auth/logout", d.authRequired(), d.logout)
		v1.GET("/auth/me", d.authRequired(), d.me)

		protected := v1.Group("")
		protected.Use(d.authRequired())
		// 会话
		protected.POST("/sessions", d.createSession)
		protected.GET("/sessions/current", d.currentSession)
		protected.GET("/sessions/history", d.listMySessions)
		protected.GET("/admin/sessions/closed", d.adminRequired(), d.listClosedSessions)
		protected.GET("/sessions/:id", d.getSession)
		protected.GET("/sessions/:id/messages", d.listMessages)
		protected.DELETE("/sessions/:id", d.closeSession)
		protected.DELETE("/sessions/:id/history", d.deleteSessionHistory)
		protected.POST("/sessions/:id/continue", d.continueSession)
		protected.GET("/sessions", d.adminRequired(), d.listLiveSessions) // 监控页：活跃 + 排队

		// 反馈（自学习闭环入口）
		protected.POST("/feedback", d.submitFeedback)
		protected.GET("/learning/notes", d.adminRequired(), d.getLearningNotes)

		// 转人工/工单
		protected.POST("/sessions/:id/handoff", d.createHandoff)
		protected.GET("/tickets", d.adminRequired(), d.listTickets)

		// 设置（模型切换 / 坐席容量）
		protected.GET("/settings/agent", d.adminRequired(), d.getAgentSettings)
		protected.PUT("/settings/agent", d.adminRequired(), d.updateAgentSettings)
		protected.GET("/settings/pool", d.adminRequired(), d.getPoolSettings)
		protected.PUT("/settings/pool", d.adminRequired(), d.updatePoolSettings)
		protected.GET("/agent/types", d.adminRequired(), d.getAgentTypes)
		protected.POST("/agent/health", d.adminRequired(), d.checkAgentHealth)
		protected.GET("/users", d.adminRequired(), d.listUsers)
		protected.PUT("/users/:id/role", d.adminRequired(), d.updateUserRole)
		protected.DELETE("/users/:id", d.adminRequired(), d.deleteUser)

		// 看板
		protected.GET("/stats/overview", d.adminRequired(), d.getStatsOverview)
		protected.GET("/stats/daily", d.adminRequired(), d.getStatsDaily)
		protected.GET("/stats/hot-questions", d.adminRequired(), d.getHotQuestions)
	}

	// 前端静态资源（生产部署：单进程同源服务）
	if d.WebDist != "" {
		r.Static("/assets", d.WebDist+"/assets")
		r.StaticFile("/favicon.svg", d.WebDist+"/favicon.svg")
		r.NoRoute(func(c *gin.Context) {
			if strings.HasPrefix(c.Request.URL.Path, "/api/") {
				c.JSON(http.StatusNotFound, gin.H{"error": "接口不存在"})
				return
			}
			c.File(d.WebDist + "/index.html")
		})
	}

	return r
}

func (d *Deps) authRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := d.Auth.UserByToken(c.Request.Context(), bearerToken(c))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
			return
		}
		c.Set("user", user)
		c.Next()
	}
}

func (d *Deps) adminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if currentUser(c).Role != model.UserRoleAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "仅管理员可访问"})
			return
		}
		c.Next()
	}
}

func bearerToken(c *gin.Context) string {
	authz := c.GetHeader("Authorization")
	if strings.HasPrefix(authz, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
	}
	return c.Query("token")
}

func currentUser(c *gin.Context) *model.User {
	u, _ := c.Get("user")
	user, _ := u.(*model.User)
	return user
}

func (d *Deps) requireSessionAccess(c *gin.Context, sessionID string) (*model.Session, bool) {
	sess, err := d.Store.GetSession(c.Request.Context(), sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "会话不存在"})
		return nil, false
	}
	user := currentUser(c)
	if user.Role != model.UserRoleAdmin && sess.UserID != user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "无权限访问该会话"})
		return nil, false
	}
	return sess, true
}

func (d *Deps) register(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	result, err := d.Auth.Register(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		status := http.StatusBadRequest
		if err == auth.ErrUsernameTaken {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (d *Deps) login(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	result, err := d.Auth.Login(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (d *Deps) logout(c *gin.Context) {
	_ = d.Auth.Logout(c.Request.Context(), bearerToken(c))
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (d *Deps) me(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"user": currentUser(c)})
}

// ---------- 会话 ----------

func (d *Deps) createSession(c *gin.Context) {
	view, err := d.Sessions.CreateSession(c.Request.Context(), currentUser(c))
	if err != nil {
		status := http.StatusInternalServerError
		switch err {
		case session.ErrQueueFull, session.ErrClientBusy:
			status = http.StatusTooManyRequests
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, view)
}

func (d *Deps) currentSession(c *gin.Context) {
	view := d.Sessions.CurrentByUser(currentUser(c).ID)
	if view == nil {
		c.JSON(http.StatusOK, gin.H{"session": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"session": view})
}

func (d *Deps) listMySessions(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	sessions, err := d.Store.ListSessionsByUser(c.Request.Context(), currentUser(c).ID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": sessions})
}

func (d *Deps) getSession(c *gin.Context) {
	sess, ok := d.requireSessionAccess(c, c.Param("id"))
	if !ok {
		return
	}
	c.JSON(http.StatusOK, sess)
}

func (d *Deps) listMessages(c *gin.Context) {
	if _, ok := d.requireSessionAccess(c, c.Param("id")); !ok {
		return
	}
	msgs, err := d.Store.ListMessages(c.Request.Context(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"messages": msgs})
}

func (d *Deps) closeSession(c *gin.Context) {
	if _, ok := d.requireSessionAccess(c, c.Param("id")); !ok {
		return
	}
	reason := model.CloseReasonUser
	if c.Query("by") == "admin" {
		reason = model.CloseReasonAdmin
	}
	if err := d.Sessions.CloseSession(c.Request.Context(), c.Param("id"), reason); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (d *Deps) deleteSessionHistory(c *gin.Context) {
	sess, ok := d.requireSessionAccess(c, c.Param("id"))
	if !ok {
		return
	}
	if sess.Status != model.SessionStatusClosed {
		c.JSON(http.StatusConflict, gin.H{"error": "只能删除已结束的历史会话"})
		return
	}
	if err := d.Store.DeleteClosedSessionCascade(c.Request.Context(), sess.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (d *Deps) continueSession(c *gin.Context) {
	source, ok := d.requireSessionAccess(c, c.Param("id"))
	if !ok {
		return
	}
	if source.Status != model.SessionStatusClosed {
		c.JSON(http.StatusConflict, gin.H{"error": "只能继续已结束的历史会话"})
		return
	}
	view, err := d.Sessions.ContinueSession(c.Request.Context(), currentUser(c), source)
	if err != nil {
		status := http.StatusInternalServerError
		switch err {
		case session.ErrQueueFull, session.ErrClientBusy:
			status = http.StatusTooManyRequests
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, view)
}

func (d *Deps) listLiveSessions(c *gin.Context) {
	active, queued := d.Sessions.Snapshot()
	sessions := sessionsFromViews(active, queued)
	resp := gin.H{"active": active, "queued": queued}
	// 可选带最近已结束会话
	if c.Query("include") == "closed" {
		closed, err := d.Store.ListSessionsByStatus(c.Request.Context(), []model.SessionStatus{model.SessionStatusClosed}, 50)
		if err == nil {
			sessions = append(sessions, closed...)
			resp["closed"] = closed
		}
	}
	if err := d.fillSessionUsernames(c.Request.Context(), sessions); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (d *Deps) listClosedSessions(c *gin.Context) {
	page := parsePositiveInt(c.Query("page"), 1, 1, 100000)
	pageSize := parsePositiveInt(c.Query("pageSize"), 10, 1, 100)
	start, err := parseOptionalTime(c.Query("start"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "开始时间格式不正确"})
		return
	}
	end, err := parseOptionalTime(c.Query("end"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "结束时间格式不正确"})
		return
	}
	if start != nil && end != nil && start.After(*end) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "开始时间不能晚于结束时间"})
		return
	}

	userID := strings.TrimSpace(c.Query("userId"))
	sessions, total, err := d.Store.ListClosedSessions(c.Request.Context(), start, end, userID, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := d.fillSessionUsernames(c.Request.Context(), sessions); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"sessions": sessions,
		"total":    total,
		"page":     page,
		"pageSize": pageSize,
	})
}

func parsePositiveInt(raw string, fallback, min, max int) int {
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func parseOptionalTime(raw string) (*time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func sessionsFromViews(groups ...[]*session.SessionView) []*model.Session {
	var result []*model.Session
	for _, views := range groups {
		for _, view := range views {
			if view != nil && view.Session != nil {
				result = append(result, view.Session)
			}
		}
	}
	return result
}

func (d *Deps) fillSessionUsernames(ctx context.Context, sessions []*model.Session) error {
	seen := make(map[string]struct{})
	ids := make([]string, 0, len(sessions))
	for _, sess := range sessions {
		if sess == nil || sess.UserID == "" {
			continue
		}
		if _, ok := seen[sess.UserID]; ok {
			continue
		}
		seen[sess.UserID] = struct{}{}
		ids = append(ids, sess.UserID)
	}
	usernames, err := d.Store.UsernamesByIDs(ctx, ids)
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		if sess == nil || sess.UserID == "" {
			continue
		}
		sess.Username = usernames[sess.UserID]
	}
	return nil
}

func (d *Deps) listUsers(c *gin.Context) {
	users, err := d.Auth.ListUsers(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

func (d *Deps) updateUserRole(c *gin.Context) {
	var req struct {
		Role model.UserRole `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if err := d.Auth.UpdateRole(c.Request.Context(), c.Param("id"), req.Role); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (d *Deps) deleteUser(c *gin.Context) {
	if err := d.Auth.DeleteUser(c.Request.Context(), currentUser(c).ID, c.Param("id")); err != nil {
		status := http.StatusBadRequest
		if err == auth.ErrCannotDeleteSelf || err == auth.ErrLastAdmin {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------- 反馈 ----------

func (d *Deps) submitFeedback(c *gin.Context) {
	var req feedback.SubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if _, ok := d.requireSessionAccess(c, req.SessionID); !ok {
		return
	}
	f, err := d.Feedback.Submit(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, f)
}

func (d *Deps) getLearningNotes(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"notes": d.Feedback.ReadNotes()})
}

// ---------- 工单 ----------

func (d *Deps) createHandoff(c *gin.Context) {
	if _, ok := d.requireSessionAccess(c, c.Param("id")); !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	c.ShouldBindJSON(&req)
	ticket, err := d.Handoff.CreateTicket(c.Request.Context(), c.Param("id"), req.Reason)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, ticket)
}

func (d *Deps) listTickets(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	tickets, err := d.Handoff.ListTickets(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tickets": tickets})
}

// ---------- 设置 ----------

func (d *Deps) getAgentSettings(c *gin.Context) {
	c.JSON(http.StatusOK, d.Settings.GetAgentSettings())
}

func (d *Deps) updateAgentSettings(c *gin.Context) {
	var req model.AgentSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if err := d.Settings.UpdateAgentSettings(c.Request.Context(), req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, d.Settings.GetAgentSettings())
}

func (d *Deps) getPoolSettings(c *gin.Context) {
	c.JSON(http.StatusOK, d.Settings.PoolSettings())
}

func (d *Deps) updatePoolSettings(c *gin.Context) {
	var req model.PoolSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
		return
	}
	if err := d.Settings.UpdatePoolSettings(c.Request.Context(), req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, d.Settings.PoolSettings())
}

func (d *Deps) getAgentTypes(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"types": agent.GetTypes()})
}

func (d *Deps) checkAgentHealth(c *gin.Context) {
	spec := d.Settings.AgentSpec()
	adapter := agent.GetAdapter(spec.Type)
	if adapter == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "未注册的 Agent 类型: " + spec.Type})
		return
	}
	if err := adapter.CheckHealth(c.Request.Context(), spec); err != nil {
		c.JSON(http.StatusOK, gin.H{"healthy": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"healthy": true})
}

// ---------- 看板 ----------

func (d *Deps) getStatsOverview(c *gin.Context) {
	o, err := d.Stats.GetOverview(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, o)
}

func (d *Deps) getStatsDaily(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "14"))
	points, err := d.Stats.GetDaily(c.Request.Context(), days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"points": points})
}

func (d *Deps) getHotQuestions(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	hot, err := d.Stats.GetHotQuestions(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"questions": hot})
}
