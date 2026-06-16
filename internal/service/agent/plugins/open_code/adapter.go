// OpenCode ACP adapter, adapted from Colink's OpenCode plugin.
package open_code

import (
	"encoding/json"
	"os"
	"path/filepath"

	"callme/internal/service/agent"
	"callme/internal/service/agent/plugins/acp"

	"go.uber.org/zap"
)

// OpenCodeAdapter runs OpenCode in ACP mode.
type OpenCodeAdapter struct {
	*acp.BaseAdapter
}

// NewOpenCodeAdapter creates an OpenCode adapter.
func NewOpenCodeAdapter() agent.Adapter {
	config := acp.AdapterConfig{
		BuildArgs: func(req *agent.SessionRequest) []string {
			return []string{"acp"}
		},
		BuildEnv: func(req *agent.SessionRequest) []string {
			return buildOpenCodeEnv(req)
		},
	}
	return &OpenCodeAdapter{BaseAdapter: acp.NewBaseAdapter(config)}
}

func buildOpenCodeEnv(req *agent.SessionRequest) []string {
	env := make([]string, 0, 4)
	if req == nil {
		env = append(env, "OPENCODE_PURE=1")
		return env
	}

	if req.WorkDir != "" {
		configDir := filepath.Join(req.WorkDir, ".opencode")
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			acp.LogWarn("OpenCode: create config dir failed", zap.String("path", configDir), zap.Error(err))
		}
		env = append(env, "OPENCODE_CONFIG_DIR="+configDir)
	}

	configContent := buildOpenCodeConfigContent(req.Spec)
	if configContent != "" {
		env = append(env, "OPENCODE_CONFIG_CONTENT="+configContent)
	}

	// Keep runtime isolated from user-level OpenCode plugins/config.
	env = append(env, "OPENCODE_PURE=1")

	acp.LogInfo("OpenCode: env configured",
		zap.String("model", req.Spec.DefaultModel),
		zap.String("baseURL", maskSecret(req.Spec.APIURL)),
		zap.String("apiKey", maskSecret(req.Spec.APIToken)))
	return env
}

func buildOpenCodeConfigContent(spec agent.AgentSpec) string {
	if spec.APIURL == "" && spec.APIToken == "" && spec.DefaultModel == "" {
		return ""
	}

	cfg := openCodeConfig{
		Provider: map[string]openCodeProvider{
			"callme": {
				Name: "Callme Provider",
				Npm:  "@ai-sdk/openai-compatible",
				Options: openCodeProviderOptions{
					APIKey:  spec.APIToken,
					BaseURL: spec.APIURL,
				},
			},
		},
	}

	if spec.DefaultModel != "" {
		provider := cfg.Provider["callme"]
		provider.Models = map[string]openCodeModel{
			spec.DefaultModel: {
				ID:   spec.DefaultModel,
				Name: spec.DefaultModel,
			},
		}
		cfg.Provider["callme"] = provider
		cfg.Model = "callme/" + spec.DefaultModel
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		acp.LogWarn("OpenCode: marshal config failed", zap.Error(err))
		return ""
	}
	return string(data)
}

type openCodeConfig struct {
	Provider map[string]openCodeProvider `json:"provider,omitempty"`
	Model    string                      `json:"model,omitempty"`
}

type openCodeProvider struct {
	Name    string                   `json:"name,omitempty"`
	Npm     string                   `json:"npm,omitempty"`
	Options openCodeProviderOptions  `json:"options,omitempty"`
	Models  map[string]openCodeModel `json:"models,omitempty"`
}

type openCodeProviderOptions struct {
	APIKey  string `json:"apiKey,omitempty"`
	BaseURL string `json:"baseURL,omitempty"`
}

type openCodeModel struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
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
