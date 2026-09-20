# 买入信号未来N天收益分析 - 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新建独立模块,统计买入策略触发信号后未来N天(默认1,3,5,10,15,20,30天)的收益率分布,输出控制台统计和HTML报告。

**Architecture:** 在 `core/forward_return.go` 实现独立分析引擎,复用 `Buyer` 接口和 `bar.NewCoroutine` 并发模型,不依赖 Backtest/Seller/Cost。CLI入口在 `cmd/forward/main.go`。

**Tech Stack:** Go, ECharts (HTML报告), bar.NewCoroutine (并发)

**Spec:** `docs/superpowers/specs/2026-07-09-forward-return-analysis-design.md`

---

## 文件结构

| 文件 | 职责 |
|------|------|
| `core/forward_return.go` | 数据结构 + scan算法 + 汇总统计 + Run编排 + 控制台输出 + HTML报告 |
| `core/forward_return_test.go` | 单元测试 |
| `cmd/forward/main.go` | 独立CLI入口 |

---

### Task 1: 数据结构和 DefaultForwardDays

**Files:**
- Create: `core/forward_return.go`
- Test: `core/forward_return_test.go`

- [ ] **Step 1: 写测试 - DefaultForwardDays 返回正确值**

创建 `core/forward_return_test.go`:

```go
package core_test

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

func TestDefaultForwardDays(t *testing.T) {
	days := core.DefaultForwardDays()
	expected := []int{1, 3, 5, 10, 15, 20, 30}
	if len(days) != len(expected) {
		t.Fatalf("expected %d days, got %d", len(expected), len(days))
	}
	for i, d := range days {
		if d != expected[i] {
			t.Fatalf("days[%d]: expected %d, got %d", i, expected[i], d)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./core/ -run TestDefaultForwardDays -v`
Expected: FAIL (DefaultForwardDays 未定义)

- [ ] **Step 3: 写最小实现**

创建 `core/forward_return.go`:

```go
package core

import (
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// ============================================================================
// 买入信号未来N天收益分析(独立于 Backtest 引擎)
// ============================================================================

// DefaultForwardDays 返回默认的未来收益统计天数。
func DefaultForwardDays() []int {
	return []int{1, 3, 5, 10, 15, 20, 30}
}

// ForwardReturn 单次买入信号的未来收益记录。
type ForwardReturn struct {
	Code     string
	BuyTime  time.Time
	BuyPrice protocol.Price
	// Returns: N天 -> 收益率% = (dks[i+N].Close - BuyPrice) / BuyPrice * 100
	Returns map[int]float64
}

// ForwardReturnSummary 按N天汇总的统计指标。
type ForwardReturnSummary struct {
	Days         int
	Count        int     // 有效信号数(有足够未来数据的)
	AvgReturn    float64 // 平均收益率%
	MedianReturn float64 // 中位数收益率%
	WinRate      float64 // 正收益占比%
	MaxReturn    float64 // 最大收益率%
	MinReturn    float64 // 最小收益率%(负数)
}

// ForwardReturnAnalysis 独立分析器,与 Backtest 无关。
// 只需提供买入策略和日线数据源,统计信号触发后未来N天的收益分布。
type ForwardReturnAnalysis struct {
	Buyer
	Codes        []string
	Years        []int
	GetDayKlines GetDayKlines
	ForwardDays  []int // 为空时使用 DefaultForwardDays()
	Goroutines   int
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./core/ -run TestDefaultForwardDays -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add core/forward_return.go core/forward_return_test.go
git commit -m "feat: 新增买入信号未来N天收益分析数据结构"
```

---

### Task 2: scan() 信号扫描算法

**Files:**
- Modify: `core/forward_return.go`
- Test: `core/forward_return_test.go`

- [ ] **Step 1: 写测试 - scan 正确计算未来收益**

在 `core/forward_return_test.go` 追加:

```go
func TestScanForwardReturns(t *testing.T) {
	// 构造5天K线,收盘价 10,11,12,13,14
	base := time.Date(2024, 1, 2, 15, 0, 0, 0, time.Local)
	dks := make(extend.Klines, 5)
	for i := 0; i < 5; i++ {
		dks[i] = testKline(base.AddDate(0, 0, i), 10, 10, 10, 10+float64(i))
	}

	bt := core.ForwardReturnAnalysis{
		Buyer:       alwaysBuyer{},
		ForwardDays: []int{1, 3},
	}

	results := bt.Scan("test", nil, dks)

	// alwaysBuyer 每天都触发,5天 = 5个信号
	if len(results) != 5 {
		t.Fatalf("expected 5 signals, got %d", len(results))
	}

	// 第1个信号: i=0, buyPrice=10
	// N=1: dks[1].Close=11, return = (11-10)/10*100 = 10%
	// N=3: dks[3].Close=13, return = (13-10)/10*100 = 30%
	r0 := results[0]
	if r0.BuyPrice.Float64() != 10 {
		t.Fatalf("buyPrice: expected 10, got %v", r0.BuyPrice)
	}
	if r0.Returns[1] != 10.0 {
		t.Fatalf("N=1 return: expected 10.0, got %v", r0.Returns[1])
	}
	if r0.Returns[3] != 30.0 {
		t.Fatalf("N=3 return: expected 30.0, got %v", r0.Returns[3])
	}

	// 最后一个信号: i=4, N=1 超出范围,不应有该N的记录
	r4 := results[4]
	if _, ok := r4.Returns[1]; ok {
		t.Fatal("last signal should not have N=1 return (out of range)")
	}
	if _, ok := r4.Returns[3]; ok {
		t.Fatal("last signal should not have N=3 return (out of range)")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./core/ -run TestScanForwardReturns -v`
Expected: FAIL (Scan 方法未定义)

- [ ] **Step 3: 实现 scan 方法**

在 `core/forward_return.go` 追加:

```go
// Scan 对单只股票执行信号扫描,返回每次买入信号的未来收益记录。
// his 为历史K线(当年之前),dks 为当年日线。
// ls 构建方式与 Backtest.Do() 一致,确保信号检测结果相同。
func (this ForwardReturnAnalysis) Scan(code string, his, dks extend.Klines) []ForwardReturn {
	if len(dks) == 0 {
		return nil
	}

	days := this.ForwardDays
	if len(days) == 0 {
		days = DefaultForwardDays()
	}

	joinKlines := func(base extend.Klines, extra ...*extend.Kline) extend.Klines {
		ls := make(extend.Klines, 0, len(base)+len(extra))
		ls = append(ls, base...)
		ls = append(ls, extra...)
		return ls
	}

	result := []ForwardReturn(nil)
	for i := 0; i < len(dks); i++ {
		today := dks[i]
		_his := joinKlines(his, dks[:i]...)
		ls := joinKlines(_his, today)

		if !this.Buy(code, ls) {
			continue
		}

		buyPrice := today.Close
		returns := make(map[int]float64, len(days))
		for _, n := range days {
			idx := i + n
			if idx < len(dks) {
				futureClose := dks[idx].Close
				bp := buyPrice.Float64()
				if bp > 0 {
					returns[n] = (futureClose.Float64() - bp) / bp * 100
				}
			}
		}

		result = append(result, ForwardReturn{
			Code:     code,
			BuyTime:  today.Time,
			BuyPrice: buyPrice,
			Returns:  returns,
		})
	}

	return result
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./core/ -run TestScanForwardReturns -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add core/forward_return.go core/forward_return_test.go
git commit -m "feat: 实现买入信号扫描和未来收益计算"
```

---

### Task 3: 汇总统计

**Files:**
- Modify: `core/forward_return.go`
- Test: `core/forward_return_test.go`

- [ ] **Step 1: 写测试 - 汇总统计计算正确**

在 `core/forward_return_test.go` 追加:

```go
func TestSummarizeForwardReturns(t *testing.T) {
	returns := []core.ForwardReturn{
		{Returns: map[int]float64{1: 10.0, 3: 20.0}},
		{Returns: map[int]float64{1: -5.0, 3: 30.0}},
		{Returns: map[int]float64{1: 0.0, 3: -10.0}},
	}

	summaries := core.SummarizeForwardReturns(returns, []int{1, 3})

	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}

	// N=1: values = [10, -5, 0], avg = 5/3 ≈ 1.667, win=1, winRate=33.33%
	s1 := summaries[0]
	if s1.Days != 1 {
		t.Fatalf("days: expected 1, got %d", s1.Days)
	}
	if s1.Count != 3 {
		t.Fatalf("count: expected 3, got %d", s1.Count)
	}
	if math.Abs(s1.AvgReturn-1.6667) > 0.01 {
		t.Fatalf("avgReturn: expected ~1.6667, got %v", s1.AvgReturn)
	}
	if math.Abs(s1.WinRate-33.33) > 0.1 {
		t.Fatalf("winRate: expected ~33.33, got %v", s1.WinRate)
	}
	if s1.MaxReturn != 10.0 {
		t.Fatalf("maxReturn: expected 10.0, got %v", s1.MaxReturn)
	}
	if s1.MinReturn != -5.0 {
		t.Fatalf("minReturn: expected -5.0, got %v", s1.MinReturn)
	}
	// median of [10, -5, 0] sorted = [-5, 0, 10], median = 0
	if s1.MedianReturn != 0.0 {
		t.Fatalf("medianReturn: expected 0.0, got %v", s1.MedianReturn)
	}

	// N=3: values = [20, 30, -10], avg = 40/3 ≈ 13.33, win=2, winRate=66.67%
	s3 := summaries[1]
	if s3.Days != 3 {
		t.Fatalf("days: expected 3, got %d", s3.Days)
	}
	if math.Abs(s3.AvgReturn-13.333) > 0.01 {
		t.Fatalf("avgReturn: expected ~13.333, got %v", s3.AvgReturn)
	}
	if math.Abs(s3.WinRate-66.67) > 0.1 {
		t.Fatalf("winRate: expected ~66.67, got %v", s3.WinRate)
	}
	// median of [20, 30, -10] sorted = [-10, 20, 30], median = 20
	if s3.MedianReturn != 20.0 {
		t.Fatalf("medianReturn: expected 20.0, got %v", s3.MedianReturn)
	}
}

func TestSummarizeForwardReturnsEmpty(t *testing.T) {
	summaries := core.SummarizeForwardReturns(nil, []int{1, 3})
	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}
	for _, s := range summaries {
		if s.Count != 0 {
			t.Fatalf("expected count 0, got %d", s.Count)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./core/ -run TestSummarizeForwardReturns -v`
Expected: FAIL (SummarizeForwardReturns 未定义)

- [ ] **Step 3: 实现汇总统计函数**

在 `core/forward_return.go` 追加 import `"sort"` 并添加:

```go
// SummarizeForwardReturns 按N天汇总所有信号的未来收益统计。
func SummarizeForwardReturns(returns []ForwardReturn, days []int) []ForwardReturnSummary {
	summaries := make([]ForwardReturnSummary, 0, len(days))
	for _, n := range days {
		values := []float64(nil)
		for _, fr := range returns {
			if r, ok := fr.Returns[n]; ok {
				values = append(values, r)
			}
		}
		summaries = append(summaries, summarizeOne(n, values))
	}
	return summaries
}

// summarizeOne 计算单个N天的收益统计。
func summarizeOne(days int, values []float64) ForwardReturnSummary {
	if len(values) == 0 {
		return ForwardReturnSummary{Days: days, Count: 0}
	}

	sum := 0.0
	win := 0
	maxVal := values[0]
	minVal := values[0]
	for _, v := range values {
		sum += v
		if v > 0 {
			win++
		}
		if v > maxVal {
			maxVal = v
		}
		if v < minVal {
			minVal = v
		}
	}

	// 中位数
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)
	median := sorted[len(sorted)/2]
	if len(sorted)%2 == 0 {
		median = (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
	}

	return ForwardReturnSummary{
		Days:         days,
		Count:        len(values),
		AvgReturn:    sum / float64(len(values)),
		MedianReturn: median,
		WinRate:      float64(win) / float64(len(values)) * 100,
		MaxReturn:    maxVal,
		MinReturn:    minVal,
	}
}
```

同时更新 `core/forward_return.go` 的 import 块,添加 `"sort"`:

```go
import (
	"sort"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./core/ -run TestSummarizeForwardReturns -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add core/forward_return.go core/forward_return_test.go
git commit -m "feat: 实现未来收益汇总统计(平均/中位数/胜率/极值)"
```

---

### Task 4: Run() 编排 + 控制台输出

**Files:**
- Modify: `core/forward_return.go`
- Test: `core/forward_return_test.go`

- [ ] **Step 1: 写测试 - PrintForwardReturnSummary 不崩溃**

在 `core/forward_return_test.go` 追加:

```go
func TestPrintForwardReturnSummary(t *testing.T) {
	summaries := []core.ForwardReturnSummary{
		{Days: 1, Count: 100, AvgReturn: 0.5, MedianReturn: 0.2, WinRate: 52.0, MaxReturn: 9.8, MinReturn: -8.2},
		{Days: 3, Count: 98, AvgReturn: 1.2, MedianReturn: 0.5, WinRate: 55.0, MaxReturn: 18.5, MinReturn: -12.3},
	}
	// 只验证不 panic
	core.PrintForwardReturnSummary("测试策略", summaries)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./core/ -run TestPrintForwardReturnSummary -v`
Expected: FAIL (PrintForwardReturnSummary 未定义)

- [ ] **Step 3: 实现 Run() + scanYear() + PrintForwardReturnSummary()**

在 `core/forward_return.go` 更新 import 块:

```go
import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/injoyai/bar"
	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)
```

在文件末尾追加:

```go
// Run 执行全部分析:并发扫描买入信号 -> 汇总统计 -> 控制台输出 -> HTML报告。
func (this ForwardReturnAnalysis) Run() {
	days := this.ForwardDays
	if len(days) == 0 {
		days = DefaultForwardDays()
	}

	buyerName := "未知策略"
	if this.Buyer != nil {
		buyerName = this.Buyer.Name()
	}
	logs.Info("买入信号未来N天收益分析: " + buyerName)

	allReturns := []ForwardReturn(nil)
	for _, year := range this.Years {
		yearReturns := this.scanYear(year)
		allReturns = append(allReturns, yearReturns...)
	}

	if len(allReturns) == 0 {
		logs.Warn("未检测到任何买入信号")
		return
	}

	logs.Infof("共检测到 %d 个买入信号", len(allReturns))

	summaries := SummarizeForwardReturns(allReturns, days)
	PrintForwardReturnSummary(buyerName, summaries)
	exportForwardReturnHTML(buyerName, summaries, allReturns, days)
}

// scanYear 扫描单个年份的所有股票买入信号。
func (this ForwardReturnAnalysis) scanYear(year int) []ForwardReturn {
	hisStart := time.Date(year-2, 6, 1, 0, 0, 0, 0, time.Local)
	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
	end := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)

	goroutines := this.Goroutines
	if goroutines <= 0 {
		goroutines = 10
	}

	result := []ForwardReturn(nil)
	mu := sync.Mutex{}
	b := bar.NewCoroutine(
		len(this.Codes),
		goroutines,
		bar.WithPrefix(fmt.Sprintf("[%d][%s]", year, "xx000000")),
	)
	defer b.Close()

	for _, code := range this.Codes {
		b.Go(func() {
			b.SetPrefix(fmt.Sprintf("[%d][%s]", year, code))

			dks, err := this.GetDayKlines(code, hisStart, end)
			if err != nil {
				b.Logf("[错误] %s", err)
				b.Flush()
				return
			}

			his := []*extend.Kline(nil)
			for i, v := range dks {
				if v.Time.Before(start) {
					his = append(his, v)
				} else {
					dks = dks[i:]
					break
				}
			}

			frs := this.Scan(code, his, dks)
			mu.Lock()
			defer mu.Unlock()
			result = append(result, frs...)
		})
	}
	b.Wait()
	return result
}

// PrintForwardReturnSummary 打印未来N天收益汇总到控制台。
func PrintForwardReturnSummary(buyerName string, summaries []ForwardReturnSummary) {
	fmt.Printf("\n买入信号未来N天收益分析: %s\n\n", buyerName)
	fmt.Printf("%5s \t%8s \t%10s \t%10s \t%8s \t%10s \t%10s\n",
		"N天", "信号数", "平均收益", "中位数", "胜率", "最大收益", "最大亏损")
	for _, s := range summaries {
		fmt.Printf("%6d \t%10d \t%12.2f%% \t%12.2f%% \t%9.1f%% \t%12.2f%% \t%12.2f%%\n",
			s.Days, s.Count, s.AvgReturn, s.MedianReturn, s.WinRate, s.MaxReturn, s.MinReturn)
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./core/ -run TestPrintForwardReturnSummary -v`
Expected: PASS

- [ ] **Step 5: 运行全部已有测试确保无回归**

Run: `go test ./core/ -v`
Expected: 所有测试 PASS

- [ ] **Step 6: 提交**

```bash
git add core/forward_return.go core/forward_return_test.go
git commit -m "feat: 实现Run编排、并发扫描和控制台输出"
```

---

### Task 5: HTML 报告

**Files:**
- Modify: `core/forward_return.go`

- [ ] **Step 1: 实现 exportForwardReturnHTML + forwardReturnHTML**

在 `core/forward_return.go` 更新 import 块,添加 `"encoding/json"`, `"path/filepath"`, `"github.com/injoyai/goutil/oss"`:

```go
import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/injoyai/bar"
	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/logs"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)
```

在文件末尾追加:

```go
// exportForwardReturnHTML 生成HTML报告到 output/forward_returns.html。
func exportForwardReturnHTML(buyerName string, summaries []ForwardReturnSummary, allReturns []ForwardReturn, days []int) {
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

	html := forwardReturnHTML(buyerName, string(summaryJSON), string(curveJSON), string(distJSON), string(labelsJSON))
	output := filepath.Join("./output/", "forward_returns.html")
	oss.New(output, []byte(html))
	logs.Info("HTML报告已生成: " + output)
}

// forwardReturnHTML 生成未来收益分析HTML报告。
func forwardReturnHTML(buyerName, summaryJSON, curveJSON, distJSON, labelsJSON string) string {
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
.container{max-width:1000px;margin:0 auto;padding:24px 20px}
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
</div>

<script>
const summaryRows = ` + summaryJSON + `;
const curve = ` + curveJSON + `;
const dists = ` + distJSON + `;
const bucketLabels = ` + labelsJSON + `;

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
      markLine:{data:[{yAxis:0,lineStyle:{color:'#999',type:'dashed'}}],symbol:none,label:{show:false}}
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
      markLine:{data:[{yAxis:50,lineStyle:{color:'#999',type:'dashed'}}],symbol:none,label:{show:false}}
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
</script>
</body>
</html>`
}
```

- [ ] **Step 2: 编译确认无语法错误**

Run: `go build ./core/`
Expected: 编译成功,无错误

- [ ] **Step 3: 运行全部测试**

Run: `go test ./core/ -v`
Expected: 所有测试 PASS

- [ ] **Step 4: 提交**

```bash
git add core/forward_return.go
git commit -m "feat: 实现HTML报告(折线图+分布柱状图+汇总表)"
```

---

### Task 6: CLI 入口

**Files:**
- Create: `cmd/forward/main.go`

- [ ] **Step 1: 创建 cmd/forward/main.go**

```go
package main

import (
	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/strategies/buy"
)

func main() {
	codes := common.GetAllCodes()
	_, _, years, _, _ := common.LoadBacktestConfig()

	core.ForwardReturnAnalysis{
		Buyer:        TestBuy,
		Codes:        codes,
		Years:        years,
		GetDayKlines: common.Pull.DayKlines,
		ForwardDays:  core.DefaultForwardDays(),
		Goroutines:   common.DefaultGoroutines * 2,
	}.Run()
}

// TestBuy 买入策略(与 cmd/backtest 保持一致,可按需修改)
var TestBuy = buy.And{
	buy.A通达信倍量{Ratio: 2.9},
}
```

- [ ] **Step 2: 编译确认**

Run: `go build ./cmd/forward/`
Expected: 编译成功

- [ ] **Step 3: 运行全部测试**

Run: `go test ./...`
Expected: 所有测试 PASS

- [ ] **Step 4: 提交**

```bash
git add cmd/forward/main.go
git commit -m "feat: 新增 cmd/forward 独立CLI入口"
```

---

## 实现顺序总结

1. Task 1: 数据结构 + DefaultForwardDays (TDD)
2. Task 2: scan() 信号扫描算法 (TDD)
3. Task 3: 汇总统计 (TDD)
4. Task 4: Run() 编排 + 控制台输出 (TDD)
5. Task 5: HTML报告 (编译+测试验证)
6. Task 6: CLI入口 (编译验证)
