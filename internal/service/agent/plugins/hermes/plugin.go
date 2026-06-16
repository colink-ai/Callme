// Package hermes Hermes Agent 插件 - 自进化 AI 代理
// 移植自 Colink internal/service/agent/plugins/hermes
package hermes

import (
	"callme/internal/service/agent"
)

// Type Hermes 插件类型常量
const Type = "hermes"

func init() {
	agent.RegisterPlugin(agent.PluginMeta{
		Type:        Type,
		Name:        "Hermes",
		Description: "Hermes Agent via ACP - 自进化AI代理",
		Factory:     NewHermesAdapter,
		DefaultPath: "hermes",
	})
}
