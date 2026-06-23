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

	"github.com/google/uuid"
	"go.uber.org/zap"
)

const (
	keyAgent         = "agent_settings"
	keyAgentProfiles = "agent_profiles"
	keyPool          = "pool_settings"
)

// Service 设置服务（实现 session.SettingsProvider）
type Service struct {
	store      *repo.Store
	agentCfg   config.AgentConfig
	sessionCfg config.SessionConfig
	logger     *zap.Logger

	mu            sync.RWMutex
	agentProfiles model.AgentProfilesSettings
	pool          model.PoolSettings
}

// NewService 创建设置服务，从 DB 加载覆盖项
func NewService(store *repo.Store, agentCfg config.AgentConfig, sessionCfg config.SessionConfig, logger *zap.Logger) *Service {
	s := &Service{
		store:         store,
		agentCfg:      agentCfg,
		sessionCfg:    sessionCfg,
		logger:        logger,
		agentProfiles: defaultAgentProfiles(agentCfg),
		pool: model.PoolSettings{
			MaxActive: sessionCfg.MaxActive,
			MaxQueue:  sessionCfg.MaxQueue,
		},
	}

	ctx := context.Background()
	var dbProfiles model.AgentProfilesSettings
	if ok, err := store.GetSetting(ctx, keyAgentProfiles, &dbProfiles); err != nil {
		logger.Warn("load agent profiles failed", zap.Error(err))
	} else if ok {
		s.agentProfiles = normalizeAgentProfiles(dbProfiles, s.agentProfiles)
	} else {
		var dbAgent model.AgentSettings
		if ok, err := store.GetSetting(ctx, keyAgent, &dbAgent); err != nil {
			logger.Warn("load agent settings failed", zap.Error(err))
		} else if ok {
			s.agentProfiles = profilesFromAgentSettings(dbAgent)
		}
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
	active := activeAgentSettings(s.agentProfiles)
	cliPath := active.CliPath
	if cliPath == "" {
		cliPath = agent.DefaultPathFor(active.Type)
	}
	return agent.AgentSpec{
		Type:          active.Type,
		CliPath:       cliPath,
		DefaultModel:  active.DefaultModel,
		APIURL:        active.APIURL,
		APIToken:      active.APIToken,
		HermesHome:    s.agentCfg.HermesHome,
		SystemPrompt:  active.SystemPrompt,
		PromptTimeout: s.agentCfg.PromptTimeout,
	}
}

// PoolSettings 当前生效的坐席池设置
func (s *Service) PoolSettings() model.PoolSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pool
}

// GetAgentSettings 读取当前激活 Agent 设置（兼容旧调用，token 脱敏）
func (s *Service) GetAgentSettings() model.AgentSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := activeAgentSettings(s.agentProfiles)
	out.APIToken = maskToken(out.APIToken)
	return out
}

// GetAgentProfiles 读取 Agent 配置档案（API 返回，token 脱敏）
func (s *Service) GetAgentProfiles() model.AgentProfilesSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.agentProfiles
	out.Profiles = append([]model.AgentProfile(nil), s.agentProfiles.Profiles...)
	for i := range out.Profiles {
		out.Profiles[i].Settings.APIToken = maskToken(out.Profiles[i].Settings.APIToken)
	}
	return out
}

// UpdateAgentSettings 更新 Agent 设置（新会话生效）
func (s *Service) UpdateAgentSettings(ctx context.Context, in model.AgentSettings) error {
	s.mu.Lock()
	current := activeAgentSettings(s.agentProfiles)
	if in.Type == "" {
		in.Type = current.Type
	}
	if in.CliPath == "" {
		in.CliPath = agent.DefaultPathFor(in.Type)
		if in.CliPath == "" && in.Type == current.Type {
			in.CliPath = current.CliPath
		}
	}
	// 前端提交脱敏占位时保留原 token
	if in.APIToken == "" || isMasked(in.APIToken) {
		in.APIToken = current.APIToken
	}
	in.UpdatedAt = time.Now()
	profiles := s.agentProfiles
	if len(profiles.Profiles) == 0 {
		profiles = profilesFromAgentSettings(in)
	} else {
		for i := range profiles.Profiles {
			if profiles.Profiles[i].ID == profiles.ActiveProfileID {
				profiles.Profiles[i].Settings = in
				break
			}
		}
		profiles.UpdatedAt = in.UpdatedAt
	}
	s.agentProfiles = normalizeAgentProfiles(profiles, s.agentProfiles)
	saved := s.agentProfiles
	s.mu.Unlock()

	s.logger.Info("agent settings updated", zap.String("model", in.DefaultModel), zap.String("type", in.Type))
	return s.store.PutSetting(ctx, keyAgentProfiles, saved)
}

// UpdateAgentProfiles 更新多套 Agent 配置档案（新会话生效）
func (s *Service) UpdateAgentProfiles(ctx context.Context, in model.AgentProfilesSettings) error {
	s.mu.Lock()
	current := s.agentProfiles
	next := normalizeAgentProfiles(in, current)
	for i := range next.Profiles {
		p := &next.Profiles[i]
		old := findAgentProfile(current, p.ID)
		p.Settings = normalizeAgentSettings(p.Settings, old.Settings)
	}
	next.UpdatedAt = time.Now()
	s.agentProfiles = next
	saved := next
	s.mu.Unlock()

	active := activeAgentSettings(saved)
	s.logger.Info("agent profiles updated",
		zap.String("activeProfileID", saved.ActiveProfileID),
		zap.String("model", active.DefaultModel),
		zap.String("type", active.Type),
		zap.Int("profiles", len(saved.Profiles)),
	)
	return s.store.PutSetting(ctx, keyAgentProfiles, saved)
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

func defaultAgentProfiles(agentCfg config.AgentConfig) model.AgentProfilesSettings {
	return profilesFromAgentSettings(model.AgentSettings{
		Type:         agentCfg.Type,
		CliPath:      agentCfg.CliPath,
		DefaultModel: agentCfg.DefaultModel,
		APIURL:       agentCfg.APIURL,
		APIToken:     agentCfg.APIToken,
		SystemPrompt: agentCfg.SystemPrompt,
	})
}

func profilesFromAgentSettings(settings model.AgentSettings) model.AgentProfilesSettings {
	if settings.Type == "" {
		settings.Type = "hermes"
	}
	if settings.CliPath == "" {
		settings.CliPath = agent.DefaultPathFor(settings.Type)
	}
	settings.UpdatedAt = time.Now()
	return model.AgentProfilesSettings{
		ActiveProfileID: "default",
		Profiles: []model.AgentProfile{{
			ID:       "default",
			Name:     "默认配置",
			Settings: settings,
		}},
		UpdatedAt: settings.UpdatedAt,
	}
}

func normalizeAgentProfiles(in model.AgentProfilesSettings, fallback model.AgentProfilesSettings) model.AgentProfilesSettings {
	out := in
	if len(out.Profiles) == 0 {
		out = fallback
	}
	if len(out.Profiles) == 0 {
		out = profilesFromAgentSettings(model.AgentSettings{Type: "hermes", CliPath: agent.DefaultPathFor("hermes")})
	}
	seen := map[string]bool{}
	for i := range out.Profiles {
		if out.Profiles[i].ID == "" || seen[out.Profiles[i].ID] {
			out.Profiles[i].ID = uuid.NewString()
		}
		seen[out.Profiles[i].ID] = true
		if out.Profiles[i].Name == "" {
			label := out.Profiles[i].ID
			if len(label) > 8 {
				label = label[:8]
			}
			out.Profiles[i].Name = "配置 " + label
		}
		out.Profiles[i].Settings = normalizeAgentSettings(out.Profiles[i].Settings, model.AgentSettings{})
	}
	if out.ActiveProfileID == "" || findAgentProfile(out, out.ActiveProfileID).ID == "" {
		out.ActiveProfileID = out.Profiles[0].ID
	}
	return out
}

func normalizeAgentSettings(in model.AgentSettings, old model.AgentSettings) model.AgentSettings {
	if in.Type == "" {
		in.Type = old.Type
	}
	if in.Type == "" {
		in.Type = "hermes"
	}
	if in.CliPath == "" {
		in.CliPath = agent.DefaultPathFor(in.Type)
		if in.CliPath == "" && in.Type == old.Type {
			in.CliPath = old.CliPath
		}
	}
	if in.APIToken == "" || isMasked(in.APIToken) {
		in.APIToken = old.APIToken
	}
	if in.UpdatedAt.IsZero() {
		in.UpdatedAt = time.Now()
	}
	return in
}

func activeAgentSettings(profiles model.AgentProfilesSettings) model.AgentSettings {
	profile := findAgentProfile(profiles, profiles.ActiveProfileID)
	if profile.ID == "" && len(profiles.Profiles) > 0 {
		profile = profiles.Profiles[0]
	}
	return profile.Settings
}

func findAgentProfile(profiles model.AgentProfilesSettings, id string) model.AgentProfile {
	for _, p := range profiles.Profiles {
		if p.ID == id {
			return p
		}
	}
	return model.AgentProfile{}
}
