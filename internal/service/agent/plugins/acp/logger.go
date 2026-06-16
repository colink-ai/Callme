// 日志辅助：委托给 agent 包（与 Colink 模式一致，供 hermes 等插件复用）
package acp

import (
	"callme/internal/service/agent"

	"go.uber.org/zap"
)

// LogInfo info 级日志
func LogInfo(msg string, fields ...zap.Field) { agent.LogInfo(msg, fields...) }

// LogWarn warn 级日志
func LogWarn(msg string, fields ...zap.Field) { agent.LogWarn(msg, fields...) }

// LogError error 级日志
func LogError(msg string, fields ...zap.Field) { agent.LogError(msg, fields...) }

// LogDebug debug 级日志
func LogDebug(msg string, fields ...zap.Field) { agent.LogDebug(msg, fields...) }
