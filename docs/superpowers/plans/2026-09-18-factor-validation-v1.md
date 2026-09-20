# 专业因子验证体系 v1 实施计划

> 日期：2026-09-18  
> 状态：Ready for implementation  
> 上游设计：`docs/superpowers/specs/2026-09-18-factor-validation-v1-design.md`  
> 实施边界：本计划只实现研究协议、可交易标签、单因子诊断、试验账本和冻结候选的滚动验证；不实现多因子评分、组合优化或实盘交易

## 1. 交付目标

把当前“样本内单周期因子分析 + 保存候选”扩展成可审计验证链：

```text
ResearchProtocol
  → AnalysisReport v4（多周期、可交易标签、HAC、换手、暴露）
  → TrialStore（完整试验家族）
  → FactorCandidate revision
  → FactorValidation（冻结协议和候选）
  → 滚动历史回放 / 平台内前瞻验证
  → passed | failed | insufficient | error
```

完成后，系统必须能够回答：

1. 因子在什么时候形成，收益从什么可成交价格开始；
2. 数据和股票池是否具备 PIT 与历史成员质量；
3. 1/5/10/20 日预测关系是否衰减、反向或只在少数年份存在；
4. HAC 修正后统计证据如何，分组成员换手多大；
5. 同一假设一共试过多少变体；
6. 候选冻结以后，在哪些不重叠窗口接受了什么门禁；
7. 为什么最终是 passed、failed、insufficient 或 error。

## 2. 实施原则与不可突破边界

1. **先锁定研究语义，再扩页面。** Label、HAC、窗口和冻结状态先用纯函数与 API 测试锁定。
2. **不修改受保护核心结构。** 不修改 `core/types.go`、`core/stats.go`、`core/cost.go`、`core/backtest.go` 中受保护对象格式。
3. **不复用 `core.WalkForward` 冒充因子验证。** 新验证引擎独立实现，并复用 `researchrun` 的数据加载能力。
4. **不复制数据加载循环。** 多周期分析继续走 `researchrun.ForEachCodeYearData`；需要的扩展先在该层增加通用回调/元数据，不在 handler 中读数据库。
5. **缺失和未知 fail closed。** 无法证明的数据质量返回 exploratory/insufficient，不用默认值伪装已验证。
6. **旧 API 和文件可读。** v3 报告、旧 `/api/analyze`、候选库和简单策略保持兼容。
7. **新结果不可变。** Analysis、Trial、Validation 正式记录均采用服务端 ID、hash、tmp+rename 和安全根目录检查。
8. **阈值可配置且可审计。** 平台默认模板不是金融规律；所有门禁在冻结前进入协议和 hash。
9. **共享热点顺序修改。** `analysis.go`、`runner.go`、`server.go`、`index.html` 当前已有未提交改动，实施者必须以当时工作树为基线融合，不得覆盖或回滚。
10. **文档目录被 `.gitignore` 忽略。** 若后续要提交这两份文档，必须显式确认后使用 `git add -f`；创建文档本身不授权提交。

## 3. 当前实现映射

| 现有能力 | 当前所有者 | v1 处理 |
|---|---|---|
| 单周期 AnalyzeConfig | `internal/lab/analysis.go` | 向后兼容，增加可选 `Protocol`；v4 以 `Horizons[]` 为主 |
| Rank IC/分组/年度 | `internal/lab/analysis.go` | 保留口径，拆出多周期复用的数据层和扩展统计 |
| 年度逐票加载 | `internal/researchrun.ForEachCodeYearData` | 继续作为唯一价格加载入口 |
| PIT 外部研究数据 | `researchdata.View` | 提供行业、市值与数据来源快照；缺少则显式降级 |
| 不可变分析历史 | `internal/lab/analysis_store.go` | 支持 v4，同时继续读取 v3 |
| 候选追加式存储 | `factor_candidate*.go` | 不改 CandidateStatus；验证通过 candidate ID/revision 引用 |
| Runner 单任务互斥 | `internal/lab/runner.go` | 增加 `factor_validation` task 和停止/进度语义 |
| HTTP/API | `internal/lab/server.go` | 路由注册留在 server.go，具体 handler 拆文件 |
| Web Lab | `internal/lab/web/lab/index.html` | 最后接入专业模式和验证详情，不先做静态假界面 |

## 4. 交付顺序与依赖

```text
Task 0  基线与合同夹具
  ├─> Task 1  研究协议与版本兼容
  ├─> Task 2  可交易标签与多周期观察
  └─> Task 3  HAC/ICIR/换手纯统计
          │
          ├─> Task 4  股票池和数据质量合同
          ├─> Task 5  AnalysisReport v4 编排与存储
          │       └─> Task 6  试验账本
          │               └─> Task 7  冻结验证领域与 Store
          │                       └─> Task 8  滚动验证引擎
          │                               └─> Task 9  API/Runner 集成
          │                                       └─> Task 10 Web 工作流
          └────────────────────────────────────────> Task 11 端到端与交付
```

Task 1、2、3 在文件不重叠时可并行；Task 4 可与纯统计并行。Task 5 以后按顺序集成，避免多人同时修改 `analysis.go`、`runner.go`、`server.go` 和单页 HTML。

## Task 0：冻结基线与建立兼容夹具

**文件：**

- Add: `internal/lab/testdata/analysis_v3.json`
- Add: `internal/lab/testdata/candidate_v1.json`
- Add: `internal/lab/factor_validation_compat_test.go`
- Read only: 当前 `git diff`、`PROJECT_RULES.md`、`MEMORY.md`

### Step 1：记录实施基线

实施开始时记录：

- Git commit；
- 工作树已有修改列表；
- `analysis.go`、`runner.go`、`server.go`、`index.html` 的当前 diff；
- 当前 `go test ./internal/lab` 与 `go test ./researchdata` 结果。

若现有未提交改动与计划文件重叠，先理解并融合；不得 checkout、reset 或覆盖。

### Step 2：保存旧合同夹具

从当前测试构造最小 v3 报告和 candidate revision，固定：

- v3 单 Window 字段；
- `ICStats` 数值字段；
- candidate/archived 状态；
- Evidence hash 与 FactorVersion；
- 缺少 Protocol/Horizons/Validation 时的零值行为。

夹具必须使用人工最小数据，不复制用户本地真实研究报告。

### Step 3：兼容测试先行

添加失败测试，证明后续目标：

- v3 报告可读取并派生 legacy/exploratory；
- 重新保存旧报告不会伪造 v4；
- 旧候选读取后没有人工 validation verdict；
- 旧 `/api/analyze` JSON 仍可 Decode。

**完成条件：**旧合同夹具进入测试，当前行为基线已记录，未修改生产逻辑。

## Task 1：研究协议与报告版本合同

**文件：**

- Add: `internal/lab/research_protocol.go`
- Add: `internal/lab/research_protocol_test.go`
- Modify: `internal/lab/analysis.go`
- Modify: `internal/lab/analysis_store.go`
- Modify: `internal/lab/analysis_store_test.go`

### Step 1：实现结构与纯校验

按设计文档定义：

- `ResearchProtocol`；
- `HypothesisSpec`；
- `UniverseSpec`；
- `DataProvenance`；
- `SignalSpec`；
- `LabelSpec`；
- `DiagnosticSpec`；
- `NeutralizationSpec`；
- `TrialSpec`。

纯函数建议：

```go
func (p ResearchProtocol) Validate() error
func (p ResearchProtocol) Normalize() (ResearchProtocol, error)
func protocolHash(p ResearchProtocol) (string, error)
func deriveEvidenceClass(p ResearchProtocol) string
```

校验要求：

- schema 只接受已知版本；
- hypothesis/family/variant 使用受限 ID，不接收路径字符；
- Horizons 去重、升序、每项 1～60、最多 8 个；
- `next_open_to_close` 与 `same_close_to_close_legacy` 之外拒绝；
- fixed HAC lag 0～60；默认 lag 模式不接受多余固定值；
- current_static 自动限制 evidence class；
- 未知 PIT/Adjustment 不被 Normalize 成 verified。

### Step 2：扩展 AnalyzeConfig 和 AnalysisReport

建议：

```go
type AnalyzeConfig struct {
    RunConfig
    Kind     string            `json:"kind"`
    Days     int               `json:"days"`
    Window   int               `json:"window,omitempty"` // legacy
    Grouping GroupingConfig    `json:"grouping,omitempty"`
    Protocol *ResearchProtocol `json:"protocol,omitempty"`
}
```

- `Protocol=nil`：保持 v3 执行和响应；
- `Protocol!=nil`：输出 `AnalysisVersion=4`；
- v4 报告保存 Protocol、ProtocolHash、EvidenceClass、Horizons；
- v3 字段只镜像主周期，不作为 v4 的 canonical 数据。

不要把新字段加进 `core` 受保护结构。

### Step 3：版本化存储测试

验证 AnalysisStore：

- v3/v4 都能保存和读取；
- v4 hash 重算一致；
- 未知未来版本拒绝或以明确 unsupported 错误返回，不能按 v3 猜读；
- list summary 增加 `analysisVersion/evidenceClass`，旧项为空时显示 legacy。

**验证：**

```powershell
go test ./internal/lab -run 'ResearchProtocol|Analysis(Store|Compat)'
```

**完成条件：**协议和版本合同稳定，尚未改变现有单周期数值结果。

## Task 2：可交易收益标签与多周期观察层

**文件：**

- Add: `internal/lab/factor_label.go`
- Add: `internal/lab/factor_label_test.go`
- Add: `internal/lab/factor_observation.go`
- Add: `internal/lab/factor_observation_test.go`
- Modify: `internal/lab/analysis.go`
- Modify: `internal/lab/analysis_run_test.go`
- Modify: `internal/researchrun/runner.go`
- Modify: `internal/researchrun/runner_test.go`

### Step 1：为年度加载增加标签缓冲区

在不改变现有回测调用行为的前提下，给 `researchrun.Config` 增加默认 0 的 `ForwardDays`，给 `YearData` 增加只用于研究标签的 `Future extend.Klines`：

```go
type Config struct {
    // existing fields...
    ForwardDays int
}

type YearData struct {
    His    extend.Klines
    Dks    extend.Klines
    Future extend.Klines
    Mks    protocol.Klines
}
```

- `ForwardDays=0` 时现有回测和调用结果不变；
- 因子 v4 分析设置为 `max(Horizons)`；
- `Future` 从请求年份后的真实交易日取得，不计入 Dks、不进入因子前缀；
- 下年读取失败不能抹掉本年 Dks，而是通过独立 forward coverage/失败原因披露；
- 数据集真实尾部允许 Future 不足，标签层逐项记 `insufficientHorizon`；
- 加载实现必须避免把下一年 Future 重复拼入下一年度 Dks。

测试跨年 12 月信号能使用次年 1 月价格，同时计数桩证明 Factor 只看到信号日以前数据。

### Step 2：拆出因子观察

建立内部对象：

```go
type FactorObservation struct {
    Date  time.Time
    Code  string
    Value float64
    KlineIndex int
}

type LabeledObservation struct {
    FactorObservation
    Horizon int
    Return  float64
}
```

加载每只股票每年数据时：

1. 拼接 prehistory 与当年 Dks；
2. 因子只使用 `series[:base+i+1]`；
3. 因子值每天只算一次；
4. 各 Horizon 只做标签投影；
5. 本票 local 数据完成后再锁内合并。

### Step 3：实现标签纯函数

建议接口：

```go
func labelReturn(dks, future extend.Klines, signalIdx, horizon int, kind string) (float64, LabelSkipReason, bool)
```

`next_open_to_close`：

```text
entry = dks[signalIdx+1].Open
exit  = dks[signalIdx+horizon].Close
```

`same_close_to_close_legacy` 保留现有公式。0、NaN、Inf、越界、缺失开盘/收盘分别返回明确 skip reason。

### Step 4：覆盖率

新增 `LabelCoverage` 聚合，并保证：

- 每个 Horizon 单独统计；
- sum(labeled + all skip reasons) 与 eligible signal 对齐；
- 股票加载失败仍走现有 Coverage/YearCoverage；
- 标签失败不冒充股票加载失败。

### Step 5：测试无前视和边界

至少覆盖：

- h=1 精确使用次日 Open→次日 Close；
- 修改 `t+2` 以后价格不影响 h=1 因子值；
- 修改未来 K 线不影响任何 `factor(t)`；
- 多 Horizon 共用一次因子调用（计数桩）；
- 年末 Horizon 跨年：从显式 Future 缓冲区生成；整个数据集尾部或下年缺失才记录 insufficient horizon；
- 停牌/涨跌停数据不足时质量标记正确。

**验证：**

```powershell
go test ./internal/lab -run 'Factor(Label|Observation)|RunAnalysis'
go test ./internal/researchrun -run 'Forward|YearData'
```

**完成条件：**标签公式、跳过原因和多周期复用由纯测试锁定。

## Task 3：扩展 IC、HAC、衰减与换手统计

**文件：**

- Add: `internal/lab/factor_stats_extended.go`
- Add: `internal/lab/factor_stats_extended_test.go`
- Add: `internal/lab/factor_turnover.go`
- Add: `internal/lab/factor_turnover_test.go`
- Modify: `internal/lab/analysis.go`
- Modify: `internal/lab/analysis_test.go`

### Step 1：HAC/Newey-West 纯函数

建议接口：

```go
func hacMeanTStat(xs []float64, lag int) (t, se *float64)
func extendedICStats(ics []float64, expectedDirection string, lag int) ExtendedICStats
```

测试使用小数组手算 `γ0/γ1/Bartlett weight/LRV`，不得只拿另一个库输出做断言。覆盖：

- lag=0 与朴素均值标准误关系；
- 正自相关使 HAC 标准误增大；
- 常数、单样本、NaN/Inf、LRV<=0 返回 null；
- ICIR 和年化 ICIR 定义固定；
- positive/direction-consistent rate 正确。

若实现 p-value，使用明确自由度/正态近似并写入注释；不得悄悄引入新依赖。

### Step 2：衰减结果

按 Horizon 升序输出 `Mean IC`、`HAC t`、首末组 spread。不要用相邻 Horizon 之间插值或平滑值作为原始统计。

### Step 3：分组换手

对每日分组成员集合计算：

```text
turnover(t, p) = 1 - |group(t) ∩ group(t-p)| / |group(t)|
```

同时记录两期组规模，组缺失则 null。并列块导致规模变化时不截断成员来制造固定组数。

### Step 4：保持旧统计

- v3 继续使用 `ICStats`；
- v4 主展示 `ExtendedICStats`；
- v4 兼容镜像字段由主 Horizon 转换，不改变旧 JSON tag；
- 旧单测不能为适配新结果而放宽断言。

**验证：**

```powershell
go test ./internal/lab -run 'HAC|ExtendedIC|Turnover|Analysis'
```

**完成条件：**所有统计都是纯函数、可手算复核、缺失不冒充零。

## Task 4：股票池、数据来源和可选中性化

**文件：**

- Add: `researchdata/universe.go`
- Add: `researchdata/universe_test.go`
- Add: `researchdata/universe_file.go`
- Add: `researchdata/universe_file_test.go`
- Add: `internal/lab/factor_neutralize.go`
- Add: `internal/lab/factor_neutralize_test.go`
- Modify: `common.go`（只注入默认只读 Universe；不改策略合同）
- Modify: `internal/lab/runner.go`（注入 View/Universe 依赖）

### Step 1：定义历史成员只读接口

建议放在 `researchdata`，保持供应商无关：

```go
type Universe interface {
    Codes(asOf time.Time) ([]string, error)
    CodesBetween(start, end time.Time) ([]string, error)
    Contains(code string, asOf time.Time) (bool, error)
    Snapshot() UniverseSnapshot
}
```

`UniverseSnapshot` 保存 ID、版本、来源、覆盖日期、是否包含退市股票和 PIT 状态。

`CodesBetween` 返回区间内任一天曾是成员的代码并集，供加载层覆盖已经退市、今天不在静态列表中的证券；每日聚合仍必须调用 `Contains` 或等价批量快照过滤当日成员，不能把区间并集直接当成每天的截面。

### Step 2：实现有界文件适配器

首版只读 CSV/JSONL 适配器，字段至少：

```text
code, listed_at, delisted_at, board, available_at, source, version
```

要求：

- 一次加载后不可变；
- 路径只来自本地配置，不由 HTTP 请求提供；
- 重复代码、非法日期、listed>delisted、缺 available_at 在严格模式拒绝；
- 无文件时回退 current_static，但显式降级，不阻塞旧探索流程；
- 不下载第三方数据，不把适配器存在当作数据可信证明。

### Step 3：数据来源快照

从当前 Runner 依赖生成 `DataProvenance`。无法获得 price version/adjustment 时写 `unknown`，不要让前端填写后伪装系统已验证。

### Step 4：中性化纯函数

建议：

```go
func neutralizeCrossSection(obs []ExposureObservation, spec NeutralizationSpec) NeutralizationResult
```

实现 MAD 去极值、rank/zscore、行业 dummy + log(size) OLS 残差。测试：

- 残差与截距/size 近似正交；
- 单行业、共线、缺行业、size<=0、样本不足；
- 输入顺序改变结果不变；
- PIT View 缺字段时披露缺失，不用零填充。

若本阶段缺少可靠行业/市值数据，功能可以完成为“可选且数据不足时 unavailable”，但严格 validation policy 必须据此返回 insufficient。

**验证：**

```powershell
go test ./researchdata
go test ./internal/lab -run 'Universe|Neutral'
```

**完成条件：**历史成员合同与质量等级可运行；当前静态股票池不会被误标为专业验证数据。

## Task 5：AnalysisReport v4 编排和不可变存储

**文件：**

- Modify: `internal/lab/analysis.go`
- Modify: `internal/lab/analysis_run_test.go`
- Modify: `internal/lab/analysis_store.go`
- Modify: `internal/lab/analysis_store_test.go`
- Modify: `internal/lab/factor_e2e_test.go`

### Step 1：组合新的分析流水线

v4 `runAnalysis` 顺序固定：

```text
解析并 hash Protocol
→ 解析股票池和数据来源，取得区间历史成员并集
→ ForEachCodeYearData 加载
→ 每个股票日只算一次 FactorObservation
→ 按 Horizon 生成可交易标签
→ 按交易日历史成员关系过滤截面
→ 每日截面 raw IC / groups / turnover
→ 可选 exposure / neutralized 结果
→ 年度和全区间聚合
→ 生成 EvidenceClass 与质量限制
→ 写不可变 v4 报告
```

避免继续把所有逻辑堆入 `analysis.go`：该文件保留编排和现有类型兼容，纯算法分别放 Task 2～4 新文件。

### Step 2：兼容镜像

- 主 Horizon = `Horizons[0]`；
- 旧 `Window/Stats/Groups/Years/Daily` 镜像主 Horizon；
- 镜像明确标记 deprecated；
- v4 HTML/CSV 从 `Horizons[]` 输出，不能只显示镜像；
- 旧固定 `report.json` 兼容镜像继续 best-effort。

### Step 3：资源边界

加入可配置但有上限的资源预算：

- Horizon 最多 8；
- 全档 quantile 预算只排序一次；
- 大样本按年/日聚合，避免同时保留所有 K 线副本；
- 进度分“加载/因子/聚合”阶段，未知总量时使用 indeterminate；
- stop/cancel 在各阶段有检查点。

### Step 4：端到端数据测试

构造 3～5 只股票的临时日线库，验证：

- 因子方向已知时各 Horizon IC 正确；
- next-open 标签不同于 legacy close 标签；
- 年度、全区间与 Horizon 覆盖一致；
- current_static 报告为 exploratory；
- v4 保存重启后可恢复。

**验证：**

```powershell
go test ./internal/lab -run 'AnalysisV4|RunAnalysis|FactorE2E|AnalysisStore'
```

**完成条件：**后端可以在没有 UI 的情况下完整生成并恢复 v4 专业分析报告。

## Task 6：追加式试验账本

**文件：**

- Add: `internal/lab/factor_trial.go`
- Add: `internal/lab/factor_trial_test.go`
- Add: `internal/lab/factor_trial_store.go`
- Add: `internal/lab/factor_trial_store_test.go`
- Modify: `internal/lab/analysis.go`

### Step 1：领域与 ID

定义 `FactorTrial`、`TrialStatus`、受限 ID 和规范化 hash。状态至少：

```text
running | completed | failed | canceled | insufficient
```

Trial 记录只描述一次运行，不保存用户可编辑名称。Family/Variant 来自协议并进入 hash。

### Step 2：Store

建议目录 `data/lab/factor-trials/`，接口：

```go
func (s *TrialStore) Start(protocol ResearchProtocol, analysisID string) (FactorTrial, error)
func (s *TrialStore) Finish(id string, result TrialResult) error
func (s *TrialStore) ListFamily(familyID string) ([]FactorTrial, error)
```

为避免覆盖，`Start` 写不可变 request，`Finish` 写独立 result 文件；完整 trial 由二者组合读取。失败和取消也必须 Finish。

### Step 3：分析集成

- 只对带 Protocol 的 v4 分析登记；
- analysis ID 在 Trial Start 前生成；
- 任意返回路径都完成对应 trial 状态；
- 报告保存失败也记录 failed；
- family 列表稳定排序并包含所有状态。

### Step 4：校正辅助

实现独立纯函数 `benjaminiHochberg([]p)`，仅作为家族披露。无 p-value 的试验保留在计数中但 q 值为 null，具体处理在报告中说明。

**验证：**

```powershell
go test ./internal/lab -run 'Trial|Benjamini'
```

**完成条件：**失败和取消不再从研究历史消失，冻结时能取得稳定 trial 清单。

## Task 7：冻结验证领域模型与不可变 Store

**文件：**

- Add: `internal/lab/factor_validation.go`
- Add: `internal/lab/factor_validation_test.go`
- Add: `internal/lab/factor_validation_store.go`
- Add: `internal/lab/factor_validation_store_test.go`
- Modify: `internal/lab/factor_candidate_store.go`（只增加按 revision 安全读取）
- Modify: `internal/lab/factor_candidate_store_test.go`

### Step 1：领域类型

定义：

- `ValidationProtocol`；
- `WalkForwardSpec`；
- `ValidationPolicy`；
- `ValidationState`；
- `ValidationVerdict`；
- `ValidationWindowReport`；
- `FactorValidationReport`。

状态转换必须由纯函数控制：

```text
frozen → running
running → passed | failed | insufficient | error
```

完成态不可重新运行；重试创建新的 Validation ID，并可写 `supersedes`。

### Step 2：冻结函数

```go
func freezeValidation(
    candidate FactorCandidate,
    evidence AnalysisReport,
    trials []FactorTrial,
    req CreateValidationRequest,
    now time.Time,
) (ValidationRequest, error)
```

校验：

- candidate ID/revision 精确匹配；
- evidence hash 仍有效；
- FactorVersion 与候选一致；
- discovery analysis IDs 都存在且因子一致；
- family trial 清单完整；
- purgeDays >= max Horizon；
- retrospective/prospective 与冻结时间关系合法；
- policy 数值有限且范围合理。

### Step 3：按 revision 读取候选

为 CandidateStore 增加内部或导出受限方法：

```go
func (s *CandidateStore) GetRevision(id string, revision int) (FactorCandidate, error)
```

复用现有路径校验和 evidence hash，不允许 API 接收文件名。

### Step 4：ValidationStore

目录和原子语义按设计 §12。接口建议：

```go
func (s *ValidationStore) Create(req CreateValidationRequest, deps FreezeDependencies) (ValidationRequest, bool, error)
func (s *ValidationStore) Get(id string) (ValidationView, error)
func (s *ValidationStore) SaveWindow(id string, window ValidationWindowReport) error
func (s *ValidationStore) Complete(id string, report FactorValidationReport) error
func (s *ValidationStore) List(filter ValidationFilter) ([]ValidationSummary, error)
```

Create request ID 幂等；同 ID 不同 hash 返回冲突。

**验证：**

```powershell
go test ./internal/lab -run 'FreezeValidation|ValidationStore|CandidateRevision'
```

**完成条件：**候选 revision、证据、协议、trial 和 policy 已形成不可变冻结记录。

## Task 8：滚动验证引擎与机器门禁

**文件：**

- Add: `internal/lab/factor_validation_run.go`
- Add: `internal/lab/factor_validation_run_test.go`
- Add: `internal/lab/factor_validation_policy.go`
- Add: `internal/lab/factor_validation_policy_test.go`

### Step 1：生成窗口

纯函数：

```go
func validationWindows(available Range, spec WalkForwardSpec, maxHorizon int) ([]ValidationWindow, error)
```

测试：

- 3 年 train/1 年 test/1 年 step；
- purge 后无标签跨边界；
- 年份不足；
- 非连续可用年份；
- prospective 不包含 FrozenAt 以前新增不了的日期；
- 最大 Horizon 变化会使不足 purge 被拒绝。

### Step 2：窗口执行

每个测试窗口：

- 使用冻结 Factor/Days/ImplementationVersion；
- 使用冻结 Universe/Data/Label/Neutralization；
- 不在窗口内改参数或重选方向；
- 复用 v4 分析纯流水线，但限定测试日期；
- train 区域只提供发现上下文摘要，不参与测试统计；
- 每个窗口单独保存 Coverage、质量状态和失败原因。

不得调用 `core.WalkForward`，也不得使用其 `OverfitScore>3` 作为因子结论。

### Step 3：门禁评估

纯函数：

```go
func evaluateValidation(req ValidationRequest, windows []ValidationWindowReport) ValidationVerdict
```

评估顺序：

1. 完整性/存储错误 → `error`；
2. 历史股票池、PIT、标签、覆盖、有效窗口不满足硬门禁 → `insufficient`；
3. 数据合格但冻结统计门槛不满足 → `failed`；
4. 全部门禁满足 → `passed`。

Verdict 保存逐项 checks：name、expected、actual、result、reason。不得只保存 bool。

### Step 4：聚合

聚合只用测试窗口，并披露：

- 按观察日加权和按窗口等权两种结果；
- 方向一致窗口比例；
- 每个 Horizon 的 IC/spread；
- coverage 加权；
- 失败/insufficient 窗口数量。

默认机器门禁使用哪种聚合必须写入 policy，不允许前端完成后切换最有利口径。

**验证：**

```powershell
go test ./internal/lab -run 'Validation(Window|Policy|Run|Verdict)'
go test ./internal/researchrun
```

**完成条件：**给定冻结输入，结论完全确定、可手算解释，任何缺失不会静默通过。

## Task 9：Runner、状态和 HTTP API 集成

**文件：**

- Modify: `internal/lab/runner.go`
- Modify: `internal/lab/server.go`（仅依赖注入和注册路由）
- Add: `internal/lab/server_trial.go`
- Add: `internal/lab/server_trial_test.go`
- Add: `internal/lab/server_validation.go`
- Add: `internal/lab/server_validation_test.go`
- Modify: `cmd/lab/main.go`（如需注入 Universe/Stores）

### Step 1：依赖注入

Server 增加：

- `trialStore`；
- `validationStore`；
- historical/current Universe；
- 继续复用同一个 Runner 和 factor data View。

测试构造器接收临时根，不写生产 `data/` 或 `output/`。

### Step 2：Runner task

增加 `StartValidation(id)`，与回测、因子分析共用 `mu.TryLock` 和 stopCh：

- `task.Store("factor_validation")` 先于 `state.Store("running")`；
- Status 增加 validation ID、window done/total、stage；
- total 未知时前端使用 indeterminate；
- Stop 后不生成完成报告；
- latest validation 以 Store 为准，不能只依赖内存。

### Step 3：路由

按设计实现：

```text
GET  /api/factor-trials?familyId=
POST /api/factor-validations
POST /api/factor-validations/{id}/run
GET  /api/factor-validations
GET  /api/factor-validations/{id}
```

规则：

- `decodeStrict`、64 KiB 限制、未知字段拒绝；
- 创建成功 201，幂等命中 200；
- revision/hash/factor drift/任务互斥 409；
- 非法 ID 400，不存在 404；
- 损坏、hash 不匹配 500，响应不含绝对路径；
- list filters 使用白名单枚举。

### Step 4：服务测试

覆盖创建幂等、冲突、run 重复、停止、重启恢复、状态顺序、损坏文件、路径穿越、未知 JSON 字段和正文过大。

**验证：**

```powershell
go test ./internal/lab -run 'Server(Trial|Validation)|RunnerValidation|Status'
go build ./cmd/lab
```

**完成条件：**完整验证链可仅通过 API 驱动，重启后结果不丢失。

## Task 10：专业研究与验证页面

**文件：**

- Modify: `DESIGN.md`
- Modify: `internal/lab/web/lab/index.html`
- Add/Modify: 与嵌入页面有关的 `internal/lab/*_test.go`

### Step 1：更新视觉合同

在 DESIGN.md 增加：

- 快速探索/专业验证的模式所有权；
- evidence class、verdict、quality warning 的颜色和文字规则；
- 多 Horizon 图表的统一颜色映射；
- 验证详情的 gate checklist；
- 不使用单一绿色大数字代表专业通过。

不要重写现有 North Star、排版和候选页合同。

### Step 2：专业模式表单

新增字段：

- 假设 ID、假设说明、预期方向、失效条件；
- 股票池与数据质量只读摘要；
- Label kind；
- Horizons 多选/输入；
- HAC 模式；
- 换手间隔；
- 可选中性化；
- Trial family/variant。

输入错误就地显示，不使用 alert。高级字段分组折叠，但提交前显示完整协议摘要和 evidence 限制。

### Step 3：v4 结果

展示：

- IC 与 spread 衰减曲线；
- 每 Horizon 的 HAC t、ICIR、方向一致率；
- 年度稳定性；
- 分组换手；
- 原始/中性化切换；
- 股票、年份、标签覆盖及跳过原因；
- trial family 累计运行数；
- evidence class 与数据质量限制。

图表不得把 null 连成看似完整的证据；允许视觉断点并提供无数据原因。

### Step 4：冻结和验证详情

候选列表增加“创建验证”，流程：

1. 锁定当前 revision；
2. 选择 retrospective/prospective；
3. 配置窗口和 policy；
4. 展示将冻结的分析、trial、数据和标签；
5. 创建后不能编辑；
6. 单独点击运行。

详情页/面板显示逐窗口和逐门禁证据。`insufficient` 使用中性/琥珀提示，不与失败混为一谈。

### Step 5：状态、响应式和可访问性

- 复用共享 statusbar，task 文案为“因子验证中”；
- 表格窄屏允许横向滚动或卡片化，不裁掉 verdict 原因；
- 表单 label/description/error 通过 ARIA 关联；
- verdict 不只靠颜色；
- reduced motion 下不执行非必要动画；
- ECharts/GSAP CDN 失败时文字表格仍可完成审核。

### Step 6：静态和浏览器验收

```powershell
go build ./cmd/lab
go test ./internal/lab -run 'Server(Analysis|Trial|Validation)|FactorE2E'
```

提取内联 JS 到系统临时文件做 `node --check`，不把临时文件写进仓库。重新启动实际 `go run ./cmd/lab` 后检查，避免旧端口继续服务 stale embed。

浏览器至少核对：

- 1440px、900px、560px；
- 快速探索旧流程；
- 专业 v4 分析；
- 保存候选→冻结→运行→查看 verdict；
- API 错误、无样本、停止、重启恢复；
- 键盘导航和 reduced motion。

**完成条件：**用户无需读 JSON 即可解释每个结论，旧流程没有回归。

## Task 11：端到端、性能、文档和交付

**文件：**

- Add: `internal/lab/factor_validation_e2e_test.go`
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-09-18-factor-validation-v1-design.md`
- Modify: `docs/superpowers/plans/2026-09-18-factor-validation-v1.md`
- Modify: `MEMORY.md`（仅在实现和验证完成后记录长期合同）

### Step 1：完整链路测试

构造临时：

- 日 K 数据；
- 历史成员文件，包含已退市代码；
- PIT 行业与市值记录；
- 两个 trial 变体；
- candidate revision 1，随后修改为 revision 2。

验证：

```text
v4 分析
→ trial completed
→ 保存 candidate revision 1
→ 冻结 validation 指向 revision 1
→ candidate 修改为 revision 2
→ validation 仍执行 revision 1
→ 多窗口报告
→ policy verdict
→ 重启后读取同一结果
```

### Step 2：负向链路

至少覆盖：

- current_static + strict policy → insufficient；
- unknown PIT → insufficient；
- 方向反转且数据合格 → failed；
- 覆盖不足 → insufficient；
- factor version 漂移 → run 前 409/fail closed；
- evidence hash 损坏 → 500/error；
- purge 不足 → 400；
- stop → 无完成报告；
- prospective 不得吞入冻结前数据；
- trial 缺失 → 不能冻结。

### Step 3：性能证据

使用固定合成数据或本地小样本 benchmark，记录：

- v3 单周期基线；
- v4 四周期；
- 因子 Value 调用次数；
- 最大内存量级；
- 中性化开/关差异。

验收重点是多 Horizon 不按数量重复因子计算。不能用单台机器毫秒数作为永久 SLA；如设置资源上限，写成配置和可解释错误。

### Step 4：全量验证

建议顺序：

```powershell
gofmt -w <本次修改的 Go 文件>
go test ./internal/lab
go test ./internal/researchrun
go test ./researchdata
go test ./...
go vet ./...
go build ./cmd/lab
```

Windows 下若 `-race` 不受工具链支持，记录未运行原因，不把普通测试说成 race 通过。网络/CDN 不可用时仍验证无动画、无图表降级路径。

### Step 5：最终差异检查

检查：

- 没有修改受保护核心结构；
- 没有覆盖用户原有工作；
- 没有调试日志、绝对路径、真实研究数据或敏感信息；
- v3/v4、candidate/validation 的版本边界清晰；
- 所有 JSON 新字段有兼容测试；
- passed 不能通过人工 API 写入；
- current_static/unknown PIT 不会在严格政策下通过；
- docs 若需要纳入 Git，已明确处理 ignored 状态。

### Step 6：更新文档状态

只有完成全部验收后：

1. 设计和计划状态改成“已实施并验证”，列出实际检查与限制；
2. README 增加研究协议、验证 API、存储备份和数据质量说明；
3. MEMORY.md 只记录稳定合同，不复制任务日志；
4. 未完成的中性化数据、历史股票池或 prospective 积累必须继续标为限制，不能因代码存在而写成数据已具备。

**完成条件：**所有必要测试通过，浏览器实际服务验证完成，最终差异没有无关变更。

## 5. 并行实施 Work Orders

若使用多个实施者，只允许按下列文件所有权并行；共享热点由集成负责人串行处理。

### Work Order A：协议与统计

**独占：**

- `research_protocol*.go`
- `factor_stats_extended*.go`
- `factor_turnover*.go`

**不得修改：**`analysis.go`、`runner.go`、`server.go`、HTML。交付纯类型/函数/测试，由集成负责人接入。

### Work Order B：标签与观察层

**独占：**

- `factor_label*.go`
- `factor_observation*.go`

**不得修改：**共享热点。使用小接口和构造数据证明一次因子计算、多 Horizon 投影。

### Work Order C：股票池与中性化

**独占：**

- `researchdata/universe*.go`
- `factor_neutralize*.go`

`common.go` 和 `runner.go` 的注入改动以补丁说明交给集成负责人，不直接同时写。

### Work Order D：Store 和验证领域

在 Task 1/6 合同稳定后独占：

- `factor_trial*.go`
- `factor_validation*.go`（不含 server/HTML）

CandidateStore 按 revision 读取的共享修改由集成负责人完成。

### 集成负责人

独占共享热点：

- `analysis.go`
- `analysis_store.go`
- `runner.go`
- `server.go`
- `cmd/lab/main.go`
- `index.html`
- `DESIGN.md`
- `README.md`
- `MEMORY.md`

任何 Work Order 发现合同需扩大时，先提交证据和接口建议，不自行跨边界重构。

## 6. 需求追踪矩阵

| 设计验收 | 实施任务 | 主要证据 |
|---|---|---|
| AC-01 协议不可变 | Task 1/5/6 | Protocol hash、AnalysisStore、Trial 测试 |
| AC-02 可交易标签 | Task 2 | label 手算与前视隔离测试 |
| AC-03 多周期复用 | Task 2/5 | factor 调用计数、v4 E2E |
| AC-04 HAC | Task 3 | 手算长期方差测试 |
| AC-05 null 语义 | Task 3/5 | 常数/不足样本测试 |
| AC-06 股票池偏差 | Task 4/5/8 | current_static→exploratory/insufficient |
| AC-07 PIT fail closed | Task 4/8 | unknown/缺 AvailableAt 负向测试 |
| AC-08 失败 trial 保留 | Task 6 | failed/canceled family list |
| AC-09 revision 冻结 | Task 7/11 | revision 1/2 完整链路 |
| AC-10 purge | Task 8 | 窗口边界测试 |
| AC-11 verdict 不可手改 | Task 7/9 | API 状态转换测试 |
| AC-12 回放/前瞻区分 | Task 8/10 | 日期边界和页面标签 |
| AC-13 旧合同 | Task 0/1/5/11 | v3 fixture、旧 API E2E |
| AC-14 重启/损坏 | Task 7/9/11 | Store 与 HTTP 负向测试 |
| AC-15 可审计 UI | Task 10/11 | 浏览器验收清单 |

## 7. 发布门禁

只有同时满足以下条件，才可以把 v1 标为已完成：

1. v3 旧请求和报告仍可工作；
2. v4 默认标签为 next-open，不把 legacy 同收盘结果写成可交易；
3. HAC、多周期、换手和覆盖由后端计算，前端不二次推断；
4. current_static、unknown PIT 和数据不足能阻止严格 policy 通过；
5. Trial 包含失败和取消；
6. Validation 完全绑定候选 revision、证据 hash、trial 和 policy；
7. passed/failed/insufficient 只能由引擎生成；
8. retrospective/prospective 清晰可见；
9. API 重启恢复、损坏 fail closed、停止语义通过；
10. 实际重启后的 Lab 页面完成桌面和窄屏验收；
11. scoped、全量测试和最终差异检查有真实记录；
12. 所有尚未具备的外部数据能力在报告和交付说明中保持显式限制。

若历史股票池或可靠 PIT 数据尚未接入，代码可以先合并为探索能力，但发布状态必须写成“验证框架已实现，专业数据门禁尚未满足”，不能把框架完成等同于因子已经通过专业验证。
