# MySQL 行情存储设计

MySQL 适配器由本项目 `internal/marketstore.MySQLStore` 实现，通过 SDK 的 `storage.BarStore` 接口注入；tdx 开源项目不承载本项目的 MySQL 适配和迁移代码。本文核对其字段与当前回测查询方式。应用的行情、股票池和更新状态统一使用 MySQL；历史 SQLite 行情只用于显式迁移。MySQL 不承接估值、交易记录、代码元数据等其他本地库。

## 表与字段

| 表/字段 | 当前选择 | 设计依据与限制 |
|---|---|---|
| `<region>_<asset>_kline_day` | 按市场日线表，如 `cn_stock_kline_day` | 由市场路由，沪深北股票继续共用 `cn/stock`。 |
| `<region>_<asset>_kline_minute_YYYY` | 市场＋源年份分表，如 `cn_index_kline_minute_2025` | 年份缓存按市场隔离；跨年由 Store 合并，不重复保存 market/year 列。 |
| 行情主键 | `(code, time)`，市场由表名确定 | 同一市场/证券/源时间只保留一条；匹配范围查询、最后时间和尾部替换，无需额外自增 ID 或重复的 code/time 索引。 |
| `code VARCHAR(32)`；辅助表保留 `market VARCHAR(16)` | 保留证券业务键 | 覆盖当前内置市场和证券；期货合约代码保留大小写。新表使用 `utf8mb4_bin`，不得使用不区分大小写的代码主键。 |
| 日线 `time DATE` | 源日期 | 不写收盘时刻、时区或 Unix。 |
| 分钟 `time DATETIME` | 源日期时分，秒为 00 | 不用会随会话时区转换的 `TIMESTAMP`；适配器按文本返回到分钟。 |
| `open/high/low/close DOUBLE` | 原币种浮点价格 | 与 SDK 的 float64 一致，保留跨市场精度和可能的负价格；不转换整数毫。浮点值不用于要求十进制精确结算的账户记账。 |
| `amount DOUBLE NULL` | 原币种成交额 | 未知为 `NULL`，与真实的 0 区分；只影响新建表，既有列不自动修改。 |
| `volume BIGINT` | SDK 最小单位的 int64 | 股票为股，期货为张，指数按 SDK 市场契约；不使用容易溢出的 INT，不统一按手解释。 |
| 日线 `turnover DOUBLE` | 换手率，百分比数值 | 保留历史导入值；SDK 新日线不再提供换手率。 |
| 日线 `float_stock/total_stock DOUBLE` | 历史股本（股） | 保留已有列和值，空值沿用旧表的 0；策略读取时可按源日期从股本事件补齐。 |
| 分钟 `source VARCHAR(8)` | `''` 或 `trade` | 空值为原生/无额外来源标记，`trade` 为分笔合成；插入、读取、迁移都保留，不得把近似合成数据伪装为原生分钟线。 |
| `minute_coverage` | `(market, code, year)` 主键 | 保存扫描起点、分笔合成终点和模式；时间文本允许空终点，属于扫描状态而非证券行情。 |
| `minute_empty_scan` | 同上主键 | 起点和最后检查时间；`checked_at` 是操作发生的 Unix 秒，用于缓存有效期。 |
| `update_marks` | `mark_key` 主键、BIGINT 时间 | 时间为任务完成时刻，不是行情源时间。 |

目前查询均为固定证券的时间范围与历史代码枚举，联合主键足够。若以后增加“某一天扫描全部证券”的查询，再用真实 `EXPLAIN` 和延迟决定是否新增以 time 开头的索引；每增加一个索引都会增加分钟线写入和磁盘成本。

参考：[MySQL 日期类型](https://dev.mysql.com/doc/refman/8.0/en/datetime.html)、[浮点类型](https://dev.mysql.com/doc/refman/8.0/en/floating-point-types.html)、[InnoDB 聚簇索引](https://dev.mysql.com/doc/refman/8.0/en/innodb-index-types.html)、[字符串大小写比较](https://dev.mysql.com/doc/refman/8.0/en/case-sensitivity.html)。

## 更新与历史迁移

旧表若将 `amount` 声明为 `NOT NULL`，已知成交额可以继续写入；收到未知成交额时，该批会报错并保留旧数据。需要写入未知成交额的旧表应由维护人员单独评估并修改列定义；应用启动和更新均不自动执行该变更，也不会重解释旧数据中的 0。

- 正常行情更新使用 `SaveDay/SaveMinute`：事务内删除 `time >= from` 后重插，修正盘中未完成 K 线。同日尾部重写时保留已有股本、换手率字段。`from=""` 表示覆盖全部日线或指定年份的全部分钟线；空 rows 不删除数据。
- MySQL 历史迁移使用 `ImportDay/ImportMinute`：事务内只补入缺失主键，冲突时保留目标记录，不删除新尾部，也不以首末记录存在推断中间完整。采用 `ON DUPLICATE KEY UPDATE time=time`，不会像 `INSERT IGNORE` 一样吞掉其他数据错误。返回实际新增行数，已存在记录不计入新增。
- MySQL 的 `clientFoundRows` 强制关闭，确保重复键的无变化操作返回 0；否则迁移新增行数会失真。
- 源时间 SQLite 迁移同时复制来源标记。覆盖标记只在目标尚无标记时导入，保留目标已有模式和范围；旧 Unix 分钟库没有可靠覆盖状态，不伪造完成标记。
- 重复迁移仍需扫描源库和检查目标主键，但不再整年删除重写。已有行的内容纠错应通过明确的数据修复或行情刷新执行，迁移不会覆盖冲突值。
- `DayCodes` 从 MySQL 日线枚举已存证券，保留退市历史代码。正式应用直接读取目标 Store，不再合并旧库或从本地目录补股票池；目标缺数据时应完成迁移或更新。

## 旧股票分钟库查缺补漏

`cmd/migrate-legacy-minute -start-year 2018` 可只导入 2018 年及以后的年度库；默认 0 仍为全部，不设结束年份。`cmd/migrate-legacy-minute` 处理 `<代码>/<代码>-<年份>.db`，支持 Kline 行表与 KLM1/KLM2 压缩块。源文件只用只读文件句柄复制到本机临时快照，复制后再读源文件核对 SHA-256，并检查大小、mtime、文件身份、活动日志及快照完整性。只在本机快照中执行 SQLite 查询，不在挂载源目录产生事务、日志或临时文件。

沿用已有旧库转换边界：INTEGER 价格和金额按整数毫除以 1000，股票成交量手乘 100 转股，UTC 存储时间转为上海源日期时分，同一分钟按原始行顺序保留最后一条。默认发现源格式异常或需丢弃记录时停止该库并返回错误，不静默丢弃后宣告成功。用户明确允许隔离后，可提供 `-quarantine-policy` JSON；`invalidNumericRows` 识别缺失/非法数值、负价格/成交量/成交额和目录年份不符，`badDays` 指定已核实的代码、日期、原因及源 SHA-256。规则仅修改本机临时快照，逐行原始字段和原因写入回执 `quarantinedRecords`。遇到数值非法记录所关联的整日平价零成交、且与相邻日期价位突变逾千倍的已复现损坏形态时，未经源哈希锁定的人工核实规则不得继续写入该库；此判断只触发暂停复核，不按经验阈值自动删除。常规有效库复用转换器验证结果，避免重复全表隔离扫描。当隔离行超过单库 20% 时停止单独复核；压缩块异常不猜测修补。隔离不修改源文件，也不删除或纠正 MySQL 已有记录。

写前通过独立只读 MySQL 连接查询该证券/年份的全部分钟时间主键，与转换后的源记录逐键比较；仅缺失分钟进入 ImportMinute 批次，完整存在的年度库不执行写入。不会用代码存在、行数、首尾时间替代逐键校验，因此可以补齐中间缺口。目标主键和 ImportMinute 的保留语义同时防止并发情况下的重复与覆盖。新增记录全部逐字段读回核验；已有目标值即使与源不同也不改写。

JSONL 回执含源文件哈希、去重后的源行数、已有分钟数、写前缺口、新增数、核验状态和异常信息。`-resume-log` 只复用同一目标、相同源哈希的成功回执；目标被另行清理、修复或需要重新应用隔离策略时，应去掉续跑参数重新核对；规则改变不会自动回撤以前写入的数据。导入分钟线不会补造日线、扩大日常更新范围或把没有日线的证券加入回测股票池。

## 独立指数分钟库导入

`cmd/migrate-index-minute -start-year 2018` 同样筛选目录年份下限，保留此后全部最新数据；与 `-year` 同时使用时两项条件同时生效。`cmd/migrate-index-minute` 面向 `<年份>/<指数代码>.db` 中的 `KlineMinute1`，不复用旧 `Kline`/KLM 解码器。只用只读文件句柄复制源文件，在本地临时快照上执行 SQLite 查询；不对源文件初始化连接、建表、锁定、checkpoint 或整理。复制前后检查活动日志、大小和修改时间，并重新读取源文件比较 SHA-256；发现日志或内容变化即报错，不导入该快照。

本地快照通过 SQLite 完整性检查后，以 `Date` 的东八区日期时分校对 `Year/Month/Day/Hour/Minute`，并验证目录年份；OHLC/金额保留 REAL 数值，不做整数毫换算，成交量按源字段原样保存（包括 0）。同分钟重复记录按 ID 保留最后一条并计数。不合并 `sh999999` 等原始代码的别名，也不把 5/15/30/60 分钟表混入一分钟表。

目标路由为 `cn/index` 对应的 `cn_index_kline_minute_YYYY`，调用 `ImportMinute` 只补缺失键；已有值即使与源不同也保留。每库导入后完整读回源时间区间，对新增记录逐字段核对、对原有记录确认未变，并在 JSONL 中记录源哈希、行数、重复数、新增数及保留冲突数。不会写入覆盖标记或全市场成功时间，也不修改日常更新指数范围。导入分钟线不会自动补造日线或改变主板股票数量。

源数据规模较大时逐库处理，临时空间仅与并发文件大小有关。中断可直接重跑（重复主键不覆盖），或使用同一目标的成功回执 `-resume-log` 跳过哈希未变的已完成文件；回执仅用于同一导入任务续跑，目标被另行删除或修复后应重新完整核对。不能把预览回执用作成功导入依据。

基金市场另支持根目录 `<代码>.csv`，根据扩展名自动识别，使用 `-market cn/etf -start-year 0`；CSV 不接受非零 `-year` 或 `-start-year`，避免把跨年单文件误当年度目录截断。输入须为未复权原始价格，UTF-8 表头顺序为 `日期,时间,开盘,最高,最低,收盘,成交量,成交额`，允许 BOM；日期格式 `YYYY-MM-DD`，时间为 `HH:mm` 或秒为零的 `HH:mm:ss`。价格及成交额保持元，整数手成交量经 SDK 乘 100 转为份数。时间必须非递减，同分钟重复按最后一行处理并计数；非法日期、非有限或负数、成交量溢出及倒序均报错。

CSV 同样只读复制并复核源 SHA256；先完整验证冻结文件，再按实际年份逐段补写及读回，内存最多保留一个证券一年的记录。后部格式错误不会导致前部年份提前写入；数据库写入或读回错误仍可能留下已成功提交的年份，重跑按缺失键补齐，不承诺跨文件或跨年份原子提交。已有值、来源字段和更新尾部保留，不依据其他数据源修正 CSV 原值。

## 旧混合市场表迁移到市场分表

先暂停所有行情写入并保留旧程序。`go run ./cmd/migrate-mysql-market` 默认只读输出源表、新表和证券年度批次数量；不会初始化 Store 或建表。正式复制必须在数据库所在主机运行，提供 `-apply -writers-paused -expected-server-uuid <UUID> -space-path <同卷路径>`。最小磁盘余量默认 100 GiB；配置仍复用私有 `pull.mysql`，NAS 专用执行支持标准输入 JSON DSN，不需要把密码写入参数或临时配置。

迁移用独立连接对全部旧行情表持有 READ 锁，其他连接逐证券/年份执行小事务。新表复制原类型，去掉 market 列，以 `(code,time)` 为主键并使用 `utf8mb4_bin`。已有目标值不覆盖；同一批中只要出现字段冲突、缺失或额外分钟，就回滚并停止。每批完整比较价格、数量、金额、来源/换手率/股本，再校验所有目标证券集合。最后才将 `kline_schema` 中 `market-v1` 的 ready 设为 1。旧表和三个辅助状态表均保留。

存在旧混合表但尚无 ready 标记时，新版 Store 拒绝打开，避免接入半成品。迁移中断可以在写入持续暂停时重新执行：已复制行不重复写入，仍会重新校验，不盲信旧进度。完成标记已写入后拒绝重新播放旧表，避免切换后以过期数据干扰新行情。数据集 Identity 保持不变，使原更新标记继续有效。

迁移验收后再部署新版所有写入入口，并恢复更新。旧程序仍会访问旧表，必须停止使用；新程序不混读或双写。回退到旧版前须暂停写入并处理切换后新表的新增/修订数据，不能直接启动旧程序将其视为完整回退。旧表清理属于单独维护操作，不由本命令执行。

市场名受辅助表字段约束，最多 16 个 ASCII 字符。内置的 `region/asset` 继续使用原表名，例如 `cn/stock`。单段自定义市场名以字母开头，可含字母和数字；用 `_x_` 加十六进制编码生成独立表前缀，以区分大小写并避免与内置表重名。本项目 `marketstore.MySQLDayTable`、`marketstore.MySQLMinuteTable` 是迁移器与业务读写的统一命名入口。

分表不是性能承诺：切换前后使用相同证券、年份和查询，比较执行计划、返回行数与耗时。InnoDB 主键索引已按市场和代码定位旧数据；表名调整本身不改变返回量。[MySQL 主键组织说明](https://dev.mysql.com/doc/refman/8.0/en/innodb-index-types.html)。

## 原表复用分表（维护窗口）

数据仅含 `cn/stock`、`cn/index` 且允许暂停所有写入时，可使用 `migrate-mysql-market -in-place`。此模式先迁出并完整校验指数，再分批从原表移除指数，单次 `ALTER ... ALGORITHM=INPLACE` 去掉 market、更换主键并改名为股票表。保留每张旧表的原排序规则：同时把历史 `utf8mb4_unicode_ci` 改为 `utf8mb4_bin` 会使本次组合要求 COPY。现有国内代码已规范为小写；新市场、新年度表仍采用二进制规则。

正式执行需额外提供 `-state-dir`（持久化校验断点）、`-backup-proof`（root 所有且不可被普通用户修改的冷备恢复点状态）、`-tmp-space-path`（MySQL 临时目录所在宿主机卷）。CLI 核对服务器 UUID、冷备成功状态及清单 SHA256。各股票表在 ALTER 前后以相同 SQL 字段文本、字段长度和行顺序计算 SHA256；行数和哈希都必须一致。指数先逐字段比较并核对全表哈希，之后每批删除至多 10,000 行。

原表和指数目标由同一 SQL 连接持有 WRITE 锁；使用 autocommit=0 与显式 COMMIT，避免 BEGIN 释放显式锁。改名后重新取得目标锁并核验。断点先于删除持久化，删除后或改名后中断均可重新校验恢复；发现目标指数冲突或股票哈希变化会停止。验收标记为 0 时，即使所有旧表已改名，新程序也拒绝接入。

原表复用不会保留原混合表，回退依赖完整冷备恢复点。Btrfs 同卷 reflink 只在数据库正常关闭后执行，强制 reflink、不降级为普通复制；它是同盘维护恢复点，不替代异盘灾备。保留恢复点期间，后续重建会增加实际占用。执行前后分别检查数据卷、临时卷余量；每次只重建一张表。长 DDL 的客户端读超时为 12 小时，SIGINT 在当前年度表完整核验后停止，不强杀数据库。

## 当前 NAS 原始 SQLite 重导

当前按用户最新决定先导入 2018 年起的数据：股票原始库 2018–2025 年，指数原始库 2018 年至最新（包含 2026 年最新分钟），只导入 1 分钟数据。更早原始源保留，待以后分批补齐。股票读取已校验的 NAS SSD 原始副本；指数从用户指定的 `/Volumes/server/pull-index-minute-kline/data/database/kline` 只读复制到独立 SSD 暂存目录，比较文件集合、大小、mtime 和 SHA256 后才导入。源目录不写入 SQLite 日志，不修改其他周期，不更名指数代码。

已按用户要求删除冷恢复点及临时只读克隆，不再使用它们作为来源。用户于 2026-11-07 提供 103 上的 2026 股票分钟源 `/vol1/1000/server/pull-stock-minute-kline/data/database/kline`，并确认沿用 2018–2025 的历史范围，在其验收通过后导入 2026；未要求补齐 2000–2017。该目录也有较早年份，2026 阶段只选取 `<代码>/<代码>-2026.db`。现有本地日线源单独复制并核验，再以 `migrate-legacy-day -start-year 2018` 查缺导入；可用日线范围以源文件为准。

历史导入在暂停业务读写的维护窗口使用 `marketstore.NewMySQLImportStore`：它允许无旧混合表且 `kline_schema` 为 `market-v1,ready=0` 的目标。正常 `NewMySQLStore` 仍拒绝该未验收标记。开始前在空目标创建 `ready=0`，逐库新增记录完整读回核验；股票成功、指数源/目标一致、日线读回通过仍不自动部署。补齐待提供源并完成业务验收后再设置 `ready=1`、恢复应用与更新。

数据库曾重新创建，之前导入回执不可作为本轮跳过依据。当前任务使用独立的新回执；同地址、同库名不代表数据仍在。具体审计路径及只读进度见 `output/mysql-market-split-20261106/ORIGINAL-2018.md`。

## 从冷恢复点重导指定年份起的数据

以下为保留的维护命令说明。本次冷恢复点已按用户要求删除，此路线已停止，不能继续使用旧证明或旧断点。

目标已经清空并重新创建时，可使用维护入口 `migrate-mysql-market -reload-from-year 2018`。本次操作从冷恢复点的独立 reflink 克隆启动只读源，保留原恢复点不变；源仅绑定 NAS 本机 `127.0.0.1:13318`，新目标为本机 `3306`。源必须开启 `read_only`、`super_read_only`，且服务器 UUID 与冷恢复点证据一致。不会连接原 SQLite 文件重新覆盖原库已保留的冲突版本。

此模式要求 `-config-stdin -apply -writers-paused -expected-server-uuid <UUID> -backup-proof <冷恢复点状态> -state-dir <断点目录> -space-path <数据卷>`。标准输入为含 `sourceDSN`、`targetDSN` 的私有 JSON 对象，不将密码放入 argv、日志或文件。首次执行只接受空目标库；续跑必须使用相同年份、冷恢复点清单哈希与断点。

日线按源日期筛选，分钟线只选择起始年份及之后的年度表，再按股票、指数路由到新表。保留原字段类型和数值，新行情表主键为 `(code,time)`、排序规则为 `utf8mb4_bin`。每个证券年度使用有界批量事务，满批复用预编译语句；目标已有值不覆盖，事务内逐字段 SHA256 和行数相同才提交。续跑先重新核验已完成目标的哈希，发现变化停止。

全部证券及行情校验完成后，复制辅助状态；年度覆盖与空扫描标记只保留起始年份及之后的数据。最终设置 `ready=1`，此前应用拒绝接入。数据库重导完成后仍需单独验证业务读取、部署并恢复更新。该模式不自动清理临时只读实例、克隆或原恢复点，也不承诺特定导入速度。

## 旧混合表的兼容处理（迁移前参考）

代码只对新建表采用二进制排序规则，不会在启动时自动 ALTER 已有大表。切换与历史迁移不执行已有表的排序规则调整。

已有 `utf8mb4_unicode_ci` 表的 A 股小写代码可以继续使用；在导入大小写有意义的期货代码或自定义市场前，需要先检查并调整相关键列。若过去已经因为不区分大小写合并了两份数据，仅改排序规则不能恢复，必须从原始数据补回。

先检查实际表与列：

```sql
SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, COLLATION_NAME
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE()
  AND (TABLE_NAME IN ('kline_day', 'minute_coverage', 'minute_empty_scan', 'update_marks')
       OR TABLE_NAME REGEXP '^kline_minute_[0-9]{4}$')
  AND COLUMN_NAME IN ('market', 'code', 'mark_key');
```

下面查询仅生成调整语句，不执行；在备份、核对磁盘空间和维护窗口后逐表执行，分钟表可能需要重建索引：

```sql
SELECT CONCAT('ALTER TABLE `', TABLE_NAME,
              '` MODIFY `market` VARCHAR(16) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,',
              ' MODIFY `code` VARCHAR(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL;') AS proposed_sql
FROM information_schema.TABLES
WHERE TABLE_SCHEMA = DATABASE()
  AND (TABLE_NAME IN ('kline_day', 'minute_coverage', 'minute_empty_scan')
       OR TABLE_NAME REGEXP '^kline_minute_[0-9]{4}$');
```

`update_marks.mark_key` 如有区分大小写的命名空间，也需单独调整为 `VARCHAR(191) ... COLLATE utf8mb4_bin NOT NULL`。调整前后应核对行数、代码数量、来源标记、固定证券范围查询与执行计划；不要直接对所有分钟表并行 ALTER。

本轮修复可以保留今后写入的 `source`，无法凭空恢复先前遗漏的标记；源时间 SQLite 仍存在时，应据源文件做专门恢复，普通“只补缺失”迁移不会修改已有空标记。

## 配置与验证边界

正式入口必须配置 MySQL，仓库中的 `pull.mysql` 留空。推荐把真实连接放在运行根目录下的私有文件 `config/mysql.yaml`：

```yaml
pull:
  mysql: "<MySQL DSN>"
```

该文件已从 Git 和镜像构建上下文排除，建议权限为 `0600`；部署时通过只读 `config` 挂载加载。连接优先级为显式环境变量 `pull.mysql` → `config/mysql.yaml` → 公共 `config/config.yaml`。显式空环境值也会报错，不会退回其他连接或 SQLite；其他配置保持原有公共文件优先规则。

单次覆盖示例：

```sh
env 'pull.mysql=<MySQL DSN>' go run ./cmd/update
```

三个迁移命令使用相同连接解析，不初始化 TDX 管理器；`-dsn` 可单独覆盖目标。不要把真实凭据写入受版本管理的配置文件或命令记录。应用账号按当前业务配置权限；Store 初始化会创建尚不存在的表，需要相应权限。

运行日志和 `/api/data-update/status.storage` 返回不含凭据的 `mysql://host/database`。部署脚本验证此字段后才判定切换成功；连接或状态读取失败会报错。旧 SQLite 文件保留，MySQL 更新不再修改其中的行情和完成标记。

固定输入对照使用 tdx `6e71554` 中未改动的 SQLite writer 作为稳定基线。测试覆盖空起点全量覆盖、来源字段、重复导入、中间缺口、已有更新尾部、事务回滚和大小写隔离。临时 MySQL 测试不能代替实际行情库的全量一致性与策略回测对照，也不能证明现有大表的性能。

## SSD 实例重新导入（2026-11-06）

用户选择在新设备重新导入，旧库导入进度不参与新任务计数。当前目标为 `192.168.1.103:3306/market_data`，MySQL 8.0.46，Docker 数据目录绑定至新设备本地 NVMe 的 `/vol1/1000/mysql/data`。私有连接仍由 `config/mysql.yaml` 提供，不在脚本参数、日志或报告中保存凭据。

首次重新导入运行在原 NAS，通过网络写入新 MySQL。2026-11-07 用户将股票源复制到 103 的 `/vol1/1000/临时/kline241(2000-2025)`，并要求先只完成股票分钟线；当前股票任务已迁到 103 本机，控制目录为 `/home/admin/strategy-tail-local-import-20261107`，历史范围仍为 2018–2025 年。原 NAS 的股票和后续队列保持暂停。指数、日线按用户说明已在 103，本次没有搜索、复制或启动。

复制后的本轮 36,560 个年度库全部通过既有源 SHA256 基线校验。保留 MySQL 中已核验的 59,264,551 条记录，目标 UUID 一致后，将成功文件回执按相对路径映射到新目录，并绑定到同实例的 `127.0.0.1:3306`；续跑参数只使用成功单文件 JSONL，不含字段类型不同的汇总行。写入和读回校验沿用上述导入契约。本机任务使用 4 并发、GOMAXPROCS=4、GOMEMLIMIT=1GiB，进度分开记录旧基数和新写入，不重复计数。

新实例已持久化 `innodb_buffer_pool_size=6442450944`（6 GiB）。实际核验 `innodb_flush_neighbors=0`、`innodb_doublewrite=ON`、`innodb_flush_log_at_trx_commit=2`；本次没有改动后三项。初始化精确核对新地址、UUID、MySQL 版本及空目标，仅建立 `market-v1/ready=0` 标记，不自动开放业务读取。股票、指数、日线分别验收，之后仍需完成数据覆盖及应用验收。

查看新任务实时进度：

```sh
ssh admin@192.168.1.103 'python3 /home/admin/strategy-tail-local-import-20261107/progress.py'
```

启动初期吞吐包含新缓存效应，不据此承诺持续速度或完成时间。下节机械盘参数仅为旧实例的部署记录，不应直接套用到新 SSD 实例。

2026 股票后续队列位于 103 的 `/home/admin/strategy-tail-stock-2026-20261107`，状态为 `status.json`。它只在上述历史任务退出码 0、完整唯一文件集合及源元数据验收通过后启动；历史暂停或失败不会启动 2026。启动时只读复制 2026 源到独立的本机私有快照，逐文件复读原源和校验副本 SHA256，拒绝活动 WAL/journal 及复制中变化。复用已测试的 `Kline` DATETIME／整数毫转换器，UTC 文本按东八区还原分钟，股票成交量手转股；只补缺失分钟、完整读回新增记录，不覆盖既有值。不对新 2026 源自动应用旧异常隔离规则，出现异常时停止待核查。

历史及后续 2026 在同一进度命令中分段显示（`next2026`），不把 2026 写入量混进历史范围。2026 仅在所有快照文件、年份、源哈希、读回状态、退出码及快照元数据通过后验收；不会自动开放业务读取或启动指数、日线。

2026-11-07 股票分钟导入已完成：2018–2025 共 36,560 个年度文件、2,061,733,434 条；2026 共 5,623 个文件、270,146,838 条，最新分钟为 `2026-11-06 15:00`。2026 曾因 Btrfs 分配空间耗尽导致 MySQL 异常退出；恢复后核对同一实例 UUID、普通恢复模式和可写状态，只为剩余 65 个文件重建哈希锁定的快照，补写并读回校验 2,569,582 条，约 18.31 秒，退出码 0。原成功回执和故障记录保留；续跑控制目录 `/home/admin/strategy-tail-stock-2026-resume-20261107`，合并验收位于原 2026 控制目录的 `acceptance.json`，进度入口已合并两轮回执并按代码/年份去重。业务 `ready` 仍为 0，指数、日线尚未启动。

本次 Btrfs 故障中，`df` 所示空闲包含已分配数据块组内部的空洞，不能直接供元数据块组分配。经受限低利用率数据整理，未分配空间由约 1MiB 增至约 30GiB；MySQL 正常启动恢复过程中元数据容量实际由 24.5GiB 自动增至 25.5GiB。续跑后未分配空间约 29GiB、元数据使用率约 94.14%。后续大批量导入应同时核对元数据和设备未分配空间，不能仅依赖 `df` 的空闲容量，也不能将本次余量视为长期不会耗尽的保证。

### 股票和 ETF 全历史日线（2026-11-07）

用户提供 103 的 `/vol1/1000/server/pull-day-kline/data/database/kline`，日线范围为源库上市以来的全部历史，显式使用 `-start-year 0`，不沿用分钟线的 2018 年起点。目录共 7,773 个 SQLite 库，股票 5,618 个、ETF/LOF 2,155 个，约 1.19GiB；代码范围经现有 SDK 的股票和基金规则核对，不包含指数任务。

这批库使用 `DayKline(Date,Last,Open,High,Low,Close,Volume,Amount,InDate)`，Date 为 Unix 秒，价格和金额为整数毫，成交量为手。控制脚本只读复制源、复读 SHA256 并拒绝活动日志，将私有副本中的 DayKline 映射为现有日线导入器的 Kline 输入；原值、最早日期和完整日期集合保留，原始周/月/季/年表不作为日线输入。源库没有换手率、流通股本和总股本，这些字段沿用现有导入器的零值，不从其他来源补造；已有 MySQL 日期及其字段保持不变。新增行完整读回校验，转换后的输入和原始快照在结束后再次校验哈希。

控制目录 `/home/admin/strategy-tail-day-all-20261107`；私有原始快照及转换副本位于 `/vol1/1000/临时/.day-all-stage-20261107/{raw,converted}`。导入前检查同一实例 UUID、普通恢复模式、可写状态、ready=0、Btrfs 未分配空间及元数据余量，4 并发、GOMEMLIMIT=1GiB。凭据只经 stdin。进度区分快照转换与实际 MySQL 写入：

```sh
ssh admin@192.168.1.103 'python3 /home/admin/strategy-tail-day-all-20261107/progress.py'
```

快照、源范围及全部唯一代码回执、读回、退出码和冻结输入的哈希通过后，才以 `acceptance.json` 的 complete=true 判定本批日线完成；没有用源库之外的上市日资料补齐或证明源本身不存在历史缺口，业务读取仍需单独验收。

2026-11-07 13:04:25 CST，源内全部现有日线通过验收：股票 17,164,040 条、ETF/LOF 2,191,802 条，合计 19,355,842 条，日期范围 1990-12-19 至 2026-11-06；start-year=0、退出码 0、源快照未变、新增行读回一致，实际导入耗时约 214.66 秒。7,773 个文件中有数据的为 7,503 个，270 个 DayKline 空表（按文件名前缀归类为股票 14、基金 256）作为零行输入处理，不能因此宣称这些代码都具备完整上市历史。

用户授权继续排查并尝试补齐空库。已对全部 270 个代码从两台不同通达信服务器、东方财富未复权全历史日线接口交叉查询；已知股票和 ETF 的全历史基准查询正常，但这 270 个代码均未返回可补日线，未写入外部数据。已有 2026-03-11 备份中仅找到对应的 8 个文件，也都为空。补源尝试完成，新增为 0；51 个代码仍出现在最新行情代码表，单凭代码表存在不能推断已有交易历史。186 个空代码与已有非空交易代码存在末位 3/4 对应关系，仅标注为认购候选，不批量断言属性；510143 的认购属性另有基金管理人正式公告依据。

逐代码核查和来源见控制目录 `empty-source-review.json`；原批次验收另存 `acceptance-source-import.json`，合并验收增加 emptySourceFiles、supplementalAttemptComplete、supplementalAdded、allCodeHistoriesVerified，区分“源内现有行导入完成”和“每个代码的上市历史已获验证”。270 个空代码历史仍未补齐，未生成零值占位日线，未修改或删除原源；业务 ready 继续为 0。

### 指数与 ETF/LOF 分钟源（2026-11-07）

本轮指数源 `/vol1/1000/server/pull-index-minute-kline/data/database/kline` 按目录内全部年份导入：2000–2026 年、8,251 个年度库，源 `KlineMinute1` 共 459,170,351 行，没有 2018 年下限。近期 ETF/LOF 源 `/vol1/1000/server/pull-etf-minute-kline/data/database/kline` 当前只有 2025、2026 年目录，4,228 个库、212,946,679 行，其中 235 个一分钟表为空；额外历史源见下节，不把近期目录视为已覆盖更早年份。

两批使用 `KlineMinute1` 的 REAL 原币种价格/金额及 Unix 秒时间。指数成交量保持源口径，ETF/LOF 成交量由手乘 100 转股；不导入同文件中其他分钟周期表，也不生成源中不存在的分钟。ETF 有 1,513 个文件的记录跨越目录年份，例如 `2026/sz159915.db` 包含从 2025-08-07 开始的分钟，必须按实际记录年份分表，保留这些旧分钟。用户确认重叠数据以 2026 文件为准；基金文件按代码分组，同一组由一个 worker 按目录年份降序逐个处理，先 2026 再 2025，不同代码仍可并行。源内部按该顺序确定首次写入值，较旧文件只补缺失键；目标已有值及其 source 字段通常保持原样，差异记为 preservedConflicts。此排序不会自动改写以前已经导入的旧值，改变优先级后需先核对已写入区间，按授权单独修正差异。

复用 `cmd/migrate-index-minute`：默认 `-market cn/index`；ETF 使用 `-market cn/etf`。`-start-year 0` 选择全部源年度目录，`-year` 选择源目录年份；ETF 文件内的跨年记录仍按实际年份写入。原始指数代码如 sh999999、bj899050 保留，不重命名到别的代码。指数源继续严格核对目录年份与原始年度字段，两类源均校验 Unix 秒与年月日时分字段一致、非负有限数值及成交量溢出；每个文件先复制至本机临时快照并复读源哈希，再完整校验新增与既有区间。成功 JSONL 回执新增 market 和 actualYears，续跑要求目标、市场和源哈希相符；旧无 market 字段回执仅适用于原指数模式。错误或 SIGINT 停止派发后续文件，当前文件结束后保留已验证回执。

控制目录 `/home/admin/strategy-tail-index-etf-minute-20261107`，按指数后 ETF 顺序运行，4 并发、GOMEMLIMIT=1GiB。启动前验证同一 UUID、普通可写模式、ready=0 及至少 16GiB 未分配空间；运行中每 5 秒检查分配余量，低于 8GiB或元数据普通余量持续过低时，通过 SIGINT 在文件检查点停止，不自动扩大 balance 范围、不自动改配置。状态、源扫描和新增写入吞吐分开显示：

```sh
ssh admin@192.168.1.103 'python3 /home/admin/strategy-tail-index-etf-minute-20261107/progress.py'
```

完整市场/文件集合、源行数、actualYears、SHA256、读回和退出码核对通过后，才写对应 `index-acceptance.json`、`etf-acceptance.json` 和合并 `acceptance.json`；指数失败或暂停则不会启动 ETF。私有连接经 stdin，不进入脚本参数、回执或配置副本。业务 ready 仍为 0。本轮导入器的跨年与单位转换、重复日期、溢出拒绝、目标旧值及新尾部保留、重复导入已通过隔离 MySQL 的 race/集成测试；实际 2000 年指数和 2025/2026 混合 ETF 文件预演通过。

14:10 左右触发未分配空间低于 8GiB 的保护，指数在 4,113 个成功文件、229,324,031 条的已验证断点停止，没有导入错误，MySQL 仍正常可查询。管理员随后通过受限低利用率数据整理将未分配空间增至约 50GiB。独立续跑目录为 `/home/admin/strategy-tail-index-etf-minute-resume-20261107`，先验证同实例、两类源元数据不变及成功回执，再使用 `-resume-log` 复核已完成文件的源哈希，旧数据不重写；原暂停记录保留。原进度命令经 resume-link.json 指向新任务，累计基数和本轮新增分开计数，4,113 份哈希跳过回执的覆盖信息由既有成功回执恢复，不将零新增误计为零覆盖，也不把哈希扫描计入新增写入速度。指数验收通过后仍自动接着 ETF，8GiB 保护线保持生效。

15:18 元数据普通余量连续三次低于 16MiB，再次按文件检查点暂停；当时设备未分配空间仍约 48GiB，7,662 个文件、426,400,511 行已核验，没有导入或回执校验错误。暂停后元数据临时占用释放，既有算法计算的普通余量恢复至约 510MiB。核对 MySQL 普通可写、实例 UUID 和源元数据后，于 `/home/admin/strategy-tail-index-etf-minute-resume2-20261107` 续跑，并发降为 2，保护阈值保持不变。原进度入口仍跟随最新控制目录；此前各轮回执和故障记录保留。Btrfs 内部预留与 pinned 计数会随负载快速变化，不能仅用一次使用率或 `df` 判断可写余量，参见 [Btrfs 管理文档](https://btrfs.readthedocs.io/en/stable/Administration.html#uuid-allocations-data-metadata-system)。

指数随后完成全部 8,251 个文件、459,170,351 行，全部源元数据、逐文件读回和退出码验收通过。近期 ETF 曾在 193 个成功文件后因同代码跨年文件并发触发校验错误：2025/sh510020 与 2026/sh510020 的重叠分钟不同，后者已完整读回通过。用户确认优先 2026 后，核对已派发的 194 份回执和源文件，已成功写入的 2026 文件保留冲突数均为 0；暂停边界上仅 sh510030 已导入 2025 而尚未处理 2026，其重叠区间有 2 条差异。先记录并持久化目标旧值，再在带行锁的事务内验证旧值、定点更新这 2 条、完整读回，提交后保留前后像与源 SHA256。没有批量覆盖其他已有分钟。

优先级续跑控制目录为 `/home/admin/strategy-tail-etf-priority-20261107`，原进度入口跟随该目录。近期 ETF 不复用旧规则的跳过回执，重新读取全部 4,228 个文件并校验目标；起点已有 8,754,761 行（含前次失败文件已提交的部分），累计基数与本轮新增单列。指数已完成结果保留，不重写。历史等待队列已在 `/home/admin/strategy-tail-fund-history-resume-20261107` 重新启动，原历史进度入口经 resume-link.json 跟随；适配新前序目录名称，仍严格要求指数与近期 ETF 全部验收，使用同一版按代码分组的导入器。修复已通过隔离 MySQL 的 race/集成测试，覆盖 2026 重叠优先、2025 独有分钟补齐、修正备份、幂等与异常目标拒绝；空间保护和 ready=0 保持不变。

### 历史 ETF SQLite 与 LOF CSV（2026-11-07）

用户补充的历史 ETF 根目录为 `/vol1/1000/临时/kline(ETF)/kline`，包含 5,707 个年度 SQLite 文件、约 22.05GiB，目录年份 2005–2025；只导入其中的 `KlineMinute1`。历史 LOF 根目录为 `/vol1/1000/临时/csv(LOF)(2000-2025)/1分钟`，包含 589 个 CSV、约 8.70GiB，实际日期范围以全量预检为准，不从目录名推定 2000 年起已有记录。两批均不设 2018 年下限，不写其他分钟周期。

用户已确认 LOF CSV 为未复权原始价格。对 6 个代码、36 个跨年交易日与通达信原始日线收盘价交叉核对，34 处在 0.0011 元内一致，2 处不同：`sz160105` 的 2004-12-20 为 CSV 0.980 / 日线 0.988，2004-12-21 为 CSV 0.976 / 日线 0.980。差异原因尚未确认，记录在核对报告中；按已确认的源口径保留 CSV 原值，不以日线价格覆盖，也不声称抽样全部一致。

历史队列控制目录 `/home/admin/strategy-tail-fund-history-20261107`。先后台只读快照预检全部文件、验证格式和源 SHA256，再等待当前指数及近期 ETF 的全部文件、源元数据、读回结果和退出码验收通过，之后依次历史 ETF、历史 LOF。历史预检或前序任务失败时不启动相应写入；前序暂停时保持等待。LOF 额外要求已记录用户确认的未复权口径。2025 年与近期 ETF 重叠只补缺失键，目标已有值和来源字段不覆盖。

历史每阶段开始需至少 16GiB 设备未分配空间及按现有算法至少 256MiB 普通元数据余量；条件不足时等待。运行中沿用低于 8GiB 或普通元数据余量持续过低的保护，SIGINT 完成本文件后停止。增加历史数据后仍可能再次触发保护，约 50GiB 的未分配池不代表全部历史必然足够。队列不执行 balance、不修改 MySQL 配置或业务 ready。暂停历史预检/等待队列时仍需停止其观察进程并记录暂停请求。

```sh
ssh admin@192.168.1.103 'python3 /home/admin/strategy-tail-fund-history-20261107/progress.py'
```

CSV 解析、单位和逐年处理、旧值/source/新尾部保留、重复导入以及后部坏行在写入前拒绝，已通过独立临时 MySQL 的 race/集成测试。测试不等同于这批实际历史已经导入完成；实际完成以各阶段 acceptance.json 为准。

## NAS 机械盘实例运行参数

2026-11-05，当前约 15 GiB 内存的 NAS 实例已调整并持久化以下参数；这是该实例的部署值，不是所有部署的默认配置：

| 参数 | 当前值 | 用途 |
|---|---:|---|
| `innodb_buffer_pool_size` | `6442450944`（6 GiB） | 为历史导入和行情查询提供缓冲页；与 NAS 其他服务共用内存，新增服务后应重新评估。 |
| `innodb_flush_neighbors` | `1` | 在机械盘上合并同一区内相邻脏页的刷新，减少寻道；迁往 SSD 时重新评估。 |
| `innodb_doublewrite_pages` | `128` | 将双写批次从 4 页增大；该变量必须重启生效，双写保护仍为 `ON`。 |

前两项通过 `SET PERSIST` 在线调整；双写批次通过 `SET PERSIST_ONLY` 保存，正常重启后于 18:50 核验实际值为 128。三项均保存在数据目录的 `mysqld-auto.cnf`，未修改 Compose。持久化变量会参与后续启动，调整 Compose 参数时也应检查它们；使用 SQL 管理，不手改该文件。核对当前值与保存值：

```sql
SELECT @@global.innodb_buffer_pool_size, @@global.innodb_flush_neighbors,
       @@global.innodb_doublewrite_pages, @@global.innodb_doublewrite,
       @@global.persisted_globals_load;
SELECT VARIABLE_NAME, VARIABLE_VALUE
FROM performance_schema.persisted_variables
WHERE VARIABLE_NAME IN ('innodb_buffer_pool_size', 'innodb_flush_neighbors', 'innodb_doublewrite_pages');
```

前两项原运行值为 1.5 GiB / `0`，原保存值只有缓冲池 1 GiB；运行值与保存值需分别恢复。仅回退前两项的语句如下，后续再次调参后应使用新的原值记录：

```sql
SET PERSIST_ONLY innodb_buffer_pool_size=1073741824;
RESET PERSIST IF EXISTS innodb_flush_neighbors;
SET GLOBAL innodb_buffer_pool_size=1610612736;
SET GLOBAL innodb_flush_neighbors=0;
```

双写批次原运行值为 4，原来没有持久化覆盖；需回退时执行 `RESET PERSIST IF EXISTS innodb_doublewrite_pages`，再经维护授权正常重启并核验恢复为 4。本次双写保持 `ON`，事务日志与 binlog 刷盘策略未变。

本次正常关闭耗时约 54 分钟，日志确认 `Shutdown complete`，没有强制结束；后续维护也应等待正常刷盘退出。128 页的持续性能仍需实测，不能仅凭启动成功判断提速。性能验收需区分源库核验、实际新增写入和并行日常更新，并观察缓存填充后的持续吞吐；增加缓冲池不能替代磁盘持续写入能力。

参考：[缓冲池刷盘](https://dev.mysql.com/doc/refman/8.0/en/innodb-buffer-pool-flushing.html)、[持久化系统变量](https://dev.mysql.com/doc/refman/8.0/en/persisted-system-variables.html)。
