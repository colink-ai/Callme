#!/usr/bin/env python3
"""探测真实 hermes acp 的事件流：initialize -> session/new -> session/prompt，
打印所有 session/update 类型与原始内容，用于核对解析器覆盖度。

用法: OPENAI_API_KEY=<token> python3 auto-test/probe-hermes-acp.py "你的问题"
"""
import json
import os
import subprocess
import sys
import threading

QUESTION = sys.argv[1] if len(sys.argv) > 1 else "你好，请用一句话介绍你自己"

env = dict(os.environ)
env.update({
    "HERMES_HOME": os.path.abspath("data/hermes-home"),
    "HERMES_INFERENCE_PROVIDER": "custom",
    "CUSTOM_BASE_URL": "https://coding.dashscope.aliyuncs.com/v1",
})

p = subprocess.Popen(
    ["/opt/homebrew/bin/hermes", "acp"],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
    env=env, text=True,
)

rid = 0
responses = {}
resp_events = {}
update_types = {}


def send(method, params):
    global rid
    rid += 1
    p.stdin.write(json.dumps({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}) + "\n")
    p.stdin.flush()
    ev = threading.Event()
    resp_events[rid] = ev
    return rid


def reader():
    for line in p.stdout:
        try:
            msg = json.loads(line)
        except Exception:
            continue
        if msg.get("method") == "session/update":
            upd = msg.get("params", {}).get("update", {})
            t = upd.get("sessionUpdate", "?")
            update_types[t] = update_types.get(t, 0) + 1
            if update_types[t] <= 3:
                print(f"UPDATE[{t}]:", json.dumps(upd, ensure_ascii=False)[:500], flush=True)
        elif "id" in msg and msg["id"] in resp_events:
            responses[msg["id"]] = msg
            print(f"RESP id={msg['id']}:", json.dumps(msg.get("result") or msg.get("error"), ensure_ascii=False)[:300], flush=True)
            resp_events[msg["id"]].set()
        elif msg.get("method"):
            print("NOTIFY:", msg["method"], json.dumps(msg.get("params", {}), ensure_ascii=False)[:300], flush=True)


threading.Thread(target=reader, daemon=True).start()

i = send("initialize", {"protocolVersion": 2025, "clientCapabilities": {}})
resp_events[i].wait(15)

n = send("session/new", {"cwd": os.getcwd(), "mcpServers": []})
resp_events[n].wait(30)
session_id = (responses.get(n, {}).get("result") or {}).get("sessionId", "")
print("SESSION_ID:", session_id, flush=True)

q = send("session/prompt", {"sessionId": session_id, "prompt": [{"type": "text", "text": QUESTION}]})
finished = resp_events[q].wait(120)

print("\n=== update 类型统计 ===", flush=True)
for t, c in update_types.items():
    print(f"  {t}: {c}", flush=True)
print("prompt finished:", finished, flush=True)

p.stdin.close()
p.terminate()
