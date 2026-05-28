package core

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"time"

	"github.com/injoyai/goutil/g"
	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/goutil/other/csv"
	"github.com/injoyai/tdx/extend"
)

type AnalyzeResult struct {
	Year            int
	TotalTrades     int
	WinRate         float64
	TotalProfit     float64
	AvgProfit       float64
	MaxProfit       float64
	MaxLoss         float64
	ProfitFactor    float64
	MaxDrawdown     float64
	RequiredCapital float64
	AnnualReturn    float64
}

func PrintAnalyzeResults(results []AnalyzeResult) {
	if len(results) == 0 {
		return
	}
	fmt.Printf("\n年度回测结果:\n")
	fmt.Printf("%5s \t%4s \t%6s \t%9s \t%10s \t%10s \t%10s \t%7s \t%10s \t%10s \t%8s\n", "年份", "交易", "胜率", "总盈亏", "平均盈亏", "最大盈利", "最大亏损", "盈亏比", "最大回撤", "最低本金", "年化")
	for _, r := range results {
		profitFactor := fmt.Sprintf("%.2f", r.ProfitFactor)
		if r.TotalTrades > 0 && r.ProfitFactor == 0 && r.MaxLoss >= 0 {
			profitFactor = "∞"
		}
		fmt.Printf("%6d \t%8d \t%8s \t%12.2f \t%12.2f \t%12.2f \t%12.2f \t%8s \t%12.2f \t%12.2f \t%10s\n",
			r.Year,
			r.TotalTrades,
			formatPercent(r.WinRate),
			r.TotalProfit,
			r.AvgProfit,
			r.MaxProfit,
			r.MaxLoss,
			profitFactor,
			r.MaxDrawdown,
			r.RequiredCapital,
			formatPercent(r.AnnualReturn),
		)
	}
}

func formatPercent(v float64) string {
	return fmt.Sprintf("%.2f%%", v)
}

func Analyze(year int, allTrades []Trade, getDayKlines func(code string) (extend.Klines, error)) AnalyzeResult {

	// 2. 按时间排序，为了计算资金曲线和回撤
	sort.Slice(allTrades, func(i, j int) bool {
		return allTrades[i].BuyTime.Before(allTrades[j].BuyTime)
	})

	var totalTrades int = len(allTrades)
	var winCount int
	var totalProfit float64
	var grossProfit float64
	var grossLoss float64

	var maxProfit float64
	var maxLoss float64

	// 资金曲线
	var equityCurve []float64
	currentEquity := 0.0
	equityCurve = append(equityCurve, currentEquity)

	for _, t := range allTrades {
		// Price 是 int64 类型, 单位是厘 (0.001元)
		buy := float64(t.BuyPrice) / 1000.0
		sell := float64(t.SellPrice) / 1000.0
		profit := sell - buy

		totalProfit += profit
		currentEquity += profit
		equityCurve = append(equityCurve, currentEquity)

		if profit > 0 {
			winCount++
			grossProfit += profit
		} else {
			grossLoss += math.Abs(profit)
		}

		if len(equityCurve) == 2 || profit > maxProfit {
			maxProfit = profit
		}
		if len(equityCurve) == 2 || profit < maxLoss {
			maxLoss = profit
		}
	}

	// 计算最大回撤
	var maxDrawdown float64
	var peakEquity float64 = -math.MaxFloat64

	for _, eq := range equityCurve {
		if eq > peakEquity {
			peakEquity = eq
		}
		drawdown := peakEquity - eq
		if drawdown > maxDrawdown {
			maxDrawdown = drawdown
		}
	}

	profitFactor := 0.0
	if grossLoss != 0 {
		profitFactor = grossProfit / grossLoss
	}
	winRate := 0.0
	avgProfit := 0.0
	if totalTrades > 0 {
		winRate = float64(winCount) / float64(totalTrades) * 100
		avgProfit = totalProfit / float64(totalTrades) * 100
	}
	requiredCapital := calculateRequiredCapital(allTrades)
	annualReturn := 0.0
	if requiredCapital > 0 {
		annualReturn = totalProfit * 100 / requiredCapital * 100
	}

	result := AnalyzeResult{
		Year:            year,
		TotalTrades:     totalTrades,
		WinRate:         winRate,
		TotalProfit:     totalProfit * 100,
		AvgProfit:       avgProfit,
		MaxProfit:       maxProfit * 100,
		MaxLoss:         maxLoss * 100,
		ProfitFactor:    profitFactor,
		MaxDrawdown:     maxDrawdown * 100,
		RequiredCapital: requiredCapital,
		AnnualReturn:    annualReturn,
	}

	data := [][]any{
		{"代码", "买入时间", "买入价格", "卖出时间", "卖出价格", "盈亏", "收益率", "持有天数"},
	}

	for _, v := range allTrades {
		buyPrice := v.BuyPrice.Float64()
		profitRate := 0.0
		if buyPrice > 0 {
			profitRate = (v.SellPrice.Float64() - buyPrice) / buyPrice * 100
		}
		data = append(data, []any{
			v.Code,
			v.BuyTime.Format(time.DateTime), v.BuyPrice.Float64(),
			v.SellTime.Format(time.DateTime), v.SellPrice.Float64(),
			(v.SellPrice - v.BuyPrice).Float64() * 100,
			profitRate,
			v.SellTime.Sub(v.BuyTime).String(),
		})
	}

	buf, err := csv.Export(data)
	if err == nil {
		output := filepath.Join("./output/", fmt.Sprintf("%d.csv", year))
		oss.New(output, buf)
	}

	visualizeTrades(year, allTrades, getDayKlines)
	return result
}

func visualizeTrades(year int, allTrades []Trade, getDayKlines func(code string) (extend.Klines, error)) {
	ExportTradeVisualHTML([]int{year}, map[int][]Trade{year: allTrades}, getDayKlines)
}

func ExportTradeVisualHTML(years []int, yearlyTrades map[int][]Trade, getDayKlines func(code string) (extend.Klines, error)) {
	if len(years) == 0 {
		return
	}

	codeYears := make(map[string]map[int][]Trade)
	for _, year := range years {
		for _, tr := range yearlyTrades[year] {
			if codeYears[tr.Code] == nil {
				codeYears[tr.Code] = make(map[int][]Trade)
			}
			codeYears[tr.Code][year] = append(codeYears[tr.Code][year], tr)
		}
	}

	charts := make([]map[string]any, 0, len(codeYears))
	codes := make([]string, 0, len(codeYears))
	for code := range codeYears {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	for _, code := range codes {
		dks, err := getDayKlines(code)
		if err != nil || len(dks) == 0 {
			continue
		}

		kline := make([][]any, 0, len(dks))
		for _, k := range dks {
			kline = append(kline, []any{
				k.Time.Format(time.DateOnly),
				k.Open.Float64(),
				k.Close.Float64(),
				k.Low.Float64(),
				k.High.Float64(),
				k.Volume,
			})
		}

		tradeYears := codeYears[code]
		yearsForCode := make([]int, 0, len(tradeYears))
		for year := range tradeYears {
			yearsForCode = append(yearsForCode, year)
		}
		sort.Ints(yearsForCode)

		marks := make([]map[string]any, 0)
		tradeRows := make([]map[string]any, 0)
		for _, year := range yearsForCode {
			for _, t := range tradeYears[year] {
				buyRate := 0.0
				if t.BuyPrice.Float64() > 0 {
					buyRate = (t.SellPrice.Float64() - t.BuyPrice.Float64()) / t.BuyPrice.Float64() * 100
				}
				profit := (t.SellPrice - t.BuyPrice).Float64() * 100
				marks = append(marks, map[string]any{
					"date":  t.BuyTime.Format(time.DateOnly),
					"time":  t.BuyTime.Format(time.TimeOnly),
					"year":  year,
					"price": t.BuyPrice.Float64(),
					"type":  "买",
					"rate":  buyRate,
				})
				marks = append(marks, map[string]any{
					"date":  t.SellTime.Format(time.DateOnly),
					"time":  t.SellTime.Format(time.TimeOnly),
					"year":  year,
					"price": t.SellPrice.Float64(),
					"type":  "卖",
					"rate":  buyRate,
				})
				tradeRows = append(tradeRows, map[string]any{
					"year":      year,
					"buyDate":   t.BuyTime.Format(time.DateOnly),
					"buyTime":   t.BuyTime.Format(time.TimeOnly),
					"buyPrice":  t.BuyPrice.Float64(),
					"sellDate":  t.SellTime.Format(time.DateOnly),
					"sellTime":  t.SellTime.Format(time.TimeOnly),
					"sellPrice": t.SellPrice.Float64(),
					"profit":    profit,
					"rate":      buyRate,
				})
			}
		}
		sort.Slice(marks, func(i, j int) bool {
			left := fmt.Sprintf("%04v-%s %s", marks[i]["year"], marks[i]["date"], marks[i]["time"])
			right := fmt.Sprintf("%04v-%s %s", marks[j]["year"], marks[j]["date"], marks[j]["time"])
			return left < right
		})
		sort.Slice(tradeRows, func(i, j int) bool {
			left := fmt.Sprintf("%04v-%s %s", tradeRows[i]["year"], tradeRows[i]["buyDate"], tradeRows[i]["buyTime"])
			right := fmt.Sprintf("%04v-%s %s", tradeRows[j]["year"], tradeRows[j]["buyDate"], tradeRows[j]["buyTime"])
			return left < right
		})

		charts = append(charts, map[string]any{
			"code":   code,
			"years":  yearsForCode,
			"kline":  kline,
			"trades": marks,
			"rows":   tradeRows,
		})
	}

	if len(charts) == 0 {
		return
	}

	content, err := buildTradeVisualHTML(charts)
	if err != nil {
		return
	}
	output := filepath.Join("./output/", "trades.html")
	oss.New(output, []byte(content))
}

func buildTradeVisualHTML(charts []map[string]any) (string, error) {
	b, err := json.Marshal(charts)
	if err != nil {
		return "", err
	}
	data := string(b)
	return `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>回测买卖点</title>
<script src="https://cdn.jsdelivr.net/npm/echarts@5/dist/echarts.min.js"></script>
<style>
body{margin:0;font-family:Arial,"Microsoft YaHei",sans-serif;background:#f5f7fb;color:#222}.toolbar{position:sticky;top:0;z-index:10;background:#fff;padding:12px 16px;box-shadow:0 2px 10px rgba(0,0,0,.08);display:flex;gap:12px;align-items:center;flex-wrap:wrap}select,input{height:32px;padding:0 8px}.chart{height:720px;margin:16px;background:#fff;border-radius:8px;box-shadow:0 2px 10px rgba(0,0,0,.06)}.tips{color:#666;font-size:13px}.summary{display:flex;gap:10px;flex-wrap:wrap;margin:0 16px 16px}.card{background:#fff;border-radius:8px;box-shadow:0 2px 10px rgba(0,0,0,.06);padding:10px 14px;min-width:110px}.card b{display:block;font-size:18px;margin-top:4px}.profit{color:#ef232a}.loss{color:#14b143}.trades{margin:0 16px 24px;background:#fff;border-radius:8px;box-shadow:0 2px 10px rgba(0,0,0,.06);max-height:260px;overflow:auto}table{width:100%;border-collapse:collapse;font-size:13px}th,td{padding:8px 10px;border-bottom:1px solid #eef0f5;text-align:right}th:first-child,td:first-child{text-align:left}tbody tr:hover{background:#f7f9ff}
</style>
</head>
<body>
<div class="toolbar">
<label>股票代码 <select id="code"></select></label>
<label>搜索 <input id="search" placeholder="输入代码过滤"></label>
<span class="tips">红色 B 为买点，绿色 S 为卖点；鼠标悬停可查看价格和收益率</span>
</div>
<div id="summary" class="summary"></div>
<div id="chart" class="chart"></div>
<div class="trades">
<table>
<thead><tr><th>年份</th><th>买入日期</th><th>买入时间</th><th>买入价</th><th>卖出日期</th><th>卖出时间</th><th>卖出价</th><th>盈亏</th><th>收益率</th></tr></thead>
<tbody id="tradeRows"></tbody>
</table>
</div>
<script id="data" type="application/json">` + data + `</script>
<script>
const allData = ` + data + `;
const select = document.getElementById('code');
const search = document.getElementById('search');
const summary = document.getElementById('summary');
const tradeRows = document.getElementById('tradeRows');
const chart = echarts.init(document.getElementById('chart'));
function ema(values, period){
  if(!values.length){ return []; }
  const alpha = 2 / (period + 1);
  const result = [values[0]];
  for(let i = 1; i < values.length; i++){
    result.push(result[i - 1] + alpha * (values[i] - result[i - 1]));
  }
  return result;
}
function calcMACD(closes){
  const ema12 = ema(closes, 12);
  const ema26 = ema(closes, 26);
  const dif = closes.map((_, i) => ema12[i] - ema26[i]);
  const dea = ema(dif, 9);
  const macd = dif.map((v, i) => (v - dea[i]) * 2);
  return {dif, dea, macd};
}
function ma(values, period){
  return values.map((_, i) => {
    if(i + 1 < period){ return null; }
    let sum = 0;
    for(let j = i - period + 1; j <= i; j++){
      sum += Number(values[j] || 0);
    }
    return Number((sum / period).toFixed(3));
  });
}
function fmt(v, digits = 2){
  return Number(v || 0).toFixed(digits);
}
function cls(v){
  return Number(v || 0) >= 0 ? 'profit' : 'loss';
}
function renderSummary(item){
  const rows = item.rows || [];
  const total = rows.length;
  const win = rows.filter(x => Number(x.profit) > 0).length;
  const totalProfit = rows.reduce((sum, x) => sum + Number(x.profit || 0), 0);
  const avgRate = total ? rows.reduce((sum, x) => sum + Number(x.rate || 0), 0) / total : 0;
  const winRate = total ? win / total * 100 : 0;
  const maxProfit = total ? Math.max(...rows.map(x => Number(x.profit || 0))) : 0;
  const maxLoss = total ? Math.min(...rows.map(x => Number(x.profit || 0))) : 0;
  summary.innerHTML = [
    ['回测年份', (item.years || []).join('、'), ''],
    ['交易次数', total + '笔', ''],
    ['胜率', fmt(winRate) + '%', winRate >= 50 ? 'profit' : 'loss'],
    ['总盈亏', fmt(totalProfit) + '元', cls(totalProfit)],
    ['平均收益率', fmt(avgRate) + '%', cls(avgRate)],
    ['最大盈利', fmt(maxProfit) + '元', cls(maxProfit)],
    ['最大亏损', fmt(maxLoss) + '元', cls(maxLoss)]
  ].map(x => '<div class="card">' + x[0] + '<b class="' + x[2] + '">' + x[1] + '</b></div>').join('');
  tradeRows.innerHTML = rows.map((x, i) => '<tr data-index="' + i + '"><td>' + x.year + '</td><td>' + x.buyDate + '</td><td>' + x.buyTime + '</td><td>' + fmt(x.buyPrice) + '</td><td>' + x.sellDate + '</td><td>' + x.sellTime + '</td><td>' + fmt(x.sellPrice) + '</td><td class="' + cls(x.profit) + '">' + fmt(x.profit) + '</td><td class="' + cls(x.rate) + '">' + fmt(x.rate) + '%</td></tr>').join('');
}
function refreshOptions(){
  const keyword = search.value.trim();
  select.innerHTML = '';
  allData.filter(x => !keyword || x.code.includes(keyword)).forEach(x => {
    const option = document.createElement('option');
    option.value = x.code;
    option.textContent = x.code + '（' + Math.floor((x.trades || []).length / 2) + '笔）';
    select.appendChild(option);
  });
  render();
}
function render(){
  const item = allData.find(x => x.code === select.value) || allData[0];
  if(!item){ chart.setOption({title:{text:'没有可展示的交易'}}); return; }
  const dates = item.kline.map(x => x[0]);
  const values = item.kline.map(x => [x[1], x[2], x[3], x[4]]);
  const dateMap = new Map(item.kline.map(x => [x[0], x]));
  const closes = item.kline.map(x => x[2]);
  const ma5 = ma(closes, 5);
  const ma10 = ma(closes, 10);
  const ma20 = ma(closes, 20);
  const ma30 = ma(closes, 30);
  const ma60 = ma(closes, 60);
  const volumes = item.kline.map((x, i) => [i, x[5], x[2] >= x[1] ? 1 : -1]);
  const macd = calcMACD(closes);
  const macdBar = macd.macd.map(v => Number(v.toFixed(4)));
  const difLine = macd.dif.map(v => Number(v.toFixed(4)));
  const deaLine = macd.dea.map(v => Number(v.toFixed(4)));
  const markData = item.trades.map(x => {
    const k = dateMap.get(x.date);
    const basePrice = k ? (x.type === '买' ? k[3] : k[4]) : x.price;
    return {
      name: x.type,
      coord: [x.date, basePrice],
      value: x.type === '买' ? 'B' : 'S',
      symbol: x.type === '买' ? 'triangle' : 'triangle',
      symbolRotate: x.type === '买' ? 0 : 180,
      symbolSize: 14,
      symbolOffset: [0, x.type === '买' ? 12 : -12],
      itemStyle: {color: x.type === '买' ? '#ef232a' : '#14b143'},
      label: {show: true, formatter: x.type === '买' ? 'B' : 'S', color: '#fff', fontWeight: 'bold', fontSize: 10, offset: [0, x.type === '买' ? 4 : -4]},
      tooltip: {formatter: x.type + '<br/>' + x.date + ' ' + x.time + '<br/>成交价: ' + Number(x.price).toFixed(2) + '<br/>收益率: ' + Number(x.rate).toFixed(2) + '%'}
    };
  });
  let startIndex = 0;
  let endIndex = dates.length - 1;
  if(markData.length){
    const dateIndex = new Map(dates.map((d, i) => [d, i]));
    const tradeIndexes = markData.map(x => dateIndex.get(x.coord[0])).filter(x => x !== undefined);
    if(tradeIndexes.length){
      startIndex = Math.max(0, Math.min(...tradeIndexes) - 20);
      endIndex = Math.min(dates.length - 1, Math.max(...tradeIndexes) + 20);
    }
  }
  const startPercent = dates.length > 1 ? startIndex / (dates.length - 1) * 100 : 0;
  const endPercent = dates.length > 1 ? endIndex / (dates.length - 1) * 100 : 100;
  renderSummary(item);
  chart.setOption({
    animation:false,
    title:{text:item.code + ' 回测买卖点',left:16,top:10},
    legend:{top:12,textStyle:{color:'#222'},data:[
      {name:'日K'},
      {name:'MA5',icon:'roundRect'},
      {name:'MA10',icon:'roundRect'},
      {name:'MA20',icon:'roundRect'},
      {name:'MA30',icon:'roundRect'},
      {name:'MA60',icon:'roundRect'},
      {name:'成交量'},
      {name:'MACD'},
      {name:'DIF',icon:'roundRect'},
      {name:'DEA',icon:'roundRect'}
    ]},
    tooltip:{trigger:'axis',axisPointer:{type:'cross'}},
    axisPointer:{link:[{xAxisIndex:'all'}]},
    dataZoom:[{type:'inside',xAxisIndex:[0,1,2],start:startPercent,end:endPercent},{show:true,xAxisIndex:[0,1,2],type:'slider',bottom:8,start:startPercent,end:endPercent}],
    grid:[{left:60,right:30,top:60,height:'50%'},{left:60,right:30,top:'64%',height:'12%'},{left:60,right:30,top:'80%',height:'12%'}],
    xAxis:[{type:'category',data:dates,boundaryGap:false,axisLine:{onZero:false}},{type:'category',gridIndex:1,data:dates,boundaryGap:false,axisLine:{onZero:false},axisLabel:{show:false}},{type:'category',gridIndex:2,data:dates,boundaryGap:false,axisLine:{onZero:false},axisLabel:{show:false}}],
    yAxis:[{scale:true,splitArea:{show:true}},{scale:true,gridIndex:1,splitNumber:2,axisLabel:{show:false},splitLine:{show:false}},{scale:true,gridIndex:2,splitNumber:3,splitLine:{show:true}}],
    series:[
      {name:'日K',type:'candlestick',data:values,itemStyle:{color:'#ef232a',color0:'#14b143',borderColor:'#ef232a',borderColor0:'#14b143'},markPoint:{silent:false,data:markData}},
      {name:'MA5',type:'line',data:ma5,symbol:'none',smooth:true,lineStyle:{width:1,color:'#f5a623'},itemStyle:{color:'#f5a623'}},
      {name:'MA10',type:'line',data:ma10,symbol:'none',smooth:true,lineStyle:{width:1,color:'#9013fe'},itemStyle:{color:'#9013fe'}},
      {name:'MA20',type:'line',data:ma20,symbol:'none',smooth:true,lineStyle:{width:1,color:'#4a90e2'},itemStyle:{color:'#4a90e2'}},
      {name:'MA30',type:'line',data:ma30,symbol:'none',smooth:true,lineStyle:{width:1,color:'#8b572a'},itemStyle:{color:'#8b572a'}},
      {name:'MA60',type:'line',data:ma60,symbol:'none',smooth:true,lineStyle:{width:1,color:'#7ed321'},itemStyle:{color:'#7ed321'}},
      {name:'成交量',type:'bar',xAxisIndex:1,yAxisIndex:1,data:volumes.map(x=>x[1]),itemStyle:{color:p=>volumes[p.dataIndex][2]>0?'#ef232a':'#14b143'}},
      {name:'MACD',type:'bar',xAxisIndex:2,yAxisIndex:2,data:macdBar,itemStyle:{color:p=>p.data>=0?'#ef232a':'#14b143'}},
      {name:'DIF',type:'line',xAxisIndex:2,yAxisIndex:2,data:difLine,symbol:'none',lineStyle:{width:1,color:'#f5a623'},itemStyle:{color:'#f5a623'}},
      {name:'DEA',type:'line',xAxisIndex:2,yAxisIndex:2,data:deaLine,symbol:'none',lineStyle:{width:1,color:'#4a90e2'},itemStyle:{color:'#4a90e2'}}
    ]
  }, true);
}
window.addEventListener('resize', () => chart.resize());
select.addEventListener('change', render);
search.addEventListener('input', refreshOptions);
refreshOptions();
</script>
</body>
</html>`, nil
}

func calculateRequiredCapital(allTrades []Trade) float64 {

	m := map[string][]Trade{}

	for _, v := range allTrades {
		m[v.BuyTime.Format(time.DateOnly)] = append(m[v.BuyTime.Format(time.DateOnly)], v)
	}

	if len(m) == 0 {
		return 0
	}

	xx := make([]float64, 0, len(m))
	for _, ls := range m {
		xx = append(xx, func() float64 {
			x := float64(0)
			for _, v := range ls {
				x += v.BuyPrice.Float64() * 100
			}
			return x
		}())
	}

	return g.Max(0., xx...)

}
