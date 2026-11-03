package rule

import (
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/researchdata"
	"github.com/injoyai/tdx/v2/protocol"
)

// 以下为包内测试夹具：手写 K 线与可观测求值行为的伪规则/伪操作数。

// makeKline 构造单根 K 线，价格为元（内部转厘），时间从基准日按天推算。
func makeKline(day int, open, high, low, close float64) *extend.Kline {
	return &extend.Kline{
		Kline: &protocol.Kline{
			Time:  time.Date(2026, 10, day, 0, 0, 0, 0, time.UTC),
			Open:  open,
			High:  high,
			Low:   low,
			Close: close,
		},
	}
}

// makeKlines 构造收盘价单调给定的 K 线前缀，开高低随收盘平移。
func makeKlines(closes ...float64) extend.Klines {
	ks := make(extend.Klines, 0, len(closes))
	for i, c := range closes {
		ks = append(ks, makeKline(i+1, c-0.1, c+0.1, c-0.2, c))
	}
	return ks
}

// closeOperand 取末根收盘价的整数厘值，验证规则真实读取可见前缀；
// 空前缀返回无效值。
type closeOperand struct{}

func (closeOperand) Name() string { return "close" }

func (closeOperand) Value(in Input) Value {
	if len(in.Klines) == 0 {
		return InvalidValue()
	}
	return PriceValue(in.Klines[len(in.Klines)-1].Close)
}

// recordingRule 记录求值顺序的伪规则，用于断言 And/Or 短路与左到右顺序。
type recordingRule struct {
	name   string
	order  *[]string
	result bool
}

func (r recordingRule) Name() string { return r.name }

func (r recordingRule) Eval(Input) bool {
	*r.order = append(*r.order, r.name)
	return r.result
}

func recordingRuleOf(name string, result bool, order *[]string) Rule {
	return recordingRule{name: name, order: order, result: result}
}

// positionProbe 捕获求值输入中的持仓可见性与 PIT 视图，用于适配器测试。
type positionProbe struct {
	available bool
	buy       *core.Buy
	data      any
	lastLen   int
}

func (p *positionProbe) Name() string { return "probe" }

func (p *positionProbe) Eval(in Input) bool {
	p.buy, p.available = in.Position()
	p.data = in.Data
	p.lastLen = len(in.Klines)
	return true
}

// stubView researchdata.View 测试桩，仅验证视图被原样注入。
type stubView struct{}

func (stubView) Latest(string, string, time.Time) (researchdata.Record, bool) {
	return researchdata.Record{}, false
}

func (stubView) Range(string, string, time.Time, time.Time, time.Time) []researchdata.Record {
	return nil
}
