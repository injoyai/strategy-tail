package main

// 策略成交样例 K 线图导出
//
// 从最优参数组合（连涨2天·实体≥70%）的交易记录 CSV 中按收益率升序等距选取
// sampleCount（50）笔代表性成交（分位 0%~100%，覆盖最大亏损到最大盈利），
// 逐笔拉取日K（买入日前 1 年预热 EMA，卖出日后 20 天），计算 MA5/10/20 与
// MACD 量柱（12,26,9 —— 与策略组件同源的 util.MACDHistogram），
// 生成 ECharts K 线图 HTML（红涨绿跌，标注买卖点与持仓区间）。
//
// 本程序只负责生成 HTML；PDF 转换由 Chrome headless 完成：
//   & "C:\Program Files\Google\Chrome\Application\chrome.exe" --headless --disable-gpu `
//     --no-pdf-header-footer --virtual-time-budget=30000 `
//     --print-to-pdf="output\reports\策略成交样例K线图.pdf" `
//     "file:///C:/ssd/strategy-tail/output/reports/策略成交样例K线图.html"
//
// 用法（项目根目录执行）：go run ./cmd/kline_export

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/injoyai/logs"
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/strategies/util"
)

const (
	csvPath     = `output/trades/尾盘量柱阴线次日尾盘卖/连涨2天·实体≥70%.csv`
	reportDir   = "output/reports"
	htmlName    = "策略成交样例K线图50笔"
	sampleCount = 50  // 等距分位选样笔数
	warmupDays  = 365 // 买入日前自然日数（EMA26 + DEA9 预热充分）
	afterDays   = 20  // 卖出日后自然日数
	showBefore  = 30  // 图上买入日前显示的K线根数
	showAfter   = 6   // 图上卖出日后显示的K线根数
)

// tradeRec 一笔实际成交
type tradeRec struct {
	row       int
	code      string
	buyTime   time.Time
	buyPrice  float64
	sellTime  time.Time
	sellPrice float64
	qty       int64
	profit    float64
	rate      float64
	days      int
	reason    string // 选样原因
}

// ---- 输出 JSON 结构 ----

type chartJSON struct {
	Dates   []string     `json:"dates"`
	K       [][4]float64 `json:"k"` // [open, close, low, high]
	Vol     []float64    `json:"vol"`
	MACD    []float64    `json:"macd"`
	MA5     []*float64   `json:"ma5"`
	MA10    []*float64   `json:"ma10"`
	MA20    []*float64   `json:"ma20"`
	BuyIdx  int          `json:"buyIdx"`  // 窗口内索引
	SellIdx int          `json:"sellIdx"` // 窗口内索引
}

type sampleJSON struct {
	Meta  metaJSON  `json:"meta"`
	Chart chartJSON `json:"chart"`
}

type metaJSON struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	Reason    string  `json:"reason"`
	BuyTime   string  `json:"buyTime"`
	SellTime  string  `json:"sellTime"`
	BuyPrice  float64 `json:"buyPrice"`
	SellPrice float64 `json:"sellPrice"`
	Qty       int64   `json:"qty"`
	Profit    float64 `json:"profit"`
	Rate      float64 `json:"rate"`
	Days      int     `json:"days"`
	Total     int     `json:"total"` // 全部实际成交笔数
}

func main() {
	common.MustInitialize()

	trades := readTrades(csvPath)
	if len(trades) < 20 {
		logs.Errorf("实际成交样本过少: %d", len(trades))
		os.Exit(1)
	}
	logs.Infof("读取实际成交 %d 笔", len(trades))

	picked := pickSamples(trades, sampleCount)
	logs.Infof("选取代表样本 %d 笔", len(picked))

	samples := make([]sampleJSON, 0, len(picked))
	for _, t := range picked {
		s, err := buildSample(t, len(trades))
		if err != nil {
			logs.Errorf("样本 %s(%s) 处理失败: %v", t.code, t.buyTime.Format(time.DateOnly), err)
			continue
		}
		samples = append(samples, *s)
		logs.Infof("  %s %s | %s ~ %s | %.2f%% | %s",
			s.Meta.Code, s.Meta.Name, s.Meta.BuyTime, s.Meta.SellTime, s.Meta.Rate, t.reason)
	}
	if len(samples) == 0 {
		logs.Errorf("无可用样本")
		os.Exit(1)
	}

	if err := writeHTML(samples); err != nil {
		logs.Errorf("写 HTML 失败: %v", err)
		os.Exit(1)
	}
}

// readTrades 读取最优组合 CSV，剔除期末未平仓（虚拟成交）
func readTrades(path string) []tradeRec {
	f, err := os.Open(path)
	if err != nil {
		logs.Errorf("打开 CSV 失败: %v", err)
		os.Exit(1)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = 10
	header, err := r.Read()
	if err != nil {
		logs.Errorf("读表头失败: %v", err)
		os.Exit(1)
	}
	_ = header // 列序固定：代码,买入时间,买入价,卖出时间,卖出价,数量,盈亏(元),收益率(%),持仓天数,期末未平仓

	var out []tradeRec
	row := 1
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			logs.Errorf("第 %d 行解析失败: %v", row+1, err)
			os.Exit(1)
		}
		row++
		if rec[9] != "false" { // 期末未平仓 = 虚拟持仓，跳过
			continue
		}
		t := tradeRec{row: row, code: rec[0]}
		if t.buyTime, err = time.ParseInLocation(time.DateTime, rec[1], time.Local); err != nil {
			continue
		}
		if t.sellTime, err = time.ParseInLocation(time.DateTime, rec[3], time.Local); err != nil {
			continue
		}
		if t.buyPrice, err = strconv.ParseFloat(rec[2], 64); err != nil {
			continue
		}
		if t.sellPrice, err = strconv.ParseFloat(rec[4], 64); err != nil {
			continue
		}
		if t.qty, err = strconv.ParseInt(rec[5], 10, 64); err != nil {
			continue
		}
		t.profit, _ = strconv.ParseFloat(rec[6], 64) // 盈亏(元)
		t.rate, _ = strconv.ParseFloat(rec[7], 64)   // 收益率(%)
		t.days, _ = strconv.Atoi(rec[8])
		out = append(out, t)
	}
	return out
}

// pickSamples 确定性选样：按收益率升序等距取 count 个分位样本
// （0%~100% 均匀覆盖，两端即最大亏损与最大盈利），索引冲突时就近顺延
func pickSamples(trades []tradeRec, count int) []tradeRec {
	sorted := make([]tradeRec, len(trades))
	copy(sorted, trades)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].rate < sorted[j].rate })

	n := len(sorted)
	if count > n {
		count = n
	}
	type cand struct {
		idx    int
		reason string
	}
	candidates := make([]cand, 0, count)
	for i := 0; i < count; i++ {
		reason := fmt.Sprintf("分位%d%%", i*100/(count-1))
		switch i {
		case 0:
			reason = "最大亏损"
		case count - 1:
			reason = "最大盈利"
		}
		candidates = append(candidates, cand{(n - 1) * i / (count - 1), reason})
	}

	seen := map[int]bool{}
	var picked []tradeRec
	for _, c := range candidates {
		if c.idx < 0 || c.idx >= n {
			continue
		}
		if seen[c.idx] { // 分位重合时向两侧就近找未选样本
			found := -1
			for d := 1; d < n && found < 0; d++ {
				if lo := c.idx - d; lo >= 0 && !seen[lo] {
					found = lo
				} else if hi := c.idx + d; hi < n && !seen[hi] {
					found = hi
				}
			}
			if found < 0 {
				continue
			}
			c.idx = found
		}
		seen[c.idx] = true
		t := sorted[c.idx]
		t.reason = c.reason
		picked = append(picked, t)
	}

	// 按买入时间正序展示
	sort.Slice(picked, func(i, j int) bool { return picked[i].buyTime.Before(picked[j].buyTime) })
	return picked
}

// buildSample 拉取日K、计算 MA 与 MACD 量柱，组装单笔样本数据
func buildSample(t tradeRec, total int) (*sampleJSON, error) {

	name := common.Manage.Codes.GetName(t.code)

	start := time.Date(t.buyTime.Year(), t.buyTime.Month(), t.buyTime.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, -warmupDays)
	end := time.Date(t.sellTime.Year(), t.sellTime.Month(), t.sellTime.Day(), 23, 0, 0, 0, time.Local).AddDate(0, 0, afterDays)
	all, err := common.Pull.DayKlines(t.code, start, end)
	if err != nil {
		return nil, fmt.Errorf("拉取日K失败: %w", err)
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("无日K数据")
	}

	// 定位买入/卖出日索引（按日期匹配，日K时间戳为 15:00）
	buyDate := t.buyTime.Format(time.DateOnly)
	sellDate := t.sellTime.Format(time.DateOnly)
	buyIdx, sellIdx := -1, -1
	for i, k := range all {
		switch k.Time.Format(time.DateOnly) {
		case buyDate:
			buyIdx = i
		case sellDate:
			sellIdx = i
		}
	}
	if buyIdx < 0 {
		return nil, fmt.Errorf("日K中未找到买入日 %s", buyDate)
	}
	if sellIdx < 0 {
		sellIdx = len(all) - 1 // 极端：卖出日无K线（停牌边界），退化为最后一天
	}

	// 全量计算指标（保证窗口起点处 MA/MACD 已有历史），再切显示窗口
	closes := make([]float64, len(all))
	for i, k := range all {
		closes[i] = k.Close.Float64()
	}
	maSeries := func(p int) []*float64 {
		out := make([]*float64, len(all))
		for i := p - 1; i < len(all); i++ {
			s := 0.0
			for j := i - p + 1; j <= i; j++ {
				s += closes[j]
			}
			v := s / float64(p)
			out[i] = &v
		}
		return out
	}
	hist := util.MACDHistogram(all, 12, 26, 9) // 与 MACD连涨{MinDays:2} 默认参数一致

	lo := buyIdx - showBefore
	if lo < 0 {
		lo = 0
	}
	hi := sellIdx + showAfter
	if hi > len(all) {
		hi = len(all)
	}

	chart := chartJSON{
		Dates:   make([]string, 0, hi-lo),
		K:       make([][4]float64, 0, hi-lo),
		Vol:     make([]float64, 0, hi-lo),
		MACD:    make([]float64, 0, hi-lo),
		MA5:     maSeries(5)[lo:hi],
		MA10:    maSeries(10)[lo:hi],
		MA20:    maSeries(20)[lo:hi],
		BuyIdx:  buyIdx - lo,
		SellIdx: sellIdx - lo,
	}
	for i := lo; i < hi; i++ {
		k := all[i]
		chart.Dates = append(chart.Dates, k.Time.Format("06-01-02"))
		chart.K = append(chart.K, [4]float64{k.Open.Float64(), k.Close.Float64(), k.Low.Float64(), k.High.Float64()})
		chart.Vol = append(chart.Vol, float64(k.Volume))
		chart.MACD = append(chart.MACD, hist[i])
	}

	return &sampleJSON{
		Meta: metaJSON{
			Code: t.code, Name: name, Reason: t.reason,
			BuyTime: t.buyTime.Format("2006-01-02 15:04"), SellTime: t.sellTime.Format("2006-01-02 15:04"),
			BuyPrice: t.buyPrice, SellPrice: t.sellPrice,
			Qty: t.qty, Profit: t.profit, Rate: t.rate, Days: t.days,
			Total: total,
		},
		Chart: chart,
	}, nil
}

// writeHTML 生成 ECharts K 线图 HTML
func writeHTML(samples []sampleJSON) error {
	data, err := json.Marshal(samples)
	if err != nil {
		return err
	}

	html := strings.Replace(htmlTemplate, "__DATA__", string(data), 1)
	html = strings.Replace(html, "__GEN_AT__", time.Now().Format("2006-01-02 15:04"), 1)

	out := filepath.Join(reportDir, htmlName+".html")
	return os.WriteFile(out, []byte(html), 0644)
}

const htmlTemplate = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<title>策略成交样例K线图</title>
<script src="assets/echarts.min.js"></script>
<style>
  @page { size: A4; margin: 8mm; }
  * { box-sizing: border-box; -webkit-print-color-adjust: exact; print-color-adjust: exact; }
  body { margin: 0; font-family: "Microsoft YaHei", "PingFang SC", "Noto Sans CJK SC", sans-serif;
         color: #111827; font-size: 13px; }
  .page { width: 733px; margin: 0 auto; page-break-after: always; }
  .page:last-child { page-break-after: auto; }
  h1 { font-size: 21px; margin: 6px 0 2px; }
  .sub { color: #6b7280; font-size: 12px; margin-bottom: 10px; }
  .card { border: 1px solid #e5e7eb; border-radius: 6px; padding: 7px 12px; margin: 6px 0; background: #f9fafb; font-size: 11.5px; line-height: 1.6; }
  .card b { color: #111827; }
  table { border-collapse: collapse; width: 100%; font-size: 10.5px; margin-top: 4px; }
  th, td { border: 1px solid #d1d5db; padding: 2px 4px; text-align: center; white-space: nowrap; }
  th { background: #f3f4f6; font-weight: 600; }
  .win { color: #dc2626; font-weight: 600; }
  .loss { color: #16a34a; font-weight: 600; }
  .badge { display: inline-block; padding: 1px 9px; border-radius: 4px; color: #fff; font-weight: bold; font-size: 13px; }
  .badge.win { background: #ef4444; }
  .badge.loss { background: #22c55e; }
  .page-head { display: flex; align-items: baseline; gap: 10px; border-bottom: 2px solid #111827;
               padding-bottom: 5px; margin-bottom: 6px; }
  .page-head .title { font-size: 17px; font-weight: bold; }
  .page-head .reason { color: #6b7280; font-size: 12px; }
  .detail { margin-top: 6px; }
  .foot { color: #9ca3af; font-size: 10.5px; margin-top: 5px; }
</style>
</head>
<body>
<div id="pages"></div>
<script>
"use strict";
const DATA = __DATA__;
const RED = "#ef4444", GREEN = "#22c55e";

function esc(s) { return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;"); }
function fmt(n, d) { return Number(n).toFixed(d === undefined ? 2 : d); }
function cls(v) { return v >= 0 ? "win" : "loss"; }

// ---------- 封面页 ----------
(function buildCover() {
  const total = DATA[0].meta.total;
  function overviewTable(list, offset) {
    let rows = "";
    list.forEach(function (s, j) {
      const i = j + offset;
      rows += "<tr><td>" + (i + 1) + "</td><td>" + esc(s.meta.code) + "</td><td>" + esc(s.meta.name) +
        "</td><td class='" + cls(s.meta.rate) + "'>" + fmt(s.meta.rate) + "%</td>" +
        "<td style='text-align:left;white-space:normal;font-size:10.5px'>" + esc(s.meta.reason) + "</td></tr>";
    });
    return "<table style='width:32.6%;float:left;margin-right:1.1%'><tr><th>#</th><th>代码</th><th>名称</th>" +
      "<th>收益率</th><th>选样</th></tr>" + rows + "</table>";
  }
  const third = Math.ceil(DATA.length / 3);
  const html =
    "<div class='page'>" +
    "<h1>策略成交样例 K 线图</h1>" +
    "<div class='sub'>尾盘量柱阴线（次日尾盘卖）· 最优组合「连涨2天·实体≥70%」· 回测区间 2022-01-01 ~ 2026-09-04 · 生成于 " + "__GEN_AT__" + "</div>" +
    "<div class='card'><b>买入条件</b>（同时满足，尾盘按收盘价买入）：" +
    "流通市值 ≥ 20 亿；价格 2 ~ 120 元；过滤当日涨停；MACD 量柱（12,26,9）连续上升 ≥ 2 天；" +
    "当日收阴且阴线实体占振幅 ≥ 70%。<br>" +
    "<b>卖出规则</b>：持有 1 天，次日尾盘 14:55 起按分钟快照卖出（无分钟数据时按次日收盘价成交）。</div>" +
    "<div class='card'><b>选样方法</b>：全部 " + total + " 笔实际成交按收益率升序排列，" +
    "等距取 " + DATA.length + " 个分位样本（0% ~ 100% 均匀覆盖，两端即最大亏损与最大盈利），" +
    "覆盖整条盈亏分布。<br>" +
    "<b>图示约定</b>：红涨绿跌；<span style='color:#2563db'>▲买入</span> / <span style='color:#15803d'>▼卖出</span> 标注实际成交时点，" +
    "浅黄色底纹为持仓区间；副图为成交量与 MACD 量柱（与策略判定同源公式计算）。</div>" +
    overviewTable(DATA.slice(0, third), 0) +
    overviewTable(DATA.slice(third, third * 2), third) +
    overviewTable(DATA.slice(third * 2), third * 2) +
    "<div style='clear:both'></div>" +
    "<div class='foot'>数据来源：本地 SQLite 日线库（截至 2026-09-04）；买卖价格为成交时点价格，已含回测成本设定；各样本买卖时间与盈亏明细见对应样本页。</div>" +
    "</div>";
  document.getElementById("pages").innerHTML = html;
})();

// ---------- 每笔样本页 ----------
function makeOption(s) {
  const d = s.chart;
  const kData = d.k.map(function (v) {
    return { value: v, itemStyle: v[1] >= v[0]
      ? { color: RED, borderColor: RED } : { color: GREEN, borderColor: GREEN } };
  });
  const volData = d.k.map(function (v, i) {
    return { value: d.vol[i], itemStyle: { color: v[1] >= v[0] ? "#f87171" : "#4ade80", opacity: 0.9 } };
  });
  const macdData = d.macd.map(function (v) {
    return { value: v, itemStyle: { color: v >= 0 ? RED : GREEN } };
  });
  const step = Math.max(1, Math.round(d.dates.length / 10));
  const axis = {
    type: "category", data: d.dates, boundaryGap: true,
    axisLine: { lineStyle: { color: "#9ca3af" } }, axisTick: { show: false },
    axisLabel: { interval: function (i) { return i % step === 0; }, fontSize: 10, color: "#6b7280" }
  };
  const buyPt = {
    coord: [d.buyIdx, d.k[d.buyIdx][2]],
    symbol: "triangle", symbolSize: 13, symbolOffset: [0, "60%"],
    itemStyle: { color: "#2563db" },
    label: { show: true, position: "bottom", distance: 8, formatter: "买入", color: "#2563db", fontSize: 11, fontWeight: "bold" }
  };
  const sellPt = {
    coord: [d.sellIdx, d.k[d.sellIdx][3]],
    symbol: "triangle", symbolRotate: 180, symbolSize: 13, symbolOffset: [0, "-60%"],
    itemStyle: { color: "#15803d" },
    label: { show: true, position: "top", distance: 8, formatter: "卖出", color: "#15803d", fontSize: 11, fontWeight: "bold" }
  };
  return {
    animation: false,
    legend: { data: ["MA5", "MA10", "MA20"], top: 2, right: 10, itemWidth: 14, itemHeight: 8, textStyle: { fontSize: 11, color: "#374151" } },
    grid: [
      { left: 58, right: 14, top: 28, height: 336 },
      { left: 58, right: 14, top: 398, height: 82 },
      { left: 58, right: 14, top: 516, height: 118 }
    ],
    xAxis: [
      Object.assign({}, axis, { gridIndex: 0, show: false }),
      Object.assign({}, axis, { gridIndex: 1, show: false }),
      Object.assign({}, axis, { gridIndex: 2 })
    ],
    yAxis: [
      { gridIndex: 0, scale: true, splitLine: { lineStyle: { color: "#f3f4f6" } }, axisLabel: { fontSize: 10, color: "#6b7280" } },
      { gridIndex: 1, splitNumber: 2, axisLabel: { fontSize: 10, color: "#6b7280", formatter: function (v) { return v >= 1e8 ? (v / 1e8).toFixed(1) + "亿" : (v / 1e4).toFixed(0) + "万"; } }, splitLine: { show: false } },
      { gridIndex: 2, scale: true, splitNumber: 3, splitLine: { lineStyle: { color: "#f3f4f6" } }, axisLabel: { fontSize: 10, color: "#6b7280", formatter: function (v) { return v.toFixed(2); } } }
    ],
    series: [
      {
        name: "K线", type: "candlestick", xAxisIndex: 0, yAxisIndex: 0, data: kData,
        barWidth: "62%",
        markArea: { silent: true, itemStyle: { color: "rgba(250,204,21,0.13)" },
          data: [[{ xAxis: d.buyIdx }, { xAxis: d.sellIdx }]] },
        markPoint: { data: [buyPt, sellPt], animation: false }
      },
      { name: "MA5", type: "line", xAxisIndex: 0, yAxisIndex: 0, data: d.ma5, showSymbol: false, lineStyle: { width: 1, color: "#f59e0b" }, itemStyle: { color: "#f59e0b" } },
      { name: "MA10", type: "line", xAxisIndex: 0, yAxisIndex: 0, data: d.ma10, showSymbol: false, lineStyle: { width: 1, color: "#3b82f6" }, itemStyle: { color: "#3b82f6" } },
      { name: "MA20", type: "line", xAxisIndex: 0, yAxisIndex: 0, data: d.ma20, showSymbol: false, lineStyle: { width: 1, color: "#8b5cf6" }, itemStyle: { color: "#8b5cf6" } },
      { name: "成交量", type: "bar", xAxisIndex: 1, yAxisIndex: 1, data: volData, barWidth: "62%" },
      { name: "MACD", type: "bar", xAxisIndex: 2, yAxisIndex: 2, data: macdData, barWidth: "62%",
        markLine: { silent: true, symbol: "none", label: { show: false },
          lineStyle: { color: "#9ca3af", width: 1, type: "solid" }, data: [{ yAxis: 0 }] } }
    ]
  };
}

(function buildPages() {
  const root = document.getElementById("pages");
  let html = "";
  DATA.forEach(function (s, i) {
    html +=
      "<div class='page'>" +
      "<div class='page-head'>" +
      "<span class='title'>" + esc(s.meta.name) + "（" + esc(s.meta.code) + "）</span>" +
      "<span class='badge " + cls(s.meta.rate) + "'>" + fmt(s.meta.rate) + "%</span>" +
      "<span class='reason'>选样：" + esc(s.meta.reason) + " · 全部 " + s.meta.total + " 笔实际成交</span>" +
      "</div>" +
      "<div id='chart" + i + "' style='width:733px;height:678px;'></div>" +
      "<table class='detail'><tr>" +
      "<th>买入时间</th><th>买入价</th><th>卖出时间</th><th>卖出价</th>" +
      "<th>数量(股)</th><th>盈亏(元)</th><th>持仓(天)</th></tr>" +
      "<tr><td>" + esc(s.meta.buyTime) + "</td><td>" + fmt(s.meta.buyPrice) +
      "</td><td>" + esc(s.meta.sellTime) + "</td><td>" + fmt(s.meta.sellPrice) +
      "</td><td>" + s.meta.qty + "</td>" +
      "<td class='" + cls(s.meta.profit) + "'>" + fmt(s.meta.profit) + "</td>" +
      "<td>" + s.meta.days + "</td></tr></table>" +
      "<div class='foot'>▲买入 = 买入日收盘成交；▼卖出 = 卖出日 14:55 后分钟快照成交；浅黄底纹 = 持仓区间；MACD 参数 (12,26,9)。</div>" +
      "</div>";
  });
  root.innerHTML += html;
  DATA.forEach(function (s, i) {
    const el = document.getElementById("chart" + i);
    const chart = echarts.init(el, null, { renderer: "svg", width: 733, height: 678 });
    chart.setOption(makeOption(s));
  });
})();
</script>
</body>
</html>
`
