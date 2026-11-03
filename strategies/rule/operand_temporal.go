package rule

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// 时序操作数（迁移计划 T3 前置）：附录 B 19 项时序组件所需的
// 历史引用、MACD 序列标量、窗口极值、区间涨幅与逐日字段取值。
//
// 约定（沿用 operand_factor.go）：
//   - 浮点结果一律经 FloatValue 归一（NaN/±Inf → 无效值），不补 0；
//   - 历史求值按交易日索引截断可见前缀（truncatedInput），因子只看
//     截断后的前缀，不拿今天的视图填过去值（计划第 4 节"时间引用"）；
//   - 窗口是否含今日由 includeToday 显式声明并在 Name 中可见
//     （windowName），不做隐式口径；
//   - 全部实现只读 Input、无副作用、无跨调用缓存（切片地址/长度/代码
//     均不作缓存键，计划 3.4 与 T3 验收）。

// truncatedInput 返回截断到前 end 根 K 线的求值输入副本：只收窄 Klines
// 切片（与原前缀共享底层数组、只读不写），代码、PIT 视图与持仓原样保留。
// 调用方保证 0 <= end <= len(in.Klines)。
func truncatedInput(in Input, end int) Input {
	in.Klines = in.Klines[:end]
	return in
}

// RefOperand 历史引用操作数：对 op 在 days 个交易日之前的求值——
// 把可见前缀截断到 dks[:n-days] 后交给 op（同时回退因子求值时点，
// 等价旧组件"dks[:n-1] 上重算"的写法）。截断后前缀为空时仍交给 op
// 按空前缀语义求值：K 线/因子操作数返回无效值，Literal 常量原样有效
// （昨日阈值是常量，不因前缀不足失效）。days<=0 拒绝构造（今日引用
// 直接用原操作数）；days>=n 即空前缀求值，不越界取数、不补 0。
// Name 形如 "3天前·均线(20)"，历史口径在名称中可见。
func RefOperand(days int, op Operand) (Operand, error) {
	if days <= 0 {
		return nil, fmt.Errorf("历史引用天数必须为正，得到 %d", days)
	}
	if op == nil {
		return nil, fmt.Errorf("历史引用的操作数不能为空")
	}
	return refOperand{days: days, op: op}, nil
}

type refOperand struct {
	days int
	op   Operand
}

func (r refOperand) Name() string { return strconv.Itoa(r.days) + "天前·" + r.op.Name() }

func (r refOperand) Value(in Input) Value {
	end := len(in.Klines) - r.days
	if end < 0 {
		end = 0
	}
	return r.op.Value(truncatedInput(in, end))
}

// MACDHist 当日 MACD 柱值（hist，浮点）。委托 factor.MACD柱值；其底层
// util.MACDHistogram 为从头递推的因果 EMA，对截断前缀求值与全序列对应日
// 逐位一致，故窗口极值/连续段原语经本操作数逐日求值不引入数值漂移。
// 空前缀 → NaN → 无效。预热拦截（n<slow+signal）不由本操作数承担：
// MACDHistogram 对任意非空前缀都给出有限值，旧组件的显式长度拦截由
// 门面用 MinKlines 表达，避免预热不足时的形态误判。
func MACDHist(fast, slow, signal int) Operand {
	return macdOperand{params: f.MACD柱值{Fast: fast, Slow: slow, Signal: signal}}
}

type macdOperand struct{ params f.MACD柱值 }

func (m macdOperand) Name() string { return m.params.Name() }

func (m macdOperand) Value(in Input) Value {
	n := len(in.Klines)
	if n == 0 {
		return InvalidValue()
	}
	if in.macd == nil {
		return FloatValue(m.params.Value(in.Code, in.Klines))
	}
	return in.macd.value(m.params, n)
}

// 调用内复用因果序列，截断前缀按索引取值；参数切换重算，不跨分钟保留。
type macdScope struct {
	sync.Mutex
	klines extend.Klines
	params f.MACD柱值
	series []float64
}

func (s *macdScope) value(params f.MACD柱值, end int) Value {
	if end > len(s.klines) {
		return InvalidValue()
	}
	return FloatValue(s.histogram(params)[end-1])
}

// histogram 返回本次求值内已完成的只读序列；参数切换只替换切片，不改写旧序列。
func (s *macdScope) histogram(params f.MACD柱值) []float64 {
	s.Lock()
	defer s.Unlock()
	if s.series == nil || s.params != params {
		s.series = params.Series(s.klines)
		s.params = params
	}
	return s.series
}

// WindowMin 窗口最小值操作数：对近 days 天（是否含今日由 includeToday
// 声明）逐日对截断前缀求值 op，取最小值。窗口左端越过前缀起点时钳制
// 到 0——与旧 MACD 反转族（buy_macd.go:82 hist[max(n-1-k,0):n-1]、
// sell_macd.go:66 windowStart<0 钳 0）的扫描口径一致；这与 factor
// 固定窗口因子（A最高价 等"窗口不足即 NaN"）不同，选用时按旧组件口径。
// 窗口为空（无可用数据）返回无效值；窗口内任一日无效 → 整体无效
// （不跳过），与旧 SeriesPosition 对缺失值传播 NaN → 比较不触发一致。
// 极值仅定义在浮点域：op 返回非浮点值域时整体无效。
func WindowMin(days int, op Operand, includeToday bool) (Operand, error) {
	return windowExtreme(days, op, includeToday, false)
}

// WindowMax 窗口最大值操作数：语义同 WindowMin，取最大值。
// sell.MACD反转 的"昨日为近 Lookback 日窗口最大值（严格相等）"经
// Compare(RefOperand(1, MACDHist), Eq, WindowMax(lookback, MACDHist, false))
// 表达——昨日恰是窗口末值，包含在窗口内，等价于旧全等比较。
func WindowMax(days int, op Operand, includeToday bool) (Operand, error) {
	return windowExtreme(days, op, includeToday, true)
}

func windowExtreme(days int, op Operand, includeToday bool, wantMax bool) (Operand, error) {
	if days <= 0 {
		return nil, fmt.Errorf("窗口极值天数必须为正，得到 %d", days)
	}
	if op == nil {
		return nil, fmt.Errorf("窗口极值的操作数不能为空")
	}
	return windowExtremeOperand{days: days, op: op, includeToday: includeToday, wantMax: wantMax}, nil
}

type windowExtremeOperand struct {
	days         int
	op           Operand
	includeToday bool
	wantMax      bool
}

func (w windowExtremeOperand) Name() string {
	kind := "窗口最小"
	if w.wantMax {
		kind = "窗口最大"
	}
	return windowName(w.includeToday, fmt.Sprintf("近%d日%s·%s", w.days, kind, w.op.Name()))
}

func (w windowExtremeOperand) Value(in Input) Value {
	end := len(in.Klines)
	if !w.includeToday {
		end--
	}
	start := end - w.days
	if start < 0 {
		start = 0
	}
	if end <= start {
		return InvalidValue()
	}
	var series []float64
	if m, ok := w.op.(macdOperand); ok && in.macd != nil {
		if end > len(in.macd.klines) {
			return InvalidValue()
		}
		series = in.macd.histogram(m.params)
	}
	var ext float64
	first := true
	for i := start; i < end; i++ {
		var v Value
		if series != nil {
			v = FloatValue(series[i])
		} else {
			v = w.op.Value(truncatedInput(in, i+1))
		}
		if !v.Valid() || v.Kind() != KindFloat {
			return InvalidValue()
		}
		if first {
			ext = v.Float()
			first = false
			continue
		}
		if w.wantMax {
			if v.Float() > ext {
				ext = v.Float()
			}
		} else if v.Float() < ext {
			ext = v.Float()
		}
	}
	return FloatValue(ext)
}

// GainPercent 近 N 日累计涨幅（%，起点为 N 个交易日前收盘）。
// 公式与旧 riseRateNDays（strategies/buy/buy_trend_volume.go:294-305）
// 逐位一致：(endPrice-startPrice)/startPrice*100，运算次序不变
// （T2 教训：乖离率 1-ULP 恰等翻转，公式次序必须与旧门面一致）。
// len<days+1 或起点收盘 <=0 时无效（旧 ok=false → 不触发）；
// 与阈值的严格/含等边界由门面比较操作符表达。
func GainPercent(days int) (Operand, error) {
	if days <= 0 {
		return nil, fmt.Errorf("涨幅窗口天数必须为正，得到 %d", days)
	}
	return factorOperand{
		name: fmt.Sprintf("近%d日累计涨幅%%", days),
		eval: func(in Input) Value {
			n := len(in.Klines)
			if n < days+1 {
				return InvalidValue()
			}
			startPrice := in.Klines[n-1-days].Close
			endPrice := in.Klines[n-1].Close
			if startPrice <= 0 {
				return InvalidValue()
			}
			return FloatValue((endPrice - startPrice) / startPrice * 100)
		},
	}, nil
}

// LowFloat 今低（浮点元）。逐日谓词使用（如 A回踩N日均线 的触及判定
// "最低价<=当日均线"，buy_pullback_ma.go:91）；窗口 Low 极值用
// LowestValue/LowestPrice（factor 窗口因子），不在此重复窗口扫描。
func LowFloat() Operand {
	return factorOperand{name: "今低(元)", eval: func(in Input) Value {
		if len(in.Klines) == 0 {
			return InvalidValue()
		}
		return FloatValue(in.Klines[len(in.Klines)-1].Low)
	}}
}

// MASlope 均线最弱日相对斜率（浮点）。委托 factor.MA最弱日斜率
// （Period/Lookback 为 0 时因子内默认 20/5；数据不足返回 NaN → 无效，
// 与 MAUp 的预热守卫等价）。
func MASlope(period, lookback int) Operand {
	fa := f.MA最弱日斜率{Period: period, Lookback: lookback}
	return floatOperand(fa.Name(), fa.Value)
}

// MAIncre 均线最弱日绝对增量（浮点）。委托 factor.MA最弱日增量。
// 与 MASlope 不同源（相对/绝对口径，台账风险 §13）：LongMADown 用本
// 操作数且必须先经 MinKlines 拦截长度，避免"无法判断"（NaN）被取反
// 误判为转弱。
func MAIncre(period, lookback int) Operand {
	fa := f.MA最弱日增量{Period: period, Lookback: lookback}
	return floatOperand(fa.Name(), fa.Value)
}
