package lab

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/lab/builder"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/util"
	"github.com/injoyai/tdx/protocol"
)

// practice_test.go 盘感练习领域状态机与存储测试。
// 全部使用临时目录与固定行情，不触碰真实行情库与策略库。
// 期望值均由 core.Cost 同一口径手工推算：买入含费总成本、卖出含费净收入。

// practiceTestCost 测试费用：常见 A 股费率＋最低佣金 5 元，滑点由快照清零。
var practiceTestCost = core.Cost{
	CommissionRate:  0.0003,
	StampDutyRate:   0.0005,
	TransferFeeRate: 0.00001,
	MinCommission:   5,
}

// practiceTestKlines 用整数元收盘价构造固定日线（Open=High=Low=Close）。
// 整数元经 protocol.Yuan 转换无截断误差，便于手工推算期望值。
func practiceTestKlines(closes []float64) extend.Klines {
	base := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	ks := make(extend.Klines, len(closes))
	for i, c := range closes {
		at := base.AddDate(0, 0, i)
		price := protocol.Yuan(c)
		ks[i] = &extend.Kline{
			Unix: at.Unix(),
			Kline: &protocol.Kline{
				Time: at, Open: price, High: price, Low: price, Close: price, Volume: 10000,
			},
		}
	}
	return ks
}

// practiceTestMarket 构建固定代码的行情快照。
func practiceTestMarket(t *testing.T, closes []float64) *practiceMarket {
	t.Helper()
	m, err := buildPracticeMarket("sh600000", practiceTestKlines(closes), "local", practiceNowUTC())
	if err != nil {
		t.Fatalf("buildPracticeMarket: %v", err)
	}
	return m
}

// practiceTestSession 构造已发布的 active 会话（revision=1，位于信号日）。
func practiceTestSession(m *practiceMarket, signalIndex int) *practiceSession {
	return &practiceSession{
		Version:      practiceFormatV1,
		ID:           "practice_20260101T000000000Z_00000001",
		Revision:     1,
		Phase:        practicePhaseActive,
		Stage:        practiceStageWaiting,
		StrategyID:   "stg-test",
		StrategySpec: builder.BuilderSpec{Version: 1, Name: "测试策略"},
		Cost:         practiceCostSnapshot(practiceTestCost),
		BudgetYuan:   practiceBudgetYuan,
		FromYear:     2024,
		ToYear:       2024,
		RequestID:    "req-test",
		CreatedAt:    practiceNowUTC(),
		UpdatedAt:    practiceNowUTC(),
		SignalIndex:  signalIndex,
		CurrentIndex: signalIndex,
		BuyIndex:     -1,
		SellIndex:    -1,
	}
}

// practiceTestCmd 构造命令。
func practiceTestCmd(action, id string, rev int64) practiceCommand {
	return practiceCommand{Action: action, CommandID: id, ExpectedRevision: rev}
}

// practiceCmdID 生成第 i 步的命令 ID。
func practiceCmdID(i int) string { return "cmd-" + string(rune('a'+i)) }

// practiceAlmost 浮点相等（费用与评价为浮点推算，容差只抵消舍入误差）。
func practiceAlmost(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// TestPracticeAffordableQuantity 覆盖含费预算取整：整手下调、最低佣金、
// 预算不足与恰好贴合预算的边界。
func TestPracticeAffordableQuantity(t *testing.T) {
	cases := []struct {
		name     string
		price    float64 // 元
		budget   float64 // 元
		wantQty  int
		wantCost float64
	}{
		{"10元按整手下调", 10, 20000, 1900, 19005.89},
		{"2元最低佣金生效", 2, 20000, 9900, 19806.138},
		{"12元上界非整手", 12, 20000, 1600, 19205.952},
		{"300元预算不足一手", 300, 20000, 0, 0},
		{"恰好贴合预算", 10, 20006.2, 2000, 20006.2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			qty, total := practiceAffordableQuantity(practiceTestCost, protocol.Yuan(tc.price), tc.budget)
			if qty != tc.wantQty || !practiceAlmost(total, tc.wantCost) {
				t.Fatalf("got (%d, %v), want (%d, %v)", qty, total, tc.wantQty, tc.wantCost)
			}
		})
	}
}

// MA 保留整数价格口径；MACD 与策略共享浮点算法，逐日前缀不能受未来影响。
func TestBuildPracticeMarketIndicatorsMatchExtend(t *testing.T) {
	closes := make([]float64, 30)
	for i := range closes {
		closes[i] = float64(10 + i%7 + i%5)
	}
	ks := practiceTestKlines(closes)
	m, err := buildPracticeMarket("sh600000", ks, "local", practiceNowUTC())
	if err != nil {
		t.Fatalf("buildPracticeMarket: %v", err)
	}
	if m.Version != practiceFormatV1 || m.Code != "sh600000" || len(m.Days) != len(closes) {
		t.Fatalf("快照头不正确: version=%d code=%s days=%d", m.Version, m.Code, len(m.Days))
	}
	for i := range closes {
		prefix := ks[:i+1]
		d := &m.Days[i]

		// OHLC 与 Unix 原样透传。
		if d.Unix != ks[i].Unix || d.Close != ks[i].Close || d.Open != ks[i].Open {
			t.Fatalf("第 %d 根 OHLC/Unix 透传不一致", i)
		}

		// MA：窗口不足为 null，否则与 extend.MA 的整除口径精确一致。
		for _, c := range []struct {
			n   int
			got *float64
			ext protocol.Price
		}{
			{5, d.MA5, prefix.MA(5)},
			{10, d.MA10, prefix.MA(10)},
			{20, d.MA20, prefix.MA(20)},
		} {
			if i < c.n-1 {
				if c.got != nil {
					t.Fatalf("第 %d 根 MA%d 应为 null", i+1, c.n)
				}
				continue
			}
			if c.got == nil || *c.got != float64(c.ext)/1000 {
				t.Fatalf("第 %d 根 MA%d: got %v, want %v", i+1, c.n, c.got, float64(c.ext)/1000)
			}
		}

		difs := util.MACDDIF(prefix, 12, 26)
		hists := util.MACDHistogram(prefix, 12, 26, 9)
		dif, hist := difs[i], hists[i]
		if *d.DIF != dif {
			t.Fatalf("第 %d 根 DIF: got %v, want %v", i+1, *d.DIF, dif)
		}
		if *d.DEA != dif-hist {
			t.Fatalf("第 %d 根 DEA: got %v, want %v", i+1, *d.DEA, dif-hist)
		}
		if *d.HIST != hist {
			t.Fatalf("第 %d 根 HIST: got %v, want %v", i+1, *d.HIST, hist)
		}
	}
}

// TestBuildPracticeMarketRejectsInvalid 损坏行情拒绝出题。
func TestBuildPracticeMarketRejectsInvalid(t *testing.T) {
	now := practiceNowUTC()
	if _, err := buildPracticeMarket("sh600000", nil, "local", now); err == nil {
		t.Fatal("空行情应报错")
	}
	ks := practiceTestKlines([]float64{10, 0, 11})
	if _, err := buildPracticeMarket("sh600000", ks, "local", now); err == nil {
		t.Fatal("零收盘价应报错")
	}
	ks[1] = nil
	if _, err := buildPracticeMarket("sh600000", ks, "local", now); err == nil {
		t.Fatal("空日K应报错")
	}
	bad := practiceTestKlines([]float64{10, 11, 12})
	bad[2].Unix = bad[1].Unix // 日期未严格递增
	if _, err := buildPracticeMarket("sh600000", bad, "local", now); err == nil {
		t.Fatal("日期未递增应报错")
	}
}

// TestPracticeSessionFullFlow 完整流程：买入费用、T+1、卖出净收入、
// 已实现与浮动评价、卖后观察不变、行情用尽收口、终态拒绝操作。
func TestPracticeSessionFullFlow(t *testing.T) {
	m := practiceTestMarket(t, []float64{10, 10, 10, 10, 10, 10, 11, 12, 12, 12})
	s := practiceTestSession(m, 5)

	// 信号日买入：10 元 → 1900 股，含费 19005.89。
	changed, err := s.applyCommand(m, practiceTestCmd("buy", "c1", 1))
	if err != nil || !changed {
		t.Fatalf("买入失败: changed=%v err=%v", changed, err)
	}
	if s.Revision != 2 || s.Stage != practiceStageHolding || s.Quantity != 1900 || !practiceAlmost(s.BuyCostYuan, 19005.89) {
		t.Fatalf("买入后状态不正确: rev=%d stage=%s qty=%d cost=%v", s.Revision, s.Stage, s.Quantity, s.BuyCostYuan)
	}

	// 买入当天不能卖出。
	if _, err := s.applyCommand(m, practiceTestCmd("sell", "c2", 2)); !errors.Is(err, errPracticeSameDaySell) {
		t.Fatalf("同日卖出应被拒绝: %v", err)
	}

	// 推进一日后按收盘价 11 卖出。
	if changed, err := s.applyCommand(m, practiceTestCmd("next", "c3", 2)); err != nil || !changed || s.CurrentIndex != 6 {
		t.Fatalf("推进失败: changed=%v idx=%d err=%v", changed, s.CurrentIndex, err)
	}
	if changed, err := s.applyCommand(m, practiceTestCmd("sell", "c4", 3)); err != nil || !changed {
		t.Fatalf("卖出失败: %v", err)
	}
	if s.Stage != practiceStageSold || s.SellIndex != 6 || !practiceAlmost(s.SellIncomeYuan, 20883.071) {
		t.Fatalf("卖出后状态不正确: stage=%s income=%v", s.Stage, s.SellIncomeYuan)
	}

	// 卖出评价：已实现 20883.071−19005.89，持有 1 日，两端收盘极值。
	ev := s.evaluate(m)
	if !practiceAlmost(ev.RealizedPnL, 1877.181) || !practiceAlmost(ev.RealizedPct, 1877.181/19005.89*100) {
		t.Fatalf("已实现收益不正确: %+v", ev)
	}
	if ev.HoldingDays != 1 || !practiceAlmost(ev.MaxFloatProfit, 1894.11) || !practiceAlmost(ev.MaxFloatLoss, -5.89) {
		t.Fatalf("持有指标不正确: %+v", ev)
	}
	if ev.OpenPnL != 0 || ev.OpenPct != 0 {
		t.Fatalf("已卖出不应有未平仓估值: %+v", ev)
	}

	// 卖后逐日观察：推进到末日，评价保持不变。
	for i := 7; i <= 9; i++ {
		if changed, err := s.applyCommand(m, practiceTestCmd("next", practiceCmdID(i), s.Revision)); err != nil || !changed || s.CurrentIndex != i {
			t.Fatalf("推进到 %d 失败: err=%v", i, err)
		}
		ev2 := s.evaluate(m)
		if !practiceAlmost(ev2.RealizedPnL, ev.RealizedPnL) || !practiceAlmost(ev2.MaxFloatProfit, 1894.11) || ev2.HoldingDays != 1 {
			t.Fatalf("卖后评价应固定于卖出日: %+v", ev2)
		}
	}

	// 末日之后再推进：行情用尽收口，不虚构卖出。
	if changed, err := s.applyCommand(m, practiceTestCmd("next", "c-end", s.Revision)); err != nil || !changed {
		t.Fatalf("末日推进收口失败: %v", err)
	}
	if s.Phase != practicePhaseFinished || s.EndReason != practiceEndDataEnd {
		t.Fatalf("结束状态不正确: phase=%s end=%s", s.Phase, s.EndReason)
	}
	if s.SellIndex != 6 {
		t.Fatalf("收口不应改变成交记录: sellIndex=%d", s.SellIndex)
	}

	// 终态拒绝一切操作。
	for _, action := range []string{"next", "buy", "sell", "skip", "finish"} {
		if _, err := s.applyCommand(m, practiceTestCmd(action, "c-x", s.Revision)); !errors.Is(err, errPracticeInvalidCommand) {
			t.Fatalf("终态执行 %s 应被拒绝: %v", action, err)
		}
	}
}

// TestPracticeWaitThenBuy 信号日可等待再买，按推进后交易日收盘价成交。
func TestPracticeWaitThenBuy(t *testing.T) {
	m := practiceTestMarket(t, []float64{10, 10, 10, 10, 10, 10, 11, 12})
	s := practiceTestSession(m, 5)
	for i := 0; i < 2; i++ {
		if _, err := s.applyCommand(m, practiceTestCmd("next", practiceCmdID(i), s.Revision)); err != nil {
			t.Fatalf("推进失败: %v", err)
		}
	}
	if s.CurrentIndex != 7 {
		t.Fatalf("应位于索引 7: %d", s.CurrentIndex)
	}
	if _, err := s.applyCommand(m, practiceTestCmd("buy", "cb", s.Revision)); err != nil {
		t.Fatalf("等待后买入失败: %v", err)
	}
	if s.BuyIndex != 7 || s.Quantity != 1600 || !practiceAlmost(s.BuyCostYuan, 19205.952) {
		t.Fatalf("按 12 元买入结果不正确: qty=%d cost=%v", s.Quantity, s.BuyCostYuan)
	}
}

// TestPracticeBudgetShort 高价股含费不足一手时拒绝买入且状态不变。
func TestPracticeBudgetShort(t *testing.T) {
	m := practiceTestMarket(t, []float64{300, 300, 300})
	s := practiceTestSession(m, 0)
	if _, err := s.applyCommand(m, practiceTestCmd("buy", "c1", 1)); !errors.Is(err, errPracticeBudgetShort) {
		t.Fatalf("预算不足应报错: %v", err)
	}
	if s.Stage != practiceStageWaiting || s.Revision != 1 || s.Quantity != 0 {
		t.Fatalf("失败命令不应改变状态: %+v", s)
	}
}

// TestPracticeSkipFinishAndGuards 跳过与主动结束的三态记录及守卫。
func TestPracticeSkipFinishAndGuards(t *testing.T) {
	m := practiceTestMarket(t, []float64{10, 10, 11, 12})

	// waiting 时 sell / 未知动作 / 空 commandID 拒绝。
	s := practiceTestSession(m, 0)
	if _, err := s.applyCommand(m, practiceTestCmd("sell", "c1", 1)); !errors.Is(err, errPracticeInvalidCommand) {
		t.Fatalf("未买入卖出应拒绝: %v", err)
	}
	if _, err := s.applyCommand(m, practiceTestCmd("foo", "c2", 1)); !errors.Is(err, errPracticeInvalidCommand) {
		t.Fatalf("未知动作应拒绝: %v", err)
	}
	if _, err := s.applyCommand(m, practiceTestCmd("next", "", 1)); !errors.Is(err, errPracticeInvalidCommand) {
		t.Fatalf("空 commandID 应拒绝: %v", err)
	}

	// 未交易主动结束。
	if _, err := s.applyCommand(m, practiceTestCmd("finish", "c3", 1)); err != nil {
		t.Fatalf("finish 失败: %v", err)
	}
	if s.Phase != practicePhaseFinished || s.EndReason != practiceEndFinishedUntraded {
		t.Fatalf("未交易结束不正确: %+v", s)
	}

	// 持仓时 skip 拒绝；重复买入拒绝。
	s2 := practiceTestSession(m, 0)
	if _, err := s2.applyCommand(m, practiceTestCmd("buy", "b1", 1)); err != nil {
		t.Fatalf("买入失败: %v", err)
	}
	if _, err := s2.applyCommand(m, practiceTestCmd("skip", "b2", 2)); !errors.Is(err, errPracticeInvalidCommand) {
		t.Fatalf("持仓跳过应拒绝: %v", err)
	}
	if _, err := s2.applyCommand(m, practiceTestCmd("buy", "b3", 2)); !errors.Is(err, errPracticeInvalidCommand) {
		t.Fatalf("重复买入应拒绝: %v", err)
	}
	// 持仓主动结束：未平仓只估值。
	if _, err := s2.applyCommand(m, practiceTestCmd("finish", "b4", 2)); err != nil {
		t.Fatalf("持仓结束失败: %v", err)
	}
	if s2.EndReason != practiceEndFinishedOpen {
		t.Fatalf("未平仓结束原因不正确: %s", s2.EndReason)
	}
	ev := s2.evaluate(m)
	if !practiceAlmost(ev.OpenPnL, 10*1900.0-19005.89) || ev.RealizedPnL != 0 {
		t.Fatalf("未平仓估值不正确: %+v", ev)
	}

	// 卖出后主动结束。
	s3 := practiceTestSession(m, 0)
	if _, err := s3.applyCommand(m, practiceTestCmd("buy", "d1", 1)); err != nil {
		t.Fatalf("买入失败: %v", err)
	}
	if _, err := s3.applyCommand(m, practiceTestCmd("next", "d2", 2)); err != nil {
		t.Fatalf("推进失败: %v", err)
	}
	if _, err := s3.applyCommand(m, practiceTestCmd("sell", "d3", 3)); err != nil {
		t.Fatalf("卖出失败: %v", err)
	}
	if _, err := s3.applyCommand(m, practiceTestCmd("finish", "d4", 4)); err != nil {
		t.Fatalf("卖出后结束失败: %v", err)
	}
	if s3.EndReason != practiceEndFinishedSold {
		t.Fatalf("已卖出结束原因不正确: %s", s3.EndReason)
	}
}

// TestPracticeLastDayDecision 信号日即最后一根：允许当日买入，next 收口，
// 未平仓结束只估值不虚构卖出。
func TestPracticeLastDayDecision(t *testing.T) {
	closes := []float64{10, 10, 10, 10, 10, 10, 11}
	m := practiceTestMarket(t, closes)

	// 当日买入成功，同日卖出拒绝，next 直接行情用尽。
	s := practiceTestSession(m, len(closes)-1)
	if _, err := s.applyCommand(m, practiceTestCmd("buy", "c1", 1)); err != nil {
		t.Fatalf("末日买入失败: %v", err)
	}
	if _, err := s.applyCommand(m, practiceTestCmd("sell", "c2", 2)); !errors.Is(err, errPracticeSameDaySell) {
		t.Fatalf("末日同日卖出应拒绝: %v", err)
	}
	if _, err := s.applyCommand(m, practiceTestCmd("next", "c3", 2)); err != nil {
		t.Fatalf("末日推进失败: %v", err)
	}
	if s.Phase != practicePhaseFinished || s.EndReason != practiceEndDataEnd {
		t.Fatalf("末日结束不正确: %+v", s)
	}
	ev := s.evaluate(m)
	// 末日买入以 close=11 元成交：预算 2 万整手推算 qty=1800（19800 元 +
	// 费 5.94+0.198=6.138），成本 19806.138 元；next 收口后未平仓按当日
	// 收盘估值，市值不变、浮亏恰为买入费用。
	if s.Quantity != 1800 || !practiceAlmost(s.BuyCostYuan, 19806.138) {
		t.Fatalf("末日买入预算推算不正确: qty=%d cost=%v", s.Quantity, s.BuyCostYuan)
	}
	wantPnl := 11*float64(s.Quantity) - s.BuyCostYuan
	if !practiceAlmost(ev.OpenPnL, wantPnl) || ev.RealizedPnL != 0 || ev.HoldingDays != 0 {
		t.Fatalf("末日未平仓估值不正确: %+v", ev)
	}

	// 未买入直接 next 同样收口。
	s2 := practiceTestSession(m, len(closes)-1)
	if _, err := s2.applyCommand(m, practiceTestCmd("next", "d1", 1)); err != nil {
		t.Fatalf("末日推进失败: %v", err)
	}
	if s2.EndReason != practiceEndDataEnd || s2.Quantity != 0 {
		t.Fatalf("末日未交易收口不正确: %+v", s2)
	}

	// 买入后主动结束为 finished_open。
	s3 := practiceTestSession(m, len(closes)-1)
	if _, err := s3.applyCommand(m, practiceTestCmd("buy", "e1", 1)); err != nil {
		t.Fatalf("末日买入失败: %v", err)
	}
	if _, err := s3.applyCommand(m, practiceTestCmd("finish", "e2", 2)); err != nil {
		t.Fatalf("末日主动结束失败: %v", err)
	}
	if s3.EndReason != practiceEndFinishedOpen {
		t.Fatalf("末日主动结束原因不正确: %s", s3.EndReason)
	}
}

// TestPracticeCommandIdempotency 同 commandId 同动作重放返回权威状态且
// 不重复执行；同 commandId 换动作拒绝。
func TestPracticeCommandIdempotency(t *testing.T) {
	m := practiceTestMarket(t, []float64{10, 11, 12})
	s := practiceTestSession(m, 0)

	changed, err := s.applyCommand(m, practiceTestCmd("buy", "c1", 1))
	if err != nil || !changed || s.Revision != 2 {
		t.Fatalf("首次买入不正确: changed=%v rev=%d err=%v", changed, s.Revision, err)
	}

	// 同 ID 同动作、旧 revision 重放：不 409、不重复扣费。
	changed, err = s.applyCommand(m, practiceTestCmd("buy", "c1", 1))
	if err != nil || changed {
		t.Fatalf("重放应返回 changed=false: changed=%v err=%v", changed, err)
	}
	if s.Revision != 2 || s.Quantity != 1900 {
		t.Fatalf("重放不应改变状态: rev=%d qty=%d", s.Revision, s.Quantity)
	}

	// 同 ID 换动作拒绝。
	if _, err := s.applyCommand(m, practiceTestCmd("next", "c1", 2)); !errors.Is(err, errPracticeCommandMismatch) {
		t.Fatalf("同 ID 换动作应拒绝: %v", err)
	}
}

// TestPracticeRevisionConflict 旧 revision 冲突且状态不变。
func TestPracticeRevisionConflict(t *testing.T) {
	m := practiceTestMarket(t, []float64{10, 11, 12})
	s := practiceTestSession(m, 0)
	if _, err := s.applyCommand(m, practiceTestCmd("buy", "c1", 1)); err != nil {
		t.Fatalf("买入失败: %v", err)
	}
	if _, err := s.applyCommand(m, practiceTestCmd("next", "c2", 1)); !errors.Is(err, errPracticeVersionConflict) {
		t.Fatalf("旧 revision 应冲突: %v", err)
	}
	if s.CurrentIndex != 0 || s.Revision != 2 || s.Stage != practiceStageHolding {
		t.Fatalf("冲突不应改变状态: %+v", s)
	}
}

// TestPracticeStoreLifecycle 存储全生命周期：幂等创建、发布、命令、列表、
// 重启恢复、中断恢复与开题快照隔离。
func TestPracticeStoreLifecycle(t *testing.T) {
	root := t.TempDir()
	store := NewPracticeStore(root)
	spec := builder.BuilderSpec{Version: 1, Name: "快照策略", Buy: &builder.BuilderNode{Type: "group", Op: "and"}}
	cost := practiceTestCost

	sess, err := store.CreatePreparing("stg-1", 3, spec, cost, 2023, 2025, "req-1")
	if err != nil {
		t.Fatalf("CreatePreparing: %v", err)
	}
	if sess.Phase != practicePhasePreparing || sess.Revision != 0 || sess.BudgetYuan != practiceBudgetYuan {
		t.Fatalf("preparing 会话不正确: %+v", sess)
	}
	// 费用快照冻结：滑点清零、其余字段保留。
	if sess.Cost.Slippage != 0 || sess.Cost.CommissionRate != cost.CommissionRate || sess.Cost.MinCommission != cost.MinCommission {
		t.Fatalf("费用快照不正确: %+v", sess.Cost)
	}

	// 同 requestID 同参数幂等返回已有记录。
	again, err := store.CreatePreparing("stg-1", 3, spec, cost, 2023, 2025, "req-1")
	if err != nil || again.ID != sess.ID {
		t.Fatalf("幂等创建失败: %v %s", err, again.ID)
	}
	// 同 requestID 异参数冲突。
	if _, err := store.CreatePreparing("stg-1", 3, spec, cost, 2023, 2024, "req-1"); !errors.Is(err, errPracticeRequestConflict) {
		t.Fatalf("异参数应冲突: %v", err)
	}
	// 空 requestID 拒绝。
	if _, err := store.CreatePreparing("stg-1", 3, spec, cost, 2023, 2025, ""); !errors.Is(err, errPracticeRequestIDRequired) {
		t.Fatalf("空 requestID 应拒绝: %v", err)
	}

	// 发布抽题结果（信号日为第 0 根，收盘 10 元）。
	m := practiceTestMarket(t, []float64{10, 11, 12, 12, 12})
	if err := store.Publish(sess.ID, m, 0); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := store.Publish(sess.ID, m, 0); err == nil {
		t.Fatal("重复发布应报错")
	}

	// 快照隔离：调用方之后修改策略与费用不影响已建记录。
	spec.Name = "改名后"
	cost.CommissionRate = 0.9
	got, gm, err := store.Get(sess.ID)
	if err != nil || gm == nil {
		t.Fatalf("Get: %v", err)
	}
	if got.StrategySpec.Name != "快照策略" || got.Cost.CommissionRate != practiceTestCost.CommissionRate {
		t.Fatalf("开题快照被外部修改: %+v", got)
	}
	if got.Phase != practicePhaseActive || got.Revision != 1 || got.CurrentIndex != 0 {
		t.Fatalf("发布状态不正确: %+v", got)
	}

	// 命令应用与 revision 递增（10 元 → 1900 股）。
	got, changed, err := store.Apply(sess.ID, practiceTestCmd("buy", "c1", 1))
	if err != nil || !changed || got.Revision != 2 || got.Quantity != 1900 {
		t.Fatalf("Apply 买入不正确: changed=%v rev=%d err=%v", changed, got.Revision, err)
	}

	// 列表摘要。
	list, err := store.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v %d", err, len(list))
	}
	if list[0].ID != sess.ID || list[0].Phase != practicePhaseActive || list[0].StrategyName != "快照策略" || list[0].RealizedPnL != 0 {
		t.Fatalf("摘要不正确: %+v", list[0])
	}

	// 重启恢复：新 Store 实例从磁盘读取并可继续操作。
	reopened := NewPracticeStore(root)
	got2, _, err := reopened.Get(sess.ID)
	if err != nil || got2.Revision != 2 {
		t.Fatalf("重启后读取失败: %v", err)
	}
	if _, changed, err := reopened.Apply(sess.ID, practiceTestCmd("next", "c2", 2)); err != nil || !changed {
		t.Fatalf("重启后继续操作失败: %v", err)
	}

	// 中断恢复：preparing 标为 failed，active 不动。
	stuck, err := store.CreatePreparing("stg-2", 1, spec, practiceTestCost, 2023, 2025, "req-2")
	if err != nil {
		t.Fatalf("CreatePreparing: %v", err)
	}
	if err := store.RecoverInterrupted(); err != nil {
		t.Fatalf("RecoverInterrupted: %v", err)
	}
	stuckGot, _, err := store.Get(stuck.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stuckGot.Phase != practicePhaseFailed || stuckGot.FailReason != practiceFailInterrupt {
		t.Fatalf("中断恢复不正确: %+v", stuckGot)
	}
	active, _, err := store.Get(sess.ID)
	if err != nil || active.Phase != practicePhaseActive {
		t.Fatalf("active 会话不应被恢复逻辑改动: %v %+v", err, active)
	}
}

// TestPracticeRetry 重练：复制快照、回到信号日、幂等与前置条件。
func TestPracticeRetry(t *testing.T) {
	root := t.TempDir()
	store := NewPracticeStore(root)
	spec := builder.BuilderSpec{Version: 1, Name: "重练策略"}

	sess, err := store.CreatePreparing("stg-1", 1, spec, practiceTestCost, 2024, 2024, "req-1")
	if err != nil {
		t.Fatalf("CreatePreparing: %v", err)
	}
	m := practiceTestMarket(t, []float64{10, 11, 12, 12, 12})
	if err := store.Publish(sess.ID, m, 1); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// 未结束不能重练。
	if _, err := store.Retry(sess.ID, "retry-0"); !errors.Is(err, errPracticeNotFinished) {
		t.Fatalf("未结束重练应拒绝: %v", err)
	}

	// 完整交易后主动结束（发布 rev1 → buy2 → next3 → sell4 → finish5）。
	for _, cmd := range []practiceCommand{
		practiceTestCmd("buy", "c1", 1),
		practiceTestCmd("next", "c2", 2),
		practiceTestCmd("sell", "c3", 3),
		practiceTestCmd("finish", "c4", 4),
	} {
		if _, _, err := store.Apply(sess.ID, cmd); err != nil {
			t.Fatalf("Apply %s: %v", cmd.Action, err)
		}
	}

	// 重练：新记录回到信号日，无成交。
	retry1, err := store.Retry(sess.ID, "retry-1")
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if retry1.ID == sess.ID || retry1.Phase != practicePhaseActive || retry1.Stage != practiceStageWaiting ||
		retry1.Revision != 1 || retry1.CurrentIndex != retry1.SignalIndex ||
		retry1.Quantity != 0 || retry1.SourceSessionID != sess.ID {
		t.Fatalf("重练记录不正确: %+v", retry1)
	}
	if retry1.StrategySpec.Name != "重练策略" || retry1.Cost.CommissionRate != practiceTestCost.CommissionRate || retry1.BudgetYuan != practiceBudgetYuan {
		t.Fatalf("重练快照不正确: %+v", retry1)
	}

	// 行情快照字节与原题完全一致。
	origMarket, err := os.ReadFile(filepath.Join(root, sess.ID, practiceMarketFile))
	if err != nil {
		t.Fatal(err)
	}
	retryMarket, err := os.ReadFile(filepath.Join(root, retry1.ID, practiceMarketFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(origMarket) != string(retryMarket) {
		t.Fatal("重练行情快照与原题不一致")
	}

	// 同 requestID 幂等；原题记录最后一次重练且不递增 revision。
	retryAgain, err := store.Retry(sess.ID, "retry-1")
	if err != nil || retryAgain.ID != retry1.ID {
		t.Fatalf("重练幂等失败: %v %s", err, retryAgain.ID)
	}
	last, _, err := store.Get(sess.ID)
	if err != nil || last.LastRetrySessionID != retry1.ID || last.Revision != 5 {
		t.Fatalf("原题重练簿记不正确: rev=%d retry=%s err=%v", last.Revision, last.LastRetrySessionID, err)
	}

	// 不同 requestID 另建记录。
	retry2, err := store.Retry(sess.ID, "retry-2")
	if err != nil || retry2.ID == retry1.ID {
		t.Fatalf("新 requestID 应另建记录: %v", err)
	}
	// 另一次重练后重放旧请求，仍须返回第一次记录。
	oldRetry, err := store.Retry(sess.ID, "retry-1")
	if err != nil || oldRetry.ID != retry1.ID {
		t.Fatalf("旧重练请求不能创建新记录: %+v %v", oldRetry, err)
	}

	// 空 requestID 拒绝。
	if _, err := store.Retry(sess.ID, ""); !errors.Is(err, errPracticeRequestIDRequired) {
		t.Fatalf("空 requestID 应拒绝: %v", err)
	}
}

// TestPracticeCorruptData 损坏数据 fail closed，不猜测不补造。
func TestPracticeCorruptData(t *testing.T) {
	root := t.TempDir()
	store := NewPracticeStore(root)
	validID := "practice_20260101T000000000Z_00000000"
	dir := filepath.Join(root, validID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	// 垃圾 session.json。
	if err := os.WriteFile(filepath.Join(dir, practiceSessionFile), []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Get(validID); !errors.Is(err, errPracticeCorrupt) {
		t.Fatalf("垃圾会话应报损坏: %v", err)
	}

	// 未知版本。
	if err := os.WriteFile(filepath.Join(dir, practiceSessionFile), []byte(`{"version":99,"phase":"preparing"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Get(validID); !errors.Is(err, errPracticeCorrupt) {
		t.Fatalf("未知版本应报损坏: %v", err)
	}

	// 发布后丢失行情快照。
	root2 := t.TempDir()
	store2 := NewPracticeStore(root2)
	sess, err := store2.CreatePreparing("stg-1", 1, builder.BuilderSpec{Name: "x"}, practiceTestCost, 2024, 2024, "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store2.Publish(sess.ID, practiceTestMarket(t, []float64{10, 11}), 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root2, sess.ID, practiceMarketFile)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store2.Get(sess.ID); !errors.Is(err, errPracticeCorrupt) {
		t.Fatalf("缺行情应报损坏: %v", err)
	}

	// 不存在的合法 ID 与非法 ID 均按不存在处理。
	if _, _, err := store.Get("practice_20260101T000000000Z_ffffffff"); !errors.Is(err, errPracticeNotFound) {
		t.Fatalf("不存在应报 not found: %v", err)
	}
	if _, _, err := store.Get("../escape"); !errors.Is(err, errPracticeNotFound) {
		t.Fatalf("非法 ID 应报 not found: %v", err)
	}
}

// TestPracticeEvalExactNumbers 浮亏为主的评价精度：持有天数、逐日收盘
// 极值（含买入日与卖出日）与卖后观察不变。
func TestPracticeEvalExactNumbers(t *testing.T) {
	closes := []float64{10, 10, 10, 11, 9, 12, 9, 12, 12, 12}
	m := practiceTestMarket(t, closes)
	s := practiceTestSession(m, 2)

	// 信号日买入后推进到索引 6（收盘 9 元）卖出。
	for i, cmd := range []practiceCommand{
		practiceTestCmd("buy", "c1", 1),
		practiceTestCmd("next", "c2", 2),
		practiceTestCmd("next", "c3", 3),
		practiceTestCmd("next", "c4", 4),
		practiceTestCmd("next", "c5", 5),
		practiceTestCmd("sell", "c6", 6),
	} {
		if _, err := s.applyCommand(m, cmd); err != nil {
			t.Fatalf("第 %d 步 %s: %v", i, cmd.Action, err)
		}
	}
	if s.SellIndex != 6 || !practiceAlmost(s.SellIncomeYuan, 17086.149) {
		t.Fatalf("卖出结果不正确: idx=%d income=%v", s.SellIndex, s.SellIncomeYuan)
	}
	ev := s.evaluate(m)
	if ev.HoldingDays != 4 {
		t.Fatalf("持有天数不正确: %d", ev.HoldingDays)
	}
	// 逐日收盘：idx2 −5.89、idx3 +1894.11、idx4 −1905.89、idx5 +3794.11、idx6 −1905.89。
	if !practiceAlmost(ev.MaxFloatProfit, 3794.11) || !practiceAlmost(ev.MaxFloatLoss, -1905.89) {
		t.Fatalf("极值不正确: max=%v min=%v", ev.MaxFloatProfit, ev.MaxFloatLoss)
	}
	if !practiceAlmost(ev.RealizedPnL, 17086.149-19005.89) {
		t.Fatalf("已实现收益不正确: %v", ev.RealizedPnL)
	}

	// 卖后推进不改变评价。
	if _, err := s.applyCommand(m, practiceTestCmd("next", "c7", 7)); err != nil {
		t.Fatalf("卖后推进失败: %v", err)
	}
	ev2 := s.evaluate(m)
	if !practiceAlmost(ev2.RealizedPnL, ev.RealizedPnL) || !practiceAlmost(ev2.MaxFloatProfit, 3794.11) ||
		!practiceAlmost(ev2.MaxFloatLoss, -1905.89) || ev2.HoldingDays != 4 {
		t.Fatalf("卖后评价应固定: %+v", ev2)
	}
}

// TestPracticeInvalidPrice 快照中出现无效价格时拒绝成交且状态不变。
func TestPracticeInvalidPrice(t *testing.T) {
	m := practiceTestMarket(t, []float64{10, 11})
	m.Days[0].Close = 0 // 手工构造损坏价格（buildPracticeMarket 本身已拒绝该输入）
	s := practiceTestSession(m, 0)
	if _, err := s.applyCommand(m, practiceTestCmd("buy", "c1", 1)); !errors.Is(err, errPracticeInvalidPrice) {
		t.Fatalf("零价买入应拒绝: %v", err)
	}
	if s.Stage != practiceStageWaiting || s.Revision != 1 {
		t.Fatalf("失败命令不应改变状态: %+v", s)
	}

	// 买入后推进到零价日卖出同样拒绝。
	m2 := practiceTestMarket(t, []float64{10, 11})
	m2.Days[1].Close = 0
	s2 := practiceTestSession(m2, 0)
	if _, err := s2.applyCommand(m2, practiceTestCmd("buy", "c1", 1)); err != nil {
		t.Fatalf("买入失败: %v", err)
	}
	if _, err := s2.applyCommand(m2, practiceTestCmd("next", "c2", 2)); err != nil {
		t.Fatalf("推进失败: %v", err)
	}
	if _, err := s2.applyCommand(m2, practiceTestCmd("sell", "c3", 3)); !errors.Is(err, errPracticeInvalidPrice) {
		t.Fatalf("零价卖出应拒绝: %v", err)
	}
	if s2.Stage != practiceStageHolding || s2.SellIndex != -1 {
		t.Fatalf("失败卖出不应改变状态: %+v", s2)
	}
}

// TestPracticeWriteFailureKeepsState 写盘失败时磁盘保持旧状态，返回错误
// 而非假装成功。
func TestPracticeWriteFailureKeepsState(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 用户无法用目录权限模拟写盘失败")
	}
	root := t.TempDir()
	store := NewPracticeStore(root)
	sess, err := store.CreatePreparing("stg-1", 1, builder.BuilderSpec{Name: "x"}, practiceTestCost, 2024, 2024, "req-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Publish(sess.ID, practiceTestMarket(t, []float64{10, 11}), 0); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, sess.ID)
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })

	if _, _, err := store.Apply(sess.ID, practiceTestCmd("buy", "c1", 1)); err == nil {
		t.Fatal("写盘失败应返回错误")
	}
	got, _, err := store.Get(sess.ID)
	if err != nil {
		t.Fatalf("读取旧状态失败: %v", err)
	}
	if got.Revision != 1 || got.Stage != practiceStageWaiting || got.Quantity != 0 {
		t.Fatalf("写盘失败后状态应保持不变: %+v", got)
	}
}

// TestPracticeSessionJSONRoundTrip 会话与行情快照 JSON 往返保真：
// 指针型指标窗口不足时保持 null、价格保持厘整数。
func TestPracticeSessionJSONRoundTrip(t *testing.T) {
	m := practiceTestMarket(t, []float64{10, 11})
	buf, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if m.Days[0].MA5 != nil {
		t.Fatal("首根 MA5 应为 null")
	}
	var back practiceMarket
	if err := json.Unmarshal(buf, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Days) != 2 || back.Days[1].Close != m.Days[1].Close || back.Days[0].MA5 != nil {
		t.Fatalf("行情快照往返不一致: %+v", back)
	}

	s := practiceTestSession(m, 1)
	sbuf, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var sback practiceSession
	if err := json.Unmarshal(sbuf, &sback); err != nil {
		t.Fatal(err)
	}
	if sback.ID != s.ID || sback.Revision != s.Revision || sback.StrategySpec.Name != s.StrategySpec.Name {
		t.Fatalf("会话往返不一致: %+v", sback)
	}
}
