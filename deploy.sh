#!/usr/bin/env bash
# 部署策略实验室（cmd/lab）到 Docker。
# K线数据库、回测报告、Lab 脚本、候选因子库均通过卷挂载回项目目录，
# 容器重建后数据不丢，宿主机 ./data ./output 与容器内实时一致。
#
# 用法：./deploy.sh
# 需要：bash、curl；docker 可用（Git Bash 直接用，WSL 未开集成时自动回退 docker.exe）。
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
ROOT_MOUNT="$ROOT"
DOCKER=docker

# WSL 未开启 Docker Desktop 集成时回退到 Windows 侧 docker.exe
if ! docker version >/dev/null 2>&1 && command -v docker.exe >/dev/null 2>&1; then
  DOCKER=docker.exe
fi
# 挂载路径统一转 C:/ 风格：
# - Git Bash: 避免 MSYS 把 "-v /c/...:/app/..." 错误转换
# - WSL + docker.exe: docker.exe 只认 Windows 路径
if command -v cygpath >/dev/null 2>&1; then
  ROOT_MOUNT="$(cygpath -m "$ROOT")"
elif [ "$DOCKER" = docker.exe ] && command -v wslpath >/dev/null 2>&1; then
  ROOT_MOUNT="$(wslpath -m "$ROOT")"
fi

IMAGE=strategy-lab
CONTAINER=strategy-lab
HOST_PORT=8765
# 默认仅绑回环地址（服务无鉴权，与本地直跑的安全边界一致）；
# 需要局域网访问时改为 BIND=0.0.0.0 ./deploy.sh
BIND=127.0.0.1

cd "$ROOT"

echo "==> [1/4] 构建镜像 ${IMAGE}:latest"
"$DOCKER" build -t "${IMAGE}:latest" .

echo "==> [2/4] 移除旧容器（如存在）"
"$DOCKER" rm -f "$CONTAINER" 2>/dev/null || true

echo "==> [3/4] 启动容器（数据/输出/脚本/配置挂载自项目目录）"
"$DOCKER" run -d \
  --name "$CONTAINER" \
  --restart unless-stopped \
  -p "${BIND}:${HOST_PORT}:8765" \
  -v "${ROOT_MOUNT}/data:/app/data" \
  -v "${ROOT_MOUNT}/output:/app/output" \
  -v "${ROOT_MOUNT}/config:/app/config:ro" \
  -v "${ROOT_MOUNT}/strategies/script:/app/strategies/script" \
  "${IMAGE}:latest"

echo "==> [4/4] 等待服务就绪（容器内自检）"
ready=0
for _ in $(seq 1 30); do
  # alpine 自带 busybox wget，从容器内探测，避免宿主机/WSL 网络差异
  if "$DOCKER" exec "$CONTAINER" wget -qO- "http://127.0.0.1:8765/api/factors" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done

echo
"$DOCKER" logs --tail 20 "$CONTAINER" || true
echo
if [ "$ready" = 1 ]; then
  echo "部署完成: http://localhost:${HOST_PORT}"
else
  echo "警告: 30 秒内服务未就绪，请查看完整日志: docker logs $CONTAINER"
  exit 1
fi
