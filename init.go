package common

import (
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/lib/xorms"
	"github.com/injoyai/tdx/protocol"
)

var (
	DefaultBuyer = buy.And{
		buy.A流通市值{Min: 400}, //流通市值大于N亿
		buy.A现价{Max: 120},   //价格小于120,太贵了买不起
		buy.A过滤涨停{},         //过滤涨停,涨停买不进去

		buy.MACD{Lookback: 20}, //MACD
		buy.MACD负数{Days: 5},    //MACD阴线,5

		buy.A现价大于N日均线(30), //当天价格高于N日均线

		buy.And{
			buy.MAUp{Period: 20, MinSlope: 0.0002}, //N日均线向上,且增速大于0.05%
			buy.MAUp{Period: 30, MinSlope: 0.0005}, //N日均线向上,且增速大于0.05%
		},
	}
	DefaultSeller = sell.Or{
		sell.MACD{Lookback: 10},
	}

	// TrendVolumeV2Buyer 均线趋势+量价突破V2买入策略（优化版，由单功能策略组合而成）
	// 优化要点：
	// - 用"突破20日新高"替代单纯"站上MA20"，避免横盘伪突破
	// - 增加"均线多头排列"作为趋势确认
	// - 增加"实体阳线"过滤十字星犹豫线
	// - 增加"ATR波动率范围"过滤死水股和妖股
	// - 收紧成交量条件至1.8倍
	// - 提高成交额门槛至1亿（提升流动性）
	// - MACD 仅保留"零轴上方"（避免反弹陷阱）
	TrendVolumeV2Buyer = buy.And{
		// === 趋势确认（核心） ===
		buy.A现价大于N日均线(20),                      // 收盘价站上MA20
		buy.A均线向上{Period: 20, MinSlope: 0.001}, // MA20向上且斜率>0.1%
		buy.A均线多头排列{Periods: []int{5, 10, 20}}, // 5/10/20 多头排列
		buy.A突破N日高点{Period: 20},                // 突破20日新高（真突破信号）

		// === 量价确认 ===
		buy.A成交量放大{Period: 5, Ratio: 1.8}, // 放量1.8倍
		buy.A实体阳线{MinBodyRatio: 0.5},      // 实体占比>50%的阳线

		// === 动能确认 ===
		buy.MACD零轴上方{},                          // MACD运行在零轴上方（趋势好）
		buy.RSI区间{Period: 14, Min: 50, Max: 70}, // RSI在强势区但未超买

		// === 风险过滤 ===
		buy.A涨幅小于(7),                                       // 涨幅<7%（避免追高，留出空间）
		buy.A近N日涨幅小于{Days: 5, Max: 12},                     // 近5日涨幅<12%
		buy.A乖离率小于{Period: 20, Max: 10},                    // 乖离率<10%（不偏离过大）
		buy.ATR波动率范围{Period: 14, MinPct: 1.0, MaxPct: 4.0}, // ATR波动率1%~4%
		buy.A成交额大于(1 * 亿),                                  // 成交额>1亿（流动性）
	}

	// TrendVolumeV2Seller 均线趋势+量价突破V2卖出策略（优化版）
	// 优化要点：
	// - 增加"固定止损-5%"作为第一道防线（无延迟）
	// - 增加"ATR动态止损x2.5"适应不同波动性
	// - 增加"移动止盈"保护浮盈
	// - 增加"放量大阴线"识别主力出货
	// - 跌破均线响应改为1日（不再等3日）
	// - 单日跌幅阈值从7%收紧至5%
	TrendVolumeV2Seller = sell.Or{
		// === 风险控制（最优先） ===
		sell.A固定止损{Pct: 0.05},                     // 固定止损-5%
		sell.A_ATR止损{Period: 14, Multiple: 2.5},   // ATR动态止损
		sell.A单日跌幅大于{Max: 5},                      // 单日跌幅>5%
		sell.A放量大阴线{VolumeRatio: 1.5, FallPct: 2}, // 放量大阴线（出货）

		// === 趋势反转 ===
		sell.A收盘跌破均线{Period: 20, Days: 1}, // 跌破MA20立即卖（不等3日）
		sell.A_MACD死叉且DIF为负{},             // MACD死叉且DIF<0

		// === 利润保护 ===
		sell.A移动止盈{ActivateProfitPct: 0.05, RetreatPct: 0.4}, // 盈利5%后回撤40%卖
		sell.A_RSI超买{Period: 14, Threshold: 78},              // RSI严重超买

		// === 时间止损 ===
		sell.A时间止损{MaxHoldDays: 15, MinProfitRate: 0.03}, // 持有15日未盈利3%卖
	}
)

const (
	万 = 1e4
	亿 = 1e8
)

var (
	DatabaseDir = tdx.DefaultDatabaseDir
	DayKlineDir = filepath.Join(DatabaseDir, "day-kline")
	MinKlineDir = filepath.Join(DatabaseDir, "min-kline")
	Pull        *extend.PullKline
	Manage      *tdx.Manage
)

func init() {
	logs.SetFormatter(logs.TimeFormatter)

	db, err := xorms.NewSqlite(filepath.Join(DatabaseDir, "update.db"))
	logs.PanicErr(err)

	update, err := tdx.NewUpdated(db, 15, 1)
	logs.PanicErr(err)

	Manage, err = tdx.NewManage(tdx.WithDialGbbqDefault())
	logs.PanicErr(err)

	Pull = extend.NewPullKline(extend.PullKlineConfig{
		Tables:     []string{extend.Day},
		Dir:        DayKlineDir,
		Goroutines: 10,
	})

	if updated, err := update.Updated("pull"); err != nil || !updated {
		if Manage.Workday.TodayIs() {
			err = Pull.Update(Manage)
			logs.PanicErr(err)
			err = update.Update("pull")
			logs.PanicErr(err)
		}
	}

}

func GetNoPriceLimitCodes() []string {
	codes := []string(nil)
	for _, code := range Manage.Codes.GetStockCodes() {
		if strings.HasPrefix(code, "sh60") || strings.HasPrefix(code, "sz00") {
			codes = append(codes, code)
		}
	}
	return codes
}

func GetDayKlines(code string, start, end time.Time) (extend.Klines, error) {
	ks, err := Pull.DayKlines(code)
	if err != nil {
		return nil, err
	}
	ls := extend.Klines{}
	for _, k := range ks {
		if k.Time.Before(start) || k.Time.After(end) {
			continue
		}
		ls = append(ls, k)
	}
	return ls, nil
}

func GetMinKlines(code string, start, end time.Time) (protocol.Klines, error) {
	years := []int(nil)
	for i := start.Year(); i <= end.Year(); i++ {
		years = append(years, i)
	}
	ks := protocol.Klines{}
	mu := sync.Mutex{}
	wg := sync.WaitGroup{}
	for _, year := range years {
		wg.Add(1)
		go func(code string, year int) {
			defer wg.Done()
			filename := filepath.Join(MinKlineDir, code, code+"-"+strconv.Itoa(year)+".db")
			if !oss.Exists(filename) {
				return
			}
			db, err := xorms.NewSqlite(filename)
			if err != nil {
				logs.Err(err)
				return
			}
			defer db.Close()
			ls := protocol.Klines{}
			err = db.Find(&ls)
			if err != nil {
				logs.Err(err)
				return
			}
			res := protocol.Klines{}
			for _, l := range ls {
				if l.Time.Year() != year {
					continue
				}
				res = append(res, l)
			}
			mu.Lock()
			defer mu.Unlock()
			ks = append(ks, res...)
		}(code, year)
	}
	wg.Wait()
	ks.Sort()
	return ks, nil
}
