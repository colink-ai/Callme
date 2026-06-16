package agent

import (
	"fmt"
	"sort"
	"sync"

	"go.uber.org/zap"
)

// PluginMeta 插件元数据（移植自 Colink agent.PluginMeta）
type PluginMeta struct {
	Type        string  // 插件类型："hermes"
	Name        string  // 显示名称
	Description string  // 描述
	Factory     Factory // 适配器工厂
	DefaultPath string  // 默认 CLI 路径
}

// Factory 适配器工厂函数
type Factory func() Adapter

// PluginTypeInfo 插件类型信息（API 返回）
type PluginTypeInfo struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	DefaultPath string `json:"defaultPath"`
}

type registry struct {
	plugins map[string]PluginMeta
	mu      sync.RWMutex
}

var globalRegistry = &registry{plugins: make(map[string]PluginMeta)}

// RegisterPlugin 注册插件（插件包 init() 调用）
func RegisterPlugin(meta PluginMeta) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	if _, exists := globalRegistry.plugins[meta.Type]; exists {
		panic(fmt.Sprintf("agent plugin %s already registered", meta.Type))
	}
	globalRegistry.plugins[meta.Type] = meta
}

// GetAdapter 按类型获取适配器实例
func GetAdapter(typ string) Adapter {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	meta, exists := globalRegistry.plugins[typ]
	if !exists {
		return nil
	}
	return meta.Factory()
}

// DefaultPathFor 返回插件默认 CLI 路径。
func DefaultPathFor(typ string) string {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	meta, exists := globalRegistry.plugins[typ]
	if !exists {
		return ""
	}
	return meta.DefaultPath
}

// GetTypes 列出所有已注册插件类型
func GetTypes() []PluginTypeInfo {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()
	types := make([]PluginTypeInfo, 0, len(globalRegistry.plugins))
	for _, meta := range globalRegistry.plugins {
		types = append(types, PluginTypeInfo{
			Type:        meta.Type,
			Name:        meta.Name,
			Description: meta.Description,
			DefaultPath: meta.DefaultPath,
		})
	}
	sort.Slice(types, func(i, j int) bool { return types[i].Type < types[j].Type })
	return types
}

// ---------- 包级日志（与 Colink agent 包模式一致） ----------

var logger = zap.NewNop()

// SetLogger 注入 zap logger
func SetLogger(l *zap.Logger) {
	if l != nil {
		logger = l
	}
}

// LogInfo info 级日志
func LogInfo(msg string, fields ...zap.Field) { logger.Info(msg, fields...) }

// LogWarn warn 级日志
func LogWarn(msg string, fields ...zap.Field) { logger.Warn(msg, fields...) }

// LogError error 级日志
func LogError(msg string, fields ...zap.Field) { logger.Error(msg, fields...) }

// LogDebug debug 级日志
func LogDebug(msg string, fields ...zap.Field) { logger.Debug(msg, fields...) }
