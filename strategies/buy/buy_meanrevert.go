package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/rule"
)

// ----------------------------------------------------------------------------
// 1. A布林下轨 — 收盘价跌破布林下轨买入（最经典的均值回归入场）
// ----------------------------------------------------------------------------

// A布林下轨 是布林带下轨回归买入策略。
// Period 表示布林带计算周期，默认 20。
// StdTimes 表示标准差倍数，默认 2。
// 触发条件：最新收盘价 <= 布林下轨（mid - StdTimes*std）。
// 逻辑：价格偏离均值超过 StdTimes 个标准差，预期回归中轨。
type A布林下轨 struct {
	Period   int
	StdTimes float64
}

func (s A布林下轨) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	st := s.StdTimes
	if st == 0 {
		st = 2
	}
	return fmt.Sprintf("布林%d下轨%.0fσ", p, st)
}

func (s A布林下轨) Buy(code string, dks extend.Klines) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	st := s.StdTimes
	if st == 0 {
		st = 2
	}
	// 判断委托共用内核：今收（浮点元）<= 布林下轨。数据不足或 σ=0 时
	// 下轨为无效值，比较不成立，与旧 NaN 比较为 false 的行为一致。
	c, err := rule.NewCompare(rule.CloseFloat(), rule.Le, rule.BollLower(p, st))
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, nil))
}

// ----------------------------------------------------------------------------
// 2. A乖离超卖 — 乖离率 BIAS 偏离过大买入
// ----------------------------------------------------------------------------

// A乖离超卖 是乖离率回归买入策略。
// Period 表示均线周期，默认 20。
// MinBias 表示最低乖离率（%），默认 -7，即 (Close-MA)/MA*100 <= -7 时买入。
// 逻辑：价格相对均线负偏离超过阈值，预期向均线回归。
type A乖离超卖 struct {
	Period  int
	MinBias float64
}

func (s A乖离超卖) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	mb := s.MinBias
	if mb == 0 {
		mb = -7
	}
	return fmt.Sprintf("乖离%d日<%.1f%%", p, mb)
}

func (s A乖离超卖) Buy(code string, dks extend.Klines) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	mb := s.MinBias
	if mb == 0 {
		mb = -7
	}
	// 判断委托共用内核，与旧实现同序：先均线守卫（MA>0，数据不足或
	// 零值均线时无效值不成立，等价旧 ma<=0 提前返回），再乖离比较。
	// 乖离率经 factor.Build("ma_bias") 的 (close/ma-1)*100 口径，与旧
	// (close-ma)/ma*100 在恰等边界存在 1-ULP 舍入差异，快照已覆盖
	// 本批恰等夹具（新旧判定一致）。
	guard, err := rule.NewCompare(rule.MAValue(p), rule.Gt, rule.Literal(rule.FloatValue(0)))
	if err != nil {
		return false
	}
	main, err := rule.NewCompare(rule.BiasPercent(p), rule.Le, rule.Literal(rule.FloatValue(mb)))
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(guard, main)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, nil))
}

// ----------------------------------------------------------------------------
// 3. AZScore超卖 — 标准化偏离买入
// ----------------------------------------------------------------------------

// AZScore超卖 是 Z-Score 偏离买入策略。
// Period 表示统计窗口，默认 20。
// MinZ 表示最低 Z 值，默认 -2，即 Z <= -2 时买入。
// Z = (Close - MA) / Std，衡量价格偏离均值的标准化程度。
// 逻辑：Z < -2 表示价格偏离均值超过 2 倍标准差，预期回归。
type AZScore超卖 struct {
	Period int
	MinZ   float64
}

func (s AZScore超卖) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	mz := s.MinZ
	if mz == 0 {
		mz = -2
	}
	return fmt.Sprintf("ZScore%d<%.1f", p, mz)
}

func (s AZScore超卖) Buy(code string, dks extend.Klines) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	mz := s.MinZ
	if mz == 0 {
		mz = -2
	}
	// 判断委托共用内核：Z 值 <= 阈值。数据不足或 σ=0 时 Z 为无效值
	// 不成立，与旧 NaN 比较为 false 的行为一致。
	c, err := rule.NewCompare(rule.ZScore(p), rule.Le, rule.Literal(rule.FloatValue(mz)))
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, nil))
}

// ----------------------------------------------------------------------------
// 4. A唐奇安下沿 — 触及通道下沿反转买入
// ----------------------------------------------------------------------------

// A唐奇安下沿 是唐奇安通道下沿回归买入策略。
// Period 表示通道周期，默认 20。
// 触发条件：最新收盘价 <= 近 Period 日最低价（触及通道下沿）。
// 逻辑：价格跌至近期区间底部，均值回归预期反弹至通道中轨。
type A唐奇安下沿 struct {
	Period int
}

func (s A唐奇安下沿) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	return fmt.Sprintf("唐奇安%d日下沿", p)
}

func (s A唐奇安下沿) Buy(code string, dks extend.Klines) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	// 判断委托共用内核：今收（整数厘）<= 前 p 日最低价。LowestPrice
	// 窗口不含今日（dks[:n-1]，台账风险 §5.5 买入侧口径），避免恒真；
	// 数据不足时下沿为无效值不成立，与旧预检查为 false 一致。
	c, err := rule.NewCompare(rule.ClosePrice(), rule.Le, rule.LowestPrice(p, false))
	if err != nil {
		return false
	}
	return c.Eval(rule.NewInput(code, dks, nil, nil))
}

// ----------------------------------------------------------------------------
// 5. A布林下轨缩量 — 布林下轨 + 地量确认（双重过滤，降低假信号）
// ----------------------------------------------------------------------------

// A布林下轨缩量 是布林带下轨配合缩量确认的买入策略。
// Period 表示布林带周期，默认 20。
// StdTimes 标准差倍数，默认 2。
// VolDays 成交量均量天数，默认 5。
// VolRatio 今日量 <= VolDays 均量的比例，默认 0.6（地量）。
// 逻辑：下轨偏离 + 地量说明卖盘枯竭，回归概率更高。
type A布林下轨缩量 struct {
	Period   int
	StdTimes float64
	VolDays  int
	VolRatio float64
}

func (s A布林下轨缩量) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	vr := s.VolRatio
	if vr == 0 {
		vr = 0.6
	}
	return fmt.Sprintf("布林%d下轨+地量%.0f%%", p, vr*100)
}

func (s A布林下轨缩量) Buy(code string, dks extend.Klines) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	st := s.StdTimes
	if st == 0 {
		st = 2
	}
	vd := s.VolDays
	if vd == 0 {
		vd = 5
	}
	vr := s.VolRatio
	if vr == 0 {
		vr = 0.6
	}
	// 判断委托共用内核，与旧实现同序短路：先下轨（今收 <= 布林下轨，
	// 失败即整体不成立），再地量（今日量 <= 前vd日均量(不含今日)×vr，
	// 含边界）。基准均量缺失或 <=0 时右操作数为无效值不成立，等价旧
	// avgVol<=0 提前返回；len<p、len<vd+1 由两个操作数自身的无效值兜住。
	lower, err := rule.NewCompare(rule.CloseFloat(), rule.Le, rule.BollLower(p, st))
	if err != nil {
		return false
	}
	quiet, err := rule.NewCompare(rule.Volume(), rule.Le, rule.PrevAvgVolumeScaled(vd, vr))
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(lower, quiet)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, nil))
}

// ----------------------------------------------------------------------------
// 6. A布林下轨RSI超卖 — 布林下轨 + RSI 超卖（双重确认）
// ----------------------------------------------------------------------------

// A布林下轨RSI超卖 是布林带下轨与 RSI 超卖的双重确认买入策略。
// Period 布林带周期，默认 20。
// StdTimes 标准差倍数，默认 2。
// RSIPeriod RSI 周期，默认 14。
// RSIThreshold RSI 买入阈值，默认 30。
// 逻辑：价格在下轨且 RSI 超卖，两个独立信号共振，假突破概率更低。
type A布林下轨RSI超卖 struct {
	Period       int
	StdTimes     float64
	RSIPeriod    int
	RSIThreshold float64
}

func (s A布林下轨RSI超卖) Name() string {
	p := s.Period
	if p == 0 {
		p = 20
	}
	rt := s.RSIThreshold
	if rt == 0 {
		rt = 30
	}
	return fmt.Sprintf("布林%d下轨+RSI<%.0f", p, rt)
}

func (s A布林下轨RSI超卖) Buy(code string, dks extend.Klines) bool {
	p := s.Period
	if p == 0 {
		p = 20
	}
	st := s.StdTimes
	if st == 0 {
		st = 2
	}
	rp := s.RSIPeriod
	if rp == 0 {
		rp = 14
	}
	rt := s.RSIThreshold
	if rt == 0 {
		rt = 30
	}
	// 判断委托共用内核，与旧实现同序短路：先下轨（今收 <= 布林下轨，
	// 失败即整体不成立），再 RSI 超卖（浮点 RSI <= 阈值，含边界）。
	// RSI 数据不足时右操作数为无效值不成立，等价旧 len<rp+1 预检查。
	lower, err := rule.NewCompare(rule.CloseFloat(), rule.Le, rule.BollLower(p, st))
	if err != nil {
		return false
	}
	oversold, err := rule.NewCompare(rule.RSIFloat(rp), rule.Le, rule.Literal(rule.FloatValue(rt)))
	if err != nil {
		return false
	}
	and, err := rule.NewAnd(lower, oversold)
	if err != nil {
		return false
	}
	return and.Eval(rule.NewInput(code, dks, nil, nil))
}
