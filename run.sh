#!/usr/bin/env bash
# 一条本地命令启动：构建前端 → 构建后端 → 启动服务（同时提供页面与 API）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

# 兼容本机没有全局 go、但装在 ~/sdk/go 的情况
if ! command -v go >/dev/null 2>&1; then
  if [ -x "$HOME/sdk/go/bin/go" ]; then
    export PATH="$HOME/sdk/go/bin:$PATH"
  else
    echo "未找到 Go，请先安装 Go 1.23+：https://golang.google.cn/dl/" >&2
    exit 1
  fi
fi
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

# 1) 前端：仅在 dist 缺失时构建（首次运行需要 Node.js）
if [ ! -f backend/web/dist/index.html ]; then
  echo "==> 首次运行，构建 Vue 前端…"
  (cd frontend && npm install --no-audit --no-fund && npm run build)
fi

# 2) 后端
echo "==> 编译后端…"
(cd backend && go build -o ../lostfound-server ./cmd/server)

# 3) 启动（SQLite 台账与照片都在 backend/data 下）
mkdir -p backend/data/uploads
cd backend
echo "==> 启动服务：http://localhost:8080"
exec ../lostfound-server
