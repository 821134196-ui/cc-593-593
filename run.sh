#!/usr/bin/env bash
# 酒店遗留物保管与认领系统 —— 一条命令启动
# 用法: ./run.sh          （首次自动安装依赖并构建前端）
#       REBUILD=1 ./run.sh （强制重新构建前端）
set -euo pipefail
cd "$(dirname "$0")"

# 定位 Go（未在 PATH 时尝试常见安装位置）
if ! command -v go >/dev/null 2>&1; then
  for d in "$HOME/sdk/go/bin" /usr/local/go/bin /usr/lib/go/bin; do
    if [ -x "$d/go" ]; then export PATH="$d:$PATH"; break; fi
  done
fi
command -v go >/dev/null 2>&1 || { echo "错误: 未找到 Go，请安装 Go 1.23+"; exit 1; }
command -v npm >/dev/null 2>&1 || { echo "错误: 未找到 npm，请安装 Node.js"; exit 1; }

export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

# 构建前端
if [ ! -f frontend/dist/index.html ] || [ "${REBUILD:-0}" = "1" ]; then
  echo "==> 构建前端..."
  (cd frontend && npm install --no-fund --no-audit && npm run build)
fi

PORT="${PORT:-8080}"
echo "==> 启动服务: http://localhost:${PORT}"
exec go run .
