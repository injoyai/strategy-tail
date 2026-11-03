package factor

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

func TestTechnicalFactors(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ks := extend.Klines{
		mk(base, 0, 10, 11, 9, 10, 100),
		mk(base, 1, 10, 13, 10, 12, 300),
		mk(base, 2, 12, 12, 9, 10, 900),
	}
	ks[2].Amount = 1234.0
	ks[2].FloatStock = 1000000000
	for _, tt := range []struct {
		factor core.Factor
		want   float64
	}{
		{A均线(2), 11},
		{A前N日均量(2), 200},
		{RSI值(2), 50},
		{ATR值(2), 3},
		{A布林下轨{Days: 2, StdTimes: 2}, 9},
		{ZScore值(2), -1},
		{A价格("open"), 12},
		{A价格("close"), 10},
		{A价格("high"), 12},
		{A价格("low"), 9},
		{A成交量{}, 900},
		{A成交额{}, 1234},
		{A流通市值{}, 100},
		{A单日涨幅{}, -100.0 / 6},
		{A实体振幅占比{}, 2.0 / 3},
		{MACD柱值{Fast: 1, Slow: 2, Signal: 2}, -8.0 / 27},
		{MACDDIF值{Fast: 1, Slow: 2}, -4.0 / 9},
	} {
		wantVal(t, tt.factor.Name(), tt.factor.Value("sh600000", ks), tt.want, 1e-12)
		wantNaN(t, tt.factor.Name(), tt.factor.Value("sh600000", nil))
	}
	wantNaN(t, "未知价格字段", A价格("unknown").Value("", ks))
	flat := closes(base, 10, 10)
	wantNaN(t, "零方差布林", (A布林下轨{Days: 2}).Value("", flat))
	wantNaN(t, "零方差ZScore", ZScore值(2).Value("", flat))
	// v2 均线与布林统计均保留浮点精度。
	precision := closes(base, 10, 10.001)
	wantVal(t, "价格均线", A均线(2).Value("", precision), 10.0005, 1e-12)
	mean, std := closeMeanStd(precision, 2)
	wantVal(t, "浮点均值", mean, 10.0005, 1e-12)
	wantVal(t, "总体标准差", std, 0.0005, 1e-12)
	for _, f := range []core.Factor{A整数RSI值(2), MACD柱值{}} {
		wantNaN(t, f.Name(), f.Value("", nil))
	}
	if !math.IsNaN(A均线(4).Value("", ks)) || !math.IsNaN(A前N日均量(3).Value("", ks)) {
		t.Fatal("数据不足必须保留为无效值")
	}
	if (MACD柱值{}).Name() != (MACD柱值{Fast: 12, Slow: 26, Signal: 9}).Name() {
		t.Fatal("默认 MACD 参数应有相同因子名")
	}
	if (MACD柱值{}).Name() == (MACD柱值{Fast: 5}).Name() ||
		(A布林下轨{StdTimes: 2}).Name() == (A布林下轨{StdTimes: 2.00001}).Name() {
		t.Fatal("不同计算参数不能共享因子名")
	}
}
