# 因子分组可解释性与固定区间研究实施计划

> 日期：2026-09-16
>
> 状态：Ready for implementation
>
> 上游设计：`docs/superpowers/specs/2026-09-16-factor-web-mvp-design.md`
>
> 适用范围：Strategy Lab 的单因子研究、分析报告和导出；不改变回测成交模型

## 1. 背景与问题

当前“因子研究”按每日横截面因子值升序等分五组，展示 Q1～Q5 的未来平均收益。
该视图能够回答“因子排序是否具有区分能力”，但不能回答用户紧接着需要的问题：

1. Q1～Q5 分别对应什么原始因子值；
2. 对于均线偏离，五组大致对应多少偏离率；
3. 分位组每天重算时，图上的区间是不是固定阈值；
4. 如果需要把研究结果用于 `A因子过滤`，应如何研究具体阈值。

当前报告只保存：

- 五组未来收益 `quintiles`；
- 五组方向摘要 `summary`；
- IC、Coverage 和逐日 IC。

它没有保存每组原始因子值的中位数、分布范围、样本数和日期覆盖，因此前端无法补出
可靠解释。这是数据合同缺口，不是仅靠修改文案可以解决的问题。

成熟因子工具通常区分两类分析：

- **分位数组（quantiles）**：每个截面按排名分组，用于检验排序能力；
- **固定区间组（bins）**：使用明确的原始数值断点，用于研究可执行阈值。

Alphalens 同时支持 `quantiles` 和 `bins`，并在摘要中输出各分位组的
`min/max/mean/std/count/count %`。本计划沿用这一边界，同时针对本项目 Web 用户补充
中位数与四分位范围，避免极值主导解释。

## 2. 交付目标

1. 五组收益旁同时展示每组的原始因子值分布，让用户知道每组“大致是什么数值”。
2. 明确分位组是每日动态边界，不能伪装成永久有效的策略阈值。
3. 新增固定区间研究模式，让用户用四个明确断点形成五组并研究实际阈值。
4. 所有因子共用一套分组和展示逻辑，不写死“均线上方/下方”等单因子文案。
5. 根据因子单位正确展示百分比、倍数、分数和相关系数，请求与报告仍保存原始值。
6. 保持旧 `/api/analyze` 请求、旧 `report.json` 和现有因子分析结果页面可读取。
7. 分组、统计和导出必须确定性可复现，不因 Go map 遍历顺序改变结果。

## 3. 明确不做

- 不自动推荐“最佳阈值”，不使用全样本收益反向挑选最优断点；
- 不把分组未来收益描述为可实现的策略收益；
- 不修改 `core.Factor`、`core.Buyer`、`core.Seller` 或受保护结构；
- 不改变未来收益的收盘到收盘口径、回测成交时点、成本、滑点或 T+1；
- 不增加多因子权重、标准化、TopN、行业中性化或组合调仓；
- 不新增 npm、前端框架或生产依赖；
- 不把比例因子的显示值 `5%` 作为 API 原始值 `5` 保存，API 始终使用 `0.05`；
- 不在本任务中清理或重构 `internal/lab/web/lab/index.html` 的无关区域。

## 4. 核心概念与口径

### 4.1 分位组回答排序问题

分位模式继续按每个交易日的横截面因子值从低到高分组：

```text
Q1 = 当日因子值最低的一组
...
Q5 = 当日因子值最高的一组
```

目标每组约占当日有效股票的 20%。组边界每天随横截面分布变化，因此历史汇总得到的
Q1～Q5 因子范围可能重叠。页面必须把它称为“历史分布”，不得称为“固定选股区间”。

### 4.2 固定区间组回答阈值问题

固定区间模式由用户输入四个严格递增的原始值断点 `b1 < b2 < b3 < b4`，形成五组：

```text
B1 = (-∞, b1]
B2 = (b1, b2]
B3 = (b2, b3]
B4 = (b3, b4]
B5 = (b4, +∞)
```

例如均线偏离在页面输入 `-5%、-2%、0%、2%`，请求体保存
`[-0.05, -0.02, 0, 0.02]`。固定区间在所有日期保持不变，允许某个日期某组为空。

### 4.3 因子值和未来收益必须分栏

- 因子值统计描述“这一组是什么”；
- 未来收益统计描述“这一组后来表现如何”；
- 两者不能共用一个“范围”字段，也不能把未来收益区间误写成因子区间。

### 4.4 分组结果不是策略收益

未来收益继续使用当前实现：

```text
future_return = close[t+Window] / close[t] - 1
```

它不包含成交延迟、手续费、滑点、涨跌停、停牌和持仓重叠处理。页面和导出报告必须保留
“用于因子评价，不代表可实现组合收益”的说明。

## 5. 当前实现映射

| 能力 | 当前位置 | 本计划处理 |
|---|---|---|
| 分组纯函数 | `internal/lab/analysis.go:quintileMeans` | 重构为带分组明细的确定性纯函数 |
| 分析配置 | `internal/lab/analysis.go:AnalyzeConfig` | 新增可选 `grouping`，缺省保持五分位 |
| 分析报告 | `internal/lab/analysis.go:AnalysisReport` | 新增版本、因子快照、分组配置和 `groups` |
| 分析主循环 | `internal/lab/analysis.go:runAnalysis` | 同时累计收益与原始因子值统计 |
| API | `POST /api/analyze`、`GET /api/analysis/latest` | 路径不变，合同只做向后兼容扩展 |
| JSON/HTML/CSV | `internal/lab/analysis.go` | 保留旧产物，新增分组统计表/CSV |
| 因子元数据 | `strategies/factor/registry.go` | 复用 `unit/description/example`，不增加单因子硬编码 |
| Web | `internal/lab/web/lab/index.html` | 增加模式切换、分组统计表和单位格式化 |
| 测试 | `analysis_test.go`、`analysis_run_test.go`、`factor_e2e_test.go` | 扩展纯函数、报告和 API 端到端覆盖 |

## 6. 数据合同冻结

### 6.1 AnalyzeConfig 向后兼容扩展

建议模型：

```go
type AnalyzeConfig struct {
	RunConfig
	Kind     string         `json:"kind"`
	Days     int            `json:"days"`
	Window   int            `json:"window"`
	Grouping GroupingConfig `json:"grouping,omitempty"`
}

type GroupingConfig struct {
	Mode string    `json:"mode,omitempty"` // quantile | bins
	Cuts []float64 `json:"cuts,omitempty"` // bins 模式必须恰好 4 个
}
```

兼容规则：

- `grouping` 缺失或 `mode==""`：按 `quantile` 处理；
- `quantile`：`cuts` 必须为空；
- `bins`：`cuts` 必须恰好四个、全部有限、严格递增；
- 未知 `mode` 返回 409，与现有分析配置校验失败口径一致；
- 首版固定五组，不暴露任意组数，避免报告和 UI 同时泛化。

请求示例：

```json
{
  "startYear": 2022,
  "endYear": 2025,
  "sampleMode": "random",
  "sampleSize": 300,
  "kind": "ma_bias",
  "days": 20,
  "window": 1,
  "grouping": {
    "mode": "bins",
    "cuts": [-0.05, -0.02, 0, 0.02]
  }
}
```

### 6.2 报告新增字段

保留现有 `quintiles` 和 `summary`，新增以下合同：

```go
type FactorSnapshot struct {
	Kind           string `json:"kind"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	ParameterLabel string `json:"parameterLabel"`
	Days           int    `json:"days"`
	Unit           string `json:"unit"`
}

type FactorGroupStats struct {
	Index         int      `json:"index"`         // 1..5
	Label         string   `json:"label"`         // Q1..Q5 或 B1..B5
	Lower         *float64 `json:"lower"`         // 固定区间边界；开放端为 null
	Upper         *float64 `json:"upper"`
	FactorMin     *float64 `json:"factorMin"`
	FactorP25     *float64 `json:"factorP25"`
	FactorMedian  *float64 `json:"factorMedian"`
	FactorMean    *float64 `json:"factorMean"`
	FactorP75     *float64 `json:"factorP75"`
	FactorMax     *float64 `json:"factorMax"`
	FactorStd     *float64 `json:"factorStd"`
	Observations  int      `json:"observations"`  // 股票×日期有效观测数
	Dates         int      `json:"dates"`         // 该组非空日期数
	CountPct      float64  `json:"countPct"`      // 占全部有效观测比例，0..1
	ForwardReturn *float64 `json:"forwardReturn"` // 每日组均值再按日期等权
}
```

`AnalysisReport` 新增：

```go
AnalysisVersion int                `json:"analysisVersion"`
Factor          FactorSnapshot     `json:"factor"`
Grouping        GroupingConfig     `json:"grouping"`
Groups          []FactorGroupStats `json:"groups"`
```

约束：

- 新报告 `analysisVersion=2`；
- 数值不存在时使用 JSON `null`，禁止用 `NaN`、`+Inf`、`0` 冒充；
- `quintiles` 在分位模式继续输出五个收益值，供旧页面/旧报告兼容；
- 固定区间模式以 `groups` 为唯一分组结果，`quintiles` 输出 `null`；
- `summary` 对两种模式都基于五组 `ForwardReturn` 计算；任一组无收益时返回
  `insufficient`，`spread=null`；
- 历史报告没有 `groups` 时，前端退回旧五组柱状图并提示“旧报告未保存因子值范围”。

### 6.3 分位分组的并列值规则

当前实现从 map 取值后只按因子值排序；因子值相同时，组归属可能受 map 遍历顺序影响。
实施时必须消除该不确定性，并避免相同因子值被人为拆到不同组后产生虚假收益差异。

冻结规则：

1. 输入观测先按 `FactorValue` 升序，再按 `Code` 升序，确保输出稳定；
2. 相同 `FactorValue` 的连续观测视为一个 tie block；
3. tie block 按其排序位置中点归入一个组，整个 block 不拆分；
4. 因此实际组占比允许偏离 20%，极端离散因子可能不足五个有效组；
5. 五组不完整时返回 `insufficient`，不为了画满五根柱子伪造区分度；
6. 对离散值或大量零值因子，页面提示优先使用固定区间模式。

分组中点公式以零基位置 `i..j` 和样本数 `n` 计算：

```text
group = floor(((i+j)/2) * 5 / n)
```

实际实现应使用整数倍增形式避免浮点边界误差，并把结果限制在 `0..4`。

### 6.4 固定区间边界规则

- 使用四个严格递增断点；
- 断点值归入左侧组，即 `v == b1` 属于 B1；
- `-0` 与 `0` 视为同一个数值，重复断点校验失败；
- `NaN` 和正负无穷禁止作为断点；
- 因子值本身为 `NaN/Inf` 时继续按现有逻辑剔除；
- 某组为空时保留该组，统计字段和未来收益为 `null`，不能挪用相邻组数据。

### 6.5 统计聚合口径

每个组同时维护两套聚合：

1. **原始因子值分布**：汇总全部股票×日期观测，用于
   `min/p25/median/mean/p75/max/std/count`；
2. **未来收益**：先计算每个日期该组的股票平均收益，再对有效日期等权平均，保持当前
   五组收益口径，避免股票数量较多的日期获得更高权重。

`CountPct = Observations / 全部五组 Observations`。

百分位使用确定性的 Type-7 线性插值，与常见数据分析工具默认行为一致：

```text
h = (n-1) * p
q(p) = x[floor(h)] + (h-floor(h)) * (x[ceil(h)]-x[floor(h)])
```

总体标准差继续除以 `n`，与现有 IC 总体标准差风格一致。所有统计只接收有限值。

### 6.6 单位格式化

后端始终保存原始数值；显示规则集中在前端一个格式化函数，不散落到 Tooltip、表格和
条件输入中：

| `unit` | 显示方式 | 示例 |
|---|---|---|
| `ratio` | 乘 100，加 `%` | `0.0312 → 3.12%` |
| `multiple` | 保留适当小数，加 `×` | `1.83 → 1.83×` |
| `score` | 原始数值 | `72.4` |
| `correlation` | 原始值，最多四位小数 | `-0.3281` |

格式化只影响展示，不改变 JSON、CSV 原始值。未知单位回退为通用数值格式并显示单位未知，
不得默认当作百分比。

## 7. 页面与交互设计

### 7.1 分组模式

在未来收益窗口后增加模式切换：

```text
分组方式
● 等数量五组：判断因子排序能力
○ 固定数值区间：研究具体阈值
```

选择固定区间后显示四个断点输入。标签、后缀和占位示例根据当前因子的 `unit` 动态变化。
切换因子时：

- 等数量五组可以直接运行；
- 固定区间清空旧断点，不把另一因子的值沿用过来；
- 不根据未来收益自动填入断点；
- 页面可以根据因子定义提供纯解释性占位符，但占位符不成为实际提交值。

### 7.2 结果主区

主区顺序固定为：

1. 一句话说明分组口径；
2. 五组未来收益柱状图；
3. 五组原始因子值统计表；
4. 收益方向摘要；
5. Coverage 与研究限制；
6. 折叠的 IC 统计详情。

分位模式说明：

> 每个交易日按当前因子值从低到高分组；组边界每日变化。下表展示历史组内因子值分布，
> 不是固定选股阈值。

固定区间模式说明：

> 所有交易日使用同一组数值边界；空组表示该时期没有股票落入对应区间。

### 7.3 五组统计表

默认列：

| 组别 | 固定边界/历史典型值 | 中间 50% 范围 | 样本数 | 有效日期 | 未来 N 日平均收益 |
|---|---|---|---:|---:|---:|

- 分位模式“历史典型值”显示 `median`；
- 固定区间模式优先显示配置边界，同时在次要文本显示组内 `median`；
- “中间 50% 范围”显示 `[P25, P75]`；
- 展开“完整统计”后显示 min/max/mean/std/countPct；
- 表头和帮助文本必须说明 Q1/Q5 的高低指因子值，不指收益高低；
- 相邻分位组历史 min/max 重叠属于正常现象，帮助文本解释原因；
- 窄屏使用横向滚动容器，不隐藏列、不把数值截断为省略号。

### 7.4 图表 Tooltip

每根柱的 Tooltip 至少显示：

```text
Q1｜因子值最低组
因子中位数：-4.10%
中间50%范围：-5.80% ～ -3.00%
有效观测：12,430
有效日期：782
随后1日平均收益：+0.40%
```

固定区间模式第一行改为实际边界，例如 `B1｜(-∞, -5%]`。

### 7.5 通用文案边界

- 通用图表只使用“因子值最低/最高”，不写“均线上方/下方”“低波动/高波动”；
- 当前因子的具体含义继续由概念卡的 `description/example` 动态解释；
- 不根据收益正负自动生成“建议买入”“最佳区间”等文案；
- “高值组收益更高”只描述样本统计关系，不描述因果关系。

## 8. 导出设计

继续输出：

- `report.json`：新增 v2 字段；
- `ic.csv`：保持现有逐日 IC 合同；
- `report.html`：增加分组统计表和口径说明。

新增：

- `groups.csv`：一行一个组，包含报告中的全部 `FactorGroupStats` 字段；
- `daily_groups.csv`：一行一个“日期 × 组”，至少包含日期、组号、股票数、当日因子
  min/max/median、当日组未来平均收益，用于审计动态边界。

导出约束：

- CSV 数值保存原始值，不乘 100；列名注明 `raw`；
- 固定区间的开放端留空，不写 `Inf`；
- HTML 和 Web 使用同一字段语义，不能各算一套统计；
- `report.json` 仍以临时文件加 rename 原子落盘；
- CSV/HTML 的 best-effort 失败策略沿用当前实现，但错误应记录在可见日志中。

## 9. 文件级实施清单

### 修改

- `internal/lab/analysis.go`
  - 分组配置、分组纯函数、统计 DTO、报告 v2、导出；
- `internal/lab/analysis_test.go`
  - 分位分组、并列值、固定断点、百分位和统计纯函数；
- `internal/lab/analysis_run_test.go`
  - 主循环聚合、空组、Coverage、旧请求缺省行为；
- `internal/lab/factor_e2e_test.go`
  - `/api/analyze` 两种模式与报告 JSON；
- `internal/lab/web/lab/index.html`
  - 模式切换、断点输入、统计表、Tooltip、旧报告兼容；
- `docs/superpowers/specs/2026-09-16-factor-web-mvp-design.md`
  - 代码完成后同步最终用户可见行为，不能提前写成已实现；
- `MEMORY.md`
  - 仅在代码和验证实际完成后记录长期合同，本次只写计划时不修改。

### 不修改

- `AGENTS.md`；
- `core/types.go`、`core/factor.go`、`core/backtest.go`；
- `strategies/factor` 中各因子的数学公式；
- `strategies/buy/factor_filter.go` 的过滤语义；
- 现有回测配置、成交模型和报告。

## 10. 分阶段实施任务

### Task 1：分组配置与纯函数（TDD）

1. 为 `GroupingConfig.Validate()` 写失败测试：未知模式、quantile 带 cuts、cuts 数量错误、
   非有限值、非递增、重复零。
2. 写分位分组测试：唯一值、样本数不能被五整除、并列跨边界、全部相等、输入乱序。
3. 断言相同输入多次运行得到完全一致的组成员与统计。
4. 写固定区间边界测试，锁定 `(lower, upper]` 语义和开放端。
5. 写 Type-7 的 P25/Median/P75 手算用例，包括 1、2、奇数、偶数样本。
6. 实现最小纯函数，不接 Runner、不做 IO。

完成门槛：`go test ./internal/lab -run 'Test(Grouping|Quantile|Bins|Percentile|FactorGroup)'`。

### Task 2：报告 v2 与主循环聚合（TDD）

1. 扩展 `AnalyzeConfig`，先锁定旧请求无 grouping 时仍为五分位。
2. 在逐日处理阶段生成带 code、factor、return 的观测，避免丢失确定性 tie-break 信息。
3. 每日先分组并计算每日组收益；同时把组内因子值追加到报告级累计器。
4. 构建 `Groups`、`FactorSnapshot`、`Grouping` 和 `analysisVersion=2`。
5. 保留 `quintiles` 兼容字段；固定区间模式不伪造 quintiles。
6. 测试缺组、全相等因子、部分日期不足五票、跳过股票和停止任务。

完成门槛：`go test ./internal/lab -run 'TestRunAnalysis|TestAnalyzeConfig|TestSummarize'`。

### Task 3：报告导出与历史兼容

1. 为 v2 `report.json` 做 marshal/unmarshal 测试，确认无 NaN/Inf。
2. 新增 `groups.csv` 与 `daily_groups.csv`，锁定列名和原始值单位。
3. 扩展静态 `report.html`，复用报告字段，不在 JavaScript 中重算分位统计。
4. 准备一个无 `groups` 的 v1 fixture，验证旧报告仍可展示旧柱状图。
5. 导出目录继续使用 `output/factor/<因子名>/`，不迁移历史文件。

### Task 4：Web 交互和解释层

1. 增加“等数量五组/固定数值区间”切换，默认等数量五组。
2. 固定区间模式渲染四个单位感知输入；提交前统一转换为原始值。
3. 增加通用单位格式化函数并替换图表、表格、Tooltip 中的重复格式化。
4. 柱状图读取 `groups[].forwardReturn`；旧报告才回退 `quintiles`。
5. 新增五组统计表和完整统计折叠区；空组显示“无有效样本”，不显示 0。
6. 增加动态/固定边界说明、评价收益非策略收益提示和旧报告提示。
7. 保持 ECharts 容器稳定尺寸、可见后初始化、resize 正常、红涨绿跌规则不变。
8. 键盘可以切换模式、输入断点、运行分析、浏览表格和展开详情。

### Task 5：API 与端到端验证

1. `POST /api/analyze`：旧请求成功；合法 bins 成功；非法 cuts 返回 409。
2. `GET /api/analysis/latest`：返回 v2 因子快照、五组统计和 Coverage。
3. 使用至少五只构造股票验证分位组的 factor median 和 future return 手算一致。
4. 使用均线偏离构造数据验证 ratio 在 JSON 中为原始小数、页面显示百分比。
5. 使用一个非 ratio 因子验证页面不会错误乘 100。
6. 验证全相等因子不会被强拆成看似有收益差异的五组。
7. 验证 bins 空组、断点值归左组和 Q5/B5 减 Q1/B1 的 spread。

### Task 6：全量验证与正式文档同步

自动化检查：

```powershell
gofmt -w internal/lab/analysis.go internal/lab/analysis_test.go internal/lab/analysis_run_test.go internal/lab/factor_e2e_test.go
go test ./internal/lab
go test ./...
go build ./...
git diff --check
```

浏览器验收：

1. 均线偏离、20 日、未来 1 日、等数量五组运行成功；
2. 每组同时看到未来收益、中位偏离率、中间 50% 范围和样本数；
3. 页面明确说明历史分位范围不是固定阈值；
4. 切换固定区间，输入 `-5%、-2%、0%、2%`，请求保存原始小数；
5. 固定边界完整显示，空组不报错；
6. 切换量比、K 值、相关系数，单位和断点输入随元数据变化；
7. 打开旧报告，仍能查看收益图并看到缺少因子值范围的提示；
8. 900px 和 560px 宽度下表格可访问，无页面级横向溢出；
9. 键盘与可见焦点、`aria-live` 状态、折叠详情正常；
10. 高级脚本、简单策略运行和交易明细无回归。

代码和验证完成后，才同步更新上游设计状态与项目 `MEMORY.md`。不得把本实施计划记录为
已经实现的事实。

## 11. 测试矩阵

| 层级 | 必测内容 | 主要风险 |
|---|---|---|
| 配置 | 默认 quantile、合法/非法 cuts、有限值 | 旧请求失效或错误边界进入分析 |
| 纯函数 | 并列值、空组、Type-7、单位无关统计 | 结果不确定或产生虚假分组差异 |
| 编排 | 每日等权收益、全样本因子分布、Coverage | 因子统计和收益统计口径混淆 |
| JSON | null 语义、v1/v2 兼容、无 Inf/NaN | 报告无法落盘或旧报告失读 |
| API | 两种模式、409、任务互斥、最新报告 | 前后端合同不一致 |
| 导出 | report/groups/daily_groups/ic | Web 与离线报告不可审计 |
| 浏览器 | 模式、单位、Tooltip、空态、窄屏、键盘 | 用户仍无法理解分组数值 |
| 回归 | 简单策略、高级脚本、全仓测试和构建 | 因子解释改造破坏现有工作流 |

## 12. 兼容与迁移策略

- API 只新增可选字段，旧客户端无需立即升级；
- 新前端优先读 `groups`，缺失时读取 `quintiles`；
- 不批量重写 `output/factor` 历史报告；
- v1 报告不推算不存在的因子值范围，明确标注“未保存”；
- 若固定区间功能需要回滚，可隐藏 bins 控件，默认 quantile 和旧 API 仍可工作；
- `AnalysisVersion` 以后用于显式迁移，不能依赖字段是否为空猜测全部语义；
- 变更不触及回测报告，不需要迁移 `output/trades`。

## 13. 实施停止条件

出现以下任一情况时停止扩张并先确认：

- 需要修改受保护的核心结构或改变 `core.Factor` 接口；
- 需要改变未来收益价格口径或回测成交时点；
- 需要引入自动阈值优化、收益最大化搜索或样本外选择机制；
- 需要新增任意组数、行业内分组或多因子联合分组；
- 无法在保留旧请求的情况下扩展 `AnalyzeConfig`；
- 因子目录单位不足以无歧义格式化某个因子；
- 实现必须引入新的前端框架或生产依赖；
- 发现当前脏工作区与本计划目标文件存在无法安全合并的并行修改。

## 14. 完成定义

只有同时满足以下条件，才能称为完成：

1. 用户能从每个组看到原始因子值中位数、P25～P75、样本数和未来收益；
2. 页面明确区分动态分位边界和固定数值边界；
3. 用户可以用固定四断点研究五个实际数值区间；
4. 所有因子通过目录单位复用同一套 UI，不存在均线偏离专用硬编码；
5. 并列因子值不会因 map 遍历顺序产生不稳定或虚假的组间差异；
6. 新旧请求、新旧报告和现有回测工作流兼容；
7. JSON/CSV/HTML 对分组、单位和聚合口径一致；
8. 目标测试、全仓测试、构建、浏览器路径和差异检查都有真实验证记录；
9. 正式设计文档和项目记忆只记录最终已实现、已验证的事实。

## 15. 参考

- Alphalens `quantize_factor`：每日分位数组、固定 `bins`、分组内量化；
  <https://github.com/quantopian/alphalens/blob/master/alphalens/utils.py>
- Alphalens Overview：五组收益、收益分布、Top-Bottom spread、Quantiles Statistics；
  <https://alphalens.ml4trading.io/notebooks/overview.html>
- Fama/French Benchmark Portfolios：使用公开断点规则构造可复查组合；
  <https://mba.tuck.dartmouth.edu/pages/faculty/ken.french/Data_Library/f-f_portfolios.html>
