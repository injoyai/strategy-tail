# 因子分析分组数扩展与值域展示设计

> 日期：2026-09-17
>
> 状态：已实施并验证（2026-09-17）
>
> 上游背景：`docs/superpowers/plans/2026-09-16-factor-group-interpretability.md`（其
> 后端半区已实现：`groups` 数据合同、bins 模式、并列块规则；前端展示半区未实施）
>
> 适用范围：Strategy Lab 因子研究页与分析 API；不改回测成交模型

## 1. 背景与问题

因子研究页的分析结果按五组分组展示未来收益，存在两个问题：

1. **分组不显示值域**：柱状图 X 轴只有"最低组…最高组"标签，用户看不到每组
   对应的因子值范围，无法直观回答"这一组大致是什么数值"。
2. **分组数固定**：分组数在后端硬编码为 5 组，无法观察更细的分层单调性
   （如 7 组、11 组）。

现状盘点（关键事实）：

- 后端 v2 报告**已经**保存每组完整的因子值分布
  （`FactorGroupStats`：min/P25/median/mean/P75/max/std/observations/countPct，
  见 [analysis.go](../../../internal/lab/analysis.go)），但前端从未读取 `groups`
  字段——柱状图只消费旧字段 `quintiles`（5 个收益值）。
- 分组数硬编码位置：`quantileAssign`（中点公式 `*5/`）、`aggregateGroups`
  （循环上限 5）、`summarizeQuintiles`（`len(qs) < 5`）、`GroupingConfig.Validate`
  （bins 恰好 4 断点）。
- 前端没有分组配置控件，请求从不携带 `grouping`。
- interpretability 计划当时的停止条件之一"不新增任意组数"是防扩散决定，
  本设计由用户明确需求推翻该限制，属有授权的变更。

## 2. 交付目标

1. 每个分组直接展示因子值范围：柱状图轴标签 + 分组明细表。
2. 分组数可配：前端下拉 5（默认）/7/10/11，后端校验 2–20。
3. 等频分组的中点公式、并列块整块归组、确定性 tie-break 语义原样泛化。
4. 新旧请求与新旧报告兼容：无 `grouping.groups` 的请求仍为五组；无 `groups`
   字段的旧报告仍可渲染（降级无值域）。
5. 值域展示按因子 `unit` 感知格式化（ratio 显示百分比等）。

## 3. 明确不做

- 不做 bins 固定区间模式的**前端输入 UI**（后端合同已支持，API 直连可用；
  前端只需保证 bins 报告能正确渲染其边界）；
- 不做 `groups.csv` / `daily_groups.csv` 导出（interpretability 计划遗留，
  另行处理）；
- 不改 `report.html` 静态导出内容（仍只有 IC 折线）；
- 不改 IC、未来收益的统计口径与 `core` 受保护结构；
- 不新增前端框架或生产依赖。

## 4. 后端设计（[analysis.go](../../../internal/lab/analysis.go)）

### 4.1 GroupingConfig 扩展

```go
type GroupingConfig struct {
    Mode  string    `json:"mode,omitempty"`  // quantile | bins
    Groups int      `json:"groups,omitempty"` // 分组数；0=缺省 5；有效 2–20
    Cuts  []float64 `json:"cuts,omitempty"`  // bins 模式必须恰好 Groups−1 个
}
```

校验规则（`Validate`）：

- `Groups`：0（缺省）视为 5；非 0 时必须 2–20，否则报错；
- quantile 模式：`cuts` 必须为空（现有规则不变）；
- bins 模式：`len(cuts)` 必须等于生效组数 − 1（缺省 5 组 → 4 断点，
  与现有报告和测试兼容）；断点仍要求有限、严格递增（`-0` 与 `0` 重复）；
- 未知 `mode` 报错（现有规则不变）。

生效组数统一由一个纯函数（如 `func (c GroupingConfig) groupCount() int`）
解析：`Groups==0 → 5`。`Validate` 与 `aggregateGroups` 共用，禁止两处各写一遍。

### 4.2 纯函数泛化

| 函数 | 变更 |
|---|---|
| `quantileAssign(obs, g)` | 中点公式 `(i+j)*5/(2n)` → `(i+j)*g/(2n)`；结果 0..g−1；并列块整块归组不变 |
| `aggregateGroups(days, vals, rets, grouping)` | 组数 `g = grouping.groupCount()`；累计器、标签、循环上限按 g |
| `summarizeQuintiles(qs, n)` | `len(qs) < n → insufficient`；spread = `qs[len−1] − qs[0]`；方向/flat 判定不变 |

每日有效票数不足 g 时：空组统计为 null、`ForwardReturn` 为 null、摘要
insufficient——现有语义的自然泛化，不为凑满柱子伪造区分度。

### 4.3 报告合同（字段名不变，长度泛化）

- `groups`：长度 N，`Index` 1..N，`Label` `Q1..QN` / `B1..BN`；
- `quintiles` 镜像：分位模式组完整时输出 N 个收益值，否则 null；
- `years[].quintiles`：长度 N；
- `AnalysisVersion` 保持 2：无破坏性变更，前端按字段存在性与数组长度自适应，
  不需要新版本号迁移。

### 4.4 API

`POST /api/analyze` 无路由变更：`Grouping.Groups` 随 JSON 解码进入
`AnalyzeConfig`，校验失败仍返回 409（与现有配置错误口径一致）。

## 5. 前端设计（[index.html](../../../internal/lab/web/lab/index.html)）

### 5.1 分组数控件

因子研究页参数行、"未来收益窗口"之后新增：

```html
<label for="factorGroups">分组数</label>
<select id="factorGroups">
  <option value="5" selected>5 组</option>
  <option value="7">7 组</option>
  <option value="10">10 组</option>
  <option value="11">11 组</option>
</select>
```

提交时请求体追加 `grouping: {mode: "quantile", groups: N}`。

### 5.2 因子值格式化（unit 感知，集中一个函数）

后端保存原始值，前端显示转换，格式化只影响展示：

| `unit` | 显示方式 | 示例 |
|---|---|---|
| `ratio` | ×100 + `%` | `-0.051 → -5.1%` |
| `multiple` | 数值 + `×` | `1.83 → 1.83×` |
| `score` | 原值 | `72.4` |
| `correlation` | 原值 | `-0.33` |
| 未知/缺失 | 原值 | — |

数值统一 3 位有效数字（`Number(v.toPrecision(3))` 去尾零）；`unit` 取自
报告 `factor.unit`（v2 报告必有；无 groups 的旧报告不进入此展示路径）。

### 5.3 柱状图（renderQuintiles 重写）

- 数据源改为 `report.groups[].forwardReturn`；旧报告（无 `groups`）降级读
  `quintiles`，此时无值域可显示；
- X 轴标签两行：`G1 最低` / `G2` … `GN 最高` + 第二行值域 `[min ~ max]`
  （旧报告降级时第二行留空）；`interval: 0` 保证 N 组全部显示；
- 标题动态：`N 组平均收益%（按因子值升序等频分组）`；
- 保留现有配色（正收益红 / 负收益绿）、tooltip 百分比口径。

### 5.4 分组明细表（新增，图表下方）

| 组 | 因子值范围 | 中位数 | 均值 | 样本占比 | 未来收益 |
|---|---|---|---|---|---|

- 分位模式"因子值范围"= 该组全样本 `factorMin ~ factorMax`（历史分布，
  非固定边界）；bins 模式 = 固定边界 `(lower, upper]`（开放端 −∞/+∞）；
- 样本占比 = `countPct`（约 1/N）；未来收益 = `forwardReturn` ×100%；
- 空组行显示"无有效样本"，不显示 0；
- 旧报告无 `groups` 时整表隐藏；
- 样式复用 `.year-wrap` 滚动容器与 `#yearStability` 的数字/na 样式
  （选择器扩展为两者共用）。

### 5.5 年度稳定性表（renderYearStability）

- 首末组列头动态：`G1` / `GN`（bins 报告为 `B1` / `BN`；旧报告回退 `Q1`/`Q5`）；
  差值列头 `GN−G1`；
- 单元格取 `qs[0]`、`qs[qs.length−1]`、差值——对 5/N 组通用；
- `qs.length === 5` 的检查放宽为 `qs.length >= 2`。

### 5.6 文案动态化

- "五组收益方向不一致/接近"等 DIR 文案去掉硬编码数字（"各组…"）；
- `quintileNA` 提示文案中的"五组"改为当前分组数；
- `rangeMeta` 增加"分组：N 组等频"（bins 报告显示"固定区间"）；
- 概念卡 hint 中"五组分布"改"分组分布"。

## 6. 测试设计

### 后端（`analysis_test.go` / `analysis_run_test.go`）

- `TestGroupingValidate` 增补：`Groups` 0/2/20 合法、1/21 非法；bins 断点数
  = N−1（6 断点 7 组合法、4 断点 7 组非法）；默认缺省仍 5 组 4 断点；
- `TestQuantileAssign` / `TestSummarizeQuintiles` 适配新签名，加 7 组用例
  （含 n 不能被 7 整除、并列跨边界、不足 7 组 insufficient）；
- 新增运行测试：7 票 7 组等频 → `len(Groups)==7`、`CountPct≈1/7`、
  `Quintiles` 镜像与 `Years[].Quintiles` 长度 7、方向摘要正常；
- 现有五组测试（`TestRunAnalysis`、`TestRunAnalysisBins`、
  `TestRunAnalysisAllEqual`、`TestRunAnalysisMultiYearCoverage`）在缺省
  grouping 下预期不变——这是向后兼容的直接证据。

### 前端

无自动化基建；验证 = `go build ./...`（embed 参与编译）+ lab 单测 + 浏览器
手动核对：5/7/11 组渲染、值域标签与明细表、旧报告降级、窄屏无溢出。

## 7. 兼容与回滚

- 旧请求（无 `grouping` 或无 `groups`）→ 五组，行为与现有完全一致；
- 旧报告（无 `groups`）→ 柱状图降级 + 明细表隐藏 + "历史报告未保存因子值
  范围"提示；
- 回滚 = 隐藏分组数下拉（前端）/ 拒绝 `Groups` 字段（后端），缺省路径不受影响；
- 不迁移、不重写 `output/factor` 历史报告。

## 8. 决策记录

| 决策 | 选项 | 裁决 |
|---|---|---|
| 分组数选择方式 | 固定下拉 / 自由输入 / 下拉+自定义 | 固定下拉（用户 2026-09-17 确认） |
| 值域展示 | 图表+明细表 / 仅表 / 仅图表 | 图表轴标签 + 明细表（用户确认） |
| `AnalysisVersion` | 升 3 / 保持 2 | 保持 2（纯长度泛化，无字段增删） |
| bins 前端 UI | 本期一并做 / 不做 | 不做（范围控制，仅保证渲染兼容） |
| 分析后切组 | 后端预算全部组数 / 前端本地重算 / 只预算固定四档 | 后端预算全部组数 2-20（用户 2026-09-17 确认），参数区下拉保留为默认档 |

## 9. 分析后切组（同日增量，已实施并验证）

目标：分析完成后在结果区切换分组数，即时生效，不重新分析。

### 9.1 后端

- 报告新增 `allGroupings`（`GroupingSet{Groups,Quintiles,Stats,Summary}`），
  `YearAnalysis` 新增 `AllGroupings`：
  - 仅 quantile 模式预算 2-20 全部组数；bins 固定断点不参与（`nil`）；
  - 全区间 `Stats` 含每组因子值分布/计数；年度 `AllGroupings` 只含
    Quintiles/Summary（控制报告体积）；
- 新函数 `aggregateQuantileSets(days, vals, rets, fullStats)`：
  - **分位排序与组数无关**——每日横截面按 value/code 只排序一次并缓存，
    各档按中点公式 `(i+j)*g/(2n)` 线性扫描归组，避免 19 档重复排序；
  - 全市场 × 多年场景增量约几秒，报告体积增加几百 KB 以内；
- 抽 `summarizeFromStats`（从组统计提取收益并求摘要），`aggregateGroups`
  尾部复用，行为不变。

### 9.2 前端

- 结果区新增 `#groupSwitch` 分组数切换器（2-20），`#resultGroups` 下拉；
- 状态 `currentGroupingN` + `currentGrouping(rep, n)` 查表：
  - 有 `allGroupings` → 按 n 取该档（groups/quintiles/summary）；
  - 无（旧报告/bins）→ 回退顶层 groups/quintiles/summary；
- 切换器 change → 更新 `currentGroupingN` → `renderAnalysis()` 整体重跑，
  柱状图/明细表/年度表/rangeMeta 摘要全部随档；
- 新分析提交时重置 `currentGroupingN` 为请求档；
- 旧报告/bins：切换器隐藏，行为与上一版一致。

### 9.3 测试

- `TestRunAnalysisAllGroupings`：`allGroupings` 覆盖 2-20、组数升序、
  `allGroupings[7]` 与单档 7 组请求逐组收益/中位数相等、年度
  `AllGroupings` 只含收益/摘要（Stats 为空）；
- `TestRunAnalysisBinsNoAllGroupings`：bins 模式全区间与年度
  `AllGroupings` 均为 nil。
