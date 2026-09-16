# 因子 Web 入门工作流实施计划

> 日期：2026-09-16
>
> 状态：Ready for implementation
>
> 上游设计：`docs/superpowers/specs/2026-09-16-factor-web-mvp-design.md`
>
> 实施边界：本文只规划 MVP，不在本阶段实现 TopN、多因子评分、组合权重、调仓、策略库 CRUD 或成交模型调整。

## 1. 交付目标

把现有“编辑 Go/Yaegi 脚本后运行”的主要使用方式，扩展成一个不要求用户理解代码的 Web 入门流程：

1. 在“因子研究”中用通俗说明理解一个观察指标。
2. 查看该指标从低到高五组对应的未来收益表现。
3. 逐个把 2～4 个区间条件加入预设买入策略，全部条件固定为 AND。
4. 由后端构造基准、各单条件和全部条件组合 N+2 个 `core.Variant`。
5. 在同一批数据、同一卖出规则下运行，主比较基准/组合增强，并展示单条件归因。
6. 保留现有脚本编辑器作为“高级模式”，不删除、不改写脚本文件。

本计划的核心不是在 Web 中复制一套策略引擎，而是让 Web 成为既有接口的配置入口：

```text
Web 表单
  │
  ▼
StrategySpec（可审计、可校验的声明式配置）
  ├─ BasePresetID ──> 预设工厂 ──> core.Buyer
  ├─ FactorFilters ─> factor.Build + 多个 buy.A因子过滤（固定 AND）
  └─ Exit          ──> RunConfig.seller() ──> core.Seller
                              │
                              ▼
               []core.Variant（基准 / 各单条件 / 组合增强）
                              │
                              ▼
                  lab.Runner → researchrun → core.Backtest
```

## 2. 不可突破的边界

### 2.1 核心执行契约不变

- 不修改 `core/types.go` 中受保护的 `Buyer`、`Seller`、`Trade`、`Buy`、`Klines`。
- 不在回测引擎中硬编码因子、止盈、止损或持有期判断。
- 买入条件仍通过 `core.Buyer` 组合；卖出/风控仍由 `core.Seller` 和 `sell.Or` 组合。
- 不复制 `researchrun` 的数据加载、并发执行、K 线隔离或 Coverage 逻辑。
- 不让浏览器生成 Go 源码，也不让简单模式写入 `strategies/script/matrix.go`。

### 2.2 数据与口径不变

- 因子 API 使用原始值。例如动量 `0.05` 表示 5%，不是 `5`。
- 前端只做显示单位换算；提交给后端前必须转换回原始值。
- 因子研究中的未来收益只是评价标签，不进入 Buyer，不允许前视。
- 当前“当日收盘信号/成交”的执行口径继续沿用，但必须在运行前和报告中显式提示。
- 简单模式和高级模式共用 `/api/status`、`/api/stop`、报告存储和单任务互斥。

### 2.3 本轮明确不做

- 不做因子 TopN 选股界面。
- 不做多因子权重、标准化、打分或自动选阈值；条件组合只支持 AND。
- 不做资金组合、仓位权重、调仓日历或组合净值。
- 不做策略保存、复制、发布、权限和版本库。
- 不调整数据源、成本、滑点、T+1 或成交模拟。
- 不新增前端框架、状态库或构建链；继续使用嵌入式单页 HTML/CSS/JavaScript。

## 3. 当前实现映射

| 能力 | 当前事实 | MVP 处理方式 |
|---|---|---|
| 回测配置 | `internal/lab/runner.go:RunConfig` | 继续作为执行配置，由 `StrategySpec` 转换得到 |
| Buyer/Seller | `core.Buyer`、`core.Seller` | 保持接口不变，只增加配置到组合对象的构建层 |
| 回测执行 | `Runner.Start(cfg, variants)` → `researchrun.Run` | 抽出可携带简单模式元数据的内部启动路径，不复制执行循环 |
| 因子目录 | `strategies/factor/registry.go` | 补充面向 Web 的解释性元数据 |
| 因子分析 | `internal/lab/analysis.go` | 增加 Coverage 和后端生成的五组摘要 |
| 高级脚本 | `/api/script`、`/api/run` | 原样保留，标记为高级模式 |
| 页面 | `internal/lab/web/lab/index.html` | 在同一页加入简单模式、概念说明和基准/组合增强对照 |
| 报告 | `internal/lab.Report` | 向后兼容地增加 source/spec/comparison 字段 |

已确认的一个现状问题：`core.TradeStats` 没有 JSON tag，而页面使用 `stats.total` 等小写字段。MVP 不修改 `core.TradeStats`，改为在 `internal/lab` 定义带 camelCase JSON tag 的 Web DTO，并从核心统计结果转换。

## 4. MVP 合同冻结

实施开始后，除非发现与真实代码冲突，下列合同不再临时扩展。

### 4.1 预设策略

首版只暴露当前 `strategies/script/matrix.go` 已经验证过的四个 Buyer 组合，确保简单模式与高级脚本有可比基线：

| ID | 展示名 | 既有 Buyer 组合 |
|---|---|---|
| `pullback_ma5_up` | MA5 向上 · 收回 MA5 | 市值 + 价格 + 非涨停 + MA5 趋势下收回 MA5 |
| `pullback_ma10_up` | MA5 向上 · 收回 MA10 | 市值 + 价格 + 非涨停 + MA5 趋势下收回 MA10 |
| `pullback_ma5_bull` | 多头排列 · 收回 MA5 | 市值 + 价格 + 非涨停 + 5/10/20 多头排列下收回 MA5 |
| `pullback_ma5_plain` | 无趋势 · 收回 MA5 | 市值 + 价格 + 非涨停 + 无趋势约束收回 MA5 |

每次构建必须返回新的 Buyer 组合，不共享可变 slice。API 只返回 ID、名称、说明和规则摘要，不序列化函数或具体 Go 类型。

### 4.2 StrategySpec v1

服务端模型建议固定如下；字段名是 API 契约，不直接复用页面 DOM 名称：

```go
type StrategySpec struct {
	Version       int                `json:"version"`
	Name          string             `json:"name"`
	BasePresetID  string             `json:"basePresetId"`
	FactorFilters []FactorFilterSpec `json:"factorFilters"`
	Exit          ExitSpec           `json:"exit"`
	Run           StrategyRunSpec    `json:"run"`
}

type FactorFilterSpec struct {
	Kind     string   `json:"kind"`
	Days     int      `json:"days"`
	Operator string   `json:"operator"` // gte | lte | between
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
}

type ExitSpec struct {
	HoldingDays int     `json:"holdingDays"`
	TakeProfit  float64 `json:"takeProfit"` // 原始比例，0.10 = 10%
	StopLoss    float64 `json:"stopLoss"`   // 原始比例
}

type StrategyRunSpec struct {
	StartYear   int      `json:"startYear"`
	EndYear     int      `json:"endYear"`
	SampleMode  string   `json:"sampleMode"`
	SampleSize  int      `json:"sampleSize,omitempty"`
	SampleCodes []string `json:"sampleCodes,omitempty"`
}
```

v1 校验规则：

- `version` 必须为 `1`。
- `basePresetId` 必须在预设目录中。
- `factorFilters` 必须有 2～4 项；服务端不接受空数组、单项或超过上限。
- 每项 `kind` 必须能由 `factor.Build` 构造，`days > 0`。
- 相同 `kind + days` 拒绝重复，不同窗口的同类因子允许并存。
- `gte` 只要求 `min`；`lte` 只要求 `max`；`between` 同时要求 `min/max` 且 `min <= max`。
- 阈值必须是有限数，拒绝 NaN 和正负无穷。
- 单边区间在构造 `buy.A因子过滤` 时映射为既有约定的 `-1e9` 或 `+1e9`。
- 年份、样本和卖出规则继续委托 `RunConfig.Validate()`，不维护第二套规则。
- `name` 为空时由后端生成可读名称；非空时 trim，并限制为 80 个 Unicode 字符以内。

配置 N 个条件时，变体顺序固定：

1. `基准 · {预设名称}`
2. `单条件 1 · {因子名称与区间}`
3. 依照 `factorFilters` 数组顺序生成其余单条件变体
4. `组合增强 · {预设名称} · {N个条件}`

总数恒为 N+2。顺序是报告对比合同，前端不得重新推断谁是基准、单条件或组合增强。最终组合使用 `buy.And{base, filter1, ..., filterN}`；单条件变体只用于解释，不代表推荐策略。

### 4.3 API

新增两个端点：

```text
GET  /api/strategy-presets
POST /api/strategy/run
```

`GET /api/strategy-presets` 返回：

```json
[
  {
    "id": "pullback_ma5_up",
    "name": "MA5 向上 · 收回 MA5",
    "description": "回调到 5 日均线附近且短期趋势向上",
    "rules": ["流通市值 ≥ 20", "价格 2～120", "过滤涨停", "MA5 向上"]
  }
]
```

`POST /api/strategy/run` 接收 `StrategySpec`。响应保持轻量：

```json
{"ok": true, "variants": 4, "source": "simple"}
```

错误状态约定：

- JSON 解码或 StrategySpec 校验失败：`400`。
- 已有回测或分析任务运行：`409`。
- 服务端意外错误：`500`。
- 错误体继续使用 `{"error":"..."}`。

原有 `/api/run` 继续表示高级脚本运行，不改变请求和响应。

### 4.4 报告扩展

`Report` 兼容增加：

```go
Source       string             `json:"source"` // script | simple
StrategySpec *StrategySpec      `json:"strategySpec,omitempty"`
Comparison   *ComparisonSummary `json:"comparison,omitempty"`
```

`ComparisonSummary` 只包含后端计算且不会被页面误解的字段：

```go
type ComparisonSummary struct {
	BaselineVariant string   `json:"baselineVariant"`
	CombinedVariant string   `json:"combinedVariant"`
	BaselineTrades  int      `json:"baselineTrades"`
	CombinedTrades  int      `json:"combinedTrades"`
	RetentionRate   *float64 `json:"retentionRate"` // 基准为 0 时为 null
	FactorVariants  []FactorVariantSummary `json:"factorVariants"`
}

type FactorVariantSummary struct {
	Index         int      `json:"index"`
	Kind          string   `json:"kind"`
	Days          int      `json:"days"`
	Variant       string   `json:"variant"`
	Trades        int      `json:"trades"`
	RetentionRate *float64 `json:"retentionRate"`
}
```

组合和各单条件的胜率、平均单笔收益、盈亏比等继续来自各变体的统计 DTO。MVP 不把逐笔收益率简单累加伪装成组合净值；现有图表若保留，标题必须明确为“逐笔收益率累计（非资金曲线）”。

### 4.5 因子目录扩展

`CatalogEntry` 在保留现有字段的基础上增加：

```go
Category       string `json:"category"`
ParameterLabel string `json:"parameterLabel"`
DefaultDays    int    `json:"defaultDays"`
Unit           string `json:"unit"`    // ratio | multiple | score | correlation
Example        string `json:"example"`
```

14 个已注册因子必须全部补齐元数据。示例必须解释原始值，不给出“推荐阈值”。例如：

```text
N日动量原始值 0.05，表示近 N 日上涨 5%；-0.05 表示下跌 5%。
```

### 4.6 因子分析报告扩展

`AnalysisReport` 增加：

```go
Coverage researchrun.Coverage `json:"coverage"`
Summary  QuintileSummary      `json:"summary"`
```

`QuintileSummary` 由后端根据五组结果生成：

```go
type QuintileSummary struct {
	Direction string   `json:"direction"` // ascending | descending | mixed | flat | insufficient
	Spread    *float64 `json:"spread"`    // Q5 - Q1；无五组数据时为 null
	Monotonic bool     `json:"monotonic"`
}
```

该摘要只描述样本结果，不使用“有效”“显著”“可交易”等结论。IC、标准差和 t 值保留在“统计详情”折叠区域。

## 5. 文件级实施清单

### 新增

- `DESIGN.md`
- `premium-ui.json`
- `internal/lab/presets.go`
- `internal/lab/presets_test.go`
- `internal/lab/strategy_spec.go`
- `internal/lab/strategy_spec_test.go`

### 修改

- `strategies/factor/registry.go`
- `strategies/factor/registry_test.go`
- `internal/lab/analysis.go`
- `internal/lab/analysis_run_test.go`
- `internal/lab/runner.go`
- `internal/lab/runner_test.go` 或现有最接近的 runner 测试文件
- `internal/lab/server.go`
- `internal/lab/server_test.go`
- `internal/lab/factor_e2e_test.go`
- `internal/lab/web/lab/index.html`
- `MEMORY.md`

### 不修改

- `core/types.go`
- `core/backtest.go`
- `internal/researchrun/*`
- `strategies/script/matrix.go`
- 数据源、成本、成交和仓位配置

## 6. 分阶段实施任务

任务必须按顺序执行。同一文件 `internal/lab/web/lab/index.html` 的两个前端任务不可并行。

### Task 1：冻结 Web 视觉与控件所有权

**文件：**

- 新建 `DESIGN.md`
- 新建 `premium-ui.json`

**步骤：**

1. 从现有页面提取颜色、字号、间距、边框和信息密度，写入根目录 `DESIGN.md`。
2. 记录本页是“桌面优先的本地研究工具”，不是营销网站。
3. 明确控件所有权：
   - Tab 为应用自有组件，必须支持 `role=tablist/tab/tabpanel`、方向键和焦点管理。
   - Select 保持原生控件；MVP 不实现自定义下拉。
   - 年份使用 `input[type=number]`；交易明细日期筛选保持原生日期输入。
   - 表单错误和异步状态由应用呈现，不能只依赖浏览器原生校验气泡。
4. 在 `premium-ui.json` 中把审计源目录限定到 `internal/lab/web/lab`，记录 `zh-CN`、原生 Select/Date 决策和应用拥有的 Tab/Validation/Status。
5. 运行 UI 审计作为基线；把现有问题分为“本次必须修复”和“范围外既有问题”，不得用扩大全仓库扫描制造无关阻塞。

**验收：**

- 设计上下文能解释后续页面为什么使用当前紧凑桌面布局。
- 审计配置只扫描 Lab 页面。
- 没有修改业务代码。

### Task 2：补齐因子目录的人类可读元数据

**文件：**

- 修改 `strategies/factor/registry.go`
- 修改或新建 `strategies/factor/registry_test.go`
- 修改 `internal/lab/factor_e2e_test.go`

**先写失败测试：**

1. `All()` 仍返回 14 项，Kind 唯一且顺序稳定。
2. 每项 `category/parameterLabel/defaultDays/unit/example` 均满足合同。
3. `defaultDays` 与 `Build(kind, 0)` 使用的默认值一致。
4. 单位只能是 `ratio/multiple/score/correlation`。
5. `/api/factors` 返回新字段，同时保留 `kind/name/description`。

**实现：**

1. 扩展内部 `entry`，让默认窗口和展示元数据只有一个来源。
2. `All()` 复制为 API DTO，不暴露构造函数。
3. 为 14 个因子逐项写清原始值示例。
4. 不在目录中加入阈值推荐、评级或经验收益。

**验证：**

```powershell
go test ./strategies/factor ./internal/lab -run 'Test.*(Registry|Factors)'
```

### Task 3：为因子分析补 Coverage 和五组摘要

**文件：**

- 修改 `internal/lab/analysis.go`
- 修改 `internal/lab/analysis_run_test.go`
- 修改 `internal/lab/factor_e2e_test.go`

**先写失败测试：**

1. 正常数据得到 `Coverage{Requested:2, Completed:2, Skipped:0}`。
2. 一只股票加载失败时，报告明确记录 Skipped 和 Failure，不能静默减少样本。
3. 五组严格递增、递减、混合、相等和不足五组时，Summary 分别返回正确枚举。
4. `spread == Q5-Q1`；无五组数据时为 `null`。
5. report.json 与 `/api/analysis/latest` 都包含 Coverage 和 Summary。

**实现：**

1. 在 `runAnalysis` 调用 `researchrun.ForEachCodeData` 时，利用串行 `OnCodeDone` 汇总 Coverage。
2. Failure 复制进报告并按 code/year/stage 排序，保证测试和产物稳定。
3. 增加纯函数 `summarizeQuintiles`；不要在浏览器中重新计算方向或 spread。
4. 保留现有 IC 与五分位统计算法，不借此任务修改统计口径。

**验证：**

```powershell
go test ./internal/lab -run 'Test.*(Analysis|Quintile)'
```

### Task 4：实现预设 Buyer 目录

**文件：**

- 新建 `internal/lab/presets.go`
- 新建 `internal/lab/presets_test.go`

**先写失败测试：**

1. 目录只返回冻结的四个 ID，ID 唯一、顺序稳定。
2. 未知 ID 返回明确错误。
3. 每次 Build 返回非 nil、名称稳定的新 Buyer。
4. 四个 Builder 与当前 `matrix.go` 的 Buyer 参数逐项一致。
5. API DTO 中不出现 Go 类型名、函数或源码。

**实现：**

1. 使用显式 slice + 工厂函数，不使用反射或字符串动态装配。
2. Buyer 仍由 `buy.And` 和现有原子 Buyer 构成。
3. `rules` 文案只描述真实参数；不要创造代码里不存在的业务规则。

**验证：**

```powershell
go test ./internal/lab -run 'Test.*Preset'
```

### Task 5：实现 StrategySpec 校验和 Variant 构建

**文件：**

- 新建 `internal/lab/strategy_spec.go`
- 新建 `internal/lab/strategy_spec_test.go`

**先写失败测试：**

1. version、preset、2～4 项数量、重复 `kind+days`、factor kind、days、operator、有限数和区间边界校验。
2. 每项 `gte/lte/between` 正确映射到 `buy.A因子过滤.Min/Max`。
3. N 个条件固定生成 N+2 个 Variant：基准、N 个单条件、一个组合增强。
4. 所有 Variant 使用相同的基础 Buyer 语义；组合增强只追加 N 个因子过滤且固定 AND。
5. 单条件 Variant 与 `factorFilters` 数组顺序一一对应，名称稳定。
6. `ExitSpec + StrategyRunSpec` 转换得到的 `RunConfig` 通过现有 Validate。
7. 止盈 10% 在 API 中为 `0.10`，不得二次除以 100。
8. StrategySpec JSON round-trip 保留条件顺序，且不丢失 nil 和 0 的区别。

**实现：**

1. `StrategySpec.Validate()` 负责声明式合同。
2. `StrategySpec.RunConfig()` 只做字段转换，把最终合法性留给现有 RunConfig。
3. `StrategySpec.Variants()` 从预设 Builder 取得 Buyer，逐项构建过滤条件，再生成基准、单条件和组合增强变体。
4. 不给 `core.Buyer` 增加序列化能力，不把接口实例写入报告。

**验证：**

```powershell
go test ./internal/lab -run 'Test.*StrategySpec'
```

### Task 6：让 Runner 同时承载简单模式和高级模式元数据

**文件：**

- 修改 `internal/lab/runner.go`
- 修改相关 runner/server 测试

**先写失败测试：**

1. 高级 `/api/run` 的 `source` 为 `script`，旧请求仍可执行。
2. 简单模式报告包含原始 StrategySpec，source 为 `simple`。
3. 简单模式按 StrategySpec 生成 Comparison，包含基准、组合增强和 N 个单条件摘要；基准交易为 0 时 retentionRate 为 null。
4. 高级多变体脚本不生成错误的多条件 Comparison。
5. `TradeStatsJSON` 输出 camelCase；旧报告中的大写字段仍能被 Go 反序列化读取。
6. Coverage、交易明细和报告落盘路径保持原行为。

**实现：**

1. 保留公开 `Runner.Start(cfg, variants)` 作为高级模式入口。
2. 抽出一个接收可选 `*StrategySpec` 的内部 start/run 路径；简单模式调用该路径。
3. 报告保存前由后端生成 Comparison，不让前端按数组位置临时计算。
4. 在 `internal/lab` 增加带 JSON tag 的 `TradeStatsJSON`，由 `core.TradeStats` 转换。
5. 不改 `core.Stats`、`researchrun.Run` 或 `Backtest.Do()`。

**验证：**

```powershell
go test ./internal/lab -run 'Test.*(Runner|Report|StatsJSON)'
```

### Task 7：增加预设与简单运行 API

**文件：**

- 修改 `internal/lab/server.go`
- 修改 `internal/lab/server_test.go`
- 修改 `internal/lab/factor_e2e_test.go`

**先写失败测试：**

1. `GET /api/strategy-presets` 返回四项和完整展示字段。
2. 非法 JSON、未知 preset、少于 2 项、超过 4 项、重复 kind+days、错误 operator、反区间返回 400。
3. 忙碌时返回 409，且不会覆盖正在运行的任务。
4. 含两个条件的合法请求产生四个有稳定顺序的变体，最新报告有 source/spec/comparison。
5. 简单模式不读取、不写入 `ScriptPath`；即使脚本文件不存在也能运行。
6. 现有 `/api/run`、`/api/script`、`/api/analyze` 回归测试继续通过。

**实现：**

1. 注册 `/api/strategy-presets` 和 `/api/strategy/run`。
2. handler 先 decode/validate/build，再进入 Runner；区分 400 和 409。
3. 复用统一 `writeJSON/writeErr` 和现有状态轮询。
4. 更新 `server.go` 顶部 API 契约注释。

**验证：**

```powershell
go test ./internal/lab -run 'TestServer.*(Strategy|Run|Analysis|Script)'
```

### Task 8：重做“因子研究”为概念优先页面

**文件：**

- 修改 `internal/lab/web/lab/index.html`

**实现顺序：**

1. 先把 Tab 标记改成 WAI-ARIA tabs，并实现键盘左右切换、Home/End、焦点和选中态同步。
2. Tab 状态写入 URL 查询参数，例如 `?tab=factor`；刷新后恢复。
3. 因子选择后显示：
   - 中文名和类别；
   - 一句话定义；
   - 参数含义与默认窗口；
   - 原始值示例；
   - “不存在通用好阈值”的提示。
4. 主结果首先显示五组柱状图和后端 Summary 文案。
5. IC、标准差、t 值、逐日 IC 图移动到 `<details>` 的“统计详情”。
6. 显示 Coverage：请求、完成、跳过；失败项可展开查看。
7. 加入“添加到策略条件”动作，把 kind/days 追加到简单策略页但不自动猜测 min/max；相同 kind/days 已存在时聚焦原条件。
8. 为加载中、空结果、失败、任务被停止、结果不足分别提供页面内状态，状态区域使用 `aria-live`。

**前端约束：**

- 所有 API 文本进入 `textContent` 或经过显式 HTML 转义，不能直接把外部字段拼进 `innerHTML`。
- ECharts 初始化必须在容器可见后执行，并在窗口 resize 时调用 `resize()`。
- 使用原生 select，不实现自定义下拉。
- 因子百分比显示可以乘 100，但提交 API 前必须还原为原始比例。

**浏览器验收：**

- 只用键盘可以进入因子 Tab、选择因子、运行、展开统计详情和转入策略页。
- 刷新后保留当前 Tab，但不自动重跑任务。
- 无 quintiles 时不画空柱，不显示 NaN/undefined。
- Coverage skipped > 0 时页面有醒目但不夸张的提示。

### Task 9：增加多条件简单策略和组合对照

**文件：**

- 继续修改 `internal/lab/web/lab/index.html`

**实现顺序：**

1. 在第一 Tab 增加“简单配置 / 高级脚本”模式切换，默认简单配置。
2. 简单配置包含：预设策略、2～4 个因子条件、卖出规则、年份和样本。
3. 默认渲染两张编号条件卡，卡片间显示“并且（AND）”；支持添加、删除，不提供拖拽、OR 或嵌套。
4. 每个条件展示原始值含义；ratio 类型用百分比输入，但请求发送原始比例。
5. 页面提交前校验条件数量、重复 kind+days、必填、区间、年份、样本和至少一个卖出规则；错误贴近字段展示并聚焦第一个错误。
6. 达到四条后禁用“添加条件”并解释上限；删除后把焦点移到相邻条件或“添加条件”。
7. 运行前显示固定口径提示：信号和成交使用当前回测模型，未来收益只用于因子评价。
8. 简单模式调用 `/api/strategy/run`；高级模式继续保存脚本后调用 `/api/run`。
9. 回测完成后读取服务端 Comparison：
   - 基准交易数；
   - 组合增强交易数和信号保留率；
   - 各单条件的交易数、信号保留率、胜率、平均单笔收益和盈亏比；
   - 组合与基准的胜率、平均单笔收益、盈亏比；
   - Coverage。
10. 把现有“累计收益率”图明确改名为“逐笔收益率累计（非资金曲线）”，或者在简单模式隐藏；不得称为组合净值。
11. 高级 textarea 使用固定且足够的高度，移除用户可拖拽导致布局不稳定的 resize；如需要更大视图，提供显式展开/收起按钮。
12. 重载最新报告时，根据 `source` 恢复正确的结果说明，不把高级多变体脚本误当成多条件组合。

**浏览器验收：**

- 从因子页点击“添加到策略条件”后，简单配置新增相同 kind/days，阈值仍为空；重复添加不会产生重复卡片。
- 使用动量 20 日 `0.05～0.30` 加波动率 20 日 `≤0.03` 时，页面分别显示 5%～30% 和 ≤3%，请求体保持原始比例。
- 两个条件提交后生成四个变体，主结果突出基准/组合增强，单条件结果位于条件贡献区域。
- 简单模式不触发任何 `/api/script` PUT。
- 运行中两个入口都禁用，停止按钮有效；完成后自动进入对照结果。
- 1280×720 与 1440×900、100% 缩放下无重叠、截断或横向页面滚动。

### Task 10：端到端回归、UI 审计和项目记忆

**文件：**

- 修改必要的测试文件
- 修改 `MEMORY.md`

**自动化验证：**

```powershell
gofmt -w internal/lab/presets.go internal/lab/presets_test.go internal/lab/strategy_spec.go internal/lab/strategy_spec_test.go internal/lab/analysis.go internal/lab/analysis_run_test.go internal/lab/runner.go internal/lab/server.go internal/lab/server_test.go internal/lab/factor_e2e_test.go strategies/factor/registry.go strategies/factor/registry_test.go
go test ./strategies/factor ./internal/lab
go test ./...
go build ./...
```

运行已安装的 Premium UI 审计，严格模式必须通过；如果执行环境缺少审计工具，应明确记录“未验证”，不能当作通过。

**浏览器完整路径：**

1. 启动 Lab 服务，打开首页。
2. 因子研究选择 N 日动量，确认概念卡的 `0.05 = 5%` 示例。
3. 用小样本运行分析，确认五组图、统计详情和 Coverage。
4. 分别把两个因子带入简单策略，手工输入区间并确认 AND 摘要。
5. 运行基准/组合增强对照，确认报告 source/spec/comparison 和 N+2 个变体。
6. 打开交易明细，确认基准、两个单条件和组合增强都可以筛选。
7. 切到高级脚本，确认读取、校验和运行仍工作。
8. 刷新页面，确认 Tab 恢复、不会重复发起任务。
9. 使用键盘完成 Tab 切换、表单提交、折叠区域和停止操作。

**最终检查：**

- `git diff --check` 无空白错误。
- `git status --short` 只有本计划列出的文件。
- 不包含生成产物、output 报告、临时数据库、截图或缓存。
- `MEMORY.md` 只记录已经实现并验证的长期事实；不能把计划写成已完成。
- 未经用户明确要求不执行 `git commit`。

## 7. 测试矩阵

| 层级 | 必测内容 | 失败时含义 |
|---|---|---|
| 纯函数 | StrategySpec 校验、区间映射、五组摘要、统计 DTO | API/报告合同不可靠，停止进入 UI |
| 组件 | 预设 Builder 与 factor registry | Web 配置无法稳定映射现有 Buyer/Factor |
| API | presets、strategy/run、status、report、analysis | 前后端合同不完整 |
| 执行 | 基准、各单条件、组合增强同样本同 Seller，Coverage 可见 | 对照不可解释或存在静默丢数据 |
| 回归 | 旧 script run、TopN、analysis | MVP 破坏高级模式或已有因子功能 |
| 浏览器 | 键盘、刷新、错误态、两种视口 | Web 入口不可用或状态不可信 |
| 全仓库 | `go test ./...`、`go build ./...` | 共享契约或编译受影响 |

## 8. 兼容与回滚策略

- 新报告字段均为新增字段；旧报告读取时允许 `source` 为空并按 `script` 展示。
- `TradeStatsJSON` 反序列化应兼容旧报告的 Go 默认大写键，避免历史报告失读。
- `/api/run` 不改语义；出现问题时可隐藏简单模式入口，高级脚本仍可工作。
- 简单模式不改脚本文件，因此回滚 Web/API 不会覆盖用户策略脚本。
- 不迁移或重写已有 `output/trades`、`output/factor`。
- 如果预设参数需要变化，应新增 preset ID 或显式版本，而不是悄悄改变已有 ID 的语义。

## 9. 实施停止条件

出现以下任一情况，必须停止扩张并先确认：

- 需要修改 `core/types.go` 的受保护结构或接口。
- 需要改变 Backtest 成交时点、成本、滑点、T+1 或分钟线处理。
- 需要给 StrategySpec 增加 TopN、权重评分、OR/嵌套条件或调仓语义。
- 预设策略无法与当前 `matrix.go` 保持参数一致。
- 浏览器需要新增 npm 前端框架或生产依赖。
- 简单模式无法复用 Runner/researchrun，必须复制执行循环。
- 因子分析 Coverage 无法从现有 `researchrun.Progress` 准确取得。

## 10. 完成定义

只有同时满足下列条件，才能称为“因子 Web MVP 实施完成”：

1. 用户不改代码即可完成逐个因子理解、2～4 条 AND 组合和基准/组合增强回测。
2. Web 配置最终仍映射为现有 `core.Buyer/core.Seller/core.Variant`，没有第二套策略语义。
3. 因子原始值、未来收益标签、样本 Coverage 和成交口径均可见。
4. 高级 Yaegi 脚本路径完整保留且回归通过。
5. 所有新增 API 有 httptest，核心映射有单元测试，完整路径有端到端测试。
6. 目标 Go 测试、全仓测试、构建、UI 审计和浏览器验收都有真实结果记录。
7. 文档、代码和 `MEMORY.md` 对“已实现/未实现”的描述一致。
