// Package runtime is Callme's thin integration layer over Helios.
//
// Agent protocol adapters live in Helios. Callme owns application concerns:
// settings, domain isolation, session persistence, WebSocket forwarding, and
// audit-friendly rendering of Helios events.
package runtime

import (
	"context"
	"fmt"
	"sync"

	"callme/internal/service/agent"

	helioshermes "github.com/colink-ai/helios/adapters/hermes"
	helioscontracts "github.com/colink-ai/helios/contracts"
	helios "github.com/colink-ai/helios/runtime"
	"go.uber.org/zap"
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
	logger  *zap.Logger
	adapter *HeliosAdapter
}

func NewService(logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{logger: logger, adapter: newHeliosAdapter(logger)}
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
	return s.adapter, nil
}

func (s *Service) CheckHealth(ctx context.Context, spec agent.AgentSpec) error {
	if _, err := s.NewAdapter(spec); err != nil {
		return err
	}
	return s.adapter.CheckHealth(ctx, spec)
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
	if err := helioshermes.Register(registry, helioshermes.WithConfigMutator(applyCallmeHermesConfig)); err != nil {
		logger.Error("register Helios Hermes adapter failed", zap.Error(err))
	}
	return registry
}

func (h *HeliosAdapter) StartSession(ctx context.Context, sessionID string, req *agent.SessionRequest) error {
	if req == nil {
		return fmt.Errorf("session request is required")
	}
	handle, err := h.engine.StartSession(ctx, helios.SessionRequest{
		SessionID:         sessionID,
		Agent:             toHeliosAgentSpec(req.Spec),
		WorkDir:           req.WorkDir,
		RuntimeConfigMode: helios.RuntimeConfigIsolated,
		RuntimeHome:       runtimeHome(req.Spec),
		MCPServers:        toHeliosMCPServers(req.MCPServers),
		ResumeSessionID:   req.ResumeSessionID,
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
	if event.SessionID == "" {
		return nil
	}
	var chunk agent.Chunk
	switch {
	case event.Chunk != nil:
		chunk = fromHeliosChunk(*event.Chunk)
	case event.Usage != nil:
		chunk = agent.Chunk{Type: agent.ChunkTypeUsage, Usage: fromHeliosUsage(event.Usage)}
	default:
		return nil
	}
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
		Type:               helioshermes.Type,
		CLIPath:            spec.CliPath,
		DefaultModel:       spec.DefaultModel,
		APIURL:             spec.APIURL,
		APIToken:           spec.APIToken,
		RuntimeConfigMode:  helios.RuntimeConfigIsolated,
		RuntimeHome:        runtimeHome(spec),
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

func toHeliosImages(images []agent.ImageContent) []helioscontracts.ImageContent {
	out := make([]helioscontracts.ImageContent, 0, len(images))
	for _, image := range images {
		out = append(out, helioscontracts.ImageContent{MimeType: image.MimeType, Data: image.Data, URL: image.URL})
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
		Metadata:  chunk.Metadata,
	}
	if chunk.Usage != nil {
		out.Usage = fromHeliosUsage(chunk.Usage)
	}
	if len(chunk.ToolResultBlocks) > 0 {
		out.ToolResultBlocks = make([]agent.ContentBlock, 0, len(chunk.ToolResultBlocks))
		for _, block := range chunk.ToolResultBlocks {
			out.ToolResultBlocks = append(out.ToolResultBlocks, agent.ContentBlock{
				Type:     block.Type,
				Text:     block.Text,
				MimeType: block.MimeType,
				Data:     block.Data,
				URL:      block.URL,
				Metadata: block.Metadata,
			})
		}
	}
	return out
}

func fromHeliosUsage(usage *helioscontracts.TokenUsage) *agent.TokenUsage {
	if usage == nil {
		return nil
	}
	return &agent.TokenUsage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
	}
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
	case helioscontracts.ChunkInputJSONDelta:
		return agent.ChunkTypeInputJSONDelta
	case helioscontracts.ChunkUsage:
		return agent.ChunkTypeUsage
	case helioscontracts.ChunkQuestion:
		return agent.ChunkTypeQuestion
	case helioscontracts.ChunkPermission:
		return agent.ChunkTypePermission
	case helioscontracts.ChunkArtifact:
		return agent.ChunkTypeArtifact
	case helioscontracts.ChunkHandoff:
		return agent.ChunkTypeHandoff
	case helioscontracts.ChunkDone:
		return agent.ChunkTypeDone
	default:
		return agent.ChunkTypeStatus
	}
}

func runtimeHome(spec agent.AgentSpec) string {
	if spec.RuntimeHome != "" {
		return spec.RuntimeHome
	}
	return spec.HermesHome
}

func applyCallmeHermesConfig(cfg map[string]any) {
	mergeConfigSection(cfg, "memory", map[string]any{"memory_enabled": false})
	mergeConfigSection(cfg, "curator", map[string]any{"enabled": false})
	mergeConfigSection(cfg, "skills", map[string]any{"guard_agent_created": true})
}

func mergeConfigSection(cfg map[string]any, key string, values map[string]any) {
	section, _ := cfg[key].(map[string]any)
	if section == nil {
		section = map[string]any{}
	}
	for k, v := range values {
		section[k] = v
	}
	cfg[key] = section
}
