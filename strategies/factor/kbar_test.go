package factor

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

func TestK线形态(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 光头光脚阳线 o=10 h=11 l=10 c=11 → 实体 0.1、上下影 0
	one := extend.Klines{mk(base, 0, 10, 11, 10, 11, 10000)}
	body := 实体幅度{}
	wantVal(t, "实体", body.Value("sh600000", one), 0.1, 1e-9)
	upper := 上影占比{}
	wantVal(t, "上影", upper.Value("sh600000", one), 0, 1e-12)
	lower := 下影占比{}
	wantVal(t, "下影", lower.Value("sh600000", one), 0, 1e-12)

	// 长上影：o=10 c=10.5 h=11 l=10 → 上影 (11-10.5)/1=0.5，下影 (10-10)/1=0
	two := extend.Klines{mk(base, 0, 10, 11, 10, 10.5, 10000)}
	wantVal(t, "上影", upper.Value("sh600000", two), 0.5, 1e-9)
	wantVal(t, "下影", lower.Value("sh600000", two), 0, 1e-12)

	// 长下影：o=10 c=10.5 h=10.5 l=9 → 上影 0，下影 (10-9)/1.5
	three := extend.Klines{mk(base, 0, 10, 10.5, 9, 10.5, 10000)}
	wantVal(t, "上影", upper.Value("sh600000", three), 0, 1e-12)
	wantVal(t, "下影", lower.Value("sh600000", three), 1.0/1.5, 1e-9)

	// 阴线 o=10.5 c=10 h=11 l=9.5 → 实体 0.5/10.5，上影 (11-10.5)/1.5，下影 (10-9.5)/1.5
	four := extend.Klines{mk(base, 0, 10.5, 11, 9.5, 10, 10000)}
	wantVal(t, "阴线实体", body.Value("sh600000", four), 0.5/10.5, 1e-9)
	wantVal(t, "阴线上影", upper.Value("sh600000", four), 0.5/1.5, 1e-9)
	wantVal(t, "阴线下影", lower.Value("sh600000", four), 0.5/1.5, 1e-9)

	// 一字线 h=l → 0 非 NaN
	doji := extend.Klines{mk(base, 0, 10, 10, 10, 10, 10000)}
	wantVal(t, "一字上影", upper.Value("sh600000", doji), 0, 1e-12)
	wantVal(t, "一字下影", lower.Value("sh600000", doji), 0, 1e-12)

	// 空数据 → NaN
	wantNaN(t, "空实体", body.Value("sh600000", nil))
	wantNaN(t, "空上影", upper.Value("sh600000", nil))
	wantNaN(t, "空下影", lower.Value("sh600000", nil))

	// 开盘价 0 → 实体幅度 NaN
	wantNaN(t, "零开盘", (实体幅度{}).Value("sh600000", extend.Klines{mk(base, 0, 0, 1, 0, 1, 10000)}))
}
