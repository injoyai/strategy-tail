package core

import (
	"fmt"
	"sort"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// ============================================================================
// 前瞻偏差审计 (Stage 3)
// ============================================================================

// AuditResult 审计结果。
// Passed 为 true 表示未发现任何前瞻偏差或数据质量问题。
// Issues 记录所有发现的问题描述。
type AuditResult struct {
	Passed            bool
	Issues            []string
	StrategyName      string
	TradeCount        int
	DataPointsChecked int
}

// AuditLookAhead 审计交易记录是否存在前瞻偏差 (look-ahead bias)。
//
// 审计逻辑:
//  1. 每笔交易的买入时间不得晚于卖出时间(时间倒置通常意味着使用了未来数据)。
//  2. 买入/卖出日期必须在真实K线数据中存在,否则视为可疑(可能引用了虚构的未来日期)。
//  3. 买入/卖出的成交价(含滑点)必须落在当日日线 [Low, High]±滑点 区间内。
//     成交价超出当日实际波动区间,说明策略可能使用了尚未发生的未来价格。
//     用区间而非"==收盘价",是为了兼容盘内(分钟级)成交价与滑点偏离,
//     同时仍能抓出引用了别日价格的真前视偏差。
//
// cost 用于确定滑点容差;getDayKlines 用于按代码拉取完整日线数据,内部按代码做缓存避免重复拉取。
// 返回的 AuditResult.Passed 在未发现任何问题时为 true。
func AuditLookAhead(trades []Trade, cost Cost, getDayKlines func(code string) (extend.Klines, error)) AuditResult {
	result := AuditResult{
		TradeCount: len(trades),
		Issues:     []string{},
	}

	// 按代码缓存日线数据及其 "日期 -> (Low,High)" 索引,避免重复拉取与构建
	type dayRange struct {
		Low, High protocol.Price
	}
	type codeData struct {
		index map[string]dayRange
		err   error
	}
	cache := make(map[string]*codeData)

	getIndex := func(code string) (*codeData, error) {
		if cd, ok := cache[code]; ok {
			return cd, cd.err
		}
		cd := &codeData{index: make(map[string]dayRange)}
		dks, err := getDayKlines(code)
		if err != nil {
			cd.err = err
			cache[code] = cd
			return cd, err
		}
		for _, k := range dks {
			cd.index[k.Time.Format(time.DateOnly)] = dayRange{Low: k.Low, High: k.High}
		}
		cache[code] = cd
		return cd, nil
	}

	slip := cost.Slippage
	for i, t := range trades {
		idx := i + 1

		// 1. 买入时间不得晚于卖出时间
		if t.BuyTime.After(t.SellTime) {
			result.Issues = append(result.Issues, fmt.Sprintf(
				"交易#%d [%s] 买入时间 %s 晚于卖出时间 %s,时间倒置(疑似使用未来数据)",
				idx, t.Code,
				t.BuyTime.Format(time.DateOnly),
				t.SellTime.Format(time.DateOnly),
			))
			// 时间倒置时价格比对失去意义,跳过后续检查
			continue
		}

		// 拉取(或复用缓存)该代码的日线数据
		cd, err := getIndex(t.Code)
		if err != nil {
			result.Issues = append(result.Issues, fmt.Sprintf(
				"交易#%d [%s] 拉取日线数据失败: %v", idx, t.Code, err,
			))
			continue
		}

		// 2. 校验买入成交价落在买入当日 [Low,High]±滑点 区间内
		buyDate := t.BuyTime.Format(time.DateOnly)
		if dr, ok := cd.index[buyDate]; !ok {
			result.Issues = append(result.Issues, fmt.Sprintf(
				"交易#%d [%s] 买入日期 %s 在K线数据中不存在(可能引用虚构未来日期)",
				idx, t.Code, buyDate,
			))
		} else {
			result.DataPointsChecked++
			lo, hi := dr.Low-slip, dr.High+slip
			if t.BuyExecPrice < lo || t.BuyExecPrice > hi {
				result.Issues = append(result.Issues, fmt.Sprintf(
					"交易#%d [%s] 买入成交价 %.3f 超出 %s 日线区间 [%.3f, %.3f]±滑点(疑似前瞻偏差)",
					idx, t.Code, t.BuyExecPrice.Float64(), buyDate,
					dr.Low.Float64(), dr.High.Float64(),
				))
			}
		}

		// 3. 校验卖出成交价落在卖出当日 [Low,High]±滑点 区间内
		sellDate := t.SellTime.Format(time.DateOnly)
		if dr, ok := cd.index[sellDate]; !ok {
			result.Issues = append(result.Issues, fmt.Sprintf(
				"交易#%d [%s] 卖出日期 %s 在K线数据中不存在(可能引用虚构未来日期)",
				idx, t.Code, sellDate,
			))
		} else {
			result.DataPointsChecked++
			lo, hi := dr.Low-slip, dr.High+slip
			if t.SellExecPrice < lo || t.SellExecPrice > hi {
				result.Issues = append(result.Issues, fmt.Sprintf(
					"交易#%d [%s] 卖出成交价 %.3f 超出 %s 日线区间 [%.3f, %.3f]±滑点(疑似前瞻偏差)",
					idx, t.Code, t.SellExecPrice.Float64(), sellDate,
					dr.Low.Float64(), dr.High.Float64(),
				))
			}
		}
	}

	result.Passed = len(result.Issues) == 0
	return result
}

// AuditDataQuality 检查日线数据序列的数据质量问题,返回问题描述列表。
//
// 检查项:
//  1. 日期间隔 > 7 个自然日(排除法定节假日,如春节/国庆等长假窗口);
//  2. 成交量为 0 的交易日(停牌或数据缺失);
//  3. 负价格(Open/High/Low/Close < 0,脏数据);
//  4. 重复日期(同一交易日存在多条K线)。
//
// 空序列返回空列表。输入序列会被先按时间排序后再做间隔检查(不修改原切片)。
func AuditDataQuality(dks extend.Klines) []string {
	issues := []string{}
	if len(dks) == 0 {
		return issues
	}

	// 复制一份并按时间排序,避免修改调用方切片,同时保证间隔检查的连续性
	sorted := make(extend.Klines, len(dks))
	copy(sorted, dks)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Time.Before(sorted[j].Time)
	})

	seen := make(map[string]bool) // 日期去重
	for i, k := range sorted {
		date := k.Time.Format(time.DateOnly)

		// 重复日期
		if seen[date] {
			issues = append(issues, fmt.Sprintf(
				"重复日期: %s 存在多条K线记录", date,
			))
		}
		seen[date] = true

		// 负价格
		if k.Open < 0 || k.High < 0 || k.Low < 0 || k.Close < 0 {
			issues = append(issues, fmt.Sprintf(
				"负价格: %s 开%.3f 高%.3f 低%.3f 收%.3f",
				date, k.Open.Float64(), k.High.Float64(),
				k.Low.Float64(), k.Close.Float64(),
			))
		}

		// 零成交量
		if k.Volume <= 0 {
			issues = append(issues, fmt.Sprintf(
				"零成交量: %s 成交量为 %d", date, k.Volume,
			))
		}

		// 日期间隔 > 7 自然日(排除节假日长假)
		if i > 0 {
			prev := sorted[i-1].Time
			gapDays := daysBetween(prev, k.Time)
			if gapDays > 7 && !isLikelyHolidayGap(prev, k.Time) {
				issues = append(issues, fmt.Sprintf(
					"日期缺口: %s 至 %s 间隔 %d 天(超过7天,非节假日)",
					prev.Format(time.DateOnly), date, gapDays,
				))
			}
		}
	}

	return issues
}

// daysBetween 计算两个时间之间的自然日天数(忽略时分秒)。
func daysBetween(prev, next time.Time) int {
	a := time.Date(prev.Year(), prev.Month(), prev.Day(), 0, 0, 0, 0, prev.Location())
	b := time.Date(next.Year(), next.Month(), next.Day(), 0, 0, 0, 0, next.Location())
	return int(b.Sub(a).Hours() / 24)
}

// isLikelyHolidayGap 判断两个相邻交易日之间的间隔是否落在A股法定长假窗口内。
// A股会产生 >7 自然日间隔的仅有春节和国庆两个长假:
//   - 国庆: 10月1日 - 10月7日
//   - 春节: 日期逐年浮动,近似取 1月20日 - 2月25日
//
// 落在上述窗口且间隔 <= 11 天的,视为正常节假日,不作为数据缺口上报。
func isLikelyHolidayGap(prev, next time.Time) bool {
	gapDays := daysBetween(prev, next)
	if gapDays <= 7 {
		return false
	}
	// 国庆长假窗口
	if intervalOverlapsMonthDay(prev, next, 10, 1, 10, 7) && gapDays <= 11 {
		return true
	}
	// 春节长假窗口(近似)
	if intervalOverlapsMonthDay(prev, next, 1, 20, 2, 25) && gapDays <= 11 {
		return true
	}
	return false
}

// intervalOverlapsMonthDay 判断 [prev, next] 区间内是否存在某天的"月-日"
// 落在 [startMonth/startDay, endMonth/endDay] 范围内。
// 用于识别长假窗口。区间长度有限(<= 数十天),逐日遍历开销可忽略。
func intervalOverlapsMonthDay(prev, next time.Time, startMonth, startDay, endMonth, endDay int) bool {
	t := time.Date(prev.Year(), prev.Month(), prev.Day(), 0, 0, 0, 0, prev.Location())
	end := time.Date(next.Year(), next.Month(), next.Day(), 0, 0, 0, 0, next.Location())
	for t.Before(end) || t.Equal(end) {
		if inMonthDayRange(int(t.Month()), t.Day(), startMonth, startDay, endMonth, endDay) {
			return true
		}
		t = t.AddDate(0, 0, 1)
	}
	return false
}

// inMonthDayRange 判断 (month, day) 是否落在跨月范围 [sm/sd, em/ed] 内。
// 要求 sm <= em(同一公历年内,不跨年)。
func inMonthDayRange(month, day, sm, sd, em, ed int) bool {
	switch {
	case sm == em:
		return month == sm && day >= sd && day <= ed
	case month == sm && day >= sd:
		return true
	case month == em && day <= ed:
		return true
	case month > sm && month < em:
		return true
	default:
		return false
	}
}

// String 将审计结果格式化为可读的多行文本。
func (r AuditResult) String() string {
	status := "通过 (PASS)"
	if !r.Passed {
		status = "未通过 (FAIL)"
	}
	name := r.StrategyName
	if name == "" {
		name = "(未命名)"
	}
	out := fmt.Sprintf(
		"前瞻偏差审计: %s\n策略: %s\n交易笔数: %d\n检查数据点: %d\n问题数: %d",
		status, name, r.TradeCount, r.DataPointsChecked, len(r.Issues),
	)
	if len(r.Issues) > 0 {
		out += "\n问题列表:"
		for i, issue := range r.Issues {
			out += fmt.Sprintf("\n  %d. %s", i+1, issue)
		}
	}
	return out
}
