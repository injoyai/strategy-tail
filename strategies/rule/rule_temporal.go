package rule

import (
	"fmt"
	"strconv"
)

// 时序规则（迁移计划 T3 前置）：附录 B 19 项时序组件所需的窗口序列
// 判定、前缀长度门槛、规则级逃逸口与交叉便捷组合。
//
// 硬约束（沿用 predicate.go）：
//   - Rule 级原语不做算术泛化、无 NOT（计划 3.3/3.4）；
//   - 逐日判定每次求值从本次 Input 的前缀重新计算（truncatedInput 只
//     收窄切片），无任何跨调用缓存；对末根 K 线原地变化后重新求值，
//     结果随之更新，不误用旧值；
//   - 窗口是否含今日显式声明（IncludeToday）并在 Name 中可见
//     （windowName）；预热不足/越界一律不触发，不补 0。

// Consecutive 连续段规则：从窗口末端（含/不含今日由 IncludeToday 声明）
// 逐日对截断前缀求值谓词，统计连续成立天数；任一日不成立（含数据无效
// 导致谓词不触发）即中断；天数落在 [MinDays, MaxDays] 才成立
// （MaxDays<=0 表示上限不限——旧组件 MaxDays<MinDays 视为"不限"的归一
// 由门面完成，内核不做隐式退化）。
//
// 与旧序列因子对拍约定：factor.NegativeStreak/PositiveStreak/RisingStreak
// 与 maPositionStreak（factor/temporal.go）均为"末尾连续满足、失败即断"，
// NaN/零值对 Lt/Gt 谓词同样中断，语义一致；统计只进行到判定所需长度
// （无上限时达到 MinDays 即停），不影响判定结果。
type Consecutive struct {
	Predicate    Rule
	MinDays      int
	MaxDays      int // <=0 表示上限不限
	IncludeToday bool
}

// NewConsecutive 构造连续段规则，构造期校验谓词与天数边界。
func NewConsecutive(minDays, maxDays int, predicate Rule, includeToday bool) (Consecutive, error) {
	if predicate == nil {
		return Consecutive{}, fmt.Errorf("连续段规则的谓词不能为空")
	}
	if minDays <= 0 {
		return Consecutive{}, fmt.Errorf("连续段最少天数必须为正，得到 %d", minDays)
	}
	if maxDays > 0 && maxDays < minDays {
		return Consecutive{}, fmt.Errorf("连续段最多天数 %d 不能小于最少天数 %d（上限不限请传 0）", maxDays, minDays)
	}
	return Consecutive{Predicate: predicate, MinDays: minDays, MaxDays: maxDays, IncludeToday: includeToday}, nil
}

func (c Consecutive) Name() string {
	limit := strconv.Itoa(c.MinDays)
	if c.MaxDays > 0 {
		limit += "~" + strconv.Itoa(c.MaxDays)
	}
	return windowName(c.IncludeToday, "连续"+limit+"天·"+c.Predicate.Name())
}

func (c Consecutive) Eval(in Input) bool {
	if c.Predicate == nil {
		return false
	}
	end := len(in.Klines)
	if !c.IncludeToday {
		end--
	}
	// 统计上限：判定所需的最长统计长度（有上限时多统计一天以区分
	// count==MaxDays 与 count>MaxDays；无上限时达到 MinDays 即可判定）。
	limit := c.MinDays
	if c.MaxDays > 0 {
		limit = c.MaxDays + 1
	}
	count := 0
	for i := end - 1; i >= 0 && count < limit; i-- {
		if !c.Predicate.Eval(truncatedInput(in, i+1)) {
			break
		}
		count++
	}
	if count < c.MinDays {
		return false
	}
	if c.MaxDays > 0 && count > c.MaxDays {
		return false
	}
	return true
}

// CountWindow 窗口计数规则：近 Days 天（是否含今日由 IncludeToday 声明）
// 中满足谓词的天数 >= MinCount 才成立；MinCount=1 即存在性判定
// （如 A回踩N日均线 条件3"近 MinTouchDays 天（不含今日）至少 1 天最低价
// 触及当日均线"，buy_pullback_ma.go:84-100）。
// 窗口左端越过前缀起点时钳制到 0（与旧组件 windowStart<0 钳 0 的扫描
// 口径一致，配合门面的 MinKlines 预热拦截后通常不可达）；窗口为空
// 返回 false。逐日截断求值同 Consecutive，无跨调用缓存。
type CountWindow struct {
	Predicate    Rule
	Days         int
	MinCount     int
	IncludeToday bool
}

// NewCountWindow 构造窗口计数规则，构造期校验谓词与计数边界。
func NewCountWindow(days, minCount int, predicate Rule, includeToday bool) (CountWindow, error) {
	if predicate == nil {
		return CountWindow{}, fmt.Errorf("窗口计数规则的谓词不能为空")
	}
	if days <= 0 {
		return CountWindow{}, fmt.Errorf("窗口计数天数必须为正，得到 %d", days)
	}
	if minCount <= 0 {
		return CountWindow{}, fmt.Errorf("窗口最少满足天数必须为正，得到 %d", minCount)
	}
	if minCount > days {
		return CountWindow{}, fmt.Errorf("窗口最少满足天数 %d 不能大于窗口天数 %d", minCount, days)
	}
	return CountWindow{Predicate: predicate, Days: days, MinCount: minCount, IncludeToday: includeToday}, nil
}

func (c CountWindow) Name() string {
	return windowName(c.IncludeToday,
		fmt.Sprintf("近%d天满足≥%d天·%s", c.Days, c.MinCount, c.Predicate.Name()))
}

func (c CountWindow) Eval(in Input) bool {
	if c.Predicate == nil {
		return false
	}
	end := len(in.Klines)
	if !c.IncludeToday {
		end--
	}
	start := end - c.Days
	if start < 0 {
		start = 0
	}
	if end <= start {
		return false
	}
	count := 0
	for i := start; i < end; i++ {
		if c.Predicate.Eval(truncatedInput(in, i+1)) {
			count++
		}
	}
	return count >= c.MinCount
}

// MinKlines 前缀长度门槛规则：可见 K 线不足 MinLen 根时不触发。
// 显式表达旧组件的长度拦截：其一是 MACD 族的 n<slow+signal 预热门槛
// （MACDHistogram 对短前缀仍给出有限值，无效值兜不住，必须显式拦截）；
// 其二是"Or 结构中单侧因子预热即成立"的场景——LongMADown 先拦
// len<250+lookback（sell_ma.go:28-30），避免 250 日侧无法判断而
// 60 日侧已成立时误触发（台账风险 §13）。
type MinKlines struct{ MinLen int }

// NewMinKlines 构造前缀长度门槛规则。
func NewMinKlines(minLen int) (MinKlines, error) {
	if minLen <= 0 {
		return MinKlines{}, fmt.Errorf("K线门槛必须为正，得到 %d", minLen)
	}
	return MinKlines{MinLen: minLen}, nil
}

func (m MinKlines) Name() string { return "K线≥" + strconv.Itoa(m.MinLen) + "根" }

func (m MinKlines) Eval(in Input) bool { return len(in.Klines) >= m.MinLen }

// ruleFunc 规则级逃逸口（与 OperandFunc 对称）：为高度定制的形态判定
// （如 A底部抬升 的枢轴低点扫描——前后各 window 根均更高的双低点结构，
// 无跨组件研究意义、不宜泛化为窗口原语）提供受控入口。fn 在每次求值
// 时调用，必须只读 Input、无副作用、无跨调用缓存。
type ruleFunc struct {
	name string
	fn   func(Input) bool
}

// NewRuleFunc 构造规则级逃逸口；name 不能为空，fn 不能为 nil。
func NewRuleFunc(name string, fn func(Input) bool) (Rule, error) {
	if name == "" {
		return nil, fmt.Errorf("规则逃逸口名称不能为空")
	}
	if fn == nil {
		return nil, fmt.Errorf("规则逃逸口的函数不能为 nil")
	}
	return ruleFunc{name: name, fn: fn}, nil
}

func (r ruleFunc) Name() string { return r.name }

func (r ruleFunc) Eval(in Input) bool { return r.fn(in) }

// CrossUp 上穿便捷组合：a 从 b 下方穿到上方的两日状态对。
// 返回 And 组合（非新 Rule 类型）——交叉完全可由 Compare+RefOperand
// 表达，此处只为统一恰等口径的文档化；昨日两侧均经 RefOperand(1, ..)
// 对截断前缀求值，昨日任一侧无效则不触发。b 可为阈值常量（Literal），
// 昨日 b 即同值常量。恰等边界按组件口径显式声明（台账附录 B，四种
// 口径并存，不得统一）：
//   - MACD金叉（buy_trend_volume.go:199）：昨柱<=0 且 今柱>0
//     → CrossUp(a, b, false)（恰等在昨日侧）；
//   - RSI超卖回升（buy_trend_volume.go:173）：昨RSI<t 且 今RSI>=t
//     → CrossUp(a, b, true)（恰等在今日侧）。
func CrossUp(a, b Operand, todayInclusive bool) (And, error) {
	return crossRule(a, b, todayInclusive, true)
}

// CrossDown 下穿便捷组合：a 从 b 上方穿到下方，恰等口径同 CrossUp：
//   - 均线死叉（sell_bluechip_pullback.go:131-139）：昨短>=长 且 今短<长
//     → CrossDown(a, b, false)（恰等在昨日侧）；
//   - MACD死叉且DIF负（sell_trend_volume.go:107）：昨柱>0 且 今柱<=0
//     → CrossDown(a, b, true)（恰等在今日侧）。
func CrossDown(a, b Operand, todayInclusive bool) (And, error) {
	return crossRule(a, b, todayInclusive, false)
}

func crossRule(a, b Operand, todayInclusive, up bool) (And, error) {
	aPrev, err := RefOperand(1, a)
	if err != nil {
		return nil, err
	}
	bPrev, err := RefOperand(1, b)
	if err != nil {
		return nil, err
	}
	var prevOp, curOp Op
	switch {
	case up && !todayInclusive: // 昨 a<=b，今 a>b（MACD金叉）
		prevOp, curOp = Le, Gt
	case up && todayInclusive: // 昨 a<b，今 a>=b（RSI超卖回升）
		prevOp, curOp = Lt, Ge
	case !up && !todayInclusive: // 昨 a>=b，今 a<b（均线死叉）
		prevOp, curOp = Ge, Lt
	default: // 昨 a>b，今 a<=b（MACD死叉）
		prevOp, curOp = Gt, Le
	}
	prev, err := NewCompare(aPrev, prevOp, bPrev)
	if err != nil {
		return nil, err
	}
	cur, err := NewCompare(a, curOp, b)
	if err != nil {
		return nil, err
	}
	return NewAnd(prev, cur)
}

// 编译期断言：时序规则满足 Rule 契约。
var (
	_ Rule = Consecutive{}
	_ Rule = CountWindow{}
	_ Rule = MinKlines{}
	_ Rule = ruleFunc{}
)
