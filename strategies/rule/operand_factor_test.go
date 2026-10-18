package rule

import (
	"math"
	"testing"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// glueKline 构造带昨收/量/额字段的 K 线（价格为元，内部转厘），
// 供因子操作数测试使用；与 fixture_test.go 的 makeKline 互不影响。
func glueKline(last, open, high, low, close float64, volume int64, amountYuan float64) *extend.Kline {
	return &extend.Kline{
		Kline: &protocol.Kline{
			Last:   protocol.Yuan(last),
			Open:   protocol.Yuan(open),
			High:   protocol.Yuan(high),
			Low:    protocol.Yuan(low),
			Close:  protocol.Yuan(close),
			Volume: volume,
			Amount: protocol.Yuan(amountYuan),
		},
	}
}

// assertOperand 断言操作数求值结果：KindInvalid 只要求无效，
// KindPrice 精确断言 int64 厘值，KindFloat 按 1e-9 容差断言。
func assertOperand(t *testing.T, what string, op Operand, in Input, wantKind Kind, wantPrice protocol.Price, wantFloat float64) {
	t.Helper()
	got := op.Value(in)
	if got.Kind() != wantKind {
		t.Fatalf("%s: Kind = %s, 期望 %s（值=%s）", what, got.Kind(), wantKind, got)
	}
	switch wantKind {
	case KindPrice:
		if got.Price() != wantPrice {
			t.Fatalf("%s: Price = %d, 期望 %d", what, got.Price(), wantPrice)
		}
	case KindFloat:
		if math.Abs(got.Float()-wantFloat) > 1e-9 {
			t.Fatalf("%s: Float = %v, 期望 %v", what, got.Float(), wantFloat)
		}
	default:
		if got.Valid() {
			t.Fatalf("%s: 期望无效值，实际 %s", what, got)
		}
	}
}

func TestKlineFieldOperands(t *testing.T) {
	ks := extend.Klines{
		glueKline(10, 9.9, 10.8, 9.5, 10.5, 100, 1000),
		glueKline(10.5, 12, 12.5, 11.8, 12.34, 250, 2000.5),
	}
	in := NewInput("sh600000", ks, nil, nil)
	for _, c := range []struct {
		name  string
		op    Operand
		kind  Kind
		price protocol.Price
		fl    float64
	}{
		{"今收厘", ClosePrice(), KindPrice, 12340, 0},
		{"今收元", CloseFloat(), KindFloat, 0, 12.34},
		{"今开厘", OpenPrice(), KindPrice, 12000, 0},
		{"昨收厘", PrevClosePrice(), KindPrice, 10500, 0},
		{"昨开厘", PrevOpenPrice(), KindPrice, 9900, 0},
		{"今量", Volume(), KindFloat, 0, 250},
		{"今额", Turnover(), KindFloat, 0, 2000.5},
	} {
		assertOperand(t, c.name, c.op, in, c.kind, c.price, c.fl)
	}
	// 单根前缀：今字段有效，昨字段（n-2 越界）无效。
	one := NewInput("sh600000", ks[:1], nil, nil)
	assertOperand(t, "单根今收厘", ClosePrice(), one, KindPrice, 10500, 0)
	assertOperand(t, "单根昨收", PrevClosePrice(), one, KindInvalid, 0, 0)
	assertOperand(t, "单根昨开", PrevOpenPrice(), one, KindInvalid, 0, 0)
	// 空前缀：全部无效，不补 0。
	empty := NewInput("sh600000", nil, nil, nil)
	for _, op := range []Operand{
		ClosePrice(), CloseFloat(), OpenPrice(),
		PrevClosePrice(), PrevOpenPrice(), Volume(), Turnover(),
	} {
		assertOperand(t, "空前缀"+op.Name(), op, empty, KindInvalid, 0, 0)
	}
}

func TestFactorOperands(t *testing.T) {
	// 收盘 10.5 → 12.5 → 10：先涨后跌；量能逐日放大。
	ks := extend.Klines{
		glueKline(10, 10.4, 11, 9, 10.5, 100, 1500),
		glueKline(10.5, 12.2, 13, 10, 12.5, 300, 3600),
		glueKline(12.5, 11, 12, 9, 10, 900, 2700),
	}
	in := NewInput("sh600000", ks, nil, nil)
	for _, c := range []struct {
		name  string
		op    Operand
		kind  Kind
		price protocol.Price
		fl    float64
	}{
		{"MA浮点元", MAValue(2), KindFloat, 0, 11.25},
		{"MA整数厘", MAPrice(2), KindPrice, 11250, 0},
		{"RSI浮点", RSIFloat(2), KindFloat, 0, 100 - 100/1.8},
		{"RSI整数截断", RSIInt(2), KindFloat, 0, 44},
		{"前N日均量不含今日", PrevAvgVolume(2), KindFloat, 0, 200},
		{"今量", Volume(), KindFloat, 0, 900},
		{"今额", Turnover(), KindFloat, 0, 2700},
		{"单日涨幅", RiseRate(), KindFloat, 0, -20},
		{"ATR", ATR(2), KindFloat, 0, 3.25},
		{"布林下轨", BollLower(2, 2), KindFloat, 0, 8.75},
		{"乖离率", BiasPercent(2), KindFloat, 0, ((10.0 - 11.25) / 11.25) * 100},
		{"ZScore", ZScore(2), KindFloat, 0, -1},
		{"最高价含今日", HighestValue(2, true), KindFloat, 0, 13},
		{"最低价含今日", LowestValue(2, true), KindFloat, 0, 9},
		{"最高价厘含今日", HighestPrice(2, true), KindPrice, 13000, 0},
		{"最低价厘含今日", LowestPrice(2, true), KindPrice, 9000, 0},
	} {
		assertOperand(t, c.name, c.op, in, c.kind, c.price, c.fl)
	}
}

func TestMarketCapOperand(t *testing.T) {
	k := glueKline(0, 9.5, 11, 9, 10, 100, 1500)
	k.FloatStock = 1_000_000_000
	in := NewInput("sh600000", extend.Klines{k}, nil, nil)
	assertOperand(t, "流通市值亿元", MarketCap(), in, KindFloat, 0, 100)
	assertOperand(t, "流通市值空前缀", MarketCap(),
		NewInput("sh600000", nil, nil, nil), KindInvalid, 0, 0)
}

func TestHighestLowestWindowSwitch(t *testing.T) {
	ks := extend.Klines{
		glueKline(0, 11, 11.5, 8, 10, 0, 0),
		glueKline(0, 12, 12.5, 9, 12, 0, 0),
		glueKline(0, 20, 20.5, 1, 20, 0, 0),
	}
	in := NewInput("sh600000", ks, nil, nil)
	for _, c := range []struct {
		name  string
		op    Operand
		kind  Kind
		price protocol.Price
		fl    float64
	}{
		{"最高含今日", HighestValue(2, true), KindFloat, 0, 20.5},
		{"最高不含今日", HighestValue(2, false), KindFloat, 0, 12.5},
		{"最低含今日", LowestValue(2, true), KindFloat, 0, 1},
		{"最低不含今日", LowestValue(2, false), KindFloat, 0, 8},
		{"最高厘含今日", HighestPrice(2, true), KindPrice, 20500, 0},
		{"最高厘不含今日", HighestPrice(2, false), KindPrice, 12500, 0},
		{"最低厘含今日", LowestPrice(2, true), KindPrice, 1000, 0},
		{"最低厘不含今日", LowestPrice(2, false), KindPrice, 8000, 0},
		{"三天窗口含今日有效", HighestValue(3, true), KindFloat, 0, 20.5},
	} {
		assertOperand(t, c.name, c.op, in, c.kind, c.price, c.fl)
	}
	// 不含今日需要 days+1 根预热：3 根配 days=3 → 无效；含今日则刚好有效。
	assertOperand(t, "不含今日预热不足", HighestValue(3, false), in, KindInvalid, 0, 0)
	assertOperand(t, "不含今日厘预热不足", LowestPrice(3, false), in, KindInvalid, 0, 0)
}

func TestPrevAvgVolumeScaledSemantics(t *testing.T) {
	ks := extend.Klines{
		glueKline(0, 1, 2, 0.5, 1, 100, 0),
		glueKline(0, 1, 2, 0.5, 1, 300, 0),
		glueKline(0, 1, 2, 0.5, 1, 900, 0),
	}
	in := NewInput("sh600000", ks, nil, nil)
	assertOperand(t, "均量×1.5", PrevAvgVolumeScaled(2, 1.5), in, KindFloat, 0, 300)
	// ratio==0：0 乘出 0 是合法浮点值，胶水不吞 0（是否视为阈值不生效由门面决定）。
	assertOperand(t, "ratio=0", PrevAvgVolumeScaled(2, 0), in, KindFloat, 0, 0)
	// 基准 <=0：零量基准没有可比性 → 无效值，而非 0×ratio。
	zeroVol := extend.Klines{
		glueKline(0, 1, 2, 0.5, 1, 0, 0),
		glueKline(0, 1, 2, 0.5, 1, 0, 0),
		glueKline(0, 1, 2, 0.5, 1, 900, 0),
	}
	assertOperand(t, "基准为零", PrevAvgVolumeScaled(2, 1.5),
		NewInput("sh600000", zeroVol, nil, nil), KindInvalid, 0, 0)
	// 预热不足：前 N 日均量需 N+1 根。
	assertOperand(t, "预热不足", PrevAvgVolumeScaled(3, 1.5), in, KindInvalid, 0, 0)
}

func TestVolumeRatioDivisionOrder(t *testing.T) {
	ks := extend.Klines{
		glueKline(0, 1, 1, 1, 1, 50, 0),
		glueKline(0, 1, 1, 1, 1, 55, 0),
	}
	op := VolumeRatio(1)
	got := op.Value(NewInput("sh600000", ks, nil, nil))
	if !got.Valid() || got.Float() != 1.1 {
		t.Fatalf("55/50 必须与阈值 1.1 逐位相等，得到 %v", got)
	}
	if op.Value(NewInput("sh600000", ks[:1], nil, nil)).Valid() {
		t.Fatal("均量预热不足时量比无效")
	}
	ks[0].Volume = 0
	if op.Value(NewInput("sh600000", ks, nil, nil)).Valid() {
		t.Fatal("基准为零时量比无效")
	}
	ks[0].Volume = -50
	if op.Value(NewInput("sh600000", ks, nil, nil)).Valid() {
		t.Fatal("基准为负时量比无效")
	}
}

func TestFactorOperandsInvalidValues(t *testing.T) {
	ks := extend.Klines{
		glueKline(10, 10.4, 11, 9, 10.5, 100, 1500),
		glueKline(10.5, 12.2, 13, 10, 12.5, 300, 3600),
		glueKline(12.5, 11, 12, 9, 10, 900, 2700),
	}
	short := NewInput("sh600000", ks, nil, nil)
	empty := NewInput("sh600000", nil, nil, nil)
	for _, c := range []struct {
		name string
		in   Input
		op   Operand
	}{
		{"MA预热不足", short, MAValue(4)},
		{"MA厘预热不足", short, MAPrice(4)},
		{"RSI浮点预热不足", short, RSIFloat(3)},
		{"RSI整数预热不足", short, RSIInt(3)},
		{"前N日均量预热不足", short, PrevAvgVolume(3)},
		{"ATR预热不足", short, ATR(3)},
		{"布林下轨预热不足", short, BollLower(4, 2)},
		{"乖离率预热不足", short, BiasPercent(4)},
		{"ZScore预热不足", short, ZScore(4)},
		{"MA厘空前缀", empty, MAPrice(2)},
		{"RSI浮点空前缀", empty, RSIFloat(2)},
		{"RSI整数空前缀", empty, RSIInt(2)},
		{"前N日均量空前缀", empty, PrevAvgVolume(2)},
		{"ATR空前缀", empty, ATR(2)},
		{"乖离率空前缀", empty, BiasPercent(2)},
		{"ZScore空前缀", empty, ZScore(2)},
	} {
		assertOperand(t, c.name, c.op, c.in, KindInvalid, 0, 0)
	}
	// NaN/±Inf 归无效：零方差（布林/ZScore）、均线为零（乖离）、除零（涨幅 +Inf）。
	flat := NewInput("sh600000", extend.Klines{
		glueKline(10, 10, 10, 10, 10, 0, 0),
		glueKline(10, 10, 10, 10, 10, 0, 0),
	}, nil, nil)
	assertOperand(t, "零方差布林", BollLower(2, 2), flat, KindInvalid, 0, 0)
	assertOperand(t, "零方差ZScore", ZScore(2), flat, KindInvalid, 0, 0)
	zero := NewInput("sh600000", extend.Klines{
		glueKline(0, 0, 0, 0, 0, 0, 0),
		glueKline(0, 0, 0, 0, 0, 0, 0),
	}, nil, nil)
	assertOperand(t, "均线为零乖离", BiasPercent(2), zero, KindInvalid, 0, 0)
	inf := NewInput("sh600000", extend.Klines{
		glueKline(0, 0, 5, 1, 10, 0, 0),
	}, nil, nil)
	assertOperand(t, "涨幅除零正无穷", RiseRate(), inf, KindInvalid, 0, 0)
}

func TestOperandFuncEscapeHatch(t *testing.T) {
	ks := extend.Klines{
		glueKline(0, 1, 2, 0.5, 1.5, 0, 0),
		glueKline(0, 1, 2, 0.5, 1.5, 0, 0),
	}
	in := NewInput("sh600000", ks, nil, nil)
	seenLen := 0
	op := OperandFunc("测试操作数", func(cur Input) Value {
		seenLen = len(cur.Klines)
		return PriceValue(12345)
	})
	if op.Name() != "测试操作数" {
		t.Fatalf("Name = %q, 期望 %q", op.Name(), "测试操作数")
	}
	v := op.Value(in)
	if !v.Valid() || v.Kind() != KindPrice || v.Price() != 12345 {
		t.Fatalf("OperandFunc 求值 = %s, 期望厘值 12345", v)
	}
	if seenLen != len(ks) {
		t.Fatalf("OperandFunc 收到的前缀长度 = %d, 期望 %d", seenLen, len(ks))
	}
	if OperandFunc("空函数", nil).Value(in).Valid() {
		t.Fatal("nil 函数应返回无效值")
	}
}

func TestGuardViaCompareConvention(t *testing.T) {
	// 约定示例（operand_factor.go 头注释）："MA<=0 不触发"的组件合同守卫
	// 不进胶水，由门面用 Compare(MA > 0) 与主比较 And 组合表达。
	guard, err := NewCompare(MAValue(2), Gt, Literal(FloatValue(0)))
	if err != nil {
		t.Fatalf("构造守卫失败: %v", err)
	}
	main, err := NewCompare(CloseFloat(), Ge, MAValue(2))
	if err != nil {
		t.Fatalf("构造主比较失败: %v", err)
	}
	combined, err := NewAnd(guard, main)
	if err != nil {
		t.Fatalf("构造组合失败: %v", err)
	}
	rising := extend.Klines{
		glueKline(0, 10.5, 11, 9.5, 10, 0, 0),
		glueKline(0, 11.5, 12, 10.5, 11, 0, 0),
		glueKline(0, 12.5, 13, 11.5, 12, 0, 0),
	}
	if !combined.Eval(NewInput("sh600000", rising, nil, nil)) {
		t.Fatal("MA>0 且收盘>=MA 应触发")
	}
	// MA==0（全平零价数据）：守卫不成立，整体不触发——旧组件 ma<=0 → false 的等价表达。
	zero := extend.Klines{
		glueKline(0, 0, 0, 0, 0, 0, 0),
		glueKline(0, 0, 0, 0, 0, 0, 0),
	}
	if combined.Eval(NewInput("sh600000", zero, nil, nil)) {
		t.Fatal("MA==0 守卫应拦截")
	}
	// 预热不足：MAValue 无效 → 守卫不成立。
	if combined.Eval(NewInput("sh600000", rising[:1], nil, nil)) {
		t.Fatal("预热不足守卫应拦截")
	}
}

func TestOperandDiagnosticNames(t *testing.T) {
	if got := RSIFloat(14).Name(); got != "RSI(14)" {
		t.Fatalf("RSIFloat.Name = %q", got)
	}
	if got := RSIInt(14).Name(); got != "整数RSI(14)" {
		t.Fatalf("RSIInt.Name = %q", got)
	}
	if RSIFloat(14).Name() == RSIInt(14).Name() {
		t.Fatal("浮点与整数 RSI 的诊断名必须不同（台账风险 §5.4）")
	}
	if got := PrevAvgVolumeScaled(5, 1.5).Name(); got != "前N日均量(5)×1.5" {
		t.Fatalf("PrevAvgVolumeScaled.Name = %q", got)
	}
	if got := HighestValue(20, true).Name(); got != "含今日·最高价(20)" {
		t.Fatalf("HighestValue.Name = %q", got)
	}
	if got := LowestPrice(20, false).Name(); got != "不含今日·最低价(20)" {
		t.Fatalf("LowestPrice.Name = %q", got)
	}
	// days<=0 时诊断名必须反映因子内的有效默认窗口，而不是透传 0。
	if got := MAPrice(0).Name(); got != "均线(20)" {
		t.Fatalf("MAPrice(0).Name = %q, 期望默认窗口可见", got)
	}
	if got := ClosePrice().Name(); got != "今收(厘)" {
		t.Fatalf("ClosePrice.Name = %q", got)
	}
}
