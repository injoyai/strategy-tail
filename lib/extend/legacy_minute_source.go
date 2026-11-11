package extend

import (
	"errors"
	"fmt"
	"math"
	"sort"

	"github.com/injoyai/tdx/v2/extend/model"
	"github.com/injoyai/tdx/v2/protocol"
)

// ErrLegacyMinuteCorrupt 表示旧分钟库中大量记录的时间或价格非法，通常是历史压缩
// 产物已损坏；此时整库跳过，避免把噪声写入目标库。
var ErrLegacyMinuteCorrupt = errors.New("旧分钟库数据异常")

// legacyMinuteCorruptRatio 是判定整库损坏的丢弃比例阈值。正常股票库只会丢弃
// 年份不符或负价格的个别记录，远低于该比例。
const legacyMinuteCorruptRatio = 0.2

// LegacyMinuteRows 是旧分钟库的转换结果。
type LegacyMinuteRows struct {
	Rows    []*model.KlineMinute
	Dropped int // 时间不属于目标年份或价格/成交量为负的记录数
}

// ReadLegacyMinuteSource 读取旧分钟库（Kline 表或 KLM1/KLM2 压缩块），转换为
// 源时间分钟线。时间按东八区取源日期时分；成交量按市场口径换算为最小单位
// （股票为股，指数为手）。同一时间重复记录保留读取顺序中的最后一条。
func ReadLegacyMinuteSource(filename string, code model.Code, year int) (*LegacyMinuteRows, error) {
	ks, err := readMinuteFile(filename)
	if err != nil {
		return nil, err
	}
	info, ok := code.Market.Info()
	if !ok {
		return nil, fmt.Errorf("旧分钟库不支持的市场: %s", code.Market)
	}
	out := &LegacyMinuteRows{}
	byTime := map[string]*model.KlineMinute{}
	times := []string(nil)
	for _, k := range ks {
		if k == nil {
			out.Dropped++
			continue
		}
		at := k.Time.In(protocol.Location())
		if at.Year() != year || legacyMinuteInvalid(k) {
			out.Dropped++
			continue
		}
		volume, e := info.Minute.ToStorageVolume(k.Volume)
		if e != nil {
			return nil, fmt.Errorf("旧分钟库成交量换算: %w", e)
		}
		text := at.Format(protocol.TimeLayout)
		if _, exists := byTime[text]; !exists {
			times = append(times, text)
		}
		byTime[text] = &model.KlineMinute{
			Time: text, Open: k.Open, High: k.High, Low: k.Low, Close: k.Close,
			Volume: volume, Amount: &k.Amount,
		}
	}
	if len(ks) > 0 && float64(out.Dropped)/float64(len(ks)) > legacyMinuteCorruptRatio {
		return nil, fmt.Errorf("%w: 丢弃 %d/%d 条记录（时间或价格非法）", ErrLegacyMinuteCorrupt, out.Dropped, len(ks))
	}
	if len(times) > 0 {
		sort.Strings(times)
		out.Rows = make([]*model.KlineMinute, 0, len(times))
		for _, text := range times {
			out.Rows = append(out.Rows, byTime[text])
		}
	}
	return out, nil
}

// legacyMinuteInvalid 判断记录是否明显损坏。零价格是旧库的停牌占位记录，保留；
// 负数或非有限值不属于任何真实行情。
func legacyMinuteInvalid(k *protocol.Kline) bool {
	for _, v := range []float64{k.Last, k.Open, k.High, k.Low, k.Close, k.Amount} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return true
		}
	}
	return k.Volume < 0
}
