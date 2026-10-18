package rule

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/tdx/protocol"
)

// T4 持仓标量操作数测试：持仓不可用/买入价非正/空前缀/找不到买入日
// 一律无效值；正常路径数值与旧组件口径逐位一致。

// t4Buy 构造持仓信息。
func t4Buy(day int, price float64) *core.Buy {
	return &core.Buy{
		Code:  "sh600000",
		Time:  time.Date(2026, 10, day, 0, 0, 0, 0, time.UTC),
		Price: protocol.Yuan(price),
	}
}

func TestT4PositionBuyPrice(t *testing.T) {
	ks := t3CloseKlines(10, 11, 12)
	op := PositionBuyPrice()

	// 持仓不可用（买入侧求值）→ 无效。
	if got := op.Value(NewInput("sh600000", ks, nil, nil)); got.Valid() {
		t.Fatalf("无持仓应无效，实际 %v", got)
	}

	// 正常路径：买入价 10.5 元 = 10500 厘，厘级精确。
	in := NewInput("sh600000", ks, nil, t4Buy(2, 10.5))
	got := op.Value(in)
	if got.Kind() != KindPrice || got.Price() != 10500 {
		t.Fatalf("买入价(厘) = %v, 期望 10500", got)
	}

	// 买入价 0 厘为有效值：旧组件（买入次日跌破买入价）无 buy<=0 守卫，
	// 厘域不归一无效以保持精确语义。
	inZero := NewInput("sh600000", ks, nil, t4Buy(2, 0))
	gotZero := op.Value(inZero)
	if gotZero.Kind() != KindPrice || gotZero.Price() != 0 {
		t.Fatalf("买入价 0 应有效为 0，实际 %v", gotZero)
	}

	if op.Name() != "买入价(厘)" {
		t.Fatalf("Name = %q", op.Name())
	}
}

func TestT4PositionBuyPriceFloat(t *testing.T) {
	ks := t3CloseKlines(10, 11, 12)
	op := PositionBuyPriceFloat()

	// 持仓不可用 → 无效。
	if got := op.Value(NewInput("sh600000", ks, nil, nil)); got.Valid() {
		t.Fatalf("无持仓应无效，实际 %v", got)
	}

	// 买入价非正归一无效（旧组件 buyPrice<=0 预检查的集中表达）。
	for _, p := range []float64{0, -1} {
		in := NewInput("sh600000", ks, nil, t4Buy(2, p))
		if got := op.Value(in); got.Valid() {
			t.Fatalf("买入价 %v 应无效，实际 %v", p, got)
		}
	}

	// 正常路径：10.5 元浮点。
	in := NewInput("sh600000", ks, nil, t4Buy(2, 10.5))
	got := op.Value(in)
	if got.Kind() != KindFloat || got.Float() != 10.5 {
		t.Fatalf("买入价(元) = %v, 期望 10.5", got)
	}
}

func TestT4PositionProfitPercent(t *testing.T) {
	ks := t3CloseKlines(10, 11, 12) // 末根收 12 元
	op := PositionProfitPercent()

	// 无效输入：无持仓 / 买入价非正 / 空前缀。
	if got := op.Value(NewInput("sh600000", ks, nil, nil)); got.Valid() {
		t.Fatalf("无持仓应无效，实际 %v", got)
	}
	for _, p := range []float64{0, -5} {
		in := NewInput("sh600000", ks, nil, t4Buy(2, p))
		if got := op.Value(in); got.Valid() {
			t.Fatalf("买入价 %v 应无效，实际 %v", p, got)
		}
	}
	if got := op.Value(NewInput("sh600000", ks[:0], nil, t4Buy(2, 10))); got.Valid() {
		t.Fatalf("空前缀应无效，实际 %v", got)
	}

	// 正常路径：(12-10)/10 = 0.2，运算次序与旧组件一致。
	in := NewInput("sh600000", ks, nil, t4Buy(2, 10))
	got := op.Value(in)
	if got.Kind() != KindFloat || math.Abs(got.Float()-0.2) > 1e-12 {
		t.Fatalf("持仓收益率 = %v, 期望 0.2", got)
	}

	// 负收益：买入价高于今收。
	inLoss := NewInput("sh600000", ks, nil, t4Buy(2, 15))
	gotLoss := op.Value(inLoss)
	want := (12.0 - 15.0) / 15.0
	if gotLoss.Kind() != KindFloat || math.Abs(gotLoss.Float()-want) > 1e-12 {
		t.Fatalf("持仓收益率 = %v, 期望 %v", gotLoss, want)
	}

	if op.Name() != "持仓收益率" {
		t.Fatalf("Name = %q", op.Name())
	}
}

func TestT4PositionHoldingDays(t *testing.T) {
	ks := t3CloseKlines(10, 11, 12, 13, 14) // 时间为 2026-10-01..05
	op := PositionHoldingDays()

	// 无效输入：无持仓 / 空前缀。
	if got := op.Value(NewInput("sh600000", ks, nil, nil)); got.Valid() {
		t.Fatalf("无持仓应无效，实际 %v", got)
	}
	if got := op.Value(NewInput("sh600000", ks[:0], nil, t4Buy(2, 10))); got.Valid() {
		t.Fatalf("空前缀应无效，实际 %v", got)
	}

	// 找不到买入日（全部 K 线早于买入时刻）→ 无效。
	inNoMatch := NewInput("sh600000", ks, nil, t4Buy(9, 10))
	if got := op.Value(inNoMatch); got.Valid() {
		t.Fatalf("买入日在前缀之后应无效，实际 %v", got)
	}

	// 正常路径：买入日为第 2 根（2026-10-02），持仓天数 = 5-1-1 = 3。
	in := NewInput("sh600000", ks, nil, t4Buy(2, 10))
	got := op.Value(in)
	if got.Kind() != KindFloat || got.Float() != 3 {
		t.Fatalf("已持仓交易日数 = %v, 期望 3", got)
	}

	// 买入日为第 5 根（当日买入）→ 0。
	inToday := NewInput("sh600000", ks, nil, t4Buy(5, 10))
	if got := op.Value(inToday); got.Kind() != KindFloat || got.Float() != 0 {
		t.Fatalf("当日买入应为 0，实际 %v", got)
	}

	// 边界：k.Time == 买入时刻（不早于）即匹配。
	inEq := NewInput("sh600000", ks, nil, t4Buy(3, 10))
	if got := op.Value(inEq); got.Kind() != KindFloat || got.Float() != 2 {
		t.Fatalf("买入时刻等于 K 线时间应匹配该根，实际 %v", got)
	}

	// 盘中买入：当日 K 线时间（0 点）早于买入时刻 → 跳到次日，
	// 与旧组件 !Time.Before 口径一致（行为随测试固化）。
	intraday := &core.Buy{
		Code:  "sh600000",
		Time:  time.Date(2026, 10, 3, 14, 30, 0, 0, time.UTC),
		Price: protocol.Yuan(10),
	}
	if got := op.Value(NewInput("sh600000", ks, nil, intraday)); got.Kind() != KindFloat || got.Float() != 1 {
		t.Fatalf("盘中买入次日计入，实际 %v, 期望 1", got)
	}

	if op.Name() != "已持仓交易日数" {
		t.Fatalf("Name = %q", op.Name())
	}
}
