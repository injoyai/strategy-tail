# 本地依赖补丁

## github.com/injoyai/base

- 来源：[injoyai/base v1.2.20](https://github.com/injoyai/base/tree/v1.2.20)，完整模块快照位于 `injoyai-base/`，通过根 `go.mod` 的相对 `replace` 接入。
- 上游模块 zip 校验：`h1:S0y66Cl/VptBsX8SgpQk7o3rMK17rvqP7hI/LQCa9Sc=`；[本地差异](base-wait-race.patch)记录唯一修改。差异采用无上下文格式，手动应用时使用 `git apply --unidiff-zero`。
- 仅修改 `maps/wait/map_wait.go`：为异步回调列表添加互斥锁，注册与分发／清理使用同一把锁。其余上游文件原样保留；没有修改共享模块缓存。
- 用户已于 2026-10-18 明确授权项目内保留并接入本地依赖补丁。上游模块包未附带许可证文件，此处不添加或宣称新的上游许可证；保留来源说明，不在本任务中公开发布。
- 根 `Dockerfile` 在 `go mod download` 前复制该目录。独立拷贝源码构建时须同时保留 `third_party/injoyai-base/`，不能只复制根模块清单。

验证使用根项目的实际依赖图：`go test ./... -count=1`、`go test -race ./... -count=1`、`go build ./...`、`go vet ./...`；行情用例 `TestEmptyKlineUpdatesCompleteAndPreserveData` 另以 `-race -count=100` 运行。

根项目的 `./...` 不遍历这个嵌套模块。上游 `maps/wait` 的 `TestNew` 是带有无限循环的手工演示，快照保持原样，不把整个上游测试集当作自动化检查；本次依赖回归通过根项目的实际调用链完成。

该替换影响所有导入该模块的调用方，包括 TDX 和 goutil。后续升级应先对比上游的回调同步实现；确认上游已修复并通过以上检查后，再移除根 `replace`、本地快照与对应 Dockerfile COPY，避免升级时丢失补丁。不要将其他依赖修改混入此快照。
