# 临时策略回测与只读数据接口

## 选择入口

行情和研究数据在项目外维护。临时回测读取项目配置指向的存量数据，缺数时报告缺口，按有证据的范围运行。使用已有 Lab 时，先确认它指向同一数据集。项目规则见 [§6.9](../../PROJECT_RULES.md#69-项目数据只读与临时回测)。

| 用途 | 入口与口径 |
| --- | --- |
| 读取项目行情 | 根包 `common.OpenResearchMarket()` 复用 `pull.mysql`。返回的读取器提供 `DayKlines(code, start, end)`、`MinKlines(code, start, end)`、`LocalStockCodes()`、`StorageIdentity()` 和 `Close()`。打开连接及查询只访问 MySQL 已有表。见[入口源码](../../research_market.go)和[读取器](../../lib/extend/market_reader.go)。 |
| 日期、分钟和证券范围 | 日线日期闭区间，零值端点为无界。`MinKlines` 读取端点所在年份的全年分钟线。`MinuteKlines` 按东八区时分查询指定区间。代码带市场前缀，例如 `sh600000`、`sh000300`。股本按交易日取自 `gbbq`。`LocalStockCodes()` 枚举有日线的库存代码，包含库中保留的退市股票。 |
| 历史股票池与非行情字段 | `market.MembershipUniverse()` 读取 MySQL 历史成员，核对 `Universe.Snapshot()` 和按日期的 `Codes`。`market.ResearchView()` 提供 `Latest`、`Range` 按 `AsOf` 查询 MySQL 研究数据。所需字段见 [MySQL 数据源](mysql-storage.md#研究数据)。 |
| 逐标的或多方案回测 | 将策略声明为 `researchrun.Variant`，把读取器方法接到 `researchrun.Config`，调用 `Run` 并检查 `Coverage`。取数或运行自定义模型可用 `ForEachCodeData` 或 `ForEachCodeYearData`，回调由多个 worker 并发执行。见[执行器](../../internal/researchrun/runner.go)。 |
| 账户回测 | 已有 Lab 可用 `POST /api/builder/run` 或 `POST /api/strategy/run` 运行可表达的策略，请求带 `account` 资金参数。通过 `GET /api/status` 确认成功后，从 `GET /api/report/latest` 获取报告，核对 ID 是本次新增且配置相符，再用 `GET /api/report/{id}` 定位。可用 `GET /api/kline/{code}?from=YYYY-MM-DD&to=YYYY-MM-DD` 检查日线。见[回测路由](../../internal/lab/server.go)和[账户参数](../../internal/lab/account_backtest.go)。 |

`researchrun.Run` 的多年份输入按整只标的统计覆盖，任一年加载失败会排除该标的，各年分别计算持仓。`DailyClose` 只用日线。`Intraday` 加载分钟线供卖出，买入仍用日线收盘；现有引擎在某天缺有效分钟线时会用日线判断卖出。要求严格分钟执行时，先用日线交易日期核对有效分钟覆盖，按策略要求排除或明确标记不完整的样本。

## 最小 Go 示例

在仓库根目录建立独立工作目录，将示例保存为 `$scratch/main.go`。日志、数据探针和缓存也可放在同一目录。

```sh
mkdir -p .tmp/backtest
scratch=$(mktemp -d "$PWD/.tmp/backtest/ma-check.XXXXXX")
```

```go
package main

import (
	"context"
	"fmt"
	"log"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	market, err := common.OpenResearchMarket()
	if err != nil {
		return err
	}
	defer market.Close()

	cost, position := common.LoadBacktestConfig()
	report, err := researchrun.Run(context.Background(), researchrun.Config{
		Codes: []string{"sh600000"},
		Years: []int{2025},
		Variants: []researchrun.Variant{{
			Name:   "收盘高于昨收买入，连续三日跌破20日线卖出",
			Buyer:  buy.A收盘大于昨收{},
			Seller: sell.A收盘跌破均线{Period: 20, Days: 3},
		}},
		Cost:         cost,
		Position:     position,
		DataMode:     researchrun.DailyClose,
		GetDayKlines: market.DayKlines,
	})
	if err != nil {
		return err
	}
	fmt.Printf("数据覆盖 %+v\n", report.Coverage)
	if report.Coverage.Completed == 0 {
		return fmt.Errorf("无可用行情样本")
	}
	for _, result := range report.Results {
		closed := make([]core.Trade, 0, len(result.Trades))
		for _, trade := range result.Trades {
			if !trade.Virtual {
				closed = append(closed, trade)
			}
		}
		fmt.Printf("%s %+v，期末未平仓 %d 笔\n", result.Variant.Name,
			core.Stats(closed), len(result.Trades)-len(closed))
	}
	return nil
}
```

从仓库根目录运行 `TZ=Asia/Shanghai go run "$scratch/main.go"`。分钟卖出同时指定 `DataMode: researchrun.Intraday` 和 `GetMinKlines: market.MinKlines`。正式实验按下述格式落盘。

## 交付格式

每次使用新的 `output/backtest/<主题>/<run-id>/`，同一次运行的对照策略放在一起。`run-id` 可直接使用本次临时目录名。回测结束后可按任务清理 `.tmp/backtest/<run-id>/`，保留报告和导出文件。

| 文件 | 内容 |
| --- | --- |
| `report.md` | 用公共接口发布，图表嵌入正文。写明结论、策略规则与参数、数据与覆盖、成交与成本、对照结果、限制及复现。缺数无法完成时记录已确认的覆盖和阻塞点。 |
| `backtest-chart-*.svg` | 公共接口生成的图表，与报告一同交付。 |
| `summary.csv` | 有可比较结果时提供，表头为 `variant,period,metric,value,unit`。`period` 用日期范围或 `overall`，数值用纯数字，单位另列。不可计算的值留空并在报告说明。 |
| `trades.csv` | 有逐笔成交时导出。出图读取 `variant,virtual,net_return_pct` 和 `sell_time`（也接受 `sell_or_mark_time`），有 `code` 时显示在图上的成交点提示中。明细还应提供买卖价格、数量、成本和净盈亏。 |
| `equity.csv` | 有账户模拟时导出。出图读取 `variant,date,equity` 和可选的 `benchmark_equity`。明细还应提供现金和持仓市值，权益和基准权益单位为元。 |

- 数据部分列明数据集和截止日、请求与实际起止日期、标的或股票池、频率、价格及复权口径、预热、覆盖和失败。分钟实验另列覆盖的交易日及缺失天数。估值和分红使用当时已可得的历史记录，记录缺失或修订可验证性。日志和产物省略 DSN、账号和密码。
- 成交部分写明信号时间、成交时间和价格、T+1、滑点、费用、期末持仓以及适用的资金与持仓约束。基准采用项目内同区间数据并标明价格收益或含分红收益。比较组统一使用相同样本与成本口径，例外单独说明。
- `metric` 使用稳定字段名。逐笔结果至少提供 `closed_trades`（笔）、`win_rate`（%）、`avg_trade_return`（%）、`profit_factor`（倍）和 `open_trades`（笔）。账户结果提供 `initial_capital`（元）、`ending_equity`（元）、`net_return`（%）、`max_drawdown`（%）和 `benchmark_return`（%）。回撤填正数幅度。盈亏比为无穷时 CSV 留空，报告说明原因。只有可比较的账户净值才计算账户收益与回撤。
- 复现部分记录代码版本、运行命令和完整参数，并用规则表达式或伪代码保留自定义信号与成交逻辑。最终回复给出实测区间、覆盖、核心结果及报告路径。

### 公共报告接口

先写出适用的 CSV，再调用 [backtestreport.Write](../../backtestreport/report.go)。`date` 用 `YYYY-MM-DD`，成交时刻用 `YYYY-MM-DD HH:MM[:SS]` 或 RFC 3339，图表统一显示东八区时间。`virtual` 用 `true` 或 `false`，`net_return_pct` 填百分数，例如 `2.5` 表示 `2.5%`。

```go
import "github.com/injoyai/strategy-tail/backtestreport"

// outputDir 中已保存本次运行的 CSV，markdown 是报告正文。
err := backtestreport.Write(outputDir, markdown)
```

有 `equity.csv` 时生成账户权益图，包含可用的基准权益。有 `trades.csv` 时按实际平仓时间绘制每笔净收益率，期末估值通过 `virtual` 标记。需要展示日线或分钟信号、阈值和买卖点时，可向 `Write` 追加 `backtestreport.Chart`，用 `Series.Points` 传入各条实际序列，用 `Scatter` 标记成交点。

Python 或 Shell 脚本可调用同一个发布入口。

```sh
go run ./cmd/backtest-report -dir "$output_dir" -markdown "$scratch/report-draft.md"
```

省略 `-markdown` 时使用目录内已有的 `report.md`。重复发布会更新报告中的图表区段。SVG 与 Markdown 保存在同一目录，可直接打开，查看时无需外部图表服务。只有文字或数据不足时，图表区会说明本次可用数据的情况。
