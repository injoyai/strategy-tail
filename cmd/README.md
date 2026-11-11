# 命令入口

从项目根目录运行。`STRATEGY_TAIL_ROOT` 可指定运行时根目录，公共配置统一为该目录下的 `config/config.yaml`；行情连接放在私有 `config/mysql.yaml` 的 `pull.mysql`，同名环境变量可覆盖；正式入口缺少 MySQL 配置会报错。各命令的联网和写入行为见下表。

行情维护由项目外完成。日常策略实验走[临时回测与只读数据接口](../docs/guides/ad-hoc-backtest.md)，表中仍列出历史行情维护入口及其行为。

| 入口 | 用途与配置 | 外部访问与写入 |
| --- | --- | --- |
| `go run ./cmd/lab` | 策略搭建、回测、因子与组合研究；`LAB_ADDR` 默认 `0.0.0.0:8765` | 启动 HTTP 服务并尝试打开浏览器；用户操作可写入策略、报告或更新行情 |
| `go run ./cmd/update` | 历史入口 | 返回只读提示。行情同步由外部项目负责 |
| `go run ./cmd/kline-compress -dir data/database -workers 4` | 将分钟库转换为按日压缩格式；先升级读写程序 | 只操作本地分钟库；逐库校验并原子替换，持有行情更新锁，可中断后重跑 |
| `go run ./cmd/valuation-sync -codes sh600519 -start 2020-01-01` | 同步历史估值，默认读取 `research.valuation.database` | 请求估值接口并写入 SQLite；`-all` 同步当前股票列表，`-db` 覆盖路径 |
| `go run ./cmd/migrate-kline -dir data/database/source` | 把源时间 SQLite 行情复制到 MySQL，默认读取 `pull.mysql` | 连接目标 MySQL 写入行情；按代码/年份幂等，仅补缺失，保留已有记录 |
| `go run ./cmd/migrate-legacy-day -dir data/database/day-kline` | 把旧 Unix 主键日线库转换为源时间日线写入 MySQL，默认读取 `pull.mysql` | 连接目标 MySQL 写入日线；价格/成交额按旧整数毫换算、成交量手转股，可重复执行 |
| `go run ./cmd/migrate-legacy-minute -dir data/database/min-kline` | 把旧分钟库（Kline 表或 KLM1/KLM2 压缩块）转换为源时间分钟线写入 MySQL，默认读取 `pull.mysql` | 连接目标 MySQL 写入分钟线；股票成交量手转股、指数保持原值，写前按分钟时间查缺，只写缺失记录；异常库报错停止，可按成功回执续跑 |
| `go run ./cmd/migrate-index-minute -dir /Volumes/server/pull-index-minute-kline/data/database/kline` | 指数年度库 `<年份>/<代码>.db` 的 `KlineMinute1` 导入 MySQL | 源文件只读复制到本地临时快照；只补缺失键，保留原始代码及其他周期，逐库核对写入结果 |
| `go run ./cmd/migrate-mysql-market` | 旧 MySQL 行情按市场拆分，分钟继续按年 | 默认只读计划；`-apply -writers-paused -expected-server-uuid <UUID> -space-path <路径>` 才复制，逐批校验并保留旧表 |
| `go run ./cmd/backtest` | 作者的调参草稿，策略及年份在 `main.go` 中修改；读取公共成本、仓位、基准与模拟次数 | 先更新行情，再执行回测并写入 `output/backtest/` |
| `go run ./cmd/screen` | 实时筛选与历史买卖点服务，读取 `screen.*`；策略在 `strategies.go` | 连接行情源，写入 `data/database/trade.db`；HTTP 端口默认 9090 |

`valuation-sync` 的可选参数：`-codes` 与 `-all` 二选一，`-start`、`-end` 为日期，`-db` 为 SQLite 路径，`-workers` 为并发数（1..32，默认 4）。`migrate-kline` 的参数：`-dir` 为源 SQLite 根目录（支持 `source-time-v1` 和 `source-time-v2`，只读），`-dsn` 覆盖目标 DSN，`-workers` 为并发数（1..16，默认 4）。`migrate-legacy-day` 支持 `-start-year`（0 为全部），新增日线会完整读回核验，并保留目标已有值与新尾部。`migrate-legacy-minute` 按年份、代码顺序处理。股票和指数分钟导入均支持 `-start-year`（默认 0 为全部，边界年包含在内，不设置结束年）；本轮 NAS 使用 `-start-year 2018`，更早源保留待后补。`migrate-legacy-day`、`migrate-legacy-minute` 和 `migrate-index-minute` 均支持 `-config-stdin` 从私有 JSON 字符串读取 DSN。批量维护使用 `NewMySQLImportStore` 读取未验收的市场分表目标，须保持业务写入暂停，完成全量验收后才开放应用。`migrate-legacy-day` 的参数：`-dir` 为旧日线库目录，`-dsn` 覆盖目标 DSN，`-workers` 为并发数（1..16，默认 4），`-codes` 只迁移指定代码。`migrate-legacy-minute` 的参数与旧日线一致，`-dir` 默认为 `<pull.database>/min-kline`，另有 `-read-timeout`（默认 5 分钟）和 `-resume-log`（同一目标的成功 JSONL 回执）。正式源文件只读复制到本机临时快照，完成哈希、完整性和格式校验后再转换；写前查询目标的分钟主键，已有分钟不进入写入批次，新记录写后读回核验。默认遇到源库异常会报错并停止派发后续文件，不把异常记作成功。经用户确认隔离异常行时，可使用 `-quarantine-policy <JSON>`：只在本机快照中移除明确非法值或经源哈希锁定的已核实坏日，原始行完整写入回执的 `quarantinedRecords`；原文件保持不变，其他正常分钟继续查缺。续跑会复读源哈希，仅跳过同一目标且源内容未变的已核验文件。`migrate-index-minute` 的参数：`-dir` 必填，`-workers` 为 1..4（默认 2），`-code`、`-year` 可限制单库，`-preview` 只校验源快照，不连接 MySQL。`-resume-log` 可读取同一目标库的成功 JSONL 回执，仅在源哈希不变时跳过；应使用独立日志文件，不能覆盖续跑依据。它不会把指数加入主板回测池或扩大日常更新白名单。`-h` 查看帮助，不启动采集。

## 文件职责

- `main.go` 负责启动装配；Lab 的浏览器启动放在 `browser.go`，估值同步的代码规范化放在 `codes.go`。
- Screen 的策略定义、HTTP API、服务逻辑与持久化模型分别放在 `strategies.go`、`api.go`、`screen.go`、`model.go`，静态资源保留在 `web/`。
- `backtest/main.go` 保留临时参数、备用策略和注释，不按普通服务入口清理。

Screen 的历史镜像脚本 `cmd/screen/dockerpush.sh` 可从任意目录调用，使用项目根构建上下文及统一配置。`Dockerfile.dockerignore` 将上下文限制为预编译程序和该配置，避免沿用根目录排除 `bin` 的规则（[Docker 文档](https://docs.docker.com/build/concepts/context/#filename-and-location)）。脚本会向配置的镜像仓库推送，仅在明确需要发布该服务时运行。

## 已移除的临时入口

`diagnoser`、`screen-realtime`、`screen-kline`、`future`、`market-regime` 中写死的股票、策略或研究年份不作为维护中的命令继续保留。通用诊断、筛选、前向收益分析仍在 `core/`；专项大盘状态报告的命令及专用实现随入口删除，历史源码可从 Git 获取。`data/`、`output/` 和已保存策略不受影响。

早期设计与计划可能引用这些路径，它们是历史记录；当前可运行入口以本页为准。

`migrate-mysql-market -in-place` 使用用户确认的原表复用路径：需 `-apply -writers-paused -expected-server-uuid <UUID> -space-path <数据卷> -tmp-space-path <临时卷> -state-dir <断点目录> -backup-proof <冷备状态>`。保留原排序规则，指数校验后才从原表移除；股票表更换主键并改名，原混合表不再保留，回退依赖恢复点。

本次 NAS 冷恢复点已按用户要求删除，当前改从原始 SQLite 全历史重导；下面的冷恢复点命令保留为维护参考，不是正在运行的来源。`migrate-mysql-market -reload-from-year 2018` 支持从完整冷恢复点向新空库重导：需 `-config-stdin -apply -writers-paused -expected-server-uuid <UUID> -backup-proof <冷恢复点状态> -state-dir <断点目录> -space-path <数据卷>`。私有标准输入对象包含 `sourceDSN`、`targetDSN`；源为本机 13318 端口的独立只读克隆，目标为本机 3306。仅保留 2018 年起日线和分钟线，完整字段校验后提交；首次要求空库，断点绑定冷恢复点清单哈希。详见 [MySQL 存储说明](../docs/guides/mysql-storage.md#从冷恢复点重导指定年份起的数据)。
