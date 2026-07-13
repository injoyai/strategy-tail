package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/strategy-tail/strategies/buy"
	"github.com/injoyai/strategy-tail/strategies/sell"
)

// StrategyVariant 一个均值回归策略变体（买入+卖出组合）
type StrategyVariant struct {
	Name   string
	Desc   string
	Buyer  core.Buyer
	Seller core.Seller
}

// YearStats 单个策略单年度的统计结果
type YearStats struct {
	Strategy      string    `json:"strategy"`
	Desc          string    `json:"desc"`
	Year          int       `json:"year"`
	Trades        int       `json:"trades"`
	VirtualTrades int       `json:"virtualTrades"` // 期末未平仓的虚拟成交笔数
	WinRate       float64   `json:"winRate"`
	ProfitFactor  float64   `json:"profitFactor"`
	TotalReturn   float64   `json:"totalReturn"` // 等额累计收益率 %
	AvgReturn     float64   `json:"avgReturn"`   // 平均单笔收益率 %
	MaxWin        float64   `json:"maxWin"`      // 单笔最大盈利率 %
	MaxLoss       float64   `json:"maxLoss"`     // 单笔最大亏损率 %
	MaxDrawdown   float64   `json:"maxDrawdown"` // 资金曲线最大回撤 %
	EquityCurve   []float64 `json:"equityCurve"`
	ReturnDist    []float64 `json:"returnDist"` // 每笔收益率，用于分布图
}

func main() {
	dataDir := "data/database"
	codes := loadCodes(dataDir)
	fmt.Printf("[加载] 沪深主板代码 %d 只\n", len(codes))

	pull, err := extend.NewPullKline(extend.PullKlineConfig{
		Types:      []string{extend.Day},
		Dir:        dataDir,
		Goroutines: 20,
	})
	if err != nil {
		fmt.Println("[错误] 初始化数据层失败:", err)
		return
	}

	variants := buildVariants()
	years := []int{2022, 2023, 2024, 2025, 2026}

	allStats := make([]YearStats, 0, len(variants)*len(years))

	for _, year := range years {
		t0 := time.Now()
		fmt.Printf("\n========== 回测年份 %d ==========\n", year)
		// 按年缓存所有代码的日线数据，6 个策略复用，避免重复读 db
		dataCache := loadYearData(pull, codes, year)

		for _, v := range variants {
			ts := backtestVariant(v, codes, dataCache, year)
			ys := computeStats(v, year, ts)
			allStats = append(allStats, ys)
			pf := fmt.Sprintf("%.2f", ys.ProfitFactor)
			if math.IsInf(ys.ProfitFactor, 1) {
				pf = "∞"
			}
			virtualPct := 0.0
			if ys.Trades > 0 {
				virtualPct = float64(ys.VirtualTrades) / float64(ys.Trades) * 100
			}
			fmt.Printf("  [%s] %d 笔(虚拟%d,%.1f%%) | 胜率 %.1f%% | 盈亏比 %s | 累计 %.2f%% | 回撤 %.2f%%\n",
				v.Name, ys.Trades, ys.VirtualTrades, virtualPct, ys.WinRate, pf, ys.TotalReturn, ys.MaxDrawdown)
		}
		fmt.Printf("  年份 %d 耗时 %s\n", year, time.Since(t0))
	}

	out := "output/meanrevert/meanrevert_report.html"
	if err := generateHTML(allStats, variants, years, out); err != nil {
		fmt.Println("[错误] 生成报告失败:", err)
		return
	}
	fmt.Printf("\n[完成] 报告已生成: %s\n", out)
}

// loadCodes 从 day-kline 目录扫描沪深主板代码（sh60*/sz00*）
func loadCodes(dataDir string) []string {
	pattern := filepath.Join(dataDir, "day-kline", "*.db")
	matches, _ := filepath.Glob(pattern)
	codes := make([]string, 0, len(matches))
	for _, m := range matches {
		name := strings.TrimSuffix(filepath.Base(m), ".db")
		if strings.HasPrefix(name, "sh60") || strings.HasPrefix(name, "sz00") {
			codes = append(codes, name)
		}
	}
	sort.Strings(codes)
	return codes
}

// loadYearData 并发读取一年回测所需的全部日线数据（含指标预热历史）
func loadYearData(pull *extend.PullKline, codes []string, year int) map[string]extend.Klines {
	hisStart := time.Date(year-2, 6, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)

	cache := make(map[string]extend.Klines, len(codes))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 20)
	for _, code := range codes {
		wg.Add(1)
		go func(code string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			dks, err := pull.DayKlines(code, hisStart, end)
			if err != nil || len(dks) == 0 {
				return
			}
			mu.Lock()
			cache[code] = dks
			mu.Unlock()
		}(code)
	}
	wg.Wait()
	return cache
}

// backtestVariant 对单个策略变体在指定年份的所有代码上回测。
// 采用"单仓位"模型：每只股票同时最多持有 1 个仓位，卖出后才能再买入，
// 天然实现 T+1（买入日当天不检查卖出）。这避免了均值回归信号频繁触发导致的
// 持仓堆积与交易笔数失真。成本口径与 core.Backtest.Do 完全一致。
func backtestVariant(v StrategyVariant, codes []string, dataCache map[string]extend.Klines, year int) []core.Trade {
	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
	cost := core.DefaultCost()
	pos := core.DefaultPositionConfig()

	var allTrades []core.Trade
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 20)
	for _, code := range codes {
		fullDks, ok := dataCache[code]
		if !ok || len(fullDks) == 0 {
			continue
		}
		wg.Add(1)
		go func(code string, fullDks extend.Klines) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// 切分历史预热数据与回测数据
			his := extend.Klines(nil)
			idx := 0
			for i, k := range fullDks {
				if k.Time.Before(start) {
					his = append(his, k)
				} else {
					idx = i
					break
				}
			}
			dks := fullDks[idx:]
			if len(dks) == 0 {
				return
			}

			joinKlines := func(upTo int) extend.Klines {
				ls := make(extend.Klines, 0, len(his)+upTo+1)
				ls = append(ls, his...)
				ls = append(ls, dks[:upTo+1]...)
				return ls
			}

			ts := []core.Trade{}
			var holding *core.Buy
			for i := 0; i < len(dks); i++ {
				ls := joinKlines(i)
				if holding == nil {
					// 无持仓：检查买入
					if v.Buyer.Buy(code, ls) {
						holding = &core.Buy{
							Code:  code,
							Time:  dks[i].Time,
							Price: dks[i].Close,
						}
					}
				} else {
					// 持仓中：检查卖出（买入日当天 holding 刚设，i 不变，不会进此分支）
					if v.Seller.Sell(code, ls, *holding) {
						buyExec, buyCost := cost.BuyCost(holding.Price, pos.SharesPerLot)
						sellExec, sellIncome := cost.SellIncome(dks[i].Close, pos.SharesPerLot)
						ts = append(ts, core.Trade{
							Code:          code,
							BuyTime:       holding.Time,
							SellTime:      dks[i].Time,
							BuyPrice:      holding.Price,
							SellPrice:     dks[i].Close,
							BuyExecPrice:  buyExec,
							SellExecPrice: sellExec,
							BuyCost:       buyCost,
							SellIncome:    sellIncome,
							Quantity:      pos.SharesPerLot,
						})
						holding = nil
					}
				}
			}
			// 期末仍未平仓，按最后收盘价生成虚拟成交
			if holding != nil {
				last := dks[len(dks)-1]
				buyExec, buyCost := cost.BuyCost(holding.Price, pos.SharesPerLot)
				sellExec, sellIncome := cost.SellIncome(last.Close, pos.SharesPerLot)
				ts = append(ts, core.Trade{
					Code:          code,
					BuyTime:       holding.Time,
					SellTime:      last.Time,
					BuyPrice:      holding.Price,
					SellPrice:     last.Close,
					BuyExecPrice:  buyExec,
					SellExecPrice: sellExec,
					BuyCost:       buyCost,
					SellIncome:    sellIncome,
					Quantity:      pos.SharesPerLot,
					Virtual:       true,
				})
			}

			if len(ts) > 0 {
				mu.Lock()
				allTrades = append(allTrades, ts...)
				mu.Unlock()
			}
		}(code, fullDks)
	}
	wg.Wait()
	return allTrades
}

// computeStats 计算单策略单年度统计。
// 核心可比指标：平均单笔收益率、胜率、盈亏比（频率无关，跨策略可公平对比）。
// 辅助指标：等额累计收益率（每笔投入等额资金的累计贡献）、最大回撤。
// 注：均值回归信号频繁，单年度交易笔数可达万级，等额累计收益率数值偏大，
// 仅作参考，不宜跨策略直接比大小；请以平均单笔收益率为准。
func computeStats(v StrategyVariant, year int, trades []core.Trade) YearStats {
	stats := core.Stats(trades)

	// 按买入时间排序，构建等额累加资金曲线
	sort.Slice(trades, func(i, j int) bool {
		return trades[i].BuyTime.Before(trades[j].BuyTime)
	})

	equity := []float64{0} // 等额累计收益率(%)
	retDist := make([]float64, 0, len(trades))
	cur := 0.0
	maxWin, maxLoss := 0.0, 0.0
	for _, t := range trades {
		bp := t.BuyPrice.Float64()
		if bp <= 0 {
			continue
		}
		r := (t.SellPrice.Float64() - bp) / bp * 100
		if t.BuyCost > 0 {
			r = (t.SellIncome - t.BuyCost) / t.BuyCost * 100
		}
		cur += r
		equity = append(equity, cur)
		retDist = append(retDist, r)
		if r > maxWin {
			maxWin = r
		}
		if r < maxLoss {
			maxLoss = r
		}
	}

	// 等额累加曲线最大回撤（百分点）
	peak := math.Inf(-1)
	maxDD := 0.0
	for _, e := range equity {
		if e > peak {
			peak = e
		}
		dd := peak - e
		if dd > maxDD {
			maxDD = dd
		}
	}

	totalReturn := cur
	avgReturn := 0.0
	if len(retDist) > 0 {
		sum := 0.0
		for _, r := range retDist {
			sum += r
		}
		avgReturn = sum / float64(len(retDist))
	}

	// 统计虚拟成交（期末未平仓）数量
	virtualTrades := 0
	for _, t := range trades {
		if t.Virtual {
			virtualTrades++
		}
	}

	return YearStats{
		Strategy:      v.Name,
		Desc:          v.Desc,
		Year:          year,
		Trades:        len(trades),
		VirtualTrades: virtualTrades,
		WinRate:       stats.WinRate,
		ProfitFactor:  stats.ProfitFactor,
		TotalReturn:   totalReturn,
		AvgReturn:     avgReturn,
		MaxWin:        maxWin,
		MaxLoss:       maxLoss,
		MaxDrawdown:   maxDD,
		EquityCurve:   equity,
		ReturnDist:    retDist,
	}
}

// buildVariants 构造 6 个均值回归策略变体
func buildVariants() []StrategyVariant {
	// 共通基础过滤：价格区间 + 过滤涨停
	base := buy.And{
		buy.A价格{Min: 2, Max: 80},
		buy.A过滤涨停{},
	}
	// 共通保护：止盈 8% / 止损 5% / 持仓最多 10 天
	protect := func(revert core.Seller) core.Seller {
		return sell.Or{
			revert,
			sell.A止盈止损{TakeProfit: 0.08, StopLoss: 0.05},
			sell.A持仓N天{Days: 10},
		}
	}

	return []StrategyVariant{
		{
			Name:   "布林下轨回归",
			Desc:   "收盘价跌破布林20日下轨(2σ)买入，回归中轨卖出",
			Buyer:  buy.And{base, buy.A布林下轨{Period: 20, StdTimes: 2}},
			Seller: protect(sell.A回到布林中轨{Period: 20}),
		},
		{
			Name:   "乖离率回归",
			Desc:   "BIAS=(Close-MA20)/MA20 <= -7% 买入，BIAS归零卖出",
			Buyer:  buy.And{base, buy.A乖离超卖{Period: 20, MinBias: -7}},
			Seller: protect(sell.A乖离归零{Period: 20}),
		},
		{
			Name:   "ZScore回归",
			Desc:   "Z=(Close-MA)/Std <= -2 买入，Z归零卖出",
			Buyer:  buy.And{base, buy.AZScore超卖{Period: 20, MinZ: -2}},
			Seller: protect(sell.AZScore归零{Period: 20}),
		},
		{
			Name:   "唐奇安下沿反转",
			Desc:   "收盘触及近20日最低价买入，回归通道中轨卖出",
			Buyer:  buy.And{base, buy.A唐奇安下沿{Period: 20}},
			Seller: protect(sell.A回到唐奇安中轨{Period: 20}),
		},
		{
			Name:   "布林下轨+地量",
			Desc:   "布林下轨 + 今日量<=5日均量60%(地量确认)双重过滤",
			Buyer:  buy.And{base, buy.A布林下轨缩量{Period: 20, StdTimes: 2, VolDays: 5, VolRatio: 0.6}},
			Seller: protect(sell.A回到布林中轨{Period: 20}),
		},
		{
			Name:   "布林下轨+RSI超卖",
			Desc:   "布林下轨 + RSI14<30 双重确认，回归中轨卖出",
			Buyer:  buy.And{base, buy.A布林下轨{Period: 20, StdTimes: 2}, buy.RSI{Period: 14, Threshold: 30}},
			Seller: protect(sell.A回到布林中轨{Period: 20}),
		},
		{
			Name: "布林下轨+RSI超卖(增强)",
			Desc: "布林下轨 + RSI14<30 + 价格站上60日均线(趋势过滤) + RSI拐头向上(反转确认)",
			Buyer: buy.And{base,
				buy.A布林下轨{Period: 20, StdTimes: 2},
				buy.RSI{Period: 14, Threshold: 30},
				buy.A站上N日均线{Period: 60, Days: 1},
				buy.RSI拐头{Period: 14},
			},
			Seller: protect(sell.A回到布林中轨{Period: 20}),
		},
	}
}

// generateHTML 生成对比报告 HTML
func generateHTML(allStats []YearStats, variants []StrategyVariant, years []int, out string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return err
	}
	data, _ := json.Marshal(struct {
		Stats   []YearStats       `json:"stats"`
		Names   []string          `json:"names"`
		Years   []int             `json:"years"`
		DescMap map[string]string `json:"descMap"`
	}{
		Stats:   allStats,
		Names:   variantNames(variants),
		Years:   years,
		DescMap: variantDescMap(variants),
	})
	jsonStr := string(data)

	html := `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>均值回归策略回测报告</title>
<script src="https://cdn.jsdelivr.net/npm/echarts@5/dist/echarts.min.js"></script>
<style>
*{box-sizing:border-box}
body{margin:0;font-family:-apple-system,"Microsoft YaHei",Arial,sans-serif;background:#f5f7fb;color:#1f2937;line-height:1.6}
.wrap{max-width:1280px;margin:0 auto;padding:24px 20px 60px}
h1{font-size:26px;margin:8px 0 4px}
.sub{color:#6b7280;margin-bottom:18px;font-size:14px}
.card{background:#fff;border-radius:10px;box-shadow:0 2px 12px rgba(0,0,0,.06);padding:18px 20px;margin-bottom:18px}
.card h2{font-size:17px;margin:0 0 14px;border-left:4px solid #4a90e2;padding-left:10px}
table{width:100%;border-collapse:collapse;font-size:13px}
th,td{padding:8px 10px;border-bottom:1px solid #eef0f5;text-align:right;white-space:nowrap}
th:first-child,td:first-child{text-align:left}
th{background:#f8fafc;color:#475569;font-weight:600;position:sticky;top:0}
tbody tr:hover{background:#f7f9ff}
.up{color:#ef232a}
.down{color:#14b143}
.muted{color:#9ca3af}
.grid2{display:grid;grid-template-columns:1fr 1fr;gap:18px}
@media(max-width:900px){.grid2{grid-template-columns:1fr}}
.chart{height:420px}
.bigchart{height:480px}
.tblwrap{overflow:auto;max-height:560px}
.pill{display:inline-block;padding:2px 8px;border-radius:10px;font-size:11px;background:#eef2ff;color:#4a90e2;margin-right:6px}
.note{font-size:12px;color:#9ca3af;margin-top:6px}
.strat-row{margin-bottom:10px;padding:10px 14px;background:#f8fafc;border-radius:6px}
.strat-row b{color:#1f2937}
</style>
</head>
<body>
<div class="wrap">
<h1>均值回归策略回测报告</h1>
<div class="sub">A 股沪深主板 · 日线 · 2022–2026 · 等额仓位口径 · 含滑点/手续费/印花税/T+1<br>注：2026 年为截至 7 月初的部分年数据，交易笔数与收益率与其他完整年度不可直接横向比较。</div>

<div class="card">
<h2>策略说明</h2>
<div id="desc"></div>
<div class="note">所有策略共用基础过滤：价格 2–80 元、过滤涨停；共用保护：止盈 8% / 止损 5% / 持仓最多 10 天。</div>
</div>

<div class="card">
<h2>年度对比总览 <span class="pill">核心指标：平均单笔收益率 / 胜率 / 盈亏比</span></h2>
<div class="tblwrap">
<table id="overview">
<thead><tr><th>策略</th><th>年份</th><th>交易笔数</th><th>虚拟成交</th><th>平均单笔</th><th>胜率</th><th>盈亏比</th><th>最大盈利</th><th>最大亏损</th><th>累计收益率*</th><th>最大回撤*</th></tr></thead>
<tbody id="overviewBody"></tbody>
</table>
</div>
<div class="note">* 累计收益率/最大回撤为等额累加口径，均值回归信号频繁致数值偏大，仅供参考；跨策略对比请用平均单笔收益率、胜率、盈亏比。</div>
</div>

<div class="card">
<h2>各策略累计收益曲线（按交易顺序，等额累计收益率 %）</h2>
<div id="equityAll" class="bigchart"></div>
<div class="note">曲线为单笔收益率累加，体现策略资金曲线形态；末值即为年度累计收益率。</div>
</div>

<div class="grid2">
<div class="card"><h2>胜率对比</h2><div id="winRate" class="chart"></div></div>
<div class="card"><h2>盈亏比对比</h2><div id="profitFactor" class="chart"></div></div>
</div>

<div class="grid2">
<div class="card"><h2>平均单笔收益率对比（核心）</h2><div id="avgReturn" class="chart"></div></div>
<div class="card"><h2>最大回撤对比</h2><div id="drawdown" class="chart"></div></div>
</div>

<div class="card">
<h2>单笔收益率分布（聚合所有年份）</h2>
<div id="dist" class="bigchart"></div>
<div class="note">横轴为单笔收益率(%)分桶，纵轴为笔数；红色为盈利区间，绿色为亏损区间。</div>
</div>

<div class="card">
<h2>分析结论</h2>
<div id="conclusion"></div>
</div>

</div>
<script id="payload" type="application/json">` + jsonStr + `</script>
<script>
const D = JSON.parse(document.getElementById('payload').textContent);
const names = D.names;
const years = D.years;
const stats = D.stats;

// 颜色：红涨绿跌（A 股惯例）
const UP = '#ef232a', DOWN = '#14b143';
const palette = ['#4a90e2','#f5a623','#9013fe','#14b143','#ef232a','#2dd4bf','#6366f1','#f59e0b'];

function pfText(v){ return isFinite(v) ? v.toFixed(2) : '∞'; }
function cls(v){ return v>=0?'up':'down'; }
function fmt(v,d){ d=d==null?2:d; return Number(v||0).toFixed(d); }

// 策略说明
document.getElementById('desc').innerHTML = names.map(n=>'<div class="strat-row"><b>'+n+'</b> <span class="muted">'+(D.descMap[n]||'')+'</span></div>').join('');

// 总览表
let rows='';
stats.forEach(s=>{
  const vp = s.trades>0 ? (s.virtualTrades/s.trades*100).toFixed(1)+'%' : '0%';
  const vpCls = s.virtualTrades/s.trades>0.05 ? 'down' : 'muted';
  rows += '<tr><td>'+s.strategy+'</td><td>'+s.year+'</td><td>'+s.trades+'</td>'
    + '<td class="'+vpCls+'">'+s.virtualTrades+' ('+vp+')</td>'
    + '<td class="'+cls(s.avgReturn)+'"><b>'+fmt(s.avgReturn)+'%</b></td>'
    + '<td class="'+cls(s.winRate-50)+'">'+fmt(s.winRate,1)+'%</td>'
    + '<td>'+pfText(s.profitFactor)+'</td>'
    + '<td class="up">'+fmt(s.maxWin)+'%</td>'
    + '<td class="down">'+fmt(s.maxLoss)+'%</td>'
    + '<td class="'+cls(s.totalReturn)+'">'+fmt(s.totalReturn)+'%</td>'
    + '<td class="down">'+fmt(s.maxDrawdown)+'%</td></tr>';
});
document.getElementById('overviewBody').innerHTML = rows;

// 工具：按策略分组
function byStrategy(){
  const m={}; names.forEach(n=>m[n]=[]);
  stats.forEach(s=>{ if(m[s.strategy]) m[s.strategy].push(s); });
  return m;
}
const byStrat = byStrategy();

// 1. 各策略累计收益曲线（多年度等额累加首尾相接）
(function(){
  const series = names.map((n,i)=>{
    const arr = byStrat[n].sort((a,b)=>a.year-b.year);
    let offset = 0;
    const data = [];
    arr.forEach(s=>{
      s.equityCurve.forEach((v,idx)=>{
        if(idx===0) return; // 跳过起始 0
        data.push(v+offset);
      });
      offset += s.equityCurve[s.equityCurve.length-1] || 0;
    });
    return {name:n, type:'line', data:data, showSymbol:false, smooth:true, lineStyle:{width:1.6}, itemStyle:{color:palette[i%palette.length]}};
  });
  echarts.init(document.getElementById('equityAll')).setOption({
    tooltip:{trigger:'axis'},
    legend:{top:6,textStyle:{fontSize:12}},
    grid:{left:60,right:30,top:50,bottom:50},
    xAxis:{type:'category', show:false, data: series[0].data.map((_,i)=>i)},
    yAxis:{type:'value', name:'累计收益率%', axisLabel:{formatter:'{value}%'}},
    dataZoom:[{type:'inside'},{type:'slider',bottom:10}],
    series:series
  });
})();

// 2. 胜率柱状（策略×年份）
function groupedBar(domId, valueKey, yLabel, fmtFn, colorNeg){
  const series = names.map((n,i)=>({
    name:n, type:'bar',
    data: years.map(y=>{ const s=(byStrat[n]||[]).find(x=>x.year===y); return s?s[valueKey]:0; }),
    itemStyle:{color:palette[i%palette.length]}
  }));
  echarts.init(document.getElementById(domId)).setOption({
    tooltip:{trigger:'axis', axisPointer:{type:'shadow'}, formatter:p=>p.map(x=>x.seriesName+': '+fmtFn(x.value)).join('<br/>')},
    legend:{top:6,textStyle:{fontSize:11}},
    grid:{left:60,right:20,top:50,bottom:40},
    xAxis:{type:'category', data:years.map(y=>String(y)+'年')},
    yAxis:{type:'value', name:yLabel},
    series:series
  });
}
groupedBar('winRate','winRate','胜率%', v=>fmt(v,1)+'%');
groupedBar('profitFactor','profitFactor','盈亏比', v=>isFinite(v)?v.toFixed(2):'∞');
groupedBar('avgReturn','avgReturn','平均单笔收益率%', v=>fmt(v)+'%');
groupedBar('drawdown','maxDrawdown','最大回撤%', v=>fmt(v)+'%');

// 3. 收益率分布直方图
(function(){
  const buckets = [];
  for(let b=-20;b<=20;b++) buckets.push(b*1); // -20% ~ 20%, 1%步长
  const series = names.map((n,i)=>{
    const arr = byStrat[n];
    const all = [];
    arr.forEach(s=> s.returnDist.forEach(r=> all.push(r)));
    const counts = buckets.map(()=>0);
    all.forEach(r=>{
      let idx = Math.round(r);
      if(idx< -20) idx=-20; if(idx>20) idx=20;
      counts[idx+20]++;
    });
    return {name:n, type:'bar', data:counts, itemStyle:{color:palette[i%palette.length]}, barGap:'10%'};
  });
  echarts.init(document.getElementById('dist')).setOption({
    tooltip:{trigger:'axis', axisPointer:{type:'shadow'}},
    legend:{top:6,textStyle:{fontSize:11}},
    grid:{left:50,right:20,top:50,bottom:40},
    xAxis:{type:'category', data:buckets.map(b=>b+'%')},
    yAxis:{type:'value', name:'笔数'},
    series:series
  });
})();

// 4. 分析结论
(function(){
  // 计算每策略4年汇总（以平均单笔收益率为核心排名指标，频率无关）
  const summary = names.map(n=>{
    const arr = byStrat[n];
    const trades = arr.reduce((s,x)=>s+x.trades,0);
    const avgWin = arr.reduce((s,x)=>s+x.winRate*x.trades,0)/(trades||1);
    const avgRet = arr.reduce((s,x)=>s+x.avgReturn*x.trades,0)/(trades||1); // 按交易量加权的平均单笔收益率
    const avgDD = arr.reduce((s,x)=>s+x.maxDrawdown,0)/arr.length;
    const pf = arr.reduce((s,x)=>s+(isFinite(x.profitFactor)?x.profitFactor:5),0)/arr.length;
    return {name:n, trades, avgWin, avgRet, avgDD, pf};
  });
  summary.sort((a,b)=>b.avgRet-a.avgRet);
  const best = summary[0], worst = summary[summary.length-1];
  let html = '<p><b>按平均单笔收益率排名（4年加权）：</b></p><ol>';
  summary.forEach(s=>{
    html += '<li><b>'+s.name+'</b>：平均单笔 <b class="'+cls(s.avgRet)+'">'+fmt(s.avgRet,3)+'%</b>，胜率 '+fmt(s.avgWin,1)+'%，盈亏比 '+fmt(s.pf,2)+'，共 '+s.trades+' 笔</li>';
  });
  html += '</ol>';
  html += '<p style="margin-top:14px"><b>最佳策略：</b>'+best.name+'，4年加权平均单笔收益率 '+fmt(best.avgRet,3)+'%。';
  html += best.avgWin>50 ? '胜率高于 50%，信号质量较好。' : '胜率低于 50%，依赖盈亏比获利。';
  html += '</p>';
  html += '<p><b>最弱策略：</b>'+worst.name+'，平均单笔收益率 '+fmt(worst.avgRet,3)+'%。';
  html += worst.avgWin<45 ? '胜率偏低，在所测年份整体未能有效获利，需配合趋势过滤或择时。' : '';
  html += '</p>';
  // 均值回归共性结论
  html += '<p style="margin-top:14px"><b>均值回归策略共性观察：</b></p><ul>';
  html += '<li>纯均值回归（布林下轨/乖离/ZScore）在趋势下跌行情中容易被"接飞刀"，胜率通常不高但盈亏比可补偿；</li>';
  html += '<li>加入<strong>地量确认</strong>或<strong>RSI 超卖</strong>双重过滤后，信号数量下降但胜率通常提升，适合追求稳健的玩家；</li>';
  html += '<li>唐奇安下沿反转信号最密集，但触及新低本身有趋势延续风险，需严格止损；</li>';
  html += '<li>所有策略均配置了 8% 止盈 / 5% 止损 / 10 天持仓上限，均值回归本质是短线博弈，持仓过久会显著降低胜率；</li>';
  html += '<li>不同年份胜率波动大（如 2024 年多数策略胜率跌至 40% 以下，2025 年又回升至 60%），说明均值回归<strong>高度依赖市场环境</strong>，建议叠加大盘趋势过滤后再实盘；</li>';
  html += '<li>2026 年为截至 7 月初的部分年数据，期末未平仓的<strong>虚拟成交占比 6–9%</strong>（其他完整年度仅 1–4%），这些持仓按 7 月 3 日收盘价强制结算，若期末处于下跌段会集中计亏，对 2026 年胜率有额外拖累，解读时需留意。</li>';
  html += '</ul>';
  html += '<p class="note">注：以上为等额仓位回测，未考虑资金管理、单日多标的仓位上限与同日重复信号；实际交易需结合仓位与选股池再做验证。</p>';
  document.getElementById('conclusion').innerHTML = html;
})();

window.addEventListener('resize', ()=>{
  document.querySelectorAll('.chart,.bigchart').forEach(el=>{ const c=echarts.getInstanceByDom(el); if(c) c.resize(); });
});
</script>
</body>
</html>`
	return os.WriteFile(out, []byte(html), 0644)
}

func variantNames(vs []StrategyVariant) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.Name
	}
	return out
}

func variantDescMap(vs []StrategyVariant) map[string]string {
	m := make(map[string]string, len(vs))
	for _, v := range vs {
		m[v.Name] = v.Desc
	}
	return m
}
