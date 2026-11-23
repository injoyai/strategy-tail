#!/usr/bin/env bash
# 部署 Lab；保留旧容器及原数据挂载，新服务失败时恢复旧容器。
# 用法：./deploy.sh [--verify]；默认绑定 0.0.0.0:80，可通过 BIND、PORT 覆盖。
# --verify 在切换前运行 Go、前端和部署回归。脚本保留旧容器及数据。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ "${1:-}" = --verify ] && [ "$#" -eq 1 ]; then
    (cd "${ROOT}" && go test ./... && go vet ./... && node --test internal/lab/web/*_test.mjs && python3 tests/deploy_test.py)
elif [ "$#" -ne 0 ]; then
    echo "用法: ./deploy.sh [--verify]" >&2
    exit 2
fi
IMAGE="strategy-lab"
CONTAINER="strategy-lab"
HOST_PORT="${PORT:-80}"
BIND="${BIND:-0.0.0.0}"
BACKUP="${CONTAINER}-previous-$(date +%Y%m%dT%H%M%S)-$$"
FAILED="${CONTAINER}-failed-$(date +%Y%m%dT%H%M%S)-$$"
old_exists=0
old_running=false
switched=0
old_stopped=0

for dependency in docker python3 git; do
    if ! command -v "${dependency}" >/dev/null 2>&1; then
        echo "错误: 未找到 ${dependency}"
        exit 1
    fi
done

# 相邻 SDK 必须是 tdx.rev 记录的兼容提交。只核对和提示，不切换或清理 SDK 工作区。
SDK_DIR="${ROOT}/../tdx"
if [ ! -f "${ROOT}/tdx.rev" ]; then
    echo "错误: 缺少 SDK 版本记录 ${ROOT}/tdx.rev" >&2
    exit 1
fi
SDK_REV=$(tr -d '[:space:]' < "${ROOT}/tdx.rev")
if [ -z "${SDK_REV}" ]; then
    echo "错误: SDK 版本记录 ${ROOT}/tdx.rev 为空" >&2
    exit 1
fi
if [ ! -d "${SDK_DIR}/.git" ]; then
    echo "错误: 未找到相邻 SDK 目录 ${SDK_DIR}（期望提交 ${SDK_REV}）" >&2
    exit 1
fi
SDK_ACTUAL=$(git -C "${SDK_DIR}" rev-parse HEAD)
if [ "${SDK_ACTUAL}" != "${SDK_REV}" ]; then
    echo "错误: 相邻 SDK 提交不匹配" >&2
    echo "  期望: ${SDK_REV}（tdx.rev）" >&2
    echo "  实际: ${SDK_ACTUAL}（${SDK_DIR}）" >&2
    exit 1
fi
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
    if [ -n "${image_receipt:-}" ]; then rm -f "${image_receipt}"; fi
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
repository_revision() {
    local revision changes
    revision=$(git -C "$1" rev-parse HEAD)
    changes=$(git -C "$1" status --porcelain --untracked-files=normal)
    if [ -n "${changes}" ]; then revision="${revision}+dirty"; fi
    echo "${revision}"
}
APP_REVISION=$(repository_revision "${ROOT}")
TDX_REVISION=$(repository_revision "${SDK_DIR}")
image_tag="${IMAGE}:${APP_REVISION:0:12}-${TDX_REVISION:0:12}-$(date -u +%Y%m%dT%H%M%SZ)-$$"
image_receipt=$(mktemp)
echo "==> 构建镜像 ${image_tag}（Lab ${APP_REVISION}，TDX ${TDX_REVISION}）"
docker build --build-context "tdx=${SDK_DIR}" --iidfile "${image_receipt}" \
    --build-arg "APP_REVISION=${APP_REVISION}" --build-arg "TDX_REVISION=${TDX_REVISION}" -t "${image_tag}" "${ROOT}"
image_id=$(cat "${image_receipt}")
if [[ ! "${image_id}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    echo "错误: 未得到有效的镜像 ID" >&2
    exit 1
fi
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
    --env pull.mysql \
    -p "${BIND}:${HOST_PORT}:8765" \
    -v "${ROOT}/data:/app/data" \
    -v "${ROOT}/output:/app/output" \
    -v "${ROOT}/config:/app/config:ro" \
    "${image_id}"

for _ in $(seq 1 30); do
    if docker exec "${CONTAINER}" wget -qO- 'http://127.0.0.1:8765/api/builder/components' >/dev/null 2>&1 &&
       docker exec "${CONTAINER}" wget -qO- 'http://127.0.0.1:8765/api/market-data/status' |
           python3 -c 'import json,sys; status=json.load(sys.stdin); sys.exit(0 if status.get("storage", "").startswith("mysql://") and not status.get("readError") else 1)' >/dev/null 2>&1; then
        echo "部署完成: http://127.0.0.1:${HOST_PORT}"
        echo "运行镜像: ${image_tag}（${image_id}）"
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
