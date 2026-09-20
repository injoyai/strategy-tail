# Risk-as-Seller 重构 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 移除回测引擎中的 `RiskConfig` 硬编码风控层，所有卖出条件（含止损/止盈/持仓天数/追踪止损）统一由 `Seller` 组合实现。

**Architecture:** 引擎 `Backtest.Do()` 只保留一条卖出路径——分钟级循环调用 `this.Sell(...)`。新增无状态 `A追踪止损` Seller 补齐追踪止损能力。风控参数仍由 `config.yaml` 的 `backtest.risk.*` 驱动，但在回测入口（`cmd/backtest`）构造为 `sell.Or` 组合 Seller，不放进 `common` 公共配置。`core` 不 import `strategies/sell`（循环依赖），故 Seller 构造在 `core` 之外。

**Tech Stack:** Go 1.x，已有 `core.Buyer/Seller` 接口、`sell.Or/A止盈止损/A持仓N天`、`github.com/injoyai/conv/cfg` 配置读取。

**Spec:** `docs/superpowers/specs/2026-07-06-risk-as-seller-design.md`

---

## 文件结构

| 文件 | 责任 | 动作 |
|---|---|---|
| `strategies/sell/sell_trailing_stop.go` | 新增 `A追踪止损` Seller（无状态，扫描 dks 求 peak 回撤） | 新建 |
| `strategies/sell/sell_trailing_stop_test.go` | `A追踪止损` 单元测试 | 新建 |
| `core/types.go` | 删除 `RiskConfig`/`DefaultRiskConfig`/`Has*` 方法 | 修改 |
| `core/backtest.go` | 删除 `Risk` 字段、`checkRiskAndSell`、`maxProfitRate`；`Do()` 只剩分钟级卖出路径 | 修改 |
| `core/backtest_test.go` | 改为 `package core_test`，用真实 `sell.A止盈止损/A持仓N天` 替换 `RiskConfig` | 修改 |
| `common.go` | `LoadBacktestConfig` 移除 `risk` 返回值与读取 | 修改 |
| `cmd/backtest/config.go` | `loadRiskSeller()`：读 `backtest.risk.*` 构造 `sell.Or` | 新建 |
| `cmd/backtest/main.go` | `Seller: sell.Or{loadRiskSeller(), common.MACDSeller}` | 修改 |
| `config/config.yaml` | 删除 `max_drawdown` 行 | 修改 |
| `AGENT.md` | §2/§6.3 规范（已完成，仅核对） | 已改 |

---

### Task 1: 新增 `A追踪止损` Seller（TDD）

**Files:**
- Create: `strategies/sell/sell_trailing_stop.go`
- Test: `strategies/sell/sell_trailing_stop_test.go`

- [ ] **Step 1: 写失败测试**

创建 `strategies/sell/sell_trailing_stop_test.go`（复用同包已有的 `make持仓K线`/`makeBuyAt`，见 `sell_holding_days_test.go`）：

```go
package sell

import (
	"testing"

	"github.com/injoyai/tdx/protocol"
)

func Test追踪止损_从峰值回撤达阈值应卖出(t *testing.T) {
	ks := make持仓K线(10) // 价格 10, 10.1, ..., 10.9
	buy := makeBuyAt(ks, 2)
	// 把最后一天调低，制造回撤：peak=10.8(idx8)，current=10.0
	ks[9].Close = protocol.Yuan(10.0)
	ks[9].Open = protocol.Yuan(10.0)
	// 回撤 = (10.8-10.0)/10.8 ≈ 7.4% >= 5%
	s := A追踪止损{Drawdown: 0.05}
	if !s.Sell("sh600000", ks, buy) {
		t.Fatal("从峰值回撤超5%应触发卖出")
	}
}

func Test追踪止损_无回撤不卖出(t *testing.T) {
	ks := make持仓K线(10) // 持续上涨，最后一天即峰值
	buy := makeBuyAt(ks, 2)
	s := A追踪止损{Drawdown: 0.05}
	if s.Sell("sh600000", ks, buy) {
		t.Fatal("当前即峰值、无回撤不应触发")
	}
}

func Test追踪止损_回撤不足不卖出(t *testing.T) {
	ks := make持仓K线(10)
	buy := makeBuyAt(ks, 2)
	ks[9].Close = protocol.Yuan(10.7) // peak=10.8, current=10.7, 回撤≈0.93%
	s := A追踪止损{Drawdown: 0.05}
	if s.Sell("sh600000", ks, buy) {
		t.Fatal("回撤不足5%不应触发")
	}
}

func Test追踪止损_买入日前不误触发(t *testing.T) {
	ks := make持仓K线(10)
	buy := makeBuyAt(ks, 8) // 买入靠后，买入后无回撤
	s := A追踪止损{Drawdown: 0.05}
	if s.Sell("sh600000", ks, buy) {
		t.Fatal("买入后无回撤不应触发")
	}
}

func Test追踪止损_关闭时不卖出(t *testing.T) {
	ks := make持仓K线(10)
	buy := makeBuyAt(ks, 2)
	ks[9].Close = protocol.Yuan(10.0)
	s := A追踪止损{Drawdown: 0} // 关闭
	if s.Sell("sh600000", ks, buy) {
		t.Fatal("Drawdown=0 应关闭，不触发")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./strategies/sell/ -run Test追踪止损 -v`
Expected: FAIL（`A追踪止损` 未定义）

- [ ] **Step 3: 实现 `A追踪止损`**

创建 `strategies/sell/sell_trailing_stop.go`：

```go
package sell

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A追踪止损 从买入后最高收盘价回撤达阈值时卖出的追踪止损策略。
// Drawdown 表示回撤比例（相对峰值），例如 0.05 表示从最高收盘价回撤 5% 触发。
//
// 无状态：扫描 dks 中买入日（含）至今天的收盘价找出峰值 peak，
// 计算当前收盘价相对 peak 的回撤 (peak-current)/peak，回撤 >= Drawdown 则触发。
// 只使用买入日之后的数据，不会有前视偏差。
type A追踪止损 struct {
	Drawdown float64
}

func (s A追踪止损) Name() string {
	d := s.Drawdown
	if d == 0 {
		d = 0.05
	}
	return fmt.Sprintf("追踪止损%.0f%%", d*100)
}

func (s A追踪止损) Sell(code string, dks extend.Klines, buy core.Buy) bool {
	if s.Drawdown <= 0 {
		return false
	}
	if len(dks) == 0 {
		return false
	}
	buyPrice := buy.Price.Float64()
	if buyPrice <= 0 {
		return false
	}

	// 定位买入日在 dks 中的索引
	buyIdx := -1
	buyDate := buy.Time.Format("2006-01-02")
	for i, k := range dks {
		if k.Time.Format("2006-01-02") == buyDate {
			buyIdx = i
			break
		}
	}
	if buyIdx < 0 {
		return false
	}

	// 扫描买入日（含）到今天的最高收盘价
	peak := 0.0
	for i := buyIdx; i < len(dks); i++ {
		c := dks[i].Close.Float64()
		if c > peak {
			peak = c
		}
	}
	if peak <= 0 {
		return false
	}

	current := dks[len(dks)-1].Close.Float64()
	drawdown := (peak - current) / peak
	return drawdown >= s.Drawdown
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./strategies/sell/ -run Test追踪止损 -v`
Expected: PASS（全部 5 个用例）

- [ ] **Step 5: 提交**

```bash
git add strategies/sell/sell_trailing_stop.go strategies/sell/sell_trailing_stop_test.go
git commit -m "feat(sell): 新增 A追踪止损 Seller（无状态扫描峰值回撤）"
```

---

### Task 2: 移除 RiskConfig，引擎卖出统一走 Seller

> 本任务原子提交：所有步骤完成后一次性编译/测试/提交，中间状态不编译属正常。

**Files:**
- Modify: `core/types.go`
- Modify: `core/backtest.go`
- Modify: `core/backtest_test.go`
- Modify: `common.go`
- Create: `cmd/backtest/config.go`
- Modify: `cmd/backtest/main.go`
- Modify: `config/config.yaml`

- [ ] **Step 1: 删除 `core/types.go` 中的 RiskConfig**

删除 `core/types.go` 中整个 `风控层` 区块（`RiskConfig` 结构体、`DefaultRiskConfig`、`HasStopLoss/HasTakeProfit/HasMaxHoldingDays/HasMaxDrawdown/HasTrailingStop` 全部方法），含上方的 `// ============ 风控层 ============` 分隔注释。保留下方的代码不动。删除后 `PositionConfig` 之后直接是文件末尾或后续内容。

- [ ] **Step 2: 删除 `core/backtest.go` 的 risk 字段与 `checkRiskAndSell`**

2a. `Backtest` 结构体删除 `Risk RiskConfig` 字段及其注释，结果为：

```go
type Backtest struct {
	Buyer
	Seller
	Goroutines   int
	Codes        []string
	Years        []int
	GetDayKlines func(code string, start, end time.Time) (extend.Klines, error)
	GetMinKlines func(code string, start, end time.Time) (protocol.Klines, error)
	UseMinute    bool
	Benchmark    string

	// 成本模型（默认 DefaultCost）
	Cost Cost

	// 仓位管理（默认 DefaultPositionConfig）
	Position PositionConfig
}
```

2b. `applyDefaults` 删除 risk 分支：

```go
func (this *Backtest) applyDefaults() {
	if this.Cost.CommissionRate == 0 && this.Cost.StampDutyRate == 0 && this.Cost.Slippage == 0 {
		this.Cost = DefaultCost()
	}
	if this.Position.SharesPerLot == 0 {
		this.Position = DefaultPositionConfig()
	}
}
```

2c. `Run()` 删除风控日志行，仓位日志后直接接 results：

```go
	logs.Infof("仓位: 单票%d笔 全局上限%d股/笔",
		this.Position.MaxPerCode, this.Position.SharesPerLot)

	results := make([]AnalyzeResult, 0, len(this.Years))
```

2d. `Do()` 整体替换为下面版本（删除原 section 2 风控检查、`maxProfitRate`、`checkRiskAndSell`；分钟级卖出成为唯一卖出路径）：

```go
// Do 对单只股票执行回测。
// 仓位管理（单票上限）、成本模型集成于引擎；所有卖出条件（含风控）由 Seller 组合实现，
// 在分钟级循环中统一求值。返回该股票的所有成交记录。
func (this Backtest) Do(code string, his, dks extend.Klines, mks protocol.Klines) []Trade {

	cost := this.Cost
	pos := this.Position

	// 分钟线按日期分组
	m := map[string]protocol.Klines{}
	for _, mk := range mks {
		key := mk.Time.Format(time.DateOnly)
		m[key] = append(m[key], mk)
	}

	joinKlines := func(base extend.Klines, extra ...*extend.Kline) extend.Klines {
		ls := make(extend.Klines, 0, len(base)+len(extra))
		ls = append(ls, base...)
		ls = append(ls, extra...)
		return ls
	}

	ts := []Trade(nil)
	currentBuys := make([]Buy, 0)

	for i := 0; i < len(dks); i++ {

		today := dks[i]
		_his := joinKlines(his, dks[:i]...)
		ls := joinKlines(_his, today)

		// ---- 1. 买入信号 ----
		if this.Buy(code, ls) {
			currentBuys = append(currentBuys, Buy{
				Code:  code,
				Time:  today.Time,
				Price: today.Close,
			})
		}

		if len(currentBuys) == 0 {
			continue
		}

		// ---- 2. 卖出信号（分钟级精度；风控 Seller 在前由 sell.Or 保证优先）----
		todayMinuteKlines, ok := m[today.Time.Format(time.DateOnly)]
		if !ok || len(todayMinuteKlines) == 0 {
			todayMinuteKlines = protocol.Klines{today.Kline}
		}

		remaining := make([]Buy, 0, len(currentBuys))
		for _, currentBuy := range currentBuys {
			if currentBuy.Time.Equal(today.Time) {
				// T+1：买入当天不卖出
				remaining = append(remaining, currentBuy)
				continue
			}
			sold := false
			for ii := range todayMinuteKlines {
				minuteKlines := todayMinuteKlines[:ii+1]
				lastMinuteKline := todayMinuteKlines[ii]
				// 与原版一致：直接修改 today.Kline（today = dks[i] 是指针）
				// 这会影响后续交易日的 _his，是原版行为，不可更改
				today.Kline = minuteKlines.Kline(lastMinuteKline.Time, lastMinuteKline.Open)

				lsSell := joinKlines(_his, today)
				if this.Sell(code, lsSell, currentBuy) {
					this.executeSell(code, currentBuy, today.Close, pos, cost, &ts, todayMinuteKlines[ii].Time)
					sold = true
					break
				}
			}
			if !sold {
				remaining = append(remaining, currentBuy)
			}
		}
		currentBuys = remaining
	}

	// ---- 3. 期末未平仓：按最后收盘价生成虚拟成交 ----
	if len(currentBuys) > 0 && len(dks) > 0 {
		last := dks[len(dks)-1]
		for _, currentBuy := range currentBuys {
			this.executeSell(code, currentBuy, last.Close, pos, cost, &ts, last.Time)
			ts[len(ts)-1].Virtual = true
		}
	}

	return ts
}
```

2e. 删除整个 `checkRiskAndSell` 方法。`executeSell` 保持不变。

- [ ] **Step 3: 重写 `core/backtest_test.go` 为 `package core_test`**

将整个文件替换为（改包名、`Buy` 限定为 `core.Buy`、风控用例改用真实 Seller、移除所有 `Risk:` 字段）：

```go
package core_test

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx/protocol"
)

// ============================================================================
// 成本模型测试
// ============================================================================

func TestBuyCost含滑点和佣金(t *testing.T) {
	c := core.Cost{
		CommissionRate: 0.0003,
		Slippage:       protocol.Yuan(0.01),
		MinCommission:  5.0,
	}
	execPrice, totalCost := c.BuyCost(protocol.Yuan(10.00), 100)
	if math.Abs(execPrice.Float64()-10.01) > 1e-6 {
		t.Fatalf("execPrice expected 10.01, got %v", execPrice)
	}
	if math.Abs(totalCost-1006) > 0.01 {
		t.Fatalf("totalCost expected ~1006, got %v", totalCost)
	}
}

func TestBuyCost大额佣金超过最低(t *testing.T) {
	c := core.Cost{
		CommissionRate: 0.0003,
		Slippage:       protocol.Yuan(0.01),
		MinCommission:  5.0,
	}
	execPrice, totalCost := c.BuyCost(protocol.Yuan(100), 1000)
	if math.Abs(execPrice.Float64()-100.01) > 1e-6 {
		t.Fatalf("execPrice expected 100.01, got %v", execPrice)
	}
	if math.Abs(totalCost-100040.003) > 0.1 {
		t.Fatalf("totalCost expected ~100040, got %v", totalCost)
	}
}

func TestSellIncome含印花税和佣金(t *testing.T) {
	c := core.Cost{
		CommissionRate: 0.0003,
		StampDutyRate:  0.001,
		Slippage:       protocol.Yuan(0.01),
		MinCommission:  5.0,
	}
	execPrice, netIncome := c.SellIncome(protocol.Yuan(11.00), 100)
	if math.Abs(execPrice.Float64()-10.99) > 1e-6 {
		t.Fatalf("execPrice expected 10.99, got %v", execPrice)
	}
	if math.Abs(netIncome-1092.901) > 0.01 {
		t.Fatalf("netIncome expected ~1092.901, got %v", netIncome)
	}
}

func TestTradeProfit含成本口径(t *testing.T) {
	c := core.DefaultCost()
	_, buyCost := c.BuyCost(protocol.Yuan(10.00), 100)
	_, sellIncome := c.SellIncome(protocol.Yuan(11.00), 100)

	tr := core.Trade{
		BuyPrice:   protocol.Yuan(10.00),
		SellPrice:  protocol.Yuan(11.00),
		BuyCost:    buyCost,
		SellIncome: sellIncome,
		Quantity:   100,
	}

	profit := tr.Profit()
	if profit >= 10.0 {
		t.Fatalf("含成本收益率应低于10%%, got %.4f%%", profit)
	}
	if profit <= 0 {
		t.Fatalf("盈利交易收益率应为正, got %.4f%%", profit)
	}
}

// ============================================================================
// 回测引擎测试（卖出统一走 Seller 组合）
// ============================================================================

func TestDo止损触发(t *testing.T) {
	day0 := testKline(time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local), 10, 10, 10, 10)
	day1 := testKline(time.Date(2024, 1, 3, 15, 0, 0, 0, time.Local), 9, 9, 9, 9)

	dks := extend.Klines{day0, day1}

	bt := core.Backtest{
		Buyer:  alwaysBuyer{},
		Seller: sell.Or{sell.A止盈止损{StopLoss: 0.08}},
		Cost:   core.Cost{Slippage: protocol.Yuan(0)},
		Position: core.PositionConfig{SharesPerLot: 100},
	}

	ts := bt.Do("test", nil, dks, nil)
	if len(ts) < 1 {
		t.Fatalf("expected at least 1 trade (stop loss), got %d", len(ts))
	}
	if !ts[0].SellTime.Equal(day1.Time) {
		t.Fatalf("expected sell on day1 (stop loss), got %v", ts[0].SellTime)
	}
	if ts[0].Virtual {
		t.Fatal("first trade should not be virtual (stop loss)")
	}
}

func TestDo止盈触发(t *testing.T) {
	day0 := testKline(time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local), 10, 10, 10, 10)
	day1 := testKline(time.Date(2024, 1, 3, 15, 0, 0, 0, time.Local), 12, 12, 12, 12)

	dks := extend.Klines{day0, day1}

	bt := core.Backtest{
		Buyer:  alwaysBuyer{},
		Seller: sell.Or{sell.A止盈止损{TakeProfit: 0.15}},
		Cost:   core.Cost{Slippage: protocol.Yuan(0)},
		Position: core.PositionConfig{SharesPerLot: 100},
	}

	ts := bt.Do("test", nil, dks, nil)
	if len(ts) < 1 {
		t.Fatalf("expected at least 1 trade (take profit), got %d", len(ts))
	}
	if !ts[0].SellTime.Equal(day1.Time) {
		t.Fatalf("expected sell on day1 (take profit), got %v", ts[0].SellTime)
	}
	if ts[0].Virtual {
		t.Fatal("first trade should not be virtual (take profit)")
	}
}

func TestDo持仓天数上限(t *testing.T) {
	base := time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local)
	dks := make(extend.Klines, 25)
	for i := 0; i < 25; i++ {
		dks[i] = testKline(base.AddDate(0, 0, i), 10, 10, 10, 10)
	}

	bt := core.Backtest{
		Buyer:  alwaysBuyer{},
		Seller: sell.Or{sell.A持仓N天{Days: 5}},
		Cost:   core.Cost{Slippage: protocol.Yuan(0)},
		Position: core.PositionConfig{SharesPerLot: 100},
	}

	ts := bt.Do("test", nil, dks, nil)
	if len(ts) == 0 {
		t.Fatalf("expected trades, got 0")
	}
	expectedSell := dks[5].Time
	if !ts[0].SellTime.Equal(expectedSell) {
		t.Fatalf("expected first sell on day5 (max holding), got %v", ts[0].SellTime)
	}
}

func TestDoT加1规则(t *testing.T) {
	day0 := testKline(time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local), 10, 10, 10, 10)
	day1 := testKline(time.Date(2024, 1, 3, 15, 0, 0, 0, time.Local), 11, 11, 11, 11)
	day2 := testKline(time.Date(2024, 1, 4, 15, 0, 0, 0, time.Local), 12, 12, 12, 12)

	dks := extend.Klines{day0, day1, day2}

	bt := core.Backtest{
		Buyer:  alwaysBuyer{},
		Seller: alwaysSeller{},
		Cost:   core.Cost{Slippage: protocol.Yuan(0)},
		Position: core.PositionConfig{SharesPerLot: 100},
	}

	ts := bt.Do("test", nil, dks, nil)
	if len(ts) == 0 {
		t.Fatalf("expected trades, got 0")
	}
	for _, tr := range ts {
		if tr.Virtual {
			continue
		}
		if tr.BuyTime.Equal(tr.SellTime) {
			t.Fatalf("T+1 violated: trade bought and sold on %v", tr.BuyTime)
		}
	}
}

func TestDo纯策略卖出(t *testing.T) {
	day0 := testKline(time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local), 10, 10, 10, 10)
	day1 := testKline(time.Date(2024, 1, 3, 15, 0, 0, 0, time.Local), 11, 11, 11, 11)
	day2 := testKline(time.Date(2024, 1, 4, 15, 0, 0, 0, time.Local), 12, 12, 12, 12)

	dks := extend.Klines{day0, day1, day2}

	bt := core.Backtest{
		Buyer:  alwaysBuyer{},
		Seller: alwaysSeller{},
		Cost:   core.Cost{Slippage: protocol.Yuan(0)},
		Position: core.PositionConfig{SharesPerLot: 100},
	}

	ts := bt.Do("test", nil, dks, nil)
	if len(ts) == 0 {
		t.Fatalf("expected trades, got 0")
	}
	if !ts[len(ts)-1].Virtual {
		t.Fatal("last trade should be virtual (end of period)")
	}
}

// ============================================================================
// 辅助类型
// ============================================================================

type alwaysBuyer struct{}

func (alwaysBuyer) Name() string                              { return "always" }
func (alwaysBuyer) Buy(code string, dks extend.Klines) bool   { return true }

type alwaysSeller struct{}

func (alwaysSeller) Name() string                                          { return "always" }
func (alwaysSeller) Sell(code string, dks extend.Klines, buy core.Buy) bool { return true }

type neverSeller struct{}

func (neverSeller) Name() string                                           { return "never" }
func (neverSeller) Sell(code string, dks extend.Klines, buy core.Buy) bool  { return false }

func testKline(t time.Time, open, high, low, close float64) *extend.Kline {
	return &extend.Kline{
		Unix: t.Unix(),
		Kline: &protocol.Kline{
			Time:  t,
			Open:  protocol.Yuan(open),
			High:  protocol.Yuan(high),
			Low:   protocol.Yuan(low),
			Close: protocol.Yuan(close),
		},
	}
}
```

- [ ] **Step 4: 更新 `common.go` 的 `LoadBacktestConfig`**

删除 `risk` 返回值与读取块。新签名与函数体：

```go
// LoadBacktestConfig 从 config.yaml 读取回测引擎配置（成本/仓位/年份/基准）。
// 未配置的字段使用 DefaultXxx() 填充。
// 风控参数不在此读取——由 cmd/backtest 入口构造为 Seller（见 loadRiskSeller）。
func LoadBacktestConfig() (cost core.Cost, pos core.PositionConfig, years []int, benchmark string, mcIterations int) {
	cost = core.Cost{
		CommissionRate:  cfg.GetFloat64("backtest.cost.commission_rate", 0.0003),
		StampDutyRate:   cfg.GetFloat64("backtest.cost.stamp_duty_rate", 0.001),
		TransferFeeRate: cfg.GetFloat64("backtest.cost.transfer_fee_rate", 0),
		Slippage:        protocol.Yuan(cfg.GetFloat64("backtest.cost.slippage", 0.01)),
		MinCommission:   cfg.GetFloat64("backtest.cost.min_commission", 5.0),
	}
	pos = core.PositionConfig{
		MaxPositions: cfg.GetInt("backtest.position.max_positions", 0),
		MaxPerCode:   cfg.GetInt("backtest.position.max_per_code", 1),
		SharesPerLot: cfg.GetInt("backtest.position.shares_per_lot", 100),
	}
	years = cfg.GetInts("backtest.years", []int{2024, 2025, 2026})
	benchmark = cfg.GetString("backtest.benchmark", "sh000300")
	mcIterations = cfg.GetInt("backtest.monte_carlo_iterations", 1000)
	return
}
```

- [ ] **Step 5: 新建 `cmd/backtest/config.go`**

```go
package main

import (
	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/sell"
)

// loadRiskSeller 从 config.yaml 的 backtest.risk.* 读取风控参数，
// 构造一个由 A止盈止损 / A持仓N天 / A追踪止损 组合的 Seller。
// 各参数为 0 时对应规则不启用；全部为 0 返回 nil。
// 风控 Seller 排在策略 Seller 之前（由调用方组合），保留"风控优先保护"语义。
func loadRiskSeller() core.Seller {
	stopLoss := cfg.GetFloat64("backtest.risk.stop_loss", 0.08)
	takeProfit := cfg.GetFloat64("backtest.risk.take_profit", 0.15)
	maxHoldingDays := cfg.GetInt("backtest.risk.max_holding_days", 20)
	trailingStop := cfg.GetFloat64("backtest.risk.trailing_stop", 0)

	sellers := make([]core.Seller, 0, 3)
	if takeProfit > 0 || stopLoss > 0 {
		sellers = append(sellers, sell.A止盈止损{TakeProfit: takeProfit, StopLoss: stopLoss})
	}
	if maxHoldingDays > 0 {
		sellers = append(sellers, sell.A持仓N天{Days: maxHoldingDays})
	}
	if trailingStop > 0 {
		sellers = append(sellers, sell.A追踪止损{Drawdown: trailingStop})
	}
	if len(sellers) == 0 {
		return nil
	}
	return sell.Or(sellers)
}
```

- [ ] **Step 6: 更新 `cmd/backtest/main.go`**

整体替换为：

```go
package main

import (
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/sell"
)

func main() {

	//获取所有代码（与原版一致）
	codes := common.GetAllCodes()

	// 从 config.yaml 加载成本和仓位配置
	cost, pos, _, benchmark, _ := common.LoadBacktestConfig()

	years := []int{2022, 2023, 2024, 2025, 2026}

	core.Backtest{
		Buyer:        common.BaseBuyer,
		Seller: sell.Or{
			loadRiskSeller(),       // 风控优先（止损/止盈/持仓天数/追踪止损）
			common.MACDSeller,      // 策略卖出
		},
		Goroutines:   common.DefaultGoroutines * 2,
		Codes:        codes,
		Years:        years,
		GetDayKlines: common.Pull.DayKlines,
		GetMinKlines: common.Pull.MinKlines,
		Benchmark:    benchmark,

		// 成本和仓位从 config.yaml 读取
		Cost:     cost,
		Position: pos,
	}.Run()

}
```

- [ ] **Step 7: 更新 `config/config.yaml`**

删除 `risk:` 段下的 `max_drawdown` 行（含注释）。结果：

```yaml
  # 风控层
  risk:
    stop_loss: 0.08           # 单笔止损比例（8%）
    take_profit: 0.15         # 单笔止盈比例（15%）
    max_holding_days: 20      # 最大持仓交易日数
    trailing_stop: 0.0        # 追踪止损比例（0=关闭）
```

- [ ] **Step 8: 编译并运行全部测试**

Run: `go build ./...`
Expected: 编译通过，无错误。

Run: `go test ./...`
Expected: 全部 PASS。重点核对：
- `./strategies/sell/` 含新增 `Test追踪止损_*` 全绿；
- `./core/` 含改写后的 `TestDo止损触发/TestDo止盈触发/TestDo持仓天数上限/TestDoT加1规则/TestDo纯策略卖出` 全绿；
- 无任何 `RiskConfig` 残留引用（编译已保证）。

- [ ] **Step 9: 提交**

```bash
git add core/types.go core/backtest.go core/backtest_test.go common.go cmd/backtest/config.go cmd/backtest/main.go config/config.yaml
git commit -m "refactor(backtest): 移除 RiskConfig，卖出/风控统一由 Seller 组合"
```

---

### Task 3: 核对 AGENT.md 规范

**Files:**
- Verify: `AGENT.md`

- [ ] **Step 1: 核对 §2 与 §6.3**

确认 `AGENT.md` §2 末尾有"风控规则一律实现为 Seller"一条，§6.3 标题为"风控即 Seller（重要）"且含 Seller 映射表与组合规范。（已在 brainstorming 阶段写入，仅核对未被回退。）

- [ ] **Step 2: 运行 go vet 兜底**

Run: `go vet ./...`
Expected: 无告警。

---

## Self-Review

**Spec coverage:**
- 引擎删除 Risk/applyDefaults risk/Run 日志/checkRiskAndSell/maxProfitRate → Task 2 Step 2 ✓
- types.go 删 RiskConfig/Default/Has* → Task 2 Step 1 ✓
- 新增 A追踪止损 + 测试 → Task 1 ✓
- config.yaml 保留 risk.* 删 max_drawdown → Task 2 Step 7 ✓
- common.go 移除 risk 返回 → Task 2 Step 4 ✓
- cmd/backtest 构造 Seller → Task 2 Step 5/6 ✓
- backtest_test.go 改写 → Task 2 Step 3 ✓
- AGENT.md 规范 → Task 3 ✓

**Placeholder scan:** 无 TBD/TODO；所有代码块均为完整可用代码。

**Type consistency:** `A追踪止损{Drawdown}` 在 Task 1 实现、Task 2 Step 5 引用，字段名一致；`sell.Or(sellers)` 与 `sell.Or{...}` 字面量均合法（`type Or []core.Seller`）；`LoadBacktestConfig` 新签名 5 返回值与 main.go 的 `cost, pos, _, benchmark, _ :=` 一致；`core_test` 包引用 `core.Buy/core.Cost/core.Backtest` 全部导出，无循环依赖（core_test→core、core_test→sell、sell→core，无环）。
