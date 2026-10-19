package lab

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/internal/lab/builder"
	"github.com/injoyai/strategy-tail/internal/researchrun"
	"github.com/injoyai/strategy-tail/lib/extend"
	ss "github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx/protocol"
)

// countBuyer / countSeller 在既有桩实现上累计真实求值次数，
// 用于验证原因记录包装不增加策略求值次数（R07）。
type countBuyer struct {
	accountBuyer
	calls int
}

func (b *countBuyer) Buy(code string, ks extend.Klines) bool {
	b.calls++
	return b.accountBuyer.Buy(code, ks)
}

type countSeller struct {
	ss.A持仓N天
	calls int
}

func (s *countSeller) Sell(code string, ks extend.Klines, buy core.Buy) bool {
	s.calls++
	return s.A持仓N天.Sell(code, ks, buy)
}

// accountMinuteBars 生成每日两根分钟线（09:31/09:36，价格 9.9/10.4）。
func accountMinuteBars(dates []string) protocol.Klines {
	out := make(protocol.Klines, 0, len(dates)*2)
	for _, d := range dates {
		for _, m := range []struct {
			clock string
			price float64
		}{{"09:31:00", 9.9}, {"09:36:00", 10.4}} {
			ts, _ := time.ParseInLocation(time.DateTime, d+" "+m.clock, time.Local)
			v := protocol.Yuan(m.price)
			out = append(out, &protocol.Kline{Time: ts, Open: v, Close: v, High: v, Low: v, Volume: 1000})
		}
	}
	return out
}

// recordTestConfig 同一场景分别供裸版与记录版运行：仅 sh600001 有分钟线，
// sh600002 走分钟缺失回退；两股按 A持仓N天{Days:1} 周期性触发卖出。
func recordTestConfig(buyer core.Buyer, seller core.Seller, data map[string]extend.Klines, dates []string) researchrun.Config {
	cfg := accountTestConfig(data, buyer, seller)
	cfg.Variants = []researchrun.Variant{{Name: "v", Buyer: buyer, Seller: seller}}
	cfg.Cost = core.Cost{MinCommission: 5, StampDutyRate: .001, Slippage: protocol.Yuan(.01)}
	cfg.GetMinKlines = func(code string, _, _ time.Time) (protocol.Klines, error) {
		if code == "sh600001" {
			return accountMinuteBars(dates), nil
		}
		return nil, nil
	}
	return cfg
}

// TestAccountRecordEquivalence 验证原因记录不改变账户回测行为（R07/R15）：
// 同一行情下裸策略与记录包装策略的求值次数一致，成交、每日账户与基准逐位一致。
func TestAccountRecordEquivalence(t *testing.T) {
	dates := []string{"2025-01-02", "2025-01-03", "2025-01-06", "2025-01-07", "2025-01-08"}
	data := map[string]extend.Klines{
		"sh000001": accountBars(dates, 100, 101, 102, 103, 104),
		"sh600001": accountBars(dates, 10, 10.5, 10.2, 10.8, 11),
		"sh600002": accountBars(dates, 20, 20.4, 19.8, 20.6, 21),
	}
	account := AccountConfig{InitialCapital: 100000, MaxPositions: 5, OrderBudget: 20000}

	// 裸版：不提供记录上下文。
	bareBuyer := &countBuyer{}
	bareSeller := &countSeller{A持仓N天: ss.A持仓N天{Days: 1}}
	bareReport, bareAccounts, bareReplays, bareBench, _, err := runAccountBacktest(context.Background(),
		recordTestConfig(bareBuyer, bareSeller, data, dates), account)
	if err != nil {
		t.Fatal(err)
	}

	// 记录版：Buyer/Seller 经 RecordContext 包装，第 4 参传入上下文。
	recBuyer := &countBuyer{}
	recSeller := &countSeller{A持仓N天: ss.A持仓N天{Days: 1}}
	rc := builder.NewRecordContext()
	wrappedBuyer := rc.WrapBuyerTree(recBuyer, "buy", builder.RoleBuy, true)
	wrappedSeller := rc.WrapSellerTree(recSeller, "sell", builder.RoleSell, true)
	recReport, recAccounts, recReplays, recBench, _, err := runAccountBacktest(context.Background(),
		recordTestConfig(wrappedBuyer, wrappedSeller, data, dates), account, rc)
	if err != nil {
		t.Fatal(err)
	}

	// 未提供上下文的变体不产生回放数据；提供了的恰好一份且非 nil。
	for i, rv := range bareReplays {
		if rv != nil {
			t.Fatalf("裸版变体 %d 不应产生回放数据", i)
		}
	}
	if len(recReplays) != 1 || recReplays[0] == nil {
		t.Fatalf("记录版回放数据缺失: %d", len(recReplays))
	}

	if bareBuyer.calls == 0 || bareSeller.calls == 0 {
		t.Fatalf("场景未命中求值: buyer=%d seller=%d", bareBuyer.calls, bareSeller.calls)
	}
	if recBuyer.calls != bareBuyer.calls || recSeller.calls != bareSeller.calls {
		t.Fatalf("记录改变求值次数: buyer %d→%d, seller %d→%d",
			bareBuyer.calls, recBuyer.calls, bareSeller.calls, recSeller.calls)
	}
	// Report.Results 内嵌 Variant（含 Buyer/Seller 接口值），记录版的包装器
	// 与裸版策略对象必然不同；等价性只关心数据结果，逐字段比对 Trades 与覆盖。
	if !reflect.DeepEqual(bareReport.Coverage, recReport.Coverage) {
		t.Fatal("记录改变数据覆盖")
	}
	for i := range bareReport.Results {
		if !reflect.DeepEqual(bareReport.Results[i].Trades, recReport.Results[i].Trades) {
			t.Fatalf("记录改变成交结果（变体 %d）", i)
		}
	}
	if !reflect.DeepEqual(bareAccounts, recAccounts) {
		t.Fatalf("记录改变每日账户: %+v vs %+v", bareAccounts[0], recAccounts[0])
	}
	if !reflect.DeepEqual(bareBench, recBench) {
		t.Fatal("记录改变基准")
	}

	// 场景 sanity：确有持仓卖出、确有分钟缺失回退、确有期末 Virtual 成交。
	trades := recReport.Results[0].Trades
	if len(trades) < 4 {
		t.Fatalf("场景成交过少: %d", len(trades))
	}
	if recAccounts[0].MissingMinuteDays == 0 {
		t.Fatal("场景未覆盖分钟缺失回退")
	}
	virtual := 0
	for _, tr := range trades {
		if tr.Virtual {
			virtual++
		}
	}
	if virtual == 0 || virtual == len(trades) {
		t.Fatalf("场景 Virtual 成交异常: %d/%d", virtual, len(trades))
	}

	// 记录接线 sanity：包装 Buyer 委托内层结果并登记根帧，可提取成功路径。
	if !wrappedBuyer.Buy("x", nil) {
		t.Fatal("包装 Buyer 未委托内层结果")
	}
	if path := rc.ExtractSuccessPath(); !reflect.DeepEqual(path, []string{"buy"}) {
		t.Fatalf("成功路径 = %v, want [buy]", path)
	}
}

// dateCodeBuyer / dateCodeSeller 按 (代码, 日期) 精确触发买卖信号，
// 用于构造手工可核算的回放内容场景。
type dateCodeBuyer map[string]map[string]bool

func (dateCodeBuyer) Name() string { return "date-code buyer" }

func (b dateCodeBuyer) Buy(code string, ks extend.Klines) bool {
	dates, ok := b[code]
	return ok && dates[ks[len(ks)-1].Time.Format(time.DateOnly)]
}

type dateCodeSeller map[string]map[string]bool

func (dateCodeSeller) Name() string { return "date-code seller" }

func (s dateCodeSeller) Sell(code string, ks extend.Klines, _ core.Buy) bool {
	dates, ok := s[code]
	return ok && dates[ks[len(ks)-1].Time.Format(time.DateOnly)]
}

// wantReplayTrade 核对单笔成交事件的确定性字段与数值（容差同 nearAccount）。
func wantReplayTrade(t *testing.T, ev ReplayTrade, want ReplayTrade) {
	t.Helper()
	if ev.Seq != want.Seq || ev.PositionID != want.PositionID || ev.Code != want.Code ||
		ev.Time != want.Time || ev.Side != want.Side || ev.Quantity != want.Quantity ||
		ev.MinuteFallback != want.MinuteFallback {
		t.Fatalf("事件头不符:\n got %+v\nwant %+v", ev, want)
	}
	nearAccount(t, ev.Price, want.Price)
	nearAccount(t, ev.ExecPrice, want.ExecPrice)
	nearAccount(t, ev.Fee, want.Fee)
	if !reflect.DeepEqual(ev.Reason, want.Reason) {
		t.Fatalf("事件路径 = %v, want %v", ev.Reason, want.Reason)
	}
}

// wantReplayHolding 核对单个持仓帧的确定性字段与数值（容差同 nearAccount）。
func wantReplayHolding(t *testing.T, h ReplayHolding, want ReplayHolding) {
	t.Helper()
	if h.PositionID != want.PositionID || h.Code != want.Code || h.Quantity != want.Quantity ||
		h.BuyTime != want.BuyTime || h.MarkDate != want.MarkDate {
		t.Fatalf("持仓帧头不符:\n got %+v\nwant %+v", h, want)
	}
	nearAccount(t, h.BuyPrice, want.BuyPrice)
	nearAccount(t, h.BuyExecPrice, want.BuyExecPrice)
	nearAccount(t, h.TotalCost, want.TotalCost)
	nearAccount(t, h.CostPrice, want.CostPrice)
	nearAccount(t, h.MarkPrice, want.MarkPrice)
	nearAccount(t, h.MarketValue, want.MarketValue)
	nearAccount(t, h.FloatingPnL, want.FloatingPnL)
	nearAccount(t, h.FloatingRate, want.FloatingRate)
}

// TestAccountReplayContent 用手工可核算的行情验证回放内容（R05/R06/R11）：
// 买入最低佣金、分钟价卖出、分钟缺失回退、同日卖出再买入得到新仓位身份、
// 收盘帧与账户点逐位一致、期末 Virtual 清算不产生事件。
// 成本配置 {MinCommission:5, StampDutyRate:.001, Slippage:.01} 下：
// 买入 1900/900/1800 股各含 5 元费用；卖出费用 = 佣金 + 印花税。
func TestAccountReplayContent(t *testing.T) {
	dates := []string{"2025-01-02", "2025-01-03", "2025-01-06"}
	data := map[string]extend.Klines{
		"sh000001": accountBars(dates, 100, 101, 102),
		"sh600001": accountBars(dates, 10, 11, 12),
		"sh600002": accountBars(dates, 20, 21, 22),
	}
	buyer := dateCodeBuyer{
		"sh600001": {"2025-01-02": true, "2025-01-03": true},
		"sh600002": {"2025-01-02": true},
	}
	seller := dateCodeSeller{
		"sh600001": {"2025-01-03": true},
		"sh600002": {"2025-01-06": true},
	}
	rc := builder.NewRecordContext()
	cfg := recordTestConfig(
		rc.WrapBuyerTree(buyer, "buy", builder.RoleBuy, true),
		rc.WrapSellerTree(seller, "sell", builder.RoleSell, true),
		data, dates)
	account := AccountConfig{InitialCapital: 100000, MaxPositions: 2, OrderBudget: 20000}
	_, accounts, replays, _, _, err := runAccountBacktest(context.Background(), cfg, account, rc)
	if err != nil {
		t.Fatal(err)
	}
	if len(replays) != 1 || replays[0] == nil {
		t.Fatalf("回放数据缺失: %d", len(replays))
	}
	rv := replays[0]
	if rv.Version != replayVersion || rv.VariantIndex != 0 || rv.VariantName != "v" {
		t.Fatalf("回放头不符: v=%d idx=%d name=%q", rv.Version, rv.VariantIndex, rv.VariantName)
	}
	if !reflect.DeepEqual(rv.Dates, dates) {
		t.Fatalf("回放日期 = %v, want %v", rv.Dates, dates)
	}
	if len(rv.Days) != len(dates) {
		t.Fatalf("回放帧数 = %d, want %d", len(rv.Days), len(dates))
	}
	// 回放帧与每日账户点逐位一致（采集旁路不改变账户结果，R15）。
	for i, day := range rv.Days {
		p := accounts[0].Points[i]
		if day.Date != p.Date || day.Equity != p.Equity || day.Cash != p.Cash {
			t.Fatalf("回放帧与账户点不一致: day %+v point %+v", day, p)
		}
	}

	// D1：买入 sh600001 1900 股（19024 元）与 sh600002 900 股（18014 元）。
	day := rv.Days[0]
	nearAccount(t, day.Equity, 99962)
	nearAccount(t, day.Cash, 62962)
	nearAccount(t, day.MarketValue, 37000)
	nearAccount(t, day.PositionRatio, 37000.0/99962*100)
	nearAccount(t, day.DayPnL, -38)
	if len(day.Trades) != 2 {
		t.Fatalf("D1 事件数 = %d, want 2", len(day.Trades))
	}
	wantReplayTrade(t, day.Trades[0], ReplayTrade{Seq: 1, PositionID: 1, Code: "sh600001",
		Time: "2025-01-02 15:00:00", Side: "buy", Price: 10, ExecPrice: 10.01,
		Quantity: 1900, Fee: 5, Reason: []string{"buy"}})
	wantReplayTrade(t, day.Trades[1], ReplayTrade{Seq: 2, PositionID: 2, Code: "sh600002",
		Time: "2025-01-02 15:00:00", Side: "buy", Price: 20, ExecPrice: 20.01,
		Quantity: 900, Fee: 5, Reason: []string{"buy"}})
	if len(day.Holdings) != 2 {
		t.Fatalf("D1 持仓数 = %d, want 2", len(day.Holdings))
	}
	wantReplayHolding(t, day.Holdings[0], ReplayHolding{PositionID: 1, Code: "sh600001",
		BuyTime: "2025-01-02 15:00:00", Quantity: 1900, BuyPrice: 10, BuyExecPrice: 10.01,
		TotalCost: 19024, CostPrice: 19024.0 / 1900, MarkPrice: 10, MarkDate: "2025-01-02",
		MarketValue: 19000, FloatingPnL: -24, FloatingRate: -24.0 / 19024 * 100})
	wantReplayHolding(t, day.Holdings[1], ReplayHolding{PositionID: 2, Code: "sh600002",
		BuyTime: "2025-01-02 15:00:00", Quantity: 900, BuyPrice: 20, BuyExecPrice: 20.01,
		TotalCost: 18014, CostPrice: 18014.0 / 900, MarkPrice: 20, MarkDate: "2025-01-02",
		MarketValue: 18000, FloatingPnL: -14, FloatingRate: -14.0 / 18014 * 100})

	// D2：09:31 分钟价 9.9（含滑点 9.89）卖出 pos1（费用 23.791 = 5 + 18.791），
	// 同日再买同票得到新仓位身份 pos3；持仓帧顺序 [pos2, pos3]。
	day = rv.Days[1]
	nearAccount(t, day.Equity, 100606.209)
	nearAccount(t, day.Cash, 61906.209)
	nearAccount(t, day.MarketValue, 38700)
	nearAccount(t, day.PositionRatio, 38700/100606.209*100)
	nearAccount(t, day.DayPnL, 644.209)
	if len(day.Trades) != 2 {
		t.Fatalf("D2 事件数 = %d, want 2", len(day.Trades))
	}
	wantReplayTrade(t, day.Trades[0], ReplayTrade{Seq: 3, PositionID: 1, Code: "sh600001",
		Time: "2025-01-03 09:31:00", Side: "sell", Price: 9.9, ExecPrice: 9.89,
		Quantity: 1900, Fee: 23.791, Reason: []string{"sell"}})
	wantReplayTrade(t, day.Trades[1], ReplayTrade{Seq: 4, PositionID: 3, Code: "sh600001",
		Time: "2025-01-03 15:00:00", Side: "buy", Price: 11, ExecPrice: 11.01,
		Quantity: 1800, Fee: 5, Reason: []string{"buy"}})
	if len(day.Holdings) != 2 || day.Holdings[0].PositionID != 2 || day.Holdings[1].PositionID != 3 {
		t.Fatalf("D2 持仓身份顺序不符: %v", day.Holdings)
	}
	wantReplayHolding(t, day.Holdings[0], ReplayHolding{PositionID: 2, Code: "sh600002",
		BuyTime: "2025-01-02 15:00:00", Quantity: 900, BuyPrice: 20, BuyExecPrice: 20.01,
		TotalCost: 18014, CostPrice: 18014.0 / 900, MarkPrice: 21, MarkDate: "2025-01-03",
		MarketValue: 18900, FloatingPnL: 886, FloatingRate: 886.0 / 18014 * 100})
	wantReplayHolding(t, day.Holdings[1], ReplayHolding{PositionID: 3, Code: "sh600001",
		BuyTime: "2025-01-03 15:00:00", Quantity: 1800, BuyPrice: 11, BuyExecPrice: 11.01,
		TotalCost: 19823, CostPrice: 19823.0 / 1800, MarkPrice: 11, MarkDate: "2025-01-03",
		MarketValue: 19800, FloatingPnL: -23, FloatingRate: -23.0 / 19823 * 100})

	// D3：sh600002 无分钟线回退收盘价卖出（费用 24.791 = 5 + 19.791）；
	// pos3 持有到期，期末 Virtual 清算不产生事件；有日线无分钟时估值价仍更新。
	day = rv.Days[2]
	nearAccount(t, day.Equity, 103272.418)
	nearAccount(t, day.Cash, 81672.418)
	nearAccount(t, day.MarketValue, 21600)
	nearAccount(t, day.PositionRatio, 21600/103272.418*100)
	nearAccount(t, day.DayPnL, 2666.209)
	if len(day.Trades) != 1 {
		t.Fatalf("D3 事件数 = %d, want 1（期末 Virtual 不产生事件）", len(day.Trades))
	}
	wantReplayTrade(t, day.Trades[0], ReplayTrade{Seq: 5, PositionID: 2, Code: "sh600002",
		Time: "2025-01-06 15:00:00", Side: "sell", Price: 22, ExecPrice: 21.99,
		Quantity: 900, Fee: 24.791, MinuteFallback: true, Reason: []string{"sell"}})
	if len(day.Holdings) != 1 {
		t.Fatalf("D3 持仓数 = %d, want 1", len(day.Holdings))
	}
	wantReplayHolding(t, day.Holdings[0], ReplayHolding{PositionID: 3, Code: "sh600001",
		BuyTime: "2025-01-03 15:00:00", Quantity: 1800, BuyPrice: 11, BuyExecPrice: 11.01,
		TotalCost: 19823, CostPrice: 19823.0 / 1800, MarkPrice: 12, MarkDate: "2025-01-06",
		MarketValue: 21600, FloatingPnL: 1777, FloatingRate: 1777.0 / 19823 * 100})
	// sh600002 全程无分钟数据：D2、D3 两个持仓估值日均计回退。
	if accounts[0].MissingMinuteDays != 2 {
		t.Fatalf("MissingMinuteDays = %d, want 2", accounts[0].MissingMinuteDays)
	}
}
