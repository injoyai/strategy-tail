# 命令入口

从项目根目录运行。`STRATEGY_TAIL_ROOT` 可指定运行时根目录，公共配置统一为该目录下的 `config/config.yaml`；各命令的联网和写入行为见下表。

行情维护由项目外完成。日常策略实验走[临时回测指南](../docs/guides/ad-hoc-backtest.md)，表中仍列出历史行情维护入口及其行为。

| 入口 | 用途与配置 | 外部访问与写入 |
| --- | --- | --- |
| `go run ./cmd/lab` | 策略搭建、回测、因子与组合研究；`LAB_ADDR` 默认 `0.0.0.0:8765` | 启动 HTTP 服务并尝试打开浏览器；用户操作可写入策略、报告或更新行情 |
| `go run ./cmd/update` | 显式更新行情，读取 `pull.*` 与估值库路径 | 连接行情源，写入本地行情与估值快照，可能耗时较长 |
| `go run ./cmd/kline-compress -dir data/database -workers 4` | 将分钟库转换为按日压缩格式；先升级读写程序 | 只操作本地分钟库；逐库校验并原子替换，持有行情更新锁，可中断后重跑 |
| `go run ./cmd/valuation-sync -codes sh600519 -start 2020-01-01` | 同步历史估值，默认读取 `research.valuation.database` | 请求估值接口并写入 SQLite；`-all` 同步当前股票列表，`-db` 覆盖路径 |
| `go run ./cmd/backtest` | 作者的调参草稿，策略及年份在 `main.go` 中修改；读取公共成本、仓位、基准与模拟次数 | 先更新行情，再执行回测并写入 `output/backtest/` |
| `go run ./cmd/screen` | 实时筛选与历史买卖点服务，读取 `screen.*`；策略在 `strategies.go` | 连接行情源，写入 `data/database/trade.db`；HTTP 端口默认 9090 |

`valuation-sync` 的可选参数：`-codes` 与 `-all` 二选一，`-start`、`-end` 为日期，`-db` 为 SQLite 路径，`-workers` 为并发数（1..32，默认 4）。`-h` 查看帮助，不启动采集。

## 文件职责

- `main.go` 负责启动装配；Lab 的浏览器启动放在 `browser.go`，估值同步的代码规范化放在 `codes.go`。
- Screen 的策略定义、HTTP API、服务逻辑与持久化模型分别放在 `strategies.go`、`api.go`、`screen.go`、`model.go`，静态资源保留在 `web/`。
- `backtest/main.go` 保留临时参数、备用策略和注释，不按普通服务入口清理。

Screen 的历史镜像脚本 `cmd/screen/dockerpush.sh` 可从任意目录调用，使用项目根构建上下文及统一配置。`Dockerfile.dockerignore` 将上下文限制为预编译程序和该配置，避免沿用根目录排除 `bin` 的规则（[Docker 文档](https://docs.docker.com/build/concepts/context/#filename-and-location)）。脚本会向配置的镜像仓库推送，仅在明确需要发布该服务时运行。

## 已移除的临时入口

`diagnoser`、`screen-realtime`、`screen-kline`、`future`、`market-regime` 中写死的股票、策略或研究年份不作为维护中的命令继续保留。通用诊断、筛选、前向收益分析仍在 `core/`；专项大盘状态报告的命令及专用实现随入口删除，历史源码可从 Git 获取。`data/`、`output/` 和已保存策略不受影响。

早期设计与计划可能引用这些路径，它们是历史记录；当前可运行入口以本页为准。
