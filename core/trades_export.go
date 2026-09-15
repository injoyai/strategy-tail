package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/goutil/oss/csv"
)

// ============================================================================
// 通用交易记录落盘与可视化
//
// 约定（见 AGENTS.md 6.1）：任何策略回测入口都必须将交易明细落盘。
// 与 Analyze()（按年份组织、内部使用）不同，本文件提供按"策略名"组织的
// 通用导出，适配参数矩阵等多组合场景：
//   - ExportTradesCSV  每组合一个 CSV，output/trades/<策略名>/<文件名>.csv
//   - ExportTradesHTML 汇总报告，output/trades/<策略名>/<文件名>.html
// ============================================================================

// TradesExportName 将任意名称转为安全的文件名（替换路径非法字符）。
func TradesExportName(name string) string {
	repl := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_", " ", "_",
	)
	return repl.Replace(name)
}

// ExportTradesCSV 将一组交易明细导出为 CSV。
// 返回写入的文件路径；trades 为空时不生成文件，返回空字符串。
//
// data 为表头，每行: 代码, 买入时间, 买入价, 卖出时间, 卖出价, 数量, 盈亏(元), 收益率(%), 持仓天数, 期末未平仓
func ExportTradesCSV(strategyName, filename string, trades []Trade) string {
	if len(trades) == 0 {
		return ""
	}

	sorted := make([]Trade, len(trades))
	copy(sorted, trades)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].BuyTime.Before(sorted[j].BuyTime) })

	data := [][]any{
		{"代码", "买入时间", "买入价", "卖出时间", "卖出价", "数量", "盈亏(元)", "收益率(%)", "持仓天数", "期末未平仓"},
	}
	for _, t := range sorted {
		data = append(data, []any{
			t.Code,
			t.BuyTime.Format(time.DateTime),
			round2(t.BuyPrice.Float64()),
			t.SellTime.Format(time.DateTime),
			round2(t.SellPrice.Float64()),
			t.Quantity,
			round2(t.ProfitAmount()),
			round2(tradeReturnRate(t)),
			t.HoldingDays(),
			t.Virtual,
		})
	}

	buf, err := csv.Export(data)
	if err != nil {
		return ""
	}

	dir := filepath.Join("output", "trades", TradesExportName(strategyName))
	os.MkdirAll(dir, 0755)
	output := filepath.Join(dir, TradesExportName(filename)+".csv")
	if err := oss.New(output, buf); err != nil {
		return ""
	}
	return output
}

// ExportTradesHTML 生成策略交易可视化 HTML（日K + 买卖点标注 + 交易明细表）。
// getDayKlines 为nil时跳过K线部分，仅生成交易明细表。
// 返回写入的文件路径；trades 为空时不生成文件，返回空字符串。
func ExportTradesHTML(strategyName, filename string, trades []Trade, getDayKlines GetDayKlines) string {
	if len(trades) == 0 {
		return ""
	}

	sorted := make([]Trade, len(trades))
	copy(sorted, trades)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].BuyTime.Before(sorted[j].BuyTime) })

	// 按代码分组，每个代码一张K线图
	byCode := make(map[string][]Trade, len(sorted))
	codes := make([]string, 0, len(byCode))
	for _, t := range sorted {
		if _, ok := byCode[t.Code]; !ok {
			codes = append(codes, t.Code)
		}
		byCode[t.Code] = append(byCode[t.Code], t)
	}
	sort.Strings(codes)

	charts := make([]map[string]any, 0, len(codes))
	for _, code := range codes {
		ts := byCode[code]
		chart := map[string]any{"code": code}

		// K线与买卖点（拉不到日线时跳过图，仅保留明细）
		if getDayKlines != nil {
			start := ts[0].BuyTime.AddDate(0, 0, -30) // 前置30日给均线预热
			end := ts[len(ts)-1].SellTime
			if dks, err := getDayKlines(code, start, end); err == nil && len(dks) > 0 {
				kline := make([][]any, 0, len(dks))
				for _, k := range dks {
					kline = append(kline, []any{
						k.Time.Format(time.DateOnly),
						k.Open.Float64(), k.Close.Float64(),
						k.Low.Float64(), k.High.Float64(),
						k.Volume,
					})
				}
				marks := make([]map[string]any, 0, len(ts)*2)
				for _, t := range ts {
					marks = append(marks,
						map[string]any{
							"date":  t.BuyTime.Format(time.DateOnly),
							"type":  "买",
							"price": t.BuyPrice.Float64(),
							"rate":  tradeReturnRate(t),
						},
						map[string]any{
							"date":  t.SellTime.Format(time.DateOnly),
							"type":  "卖",
							"price": t.SellPrice.Float64(),
							"rate":  tradeReturnRate(t),
						},
					)
				}
				sort.Slice(marks, func(i, j int) bool { return marks[i]["date"].(string) < marks[j]["date"].(string) })
				chart["kline"] = kline
				chart["trades"] = marks
			}
		}

		// 明细行
		rows := make([]map[string]any, 0, len(ts))
		for _, t := range ts {
			rows = append(rows, map[string]any{
				"buyDate":   t.BuyTime.Format(time.DateTime),
				"buyPrice":  t.BuyPrice.Float64(),
				"sellDate":  t.SellTime.Format(time.DateTime),
				"sellPrice": t.SellPrice.Float64(),
				"quantity":  t.Quantity,
				"profit":    t.ProfitAmount(),
				"rate":      tradeReturnRate(t),
				"holding":   t.HoldingDays(),
				"virtual":   t.Virtual,
			})
		}
		chart["rows"] = rows
		charts = append(charts, chart)
	}

	// 汇总统计
	var totalProfit float64
	winCount := 0
	for _, t := range sorted {
		totalProfit += t.ProfitAmount()
		if tradeReturnRate(t) > 0 {
			winCount++
		}
	}
	summary := map[string]any{
		"strategy":    strategyName,
		"totalTrades": len(sorted),
		"winRate":     float64(winCount) / float64(len(sorted)) * 100,
		"totalProfit": totalProfit,
		"generated":   time.Now().Format(time.DateTime),
	}

	content, err := buildTradesExportHTML(charts, summary)
	if err != nil {
		return ""
	}

	dir := filepath.Join("output", "trades", TradesExportName(strategyName))
	os.MkdirAll(dir, 0755)
	output := filepath.Join(dir, TradesExportName(filename)+".html")
	if err := oss.New(output, []byte(content)); err != nil {
		return ""
	}
	return output
}

// round2 保留两位小数，避免 CSV/HTML 中浮点尾数噪音。
func round2(v float64) float64 {
	return float64(int(v*100+copysignForRound(v))) / 100
}

// copysignForRound 返回 v 的符号（v>=0 为 1，否则 -1），配合 round2 实现四舍五入。
func copysignForRound(v float64) float64 {
	if v >= 0 {
		return 0.5
	}
	return -0.5
}

// buildTradesExportHTML 生成可视化 HTML（ECharts K线 + 买卖点 + 明细表）。
func buildTradesExportHTML(charts []map[string]any, summary map[string]any) (string, error) {
	chartsJSON, err := json.Marshal(charts)
	if err != nil {
		return "", err
	}
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>交易记录 - %s</title>
<script src="https://cdn.jsdelivr.net/npm/echarts@5/dist/echarts.min.js"></script>
<style>
:root{--bg:#f8f9fb;--bg2:#fff;--ink:#1a1a2e;--muted:#6b7280;--rule:#e5e7eb;--accent:#3b82f6;--red:#ef4444;--green:#22c55e}
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,"Microsoft YaHei","PingFang SC",sans-serif;background:var(--bg);color:var(--ink);line-height:1.6;font-size:15px}
.container{max-width:1200px;margin:0 auto;padding:24px 20px}
.header{padding:32px;background:linear-gradient(135deg,#1e293b,#334155);color:#fff;border-radius:12px;margin-bottom:24px}
.header h1{font-size:24px;font-weight:700;margin-bottom:12px}
.header .metrics{display:flex;gap:32px;flex-wrap:wrap}
.header .metrics div span{display:block;font-size:13px;opacity:.75}
.header .metrics div b{font-size:22px}
.pos{color:var(--red)}.neg{color:var(--green)}
.section{background:var(--bg2);border:1px solid var(--rule);border-radius:10px;padding:20px;margin-bottom:20px}
.section h2{font-size:16px;margin-bottom:12px;padding-bottom:8px;border-bottom:2px solid var(--accent)}
.toolbar{display:flex;gap:12px;align-items:center;margin-bottom:12px}
.toolbar select{height:34px;padding:0 10px;border:1px solid var(--rule);border-radius:6px;font-size:14px}
#klineChart{width:100%%;height:520px}
.table-wrap{max-height:480px;overflow:auto}
table{width:100%%;border-collapse:collapse;font-size:14px}
th,td{padding:8px 12px;text-align:center;border-bottom:1px solid var(--rule);white-space:nowrap}
th{background:#f9fafb;color:var(--muted);position:sticky;top:0}
tbody tr:hover{background:#f0f4ff}
.chart-item{margin-bottom:24px}
.chart-item h3{font-size:14px;margin-bottom:8px;color:var(--muted)}
.small-chart{width:100%%;height:360px}
</style>
</head>
<body>
<div class="container">
<div class="header">
<h1 id="title"></h1>
<div class="metrics">
<div><span>总笔数</span><b id="mTotal"></b></div>
<div><span>胜率</span><b id="mWinRate"></b></div>
<div><span>累计盈亏(元)</span><b id="mProfit"></b></div>
<div><span>生成时间</span><b id="mTime" style="font-size:14px"></b></div>
</div>
</div>

<div class="section">
<h2>K线买卖点</h2>
<div class="toolbar"><label>股票代码 <select id="code"></select></label></div>
<div id="klineChart"></div>
</div>

<div class="section">
<h2>交易明细（当前选中代码）</h2>
<div class="table-wrap">
<table>
<thead><tr><th>买入时间</th><th>买入价</th><th>卖出时间</th><th>卖出价</th><th>数量</th><th>盈亏(元)</th><th>收益率</th><th>持仓天数</th><th>虚拟</th></tr></thead>
<tbody id="rows"></tbody>
</table>
</div>
</div>
</div>

<script>
const allCharts = %s;
const summary = %s;

document.getElementById('title').textContent = '交易记录 - ' + summary.strategy;
document.getElementById('mTotal').textContent = summary.totalTrades;
document.getElementById('mWinRate').textContent = summary.winRate.toFixed(1) + '%%';
document.getElementById('mWinRate').className = summary.winRate >= 50 ? 'pos' : 'neg';
document.getElementById('mProfit').textContent = summary.totalProfit.toFixed(0);
document.getElementById('mProfit').className = summary.totalProfit >= 0 ? 'pos' : 'neg';
document.getElementById('mTime').textContent = summary.generated;

const select = document.getElementById('code');
allCharts.forEach(c => {
  const opt = document.createElement('option');
  opt.value = c.code;
  opt.textContent = c.code + '（' + (c.rows || []).length + '笔）';
  select.appendChild(opt);
});

function ma(v, p) {
  return v.map((_, i) => {
    if (i + 1 < p) return null;
    let s = 0;
    for (let j = i - p + 1; j <= i; j++) s += Number(v[j] || 0);
    return Number((s / p).toFixed(3));
  });
}

let chart = null;
function render() {
  const item = allCharts.find(x => x.code === select.value) || allCharts[0];
  if (!item) return;

  // 明细表
  document.getElementById('rows').innerHTML = (item.rows || []).map(r => {
    const rate = Number(r.rate), profit = Number(r.profit);
    return '<tr><td>' + r.buyDate + '</td><td>' + Number(r.buyPrice).toFixed(2) + '</td><td>' + r.sellDate +
      '</td><td>' + Number(r.sellPrice).toFixed(2) + '</td><td>' + r.quantity +
      '</td><td class="' + (profit >= 0 ? 'pos' : 'neg') + '">' + profit.toFixed(2) +
      '</td><td class="' + (rate >= 0 ? 'pos' : 'neg') + '">' + rate.toFixed(2) + '%%</td><td>' + r.holding +
      '</td><td>' + (r.virtual ? '是' : '') + '</td></tr>';
  }).join('');

  // K线（无日线数据的代码自动跳过）
  const box = document.getElementById('klineChart');
  if (!item.kline || !item.kline.length) {
    box.innerHTML = '<p style="color:#999;padding:40px;text-align:center">该代码无可绘制日线数据</p>';
    return;
  }
  if (!chart) chart = echarts.init(box);
  const dates = item.kline.map(x => x[0]);
  const values = item.kline.map(x => [x[1], x[2], x[3], x[4]]);
  const closes = item.kline.map(x => x[2]);
  const vols = item.kline.map(x => x[5]);
  const dateMap = new Map(item.kline.map(x => [x[0], x]));
  const marks = (item.trades || []).map(x => {
    const k = dateMap.get(x.date);
    const bp = k ? (x.type === '买' ? k[3] : k[4]) : x.price;
    const isBuy = x.type === '买';
    return {
      name: x.type, coord: [x.date, bp], value: isBuy ? 'B' : 'S',
      symbol: 'triangle', symbolRotate: isBuy ? 0 : 180, symbolSize: 14,
      symbolOffset: [0, isBuy ? 12 : -12],
      itemStyle: { color: isBuy ? '#ef4444' : '#22c55e' },
      label: { show: true, formatter: isBuy ? 'B' : 'S', color: '#fff', fontSize: 10, offset: [0, isBuy ? 4 : -4] },
      tooltip: { formatter: x.type + ' ' + x.date + '<br/>价格: ' + Number(x.price).toFixed(2) + '<br/>本笔收益: ' + Number(x.rate).toFixed(2) + '%%' }
    };
  });
  chart.setOption({
    animation: false,
    title: { text: item.code + ' K线买卖点', left: 16, top: 10 },
    tooltip: { trigger: 'axis', axisPointer: { type: 'cross' } },
    legend: { top: 12, data: ['日K', 'MA5', 'MA10', 'MA20'] },
    dataZoom: [{ type: 'inside', xAxisIndex: [0, 1] }, { show: true, xAxisIndex: [0, 1], type: 'slider', bottom: 8 }],
    grid: [{ left: 60, right: 30, top: 60, height: '55%%' }, { left: 60, right: 30, top: '74%%', height: '14%%' }],
    xAxis: [
      { type: 'category', data: dates, boundaryGap: false },
      { type: 'category', gridIndex: 1, data: dates, boundaryGap: false, axisLabel: { show: false } }
    ],
    yAxis: [
      { scale: true, splitArea: { show: true } },
      { scale: true, gridIndex: 1, splitNumber: 2, axisLabel: { show: false }, splitLine: { show: false } }
    ],
    series: [
      { name: '日K', type: 'candlestick', data: values, itemStyle: { color: '#ef4444', color0: '#22c55e', borderColor: '#ef4444', borderColor0: '#22c55e' }, markPoint: { data: marks } },
      { name: 'MA5', type: 'line', data: ma(closes, 5), symbol: 'none', lineStyle: { width: 1, color: '#f59e0b' } },
      { name: 'MA10', type: 'line', data: ma(closes, 10), symbol: 'none', lineStyle: { width: 1, color: '#8b5cf6' } },
      { name: 'MA20', type: 'line', data: ma(closes, 20), symbol: 'none', lineStyle: { width: 1, color: '#3b82f6' } },
      { name: '成交量', type: 'bar', xAxisIndex: 1, yAxisIndex: 1, data: vols, itemStyle: { color: p => values[p.dataIndex] && values[p.dataIndex][1] >= values[p.dataIndex][0] ? '#ef4444' : '#22c55e' } }
    ]
  }, true);
}
window.addEventListener('resize', () => chart && chart.resize());
select.addEventListener('change', render);
render();
</script>
</body>
</html>`, summary["strategy"], chartsJSON, summaryJSON), nil
}
