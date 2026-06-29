// Package runtime is Callme's thin integration layer over Helios.
//
// Agent protocol adapters live in Helios. Callme owns application concerns:
// settings, domain isolation, session persistence, WebSocket forwarding, and
// audit-friendly rendering of Helios events.
package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"callme/internal/service/agent"

	heliosacp "github.com/colink-ai/helios/adapters/acp"
	helioscontracts "github.com/colink-ai/helios/contracts"
	helios "github.com/colink-ai/helios/runtime"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

const (
	TypeHermes        = "hermes"
	defaultHermesPath = "hermes"
)

// AgentType describes a runtime backend exposed to Callme settings.
type AgentType struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	DefaultPath string `json:"defaultPath"`
}

// Service creates runtime sessions through Helios and exposes the supported
// agent surface to Callme.
type Service struct {
	logger *zap.Logger
}

func NewService(logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{logger: logger}
}

func (s *Service) Types() []AgentType {
	return []AgentType{{
		Type:        TypeHermes,
		Name:        "Hermes",
		Description: "Hermes via Helios Runtime and ACP",
		DefaultPath: defaultHermesPath,
	}}
}

func (s *Service) DefaultPathFor(typ string) string {
	if typ == TypeHermes {
		return defaultHermesPath
	}
	return ""
}

func (s *Service) NewAdapter(spec agent.AgentSpec) (agent.Adapter, error) {
	if spec.Type == "" {
		spec.Type = TypeHermes
	}
	if spec.Type != TypeHermes {
		return nil, fmt.Errorf("unsupported agent type %s; custom agents should be added as Helios adapters", spec.Type)
	}
	if spec.CliPath == "" {
		spec.CliPath = defaultHermesPath
	}
	return newHeliosAdapter(s.logger), nil
}

func (s *Service) CheckHealth(ctx context.Context, spec agent.AgentSpec) error {
	adapter, err := s.NewAdapter(spec)
	if err != nil {
		return err
	}
	return adapter.CheckHealth(ctx, spec)
}

// HeliosAdapter adapts Helios runtime events to Callme's existing session
// manager callbacks.
type HeliosAdapter struct {
	mu        sync.RWMutex
	engine    *helios.Engine
	registry  *helios.Registry
	logger    *zap.Logger
	callbacks map[string]func(agent.Chunk)
	sessions  map[string]sessionInfo
}

type sessionInfo struct {
	status           agent.SessionStatus
	agentSessionID   string
	usedNativeResume bool
}

func newHeliosAdapter(logger *zap.Logger) *HeliosAdapter {
	adapter := &HeliosAdapter{
		registry:  newHeliosRegistry(logger),
		logger:    logger,
		callbacks: map[string]func(agent.Chunk){},
		sessions:  map[string]sessionInfo{},
	}
	adapter.engine = helios.NewEngine(adapter.registry, helios.WithEventSink(helios.EventSinkFunc(adapter.onRunEvent)))
	return adapter
}

func newHeliosRegistry(logger *zap.Logger) *helios.Registry {
	registry := helios.NewRegistry()
	err := registry.Register(helios.AdapterMeta{
		Type:        TypeHermes,
		Name:        "Hermes",
		Description: "Hermes ACP adapter managed through Helios",
		DefaultPath: defaultHermesPath,
		Factory: func(spec helios.AgentSpec) (helios.Adapter, error) {
			return heliosacp.NewBaseAdapter(heliosacp.Config{
				CLIPath: spec.CLIPath,
				BuildArgs: func(helios.SessionRequest) []string {
					return []string{"acp"}
				},
				BuildEnv: func(req helios.SessionRequest) []string {
					callmeSpec := fromHeliosAgentSpec(req.Agent)
					if req.RuntimeHome != "" {
						callmeSpec.RuntimeHome = req.RuntimeHome
					}
					generateHermesConfig(logger, callmeSpec, fromHeliosMCPServers(req.MCPServers))
					return buildHermesEnv(logger, callmeSpec)
				},
				PromptTimeout: spec.PromptTimeout,
			}), nil
		},
	})
	if err != nil {
		logger.Error("register Helios Hermes adapter failed", zap.Error(err))
	}
	return registry
}

func (h *HeliosAdapter) StartSession(ctx context.Context, sessionID string, req *agent.SessionRequest) error {
	if req == nil {
		return fmt.Errorf("session request is required")
	}
	handle, err := h.engine.StartSession(ctx, helios.SessionRequest{
		SessionID:       sessionID,
		Agent:           toHeliosAgentSpec(req.Spec),
		WorkDir:         req.WorkDir,
		RuntimeHome:     runtimeHome(req.Spec),
		MCPServers:      toHeliosMCPServers(req.MCPServers),
		ResumeSessionID: req.ResumeSessionID,
	})
	if err != nil {
		return err
	}
	info := sessionInfo{status: agent.SessionStatusRunning}
	if handle != nil {
		info.agentSessionID = handle.AgentSessionID
		if nativeResume, ok := handle.Metadata["nativeResume"].(bool); ok {
			info.usedNativeResume = nativeResume
		}
	}
	h.mu.Lock()
	h.sessions[sessionID] = info
	h.mu.Unlock()
	return nil
}

func (h *HeliosAdapter) Prompt(ctx context.Context, sessionID string, input string, images []agent.ImageContent, onChunk func(agent.Chunk)) error {
	h.mu.Lock()
	h.callbacks[sessionID] = onChunk
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.callbacks, sessionID)
		h.mu.Unlock()
	}()

	_, err := h.engine.Prompt(ctx, helios.PromptRequest{
		SessionID: sessionID,
		Input:     input,
		Images:    toHeliosImages(images),
	})
	return err
}

func (h *HeliosAdapter) StopSession(sessionID string) error {
	err := h.engine.StopSession(context.Background(), sessionID)
	h.mu.Lock()
	if info, ok := h.sessions[sessionID]; ok {
		info.status = agent.SessionStatusStopped
		h.sessions[sessionID] = info
	}
	delete(h.callbacks, sessionID)
	h.mu.Unlock()
	return err
}

func (h *HeliosAdapter) GetSessionStatus(sessionID string) agent.SessionStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if info, ok := h.sessions[sessionID]; ok {
		return info.status
	}
	return agent.SessionStatusIdle
}

func (h *HeliosAdapter) CheckHealth(ctx context.Context, spec agent.AgentSpec) error {
	adapter, err := h.registry.Create(toHeliosAgentSpec(spec))
	if err != nil {
		return err
	}
	return adapter.CheckHealth(ctx, toHeliosAgentSpec(spec))
}

func (h *HeliosAdapter) AgentSessionID(sessionID string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessions[sessionID].agentSessionID
}

func (h *HeliosAdapter) UsedNativeResume(sessionID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessions[sessionID].usedNativeResume
}

func (h *HeliosAdapter) onRunEvent(_ context.Context, event helioscontracts.RunEvent) error {
	if event.Chunk == nil || event.SessionID == "" {
		return nil
	}
	chunk := fromHeliosChunk(*event.Chunk)
	h.mu.RLock()
	callback := h.callbacks[event.SessionID]
	h.mu.RUnlock()
	if callback != nil {
		callback(chunk)
	}
	return nil
}

func toHeliosAgentSpec(spec agent.AgentSpec) helios.AgentSpec {
	return helios.AgentSpec{
		Type:               TypeHermes,
		CLIPath:            spec.CliPath,
		DefaultModel:       spec.DefaultModel,
		APIURL:             spec.APIURL,
		APIToken:           spec.APIToken,
		RuntimeHome:        runtimeHome(spec),
		SystemPrompt:       spec.SystemPrompt,
		SupportsMultimodal: spec.SupportsMultimodal,
		PromptTimeout:      spec.PromptTimeout,
	}
}

func fromHeliosAgentSpec(spec helios.AgentSpec) agent.AgentSpec {
	return agent.AgentSpec{
		Type:               TypeHermes,
		CliPath:            spec.CLIPath,
		DefaultModel:       spec.DefaultModel,
		APIURL:             spec.APIURL,
		APIToken:           spec.APIToken,
		RuntimeHome:        spec.RuntimeHome,
		SystemPrompt:       spec.SystemPrompt,
		SupportsMultimodal: spec.SupportsMultimodal,
		PromptTimeout:      spec.PromptTimeout,
	}
}

func toHeliosMCPServers(specs []agent.MCPServerSpec) []helios.MCPServerSpec {
	servers := make([]helios.MCPServerSpec, 0, len(specs))
	for _, spec := range specs {
		servers = append(servers, helios.MCPServerSpec{
			Name:    spec.Name,
			Type:    spec.Type,
			URL:     spec.URL,
			Headers: spec.Headers,
			Command: spec.Command,
			Args:    spec.Args,
			Env:     spec.Env,
		})
	}
	return servers
}

func fromHeliosMCPServers(specs []helios.MCPServerSpec) []agent.MCPServerSpec {
	servers := make([]agent.MCPServerSpec, 0, len(specs))
	for _, spec := range specs {
		servers = append(servers, agent.MCPServerSpec{
			Name:    spec.Name,
			Type:    spec.Type,
			URL:     spec.URL,
			Headers: spec.Headers,
			Command: spec.Command,
			Args:    spec.Args,
			Env:     spec.Env,
		})
	}
	return servers
}

func toHeliosImages(images []agent.ImageContent) []helioscontracts.ImageContent {
	out := make([]helioscontracts.ImageContent, 0, len(images))
	for _, image := range images {
		out = append(out, helioscontracts.ImageContent{MimeType: image.MimeType, Data: image.Data})
	}
	return out
}

func fromHeliosChunk(chunk helioscontracts.Chunk) agent.Chunk {
	out := agent.Chunk{
		Type:      fromHeliosChunkType(chunk.Type),
		Content:   chunk.Content,
		ToolName:  chunk.ToolName,
		ToolID:    chunk.ToolID,
		ToolInput: chunk.ToolInput,
		IsError:   chunk.IsError,
	}
	if chunk.Usage != nil {
		out.Usage = &agent.TokenUsage{
			InputTokens:  chunk.Usage.InputTokens,
			OutputTokens: chunk.Usage.OutputTokens,
		}
	}
	return out
}

func fromHeliosChunkType(chunkType helioscontracts.ChunkType) agent.ChunkType {
	switch chunkType {
	case helioscontracts.ChunkText:
		return agent.ChunkTypeText
	case helioscontracts.ChunkError:
		return agent.ChunkTypeError
	case helioscontracts.ChunkThinking:
		return agent.ChunkTypeThinking
	case helioscontracts.ChunkToolUse:
		return agent.ChunkTypeToolUse
	case helioscontracts.ChunkToolResult:
		return agent.ChunkTypeToolResult
	case helioscontracts.ChunkUsage:
		return agent.ChunkTypeUsage
	case helioscontracts.ChunkDone:
		return agent.ChunkTypeDone
	default:
		return agent.ChunkTypeStatus
	}
}

func generateHermesConfig(logger *zap.Logger, spec agent.AgentSpec, mcpServers []agent.MCPServerSpec) {
	home := runtimeHome(spec)
	if home == "" {
		return
	}

	hermesHome := absPath(home)
	configPath := filepath.Join(hermesHome, "config.yaml")

	cfg := loadHermesConfig(logger, configPath)
	cfg["model"] = map[string]any{"default": spec.DefaultModel}
	if spec.APIURL != "" || spec.APIToken != "" {
		cfg["model"].(map[string]any)["provider"] = "custom"
		cfg["model"].(map[string]any)["base_url"] = spec.APIURL
	}
	if len(mcpServers) > 0 {
		if servers := buildHermesMCPServers(logger, mcpServers); len(servers) > 0 {
			cfg["mcp_servers"] = servers
		}
	}

	cfg["memory"] = map[string]any{"memory_enabled": false}
	cfg["curator"] = map[string]any{"enabled": false}
	cfg["skills"] = map[string]any{"guard_agent_created": true}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		logger.Error("Hermes: marshal config.yaml failed", zap.Error(err))
		return
	}
	if existing, err := os.ReadFile(configPath); err == nil && string(existing) == string(data) {
		return
	}
	if err := os.MkdirAll(hermesHome, 0o755); err != nil {
		logger.Error("Hermes: create HERMES_HOME failed", zap.Error(err))
		return
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		logger.Error("Hermes: generate config.yaml failed", zap.Error(err))
		return
	}
	logger.Info("Hermes: config.yaml generated/updated",
		zap.String("path", configPath),
		zap.String("model", spec.DefaultModel),
		zap.Int("mcpServers", len(mcpServers)))
}

func loadHermesConfig(logger *zap.Logger, configPath string) map[string]any {
	cfg := map[string]any{}
	existing, err := os.ReadFile(configPath)
	if err != nil {
		return cfg
	}
	if err := yaml.Unmarshal(existing, &cfg); err != nil {
		logger.Warn("Hermes: existing config.yaml parse failed; regenerating",
			zap.String("path", configPath),
			zap.Error(err))
		return map[string]any{}
	}
	return cfg
}

func buildHermesMCPServers(logger *zap.Logger, specs []agent.MCPServerSpec) map[string]any {
	servers := make(map[string]any, len(specs))
	for _, spec := range specs {
		if spec.Name == "" {
			continue
		}
		server := map[string]any{"enabled": true}
		switch spec.Type {
		case "http":
			if spec.URL == "" {
				continue
			}
			server["url"] = spec.URL
			if len(spec.Headers) > 0 {
				server["headers"] = spec.Headers
			}
		case "stdio":
			if spec.Command == "" {
				continue
			}
			server["command"] = spec.Command
			if len(spec.Args) > 0 {
				server["args"] = spec.Args
			}
			if len(spec.Env) > 0 {
				server["env"] = spec.Env
			}
		default:
			logger.Warn("Hermes: unsupported MCP server transport",
				zap.String("name", spec.Name),
				zap.String("transport", spec.Type))
			continue
		}
		servers[spec.Name] = server
	}
	return servers
}

func buildHermesEnv(logger *zap.Logger, spec agent.AgentSpec) []string {
	env := []string{
		"NO_PROXY=127.0.0.1,localhost,::1",
		"no_proxy=127.0.0.1,localhost,::1",
	}
	if spec.APIURL != "" || spec.APIToken != "" {
		env = append(env, "HERMES_INFERENCE_PROVIDER=custom")
		if spec.APIURL != "" {
			env = append(env, "CUSTOM_BASE_URL="+spec.APIURL)
		}
		if spec.APIToken != "" {
			env = append(env, "OPENAI_API_KEY="+spec.APIToken)
		}
	}
	if home := runtimeHome(spec); home != "" {
		env = append(env, "HERMES_HOME="+absPath(home))
	}
	logger.Info("Hermes: env configured",
		zap.String("model", spec.DefaultModel),
		zap.String("baseURL", maskSecret(spec.APIURL)),
		zap.String("apiKey", maskSecret(spec.APIToken)),
		zap.String("runtimeHome", runtimeHome(spec)))
	return env
}

func runtimeHome(spec agent.AgentSpec) string {
	if spec.RuntimeHome != "" {
		return spec.RuntimeHome
	}
	return spec.HermesHome
}

func absPath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func maskSecret(s string) string {
	if s == "" {
		return "<empty>"
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}
