// mock-kb：本地 mock 知识库，以 MCP（Streamable HTTP, JSON-RPC）形式开放
//
// 提供两个端点模拟两类知识源：
//   - /code/mcp  代码知识图谱（模块/接口/实现位置）
//   - /wiki/mcp  Wiki 知识图谱（产品使用/运维问答）
//
// 实现最小 MCP 服务面：initialize / notifications/initialized / tools/list / tools/call(query)。
// 兼容 Hermes 的 http MCP 客户端。
//
// 用法: go run ./cmd/mock-kb [-port 9100]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
)

// kbEntry 知识条目
type kbEntry struct {
	Title    string   `json:"title"`
	Content  string   `json:"content"`
	Keywords []string // 命中关键词（标题/正文之外的补充）
}

var codeKB = []kbEntry{
	{
		Title:    "会话池与接入状态 internal/service/session/manager.go",
		Content:  "坐席制并发控制由 Manager 统一处理。CreateSession/ContinueSession 先判断 MaxActive 与 MaxQueue；可立即占座时会放入 active 并启动 Agent，不可占座时才进入 queue。前端用 position>0 判断真正排队；status=queued 且 position=0 表示正在接入/启动 Agent，不应展示排队页面。",
		Keywords: []string{"会话", "排队", "接入", "坐席", "position", "queued", "active", "session"},
	},
	{
		Title:    "运行时设置 internal/service/settings/service.go",
		Content:  "设置页保存的 Agent 类型、CLI 路径、模型、API Base URL、Token、系统提示词和坐席容量会写入 SQLite settings 表，key 为 agent_settings 和 pool_settings。configs/config.yaml 只保留服务启动基础配置；新部署需要先在设置页配置模型，否则创建会话会提示请先在设置中配置 Agent 模型。",
		Keywords: []string{"设置", "SQLite", "settings", "模型", "坐席", "agent_settings", "pool_settings"},
	},
	{
		Title:    "Hermes 本地配置 internal/service/agent/plugins/hermes/adapter.go",
		Content:  "Hermes 启动前会读取并更新 HERMES_HOME/config.yaml，只覆盖 model 配置，并在存在知识源声明时同步 mcp_servers。当前 HERMES_HOME 来自 agent.hermes_home，默认是项目内 data/hermes-home，不是用户机器的 ~/.hermes。Hermes 通过本地 mcp_servers 连接知识库 MCP。",
		Keywords: []string{"Hermes", "HERMES_HOME", "config.yaml", "mcp_servers", "模型", "本地配置"},
	},
	{
		Title:    "ACP 会话协议 internal/service/agent/plugins/acp/adapter.go",
		Content:  "Callme 仍使用 ACP 与 Hermes/OpenCode 建立常驻 Agent 会话，但已去除通过 ACP session/new 或 session/resume 传递 mcpServers 的方式。ACP 请求现在只传 cwd/sessionId 等会话参数；知识库 MCP 由具体 Agent 的本地配置机制负责。",
		Keywords: []string{"ACP", "mcpServers", "session/new", "session/resume", "Hermes", "OpenCode"},
	},
	{
		Title:    "知识库 MCP 配置",
		Content:  "Callme 不再提供独立的知识检索页和后端代理查询服务。知识库 MCP 由具体 Agent 的本地配置机制负责；Hermes 使用 HERMES_HOME/config.yaml 顶层 mcp_servers 连接 code-graph、wiki-graph 等 MCP 服务。",
		Keywords: []string{"知识", "检索", "MCP", "Hermes", "mcp_servers", "knowledge"},
	},
	{
		Title:    "历史会话继续 internal/service/session/manager.go",
		Content:  "继续历史会话会复用原 session 记录，不再复制出新会话；如果底层 Agent 支持原生 resume，则使用保存的 AgentSessionID 恢复。为了安全，Callme 已放弃把历史消息拼接注入下一轮 Prompt，避免上下文泄漏和过大上下文。",
		Keywords: []string{"历史", "继续", "resume", "AgentSessionID", "上下文", "注入"},
	},
	{
		Title:    "反馈自学习 internal/service/feedback/service.go",
		Content:  "用户点踩+纠错入库后，distillLoop 定时任务（单写者）将其蒸馏为 HERMES_HOME/learning_notes.md 学习笔记，系统提示词引导 Agent 回答时参考，避免重复历史错误。",
		Keywords: []string{"反馈", "自学习", "蒸馏", "点踩", "纠错", "feedback"},
	},
	{
		Title:    "WebSocket 推送 internal/ws/handler.go",
		Content:  "每个 WS 连接绑定一个会话：上行 user_message/ping/close，下行 chunk、queue、state、message、closed、error 等事件。连接建立后立即推送当前 SessionView；用户消息异步处理，Manager 用 busy 标记保证单会话不会并发执行两轮回答。",
		Keywords: []string{"websocket", "ws", "流式", "推送", "实时", "busy"},
	},
	{
		Title:    "用户管理 internal/api/router.go",
		Content:  "管理员用户管理接口包含列表、角色修改和删除用户。DELETE /api/v1/users/:id 仅管理员可用，删除前会校验权限，前端用户页提供删除入口。普通用户不能删除其他用户。",
		Keywords: []string{"用户", "管理员", "删除", "权限", "users"},
	},
	{
		Title:    "聊天前端 web/src/pages/Chat/index.tsx",
		Content:  "聊天页区分 active、queued 和 connecting。queued 只有 position>0 时才展示排队页面；status=queued 且 position=0 时展示正在接入智能坐席，避免出现前方 0 位的误导性排队提示。输入框只在 active 状态展示。",
		Keywords: []string{"前端", "聊天", "排队", "接入中", "position", "Chat"},
	},
}

var wikiKB = []kbEntry{
	{
		Title:    "如何部署 Callme",
		Content:  "部署流程：1. 复制 configs/config.yaml.example 为 configs/config.yaml，只保留服务启动基础配置；2. make build 构建前后端；3. make run 启动，默认 http://localhost:8090；4. 首次登录管理员账号后进入设置页配置 Agent 类型、模型、API、Token、系统提示词和坐席容量。数据库使用 SQLite，无需额外数据库服务。",
		Keywords: []string{"部署", "安装", "启动", "构建", "首次配置", "deploy"},
	},
	{
		Title:    "首次使用必须配置什么",
		Content:  "新数据库首次使用时，配置文件不再内置模型和 API Token。管理员需要在设置页至少配置 Agent 类型、模型 ID、API Base URL/Token（如使用自定义网关）和坐席容量。未配置模型时创建会话会被后端拦截并提示请先在设置中配置 Agent 模型。",
		Keywords: []string{"首次", "设置", "模型", "API", "Token", "管理员"},
	},
	{
		Title:    "如何切换模型",
		Content:  "进入「设置」页修改模型 ID、API Base URL 与 Token 后保存，配置会写入 SQLite 并立即成为运行时设置；新创建或继续接入的会话会使用新模型，已有活跃 Agent 进程不受影响。也可调用 PUT /api/v1/settings/agent。",
		Keywords: []string{"模型", "切换", "glm", "设置", "model", "SQLite"},
	},
	{
		Title:    "如何配置知识库 MCP",
		Content:  "当前建议把知识库 MCP 配到 Hermes 的 HERMES_HOME/config.yaml 顶层 mcp_servers 中，例如 code-graph.url=http://127.0.0.1:9100/code/mcp、wiki-graph.url=http://127.0.0.1:9100/wiki/mcp。Callme 不再通过 ACP mcpServers 参数给 Hermes 注入 MCP。",
		Keywords: []string{"知识库", "MCP", "Hermes", "mcp_servers", "配置"},
	},
	{
		Title:    "坐席数与排队规则",
		Content:  "设置页的坐席数表示同时接入的活跃会话上限。坐席未满时创建或继续会话会直接接入，只显示正在接入 Agent；只有 position>0 时才是真正排队并展示排队页面。坐席满后按 FIFO 入队，前方人数根据 position 计算。",
		Keywords: []string{"坐席", "排队", "并发", "限制", "队列", "接入"},
	},
	{
		Title:    "为什么接入会慢",
		Content:  "接入慢通常不是固定等待，而是 Agent 启动耗时：Hermes ACP initialize、session/new 或 session/resume、读取 HERMES_HOME/config.yaml、连接 MCP server、发现工具都可能耗时。后端 ACP 启动上限约 45 秒，前端创建/继续会话请求超时为 90 秒。",
		Keywords: []string{"慢", "超时", "接入", "启动", "initialize", "session/new", "MCP"},
	},
	{
		Title:    "如何继续历史会话",
		Content:  "历史会话点击继续后会复用原来的会话记录，不会新建一条重复历史。Callme 不再把历史消息注入 Prompt；如果 Agent 支持原生 resume，则用底层 Agent session id 恢复，否则只在同一条 Callme 会话里继续追加消息。",
		Keywords: []string{"历史", "继续", "resume", "会话", "上下文"},
	},
	{
		Title:    "如何转人工",
		Content:  "会话中点击「转人工」按钮，填写补充说明后提交，系统会生成带完整对话上下文的工单，并通过 webhook 外发到下游工单系统（在 config.yaml handoff.webhook_url 配置）。工单可在「工单」页查看。",
		Keywords: []string{"转人工", "人工", "工单", "客服", "handoff"},
	},
	{
		Title:    "自学习是如何工作的",
		Content:  "对回答点踩并填写纠错后，系统定时把纠错蒸馏为学习笔记（HERMES_HOME/learning_notes.md），与 Hermes 自身跨会话记忆共同生效，使客服越用越聪明。可在「设置 → 自学习 → 查看学习笔记」核对，运营看板的学习曲线跟踪每日满意率。",
		Keywords: []string{"自学习", "学习", "笔记", "反馈", "聪明"},
	},
	{
		Title:    "管理员如何删除用户",
		Content:  "管理员进入用户管理页可以删除普通用户或调整角色。删除操作调用 DELETE /api/v1/users/:id，需要管理员权限。建议不要删除当前正在使用系统的账号，避免会话和审计信息难以追踪。",
		Keywords: []string{"管理员", "用户", "删除", "角色", "权限"},
	},
}

// search 简易检索：按关键词在标题/正文/关键词表中打分
func search(kb []kbEntry, query string, limit int) []kbEntry {
	if limit <= 0 {
		limit = 3
	}
	q := strings.ToLower(strings.TrimSpace(query))
	type scored struct {
		e kbEntry
		s int
	}
	var hits []scored
	for _, e := range kb {
		score := 0
		title := strings.ToLower(e.Title)
		content := strings.ToLower(e.Content)
		for _, term := range strings.Fields(q) {
			if strings.Contains(title, term) {
				score += 3
			}
			if strings.Contains(content, term) {
				score += 1
			}
		}
		for _, kw := range e.Keywords {
			if strings.Contains(q, strings.ToLower(kw)) {
				score += 2
			}
		}
		if score > 0 {
			hits = append(hits, scored{e, score})
		}
	}
	// 无命中时返回全部摘要的前 limit 条，便于 Agent 了解知识面
	if len(hits) == 0 {
		for i, e := range kb {
			if i >= limit {
				break
			}
			hits = append(hits, scored{e, 0})
		}
	}
	for i := 0; i < len(hits); i++ {
		for j := i + 1; j < len(hits); j++ {
			if hits[j].s > hits[i].s {
				hits[i], hits[j] = hits[j], hits[i]
			}
		}
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]kbEntry, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.e)
	}
	return out
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func rpcResult(id json.RawMessage, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

// mcpHandler 处理单个知识源的 MCP JSON-RPC 请求
func mcpHandler(name, description string, kb []kbEntry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			log.Printf("[%s] %s %s accept=%q (non-POST)", name, r.Method, r.URL.Path, r.Header.Get("Accept"))
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		log.Printf("[%s] POST method=%s accept=%q sessionHdr=%q", name, req.Method, r.Header.Get("Accept"), r.Header.Get("Mcp-Session-Id"))

		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			json.NewEncoder(w).Encode(rpcResult(req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": name, "version": "0.1.0"},
			}))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			json.NewEncoder(w).Encode(rpcResult(req.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "query",
					"description": description,
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"query": map[string]any{"type": "string", "description": "检索关键词"},
							"limit": map[string]any{"type": "integer", "description": "最大返回条数"},
						},
						"required": []string{"query"},
					},
				}},
			}))
		case "tools/call":
			var params struct {
				Name      string `json:"name"`
				Arguments struct {
					Query string `json:"query"`
					Limit int    `json:"limit"`
				} `json:"arguments"`
			}
			json.Unmarshal(req.Params, &params)
			results := search(kb, params.Arguments.Query, params.Arguments.Limit)
			var b strings.Builder
			for i, e := range results {
				fmt.Fprintf(&b, "### %d. %s\n%s\n\n", i+1, e.Title, e.Content)
			}
			if b.Len() == 0 {
				b.WriteString("（无匹配知识条目）")
			}
			json.NewEncoder(w).Encode(rpcResult(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": b.String()}},
			}))
		default:
			json.NewEncoder(w).Encode(rpcResult(req.ID, map[string]any{}))
		}
	}
}

func main() {
	port := flag.Int("port", 9100, "监听端口")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/code/mcp", mcpHandler("code-graph", "检索代码知识图谱：模块结构、接口定义与实现位置", codeKB))
	mux.HandleFunc("/wiki/mcp", mcpHandler("wiki-graph", "检索 Wiki 知识图谱：产品使用、部署运维与常见问题", wikiKB))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	log.Printf("mock-kb listening on http://%s  (/code/mcp, /wiki/mcp)", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
