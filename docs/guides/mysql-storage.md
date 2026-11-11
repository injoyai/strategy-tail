# MySQL 数据源

Lab、选股和回测使用同一 MySQL 数据库，行情和研究数据由外部项目维护。连接账号只需查询权限，应用只执行查询。

## 连接配置

在运行时根目录的私有 `config/mysql.yaml` 中设置 `pull.mysql`，或通过同名环境变量覆盖。文件通过只读配置卷提供给容器。

```yaml
pull:
  mysql: 'reader:<password>@tcp(<host>:3306)/<database>'
```

优先级为环境变量 `pull.mysql`、私有配置、公共配置。缺少连接配置或连接失败时，命令启动报错。日志和 Lab `GET /api/market-data/status` 使用 `mysql://host/database` 标识数据源，行情状态显示上证指数已入库的最新日线日期。

## 行情与证券表

| 表 | 读取内容 |
| --- | --- |
| `<地区>_<资产>_kline_day` | `code, time, open, high, low, close, volume, amount`，按 `(code, time)` 排序，`time` 为 `DATE`。例如 `cn_stock_kline_day`、`cn_index_kline_day`。 |
| `<地区>_<资产>_kline_minute_<年份>` | 日线字段和 `source`，`time` 为 `DATETIME`。按源年份分表，例如 `cn_stock_kline_minute_2026`。 |
| `codes` | `exchange, code, name, multiple, decimal, last_price`。缺少代码目录时，选股使用库存日线的股票代码。 |
| `gbbq` | `id, code, time, category, c1, c2, c3, c4`。策略按交易日匹配股本事件并计算换手率。 |

日线、分钟线的时间与单位见 [源时间与策略口径](source-time-storage.md)。代码目录、股本事件和历史成员在打开读取器时载入。选股在交易时间默认每分钟查询一次 MySQL，把当日已入库的分钟线汇总成盘中 K 线，信号时间取该股票最新入库分钟。盘中新鲜度取决于外部项目的写入频率。

## 研究数据

下列两张表由数据维护方按研究需求提供。行情回测可单独运行。

| 表 | 读取契约 |
| --- | --- |
| `research_records` | `dataset, code, record_key, source, revision, event_at, available_at, values_json, attributes_json`。时间字段为 Unix 纳秒 `BIGINT`，两个 JSON 字段分别保存数值和文本属性。建议为 `(dataset, code, event_at, available_at, record_key)` 建查询索引。缺表时历史估值等研究字段显示缺数。 |
| `universe_members` | 每个代码一行，包含 `code, listed_at, delisted_at, available_at`。上市和退市日期使用 `DATE`，仍在市时 `delisted_at` 为 `NULL`。`available_at` 是记录可知时间，使用正数 Unix 纳秒 `BIGINT`。缺表或空表时使用 MySQL 股票日线覆盖集合，成员历史标记为 `unverified`。 |

`research_records` 只返回 `event_at` 和 `available_at` 都不晚于信号时点的记录。历史成员按上市到退市的日期闭区间判断，`available_at` 保留来源时间，成员关系在缺少经过验证的版本历史时标记为 `unverified`。

SQLite 的 `data/database/trade.db` 保存选股生成的买卖点。Lab 的策略、练习和报告使用本地持久状态。
