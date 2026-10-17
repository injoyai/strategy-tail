#!/usr/bin/env sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo_root"

name="screen-stock"
arch="amd64"

GOOS=linux GOARCH=$arch go build -v -ldflags="-w -s" -o ./bin/$name ./cmd/screen

docker pull --platform=linux/$arch alpine:latest

docker build --platform=linux/$arch --push -t crpi-ayrx20sj8nkmrgmh.cn-hangzhou.personal.cr.aliyuncs.com/injoyai/$name:latest -f ./cmd/screen/Dockerfile .

sleep 2
