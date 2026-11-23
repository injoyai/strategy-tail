package builder

import (
	"sync"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
)

// index.go 指数条件的数据注入。
//
// 指数条件依赖基准指数日线，而条件树工厂只接收参数、不接触行情，
// 因此由 Lab 运行时通过 SetIndexLoader 注入读取器；首次求值时按个股
// K线区间懒加载一次指数日线并缓存，后续求值只做日期对齐。

// IndexLoader 读取指数日线，区间为 [start, end]。
type IndexLoader func(code string, start, end time.Time) (extend.Klines, error)

var (
	indexLoaderMu sync.RWMutex
	indexLoader   IndexLoader
)

// SetIndexLoader 注入指数日线读取器；传 nil 表示不提供指数数据。
func SetIndexLoader(loader IndexLoader) {
	indexLoaderMu.Lock()
	indexLoader = loader
	indexLoaderMu.Unlock()
}

func currentIndexLoader() IndexLoader {
	indexLoaderMu.RLock()
	defer indexLoaderMu.RUnlock()
	return indexLoader
}

// lazyIndexKlines 首次求值时加载指数日线并缓存。
// start 预留 2 倍最大周期 + 30 个自然日以覆盖均线预热；end 取当前
// 时间，保证同一 Buyer 在跨年度回测中后续调用也有数据。
type lazyIndexKlines struct {
	code      string
	maxPeriod int

	mu     sync.Mutex
	loaded bool
	ks     extend.Klines
}

func (l *lazyIndexKlines) get(dks extend.Klines) extend.Klines {
	if len(dks) == 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loaded {
		return l.ks
	}
	loader := currentIndexLoader()
	if loader == nil {
		return nil
	}
	l.loaded = true
	start := dks[0].Time.AddDate(0, 0, -2*l.maxPeriod-30)
	end := time.Now()
	if last := dks[len(dks)-1].Time; last.After(end) {
		end = last
	}
	ks, err := loader(l.code, start, end)
	if err != nil {
		return nil
	}
	l.ks = ks
	return ks
}

// indexBullBuyer 指数均线多头排列，首次求值时加载指数日线。
type indexBullBuyer struct {
	index   lazyIndexKlines
	periods []int
}

func (b *indexBullBuyer) Name() string {
	return sb.A指数多头排列{Periods: b.periods}.Name()
}

func (b *indexBullBuyer) Buy(code string, dks extend.Klines) bool {
	return sb.A指数多头排列{Ks: b.index.get(dks), Periods: b.periods}.Buy(code, dks)
}

// indexAboveMABuyer 指数站上均线，首次求值时加载指数日线。
type indexAboveMABuyer struct {
	index  lazyIndexKlines
	period int
}

func (b *indexAboveMABuyer) Name() string {
	return sb.A指数站上MA{Period: b.period}.Name()
}

func (b *indexAboveMABuyer) Buy(code string, dks extend.Klines) bool {
	return sb.A指数站上MA{Ks: b.index.get(dks), Period: b.period}.Buy(code, dks)
}

// newIndexBullBuyer 构造指数多头排列条件。
func newIndexBullBuyer(p map[string]any) (core.Buyer, error) {
	code, err := strArg(p, "indexCode")
	if err != nil {
		return nil, err
	}
	if code == "" {
		code = "sh000001"
	}
	periods, err := intListArg(p, "periods")
	if err != nil {
		return nil, err
	}
	maxPeriod := 120
	if len(periods) == 0 {
		periods = []int{20, 60, 120}
	}
	for _, period := range periods {
		if period > maxPeriod {
			maxPeriod = period
		}
	}
	return &indexBullBuyer{
		index:   lazyIndexKlines{code: code, maxPeriod: maxPeriod},
		periods: periods,
	}, nil
}

// newIndexAboveMABuyer 构造指数站上MA条件。
func newIndexAboveMABuyer(p map[string]any) (core.Buyer, error) {
	code, err := strArg(p, "indexCode")
	if err != nil {
		return nil, err
	}
	if code == "" {
		code = "sh000001"
	}
	period, err := intArg(p, "period")
	if err != nil {
		return nil, err
	}
	if period == 0 {
		period = 60
	}
	return &indexAboveMABuyer{
		index:  lazyIndexKlines{code: code, maxPeriod: period},
		period: period,
	}, nil
}

// indexCodeParam 指数代码参数定义。
func indexCodeParam() ParamDef {
	return ParamDef{
		Key: "indexCode", Label: "指数代码", Type: "text", Default: "sh000001",
		Pattern: `^[a-z]{2}\d{6}$`, Desc: "如 sh000001 上证指数、sh000300 沪深300",
	}
}
