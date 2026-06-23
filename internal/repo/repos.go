package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"callme/internal/model"
)

// Store 聚合所有表的数据访问
type Store struct {
	db *sql.DB
}

// NewStore 创建 Store
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// ---------- sessions ----------

// CreateSession 写入新会话
func (s *Store) CreateSession(ctx context.Context, sess *model.Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, client_id, user_id, status, created_at, started_at, closed_at, close_reason, title, agent_session_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.ClientID, sess.UserID, sess.Status, sess.CreatedAt, sess.StartedAt, sess.ClosedAt, sess.CloseReason, sess.Title, sess.AgentSessionID)
	return err
}

// UpdateSession 全量更新会话状态字段
func (s *Store) UpdateSession(ctx context.Context, sess *model.Session) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET status=?, started_at=?, closed_at=?, close_reason=?, title=?, agent_session_id=? WHERE id=?`,
		sess.Status, sess.StartedAt, sess.ClosedAt, sess.CloseReason, sess.Title, sess.AgentSessionID, sess.ID)
	return err
}

// ReopenSession 复用已结束会话记录，把它重新放回排队/服务流程。
func (s *Store) ReopenSession(ctx context.Context, sess *model.Session) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions
		 SET status=?, created_at=?, started_at=?, closed_at=?, close_reason=?, title=?, agent_session_id=?
		 WHERE id=?`,
		sess.Status, sess.CreatedAt, sess.StartedAt, sess.ClosedAt, sess.CloseReason, sess.Title, sess.AgentSessionID, sess.ID)
	return err
}

// CloseUnfinishedSessions 关闭服务重启前遗留的活跃/排队会话。
func (s *Store) CloseUnfinishedSessions(ctx context.Context, reason model.CloseReason) error {
	now := time.Now()
	_, err := s.db.ExecContext(ctx,
		`UPDATE sessions
	 SET status=?, closed_at=?, close_reason=?
	 WHERE status != ?`,
		model.SessionStatusClosed, now, reason, model.SessionStatusClosed)
	return err
}

// GetSession 按 ID 查询会话
func (s *Store) GetSession(ctx context.Context, id string) (*model.Session, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, client_id, user_id, status, created_at, started_at, closed_at, close_reason, title, agent_session_id FROM sessions WHERE id=?`, id)
	return scanSession(row)
}

// ListSessionsByStatus 按状态列出会话（监控页）
func (s *Store) ListSessionsByStatus(ctx context.Context, statuses []model.SessionStatus, limit int) ([]*model.Session, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	query := `SELECT id, client_id, user_id, status, created_at, started_at, closed_at, close_reason, title, agent_session_id FROM sessions WHERE status IN (`
	args := make([]any, 0, len(statuses)+1)
	for i, st := range statuses {
		if i > 0 {
			query += ","
		}
		query += "?"
		args = append(args, st)
	}
	query += `) ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*model.Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, sess)
	}
	return result, rows.Err()
}

func (s *Store) ListClosedSessions(ctx context.Context, start, end *time.Time, userID string, page, pageSize int) ([]*model.Session, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

	where := `status=?`
	args := []any{model.SessionStatusClosed}
	if start != nil {
		where += ` AND closed_at >= ?`
		args = append(args, *start)
	}
	if end != nil {
		where += ` AND closed_at <= ?`
		args = append(args, *end)
	}
	if userID != "" {
		where += ` AND user_id = ?`
		args = append(args, userID)
	}

	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	queryArgs := append([]any{}, args...)
	queryArgs = append(queryArgs, pageSize, (page-1)*pageSize)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, client_id, user_id, status, created_at, started_at, closed_at, close_reason, title, agent_session_id
		 FROM sessions
		 WHERE `+where+`
		 ORDER BY closed_at DESC, created_at DESC
		 LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var result []*model.Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, sess)
	}
	return result, total, rows.Err()
}

// ListSessionsByUser 列出用户历史会话
func (s *Store) ListSessionsByUser(ctx context.Context, userID string, limit int) ([]*model.Session, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, client_id, user_id, status, created_at, started_at, closed_at, close_reason, title, agent_session_id
		 FROM sessions WHERE user_id=? ORDER BY created_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*model.Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, sess)
	}
	return result, rows.Err()
}

// DeleteClosedSessionCascade 删除已结束会话及其关联消息、反馈和工单。
func (s *Store) DeleteClosedSessionCascade(ctx context.Context, sessionID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var status model.SessionStatus
	if err := tx.QueryRowContext(ctx, `SELECT status FROM sessions WHERE id=?`, sessionID).Scan(&status); err != nil {
		return err
	}
	if status != model.SessionStatusClosed {
		return fmt.Errorf("只能删除已结束的历史会话")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM feedback WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tickets WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

// CountSessionsSince 统计某时间后创建的会话数
func (s *Store) CountSessionsSince(ctx context.Context, since time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE created_at >= ?`, since).Scan(&n)
	return n, err
}

// DailySessionCounts 按天统计会话量（看板）
func (s *Store) DailySessionCounts(ctx context.Context, days int) (map[string]int64, error) {
	since := time.Now().AddDate(0, 0, -days)
	rows, err := s.db.QueryContext(ctx,
		`SELECT created_at FROM sessions WHERE created_at >= ?`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[string]int64)
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		counts[t.Format("2006-01-02")]++
	}
	return counts, rows.Err()
}

type rowScanner interface{ Scan(dest ...any) error }

func scanSession(r rowScanner) (*model.Session, error) {
	var sess model.Session
	var startedAt, closedAt sql.NullTime
	if err := r.Scan(&sess.ID, &sess.ClientID, &sess.UserID, &sess.Status, &sess.CreatedAt, &startedAt, &closedAt, &sess.CloseReason, &sess.Title, &sess.AgentSessionID); err != nil {
		return nil, err
	}
	if startedAt.Valid {
		sess.StartedAt = &startedAt.Time
	}
	if closedAt.Valid {
		sess.ClosedAt = &closedAt.Time
	}
	return &sess, nil
}

// ---------- users / auth ----------

func (s *Store) CreateUser(ctx context.Context, u *model.User) error {
	u.Roles = model.NormalizeRoles(append(u.Roles, u.Role))
	u.Role = model.PrimaryRole(u.Roles)
	if u.MaxSessions <= 0 {
		u.MaxSessions = model.DefaultMaxSessionsForRoles(u.Roles)
	}
	rolesJSON, err := json.Marshal(u.Roles)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO users (id, username, password_hash, role, roles, max_sessions, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.PasswordHash, u.Role, string(rolesJSON), u.MaxSessions, u.CreatedAt, u.UpdatedAt)
	return err
}

func (s *Store) GetUser(ctx context.Context, id string) (*model.User, error) {
	var u model.User
	var roles string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, roles, max_sessions, created_at, updated_at FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &roles, &u.MaxSessions, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	u.Roles = decodeUserRoles(u.Role, roles)
	return &u, nil
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	var u model.User
	var roles string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, roles, max_sessions, created_at, updated_at FROM users WHERE username=?`, username).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &roles, &u.MaxSessions, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	u.Roles = decodeUserRoles(u.Role, roles)
	return &u, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]*model.User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, username, password_hash, role, roles, max_sessions, created_at, updated_at FROM users ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*model.User
	for rows.Next() {
		var u model.User
		var roles string
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &roles, &u.MaxSessions, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		u.Roles = decodeUserRoles(u.Role, roles)
		result = append(result, &u)
	}
	return result, rows.Err()
}

func (s *Store) UsernamesByIDs(ctx context.Context, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	query := `SELECT id, username FROM users WHERE id IN (`
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		if i > 0 {
			query += ","
		}
		query += "?"
		args = append(args, id)
	}
	query += `)`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string, len(ids))
	for rows.Next() {
		var id, username string
		if err := rows.Scan(&id, &username); err != nil {
			return nil, err
		}
		result[id] = username
	}
	return result, rows.Err()
}

func (s *Store) UpdateUserRole(ctx context.Context, id string, role model.UserRole) error {
	return s.UpdateUserRoles(ctx, id, []model.UserRole{role})
}

func (s *Store) UpdateUserRoles(ctx context.Context, id string, roles []model.UserRole) error {
	return s.UpdateUserRolesAndLimit(ctx, id, roles, 0)
}

func (s *Store) UpdateUserRolesAndLimit(ctx context.Context, id string, roles []model.UserRole, maxSessions int) error {
	roles = model.NormalizeRoles(roles)
	primary := model.PrimaryRole(roles)
	if maxSessions <= 0 {
		maxSessions = model.DefaultMaxSessionsForRoles(roles)
	}
	rolesJSON, err := json.Marshal(roles)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE users SET role=?, roles=?, max_sessions=?, updated_at=? WHERE id=?`, primary, string(rolesJSON), maxSessions, time.Now(), id)
	return err
}

func (s *Store) CountUsersByRole(ctx context.Context, role model.UserRole) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role=? OR roles LIKE ?`, role, `%`+quoteRoleForLike(role)+`%`).Scan(&n)
	return n, err
}

func decodeUserRoles(primary model.UserRole, raw string) []model.UserRole {
	var roles []model.UserRole
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &roles)
	}
	if len(roles) == 0 && primary != "" {
		roles = []model.UserRole{primary}
	}
	return model.NormalizeRoles(roles)
}

func quoteRoleForLike(role model.UserRole) string {
	data, _ := json.Marshal(string(role))
	return strings.Trim(string(data), "[]")
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_tokens WHERE user_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET user_id='' WHERE user_id=?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id=?`, id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *Store) SaveAuthToken(ctx context.Context, tok *model.AuthToken) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO auth_tokens (token, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		tok.Token, tok.UserID, tok.ExpiresAt, tok.CreatedAt)
	return err
}

func (s *Store) GetAuthToken(ctx context.Context, token string) (*model.AuthToken, error) {
	var tok model.AuthToken
	err := s.db.QueryRowContext(ctx,
		`SELECT token, user_id, expires_at, created_at FROM auth_tokens WHERE token=?`, token).
		Scan(&tok.Token, &tok.UserID, &tok.ExpiresAt, &tok.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &tok, nil
}

func (s *Store) DeleteAuthToken(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_tokens WHERE token=?`, token)
	return err
}

func (s *Store) DeleteExpiredAuthTokens(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_tokens WHERE expires_at <= ?`, now)
	return err
}

// ---------- messages ----------

// CreateMessage 写入消息
func (s *Store) CreateMessage(ctx context.Context, m *model.Message) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (id, session_id, role, content, tool_calls, model, agent_type, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.SessionID, m.Role, m.Content, m.ToolCalls, m.Model, m.AgentType, m.CreatedAt)
	return err
}

// ListMessages 列出会话消息（按时间升序）
func (s *Store) ListMessages(ctx context.Context, sessionID string) ([]*model.Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, role, content, tool_calls, model, agent_type, created_at FROM messages WHERE session_id=? ORDER BY created_at ASC`,
		sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*model.Message
	for rows.Next() {
		var m model.Message
		var toolCalls sql.NullString
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &toolCalls, &m.Model, &m.AgentType, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.ToolCalls = toolCalls.String
		result = append(result, &m)
	}
	return result, rows.Err()
}

func (s *Store) CopyMessagesToSession(ctx context.Context, sourceSessionID, targetSessionID string, now time.Time) error {
	messages, err := s.ListMessages(ctx, sourceSessionID)
	if err != nil {
		return err
	}
	for i, msg := range messages {
		copied := *msg
		copied.ID = fmt.Sprintf("%s-copy-%03d", targetSessionID, i+1)
		copied.SessionID = targetSessionID
		copied.CreatedAt = now.Add(time.Duration(i) * time.Millisecond)
		if err := s.CreateMessage(ctx, &copied); err != nil {
			return err
		}
	}
	return nil
}

// GetMessage 按 ID 查询消息
func (s *Store) GetMessage(ctx context.Context, id string) (*model.Message, error) {
	var m model.Message
	var toolCalls sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, session_id, role, content, tool_calls, model, agent_type, created_at FROM messages WHERE id=?`, id).
		Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &toolCalls, &m.Model, &m.AgentType, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	m.ToolCalls = toolCalls.String
	return &m, nil
}

// CountMessagesSince 统计某时间后用户消息数（热度指标）
func (s *Store) CountMessagesSince(ctx context.Context, role model.MessageRole, since time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE role=? AND created_at >= ?`, role, since).Scan(&n)
	return n, err
}

// CountKnowledgeHitsSince 统计带工具调用（知识检索）的助手消息数
func (s *Store) CountKnowledgeHitsSince(ctx context.Context, since time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE role='assistant' AND tool_calls IS NOT NULL AND tool_calls != '' AND tool_calls != '[]' AND created_at >= ?`,
		since).Scan(&n)
	return n, err
}

// RecentUserQuestions 最近的用户提问（热点问题分析）
func (s *Store) RecentUserQuestions(ctx context.Context, since time.Time, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT content FROM messages WHERE role='user' AND created_at >= ? ORDER BY created_at DESC LIMIT ?`,
		since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// ---------- feedback ----------

// CreateFeedback 写入反馈
func (s *Store) CreateFeedback(ctx context.Context, f *model.Feedback) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO feedback (id, session_id, message_id, rating, correction, distilled, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.SessionID, f.MessageID, f.Rating, f.Correction, f.Distilled, f.CreatedAt)
	return err
}

// ListUndistilledFeedback 列出未蒸馏的反馈（学习任务输入）
func (s *Store) ListUndistilledFeedback(ctx context.Context, limit int) ([]*model.Feedback, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, message_id, rating, correction, distilled, created_at FROM feedback WHERE distilled = FALSE ORDER BY created_at ASC LIMIT ?`,
		limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*model.Feedback
	for rows.Next() {
		var f model.Feedback
		var correction sql.NullString
		if err := rows.Scan(&f.ID, &f.SessionID, &f.MessageID, &f.Rating, &correction, &f.Distilled, &f.CreatedAt); err != nil {
			return nil, err
		}
		f.Correction = correction.String
		result = append(result, &f)
	}
	return result, rows.Err()
}

// MarkFeedbackDistilled 标记反馈已蒸馏
func (s *Store) MarkFeedbackDistilled(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, `UPDATE feedback SET distilled = TRUE WHERE id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

// FeedbackCountsSince 统计某时间后的点赞/点踩数
func (s *Store) FeedbackCountsSince(ctx context.Context, since time.Time) (up, down int64, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT
			COALESCE(SUM(CASE WHEN rating='up' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN rating='down' THEN 1 ELSE 0 END), 0)
		 FROM feedback WHERE created_at >= ?`, since).Scan(&up, &down)
	return
}

// DailyFeedbackCounts 按天统计点赞/点踩（学习曲线）
func (s *Store) DailyFeedbackCounts(ctx context.Context, days int) (map[string][2]int64, error) {
	since := time.Now().AddDate(0, 0, -days)
	rows, err := s.db.QueryContext(ctx, `SELECT rating, created_at FROM feedback WHERE created_at >= ?`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := make(map[string][2]int64)
	for rows.Next() {
		var rating string
		var t time.Time
		if err := rows.Scan(&rating, &t); err != nil {
			return nil, err
		}
		day := t.Format("2006-01-02")
		c := counts[day]
		if rating == "up" {
			c[0]++
		} else {
			c[1]++
		}
		counts[day] = c
	}
	return counts, rows.Err()
}

// ---------- tickets ----------

// CreateTicket 写入工单
func (s *Store) CreateTicket(ctx context.Context, t *model.Ticket) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO tickets (id, session_id, reason, transcript, status, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID, t.SessionID, t.Reason, t.Transcript, t.Status, t.CreatedAt)
	return err
}

// UpdateTicketStatus 更新工单状态
func (s *Store) UpdateTicketStatus(ctx context.Context, id string, status model.TicketStatus) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tickets SET status=? WHERE id=?`, status, id)
	return err
}

// ListTickets 列出工单（按时间倒序）
func (s *Store) ListTickets(ctx context.Context, limit int) ([]*model.Ticket, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, reason, transcript, status, created_at FROM tickets ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*model.Ticket
	for rows.Next() {
		var t model.Ticket
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Reason, &t.Transcript, &t.Status, &t.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, &t)
	}
	return result, rows.Err()
}

// CountTicketsSince 统计某时间后的工单数（转人工率）
func (s *Store) CountTicketsSince(ctx context.Context, since time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tickets WHERE created_at >= ?`, since).Scan(&n)
	return n, err
}

// ---------- settings (k/v JSON) ----------

// GetSetting 读取设置（JSON 反序列化到 out），不存在返回 false
func (s *Store) GetSetting(ctx context.Context, key string, out any) (bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT v FROM settings WHERE k=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(v), out); err != nil {
		return false, fmt.Errorf("unmarshal setting %s: %w", key, err)
	}
	return true, nil
}

// PutSetting 写入设置（JSON 序列化）
func (s *Store) PutSetting(ctx context.Context, key string, val any) error {
	data, err := json.Marshal(val)
	if err != nil {
		return err
	}
	// SQLite 与 MySQL 均支持的 upsert 写法
	_, err = s.db.ExecContext(ctx, `DELETE FROM settings WHERE k=?`, key)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO settings (k, v, updated_at) VALUES (?, ?, ?)`, key, string(data), time.Now())
	return err
}

// ---------- candidate_assets（自学习候选资产池） ----------

// CreateCandidate 写入候选资产
func (s *Store) CreateCandidate(ctx context.Context, a *model.CandidateAsset) error {
	targets := encodePublishTargets(a.PublishTargets)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO candidate_assets
		 (id, asset_type, publish_targets, title, question, content, evidence, source_session_id, source_feedback_id, confidence, status, reviewer, review_note, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.AssetType, targets, a.Title, a.Question, a.Content, a.Evidence, a.SourceSessionID, a.SourceFeedbackID,
		a.Confidence, a.Status, a.Reviewer, a.ReviewNote, a.CreatedAt, a.UpdatedAt)
	return err
}

// GetCandidate 按 ID 查询候选资产
func (s *Store) GetCandidate(ctx context.Context, id string) (*model.CandidateAsset, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, asset_type, publish_targets, title, question, content, evidence, source_session_id, source_feedback_id, confidence, status, reviewer, review_note, created_at, updated_at
		 FROM candidate_assets WHERE id=?`, id)
	return scanCandidate(row)
}

// ListCandidates 按状态列出候选资产（status 为空则全部），按创建时间倒序
func (s *Store) ListCandidates(ctx context.Context, status model.CandidateAssetStatus, limit int) ([]*model.CandidateAsset, error) {
	if limit <= 0 {
		limit = 200
	}
	var (
		rows *sql.Rows
		err  error
	)
	base := `SELECT id, asset_type, publish_targets, title, question, content, evidence, source_session_id, source_feedback_id, confidence, status, reviewer, review_note, created_at, updated_at FROM candidate_assets`
	if status == "" {
		rows, err = s.db.QueryContext(ctx, base+` ORDER BY created_at DESC LIMIT ?`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, base+` WHERE status=? ORDER BY created_at DESC LIMIT ?`, status, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []*model.CandidateAsset
	for rows.Next() {
		a, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

// UpdateCandidate 更新候选资产可编辑字段与审批状态
func (s *Store) UpdateCandidate(ctx context.Context, a *model.CandidateAsset) error {
	a.UpdatedAt = time.Now()
	targets := encodePublishTargets(a.PublishTargets)
	_, err := s.db.ExecContext(ctx,
		`UPDATE candidate_assets
		 SET asset_type=?, publish_targets=?, title=?, question=?, content=?, status=?, reviewer=?, review_note=?, updated_at=?
		 WHERE id=?`,
		a.AssetType, targets, a.Title, a.Question, a.Content, a.Status, a.Reviewer, a.ReviewNote, a.UpdatedAt, a.ID)
	return err
}

// CountCandidatesByStatus 统计各状态候选数（看板/角标）
func (s *Store) CountCandidatesByStatus(ctx context.Context, status model.CandidateAssetStatus) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM candidate_assets WHERE status=?`, status).Scan(&n)
	return n, err
}

func scanCandidate(r rowScanner) (*model.CandidateAsset, error) {
	var a model.CandidateAsset
	var question, evidence, reviewNote, publishTargets sql.NullString
	if err := r.Scan(&a.ID, &a.AssetType, &publishTargets, &a.Title, &question, &a.Content, &evidence,
		&a.SourceSessionID, &a.SourceFeedbackID, &a.Confidence, &a.Status, &a.Reviewer, &reviewNote,
		&a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	a.PublishTargets = decodePublishTargets(publishTargets.String)
	a.Question = question.String
	a.Evidence = evidence.String
	a.ReviewNote = reviewNote.String
	return &a, nil
}

func encodePublishTargets(targets []model.KnowledgePublishTarget) string {
	targets = model.NormalizeKnowledgePublishTargets(targets)
	data, err := json.Marshal(targets)
	if err != nil {
		return `["local"]`
	}
	return string(data)
}

func decodePublishTargets(raw string) []model.KnowledgePublishTarget {
	var targets []model.KnowledgePublishTarget
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &targets)
	}
	return model.NormalizeKnowledgePublishTargets(targets)
}

// ---------- runtime learning assets（Agent Runtime 自学习审计轨） ----------
//
// 兼容说明：底层表仍沿用 hermes_learning_assets，避免破坏既有部署；
// 对外领域模型已经切换为 RuntimeLearningAsset。

func (s *Store) CreateRuntimeLearningAsset(ctx context.Context, a *model.RuntimeLearningAsset) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO hermes_learning_assets
		 (id, asset_type, path, content_hash, content, change_type, risk_flags, status, reviewer, review_note, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.AssetType, a.Path, a.ContentHash, a.Content, a.ChangeType, a.RiskFlags,
		a.Status, a.Reviewer, a.ReviewNote, a.CreatedAt, a.UpdatedAt)
	return err
}

func (s *Store) LatestRuntimeLearningAssetByPath(ctx context.Context, path string) (*model.RuntimeLearningAsset, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, asset_type, path, content_hash, content, change_type, risk_flags, status, reviewer, review_note, created_at, updated_at
		 FROM hermes_learning_assets WHERE path=? ORDER BY created_at DESC LIMIT 1`, path)
	return scanRuntimeLearningAsset(row)
}

func (s *Store) GetRuntimeLearningAsset(ctx context.Context, id string) (*model.RuntimeLearningAsset, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, asset_type, path, content_hash, content, change_type, risk_flags, status, reviewer, review_note, created_at, updated_at
		 FROM hermes_learning_assets WHERE id=?`, id)
	a, err := scanRuntimeLearningAsset(row)
	if err != nil {
		return nil, err
	}
	hydrateRuntimeLearningContent(a)
	return a, nil
}

func (s *Store) ListLatestRuntimeLearningAssets(ctx context.Context) (map[string]*model.RuntimeLearningAsset, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT h.id, h.asset_type, h.path, h.content_hash, h.content, h.change_type, h.risk_flags, h.status, h.reviewer, h.review_note, h.created_at, h.updated_at
		 FROM hermes_learning_assets h
		 JOIN (
		   SELECT path, MAX(created_at) AS created_at
		   FROM hermes_learning_assets
		   GROUP BY path
		 ) latest ON latest.path = h.path AND latest.created_at = h.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]*model.RuntimeLearningAsset{}
	for rows.Next() {
		a, err := scanRuntimeLearningAsset(rows)
		if err != nil {
			return nil, err
		}
		result[a.Path] = a
	}
	return result, rows.Err()
}

func (s *Store) ListRuntimeLearningAssets(ctx context.Context, status model.RuntimeLearningStatus, limit int) ([]*model.RuntimeLearningAsset, error) {
	if limit <= 0 {
		limit = 200
	}
	base := `SELECT id, asset_type, path, content_hash, content, change_type, risk_flags, status, reviewer, review_note, created_at, updated_at FROM hermes_learning_assets WHERE 1=1`
	var (
		rows *sql.Rows
		err  error
	)
	invalid := ` AND path NOT LIKE '%/_quarantine/%'
		AND path NOT LIKE '%/skills/.%'
		AND path NOT LIKE '%/skills/%/references/%'
		AND (asset_type != 'skill' OR path LIKE '%/SKILL.md')
		AND (asset_type != 'memory' OR (path LIKE '%.md' AND path NOT LIKE '%.lock'))`
	if status == "" {
		rows, err = s.db.QueryContext(ctx, base+invalid+` ORDER BY created_at DESC LIMIT ?`, limit*4)
	} else {
		rows, err = s.db.QueryContext(ctx, base+invalid+` AND status=? ORDER BY created_at DESC LIMIT ?`, status, limit*4)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*model.RuntimeLearningAsset
	seen := map[string]struct{}{}
	for rows.Next() {
		a, err := scanRuntimeLearningAsset(rows)
		if err != nil {
			return nil, err
		}
		key := string(a.AssetType) + ":" + a.ContentHash
		if a.ContentHash == "" {
			key = string(a.AssetType) + ":" + a.Path
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		hydrateRuntimeLearningContent(a)
		result = append(result, a)
		if len(result) >= limit {
			break
		}
	}
	return result, rows.Err()
}

func (s *Store) UpdateRuntimeLearningAssetReview(ctx context.Context, id string, status model.RuntimeLearningStatus, reviewer, note string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE hermes_learning_assets
		 SET status=?, reviewer=?, review_note=?, updated_at=?
		 WHERE id=?`,
		status, reviewer, note, time.Now(), id)
	return err
}

func (s *Store) UpdateRuntimeLearningAssetReviewWithHash(ctx context.Context, id string, status model.RuntimeLearningStatus, contentHash, reviewer, note string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE hermes_learning_assets
		 SET status=?, content_hash=?, reviewer=?, review_note=?, updated_at=?
		 WHERE id=?`,
		status, contentHash, reviewer, note, time.Now(), id)
	return err
}

func hydrateRuntimeLearningContent(a *model.RuntimeLearningAsset) {
	if a == nil || a.Path == "" || a.ChangeType == model.RuntimeLearningChangeDeleted {
		return
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return
	}
	a.Content = string(data)
}

func scanRuntimeLearningAsset(r rowScanner) (*model.RuntimeLearningAsset, error) {
	var a model.RuntimeLearningAsset
	var content, riskFlags, reviewNote sql.NullString
	if err := r.Scan(&a.ID, &a.AssetType, &a.Path, &a.ContentHash, &content, &a.ChangeType, &riskFlags,
		&a.Status, &a.Reviewer, &reviewNote, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	a.AgentType = "hermes"
	a.Content = content.String
	a.RiskFlags = riskFlags.String
	a.ReviewNote = reviewNote.String
	return &a, nil
}

func (s *Store) CreateHermesLearningAsset(ctx context.Context, a *model.HermesLearningAsset) error {
	return s.CreateRuntimeLearningAsset(ctx, a)
}

func (s *Store) LatestHermesLearningAssetByPath(ctx context.Context, path string) (*model.HermesLearningAsset, error) {
	return s.LatestRuntimeLearningAssetByPath(ctx, path)
}

func (s *Store) GetHermesLearningAsset(ctx context.Context, id string) (*model.HermesLearningAsset, error) {
	return s.GetRuntimeLearningAsset(ctx, id)
}

func (s *Store) ListLatestHermesLearningAssets(ctx context.Context) (map[string]*model.HermesLearningAsset, error) {
	return s.ListLatestRuntimeLearningAssets(ctx)
}

func (s *Store) ListHermesLearningAssets(ctx context.Context, status model.HermesLearningStatus, limit int) ([]*model.HermesLearningAsset, error) {
	return s.ListRuntimeLearningAssets(ctx, status, limit)
}

func (s *Store) UpdateHermesLearningAssetReview(ctx context.Context, id string, status model.HermesLearningStatus, reviewer, note string) error {
	return s.UpdateRuntimeLearningAssetReview(ctx, id, status, reviewer, note)
}

func (s *Store) UpdateHermesLearningAssetReviewWithHash(ctx context.Context, id string, status model.HermesLearningStatus, contentHash, reviewer, note string) error {
	return s.UpdateRuntimeLearningAssetReviewWithHash(ctx, id, status, contentHash, reviewer, note)
}

// ---------- learning_jobs（AI 学习任务历史） ----------

func (s *Store) CreateLearningJob(ctx context.Context, j *model.LearningJob) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO learning_jobs (id, source, status, input_sessions, output_assets, error, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.Source, j.Status, j.InputSessions, j.OutputAssets, j.Error, j.StartedAt, j.FinishedAt)
	return err
}

func (s *Store) UpdateLearningJob(ctx context.Context, j *model.LearningJob) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE learning_jobs
		 SET status=?, input_sessions=?, output_assets=?, error=?, finished_at=?
		 WHERE id=?`,
		j.Status, j.InputSessions, j.OutputAssets, j.Error, j.FinishedAt, j.ID)
	return err
}

func (s *Store) ListLearningJobs(ctx context.Context, limit int) ([]*model.LearningJob, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, source, status, input_sessions, output_assets, error, started_at, finished_at
		 FROM learning_jobs ORDER BY started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*model.LearningJob
	for rows.Next() {
		var j model.LearningJob
		var errText sql.NullString
		var finished sql.NullTime
		if err := rows.Scan(&j.ID, &j.Source, &j.Status, &j.InputSessions, &j.OutputAssets, &errText, &j.StartedAt, &finished); err != nil {
			return nil, err
		}
		j.Error = errText.String
		if finished.Valid {
			j.FinishedAt = &finished.Time
		}
		result = append(result, &j)
	}
	return result, rows.Err()
}
