package buy

import (
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/util"
	"github.com/injoyai/tdx/extend"
)

// TrendVolumeV2 是均线趋势+量价突破V2买入策略。
//
// 基础条件（必须全部满足）：
// 1. 收盘价站上20日均线，且20日均线方向向上
// 2. 当日成交量 > 5日均量的1.5倍
// 3. RSI(14) 从超卖区(<30)回升至30以上，或 RSI 在 30-50 之间
// 4. MACD金叉或零轴上方运行
// 5. 非涨停（接近涨停>9.5%排除）
//
// 加分条件（影响评分，决定是否买入）：
// 6. 均线多头排列（MA5 > MA10 > MA20）: +10分
// 7. 站上5日均线: +5分
// 8. 近5日涨幅 < 15%（非追高）: +5分
// 9. 乖离率(股价偏离MA20) < 10%: +5分
// 10. 换手率 3%-8%（活跃但不异常）: +5分（需行情数据，K线中无法计算，跳过）
// 11. PE在10-30之间（估值合理）: +5分（需基本面数据，K线中无法计算，跳过）
//
// 扣分/排除条件：
// - 近5日涨幅 > 15%: -10分
// - 乖离率 > 15%: -10分
// - PE > 60 或 PE 为负: -15分（需基本面数据，跳过）
// - 日成交额 < 5000万: -10分
// - 接近涨停(>9.5%): -5分
// - 连续2年亏损: 直接排除（需基本面数据，跳过）
//
// 买入门槛：基础条件全部满足 + 评分 >= 0
type TrendVolumeV2 struct {
	// MinScore 最低评分门槛，默认0（即基础条件满足即可）
	MinScore int
}

func (s TrendVolumeV2) Name() string {
	return "均线趋势+量价突破V2"
}

func (s TrendVolumeV2) Buy(code string, dks extend.Klines) bool {
	n := len(dks)
	if n < 30 {
		return false
	}

	today := dks[n-1]
	closePrice := today.Close.Float64()

	// ========== 基础条件（必须全部满足） ==========

	// 条件1: 收盘价站上20日均线，且20日均线方向向上
	ma20 := core.MA(dks, 20)
	if closePrice <= ma20 {
		return false
	}
	if !maUp(dks, 20, 1, 0) {
		return false
	}

	// 条件2: 当日成交量 > 5日均量的1.5倍
	if n < 6 {
		return false
	}
	avgVol := core.AverageVolume(dks[n-6 : n-1])
	if avgVol <= 0 || float64(today.Volume) <= avgVol*1.5 {
		return false
	}

	// 条件3: RSI(14) 从超卖区(<30)回升至30以上，或 RSI 在 30-50 之间
	rsi := util.CalcRSI(dks, 14)
	if rsi < 30 || rsi > 50 {
		// RSI不在30-50区间，检查是否从超卖区回升
		// 需要前一天RSI<30且今天>=30
		if n < 16 {
			return false
		}
		prevRSI := util.CalcRSI(dks[:n-1], 14)
		if !(prevRSI < 30 && rsi >= 30) {
			return false
		}
	}

	// 条件4: MACD金叉或零轴上方运行
	hist := util.MACDHistogram(dks, 12, 26, 9)
	if len(hist) != n || n < 2 {
		return false
	}
	macdGoldenCross := hist[n-2] <= 0 && hist[n-1] > 0 // MACD金叉
	macdAboveZero := hist[n-1] > 0                     // 零轴上方
	if !macdGoldenCross && !macdAboveZero {
		return false
	}

	// 条件5: 非涨停（接近涨停>9.5%排除）
	riseRate := today.RiseRate()
	if riseRate > 9.5 {
		return false
	}

	// ========== 加分/扣分条件 ==========
	score := 0

	// 加分6: 均线多头排列（MA5 > MA10 > MA20）: +10分
	ma5 := core.MA(dks, 5)
	ma10 := core.MA(dks, 10)
	if ma5 > 0 && ma10 > 0 && ma5 > ma10 && ma10 > ma20 {
		score += 10
	}

	// 加分7: 站上5日均线: +5分
	if closePrice > ma5 {
		score += 5
	}

	// 加分8 & 扣分: 近5日涨幅
	rise5 := riseRateNDays(dks, 5)
	if rise5 < 15 {
		score += 5 // 非追高
	}
	if rise5 > 15 {
		score -= 10 // 短期暴涨，追高风险大
	}

	// 加分9 & 扣分: 乖离率(股价偏离MA20)
	if ma20 > 0 {
		bias := (closePrice - ma20) / ma20 * 100
		if bias < 10 {
			score += 5
		}
		if bias > 15 {
			score -= 10
		}
	}

	// 扣分: 日成交额 < 5000万
	if today.Amount.Float64() < 50000000 {
		score -= 10
	}

	// 扣分: 接近涨停(>9.5%)
	if riseRate > 9.5 {
		score -= 5
	}

	// 评分门槛判断
	minScore := s.MinScore
	if minScore == 0 {
		minScore = 0
	}
	if score < minScore {
		return false
	}

	return true
}

// riseRateNDays 计算最近N日累计涨幅百分比
func riseRateNDays(dks extend.Klines, days int) float64 {
	n := len(dks)
	if n < days+1 {
		return 0
	}
	startPrice := dks[n-1-days].Close.Float64()
	endPrice := dks[n-1].Close.Float64()
	if startPrice <= 0 {
		return 0
	}
	return (endPrice - startPrice) / startPrice * 100
}

// TrendVolumeV2Score 返回均线趋势+量价突破V2策略的评分详情
// 用于调试和展示
type TrendVolumeV2Score struct {
	Code           string  `json:"code"`
	Score          int     `json:"score"`
	MACDGolden     bool    `json:"macd_golden"`
	MACDAboveZero  bool    `json:"macd_above_zero"`
	RSI            float64 `json:"rsi"`
	VolumeRatio    float64 `json:"volume_ratio"`
	Bias           float64 `json:"bias"`
	Rise5          float64 `json:"rise5"`
	MABullishAlign bool    `json:"ma_bullish_align"`
	AboveMA5       bool    `json:"above_ma5"`
}

// ScoreTrendVolumeV2 计算股票在均线趋势+量价突破V2策略下的评分详情
// 返回nil表示不满足基础条件
func ScoreTrendVolumeV2(code string, dks extend.Klines) *TrendVolumeV2Score {
	n := len(dks)
	if n < 30 {
		return nil
	}

	today := dks[n-1]
	closePrice := today.Close.Float64()
	result := &TrendVolumeV2Score{Code: code}

	// 基础条件检查
	ma20 := core.MA(dks, 20)
	if closePrice <= ma20 || !maUp(dks, 20, 1, 0) {
		return nil
	}

	if n < 6 {
		return nil
	}
	avgVol := core.AverageVolume(dks[n-6 : n-1])
	if avgVol <= 0 {
		return nil
	}
	result.VolumeRatio = float64(today.Volume) / avgVol
	if result.VolumeRatio <= 1.5 {
		return nil
	}

	rsi := util.CalcRSI(dks, 14)
	result.RSI = rsi
	if rsi < 30 || rsi > 50 {
		if n >= 16 {
			prevRSI := util.CalcRSI(dks[:n-1], 14)
			if !(prevRSI < 30 && rsi >= 30) {
				return nil
			}
		} else {
			return nil
		}
	}

	hist := util.MACDHistogram(dks, 12, 26, 9)
	if len(hist) != n || n < 2 {
		return nil
	}
	result.MACDGolden = hist[n-2] <= 0 && hist[n-1] > 0
	result.MACDAboveZero = hist[n-1] > 0
	if !result.MACDGolden && !result.MACDAboveZero {
		return nil
	}

	riseRate := today.RiseRate()
	if riseRate > 9.5 {
		return nil
	}

	// 评分
	score := 0
	ma5 := core.MA(dks, 5)
	ma10 := core.MA(dks, 10)
	result.MABullishAlign = ma5 > 0 && ma10 > 0 && ma5 > ma10 && ma10 > ma20
	if result.MABullishAlign {
		score += 10
	}

	result.AboveMA5 = closePrice > ma5
	if result.AboveMA5 {
		score += 5
	}

	result.Rise5 = riseRateNDays(dks, 5)
	if result.Rise5 < 15 {
		score += 5
	}
	if result.Rise5 > 15 {
		score -= 10
	}

	if ma20 > 0 {
		result.Bias = (closePrice - ma20) / ma20 * 100
		if result.Bias < 10 {
			score += 5
		}
		if result.Bias > 15 {
			score -= 10
		}
	}

	if today.Amount.Float64() < 50000000 {
		score -= 10
	}

	if riseRate > 9.5 {
		score -= 5
	}

	result.Score = score
	return result
}
