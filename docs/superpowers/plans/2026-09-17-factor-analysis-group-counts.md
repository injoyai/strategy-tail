# 因子分析分组数扩展与值域展示实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 因子分析支持可配分组数（5/7/10/11，后端 2–20），并在柱状图轴标签与分组明细表中展示每组因子值范围。

**Architecture:** 后端把 `quantileAssign`/`summarizeQuintiles`/`aggregateGroups` 的硬编码 5 组泛化为 `GroupingConfig.groupCount()` 解析的参数（缺省 5，向后兼容）；前端因子页新增分组数下拉，结果区改为优先消费 v2 `groups` 字段（含每组因子值分布），旧报告降级读 `quintiles`。

**Tech Stack:** Go（`internal/lab`，纯函数 + JSON 报告）、单文件前端 `internal/lab/web/lab/index.html`（ECharts、零构建链、`//go:embed`）。

> 上游设计：`docs/superpowers/specs/2026-09-17-factor-analysis-group-counts-design.md`
>
> 关键背景：v2 `groups` 数据合同（每组 min/P25/median/mean/P75/max/std）后端已实现（`internal/lab/analysis.go`），前端从未读取——本计划补展示半区并泛化组数。

---

## 环境与提交约定（每个任务通用，必读）

1. **本机 360 拦截 git 写对象**：`git commit` 写 `.git/objects` 会 Permission denied。提交一律走仓库内绕过工具：
   - ① `git add <文件>` 循环；add 报错时对报错文件执行 `powershell -File .git/fx/_fixblob.ps1 -Files <相对路径>` 后重试；
   - ② 用 Write 工具把提交消息写入 `.git/fx/_msg.txt`（UTF-8，无 BOM，单行或末尾留一个换行）；
   - ③ 执行 `powershell -File .git/fx/_fixtree.ps1 -MsgFile .git/fx/_msg.txt` 完成提交；
   - ④ `git log --oneline -2` 验证提交已生成（`_fixtree` 直接写 commit 对象）。
   - `.git/fx/` 目录勿删勿入库。
2. **行结尾**：仓库 Go/HTML 源文件为 LF。使用编辑工具（非 PowerShell `Set-Content`）改文件即可保持 LF；改完跑 `gofmt -l internal/lab` 核对。
3. **测试命令**（PowerShell）：

```powershell
go test ./internal/lab
go test ./...
go build ./...
gofmt -l internal/lab
```

4. 前端无自动化测试基建：前端任务的验证 = `go build ./...`（embed 参与编译保证 JS 语法不破坏构建）+ Task 7 的浏览器手动清单。**前端无测试基建，验证 = go build（embed 参与编译）+ 浏览器手动核对。**

---

### Task 1: GroupingConfig 分组数字段与校验（TDD）

**Files:**
- Modify: `internal/lab/analysis.go`（`GroupingConfig` 定义与 `Validate`，约 162–194 行）
- Test: `internal/lab/analysis_test.go`（`TestGroupingValidate`）

- [ ] **Step 1: 扩展失败测试**

在 `TestGroupingValidate` 末尾（现有"合法 bins"断言之后）追加：

```go
	// 分组数：缺省 0、2、20 合法；越界非法；bins 断点数 = 组数-1
	for _, c := range []GroupingConfig{
		{Groups: 1}, {Groups: 21}, {Groups: -3},
		{Mode: "quantile", Groups: 7, Cuts: []float64{1, 2}}, // quantile 不接受断点
		{Mode: "bins", Groups: 7, Cuts: []float64{1, 2, 3, 4}}, // 7 组需 6 断点
	} {
		if err := c.Validate(); err == nil {
			t.Fatalf("应报错: %+v", c)
		}
	}
	for _, c := range []GroupingConfig{
		{Groups: 0}, {Groups: 2}, {Groups: 20},
		{Mode: "quantile", Groups: 7},
		{Mode: "bins", Groups: 7, Cuts: []float64{1, 2, 3, 4, 5, 6}},
	} {
		if err := c.Validate(); err != nil {
			t.Fatalf("应合法: %+v: %v", c, err)
		}
	}
	if got := (GroupingConfig{}).groupCount(); got != 5 {
		t.Fatalf("缺省 groupCount = %d, want 5", got)
	}
	if got := (GroupingConfig{Groups: 7}).groupCount(); got != 7 {
		t.Fatalf("groupCount = %d, want 7", got)
	}
```

- [ ] **Step 2: 运行确认失败**

```powershell
go test ./internal/lab -run TestGroupingValidate
```

预期：编译失败（`Groups` 字段与 `groupCount` 方法不存在）。

- [ ] **Step 3: 实现 Groups 字段、groupCount 与 Validate 泛化**

用下面的代码整体替换 `analysis.go` 中 `GroupingConfig`、`Validate` 两段（`AnalyzeConfig` 的 `Grouping` 字段注释同步把"缺省等数量五组"改为"缺省等数量 5 组"）：

```go
// GroupingConfig 分组方式配置：mode 缺省（空）或 quantile 为每日等数量分组，
// 组数由 Groups 决定（缺省 5）；bins 为固定数值区间，cuts 必须恰好
// Groups-1 个严格递增断点，形成首末开放、中间左开右闭的分组。
type GroupingConfig struct {
	Mode   string    `json:"mode,omitempty"`  // quantile | bins
	Groups int       `json:"groups,omitempty"` // 分组数；0=缺省 5；有效 2-20
	Cuts   []float64 `json:"cuts,omitempty"`  // bins 模式必须恰好 Groups-1 个
}

// 分组数允许范围（Groups=0 视为缺省 5，不参与该范围校验）。
const (
	minGroups = 2
	maxGroups = 20
)

// groupCount 生效分组数：Groups=0 视为默认 5。Validate 与聚合统计共用
// 同一解析，禁止两处各写一遍缺省逻辑。
func (c GroupingConfig) groupCount() int {
	if c.Groups == 0 {
		return 5
	}
	return c.Groups
}

// Validate 校验分组配置：未知模式报错；分组数缺省（0）或 2-20；
// quantile 不接受断点；bins 要求恰好 groupCount()-1 个有限且严格递增的
// 断点（-0 与 0 视为重复）。
func (c GroupingConfig) Validate() error {
	if c.Groups != 0 && (c.Groups < minGroups || c.Groups > maxGroups) {
		return fmt.Errorf("分组数无效: %d（应为 %d-%d 或缺省 5）", c.Groups, minGroups, maxGroups)
	}
	switch c.Mode {
	case "", "quantile":
		if len(c.Cuts) > 0 {
			return fmt.Errorf("等数量分组模式不接受断点")
		}
	case "bins":
		if want := c.groupCount() - 1; len(c.Cuts) != want {
			return fmt.Errorf("固定区间模式需要恰好 %d 个断点, 得到 %d 个", want, len(c.Cuts))
		}
		for i, v := range c.Cuts {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("断点 %d 不是有限值", i+1)
			}
			if i > 0 && v <= c.Cuts[i-1] {
				return fmt.Errorf("断点必须严格递增: %v", c.Cuts)
			}
		}
	default:
		return fmt.Errorf("未知分组模式: %s", c.Mode)
	}
	return nil
}
```

注意：原错误文案"等数量五组模式不接受断点"改为"等数量分组模式不接受断点"，现有测试只断言 err 非 nil，不受影响。

- [ ] **Step 4: 运行测试确认通过**

```powershell
go test ./internal/lab -run 'TestGroupingValidate|TestAnalyzeConfigValidate'
```

预期：PASS（`TestAnalyzeConfigValidate` 中现有 bins 4 断点用例在缺省 5 组下仍合法）。

- [ ] **Step 5: 提交**

`.git/fx/_msg.txt` 内容：

```
feat(lab): 因子分析分组数字段与校验（2-20 组，缺省 5）
```

按"环境与提交约定"执行 add + `_fixtree.ps1` 提交。

---

### Task 2: 分组纯函数泛化（TDD）

**Files:**
- Modify: `internal/lab/analysis.go`（`quantileAssign`、`summarizeQuintiles`、`aggregateGroups` 及相关注释）
- Test: `internal/lab/analysis_test.go`（`TestQuantileAssign`、`TestSummarizeQuintiles`）

- [ ] **Step 1: 更新纯函数测试签名并新增 7 组用例**

`TestQuantileAssign`：所有现有调用补第二参数 5（`quantileAssign(obs)` → `quantileAssign(obs, 5)`，涉及 obs/shuffled/seven/tie/same/first/again 共 8 处调用），"n=7 不能被五整除"注释段保持 `, 5`。在函数末尾（确定性循环之前或之后均可）追加：

```go
	// g=7：n=10 唯一值 → 组号 floor(i*7/10)：[0,0,1,2,2,3,4,4,5,6]
	g7 := quantileAssign(obs, 7)
	for i, want := range []int{0, 0, 1, 2, 2, 3, 4, 4, 5, 6} {
		if g7[i] != want {
			t.Fatalf("g7[%d] = %d, want %d", i, g7[i], want)
		}
	}
	// g=7、n=3：票数不足组数 → 只占用前 3 组（floor(i*7/6)：0,1,2）
	three := make([]factorObs, 3)
	for i := range three {
		three[i] = factorObs{Code: fmt.Sprintf("c%d", i), Value: float64(i + 1)}
	}
	gt := quantileAssign(three, 7)
	for i, want := range []int{0, 1, 2} {
		if gt[i] != want {
			t.Fatalf("gt[%d] = %d, want %d", i, gt[i], want)
		}
	}
```

`TestSummarizeQuintiles`：现有 6 处调用补第二参数 5（`summarizeQuintiles(qs)` → `summarizeQuintiles(qs, 5)`），末尾追加：

```go
	// 7 组：严格递增，spread 为首末差
	s = summarizeQuintiles([]float64{-0.03, -0.02, -0.01, 0, 0.01, 0.02, 0.03}, 7)
	if s.Direction != "ascending" || !s.Monotonic || s.Spread == nil || !nearlyEq(*s.Spread, 0.06) {
		t.Fatalf("7 组 ascending: %+v", s)
	}
	// 组数不足 7 → insufficient
	s = summarizeQuintiles([]float64{1, 2, 3}, 7)
	if s.Direction != "insufficient" || s.Spread != nil {
		t.Fatalf("7 组不足: %+v", s)
	}
```

- [ ] **Step 2: 运行确认失败**

```powershell
go test ./internal/lab -run 'TestQuantileAssign|TestSummarizeQuintiles'
```

预期：编译失败（签名不匹配）。

- [ ] **Step 3: 泛化三个纯函数**

`quantileAssign` 整体替换为：

```go
// quantileAssign 等数量 g 组（分位）分组：观测按因子值升序、code 升序排序，
// 相同因子值的连续观测为并列块，整块按排序位置中点归组（避免拆块产生
// 虚假组间差异），公式 floor(((i+j)/2)×g/n) 以整数倍增 (i+j)×g/(2n) 计算。
// 返回与输入同序的组号 0..g-1（0=因子值最低组）；相同输入结果恒定。
func quantileAssign(obs []factorObs, g int) []int {
	n := len(obs)
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		if obs[idx[a]].Value != obs[idx[b]].Value {
			return obs[idx[a]].Value < obs[idx[b]].Value
		}
		return obs[idx[a]].Code < obs[idx[b]].Code
	})
	out := make([]int, n)
	for i := 0; i < n; {
		j := i
		for j+1 < n && obs[idx[j+1]].Value == obs[idx[i]].Value {
			j++
		}
		gr := (i + j) * g / (2 * n) // 中点公式整数形式；结果天然在 0..g-1
		for k := i; k <= j; k++ {
			out[idx[k]] = gr
		}
		i = j + 1
	}
	return out
}
```

`summarizeQuintiles` 整体替换为（仅两处变化：签名加 `n int`、`len(qs) < 5` → `len(qs) < n`、spread 取首末）：

```go
// summarizeQuintiles n 组收益摘要（纯函数）：组数不足 n 或含 NaN 视为
// insufficient（spread null）；严格单调给方向；全相等为 flat。
func summarizeQuintiles(qs []float64, n int) QuintileSummary {
	if len(qs) < n {
		return QuintileSummary{Direction: "insufficient"}
	}
	for _, v := range qs {
		if math.IsNaN(v) {
			return QuintileSummary{Direction: "insufficient"}
		}
	}
	asc, desc := true, true
	for i := 1; i < len(qs); i++ {
		if qs[i] <= qs[i-1] {
			asc = false
		}
		if qs[i] >= qs[i-1] {
			desc = false
		}
	}
	s := QuintileSummary{}
	switch {
	case asc:
		s.Direction, s.Monotonic = "ascending", true
	case desc:
		s.Direction, s.Monotonic = "descending", true
	default:
		flat := true
		for _, v := range qs {
			if v != qs[0] {
				flat = false
				break
			}
		}
		if flat {
			s.Direction = "flat"
		} else {
			s.Direction = "mixed"
		}
	}
	spread := qs[len(qs)-1] - qs[0]
	s.Spread = &spread
	return s
}
```

`aggregateGroups` 整体替换为（组数 `g := grouping.groupCount()`，循环上限与标签按 g；逻辑与原版逐行等价）：

```go
// aggregateGroups 对一组交易日聚合分组统计：分位模式按并列块整块归组
// （确定性 tie-break），bins 模式按固定区间；组数由 grouping.groupCount()
// 决定。组内因子值为全样本累计（原始值），组收益先算日内组均值、再对
// 非空交易日等权。返回全部组统计、组收益（任一组无收益即为 nil，不以 0
// 冒充）与摘要。全区间与年度共用本函数，保证两者口径一致。
func aggregateGroups(days []time.Time, vals, rets map[time.Time]map[string]float64,
	grouping GroupingConfig) ([]FactorGroupStats, []float64, QuintileSummary) {
	isBins := grouping.Mode == "bins"
	g := grouping.groupCount()
	accVals := make([][]float64, g) // 组内因子值（全样本累计）
	accObs := make([]int, g)        // 有效观测数
	dayCnts := make([]int, g)       // 组非空日期数
	dayRets := make([]float64, g)   // 组非空日期的日内组均值之和
	for _, day := range days {
		obs := make([]factorObs, 0, len(vals[day]))
		for c, v := range vals[day] {
			obs = append(obs, factorObs{Code: c, Value: v})
		}
		assign := make([]int, len(obs))
		if isBins {
			for i, o := range obs {
				assign[i] = binGroup(o.Value, grouping.Cuts)
			}
		} else {
			assign = quantileAssign(obs, g)
		}
		gSums, gCnts := make([]float64, g), make([]int, g)
		for i, o := range obs {
			k := assign[i]
			accVals[k] = append(accVals[k], o.Value)
			accObs[k]++
			gSums[k] += rets[day][o.Code]
			gCnts[k]++
		}
		for k := 0; k < g; k++ {
			if gCnts[k] > 0 {
				dayCnts[k]++
				dayRets[k] += gSums[k] / float64(gCnts[k])
			}
		}
	}

	totalObs := 0
	for _, n := range accObs {
		totalObs += n
	}
	groups := make([]FactorGroupStats, g)
	for k := range groups {
		gs := FactorGroupStats{Index: k + 1}
		if isBins {
			gs.Label = fmt.Sprintf("B%d", k+1)
			if k > 0 {
				gs.Lower = f64p(grouping.Cuts[k-1])
			}
			if k < g-1 {
				gs.Upper = f64p(grouping.Cuts[k])
			}
		} else {
			gs.Label = fmt.Sprintf("Q%d", k+1)
		}
		if st := valueStats(accVals[k]); st != nil {
			gs.FactorMin, gs.FactorP25, gs.FactorMedian = f64p(st.Min), f64p(st.P25), f64p(st.Median)
			gs.FactorMean, gs.FactorP75, gs.FactorMax, gs.FactorStd =
				f64p(st.Mean), f64p(st.P75), f64p(st.Max), f64p(st.Std)
		}
		gs.Observations = accObs[k]
		gs.Dates = dayCnts[k]
		if totalObs > 0 {
			gs.CountPct = float64(accObs[k]) / float64(totalObs)
		}
		if dayCnts[k] > 0 {
			gs.ForwardReturn = f64p(dayRets[k] / float64(dayCnts[k]))
		}
		groups[k] = gs
	}

	// 摘要两种模式同口径：基于全部组 ForwardReturn，任一组无收益即 insufficient。
	summaryQs := make([]float64, 0, g)
	complete := true
	for _, gr := range groups {
		if gr.ForwardReturn == nil {
			complete = false
			break
		}
		summaryQs = append(summaryQs, *gr.ForwardReturn)
	}
	if !complete {
		summaryQs = nil
	}
	return groups, summaryQs, summarizeQuintiles(summaryQs, g)
}
```

同步更新纯注释（无行为变化）：
- `QuintileSummary` 结构注释"五组收益摘要"→"分组收益摘要：方向与 spread 由后端计算"；
- `FactorGroupStats.Index` 注释 `// 1..5` → `// 1..N`；`Label` 注释 `Q1..Q5 或 B1..B5` → `Q1..QN 或 B1..BN`；
- `YearAnalysis.Quintiles` 注释"该年五组收益"→"该年分组收益（长度=组数）"；
- `AnalysisReport.Quintiles` 注释"分位模式五组收益"→"分位模式分组收益（长度=组数）"。

- [ ] **Step 4: 运行测试确认通过（含既有五组用例全绿）**

```powershell
go test ./internal/lab
```

预期：全部 PASS——`TestRunAnalysis`/`TestRunAnalysisBins`/`TestRunAnalysisAllEqual`/`TestRunAnalysisMultiYearCoverage` 在缺省 grouping（5 组）下行为不变，是向后兼容的直接证据。

- [ ] **Step 5: 提交**

`.git/fx/_msg.txt` 内容：

```
feat(lab): 分组纯函数泛化——quantileAssign/summarizeQuintiles/aggregateGroups 支持任意组数
```

---

### Task 3: 7 组运行路径集成测试（TDD）

**Files:**
- Test: `internal/lab/analysis_run_test.go`（新增 `TestRunAnalysisSevenGroups`；扩展 `TestAnalyzeConfigValidate`）

- [ ] **Step 1: 写失败测试**

在 `analysis_run_test.go` 的 `TestRunAnalysisBins` 之后新增：

```go
// TestRunAnalysisSevenGroups 七组等频：7 票斜率互异 → 每日 7 个互异因子值，
// 七组完整；报告 Groups/Quintiles 镜像/年度 Quintiles 长度均为 7，
// CountPct≈1/7，组收益与斜率同向（ascending）。
func TestRunAnalysisSevenGroups(t *testing.T) {
	dir := t.TempDir()
	setupAnalysisData(t, dir)

	base := time.Date(2024, 12, 24, 0, 0, 0, 0, time.Local)
	mk := func(start, step float64) []float64 {
		cs := make([]float64, 28)
		for i := range cs {
			cs[i] = start + step*float64(i)
		}
		return cs
	}
	// 7 票斜率互异 → 2 日动量互异且与斜率同向：组序 = 斜率升序
	codes := []string{"sh600001", "sh600002", "sh600003", "sh600004", "sh600005", "sh600006", "sh600007"}
	steps := []float64{0.6, 0.45, 0.3, 0.15, 0, -0.15, -0.3}
	for i, c := range codes {
		writeDayDBCloses(t, dir, c, mk(10, steps[i]), base)
	}

	cfg := AnalyzeConfig{
		RunConfig: RunConfig{StartYear: 2025, EndYear: 2025,
			SampleMode: "codes", SampleCodes: codes, ScriptName: "matrix"},
		Kind: "momentum", Days: 2, Window: 1,
		Grouping: GroupingConfig{Mode: "quantile", Groups: 7},
	}
	rep, err := (&Runner{}).runAnalysis(cfg, make(chan struct{}))
	if err != nil {
		t.Fatalf("runAnalysis: %v", err)
	}
	if rep.Grouping.Groups != 7 {
		t.Fatalf("Grouping.Groups = %d, want 7", rep.Grouping.Groups)
	}
	if len(rep.Groups) != 7 {
		t.Fatalf("len(Groups) = %d, want 7", len(rep.Groups))
	}
	for i, g := range rep.Groups {
		if g.Index != i+1 || g.Label != fmt.Sprintf("Q%d", i+1) {
			t.Fatalf("Groups[%d] 标识 = %d/%q", i, g.Index, g.Label)
		}
		if g.Observations != 19 || g.Dates != 19 {
			t.Fatalf("Groups[%d] 计数 = %d/%d, want 19/19", i, g.Observations, g.Dates)
		}
		if !nearlyEq(g.CountPct, 1.0/7) {
			t.Fatalf("Groups[%d].CountPct = %v, want 1/7", i, g.CountPct)
		}
		if g.ForwardReturn == nil || g.FactorMedian == nil {
			t.Fatalf("Groups[%d] 统计缺失: %+v", i, g)
		}
	}
	// 组收益与斜率同向：Q7（最大斜率）收益高于 Q1（最小斜率）
	if *rep.Groups[6].ForwardReturn <= *rep.Groups[0].ForwardReturn {
		t.Fatalf("Q7 收益应高于 Q1: %v vs %v", *rep.Groups[6].ForwardReturn, *rep.Groups[0].ForwardReturn)
	}
	// 兼容镜像与年度拆分同长度
	if rep.Quintiles == nil || len(rep.Quintiles) != 7 {
		t.Fatalf("Quintiles = %v, want 7 个收益值", rep.Quintiles)
	}
	if len(rep.Years) != 1 || len(rep.Years[0].Quintiles) != 7 {
		t.Fatalf("Years Quintiles 长度错误: %+v", rep.Years)
	}
	if rep.Summary.Direction != "ascending" || !rep.Summary.Monotonic {
		t.Fatalf("Summary = %+v, want ascending", rep.Summary)
	}
}
```

在 `TestAnalyzeConfigValidate` 末尾追加：

```go
	seven := base
	seven.Grouping = GroupingConfig{Groups: 7}
	if err := seven.Validate(); err != nil {
		t.Fatalf("7 组等频应合法: %v", err)
	}
	badGroups := base
	badGroups.Grouping = GroupingConfig{Groups: 1}
	if err := badGroups.Validate(); err == nil {
		t.Fatal("分组数 1 应报错")
	}
	bins7 := base
	bins7.Grouping = GroupingConfig{Mode: "bins", Groups: 7, Cuts: []float64{1, 2, 3, 4}}
	if err := bins7.Validate(); err == nil {
		t.Fatal("7 组 bins 需 6 个断点，4 个应报错")
	}
```

- [ ] **Step 2: 运行测试**

```powershell
go test ./internal/lab -run 'TestRunAnalysisSevenGroups|TestAnalyzeConfigValidate'
```

预期：PASS（Task 1/2 已完成全部泛化，本任务是运行路径的集成验证；若失败说明泛化有缺口，按失败信息修复后重跑）。

- [ ] **Step 3: 提交**

`.git/fx/_msg.txt` 内容：

```
test(lab): 7 组等频运行路径集成测试——报告/镜像/年度长度与 CountPct 契约
```

---

### Task 4: 前端分组数控件与请求

**Files:**
- Modify: `internal/lab/web/lab/index.html`（参数行约 393–403 行；`runAnalysis()` 约 1150 行）

- [ ] **Step 1: 添加分组数下拉**

在"未来收益窗口" `select#factorWindow` 的 `</select>` 之后、"开始年份" label 之前插入：

```html
      <label class="factor-inline-label" for="factorGroups">分组数</label>
      <select id="factorGroups">
        <option value="5" selected>5 组</option>
        <option value="7">7 组</option>
        <option value="10">10 组</option>
        <option value="11">11 组</option>
      </select>
```

- [ ] **Step 2: 请求携带 grouping**

`runAnalysis()` 中 `cfg.window = +$('factorWindow').value;` 之后追加一行：

```js
    cfg.grouping = {mode: 'quantile', groups: +$('factorGroups').value};
```

- [ ] **Step 3: 构建验证**

```powershell
go build ./...
```

预期：编译通过（embed 打包 HTML，语法错误会破坏构建）。可选：`go run ./cmd/lab` 打开页面确认下拉出现且提交 7 组请求返回 done（用小样本 codes 模式快速验证，年份/样本沿用页面临时填写）。

- [ ] **Step 4: 提交**

`.git/fx/_msg.txt` 内容：

```
feat(lab): 因子页分组数下拉（5/7/10/11），分析请求携带 grouping.groups
```

---

### Task 5: 前端值域展示——柱状图轴标签 + 分组明细表

**Files:**
- Modify: `internal/lab/web/lab/index.html`（CSS 约 142–144 行；结果区 HTML 约 423–424 行；JS `renderAnalysis`/`renderQuintiles` 约 1180–1323 行）

- [ ] **Step 1: CSS 扩展（明细表复用年度表样式）**

把这三条选择器分别扩展为逗号并列（原行保留语义不变，只是同时命中新表）：

```css
#yearStability td,#groupDetail td{font-family:var(--font-num);font-variant-numeric:tabular-nums}
#yearStability tbody tr,#groupDetail tbody tr{cursor:default}
#yearStability td.na,#groupDetail td.na{color:var(--muted);font-family:inherit}
```

- [ ] **Step 2: 结果区 HTML——明细表结构**

在 `<div id="quintileChart"></div>` 与 `<div class="empty" id="quintileNA" ...>` 之后、"Coverage" 块（`<div class="coverage">` 含 `covText`）之前插入：

```html
        <div class="year-title" id="groupDetailTitle" style="display:none">分组明细</div>
        <div class="year-wrap" id="groupDetailWrap" style="display:none">
          <table id="groupDetail">
            <thead><tr>
              <th>组</th><th>因子值范围</th><th>中位数</th><th>均值</th><th>样本占比</th><th>未来收益</th>
            </tr></thead>
            <tbody id="groupBody"></tbody>
          </table>
        </div>
```

同时把静态 `#quintileNA` 初始文案中的"分成五组"改为"完成分组"（该文案运行时会被动态覆盖，初始值只需通用）。

- [ ] **Step 3: JS——格式化与渲染函数**

新增两个函数（放在 `renderQuintiles` 原位置附近）：

```js
// sig3 3 位有效数字去尾零（0.123→0.123，1234→1230，1e-7→1e-7）。
const sig3 = v => String(Number(v.toPrecision(3)));
// fmtFactorVal 因子值展示（unit 感知）：ratio ×100 加 %、multiple 加 ×、
// 其余原值；仅影响显示，JSON/提交始终用原始值。unit 见 strategies/factor/registry.go。
function fmtFactorVal(v, unit) {
  if (v == null || !isFinite(v)) return '—';
  if (unit === 'ratio') return sig3(v * 100) + '%';
  if (unit === 'multiple') return sig3(v) + '×';
  return sig3(v);
}
// groupRangeLabel 组的值域标签：分位模式=全样本累计 min~max（历史分布，
// 非固定边界）；bins 模式=固定边界 (lower, upper]，开放端 ±∞；旧数据两者皆无。
function groupRangeLabel(g, unit) {
  if (g.factorMin != null && g.factorMax != null) {
    return `[${fmtFactorVal(g.factorMin, unit)} ~ ${fmtFactorVal(g.factorMax, unit)}]`;
  }
  if (g.lower != null || g.upper != null) {
    return `(${g.lower == null ? '-∞' : fmtFactorVal(g.lower, unit)}, ${g.upper == null ? '+∞' : fmtFactorVal(g.upper, unit)}]`;
  }
  return '';
}
```

用 `renderGroupChart` 整体替换原 `renderQuintiles`：

```js
function renderGroupChart(gs, unit) {
  const el = $('quintileChart');
  if (!quintileC) { quintileC = echarts.init(el, 'dark'); charts.push(quintileC); }
  const cats = gs.map((g, i) => {
    const head = i === 0 ? `${g.label} 最低` : i === gs.length - 1 ? `${g.label} 最高` : g.label;
    const range = groupRangeLabel(g, unit);
    return range ? head + '\n' + range : head;
  });
  quintileC.setOption({
    backgroundColor: 'transparent',
    title: {text: `${gs.length} 组平均收益%（按因子值升序等频分组）`, left: 10, top: 6, textStyle: {fontSize: 13, color: '#e9eef8'}},
    tooltip: {trigger: 'axis', valueFormatter: v => v + '%'},
    grid: {left: 60, right: 20, top: 50, bottom: 56},
    xAxis: {type: 'category', data: cats, axisLabel: {interval: 0, fontSize: 10}},
    yAxis: {type: 'value', axisLabel: {formatter: '{value}%'}},
    series: [{
      type: 'bar',
      data: gs.map(g => +(g.forwardReturn * 100).toFixed(3)), // 显示乘 100；本页不提交该值
      itemStyle: {color: p => p.value >= 0 ? '#ef4444' : '#22c55e'},
      label: {show: true, position: 'top', fontSize: 11, formatter: p => p.value + '%'}
    }]
  }, true);
}
// renderGroupDetail 分组明细表：值域为该组全样本因子值分布（历史累计），
// 相邻组范围重叠属正常（分位边界每日变化）；空组显示“无有效样本”。
function renderGroupDetail(gs, unit) {
  const tb = $('groupBody');
  tb.innerHTML = '';
  gs.forEach(g => {
    const tr = document.createElement('tr');
    const cell = (txt, cls) => {
      const td = document.createElement('td');
      td.textContent = txt;
      if (cls) td.className = cls;
      tr.appendChild(td);
    };
    cell(g.label);
    const range = groupRangeLabel(g, unit);
    if (range) cell(range);
    else cell('无有效样本', 'na');
    if (g.factorMedian != null) cell(fmtFactorVal(g.factorMedian, unit));
    else cell('无有效样本', 'na');
    if (g.factorMean != null) cell(fmtFactorVal(g.factorMean, unit));
    else cell('无有效样本', 'na');
    if (g.countPct > 0) cell((g.countPct * 100).toFixed(1) + '%');
    else cell('无有效样本', 'na');
    if (g.forwardReturn == null) cell('无有效样本', 'na');
    else {
      const v = g.forwardReturn * 100;
      cell(v.toFixed(2) + '%', v > 0 ? 'pos' : (v < 0 ? 'neg' : ''));
    }
    tb.appendChild(tr);
  });
}
```

- [ ] **Step 4: renderAnalysis 分组渲染段重写**

把 `renderAnalysis()` 中"// 2) 五组柱状图（无 quintiles 不画空柱）"到对应 else 块结束（含 `renderQuintiles(qs)` 调用）整体替换为：

```js
  // 2) 分组柱状图与明细表：v2 groups 优先（含因子值域），旧报告降级 quintiles
  const unit = currentAnalysis.factor ? currentAnalysis.factor.unit : '';
  const real = Array.isArray(currentAnalysis.groups) ? currentAnalysis.groups : null;
  const qsArr = currentAnalysis.quintiles;
  const legacyGs = Array.isArray(qsArr) && qsArr.length >= 2
    ? qsArr.map((v, i) => ({label: 'Q' + (i + 1), forwardReturn: v})) : null;
  const chartGs = real && real.length ? real : legacyGs;
  const nGroups = chartGs ? chartGs.length : (real ? real.length : 5);
  const hasAll = !!(chartGs && chartGs.length &&
    chartGs.every(g => typeof g.forwardReturn === 'number' && isFinite(g.forwardReturn)));
  if (hasAll) {
    $('quintileChart').style.display = '';
    $('quintileNA').style.display = 'none';
    renderGroupChart(chartGs, unit);
  } else {
    $('quintileChart').style.display = 'none';
    $('quintileNA').style.display = '';
    $('quintileNA').textContent = `结果不足：有效样本过少，无法把样本分成 ${nGroups} 组并计算分组收益。`;
  }
  if (real && real.length) {
    $('groupDetailTitle').style.display = '';
    $('groupDetailWrap').style.display = '';
    renderGroupDetail(real, unit);
  } else {
    $('groupDetailTitle').style.display = 'none';
    $('groupDetailWrap').style.display = 'none';
  }
```

同时把 `renderAnalysis` 顶部 DIR 文案的"五组"字样去掉：

```js
  const DIR = {
    ascending: '因子值越大，未来收益越高',
    descending: '因子值越大，未来收益越低',
    mixed: '各组收益方向不一致',
    flat: '各组收益接近，因子区分度弱',
    insufficient: '结果不足：分组数据不完整，无方向结论',
  };
```

- [ ] **Step 5: 构建验证**

```powershell
go build ./...
```

预期：编译通过。`go run ./cmd/lab` 跑一次 5 组与一次 7 组分析（小样本 codes 模式），确认：柱状图 7 根柱、轴标签两行含值域（momentum 为 ratio 显示 `%`）、明细表行数与组数一致、空组显示"无有效样本"。

- [ ] **Step 6: 提交**

`.git/fx/_msg.txt` 内容：

```
feat(lab): 因子分组值域展示——柱状图轴标签 + 分组明细表（v2 groups，旧报告降级）
```

---

### Task 6: 前端年度表与文案动态化

**Files:**
- Modify: `internal/lab/web/lab/index.html`（年度表头约 444–446 行；`renderYearStability` 约 1254–1305 行；`renderRangeMeta` 约 1232–1251 行；概念卡 hint 约 415 行）

- [ ] **Step 1: 年度表头加差值列 id**

`<th id="q1Head">Q1</th><th id="q5Head">Q5</th><th>Q5-Q1</th>` 改为：

```html
              <th id="q1Head">Q1</th><th id="q5Head">Q5</th><th id="qSpreadHead">Q5-Q1</th>
```

- [ ] **Step 2: renderYearStability 动态首末组**

函数内替换：

```js
  const isBins = !!(rep.grouping && rep.grouping.mode === 'bins');
  $('q1Head').textContent = isBins ? 'B1' : 'Q1';
  $('q5Head').textContent = isBins ? 'B5' : 'Q5';
```

为：

```js
  const gs = Array.isArray(rep.groups) && rep.groups.length ? rep.groups : null;
  const nG = gs ? gs.length : ((rep.grouping && rep.grouping.groups) || 5);
  const firstL = gs ? gs[0].label : (rep.grouping && rep.grouping.mode === 'bins' ? 'B1' : 'Q1');
  const lastL = gs ? gs[gs.length - 1].label
    : (rep.grouping && rep.grouping.mode === 'bins' ? 'B' + nG : 'Q' + nG);
  $('q1Head').textContent = firstL;
  $('q5Head').textContent = lastL;
  $('qSpreadHead').textContent = `${lastL}-${firstL}`;
```

行内 `const qs = Array.isArray(y.quintiles) && y.quintiles.length === 5 ? y.quintiles : null;` 改为：

```js
    const qs = Array.isArray(y.quintiles) && y.quintiles.length >= 2 ? y.quintiles : null;
```

取值三行改为首末元素（对 5/N 组通用）：

```js
      const q1 = pct(qs && qs[0]), q5 = pct(qs && qs[qs.length - 1]);
      const sp = pct(qs && qs[qs.length - 1] - qs[0]);
```

- [ ] **Step 3: renderRangeMeta 增加分组信息**

在 `if (rep.window > 0) ...` 行之后追加：

```js
  const gp = rep.grouping || {};
  const gn = Array.isArray(rep.groups) && rep.groups.length ? rep.groups.length : (gp.groups || 5);
  el.appendChild(metaItem('分组：', gp.mode === 'bins' ? '固定区间' : `${gn} 组等频`));
```

- [ ] **Step 4: 概念卡 hint 去掉"五组"**

"请以本次样本的五组分布与统计显著性为准"改为"请以本次样本的分组分布与统计显著性为准"。

- [ ] **Step 5: 构建验证**

```powershell
go build ./...
```

预期：编译通过。浏览器核对：7 组报告年度表列头 `G…`（实际 `Q1/Q7/Q7-Q1`）、meta 行显示"分组：7 组等频"。

- [ ] **Step 6: 提交**

`.git/fx/_msg.txt` 内容：

```
feat(lab): 年度稳定性表与文案随分组数动态化（首末组/差值列/meta）
```

---

### Task 7: 全量验证、浏览器验收与文档记忆同步

**Files:**
- Modify: `docs/superpowers/specs/2026-09-17-factor-analysis-group-counts-design.md`（状态行）
- Modify: `MEMORY.md`

- [ ] **Step 1: 自动化全量检查**

```powershell
gofmt -l internal/lab
go test ./internal/lab
go test ./...
go build ./...
```

预期：`gofmt -l` 无输出；测试全绿；构建成功。任何失败先修复再继续。

- [ ] **Step 2: 浏览器手动验收**

`go run ./cmd/lab`（注意：本机 PowerShell `Invoke-WebRequest` 走系统代理连不上 127.0.0.1:8765，接口冒烟用 `curl.exe`），逐项核对：

1. 因子页选 `N日动量` 20 日 / 窗口 1 日 / 分组数 7 / 2018–2026（样本可用 random 小样本快速验证），运行完成；
2. 柱状图 7 根柱，X 轴两行标签：`Q1 最低` … `Q7 最高` + 各组值域（ratio 显示如 `[-5.1% ~ -3.2%]`），无标签互相遮挡缺失；
3. 分组明细表 7 行：范围/中位数/均值/样本占比（各≈14.3%）/未来收益，红涨绿跌配色正确；
4. 切回 5 组重跑 → 图表/明细表回到 5 行，占比≈20%；
5. 切 11 组重跑 → 11 根柱全部显示（`interval:0` 生效），窄屏 560px 无页面级横向溢出（明细表在 `.year-wrap` 滚动容器内）；
6. 年度稳定性表列头 `Q1`/`Q7`（随组数）/`Q7-Q1`；meta 行显示"分组：7 组等频"；
7. 旧报告兼容：改坏 `output/factor/<kind>/report.json` 复制一份去掉 `groups` 字段（或直接读取实施前的历史报告备份）→ 刷新页面读取旧报告，柱状图仍可用（轴标签无值域）、明细表隐藏、无 JS 报错；
8. 结果不足场景（如 codes 模式只给 2 只票 + 7 组）→ NA 文案显示"分成 7 组"；
9. Tab1 高级脚本/简单配置回测、Tab2/3 无回归（跑一次任意小样本回测）；
10. 浏览器 console 全程无报错。

- [ ] **Step 3: 更新设计文档状态**

`docs/superpowers/specs/2026-09-17-factor-analysis-group-counts-design.md` 头部状态改为：

```
> 状态：已实施并验证（2026-09-17）
```

- [ ] **Step 4: 更新 MEMORY.md**

在"因子分析时间范围扩展"条目之后追加（合并入既有因子框架叙事，只记录长期契约与坑点）：

```markdown
- **因子分析分组数扩展与值域展示（2026-09-17，已实现并验证）**：设计见 `docs/superpowers/specs/2026-09-17-factor-analysis-group-counts-design.md`，计划见 `docs/superpowers/plans/2026-09-17-factor-analysis-group-counts.md`。`GroupingConfig.Groups`（json `groups`，0=缺省 5，校验 2–20）由 `groupCount()` 统一解析，`quantileAssign(obs,g)`/`summarizeQuintiles(qs,n)`/`aggregateGroups` 全部参数化组数；bins 模式断点数 = 组数−1（缺省 5 组 4 断点，旧合同兼容）。报告 `groups`/`quintiles` 镜像/`years[].quintiles` 长度均为组数 N，`AnalysisVersion` 保持 2（纯长度泛化无需版本迁移）。前端因子页分组数下拉 5/7/10/11；结果区优先消费 v2 `groups`（柱状图轴标签两行含值域 `[min~max]`、分组明细表：范围/中位数/均值/占比/收益），旧报告降级读 `quintiles`（无值域、明细表隐藏）；`fmtFactorVal(v,unit)` unit 感知（ratio×100%、multiple×、score/correlation 原值，`sig3`=3 位有效数字去尾零，`toPrecision` 后必须 `Number()` 回转避免 0.123×100=12.299999… 尾差）；年度表首末组列头动态（`qSpreadHead`）。坑点：分位组值域为全样本累计分布，相邻组 min/max 可重叠（边界每日变化），文案不得称"固定区间"。
```

- [ ] **Step 5: 最终提交**

```powershell
git add docs/superpowers/specs/2026-09-17-factor-analysis-group-counts-design.md docs/superpowers/plans/2026-09-17-factor-analysis-group-counts.md MEMORY.md
```

`.git/fx/_msg.txt` 内容：

```
docs(lab): 因子分析分组数扩展设计/实施计划入库并同步项目记忆
```

按约定走 `_fixtree.ps1` 提交，`git log --oneline -3` 验证。

---

## 自查记录（计划编写后）

1. **Spec 覆盖**：设计 §4（后端配置/纯函数/报告合同/API）→ Task 1–3；§5.1–5.6（前端全部）→ Task 4–6；§6（测试）→ Task 1–3 与 Task 7 验证；§7（兼容）→ 缺省路径既有测试（Task 2 Step 4）+ 旧报告降级（Task 5 Step 4 + Task 7 Step 2.7）。无缺口。
2. **占位符扫描**：所有代码步骤含完整代码；无 TBD/TODO/"适当处理"。
3. **类型一致性**：`groupCount()`/`quantileAssign(obs, g)`/`summarizeQuintiles(qs, n)` 在 Task 1/2/3 间签名一致；前端 `renderGroupChart(gs, unit)`/`renderGroupDetail(gs, unit)`/`fmtFactorVal(v, unit)`/`groupRangeLabel(g, unit)`/`sig3(v)` 在 Task 5 内定义、Task 5/6 使用一致；`#qSpreadHead` 在 Task 6 Step 1 定义、Step 2 使用。
