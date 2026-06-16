#!/usr/bin/env bash
# Callme Linux 发布包打包脚本
#
# 产物：dist/callme-<version>-linux-<arch>.tar.gz
# 包含：callme-server (linux 二进制) + web 前端 + 配置模板 + 启停/升级脚本 + systemd 模板
# 不含：mock-kb（cmd/mock-kb 仅本地联调用）、真实 config.yaml、data/（数据库/密钥）、日志
#
# 用法:
#   scripts/package.sh                 # 默认 amd64
#   scripts/package.sh arm64           # 指定架构
#   VERSION=1.2.0 scripts/package.sh amd64
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"

ARCH="${1:-amd64}"
VERSION="${VERSION:-$( [ -f VERSION ] && cat VERSION || echo "0.1.0" )}"
PKG="callme-${VERSION}-linux-${ARCH}"
OUT="${ROOT}/dist"
STAGE="${OUT}/${PKG}"

# 生成启动脚本、systemd 模板与安装说明
install_runtime_files() {
  local stage="$1" version="$2"

  # 一键启动/停止/状态脚本（后台运行，PID 文件管理）
  cp "${ROOT}/scripts/start.sh"   "${stage}/start.sh"
  cp "${ROOT}/scripts/stop.sh"    "${stage}/stop.sh"
  cp "${ROOT}/scripts/status.sh"  "${stage}/status.sh"
  cp "${ROOT}/scripts/upgrade.sh" "${stage}/upgrade.sh"
  chmod +x "${stage}/start.sh" "${stage}/stop.sh" "${stage}/status.sh" "${stage}/upgrade.sh"

  cat > "${stage}/callme.service" <<UNIT
[Unit]
Description=Callme 智能问题解决 Agent
After=network.target

[Service]
Type=simple
# 按实际部署路径与运行账号修改
WorkingDirectory=/opt/callme
ExecStart=/opt/callme/callme-server -config /opt/callme/configs/config.yaml -web /opt/callme/web/dist
Restart=on-failure
RestartSec=3
# 需要 hermes CLI 在 PATH 中；如装在非标准路径，在此追加
# Environment=PATH=/usr/local/bin:/usr/bin:/bin

[Install]
WantedBy=multi-user.target
UNIT

  cat > "${stage}/INSTALL.md" <<INSTALL
# Callme ${version} 部署说明（Linux）

## 前置依赖
- \`hermes\` CLI 已安装并在 PATH 中
- Hermes 运行环境安装 \`mcp\` Python SDK（否则无法连接知识库 MCP）：
  \`\`\`bash
  "\$(head -1 \$(which hermes) | sed 's/^#!//')" -m pip install mcp
  \`\`\`

## 快速启动（后台运行）
\`\`\`bash
tar -xzf ${PKG}.tar.gz && cd ${PKG}
cp configs/config.yaml.example configs/config.yaml   # 按需修改
./start.sh        # 后台启动，默认 http://0.0.0.0:8090
./status.sh       # 查看运行状态
./stop.sh         # 停止
\`\`\`
- 首启会自动从模板生成 config.yaml；进程后台运行，PID 记录在 callme.pid。
- 运行日志写入 logs/callme.log（不输出到控制台）；启动早期/崩溃信息在 logs/bootstrap.log。
- 首次注册的账号自动成为管理员；模型 / API 地址 / Token / 坐席容量在「设置」页配置。

## 知识库（MCP）
本包不含 mock 知识库。生产环境请把真实图谱的 MCP 端点配置到 Hermes：
\`\`\`bash
hermes mcp add code-graph --url https://<your-code-graph>/mcp
hermes mcp add wiki-graph --url https://<your-wiki-graph>/mcp
\`\`\`

## systemd（可选）
\`\`\`bash
sudo mkdir -p /opt/callme && sudo cp -r ./* /opt/callme/
sudo cp /opt/callme/callme.service /etc/systemd/system/callme.service
sudo systemctl daemon-reload && sudo systemctl enable --now callme
\`\`\`

## 一键升级
在当前安装目录执行：
\`\`\`bash
./upgrade.sh /tmp/callme-新版本-linux-amd64.tar.gz
\`\`\`
升级脚本会：
- 停止正在运行的 Callme；
- 备份当前 \`callme-server\`、\`web/dist\`、\`configs/config.yaml\`、\`data/\` 等关键内容到 \`backups/\`；
- 覆盖程序文件与前端静态资源；
- 保留现有 \`configs/config.yaml\`，并把新模板写入 \`configs/config.yaml.example\` 与 \`configs/config.yaml.new\`；
- 最多保留最近 3 份 \`backups/callme-backup-*\`；
- 若升级前服务在运行，则升级后自动启动。

如不希望自动启动：
\`\`\`bash
./upgrade.sh --no-start /tmp/callme-新版本-linux-amd64.tar.gz
\`\`\`

如果使用 systemd 运行，脚本会自动检测并重启 \`callme.service\`。若服务名不同：
\`\`\`bash
CALLME_SYSTEMD_SERVICE=your-service ./upgrade.sh /tmp/callme-新版本-linux-amd64.tar.gz
\`\`\`

数据库迁移由服务启动时自动执行；升级前已经备份 \`data/\`，出现异常时可用备份目录回滚。

## 数据与密钥
- \`data/\` 运行时生成：SQLite 库（用户/会话）、Hermes 持久化（含 API Token）。务必做好权限与备份，切勿外泄。
- 配置中的 Token 等敏感项通过「设置」页写入数据库，不落在 config.yaml。
INSTALL
}

# 敏感内容自检：命中疑似真实密钥即终止打包
security_scan() {
  local stage="$1" hit=0

  # 不应出现真实 config.yaml / data 目录 / 数据库 / 日志
  for bad in "configs/config.yaml" "data" "logs" "callme.db"; do
    if [ -e "${stage}/${bad}" ]; then
      echo "❌ 安全检查：发布包不应包含 ${bad}"; hit=1
    fi
  done

  # 扫描文本文件里疑似密钥赋值（token/secret/key/password = 非空非占位）
  if grep -rInE '(api_token|token|secret|password|api_key)[":=[:space:]]+[^[:space:]"#]{12,}' \
       --include='*.yaml' --include='*.yml' --include='*.json' --include='*.env' \
       "${stage}" 2>/dev/null \
       | grep -vE 'token_ttl|""|<|占位|example|your-' ; then
    echo "❌ 安全检查：发布包中发现疑似真实密钥（见上）"; hit=1
  fi

  # mock-kb 不应进包
  if [ -e "${stage}/mock-kb" ] || grep -rqI "mock-kb" "${stage}/configs" 2>/dev/null; then
    echo "❌ 安全检查：发布包不应包含 mock-kb 相关内容"; hit=1
  fi

  if [ "${hit}" -ne 0 ]; then
    echo "打包终止。"; exit 1
  fi
  echo "==> 安全自检通过（无真实密钥 / 无 data / 无 mock-kb）"
}

echo "==> 打包 Callme ${VERSION} (linux/${ARCH})"
rm -rf "${STAGE}"
mkdir -p "${STAGE}/configs" "${STAGE}/web"

# 1) 交叉编译后端（modernc sqlite 为纯 Go，CGO 关闭即可跨平台）
echo "==> 编译后端 callme-server (linux/${ARCH})"
CGO_ENABLED=0 GOOS=linux GOARCH="${ARCH}" \
  go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
  -o "${STAGE}/callme-server" ./cmd/server
# 显式不编译 cmd/mock-kb —— mock 知识库不进发布包

# 2) 构建前端
echo "==> 构建前端 web/dist"
( cd web && npm ci --no-audit --no-fund && npm run build )
cp -R "${ROOT}/web/dist" "${STAGE}/web/dist"

# 3) 仅打入配置模板（绝不打入真实 config.yaml）
echo "==> 拷贝配置模板"
cp "${ROOT}/configs/config.yaml.example" "${STAGE}/configs/config.yaml.example"

# 4) 启动脚本 + systemd 模板 + 安装说明
install_runtime_files "${STAGE}" "${VERSION}"

# 5) 敏感内容自检（任何一项命中即终止，避免泄密）
security_scan "${STAGE}"

# 6) 打包
echo "==> 生成 tar.gz"
mkdir -p "${OUT}"
tar -C "${OUT}" -czf "${OUT}/${PKG}.tar.gz" "${PKG}"
rm -rf "${STAGE}"

echo ""
echo "✅ 完成: dist/${PKG}.tar.gz"
echo "   解压后: cp configs/config.yaml.example configs/config.yaml 并按需修改，再 ./start.sh（后台运行）"
