package core

import (
	"fmt"
	"sort"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// ============================================================================
// 参数网格搜索（Stage 3）
// 本模块提供单参数网格搜索能力，用于自动化寻优。
// 遍历一组参数取值，对每个取值执行多年份回测并采集分析指标，
// 最终输出结构化结果，便于后续排序、筛选和可视化。
// ============================================================================

// GridSearchResult 参数网格搜索的单条结果记录。
// 每条记录对应"一个参数取值 × 一个年份"的回测统计快照。
type GridSearchResult struct {
	ParamName      string  // 参数名称（如 "StopLoss"、"TakeProfit"）
	ParamValue     float64 // 参数取值
	Year           int     // 回测年份
	TotalTrades    int     // 总交易笔数
	WinRate        float64 // 胜率（%）
	AnnualReturn   float64 // 年化收益率（%）
	MaxDrawdownPct float64 // 最大回撤率（%）
	Sharpe         float64 // 夏普比率（年化）
	ProfitFactor   float64 // 盈亏比
}

// GridSearch 对回测引擎执行单参数网格搜索。
//
// 遍历 paramValues 中的每个取值，通过 paramSetter 将参数注入回测实例，
// 然后对每个年份调用 _backtest 执行回测，再用 Analyze 计算分析指标，
// 最终收集所有结果。
//
// 参数:
//   - bt: 回测引擎模板，每个参数取值会基于其副本运行，互不污染
//   - paramName: 参数名称（仅用于结果标注，不参与回测逻辑）
//   - paramValues: 待搜索的参数取值列表
//   - paramSetter: 参数注入函数，将指定值设置到回测引擎上
//
// 返回: 按 参数名 → 参数值 → 年份 升序排列的结果切片
func GridSearch(bt Backtest, paramName string, paramValues []float64, paramSetter func(bt *Backtest, v float64)) []GridSearchResult {
	results := make([]GridSearchResult, 0, len(paramValues)*len(bt.Years))

	for _, v := range paramValues {
		// 复制回测实例，避免不同参数取值之间互相污染
		b := bt
		// 通过 setter 将参数注入回测引擎
		paramSetter(&b, v)
		// 填充默认值（成本模型、仓位管理、风控层）
		//b.applyDefaults()

		// 逐年份执行回测并计算分析指标
		for _, year := range b.Years {
			trades, err := b._backtest(b.Codes, year)
			if err != nil {
				continue
			}

			// 调用 Analyze 计算年化收益、回撤、夏普等指标
			// 基准日线传 nil（网格搜索阶段暂不对比基准）
			ar := Analyze(year, trades, func(code string) (extend.Klines, error) {
				return b.GetDayKlines(code, time.Time{}, time.Now())
			}, nil, b.Cost, b.Position)

			results = append(results, GridSearchResult{
				ParamName:      paramName,
				ParamValue:     v,
				Year:           year,
				TotalTrades:    ar.TotalTrades,
				WinRate:        ar.WinRate,
				AnnualReturn:   ar.AnnualReturn,
				MaxDrawdownPct: ar.MaxDrawdownPct,
				Sharpe:         ar.Sharpe,
				ProfitFactor:   ar.ProfitFactor,
			})
		}
	}

	// 按 参数名、参数值、年份 升序排序
	sort.Slice(results, func(i, j int) bool {
		if results[i].ParamName != results[j].ParamName {
			return results[i].ParamName < results[j].ParamName
		}
		if results[i].ParamValue != results[j].ParamValue {
			return results[i].ParamValue < results[j].ParamValue
		}
		return results[i].Year < results[j].Year
	})

	return results
}

// GridSearchHeatmap 基于两组参数的网格搜索结果构建二维热力图。
//
// 外层 map 键为 paramA 的取值（格式化为字符串），内层 map 键为 paramB 的取值，
// 值为对应参数组合下的指标（如年化收益率均值）。
//
// 注意：当前 GridSearch 仅支持单参数搜索，真正的二维热力图
// 需要多参数联合搜索（Stage 4 实现），此处为占位桩函数。
func GridSearchHeatmap(results []GridSearchResult, paramA, paramB string) map[string]map[string]float64 {
	heatmap := make(map[string]map[string]float64)
	// TODO: Stage 4 实现多参数联合搜索后，在此填充热力图数据。
	// 预期逻辑：
	//   1. 从 results 中筛选 ParamName == paramA 和 ParamName == paramB 的记录
	//   2. 按年份对齐，聚合同一参数组合下的指标均值
	//   3. 以 paramA 取值为行、paramB 取值为列填入 heatmap
	return heatmap
}

// String 格式化输出单条网格搜索结果，便于日志打印和调试。
func (r GridSearchResult) String() string {
	return fmt.Sprintf("[%s=%.4f] 年份=%d 交易=%d 胜率=%.2f%% 年化=%.2f%% 回撤=%.2f%% 夏普=%.2f 盈亏比=%.2f",
		r.ParamName, r.ParamValue, r.Year, r.TotalTrades,
		r.WinRate, r.AnnualReturn, r.MaxDrawdownPct, r.Sharpe, r.ProfitFactor)
}
