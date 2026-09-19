# 策略实验室（cmd/lab）容器镜像
# sqlite 驱动为 glebarez/go-sqlite（纯 Go），CGO_ENABLED=0 静态编译即可
# 基础镜像取本地已缓存的 golang:1.25 / alpine:latest，不依赖 Docker Hub 可达
FROM golang:1.25 AS builder

WORKDIR /build

# 模块代理默认 goproxy.cn（与开发机一致），可 --build-arg GOPROXY=... 覆盖
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}

# 先复制依赖清单并下载，利用层缓存
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/lab ./cmd/lab

###############################################################################
#                                  运行阶段
###############################################################################
FROM alpine:latest

# K 线时间与交易日判断依赖本地时区（Asia/Shanghai）
ENV TZ=Asia/Shanghai
# 容器内必须监听 0.0.0.0，否则 docker 端口映射不可达（本地直跑默认仍为 127.0.0.1）
ENV LAB_ADDR=0.0.0.0:8765

WORKDIR /app
COPY --from=builder /out/lab ./lab
# 时区数据与 CA 证书直接取自构建阶段，避免运行阶段 apk 联网安装
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# 运行时读写目录（data/output/strategies/script/config 由 deploy.ps1 挂载回项目目录）
EXPOSE 8765

ENTRYPOINT ["./lab"]
