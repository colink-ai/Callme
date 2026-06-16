#!/usr/bin/env bash
# Callme 一键启动（后台运行）
# 适用于发布包（二进制在 ./callme-server）与源码目录（二进制在 ./bin/callme-server）。
set -euo pipefail
cd "$(dirname "$0")"
# 若脚本位于 scripts/ 子目录（源码场景），回到项目根
[ -f ../go.mod ] && cd ..

# 定位二进制
if [ -x ./callme-server ]; then
  BIN=./callme-server
elif [ -x ./bin/callme-server ]; then
  BIN=./bin/callme-server
else
  echo "未找到 callme-server 可执行文件（请先构建或解压发布包）" >&2
  exit 1
fi

CONFIG="configs/config.yaml"
WEB="web/dist"
PID_FILE="callme.pid"
BOOT_LOG="logs/bootstrap.log"   # 仅捕获启动早期/崩溃输出；正常日志由程序写入 logs/callme.log

mkdir -p logs

# 首次启动：从模板生成配置
if [ ! -f "$CONFIG" ]; then
  if [ -f configs/config.yaml.example ]; then
    echo "首次启动：从模板生成 $CONFIG，请按需修改后重新执行 start.sh"
    cp configs/config.yaml.example "$CONFIG"
  else
    echo "缺少 $CONFIG 且无模板 configs/config.yaml.example" >&2
    exit 1
  fi
fi

# 已在运行则退出
if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
  echo "Callme 已在运行 (PID $(cat "$PID_FILE"))，如需重启请先 ./scripts/stop.sh"
  exit 0
fi

# 后台启动（nohup + setsid 脱离终端；输出重定向到 bootstrap 日志）
echo "启动 Callme…"
nohup "$BIN" -config "$CONFIG" -web "$WEB" >> "$BOOT_LOG" 2>&1 &
echo $! > "$PID_FILE"

# 等待并确认存活
sleep 2
PID="$(cat "$PID_FILE")"
if kill -0 "$PID" 2>/dev/null; then
  echo "✅ Callme 已后台启动 (PID $PID)"
  echo "   运行日志: logs/callme.log    启动日志: $BOOT_LOG"
else
  echo "❌ 启动失败，请查看 $BOOT_LOG" >&2
  rm -f "$PID_FILE"
  exit 1
fi
