#!/usr/bin/env bash
# Callme 一键升级脚本
#
# 用法：
#   ./upgrade.sh /path/to/callme-<version>-linux-<arch>.tar.gz
#   ./upgrade.sh --no-start /path/to/callme-<version>-linux-<arch>.tar.gz
#
# 设计原则：
# - 保留 configs/config.yaml 与 data/，避免覆盖本机配置、SQLite、Hermes 持久化数据。
# - 升级前完整备份关键运行时文件，backups/ 下最多保留 3 份历史。
# - 新版本配置模板写入 configs/config.yaml.example；若已有 config.yaml，则额外生成
#   configs/config.yaml.new 供人工对比新增配置项。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"
[ -f ../go.mod ] && cd ..
APP_DIR="$(pwd)"

BACKUP_KEEP=3
NO_START=0
PACKAGE=""
SERVICE_NAME="${CALLME_SYSTEMD_SERVICE:-callme}"

usage() {
  cat <<USAGE
用法:
  ./upgrade.sh [--no-start] <callme-linux-package.tar.gz>

选项:
  --no-start   升级完成后不自动启动，即使升级前服务处于运行状态
  -h, --help   显示帮助

示例:
  ./upgrade.sh /tmp/callme-0.1.1-linux-amd64.tar.gz
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --no-start)
      NO_START=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      echo "未知选项: $1" >&2
      usage
      exit 1
      ;;
    *)
      if [ -n "$PACKAGE" ]; then
        echo "只能指定一个升级包" >&2
        usage
        exit 1
      fi
      PACKAGE="$1"
      shift
      ;;
  esac
done

if [ -z "$PACKAGE" ]; then
  echo "缺少升级包路径" >&2
  usage
  exit 1
fi

if [ ! -f "$PACKAGE" ]; then
  echo "升级包不存在: $PACKAGE" >&2
  exit 1
fi

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "缺少必要命令: $1" >&2
    exit 1
  fi
}

require_cmd tar
require_cmd date
require_cmd find
require_cmd sort

TS="$(date +%Y%m%d-%H%M%S)"
TMP_DIR="${APP_DIR}/.upgrade-tmp-${TS}"
BACKUP_ROOT="${APP_DIR}/backups"
BACKUP_DIR="${BACKUP_ROOT}/callme-backup-${TS}"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

is_running() {
  [ -f callme.pid ] && kill -0 "$(cat callme.pid)" 2>/dev/null
}

systemd_running() {
  command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null
}

copy_if_exists() {
  local src="$1" dst="$2"
  if [ -e "$src" ]; then
    mkdir -p "$(dirname "$dst")"
    cp -a "$src" "$dst"
  fi
}

echo "==> Callme 升级开始"
echo "    安装目录: $APP_DIR"
echo "    升级包:   $PACKAGE"

mkdir -p "$TMP_DIR"
tar -xzf "$PACKAGE" -C "$TMP_DIR"

PKG_ROOT="$(find "$TMP_DIR" -mindepth 1 -maxdepth 1 -type d | sort | head -n 1)"
if [ -z "$PKG_ROOT" ]; then
  echo "升级包格式错误：未找到包根目录" >&2
  exit 1
fi

for required in "callme-server" "web/dist" "configs/config.yaml.example" "start.sh" "stop.sh" "status.sh"; do
  if [ ! -e "${PKG_ROOT}/${required}" ]; then
    echo "升级包缺少必要文件: $required" >&2
    exit 1
  fi
done

WAS_RUNNING=0
START_MODE="script"
if systemd_running; then
  WAS_RUNNING=1
  START_MODE="systemd"
  echo "==> 检测到 systemd 服务 ${SERVICE_NAME} 正在运行，先停止"
  systemctl stop "$SERVICE_NAME"
elif is_running; then
  WAS_RUNNING=1
  START_MODE="script"
  echo "==> 检测到服务正在运行，先停止"
  if [ -x ./stop.sh ]; then
    ./stop.sh
  elif [ -x ./scripts/stop.sh ]; then
    ./scripts/stop.sh
  else
    echo "缺少 stop.sh，无法安全停止服务" >&2
    exit 1
  fi
else
  echo "==> 服务当前未运行"
fi

echo "==> 创建备份: $BACKUP_DIR"
mkdir -p "$BACKUP_DIR"
for item in \
  "callme-server" \
  "bin/callme-server" \
  "configs/config.yaml" \
  "configs/config.yaml.example" \
  "data" \
  "web/dist" \
  "start.sh" \
  "stop.sh" \
  "status.sh" \
  "upgrade.sh" \
  "callme.service" \
  "INSTALL.md"; do
  copy_if_exists "$item" "${BACKUP_DIR}/${item}"
done

cat > "${BACKUP_DIR}/BACKUP_INFO.txt" <<INFO
created_at=${TS}
app_dir=${APP_DIR}
package=${PACKAGE}
was_running=${WAS_RUNNING}
INFO

echo "==> 安装新版本文件"
mkdir -p configs web logs

# 程序与静态资源
cp -a "${PKG_ROOT}/callme-server" ./callme-server
rm -rf web/dist
cp -a "${PKG_ROOT}/web/dist" web/dist

# 启停脚本与可选部署文件
cp -a "${PKG_ROOT}/start.sh" ./start.sh
cp -a "${PKG_ROOT}/stop.sh" ./stop.sh
cp -a "${PKG_ROOT}/status.sh" ./status.sh
if [ -f "${PKG_ROOT}/upgrade.sh" ]; then
  cp -a "${PKG_ROOT}/upgrade.sh" ./upgrade.sh
fi
copy_if_exists "${PKG_ROOT}/callme.service" "./callme.service"
copy_if_exists "${PKG_ROOT}/INSTALL.md" "./INSTALL.md"
chmod +x ./callme-server ./start.sh ./stop.sh ./status.sh
[ -f ./upgrade.sh ] && chmod +x ./upgrade.sh

echo "==> 处理配置文件"
if [ -f configs/config.yaml ]; then
  cp -a "${PKG_ROOT}/configs/config.yaml.example" configs/config.yaml.example
  cp -a "${PKG_ROOT}/configs/config.yaml.example" configs/config.yaml.new
  echo "    已保留现有 configs/config.yaml"
  echo "    新版本模板已写入 configs/config.yaml.example"
  echo "    请对比 configs/config.yaml.new，必要时把新增配置项合并到 configs/config.yaml"
else
  cp -a "${PKG_ROOT}/configs/config.yaml.example" configs/config.yaml.example
  cp -a configs/config.yaml.example configs/config.yaml
  echo "    未发现 configs/config.yaml，已从新模板生成"
fi

mkdir -p data logs

echo "==> 清理旧备份，仅保留最近 ${BACKUP_KEEP} 份"
mkdir -p "$BACKUP_ROOT"
backup_count="$(find "$BACKUP_ROOT" -mindepth 1 -maxdepth 1 -type d -name 'callme-backup-*' | wc -l | tr -d ' ')"
if [ "$backup_count" -gt "$BACKUP_KEEP" ]; then
  remove_count=$((backup_count - BACKUP_KEEP))
  find "$BACKUP_ROOT" -mindepth 1 -maxdepth 1 -type d -name 'callme-backup-*' | sort | head -n "$remove_count" | while IFS= read -r old; do
    echo "    删除旧备份: $old"
    rm -rf "$old"
  done
fi

echo "==> 升级文件安装完成"
echo "    备份目录: $BACKUP_DIR"

if [ "$WAS_RUNNING" -eq 1 ] && [ "$NO_START" -eq 0 ]; then
  echo "==> 服务升级前处于运行状态，自动启动新版本"
  if [ "$START_MODE" = "systemd" ]; then
    systemctl start "$SERVICE_NAME"
    systemctl --no-pager --full status "$SERVICE_NAME" || true
  else
    ./start.sh
  fi
else
  echo "==> 未自动启动。需要启动时执行: ./start.sh"
fi

echo "✅ 升级完成"
