# 命令入口

从项目根目录运行。`STRATEGY_TAIL_ROOT` 可指定运行时根目录，公共配置统一为该目录下的 `config/config.yaml`；各命令的联网和写入行为见下表。

| 入口 | 用途与配置 | 外部访问与写入 |
| --- | --- | --- |
| `go run ./cmd/lab` | 策略搭建、回测、因子与组合研究；`LAB_ADDR` 默认 `0.0.0.0:8765` | 启动 HTTP 服务并尝试打开浏览器；用户操作可写入策略、报告或更新行情 |
| `go run ./cmd/update` | 显式更新行情，读取 `pull.*` 与估值库路径 | 连接行情源，写入本地行情与估值快照，可能耗时较长 |
| `go run ./cmd/kline-compress -dir data/database -workers 4` | 将分钟库转换为按日压缩格式；先升级读写程序 | 只操作本地分钟库；逐库校验并原子替换，持有行情更新锁，可中断后重跑 |
| `go run ./cmd/valuation-sync -codes sh600519 -start 2020-01-01` | 同步历史估值，默认读取 `research.valuation.database` | 请求估值接口并写入 SQLite；`-all` 同步当前股票列表，`-db` 覆盖路径 |
| `go run ./cmd/migrate-kline -dir data/database/source` | 把源时间 SQLite 行情复制到 MySQL，默认读取 `pull.mysql` | 连接目标 MySQL 写入行情；按代码/年份幂等，目标端更新的数据不会被覆盖 |
| `go run ./cmd/migrate-legacy-day -dir data/database/day-kline` | 把旧 Unix 主键日线库转换为源时间日线写入 MySQL，默认读取 `pull.mysql` | 连接目标 MySQL 写入日线；价格/成交额按旧整数毫换算、成交量手转股，可重复执行 |
| `go run ./cmd/migrate-legacy-minute -dir data/database/min-kline` | 把旧分钟库（Kline 表或 KLM1/KLM2 压缩块）转换为源时间分钟线写入 MySQL，默认读取 `pull.mysql` | 连接目标 MySQL 写入分钟线；股票成交量手转股、指数保持原值，明显损坏的库自动跳过，可重复执行 |
| `go run ./cmd/backtest` | 作者的调参草稿，策略及年份在 `main.go` 中修改；读取公共成本、仓位、基准与模拟次数 | 先更新行情，再执行回测并写入 `output/backtest/` |
| `go run ./cmd/screen` | 实时筛选与历史买卖点服务，读取 `screen.*`；策略在 `strategies.go` | 连接行情源，写入 `data/database/trade.db`；HTTP 端口默认 9090 |

`valuation-sync` 的可选参数：`-codes` 与 `-all` 二选一，`-start`、`-end` 为日期，`-db` 为 SQLite 路径，`-workers` 为并发数（1..32，默认 4）。`migrate-kline` 的参数：`-dir` 为源 SQLite 根目录，`-dsn` 覆盖目标 DSN，`-workers` 为并发数（1..16，默认 4）。`migrate-legacy-day` 的参数：`-dir` 为旧日线库目录，`-dsn` 覆盖目标 DSN，`-workers` 为并发数（1..16，默认 4），`-codes` 只迁移指定代码。`migrate-legacy-minute` 的参数与旧日线一致，`-dir` 默认为 `<pull.database>/min-kline`。`-h` 查看帮助，不启动采集。

## 文件职责

- `main.go` 负责启动装配；Lab 的浏览器启动放在 `browser.go`，估值同步的代码规范化放在 `codes.go`。
- Screen 的策略定义、HTTP API、服务逻辑与持久化模型分别放在 `strategies.go`、`api.go`、`screen.go`、`model.go`，静态资源保留在 `web/`。
- `backtest/main.go` 保留临时参数、备用策略和注释，不按普通服务入口清理。

Screen 的历史镜像脚本 `cmd/screen/dockerpush.sh` 可从任意目录调用，使用项目根构建上下文及统一配置。`Dockerfile.dockerignore` 将上下文限制为预编译程序和该配置，避免沿用根目录排除 `bin` 的规则（[Docker 文档](https://docs.docker.com/build/concepts/context/#filename-and-location)）。脚本会向配置的镜像仓库推送，仅在明确需要发布该服务时运行。

## 已移除的临时入口

`diagnoser`、`screen-realtime`、`screen-kline`、`future`、`market-regime` 中写死的股票、策略或研究年份不作为维护中的命令继续保留。通用诊断、筛选、前向收益分析仍在 `core/`；专项大盘状态报告的命令及专用实现随入口删除，历史源码可从 Git 获取。`data/`、`output/` 和已保存策略不受影响。

早期设计与计划可能引用这些路径，它们是历史记录；当前可运行入口以本页为准。

## Windows 卖点提醒

`go run ./cmd/notify -server http://服务器地址` 连接 Lab 的卖点事件流，Windows 托盘可修改服务器地址。事件进度按规范化的事件地址分别保存，切换服务器及切换回来均使用各自的进度。

旧版单整数进度首次启动时自动转为按服务器保存的 JSON。旧进度归入原配置保存的服务器；原配置缺少地址时归入本次启动地址。首次连接一个服务器时默认过滤启动前的提醒，`-replay` 可补发已有提醒，`-after` 可覆盖初始连接的事件位置。
