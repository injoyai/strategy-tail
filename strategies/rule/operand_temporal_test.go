package rule

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	"github.com/injoyai/tdx/protocol"
)

// t3 时序操作数测试夹具：收盘序列驱动的 K 线（开高低随收盘派生），
// 供历史引用、窗口极值、区间涨幅与 MACD 序列对拍使用。

// t3CloseKlines 以给定收盘序列构造 K 线（元，内部转厘）；Low 取
// 收盘-2、High 取收盘+2，便于触及类谓词构造。
func t3CloseKlines(closes ...float64) extend.Klines {
	ks := make(extend.Klines, 0, len(closes))
	for i, c := range closes {
		ks = append(ks, &extend.Kline{
			Kline: &protocol.Kline{
				Time:  time.Date(2026, 10, i+1, 0, 0, 0, 0, time.UTC),
				Open:  protocol.Yuan(c - 0.5),
				High:  protocol.Yuan(c + 2),
				Low:   protocol.Yuan(c - 2),
				Close: protocol.Yuan(c),
			},
		})
	}
	return ks
}

// t3FlatCloses 生成 n 个相同收盘价，用于预热边界构造。
func t3FlatCloses(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = 10
	}
	return out
}

func TestT3RefOperand(t *testing.T) {
	ks := t3CloseKlines(10, 11, 12, 13)
	in := NewInput("sh600000", ks, nil, nil)

	closePrice := ClosePrice()
	closeFloat := CloseFloat()

	// 正常路径：1 天前=昨收（12 元 = 12000 厘），3 天前=首根收（10 元）。
	ref1, err := RefOperand(1, closePrice)
	if err != nil {
		t.Fatalf("RefOperand(1) 构造失败: %v", err)
	}
	if got := ref1.Value(in); got.Kind() != KindPrice || got.Price() != 12000 {
		t.Fatalf("RefOperand(1, 今收厘) = %v, 期望昨收 12000", got)
	}
	ref3, err := RefOperand(3, closeFloat)
	if err != nil {
		t.Fatalf("RefOperand(3) 构造失败: %v", err)
	}
	if got := ref3.Value(in); got.Kind() != KindFloat || got.Float() != 10 {
		t.Fatalf("RefOperand(3, 今收元) = %v, 期望首根收 10", got)
	}

	// Name 可见历史口径。
	if ref1.Name() != "1天前·今收(厘)" {
		t.Fatalf("RefOperand Name = %q", ref1.Name())
	}

	// 越界：days>=n 截断为空前缀 → K 线操作数无效，不越界取数、不补 0。
	ref4, _ := RefOperand(4, closePrice)
	if got := ref4.Value(in); got.Valid() {
		t.Fatalf("days==n 应无效，实际 %v", got)
	}
	ref9, _ := RefOperand(9, closeFloat)
	if got := ref9.Value(in); got.Valid() {
		t.Fatalf("days>n 应无效，实际 %v", got)
	}

	// Literal 常量经历史引用仍有效（昨日阈值不因前缀不足失效）。
	refLit, _ := RefOperand(5, Literal(FloatValue(3.5)))
	if got := refLit.Value(in); got.Kind() != KindFloat || got.Float() != 3.5 {
		t.Fatalf("RefOperand(Literal) = %v, 期望 3.5", got)
	}

	// 与因子组合：昨日 MA(2) == MA(2) 对前 3 根求值。
	maRef, _ := RefOperand(1, MAValue(2))
	want := (11.0 + 12.0) / 2
	if got := maRef.Value(in); got.Kind() != KindFloat || math.Abs(got.Float()-want) > 1e-9 {
		t.Fatalf("RefOperand(1, MA(2)) = %v, 期望 %v", got, want)
	}

	// 单根前缀：Ref(1) 为空前缀 → 无效。
	one := NewInput("sh600000", ks[:1], nil, nil)
	if got := ref1.Value(one); got.Valid() {
		t.Fatalf("单根前缀 Ref(1) 应无效，实际 %v", got)
	}

	// 构造校验。
	if _, err := RefOperand(0, closePrice); err == nil {
		t.Fatal("days=0 应拒绝构造")
	}
	if _, err := RefOperand(-1, closePrice); err == nil {
		t.Fatal("days<0 应拒绝构造")
	}
	if _, err := RefOperand(1, nil); err == nil {
		t.Fatal("nil 操作数应拒绝构造")
	}
}

func TestT3MACDHistMatchesSeries(t *testing.T) {
	rng := rand.New(rand.NewSource(20261017))
	closes := make([]float64, 60)
	closes[0] = 10
	for i := 1; i < len(closes); i++ {
		closes[i] = closes[i-1] * (1 + rng.Float64()*0.06 - 0.028)
	}
	ks := t3CloseKlines(closes...)

	histOp := MACDHist(3, 6, 2)
	// 空前缀无效。
	if got := histOp.Value(NewInput("sh600000", nil, nil, nil)); got.Valid() {
		t.Fatalf("空前缀 MACDHist 应无效，实际 %v", got)
	}

	// 因果性 oracle：factor.MACD柱值.Series 一次算全序列，
	// 逐日断言 MACDHist 对截断前缀的求值与序列对应日一致。
	series := f.MACD柱值{Fast: 3, Slow: 6, Signal: 2}.Series(ks)
	if len(series) != len(ks) {
		t.Fatalf("Series 长度 %d != %d", len(series), len(ks))
	}
	for end := 1; end <= len(ks); end++ {
		got := histOp.Value(NewInput("sh600000", ks[:end], nil, nil))
		if got.Kind() != KindFloat {
			t.Fatalf("end=%d MACDHist 无效", end)
		}
		if got.Float() != series[end-1] {
			t.Fatalf("end=%d: 截断求值 %v != Series[%d] %v（因果性破坏）",
				end, got.Float(), end-1, series[end-1])
		}
	}
}

func TestT3WindowExtreme(t *testing.T) {
	// 收盘 10..17：含今日近 3 日收盘最大 = 17；不含今日 = 16。
	ks := t3CloseKlines(10, 11, 12, 13, 14, 15, 16, 17)
	in := NewInput("sh600000", ks, nil, nil)

	winMaxIn, err := WindowMax(3, CloseFloat(), true)
	if err != nil {
		t.Fatalf("WindowMax 构造失败: %v", err)
	}
	if got := winMaxIn.Value(in); got.Kind() != KindFloat || got.Float() != 17 {
		t.Fatalf("含今日近3日最大 = %v, 期望 17", got)
	}
	winMaxPrev, _ := WindowMax(3, CloseFloat(), false)
	if got := winMaxPrev.Value(in); got.Kind() != KindFloat || got.Float() != 16 {
		t.Fatalf("不含今日近3日最大 = %v, 期望 16", got)
	}
	winMinIn, _ := WindowMin(3, CloseFloat(), true)
	if got := winMinIn.Value(in); got.Kind() != KindFloat || got.Float() != 15 {
		t.Fatalf("含今日近3日最小 = %v, 期望 15", got)
	}
	winMinPrev, _ := WindowMin(3, CloseFloat(), false)
	if got := winMinPrev.Value(in); got.Kind() != KindFloat || got.Float() != 14 {
		t.Fatalf("不含今日近3日最小 = %v, 期望 14", got)
	}
	// Name 可见含/不含今日口径。
	if winMinPrev.Name() != "不含今日·近3日窗口最小·今收(元)" {
		t.Fatalf("WindowMin Name = %q", winMinPrev.Name())
	}

	// 钳制：窗口大于可用前缀时取实际可用范围（MACD 反转族口径）。
	// ks[:3] 收盘 10,11,12；不含今日窗口 5 日 → end=2 钳 start=0 → 覆盖收盘 10,11。
	small := NewInput("sh600000", ks[:3], nil, nil)
	winClamp, _ := WindowMax(5, CloseFloat(), false)
	if got := winClamp.Value(small); got.Kind() != KindFloat || got.Float() != 11 {
		t.Fatalf("钳制窗口最大 = %v, 期望 11", got)
	}

	// 窗口为空：空前缀、或不含今日且仅 1 根 → 无效。
	if got := winMaxIn.Value(NewInput("sh600000", nil, nil, nil)); got.Valid() {
		t.Fatalf("空前缀窗口极值应无效，实际 %v", got)
	}
	if got := winMaxPrev.Value(NewInput("sh600000", ks[:1], nil, nil)); got.Valid() {
		t.Fatalf("单根不含今日窗口应无效，实际 %v", got)
	}

	// 窗口内任一日无效 → 整体无效（不跳过）：预热不足段被窗口覆盖。
	// MA(30) 在 len<30 时无效 → n=32 含今日近 5 日窗口覆盖 len=28 → 整体无效。
	flat := t3CloseKlines(t3FlatCloses(32)...)
	maWin, _ := WindowMax(5, MAValue(30), true)
	if got := maWin.Value(NewInput("sh600000", flat, nil, nil)); got.Valid() {
		t.Fatalf("窗口含无效日应整体无效，实际 %v", got)
	}
	// 预热充足（n=40）后同一窗口有效。
	full := t3CloseKlines(t3FlatCloses(40)...)
	if got := maWin.Value(NewInput("sh600000", full, nil, nil)); !got.Valid() || got.Float() != 10 {
		t.Fatalf("预热充足窗口 = %v, 期望 10", got)
	}

	// 与旧扫描口径对拍：MACD hist 序列上，WindowMax(lb, MACDHist, false)
	// == max(hist[n-1-lb : n-1])（含钳制，对应 sell.MACD反转 窗口扫描）。
	rng := rand.New(rand.NewSource(42))
	closes := make([]float64, 50)
	closes[0] = 20
	for i := 1; i < len(closes); i++ {
		closes[i] = closes[i-1] * (1 + rng.Float64()*0.08 - 0.04)
	}
	hks := t3CloseKlines(closes...)
	histOp := MACDHist(3, 6, 2)
	hist := make([]float64, len(hks))
	for i := range hks {
		hist[i] = histOp.Value(NewInput("sh600000", hks[:i+1], nil, nil)).Float()
	}
	for _, lb := range []int{3, 20, 100} {
		winOp, _ := WindowMax(lb, histOp, false)
		got := winOp.Value(NewInput("sh600000", hks, nil, nil))
		start := len(hks) - 1 - lb
		if start < 0 {
			start = 0
		}
		want := hist[start]
		for _, v := range hist[start : len(hks)-1] {
			if v > want {
				want = v
			}
		}
		if got.Kind() != KindFloat || math.Abs(got.Float()-want) > 1e-12 {
			t.Fatalf("lb=%d: WindowMax=%v, 期望 %v", lb, got.Float(), want)
		}
	}

	// 构造校验。
	if _, err := WindowMin(0, CloseFloat(), true); err == nil {
		t.Fatal("days=0 应拒绝构造")
	}
	if _, err := WindowMax(1, nil, true); err == nil {
		t.Fatal("nil 操作数应拒绝构造")
	}
}

func TestT3GainPercent(t *testing.T) {
	// 收盘 10 →(5日)→ 12.5：涨幅 (12.5-10)/10*100 = 25。
	ks := t3CloseKlines(10, 10.5, 11, 11.5, 12, 12.5)
	in := NewInput("sh600000", ks, nil, nil)
	g5, err := GainPercent(5)
	if err != nil {
		t.Fatalf("GainPercent 构造失败: %v", err)
	}
	if got := g5.Value(in); got.Kind() != KindFloat || math.Abs(got.Float()-25) > 1e-9 {
		t.Fatalf("近5日涨幅 = %v, 期望 25", got)
	}
	if g5.Name() != "近5日累计涨幅%" {
		t.Fatalf("GainPercent Name = %q", g5.Name())
	}

	// 公式次序与旧 riseRateNDays 逐位一致（同输入直接对比手工展开式）。
	start := ks[0].Close.Float64()
	end := ks[5].Close.Float64()
	want := (end - start) / start * 100
	if got := g5.Value(in); got.Float() != want {
		t.Fatalf("公式次序不一致: got %v, want %v", got.Float(), want)
	}

	// 预热不足：len<days+1 → 无效。
	g6, _ := GainPercent(6)
	if got := g6.Value(in); got.Valid() {
		t.Fatalf("预热不足应无效，实际 %v", got)
	}
	// 起点收盘 <=0 → 无效（不除零、不补 0）。
	ks0 := t3CloseKlines(0, 10, 11, 12)
	in0 := NewInput("sh600000", ks0, nil, nil)
	g3, _ := GainPercent(3)
	if got := g3.Value(in0); got.Valid() {
		t.Fatalf("起点 0 应无效，实际 %v", got)
	}
	// 空前缀无效。
	if got := g5.Value(NewInput("sh600000", nil, nil, nil)); got.Valid() {
		t.Fatalf("空前缀应无效，实际 %v", got)
	}
	if _, err := GainPercent(0); err == nil {
		t.Fatal("days=0 应拒绝构造")
	}
}

func TestT3LowFloatAndSlopeOperands(t *testing.T) {
	ks := t3CloseKlines(10, 11, 12)
	in := NewInput("sh600000", ks, nil, nil)
	low := LowFloat()
	if got := low.Value(in); got.Kind() != KindFloat || got.Float() != 10 {
		t.Fatalf("今低 = %v, 期望 10", got)
	}
	if got := low.Value(NewInput("sh600000", nil, nil, nil)); got.Valid() {
		t.Fatalf("空前缀今低应无效，实际 %v", got)
	}

	// MASlope/MAIncre 委托 factor：预热不足（len<period+lookback）→ 无效。
	slope := MASlope(2, 2)
	if got := slope.Value(NewInput("sh600000", ks[:3], nil, nil)); got.Valid() {
		t.Fatalf("MA最弱日斜率预热不足应无效，实际 %v", got)
	}
	full := t3CloseKlines(10, 11, 12, 13, 14)
	inFull := NewInput("sh600000", full, nil, nil)
	if got := slope.Value(inFull); !got.Valid() {
		t.Fatalf("MA最弱日斜率预热充足应有效，实际 %v", got)
	}
	// 与 factor 对拍：数值与因子一致，单调上行序列均为正。
	wantSlope := f.MA最弱日斜率{Period: 2, Lookback: 2}.Value("sh600000", full)
	gotSlope := slope.Value(inFull)
	if gotSlope.Float() != wantSlope {
		t.Fatalf("MASlope=%v 与 factor %v 不一致", gotSlope.Float(), wantSlope)
	}
	if gotSlope.Float() <= 0 {
		t.Fatalf("单调上行斜率应>0，实际 %v", gotSlope.Float())
	}
	incr := MAIncre(2, 2)
	wantIncr := f.MA最弱日增量{Period: 2, Lookback: 2}.Value("sh600000", full)
	if got := incr.Value(inFull); got.Float() != wantIncr || got.Float() <= 0 {
		t.Fatalf("MAIncre=%v, 期望 %v 且 >0", got.Float(), wantIncr)
	}
	// 增量允许非正前值口径与因子一致（下跌序列：增量<=0 不判无效）。
	down := t3CloseKlines(14, 13, 12, 11, 10)
	wantDown := f.MA最弱日增量{Period: 2, Lookback: 2}.Value("sh600000", down)
	if got := incr.Value(NewInput("sh600000", down, nil, nil)); got.Float() != wantDown {
		t.Fatalf("下跌序列 MAIncre=%v, 期望 %v", got.Float(), wantDown)
	}

	// Name 委托因子名。
	if slope.Name() != fmt.Sprintf("均线最弱日斜率(%d,%d)", 2, 2) {
		t.Fatalf("MASlope Name = %q", slope.Name())
	}
}
