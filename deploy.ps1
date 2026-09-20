# 部署策略实验室（cmd/lab）到 Docker。
# K线数据库、回测报告、Lab 脚本、候选因子库均通过卷挂载回项目目录，
# 容器重建后数据不丢，宿主机 ./data ./output 与容器内实时一致。
#
# 用法：.\deploy.ps1
# 局域网访问：.\deploy.ps1 -Bind 0.0.0.0（或先 $env:BIND = '0.0.0.0'）
# 需要：PowerShell 5.1+；Docker Desktop 可用（docker 在 PATH 中）。
# 注意：本文件必须保存为 UTF-8 with BOM，否则 Windows PowerShell 5.1 会按 GBK 解析中文。
param(
    [string]$Bind = $(if ($env:BIND) { $env:BIND } else { '0.0.0.0' })
)

# 显式 Continue：规避 PS5.1 中 EAP=Stop 与原生命令 stderr 重定向（2>&1）冲突的坑；
# 原生命令成败一律用 $LASTEXITCODE 判断。
$ErrorActionPreference = 'Continue'

$Root = $PSScriptRoot
# 挂载路径统一转 C:/ 风格（Windows 版 Docker 卷挂载最稳，等价原脚本的 cygpath -m）
$RootMount = $Root -replace '\\', '/'

$Image = 'strategy-lab'
$Container = 'strategy-lab'
$HostPort = 8765
# 默认仅绑回环地址（服务无鉴权，与本地直跑的安全边界一致）

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Write-Host "错误: 未找到 docker，请确认 Docker Desktop 已安装并在 PATH 中"
    exit 1
}

Set-Location $Root

Write-Host "==> [1/4] 构建镜像 ${Image}:latest"
& docker build -t "${Image}:latest" .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "==> [2/4] 移除旧容器（如存在）"
$null = & docker rm -f $Container 2>&1

Write-Host "==> [3/4] 启动容器（数据/输出/脚本/配置挂载自项目目录）"
$runArgs = @(
    'run', '-d',
    '--name', $Container,
    '--restart', 'unless-stopped',
    '-p', "${Bind}:${HostPort}:8765",
    '-v', "$RootMount/data:/app/data",
    '-v', "$RootMount/output:/app/output",
    '-v', "$RootMount/config:/app/config:ro",
    '-v', "$RootMount/strategies/script:/app/strategies/script",
    "${Image}:latest"
)
& docker @runArgs
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

Write-Host "==> [4/4] 等待服务就绪（容器内自检）"
$ready = $false
for ($i = 0; $i -lt 30; $i++) {
    # alpine 自带 busybox wget，从容器内探测，避免宿主机网络差异
    $null = & docker exec $Container wget -qO- 'http://127.0.0.1:8765/api/factors' 2>&1
    if ($LASTEXITCODE -eq 0) { $ready = $true; break }
    Start-Sleep -Seconds 1
}

Write-Host ''
$null = & docker logs --tail 20 $Container
Write-Host ''
if ($ready) {
    Write-Host "部署完成: http://localhost:${HostPort}"
    exit 0
} else {
    Write-Host "警告: 30 秒内服务未就绪，请查看完整日志: docker logs $Container"
    exit 1
}
