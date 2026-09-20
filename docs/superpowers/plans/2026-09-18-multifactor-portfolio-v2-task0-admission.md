# v2 Task 0 产出：v1 准入逐项表与兼容夹具

> 对应计划 [2026-09-18-multifactor-portfolio-v2.md](2026-09-18-multifactor-portfolio-v2.md) §Task 0。
> 依据以"当前实现与测试事实"为准（计划 §9 实施完成定义），非设计假定。

## 1. 六项准入逐项核对

| # | 准入条件（设计 §2） | 结论 | 源码证据 | 验证方式 |
|---|---|---|---|---|
| 1 | FactorValidation 稳定引用候选 revision、因子实现版本、冻结协议 | ✅ 满足 | `ValidationProtocol`（CandidateID/CandidateRevision/EvidenceClass，[factor_validation.go](../../../internal/lab/factor_validation.go#L186-L195)）；冻结候选快照含 `FactorRef`（Kind/Days/Name/Unit/ImplementationVersion，L341-L364）；freezeValidation 八项校验链（L533-L688） | `TestCompatV2ValidationPassedFixture` 断言 revision=1、五字段因子快照、protocol hash 非空 |
| 2 | 默认标签支持 t 收盘信号 / t+1 开盘成交 | ✅ 满足 | `labelKindNextOpenToClose`（[research_protocol.go](../../../internal/lab/research_protocol.go#L50)）；`validResearchProtocol()` 默认即该标签 | `TestCompatV2ValidationPassedFixture` 断言冻结协议 Labels.Kind == next_open_to_close |
| 3 | 历史股票池 / PIT / 复权 / 可交易性 / 数据版本生成**机器可读**质量结论 | ❌ **缺口** | [coverage.go](../../../internal/researchrun/coverage.go#L7-L16) `LogCoverage` 仅 `logs.Infof/Warnf` 文本输出，无结构化机器可读质量结论 | 静态确认：无 JSON/结构体输出路径 |
| 4 | 多周期 IC / HAC / 换手 / 试验账本不可变结果 | ✅ 满足 | `extendedICStats`/`hacMeanTStat`/`buildDecayCurve`（factor_stats_extended.go）；`groupTurnoverSeries`（factor_turnover.go）；TrialStore 追加式两文件账本、Complete 一次写入拒绝覆盖 | 既有单测覆盖；v2 组合层在 Task 7/8 消费时再验证链路 |
| 5 | 验证结论至少区分 passed / failed / insufficient / error | ✅ 满足 | `finishedValidationStates` 四态终态白名单（[factor_validation.go](../../../internal/lab/factor_validation.go#L52)）；`FactorValidationReport.validate` Verdict-State 一致性 + checks 逐项披露（L490-L518） | `TestCompatV2ValidationInsufficientFixture` 走通 insufficient 终态与窗口计数披露 |
| 6 | prospective / retrospective / exploratory 证据等级不可被下游提升 | ✅ 满足 | `deriveEvidenceClass` 只降不升（research_protocol.go L344+）；`validateFreezeEvidenceClass` 拒绝 exploratory 证据冻结 retrospective（factor_validation.go L705-L735） | `TestCompatV2ValidationExploratoryInputFixture` 验证拒绝路径：显式错误、零记录 |

### 缺口 3 的处置（未满足条目）

- **结论**：v1 `internal/researchrun` 只输出日志级覆盖结论，不满足"机器可读质量结论"。
- **v2 不绕过**：组合层（portfolioresearch / 后续 Runner 集成）不得假定数据质量结论可机读。
- **前置任务**：新增"机器可读数据质量结论"能力——在 researchrun 或 Task 1 领域层输出结构化质量报告（股票池静态性、PIT 验证状态、复权方式、可交易性、价格数据版本），带 schema 版本与 fail-closed 校验，风格对齐 `factor_validation.go` 的哈希冻结模式。
- **负责人**：v2 实施者（计划 Task 1/9 范围）；**依赖**：researchrun 数据加载合同（计划 §Task 0 读取项）。
- **验收**：质量结论可写入/读取为结构化 JSON，旧版本或缺字段返回明确 `unsupported/insufficient`，不 panic、不补造事实。

## 2. 兼容夹具元数据（Task 0 动作 1/2）

夹具测试文件：`internal/lab/factor_validation_v2_compat_test.go`（真实链路：v4 分析 → 候选 → trial → freezeValidation → Store.Create/SaveWindow/Complete）。`RequestHash` 含冻结时间（内容派生 SHA-256），逐次运行值不同；测试断言非空且读取时重算 fail-closed 一致——比记录字面值更强的固定。

| 元数据项 | passed 夹具 | insufficient 夹具 | exploratory 输入夹具 |
|---|---|---|---|
| 场景 | 冻结 → 窗口 ok → passed 终态 | 双窗口数据不足 → insufficient 终态 | current_static 股票池 → retrospective 冻结拒绝 |
| candidate revision | 1 | 1 | 1 |
| factor instance | momentum / N日动量(2) / days=2 / ratio | 同左 | 同左 |
| implementation version | 1 | 1 | 1 |
| direction | positive（hypothesis.expectedDirection） | positive | positive |
| evidence class（验证层） | retrospective | retrospective | —（分析层 exploratory，拒绝冻结） |
| data snapshot | local-klines / 2026-01 / PIT=verified / 复权=forward | 同左 | 同左 |
| research protocol hash | `2e2d3349d34dee11d93bfef5a3e786c4c993a56906dc7baa4efa199143afe0b9`（validResearchProtocol 确定性值） | 同左 | `7c29edb478d12563972f898c4313c3dca08f6d7e93caac6775e73dec9ae58fae`（current_static 变体） |
| request hash | 冻结时生成，断言非空 + 读取重算一致 | 同左 | 无（拒绝路径，未冻结） |
| 断言要点 | 终态不可覆盖、完成后再写窗口被拒 | 逐窗口披露原因、窗口计数披露、不补造统计 | 显式错误信息含 retrospective、不 panic、零验证记录 |

## 3. 适配层接口草案（Task 0 动作 3）

- 文件：`internal/lab/portfolio_adapter_draft.go` + `portfolio_adapter_draft_test.go`。
- 契约：`ValidationInput` 接口（`Get(id) (ValidationView, error)` / `List(filter) ([]ValidationSummary, error)`）；`ValidationStoreAdapter` 实现之（编译期断言 `var _ ValidationInput`）。
- 规则：`internal/portfolioresearch`（Task 1 创建）只允许消费 `ValidationView` / `ValidationSummary` / `ValidationWindowReport` 等类型化视图，**禁止**直接解析 v1 JSON 文件。
- 迁移：Task 1 按相同契约把适配层迁入 portfolioresearch 包，行为不变。

## 4. 正式契约 vs 当前实现细节（Task 0 动作 4）

| 类别 | 字段 / 结构 |
|---|---|
| **正式契约（冻结后不可变）** | `ValidationRequest`（SchemaVersion/ID/FrozenAt/Protocol/Candidate/ResearchProtocol/ResearchProtocolHash/Trials/RequestHash/CreateRequestID/CreateRequestHash/Supersedes）；`FactorValidationReport`（State/Verdict/Checks/WindowCount/ValidWindowCount/InsufficientWindowCount/Aggregate/Message）；`ValidationWindowReport`（State/Message/Observations/Horizons）；`ValidationSummary`；`FactorCandidate`（含 Evidence.ReportSHA256 绑定）；终态白名单四态；证据等级只降不升 |
| **实现细节（调用方不得依赖）** | 存储目录布局 `<root>/<validationId>/`；文件名 `request.json` / `report.json` / `windows/NNNN.json`；原子写 tmp+rename 机制；`ValidationView` 的派生字段布局；ID 时间戳格式细节（`fv_YYYYMMDDTHHMMSSmmmZ_xxxxxxxx`） |

## 5. 现有未提交文件与共享热点（Task 0 动作 5）

- `AGENTS.md` **未修改**（Task 0 动作 5 明令）。
- 已修改未提交（v1 Task 5-7 产物）：`internal/lab/analysis.go`、`analysis_run_test.go`、`analysis_store.go`、`factor_candidate_store.go`、`factor_candidate_store_test.go`、`matrix_test.go`、`runner.go`。
- 新增未提交（v1 Task 5-7 + 本 Task 0）：`internal/lab/factor_analysis_v4.go`、`factor_trial.go`、`factor_trial_store.go`、`factor_trial_store_test.go`、`factor_trial_test.go`、`factor_validation.go`、`factor_validation_store.go`、`factor_validation_store_test.go`、`factor_validation_test.go`、`factor_validation_v2_compat_test.go`、`portfolio_adapter_draft.go`、`portfolio_adapter_draft_test.go`；`docs/superpowers/{plans,specs}/...`（v1/v2/v3 计划与设计文档）。
- **共享热点**（v2 实施期间不独占修改、谨慎合并）：`internal/lab/web/lab/index.html`（UX 页面）；`server.go`、`runner.go`（Task 9/10 顺序集成，避免并行冲突）；`internal/researchrun/coverage.go`（准入缺口 3 的前置任务落点）。

## 6. 完成门槛核对

- ✅ 逐项准入表已形成（§1）。
- ✅ 未满足条目（条件 3）已记录负责人/前置任务，v2 不绕过（§1 缺口 3 处置）。
- ✅ 兼容夹具可读取；旧版本/缺字段返回明确 `unsupported/insufficient`（exploratory 拒绝路径 + adapter 缺失记录 `errValidationNotFound`），不 panic、不补造事实。
