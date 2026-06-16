// Package open_code registers the OpenCode ACP agent plugin.
package open_code

import "callme/internal/service/agent"

// Type OpenCode plugin type.
const Type = "open_code"

func init() {
	agent.RegisterPlugin(agent.PluginMeta{
		Type:        Type,
		Name:        "OpenCode",
		Description: "OpenCode CLI via ACP - structured output",
		Factory:     NewOpenCodeAdapter,
		DefaultPath: "opencode",
	})
}
