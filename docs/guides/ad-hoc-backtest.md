# 临时策略回测

## 选择入口

行情和研究数据在项目外维护。临时回测读取项目配置指向的存量数据，缺数时报告缺口，按有证据的范围运行。使用已有 Lab 时，先确认它指向同一数据集且自动更新已关闭。项目规则见 [§6.9](../../PROJECT_RULES.md#69-项目数据只读与临时回测)。

| 用途 | 入口与口径 |
| --- | --- |
| 日期、分钟和证券范围 | 日线日期闭区间，零值端点为无界。分钟线按 `start` 到 `end` 所在年份整年读取，非整年实验在调用方按东八区日期和时间再切分。代码带市场前缀，例如 `sh600000`、`sh000300`。股本字段取自已入库日线。`LocalStockCodes()` 是有日线的库存代码集合，包含库中保留的退市股票，成员资格历史需另查。 |
| 历史股票池与非行情字段 | 本地成员文件可通过 `researchdata.LoadUniverseFile(common.UniverseFile(), cfg, strict)` 加载，核对 `Universe.Snapshot()` 及按日期的 `Codes`。已有 Lab 的 `researchdata.View` 可用 `Latest`、`Range` 按 `AsOf` 查询本地研究数据。`OpenSQLiteStore` 会初始化库，直接读取时使用已有视图。见[股票池契约](../../researchdata/universe.go)和[研究数据契约](../../researchdata/contract.go)。 |
| 逐标的或多方案回测 | 将策略声明为 `researchrun.Variant`，把项目的只读行情读取函数接到 `researchrun.Config`，调用 `Run` 并检查 `Coverage`。取数或运行自定义模型可用 `ForEachCodeData` 或 `ForEachCodeYearData`，回调由多个 worker 并发执行。见[执行器](../../internal/researchrun/runner.go)。 |
| 账户回测 | 已有 Lab 可用 `POST /api/builder/run` 或 `POST /api/strategy/run` 运行可表达的策略，请求带 `account` 资金参数。通过 `GET /api/status` 确认成功后，从 `GET /api/report/latest` 获取报告，核对 ID 是本次新增且配置相符，再用 `GET /api/report/{id}` 定位。可用 `GET /api/kline/{code}?from=YYYY-MM-DD&to=YYYY-MM-DD` 检查日线。见[回测路由](../../internal/lab/server.go)和[账户参数](../../internal/lab/account_backtest.go)。 |

`researchrun.Run` 的多年份输入按整只标的统计覆盖，任一年加载失败会排除该标的，各年分别计算持仓。`DailyClose` 只用日线。`Intraday` 加载分钟线供卖出，买入仍用日线收盘；现有引擎在某天缺有效分钟线时会用日线判断卖出。要求严格分钟执行时，先用日线交易日期核对有效分钟覆盖，按策略要求排除或明确标记不完整的样本。

## 建立临时目录

在仓库根目录为一次性代码、日志、探针和中间数据建立独立目录。

```sh
mkdir -p .tmp/backtest
scratch=$(mktemp -d "$PWD/.tmp/backtest/ma-check.XXXXXX")
```

将 Go 入口保存为 `$scratch/main.go` 后，从仓库根目录运行 `TZ=Asia/Shanghai go run "$scratch/main.go"`。

## 交付格式

每次使用新的 `output/backtest/<主题>/<run-id>/`，同一次运行的对照策略放在一起。`run-id` 可直接使用本次临时目录名。回测结束后可按任务清理 `.tmp/backtest/<run-id>/`，保留报告和导出文件。

| 文件 | 内容 |
| --- | --- |
| `report.md` | 必须提供。依次写明结论、策略规则与参数、数据与覆盖、成交与成本、对照结果、限制及复现。缺数无法完成时记录已确认的覆盖和阻塞点。 |
| `summary.csv` | 有可比较结果时提供，表头为 `variant,period,metric,value,unit`。`period` 用日期范围或 `overall`，数值用纯数字，单位另列。不可计算的值留空并在报告说明。 |
| `trades.csv` | 有逐笔成交时导出或链接现有明细。至少可核对策略、标的、买卖时间、成交价、数量、成本、净盈亏和期末估值标记。 |
| `equity.csv` | 有账户模拟时导出或链接现有逐日净值。至少可核对策略、交易日、现金、持仓市值和总权益。 |

- 数据部分列明数据集和截止日、请求与实际起止日期、标的或股票池、频率、价格及复权口径、预热、覆盖和失败。分钟实验另列覆盖的交易日及缺失天数。估值和分红使用当时已可得的历史记录，记录缺失或修订可验证性。日志和产物省略 DSN、账号和密码。
- 成交部分写明信号时间、成交时间和价格、T+1、滑点、费用、期末持仓以及适用的资金与持仓约束。基准采用项目内同区间数据并标明价格收益或含分红收益。比较组统一使用相同样本与成本口径，例外单独说明。
- `metric` 使用稳定字段名。逐笔结果至少提供 `closed_trades`（笔）、`win_rate`（%）、`avg_trade_return`（%）、`profit_factor`（倍）和 `open_trades`（笔）。账户结果提供 `initial_capital`（元）、`ending_equity`（元）、`net_return`（%）、`max_drawdown`（%）和 `benchmark_return`（%）。回撤填正数幅度。盈亏比为无穷时 CSV 留空，报告说明原因。只有可比较的账户净值才计算账户收益与回撤。
- 复现部分记录代码版本、运行命令和完整参数，并用规则表达式或伪代码保留自定义信号与成交逻辑。最终回复给出实测区间、覆盖、核心结果及报告路径。
