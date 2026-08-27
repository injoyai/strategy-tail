package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/logs"
)

// ============================================================================
// HTML 报告生成
// ============================================================================

// ExportHTML 导出详细 HTML 报告到 output/market-regime/report.html
func ExportHTML(r *AnalysisResult) {
	// 序列化各部分数据
	dimsJSON, _ := json.Marshal(r.DimensionResults)
	yearsJSON, _ := json.Marshal(r.Years)

	// 年度×综合状态交叉表数据
	type yearRow struct {
		Year  int                 `json:"year"`
		Cells map[string]GroupStat `json:"cells"`
	}
	yearRows := make([]yearRow, 0, len(r.YearlyComposite))
	for _, y := range r.Years {
		cells := r.YearlyComposite[y]
		if cells == nil {
			cells = make(map[string]GroupStat)
		}
		yearRows = append(yearRows, yearRow{Year: y, Cells: cells})
	}
	yearlyJSON, _ := json.Marshal(yearRows)

	// 月度热力图数据 (综合状态 × 月份)
	monthlyJSON, _ := json.Marshal(r.MonthlyComposite)

	// 各维度的柱状图数据（胜率 & 平均收益）
	type barData struct {
		Dimension string       `json:"dimension"`
		Labels    []string     `json:"labels"`
		WinRates  []float64    `json:"winRates"`
		AvgProfits []float64   `json:"avgProfits"`
		Counts    []int        `json:"counts"`
	}
	bars := make([]barData, 0, len(r.DimensionResults))
	for _, dr := range r.DimensionResults {
		bd := barData{Dimension: dr.Dimension}
		for _, g := range dr.Groups {
			if g.Label == "(无数据)" {
				continue
			}
			bd.Labels = append(bd.Labels, g.Label)
			bd.WinRates = append(bd.WinRates, g.WinRate)
			bd.AvgProfits = append(bd.AvgProfits, g.AvgProfit)
			bd.Counts = append(bd.Counts, g.Count)
		}
		bars = append(bars, bd)
	}
	barsJSON, _ := json.Marshal(bars)

	// 关键发现
	best, worst := FindBestWorst(r)
	findings := map[string]interface{}{
		"best":       best,
		"worst":      worst,
		"totalTrades": r.TotalTrades,
		"matchedTrades": r.MatchedTrades,
		"matchRate":  safeDiv(r.MatchedTrades*100, r.TotalTrades),
		"strategy":   r.StrategyName,
		"benchmark":  r.Benchmark,
		"yearStart":  r.Years[0],
		"yearEnd":    r.Years[len(r.Years)-1],
	}
	findingsJSON, _ := json.Marshal(findings)

	html := reportHTML(
		r.StrategyName, r.Benchmark,
		r.Years[0], r.Years[len(r.Years)-1],
		string(dimsJSON), string(yearsJSON), string(yearlyJSON),
		string(monthlyJSON), string(barsJSON), string(findingsJSON),
	)

	dir := filepath.Join("output", "market-regime")
	os.MkdirAll(dir, 0755)
	output := filepath.Join(dir, "report.html")
	oss.New(output, []byte(html))
	logs.Info("HTML报告已生成: " + output)

	// 同时生成手机版 PDF 专用 HTML（纯 CSS，所有表格展开）
	mobileHTML := mobileReportHTML(
		r.StrategyName, r.Benchmark,
		r.Years[0], r.Years[len(r.Years)-1],
		r, best, worst,
	)
	mobileOutput := filepath.Join(dir, "report_mobile.html")
	oss.New(mobileOutput, []byte(mobileHTML))
	logs.Info("手机版HTML已生成: " + mobileOutput)
}

func reportHTML(strategyName, benchmark string, yearStart, yearEnd int,
	dimsJSON, yearsJSON, yearlyJSON, monthlyJSON, barsJSON, findingsJSON string) string {
	return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>策略大盘状态分析报告</title>
<script src="https://cdn.jsdelivr.net/npm/echarts@5/dist/echarts.min.js"></script>
<style>
:root{--bg:#f8f9fb;--bg2:#fff;--ink:#1a1a2e;--muted:#6b7280;--rule:#e5e7eb;--accent:#3b82f6;--red:#ef4444;--green:#22c55e;--amber:#f59e0b}
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,"Microsoft YaHei","PingFang SC",sans-serif;background:var(--bg);color:var(--ink);line-height:1.6;font-size:15px}
.container{max-width:1200px;margin:0 auto;padding:24px 20px}
.header{text-align:center;padding:40px 20px 32px;background:linear-gradient(135deg,#1e293b 0%,#334155 100%);color:#fff;border-radius:12px;margin-bottom:28px}
.header h1{font-size:28px;font-weight:700;margin-bottom:8px}
.header .subtitle{font-size:14px;opacity:.85;margin-bottom:6px}
.header .meta{font-size:13px;opacity:.7}
.section{margin-bottom:32px}
.section-title{font-size:20px;font-weight:700;margin-bottom:16px;padding-bottom:10px;border-bottom:2px solid var(--accent);display:flex;align-items:center;gap:8px}
.section-title .badge{font-size:12px;background:var(--accent);color:#fff;padding:2px 8px;border-radius:10px;font-weight:400}
.card-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:16px;margin-bottom:20px}
.card{background:var(--bg2);border-radius:10px;padding:18px;box-shadow:0 1px 3px rgba(0,0,0,.06);border:1px solid var(--rule)}
.card .label{font-size:13px;color:var(--muted);margin-bottom:6px}
.card .value{font-size:24px;font-weight:700}
.card .sub{font-size:12px;color:var(--muted);margin-top:4px}
.card.good{border-left:4px solid var(--red)}
.card.bad{border-left:4px solid var(--green)}
.card.neutral{border-left:4px solid var(--accent)}
.chart-box{background:var(--bg2);border-radius:10px;padding:18px;box-shadow:0 1px 3px rgba(0,0,0,.06);border:1px solid var(--rule);margin-bottom:16px}
.chart{width:100%;height:380px}
.chart.tall{height:460px}
table{width:100%;border-collapse:collapse;font-size:14px;background:var(--bg2);border-radius:10px;overflow:hidden;box-shadow:0 1px 3px rgba(0,0,0,.06)}
th,td{padding:10px 12px;text-align:center;border-bottom:1px solid var(--rule);white-space:nowrap}
th{background:#f9fafb;font-weight:600;color:var(--muted);position:sticky;top:0}
td.pos{color:var(--red);font-weight:600}
td.neg{color:var(--green);font-weight:600}
td.dim{font-weight:600;color:var(--accent)}
tr:hover td{background:#f9fafb}
.table-wrap{overflow-x:auto;margin-bottom:16px}
.note{font-size:13px;color:var(--muted);margin-top:8px;padding:10px 14px;background:#fef3c7;border-radius:6px;border-left:3px solid var(--amber)}
.heat-cell{display:inline-block;width:42px;height:28px;line-height:28px;text-align:center;font-size:11px;border-radius:3px;color:#fff}
.tabs{display:flex;gap:6px;margin-bottom:14px;flex-wrap:wrap}
.tab{padding:6px 14px;border:1px solid var(--rule);border-radius:20px;background:var(--bg2);cursor:pointer;font-size:13px;transition:all .15s}
.tab.active{background:var(--accent);color:#fff;border-color:var(--accent)}
</style>
</head>
<body>
<div class="container">

<div class="header">
<h1>策略 × 大盘状态 分析报告</h1>
<div class="subtitle">策略：` + strategyName + `</div>
<div class="meta">基准：` + benchmark + ` ｜ 年份：` + fmt.Sprintf("%d-%d", yearStart, yearEnd) + ` ｜ 生成日期：` + time.Now().Format("2006-01-02") + `</div>
</div>

<div id="findings"></div>

<div class="section">
<div class="section-title">一、各维度分组统计 <span class="badge">11 维度</span></div>
<div class="tabs" id="dimTabs"></div>
<div id="dimTable"></div>
<div class="note">说明：颜色按 A 股惯例，红色为正收益/高胜率，绿色为负收益/低胜率。盈亏比 ∞ 表示无亏损单。</div>
</div>

<div class="section">
<div class="section-title">二、各维度胜率对比 <span class="badge">柱状图</span></div>
<div id="barCharts"></div>
</div>

<div class="section">
<div class="section-title">三、年度 × 综合状态 交叉表现</div>
<div class="table-wrap" id="yearlyTable"></div>
<div class="note">单元格为该年该状态下的平均收益率(%)，括号内为交易笔数。空白表示当年无该状态交易。</div>
</div>

<div class="section">
<div class="section-title">四、综合状态 × 月份 收益热力图</div>
<div class="chart-box"><div id="heatChart" class="chart tall"></div></div>
<div class="note">颜色越红表示该组合下平均收益越高，越绿表示越低。可识别"特定大盘状态下的季节性效应"。</div>
</div>

<div class="section">
<div class="section-title">五、结论与建议</div>
<div id="conclusion"></div>
</div>

</div>

<script>
const dims = ` + dimsJSON + `;
const years = ` + yearsJSON + `;
const yearlyRows = ` + yearlyJSON + `;
const monthly = ` + monthlyJSON + `;
const bars = ` + barsJSON + `;
const findings = ` + findingsJSON + `;

// ===== 0. 关键发现卡片 =====
(function(){
  const f = findings;
  let html = '<div class="section"><div class="section-title">关键发现</div><div class="card-grid">';
  html += '<div class="card neutral"><div class="label">总交易笔数</div><div class="value">'+f.totalTrades+'</div><div class="sub">匹配大盘数据 '+f.matchedTrades+' ('+f.matchRate.toFixed(1)+'%)</div></div>';
  if(f.best && f.best.count>0){
    html += '<div class="card good"><div class="label">最佳环境</div><div class="value" style="color:var(--red)">+'+f.best.avgProfit.toFixed(2)+'%</div><div class="sub">'+f.best.dimension+' / '+f.best.label+' ｜ 胜率'+f.best.winRate.toFixed(1)+'% ｜ '+f.best.count+'笔</div></div>';
  }
  if(f.worst && f.worst.count>0){
    html += '<div class="card bad"><div class="label">最差环境</div><div class="value" style="color:var(--green)">'+f.worst.avgProfit.toFixed(2)+'%</div><div class="sub">'+f.worst.dimension+' / '+f.worst.label+' ｜ 胜率'+f.worst.winRate.toFixed(1)+'% ｜ '+f.worst.count+'笔</div></div>';
  }
  // 计算综合状态下强势vs弱势的收益差
  let strongAvg=null, weakAvg=null;
  dims.forEach(d=>{ if(d.dimension==='综合'){ d.groups.forEach(g=>{
    if(g.label==='强势') strongAvg=g.avgProfit;
    if(g.label==='弱势') weakAvg=g.avgProfit;
  });}});
  if(strongAvg!==null && weakAvg!==null){
    html += '<div class="card neutral"><div class="label">强势 vs 弱势 收益差</div><div class="value" style="color:'+(strongAvg-weakAvg>=0?'var(--red)':'var(--green)')+'">'+(strongAvg-weakAvg>=0?'+':'')+(strongAvg-weakAvg).toFixed(2)+'%</div><div class="sub">强势环境 '+strongAvg.toFixed(2)+'% vs 弱势环境 '+weakAvg.toFixed(2)+'%</div></div>';
  }
  html += '</div></div>';
  document.getElementById('findings').innerHTML = html;
})();

// ===== 1. 维度表格（带 Tab 切换）=====
(function(){
  const tabs = document.getElementById('dimTabs');
  const tableDiv = document.getElementById('dimTable');
  dims.forEach((d,i)=>{
    const tab = document.createElement('div');
    tab.className = 'tab'+(i===0?' active':'');
    tab.textContent = d.dimension;
    tab.onclick = ()=>{ document.querySelectorAll('#dimTabs .tab').forEach(t=>t.classList.remove('active')); tab.classList.add('active'); renderTable(d); };
    tabs.appendChild(tab);
  });
  function renderTable(d){
    let html = '<div class="table-wrap"><table><thead><tr><th>标签</th><th>笔数</th><th>胜率</th><th>平均收益</th><th>盈亏比</th><th>最大收益</th><th>最大亏损</th><th>总收益(Σ%)</th></tr></thead><tbody>';
    d.groups.forEach(g=>{
      const pf = isFinite(g.profitFactor) ? g.profitFactor.toFixed(2) : '∞';
      html += '<tr><td class="dim">'+g.label+'</td><td>'+g.count+'</td>';
      html += '<td class="'+(g.winRate>=50?'pos':'neg')+'">'+g.winRate.toFixed(1)+'%</td>';
      html += '<td class="'+(g.avgProfit>=0?'pos':'neg')+'">'+g.avgProfit.toFixed(2)+'%</td>';
      html += '<td>'+pf+'</td>';
      html += '<td class="pos">'+g.maxProfit.toFixed(2)+'%</td>';
      html += '<td class="neg">'+g.maxLoss.toFixed(2)+'%</td>';
      html += '<td class="'+(g.totalProfit>=0?'pos':'neg')+'">'+g.totalProfit.toFixed(1)+'</td>';
      html += '</tr>';
    });
    html += '</tbody></table></div>';
    tableDiv.innerHTML = html;
  }
  renderTable(dims[0]);
})();

// ===== 2. 各维度柱状图 =====
(function(){
  const container = document.getElementById('barCharts');
  bars.forEach((b,i)=>{
    const box = document.createElement('div');
    box.className = 'chart-box';
    box.innerHTML = '<div style="font-weight:600;margin-bottom:8px">'+b.dimension+'</div><div id="bar'+i+'" class="chart"></div>';
    container.appendChild(box);
    const chart = echarts.init(document.getElementById('bar'+i));
    const opt = {
      tooltip: { trigger: 'axis', axisPointer: {type:'shadow'} },
      legend: { data: ['平均收益(%)','胜率(%)'], top: 0 },
      grid: { left: 50, right: 50, bottom: 40, top: 40 },
      xAxis: { type: 'category', data: b.labels, axisLabel: { interval: 0, rotate: b.labels.length>4?20:0 } },
      yAxis: [
        { type: 'value', name: '收益(%)', axisLabel: { formatter: '{value}%' } },
        { type: 'value', name: '胜率(%)', axisLabel: { formatter: '{value}%' }, max: 100 }
      ],
      series: [
        { name: '平均收益(%)', type: 'bar', data: b.avgProfits.map(v=>v.toFixed(2)),
          itemStyle: { color: function(p){ return b.avgProfits[p.dataIndex]>=0 ? '#ef4444' : '#22c55e'; } },
          label: { show: true, position: 'top', formatter: '{c}%', fontSize: 11 } },
        { name: '胜率(%)', type: 'line', yAxisIndex: 1, data: b.winRates.map(v=>v.toFixed(1)),
          itemStyle: { color: '#3b82f6' }, lineStyle: { width: 2 }, symbol: 'circle', symbolSize: 8,
          label: { show: true, formatter: '{c}%', fontSize: 11 } }
      ]
    };
    chart.setOption(opt);
    window.addEventListener('resize', ()=>chart.resize());
  });
})();

// ===== 3. 年度 × 综合状态 交叉表 =====
(function(){
  const labels = ['强势','震荡','弱势'];
  let html = '<table><thead><tr><th>年份</th>';
  labels.forEach(l=> html += '<th>'+l+'</th>');
  html += '</tr></thead><tbody>';
  yearlyRows.forEach(row=>{
    html += '<tr><td class="dim">'+row.year+'</td>';
    labels.forEach(l=>{
      const c = row.cells[l];
      if(c && c.count>0){
        const cls = c.avgProfit>=0?'pos':'neg';
        html += '<td class="'+cls+'">'+c.avgProfit.toFixed(2)+'%<br><span style="font-size:11px;color:var(--muted)">'+c.count+'笔 胜率'+c.winRate.toFixed(0)+'%</span></td>';
      } else {
        html += '<td style="color:var(--muted)">—</td>';
      }
    });
    html += '</tr>';
  });
  html += '</tbody></table>';
  document.getElementById('yearlyTable').innerHTML = html;
})();

// ===== 4. 综合状态 × 月份 热力图 =====
(function(){
  const labels = ['强势','震荡','弱势'];
  const months = [];
  for(let i=1;i<=12;i++) months.push(i+'月');
  const data = [];
  let minV = 0, maxV = 0;
  labels.forEach((l,y)=>{
    for(let m=1;m<=12;m++){
      const v = (monthly[l] && monthly[l][m]) ? monthly[l][m] : null;
      data.push([m-1, y, v]);
      if(v!==null){ if(v>maxV) maxV=v; if(v<minV) minV=v; }
    }
  });
  const absMax = Math.max(Math.abs(minV), Math.abs(maxV), 1);
  const chart = echarts.init(document.getElementById('heatChart'));
  chart.setOption({
    tooltip: { formatter: function(p){ const v=p.value[2]; return labels[p.value[1]]+' '+p.value[0]+'月<br>'+(v===null?'无数据':v.toFixed(2)+'%'); } },
    grid: { left: 70, right: 30, bottom: 50, top: 30 },
    xAxis: { type: 'category', data: months, splitArea: { show: true } },
    yAxis: { type: 'category', data: labels, splitArea: { show: true } },
    visualMap: {
      min: -absMax, max: absMax, calculable: true, orient: 'horizontal', left: 'center', bottom: 0,
      inRange: { color: ['#22c55e','#f3f4f6','#ef4444'] },
      formatter: function(v){ return v.toFixed(1)+'%'; }
    },
    series: [{
      name: '平均收益', type: 'heatmap', data: data,
      label: { show: true, formatter: function(p){ const v=p.value[2]; return v===null?'':v.toFixed(1); }, fontSize: 11 },
      emphasis: { itemStyle: { shadowBlur: 10, shadowColor: 'rgba(0,0,0,0.3)' } }
    }]
  });
  window.addEventListener('resize', ()=>chart.resize());
})();

// ===== 5. 结论 =====
(function(){
  const f = findings;
  let html = '<div class="card-grid">';
  // 整体评价
  let strongAvg=null, weakAvg=null, strongCount=0, weakCount=0;
  dims.forEach(d=>{ if(d.dimension==='综合'){ d.groups.forEach(g=>{
    if(g.label==='强势'){ strongAvg=g.avgProfit; strongCount=g.count; }
    if(g.label==='弱势'){ weakAvg=g.avgProfit; weakCount=g.count; }
  });}});

  if(strongAvg!==null && weakAvg!==null){
    const diff = strongAvg - weakAvg;
    let verdict = '';
    if(diff > 3) verdict = '该策略对大盘环境<strong style="color:var(--red)">高度敏感</strong>，强势环境显著优于弱势环境，建议结合大盘择时使用。';
    else if(diff > 1) verdict = '该策略对大盘环境<strong style="color:var(--amber)">较为敏感</strong>，弱势环境下应降低仓位或暂停。';
    else verdict = '该策略对大盘环境<strong style="color:var(--accent)">不太敏感</strong>，各环境下表现接近，可能具备一定alpha。';
    html += '<div class="card neutral" style="grid-column:1/-1"><div class="label">整体评价</div><div style="font-size:15px;line-height:1.8;margin-top:8px">'+verdict+'</div></div>';
  }

  // 操作建议
  let tips = [];
  if(f.best && f.best.count>0){
    tips.push('<strong>最佳入场时机</strong>：'+f.best.dimension+'='+f.best.label+'，平均收益 '+f.best.avgProfit.toFixed(2)+'%，胜率 '+f.best.winRate.toFixed(1)+'%');
  }
  if(f.worst && f.worst.count>0){
    tips.push('<strong>应规避的环境</strong>：'+f.worst.dimension+'='+f.worst.label+'，平均收益 '+f.worst.avgProfit.toFixed(2)+'%，胜率 '+f.worst.winRate.toFixed(1)+'%');
  }
  if(strongAvg!==null && weakAvg!==null && (strongAvg-weakAvg)>2){
    tips.push('<strong>择时建议</strong>：在"综合=弱势"时建议空仓或减仓，可显著提升整体收益');
  }
  if(strongAvg!==null && weakAvg!==null && weakAvg>0){
    tips.push('<strong>注意</strong>：即使最差环境仍为正收益，说明策略本身有alpha，但需警惕样本量不足');
  }
  if(tips.length>0){
    html += '<div class="card neutral" style="grid-column:1/-1"><div class="label">操作建议</div><ul style="margin-top:8px;padding-left:20px;line-height:2">';
    tips.forEach(t=> html += '<li>'+t+'</li>');
    html += '</ul></div>';
  }

  html += '</div>';
  document.getElementById('conclusion').innerHTML = html;
})();
</script>
</body>
</html>`
}

// ============================================================================
// 手机版 / PDF 专用 HTML（纯 CSS，无 ECharts，所有表格展开）
// ============================================================================

func mobileReportHTML(strategyName, benchmark string, yearStart, yearEnd int,
	r *AnalysisResult, best, worst GroupStat) string {

	// 构建各维度表格 HTML
	dimsHTML := ""
	for _, dr := range r.DimensionResults {
		dimsHTML += fmt.Sprintf(`<h3>%s</h3><table><tr><th>标签</th><th>笔数</th><th>胜率</th><th>平均收益</th><th>盈亏比</th></tr>`, dr.Dimension)
		for _, g := range dr.Groups {
			if g.Count == 0 && g.Label != "(无数据)" {
				continue
			}
			pf := "—"
		if g.Count > 0 {
			if math.IsInf(g.ProfitFactor, 1) {
				pf = "∞"
			} else {
				pf = fmt.Sprintf("%.2f", g.ProfitFactor)
			}
		}
		profitClass := ""
		profitStr := "—"
		winRateStr := "—"
		if g.Count > 0 {
			if g.AvgProfit >= 0 {
				profitClass = "pos"
			} else {
				profitClass = "neg"
			}
			profitStr = fmt.Sprintf("%.2f%%", g.AvgProfit)
			winRateStr = fmt.Sprintf("%.1f%%", g.WinRate)
		}
		// CSS 条形图（平均收益）
		barWidth := 0
		barColor := "#22c55e"
		if g.AvgProfit > 0 {
			barWidth = int(math.Min(g.AvgProfit*8, 50))
			barColor = "#ef4444"
		} else if g.AvgProfit < 0 {
			barWidth = int(math.Min(-g.AvgProfit*8, 50))
			barColor = "#22c55e"
		}
		dimsHTML += fmt.Sprintf(`<tr><td class="dim">%s</td><td>%d</td><td>%s</td><td><div class="bar-wrap"><span class="bar" style="width:%dpx;background:%s"></span><span class="bar-val %s">%s</span></div></td><td>%s</td></tr>`,
			g.Label, g.Count, winRateStr, barWidth, barColor, profitClass, profitStr, pf)
		}
		dimsHTML += `</table>`
	}

	// 年度×综合状态交叉表
	yearlyHTML := `<table><tr><th>年份</th><th>强势</th><th>震荡</th><th>弱势</th></tr>`
	for _, y := range r.Years {
		cells := r.YearlyComposite[y]
		yearlyHTML += fmt.Sprintf(`<tr><td class="dim">%d</td>`, y)
		for _, label := range []string{"强势", "震荡", "弱势"} {
			c := cells[label]
			if c.Count > 0 {
				cls := "neg"
				if c.AvgProfit >= 0 {
					cls = "pos"
				}
				yearlyHTML += fmt.Sprintf(`<td class="%s">%.2f%%<br><span class="sub">%d笔 %s</span></td>`,
					cls, c.AvgProfit, c.Count, fmt.Sprintf("%.0f%%", c.WinRate))
			} else {
				yearlyHTML += `<td class="muted">—</td>`
			}
		}
		yearlyHTML += `</tr>`
	}
	yearlyHTML += `</table>`

	// 月度热力表（综合状态 × 月份）
	monthlyHTML := `<table><tr><th>状态</th>`
	for m := 1; m <= 12; m++ {
		monthlyHTML += fmt.Sprintf(`<th>%d月</th>`, m)
	}
	monthlyHTML += `</tr>`
	for _, label := range []string{"强势", "震荡", "弱势"} {
		monthlyHTML += fmt.Sprintf(`<tr><td class="dim">%s</td>`, label)
		for m := 1; m <= 12; m++ {
			v := 0.0
			ok := false
			if r.MonthlyComposite[label] != nil {
				if val, exists := r.MonthlyComposite[label][m]; exists {
					v = val
					ok = true
				}
			}
			if ok {
				cls := "neg"
				if v >= 0 {
					cls = "pos"
				}
				monthlyHTML += fmt.Sprintf(`<td class="%s">%.1f</td>`, cls, v)
			} else {
				monthlyHTML += `<td class="muted">—</td>`
			}
		}
		monthlyHTML += `</tr>`
	}
	monthlyHTML += `</table>`

	// 关键发现
	bestStr := "—"
	if best.Count > 0 {
		bestStr = fmt.Sprintf(`<strong>%s / %s</strong><br>%d笔 ｜ 胜率 %.1f%% ｜ <span class="pos">平均收益 %.2f%%</span>`,
			best.Dimension, best.Label, best.Count, best.WinRate, best.AvgProfit)
	}
	worstStr := "—"
	if worst.Count > 0 {
		worstStr = fmt.Sprintf(`<strong>%s / %s</strong><br>%d笔 ｜ 胜率 %.1f%% ｜ <span class="neg">平均收益 %.2f%%</span>`,
			worst.Dimension, worst.Label, worst.Count, worst.WinRate, worst.AvgProfit)
	}

	// 强势vs弱势
	strongAvg, weakAvg := 0.0, 0.0
	strongCount, weakCount := 0, 0
	for _, dr := range r.DimensionResults {
		if dr.Dimension == "综合" {
			for _, g := range dr.Groups {
				switch g.Label {
				case "强势":
					strongAvg, strongCount = g.AvgProfit, g.Count
				case "弱势":
					weakAvg, weakCount = g.AvgProfit, g.Count
				}
			}
		}
	}
	diff := strongAvg - weakAvg
	sensitivity := "不太敏感"
	if diff > 3 {
		sensitivity = "高度敏感"
	} else if diff > 1 {
		sensitivity = "较为敏感"
	}

	return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>策略大盘状态分析报告（手机版）</title>
<style>
@page { size: A4; margin: 8mm }
* { margin:0; padding:0; box-sizing:border-box }
body { font-family:"Microsoft YaHei","PingFang SC",sans-serif; color:#1a1a2e; font-size:12px; line-height:1.5 }
.header { text-align:center; padding:18px 12px; background:#1e293b; color:#fff; border-radius:6px; margin-bottom:14px }
.header h1 { font-size:17px; margin-bottom:4px }
.header .sub { font-size:11px; opacity:.8 }
h2 { font-size:14px; margin:16px 0 8px; padding-bottom:4px; border-bottom:2px solid #3b82f6 }
h3 { font-size:12px; margin:10px 0 5px; color:#3b82f6 }
table { width:100%; border-collapse:collapse; margin-bottom:8px; font-size:11px }
th,td { padding:5px 4px; text-align:center; border-bottom:1px solid #e5e7eb }
th { background:#f9fafb; color:#6b7280; font-weight:600 }
td.dim { font-weight:600; color:#3b82f6 }
td.pos { color:#ef4444; font-weight:600 }
td.neg { color:#22c55e; font-weight:600 }
td.muted { color:#aaa }
.sub { font-size:9px; color:#999 }
.findings { display:grid; grid-template-columns:1fr 1fr; gap:8px; margin-bottom:10px }
.card { border:1px solid #e5e7eb; border-radius:6px; padding:10px; background:#fff }
.card .label { font-size:10px; color:#6b7280; margin-bottom:4px }
.card .val { font-size:14px; font-weight:700 }
.bar-wrap { display:inline-flex; align-items:center; gap:4px }
.bar { display:inline-block; height:10px; border-radius:2px }
.bar-val { font-size:11px; white-space:nowrap }
.note { font-size:10px; color:#6b7280; padding:6px 8px; background:#fef3c7; border-radius:4px; border-left:3px solid #f59e0b; margin:6px 0 }
.verdict { padding:10px; background:#f0f4ff; border-radius:6px; border-left:4px solid #3b82f6; font-size:12px; line-height:1.7; margin:8px 0 }
</style>
</head>
<body>

<div class="header">
<h1>策略 × 大盘状态 分析报告</h1>
<div class="sub">` + strategyName + `<br>基准：` + benchmark + ` ｜ ` + fmt.Sprintf("%d-%d", yearStart, yearEnd) + ` ｜ ` + time.Now().Format("2006-01-02") + `</div>
</div>

<h2>关键发现</h2>
<div class="findings">
<div class="card"><div class="label">总交易笔数</div><div class="val">` + fmt.Sprintf("%d", r.TotalTrades) + `</div><div class="sub">匹配大盘 ` + fmt.Sprintf("%d (%.1f%%)", r.MatchedTrades, safeDiv(r.MatchedTrades*100, r.TotalTrades)) + `</div></div>
<div class="card"><div class="label">策略敏感度</div><div class="val">` + sensitivity + `</div><div class="sub">强势vs弱势收益差 ` + fmt.Sprintf("%.2f%%", diff) + `</div></div>
<div class="card"><div class="label">最佳环境</div><div class="val pos" style="color:#ef4444">` + fmt.Sprintf("+%.2f%%", best.AvgProfit) + `</div><div class="sub">` + bestStr + `</div></div>
<div class="card"><div class="label">最差环境</div><div class="val neg" style="color:#22c55e">` + fmt.Sprintf("%.2f%%", worst.AvgProfit) + `</div><div class="sub">` + worstStr + `</div></div>
</div>

<div class="verdict">
<strong>整体评价：</strong>` + sensitivity + `。强势环境平均收益 ` + fmt.Sprintf("%.2f%%", strongAvg) + `（`+fmt.Sprintf("%d", strongCount)+`笔），弱势环境 ` + fmt.Sprintf("%.2f%%", weakAvg) + `（`+fmt.Sprintf("%d", weakCount)+`笔），收益差 ` + fmt.Sprintf("%.2f%%", diff) + `。<br>
<strong>结论：</strong>` + func() string {
		if diff > 2 {
			return "该策略对大盘环境高度敏感，弱势环境下应空仓或减仓，可显著提升整体收益。"
		} else if diff > 1 {
			return "该策略对大盘环境较为敏感，弱势环境下建议降低仓位。"
		}
		return "该策略各环境下表现接近，具备一定alpha。"
	}() + `
</div>

<h2>各维度分组统计</h2>
` + dimsHTML + `
<div class="note">红色为正收益，绿色为负收益。条形长度按收益绝对值等比显示。</div>

<h2>年度 × 综合状态 交叉表现</h2>
` + yearlyHTML + `
<div class="note">单元格为平均收益率(%)，下方为笔数和胜率。</div>

<h2>综合状态 × 月份 收益表</h2>
` + monthlyHTML + `
<div class="note">数值为该状态下该月的平均收益率(%)。红色为正，绿色为负。</div>

</body>
</html>`
}
