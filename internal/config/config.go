// Package config 加载 Callme 服务配置
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"callme/internal/model"

	"gopkg.in/yaml.v3"
)

// Config 服务总配置
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Agent    AgentConfig    `yaml:"agent"`
	Auth     AuthConfig     `yaml:"auth"`
	Session  SessionConfig  `yaml:"session"`
	Feedback FeedbackConfig `yaml:"feedback"`
	Handoff  HandoffConfig  `yaml:"handoff"`
	Log      LogConfig      `yaml:"log"`
}

// LogConfig 日志配置
type LogConfig struct {
	Path       string `yaml:"path"`        // 日志文件路径（空则只输出控制台）
	MaxSize    int    `yaml:"max_size"`    // 单文件最大 MB（默认 100）
	MaxBackups int    `yaml:"max_backups"` // 保留旧文件数（默认 3）
	MaxAge     int    `yaml:"max_age"`     // 保留天数（默认 7）
	Compress   bool   `yaml:"compress"`    // 是否压缩旧文件
}

// AuthConfig 登录态配置
type AuthConfig struct {
	TokenTTL time.Duration `yaml:"token_ttl"` // 登录保持时长
}

// ServerConfig HTTP 服务配置
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// DatabaseConfig 数据库配置（SQLite）
type DatabaseConfig struct {
	Driver string `yaml:"driver"` // 固定 sqlite
	DSN    string `yaml:"dsn"`    // 数据库文件路径
}

// AgentConfig Agent 基础路径配置。模型/API/提示词等运行时设置通过页面保存到数据库。
type AgentConfig struct {
	Type          string        `yaml:"type"`           // 插件类型，默认 hermes
	CliPath       string        `yaml:"cli_path"`       // CLI 路径，默认 hermes
	DefaultModel  string        `yaml:"default_model"`  // 兼容旧配置；新部署请在设置页配置
	APIURL        string        `yaml:"api_url"`        // 兼容旧配置；新部署请在设置页配置
	APIToken      string        `yaml:"api_token"`      // 兼容旧配置；新部署请在设置页配置
	RuntimeRoot   string        `yaml:"runtime_root"`   // Agent Runtime 根目录；领域会在此目录下按领域 ID 生成隔离子目录
	HermesHome    string        `yaml:"hermes_home"`    // 兼容字段：当前 Runtime 工作目录（Hermes 使用 HERMES_HOME）
	WorkDir       string        `yaml:"work_dir"`       // 会话工作目录根
	SystemPrompt  string        `yaml:"system_prompt"`  // 兼容旧配置；新部署请在设置页配置
	PromptTimeout time.Duration `yaml:"prompt_timeout"` // ACP 单轮回答最长等待时间；负数表示不主动超时
}

const DefaultDomainID = model.DefaultDomainID

// RuntimeHomeForDomain 返回领域隔离后的 Agent Runtime 工作目录。
// 配置 runtime_root 时使用新目录结构；否则回退到 hermes_home 兼容旧部署。
func (c AgentConfig) RuntimeHomeForDomain(domainID string) string {
	return c.RuntimeHomeForDomainAgent(domainID, c.Type)
}

// RuntimeHomeForDomainAgent 返回领域 + 基础 Agent 类型隔离后的运行时 home。
func (c AgentConfig) RuntimeHomeForDomainAgent(domainID, agentType string) string {
	domainID = normalizeDomainID(domainID)
	if c.RuntimeRoot != "" {
		return filepath.Join(c.AgentRuntimeDirForDomain(domainID, agentType), "home")
	}
	return c.HermesHome
}

// WorkDirForDomain 返回领域隔离后的会话工作目录根。
func (c AgentConfig) WorkDirForDomain(domainID string) string {
	return c.WorkDirForDomainAgent(domainID, c.Type)
}

// WorkDirForDomainAgent 返回领域 + 基础 Agent 类型隔离后的会话工作目录根。
func (c AgentConfig) WorkDirForDomainAgent(domainID, agentType string) string {
	domainID = normalizeDomainID(domainID)
	if c.RuntimeRoot != "" {
		return filepath.Join(c.AgentRuntimeDirForDomain(domainID, agentType), "workdir")
	}
	return c.WorkDir
}

// DomainRuntimeDir 返回领域级运行时目录。
func (c AgentConfig) DomainRuntimeDir(domainID string) string {
	if c.RuntimeRoot == "" {
		return ""
	}
	return filepath.Join(c.RuntimeRoot, normalizeDomainID(domainID))
}

// AgentRuntimeDirForDomain 返回某领域下特定基础 Agent 的运行时目录。
func (c AgentConfig) AgentRuntimeDirForDomain(domainID, agentType string) string {
	domainDir := c.DomainRuntimeDir(domainID)
	if domainDir == "" {
		return ""
	}
	return filepath.Join(domainDir, normalizeAgentType(agentType, c.Type))
}

// EnsureDomainRuntimeDirs 确保领域下当前基础 Agent 的运行时目录存在。
func (c AgentConfig) EnsureDomainRuntimeDirs(domainID, agentType string) error {
	base := c.AgentRuntimeDirForDomain(domainID, agentType)
	if base == "" {
		return nil
	}
	for _, dir := range []string{filepath.Join(base, "home"), filepath.Join(base, "workdir")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func normalizeDomainID(domainID string) string {
	domainID = strings.TrimSpace(domainID)
	if domainID == "" {
		return DefaultDomainID
	}
	var b strings.Builder
	for _, r := range strings.ToLower(domainID) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return DefaultDomainID
	}
	return b.String()
}

func normalizeAgentType(agentType, fallback string) string {
	agentType = strings.TrimSpace(agentType)
	if agentType == "" {
		agentType = strings.TrimSpace(fallback)
	}
	if agentType == "" {
		agentType = "hermes"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(agentType) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "hermes"
	}
	return b.String()
}

// SessionConfig 坐席制会话池配置
type SessionConfig struct {
	MaxActive        int           `yaml:"max_active"`         // 坐席数：最大并发活跃会话
	MaxQueue         int           `yaml:"max_queue"`          // 最大排队长度
	IdleWarnAfter    time.Duration `yaml:"idle_warn_after"`    // 空闲提醒阈值
	IdleCloseAfter   time.Duration `yaml:"idle_close_after"`   // 空闲自动结束阈值
	MaxDuration      time.Duration `yaml:"max_duration"`       // 单会话最大时长
	MaxPerClient     int           `yaml:"max_per_client"`     // 单客户端（指纹）同时会话数
	QueuePollSeconds int           `yaml:"queue_poll_seconds"` // 排队状态推送间隔
}

// FeedbackConfig 自学习蒸馏配置
type FeedbackConfig struct {
	DistillCron     string        `yaml:"distill_cron"`      // 自学习/历史会话挖掘 cron 表达式
	AuditCron       string        `yaml:"audit_cron"`        // Agent Runtime 自学习审计 cron 表达式
	DistillInterval time.Duration `yaml:"distill_interval"`  // 蒸馏任务周期
	AuditInterval   time.Duration `yaml:"audit_interval"`    // Agent Runtime 自学习审计周期
	NotesMaxEntries int           `yaml:"notes_max_entries"` // 兼容旧配置；正式知识现由审批流发布
}

// HandoffConfig 人工接管/工单外发配置
type HandoffConfig struct {
	WebhookURL     string            `yaml:"webhook_url"`
	WebhookHeaders map[string]string `yaml:"webhook_headers"`
}

// Load 从 yaml 文件加载配置并填充默认值
func Load(path string) (*Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.applyDefaults()
	return cfg, nil
}

func defaultConfig() *Config {
	return &Config{
		Server:   ServerConfig{Host: "0.0.0.0", Port: 8090},
		Database: DatabaseConfig{Driver: "sqlite", DSN: "data/callme.db"},
		Agent: AgentConfig{
			Type:          "hermes",
			CliPath:       "hermes",
			PromptTimeout: 30 * time.Minute,
		},
		Session: SessionConfig{
			MaxActive:        5,
			MaxQueue:         50,
			IdleWarnAfter:    5 * time.Minute,
			IdleCloseAfter:   10 * time.Minute,
			MaxDuration:      2 * time.Hour,
			MaxPerClient:     1,
			QueuePollSeconds: 5,
		},
		Auth: AuthConfig{
			TokenTTL: 7 * 24 * time.Hour,
		},
		Feedback: FeedbackConfig{
			DistillCron:     "0 * * * *",
			AuditCron:       "*/10 * * * *",
			DistillInterval: time.Hour,
			AuditInterval:   10 * time.Minute,
			NotesMaxEntries: 200,
		},
		Log: LogConfig{
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     7,
			Compress:   true,
		},
	}
}

func (c *Config) applyDefaults() {
	if c.Agent.Type == "" {
		c.Agent.Type = "hermes"
	}
	if c.Agent.CliPath == "" {
		c.Agent.CliPath = "hermes"
	}
	if c.Agent.RuntimeRoot != "" && c.Agent.HermesHome == "" {
		c.Agent.HermesHome = c.Agent.RuntimeHomeForDomain(DefaultDomainID)
	} else if c.Agent.HermesHome == "" {
		c.Agent.HermesHome = "data/hermes-home"
	}
	if c.Agent.WorkDir == "" {
		if c.Agent.RuntimeRoot != "" {
			c.Agent.WorkDir = c.Agent.WorkDirForDomain(DefaultDomainID)
		} else {
			c.Agent.WorkDir = "data/workdir"
		}
	}
	if c.Agent.PromptTimeout == 0 {
		c.Agent.PromptTimeout = 30 * time.Minute
	}
	if c.Auth.TokenTTL <= 0 {
		c.Auth.TokenTTL = 7 * 24 * time.Hour
	}
	if c.Session.MaxActive <= 0 {
		c.Session.MaxActive = 5
	}
	if c.Session.MaxQueue <= 0 {
		c.Session.MaxQueue = 50
	}
	if c.Session.MaxPerClient <= 0 {
		c.Session.MaxPerClient = 1
	}
	if c.Session.IdleWarnAfter <= 0 {
		c.Session.IdleWarnAfter = 5 * time.Minute
	}
	if c.Session.IdleCloseAfter <= c.Session.IdleWarnAfter {
		c.Session.IdleCloseAfter = c.Session.IdleWarnAfter * 2
	}
	if c.Session.MaxDuration <= 0 {
		c.Session.MaxDuration = 2 * time.Hour
	}
	if c.Session.QueuePollSeconds <= 0 {
		c.Session.QueuePollSeconds = 5
	}
	if c.Feedback.DistillInterval <= 0 {
		c.Feedback.DistillInterval = time.Hour
	}
	if c.Feedback.AuditInterval <= 0 {
		c.Feedback.AuditInterval = 10 * time.Minute
	}
	if c.Feedback.NotesMaxEntries <= 0 {
		c.Feedback.NotesMaxEntries = 200
	}
	// 日志默认值
	if c.Log.MaxSize <= 0 {
		c.Log.MaxSize = 100
	}
	if c.Log.MaxBackups <= 0 {
		c.Log.MaxBackups = 3
	}
	if c.Log.MaxAge <= 0 {
		c.Log.MaxAge = 7
	}
}

// Addr 返回监听地址
func (s ServerConfig) Addr() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}
