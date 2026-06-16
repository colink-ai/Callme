// Hermes ACP 适配器，移植自 Colink internal/service/agent/plugins/hermes/adapter.go
//
// 模型切换机制（与 Colink 一致）：
//   - Hermes ACP 不从环境变量读取模型配置，必须通过 HERMES_HOME/config.yaml；
//   - 每次启动会话前生成/更新 config.yaml（model.default / provider: custom / base_url）；
//   - 环境变量补充 provider 运行时配置：HERMES_INFERENCE_PROVIDER / CUSTOM_BASE_URL / OPENAI_API_KEY；
//   - HERMES_HOME 指向共享持久化目录 —— Hermes 的自学习记忆跨会话累积，系统越用越聪明。
package hermes

import (
	"os"
	"path/filepath"

	"callme/internal/service/agent"
	"callme/internal/service/agent/plugins/acp"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// HermesAdapter 基于 ACP 协议的 Hermes 适配器
type HermesAdapter struct {
	*acp.BaseAdapter
}

// NewHermesAdapter 创建 Hermes 适配器
func NewHermesAdapter() agent.Adapter {
	config := acp.AdapterConfig{
		BuildArgs: func(req *agent.SessionRequest) []string {
			return []string{"acp"}
		},
		BuildEnv: func(req *agent.SessionRequest) []string {
			// 每次启动前生成/更新 config.yaml，确保模型配置变化即时生效
			generateHermesConfig(req.Spec, req.MCPServers)
			return buildHermesEnv(req.Spec)
		},
	}
	return &HermesAdapter{BaseAdapter: acp.NewBaseAdapter(config)}
}

// generateHermesConfig 在 HERMES_HOME 下生成 config.yaml（模型配置与 Hermes 本地 MCP 配置）
func generateHermesConfig(spec agent.AgentSpec, mcpServers []agent.MCPServerSpec) {
	if spec.HermesHome == "" {
		return
	}

	hermesHome := absPath(spec.HermesHome)
	configPath := filepath.Join(hermesHome, "config.yaml")

	cfg := loadHermesConfig(configPath)
	cfg["model"] = map[string]any{
		"default": spec.DefaultModel,
	}
	if spec.APIURL != "" || spec.APIToken != "" {
		cfg["model"].(map[string]any)["provider"] = "custom"
		cfg["model"].(map[string]any)["base_url"] = spec.APIURL
	}
	if len(mcpServers) > 0 {
		if servers := buildHermesMCPServers(mcpServers); len(servers) > 0 {
			cfg["mcp_servers"] = servers
		}
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		acp.LogError("Hermes: marshal config.yaml failed", zap.Error(err))
		return
	}

	// 内容相同则跳过写入
	if existing, err := os.ReadFile(configPath); err == nil && string(existing) == string(data) {
		return
	}

	if err := os.MkdirAll(hermesHome, 0o755); err != nil {
		acp.LogError("Hermes: create HERMES_HOME failed", zap.Error(err))
		return
	}
	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		acp.LogError("Hermes: generate config.yaml failed", zap.Error(err))
		return
	}
	acp.LogInfo("Hermes: config.yaml generated/updated",
		zap.String("path", configPath),
		zap.String("model", spec.DefaultModel),
		zap.Int("mcpServers", len(mcpServers)))
}

func loadHermesConfig(configPath string) map[string]any {
	cfg := map[string]any{}
	existing, err := os.ReadFile(configPath)
	if err != nil {
		return cfg
	}
	if err := yaml.Unmarshal(existing, &cfg); err != nil {
		acp.LogWarn("Hermes: existing config.yaml parse failed; regenerating",
			zap.String("path", configPath),
			zap.Error(err))
		return map[string]any{}
	}
	return cfg
}

func buildHermesMCPServers(specs []agent.MCPServerSpec) map[string]any {
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
			acp.LogWarn("Hermes: unsupported MCP server transport",
				zap.String("name", spec.Name),
				zap.String("transport", spec.Type))
			continue
		}
		servers[spec.Name] = server
	}
	return servers
}

// buildHermesEnv 构造 Hermes 进程环境变量
func buildHermesEnv(spec agent.AgentSpec) []string {
	env := []string{
		"NO_PROXY=127.0.0.1,localhost,::1",
		"no_proxy=127.0.0.1,localhost,::1",
	}

	if spec.APIURL != "" || spec.APIToken != "" {
		env = append(env, "HERMES_INFERENCE_PROVIDER=custom")
		if spec.APIURL != "" {
			env = append(env, "CUSTOM_BASE_URL="+spec.APIURL)
		}
		// Hermes custom provider 的 key 走 OPENAI_API_KEY 回退链
		if spec.APIToken != "" {
			env = append(env, "OPENAI_API_KEY="+spec.APIToken)
		}
	}

	if spec.HermesHome != "" {
		env = append(env, "HERMES_HOME="+absPath(spec.HermesHome))
	}

	acp.LogInfo("Hermes: env configured",
		zap.String("model", spec.DefaultModel),
		zap.String("baseURL", maskSecret(spec.APIURL)),
		zap.String("apiKey", maskSecret(spec.APIToken)),
		zap.String("hermesHome", spec.HermesHome))
	return env
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

// maskSecret 日志脱敏
func maskSecret(s string) string {
	if s == "" {
		return "<empty>"
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "****" + s[len(s)-4:]
}
