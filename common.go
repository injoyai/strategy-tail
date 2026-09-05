package common

import (
	"strings"
	"time"

	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
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
	Pull   *extend.PullKline
	Manage *tdx.Manage
)

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
