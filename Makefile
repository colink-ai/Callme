.PHONY: build build-server build-web test run run-all dev-web mock-kb clean start stop status package package-arm64

VERSION := $(shell cat VERSION 2>/dev/null || echo dev)

# 完整构建：前端 + 后端
build: build-web build-server

build-server:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/callme-server ./cmd/server
	go build -o bin/mock-kb ./cmd/mock-kb

build-web:
	cd web && npm install && npm run build

test:
	go test ./...

# 本地 mock 知识库（MCP over HTTP），监听 :9100（仅本地测试用）
mock-kb:
	go run ./cmd/mock-kb -port 9100

# 运行服务（需先准备 configs/config.yaml，可从 config.yaml.example 复制）
run: build-server
	./bin/callme-server -config configs/config.yaml -web web/dist

# 本地开发：同时启动 mock 知识库 + 后端（推荐：知识源不在线会让每次会话启动多等 ~14s 重试）
run-all: build-server
	@echo "启动 mock-kb (:9100) + callme-server (:8090)…"
	@./bin/mock-kb -port 9100 & echo $$! > /tmp/callme-mockkb.pid
	@trap 'kill `cat /tmp/callme-mockkb.pid` 2>/dev/null' EXIT; \
	  ./bin/callme-server -config configs/config.yaml -web web/dist

# 前端开发模式（代理到 :8090 后端）
dev-web:
	cd web && npm run dev

# 后台启动服务（适合生产部署，仅启动 callme-server）
start: build-server
	@mkdir -p logs
	@if lsof -i :8090 2>/dev/null | grep -q LISTEN; then \
		echo "端口 8090 已被占用，请先执行 make stop"; \
		exit 1; \
	fi
	@nohup ./bin/callme-server -config configs/config.yaml -web web/dist >> logs/server.log 2>&1 &
	@sleep 2
	@if lsof -i :8090 2>/dev/null | grep -q LISTEN; then \
		echo "服务已启动"; \
	else \
		echo "启动失败，请查看 logs/server.log"; \
	fi

# 停止后台服务
stop:
	@pkill -f callme-server && echo "callme-server 已停止" || echo "服务未运行"

# 查看服务状态
status:
	@ps aux | grep callme-server | grep -v grep || echo "服务未运行"

# 打包 Linux 发布包（不含 mock-kb / 真实配置 / data）→ dist/callme-*-linux-amd64.tar.gz
package:
	bash scripts/package.sh amd64

package-arm64:
	bash scripts/package.sh arm64

clean:
	rm -rf bin web/dist logs dist
