#!/usr/bin/env bash
# 部署 Lab；保留旧容器及原数据挂载，新服务失败时恢复旧容器。
# 用法：./deploy.sh；默认绑定 0.0.0.0，可通过 BIND 覆盖。
# 需要 Docker 与 Python 3。脚本不清理旧容器或数据。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMAGE="strategy-lab"
CONTAINER="strategy-lab"
HOST_PORT="${PORT:-8765}"
BIND="${BIND:-0.0.0.0}"
BACKUP="${CONTAINER}-previous-$(date +%Y%m%dT%H%M%S)-$$"
FAILED="${CONTAINER}-failed-$(date +%Y%m%dT%H%M%S)-$$"
old_exists=0
old_running=false
switched=0
old_stopped=0

for dependency in docker python3; do
    if ! command -v "${dependency}" >/dev/null 2>&1; then
        echo "错误: 未找到 ${dependency}"
        exit 1
    fi
done
if docker container inspect "${CONTAINER}" >/dev/null 2>&1; then
    old_exists=1
    old_running=$(docker inspect --format '{{.State.Running}}' "${CONTAINER}")
fi

check_idle() {
    if [ "${old_running}" = true ]; then
        docker exec "${CONTAINER}" wget -qO- 'http://127.0.0.1:8765/api/status' |
            python3 -c 'import json,sys; state=json.load(sys.stdin).get("state"); sys.exit(0 if state in ("idle","done","error") else "任务正在运行或状态未知，请完成任务后重试部署")'
    fi
}

rollback() {
    result=$?
    trap - EXIT
    if [ "${result}" -ne 0 ] && [ "${old_stopped}" -eq 1 ] && [ "${switched}" -eq 0 ]; then
        docker start "${CONTAINER}" >/dev/null
    fi
    if [ "${result}" -ne 0 ] && [ "${switched}" -eq 1 ]; then
        echo "新服务未就绪，保留失败容器并恢复旧服务。"
        if docker container inspect "${CONTAINER}" >/dev/null 2>&1; then
            docker stop --time 30 "${CONTAINER}" >/dev/null || true
            docker rename "${CONTAINER}" "${FAILED}" || true
        fi
        if [ "${old_exists}" -eq 1 ]; then
            docker rename "${BACKUP}" "${CONTAINER}"
            if [ "${old_running}" = true ]; then
                docker start "${CONTAINER}" >/dev/null
            fi
        fi
    fi
    exit "${result}"
}
trap rollback EXIT

check_idle
echo "==> 构建镜像 ${IMAGE}:latest"
docker build -t "${IMAGE}:latest" "${ROOT}"
# 构建期间可能启动任务，切换前再次检查；不可读取状态时停止部署。
check_idle
if [ "${old_exists}" -eq 1 ]; then
    if [ "${old_running}" = true ]; then
        docker stop --time 30 "${CONTAINER}" >/dev/null
        old_stopped=1
    fi
    docker rename "${CONTAINER}" "${BACKUP}"
fi
switched=1

docker run -d \
    --name "${CONTAINER}" \
    --restart unless-stopped \
    -p "${BIND}:${HOST_PORT}:8765" \
    -v "${ROOT}/data:/app/data" \
    -v "${ROOT}/output:/app/output" \
    -v "${ROOT}/config:/app/config:ro" \
    -v "${ROOT}/strategies/script:/app/strategies/script" \
    "${IMAGE}:latest"

for _ in $(seq 1 30); do
    if docker exec "${CONTAINER}" wget -qO- 'http://127.0.0.1:8765/api/builder/components' >/dev/null 2>&1; then
        echo "部署完成: http://127.0.0.1:${HOST_PORT}"
        if [ "${old_exists}" -eq 1 ]; then
            echo "已保留旧容器: ${BACKUP}（停止新容器后可恢复）"
        fi
        if [ "${BIND}" != "127.0.0.1" ]; then
            echo "已绑定 ${BIND}，可通过本机 IP:${HOST_PORT} 访问（服务无鉴权）"
        fi
        exit 0
    fi
    sleep 1
done
docker logs --tail 20 "${CONTAINER}" || true
echo "错误: 30 秒内服务未就绪。"
exit 1
