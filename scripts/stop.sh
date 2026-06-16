#!/usr/bin/env bash
# Callme 一键停止（优雅退出，超时则强杀）
set -euo pipefail
cd "$(dirname "$0")"
[ -f ../go.mod ] && cd ..

PID_FILE="callme.pid"

stop_pid() {
  local pid="$1"
  kill "$pid" 2>/dev/null || return 1   # SIGTERM，触发优雅退出（结束会话、回收 Hermes 进程）
  for _ in $(seq 1 15); do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 1
  done
  echo "进程未在 15s 内退出，强制结束 (SIGKILL)"
  kill -9 "$pid" 2>/dev/null || true
}

if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
  PID="$(cat "$PID_FILE")"
  echo "停止 Callme (PID $PID)…"
  stop_pid "$PID"
  rm -f "$PID_FILE"
  echo "✅ 已停止"
else
  # 回退：按进程名兜底（PID 文件丢失时）
  if pgrep -f "callme-server -config" >/dev/null 2>&1; then
    echo "未找到有效 PID 文件，按进程名停止…"
    pkill -f "callme-server -config" && echo "✅ 已停止" || echo "停止失败"
  else
    echo "Callme 未在运行"
  fi
  rm -f "$PID_FILE"
fi
