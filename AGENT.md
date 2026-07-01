# AGENT 协作规范

本文件记录团队在本仓库内的统一约定，便于 AI/人工协作时保持一致。**修改任何统计、指标、策略接口前请先阅读本文。**

---

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

---

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

- `dks` 的最后一根 K 线即"今天"。
- 组合策略使用 `buy.And` / `buy.Or` / `buy.Not` 与 `sell.Or`，不要自己写遍历。

### 命名规范

**买入/卖出策略类型优先使用 `A中文` 命名**，除非该指标本身就是英文术语（MACD/RSI/BOLL/KDJ/EMA 等）。

| 类别 | 写法 | 示例 |
|---|---|---|
| 业务条件 / 自创策略 | `A` + 中文 | `A涨停`、`A流通市值`、`A倍量`、`A现价大于N日均线`、`A跌破买入日开盘` |
| 国际通用技术指标 | 保留英文 | `MACD`、`MACD连涨`、`MACD买入后连跌`、`RSI`、`MAUp` |
| 通用工具/算法（非策略） | 英文 | `Stats`、`TradeStats`、`Backtest` |

不要把"业务条件"用纯英文写（如曾经的 `BuyVolume`/`BuyCloseAboveMA` 现已分别替换为 `A倍量`/`A现价大于N日均线`）。新增策略时先想：**"这是 A 股语义还是国际通用术语？"** 选定即可。

### 参数范围约定（重要）

**涉及"天数/周期/窗口"的策略参数，默认使用 `MinXxx` + `MaxXxx` 范围对，而不是单一固定值。**

- 命名：下限 `Min` 前缀，上限 `Max` 前缀，例如 `MinDays/MaxDays`、`MinLookback/MaxLookback`、`MinPeriod/MaxPeriod`。
- `MaxXxx == 0` 或 `MaxXxx < MinXxx` 时上限不生效，只校验下限，退化为"至少 N 天"语义，向后兼容。
- 文档注释中明确写出默认值、范围语义、退化为固定值的条件。
- `Name()` 应体现范围，如 `MACD连涨3~5天`、`4-20日MACD最低点后`；当上限不生效时只显示下限。

已适配的示例：
  - [buy.MACD连涨](strategies/buy/buy_macd.go) `MinDays/MaxDays`
  - [buy.MACD负数](strategies/buy/buy_macd.go) `MinDays/MaxDays`
  - [buy.MACD反转](strategies/buy/buy_macd.go) `MinLookback/MaxLookback`

**反例**（禁止新增）：单一 `Days int` / `Lookback int` / `Period int` 字段，仅校验 `>=` 固定值，无法表达"3~5 天"这类区间。

历史字段重命名时同步更新所有调用点（[common.go](common.go)、`cmd/` 下各入口），保证编译通过。

---

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
- 单笔盈亏取**收益率（%）**，不是绝对金额。
- `profit > 0` 计入盈利桶，`profit < 0` 计入亏损桶，`profit == 0`（打平）**两边都不计入**。
- 无亏损但有盈利时 `ProfitFactor = math.Inf(1)`，打印时用 `math.IsInf(v, 1)` 转 "∞"。
- 胜率 = `Win / Total * 100`。

### 在哪里复用
| 位置 | 说明 |
|---|---|
| [core/analyze.go](core/analyze.go) `Analyze()` | 回测年度汇总，已调用 `Stats` |
| `cmd/screen/index.html` 历史面板 JS | 前端按相同公式计算，新增字段时保持口径 |
| 任何后端新增的统计接口 | 必须调用 `core.Stats`，禁止重写 |

新增交易后处理逻辑时，**先查 `core.Stats` 是否已覆盖你的需求**，不够再扩展 `TradeStats`，禁止在调用点重写。

---

## 4. MACD 计算口径

所有 MACD 策略（买/卖）共用 [strategies/util/macd.go](strategies/util/macd.go) 的 `MACDHistogram`：

```text
DIF = EMA(Close, Fast=12) - EMA(Close, Slow=26)
DEA = EMA(DIF, Signal=9)
Hist = DIF - DEA      ← 注意：不乘 2
```

- 不要在策略里重新算 EMA/MACD。
- 用 `MACDHistogram(dks, fast, slow, signal)` 返回与 `dks` 同长度的柱子序列。
- 改 EMA 算法 = 同步影响所有买卖策略，谨慎。

---

## 5. K 线与价格

- `protocol.Price` 是 `int64`，单位为**厘**（0.001 元），用 `.Float64()` 转元。
- `extend.Klines` 提供 `HHV(n)`、`LLV(n)`、`MA(n)`、`EMA(n)`、`MACD()`、`BOLL(n)`、`ATR(n)`、`RSI(n)`，**优先使用**，不要新写循环。
- K 线判断尽量按交易日索引（`dks[n-1]`、`dks[n-2]`），不要按日期偏移，避免周末/节假日错位。

---

## 6. 回测建模

[core/backtest.go](core/backtest.go) 已统一处理：

- 滑点（默认每股 0.01 元，单边加减）；
- 手续费率 `CommissionRate`（买卖双向）；
- 印花税 `StampDutyRate`（仅卖出）；
- 分钟级精度卖出（提供 `GetMinKlines` 时）；
- 回测期末未卖出的持仓按最后日收盘价生成 **Virtual=true** 的虚拟成交。

新增成本/滑点维度时改这里，不要在各个 Seller 内自己加。

---

## 7. 开发规范

### 命名/风格
- 策略类型遵循 [§2 命名规范](#命名规范)：`A中文` 优先，国际通用指标用英文；
- 通用计算/工具类型一律英文（如 `Stats`、`TradeStats`）；
- 文档注释写**触发条件**、**参数含义**、**默认值**，方便他人组合调用。

### TDD
- 新增策略 / 新增统计指标 → **先写测试**：
  - 公共代码：`core/xxx_test.go`；
  - 策略：尽量给一组可重复的 K 线断言，例如 [core/stats_test.go](core/stats_test.go)；
- 改 MACD/Stats 等公共算法前，把现有测试跑一遍：`go test ./...`。

### YAGNI
- 不写"未来可能用到"的参数；
- 删除时直接删，不留 `// removed`、不重命名 `_` 占位。

---

## 8. 验证清单（提交前）

- [ ] `go test ./...` 通过；
- [ ] 新统计 / 算法已经走 `core.Stats` 或 `strategies/util/*`；
- [ ] 策略实现了 `Name()` 且名字能体现关键参数（如 `MACD连涨3天`）。
