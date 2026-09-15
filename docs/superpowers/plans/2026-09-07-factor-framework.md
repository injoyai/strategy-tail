# 因子框架（Factor Framework）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 三期交付因子框架——① `core.Factor` 接口 + 14 个价量因子库 + `A因子过滤`；② `A因子TopN` + 横截面快照预计算注入；③ IC/分位收益研究引擎 + Lab 因子研究 Tab + 落盘。

**Architecture:** Factor 是无状态纯函数度量（float64，NaN=无效），Buyer 是决策（bool），`A因子过滤`/`A因子TopN` 是两者之间的桥。`Do()` 主循环零改动；`A因子TopN` 需要的横截面排名由回测/分析入口在启动前单遍预计算（复用 runner 的"每股数据只读一次"worker 池模式），写入 `core` 的内存快照（`map[key]map[day]map[code]int` + RWMutex，key=`TopNKey(因子名， asc)` 按因子+方向隔离），任务结束清空。IC 分析单遍扫描因子值+未来收益窗口，纯内存统计，落盘只存结论。

**Tech Stack:** Go（`math`/`sync`/`sync/atomic`/`sort`/`encoding/json`）；Yaegi binary 包注册（`(*T)(nil)`）；前端沿用 `internal/lab/web/lab/index.html` 无构建链 + ECharts 5 CDN。

**Spec:** `docs/superpowers/specs/2026-09-07-factor-framework-design.md`

---

## 与 spec 的两处偏差（已细化）

1. **`RSV{Days}` → `K值{Days}`**：spec §2.2 位置族原列 RSV；实现为标准 KDJ K 值（K = ⅔·K前 + ⅓·RSV，K₀=50），K 是 RSV 的平滑且更常用，信息包含 RSV。
2. **`backtest_tail` 不集成 TopN**：核实其组合硬编码（`A阴线收回`参数矩阵）无 TopN 使用场景，排除出计划范围；`A因子TopN` 在无快照时恒 false（注释写明契约），lab runner 是唯一注入方。

## 文件结构

| 文件 | 责任 | 动作 |
|---|---|---|
| `core/factor.go` | `Factor` 接口 + `DayOf` 交易日截断 | 新建 |
| `core/cross_section.go` | `TopNKey`/快照存储 `SetCrossSection`/`CrossSectionRank`/`ClearCrossSection` | 新建 |
| `core/factor_test.go`、`core/cross_section_test.go` | 接口/快照单测 | 新建 |
| `strategies/factor/momentum.go` | `N日动量`/`均线偏离`/`N日斜率` + `daysOr` | 新建 |
| `strategies/factor/volatility.go` | `N日波动`/`N日振幅` | 新建 |
| `strategies/factor/volume.go` | `量比`/`量分位`/`放量占比` | 新建 |
| `strategies/factor/kbar.go` | `实体幅度`/`上影占比`/`下影占比` | 新建 |
| `strategies/factor/position.go` | `N日高低位`/`K值` | 新建 |
| `strategies/factor/corr.go` | `Pearson`（导出共享）+ `量价相关` | 新建 |
| `strategies/factor/*_test.go` | 每因子 2 组用例（手算值 + NaN） | 新建 |
| `strategies/factor/registry.go` | 14 entry 注册表 `All`/`Build`/`Catalog` | 新建 |
| `strategies/buy/factor_filter.go` | `A因子过滤{Factor, Min, Max}` | 新建 |
| `strategies/buy/factor_top.go` | `A因子TopN{Factor, N, Asc}` | 新建 |
| `strategies/buy/factor_*_test.go` | 过滤/TopN 单测 | 新建 |
| `internal/lab/runner.go` | 提取 `yearData`/`forEachCodeData`；`run()` 改用；加 `task`/`lastAnalysis`/`StartAnalysis` | 修改 |
| `internal/lab/walk.go` | `collectTopN`（递归 `core.CompositeBuyer` 收集 TopN） | 新建 |
| `internal/lab/matrix.go` | `fillCrossSection`（单遍因子值 → 按 (key,day) 排名 → 快照） | 新建 |
| `internal/lab/walk_test.go`、`matrix_test.go` | 收集/填充单测 | 新建 |
| `internal/lab/factor_e2e_test.go` | TopN / 因子分析 API 端到端（server 生命周期 + 快照清理） | 新建 |
| `internal/lab/analysis.go` | `AnalyzeConfig`/`runAnalysis`/`AnalysisReport`/统计函数/`exportAnalysis` | 新建 |
| `internal/lab/analysis_test.go`、`analysis_run_test.go` | IC 统计单测 + 分析编排单测（TestRunAnalysis） | 新建 |
| `internal/lab/server.go` | 路由 `GET /api/factors`、`POST /api/analyze`、`GET /api/analysis/latest` | 修改 |
| `internal/lab/symbols.go` | 注册 14 因子类型 + `A因子过滤` + `A因子TopN` | 修改 |
| `internal/lab/factor_script_test.go` | Yaegi 因子脚本探针 | 新建 |
| `internal/lab/web/lab/index.html` | Tab④ 因子研究 | 修改 |

## 执行者必读的既有代码事实

- **价格构造**：`protocol.Yuan(10)`；`extend.Kline{Unix int64 xorm:"pk", *protocol.Kline}`，字段 `Time/ Open/ Close/ High/ Low protocol.Price`、`Volume int64`；取值用 `.Float64()`。
- **既有指标**（`lib/extend/model_kline.go`）：`Klines.REF(n)`/`HHV(n)`/`LLV(n)`/`MA(n)`/`EMA(n)`；`MA` 在 `len<n` 时返回 0（因子内先判长度）。
- **Buyer 约定**（`strategies/buy/buy_price.go`）：值接收者、中文类型名、`fmt.Sprintf` 中文名、零值参数取默认；`And`/`Or` 均为 `[]core.Buyer` 且实现 `Children()`。
- **组合遍历**：`core.CompositeBuyer{Buyer; Children() []Buyer}`（`core/types.go`）；`buy.Strategy` 包装器递归转发 `Children()`。
- **引擎**：`Backtest.Do(code, his, dks, mks)` 逐日前缀 `ls := full[:len(his)+i+1]`，会覆写 `today.Kline` 指针（因子只读不受影响，但 lab 侧每变体 `cloneKlines`）。
- **runner 模式**（`internal/lab/runner.go`）：`common.Pull.DayKlines/MinKlines` 取数；worker 池 `common.DefaultGoroutines * 2`；`resolveCodes` 按 `SampleMode` 解析；互斥 `mu.TryLock()`；状态 atomic。
- **测试数据**：`writeFakeDayDB(t, dir, code, days, base)`（server_test.go）写恒价日K sqlite（open=10 close=10.5 +5%日涨）；`common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}` 字面量替换；`t.Chdir(t.TempDir())` 隔离相对路径。
- **落盘模式**（`core/trades_export.go`）：`csv.Export([][]any)` → `oss.New(path, buf)`；`TradesExportName` 净化 Windows 非法字符。
- **因子与价格标度无关**：全部比值计算，测试用任意标度手算即可。

---

### Task 1: `core.Factor` 接口 + 横截面快照（TDD）

**Files:**
- Create: `core/factor.go`
- Create: `core/cross_section.go`
- Test: `core/factor_test.go`
- Test: `core/cross_section_test.go`

- [ ] **Step 1: 写失败测试**

创建 `core/factor_test.go`：

```go
package core

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// stubFactor 测试桩：恒返回构造时设定的值。
type stubFactor struct {
	name string
	val  float64
}

func (f stubFactor) Name() string { return f.name }
func (f stubFactor) Value(code string, dks extend.Klines) float64 { return f.val }

func TestDayOf_截断到当日墙钟零点(t *testing.T) {
	// 带时分秒的时间应截断到同日 00:00:00（不能按 UTC 绝对时间 Truncate）
	in := time.Date(2025, 3, 5, 15, 30, 0, 0, time.Local)
	got := DayOf(in)
	want := time.Date(2025, 3, 5, 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("DayOf=%v want %v", got, want)
	}
}

// mkKline 构造单根K线（因子全是比值计算，标度无关）。
func mkKline(base time.Time, i int, close float64) *extend.Kline {
	tm := base.AddDate(0, 0, i)
	return &extend.Kline{
		Unix: tm.Unix(),
		Kline: &protocol.Kline{
			Time:   tm,
			Open:   protocol.Yuan(close),
			Close:  protocol.Yuan(close),
			High:   protocol.Yuan(close),
			Low:    protocol.Yuan(close),
			Volume: 10000,
		},
	}
}
```

创建 `core/cross_section_test.go`：

```go
package core

import (
	"math/rand"
	"testing"
	"time"
)

func TestCrossSection_写入查询与名次(t *testing.T) {
	ClearCrossSection()
	defer ClearCrossSection()

	day := DayOf(time.Date(2025, 1, 6, 0, 0, 0, 0, time.Local))
	key := TopNKey("N日动量(20)", false) // 降序
	SetCrossSection(day, key, []string{"sz000003", "sh600001", "sh600002"})

	if r := CrossSectionRank(day, key, "sz000003"); r != 1 {
		t.Fatalf("榜首名次应=1, got %d", r)
	}
	if r := CrossSectionRank(day, key, "sh600002"); r != 3 {
		t.Fatalf("榜尾名次应=3, got %d", r)
	}
	if r := CrossSectionRank(day, key, "sh600009"); r != 0 {
		t.Fatalf("不在快照中名次应=0, got %d", r)
	}
	// 不同日期隔离
	other := day.AddDate(0, 0, 1)
	if r := CrossSectionRank(other, key, "sz000003"); r != 0 {
		t.Fatalf("跨日期不应命中, got %d", r)
	}
}

func TestCrossSection_方向key隔离互不覆盖(t *testing.T) {
	ClearCrossSection()
	defer ClearCrossSection()

	day := DayOf(time.Date(2025, 1, 6, 0, 0, 0, 0, time.Local))
	asc := TopNKey("量比(5)", true)
	desc := TopNKey("量比(5)", false)
	SetCrossSection(day, asc, []string{"sh600002", "sh600001"})
	SetCrossSection(day, desc, []string{"sh600001", "sh600002"})

	if CrossSectionRank(day, asc, "sh600002") != 1 || CrossSectionRank(day, desc, "sh600002") != 2 {
		t.Fatal("同因子不同方向的快照被覆盖")
	}
}

func TestCrossSection_并发读写安全(t *testing.T) {
	ClearCrossSection()
	defer ClearCrossSection()

	day := DayOf(time.Date(2025, 1, 6, 0, 0, 0, 0, time.Local))
	done := make(chan struct{})
	go func() { // 读协程
		for {
			select {
			case <-done:
				return
			default:
				CrossSectionRank(day, "k", "c1")
			}
		}
	}()
	for i := 0; i < 100; i++ {
		SetCrossSection(day, "k", []string{"c1", "c2"})
	}
	close(done)
}

func TestCrossSection_重复Set同日同key覆盖(t *testing.T) {
	ClearCrossSection()
	defer ClearCrossSection()

	day := DayOf(time.Date(2025, 1, 6, 0, 0, 0, 0, time.Local))
	SetCrossSection(day, "k", []string{"a", "b"})
	SetCrossSection(day, "k", []string{"b", "a"})
	if CrossSectionRank(day, "k", "b") != 1 || CrossSectionRank(day, "k", "a") != 2 {
		t.Fatal("重复 Set 应整体覆盖")
	}
}

// 防 lint：rand 引用占位（保持 import 稳定，供后续扩展随机并发写测试）
var _ = rand.Int
```

- [ ] **Step 2: 运行测试确认失败**

```powershell
go test ./core/ -run "TestDayOf|TestCrossSection" -count=1
```

预期：编译错误（`DayOf`/`TopNKey` 等未定义）。

- [ ] **Step 3: 实现**

创建 `core/factor.go`：

```go
package core

import (
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// Factor 策略因子：每股每天一个数值度量（与 Buyer 的布尔决策相对）。
// 因子是无状态纯函数，只读 dks（截至当日的 K 线前缀，引擎保证无前视）。
//
// 约定：
//   - 数据不足/除零/零方差返回 math.NaN()（与数值 0 区分，下游统一按"无效"处理）
//   - 全部为比值计算，与价格标度无关
type Factor interface {
	Name() string
	// Value 返回该股票当日的因子值；dks 为截至当日（含）的 K 线序列。
	Value(code string, dks extend.Klines) float64
}

// DayOf 把任意时间截断到当日墙钟零点（交易日归一）。
// 不能用 Truncate(24h)：它按 UTC 绝对时间截断，跨时区会切错日期。
func DayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
```

创建 `core/cross_section.go`：

```go
package core

import (
	"sync"
	"time"
)

// cross_section.go 横截面快照：A因子TopN 的排名上下文。
//
// A因子TopN.Buy() 是单股视角，看不到当日其他股票的因子值，排名由回测/分析
// 入口预计算后注入：先按因子+方向（TopNKey）收集需求 → 单遍算值 → 排名 →
// SetCrossSection 写入本快照 → 回测主循环 O(1) 查询 → 任务结束 ClearCrossSection。
//
// key=因子名（含参数，如 "N日动量(20)"）+方向：不同因子的快照互不覆盖；
// 同一因子多买方复用只算一次。仅内存，不落库（spec §6 边界）。

var (
	csMu    sync.RWMutex
	csStore = map[string]map[time.Time]map[string]int{} // key → day → code → 名次(1起)
)

// TopNKey 生成快照 key：因子名 + 方向，保证多变体多方向互不覆盖。
func TopNKey(factorName string, asc bool) string {
	if asc {
		return factorName + "|asc"
	}
	return factorName + "|desc"
}

// SetCrossSection 写入某日某 key 的排名快照（ranked[0] 为第 1 名），同 key 整体覆盖。
func SetCrossSection(day time.Time, key string, ranked []string) {
	day = DayOf(day)
	ranks := make(map[string]int, len(ranked))
	for i, code := range ranked {
		ranks[code] = i + 1
	}
	csMu.Lock()
	defer csMu.Unlock()
	byDay, ok := csStore[key]
	if !ok {
		byDay = map[time.Time]map[string]int{}
		csStore[key] = byDay
	}
	byDay[day] = ranks
}

// CrossSectionRank 查询某日某 key 下 code 的名次（1 起）；未命中返回 0。
func CrossSectionRank(day time.Time, key, code string) int {
	day = DayOf(day)
	csMu.RLock()
	defer csMu.RUnlock()
	byDay, ok := csStore[key]
	if !ok {
		return 0
	}
	ranks, ok := byDay[day]
	if !ok {
		return 0
	}
	return ranks[code]
}

// ClearCrossSection 清空全部快照（任务结束调用，防跨任务脏数据）。
func ClearCrossSection() {
	csMu.Lock()
	defer csMu.Unlock()
	csStore = map[string]map[time.Time]map[string]int{}
}
```

- [ ] **Step 4: 运行测试确认通过**

```powershell
go test ./core/ -run "TestDayOf|TestCrossSection" -count=1 -race
```

- [ ] **Step 5: 提交**

```powershell
git add core/factor.go core/cross_section.go core/factor_test.go core/cross_section_test.go; git commit -m "feat(core): Factor 接口与横截面快照注入上下文"
```

---

### Task 2: 动量族因子（TDD）

**Files:**
- Create: `strategies/factor/momentum.go`
- Test: `strategies/factor/momentum_test.go`

- [ ] **Step 1: 写失败测试**

创建 `strategies/factor/momentum_test.go`：

```go
package factor

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// ---- 测试工具（供 factor 包全部 *_test.go 共用）----

// mk 构造单根K线。
func mk(base time.Time, i int, o, h, l, c float64, vol int64) *extend.Kline {
	tm := base.AddDate(0, 0, i)
	return &extend.Kline{
		Unix: tm.Unix(),
		Kline: &protocol.Kline{
			Time: tm,
			Open: protocol.Yuan(o), Close: protocol.Yuan(c),
			High: protocol.Yuan(h), Low: protocol.Yuan(l),
			Volume: vol,
		},
	}
}

// closes 按收盘价序列构造等长K线（o=h=l=c，量恒 10000）。
func closes(base time.Time, cs ...float64) extend.Klines {
	ks := make(extend.Klines, 0, len(cs))
	for i, c := range cs {
		ks = append(ks, mk(base, i, c, c, c, c, 10000))
	}
	return ks
}

func wantNaN(t *testing.T, name string, v float64) {
	t.Helper()
	if !math.IsNaN(v) {
		t.Fatalf("%s 应返回 NaN, got %v", name, v)
	}
}

func wantVal(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.IsNaN(got) || math.Abs(got-want) > tol {
		t.Fatalf("%s = %v, want %v(±%v)", name, got, want, tol)
	}
}

// ---- 动量族 ----

func TestN日动量(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	// 收盘 1..10，Days=9：(10-1)/1 = 9
	ks := closes(base, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	f := N日动量{Days: 9}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 9, 1e-9)

	// 数据不足：len=9 < Days+1=10
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:9]))

	// 零值参数走默认 20
	if g := N日动量{}.Name(); g != "N日动量(20)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}

func Test均线偏离(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	// 收盘 1..10，MA5=(6+7+8+9+10)/5=8，偏离 (10-8)/8 = 0.25
	ks := closes(base, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	f := 均线偏离{Days: 5}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 0.25, 1e-9)

	wantNaN(t, "数据不足", f.Value("sh600000", ks[:4]))
}

func TestN日斜率(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	// 完美线性 ys=i+1（i=0..4）：slope=1, ȳ=3, 相对斜率 = 1/3
	ks := closes(base, 1, 2, 3, 4, 5)
	f := N日斜率{Days: 5}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 1.0/3.0, 1e-9)

	// 水平序列：slope=0
	flat := closes(base, 5, 5, 5, 5, 5)
	wantVal(t, "水平", f.Value("sh600000", flat), 0, 1e-12)

	wantNaN(t, "数据不足", f.Value("sh600000", ks[:1]))
}
```

- [ ] **Step 2: 运行测试确认失败**

```powershell
go test ./strategies/factor/ -run "TestN日动量|Test均线偏离|TestN日斜率" -count=1
```

- [ ] **Step 3: 实现**

创建 `strategies/factor/momentum.go`：

```go
package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// daysOr 参数默认值约定（与 A近N日涨幅小于 的零值默认一致）：Days<=0 走默认。
func daysOr(d, def int) int {
	if d <= 0 {
		return def
	}
	return d
}

// N日动量 是近 N 日涨跌幅：close / close[-N] - 1。正值动量强。
// 数据不足（len < Days+1）或基准收盘为 0 返回 NaN。
type N日动量 struct {
	Days int
}

func (f N日动量) Name() string {
	return fmt.Sprintf("N日动量(%d)", daysOr(f.Days, 20))
}

func (f N日动量) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n+1 {
		return math.NaN()
	}
	base := dks[len(dks)-n-1].Close.Float64()
	if base == 0 {
		return math.NaN()
	}
	return dks[len(dks)-1].Close.Float64()/base - 1
}

// 均线偏离 是收盘价相对 N 日均线的偏离率：(close - MA(N)) / MA(N)。
// 数据不足（len < Days）或均线为 0 返回 NaN。
type 均线偏离 struct {
	Days int
}

func (f 均线偏离) Name() string {
	return fmt.Sprintf("均线偏离(%d)", daysOr(f.Days, 20))
}

func (f 均线偏离) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n {
		return math.NaN()
	}
	ma := dks.MA(n).Float64()
	if ma == 0 {
		return math.NaN()
	}
	return dks[len(dks)-1].Close.Float64()/ma - 1
}

// N日斜率 是近 N 日收盘线性回归斜率的相对值：slope / ȳ（每日相对变化）。
// 数据不足（len < 2）或均价为 0 返回 NaN。
type N日斜率 struct {
	Days int
}

func (f N日斜率) Name() string {
	return fmt.Sprintf("N日斜率(%d)", daysOr(f.Days, 20))
}

func (f N日斜率) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if n < 2 || len(dks) < n {
		return math.NaN()
	}
	w := dks[len(dks)-n:]
	var sx, sy, sxx, sxy float64
	for i, k := range w {
		x, y := float64(i), k.Close.Float64()
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	fn := float64(n)
	den := fn*sxx - sx*sx
	if den == 0 {
		return math.NaN()
	}
	meanY := sy / fn
	if meanY == 0 {
		return math.NaN()
	}
	slope := (fn*sxy - sx*sy) / den
	return slope / meanY
}
```

- [ ] **Step 4: 运行测试确认通过**

```powershell
go test ./strategies/factor/ -count=1
```

- [ ] **Step 5: 提交**

```powershell
git add strategies/factor/momentum.go strategies/factor/momentum_test.go; git commit -m "feat(factor): 动量族因子（N日动量/均线偏离/N日斜率）与测试工具"
```

---

### Task 3: 波动族与量能族因子（TDD）

**Files:**
- Create: `strategies/factor/volatility.go`、`strategies/factor/volume.go`
- Test: `strategies/factor/volatility_test.go`、`strategies/factor/volume_test.go`

语义约定（执行者不得偏离）：

- `N日波动`：近 N 日日收益率的**总体**标准差（除以 n，非 n−1）；需 len ≥ N+1 根算出 N 个收益率，不足 → NaN。
- `N日振幅`：`(HHV(High,N) − LLV(Low,N)) / LLV(Low,N)`；LLV=0 → NaN；len < N → NaN。
- `量比`：今量 / 前 N 日均量（**不含今日**）；前 N 日均量=0 → NaN；len < N+1 → NaN。
- `量分位`：近 N 日（**含今日**）中 `vol ≤ 今量` 的天数占比，值域 [0,1]；len < N → NaN。
- `放量占比`：近 N 日（含今日）均量的 1.5 倍为阈值，`vol > 阈值` 的天数占比；len < N → NaN。

- [ ] **Step 1: 写失败测试**

创建 `strategies/factor/volatility_test.go`：

```go
package factor

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

func TestN日波动(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 恒定翻倍收益率（1,1,1）→ 标准差 0
	ks := closes(base, 10, 20, 40, 80)
	f := N日波动{Days: 3}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 0, 1e-12)

	// 收益率 {0.5, 0.5, -0.5}：均值 1/6，总体方差 = (1/9+1/9+4/9)/3 = 2/9
	ks2 := closes(base, 10, 15, 22.5, 11.25)
	wantVal(t, "混合", f.Value("sh600000", ks2), math.Sqrt(2.0/9.0), 1e-9)

	// 数据不足：len=3 < Days+1=4
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:3]))

	if g := N日波动{}.Name(); g != "N日波动(20)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}

func TestN日振幅(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 收盘 1..10（o=h=l=c），近5日：HHV=10, LLV=6 → (10-6)/6
	ks := closes(base, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	f := N日振幅{Days: 5}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 4.0/6.0, 1e-9)

	// LLV=0 → NaN（低点为 0 的合成K线）
	bad := extend.Klines{
		mk(base, 0, 10, 10, 0, 10, 10000),
		mk(base, 1, 10, 11, 9, 10, 10000),
	}
	f2 := N日振幅{Days: 2}
	wantNaN(t, "LLV为0", f2.Value("sh600000", bad))

	// 数据不足
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:4]))
}
```

创建 `strategies/factor/volume_test.go`：

```go
package factor

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// volKs 按收盘与成交量序列构造K线。
func volKs(base time.Time, cs []float64, vols []int64) extend.Klines {
	ks := make(extend.Klines, 0, len(cs))
	for i := range cs {
		ks = append(ks, mk(base, i, cs[i], cs[i], cs[i], cs[i], vols[i]))
	}
	return ks
}

func Test量比(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 今量 500，前 4 日均量 (100+200+300+400)/4=250 → 2
	ks := volKs(base, []float64{10, 10, 10, 10, 10}, []int64{100, 200, 300, 400, 500})
	f := 量比{Days: 4}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 2, 1e-9)

	// 恒量 → 1
	flat := volKs(base, []float64{10, 10, 10, 10}, []int64{10000, 10000, 10000, 10000})
	f2 := 量比{Days: 3}
	wantVal(t, "恒量", f2.Value("sh600000", flat), 1, 1e-9)

	// 前 N 日均量=0 → NaN
	zero := volKs(base, []float64{10, 10, 10}, []int64{0, 0, 500})
	f3 := 量比{Days: 2}
	wantNaN(t, "均量为0", f3.Value("sh600000", zero))

	// 数据不足：len=2 < Days+1=3
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:2]))

	if g := 量比{}.Name(); g != "量比(5)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}

func Test量分位(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// {1,2,3,4,5} 今量 5 → 5/5 全部 ≤ → 1
	up := volKs(base, []float64{10, 10, 10, 10, 10}, []int64{1, 2, 3, 4, 5})
	f := 量分位{Days: 5}
	wantVal(t, f.Name(), f.Value("sh600000", up), 1, 1e-9)

	// {5,4,3,2,1} 今量 1 → 仅 1 天 ≤ → 0.2
	down := volKs(base, []float64{10, 10, 10, 10, 10}, []int64{5, 4, 3, 2, 1})
	wantVal(t, "递减", f.Value("sh600000", down), 0.2, 1e-9)

	// 数据不足
	wantNaN(t, "数据不足", f.Value("sh600000", up[:4]))
}

func Test放量占比(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// {100,100,100,300}：均值 150，阈值 225 → 仅 300 放量 → 0.25
	ks := volKs(base, []float64{10, 10, 10, 10}, []int64{100, 100, 100, 300})
	f := 放量占比{Days: 4}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 0.25, 1e-9)

	// 恒量 → 0
	flat := volKs(base, []float64{10, 10, 10}, []int64{100, 100, 100})
	f2 := 放量占比{Days: 3}
	wantVal(t, "恒量", f2.Value("sh600000", flat), 0, 1e-12)

	// 数据不足
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:3]))
}
```

- [ ] **Step 2: 运行测试确认失败**

```powershell
go test ./strategies/factor/ -run "TestN日波动|TestN日振幅|Test量比|Test量分位|Test放量占比" -count=1
```

- [ ] **Step 3: 实现**

创建 `strategies/factor/volatility.go`：

```go
package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// N日波动 是近 N 日日收益率的总体标准差（衡量波动强度，值越大越剧烈）。
// 数据不足（len < Days+1）返回 NaN。
type N日波动 struct {
	Days int
}

func (f N日波动) Name() string {
	return fmt.Sprintf("N日波动(%d)", daysOr(f.Days, 20))
}

func (f N日波动) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n+1 {
		return math.NaN()
	}
	w := dks[len(dks)-n-1:]
	var sum float64
	rets := make([]float64, 0, n)
	for i := 1; i < len(w); i++ {
		prev := w[i-1].Close.Float64()
		if prev == 0 {
			return math.NaN()
		}
		r := w[i].Close.Float64()/prev - 1
		rets = append(rets, r)
		sum += r
	}
	mean := sum / float64(n)
	var ss float64
	for _, r := range rets {
		ss += (r - mean) * (r - mean)
	}
	return math.Sqrt(ss / float64(n))
}

// N日振幅 是近 N 日价格区间占比：(HHV(High,N) - LLV(Low,N)) / LLV(Low,N)。
// 数据不足（len < Days）或 LLV=0 返回 NaN。
type N日振幅 struct {
	Days int
}

func (f N日振幅) Name() string {
	return fmt.Sprintf("N日振幅(%d)", daysOr(f.Days, 20))
}

func (f N日振幅) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n {
		return math.NaN()
	}
	llv := dks.LLV(n).Float64()
	if llv == 0 {
		return math.NaN()
	}
	return (dks.HHV(n).Float64() - llv) / llv
}
```

创建 `strategies/factor/volume.go`：

```go
package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// 量比 是今日成交量相对前 N 日均量的倍数（不含今日）。
// 数据不足（len < Days+1）或前 N 日均量为 0 返回 NaN。
type 量比 struct {
	Days int
}

func (f 量比) Name() string {
	return fmt.Sprintf("量比(%d)", daysOr(f.Days, 5))
}

func (f 量比) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 5)
	if len(dks) < n+1 {
		return math.NaN()
	}
	w := dks[len(dks)-n-1 : len(dks)-1]
	var sum int64
	for _, k := range w {
		sum += k.Volume
	}
	avg := float64(sum) / float64(n)
	if avg == 0 {
		return math.NaN()
	}
	return float64(dks[len(dks)-1].Volume) / avg
}

// 量分位 是今量在近 N 日（含今日）中的分位：vol ≤ 今量 的天数占比。
// 数据不足（len < Days）返回 NaN。
type 量分位 struct {
	Days int
}

func (f 量分位) Name() string {
	return fmt.Sprintf("量分位(%d)", daysOr(f.Days, 60))
}

func (f 量分位) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 60)
	if len(dks) < n {
		return math.NaN()
	}
	today := dks[len(dks)-1].Volume
	var cnt int
	for _, k := range dks[len(dks)-n:] {
		if k.Volume <= today {
			cnt++
		}
	}
	return float64(cnt) / float64(n)
}

// 放量占比 是近 N 日（含今日）中成交量超过 1.5 倍日均量的天数占比。
// 数据不足（len < Days）返回 NaN。
type 放量占比 struct {
	Days int
}

func (f 放量占比) Name() string {
	return fmt.Sprintf("放量占比(%d)", daysOr(f.Days, 20))
}

func (f 放量占比) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n {
		return math.NaN()
	}
	w := dks[len(dks)-n:]
	var sum int64
	for _, k := range w {
		sum += k.Volume
	}
	threshold := 1.5 * float64(sum) / float64(n)
	var cnt int
	for _, k := range w {
		if float64(k.Volume) > threshold {
			cnt++
		}
	}
	return float64(cnt) / float64(n)
}
```

> 注：`dks.HHV(n)`/`dks.LLV(n)` 签名为 `func (ks Klines) HHV(n int) protocol.Price`（[model_kline.go](file:///c:/ssd/strategy-tail/lib/extend/model_kline.go#L48-L67)），`protocol.Price` 有 `.Float64()`。实现已在调用前保证 `len(dks) >= n`，不会触发其负索引 panic。

- [ ] **Step 4: 运行测试确认通过**

```powershell
go test ./strategies/factor/ -count=1
```

- [ ] **Step 5: 提交**

```powershell
git add strategies/factor/volatility.go strategies/factor/volatility_test.go strategies/factor/volume.go strategies/factor/volume_test.go; git commit -m "feat(factor): 波动族（N日波动/N日振幅）与量能族（量比/量分位/放量占比）因子"
```

---

### Task 4: K线形态族、位置族与量价相关（TDD）

**Files:**
- Create: `strategies/factor/kbar.go`、`strategies/factor/position.go`、`strategies/factor/corr.go`
- Test: `strategies/factor/kbar_test.go`、`strategies/factor/position_test.go`、`strategies/factor/corr_test.go`

语义约定：

- 三个 K 线形态因子为**单根K线**因子，无 Days 参数（registry 的 `New(days)` 对这三个 kind 忽略 days）。
  - `实体幅度` = `|c−o| / o`，o=0 → NaN，len=0 → NaN。
  - `上影占比` = `(h − max(o,c)) / (h − l)`；`h−l=0`（一字线）返回 **0 而非 NaN**（无影线）。
  - `下影占比` = `(min(o,c) − l) / (h − l)`，同上。
- `N日高低位` = `(c − LLV) / (HHV − LLV)`，值域 [0,1]；HHV=LLV → NaN；len < N → NaN。
- `K值`：KDJ 的 K 线（0..100）。逐日递推 `K = 2/3·K前 + 1/3·RSV`，首日 `K前=50`；`RSV = (c − LLVn) / (HHVn − LLVn) × 100`，窗口为截至当日最多 n 根；某日 `HHV=LLV` 时该日 RSV=50；len < n → NaN。**性能注记**：逐日重算窗口极值为 O(len×n)，全市场×250日约为 1.6e10 次比较——IC 分析（Task 12）必须用随机样本而非全市场（计划头部 Architecture 已约定）。
- `量价相关`：近 N 日（含今日）收盘价与成交量的 Pearson 相关系数；len < N 或零方差 → NaN。
- `Pearson(xs, ys []float64) float64` 导出函数：len=0 或 len 不等 → NaN；任一侧零方差 → NaN；供 `internal/lab` 的 IC 计算复用（Task 12）。

- [ ] **Step 1: 写失败测试**

创建 `strategies/factor/kbar_test.go`：

```go
package factor

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
)

func TestK线形态(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 光头光脚阳线 o=10 h=10 l=10 c=11 → 实体 0.1、上下影 0
	one := extend.Klines{mk(base, 0, 10, 11, 10, 11, 10000)}
	body := 实体幅度{}
	wantVal(t, "实体", body.Value("sh600000", one), 0.1, 1e-9)
	upper := 上影占比{}
	wantVal(t, "上影", upper.Value("sh600000", one), 0, 1e-12)
	lower := 下影占比{}
	wantVal(t, "下影", lower.Value("sh600000", one), 0, 1e-12)

	// 长上影：o=10 c=10.5 h=11 l=10 → 上影 (11-10.5)/1=0.5，下影 (10-10)/1=0
	two := extend.Klines{mk(base, 0, 10, 11, 10, 10.5, 10000)}
	wantVal(t, "上影", upper.Value("sh600000", two), 0.5, 1e-9)
	wantVal(t, "下影", lower.Value("sh600000", two), 0, 1e-12)

	// 长下影：o=10 c=10.5 h=10.5 l=9 → 上影 0，下影 (10-9)/1.5
	three := extend.Klines{mk(base, 0, 10, 10.5, 9, 10.5, 10000)}
	wantVal(t, "上影", upper.Value("sh600000", three), 0, 1e-12)
	wantVal(t, "下影", lower.Value("sh600000", three), 1.0/1.5, 1e-9)

	// 一字线 h=l → 0 非 NaN
	doji := extend.Klines{mk(base, 0, 10, 10, 10, 10, 10000)}
	wantVal(t, "一字上影", upper.Value("sh600000", doji), 0, 1e-12)
	wantVal(t, "一字下影", lower.Value("sh600000", doji), 0, 1e-12)

	// 空数据 → NaN
	wantNaN(t, "空", body.Value("sh600000", nil))
}
```

创建 `strategies/factor/position_test.go`：

```go
package factor

import (
	"math"
	"testing"
	"time"
)

func TestN日高低位(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 收盘 1..10，Days=10：c=HHV=10, LLV=1 → 1
	ks := closes(base, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	f := N日高低位{Days: 10}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 1, 1e-9)

	// 递减序列收盘在最低点 → 0
	down := closes(base, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1)
	wantVal(t, "最低点", f.Value("sh600000", down), 0, 1e-9)

	// HHV=LLV → NaN
	flat := closes(base, 5, 5, 5, 5, 5)
	f2 := N日高低位{Days: 5}
	wantNaN(t, "水平", f2.Value("sh600000", flat))

	// 数据不足
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:9]))

	if g := N日高低位{}.Name(); g != "N日高低位(60)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}

func TestK值(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 单调上涨 1..10（Days=9）：每日 RSV=100 → K9 = 100 - 50*(2/3)^9
	ks := closes(base, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	f := K值{Days: 9}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 100-50*math.Pow(2.0/3.0, 9), 1e-9)

	// 单调下跌 10..1：每日 RSV=0 → K9 = 50*(2/3)^9
	down := closes(base, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1)
	wantVal(t, "下跌", f.Value("sh600000", down), 50*math.Pow(2.0/3.0, 9), 1e-9)

	// 数据不足
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:8]))

	if g := K值{}.Name(); g != "K值(9)" {
		t.Fatalf("默认参数名异常: %s", g)
	}
}
```

> `K值` 期望式推导（上涨情形）：第 0 日窗口仅 1 根 → HHV=LLV → RSV=50 → K₀=50；第 1 日起 RSV 恒为 100，故 K₉ = 100 − (100−K₁)·(2/3)⁸ = 100 − 50·(2/3)⁹。下跌情形对称：K₉ = 50·(2/3)⁹。

创建 `strategies/factor/corr_test.go`：

```go
package factor

import (
	"math"
	"testing"
	"time"
)

func TestPearson(t *testing.T) {
	// 完全正相关
	if v := Pearson([]float64{1, 2, 3, 4, 5}, []float64{2, 4, 6, 8, 10}); math.Abs(v-1) > 1e-9 {
		t.Fatalf("Pearson = %v, want 1", v)
	}
	// 完全负相关
	if v := Pearson([]float64{1, 2, 3, 4, 5}, []float64{10, 8, 6, 4, 2}); math.Abs(v+1) > 1e-9 {
		t.Fatalf("Pearson = %v, want -1", v)
	}
	// 零方差 → NaN
	wantNaN(t, "零方差", Pearson([]float64{1, 2, 3}, []float64{5, 5, 5}))
	// 长度不等 → NaN
	wantNaN(t, "长度不等", Pearson([]float64{1, 2}, []float64{1, 2, 3}))
}

func Test量价相关(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 量随价同比例放大 → r=1
	cs := []float64{1, 2, 3, 4, 5}
	vols := []int64{100, 200, 300, 400, 500}
	ks := volKs(base, cs, vols)
	f := 量价相关{Days: 5}
	wantVal(t, f.Name(), f.Value("sh600000", ks), 1, 1e-9)

	// 恒量 → 零方差 → NaN
	flat := closes(base, 1, 2, 3, 4, 5)
	wantNaN(t, "恒量", f.Value("sh600000", flat))

	// 数据不足
	wantNaN(t, "数据不足", f.Value("sh600000", ks[:4]))
}
```

- [ ] **Step 2: 运行测试确认失败**

```powershell
go test ./strategies/factor/ -run "TestK线形态|TestN日高低位|TestK值|TestPearson|Test量价相关" -count=1
```

- [ ] **Step 3: 实现**

创建 `strategies/factor/kbar.go`：

```go
package factor

import (
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// 实体幅度 是K线实体占比：|close - open| / open。
// 空数据或 open=0 返回 NaN。
type 实体幅度 struct{}

func (f 实体幅度) Name() string { return "实体幅度" }

func (f 实体幅度) Value(code string, dks extend.Klines) float64 {
	if len(dks) == 0 {
		return math.NaN()
	}
	k := dks[len(dks)-1]
	o := k.Open.Float64()
	if o == 0 {
		return math.NaN()
	}
	return math.Abs(k.Close.Float64()-o) / o
}

// 上影占比 是上影线占全幅比例：(high - max(open, close)) / (high - low)。
// 一字线（high=low）返回 0。
type 上影占比 struct{}

func (f 上影占比) Name() string { return "上影占比" }

func (f 上影占比) Value(code string, dks extend.Klines) float64 {
	if len(dks) == 0 {
		return math.NaN()
	}
	k := dks[len(dks)-1]
	h, l := k.High.Float64(), k.Low.Float64()
	if h == l {
		return 0
	}
	top := k.Open.Float64()
	if c := k.Close.Float64(); c > top {
		top = c
	}
	return (h - top) / (h - l)
}

// 下影占比 是下影线占全幅比例：(min(open, close) - low) / (high - low)。
// 一字线（high=low）返回 0。
type 下影占比 struct{}

func (f 下影占比) Name() string { return "下影占比" }

func (f 下影占比) Value(code string, dks extend.Klines) float64 {
	if len(dks) == 0 {
		return math.NaN()
	}
	k := dks[len(dks)-1]
	h, l := k.High.Float64(), k.Low.Float64()
	if h == l {
		return 0
	}
	bot := k.Open.Float64()
	if c := k.Close.Float64(); c < bot {
		bot = c
	}
	return (bot - l) / (h - l)
}
```

创建 `strategies/factor/position.go`：

```go
package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// N日高低位 是收盘价在近 N 日区间中的位置：(close - LLV) / (HHV - LLV)，值域 [0,1]。
// 数据不足（len < Days）或 HHV=LLV 返回 NaN。
type N日高低位 struct {
	Days int
}

func (f N日高低位) Name() string {
	return fmt.Sprintf("N日高低位(%d)", daysOr(f.Days, 60))
}

func (f N日高低位) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 60)
	if len(dks) < n {
		return math.NaN()
	}
	hhv := dks.HHV(n).Float64()
	llv := dks.LLV(n).Float64()
	if hhv == llv {
		return math.NaN()
	}
	return (dks[len(dks)-1].Close.Float64() - llv) / (hhv - llv)
}

// K值 是 KDJ 指标中的 K 线（0..100）：K = 2/3·K前 + 1/3·RSV，首日 K前=50。
// RSV = (close - LLV) / (HHV - LLV) * 100，窗口为截至当日最多 n 根；HHV=LLV 时 RSV=50。
// 数据不足（len < Days）返回 NaN。
// 性能注记：逐日重算窗口极值为 O(len*n)，全市场约 1.6e10 次比较/250日——
// IC 分析（internal/lab）必须用随机样本，勿对全市场逐票全历史调用。
type K值 struct {
	Days int
}

func (f K值) Name() string {
	return fmt.Sprintf("K值(%d)", daysOr(f.Days, 9))
}

func (f K值) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 9)
	if len(dks) < n {
		return math.NaN()
	}
	k := 50.0
	for i := 0; i < len(dks); i++ {
		lo := i - n + 1
		if lo < 0 {
			lo = 0
		}
		w := dks[lo : i+1]
		hhv, llv := w[0].High.Float64(), w[0].Low.Float64()
		for _, k2 := range w[1:] {
			if v := k2.High.Float64(); v > hhv {
				hhv = v
			}
			if v := k2.Low.Float64(); v < llv {
				llv = v
			}
		}
		rsv := 50.0
		if hhv != llv {
			rsv = (dks[i].Close.Float64() - llv) / (hhv - llv) * 100
		}
		k = 2.0/3.0*k + 1.0/3.0*rsv
	}
	return k
}
```

创建 `strategies/factor/corr.go`：

```go
package factor

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/lib/extend"
)

// Pearson 计算两个等长序列的皮尔逊相关系数。
// 长度为 0、长度不等或任一侧零方差返回 NaN。
// 导出供 internal/lab 的 IC 分析复用（spearman = pearson(ranks(x), ranks(y))）。
func Pearson(xs, ys []float64) float64 {
	if len(xs) == 0 || len(xs) != len(ys) {
		return math.NaN()
	}
	var sx, sy float64
	for i := range xs {
		sx += xs[i]
		sy += ys[i]
	}
	n := float64(len(xs))
	mx, my := sx/n, sy/n
	var sxx, syy, sxy float64
	for i := range xs {
		dx, dy := xs[i]-mx, ys[i]-my
		sxx += dx * dx
		syy += dy * dy
		sxy += dx * dy
	}
	if sxx == 0 || syy == 0 {
		return math.NaN()
	}
	return sxy / math.Sqrt(sxx*syy)
}

// 量价相关 是近 N 日（含今日）收盘价与成交量的 Pearson 相关系数。
// 数据不足（len < Days）或零方差返回 NaN。
type 量价相关 struct {
	Days int
}

func (f 量价相关) Name() string {
	return fmt.Sprintf("量价相关(%d)", daysOr(f.Days, 20))
}

func (f 量价相关) Value(code string, dks extend.Klines) float64 {
	n := daysOr(f.Days, 20)
	if len(dks) < n {
		return math.NaN()
	}
	w := dks[len(dks)-n:]
	xs := make([]float64, n)
	ys := make([]float64, n)
	for i, k := range w {
		xs[i] = k.Close.Float64()
		ys[i] = float64(k.Volume)
	}
	return Pearson(xs, ys)
}
```

- [ ] **Step 4: 运行测试确认通过**

```powershell
go test ./strategies/factor/ -count=1
```

- [ ] **Step 5: 提交**

```powershell
git add strategies/factor/kbar.go strategies/factor/kbar_test.go strategies/factor/position.go strategies/factor/position_test.go strategies/factor/corr.go strategies/factor/corr_test.go; git commit -m "feat(factor): K线形态/位置/KDJ K值/量价相关因子与导出 Pearson"
```

---

### Task 5: 因子注册表（TDD）

**Files:**
- Create: `strategies/factor/registry.go`
- Test: `strategies/factor/registry_test.go`

设计约定：

- 14 个目录项 `entry{Kind, Default, New func(days int) core.Factor, Description}`，**显式 switch 式列表而非反射**（Yaegi 与 IDE 友好）。
- `All() []CatalogEntry`：`CatalogEntry{Kind, Name, Description}` 带 json tag（Task 13 的 `GET /api/factors` 直出）；Name 用 `New(Default).Name()` 复用因子自身的参数化名字。
- `Build(kind string, days int) core.Factor`：kind 未知返回 nil；days≤0 用 Default；单根K线因子（body/upper_shadow/lower_shadow）忽略 days。

- [ ] **Step 1: 写失败测试**

创建 `strategies/factor/registry_test.go`：

```go
package factor

import "testing"

func TestAll(t *testing.T) {
	all := All()
	if len(all) != 14 {
		t.Fatalf("目录应有 14 项, got %d", len(all))
	}
	if all[0].Kind != "momentum" || all[0].Name != "N日动量(20)" {
		t.Fatalf("首项异常: %+v", all[0])
	}
	// JSON 字段非空（供 /api/factors 直出）
	for _, e := range all {
		if e.Kind == "" || e.Name == "" || e.Description == "" {
			t.Fatalf("目录项字段缺失: %+v", e)
		}
	}
}

func TestBuild(t *testing.T) {
	// days<=0 → 默认参数
	if f := Build("momentum", 0); f == nil || f.Name() != "N日动量(20)" {
		t.Fatalf("默认参数异常: %v", f)
	}
	// 显式参数
	if f := Build("momentum", 5); f == nil || f.Name() != "N日动量(5)" {
		t.Fatalf("参数传递异常: %v", f)
	}
	// 具体类型断言
	if _, ok := Build("kvalue", 0).(*K值); !ok {
		t.Fatalf("kvalue 应构造 *K值")
	}
	if _, ok := Build("body", 0).(*实体幅度); !ok {
		t.Fatalf("body 应构造 *实体幅度")
	}
	// 未知 kind → nil
	if f := Build("no_such", 5); f != nil {
		t.Fatalf("未知 kind 应返回 nil, got %v", f)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```powershell
go test ./strategies/factor/ -run "TestAll|TestBuild" -count=1
```

- [ ] **Step 3: 实现**

创建 `strategies/factor/registry.go`：

```go
package factor

import (
	"github.com/injoyai/strategy-tail/core"
)

// CatalogEntry 因子目录项，json 字段供 GET /api/factors 直出。
type CatalogEntry struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type entry struct {
	Kind        string
	Default     int
	New         func(days int) core.Factor
	Description string
}

// registry 全部因子目录（显式列表，不用反射：Yaegi 与 IDE 跳转友好）。
var registry = []entry{
	{Kind: "momentum", Default: 20, New: func(d int) core.Factor { return &N日动量{Days: d} }, Description: "近N日涨跌幅"},
	{Kind: "ma_bias", Default: 20, New: func(d int) core.Factor { return &均线偏离{Days: d} }, Description: "收盘价相对N日均线的偏离率"},
	{Kind: "slope", Default: 20, New: func(d int) core.Factor { return &N日斜率{Days: d} }, Description: "近N日收盘线性回归斜率的相对值"},
	{Kind: "volatility", Default: 20, New: func(d int) core.Factor { return &N日波动{Days: d} }, Description: "近N日收益率总体标准差"},
	{Kind: "amplitude", Default: 20, New: func(d int) core.Factor { return &N日振幅{Days: d} }, Description: "近N日最高最低区间占比"},
	{Kind: "volume_ratio", Default: 5, New: func(d int) core.Factor { return &量比{Days: d} }, Description: "今日成交量相对前N日均量的倍数"},
	{Kind: "volume_pct", Default: 60, New: func(d int) core.Factor { return &量分位{Days: d} }, Description: "今量在近N日中的分位"},
	{Kind: "volume_surge", Default: 20, New: func(d int) core.Factor { return &放量占比{Days: d} }, Description: "近N日放量天数占比"},
	{Kind: "body", Default: 1, New: func(int) core.Factor { return &实体幅度{} }, Description: "K线实体占比"},
	{Kind: "upper_shadow", Default: 1, New: func(int) core.Factor { return &上影占比{} }, Description: "上影线占全幅比例"},
	{Kind: "lower_shadow", Default: 1, New: func(int) core.Factor { return &下影占比{} }, Description: "下影线占全幅比例"},
	{Kind: "position", Default: 60, New: func(d int) core.Factor { return &N日高低位{Days: d} }, Description: "收盘价在N日区间中的位置"},
	{Kind: "kvalue", Default: 9, New: func(d int) core.Factor { return &K值{Days: d} }, Description: "KDJ K线"},
	{Kind: "vp_corr", Default: 20, New: func(d int) core.Factor { return &量价相关{Days: d} }, Description: "近N日量价Pearson相关系数"},
}

// All 返回全部因子目录。
func All() []CatalogEntry {
	out := make([]CatalogEntry, 0, len(registry))
	for _, e := range registry {
		out = append(out, CatalogEntry{
			Kind:        e.Kind,
			Name:        e.New(e.Default).Name(),
			Description: e.Description,
		})
	}
	return out
}

// Build 按 kind 构造因子：kind 未知返回 nil；days<=0 使用该因子默认参数。
func Build(kind string, days int) core.Factor {
	for _, e := range registry {
		if e.Kind != kind {
			continue
		}
		if days <= 0 {
			days = e.Default
		}
		return e.New(days)
	}
	return nil
}
```

- [ ] **Step 4: 运行测试确认通过**

```powershell
go test ./strategies/factor/ -count=1
```

- [ ] **Step 5: 提交**

```powershell
git add strategies/factor/registry.go strategies/factor/registry_test.go; git commit -m "feat(factor): 14 因子注册表 All/Build/CatalogEntry"
```

---

### Task 6: A因子过滤买入策略（TDD）

**Files:**
- Create: `strategies/buy/factor_filter.go`
- Test: `strategies/buy/factor_filter_test.go`

设计约定：

- 风格对齐 `buy_price.go`：值接收者、零值语义、`fmt.Sprintf` 中文名。
- `A因子过滤{Factor core.Factor; Min, Max float64}`：闭区间 `[Min, Max]`；单边过滤另一侧用 `±1e9`（**不用 0 表示无限制**——0 是合法因子值）。
- `Factor == nil` 或因子返回 NaN → 恒 false。
- `Name()`：Factor 为 nil 时 `"因子过滤"`，否则 `fmt.Sprintf("%s∈[%.4g,%.4g]", Factor.Name(), Min, Max)`。

- [ ] **Step 1: 写失败测试**

创建 `strategies/buy/factor_filter_test.go`：

```go
package buy

import (
	"math"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// stubFactor 恒定返回预设值的因子桩。
type stubFactor struct {
	name string
	val  float64
}

func (f *stubFactor) Name() string                            { return f.name }
func (f *stubFactor) Value(code string, dks extend.Klines) float64 { return f.val }

func mkKline(base time.Time, i int, c float64) *extend.Kline {
	tm := base.AddDate(0, 0, i)
	return &extend.Kline{
		Unix: tm.Unix(),
		Kline: &protocol.Kline{Time: tm, Open: protocol.Yuan(c), Close: protocol.Yuan(c),
			High: protocol.Yuan(c), Low: protocol.Yuan(c), Volume: 10000},
	}
}

func TestA因子过滤(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	ks := extend.Klines{mkKline(base, 0, 10)}

	// 值 0.5 落在 [0, 1] → true
	b := A因子过滤{Factor: &stubFactor{name: "f", val: 0.5}, Min: 0, Max: 1}
	if !b.Buy("sh600000", ks) {
		t.Fatal("0.5∈[0,1] 应买入")
	}

	// 边界含端点：值 0 与 1 均命中
	if !(A因子过滤{Factor: &stubFactor{name: "f"}, Min: 0, Max: 1}).Buy("sh600000", ks) {
		t.Fatal("stubFactor 默认 val=0 应命中下界")
	}
	if !(A因子过滤{Factor: &stubFactor{name: "f", val: 1}, Min: 0, Max: 1}).Buy("sh600000", ks) {
		t.Fatal("1 应命中上界")
	}

	// 区间外 → false
	if (A因子过滤{Factor: &stubFactor{name: "f", val: 1.5}, Min: 0, Max: 1}).Buy("sh600000", ks) {
		t.Fatal("1.5∉[0,1] 不应买入")
	}

	// NaN → false
	nan := A因子过滤{Factor: &stubFactor{name: "f", val: math.NaN()}, Min: -1, Max: 1}
	if nan.Buy("sh600000", ks) {
		t.Fatal("NaN 不应买入")
	}

	// Factor=nil → false
	if (A因子过滤{Min: -1e9, Max: 1e9}).Buy("sh600000", ks) {
		t.Fatal("Factor 为 nil 不应买入")
	}

	// Name
	if g := b.Name(); g != "f∈[0,1]" {
		t.Fatalf("Name = %s", g)
	}
	if g := (A因子过滤{Factor: &stubFactor{name: "N日动量(5)", val: 0}, Min: -1e9, Max: 0.05}).Name(); g != "N日动量(5)∈[-1e+09,0.05]" {
		t.Fatalf("单边 Name = %s", g)
	}
	if g := (A因子过滤{}).Name(); g != "因子过滤" {
		t.Fatalf("nil Name = %s", g)
	}
}

// 编译期确认实现 Buyer。
var _ core.Buyer = A因子过滤{}
```

- [ ] **Step 2: 运行测试确认失败**

```powershell
go test ./strategies/buy/ -run TestA因子过滤 -count=1
```

- [ ] **Step 3: 实现**

创建 `strategies/buy/factor_filter.go`：

```go
package buy

import (
	"fmt"
	"math"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A因子过滤 按因子值闭区间过滤买入：Min <= 因子值 <= Max。
// 单边过滤时另一侧用 ±1e9（不用 0 表示无限制，0 是合法因子值）。
// Factor 为 nil 或因子返回 NaN（数据不足/除零）时恒 false。
type A因子过滤 struct {
	Factor core.Factor
	Min    float64
	Max    float64
}

func (b A因子过滤) Name() string {
	if b.Factor == nil {
		return "因子过滤"
	}
	return fmt.Sprintf("%s∈[%.4g,%.4g]", b.Factor.Name(), b.Min, b.Max)
}

func (b A因子过滤) Buy(code string, dks extend.Klines) bool {
	if b.Factor == nil {
		return false
	}
	v := b.Factor.Value(code, dks)
	if math.IsNaN(v) {
		return false
	}
	return b.Min <= v && v <= b.Max
}
```

- [ ] **Step 4: 运行测试确认通过**

```powershell
go test ./strategies/buy/ -run TestA因子过滤 -count=1
```

- [ ] **Step 5: 提交**

```powershell
git add strategies/buy/factor_filter.go strategies/buy/factor_filter_test.go; git commit -m "feat(buy): A因子过滤——因子值闭区间买入过滤"
```

---

### Task 7: A因子TopN 买入策略（TDD）

**Files:**
- Create: `strategies/buy/factor_top.go`
- Test: `strategies/buy/factor_top_test.go`

设计约定：

- `A因子TopN{Factor core.Factor; N int; Asc bool}`：当日该因子横截面快照中名次 ∈ `(0, N]` 时买入。
- `Asc=true` 因子值越小名次越靠前（升序，适合低位/低波动类）；`Asc=false` 值越大越靠前（降序，适合动量类）。
- **契约**：名次快照由回测任务启动前统一预填（Task 9-10 的 `fillCrossSection`），本策略自身只读不写——单股视角看不到其他股票。无快照的日期（如 `backtest_tail` 直接跑、或快照已被 `ClearCrossSection` 清空）恒 false。
- 日期取 `core.DayOf(dks 末根.Time)`（墙钟零点截断）；key 用 `core.TopNKey(Factor.Name(), Asc)` 按因子+方向隔离。
- `Factor == nil`、`len(dks) == 0`、`N <= 0` → false。

- [ ] **Step 1: 写失败测试**

创建 `strategies/buy/factor_top_test.go`（复用 factor_filter_test.go 的 `stubFactor` 与 `mkKline`，同包可见）：

```go
package buy

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

func TestA因子TopN(t *testing.T) {
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	ks := extend.Klines{mkKline(base, 0, 10)}
	day := core.DayOf(ks[len(ks)-1].Time)

	// 无快照 → 恒 false（契约：本策略只读不写快照）
	b := A因子TopN{Factor: &stubFactor{name: "f"}, N: 1, Asc: true}
	if b.Buy("sh600001", ks) {
		t.Fatal("无快照不应买入")
	}

	// 写入快照：升序名次 sh600001=1, sh600002=2 → N=1 只买名次 1
	key := core.TopNKey("f", true)
	core.SetCrossSection(day, key, []string{"sh600001", "sh600002"})
	defer core.ClearCrossSection()

	if !b.Buy("sh600001", ks) {
		t.Fatal("名次1应买入")
	}
	if b.Buy("sh600002", ks) {
		t.Fatal("名次2不应买入")
	}

	// N=2 名次 2 也买
	if !(A因子TopN{Factor: &stubFactor{name: "f"}, N: 2, Asc: true}).Buy("sh600002", ks) {
		t.Fatal("N=2 名次2应买入")
	}

	// N=0 → 恒 false
	if (A因子TopN{Factor: &stubFactor{name: "f"}, Asc: true}).Buy("sh600001", ks) {
		t.Fatal("N=0 不应买入")
	}

	// key 按方向隔离：desc 方向无快照 → false
	if (A因子TopN{Factor: &stubFactor{name: "f"}, N: 1, Asc: false}).Buy("sh600001", ks) {
		t.Fatal("desc 方向无快照不应买入")
	}

	// Name
	if g := b.Name(); g != "f TopN(1,升序)" {
		t.Fatalf("Name = %s", g)
	}

	var _ core.Buyer = A因子TopN{}
}
```

- [ ] **Step 2: 运行测试确认失败**

```powershell
go test ./strategies/buy/ -run TestA因子TopN -count=1
```

- [ ] **Step 3: 实现**

创建 `strategies/buy/factor_top.go`：

```go
package buy

import (
	"fmt"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// A因子TopN 按横截面因子排名买入：当日快照中名次 ∈ (0, N] 时买入。
// Asc=true 因子值越小名次越靠前（升序）；Asc=false 值越大越靠前（降序）。
//
// 契约：名次快照由回测任务启动前统一预填（internal/lab fillCrossSection），
// 本策略只读不写——单股视角看不到其他股票。无快照的日期恒 false
// （backtest_tail 直接跑本策略、或快照被 ClearCrossSection 清空后）。
type A因子TopN struct {
	Factor core.Factor
	N      int
	Asc    bool
}

func (b A因子TopN) Name() string {
	order := "降序"
	if b.Asc {
		order = "升序"
	}
	if b.Factor == nil {
		return "因子TopN(" + order + ")"
	}
	return fmt.Sprintf("%s TopN(%d,%s)", b.Factor.Name(), b.N, order)
}

func (b A因子TopN) Buy(code string, dks extend.Klines) bool {
	if b.Factor == nil || len(dks) == 0 || b.N <= 0 {
		return false
	}
	rank := core.CrossSectionRank(core.DayOf(dks[len(dks)-1].Time),
		core.TopNKey(b.Factor.Name(), b.Asc), code)
	return rank > 0 && rank <= b.N
}
```

- [ ] **Step 4: 运行测试确认通过**

```powershell
go test ./strategies/buy/ -run TestA因子TopN -count=1
```

- [ ] **Step 5: 提交**

```powershell
git add strategies/buy/factor_top.go strategies/buy/factor_top_test.go; git commit -m "feat(buy): A因子TopN——横截面名次买入（只读快照契约）"
```

---

### Task 8: runner 数据加载抽取 forEachCodeData（纯重构）

**Files:**
- Modify: `internal/lab/runner.go`
- Test: 既有 `internal/lab/server_test.go` 全量回归（本任务无新测试）

重构动机：Task 10（回测前填充因子快照）与 Task 12（IC 分析）都需要"逐票加载多年数据"的同一套 worker 池逻辑；把 [run()](file:///c:/ssd/strategy-tail/internal/lab/runner.go#L260-L405) 内联的加载代码提为可复用方法，两遍方案（填充与回测各加载一次，内存轻、2×DB 读可接受）。

行为保持不变的关键点：

- worker 池 `common.DefaultGoroutines * 2`（≤0 兜底 10）；停止检查语义（排队跳过 / 年份循环内跳出）逐行保留。
- 进度：`doneCodes` 对每个出队 code 恰好 +1（加载失败也计数，与原逻辑一致）；`currentCode` 仅在成功处理后更新。
- `yearData` 从 worker 闭包内 `type` 声明提升为包级。

- [ ] **Step 1: 基线验证（重构前现有测试必须全绿）**

```powershell
go test ./internal/lab/ -count=1
```

- [ ] **Step 2: 重构**

在 `internal/lab/runner.go` 中：

1. 在 `run` 上方新增包级类型与方法（原 worker 闭包内的加载代码**原样迁入**，仅做两处机械调整：`dks` 改为循环内 `dks := all[split:]` 声明；分钟线加载包进 `if needMinutes`）：

```go
// yearData 单票单年份的预加载数据切片。
type yearData struct {
	his, dks extend.Klines
	mks      protocol.Klines
}

// forEachCodeData worker 池逐票加载多年数据后回调 fn。
// fn 在 worker 协程执行，内部写共享状态需自行加锁。
// needMinutes=false 跳过分钟线加载（因子填充与 IC 分析只需日线）。
func (r *Runner) forEachCodeData(codes []string, years []int, stop chan struct{}, needMinutes bool, fn func(code string, datas []yearData)) {
	var wg sync.WaitGroup
	ch := make(chan string)
	workers := common.DefaultGoroutines * 2
	if workers <= 0 {
		workers = 10
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for code := range ch {
				// 停止检查：排队中的直接跳过
				select {
				case <-stop:
					continue
				default:
				}

				datas := make([]yearData, 0, len(years))
				ok := true
				for _, year := range years {
					select {
					case <-stop:
						ok = false
					default:
					}
					if !ok {
						break
					}
					hisStart := time.Date(year-2, 6, 1, 0, 0, 0, 0, time.Local)
					start := time.Date(year, 1, 1, 0, 0, 0, 0, time.Local)
					end := time.Date(year, 12, 31, 23, 0, 0, 0, time.Local)

					all, err := common.Pull.DayKlines(code, hisStart, end)
					if err != nil || len(all) == 0 {
						ok = false
						break
					}
					his := extend.Klines(nil)
					split := -1
					for i, v := range all {
						if v.Time.Before(start) {
							his = append(his, v)
						} else {
							split = i
							break
						}
					}
					if split < 0 {
						ok = false
						break
					}
					dks := all[split:]

					var mks protocol.Klines
					if needMinutes {
						mks, err = common.Pull.MinKlines(code, start, end)
						if err != nil {
							ok = false
							break
						}
					}
					datas = append(datas, yearData{his: his, dks: dks, mks: mks})
				}
				r.doneCodes.Add(1)
				if !ok {
					continue
				}
				fn(code, datas)

				cur := code
				r.currentCode.Store(&cur)
			}
		}()
	}

feed:
	for _, code := range codes {
		select {
		case <-stop:
			break feed
		case ch <- code:
		}
	}
	close(ch)
	wg.Wait()
}
```

2. `run()` 中删除内联的 worker 池与加载代码（原 [L280-L405](file:///c:/ssd/strategy-tail/internal/lab/runner.go#L280-L405) 的 `mu/wg/ch/workers` 声明、worker 循环、feed 循环、`close(ch)`、`wg.Wait()`），替换为：

```go
	var mu sync.Mutex
	r.forEachCodeData(codes, years, stop, true, func(code string, datas []yearData) {
		// 该股票跑全部变体；每变体独立克隆（引擎 Do() 会覆写 Kline 指针）
		variantTrades := make([][]core.Trade, len(variants))
		for vi, v := range variants {
			select {
			case <-stop:
				variantTrades = nil
			default:
			}
			if variantTrades == nil {
				break
			}
			bt := core.Backtest{
				Buyer:        sb.Strategy(v.Name, v.Buyer),
				Seller:       seller,
				Codes:        []string{code},
				Years:        years,
				Cost:         cost,
				Position:     pos,
				MCIterations: 0,
			}
			ts := []core.Trade(nil)
			for _, d := range datas {
				ts = append(ts, bt.Do(code, cloneKlines(d.his), cloneKlines(d.dks), d.mks)...)
			}
			variantTrades[vi] = ts
		}

		mu.Lock()
		for vi, ts := range variantTrades {
			merged[vi] = append(merged[vi], ts...)
		}
		mu.Unlock()
	})
```

（`select { case <-stop: return nil, errStopped ... }` 及后续报告汇总保持原样；变体循环里的 mu 语义与原代码一致——fn 内每次回调锁定合并。）

- [ ] **Step 3: 验证（编译 + 全量回归）**

```powershell
go build ./internal/lab/; go test ./internal/lab/ -count=1
```

- [ ] **Step 4: 提交**

```powershell
git add internal/lab/runner.go; git commit -m "refactor(lab): 抽取 forEachCodeData 数据加载（为因子快照填充与 IC 分析复用）"
```

---

### Task 9: TopN 快照需求收集与横截面填充（TDD）

**Files:**
- Create: `internal/lab/walk.go`
- Create: `internal/lab/matrix.go`
- Test: `internal/lab/walk_test.go`
- Test: `internal/lab/matrix_test.go`（`writeDayDBCloses`/`fnFac` 助手 Task 11 复用）

设计要点：

- `collectTopN` 递归展开 `core.CompositeBuyer`（与 [diagnose.go](file:///c:/ssd/strategy-tail/core/diagnose.go#L54-L59) 同款）；Yaegi 脚本复合字面量可能退化为值类型，故 `*sb.A因子TopN` 与 `sb.A因子TopN` 双断言；`Factor==nil` / `N<=0` 跳过；同 key（因子+方向）去重保序。
- `fillCrossSection` 前缀序列镜像回测：`series=his+dks`，第 i 天前缀 `series[:len(his)+i+1]` 与引擎 `Do()` 的 `ls := full[:len(his)+i+1]` 同口径（无前视）；NaN 跳过；并列值按代码字典序破平；`Asc=true` 名次 1=最小值。worker 本票 local map 累积 + `sync.Mutex` 锁内合并（镜像 `run()` 的 merged 模式——共享 map 并发写会 race）。快照仅进程内存（约 100MB/因子/年），任务结束由调用方 `ClearCrossSection`。
- `fillCrossSection` 的单测走真实加载路径（伪造日K sqlite + `forEachCodeData`），Task 11 的 e2e 再覆盖 `run()` 集成。

- [ ] **Step 1: 写失败测试**

创建 `internal/lab/walk_test.go`：

```go
package lab

import (
	"testing"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
)

// fakeFac 测试用常数因子。
type fakeFac struct{ name string }

func (f fakeFac) Name() string                        { return f.name }
func (f fakeFac) Value(string, extend.Klines) float64 { return 0 }

// fakeComposite 测试用组合买家（core.CompositeBuyer）。
type fakeComposite struct{ kids []core.Buyer }

func (fakeComposite) Name() string                   { return "组合" }
func (fakeComposite) Buy(string, extend.Klines) bool { return false }
func (f fakeComposite) Children() []core.Buyer       { return f.kids }

// fakePlain 非 TopN 的普通买家。
type fakePlain struct{}

func (fakePlain) Name() string                   { return "plain" }
func (fakePlain) Buy(string, extend.Klines) bool { return false }

// TestCollectTopN 递归收集：值/指针双断言、去重保序、无效项跳过。
func TestCollectTopN(t *testing.T) {
	fa := fakeFac{name: "动量"}
	variants := []core.Variant{
		// 指针型（&字面量路径）
		{Name: "a", Buyer: &sb.A因子TopN{Factor: fa, N: 5, Asc: false}},
		// 值型（脚本复合字面量退化路径）：同 key 去重；asc 方向保留
		{Name: "b", Buyer: fakeComposite{kids: []core.Buyer{
			sb.A因子TopN{Factor: fa, N: 3, Asc: false},
			sb.A因子TopN{Factor: fa, N: 1, Asc: true},
		}}},
		// 无效项跳过、非 TopN 买家忽略
		{Name: "c", Buyer: fakeComposite{kids: []core.Buyer{
			sb.A因子TopN{N: 1},
			sb.A因子TopN{Factor: fa, N: 0, Asc: true},
			fakePlain{},
		}}},
	}

	reqs := collectTopN(variants)
	if len(reqs) != 2 {
		t.Fatalf("去重后应剩 2 个请求，得 %d: %+v", len(reqs), reqs)
	}
	if reqs[0].key != core.TopNKey("动量", false) || reqs[0].asc {
		t.Fatalf("reqs[0] 应为 动量|desc: %+v", reqs[0])
	}
	if reqs[1].key != core.TopNKey("动量", true) || !reqs[1].asc {
		t.Fatalf("reqs[1] 应为 动量|asc: %+v", reqs[1])
	}
}
```

创建 `internal/lab/matrix_test.go`：

```go
package lab

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/lib/xorms"
	"github.com/injoyai/tdx/protocol"
)

// writeDayDBCloses 按收盘价序列写伪造日K sqlite（表结构同 server_test.go
// writeFakeDayDB；区别：每日收盘可指定，构造有方向的因子序列）。
func writeDayDBCloses(t *testing.T, dir, code string, closes []float64, base time.Time) {
	t.Helper()
	db, err := xorms.NewSqlite(filepath.Join(dir, extend.DirDay, code+".db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Sync2(new(extend.Kline)); err != nil {
		t.Fatal(err)
	}
	rows := make([]*extend.Kline, 0, len(closes))
	for i, c := range closes {
		tm := base.AddDate(0, 0, i)
		rows = append(rows, &extend.Kline{
			Unix: tm.Unix(),
			Kline: &protocol.Kline{
				Time: tm, Open: protocol.Yuan(c), Close: protocol.Yuan(c),
				High: protocol.Yuan(c), Low: protocol.Yuan(c), Volume: 10000,
			},
		})
	}
	if _, err := db.Insert(rows); err != nil {
		t.Fatal(err)
	}
}

// fnFac 闭包因子：动量 = 末收盘/前第 2 根收盘 − 1，不足 3 根返回 NaN。
type fnFac struct{ name string }

func (f fnFac) Name() string { return f.name }
func (f fnFac) Value(_ string, dks extend.Klines) float64 {
	if len(dks) < 3 {
		return math.NaN()
	}
	return dks[len(dks)-1].Close/dks[len(dks)-3].Close - 1
}

// TestFillCrossSection 走伪造日K DB + forEachCodeData 真实路径：
// 升票第一、并列票按代码字典序破平、降票垫底。
func TestFillCrossSection(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })

	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)
	writeDayDBCloses(t, dir, "sh600001", []float64{10, 11, 12}, base)
	writeDayDBCloses(t, dir, "sh600002", []float64{20, 18, 16}, base)
	writeDayDBCloses(t, dir, "sh600003", []float64{10, 11, 12}, base) // 与 001 并列

	fa := fnFac{name: "动量"}
	reqs := []topNReq{{factor: fa, key: core.TopNKey(fa.Name(), false), asc: false}}

	fillCrossSection(&Runner{}, []string{"sh600001", "sh600002", "sh600003"}, []int{2025}, reqs, make(chan struct{}))
	defer core.ClearCrossSection()

	day := core.DayOf(base.AddDate(0, 0, 2))
	if got := core.CrossSectionRank(day, reqs[0].key, "sh600001"); got != 1 {
		t.Fatalf("sh600001 名次 = %d (期望 1)", got)
	}
	if got := core.CrossSectionRank(day, reqs[0].key, "sh600003"); got != 2 {
		t.Fatalf("sh600003 并列破平名次 = %d (期望 2)", got)
	}
	if got := core.CrossSectionRank(day, reqs[0].key, "sh600002"); got != 3 {
		t.Fatalf("sh600002 名次 = %d (期望 3)", got)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```powershell
go test ./internal/lab/ -run "TestCollectTopN|TestFillCrossSection" -count=1
```

预期：编译错误（`collectTopN`/`fillCrossSection`/`topNReq` 未定义）。

- [ ] **Step 3: 实现**

创建 `internal/lab/walk.go`：

```go
package lab

import (
	"github.com/injoyai/strategy-tail/core"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
)

// topNReq 一次横截面快照填充请求。asc 与 A因子TopN.Asc 一致，
// 排名时直接使用，无需解析 key 后缀。
type topNReq struct {
	factor core.Factor
	key    string
	asc    bool
}

// collectTopN 递归遍历全部变体的买入策略树（CompositeBuyer 展开，与
// core diagnose 同款），收集 A因子TopN 所需的快照请求。
//
// Yaegi 脚本复合字面量可能退化为值类型，故 *A因子TopN 与 A因子TopN
// 双断言。Factor==nil 或 N<=0 永不触发买入，跳过。同一 key（因子+方向）
// 的值与排名完全一致，去重后只算一次；保序返回。
func collectTopN(variants []core.Variant) []topNReq {
	seen := map[string]bool{}
	var reqs []topNReq
	var walk func(b core.Buyer)
	walk = func(b core.Buyer) {
		switch t := b.(type) {
		case *sb.A因子TopN:
			if t.Factor != nil && t.N > 0 {
				key := core.TopNKey(t.Factor.Name(), t.Asc)
				if !seen[key] {
					seen[key] = true
					reqs = append(reqs, topNReq{factor: t.Factor, key: key, asc: t.Asc})
				}
			}
		case sb.A因子TopN:
			walk(&t)
		case core.CompositeBuyer:
			for _, child := range t.Children() {
				walk(child)
			}
		}
	}
	for _, v := range variants {
		walk(v.Buyer)
	}
	return reqs
}
```

创建 `internal/lab/matrix.go`：

```go
package lab

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// matrix.go 横截面快照填充：A因子TopN 的名次上下文预计算。
//
// 数据口径与回测引擎逐日对齐：单票多年 series=his+dks，第 i 天的因子
// 前缀 series[:len(his)+i+1] 与引擎 Do() 的 ls := full[:len(his)+i+1]
// 完全一致（无前视）。NaN 视为无效跳过（不参与排名）；并列值按代码
// 字典序破平，名次确定可复现。快照仅进程内存（约 100MB/因子/年），
// 任务结束由调用方 ClearCrossSection（Task 11）。

// fillCrossSection worker 池逐票计算因子值——本票 local map 累积、锁内
// 合并（共享 map 并发写会 race，镜像 run() 的 merged 模式）——结束后对
// 每个 (key, day) 排名写入 core.SetCrossSection。
func fillCrossSection(r *Runner, codes []string, years []int, reqs []topNReq, stop chan struct{}) {
	var (
		mu     sync.Mutex
		merged []map[string]map[time.Time]map[string]float64 // key → day → code → 值
	)

	r.forEachCodeData(codes, years, stop, false, func(code string, datas []yearData) {
		vals := map[string]map[time.Time]map[string]float64{}
		for _, d := range datas {
			series := make(extend.Klines, 0, len(d.his)+len(d.dks))
			series = append(series, d.his...)
			series = append(series, d.dks...)
			base := len(d.his)
			for i := range d.dks {
				prefix := series[:base+i+1]
				day := core.DayOf(d.dks[i].Time)
				for _, req := range reqs {
					v := req.factor.Value(code, prefix)
					if math.IsNaN(v) {
						continue
					}
					byDay := vals[req.key]
					if byDay == nil {
						byDay = map[time.Time]map[string]float64{}
						vals[req.key] = byDay
					}
					if byDay[day] == nil {
						byDay[day] = map[string]float64{}
					}
					byDay[day][code] = v
				}
			}
		}
		mu.Lock()
		merged = append(merged, vals)
		mu.Unlock()
	})

	// 汇总排名（forEachCodeData 返回后单协程执行，无竞争）
	for _, req := range reqs {
		byDay := map[time.Time]map[string]float64{}
		for _, m := range merged {
			for day, cv := range m[req.key] {
				if byDay[day] == nil {
					byDay[day] = map[string]float64{}
				}
				for c, v := range cv {
					byDay[day][c] = v
				}
			}
		}
		for day, cv := range byDay {
			type pair struct {
				val  float64
				code string
			}
			ps := make([]pair, 0, len(cv))
			for c, v := range cv {
				ps = append(ps, pair{val: v, code: c})
			}
			sort.Slice(ps, func(i, j int) bool {
				if ps[i].val != ps[j].val {
					if req.asc {
						return ps[i].val < ps[j].val
					}
					return ps[i].val > ps[j].val
				}
				return ps[i].code < ps[j].code
			})
			ranked := make([]string, len(ps))
			for i, p := range ps {
				ranked[i] = p.code
			}
			core.SetCrossSection(day, req.key, ranked)
		}
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

```powershell
go test ./internal/lab/ -count=1
```

- [ ] **Step 5: 提交**

```powershell
git add internal/lab/walk.go internal/lab/matrix.go internal/lab/walk_test.go internal/lab/matrix_test.go; git commit -m "feat(lab): TopN 快照需求收集与横截面填充（前缀序列镜像回测）"
```

---

### Task 10: Yaegi 符号注册——14 因子 + 因子策略（TDD）

**Files:**
- Modify: `internal/lab/symbols.go`
- Test: `internal/lab/factor_script_test.go`

设计要点：

- buy 段补注册 `A因子过滤`/`A因子TopN`；新增 factor 段：14 类因子类型 + `Build` 工厂（import 别名 f，Yaegi key `"github.com/injoyai/strategy-tail/strategies/factor/factor"`）。
- 因子方法全部为值接收者（Task 2-4 落盘），脚本内值字面量 `f.N日动量{Days: 5}` 即可实现 `core.Factor`，无需取址。
- 探针走 `LoadScript` 全链（stdlib + ProjectSymbols → interp → extractVariants）：脚本加载成功即证明全部符号可解析；行为断言用内存 K 线（不依赖 DB/网络）。第二变体经 `f.Build("momentum", 5)` + `A因子TopN` 构造，覆盖工厂与 TopN 注册（无快照恒 false 的契约由 Task 7 单测覆盖，此处仅断言不崩）。

- [ ] **Step 1: 写失败测试**

创建 `internal/lab/factor_script_test.go`：

```go
package lab

import (
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/lib/extend"
	"github.com/injoyai/tdx/protocol"
)

// factorScript 探针脚本：值字面量因子 + A因子过滤 行为断言；
// Build 工厂 + A因子TopN 构造（覆盖另两处注册）。
const factorScript = `package main

import (
	"github.com/injoyai/strategy-tail/core"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
)

func Strategy() []core.Variant {
	return []core.Variant{
		{Name: "动量过滤", Buyer: sb.A因子过滤{Factor: f.N日动量{Days: 5}, Min: 0, Max: 1}},
		{Name: "动量TopN", Buyer: sb.A因子TopN{Factor: f.Build("momentum", 5), N: 3, Asc: false}},
	}
}
`

// closeKs 构造 o=h=l=c 的内存日K序列（量 10000）。
func closeKs(base time.Time, closes ...float64) extend.Klines {
	ks := make(extend.Klines, 0, len(closes))
	for i, c := range closes {
		tm := base.AddDate(0, 0, i)
		ks = append(ks, &extend.Kline{
			Unix: tm.Unix(),
			Kline: &protocol.Kline{
				Time: tm, Open: protocol.Yuan(c), Close: protocol.Yuan(c),
				High: protocol.Yuan(c), Low: protocol.Yuan(c), Volume: 10000,
			},
		})
	}
	return ks
}

// TestFactorScript Yaegi 因子探针：注册缺失时 LoadScript 报 undefined。
func TestFactorScript(t *testing.T) {
	variants, err := LoadScript(factorScript)
	if err != nil {
		t.Fatalf("脚本加载失败（符号注册缺失或类型不可用）: %v", err)
	}
	if len(variants) != 2 {
		t.Fatalf("变体数 = %d (期望 2)", len(variants))
	}

	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local)

	// 上行 6 根：动量(5) = 12.5/10 − 1 = 0.25 ∈ [0,1] → 买入
	if !variants[0].Buyer.Buy("sh600001", closeKs(base, 10, 10.5, 11, 11.5, 12, 12.5)) {
		t.Fatal("动量 0.25 应在 [0,1] 区间买入")
	}

	// 3 根：动量(5) 数据不足 NaN → 不买
	if variants[0].Buyer.Buy("sh600001", closeKs(base, 10, 10.5, 11)) {
		t.Fatal("数据不足（NaN）不应买入")
	}

	// TopN 无快照恒 false（只验证脚本构造的 TopN 可调用、不崩）
	if variants[1].Buyer.Buy("sh600001", closeKs(base, 10, 10.5, 11, 11.5, 12, 12.5)) {
		t.Fatal("无快照 TopN 不应买入")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```powershell
go test ./internal/lab/ -run TestFactorScript -count=1
```

预期：脚本加载报错（`A因子过滤`/`A因子TopN`/`N日动量`/`Build` 未注册，Yaegi undefined）。

- [ ] **Step 3: 实现**

替换 `internal/lab/symbols.go` 全文（buy 段追加因子策略、新增 factor 段）：

```go
package lab

import (
	"reflect"

	"github.com/injoyai/strategy-tail/core"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
	ss "github.com/injoyai/strategy-tail/strategies/sell"
	"github.com/traefik/yaegi/interp"
)

// symbols.go 项目包符号表：供策略脚本 import 项目现有组件。
//
// Yaegi binary 包机制：(*T)(nil) 注册类型符号、reflect.ValueOf(fn) 注册函数符号。
// 脚本内 `import "github.com/injoyai/strategy-tail/strategies/buy"` 会映射到
// key "github.com/injoyai/strategy-tail/strategies/buy/buy"（包路径 + 包名后缀）。
//
// 首批只注册回测脚本常用类型（探针已验证 A阴线收回/And/MAUp 可用）；
// 后续按需追加，踩雷类型记入 MEMORY.md。

// ProjectSymbols 项目包符号表（供脚本 import）。
func ProjectSymbols() interp.Exports {
	return interp.Exports{
		"github.com/injoyai/strategy-tail/core/core": {
			// 契约类型
			"Variant": reflect.ValueOf((*core.Variant)(nil)),
		},
		"github.com/injoyai/strategy-tail/strategies/buy/buy": {
			// 组合子
			"And": reflect.ValueOf((*sb.And)(nil)),
			"Or":  reflect.ValueOf((*sb.Or)(nil)),
			"Not": reflect.ValueOf(sb.Not),
			// 形态/过滤
			"A阴线收回": reflect.ValueOf((*sb.A阴线收回)(nil)),
			"A价格":   reflect.ValueOf((*sb.A价格)(nil)),
			"A流通市值": reflect.ValueOf((*sb.A流通市值)(nil)),
			"A过滤涨停": reflect.ValueOf((*sb.A过滤涨停)(nil)),
			// 因子策略（Task 6/7）
			"A因子过滤": reflect.ValueOf((*sb.A因子过滤)(nil)),
			"A因子TopN": reflect.ValueOf((*sb.A因子TopN)(nil)),
			// 趋势
			"MAUp":        reflect.ValueOf((*sb.MAUp)(nil)),
			"A均线多头排列": reflect.ValueOf((*sb.A均线多头排列)(nil)),
		},
		"github.com/injoyai/strategy-tail/strategies/sell/sell": {
			// 组合子
			"And": reflect.ValueOf((*ss.And)(nil)),
			"Or":  reflect.ValueOf((*ss.Or)(nil)),
			// 规则
			"A持仓N天": reflect.ValueOf((*ss.A持仓N天)(nil)),
			"A止盈止损": reflect.ValueOf((*ss.A止盈止损)(nil)),
		},
		"github.com/injoyai/strategy-tail/strategies/factor/factor": {
			// 14 类因子：值接收者方法集，脚本内值字面量即可实现 core.Factor
			"N日动量":  reflect.ValueOf((*f.N日动量)(nil)),
			"均线偏离":  reflect.ValueOf((*f.均线偏离)(nil)),
			"N日斜率":  reflect.ValueOf((*f.N日斜率)(nil)),
			"N日波动":  reflect.ValueOf((*f.N日波动)(nil)),
			"N日振幅":  reflect.ValueOf((*f.N日振幅)(nil)),
			"量比":     reflect.ValueOf((*f.量比)(nil)),
			"量分位":    reflect.ValueOf((*f.量分位)(nil)),
			"放量占比":   reflect.ValueOf((*f.放量占比)(nil)),
			"实体幅度":   reflect.ValueOf((*f.实体幅度)(nil)),
			"上影占比":   reflect.ValueOf((*f.上影占比)(nil)),
			"下影占比":   reflect.ValueOf((*f.下影占比)(nil)),
			"N日高低位":  reflect.ValueOf((*f.N日高低位)(nil)),
			"K值":      reflect.ValueOf((*f.K值)(nil)),
			"量价相关":    reflect.ValueOf((*f.量价相关)(nil)),
			// 工厂：未知 kind 返回 nil；days<=0 用默认参数
			"Build": reflect.ValueOf(f.Build),
		},
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

```powershell
go test ./internal/lab/ -run TestFactorScript -count=1
```

- [ ] **Step 5: 提交**

```powershell
git add internal/lab/symbols.go internal/lab/factor_script_test.go; git commit -m "feat(lab): 注册 Yaegi 因子符号（14 因子类型 + Build + A因子过滤/A因子TopN）"
```

---

### Task 11: run() 集成快照填充 + TopN 端到端（TDD）

**Files:**
- Modify: `internal/lab/runner.go`
- Test: `internal/lab/factor_e2e_test.go`

设计要点：

- 插入点：`run()` 中 `years := cfg.years()` 之后、`seller := cfg.seller()` 之前。`collectTopN(variants)` 无 TopN 请求时零开销跳过（普通策略路径不受影响）；有请求时先 `fillCrossSection`（复用 forEachCodeData worker 池，进度经 doneCodes/totalCodes 上报），完成后 stop 检查（填充中停止 → `errStopped`）、`defer core.ClearCrossSection()`（任务结束清理快照）、`doneCodes` 归零（回测进度从头计）。
- e2e 数据不依赖并列破平：升票每日动量恒为正、降票恒为负，每日名次严格有序；复用 matrix_test.go 的 `writeDayDBCloses`（2024 年 8 根垫底保证 his 非空）。走 server 生命周期（`NewServer().Handler()` + doReq + 轮询 done，模板同 TestServerRunLifecycle），断言交易全部来自 sh600001 且任务结束后快照已清理（`CrossSectionRank == 0`）。

- [ ] **Step 1: 写失败测试**

创建 `internal/lab/factor_e2e_test.go`：

```go
package lab

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	common "github.com/injoyai/strategy-tail"
	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
)

// topNScript TopN 买入脚本：动量(2) 降序第 1 名，每日只买横截面最强一票。
const topNScript = `package main

import (
	"github.com/injoyai/strategy-tail/core"
	f "github.com/injoyai/strategy-tail/strategies/factor"
	sb "github.com/injoyai/strategy-tail/strategies/buy"
)

func Strategy() []core.Variant {
	return []core.Variant{
		{Name: "动量TopN1", Buyer: sb.A因子TopN{Factor: f.N日动量{Days: 2}, N: 1, Asc: false}},
	}
}
`

// TestServerTopNRun 端到端：回测前填充快照 → 仅买 TopN 票 → 任务结束清理。
func TestServerTopNRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })

	// 2024 年 8 根垫底（his 非空）+ 2025 年 8 根：
	// 001 每日动量(2) 恒为正、002 恒为负 → 001 每天都是降序第 1 名（无并列）
	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	writeDayDBCloses(t, dir, "sh600001", []float64{
		9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5,
		10, 10.2, 10.4, 10.6, 10.8, 11, 11.2, 11.4,
	}, base)
	writeDayDBCloses(t, dir, "sh600002", []float64{
		21, 21, 21, 21, 21, 21, 21, 21,
		20, 19.8, 19.6, 19.4, 19.2, 19, 18.8, 18.6,
	}, base)

	if err := os.MkdirAll(filepath.Dir(ScriptPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ScriptPath, []byte(topNScript), 0644); err != nil {
		t.Fatal(err)
	}

	h := NewServer().Handler()

	cfg := map[string]any{
		"startYear": 2025, "endYear": 2025,
		"sampleMode": "codes", "sampleCodes": []string{"sh600001", "sh600002"},
		"holdingDays": 1,
	}
	res := doReq(t, h, http.MethodPost, "/api/run", cfg, http.StatusOK)
	if !bytes.Contains(res, []byte(`"ok":true`)) {
		t.Fatalf("run 响应异常: %s", res)
	}

	// 轮询至完成（模板同 TestServerRunLifecycle）
	deadline := time.Now().Add(30 * time.Second)
	var st map[string]any
	for {
		res = doReq(t, h, http.MethodGet, "/api/status", nil, http.StatusOK)
		st = nil
		if err := json.Unmarshal(res, &st); err != nil {
			t.Fatal(err)
		}
		if st["state"] == "done" || st["state"] == "error" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("回测超时未完成: %v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st["state"] != "done" {
		t.Fatalf("回测失败: %v", st)
	}
	if n := st["totalCodes"].(float64); n != 2 {
		t.Fatalf("totalCodes=%v", n)
	}

	// 交易只应来自横截面第 1 名 sh600001
	res = doReq(t, h, http.MethodGet, "/api/report/latest", nil, http.StatusOK)
	var rep Report
	if err := json.Unmarshal(res, &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Variants) != 1 {
		t.Fatalf("变体数 = %d", len(rep.Variants))
	}
	if len(rep.Variants[0].Trades) == 0 {
		t.Fatal("应产生交易")
	}
	for _, tr := range rep.Variants[0].Trades {
		if tr.Code != "sh600001" {
			t.Fatalf("非 TopN 票被买入: %+v", tr)
		}
	}

	// 任务结束快照已清理
	day := core.DayOf(base.AddDate(0, 0, 15))
	key := core.TopNKey("N日动量(2)", false)
	if got := core.CrossSectionRank(day, key, "sh600001"); got != 0 {
		t.Fatalf("快照未清理: 名次 = %d", got)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```powershell
go test ./internal/lab/ -run TestServerTopNRun -count=1
```

预期：run() 尚未填充快照，TopN 恒 false → 交易数为 0，`应产生交易` 失败。

- [ ] **Step 3: 实现**

在 `internal/lab/runner.go` 的 `run()` 中，`years := cfg.years()` 之后、`seller := cfg.seller()` 之前插入：

```go
	years := cfg.years()

	// TopN 横截面快照：回测前统一填充（无快照时 A因子TopN 恒 false）。
	// 快照仅本进程内存，任务结束清理；填充进度复用 doneCodes 上报，回测前归零。
	if reqs := collectTopN(variants); len(reqs) > 0 {
		fillCrossSection(r, codes, years, reqs, stop)
		select {
		case <-stop:
			return nil, errStopped
		default:
		}
		defer core.ClearCrossSection()
		r.doneCodes.Store(0)
	}

	seller := cfg.seller()
```

- [ ] **Step 4: 运行测试确认通过**

```powershell
go test ./internal/lab/ -run TestServerTopNRun -count=1
```

- [ ] **Step 5: 提交**

```powershell
git add internal/lab/runner.go internal/lab/factor_e2e_test.go; git commit -m "feat(lab): 回测前填充 TopN 横截面快照（e2e 覆盖）"
```

---

### Task 12: IC/分位统计纯函数（TDD）

**Files:**
- Modify: `internal/lab/analysis.go`（新建，本任务只放统计层）
- Test: `internal/lab/analysis_test.go`

设计要点：

- 纯函数无 IO，Task 13 的编排与导出追加在同一文件。单期 IC 用 Spearman 秩相关（`avgRanks` 并列取均值 + 复用 Task 4 导出的 `factor.Pearson`）；分位按因子值升序等频五分位（Q1=因子最低 20%，`k*5/n` 均匀切分）；`icStats` 汇总逐期 IC（剔除 NaN、总体标准差、`TStat=Mean/(Std/√n)`，有效样本 < `minPairs=10` 全 0）。
- 名次方向约定：`avgRanks` 升序（值最小名次 1）。Spearman 对两个序列用同一方向约定时符号不受影响，IC 符号只由因子-收益单调性决定。

- [ ] **Step 1: 写失败测试**

创建 `internal/lab/analysis_test.go`：

```go
package lab

import (
	"math"
	"testing"
)

func nearlyEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestAvgRanks 升序名次、并列取均值。
func TestAvgRanks(t *testing.T) {
	got := avgRanks([]float64{1, 2, 2, 3})
	want := []float64{1, 2.5, 2.5, 4}
	for i := range want {
		if !nearlyEq(got[i], want[i]) {
			t.Fatalf("avgRanks[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestSpearmanIC 完全单调 ±1、并列衰减、弱相关确定性小值。
func TestSpearmanIC(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5}
	if ic := spearmanIC(vals, []float64{0.1, 0.2, 0.3, 0.4, 0.5}); !nearlyEq(ic, 1) {
		t.Fatalf("同向 IC = %v, want 1", ic)
	}
	if ic := spearmanIC(vals, []float64{0.5, 0.4, 0.3, 0.2, 0.1}); !nearlyEq(ic, -1) {
		t.Fatalf("反向 IC = %v, want -1", ic)
	}
	// 并列：因子 [1,2,2,3] 对 [0.1,0.3,0.2,0.4] 的秩相关 = 0.9
	if ic := spearmanIC([]float64{1, 2, 2, 3}, []float64{0.1, 0.3, 0.2, 0.4}); !nearlyEq(ic, 0.9) {
		t.Fatalf("并列 IC = %v, want 0.9", ic)
	}
	// 弱相关（排名 [3,5,1,2,4]）：手算 Pearson = -0.1
	if ic := spearmanIC([]float64{3, 5, 1, 2, 4}, vals); math.Abs(ic+0.1) > 1e-9 {
		t.Fatalf("弱相关 IC = %v, want -0.1", ic)
	}
}

// TestQuintileMeans 按因子值升序等频五分位，收益严格单调。
func TestQuintileMeans(t *testing.T) {
	vals := make([]float64, 20)
	rets := make([]float64, 20)
	for i := range vals {
		vals[i] = float64(i + 1)
		rets[i] = 0.01 * float64(i+1)
	}
	qs := quintileMeans(vals, rets)
	if len(qs) != 5 {
		t.Fatalf("组数 = %d", len(qs))
	}
	for i := 1; i < 5; i++ {
		if qs[i] <= qs[i-1] {
			t.Fatalf("分位收益应严格递增: %v", qs)
		}
	}
	// n=20 等频每组 4 个：Q1=(0.01+0.02+0.03+0.04)/4=0.025，Q5=0.185
	if !nearlyEq(qs[0], 0.025) || !nearlyEq(qs[4], 0.185) {
		t.Fatalf("Q1/Q5 = %v/%v, want 0.025/0.185", qs[0], qs[4])
	}
	if quintileMeans([]float64{1, 2, 3, 4}, []float64{0, 0, 0, 0}) != nil {
		t.Fatal("n<5 应返回 nil")
	}
}

// TestICStats 均值/总体标准差/t 统计量；样本不足全 0；NaN 剔除。
func TestICStats(t *testing.T) {
	ics := make([]float64, 10)
	for i := range ics {
		if i%2 == 0 {
			ics[i] = 0.1
		} else {
			ics[i] = 0.3
		}
	}
	s := icStats(ics)
	if s.Pairs != 10 || !nearlyEq(s.Mean, 0.2) || !nearlyEq(s.Std, 0.1) {
		t.Fatalf("icStats = %+v", s)
	}
	// t = 0.2/(0.1/√10) = 2√10
	if math.Abs(s.TStat-2*math.Sqrt(10)) > 1e-9 {
		t.Fatalf("TStat = %v", s.TStat)
	}

	// 有效样本 9 < minPairs → 全 0
	short := append([]float64{0.5}, make([]float64, 8)...)
	if s = icStats(short); s.Pairs != 9 || s.Mean != 0 || s.Std != 0 || s.TStat != 0 {
		t.Fatalf("样本不足应全 0: %+v", s)
	}

	// NaN 剔除后仍达 minPairs
	s = icStats([]float64{0.1, 0.3, math.NaN(), 0.1, 0.3, 0.1, 0.3, 0.1, 0.3, 0.1, 0.3})
	if s.Pairs != 10 || !nearlyEq(s.Mean, 0.2) {
		t.Fatalf("NaN 未剔除: %+v", s)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```powershell
go test ./internal/lab/ -run "TestAvgRanks|TestSpearmanIC|TestQuintileMeans|TestICStats" -count=1
```

预期：编译错误（`avgRanks`/`spearmanIC`/`quintileMeans`/`icStats`/`ICStats` 未定义）。

- [ ] **Step 3: 实现**

创建 `internal/lab/analysis.go`（本任务只含统计层）：

```go
package lab

import (
	"math"
	"sort"

	f "github.com/injoyai/strategy-tail/strategies/factor"
)

// analysis.go 因子研究：统计层（Task 12）与编排导出（Task 13）。
//
// 统计全部纯函数无 IO。单期 IC 用 Spearman 秩相关——对量纲不敏感，
// 只关心截面单调性；样本不足 minPairs 时汇总归零（无推断意义）。

// minPairs IC 汇总的最小有效样本数。
const minPairs = 10

// ICStats 一组逐期 IC 的汇总统计。
type ICStats struct {
	Pairs int     `json:"pairs"` // 有效样本数
	Mean  float64 `json:"mean"`  // 均值
	Std   float64 `json:"std"`   // 总体标准差
	TStat float64 `json:"tStat"` // Mean/(Std/√n)；Std=0 时 0
}

// avgRanks 升序平均名次（值最小名次 1，并列取均值），Spearman 基础。
func avgRanks(xs []float64) []float64 {
	n := len(xs)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(i, j int) bool { return xs[idx[i]] < xs[idx[j]] })
	ranks := make([]float64, n)
	for i := 0; i < n; {
		j := i
		for j+1 < n && xs[idx[j+1]] == xs[idx[i]] {
			j++
		}
		avg := float64(i+j+2) / 2
		for k := i; k <= j; k++ {
			ranks[idx[k]] = avg
		}
		i = j + 1
	}
	return ranks
}

// spearmanIC 单期 IC：因子值名次与收益名次的 Pearson 相关。
// NaN 由调用方剔除（Pearson 遇 NaN 结果不可用）。
func spearmanIC(vals, rets []float64) float64 {
	return f.Pearson(avgRanks(vals), avgRanks(rets))
}

// quintileMeans 按因子值升序等频五分位（Q1=因子最低 20%），返回各组
// 收益均值；n<5 返回 nil。
func quintileMeans(vals, rets []float64) []float64 {
	n := len(vals)
	if n < 5 {
		return nil
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(i, j int) bool { return vals[idx[i]] < vals[idx[j]] })
	sums := make([]float64, 5)
	cnts := make([]int, 5)
	for k, i := range idx {
		q := k * 5 / n
		sums[q] += rets[i]
		cnts[q]++
	}
	out := make([]float64, 5)
	for q := range out {
		out[q] = sums[q] / float64(cnts[q])
	}
	return out
}

// icStats 汇总逐期 IC：剔除 NaN，有效样本 < minPairs 时全 0。
func icStats(ics []float64) ICStats {
	valid := make([]float64, 0, len(ics))
	for _, v := range ics {
		if !math.IsNaN(v) {
			valid = append(valid, v)
		}
	}
	var s ICStats
	s.Pairs = len(valid)
	if s.Pairs < minPairs {
		return s
	}
	sum := 0.0
	for _, v := range valid {
		sum += v
	}
	mean := sum / float64(s.Pairs)
	ss := 0.0
	for _, v := range valid {
		ss += (v - mean) * (v - mean)
	}
	s.Mean = mean
	s.Std = math.Sqrt(ss / float64(s.Pairs))
	if s.Std > 0 {
		s.TStat = s.Mean / (s.Std / math.Sqrt(float64(s.Pairs)))
	}
	return s
}
```

- [ ] **Step 4: 运行测试确认通过**

```powershell
go test ./internal/lab/ -run "TestAvgRanks|TestSpearmanIC|TestQuintileMeans|TestICStats" -count=1
```

- [ ] **Step 5: 提交**

```powershell
git add internal/lab/analysis.go internal/lab/analysis_test.go; git commit -m "feat(lab): IC/分位统计纯函数（Spearman 秩相关 + 等频五分位）"
```

---

### Task 13: 因子分析编排（AnalyzeConfig + runAnalysis + exportAnalysis）（TDD）

**Files:**
- Modify: `internal/lab/runner.go`（Validate 拆分纯重构 + Runner 字段 + StartAnalysis）
- Modify: `internal/lab/analysis.go`（追加编排与导出层）
- Create: `internal/lab/analysis_run_test.go`

**设计要点：**

- `AnalyzeConfig` 内嵌 `RunConfig`：复用年份/样本解析（`resolveCodes`/`years`）；卖出规则与分析无关，**不**做卖出检查。
- `Runner.Validate` 拆出 `validateYears`/`validateSample` 纯重构（行为不变），供回测与分析共用；`AnalyzeConfig.Validate` 复用二者 + 窗口/因子校验。放 analysis.go（需 `f.Build`，避免 runner.go 引入 strategies/factor 依赖）。
- 口径与 [fillCrossSection](#) 完全一致：`series=his+dks`，第 i 日前缀 `series[:len(his)+i+1]`（无前视）；未来收益 `dks[i+Window].Close/dks[i].Close−1`，尾部 Window 日无未来收益剔除（`i+Window < len(dks)`）。
- `DailyIC.IC` 用 `*float64`：`json.Marshal` 遇 NaN 直接报错，无效日序列化为 `null`。
- `Quintiles` 为逐日五分位收益均值的再平均；若没有任何一天票数 ≥5（`qCnts[0]==0`）保持 nil（避免 0 除产生 NaN 炸 JSON）。
- 产物 `output/factor/<TradesExportName(kind)>/`：report.json（tmp+rename 原子写，镜像 saveReport）+ ic.csv + report.html（后两者 best-effort）。
- `StartAnalysis` 与 `Start` 同构：共用 `mu.TryLock`（回测/分析互斥）、`running`（复用 Stop 路径）、状态机 idle/running/error/done；成功后 `lastAnalysis.Store`。
- 停止语义镜像 `run()`：`forEachCodeData` 内部跳过排队票，返回后 `select stop → errStopped`。

- [ ] **Step 1: 写失败测试**

新建 `internal/lab/analysis_run_test.go`（复用 [matrix_test.go](#) 的 `writeDayDBCloses` 与 `nearlyEq`）：

```go
package lab

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/strategy-tail/common"
	"github.com/injoyai/strategy-tail/internal/lab/extend"
)

func setupAnalysisData(t *testing.T, dir string) {
	t.Helper()
	t.Chdir(dir)
	old := common.Pull
	common.Pull = &extend.PullKline{Config: extend.PullKlineConfig{Dir: dir}}
	t.Cleanup(func() { common.Pull = old })
}

// TestRunAnalysis 升票动量恒正、降票恒负 → 每日横截面名次恒定 → IC 恒 1。
// 每票 2025 年 20 根日 K（base 起顺序生成），Window=1 → 19 个有效日 ≥ minPairs(10)。
func TestRunAnalysis(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)

	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	up := append([]float64{9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5},
		10, 10.2, 10.4, 10.6, 10.8, 11, 11.2, 11.4, 11.6, 11.8,
		12, 12.2, 12.4, 12.6, 12.8, 13, 13.2, 13.4, 13.6, 13.8)
	down := append([]float64{21, 21, 21, 21, 21, 21, 21, 21},
		20.8, 20.6, 20.4, 20.2, 20, 19.8, 19.6, 19.4, 19.2, 19,
		18.8, 18.6, 18.4, 18.2, 18, 17.8, 17.6, 17.4, 17.2, 17)
	writeDayDBCloses(t, dir, "sh600001", up, base)
	writeDayDBCloses(t, dir, "sh600002", down, base)

	cfg := AnalyzeConfig{
		RunConfig: RunConfig{StartYear: 2025, EndYear: 2025,
			SampleMode: "codes", SampleCodes: []string{"sh600001", "sh600002"},
			ScriptName: "matrix"},
		Kind: "momentum", Days: 2, Window: 1,
	}
	rep, err := (&Runner{}).runAnalysis(cfg, make(chan struct{}))
	if err != nil {
		t.Fatalf("runAnalysis: %v", err)
	}
	if rep.FactorName != "N日动量(2)" {
		t.Fatalf("FactorName = %q", rep.FactorName)
	}
	if len(rep.Daily) != 19 {
		t.Fatalf("len(Daily) = %d, want 19", len(rep.Daily))
	}
	if rep.Stats.Pairs != 19 {
		t.Fatalf("Pairs = %d, want 19", rep.Stats.Pairs)
	}
	if !nearlyEq(rep.Stats.Mean, 1) {
		t.Fatalf("Mean = %v, want 1", rep.Stats.Mean)
	}
	if rep.Stats.Std != 0 {
		t.Fatalf("Std = %v, want 0", rep.Stats.Std)
	}
	if rep.Quintiles != nil {
		t.Fatalf("Quintiles = %v, want nil（每日 2 票不足 5 分位）", rep.Quintiles)
	}
	if _, err := os.Stat(filepath.Join("output", "factor", "momentum", "report.json")); err != nil {
		t.Fatalf("report.json 不存在: %v", err)
	}
}

func TestAnalyzeConfigValidate(t *testing.T) {
	base := AnalyzeConfig{RunConfig: RunConfig{StartYear: 2025, EndYear: 2025,
		SampleMode: "all", ScriptName: "matrix"}, Kind: "momentum", Days: 2, Window: 1}
	if err := base.Validate(); err != nil {
		t.Fatalf("base: %v", err)
	}
	w0 := base
	w0.Window = 0
	if err := w0.Validate(); err == nil {
		t.Fatal("Window=0 应报错")
	}
	badKind := base
	badKind.Kind = "nope"
	if err := badKind.Validate(); err == nil {
		t.Fatal("未知 Kind 应报错")
	}
	empty := base
	empty.SampleMode = "codes"
	empty.SampleCodes = nil
	if err := empty.Validate(); err == nil {
		t.Fatal("codes 空样本应报错")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```powershell
go test ./internal/lab/ -run "TestRunAnalysis|TestAnalyzeConfigValidate" -count=1
```

预期：编译错误（`AnalyzeConfig`/`AnalysisReport`/`runAnalysis` 未定义）。

- [ ] **Step 3: 实现**

**3a.** [runner.go](file:///c:/ssd/strategy-tail/internal/lab/runner.go) `Validate`（L42-70）纯重构拆分——年份段（L44-49）与样本段（L50-62）分别抽为方法，卖出规则两段留在 `Validate`，行为不变：

```go
// Validate 配置合法性检查。
func (c RunConfig) Validate() error {
	if err := c.validateYears(); err != nil {
		return err
	}
	if err := c.validateSample(); err != nil {
		return err
	}
	if c.HoldingDays <= 0 && c.TakeProfit <= 0 && c.StopLoss <= 0 {
		return fmt.Errorf("卖出规则至少启用一项（持仓天数/止盈/止损）")
	}
	if c.HoldingDays < 0 || c.TakeProfit < 0 || c.StopLoss < 0 {
		return fmt.Errorf("卖出参数不能为负")
	}
	return nil
}

// validateYears 年份范围检查（回测/因子分析共用）。
func (c RunConfig) validateYears() error {
	if c.StartYear <= 0 || c.EndYear <= 0 || c.StartYear > c.EndYear {
		return fmt.Errorf("年份范围无效: %d-%d", c.StartYear, c.EndYear)
	}
	if c.EndYear > time.Now().Year() {
		return fmt.Errorf("结束年份 %d 超过当前年份", c.EndYear)
	}
	return nil
}

// validateSample 样本配置检查（回测/因子分析共用）。
func (c RunConfig) validateSample() error {
	switch c.SampleMode {
	case "all":
	case "random":
		if c.SampleSize <= 0 {
			return fmt.Errorf("随机样本数无效: %d", c.SampleSize)
		}
	case "codes":
		if len(c.SampleCodes) == 0 {
			return fmt.Errorf("指定代码样本为空")
		}
	default:
		return fmt.Errorf("样本模式无效: %s", c.SampleMode)
	}
	return nil
}
```

**3b.** Runner 结构体（`runID` 字段后）追加两个字段；`LatestReport()`（L241）之后追加 `StartAnalysis`：

```go
	task         atomic.Pointer[string] // 当前任务类型 backtest/analysis
	lastAnalysis atomic.Pointer[AnalysisReport]
```

```go
// StartAnalysis 启动因子分析（与回测共用 mu 互斥）。
func (r *Runner) StartAnalysis(cfg AnalyzeConfig) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if !r.mu.TryLock() {
		return fmt.Errorf("已有任务在运行")
	}

	stop := make(chan struct{})
	r.stopCh = stop
	r.running.Store(true)
	r.doneCodes.Store(0)
	state := "running"
	r.state.Store(&state)
	task := "analysis"
	r.task.Store(&task)
	rc := cfg.RunConfig
	r.lastRun.Store(&rc)

	go func() {
		defer r.mu.Unlock()
		defer r.running.Store(false)

		rep, err := r.runAnalysis(cfg, stop)
		if err != nil {
			if err == errStopped {
				st := "idle"
				r.state.Store(&st)
				return
			}
			msg := err.Error()
			r.errMsg.Store(&msg)
			st := "error"
			r.state.Store(&st)
			return
		}
		r.lastAnalysis.Store(rep)
		st := "done"
		r.state.Store(&st)
	}()
	return nil
}
```

**3c.** [analysis.go](#) import 替换为（Task 12 只有 math/sort/f）：

```go
import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/goutil/oss/csv"

	"github.com/injoyai/strategy-tail/core"
	"github.com/injoyai/strategy-tail/lib/extend"
	f "github.com/injoyai/strategy-tail/strategies/factor"
)
```

文件末尾追加编排与导出层：

```go
// AnalyzeConfig 因子分析配置：内嵌 RunConfig 复用年份/样本解析；
// 卖出规则与分析无关（不校验）。
type AnalyzeConfig struct {
	RunConfig
	Kind   string `json:"kind"`   // 因子类型（registry kind）
	Days   int    `json:"days"`   // 因子窗口参数（≤0 用各因子默认）
	Window int    `json:"window"` // 未来收益窗口（交易日）
}

// Validate 校验分析配置（复用回测的年份/样本检查）。
func (c AnalyzeConfig) Validate() error {
	if err := c.validateYears(); err != nil {
		return err
	}
	if err := c.validateSample(); err != nil {
		return err
	}
	if c.Window < 1 || c.Window > 60 {
		return fmt.Errorf("未来收益窗口无效: %d（应为 1-60 交易日）", c.Window)
	}
	if f.Build(c.Kind, c.Days) == nil {
		return fmt.Errorf("未知因子类型: %s", c.Kind)
	}
	return nil
}

// AnalysisReport 因子分析报告。
type AnalysisReport struct {
	FactorName string    `json:"factorName"` // 因子中文名（如 N日动量(2)）
	Kind       string    `json:"kind"`
	Window     int       `json:"window"`
	Stats      ICStats   `json:"stats"`
	Quintiles  []float64 `json:"quintiles"` // 逐日五分位收益均值的再平均；无有效日为 nil
	Daily      []DailyIC `json:"daily"`
	StartedAt  string    `json:"startedAt"`
	FinishedAt string    `json:"finishedAt"`
}

// DailyIC 单日横截面 IC；IC=nil 表示当日无效（NaN，JSON 序列化为 null）。
type DailyIC struct {
	Date string   `json:"date"`
	IC   *float64 `json:"ic"`
}

// runAnalysis 因子分析主循环：逐日横截面 因子值名次 vs 未来收益名次 → Spearman IC。
// 口径与 fillCrossSection 一致：series=his+dks，前缀无前视；未来收益
// dks[i+Window].Close/dks[i].Close−1，尾部 Window 日无未来收益剔除。
func (r *Runner) runAnalysis(cfg AnalyzeConfig, stop chan struct{}) (*AnalysisReport, error) {
	started := time.Now()

	codes, err := r.resolveCodes(cfg.RunConfig, stop)
	if err != nil {
		return nil, err
	}
	r.totalCodes.Store(int64(len(codes)))
	years := cfg.RunConfig.years()
	fct := f.Build(cfg.Kind, cfg.Days)

	var mu sync.Mutex
	var mergedV, mergedR []map[time.Time]map[string]float64 // day → code → 值/收益

	r.forEachCodeData(codes, years, stop, false, func(code string, datas []yearData) {
		lv := map[time.Time]map[string]float64{}
		lr := map[time.Time]map[string]float64{}
		for _, d := range datas {
			series := make(extend.Klines, 0, len(d.his)+len(d.dks))
			series = append(series, d.his...)
			series = append(series, d.dks...)
			base := len(d.his)
			for i := 0; i+cfg.Window < len(d.dks); i++ {
				v := fct.Value(code, series[:base+i+1])
				if math.IsNaN(v) {
					continue
				}
				day := core.DayOf(d.dks[i].Time)
				if lv[day] == nil {
					lv[day] = map[string]float64{}
					lr[day] = map[string]float64{}
				}
				lv[day][code] = v
				lr[day][code] = d.dks[i+cfg.Window].Close/d.dks[i].Close - 1
			}
		}
		mu.Lock()
		mergedV = append(mergedV, lv)
		mergedR = append(mergedR, lr)
		mu.Unlock()
	})

	select {
	case <-stop:
		return nil, errStopped
	default:
	}

	// 合并各票数据（单协程，无竞争）
	vals := map[time.Time]map[string]float64{}
	rets := map[time.Time]map[string]float64{}
	for i, m := range mergedV {
		for day, cv := range m {
			if vals[day] == nil {
				vals[day] = map[string]float64{}
				rets[day] = map[string]float64{}
			}
			for c, v := range cv {
				vals[day][c] = v
				rets[day][c] = mergedR[i][day][c]
			}
		}
	}

	// 逐日计算 IC（日期升序输出）
	days := make([]time.Time, 0, len(vals))
	for day := range vals {
		days = append(days, day)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })

	daily := make([]DailyIC, 0, len(days))
	ics := make([]float64, 0, len(days))
	qSums, qCnts := make([]float64, 5), make([]int, 5)
	for _, day := range days {
		vs := make([]float64, 0, len(vals[day]))
		rs := make([]float64, 0, len(vals[day]))
		for c, v := range vals[day] {
			vs = append(vs, v)
			rs = append(rs, rets[day][c])
		}
		di := DailyIC{Date: day.Format("2006-01-02")}
		ic := spearmanIC(vs, rs)
		if !math.IsNaN(ic) {
			ics = append(ics, ic)
			v := ic
			di.IC = &v
			// 逐日五分位均值再平均（每日等权，与 IC 口径一致）
			if qs := quintileMeans(vs, rs); qs != nil {
				for q := range qs {
					qSums[q] += qs[q]
					qCnts[q]++
				}
			}
		}
		daily = append(daily, di)
	}

	rep := &AnalysisReport{
		FactorName: fct.Name(),
		Kind:       cfg.Kind,
		Window:     cfg.Window,
		Stats:      icStats(ics),
		Daily:      daily,
		StartedAt:  started.Format(time.RFC3339),
		FinishedAt: time.Now().Format(time.RFC3339),
	}
	if qCnts[0] > 0 { // 至少有一天票数 ≥5 才有意义（防 0 除 NaN 炸 JSON）
		rep.Quintiles = make([]float64, 5)
		for q := range qSums {
			rep.Quintiles[q] = qSums[q] / float64(qCnts[q])
		}
	}
	if err := exportAnalysis(rep); err != nil {
		return nil, fmt.Errorf("分析报告落盘失败: %w", err)
	}
	return rep, nil
}

// analysisHTML 分析报告页模板（ECharts 逐日 IC 折线；%% 为 CSS 转义）。
const analysisHTML = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>%s IC 分析</title>
<script src="https://cdn.jsdelivr.net/npm/echarts@5/dist/echarts.min.js"></script>
</head>
<body>
<h2>%s · 未来 %d 交易日 IC</h2>
<p>均值 %.4f · 标准差 %.4f · t 值 %.2f · 有效样本 %d 日</p>
<div id="chart" style="width:100%%;height:420px"></div>
<script>
const days = %s, ics = %s;
echarts.init(document.getElementById('chart')).setOption({
  tooltip: {trigger: 'axis'},
  xAxis: {type: 'category', data: days},
  yAxis: {type: 'value', min: -1, max: 1},
  series: [{type: 'line', data: ics, connectNulls: true, markLine: {data: [{yAxis: 0}]}}]
});
</script>
</body>
</html>`

// exportAnalysis 落盘分析产物到 output/factor/<清洗后kind>/：
// report.json（tmp+rename 原子写，镜像 saveReport）+ ic.csv + report.html
// （后两者 best-effort，不阻塞主产物）。
func exportAnalysis(rep *AnalysisReport) error {
	dir := filepath.Join("output", "factor", core.TradesExportName(rep.Kind))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	buf, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, ".report.json.tmp")
	if err := os.WriteFile(tmp, buf, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "report.json")); err != nil {
		return err
	}

	// ic.csv（无效日按 0 写）
	rows := [][]any{{"date", "ic"}}
	for _, d := range rep.Daily {
		v := 0.0
		if d.IC != nil {
			v = *d.IC
		}
		rows = append(rows, []any{d.Date, v})
	}
	if buf, err := csv.Export(rows); err == nil {
		_ = oss.New(filepath.Join(dir, "ic.csv"), buf)
	}

	// report.html（无效日为 null，前端 connectNulls 补线）
	days := make([]string, 0, len(rep.Daily))
	ics := make([]*float64, 0, len(rep.Daily))
	for _, d := range rep.Daily {
		days = append(days, d.Date)
		ics = append(ics, d.IC)
	}
	dayJSON, _ := json.Marshal(days)
	icJSON, _ := json.Marshal(ics)
	html := fmt.Sprintf(analysisHTML, rep.FactorName, rep.FactorName, rep.Window,
		rep.Stats.Mean, rep.Stats.Std, rep.Stats.TStat, rep.Stats.Pairs, dayJSON, icJSON)
	_ = os.WriteFile(filepath.Join(dir, "report.html"), []byte(html), 0644)
	return nil
}
```

- [ ] **Step 4: 运行测试确认通过**

```powershell
go test ./internal/lab/ -run "TestRunAnalysis|TestAnalyzeConfigValidate" -count=1; go test ./internal/lab/ -count=1
```

- [ ] **Step 5: 提交**

```powershell
git add internal/lab/analysis.go internal/lab/analysis_run_test.go internal/lab/runner.go; git commit -m "feat(lab): 因子分析编排（逐日横截面 IC + 原子落盘 report.json/ic.csv/report.html）"
```

---

### Task 14: 分析 API（/api/factors、/api/analyze、/api/analysis/latest）+ task 状态（TDD）

**Files:**
- Modify: `internal/lab/runner.go`
- Modify: `internal/lab/server.go`
- Test: `internal/lab/factor_e2e_test.go`（末尾追加）

设计要点：

- task 标注：Task 13 的 `StartAnalysis` 已把 `r.task` 标为 `analysis`，本任务补上 `Start` 的 `backtest` 标注（否则回测后 task 残留上次分析的值）。`Status()` 初始 map 恒含 `"task": "backtest"`（无任务时的默认展示，兼容旧前端），随后若 task 有值则覆盖为实际值。
- 分析不加载脚本（不需要 variants），`handleAnalyze` 镜像 `handleRun`：Decode → 补 ScriptName → `StartAnalysis` 失败（含校验失败与任务互斥）一律 409。
- `GET /api/factors` 直接返回注册表 `f.All()`（Task 6），是前端因子下拉的唯一数据源。

- [ ] **Step 1: 写失败测试**

在 `internal/lab/factor_e2e_test.go` 末尾追加（同包直连，复用 Task 13 的 `setupAnalysisData` 与 matrix_test.go 的 `writeDayDBCloses`）：

```go
// TestServerAnalysisAPI 端到端：因子目录 → 非法 kind 409 → 合法分析 → task=analysis → 最新报告。
func TestServerAnalysisAPI(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)

	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	up := append([]float64{9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5, 9.5},
		10, 10.2, 10.4, 10.6, 10.8, 11, 11.2, 11.4, 11.6, 11.8,
		12, 12.2, 12.4, 12.6, 12.8, 13, 13.2, 13.4, 13.6, 13.8)
	down := append([]float64{21, 21, 21, 21, 21, 21, 21, 21},
		20.8, 20.6, 20.4, 20.2, 20, 19.8, 19.6, 19.4, 19.2, 19,
		18.8, 18.6, 18.4, 18.2, 18, 17.8, 17.6, 17.4, 17.2, 17)
	writeDayDBCloses(t, dir, "sh600001", up, base)
	writeDayDBCloses(t, dir, "sh600002", down, base)

	h := NewServer().Handler()

	// 因子目录：14 项，每项含 kind/name/description
	res := doReq(t, h, http.MethodGet, "/api/factors", nil, http.StatusOK)
	var catalog []map[string]any
	if err := json.Unmarshal(res, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 14 {
		t.Fatalf("因子目录项数 = %d", len(catalog))
	}
	for _, e := range catalog {
		for _, k := range []string{"kind", "name", "description"} {
			if _, ok := e[k]; !ok {
				t.Fatalf("目录项缺字段 %s: %v", k, e)
			}
		}
	}

	// 未知因子 → StartAnalysis 校验失败 → 409
	res = doReq(t, h, http.MethodPost, "/api/analyze", map[string]any{
		"kind": "nope",
	}, http.StatusConflict)

	// 合法分析请求
	res = doReq(t, h, http.MethodPost, "/api/analyze", map[string]any{
		"startYear": 2025, "endYear": 2025,
		"sampleMode": "codes", "sampleCodes": []string{"sh600001", "sh600002"},
		"kind": "momentum", "days": 2, "window": 1,
	}, http.StatusOK)
	if !bytes.Contains(res, []byte(`"ok":true`)) {
		t.Fatalf("analyze 响应异常: %s", res)
	}

	// 轮询至完成，task 应为 analysis
	deadline := time.Now().Add(30 * time.Second)
	var st map[string]any
	for {
		res = doReq(t, h, http.MethodGet, "/api/status", nil, http.StatusOK)
		st = nil
		if err := json.Unmarshal(res, &st); err != nil {
			t.Fatal(err)
		}
		if st["state"] == "done" || st["state"] == "error" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("分析超时未完成: %v", st)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st["state"] != "done" {
		t.Fatalf("分析失败: %v", st)
	}
	if st["task"] != "analysis" {
		t.Fatalf("task = %v, want analysis", st["task"])
	}

	// 最新分析报告
	res = doReq(t, h, http.MethodGet, "/api/analysis/latest", nil, http.StatusOK)
	var rep AnalysisReport
	if err := json.Unmarshal(res, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.FactorName != "N日动量(2)" {
		t.Fatalf("FactorName = %q", rep.FactorName)
	}
	if !nearlyEq(rep.Stats.Mean, 1) {
		t.Fatalf("Mean = %v, want 1", rep.Stats.Mean)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```powershell
go test ./internal/lab/ -run TestServerAnalysisAPI -count=1
```

预期：编译失败——`s.handleFactors` / `s.handleAnalyze` / `s.handleLatestAnalysis` 未定义（路由尚未注册）。

- [ ] **Step 3: 实现**

**3a. `internal/lab/runner.go`** —— task 标注与 LatestAnalysis：

`Start` 中 `r.state.Store(&state)` 之后、`rc := cfg` 之前插入：

```go
	task := "backtest"
	r.task.Store(&task)
```

`Status` 中 map 初始化补 task 默认值：

```go
	m := map[string]any{
		"state":      st,
		"progress":   0.0,
		"doneCodes":  r.doneCodes.Load(),
		"totalCodes": r.totalCodes.Load(),
		"task":       "backtest",
	}
```

map 构建完（`if cfg := r.lastRun.Load(); ...` 块之后、`return m` 之前）追加覆盖：

```go
	if p := r.task.Load(); p != nil {
		m["task"] = *p
	}
```

`StartAnalysis` 之前新增：

```go
// LatestAnalysis 最近一次完成的因子分析报告（无则 nil）。
func (r *Runner) LatestAnalysis() *AnalysisReport {
	return r.lastAnalysis.Load()
}
```

**3b. `internal/lab/server.go`** —— 契约注释、路由与处理器：

import 块补因子包（`lib/extend` 之后）：

```go
	f "github.com/injoyai/strategy-tail/strategies/factor"
```

API 契约注释（`GET /api/kline/{code}` 行后）补 3 行：

```go
//	GET      /api/factors       因子目录
//	POST     /api/analyze       启动因子分析（与回测共用任务互斥）
//	GET      /api/analysis/latest 最新分析报告
```

`NewServer` 中 `handleKline` 注册行之后补 3 条路由：

```go
	s.mux.HandleFunc("GET /api/factors", s.handleFactors)
	s.mux.HandleFunc("POST /api/analyze", s.handleAnalyze)
	s.mux.HandleFunc("GET /api/analysis/latest", s.handleLatestAnalysis)
```

`handleLatestReport` 之后追加 3 个处理器：

```go
// handleFactors 因子目录（Task 6 注册表 All()，供前端下拉）。
func (s *Server) handleFactors(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, f.All())
}

// handleAnalyze 启动因子分析（不加载脚本，与回测共用 Runner 互斥）。
func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	var cfg AnalyzeConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无效: "+err.Error())
		return
	}
	cfg.ScriptName = scriptName()
	if err := s.runner.StartAnalysis(cfg); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleLatestAnalysis 最新完成的分析报告。
func (s *Server) handleLatestAnalysis(w http.ResponseWriter, r *http.Request) {
	rep := s.runner.LatestAnalysis()
	if rep == nil {
		writeErr(w, http.StatusNotFound, "暂无完成的分析报告")
		return
	}
	writeJSON(w, rep)
}
```

- [ ] **Step 4: 运行测试确认通过**

```powershell
go test ./internal/lab/ -run TestServerAnalysisAPI -count=1; go test ./internal/lab/ -count=1
```

- [ ] **Step 5: 提交**

```powershell
git add internal/lab/runner.go internal/lab/server.go internal/lab/factor_e2e_test.go; git commit -m "feat(lab): 因子分析 API 路由与 task 状态字段"
```

---

### Task 15: 前端 Tab④ 因子研究（index.html）

**Files:**
- Modify: `internal/lab/web/lab/index.html`

设计要点：

- 前端无测试基建（无 JS 测试链），本任务为纯静态页修改，验证方式为 `go build ./...`（embed 参与编译）+ 启动服务后手动核对（curl 首页 + 浏览器交互）。
- Tab④ 复用 Tab① 的运行配置（年份、样本池经 `buildConfig()` 读取），仅追加因子下拉（GET /api/factors）、Days 参数与收益窗口；启动分析后走与回测同一 `pollStatus` 轮询，done 后按 `s.task` 分流取报告并切 Tab。
- ECharts 遵循页面既有惰性初始化模式（`if (!icC) icC = echarts.init(...)`，同 trendChart/klineChart）；IC 折线 y 轴固定 [-1, 1]，无效日为 null 时 `connectNulls: true` 补线，y=0 虚线参考线。
- 分析期间状态栏按钮逻辑自动复用：`running` 时禁用运行/显示停止（Runner 互斥对分析同样生效）。

- [ ] **Step 1: HTML——新增第 4 个 Tab 与面板**

tabs 区（L68-72 `<div class="tabs">` 内）第 3 个按钮后追加：

```html
  <button data-tab="4">④ 因子研究</button>
```

Tab3 面板收尾 `</div>` 与 `</main>` 之间插入 Tab4 面板：

```html
<!-- Tab4 -->
<div class="tab-panel" id="tab4">
  <div class="card">
    <h3>因子研究（横截面 IC）</h3>
    <div class="form-row"><label>因子</label>
      <select id="factorKind" style="min-width:220px"></select>
      <label style="margin-left:16px">参数 Days</label>
      <input type="number" id="factorDays" value="20" min="1" style="width:80px">
      <label style="margin-left:16px">收益窗口(日)</label>
      <select id="factorWindow">
        <option value="1" selected>1</option>
        <option value="5">5</option>
        <option value="20">20</option>
      </select>
    </div>
    <div class="editor-actions">
      <button class="primary" id="btnAnalyze" onclick="runAnalysis()">开始分析</button>
    </div>
    <div class="msg" id="factorMsg"></div>
    <div class="hint">每日计算全样本因子值与未来 Window 日收益的 Spearman 秩相关（IC）：均值显著为正 → 因子值越大未来收益越高，为负则反之。运行配置复用 Tab① 的年份与样本池。</div>
    <div class="hint" id="factorSummary"></div>
    <div id="icChart" style="height:360px"></div>
  </div>
</div>
```

- [ ] **Step 2: JS——目录加载、启动分析、轮询分流、IC 渲染**

（1）Tab 点击处理：`if (btn.dataset.tab === '3') renderTab3();` 之后追加：

```js
    if (btn.dataset.tab === '4') renderAnalysis();
```

（2）`switchTab(n)` 函数之后追加 Tab4 区块：

```js
// ============ Tab4 因子研究 ============
let factorCatalog = [];
let currentAnalysis = null; // Tab4 共享的分析报告
let icC = null;             // IC 图表实例（惰性初始化）

async function loadFactors() {
  try {
    factorCatalog = await api('GET', '/api/factors');
    $('factorKind').innerHTML = factorCatalog.map(e => `<option value="${e.kind}">${e.name}</option>`).join('');
  } catch (e) { showFactorMsg(e.message, false); }
}
function showFactorMsg(msg, ok) {
  const el = $('factorMsg');
  el.textContent = msg;
  el.className = 'msg ' + (ok ? 'ok' : 'err');
}
async function runAnalysis() {
  showFactorMsg('', true);
  try {
    const cfg = buildConfig(); // 复用 Tab1 年份/样本配置
    cfg.kind = $('factorKind').value;
    cfg.days = +$('factorDays').value;
    cfg.window = +$('factorWindow').value;
    await api('POST', '/api/analyze', cfg);
    showFactorMsg('分析已启动', true);
    pollStatus();
  } catch (e) { showFactorMsg(e.message, false); }
}
function renderAnalysis() {
  if (!currentAnalysis) return;
  const s = currentAnalysis.stats;
  $('factorSummary').textContent =
    `${currentAnalysis.factorName} · 窗口${currentAnalysis.window}日 · 有效日 ${s.pairs} · IC均值 ${s.mean.toFixed(4)} · 标准差 ${s.std.toFixed(4)} · t值 ${s.tStat.toFixed(2)}`;
  const days = currentAnalysis.daily.map(d => d.date);
  const ics = currentAnalysis.daily.map(d => d.ic);
  if (!icC) icC = echarts.init($('icChart'));
  icC.setOption({
    tooltip: {trigger: 'axis'},
    grid: {left: 60, right: 20, top: 20, bottom: 60},
    xAxis: {type: 'category', data: days},
    yAxis: {type: 'value', min: -1, max: 1},
    series: [{
      name: 'IC', type: 'line', data: ics, symbol: 'none',
      connectNulls: true, // 无效日（样本不足）跳过连线
      markLine: {silent: true, symbol: 'none', lineStyle: {type: 'dashed'}, data: [{yAxis: 0}]}
    }]
  });
}
```

（3）`pollStatusOnce` 两处调整——

状态标签按任务区分运行中文案（原 `{idle: '空闲', running: '回测中', ...}` 行）改为：

```js
    $('statusState').textContent = {idle: '空闲', running: s.task === 'analysis' ? '因子分析中' : '回测中', done: '已完成', error: '出错'}[s.state] || s.state;
```

done 分支按 `s.task` 分流：

```js
    if (s.state === 'done') {
      clearInterval(pollTimer); pollTimer = null;
      if (s.task === 'analysis') {
        currentAnalysis = await api('GET', '/api/analysis/latest');
        switchTab(4);
      } else {
        currentReport = await api('GET', '/api/report/latest');
        switchTab(2);
      }
    } else if (s.state === 'idle' || s.state === 'error') {
```

（4）初始化区 `loadScript();` 之后追加：

```js
loadFactors();
```

- [ ] **Step 3: 编译与手动验证**

```powershell
go build ./...; go run ./cmd/lab
```

- 浏览器自动打开后确认出现"④ 因子研究"标签，下拉为 14 个因子（名称如"N日动量(20)"）；
- PowerShell 确认首页含新 Tab：`(Invoke-WebRequest -UseBasicParsing http://127.0.0.1:8765/).Content.Contains('data-tab="4"')` 返回 True；
- 选"N日动量(2)"跑一次小样本分析：状态栏显示"因子分析中"、完成后自动切到 Tab④ 并渲染 IC 折线与摘要文案；回测 Tab①/② 功能不受影响。

- [ ] **Step 4: 提交**

```powershell
git add internal/lab/web/lab/index.html; git commit -m "feat(lab): 前端新增④因子研究页（IC 分析交互与折线图）"
```

---

### Task 16: 全量验证与项目记忆更新

**Files:**
- Modify: `MEMORY.md`

- [ ] **Step 1: 全量验证**

```powershell
go build ./...; go vet ./...; go test ./core/ ./strategies/... ./internal/lab/ -count=1
```

确认全部通过。lab 测试均使用 `t.TempDir()` + `t.Chdir` 隔离，`output/` 产物不污染仓库。

- [ ] **Step 2: 更新 MEMORY.md**

合并入现有结构（不新增重复段落），记录因子框架长期要点：

- 架构：`core/factor.go` 定义 Factor 接口（Name/Value，NaN=无效与 0 区分）；`strategies/factor` 内置 14 个因子 + 注册表（Catalog/Build）；TopN 买入组件 `A因子TopN` + 横截面快照（`core/cross_section.go`，进程内存，任务结束 `ClearCrossSection` 清理）。
- 数据流：lab `run()` 回测前经 `fillCrossSection` 预填快照 → 买入时 `CrossSectionRank` 查询；因子分析 `runAnalysis` 独立编排（复用 forEachCodeData）→ 逐日 Spearman IC → 产物 `output/factor/<kind>/`（report.json/ic.csv/report.html）。
- 约定：因子实时计算不存库；数据不足返回 NaN；JSON 中无效 IC 用 `*float64=null`；新因子只需实现 Factor + 注册 Catalog 条目，API/前端自动收录。
- API：GET /api/factors、POST /api/analyze、GET /api/analysis/latest；Status 新增 task 字段（backtest/analysis）。

- [ ] **Step 3: 终检**

`git status` + `git log --oneline -16` 复查最终差异：无无关修改、无调试残留、16 个任务提交完整。
