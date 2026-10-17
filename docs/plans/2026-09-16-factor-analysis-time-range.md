# 因子分析时间范围扩展实施计划

> 日期：2026-09-16
>
> 状态：Ready for implementation
>
> 范围：Strategy Lab 因子分析的可配置年份、跨年覆盖率与年度稳定性

## 1. 背景

当前策略实验室的因子研究页没有独立展示分析年份，提交分析时复用策略运行页的 `startYear`、`endYear`。页面默认值为 2025～2026，样本集中在单一偏强市场阶段，无法充分检验因子在下跌、震荡、风格切换和高低波动环境中的稳定性。

本次修改将因子分析默认区间扩展为 2018～2026，并允许用户在因子研究页直接修改起止年份。扩大区间后仍须显式展示实际数据覆盖率，不能把缺失年份、上市时间不足或数据加载失败静默隐藏。

## 2. 目标与非目标

### 2.1 目标

1. 因子研究页提供独立、可见的开始年份和结束年份输入框。
2. 默认值为 `2018` 和 `2026`，用户可以在启动分析前自行修改。
3. 后端继续使用现有 `AnalyzeConfig.RunConfig.StartYear/EndYear` 请求字段，不新增重复的年份参数。
4. 分析报告保存本次实际使用的年份范围，并在页面结果区展示。
5. 结果增加年度拆分，使跨周期汇总值不能掩盖某些年份失效或反向。
6. 扩大年份范围后，按“股票 × 年份”统计覆盖率；单个年份缺失不得默认导致该股票其他可用年份全部作废。
7. 保持无前视口径：日期 `t` 的因子只使用截至 `t` 的数据，未来收益继续使用 `Close(t+W)/Close(t)-1`。

### 2.2 非目标

1. 本次不修改任何因子公式、默认因子窗口或原始值含义。
2. 本次不改变回测页的默认年份；因子分析与策略回测的时间控件分离。
3. 本次不引入因子归一化、全样本 Min-Max 或自动阈值推荐。
4. 本次不承诺解决历史成分股和退市股票缺失造成的生存者偏差；报告必须披露该限制。
5. 市场状态拆分需要先确定基准、趋势和波动阈值，本次不擅自指定规则，作为后续独立决策项。

## 3. 用户可见行为

### 3.1 因子研究页

在因子、因子窗口和未来收益窗口附近增加：

- `开始年份`：整数，默认 `2018`。
- `结束年份`：整数，默认 `2026`。

输入框必须直接显示在因子研究页，不能要求用户切换到策略运行页修改隐藏配置。

提交 `POST /api/analyze` 时使用因子研究页的年份值：

```json
{
  "startYear": 2018,
  "endYear": 2026,
  "sampleMode": "all",
  "kind": "momentum",
  "days": 20,
  "window": 5
}
```

页面结果区至少展示：

```text
分析区间：2018–2026
实际有效日期：2018-01-02–2026-09-03
未来收益窗口：5 个交易日
```

“配置区间”和“实际有效日期”必须分开。前者来自请求，后者来自成功生成标签的首末交易日；数据不足时两者可能不同。

### 3.2 校验与错误提示

沿用后端现有年份校验，并在前端提供同口径即时提示：

- 开始年份和结束年份必须为正整数。
- `startYear <= endYear`。
- `endYear` 不得超过当前年份。
- 不因为默认值是 2018～2026 而限制用户只能选择该区间。
- 后端校验是最终约束，不能只依赖 HTML 的 `min`、`max` 属性。

## 4. 后端实现

### 4.1 API 契约

`AnalyzeConfig` 已内嵌 `RunConfig`，请求字段保持不变：

```go
type RunConfig struct {
    StartYear int `json:"startYear"`
    EndYear   int `json:"endYear"`
    // 其他字段保持不变
}
```

不得新增另一组仅供因子分析使用的同义字段。`AnalyzeConfig.Validate()` 继续复用 `validateYears()`。

### 4.2 分析报告保存配置

当前 `AnalysisReport` 只保存因子、未来收益窗口和结果，不能从报告中确认请求年份。增加明确的分析范围字段：

```go
type AnalysisRange struct {
    StartYear  int    `json:"startYear"`
    EndYear    int    `json:"endYear"`
    SampleMode string `json:"sampleMode"`
    SampleSize int    `json:"sampleSize,omitempty"`
}
```

`AnalysisReport` 增加：

```go
Range         AnalysisRange `json:"range"`
FirstDataDate string        `json:"firstDataDate"`
LastDataDate  string        `json:"lastDataDate"`
```

指定代码模式涉及完整代码列表，报告中是否保存全部代码应沿用现有报告的隐私和体积约定；至少保存代码数量，并保证 Coverage 中可以定位失败代码。

新增字段为向后兼容扩展。读取历史报告时字段缺失应正常处理，前端显示“历史报告未记录”。

### 4.3 跨年份数据加载与覆盖率

现有 `researchrun.ForEachCodeData` 对年份采用全有或全无语义：任一请求年份加载失败，整只股票不会进入回调。直接把默认范围扩展为 2018～2026 会排除 2018 年之后上市的股票，也会让单年数据缺口抹掉其他年份的有效观测。

因子分析应改为逐“股票 × 年份”处理：

1. 每个成功加载的代码年份独立生成因子值和未来收益。
2. 某年无数据时记录失败，但继续处理该股票的其他年份。
3. 真正的数据库读取错误同样不得静默吞掉，必须进入失败明细。
4. 取消任务时立即停止，不得把未访问数据计为普通缺失。
5. 不改变策略回测当前的全年份一致性语义；新增能力应为分析专用入口或显式配置，不能悄悄改变共享回测行为。

建议新增分析用遍历接口，而不是修改现有 `ForEachCodeData` 的默认语义：

```go
ForEachCodeYearData(
    ctx context.Context,
    cfg Config,
    fn func(code string, year int, data YearData),
) (YearCoverage, error)
```

建议覆盖率结构：

```go
type YearCoverage struct {
    RequestedCodes      int       `json:"requestedCodes"`
    CompletedCodes      int       `json:"completedCodes"`
    RequestedCodeYears  int       `json:"requestedCodeYears"`
    CompletedCodeYears  int       `json:"completedCodeYears"`
    SkippedCodeYears    int       `json:"skippedCodeYears"`
    Failures            []Failure `json:"failures"`
}
```

`CompletedCodes` 定义为至少有一个成功年份的股票数。失败明细继续携带 `Code`、`Year`、`Stage` 和 `Message`。

### 4.4 年度结果

在保留全区间汇总的同时增加年度结果：

```go
type YearAnalysis struct {
    Year       int             `json:"year"`
    Stats      ICStats         `json:"stats"`
    Quintiles  []float64       `json:"quintiles"`
    Summary    QuintileSummary `json:"summary"`
    TradingDays int            `json:"tradingDays"`
}
```

`AnalysisReport` 增加 `Years []YearAnalysis`。年度统计与全区间统计使用同一批已清洗的逐日观测：

- IC 仍是每日横截面 Spearman IC。
- 年度 IC 均值对该年有效交易日等权。
- 年度五组收益先按日计算，再对该年有效交易日等权。
- 全区间统计继续按交易日等权，不能先对年度均值再次等权，否则交易日较少的年份会获得过高权重。
- 有效日不足现有 `minPairs` 时，保留样本数并标记不足，不把零值解释为真实 IC 为零。

## 5. 前端展示

因子研究结果增加“年度稳定性”表：

| 年份 | 有效日 | IC 均值 | IC 标准差 | t 值 | Q1 | Q5 | Q5-Q1 | 方向 |
|---|---:|---:|---:|---:|---:|---:|---:|---|

展示规则：

1. 收益字段由原始小数转为百分比展示，例如 `0.0123` 显示为 `1.23%`。
2. IC 保持原值，不乘 100。
3. 缺失或样本不足显示“样本不足”，不得显示为 `0.0000` 误导用户。
4. 全区间汇总与年度结果同时展示，默认先看到年度稳定性，再展开每日 IC。
5. 页面明确提示：当前 `all` 股票池来自当前沪深主板代码列表，不是历史时点成分股，存在生存者偏差。

## 6. 数据口径与偏差披露

实施完成后，报告和页面必须保留以下说明：

1. 因子输入为本地 TDX 日 K 数据。
2. 每个年度加载额外历史数据用于因子预热，但只统计所选年份内的观测。
3. 未来收益窗口末尾没有足够未来交易日的数据会被剔除。
4. 因子值或未来收益为 `NaN/Inf` 的股票日会被剔除。
5. 当前全市场代码池不是历史时点股票池，退市股票缺失会造成生存者偏差。
6. 后上市股票只从其有数据的年份开始参与，不应因为早期年份不存在而整只排除。
7. 2018～2026 是默认研究区间，不代表所有本地股票均完整覆盖该区间；以 Coverage 和实际有效日期为准。

## 7. 修改范围

预计涉及：

- `internal/lab/web/lab/index.html`
  - 增加因子分析独立年份控件。
  - 默认 2018～2026。
  - 提交分析时读取独立年份。
  - 展示配置区间、实际日期、年度结果和覆盖率。
- `internal/lab/analysis.go`
  - 报告保存分析范围和实际日期。
  - 按年度聚合 IC、五组收益和样本数。
  - 使用逐代码年份加载结果。
- `internal/researchrun/runner.go`
  - 新增不影响回测语义的逐代码年份遍历入口和覆盖率。
- `internal/lab/analysis_test.go`
  - 年份配置、年度聚合、样本不足和首末有效日期测试。
- `internal/lab/analysis_run_test.go`
  - 部分年份缺失仍保留其他年份、覆盖率和取消传播测试。
- `internal/lab/server_test.go`
  - API 请求与新增报告字段的兼容性测试。

不修改 `AGENTS.md`，不修改因子公式，不修改策略回测默认年份。

## 8. 实施顺序

1. 先补充失败测试，锁定 2018～2026 默认值和年份可编辑行为。
2. 新增逐代码年份数据遍历与 `YearCoverage`，验证不改变现有回测执行路径。
3. 扩展 `AnalysisReport`，实现实际日期和年度统计。
4. 修改因子研究页的独立年份控件、报告元数据和年度表格。
5. 验证历史报告缺少新字段时仍可加载。
6. 运行目标包测试、全仓库测试及格式检查。

## 9. 验收标准

### 9.1 功能验收

- 打开因子研究页时，开始年份为 2018，结束年份为 2026。
- 用户可改为任意合法区间，例如 2020～2024，并且请求和报告均使用该区间。
- 修改因子分析年份不会改变策略回测页的年份值。
- 后端拒绝开始年份大于结束年份、结束年份超过当前年份等非法输入。
- 报告同时显示配置区间和实际有效日期。
- 报告包含全区间与逐年度 IC、五组收益和覆盖率。
- 股票缺少 2018 年数据但拥有 2020～2026 年数据时，后续年份仍参与分析，缺失年份进入失败明细。
- 未来窗口为 `W` 时，每个年度尾部不足 `W` 个交易日的观测不进入标签。
- 历史报告没有新增字段时，页面不报错。

### 9.2 验证命令

```bash
go test ./internal/researchrun ./internal/lab ./strategies/factor
go test ./...
gofmt -l internal/researchrun internal/lab
git diff --check
```

若工作区已有与本任务无关的未提交修改，验证和交付必须区分既有失败与本次引入的失败，不得覆盖或清理其他协作者的内容。

## 10. 后续决策项

市场状态拆分另开任务，实施前需要用户明确：

1. 市场基准使用上证指数、沪深 300，还是与股票池匹配的其他指数。
2. 上涨、下跌、震荡的判定窗口和阈值。
3. 高波动、低波动使用历史波动率、ATR 还是基准回撤。
4. 市场状态按决策日可见数据实时判定，禁止使用完整区间事后极值划分。
