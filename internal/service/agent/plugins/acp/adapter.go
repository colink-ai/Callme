// ACP 基础适配器，移植自 Colink internal/service/agent/plugins/acp/adapter_base.go
// 改造点：客服场景为"常驻会话 + 多轮 Prompt"，每轮 Prompt 携带自己的流式回调；
// Colink 的一次性 ExecuteWithStream 模式不适用，故去掉。
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"callme/internal/service/agent"

	"go.uber.org/zap"
)

// AdapterConfig ACP 适配器配置（供 hermes 等插件复用）
type AdapterConfig struct {
	BuildArgs func(req *agent.SessionRequest) []string
	BuildEnv  func(req *agent.SessionRequest) []string
	// ConfigureModelViaACP 为 true 时在 session/new 后通过 ACP（set_config_option / set_model）设置模型。
	// Hermes 通过 config.yaml 的 model.default 设置模型，set_model 既冗余又会报 -32603，
	// 因此默认 false（跳过），省掉一次失败的握手往返。
	ConfigureModelViaACP bool
}

// maxStderrSize stderr 缓冲上限（64KB），防止异常输出撑爆内存
const maxStderrSize = 64 * 1024
const startupTimeout = 45 * time.Second

// promptTimeout 单轮回答上限。模型/网关极端慢或挂死时（如某些视觉端点处理图片
// 可能数分钟无响应），超时让本轮干净失败，避免 UI 永远卡在"正在生成回复"。
const promptTimeout = 5 * time.Minute

type acpSession struct {
	id           string // ACP 协议会话 ID
	transport    *acpTransport
	cmd          *exec.Cmd
	cancel       context.CancelFunc
	status       agent.SessionStatus
	nativeResume bool
	stderrOutput strings.Builder
	onChunk      func(agent.Chunk) // 当前轮的流式回调（Prompt 期间有效）
	mu           sync.Mutex
}

func (s *acpSession) emit(chunk agent.Chunk) {
	s.mu.Lock()
	cb := s.onChunk
	s.mu.Unlock()
	if cb != nil {
		cb(chunk)
	}
}

// BaseAdapter ACP 协议基础适配器
// 生命周期: StartSession(进程+initialize+session/new) -> Prompt* -> StopSession
type BaseAdapter struct {
	Config   AdapterConfig
	sessions map[string]*acpSession // 业务 sessionID -> ACP 会话
	mu       sync.RWMutex
}

// NewBaseAdapter 创建基础适配器
func NewBaseAdapter(config AdapterConfig) *BaseAdapter {
	return &BaseAdapter{Config: config, sessions: make(map[string]*acpSession)}
}

// StartSession 拉起 Agent 进程并完成 ACP 握手与会话创建
func (a *BaseAdapter) StartSession(ctx context.Context, sessionID string, req *agent.SessionRequest) error {
	a.mu.Lock()
	if _, exists := a.sessions[sessionID]; exists {
		a.mu.Unlock()
		return fmt.Errorf("ACP: session already exists: %s", sessionID)
	}
	a.mu.Unlock()

	cliPath := req.Spec.CliPath
	args := a.Config.BuildArgs(req)
	startupCtx, startupCancel := context.WithTimeout(ctx, startupTimeout)
	defer startupCancel()

	// 会话生命周期独立于调用方 ctx：进程要跨多轮请求存活
	sessionCtx, sessionCancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(sessionCtx, cliPath, args...)
	if req.WorkDir != "" {
		if err := os.MkdirAll(req.WorkDir, 0o755); err != nil {
			sessionCancel()
			return fmt.Errorf("ACP: create workdir: %w", err)
		}
		cmd.Dir = req.WorkDir
	}
	cmd.Env = mergeEnv(a.Config.BuildEnv(req))

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		sessionCancel()
		return fmt.Errorf("ACP: stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		sessionCancel()
		return fmt.Errorf("ACP: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		sessionCancel()
		return fmt.Errorf("ACP: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		sessionCancel()
		return fmt.Errorf("ACP: start CLI process: %w", err)
	}

	session := &acpSession{
		cmd:    cmd,
		cancel: sessionCancel,
		status: agent.SessionStatusRunning,
	}

	// stderr 消费（诊断用）
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			line := scanner.Text()
			LogDebug("ACP: stderr", zap.String("sessionID", sessionID), zap.String("line", line))
			session.mu.Lock()
			if session.stderrOutput.Len() < maxStderrSize {
				session.stderrOutput.WriteString(line)
				session.stderrOutput.WriteString("\n")
			}
			session.mu.Unlock()
		}
	}()

	transport := newACPTransport(stdinPipe, stdoutPipe, func(method string, params json.RawMessage) {
		a.handleNotification(session, method, params)
	})
	session.transport = transport
	transport.Start()

	failStart := func(stage string, cause error) error {
		session.mu.Lock()
		stderrContent := session.stderrOutput.String()
		session.mu.Unlock()
		a.teardown(session)
		return fmt.Errorf("ACP: %s failed: %w\nstderr: %s", stage, cause, stderrContent)
	}

	initResult, err := transport.SendRequestContext(startupCtx, "initialize", &acpInitializeParams{
		ProtocolVersion:    2025,
		ClientCapabilities: acpClientCapabilities{},
	})
	if err != nil {
		return failStart("initialize handshake", err)
	}
	var initResp acpInitializeResult
	if err := json.Unmarshal(initResult, &initResp); err != nil {
		LogWarn("ACP: initialize response parse warning", zap.Error(err))
	}

	_ = initResp

	var sessionResp acpNewSessionResult
	if req.ResumeSessionID != "" && supportsSessionResume(initResp.AgentCapabilities) {
		resumeResult, err := transport.SendRequestContext(startupCtx, "session/resume", &acpResumeSessionParams{
			CWD:        req.WorkDir,
			SessionID:  req.ResumeSessionID,
			MCPServers: []any{},
		})
		if err != nil {
			return failStart("session/resume", err)
		}
		var resumeResp acpResumeSessionResult
		if err := json.Unmarshal(resumeResult, &resumeResp); err == nil {
			sessionResp.ConfigOptions = resumeResp.ConfigOptions
		}
		session.id = req.ResumeSessionID
		session.nativeResume = true
	} else {
		if req.ResumeSessionID != "" {
			LogWarn("ACP: agent does not advertise session/resume; falling back to session/new",
				zap.String("sessionID", sessionID),
				zap.String("resumeSessionID", req.ResumeSessionID))
		}
		sessionNewResult, err := transport.SendRequestContext(startupCtx, "session/new", &acpNewSessionParams{
			CWD:        req.WorkDir,
			MCPServers: []any{},
		})
		if err != nil {
			return failStart("session/new", err)
		}
		if err := json.Unmarshal(sessionNewResult, &sessionResp); err == nil && sessionResp.SessionID != "" {
			session.id = sessionResp.SessionID
		} else {
			session.id = sessionID
		}
	}

	// 默认跳过 ACP 模型配置：Hermes 已通过 config.yaml 设好模型，set_model 冗余且会报错。
	if a.Config.ConfigureModelViaACP {
		if err := a.configureModel(startupCtx, transport, session, &sessionResp, req.Spec.DefaultModel); err != nil {
			LogWarn("ACP: session model configuration warning", zap.Error(err))
		}
	}

	a.mu.Lock()
	a.sessions[sessionID] = session
	a.mu.Unlock()

	LogInfo("ACP: session started",
		zap.String("sessionID", sessionID),
		zap.String("acpSessionID", session.id),
		zap.Bool("nativeResume", session.nativeResume),
		zap.String("model", req.Spec.DefaultModel))
	return nil
}

// Prompt 发送一轮输入，阻塞至本轮回答结束；期间 session/update 通知经 onChunk 流式回调
func (a *BaseAdapter) Prompt(ctx context.Context, sessionID string, input string, images []agent.ImageContent, onChunk func(agent.Chunk)) error {
	a.mu.RLock()
	session, exists := a.sessions[sessionID]
	a.mu.RUnlock()
	if !exists {
		return fmt.Errorf("ACP: session not found: %s", sessionID)
	}

	session.mu.Lock()
	if session.transport == nil {
		session.mu.Unlock()
		return fmt.Errorf("ACP: session transport not available: %s", sessionID)
	}
	session.onChunk = onChunk
	session.mu.Unlock()

	defer func() {
		session.mu.Lock()
		session.onChunk = nil
		session.mu.Unlock()
	}()

	// 给单轮回答设上限：模型/网关挂死时干净失败，而不是无限阻塞占着坐席。
	promptCtx, cancel := context.WithTimeout(ctx, promptTimeout)
	defer cancel()
	promptResult, err := session.transport.SendRequestContext(promptCtx, "session/prompt", &acpPromptParams{
		SessionID: session.id,
		Prompt:    buildContentBlocks(input, images),
	})
	if err != nil {
		if promptCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("ACP: 回答超时（超过 %s 模型仍无响应）", promptTimeout)
		}
		return fmt.Errorf("ACP: session/prompt failed: %w", err)
	}

	var promptResp acpPromptResult
	if err := json.Unmarshal(promptResult, &promptResp); err == nil {
		LogDebug("ACP: prompt finished", zap.String("sessionID", sessionID), zap.String("stopReason", promptResp.StopReason))
	}
	return nil
}

func buildContentBlocks(text string, images []agent.ImageContent) []acpContentBlock {
	blocks := []acpContentBlock{{Type: "text", Text: text}}
	for _, img := range images {
		// ACP ImageContentBlock: {type:"image", data:<base64>, mimeType:"image/png"}
		blocks = append(blocks, acpContentBlock{
			Type:     "image",
			Data:     img.Data,
			MimeType: img.MimeType,
		})
	}
	return blocks
}

// StopSession 停止会话并回收进程
func (a *BaseAdapter) StopSession(sessionID string) error {
	a.mu.Lock()
	session, exists := a.sessions[sessionID]
	if !exists {
		a.mu.Unlock()
		return nil
	}
	delete(a.sessions, sessionID)
	a.mu.Unlock()

	session.mu.Lock()
	session.status = agent.SessionStatusStopped
	session.mu.Unlock()

	a.teardown(session)
	LogInfo("ACP: session stopped", zap.String("sessionID", sessionID))
	return nil
}

// GetSessionStatus 查询会话状态
func (a *BaseAdapter) GetSessionStatus(sessionID string) agent.SessionStatus {
	a.mu.RLock()
	session, exists := a.sessions[sessionID]
	a.mu.RUnlock()
	if !exists {
		return agent.SessionStatusIdle
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.status
}

func (a *BaseAdapter) AgentSessionID(sessionID string) string {
	a.mu.RLock()
	session, exists := a.sessions[sessionID]
	a.mu.RUnlock()
	if !exists {
		return ""
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.id
}

func (a *BaseAdapter) UsedNativeResume(sessionID string) bool {
	a.mu.RLock()
	session, exists := a.sessions[sessionID]
	a.mu.RUnlock()
	if !exists {
		return false
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.nativeResume
}

func supportsSessionResume(capabilities map[string]any) bool {
	if len(capabilities) == 0 {
		return false
	}
	for _, key := range []string{"sessionCapabilities", "session_capabilities"} {
		raw, ok := capabilities[key]
		if !ok {
			continue
		}
		sessionCaps, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		resume, ok := sessionCaps["resume"]
		if !ok || resume == nil {
			return false
		}
		if b, ok := resume.(bool); ok {
			return b
		}
		return true
	}
	return false
}

// CheckHealth 拉起进程完成 initialize 握手以验证 CLI 可用
func (a *BaseAdapter) CheckHealth(ctx context.Context, spec agent.AgentSpec) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req := &agent.SessionRequest{Spec: spec}
	cmd := exec.CommandContext(ctx, spec.CliPath, a.Config.BuildArgs(req)...)
	cmd.Dir = os.TempDir()
	cmd.Env = mergeEnv(a.Config.BuildEnv(req))

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("ACP: health check stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ACP: health check stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ACP: health check start failed: %w", err)
	}

	transport := newACPTransport(stdinPipe, stdoutPipe, nil)
	transport.Start()
	defer transport.Close()

	if _, err := transport.SendRequestContext(ctx, "initialize", &acpInitializeParams{
		ProtocolVersion:    2025,
		ClientCapabilities: acpClientCapabilities{},
	}); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return fmt.Errorf("ACP: health check initialize failed: %w", err)
	}

	transport.Close()
	cmd.Wait()
	return nil
}

func (a *BaseAdapter) handleNotification(session *acpSession, method string, params json.RawMessage) {
	switch method {
	case "session/update":
		var updateParams acpSessionUpdateParams
		if err := json.Unmarshal(params, &updateParams); err != nil {
			LogError("ACP: parse session/update params", zap.Error(err))
			return
		}
		chunks, err := parseACPSessionUpdate(updateParams.Update)
		if err != nil {
			LogError("ACP: parse session update", zap.Error(err))
			return
		}
		for _, chunk := range chunks {
			session.emit(chunk)
		}

	case "session/request_permission":
		// 客服场景无人值守：自动放行（Agent 工具面仅有只读知识检索）
		if session.transport != nil {
			session.transport.SendNotification("session/resolve_permission", &acpPermissionResponse{Allow: "allow_always"})
		}

	default:
		LogDebug("ACP: unknown notification", zap.String("method", method))
	}
}

func (a *BaseAdapter) configureModel(ctx context.Context, transport *acpTransport, session *acpSession, sessionResp *acpNewSessionResult, desiredModel string) error {
	if desiredModel == "" {
		return nil
	}
	// 优先 configOptions（新 API），否则 legacy session/set_model
	for _, opt := range sessionResp.ConfigOptions {
		if opt.ConfigID == "model" {
			if _, err := transport.SendRequestContext(ctx, "session/set_config_option", &acpSetConfigOptionParams{
				SessionID: session.id, ConfigID: "model", Value: desiredModel,
			}); err != nil {
				return fmt.Errorf("set_config_option model=%s: %w", desiredModel, err)
			}
			LogInfo("ACP: model set via configOptions", zap.String("model", desiredModel))
			return nil
		}
	}
	if _, err := transport.SendRequestContext(ctx, "session/set_model", &acpSetModelParams{
		SessionID: session.id, ModelID: desiredModel,
	}); err != nil {
		return fmt.Errorf("set_model %s: %w", desiredModel, err)
	}
	LogInfo("ACP: model set via legacy API", zap.String("model", desiredModel))
	return nil
}

func (a *BaseAdapter) teardown(session *acpSession) {
	if session.transport != nil {
		session.transport.Close()
	}
	if session.cancel != nil {
		session.cancel()
	}
	if session.cmd != nil && session.cmd.Process != nil {
		done := make(chan error, 1)
		go func() { done <- session.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			LogWarn("ACP: process still running, killing process tree", zap.Int("pid", session.cmd.Process.Pid))
			killProcessTree(session.cmd.Process)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				<-done
			}
		}
	}
}

// mergeEnv 把插件环境变量叠加到当前进程环境之上
func mergeEnv(extra []string) []string {
	envMap := make(map[string]string)
	for _, e := range os.Environ() {
		if idx := strings.Index(e, "="); idx > 0 {
			envMap[e[:idx]] = e[idx+1:]
		}
	}
	for _, e := range extra {
		if idx := strings.Index(e, "="); idx > 0 {
			envMap[e[:idx]] = e[idx+1:]
		}
	}
	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, k+"="+v)
	}
	return env
}
