// Package settings 运行时设置：DB 中的覆盖项叠加在 config.yaml 默认值之上
// 模型切换入口：Settings 页修改 AgentSettings -> 新会话启动时生成新的 HERMES_HOME/config.yaml 生效
package settings

import (
	"context"
	"sync"
	"time"

	"callme/internal/config"
	"callme/internal/model"
	"callme/internal/repo"
	"callme/internal/service/agent"

	"go.uber.org/zap"
)

const (
	keyAgent = "agent_settings"
	keyPool  = "pool_settings"
)

// Service 设置服务（实现 session.SettingsProvider）
type Service struct {
	store      *repo.Store
	agentCfg   config.AgentConfig
	sessionCfg config.SessionConfig
	logger     *zap.Logger

	mu    sync.RWMutex
	agent model.AgentSettings
	pool  model.PoolSettings
}

// NewService 创建设置服务，从 DB 加载覆盖项
func NewService(store *repo.Store, agentCfg config.AgentConfig, sessionCfg config.SessionConfig, logger *zap.Logger) *Service {
	s := &Service{
		store:      store,
		agentCfg:   agentCfg,
		sessionCfg: sessionCfg,
		logger:     logger,
		agent: model.AgentSettings{
			Type:         agentCfg.Type,
			CliPath:      agentCfg.CliPath,
			DefaultModel: agentCfg.DefaultModel,
			APIURL:       agentCfg.APIURL,
			APIToken:     agentCfg.APIToken,
			SystemPrompt: agentCfg.SystemPrompt,
		},
		pool: model.PoolSettings{
			MaxActive: sessionCfg.MaxActive,
			MaxQueue:  sessionCfg.MaxQueue,
		},
	}

	ctx := context.Background()
	var dbAgent model.AgentSettings
	if ok, err := store.GetSetting(ctx, keyAgent, &dbAgent); err != nil {
		logger.Warn("load agent settings failed", zap.Error(err))
	} else if ok {
		s.agent = dbAgent
	}
	var dbPool model.PoolSettings
	if ok, err := store.GetSetting(ctx, keyPool, &dbPool); err != nil {
		logger.Warn("load pool settings failed", zap.Error(err))
	} else if ok && dbPool.MaxActive > 0 {
		s.pool = dbPool
	}
	return s
}

// AgentSpec 当前生效的 Agent 运行配置
func (s *Service) AgentSpec() agent.AgentSpec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cliPath := s.agent.CliPath
	if cliPath == "" {
		cliPath = agent.DefaultPathFor(s.agent.Type)
	}
	return agent.AgentSpec{
		Type:          s.agent.Type,
		CliPath:       cliPath,
		DefaultModel:  s.agent.DefaultModel,
		APIURL:        s.agent.APIURL,
		APIToken:      s.agent.APIToken,
		HermesHome:    s.agentCfg.HermesHome,
		SystemPrompt:  s.agent.SystemPrompt,
		PromptTimeout: s.agentCfg.PromptTimeout,
	}
}

// PoolSettings 当前生效的坐席池设置
func (s *Service) PoolSettings() model.PoolSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pool
}

// GetAgentSettings 读取 Agent 设置（API 返回，token 脱敏）
func (s *Service) GetAgentSettings() model.AgentSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.agent
	out.APIToken = maskToken(out.APIToken)
	return out
}

// UpdateAgentSettings 更新 Agent 设置（新会话生效）
func (s *Service) UpdateAgentSettings(ctx context.Context, in model.AgentSettings) error {
	s.mu.Lock()
	if in.Type == "" {
		in.Type = s.agent.Type
	}
	if in.CliPath == "" {
		in.CliPath = agent.DefaultPathFor(in.Type)
		if in.CliPath == "" && in.Type == s.agent.Type {
			in.CliPath = s.agent.CliPath
		}
	}
	// 前端提交脱敏占位时保留原 token
	if in.APIToken == "" || isMasked(in.APIToken) {
		in.APIToken = s.agent.APIToken
	}
	in.UpdatedAt = time.Now()
	s.agent = in
	s.mu.Unlock()

	s.logger.Info("agent settings updated", zap.String("model", in.DefaultModel), zap.String("type", in.Type))
	return s.store.PutSetting(ctx, keyAgent, in)
}

// UpdatePoolSettings 更新坐席池设置
func (s *Service) UpdatePoolSettings(ctx context.Context, in model.PoolSettings) error {
	if in.MaxActive <= 0 {
		in.MaxActive = 1
	}
	if in.MaxQueue < 0 {
		in.MaxQueue = 0
	}
	in.UpdatedAt = time.Now()
	s.mu.Lock()
	s.pool = in
	s.mu.Unlock()

	s.logger.Info("pool settings updated", zap.Int("maxActive", in.MaxActive), zap.Int("maxQueue", in.MaxQueue))
	return s.store.PutSetting(ctx, keyPool, in)
}

func maskToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 8 {
		return "****"
	}
	return token[:4] + "****" + token[len(token)-4:]
}

func isMasked(token string) bool {
	for _, c := range token {
		if c == '*' {
			return true
		}
	}
	return false
}
