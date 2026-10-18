package rule

import (
	"math/rand"
	"testing"

	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// t3 时序规则测试：连续段/计数的逐日模式用前缀长度驱动的伪谓词精确
// 控制并断言天数边界；MACD 序列类与 factor 序列因子对拍；交叉用
// 收盘序列 + 阈值常量控制恰等边界。

// t3LenPredicate 前缀长度驱动的伪谓词：对截断前缀 len(dks) 返回
// decide 的结果，用于精确控制逐日满足模式。
type t3LenPredicate struct {
	decide func(n int) bool
}

func (p t3LenPredicate) Name() string { return "长度谓词" }

func (p t3LenPredicate) Eval(in Input) bool { return p.decide(len(in.Klines)) }

func TestT3ConsecutiveDayBoundaries(t *testing.T) {
	// 6 根 K 线；模式按前缀长度 1..6 给定：[T,T,T,F,T,T]。
	// 末尾连续段 = 2（长度 5、6）。
	ks := t3CloseKlines(10, 10, 10, 10, 10, 10)
	in := NewInput("sh600000", ks, nil, nil)
	pattern := map[int]bool{1: true, 2: true, 3: true, 4: false, 5: true, 6: true}
	pred := t3LenPredicate{decide: func(n int) bool { return pattern[n] }}

	cases := []struct {
		name         string
		min, max     int
		includeToday bool
		want         bool
	}{
		{"恰好达到下限2", 2, 0, true, true},
		{"下限3不足", 3, 0, true, false},
		{"中断后天数不累计", 5, 0, true, false},
		{"上限2恰等(末尾连续2天)", 1, 2, true, true},
		{"上限1被超(末尾连续2天)", 1, 1, true, false},
		{"不含今日窗口=长度1..5末段T,T", 1, 0, false, true},
		{"不含今日下限3不足", 3, 0, false, false},
	}
	for _, c := range cases {
		r, err := NewConsecutive(c.min, c.max, pred, c.includeToday)
		if err != nil {
			t.Fatalf("%s: 构造失败: %v", c.name, err)
		}
		if got := r.Eval(in); got != c.want {
			t.Fatalf("%s: Eval = %v, 期望 %v", c.name, got, c.want)
		}
	}

	// 恰等边界：末尾恰好连续 3 天（长度 4、5、6 成立）→ Min=3 真、Min=4 假。
	ks3 := t3CloseKlines(10, 10, 10, 10, 10, 10)
	pattern3 := map[int]bool{1: false, 2: false, 3: false, 4: true, 5: true, 6: true}
	pred3 := t3LenPredicate{decide: func(n int) bool { return pattern3[n] }}
	r3, _ := NewConsecutive(3, 0, pred3, true)
	if !r3.Eval(NewInput("sh600000", ks3, nil, nil)) {
		t.Fatal("恰好连续3天应成立")
	}
	r4, _ := NewConsecutive(4, 0, pred3, true)
	if r4.Eval(NewInput("sh600000", ks3, nil, nil)) {
		t.Fatal("连续3天不满足下限4")
	}

	// 含/不含今日：仅今日成立时，含今日 true、不含今日 false。
	onlyToday := t3LenPredicate{decide: func(n int) bool { return n == 6 }}
	rIn, _ := NewConsecutive(1, 0, onlyToday, true)
	rOut, _ := NewConsecutive(1, 0, onlyToday, false)
	if !rIn.Eval(in) || rOut.Eval(in) {
		t.Fatalf("含今日=%v 不含今日=%v, 期望 true/false", rIn.Eval(in), rOut.Eval(in))
	}

	// 空前缀 / 不含今日且单根 → false。
	if rIn.Eval(NewInput("sh600000", nil, nil, nil)) {
		t.Fatal("空前缀连续段应 false")
	}
	if rOut.Eval(NewInput("sh600000", ks[:1], nil, nil)) {
		t.Fatal("单根不含今日连续段应 false")
	}

	// 无跨调用缓存：同一规则实例两次求值各自从本次前缀重算（计数器
	// 反映逐日判定总次数，不误用旧值；切片地址/长度均不作缓存键）。
	mut := t3CloseKlines(10, 10, 10, 10, 10, 10)
	always := t3LenPredicate{decide: func(n int) bool { return true }}
	// 全真前缀连续 6 天：Min=3 Max=3 要求恰好 3 天 → 不成立；无上限 → 成立。
	rCapped, _ := NewConsecutive(3, 3, always, true)
	inMut := NewInput("sh600000", mut, nil, nil)
	if rCapped.Eval(inMut) {
		t.Fatal("全真前缀连续 6 天超出上限 3 应不成立")
	}
	rAlways, _ := NewConsecutive(3, 0, always, true)
	if !rAlways.Eval(inMut) {
		t.Fatal("全真前缀无上限应成立")
	}
	counter := 0
	counting := t3LenPredicate{decide: func(n int) bool { counter++; return true }}
	rCounting, _ := NewConsecutive(3, 0, counting, true)
	_ = rCounting.Eval(inMut)
	_ = rCounting.Eval(inMut)
	if counter != 6 { // 3(达到 MinDays 即停) + 3
		t.Fatalf("两次求值逐日判定次数 = %d, 期望 6（无跨调用缓存，每次从本次前缀重算）", counter)
	}

	// 构造校验。
	if _, err := NewConsecutive(0, 0, pred, true); err == nil {
		t.Fatal("MinDays=0 应拒绝构造")
	}
	if _, err := NewConsecutive(2, 1, pred, true); err == nil {
		t.Fatal("MaxDays<MinDays 应拒绝构造（上限不限请传 0）")
	}
	if _, err := NewConsecutive(2, 0, nil, true); err == nil {
		t.Fatal("nil 谓词应拒绝构造")
	}
	// MaxDays == MinDays 合法。
	if _, err := NewConsecutive(3, 3, pred, true); err != nil {
		t.Fatalf("MaxDays==MinDays 应可构造: %v", err)
	}
}

func TestT3ConsecutiveParityWithFactors(t *testing.T) {
	// 与 factor.MA上方/下方连续天数 对拍：同一收盘序列、period=3、
	// days=1..6，内核连续段判定与旧序列因子判定逐一一致。
	closes := []float64{10, 10.5, 11, 10.2, 9.8, 10.4, 10.8, 10.9, 10.7, 11.2}
	ks := t3CloseKlines(closes...)
	in := NewInput("sh600000", ks, nil, nil)
	const period = 3
	aboveCmp := mustCompare(t, CloseFloat(), Gt, MAValue(period))
	belowCmp := mustCompare(t, CloseFloat(), Le, MAValue(period))
	for days := 1; days <= 6; days++ {
		wantAbove := f.MA上方连续天数{Period: period, Days: days}.Value("sh600000", ks) >= float64(days)
		wantBelow := f.MA下方连续天数{Period: period, Days: days}.Value("sh600000", ks) >= float64(days)
		rA, _ := NewConsecutive(days, 0, aboveCmp, true)
		rB, _ := NewConsecutive(days, 0, belowCmp, true)
		if got := rA.Eval(in); got != wantAbove {
			t.Fatalf("days=%d: 上方连续段=%v, factor=%v", days, got, wantAbove)
		}
		if got := rB.Eval(in); got != wantBelow {
			t.Fatalf("days=%d: 下方连续段=%v, factor=%v", days, got, wantBelow)
		}
	}

	// 与 factor.NegativeStreak/PositiveStreak/RisingStreak 对拍：
	// MACD hist 序列上的连续段（含今日）与序列因子一致。随机游走按种子
	// 搜索末尾负值连续段 >=2 的序列，保证 MaxDays 上限分支可覆盖。
	histOp := MACDHist(3, 6, 2)
	var mks extend.Klines
	var series []float64
	streak := 0
	for seed := int64(1); seed <= 200; seed++ {
		rng := rand.New(rand.NewSource(seed))
		closesMacd := make([]float64, 45)
		closesMacd[0] = 15
		for i := 1; i < len(closesMacd); i++ {
			closesMacd[i] = closesMacd[i-1] * (1 + rng.Float64()*0.07 - 0.033)
		}
		cand := t3CloseKlines(closesMacd...)
		candSeries := f.MACD柱值{Fast: 3, Slow: 6, Signal: 2}.Series(cand)
		if s := f.NegativeStreak(candSeries); s >= 2 {
			mks, series, streak = cand, candSeries, s
			break
		}
	}
	if streak < 2 {
		t.Fatal("200 个种子内未找到末尾负值连续段 >=2 的夹具")
	}
	min := NewInput("sh600000", mks, nil, nil)

	ltZero, _ := NewCompare(histOp, Lt, Literal(FloatValue(0)))
	gtZero, _ := NewCompare(histOp, Gt, Literal(FloatValue(0)))
	histRef, errRef := RefOperand(1, histOp)
	if errRef != nil {
		t.Fatalf("RefOperand 构造失败: %v", errRef)
	}
	rising, _ := NewCompare(histOp, Gt, histRef)

	for days := 1; days <= 8; days++ {
		neg, _ := NewConsecutive(days, 0, ltZero, true)
		pos, _ := NewConsecutive(days, 0, gtZero, true)
		rise, _ := NewConsecutive(days, 0, rising, true)
		if got, want := neg.Eval(min), f.NegativeStreak(series) >= days; got != want {
			t.Fatalf("days=%d: 负值连续段=%v, NegativeStreak=%v", days, got, want)
		}
		if got, want := pos.Eval(min), f.PositiveStreak(series) >= days; got != want {
			t.Fatalf("days=%d: 正值连续段=%v, PositiveStreak=%v", days, got, want)
		}
		if got, want := rise.Eval(min), f.RisingStreak(series) >= days; got != want {
			t.Fatalf("days=%d: 上升连续段=%v, RisingStreak=%v", days, got, want)
		}
	}

	// MaxDays 上限对拍：实际 streak 超过 max 时不成立（streak 已由种子
	// 搜索保证 >=2）。
	negMax, _ := NewConsecutive(1, streak-1, ltZero, true)
	if negMax.Eval(min) {
		t.Fatalf("streak=%d 上限 %d 应不成立", streak, streak-1)
	}
	negEq, _ := NewConsecutive(1, streak, ltZero, true)
	if !negEq.Eval(min) {
		t.Fatalf("streak=%d 上限恰等应成立", streak)
	}
}

// mustCompare 测试辅助：构造比较谓词，失败直接终止测试。
func mustCompare(t *testing.T, left Operand, op Op, right Operand) Compare {
	t.Helper()
	c, err := NewCompare(left, op, right)
	if err != nil {
		t.Fatalf("构造比较谓词失败: %v", err)
	}
	return c
}

func TestT3CountWindow(t *testing.T) {
	// 5 根 K 线，窗口 4 天不含今日 → 覆盖前缀长度 1..4。
	ks := t3CloseKlines(10, 10, 10, 10, 10)
	in := NewInput("sh600000", ks, nil, nil)
	pattern := map[int]bool{1: false, 2: true, 3: false, 4: true, 5: false}
	pred := t3LenPredicate{decide: func(n int) bool { return pattern[n] }}

	cases := []struct {
		name         string
		days, minCnt int
		includeToday bool
		want         bool
	}{
		{"不含今日满足2达到下限", 4, 2, false, true},
		{"不含今日下限3不足", 4, 3, false, false},
		{"含今日满足2（长度5为false）", 4, 2, true, true},
		{"存在性判定 MinCount=1", 4, 1, false, true},
	}
	for _, c := range cases {
		r, err := NewCountWindow(c.days, c.minCnt, pred, c.includeToday)
		if err != nil {
			t.Fatalf("%s: 构造失败: %v", c.name, err)
		}
		if got := r.Eval(in); got != c.want {
			t.Fatalf("%s: Eval = %v, 期望 %v", c.name, got, c.want)
		}
	}

	// 钳制：窗口 5 天不含今日但前缀仅 3 根 → 覆盖前缀长度 1..2，其中 1 个 true。
	small := NewInput("sh600000", ks[:3], nil, nil)
	clampPred := t3LenPredicate{decide: func(n int) bool { return n == 2 }}
	rClamp, _ := NewCountWindow(5, 1, clampPred, false)
	if !rClamp.Eval(small) {
		t.Fatal("钳制窗口内 1 天满足应成立")
	}
	rClamp2, _ := NewCountWindow(5, 2, clampPred, false)
	if rClamp2.Eval(small) {
		t.Fatal("钳制窗口内仅 1 天满足不满足下限 2")
	}

	// 窗口为空：空前缀、或不含今日且单根 → false。
	rAny, _ := NewCountWindow(3, 1, t3LenPredicate{decide: func(int) bool { return true }}, false)
	if rAny.Eval(NewInput("sh600000", nil, nil, nil)) {
		t.Fatal("空前缀计数应 false")
	}
	if rAny.Eval(NewInput("sh600000", ks[:1], nil, nil)) {
		t.Fatal("单根不含今日计数应 false")
	}

	// 真实触及谓词对拍 A回踩N日均线 条件3口径：
	// 近 touchDays 天（不含今日）至少 1 天 最低价<=当日均线（逐日重算）。
	// 收盘 [12,11,10,11]，Low=收-2：
	//   前缀2 (12,11): MA2=11.5, 低 9 ≤ 11.5 → true
	//   前缀3 (12,11,10): MA2=10.5, 低 8 ≤ 10.5 → true
	//   前缀4 (含今日, 10,11): MA2=10.5, 低 9 ≤ 10.5 → true
	touchKs := t3CloseKlines(12, 11, 10, 11)
	touchPred := mustCompare(t, LowFloat(), Le, MAValue(2))
	touch2, _ := NewCountWindow(3, 2, touchPred, false)
	touch3, _ := NewCountWindow(3, 3, touchPred, false)
	touchIn := NewInput("sh600000", touchKs, nil, nil)
	if !touch2.Eval(touchIn) {
		t.Fatal("触及计数: 窗口3天满足2天应成立（首日均线预热不足不计）")
	}
	if touch3.Eval(touchIn) {
		t.Fatal("触及计数: 窗口3天满足2天不满足下限3")
	}
	// 无触及场景：低点始终高于当日均线（低点抬到最高价）。
	highLowKs := make(extend.Klines, 0, 4)
	for _, c := range []float64{12, 11, 10, 11} {
		k := t3CloseKlines(c)[0]
		k.Low = k.High // Low > 当日均线 恒成立
		highLowKs = append(highLowKs, k)
	}
	if touch3.Eval(NewInput("sh600000", highLowKs, nil, nil)) {
		t.Fatal("无触及场景计数应 false")
	}

	// 构造校验。
	if _, err := NewCountWindow(0, 1, pred, true); err == nil {
		t.Fatal("days=0 应拒绝构造")
	}
	if _, err := NewCountWindow(3, 0, pred, true); err == nil {
		t.Fatal("minCount=0 应拒绝构造")
	}
	if _, err := NewCountWindow(3, 4, pred, true); err == nil {
		t.Fatal("minCount>days 应拒绝构造")
	}
	if _, err := NewCountWindow(3, 1, nil, true); err == nil {
		t.Fatal("nil 谓词应拒绝构造")
	}
}

func TestT3MinKlinesAndRuleFunc(t *testing.T) {
	mk, err := NewMinKlines(3)
	if err != nil {
		t.Fatalf("NewMinKlines 构造失败: %v", err)
	}
	ks := t3CloseKlines(10, 10, 10, 10)
	if !mk.Eval(NewInput("sh600000", ks[:3], nil, nil)) {
		t.Fatal("len==minLen 应成立")
	}
	if mk.Eval(NewInput("sh600000", ks[:2], nil, nil)) {
		t.Fatal("len<minLen 应不成立")
	}
	if mk.Name() != "K线≥3根" {
		t.Fatalf("MinKlines Name = %q", mk.Name())
	}
	if _, err := NewMinKlines(0); err == nil {
		t.Fatal("minLen=0 应拒绝构造")
	}

	var seenLen int
	fn, err := NewRuleFunc("形态判定", func(in Input) bool {
		seenLen = len(in.Klines)
		return seenLen >= 3
	})
	if err != nil {
		t.Fatalf("NewRuleFunc 构造失败: %v", err)
	}
	if !fn.Eval(NewInput("sh600000", ks, nil, nil)) || seenLen != 4 {
		t.Fatalf("RuleFunc 收到前缀长度 %d, 期望 4", seenLen)
	}
	if fn.Eval(NewInput("sh600000", ks[:2], nil, nil)) {
		t.Fatal("RuleFunc 前缀不足应 false")
	}
	if _, err := NewRuleFunc("", func(Input) bool { return true }); err == nil {
		t.Fatal("空名称应拒绝构造")
	}
	if _, err := NewRuleFunc("x", nil); err == nil {
		t.Fatal("nil 函数应拒绝构造")
	}
}

func TestT3CrossRules(t *testing.T) {
	// a=今收（元），b=阈值常量：四组恰等口径逐一验证。
	// 昨日 a 经 RefOperand(1) 对截断前缀求值。
	build := func(t *testing.T, up, todayInclusive bool, a Operand, b Operand) Rule {
		t.Helper()
		var r And
		var err error
		if up {
			r, err = CrossUp(a, b, todayInclusive)
		} else {
			r, err = CrossDown(a, b, todayInclusive)
		}
		if err != nil {
			t.Fatalf("交叉构造失败: %v", err)
		}
		return r
	}
	lit := func(v float64) Operand { return Literal(FloatValue(v)) }

	// 上穿：昨 10 <= 10.5，今 11 > 10.5。
	upKS := t3CloseKlines(10, 11)
	upIn := NewInput("sh600000", upKS, nil, nil)
	if !build(t, true, false, CloseFloat(), lit(10.5)).Eval(upIn) {
		t.Fatal("上穿(恰等昨侧) 昨<=阈值 且 今>阈值 应成立")
	}
	// 恰等在昨侧：今 == 阈值 → CrossUp(false) 不成立。
	eqKS := t3CloseKlines(10, 10.5)
	eqIn := NewInput("sh600000", eqKS, nil, nil)
	if build(t, true, false, CloseFloat(), lit(10.5)).Eval(eqIn) {
		t.Fatal("上穿(恰等昨侧) 今==阈值 不应成立")
	}
	// 恰等在今侧：昨 < 阈值 且 今 == 阈值 → CrossUp(true) 成立。
	if !build(t, true, true, CloseFloat(), lit(10.5)).Eval(eqIn) {
		t.Fatal("上穿(恰等今侧) 今==阈值 应成立")
	}
	// 恰等在昨侧的反例：昨 == 阈值 → CrossUp(true) 不成立。
	if build(t, true, true, CloseFloat(), lit(10)).Eval(upIn) {
		t.Fatal("上穿(恰等今侧) 昨==阈值 不应成立")
	}

	// 下穿：昨 11 >= 10.5，今 10 < 10.5。
	dnKS := t3CloseKlines(11, 10)
	dnIn := NewInput("sh600000", dnKS, nil, nil)
	if !build(t, false, false, CloseFloat(), lit(10.5)).Eval(dnIn) {
		t.Fatal("下穿(恰等昨侧) 昨>=阈值 且 今<阈值 应成立")
	}
	// 恰等在昨侧：昨 == 阈值 → CrossDown(false) 成立。
	dnEqKS := t3CloseKlines(10.5, 10)
	dnEqIn := NewInput("sh600000", dnEqKS, nil, nil)
	if !build(t, false, false, CloseFloat(), lit(10.5)).Eval(dnEqIn) {
		t.Fatal("下穿(恰等昨侧) 昨==阈值 应成立")
	}
	// 恰等在今侧：今 == 阈值 → CrossDown(true) 成立；CrossDown(false) 不成立。
	dnTodayKS := t3CloseKlines(10.6, 10.5)
	dnTodayIn := NewInput("sh600000", dnTodayKS, nil, nil)
	if !build(t, false, true, CloseFloat(), lit(10.5)).Eval(dnTodayIn) {
		t.Fatal("下穿(恰等今侧) 今==阈值 应成立")
	}
	if build(t, false, false, CloseFloat(), lit(10.5)).Eval(dnTodayIn) {
		t.Fatal("下穿(恰等昨侧) 今==阈值 不应成立")
	}

	// 两序列交叉：a=今收，b=MA(2)。收盘 [10, 8, 12]：
	//   昨: 8 <= MA2(前两根)=9；今: 12 > MA2=10 → 上穿成立。
	twoKS := t3CloseKlines(10, 8, 12)
	twoIn := NewInput("sh600000", twoKS, nil, nil)
	twoCross, err := CrossUp(CloseFloat(), MAValue(2), false)
	if err != nil {
		t.Fatalf("两序列交叉构造失败: %v", err)
	}
	if !twoCross.Eval(twoIn) {
		t.Fatal("收盘上穿 MA(2) 应成立")
	}

	// 预热不足：单根前缀昨日侧无效 → 不触发。
	if build(t, true, false, CloseFloat(), lit(0)).Eval(NewInput("sh600000", upKS[:1], nil, nil)) {
		t.Fatal("单根前缀交叉应不触发")
	}
	// 无效值不触发：b 为无效值操作数。
	invalidOp := OperandFunc("恒无效", func(Input) Value { return InvalidValue() })
	if build(t, true, false, CloseFloat(), invalidOp).Eval(upIn) {
		t.Fatal("b 无效时交叉应不触发")
	}

	// 构造校验：nil 操作数沿 RefOperand 传导。
	if _, err := CrossUp(nil, lit(1), false); err == nil {
		t.Fatal("nil a 应拒绝构造")
	}
	if _, err := CrossUp(CloseFloat(), nil, false); err == nil {
		t.Fatal("nil b 应拒绝构造")
	}
}
