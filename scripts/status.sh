#!/usr/bin/env bash
# Callme 运行状态
set -euo pipefail
cd "$(dirname "$0")"
[ -f ../go.mod ] && cd ..

PID_FILE="callme.pid"
if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
  PID="$(cat "$PID_FILE")"
  echo "● Callme 运行中 (PID $PID)"
  ps -o pid,etime,rss,command -p "$PID" 2>/dev/null | tail -n +1
else
  echo "○ Callme 未运行"
  exit 1
fi
