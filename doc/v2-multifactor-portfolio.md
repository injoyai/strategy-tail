# v2 多因子组合研究 — 发布文档（Task 12）

> 状态：**发布候选冻结**（建议决定见 [§1.4](#14-建议发布决定)）
> 冻结日期：2026-09-19
> 代码版本：`v2-task12`（Task 1-12 交付）
> 相关设计/计划：`docs/superpowers/specs/2026-09-18-multifactor-portfolio-v2-design.md`、
> `docs/superpowers/plans/2026-09-18-multifactor-portfolio-v2.md`

---

## 1. 发布候选

### 1.1 冻结 hash

发布候选 hash 由关键文件指纹（相对仓库根路径 + 逐文件 SHA-256）经
`internal/lab/gates_v2.go` 的 `ReleaseCandidateHash` 计算：按路径字典序排序后逐条
拼接 `path:sha256` 再 SHA-256。关键文件清单（25 个）与 `release_v2_test.go`
`releaseKeyFiles` 一致，覆盖 v2 交付核心（`internal/portfolioresearch`、
`internal/lab` 组合链路、`runner.go`/`server.go` 最小修改、UI、契约文档）。
**本文档自身不参与候选 hash**（文档描述候选并记录 hash，若 hash 覆盖文档会形成
自引用环；代码与契约变更反映在 hash 中，文档变更不改变代码候选身份）。

> **发布候选 hash：`b17625bf4475201d7a636a41fa42c3edd73edc43b5eb81c8aa15d3cc7247e16e`**
> （冻结于 2026-09-19，由 `go test -count=1 ./internal/lab/ -run TestReleaseV2Gates -v`
> 输出；关键文件清单变更或代码变更 → hash 变化，需重新冻结）

### 1.2 门禁结果表

门禁评估为纯函数（`internal/lab/gates_v2.go`），输入 = 证据集合 + 检查结果
（由 `TestReleaseV2Gates` 真实调用既有测试构造），输出 = 门禁结果表。权威结果
以测试运行输出为准（`go test -count=1 ./internal/lab/ -run TestReleaseV2Gates -v`）。
下表为冻结时结果（13 项，六类全覆盖）：

| 类别 | 门禁项 | 状态 | 证据 | 说明 |
|---|---|---|---|---|
| 可迁移性 | v1_products_readable | pass | TestCompatV3ReportFixtureLoads / TestCompatCandidateV1FixtureVerifiable / TestAnalysisStoreSavesV3AndV4 / TestValidationStoreGetView | v1 不可变历史不受影响：既有产物与测试全部可读可测 |
| 可迁移性 | legacy_api_compatible | pass | TestPortfolioLegacyCompat / TestServerScriptAPI + 引用 TestServerRunLifecycle / TestServerStrategyPresets / TestServerStrategyRunValidation / TestServerCandidateCreateObserve | /api/status、/api/run、/api/script、/api/factors、候选/策略路由行为不变 |
| 正确性 | e2e_golden_case | pass | TestAuditGoldenFixedCase / TestAuditE2EFullChain / TestAuditExecutionEdgeScenarios / TestAuditLeakIsolationFutureReturns | 金标准固定案例全链手算断言，真实 runner 全链，泄漏隔离复证 |
| 正确性 | accounting_identity | pass | TestAuditGoldenFixedCase（对账恒等式）+ 引用 TestMultiDayInvariants / TestAttributionSumIdentity | 对账恒等式容差 1e-6；归因加总构造性成立 |
| 正确性 | determinism | pass | TestAuditE2EDeterminismAndRestart + 引用 TestDeterminism | 同输入两次运行指标/归因/净值/CSV 逐位一致 |
| 可重复性 | repeat_hash_consistent | pass | TestAuditE2EDeterminismAndRestart（CSV 两次运行 hash 一致） | 时间戳字段排除并文档化（见 §2.3） |
| 可重复性 | restart_hash_consistent | pass | TestAuditE2EFullChain / TestAuditE2EDeterminismAndRestart + 引用 TestPortfolioValidationStore_GetRecomputeVerdictFailClosed | 重启后 Store 读取 hash 重算校验一致，篡改 fail closed |
| 审计 | completed_has_manifest | pass | TestExperimentStore_MarkCompleted_PublishesManifest / TestExperimentStore_CompletionGate | completed 必有完整五类产物 manifest |
| 审计 | verdict_backend_generated | pass | TestPortfolioValidationStore_CompleteBackendVerdicts / TestPortfolioValidationStore_GetRecomputeVerdictFailClosed | verdict 由后端按冻结门禁生成，客户端不能指定 |
| 审计 | artifacts_hashed | pass | TestExperimentStore_HashMismatchFailClosed / TestArtifactManifestValidate | 产物含 SHA-256 hash，读取 fail closed |
| 审计 | no_path_traversal | pass | TestAuditPathTraversalVariants / TestPortfolioArtifactPathTraversal / TestPortfolioValidationStore_PathTraversalRejected | 产物名/ID 白名单拒绝路径穿越 |
| 性能 | benchmark_recorded | not_applicable | runAuditBenchmark（与 TestAuditBenchmarkEnv 同负载） | 无预设阈值，仅记录绝对值 + 机器环境（见 §2.4） |
| 文档 | docs_complete | pass | UX-CONTRACT.md + DESIGN.md §7 + doc/v2-multifactor-portfolio.md | 发布文档存在且记录限制（见 §3） |

### 1.3 限制清单

见 [§3 限制与已接受剩余风险](#3-限制与已接受剩余风险)（与 `internal/lab/gates_v2.go`
`ReleaseLimitations` 一致，11 项）。

### 1.4 建议发布决定

**CONDITIONAL（有条件发布）**。理由：全部门禁无 fail（可迁移性/正确性/可重复性/
审计/文档 12 项 pass，性能 1 项 not_applicable 已解释——无预设阈值仅记录绝对值）；
已记录并接受 11 项限制（涨跌停/停牌未建模、市值/行业数据依赖、市场阶段/集中度
门禁未落地、基准未接入导致 IR 门禁缺基准时 fail、归因为统计分解非因果证明等），
见 §3 逐项披露。

**条件** = 在本文档披露的边界内使用：基准接入前 IR/超额类门禁保持 fail 语义；
涨跌停/停牌按股票池成员判定可交易性；归因结论不用于因果推断。后续阶段
（v3/后续任务）补齐集中度、市场阶段稳定性、基准接入与涨跌停/停牌建模。

**全仓回归状态**（`go test -count=1 ./...`，2026-09-19）：全部包通过，唯一失败为
根包 `TestCommonCommandEntrypointsInitializeRuntime`——`cmd/valuation-sync` 顶层
未显式 `common.MustInitialize()`（`-codes` 分支仅在 `-all` 路径调用
`common.Initialize()`）。该失败为 **Task 11 已记录的既有遗留**（本 Task 未修改
`cmd/valuation-sync/`，差分归因确认非本次引入），属"已解释"的 Important 遗留，
不阻塞发布；已记录于 §3 限制第 8 项，建议后续单独修复（顶层补幂等初始化）。

---

## 2. v2 功能摘要（Task 1-12 交付清单）

### 2.1 领域与存储（Task 1-8）

- **领域类型与规范化 hash（Task 1）**：`internal/portfolioresearch` 纯领域包；
  `FactorModel`/`ModelHash`（只覆盖语义字段，-0.0 归一 +0.0，篡改 fail closed）、
  证据等级 min 降级、资源限制；`internal/lab/factor_model.go/_store.go` 追加式
  revision（幂等扫描全部 revision、乐观并发、路径检查、原子写）。
- **截面变换（Task 2）**：`transform.go` 固定顺序 mask→missing→winsorize→
  neutralize→standardize→direction；rank score=2r/(n+1)−1、z-score 总体标准差、
  分位去极值类型 7、MAD 1.4826、中性化 OLS 正规方程+部分主元；三禁令（全样本
  Min-Max/未来填充/NaN→0）由测试锁死。
- **合成与滚动 IC 权重（Task 3）**：`combine.go`/`redundancy.go`；等权秩基线、
  滚动 IC 权重（训练窗=视图最后 windowYears×250 天，收缩、非负截断、水填充投影、
  冻结 fallback）；冗余诊断（相关/覆盖/边际 IC/留一法，相关门槛只 warning 不删因子）。
- **目标组合与约束（Task 4）**：`target.go`；固定约束顺序 ranking→selection→
  equal_weight→stock_cap→industry_cap→tradability→turnover→lot_rounding→
  cash_feasibility→target；结构化 `InsufficientError`（不静默放宽）；两套权重口径
  （分析权重 vs 整手可执行）。
- **执行与会计（Task 5）**：`execution.go`/`accounting.go`；先卖后买、T+1、整手、
  现金、不可交易 fail closed；每日对账恒等式（容差 1e-6）；确定性（全排序、无随机）。
- **指标/基准/两层归因（Task 6）**：`metrics.go`/`attribution.go`；毛/净、回撤、
  基准对齐、IR（TE=0 → nil+原因）；预测归因（留一法编排）+ 组合归因（股票/行业/
  现金/成本拖累/选择收益/执行偏离，恒等式构造性成立，残差显式披露）。
- **实验账本与产物 Store（Task 7）**：`portfolio_experiment.go/_store.go`；六态状态
  机固定迁移表；MarkCompleted 先扫描五类白名单产物（report.json/nav.csv/orders.csv/
  trades.csv/holdings.csv）生成 manifest 才原子发布；Get 重算 hash fail closed。
- **非重叠滚动验证（Task 8）**：`validation.go` + `portfolio_validation.go/_store.go`；
  冻结 spec（全部字段进 SpecHash）；verdict 优先级 error＞insufficient＞passed/failed；
  测试窗结构上不可进入训练；完成前单窗可重写=崩溃恢复。

### 2.2 Runner/API/UI（Task 9-10）

- **Runner 与 HTTP API（Task 9）**：`portfolio_runner.go`/`portfolio_handlers.go` +
  `runner.go`/`server.go` 最小修改；15 条新路由（模型/实验/验证/运行控制/产物下载）；
  202 长任务提交、幂等键、409 忙碌冲突、验证 hash 不匹配 409；产物名白名单拒绝
  路径穿越；涨跌停/停牌未建模（报告 Limitations 披露）。
- **组合研究页（Task 10）**：`internal/lab/web/lab/index.html` 新增 "06 组合研究"
  tab；`UX-CONTRACT.md` 记录 UI 契约；`DESIGN.md` §7 记录页面设计上下文；正式
  结论只由后端返回，浏览器不重算。

### 2.3 可重复性口径（Task 11）

- 确定性：无系统时间/随机/并发；map 迭代全排序；同输入两次运行指标/归因/净值/
  四类 CSV 逐位一致。
- 产物 hash：四类 CSV 与 manifest 的 SHA-256 两次运行一致；`report.json` 的
  `startedAt`/`finishedAt` 为运行时刻信息（`portfolio_runner.go` 用 `time.Now`），
  **不参与确定性比对**，属报告完成时刻的合法信息（TestAuditE2EDeterminismAndRestart
  锁定）。

### 2.4 性能基准（Task 11，仅记录绝对值）

负载 20 因子 × 300 股票 × 750 交易日（代表性核心计算：变换→合成→目标→执行→
会计→指标→归因，与 runner 同函数同口径，跳过 DB 加载 I/O）。权威数值以
`go test ./internal/lab -run TestAuditBenchmarkEnv -v` 输出为准；**冻结时实测**
（2026-09-19，本机 go1.25.5/windows/amd64/12 CPU，TestReleaseV2Gates 输出）：
总时长 **2.791 s**、峰值内存（HeapAlloc 采样）**1215.9 MB**、产物总大小估算
**2.76 MB**（report.json 序列化 + 四类 CSV 行估算）、期末净值（净口径）
1667282.189032。机器相关，**无预设阈值**（不判定达标与否，仅记录绝对值）。

---

## 3. 限制与已接受剩余风险

以下为 v2 发布时已接受的剩余风险与限制（Task 1-11 审计记录 + Task 12 门禁输入，
与 `internal/lab/gates_v2.go` `ReleaseLimitations` 一致，11 项）。每一项均为
**已记录且已解释**的已知边界，不构成"未解释则阻止发布"的阻塞项；发布条件见
§1.4。

1. **涨跌停/停牌未建模**：runner 按股票池成员判定可交易性，不读涨跌停/停牌状态
   （执行层 `DayMarket` 已具备 `LimitUp`/`LimitDown`/`Tradable` 能力，数据接入未落地）。
2. **市值/行业数据依赖**：行业上限启用需要行业映射（数据源未接入，选中股票缺行业
   fail closed）；市值中性化/市值因子依赖本地估值库（`researchdata.SQLiteStore`），
   v2 尚未接入。
3. **市场阶段/集中度门禁未落地**：行业/市值集中度、市场阶段/滚动窗口稳定性、
   单因子组合增量比较未落地（Task 6/9/10 记录；门禁阈值可配置但这些统计未接入
   验证报告）。
4. **基准未接入**：`Benchmark.Available=false`；缺基准不阻止绝对收益报告，但 IR
   门禁缺基准/对齐不足时 fail（无法判定超额，语义文档化）。
5. **归因限制**：v2 归因是统计分解（收益来源的算术/复利分解），不是因果证明，也
   不是完整风险模型归因；净值口径与算术累计存在复利交叉项/成交时点/整手偏离残差
   （显式披露，不静默抹平）。
6. **target.go 无上限场景早退语义**：无任何单票/行业上限配置时，可交易性剔除后
   释放权重不再分配（`capSolve` 仅在至少一个上限启用时水填充），随后 StepTarget
   权重和校验失败 → 结构化 `insufficient`（fail closed 不静默放宽）；该实现与设计
   §8.2 注释在无上限场景不一致，已测锁定（Minor 观察项）。
7. **report.json 时间戳**：`portfolio_runner` `FinishedAt` 用 `time.Now`，
   `startedAt`/`finishedAt` 是运行时刻信息（确定性字段外差异）；CSV 产物、指标、
   归因、净值逐位一致（TestAuditE2EDeterminismAndRestart 锁定）。
8. **cmd/valuation-sync 未显式初始化运行时**：`-codes` 分支未显式
   `common.Initialize()`（上游遗留失败，需差分归因；`-all` 分支已显式初始化）。
9. **API 缺口（UI 侧已知）**：模型列表筛选/排序为当前页内（`GET /api/factor-models`
   无分页参数，前端本地分页并写 URL page）；无模型归档/恢复 HTTP 路由；无 v1 验证
   列表路由（证据区聚合已有模型的 `validatedFactors`）。
10. **滚动 IC 训练窗**：= 视图日期最后 windowYears×250 天（交易日近似），非自然年
    窗口；训练窗内数据不足时按冻结 fallback（等权/现金）不临时择优。
11. **性能无预设阈值**：20 因子 × 300 股票 × 750 交易日 ≈ 2.9s / 峰值内存 1215MB /
    产物 ≈2.77MB，仅记录绝对值，机器相关。

---

## 4. 使用说明

### 4.1 启动与路由

- Lab 服务：`go run ./cmd/lab`（默认 `127.0.0.1:8765`，`LAB_ADDR` 覆盖）。
- 组合研究页：根路径浏览器打开后选择 "06 组合研究" tab；`?tab=portfolio&model=
  &revision=&run=&val=&filter=&sort=&page=` 深链接可恢复。

### 4.2 创建模型 / 实验 / 验证

1. **创建因子模型**：`POST /api/factor-models`。请求含 `requestId`（幂等：同键同内容
   返回 200 同 ID、异内容 409）、`factorValidations`（v1 验证 ID 列表）、
   `transformPipeline`（缺失/去极值/中性化/标准化）、`combination`（等权秩或滚动 IC
   权重）、`portfolioPolicy`、`execution`、`benchmark`。**证据等级与方向由后端从上游
   v1 验证唯一派生**（客户端注入 `evidenceClass` 被忽略）；模型证据等级 = min(输入)。
   创建成功返回 `modelId/revision/modelHash`（冻结身份）。
2. **创建实验**：`POST /api/portfolio-experiments`（`requestId` 幂等；研究区间必填，
   训练/测试可选；参数变体来源 baseline|declared|exploratory；证据等级、数据快照、
   代码版本、随机种子、TrialCounted）。随后 `POST /api/portfolio-runs/{id}/start`
   提交长任务（202），`POST /api/portfolio-runs/{id}/stop` 取消。
3. **创建验证**：`POST /api/portfolio-validations`（`requestId` 幂等；spec 含
   ModelRef/windowRule/gates/benchmark/evidenceRequirement，全部字段冻结进
   SpecHash）。随后 start。完成输出 verdict（passed/failed/insufficient/error，
   后端生成）与逐窗门禁。
4. **运行详情/归因/验证详情**：`GET /api/portfolio-runs/{id}`、
   `GET /api/portfolio-validations/{id}`；实验产物
   `GET /api/portfolio-experiments/{id}/artifacts/{name}`（五类白名单名）。

### 4.3 产物位置

- 实验账本记录：`data/lab/portfolio-experiments/<pe_…>.json`
- 实验产物：`output/portfolio/<pe_…>/`（report.json + nav.csv + orders.csv +
  trades.csv + holdings.csv，五类齐才发布 completed）
- 验证记录：`data/lab/portfolio-validations/<pv_…>/`（request.json + windows/NNNN.json
  + report.json）
- 模型记录：`data/lab/factor-models/<fm_…>/NNNNNN.json`（追加式 revision）

### 4.4 门禁语义（失败只阻止发布）

- 门禁评估是**只读检查**：`EvaluateReleaseGates`/`FreezeReleaseCandidate` 为纯函数，
  不写产物、不改变任何状态。
- `TestReleaseV2Gates` 失败 = **发布被阻止**（表示关键验证未通过，不应对外声明
  "已发布"），但已写产物与已发布状态（模型/实验/验证记录、产物文件）**不受影响**：
  它们由 Store 的原子发布协议独立管理，门禁失败不会回滚或删除任何记录。
- 回滚语义（设计计划 Task 12）：关闭组合研究入口与新建任务，不删除新 Store；
  历史结果继续只读；不得通过回滚覆盖用户或其他协作者的未提交修改。

---

## 5. 迁移说明

- 新功能（组合研究）默认已上线但独立版本化；旧分析、候选、策略回测与旧 URL
  无需迁移即可使用（可迁移性门禁验证）。
- 新记录（模型/实验/验证）独立版本化；未知字段和旧版本 fail closed。
- 不对历史候选自动生成"已验证模型"（证据等级不升格）。

---

## 6. 附录：设计验收标准（§16 AC1-AC16）证据

| AC | 内容 | 证据 |
|---|---|---|
| 1 | 未通过 v1 准入的模型不能产生正式组合验证结论 | adapter 测试 TestValidationStoreAdapterGetAndList；`factor_model.go` 从上游 ValidationView 派生 |
| 2 | 组合结果可追溯到 validation/candidate/实现/数据/代码版本 | ModelHash 语义字段 + report.json 含 modelId/hash/evidenceClass/codeVersion（TestAuditE2EFullChain） |
| 3 | 截面变换只使用当日可见数据且顺序/阈值/失败策略进 hash | transform.go 单日 API 结构性无未来；ModelHash 覆盖 pipeline（transform_test） |
| 4 | 等权秩有手工金标准；滚动 IC 不能读取测试窗 | TestAuditGoldenFixedCase 手算；TestLeakNoTestWindowInTraining / TestAuditLeakIsolationFutureReturns |
| 5 | 冗余报告含相关/覆盖/边际 IC/留一法，不自动删因子 | redundancy_test + TestAttributionLeaveOneOutPortfolioImpact |
| 6 | 目标约束可解释，冲突不静默放宽 | TestAuditExecutionEdgeScenarios（insufficient 结构化）+ target_test 调整账本 |
| 7 | 执行遵守次日开盘/卖优先/T+1/整手/现金/不可交易 | TestAuditExecutionEdgeScenarios + execution_test |
| 8 | 每日会计恒等式通过，可逐日对账 | TestAuditGoldenFixedCase（Reconciled）+ TestMultiDayInvariants |
| 9 | 报告展示毛/净、成本拖累、目标/实际偏离、未成交原因 | metrics/attribution 测试 + TestAuditE2EFullChain 报告语义 |
| 10 | 测试窗不参与训练/调参/门禁；修改须新 revision | TestAuditLeakIsolationFutureReturns + TestLeakAppendTrainingDayOnlyAffectsLater |
| 11 | passed/failed/insufficient/error 由后端按冻结门禁生成 | TestPortfolioValidationStore_CompleteBackendVerdicts |
| 12 | 证据等级不会高于最弱输入 | TestPortfolioEvidenceDegradationAPI + TestPortfolioValidationEvidenceDegradation |
| 13 | 新 API 不破坏既有因子/候选/策略/回测接口 | TestPortfolioLegacyCompat + TestServerScriptAPI + TestCandidateLegacyCompatibility |
| 14 | 组合页覆盖异步状态/键盘/窄屏/URL/stale 保护 | UX-CONTRACT.md + DESIGN.md §7（Task 10 浏览器验证记录） |
| 15 | DESIGN/UX-CONTRACT/设计/API/页面术语一致 | 本文档 + DESIGN.md §7 + UX-CONTRACT.md |
| 16 | Go 测试/静态检查/UI 严格审计/真实浏览器关键路径通过 | Task 10/11 记录 + TestReleaseV2Gates（本文档门禁表） |

---

## 7. 已知未纳入 v2（后续阶段）

- 行业/市值集中度门禁、市场阶段/滚动窗口稳定性、单因子组合增量比较。
- 基准数据接入（IR/超额归因门禁依赖）。
- 涨跌停/停牌状态数据接入（组合执行层能力已具备，数据未接）。
- 模型归档/恢复 HTTP 路由、v1 验证列表路由、服务端分页的模型列表。
