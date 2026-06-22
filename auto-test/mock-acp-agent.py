#!/usr/bin/env python3
"""Mock ACP agent：模拟 hermes acp 的 JSON-RPC stdio 协议，用于无 hermes CLI 环境的端到端冒烟测试。

用法：在 config.yaml 中把 agent.cli_path 指向本脚本（它会忽略 "acp" 参数）。
"""
import json
import sys


def send(obj):
    sys.stdout.write(json.dumps(obj, ensure_ascii=False) + "\n")
    sys.stdout.flush()


def notify(session_id, update):
    send({
        "jsonrpc": "2.0",
        "method": "session/update",
        "params": {"sessionId": session_id, "update": update},
    })


def main():
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            msg = json.loads(line)
        except json.JSONDecodeError:
            continue
        method = msg.get("method")
        mid = msg.get("id")

        if method == "initialize":
            send({"jsonrpc": "2.0", "id": mid,
                  "result": {"protocolVersion": 2025, "agentCapabilities": {}}})
        elif method == "session/new":
            send({"jsonrpc": "2.0", "id": mid, "result": {"sessionId": "mock-acp-session"}})
        elif method == "session/prompt":
            params = msg.get("params", {})
            sid = params.get("sessionId", "")
            text = "".join(b.get("text", "") for b in params.get("prompt", []))
            # 模拟知识检索工具调用（验证引用展示链路）
            notify(sid, {"sessionUpdate": "tool_call", "toolCallId": "tc-1",
                         "status": "in_progress", "title": "mcp_code_graph_query",
                         "rawInput": {"query": text[:50]}})
            notify(sid, {"sessionUpdate": "tool_call_update", "toolCallId": "tc-1",
                         "status": "completed",
                         "content": [{"type": "text", "text": "mock knowledge snippet"}]})
            # 模拟流式文本输出
            for piece in ["您好，", "这是 mock ACP agent 的回答：", text[:80]]:
                notify(sid, {"sessionUpdate": "agent_message_chunk",
                             "content": {"type": "text", "text": piece}})
            send({"jsonrpc": "2.0", "id": mid, "result": {"stopReason": "end_turn"}})
        elif mid is not None:
            # session/set_model 等其余请求一律成功
            send({"jsonrpc": "2.0", "id": mid, "result": {}})


if __name__ == "__main__":
    main()
