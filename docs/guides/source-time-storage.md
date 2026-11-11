# 行情源时间与数据层

行情拉取和新库存储沿用 tdx v2 的源时间契约，不增加时区字段或 Unix 时间戳：日线为 `YYYY-MM-DD`，分钟线为 `YYYY-MM-DD HH:MM`。日线日期是源标签，不在库中补造收盘时刻。行情源时间不按主机 `TZ` 或证券所在地重新解释。

## 当前存储与旧数据

应用运行入口使用只读的 MySQL 行情接口。行情同步、分表创建和补数由外部项目负责。读取目标可从启动日志或 `/api/data-update/status` 的 `storage` 字段确认。该接口返回上证指数最近一根日线的 `benchmarkDate`。

`pull.database` 继续承载更新锁及旧数据目录；估值、代码元数据、练习和策略状态仍保存在各自的本地库。以下目录仅用于归档和显式迁移，应用不会读取其中的行情或更新状态：

```text
data/database/
  day-kline/                         # 旧 Unix 日线
  min-kline/                         # 旧分钟线/压缩块
  update.db                          # 旧更新标记
  source/                            # 之前的 SDK 源时间 SQLite 数据
```

应用直接返回 MySQL 的记录，缺数据时返回缺失，不用旧文件补齐。股票池只从 MySQL 日线枚举，保留目标库中的退市历史代码；没有日线股票时报错。切换前先完成所需历史迁移，并停止旧版更新任务，避免两套程序维护不同数据集。旧文件不会自动删除；回退旧程序只能看到旧数据。

库内保存股票成交量为股，指数保留其行情单位；价格和金额为原币种浮点值，未知成交额保存为 SQL NULL。默认首次拉取范围沿用 SDK：未设置 `StartAt` 时最近两年，之后增量更新；更早历史需要迁移。`kline-compress` 仅维护旧 `min-kline/`，不会压缩或更新 MySQL。

## 应用与 SDK 的职责

- strategy-tail 从 MySQL 读取行情和历史股票池。Lab 和回测启动时从 `pull.database` 下已有的 `metadata.db`、`gbbq.db` 以及 `research.valuation.database` 读取可选的代码、股本事件和历史估值。策略优先使用已入库的股本和换手率，缺少股本时按该交易日查询已有股本事件。
- 实时选股保留 TDX 实时报价读取，历史日线和分钟线使用 MySQL。Lab 状态直接查询库中上证指数的最新日线日期。
- 显式历史迁移工具单独打开写入目标，日常运行入口使用只读打开方式。

## 回测时间边界

策略仍使用现有 `Kline/Klines`、`time.Time` 和整数手模型。股票读取边界显式将股转换为手，指数保持解码后的量单位，将源日期映射到原有日线执行时刻；15:00 只存在于当前标准市场回测视图，不写入源日线表。旧库读取统一使用标准行情约定的固定东八区，不依赖主机 `time.Local`。内存中的 `Kline.Unix` 仅供旧模型兼容，不写入新源时间表。

横截面排名以内存源日期字符串作为键，同一天不因 `time.Location` 指针或偏移不同而查不到；接口仍接收 `time.Time`，取其钟面日期，不按绝对时刻换日。

港美股及期货的源时间可由 SDK Store 原样保存。现有标准市场回测适配器拒绝解释这些扩展源时间，不猜测来源时区、夏令时或夜盘交易日，也不把 A 股 15:00/T+1 规则直接套用过去。跨市场真实时序回测需要另行确认来源口径与交易规则。

## MySQL 存储与迁移

正式入口通过必需的 `pull.mysql` 使用本项目的 `marketstore.MySQLStore`：日线按市场分表，分钟线按市场和年份分表，覆盖标记、空扫描和更新标记各一张小表；连接失败即启动失败，不回落 SQLite。DSN 由本项目适配器收敛为源时间文本（`parseTime=false`）、`utf8mb4`，不使用 `interpolateParams` 和 `compress`。

连接配置及优先级见 [MySQL 存储设计](mysql-storage.md#配置与验证边界)。部署脚本检查现有实例是否有运行中的任务，并验证新服务实际使用 MySQL。

已有 `source/` SQLite 行情用 `go run ./cmd/migrate-kline`（从项目根目录运行）复制到 MySQL，默认读取 `<pull.database>/source` 和 `pull.mysql`。支持 `source-time-v1`、`source-time-v2` 目录，均以只读方式打开；v1 的历史股本和换手率一并保留。迁移按代码/年份重复执行，MySQL 目标仅补入缺失主键，保留已有记录、来源字段与新尾部。分钟覆盖标记仅在目标尚无标记时复制；空扫描缓存只是短期缓存，不迁移。迁移不删除源 SQLite，也不清理目标库中的其他代码。

旧 `day-kline/*.db`（Unix 主键、整数毫/手）用 `go run ./cmd/migrate-legacy-day` 直接转换为源时间日线写入 MySQL，默认读取 `<pull.database>/day-kline` 和 `pull.mysql`。读取时复用旧库的价格/成交额换算规则：INT 列按 `÷1000` 转元，`Volume`（手）`×100` 转股，换手率与股本原样保留；日期由 `Unix` 按东八区取钟面日期，同一日期多行保留最后一条，与旧读取路径合并语义一致。仅补入目标缺失的日期，已有记录和更新尾部优先保留。

旧 `min-kline/{code}/{code}-{year}.db`（旧 `Kline` 表或 KLM1/KLM2 压缩块）用 `go run ./cmd/migrate-legacy-minute` 逐代码/年份写入 `kline_minute_{year}`，默认读取 `<pull.database>/min-kline` 和 `pull.mysql`。解码后时间按东八区取 `YYYY-MM-DD HH:MM`，股票成交量 `×100`（手转股）、指数保持原值；同一分钟重复记录保留最后一条，负价格/非有限值记录丢弃。丢弃比例超过 20% 判定为历史压缩损坏；迁移入口发现异常或需丢弃记录时不写入该库，并报错停止后续派发，`invalid` 标记严重损坏的文件。命令先以只读文件句柄复制到本机快照并核对源哈希、大小、mtime 与活动日志；SQLite 查询只发生在本机快照。逐分钟查询目标键做差集，已有分钟不送入写入批次；新记录全部读回核验。`-resume-log` 可复用同一目标的已核验回执，源哈希匹配时跳过已完成文件。迁移不写 `MinuteCoverage`，后续增量拉取会按正常覆盖探测补齐。

MySQL 字段、索引、更新/导入语义与已有表调整边界见 [MySQL 存储设计](mysql-storage.md)。所有迁移进度中的 rows/dayRows/minuteRows 表示实际补入行数，重复执行且没有缺口时为 0；skipped 表示该批没有新增记录。
