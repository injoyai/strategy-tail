package main

import (
	"time"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

// 均线多头排列策略
// 1. 5日线 > 10日线 > 20日线 > 30日线
// 2. 相对于之前20个交易日没有太大的放量 (当日成交量 < 20日均量 * 2)
type StrategyMA struct {
	BuyTime  string // "14:40:00"
	SellTime string // "10:00:00"
}

func (s StrategyMA) Buy(code string, dks extend.Klines, mks protocol.Klines) *trade {
	// 需要至少31根K线来计算昨日MA30
	if len(dks) < 31 {
		return nil
	}

	// 获取最后一天的数据
	dk := dks[len(dks)-1]

	// 1. 计算当日均线
	ma5 := MA(dks, 5)
	ma10 := MA(dks, 10)
	ma20 := MA(dks, 20)
	ma30 := MA(dks, 30)

	// 计算昨日均线
	prevDks := dks[:len(dks)-1]
	prevMa5 := MA(prevDks, 5)
	prevMa10 := MA(prevDks, 10)
	prevMa20 := MA(prevDks, 20)
	prevMa30 := MA(prevDks, 30)

	// 2. 判断均线多头排列 (MA5 > MA10 > MA20 > MA30) 且 均线向上 (当日 > 昨日)
	// 且是刚刚变成多头排列 (昨日不是多头排列)
	isCurrentBullish := ma5 > ma10 && ma10 > ma20 && ma20 > ma30
	isPrevBullish := prevMa5 > prevMa10 && prevMa10 > prevMa20 && prevMa20 > prevMa30

	if !isCurrentBullish || isPrevBullish {
		return nil
	}

	if !(ma5 > prevMa5 && ma10 > prevMa10 && ma20 > prevMa20 && ma30 > prevMa30) {
		return nil
	}

	// 3. 判断成交量
	// 3.1 当日不放巨量：当日成交量 < 20日均量 * 2
	startIdx := len(dks) - 11
	if startIdx < 0 {
		startIdx = 0
	}
	historyDks := dks[startIdx : len(dks)-1]

	avgVol := AverageVolume(historyDks)
	if len(historyDks) > 0 {
		if float64(dk.Volume) > avgVol*2.0 {
			return nil
		}
	}

	// 3.2 历史放巨量：近60个交易日到近20个交易日 (即 dks[len-60 : len-20]) 出现过放巨量
	// 巨量定义：单日成交量 > 该区间平均成交量 * 3
	if len(dks) >= 60 {
		// 3.3 底部抬高：近20天的最低点 > 近60天的最低点
		// 近20天最低点
		last20 := dks[len(dks)-20:]
		min20 := LowestLow(last20)

		// 近60天最低点
		last60 := dks[len(dks)-60:]
		min60 := LowestLow(last60)

		if min20 <= min60 {
			return nil
		}

		// 取区间 [len-60, len-20]
		hugeVolStart := len(dks) - 60
		hugeVolEnd := len(dks) - 20
		historyRange := dks[hugeVolStart:hugeVolEnd]

		avgVolRange := AverageVolume(historyRange)
		hasHugeVol := false
		for _, k := range historyRange {
			if float64(k.Volume) > avgVolRange*3.0 {
				hasHugeVol = true
				break
			}
		}
		if !hasHugeVol {
			return nil
		}
	} else {
		// 数据不足60天，无法判断历史巨量，保守起见不买入
		return nil
	}

	// 满足条件，寻找买点
	t := &trade{
		Code:  code,
		Buy:   true,
		Time:  time.Time{},
		Price: 0,
	}

	// 计算昨日收盘价，用于判断涨停
	prevClose := float64(dks[len(dks)-2].Close)

	// 判断涨停幅度
	limitRatio := 0.10
	if len(code) >= 3 {
		// 简单判断科创板和创业板
		if code[:3] == "688" || code[:3] == "300" || (len(code) >= 5 && (code[:5] == "sh688" || code[:5] == "sz300")) {
			limitRatio = 0.20
		}
		// 北交所 30% 暂时不考虑，数据中可能没有
	}

	//过滤涨停的
	if dk.RiseRate() >= 0.5 {
		return nil
	}

	if len(mks) == 0 {
		return &trade{
			Code:  code,
			Buy:   true,
			Time:  dk.Time,
			Price: dk.Close,
		}
	}

	// 在分钟线中寻找买入时间点
	for _, v := range mks {
		if v.Time.Format(time.TimeOnly) >= s.BuyTime {
			// 检查是否涨停
			// 如果当前价格接近涨停价（涨幅超过 LimitRatio - 0.2%），则不买入
			currPrice := float64(v.Close)
			increaseRate := (currPrice - prevClose) / prevClose
			if increaseRate >= (limitRatio - 0.002) {
				return nil
			}

			t.Time = v.Time
			t.Price = v.High
			return t
		}
	}

	return nil
}

func (s StrategyMA) Sell(code string, dks extend.Klines, mk protocol.Klines, buyPrice protocol.Price) *trade {
	// 1. 检查止损 (优先)
	if buyPrice > 0 {
		for _, v := range mk {
			// 如果亏损超过 10%，立马卖出
			if (v.Close-buyPrice).Float64()/buyPrice.Float64() < -0.20 {
				//return &trade{
				//	Code:  code,
				//	Buy:   false,
				//	Time:  v.Time,
				//	Price: v.Close,
				//}
			}
		}
	}

	// 获取当日K线
	if len(dks) < 21 {
		return nil
	}
	dk := dks[len(dks)-1]

	// 计算过去20日（不包含当日）均量
	historyDks := dks[len(dks)-21 : len(dks)-1]
	avgVol := AverageVolume(historyDks)

	// 判断是否放量：当日成交量 > 2.5倍均量
	isHugeVol := float64(dk.Volume) > avgVol*2.5

	// 如果放量，则卖出
	if isHugeVol {
		t := &trade{Code: code, Buy: false}
		// 卖出时间：如果是尾盘战法，通常在尾盘确认放量后卖出，或者收盘卖出
		// 这里简化为：如果在 14:40 之后
		// 或者直接取收盘价

		// 寻找合适的卖出点：如果全天放量，可能在尾盘卖出比较稳妥
		for _, v := range mk {
			if v.Time.Format(time.TimeOnly) >= "14:50:00" {
				t.Time = v.Time
				t.Price = v.Close // 用收盘附近的Close
				return t
			}
		}
		// 如果没找到尾盘时间（数据缺失等），就用最后一条
		last := mk[len(mk)-1]
		t.Time = last.Time
		t.Price = last.Close
		return t
	}

	// 如果没有放量，继续持有，返回 nil
	return nil
}

// 辅助函数：计算MA
func MA(dks extend.Klines, n int) float64 {
	if len(dks) < n {
		return 0
	}
	sum := 0.0
	// 取最后n个
	for _, k := range dks[len(dks)-n:] {
		sum += k.Close.Float64()
	}
	return sum / float64(n)
}

// 辅助函数：计算平均成交量
func AverageVolume(dks extend.Klines) float64 {
	if len(dks) == 0 {
		return 0
	}
	sum := 0.0
	for _, k := range dks {
		sum += float64(k.Volume)
	}
	return sum / float64(len(dks))
}
