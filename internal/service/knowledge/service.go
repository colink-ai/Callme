// Package knowledge 知识源服务
// 移植自 Colink internal/service/knowledge（裁剪为 MCP 类型）：
//   - 主通道：把代码图谱 / wiki(LLMwiki) 图谱的 MCP 端点注入 Hermes 会话，Agent 自主调用工具检索；
//   - 辅通道：后端直接代理查询（queryMCP），用于健康检查与运营统计。
package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"callme/internal/config"
	"callme/internal/service/agent"

	"go.uber.org/zap"
)

// Service 知识源服务
type Service struct {
	sources []config.KnowledgeSource
	logger  *zap.Logger
	client  *http.Client
}

// NewService 创建知识源服务
func NewService(cfg config.KnowledgeConfig, logger *zap.Logger) *Service {
	return &Service{
		sources: cfg.Sources,
		logger:  logger,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// SourceInfo 知识源信息（API 返回）
type SourceInfo struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Transport   string `json:"transport"`
	Healthy     *bool  `json:"healthy,omitempty"`
}

// ListSources 列出已配置知识源
func (s *Service) ListSources() []SourceInfo {
	infos := make([]SourceInfo, 0, len(s.sources))
	for _, src := range s.sources {
		infos = append(infos, SourceInfo{Name: src.Name, DisplayName: src.DisplayName, Transport: src.Transport})
	}
	return infos
}

// MCPServerSpecs 把知识源转换为注入 Agent 会话的 MCP server 配置
func (s *Service) MCPServerSpecs() []agent.MCPServerSpec {
	specs := make([]agent.MCPServerSpec, 0, len(s.sources))
	for _, src := range s.sources {
		spec := agent.MCPServerSpec{Name: src.Name, Type: src.Transport}
		switch src.Transport {
		case "http":
			spec.URL = src.URL
			if src.Token != "" {
				spec.Headers = map[string]string{"Authorization": "Bearer " + src.Token}
			}
		case "stdio":
			spec.Command = src.Command
			spec.Args = src.Args
			spec.Env = src.Env
		default:
			s.logger.Warn("knowledge: unsupported transport, skipped",
				zap.String("source", src.Name), zap.String("transport", src.Transport))
			continue
		}
		specs = append(specs, spec)
	}
	return specs
}

// QueryResult 代理查询结果
type QueryResult struct {
	Source  string `json:"source"`
	Query   string `json:"query"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Query 通过 MCP tools/call 代理查询单个知识源（移植自 Colink queryMCP）
func (s *Service) Query(ctx context.Context, sourceName, query string, limit int) (*QueryResult, error) {
	var src *config.KnowledgeSource
	for i := range s.sources {
		if s.sources[i].Name == sourceName {
			src = &s.sources[i]
			break
		}
	}
	if src == nil {
		return nil, fmt.Errorf("知识源不存在: %s", sourceName)
	}
	if src.Transport != "http" {
		return nil, errors.New("仅 http 类型知识源支持后端代理查询")
	}

	arguments := map[string]any{"query": query}
	if limit > 0 {
		arguments["limit"] = limit
	}
	mcpRequest := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "query",
			"arguments": arguments,
		},
	}

	body, _ := json.Marshal(mcpRequest)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, src.URL, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if src.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+src.Token)
	}

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("请求知识源失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("知识源返回错误状态: %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	var mcpResponse struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &mcpResponse); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	if mcpResponse.Error != nil {
		return nil, fmt.Errorf("MCP 错误: %s", mcpResponse.Error.Message)
	}

	var texts []string
	for _, c := range mcpResponse.Result.Content {
		if c.Type == "text" && c.Text != "" {
			texts = append(texts, c.Text)
		}
	}
	return &QueryResult{Source: sourceName, Query: query, Content: strings.Join(texts, "\n")}, nil
}

// CheckHealth 健康检查：对 http 源发起一次轻量查询
func (s *Service) CheckHealth(ctx context.Context) []SourceInfo {
	infos := s.ListSources()
	for i := range infos {
		healthy := false
		if infos[i].Transport == "http" {
			ctxTimeout, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, err := s.Query(ctxTimeout, infos[i].Name, "ping", 1)
			cancel()
			healthy = err == nil
		}
		infos[i].Healthy = &healthy
	}
	return infos
}
