# AGENT 协作规范

本文件记录团队在本仓库内的统一约定，便于 AI/人工协作时保持一致。**修改任何统计、指标、策略接口前请先阅读本文。**

***

## 1. 项目结构

```
core/        ← 核心类型与公共算法（Buyer / Seller / Trade / Stats）
strategies/
  buy/       ← 买入策略，命名习惯：buy_*.go
  sell/      ← 卖出策略，命名习惯：sell_*.go
  util/      ← 策略共用计算（MACD/RSI 等）
lib/extend/  ← K 线模型与拉取
cmd/
  backtest/  ← 历史回测入口
  screen/    ← 实时选股 + Web 监控
common.go    ← 全局组合好的 Buyer/Seller 与初始化
```

新功能优先放进对应包，避免在 `cmd/` 写算法逻辑。

***

## 2. 策略接口

所有买入/卖出策略实现下列接口（见 [core/types.go](core/types.go)）：

```go
type Buyer interface {
    Name() string
    Buy(code string, dks extend.Klines) bool
}

type Seller interface {
    Name() string
    Sell(code string, dks extend.Klines, buy Buy) bool
}
```

* `dks` 的最后一根 K 线即"今天"。

* 组合策略使用 `buy.And` / `buy.Or` / `buy.Not` 与 `sell.Or`，不要自己写遍历。

* **风控规则（止损/止盈/持仓天数/追踪止损）一律实现为** **`Seller`** **并用** **`sell.Or`** **与策略 Seller 组合，禁止在回测引擎里用** **`if`** **硬编码风控判断。** 引擎只调用 `this.Sell(...)`，所有卖出条件都由 Seller 组合而来（详见 [§6.3](#63-风控即-seller重要)）。

### 命名规范

**买入/卖出策略类型优先使用** **`A中文`** **命名**，除非该指标本身就是英文术语（MACD/RSI/BOLL/KDJ/EMA 等）。

| 类别           | 写法       | 示例                                         |
| ------------ | -------- | ------------------------------------------ |
| 业务条件 / 自创策略  | `A` + 中文 | `A涨停`、`A流通市值`、`A倍量`、`A现价大于N日均线`、`A跌破买入日开盘` |
| 国际通用技术指标     | 保留英文     | `MACD`、`MACD连涨`、`MACD买入后连跌`、`RSI`、`MAUp`   |
| 通用工具/算法（非策略） | 英文       | `Stats`、`TradeStats`、`Backtest`            |

不要把"业务条件"用纯英文写（如曾经的 `BuyVolume`/`BuyCloseAboveMA` 现已分别替换为 `A倍量`/`A现价大于N日均线`）。新增策略时先想：**"这是 A 股语义还是国际通用术语？"** 选定即可。

### 参数范围约定（重要）

**涉及"天数/周期/窗口"的策略参数，默认使用** **`MinXxx`** **+** **`MaxXxx`** **范围对，而不是单一固定值。**

* 命名：下限 `Min` 前缀，上限 `Max` 前缀，例如 `MinDays/MaxDays`、`MinLookback/MaxLookback`、`MinPeriod/MaxPeriod`。

* `MaxXxx == 0` 或 `MaxXxx < MinXxx` 时上限不生效，只校验下限，退化为"至少 N 天"语义，向后兼容。

* 文档注释中明确写出默认值、范围语义、退化为固定值的条件。

* `Name()` 应体现范围，如 `MACD连涨3~5天`、`4-20日MACD最低点后`；当上限不生效时只显示下限。

已适配的示例：

* [buy.MACD连涨](strategies/buy/buy_macd.go) `MinDays/MaxDays`

* [buy.MACD负数](strategies/buy/buy_macd.go) `MinDays/MaxDays`

* [buy.MACD反转](strategies/buy/buy_macd.go) `MinLookback/MaxLookback`

* [buy.MACD平滑](strategies/buy/buy_macd.go) `Days/MaxRatio` — 量柱走势光滑度过滤，作为组合条件引用

**反例**（禁止新增）：单一 `Days int` / `Lookback int` / `Period int` 字段，仅校验 `>=` 固定值，无法表达"3\~5 天"这类区间。

历史字段重命名时同步更新所有调用点（[common.go](common.go)、`cmd/` 下各入口），保证编译通过。

***

## 3. 统一统计入口（必读）

**严禁在选股、回测、前端各写一套胜率/盈亏比。** 所有"汇总一组 `Trade`"的逻辑必须走：

[core/stats.go](core/stats.go)

```go
func Stats(trades []Trade) TradeStats
```

### 盈亏比口径：百分比

```text
单笔收益率 = (Sell - Buy) / Buy
ProfitFactor = Σ盈利单收益率 / |Σ亏损单收益率|
```

原因：本系统按"一手"等数量买入，金额口径会被高价股亏损放大，导致盈亏比失真。
百分比口径下高低价股权重一致，结果反映真实策略质量。

### 计算规则

* 单笔盈亏取**收益率（%）**，不是绝对金额。

* `profit > 0` 计入盈利桶，`profit < 0` 计入亏损桶，`profit == 0`（打平）**两边都不计入**。

* 无亏损但有盈利时 `ProfitFactor = math.Inf(1)`，打印时用 `math.IsInf(v, 1)` 转 "∞"。

* 胜率 = `Win / Total * 100`。

* `AvgProfit` = Σ所有交易收益率 / Total（%，含亏损和打平）。

* `MaxProfit` = 最大单笔收益率（%）。

* `MaxLoss` = 最小单笔收益率（%，负数）。

* `Analyze()` 中的 `AvgProfit/MaxProfit/MaxLoss` 必须取自 `Stats()`，禁止在 `Analyze` 内用金额口径重算。

### 在哪里复用

| 位置                                             | 说明                     |
| ---------------------------------------------- | ---------------------- |
| [core/analyze.go](core/analyze.go) `Analyze()` | 回测年度汇总，已调用 `Stats`     |
| `cmd/screen/index.html` 历史面板 JS                | 前端按相同公式计算，新增字段时保持口径    |
| 任何后端新增的统计接口                                    | 必须调用 `core.Stats`，禁止重写 |

新增交易后处理逻辑时，**先查** **`core.Stats`** **是否已覆盖你的需求**，不够再扩展 `TradeStats`，禁止在调用点重写。

***

## 4. MACD 计算口径

所有 MACD 策略（买/卖）共用 [strategies/util/macd.go](strategies/util/macd.go) 的 `MACDHistogram`：

```text
DIF = EMA(Close, Fast=12) - EMA(Close, Slow=26)
DEA = EMA(DIF, Signal=9)
Hist = DIF - DEA      ← 注意：不乘 2
```

* 不要在策略里重新算 EMA/MACD。

* 用 `MACDHistogram(dks, fast, slow, signal)` 返回与 `dks` 同长度的柱子序列。

* 改 EMA 算法 = 同步影响所有买卖策略，谨慎。

***

## 5. K 线与价格

* `protocol.Price` 是 `int64`，单位为**厘**（0.001 元），用 `.Float64()` 转元。

* `extend.Klines` 提供 `HHV(n)`、`LLV(n)`、`MA(n)`、`EMA(n)`、`MACD()`、`BOLL(n)`、`ATR(n)`、`RSI(n)`，**优先使用**，不要新写循环。

* K 线判断尽量按交易日索引（`dks[n-1]`、`dks[n-2]`），不要按日期偏移，避免周末/节假日错位。

### 颜色规则（A 股惯例）

**A 股市场红涨绿跌，与国际市场相反。** 前端所有涉及涨跌颜色的地方必须遵循：

| 含义       | 颜色                | 说明               |
| -------- | ----------------- | ---------------- |
| 上涨 / 正收益 | 红色 `#ef4444`      | 收益率 > 0、K 线收 > 开 |
| 下跌 / 负收益 | 绿色 `#22c55e`      | 收益率 < 0、K 线收 < 开 |
| 持平       | 灰色 `--text-muted` | 收益率 = 0          |

* CSS 变量：`--color-up: #ef4444`（红涨）、`--color-down: #22c55e`（绿跌）。

* 数字样式：`.num-up`（正）= 红、`.num-down`（负）= 绿。

* K 线图（lightweight-charts）：`upColor` = 红、`downColor` = 绿，成交量同步。

* **禁止**使用绿涨红跌的西方惯例。新增任何涉及颜色的 UI 元素时，务必按此规则。

***

## 6. 回测建模

[core/backtest.go](core/backtest.go) 已统一处理以下专业级模块：

### 6.1 成本模型（[core/cost.go](core/cost.go)）

```go
type Cost struct {
    CommissionRate  float64        // 佣金费率（双边，万三=0.0003）
    StampDutyRate   float64        // 印花税率（仅卖出，千一=0.001）
    TransferFeeRate float64        // 过户费率（沪市双边，默认0）
    Slippage        protocol.Price // 滑点（每股绝对值，默认0.01元）
    MinCommission   float64        // 最低佣金（元，默认5）
}
```

* `Cost.BuyCost(price, qty)` → (实际成交价, 含佣金总支出)

* `Cost.SellIncome(price, qty)` → (实际成交价, 扣费后净收入)

* 新增成本维度时扩展 `Cost` 结构体，不要在 Seller 内自己加。

### 6.2 仓位管理（按笔数模型）

```go
type PositionConfig struct {
    MaxPositions int // 全局最大同时持仓股票数（0=不限）
    MaxPerCode   int // 单票最大同时持仓笔数（1=T+1单仓位）
    SharesPerLot int // 每笔买入股数（A股一手=100）
}
```

* `Do()` 在买入前检查 `MaxPerCode`，已持仓达上限则不再买入；

* 每笔交易股数由 `SharesPerLot` 控制。

### 6.3 风控即 Seller（重要）

**止损/止盈/持仓天数/追踪止损等风控规则一律实现为** **`Seller`，用** **`sell.Or`** **与策略 Seller 组合，禁止在回测引擎里用** **`if`** **硬编码风控判断。** 引擎只负责调用 `this.Sell(...)`，所有卖出条件（含风控）都由 Seller 组合而来。

| 风控规则   | Seller       | 说明                    |
| ------ | ------------ | --------------------- |
| 止盈/止损  | `sell.A止盈止损` | 按买入价固定比例，只看买入日之后的 K 线 |
| 持仓天数上限 | `sell.A持仓N天` | 满 N 个交易日强制平仓          |
| 追踪止损   | `sell.A追踪止损` | 从买入后最高收盘价回撤达阈值卖出      |

* 组合方式：`sell.Or{ 风控Seller..., 策略Seller }`，风控在前保留"风控优先保护"；

* 所有 Seller 在分钟级循环统一求值（可盘内触发），T+1 由循环统一跳过；

* **新增风控规则 = 新写一个** **`Seller`**，不要扩展引擎、不要新增 `XxxConfig` 字段；

* 组合级（跨仓位）规则无法用单仓位 Seller 表达，如需新增须单独评估，不要塞回引擎的 per-position 路径；

* 风控参数从 `config.yaml` 的 `backtest.risk.*` 读取后在回测入口（`cmd/backtest`）构造为 Seller，不要放在 `common` 公共配置里。

### 6.4 交易记录

```go
type Trade struct {
    BuyPrice, SellPrice     protocol.Price // 原始收盘价
    BuyExecPrice, SellExecPrice protocol.Price // 含滑点实际成交价
    BuyCost, SellIncome     float64 // 含佣金/印花税的实际投入/收回（元）
    Quantity                int     // 成交股数
    Virtual                 bool    // 期末未平仓的虚拟成交
}
```

* `Trade.Profit()` = (SellIncome - BuyCost) / BuyCost × 100，含成本口径收益率；

* `Trade.ProfitAmount()` = SellIncome - BuyCost，绝对盈亏（元）。

### 6.5 其他

* 分钟级精度卖出（提供 `GetMinKlines` 时）；

* 回测期末未卖出的持仓按最后日收盘价生成 **Virtual=true** 的虚拟成交；

* T+1 规则：买入当天不检查卖出。

**禁止修改原始** **`dks`** **数据。** 分钟级卖出遍历时必须使用 `today` 的副本（`todayCopy := *today`）。

### 6.6 绩效分析（[core/analyze.go](core/analyze.go) + [core/performance.go](core/performance.go)）

| 类别      | 指标                        | 函数                                                           |
| ------- | ------------------------- | ------------------------------------------------------------ |
| 基础      | 胜率/盈亏比/平均收益               | `Stats()`                                                    |
| 风险调整    | Sharpe / Sortino / Calmar | `SharpeRatio()` / `SortinoRatio()` / `CalmarRatio()`         |
| 回撤      | 最大回撤/回撤率/回撤天数/水下曲线        | `Analyze()` 内计算                                              |
| 分布      | 偏度/峰度/VaR/CVaR            | `skewKurtosis()` / `varCVaR()`                               |
| 连续性     | 最大连胜/最大连亏                 | `calculateStreaks()`                                         |
| 蒙特卡洛    | 收益百分位带/破产概率               | `MonteCarlo()`                                               |
| Rolling | 滚动Sharpe/回撤/胜率            | `RollingSharpe()` / `RollingDrawdown()` / `RollingWinRate()` |
| 月度      | 月度收益矩阵                    | `MonthlyReturns()` / `MonthlyReturnMatrix()`                 |
| 基准      | Alpha/Beta/基准收益           | `AlphaBeta()` / `BenchmarkReturn()`                          |

### 6.7 稳健性验证（[core/grid.go](core/grid.go) + [core/walkforward.go](core/walkforward.go) + [core/audit.go](core/audit.go)）

| 功能           | 函数                   | 用途                    |
| ------------ | -------------------- | --------------------- |
| 参数网格搜索       | `GridSearch()`       | 单参数扫描，找出参数平原 vs 过拟合尖峰 |
| Walk-Forward | `WalkForward()`      | 滚动窗口样本内调参 + 样本外验证     |
| 前视偏差审计       | `AuditLookAhead()`   | 校验交易价格与实际K线一致         |
| 数据质量检查       | `AuditDataQuality()` | 检测缺口/零量/负价/重复日期       |

### 6.8 配置化

所有回测参数通过 `config.yaml` 的 `backtest:` 段配置，`common.LoadBacktestConfig()` 统一加载。

***

## 7. 开发规范

### 命名/风格

* 策略类型遵循 [§2 命名规范](#命名规范)：`A中文` 优先，国际通用指标用英文；

* 通用计算/工具类型一律英文（如 `Stats`、`TradeStats`）；

* 文档注释写**触发条件**、**参数含义**、**默认值**，方便他人组合调用。

### TDD

* 新增策略 / 新增统计指标 → **先写测试**：

  * 公共代码：`core/xxx_test.go`；

  * 策略：尽量给一组可重复的 K 线断言，例如 [core/stats\_test.go](core/stats_test.go)；

* 改 MACD/Stats 等公共算法前，把现有测试跑一遍：`go test ./...`。

### YAGNI

* 不写"未来可能用到"的参数；

* 删除时直接删，不留 `// removed`、不重命名 `_` 占位。

***

## 8. 验证清单（提交前）

* [ ] `go test ./...` 通过；

* [ ] 新统计 / 算法已经走 `core.Stats` 或 `strategies/util/*`；

* [ ] 策略实现了 `Name()` 且名字能体现关键参数（如 `MACD连涨3天`）。

