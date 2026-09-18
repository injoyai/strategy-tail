package common

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/researchdata"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/protocol"
)

// Market 表示市场（板块）类型。
type Market int

const (
	MarketAll      Market = iota // 全部
	Market沪深主板               // 沪深主板（sh60/sz00）
	Market科创板                 // 科创板（sh68）
	Market创业板                 // 创业板（sz30）
)

var marketNames = map[Market]string{
	MarketAll:      "全部",
	Market沪深主板: "沪深主板",
	Market科创板:   "科创板",
	Market创业板:   "创业板",
}

// String 返回市场中文名。
func (m Market) String() string {
	if s, ok := marketNames[m]; ok {
		return s
	}
	return "未知市场"
}

// Prefixes 返回该市场对应的股票代码前缀。
func (m Market) Prefixes() []string {
	switch m {
	case Market沪深主板:
		return []string{"sh60", "sz00"}
	case Market科创板:
		return []string{"sh68"}
	case Market创业板:
		return []string{"sz30"}
	default:
		return nil
	}
}

// Codes 返回该市场对应的股票代码列表。
func (m Market) Codes() []string {
	if m == MarketAll {
		return GetAllCodes()
	}
	codes := []string(nil)
	for _, code := range GetAllCodes() {
		for _, prefix := range m.Prefixes() {
			if strings.HasPrefix(code, prefix) {
				codes = append(codes, code)
				break
			}
		}
	}
	return codes
}

// AllCodes 返回该市场的全部股票代码，等价于 Codes()。
func (m Market) AllCodes() []string { return m.Codes() }

var (
	DefaultBuyer  = MACDBuyer
	DefaultSeller = MACDSeller

	MACDBuyer = buy.And{
		buy.A流通市值{Min: 400}, //流通市值大于N亿
		buy.A现价{Max: 120},     //价格小于120,太贵了买不起
		buy.A过滤涨停{},         //过滤涨停,涨停买不进去

		buy.MACD反转{MinLookback: 4}, //MACD
		buy.MACD负数{MinDays: 5},     //MACD阴线,5

		buy.A现价大于N日均线(30), //当天价格高于N日均线

		buy.And{
			buy.MAUp{Period: 20, MinSlope: 0.0002}, //N日均线向上,且增速大于0.05%
			buy.MAUp{Period: 30, MinSlope: 0.0005}, //N日均线向上,且增速大于0.05%
		},
	}

	MACDSeller = sell.Or{
		//无盈利等第二次上升浪
		sell.MACD反转{Lookback: 10},
	}

	// MACDBaseBuyer 57.58% 胜率 1.58 盈亏比 105.01% 年化
	MACDBaseBuyer = buy.And{
		buy.A流通市值{Min: 400},
		buy.A现价{Max: 120},
		buy.A过滤涨停{},

		buy.MAUp{Period: 20},
		buy.MAUp{Period: 30},
		buy.MAUp{Period: 60},

		// MACD量柱反转向上
		buy.MACD反转{MinLookback: 4},
	}

	BaseBuyer = buy.And{
		buy.A价格{Min: 2, Max: 120},
		buy.A过滤涨停{},
	}
)

const (
	万                = 1e4
	亿                = 1e8
	DefaultGoroutines = 10
	DatabaseDir       = tdx.DefaultDatabaseDir
)

var (
	Pull         *extend.PullKline
	Manage       *tdx.Manage
	ResearchData = researchdata.NewHub()

	// DefaultUniverse 默认只读股票池：优先加载本地成员文件（路径仅来自本地
	// 配置 research.universe，HTTP 请求不得指定），文件缺失或加载失败时显式
	// 降级 current_static——静态池存在生存者偏差、PIT 恒为 unverified，只支撑
	// exploratory 证据等级，不阻塞旧探索流程。
	DefaultUniverse = newDefaultUniverse()
)

// UniverseFile 股票池成员文件路径，仅来自本地配置。
func UniverseFile() string {
	return cfg.GetString("research.universe", filepath.Join("config", "universe.csv"))
}

// newDefaultUniverse 构造默认股票池；宽松模式加载（非法行跳过并计数披露），
// 失败一律降级 current_static 并告警。
func newDefaultUniverse() researchdata.Universe {
	path := UniverseFile()
	u, fallback, err := researchdata.LoadUniverseOrDefault(path, researchdata.FileUniverseConfig{}, false, GetAllCodes)
	if err != nil {
		logs.Warnf("universe 文件加载失败，降级 current_static: path=%s err=%v", path, err)
		return researchdata.NewStaticUniverse(researchdata.StaticUniverseConfig{
			ID:     "current_static",
			Source: "tdx_local",
			Codes:  GetAllCodes,
		})
	}
	if fallback {
		logs.Warnf("universe 文件不存在，降级 current_static（生存者偏差，仅 exploratory）: %s", path)
	}
	return u
}

func init() {
	logs.SetFormatter(logs.TimeFormatter)

	var err error

	Manage, err = tdx.NewManage(tdx.WithDialGbbqDefault())
	logs.PanicErr(err)

	Pull, err = extend.NewPullKline(extend.PullKlineConfig{
		Types:      cfg.GetStrings("pull.types", []string{extend.Day}),
		Dir:        cfg.GetString("pull.database", tdx.DefaultDatabaseDir),
		Goroutines: cfg.GetInt("pull.goroutines", DefaultGoroutines),
	})
	logs.PanicErr(err)

	// 数据更新改为显式调用 Update()，避免 import 本包即触发全量数据更新副作用
}

func Update() error {
	return Pull.Update(Manage, true)
}

// LoadBacktestConfig 从 config.yaml 读取回测引擎配置（成本/仓位/基准）。
// 未配置的字段使用 DefaultXxx() 填充。
// 风控参数不在此读取——由 cmd/backtest 入口构造为 Seller（见 loadRiskSeller）。
func LoadBacktestConfig() (cost core.Cost, pos core.PositionConfig, years []int, benchmark string, mcIterations int) {
	cost = core.Cost{
		CommissionRate:  cfg.GetFloat64("backtest.cost.commission_rate", 0.0001),
		StampDutyRate:   cfg.GetFloat64("backtest.cost.stamp_duty_rate", 0.0005),
		TransferFeeRate: cfg.GetFloat64("backtest.cost.transfer_fee_rate", 0.00001),
		Slippage:        protocol.Yuan(cfg.GetFloat64("backtest.cost.slippage", 0.01)),
		MinCommission:   cfg.GetFloat64("backtest.cost.min_commission", 0),
	}
	pos = core.PositionConfig{
		MaxPositions: cfg.GetInt("backtest.position.max_positions", 0),
		MaxPerCode:   cfg.GetInt("backtest.position.max_per_code", 1),
		SharesPerLot: cfg.GetInt("backtest.position.shares_per_lot", 100),
	}
	years = cfg.GetInts("backtest.years", []int{2020, 2021, 2022, 2023, 2024, 2025, 2026})
	benchmark = cfg.GetString("backtest.benchmark", "sh000300")
	mcIterations = cfg.GetInt("backtest.monte_carlo_iterations", 1000)
	return
}

func GetNoPriceLimitCodes() []string {
	return Market沪深主板.Codes()
}

func Get沪深Codes() []string {
	codes := []string(nil)
	for _, code := range Manage.Codes.GetStockCodes() {
		if strings.HasPrefix(code, "sz") || strings.HasPrefix(code, "sh") {
			codes = append(codes, code)
		}
	}
	return codes
}

func Get科创Codes() []string {
	return Market科创板.Codes()
}

func Get创业Codes() []string {
	return Market创业板.Codes()
}

const (
	// 红利连续年数 红利股要求最近 N 个完整自然年（不含当年）每年都有现金分红。
	红利连续年数 = 5
	// 红利最低股息率 红利股近 12 个月股息率下限（%）。
	红利最低股息率 = 4.0
)

// Get红利Codes 返回红利股代码列表（严格口径）：
//   - 最近 5 个完整自然年（不含当年）每年都有现金分红（除权除息 Fenhong>0，每10股分N元）；
//   - 近 12 个月每股现金分红合计（Fenhong/10）÷ 最新收盘价 ≥ 4%；
//   - 仅沪深两市，剔除 ST/退市股（股价崩塌会虚增股息率）。
//
// 最新价取本地日K库最后一根收盘价，无数据的股票跳过并告警计数。
// 分红数据来自 tdx Gbbq 内存缓存，价格读取仅针对通过分红筛选的候选，全市场扫描开销小。
func Get红利Codes() []string {
	now := time.Now()

	// 第一遍（纯内存）：沪深两市 + 非 ST/退 + 连续 N 年现金分红
	type 候选 struct {
		code   string
		events protocol.XRXDs
	}
	候选s := []候选(nil)
	for _, code := range Get沪深Codes() {
		name := Manage.Codes.GetName(code)
		if strings.Contains(name, "ST") || strings.Contains(name, "退") {
			continue
		}
		events := Manage.Gbbq.GetXRXDs(code)
		if !连续分红(events, now, 红利连续年数) {
			continue
		}
		候选s = append(候选s, 候选{code, events})
	}

	// 第二遍（本地日K库）：并行读最新收盘价，算近 12 个月股息率
	codes := []string(nil)
	skipped := 0
	var mu sync.Mutex
	wg := sync.WaitGroup{}
	sem := make(chan struct{}, DefaultGoroutines)
	for _, v := range 候选s {
		wg.Add(1)
		go func(code string, events protocol.XRXDs) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			k, err := Pull.DayKline(code)
			if err != nil || k == nil {
				mu.Lock()
				skipped++
				mu.Unlock()
				return
			}
			price := k.Close.Float64()
			if price <= 0 {
				mu.Lock()
				skipped++
				mu.Unlock()
				return
			}
			yield := 近一年每股分红(events, now) / price * 100
			if yield >= 红利最低股息率 {
				mu.Lock()
				codes = append(codes, code)
				mu.Unlock()
			}
		}(v.code, v.events)
	}
	wg.Wait()

	if skipped > 0 {
		logs.Warnf("红利股筛选：%d 只候选无本地日K数据，已跳过", skipped)
	}
	sort.Strings(codes)
	return codes
}

// 连续分红 判断 events 在最近 years 个完整自然年（不含当年）是否每年都有现金分红。
func 连续分红(events protocol.XRXDs, now time.Time, years int) bool {
	has := map[int]bool{}
	for _, v := range events {
		if v.Fenhong > 0 {
			has[v.Time.Year()] = true
		}
	}
	for i := 1; i <= years; i++ {
		if !has[now.Year()-i] {
			return false
		}
	}
	return true
}

// 近一年每股分红 返回 events 在 [now-1年, now] 窗口内的每股现金分红合计（元）。
// XRXD.Fenhong 为每 10 股分红金额，除以 10 折算每股。
func 近一年每股分红(events protocol.XRXDs, now time.Time) float64 {
	start := now.AddDate(-1, 0, 0)
	sum := 0.0
	for _, v := range events {
		if v.Fenhong > 0 && !v.Time.Before(start) && !v.Time.After(now) {
			sum += v.Fenhong / 10
		}
	}
	return sum
}

func GetAllCodes() []string {
	return Manage.Codes.GetStockCodes()
}

func GetIndexCodes() []string {
	return Manage.Codes.GetIndexCodes()
}

// IsTradingTime - 判断是否处于交易时间段
// 交易时间：上午 09:30 - 11:30，下午 13:00 - 15:01
func IsTradingTime() bool {
	now := time.Now()
	h, m := now.Hour(), now.Minute()

	// 上午 09:30 - 11:30
	if h == 9 && m >= 30 {
		return true
	}
	if h == 10 {
		return true
	}
	if h == 11 && m <= 30 {
		return true
	}

	// 下午 13:00 - 15:01
	if h == 13 || h == 14 {
		return true
	}
	if h == 15 && m <= 1 {
		return true
	}

	return false
}
