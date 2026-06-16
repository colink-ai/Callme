// Package stats 运营统计看板数据聚合
package stats

import (
	"context"
	"sort"
	"strings"
	"time"

	"callme/internal/repo"
)

// Service 统计服务
type Service struct {
	store *repo.Store
	// 实时数据来源：会话管理器
	liveCounts func() (active, queued int)
}

// NewService 创建统计服务
func NewService(store *repo.Store, liveCounts func() (active, queued int)) *Service {
	return &Service{store: store, liveCounts: liveCounts}
}

// Overview 看板总览
type Overview struct {
	ActiveSessions   int     `json:"activeSessions"`
	QueuedSessions   int     `json:"queuedSessions"`
	SessionsToday    int64   `json:"sessionsToday"`
	Sessions7d       int64   `json:"sessions7d"`
	UserMessages7d   int64   `json:"userMessages7d"`
	KnowledgeHits7d  int64   `json:"knowledgeHits7d"`  // 使用了知识检索的回答数
	KnowledgeHitRate float64 `json:"knowledgeHitRate"` // 知识命中率
	FeedbackUp7d     int64   `json:"feedbackUp7d"`
	FeedbackDown7d   int64   `json:"feedbackDown7d"`
	SatisfactionRate float64 `json:"satisfactionRate"` // 点赞 /（点赞+点踩）
	Tickets7d        int64   `json:"tickets7d"`
	HandoffRate      float64 `json:"handoffRate"` // 转人工率 = 工单 / 会话
}

// GetOverview 聚合总览指标
func (s *Service) GetOverview(ctx context.Context) (*Overview, error) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	week := now.AddDate(0, 0, -7)

	o := &Overview{}
	o.ActiveSessions, o.QueuedSessions = s.liveCounts()

	var err error
	if o.SessionsToday, err = s.store.CountSessionsSince(ctx, todayStart); err != nil {
		return nil, err
	}
	if o.Sessions7d, err = s.store.CountSessionsSince(ctx, week); err != nil {
		return nil, err
	}
	if o.UserMessages7d, err = s.store.CountMessagesSince(ctx, "user", week); err != nil {
		return nil, err
	}
	if o.KnowledgeHits7d, err = s.store.CountKnowledgeHitsSince(ctx, week); err != nil {
		return nil, err
	}
	assistant7d, err := s.store.CountMessagesSince(ctx, "assistant", week)
	if err != nil {
		return nil, err
	}
	if assistant7d > 0 {
		o.KnowledgeHitRate = float64(o.KnowledgeHits7d) / float64(assistant7d)
	}
	if o.FeedbackUp7d, o.FeedbackDown7d, err = s.store.FeedbackCountsSince(ctx, week); err != nil {
		return nil, err
	}
	if total := o.FeedbackUp7d + o.FeedbackDown7d; total > 0 {
		o.SatisfactionRate = float64(o.FeedbackUp7d) / float64(total)
	}
	if o.Tickets7d, err = s.store.CountTicketsSince(ctx, week); err != nil {
		return nil, err
	}
	if o.Sessions7d > 0 {
		o.HandoffRate = float64(o.Tickets7d) / float64(o.Sessions7d)
	}
	return o, nil
}

// DailyPoint 学习曲线/趋势数据点
type DailyPoint struct {
	Date     string `json:"date"`
	Sessions int64  `json:"sessions"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
}

// GetDaily 按天趋势（看板折线图：会话量 + 点赞/点踩学习曲线）
func (s *Service) GetDaily(ctx context.Context, days int) ([]DailyPoint, error) {
	if days <= 0 || days > 90 {
		days = 14
	}
	sessions, err := s.store.DailySessionCounts(ctx, days)
	if err != nil {
		return nil, err
	}
	feedback, err := s.store.DailyFeedbackCounts(ctx, days)
	if err != nil {
		return nil, err
	}

	points := make([]DailyPoint, 0, days)
	for i := days - 1; i >= 0; i-- {
		day := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		fb := feedback[day]
		points = append(points, DailyPoint{
			Date:     day,
			Sessions: sessions[day],
			Up:       fb[0],
			Down:     fb[1],
		})
	}
	return points, nil
}

// HotQuestion 热点问题
type HotQuestion struct {
	Keyword string `json:"keyword"`
	Count   int    `json:"count"`
}

// GetHotQuestions 简易热点问题分析：按去停用词后的关键词频次
func (s *Service) GetHotQuestions(ctx context.Context, limit int) ([]HotQuestion, error) {
	if limit <= 0 {
		limit = 10
	}
	questions, err := s.store.RecentUserQuestions(ctx, time.Now().AddDate(0, 0, -7), 500)
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int)
	for _, q := range questions {
		for _, w := range tokenize(q) {
			counts[w]++
		}
	}

	hot := make([]HotQuestion, 0, len(counts))
	for w, c := range counts {
		if c >= 2 {
			hot = append(hot, HotQuestion{Keyword: w, Count: c})
		}
	}
	sort.Slice(hot, func(i, j int) bool { return hot[i].Count > hot[j].Count })
	if len(hot) > limit {
		hot = hot[:limit]
	}
	return hot, nil
}

var stopWords = map[string]bool{
	"的": true, "了": true, "是": true, "我": true, "你": true, "他": true,
	"吗": true, "怎么": true, "什么": true, "如何": true, "请问": true, "一下": true,
	"这个": true, "那个": true, "可以": true, "为什么": true, "哪里": true, "帮我": true,
	"the": true, "a": true, "is": true, "to": true, "how": true, "what": true, "why": true,
}

// tokenize 简易分词：英文按空格，中文取 2-4 字滑窗中的高频词不现实，这里退化为按标点/空格切段
func tokenize(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', ',', '.', '?', '!', '，', '。', '？', '！', '、', '；', ';', ':', '：', '(', ')', '（', '）':
			return true
		}
		return false
	})
	var words []string
	for _, f := range fields {
		if stopWords[f] || len([]rune(f)) < 2 {
			continue
		}
		words = append(words, f)
	}
	return words
}
