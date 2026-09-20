# 专业因子验证体系 v1 设计

> 日期：2026-09-18  
> 状态：设计完成，待实施  
> 上游：`2026-09-17-factor-candidate-library-design.md`  
> 范围：研究协议、可交易收益标签、单因子完整诊断、冻结候选的滚动样本外验证  
> 本文只定义产品和技术契约，不代表相关能力已经实现

## 1. 背景与问题

当前 Strategy Lab 已经能够：

- 按交易日、股票截面计算 Spearman Rank IC；
- 使用等数量分组或固定数值区间观察未来收益；
- 展示年度稳定性、有效日期、股票和股票×年份覆盖率；
- 以不可变 `analysisId` 保存分析报告；
- 把分析证据保存成带因子实现版本的候选；
- 通过 `researchdata` 读取带 `EventAt`、`AvailableAt` 和修订版本的数据。

这些能力可以支持探索，但尚不能把候选称为“经过专业验证的因子”。当前主要缺口是：

1. 因子使用当日完整收盘数据时，未来收益仍从同一收盘价起算，没有把决策时点和可成交时点分开；
2. `ICStats.TStat` 使用独立样本标准误，未修正逐日 IC 自相关和多日收益重叠；
3. 报告缺少多周期衰减、换手、风险暴露和中性化对照；
4. 当前 `all` 股票池来自当前代码列表，缺少退市股票和历史时点成分，存在生存者偏差；
5. 候选只证明“样本内分析曾经发生”，没有冻结参数、验证区间和机器生成的晋级结论；
6. 系统没有完整记录同一研究假设试过多少个因子、窗口和阈值，无法披露选择偏差。

专业因子研究的目标不是找到一次漂亮结果，而是形成可审计证据链：

```text
经济假设
  → 研究协议
  → PIT 数据与历史股票池
  → 因子与可交易收益标签
  → 单因子诊断
  → 稳健性和试验披露
  → 冻结候选
  → 滚动样本外验证
  → 明确结论和限制
```

## 2. 目标与非目标

### 2.1 v1 目标

1. 每次专业分析都绑定一份结构化研究协议，明确假设、股票池、数据、信号时点、收益标签、周期和试验家族。
2. 默认使用“`t` 日收盘后形成信号，`t+1` 日开盘进入”的可交易标签；保留旧同收盘口径仅用于兼容和对照。
3. 一次分析可同时计算 1/5/10/20 等多个未来周期，输出 IC 衰减和分组表现。
4. 增加 HAC/Newey-West t 值、ICIR、年化 ICIR、正 IC 比例和有效日数；旧朴素 t 值继续读取但不作为专业结论。
5. 输出分组成员换手、因子值分布、年度/市场阶段稳定性和可选的行业/市值暴露与中性化对照。
6. 记录每个研究变体和试验家族；冻结时快照试验次数，不把反复调参后的最佳结果伪装成单次检验。
7. 从现有候选 revision 创建不可变验证任务，使用非重叠滚动窗口运行，并由后端生成 `passed`、`failed` 或 `insufficient` 结论。
8. 区分回放式滚动验证和真正的前瞻验证；已经看过的历史不得标为“未见样本”。
9. 对历史股票池、PIT、复权、可交易性或数据版本不满足要求的结果降级标识，不用统计结果掩盖数据缺陷。
10. 保持现有分析、候选、简单策略和回测 API 向后兼容。

### 2.2 v1 明确不做

- 不自动生成新因子、搜索公式或遍历全部参数；
- 不自动推荐“最佳窗口”“最佳阈值”或全市场统一 IC 门槛；
- 不实现机器学习选权重、遗传搜索或贝叶斯优化；
- 不把多个因子的 AND 过滤包装成专业多因子评分模型；
- 不实现完整风险模型、优化器、容量模型或实盘下单；
- 不宣称理论多空 spread 可以在 A 股直接做空复制；
- 不以 PBO、Deflated Sharpe 或单一数字替代完整证据；v1 先保留试验账本和校正所需输入；
- 不重写 `core.WalkForward`。现有实现面向策略收益且有固定“过拟合分数”语义，不能直接冒充因子验证引擎；
- 不修改 `PROJECT_RULES.md` 保护的核心结构体和接口；
- 不在本设计中选择或接入收费数据供应商。

## 3. 研究证据等级

报告必须根据数据和验证方式标注证据等级，而不是只显示“成功”。

| 等级 | 含义 | 允许用途 |
|---|---|---|
| `exploratory` | 当前静态股票池、旧同收盘标签、未知数据版本或其他关键限制仍存在 | 探索方向、保存候选；不得晋级 |
| `retrospective` | 协议冻结后按历史时间滚动回放，但验证日期在冻结前已经存在 | 稳健性证据；必须显示“历史回放” |
| `prospective` | 冻结之后新产生的数据按原协议评价，期间未修改因子或门槛 | 真正前瞻证据 |

证据等级和统计结论相互独立。例如高 IC 的静态股票池报告仍是 `exploratory`；低 IC 的前瞻报告仍是 `prospective + failed`。

## 4. 核心对象与所有权

### 4.1 对象关系

```text
ResearchProtocol
  ├─ HypothesisSpec
  ├─ UniverseSpec
  ├─ DataProvenance
  ├─ SignalSpec
  ├─ LabelSpec
  ├─ DiagnosticSpec
  └─ TrialSpec
          │
          ▼
AnalysisReport v4 ──保存──> FactorCandidate revision
                                  │ 冻结 revision
                                  ▼
                         FactorValidation
                           ├─ ValidationProtocol
                           ├─ immutable request
                           ├─ window reports
                           └─ machine verdict
```

### 4.2 不改变现有候选状态语义

`CandidateStatus` 继续只有 `candidate` 和 `archived`。归档是资料管理动作，不是统计结论。

验证状态放在独立对象 `FactorValidation` 中：

```text
frozen → running → passed | failed | insufficient | error
```

- `passed/failed/insufficient` 只能由验证引擎根据冻结政策生成；
- 用户不能通过 `PUT /api/factor-candidates/{id}` 手工设置；
- 同一候选 revision 可以有多份不同协议的验证，列表展示最新一份和全部历史；
- 修改候选阈值或用途会产生新 revision，旧验证不会自动迁移。

## 5. 研究协议合同

### 5.1 建议结构

以下字段为合同草案，实施时可拆文件，但 JSON 语义必须保持：

```go
type ResearchProtocol struct {
    SchemaVersion int            `json:"schemaVersion"`
    Hypothesis    HypothesisSpec `json:"hypothesis"`
    Universe      UniverseSpec   `json:"universe"`
    Data          DataProvenance `json:"data"`
    Signal        SignalSpec     `json:"signal"`
    Labels        LabelSpec      `json:"labels"`
    Diagnostics   DiagnosticSpec `json:"diagnostics"`
    Trial         TrialSpec      `json:"trial"`
}

type HypothesisSpec struct {
    ID                string `json:"id"`
    Thesis            string `json:"thesis"`
    ExpectedDirection string `json:"expectedDirection"` // positive | negative | two_sided
    FailureCondition  string `json:"failureCondition"`
}

type SignalSpec struct {
    Frequency string `json:"frequency"` // v1: daily
    FormedAt  string `json:"formedAt"`  // v1: close
}

type LabelSpec struct {
    Kind     string `json:"kind"`     // next_open_to_close | same_close_to_close_legacy
    Horizons []int  `json:"horizons"` // 1..60，去重升序
}

type DiagnosticSpec struct {
    Grouping       GroupingConfig    `json:"grouping"`
    HACLagMode     string            `json:"hacLagMode"` // horizon_minus_one | fixed
    HACLag         int               `json:"hacLag,omitempty"`
    TurnoverPeriods []int            `json:"turnoverPeriods"`
    Neutralization NeutralizationSpec `json:"neutralization"`
}

type TrialSpec struct {
    FamilyID  string `json:"familyId"`
    VariantID string `json:"variantId"`
    Rationale string `json:"rationale"`
}
```

`HypothesisSpec` 不是展示备注。它在分析保存后不可修改；改变预期方向、因子窗口、收益标签或股票池都构成新的研究变体。

### 5.2 默认值不是硬门槛

建议 UI 默认值：

- 标签：`next_open_to_close`；
- 周期：`[1, 5, 10, 20]`；
- HAC lag：`horizon_minus_one`；
- 换手间隔：`[1, 5, 10, 20]`；
- 分组：5 组 quantile；
- 中性化：`none`，但显式显示“未中性化”。

这些值是可编辑研究配置，不是平台宣称的最佳参数。服务端限制只用于资源和数据安全，例如周期上限 60、数量上限 8；不得把资源限制描述成金融规律。

## 6. 股票池与数据来源

### 6.1 UniverseSpec

```go
type UniverseSpec struct {
    Mode              string `json:"mode"` // current_static | historical_membership | codes
    ID                string `json:"id"`
    Version           string `json:"version"`
    IncludeDelisted   bool   `json:"includeDelisted"`
    MembershipPIT     string `json:"membershipPit"` // verified | unverified
    TradabilityPolicy string `json:"tradabilityPolicy"`
}
```

规则：

- `current_static` 和当前 `all` 行为兼容，但证据等级最高只能是 `exploratory`；
- `historical_membership` 必须按每个交易日返回当时已上市且尚未退市的证券，并能包含现已退市代码；
- 运行前先取得研究区间内历史成员代码并集，确保已经退市、今天不在代码列表中的股票仍会被加载；计算每日截面时再按当日成员关系过滤，不能只用起点、终点或今天的成员快照；
- 指定代码 `codes` 适合研究个案，不得据此给出全市场截面结论；
- 后上市股票只从上市后参与；退市股票必须保留退市前历史和可获得的退市收益处理说明；
- ST、停牌、涨跌停、上市初期等过滤必须依据当时状态，不得使用今天的状态回填历史。

首版历史股票池允许使用有界本地适配器，但报告必须保存来源、版本、覆盖日期和质量等级。没有理想数据源时可以运行探索，不得静默升级证据等级。

### 6.2 DataProvenance

```go
type DataProvenance struct {
    PriceSource       string `json:"priceSource"`
    PriceVersion      string `json:"priceVersion"`
    Adjustment        string `json:"adjustment"` // none | forward | backward | unknown
    ResearchDataView  string `json:"researchDataView"`
    PITState          string `json:"pitState"` // verified | partial | unverified
    SnapshotAt        string `json:"snapshotAt"`
}
```

报告必须区分：

- 价格序列用于因子计算的复权口径；
- 用于模拟成交的原始价格口径；
- 财务、行业、市值等字段的 `AvailableAt` 与修订规则；
- 当前数据快照时间和可复现版本。

只有当前快照、没有发布日期或修订历史的数据，`PITState` 必须是 `unverified`，不能参加严格晋级。

## 7. 信号与收益标签

### 7.1 专业默认标签

对信号日索引 `t` 和周期 `h >= 1`：

```text
factor(t) = 使用截至 t 日收盘可见数据计算
entry     = Open[t+1]
exit      = Close[t+h]
return    = exit / entry - 1
```

因此：

- `h=1` 表示次日开盘买入、次日收盘退出；
- `h=5` 表示次日开盘买入、第 5 个交易日收盘退出；
- 尾部没有 `t+h`、次日无有效开盘价或不可交易时，该观察不产生标签；
- 因子计算仍只看到 `series[:t+1]`，标签计算不得把未来 K 线传入因子。

自然年只是加载和汇总边界，不是收益标签边界。分析每个代码年份时必须额外加载最多 `max(Horizons)` 个后续交易日作为只读标签缓冲区，使 12 月末信号可以使用次年价格完成标签。缓冲区绝不进入 `FactorContext.Klines`，也不计入本年度因子观察；只有整个数据集真实尾部或下年数据缺失时才记为 `insufficientHorizon`。

### 7.2 旧标签兼容

旧报告的：

```text
Close[t+h] / Close[t] - 1
```

标记为 `same_close_to_close_legacy`。它继续可读、可运行对照，但：

- 页面固定提示它没有解决完整收盘信号与同收盘成交冲突；
- 使用该标签的分析证据等级最高为 `exploratory`；
- 不得作为验证任务的唯一标签。

### 7.3 可交易性

当 `t+1` 日停牌、开盘价缺失或按政策不可买入时，v1 默认跳过该股票该日，并分别统计原因。涨停能否成交不能只从收盘涨幅推断；缺少可靠状态数据时标记 `tradability=partial`。

报告新增标签覆盖字段，至少包括：

- `eligibleSignals`；
- `labeledSignals`；
- `missingEntryPrice`；
- `missingExitPrice`；
- `untradableEntry`；
- `insufficientHorizon`。

## 8. 单因子统计合同

### 8.1 多周期结果

`AnalysisReport v4` 不再把单个 `Window` 作为唯一结果，新增：

```go
type HorizonAnalysis struct {
    Horizon      int                 `json:"horizon"`
    Label        string              `json:"label"`
    IC           ExtendedICStats     `json:"ic"`
    Groups       []FactorGroupStats  `json:"groups"`
    Summary      QuintileSummary     `json:"summary"`
    Years        []YearHorizonResult `json:"years"`
    Turnover     []TurnoverPoint     `json:"turnover"`
    LabelCoverage LabelCoverage      `json:"labelCoverage"`
}
```

旧 `Window/Stats/Groups/Years/Daily` 在一个兼容周期内继续镜像主周期结果；主周期默认取请求 `horizons` 中的第一个值。新代码以 `Horizons[]` 为准。

### 8.2 ExtendedICStats

```go
type ExtendedICStats struct {
    Pairs          int      `json:"pairs"`
    Mean           *float64 `json:"mean"`
    Std            *float64 `json:"std"`
    ICIR           *float64 `json:"icir"`
    AnnualizedICIR *float64 `json:"annualizedIcir"`
    PositiveRate   *float64 `json:"positiveRate"`
    NaiveTStat     *float64 `json:"naiveTStat"`
    HACTStat       *float64 `json:"hacTStat"`
    HACLag         int      `json:"hacLag"`
    PValue         *float64 `json:"pValue"`
}
```

定义：

- `ICIR = Mean / Std`，不年化；
- `AnnualizedICIR = sqrt(252) * ICIR`，仅日频；
- `PositiveRate` 为有限日 IC 中 `IC > 0` 的比例；预期负向因子在展示层同时给出方向一致率；
- 无有效结果使用 JSON `null`，不得用数值 0 冒充“真实为零”；
- 朴素 t 值只用于兼容和对照；专业主展示使用 HAC t 值。

### 8.3 HAC/Newey-West

对按日期排序的日 IC 序列 `x_t`，用 Bartlett 权重估计长期方差：

```text
LRV = γ0 + 2 * Σ[l=1..L] (1 - l/(L+1)) * γl
SE(mean) = sqrt(LRV / n)
HAC t = mean / SE(mean)
```

默认 `L=max(h-1, 0)`，因为多日未来收益发生重叠。用户选择固定 lag 时必须记录。有效日不足、长期方差非正或数值不稳定时返回 `null` 并说明原因，不回退成朴素 t 值。

### 8.4 分组、衰减与换手

- 每个周期继续使用同一日截面因子排序，避免周期之间因缺失处理不同而产生隐式股票池漂移；
- 报告以 Horizon 序列形成 IC 与 spread 衰减曲线；
- quantile 换手定义为指定间隔前后该组新增成员比例；
- 并列值保持现有“并列块不拆分”语义，组规模变化必须参与分母披露；
- spread 是最高组减最低组的理论研究诊断，不等于可直接做空的可交易收益；
- v1 不用换手乘固定费率伪造精确净收益。成本后表现交给具有成交和持仓语义的策略回测；报告可显示成本拖累情景，但必须标为估算。

## 9. 风险暴露与中性化

### 9.1 中性化配置

```go
type NeutralizationSpec struct {
    Mode            string `json:"mode"` // none | industry_size
    IndustryDataset string `json:"industryDataset,omitempty"`
    SizeDataset     string `json:"sizeDataset,omitempty"`
    Winsorization   string `json:"winsorization"` // none | mad
    Standardization string `json:"standardization"` // rank | zscore
}
```

`industry_size` 的日截面顺序固定为：

```text
过滤无效值
→ 可选 MAD 去极值
→ 标准化
→ 对行业哑变量和 log(流通市值) 做截面回归
→ 残差作为中性化因子
→ 用同一收益标签重新计算 IC 和分组
```

所有行业与市值数据都必须按 `AsOf` 从 PIT View 读取。使用当前行业分类或当前流通股本回填历史时只能标为 `unverified`。

### 9.2 展示原则

报告同时展示：

- 原始因子表现；
- 对行业和市值的日截面暴露；
- 中性化后表现（配置并且数据足够时）；
- 中性化前后有效样本变化。

不规定所有因子必须中性化。研究者可以研究本身就是风险溢价的规模或行业因子，但必须显式声明；机器门禁根据冻结的 `ValidationPolicy` 判断，而不是平台暗中替用户决定。

## 10. 试验账本与多重检验

### 10.1 Trial 不是可选日志

专业模式每次运行都写入追加式试验记录：

```go
type FactorTrial struct {
    ID             string           `json:"id"`
    FamilyID       string           `json:"familyId"`
    VariantID      string           `json:"variantId"`
    AnalysisID     string           `json:"analysisId"`
    Factor         FactorSnapshot   `json:"factor"`
    ProtocolHash   string           `json:"protocolHash"`
    StartedAt      string           `json:"startedAt"`
    FinishedAt     string           `json:"finishedAt"`
    Status         string           `json:"status"`
}
```

- 同一假设下改变窗口、方向、股票池、标签或中性化方式，使用相同 `FamilyID`、新的 `VariantID`；
- 完全不同的经济假设使用新的 family；
- 失败、取消和无结果的试验也保留，不能只记录成功结果；
- 前端展示家族内累计试验数；
- 冻结验证时保存当时可见的全部 trial ID 和 hash。

v1 可以提供基于冻结家族 p 值的 Benjamini-Hochberg q 值作为辅助披露，但不得把 q 值单独用作自动晋级条件。PBO/Deflated Sharpe 留待组合层有完整策略收益序列后实现。

## 11. 冻结与滚动验证

### 11.1 ValidationProtocol

```go
type ValidationProtocol struct {
    SchemaVersion  int              `json:"schemaVersion"`
    EvidenceClass  string           `json:"evidenceClass"` // retrospective | prospective
    CandidateID    string           `json:"candidateId"`
    CandidateRevision int           `json:"candidateRevision"`
    DiscoveryAnalysisIDs []string   `json:"discoveryAnalysisIds"`
    TrialIDs       []string         `json:"trialIds"`
    Windows        WalkForwardSpec  `json:"windows"`
    Policy         ValidationPolicy `json:"policy"`
}

type WalkForwardSpec struct {
    TrainYears int `json:"trainYears"`
    TestYears  int `json:"testYears"`
    StepYears  int `json:"stepYears"`
    PurgeDays  int `json:"purgeDays"`
}
```

默认建议是 3～5 年观察窗口、1 年测试窗口、1 年步长，但它是可配置建议，不是硬编码金融规则。`PurgeDays` 至少覆盖最大收益周期，防止边界附近标签跨越训练/测试区间；不满足时服务端拒绝。

因子本身不需要“训练”时，Train 窗口仍用于披露当时可见的方向和参数依据。验证引擎不能在每个测试窗口重新选择窗口、方向或阈值；如需滚动再训练，必须另建显式模型协议，不属于 v1。

### 11.2 冻结内容

冻结时复制并哈希：

- 候选指定 revision 的 `FactorRef`、`Use` 和发现期证据；
- 完整 ResearchProtocol；
- 因子实现版本；
- 股票池、数据和标签版本；
- 全部已登记 trial；
- Walk-Forward 窗口；
- 验收政策。

冻结后不能编辑。发现错误只能创建新的 validation；旧记录保留并可标记 `supersededBy`，但不删除。

### 11.3 ValidationPolicy

机器门禁只检查冻结协议中的明确条件：

```go
type ValidationPolicy struct {
    MinCoverage          float64 `json:"minCoverage"`
    MinValidWindows      int     `json:"minValidWindows"`
    RequiredDirectionRate float64 `json:"requiredDirectionRate"`
    MaxICDecayRatio      *float64 `json:"maxIcDecayRatio,omitempty"`
    RequireHistoricalUniverse bool `json:"requireHistoricalUniverse"`
    RequireVerifiedPIT   bool    `json:"requireVerifiedPit"`
    RequireTradableLabel bool    `json:"requireTradableLabel"`
}
```

策略：

- 平台提供透明默认模板，但所有阈值均在冻结前可见、可编辑并进入 hash；
- 不提供“IC > 0.03 即专业因子”之类通用门槛；
- `passed` 要求所有硬性数据门禁通过，并满足冻结的统计规则；
- 数据不足、窗口不足或质量未知返回 `insufficient`，不能当作 `failed` 或 `passed`；
- 方向反转、覆盖合格但统计门槛未通过返回 `failed`；
- 执行异常或存储损坏返回 `error`，不生成统计结论。

### 11.4 前瞻验证

`prospective` 验证记录 `FrozenAt`。只有 `FrozenAt` 之后新进入系统且未被旧分析使用的日期才能累计。系统能证明的是自己的分析历史；无法证明用户没有在外部看过同一数据，因此页面使用“平台内前瞻证据”，不使用绝对化“从未见过”。

## 12. 存储与 API

### 12.1 存储目录

```text
data/lab/factor-trials/
  <trialId>.json

data/lab/factor-validations/
  <validationId>/
    request.json
    report.json
    windows/
      0001.json
      0002.json
```

- trial 与 validation 均使用服务端生成、正则校验的 ID；
- 所有正式文件 tmp+rename 原子写；
- `request.json` 先发布且永不修改；
- `report.json` 是完成标记；运行进度保留内存或独立临时状态，不覆盖最终报告；
- API 不接收路径，错误响应不暴露绝对路径；
- 读取到正式文件损坏、hash 不匹配或引用缺失时 fail closed。

### 12.2 API 草案

| 方法 | 路径 | 行为 |
|---|---|---|
| `POST` | `/api/analyze` | 兼容旧请求；带 protocol 时运行 v4 专业分析并登记 trial |
| `GET` | `/api/factor-trials?familyId=` | 查询试验家族，包含失败与取消记录 |
| `POST` | `/api/factor-validations` | 从指定候选 revision 冻结并创建验证 |
| `POST` | `/api/factor-validations/{id}/run` | 启动尚未运行的冻结验证 |
| `GET` | `/api/factor-validations` | 按候选、结论和证据等级筛选列表 |
| `GET` | `/api/factor-validations/{id}` | 返回请求、进度或最终不可变报告 |

创建和运行使用 request ID 幂等；同一 ID 内容不同返回 409。验证与现有回测/分析共用 Runner 单任务互斥和停止能力，`/api/status` 的 task 增加 `factor_validation`。

## 13. 页面工作流

### 13.1 专业研究模式

因子研究页增加“快速探索 / 专业验证”模式。专业模式按以下顺序：

1. 写研究假设和预期方向；
2. 选择股票池及查看数据质量；
3. 选择因子、窗口、标签与多个收益周期；
4. 选择分组、HAC 和可选中性化；
5. 确认试验 family/variant；
6. 运行并查看衰减、年度、换手、暴露和覆盖；
7. 保存候选；
8. 冻结候选 revision 并创建验证。

快速探索保留当前低门槛表单，但结果显著标注 `exploratory`。

### 13.2 验证详情

验证页必须展示：

- 冻结候选 revision 和 hash；
- 发现期与每个测试窗口；
- 数据质量门禁；
- 每个窗口方向、IC、HAC t、覆盖率和分组 spread；
- 聚合结论及逐条门禁通过/失败原因；
- `retrospective` 或 `prospective` 证据标签；
- 试验家族数量；
- “验证通过不等于可实盘交易”的固定说明。

不得只显示一个绿色“通过”。颜色必须配合文字和逐项证据，失败与数据不足要明显区分。

## 14. 兼容与迁移

1. `AnalysisReport v3` 继续读取；缺少 ResearchProtocol 时派生 `exploratory + same_close_to_close_legacy`，不猜测 PIT 或数据版本。
2. v4 报告保留 v3 主周期镜像字段至少一个兼容周期。
3. 现有 Candidate schema 不改写；新验证通过 candidate ID/revision 引用。
4. 旧候选可以创建验证，但必须先补充并冻结完整协议，且原发现证据仍标为 legacy。
5. 现有 `core.WalkForward` 保留原行为；新因子验证放在 `internal/lab`/`internal/factorvalidation`，不得改变受保护核心合同。
6. `current_static` 股票池和未知价格版本继续允许运行探索，但默认严格政策会给出 `insufficient`。
7. 不自动把历史候选标成 `passed`，也不根据页面中已有年度表格推断验证结论。

## 15. 安全、性能与失败语义

- 多周期因子值每天只计算一次；不同 Horizon 复用当日因子截面，避免重复加载和重复排序；
- `researchrun` 为因子分析提供显式 forward-label buffer；其长度、来源年份和实际取得天数进入覆盖统计，任何未来缓冲数据都不得进入因子前缀；
- 先建立 `(date, code)` 因子观察，再按 Horizon 生成标签；控制内存时允许按年分块，但聚合结果必须与非分块算法等价；
- 中性化缺数据时只剔除对应股票日并披露，不用 0 填充；
- 某一验证窗口失败不得静默跳过；最终结论至少为 `insufficient`，并保留失败详情；
- 停止任务返回 idle/aborted 语义，不写完整 `report.json`；已写窗口文件可以保留为诊断，但再次运行不得误认为已提交；
- 不为通过门禁而自动放宽 coverage、HAC lag、PIT 或股票池要求；
- 不在 HTTP 错误、报告、试验备注或项目记忆中写密钥和本机敏感路径。

## 16. 验收标准

### AC-01 研究协议不可变

同一 v4 报告保存完整协议及 hash；改变因子窗口、标签、股票池或预期方向会产生新的 variant/analysis，不覆盖旧记录。

### AC-02 可交易标签无前视

`t` 日因子只看到 `t` 及以前数据；`next_open_to_close` 的 `h=1` 使用 `Open[t+1]` 到 `Close[t+1]`。构造未来价格异常值不能改变 `factor(t)`。

### AC-03 多周期一次运行

请求 `[1,5,10,20]` 返回四个按周期升序的结果；尾部覆盖分别正确，因子截面计算不会按周期重复四次。

### AC-04 HAC 统计正确

使用手算序列验证长期方差、lag、HAC t 和 null 边界；多日重叠标签默认 lag 为 `h-1`。

### AC-05 缺失不冒充零

样本不足、零方差、非正长期方差和无完整分组均返回 null/insufficient，不显示成真实 0。

### AC-06 股票池偏差可见

`current_static` 报告明确显示生存者偏差并标为 `exploratory`；只有满足冻结政策的历史成员数据才可通过数据门禁。

### AC-07 PIT fail closed

缺 `AvailableAt`、数据版本未知或中性化数据使用当前快照时，严格政策返回 `insufficient`，不会自动降级后仍标记 passed。

### AC-08 试验失败也记录

同一家族的成功、失败、取消和无样本运行均出现在试验列表；冻结快照包含完整 trial ID 集合。

### AC-09 候选 revision 真冻结

验证绑定 revision 1 后修改候选产生 revision 2，原验证继续指向 revision 1；运行前后 hash 一致。

### AC-10 滚动窗口不重叠泄漏

窗口边界应用至少为最大 Horizon 的 purge；测试标签不使用训练边界另一侧的价格，窗口不足返回 validation error。

### AC-11 结论不可手工修改

候选 PUT 不接受 validation verdict；只有完成的验证报告能产生 passed/failed/insufficient。

### AC-12 回放与前瞻不混淆

冻结日前已经存在的历史只能生成 `retrospective`；`prospective` 报告仅累计冻结后日期，并在页面明确显示。

### AC-13 旧合同继续工作

旧 `/api/analyze` 请求、v3 报告、候选列表、加入简单策略和回测行为不变；旧报告不能被静默升级为 v4 证据。

### AC-14 重启和损坏恢复

服务重启后可读取验证请求和已完成报告；损坏正式文件、证据 hash 不匹配或引用丢失均返回安全错误并 fail closed。

### AC-15 用户能审计每个结论

验证详情逐项列出数据门禁、统计门禁、窗口结果、覆盖率、失败原因和试验数；仅看页面即可解释为何 passed、failed 或 insufficient。

## 17. 后续阶段

v1 稳定后再考虑：

1. 多因子标准化、相关聚类和综合评分；
2. 组合层 Purged/CPCV、PBO 和 Deflated Sharpe；
3. 行业风险模型、约束优化和容量估计；
4. 真实前瞻观察自动累计与漂移告警；
5. 数据集内容寻址、原始文件清单和端到端可复现快照；
6. 研究团队权限、审批和远程实验注册表。

这些能力不得提前塞进 v1 的局部字段中。v1 的完成标准是：每一个“通过”的结论都有冻结协议、可交易标签、完整试验披露、滚动验证和可追溯数据边界。
