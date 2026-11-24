# 策略实验室（cmd/lab）容器镜像
# sqlite 驱动为 glebarez/go-sqlite（纯 Go），CGO_ENABLED=0 静态编译即可
# 基础镜像取本地已缓存的 golang:1.25-alpine / alpine:3.22，不依赖 Docker Hub 可达
FROM golang:1.25-alpine AS builder

WORKDIR /build

# 模块代理默认 goproxy.cn（与开发机一致），可 --build-arg GOPROXY=... 覆盖
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}

COPY go.mod go.sum ./

# golang:alpine 基础镜像不含 tzdata，运行阶段从这里拷贝时区数据
RUN apk add --no-cache tzdata

COPY . .
ARG APP_REVISION=unknown
ARG TDX_REVISION=unknown
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/injoyai/strategy-tail/internal/lab.appRevision=${APP_REVISION} -X github.com/injoyai/strategy-tail/internal/lab.tdxRevision=${TDX_REVISION}" -o /out/lab ./cmd/lab
# 同时生成 Windows 提醒客户端，供页面下载；与当前服务版本保持一致
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w -H windowsgui" -o /out/strategy-notify.exe ./cmd/notify

###############################################################################
#                                  运行阶段
###############################################################################
FROM alpine:3.22

ARG APP_REVISION=unknown
ARG TDX_REVISION=unknown
LABEL org.opencontainers.image.revision="${APP_REVISION}" \
      io.injoyai.tdx.revision="${TDX_REVISION}"

# K 线时间与交易日判断依赖本地时区（Asia/Shanghai）
ENV TZ=Asia/Shanghai
# 本地直跑与容器内默认均监听 0.0.0.0。
ENV LAB_ADDR=0.0.0.0:8765

WORKDIR /app
COPY --from=builder /out/lab ./lab
COPY --from=builder /out/strategy-notify.exe ./strategy-notify.exe
# 时区数据与 CA 证书直接取自构建阶段，避免运行阶段 apk 联网安装
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# 运行时读写目录（data/output/config 由 deploy.sh 挂载回项目目录）
EXPOSE 8765

ENTRYPOINT ["./lab"]
