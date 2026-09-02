package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/logs"
)

// ============================================================================
// 未来收益分析 HTML 报告导出（自 forward_return.go 拆分，纯模板与导出逻辑）
// ============================================================================

// exportForwardReturnHTML 生成HTML报告到 output/future/future_report.html。
// 在原有统计图表基础上,新增命中点K线可视化区域。
func exportForwardReturnHTML(buyerName string, summaries []ForwardReturnSummary, allReturns []ForwardReturn, days []int, klineBefore, klineAfter int) {
	// 汇总表格数据(包含全部字段)
	type summaryRow struct {
		Days         int     `json:"days"`
		Count        int     `json:"count"`
		AvgReturn    float64 `json:"avgReturn"`
		MedianReturn float64 `json:"medianReturn"`
		WinRate      float64 `json:"winRate"`
		MaxReturn    float64 `json:"maxReturn"`
		MinReturn    float64 `json:"minReturn"`
	}
	rows := make([]summaryRow, 0, len(summaries))
	for _, s := range summaries {
		rows = append(rows, summaryRow{
			Days:         s.Days,
			Count:        s.Count,
			AvgReturn:    s.AvgReturn,
			MedianReturn: s.MedianReturn,
			WinRate:      s.WinRate,
			MaxReturn:    s.MaxReturn,
			MinReturn:    s.MinReturn,
		})
	}
	summaryJSON, _ := json.Marshal(rows)

	// 折线图数据(平均收益+胜率随天数变化)
	type dayPoint struct {
		Days      int     `json:"days"`
		AvgReturn float64 `json:"avgReturn"`
		WinRate   float64 `json:"winRate"`
		Count     int     `json:"count"`
	}
	curve := make([]dayPoint, 0, len(summaries))
	for _, s := range summaries {
		curve = append(curve, dayPoint{
			Days:      s.Days,
			AvgReturn: s.AvgReturn,
			WinRate:   s.WinRate,
			Count:     s.Count,
		})
	}
	curveJSON, _ := json.Marshal(curve)

	// 每个N天的收益率分布(分桶)
	type distData struct {
		Days    string    `json:"days"`
		Buckets []float64 `json:"buckets"`
	}
	bucketLabels := []string{"<-10%", "-10~-5%", "-5~0%", "0%", "0~5%", "5~10%", "10~20%", ">20%"}
	dists := make([]distData, 0, len(days))
	for _, n := range days {
		buckets := make([]float64, 8)
		for _, fr := range allReturns {
			r, ok := fr.Returns[n]
			if !ok {
				continue
			}
			switch {
			case r < -10:
				buckets[0]++
			case r < -5:
				buckets[1]++
			case r < 0:
				buckets[2]++
			case r == 0:
				buckets[3]++
			case r < 5:
				buckets[4]++
			case r < 10:
				buckets[5]++
			case r < 20:
				buckets[6]++
			default:
				buckets[7]++
			}
		}
		dists = append(dists, distData{
			Days:    fmt.Sprintf("%d天", n),
			Buckets: buckets,
		})
	}
	distJSON, _ := json.Marshal(dists)
	labelsJSON, _ := json.Marshal(bucketLabels)

	// 命中点K线可视化数据
	type hitKline struct {
		Time   string  `json:"time"`
		Open   float64 `json:"open"`
		Close  float64 `json:"close"`
		Low    float64 `json:"low"`
		High   float64 `json:"high"`
		Volume int64   `json:"volume"`
	}
	type hitCard struct {
		Index      int        `json:"index"`
		Code       string     `json:"code"`
		CodeName   string     `json:"codeName"`
		Date       string     `json:"date"`
		Year       int        `json:"year"`
		BuyPrice   float64    `json:"buyPrice"`
		Return5d   float64    `json:"return5d"`
		Return10d  float64    `json:"return10d"`
		Return20d  float64    `json:"return20d"`
		Klines     []hitKline `json:"klines"`
		BuyIdx     int        `json:"buyIdx"` // 命中日在 Klines 中的索引
		BeforeDays int        `json:"beforeDays"`
		AfterDays  int        `json:"afterDays"`
	}

	hitCards := make([]hitCard, 0, len(allReturns))
	for idx, fr := range allReturns {
		// 组装 K 线数组: before + buy + after
		ks := make([]hitKline, 0, len(fr.HitsBefore)+1+len(fr.HitsAfter))
		for _, k := range fr.HitsBefore {
			ks = append(ks, hitKline{
				Time: k.Time.Format("2006-01-02"),
				Open: k.Open.Float64(), Close: k.Close.Float64(),
				Low: k.Low.Float64(), High: k.High.Float64(),
				Volume: k.Volume,
			})
		}
		buyIdx := len(ks)
		if fr.BuyKline != nil {
			ks = append(ks, hitKline{
				Time: fr.BuyKline.Time.Format("2006-01-02"),
				Open: fr.BuyKline.Open.Float64(), Close: fr.BuyKline.Close.Float64(),
				Low: fr.BuyKline.Low.Float64(), High: fr.BuyKline.High.Float64(),
				Volume: fr.BuyKline.Volume,
			})
		}
		for _, k := range fr.HitsAfter {
			ks = append(ks, hitKline{
				Time: k.Time.Format("2006-01-02"),
				Open: k.Open.Float64(), Close: k.Close.Float64(),
				Low: k.Low.Float64(), High: k.High.Float64(),
				Volume: k.Volume,
			})
		}

		card := hitCard{
			Index:      idx,
			Code:       fr.Code,
			CodeName:   fr.CodeName,
			Date:       fr.BuyTime.Format("2006-01-02"),
			Year:       fr.Year,
			BuyPrice:   fr.BuyPrice.Float64(),
			Klines:     ks,
			BuyIdx:     buyIdx,
			BeforeDays: klineBefore,
			AfterDays:  klineAfter,
		}
		if r, ok := fr.Returns[5]; ok {
			card.Return5d = r
		}
		if r, ok := fr.Returns[10]; ok {
			card.Return10d = r
		}
		if r, ok := fr.Returns[20]; ok {
			card.Return20d = r
		}
		hitCards = append(hitCards, card)
	}
	hitsJSON, _ := json.Marshal(hitCards)

	html := futureReportHTML(buyerName, string(summaryJSON), string(curveJSON), string(distJSON), string(labelsJSON), string(hitsJSON))
	dir := filepath.Join("output", "future")
	os.MkdirAll(dir, 0755)
	output := filepath.Join(dir, "future_report.html")
	oss.New(output, []byte(html))
	logs.Info("HTML报告已生成: " + output)
}

// futureReportHTML 生成整合的未来收益分析 + 命中点K线可视化 HTML报告。
func futureReportHTML(buyerName, summaryJSON, curveJSON, distJSON, labelsJSON, hitsJSON string) string {
	return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>买入信号未来N天收益分析</title>
<script src="https://cdn.jsdelivr.net/npm/echarts@5/dist/echarts.min.js"></script>
<style>
:root{--bg:#f8f9fb;--bg2:#fff;--ink:#1a1a2e;--muted:#6b7280;--rule:#e5e7eb;--accent:#3b82f6;--red:#ef4444;--green:#22c55e}
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,"Microsoft YaHei","PingFang SC",sans-serif;background:var(--bg);color:var(--ink);line-height:1.6;font-size:15px}
.container{max-width:1100px;margin:0 auto;padding:24px 20px}
.header{text-align:center;padding:36px 20px 28px;background:linear-gradient(135deg,#1e293b 0%,#334155 100%);color:#fff;border-radius:12px;margin-bottom:28px}
.header h1{font-size:26px;font-weight:700;margin-bottom:6px}
.header .subtitle{font-size:14px;opacity:.8}
.section{margin-bottom:28px}
.section-title{font-size:19px;font-weight:700;margin-bottom:14px;padding-bottom:8px;border-bottom:2px solid var(--accent)}
.chart-box{background:var(--bg2);border-radius:10px;padding:18px;box-shadow:0 1px 3px rgba(0,0,0,.06);border:1px solid var(--rule);margin-bottom:16px}
.chart{width:100%;height:360px}
table{width:100%;border-collapse:collapse;font-size:14px}
th,td{padding:9px 12px;text-align:center;border-bottom:1px solid var(--rule);white-space:nowrap}
th{background:#f9fafb;font-weight:600;color:var(--muted)}
td.pos{color:var(--red);font-weight:600}
td.neg{color:var(--green);font-weight:600}
/* 命中点K线卡片 */
.toolbar{display:flex;gap:10px;align-items:center;flex-wrap:wrap;margin-bottom:16px}
.toolbar select,.toolbar input{height:34px;padding:0 10px;border:1px solid var(--rule);border-radius:6px;font-size:14px;background:var(--bg2)}
.toolbar label{font-size:14px;color:var(--muted)}
.hit-card{background:var(--bg2);border-radius:10px;padding:16px;box-shadow:0 1px 3px rgba(0,0,0,.06);border:1px solid var(--rule);margin-bottom:16px;page-break-inside:avoid}
.hit-header{font-size:15px;font-weight:600;margin-bottom:10px;display:flex;justify-content:space-between;align-items:center;flex-wrap:wrap;gap:8px}
.hit-header .code{color:var(--accent)}
.hit-header .ret{font-size:14px;font-weight:600}
.hit-header .ret.pos{color:var(--red)}
.hit-header .ret.neg{color:var(--green)}
.hit-chart{width:100%;height:320px}
.pager{display:flex;justify-content:center;gap:8px;align-items:center;margin:20px 0}
.pager button{padding:6px 14px;border:1px solid var(--rule);border-radius:6px;background:var(--bg2);cursor:pointer;font-size:14px}
.pager button:disabled{opacity:.5;cursor:not-allowed}
.pager span{font-size:14px;color:var(--muted)}
</style>
</head>
<body>
<div class="container">
<div class="header">
<h1>买入信号未来N天收益分析</h1>
<div class="subtitle">策略：` + buyerName + `  |  生成日期：` + time.Now().Format("2006-01-02") + `</div>
</div>

<div class="section">
<div class="section-title">汇总统计</div>
<div id="summaryTable"></div>
</div>

<div class="section">
<div class="section-title">平均收益率随持有天数变化</div>
<div class="chart-box"><div id="avgChart" class="chart"></div></div>
</div>

<div class="section">
<div class="section-title">胜率随持有天数变化</div>
<div class="chart-box"><div id="winChart" class="chart"></div></div>
</div>

<div class="section">
<div class="section-title">收益率分布</div>
<div class="chart-box"><div id="distChart" class="chart" style="height:400px"></div></div>
</div>

<div class="section">
<div class="section-title">命中点 K 线可视化</div>
<div class="toolbar">
<label>股票 <select id="hitCode"><option value="">全部代码</option></select></label>
<label>年份 <select id="hitYear"><option value="">全部年份</option></select></label>
<label>5日收益 min <input type="number" id="hitRetMin" style="width:70px" placeholder="-100"></label>
<label>max <input type="number" id="hitRetMax" style="width:70px" placeholder="1000"></label>
<label>起 <input type="date" id="hitDateStart"></label>
<label>止 <input type="date" id="hitDateEnd"></label>
<button id="hitFilter" style="height:34px;padding:0 14px;border:1px solid var(--rule);border-radius:6px;background:var(--accent);color:#fff;cursor:pointer">筛选</button>
</div>
<div id="hitList"></div>
<div class="pager" id="hitPager"></div>
</div>

</div>

<script>
const summaryRows = ` + summaryJSON + `;
const curve = ` + curveJSON + `;
const dists = ` + distJSON + `;
const bucketLabels = ` + labelsJSON + `;
const hitCards = ` + hitsJSON + `;
const PAGE_SIZE = 12;

// 汇总表格
(function(){
  let html = '<table><thead><tr><th>N天</th><th>信号数</th><th>平均收益</th><th>中位数</th><th>胜率</th><th>最大收益</th><th>最大亏损</th></tr></thead><tbody>';
  summaryRows.forEach(r=>{
    html += '<tr><td><b>'+r.days+'</b></td><td>'+r.count+'</td><td class="'+(r.avgReturn>=0?'pos':'neg')+'">'+r.avgReturn.toFixed(2)+'%</td><td>'+r.medianReturn.toFixed(2)+'%</td><td class="'+(r.winRate>=50?'pos':'neg')+'">'+r.winRate.toFixed(1)+'%</td><td class="pos">'+r.maxReturn.toFixed(2)+'%</td><td class="neg">'+r.minReturn.toFixed(2)+'%</td></tr>';
  });
  html += '</tbody></table>';
  document.getElementById('summaryTable').innerHTML = html;
})();

// 平均收益率折线
(function(){
  const chart = echarts.init(document.getElementById('avgChart'));
  chart.setOption({
    animation:false,
    tooltip:{trigger:'axis',appendToBody:true,formatter:p=>p[0].axisValue+'天<br/>平均收益: '+p[0].data.toFixed(2)+'%'},
    grid:{left:70,right:30,top:30,bottom:40},
    xAxis:{type:'category',data:curve.map(c=>c.days),name:'天数'},
    yAxis:{type:'value',name:'收益率%',axisLine:{lineStyle:{color:'#ccc'}}},
    series:[{
      name:'平均收益',type:'line',data:curve.map(c=>+c.avgReturn.toFixed(4)),
      symbol:'circle',symbolSize:8,lineStyle:{width:2,color:'#3b82f6'},
      itemStyle:{color:'#3b82f6'},
      markLine:{data:[{yAxis:0,lineStyle:{color:'#999',type:'dashed'}}],symbol:'none',label:{show:false}}
    }]
  });
  window.addEventListener('resize',()=>chart.resize());
})();

// 胜率折线
(function(){
  const chart = echarts.init(document.getElementById('winChart'));
  chart.setOption({
    animation:false,
    tooltip:{trigger:'axis',appendToBody:true,formatter:p=>p[0].axisValue+'天<br/>胜率: '+p[0].data.toFixed(1)+'%'},
    grid:{left:70,right:30,top:30,bottom:40},
    xAxis:{type:'category',data:curve.map(c=>c.days),name:'天数'},
    yAxis:{type:'value',name:'胜率%',min:0,max:100,axisLine:{lineStyle:{color:'#ccc'}}},
    series:[{
      name:'胜率',type:'line',data:curve.map(c=>+c.winRate.toFixed(2)),
      symbol:'circle',symbolSize:8,lineStyle:{width:2,color:'#ef4444'},
      itemStyle:{color:'#ef4444'},
      markLine:{data:[{yAxis:50,lineStyle:{color:'#999',type:'dashed'}}],symbol:'none',label:{show:false}}
    }]
  });
  window.addEventListener('resize',()=>chart.resize());
})();

// 收益率分布
(function(){
  const chart = echarts.init(document.getElementById('distChart'));
  const series = dists.map(d=>({
    name:d.days,type:'bar',stack:'total',
    data:d.buckets
  }));
  chart.setOption({
    animation:false,
    tooltip:{trigger:'axis',appendToBody:true},
    legend:{top:0,data:dists.map(d=>d.days)},
    grid:{left:60,right:30,top:40,bottom:40},
    xAxis:{type:'category',data:bucketLabels,axisLabel:{rotate:20}},
    yAxis:{type:'value',name:'信号数'},
    series:series
  });
  window.addEventListener('resize',()=>chart.resize());
})();

// ============ 命中点 K 线可视化 ============
(function(){
  // 初始化筛选下拉框
  const codeSet = new Set();
  const yearSet = new Set();
  hitCards.forEach(h=>{ codeSet.add(h.code); if(h.year) yearSet.add(h.year); });
  const codeSel = document.getElementById('hitCode');
  Array.from(codeSet).sort().forEach(c=>{
    const o=document.createElement('option'); o.value=c; o.textContent=c; codeSel.appendChild(o);
  });
  const yearSel = document.getElementById('hitYear');
  Array.from(yearSet).sort((a,b)=>b-a).forEach(y=>{
    const o=document.createElement('option'); o.value=y; o.textContent=y+'年'; yearSel.appendChild(o);
  });

  let filtered = hitCards.slice();
  let currentPage = 0;
  let renderedCharts = {}; // index -> echarts instance

  function applyFilter(){
    const code = codeSel.value;
    const year = yearSel.value;
    const rMin = parseFloat(document.getElementById('hitRetMin').value);
    const rMax = parseFloat(document.getElementById('hitRetMax').value);
    const dStart = document.getElementById('hitDateStart').value;
    const dEnd = document.getElementById('hitDateEnd').value;
    filtered = hitCards.filter(h=>{
      if(code && h.code!==code) return false;
      if(year && h.year!==parseInt(year)) return false;
      if(!isNaN(rMin) && h.return5d < rMin) return false;
      if(!isNaN(rMax) && h.return5d > rMax) return false;
      if(dStart && h.date < dStart) return false;
      if(dEnd && h.date > dEnd) return false;
      return true;
    });
    currentPage = 0;
    renderPage();
  }

  function renderPage(){
    const list = document.getElementById('hitList');
    const pager = document.getElementById('hitPager');
    // dispose 旧图表
    Object.values(renderedCharts).forEach(c=>{ try{c.dispose()}catch(e){} });
    renderedCharts = {};
    list.innerHTML = '';

    const start = currentPage * PAGE_SIZE;
    const end = Math.min(start + PAGE_SIZE, filtered.length);
    if(filtered.length === 0){
      list.innerHTML = '<div style="text-align:center;padding:40px;color:#999">无符合条件的命中点</div>';
      pager.innerHTML = '';
      return;
    }
    for(let i=start;i<end;i++){
      const h = filtered[i];
      const retCls = h.return5d >= 0 ? 'pos' : 'neg';
      const retStr = h.return5d !== 0 || h.return10d || h.return20d
        ? '5日:<span class="'+retCls+'">'+h.return5d.toFixed(2)+'%</span> 10日:<span class="'+(h.return10d>=0?'pos':'neg')+'">'+h.return10d.toFixed(2)+'%</span> 20日:<span class="'+(h.return20d>=0?'pos':'neg')+'">'+h.return20d.toFixed(2)+'%</span>'
        : '';
      const nameStr = h.codeName ? (' '+h.codeName) : '';
      const card = document.createElement('div');
      card.className = 'hit-card';
      card.innerHTML = '<div class="hit-header"><span><span class="code">'+h.code+'</span>'+nameStr+' · '+h.date+' · 买入'+h.buyPrice.toFixed(2)+'</span><span class="ret">'+retStr+'</span></div><div id="hit-chart-'+h.index+'" class="hit-chart"></div>';
      list.appendChild(card);
      // 初始化 ECharts
      renderHitChart(h);
    }
    // 分页器
    const totalPages = Math.ceil(filtered.length / PAGE_SIZE);
    pager.innerHTML = '';
    const prevBtn = document.createElement('button');
    prevBtn.textContent = '上一页';
    prevBtn.disabled = currentPage === 0;
    prevBtn.onclick = ()=>{ if(currentPage>0){ currentPage--; renderPage(); } };
    pager.appendChild(prevBtn);
    const info = document.createElement('span');
    info.textContent = '第 '+(currentPage+1)+'/'+totalPages+' 页（'+(start+1)+'-'+end+' / '+filtered.length+'）';
    pager.appendChild(info);
    const nextBtn = document.createElement('button');
    nextBtn.textContent = '下一页';
    nextBtn.disabled = currentPage >= totalPages - 1;
    nextBtn.onclick = ()=>{ if(currentPage<totalPages-1){ currentPage++; renderPage(); } };
    pager.appendChild(nextBtn);
  }

  function ma(v,p){
    const r=[];
    for(let i=0;i<v.length;i++){
      if(i+1<p){ r.push(null); continue; }
      let s=0;
      for(let j=i-p+1;j<=i;j++) s+=Number(v[j]||0);
      r.push(Number((s/p).toFixed(3)));
    }
    return r;
  }
  function ema(v,p){
    const a=2/(p+1);
    const r=[v[0]];
    for(let i=1;i<v.length;i++) r.push(r[i-1]+a*(v[i]-r[i-1]));
    return r;
  }
  function calcMACD(c){
    const e12=ema(c,12), e26=ema(c,26);
    const d=c.map((_,i)=>e12[i]-e26[i]);
    const de=ema(d,9);
    return{dif:d,dea:de,macd:d.map((v,i)=>(v-de[i])*2)};
  }

  function renderHitChart(h){
    const el = document.getElementById('hit-chart-'+h.index);
    if(!el) return;
    const chart = echarts.init(el);
    renderedCharts[h.index] = chart;

    const dates = h.klines.map(k=>k.time);
    const values = h.klines.map(k=>[k.open,k.close,k.low,k.high]);
    const closes = h.klines.map(k=>k.close);
    const vols = h.klines.map(k=>k.volume);
    const macd = calcMACD(closes);

    // 命中日 markPoint
    const markData = [];
    if(h.buyIdx >= 0 && h.buyIdx < values.length){
      const k = h.klines[h.buyIdx];
      markData.push({
        name:'命中日',
        coord:[dates[h.buyIdx], k.low],
        value:'B',
        symbol:'triangle',
        symbolSize:14,
        symbolRotate:0,
        symbolOffset:[0,16],
        itemStyle:{color:'#ef4444'},
        label:{show:true,formatter:'B',color:'#fff',fontWeight:'bold',fontSize:10,offset:[0,3]},
        tooltip:{formatter:'命中日 '+h.date+'<br/>买入: '+h.buyPrice.toFixed(2)}
      });
    }

    // 命中日K线高亮: 用 markLine 画一条竖线
    const markLineData = [];
    if(h.buyIdx >= 0 && h.buyIdx < dates.length){
      markLineData.push([{xAxis:dates[h.buyIdx],yAxis:'max'},{xAxis:dates[h.buyIdx],yAxis:'min'}]);
    }

    chart.setOption({
      animation:false,
      legend:{top:2,data:['日K','MA5','MA10','MA20','成交量','MACD','DIF','DEA']},
      tooltip:{trigger:'axis',axisPointer:{type:'cross'}},
      axisPointer:{link:[{xAxisIndex:'all'}]},
      grid:[{left:50,right:20,top:30,height:'52%'},{left:50,right:20,top:'66%',height:'12%'},{left:50,right:20,top:'82%',height:'14%'}],
      xAxis:[
        {type:'category',data:dates,boundaryGap:false,axisLabel:{fontSize:10}},
        {type:'category',gridIndex:1,data:dates,boundaryGap:false,axisLabel:{show:false}},
        {type:'category',gridIndex:2,data:dates,boundaryGap:false,axisLabel:{show:false}}
      ],
      yAxis:[
        {scale:true,splitArea:{show:true}},
        {scale:true,gridIndex:1,splitNumber:2,axisLabel:{show:false},splitLine:{show:false}},
        {scale:true,gridIndex:2,splitNumber:3,splitLine:{show:true}}
      ],
      series:[
        {name:'日K',type:'candlestick',data:values,
          itemStyle:{color:'#ef4444',color0:'#22c55e',borderColor:'#ef4444',borderColor0:'#22c55e'},
          markPoint:{data:markData},
          markLine:{data:markLineData,symbol:'none',lineStyle:{color:'#ef4444',type:'dashed',width:1},label:{show:false}}
        },
        {name:'MA5',type:'line',data:ma(closes,5),symbol:'none',lineStyle:{width:1,color:'#f59e0b'}},
        {name:'MA10',type:'line',data:ma(closes,10),symbol:'none',lineStyle:{width:1,color:'#8b5cf6'}},
        {name:'MA20',type:'line',data:ma(closes,20),symbol:'none',lineStyle:{width:1,color:'#3b82f6'}},
        {name:'成交量',type:'bar',xAxisIndex:1,yAxisIndex:1,data:vols,
          itemStyle:{color:p=>values[p.dataIndex]&&values[p.dataIndex][1]>=values[p.dataIndex][0]?'#ef4444':'#22c55e'}
        },
        {name:'MACD',type:'bar',xAxisIndex:2,yAxisIndex:2,data:macd.macd.map(v=>+v.toFixed(4)),
          itemStyle:{color:p=>p.data>=0?'#ef4444':'#22c55e'}
        },
        {name:'DIF',type:'line',xAxisIndex:2,yAxisIndex:2,data:macd.dif.map(v=>+v.toFixed(4)),symbol:'none',lineStyle:{width:1,color:'#f59e0b'}},
        {name:'DEA',type:'line',xAxisIndex:2,yAxisIndex:2,data:macd.dea.map(v=>+v.toFixed(4)),symbol:'none',lineStyle:{width:1,color:'#3b82f6'}}
      ]
    });
    window.addEventListener('resize',()=>chart.resize());
  }

  document.getElementById('hitFilter').addEventListener('click', applyFilter);
  // 初次渲染
  applyFilter();
})();
</script>
</body>
</html>`
}
