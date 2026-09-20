# 前瞻纸面交易与模型治理 v3 设计

> 日期：2026-09-18  
> 状态：设计完成，待 v1/v2 准入条件满足后实施  
> 上游：`docs/superpowers/specs/2026-09-18-multifactor-portfolio-v2-design.md`  
> 范围：冻结组合发布、真实时间纸面运行、前瞻证据、运行监控、恢复与晋级治理  
> 本文只定义产品和技术契约，不代表相关能力已经实现，也不授权连接券商或发送真实订单

## 1. 阶段定位

v1 回答“单因子证据是否可信”，v2 回答“多个因子组成的组合在可交易历史模拟中是否仍然成立”。再下一步不是继续扩大历史参数搜索，而是让一个冻结模型接受真实时间检验：

```text
FactorValidation v1
  → PortfolioValidation v2
  → ModelRelease
  → PaperDeployment
  → 每日数据门禁
  → 冻结决策与纸面订单
  → 基于已观察行情的成交
  → 账户对账、漂移和告警
  → ProspectiveEvidence
  → 晋级 / 延长观察 / 拒绝 / 退役
```

第三阶段的价值不是制造另一条更漂亮的净值曲线，而是暴露历史研究很难证明的问题：

1. 数据能否在承诺的时间到达，缺失和修订如何影响决策；
2. 同一个冻结模型能否日复一日确定性运行；
3. 目标组合与实际可成交组合的偏离是否可接受；
4. 换手、成本、容量告警和暴露是否与历史预期一致；
5. 进程重启、重复触发、迟到数据和部分失败后能否安全恢复；
6. 研究者查看阶段性结果后，系统能否阻止静默改模型继续累计“前瞻”样本。

## 2. 实施准入条件

v3 的业务实现必须同时满足：

### 2.1 v1 准入

- 输入因子有不可变 candidate revision、implementation version 和验证记录；
- 研究协议、数据质量、试验次数和证据等级可机读；
- `passed | failed | insufficient | error` 语义稳定。

### 2.2 v2 准入

- `FactorModel`、`PortfolioExperiment`、`PortfolioValidation` 均有不可变 ID、revision 和 hash；
- 至少有一个模型通过冻结的组合样本外门禁；
- 目标组合、实际成交、订单、持仓、现金和净值可以逐日对账；
- 次日开盘、T+1、停牌/涨跌停、整手和费用语义已有金标准测试；
- 毛/净收益、执行偏离和归因可以从基础产物复算；
- 组合证据等级不会高于最弱输入。

### 2.3 运行数据准入

要获得 `live_paper` 证据，数据源还必须提供或由接入层可靠记录：

- `eventAt`：市场事件发生时间；
- `availableAt`：信息按业务规则可用时间；
- `receivedAt`：本机实际收到时间；
- 来源、版本、修订和完整性；
- 交易日历、股票状态和公司行为口径。

只有事件日期、没有接收时间的历史文件不能证明按时到达，只能产生 `delayed_paper` 或 `catch_up_replay` 证据。

## 3. 目标与非目标

### 3.1 v3 目标

1. 从通过 v2 的固定 model revision 创建不可变 `ModelRelease`，绑定代码、数据、日历、成本、风险和运行配置。
2. 创建有明确起止条件的 `PaperDeployment`，模型变更必须终止旧部署并创建新部署。
3. 按交易日和阶段运行确定性日循环，任何重复触发都不能重复生成决策、订单、成交或账本。
4. 决策前检查数据新鲜度、完整性、PIT 和交易日状态；质量不足默认不交易。
5. 决策、目标、订单意图和纸面成交均先持久化事实，再推进状态。
6. 纸面成交只使用当时已经观察到的行情事件，不读取后续最高价、最低价或收盘价决定早先成交。
7. 建立独立纸面账户、订单生命周期、不可变账本、每日结算和对账。
8. 实施组合级运行前风险门禁和可持久暂停开关，验证将来实盘需要的控制面。
9. 监控数据、信号、组合、执行、风险、性能和运行健康的漂移。
10. 累积不可回填冒充的 `ProspectiveEvidence`，按预先冻结门禁生成治理结论。
11. 在 Lab 增加“07 前瞻观察”，展示交易日时间线、证据年龄、异常、恢复和部署状态。
12. 保持完全本地、单节点、无真实资金和无外部订单副作用。

### 3.2 v3 明确不做

- 不接券商 API，不登录真实账户，不发送、撤销或改单真实委托；
- 不处理真实资金、融资融券、杠杆、期权、期货或多账户；
- 不宣称纸面成交等同真实成交，也不把纸面利润称为可实现利润；
- 不自动根据前瞻表现修改因子、权重、阈值、风险限额或门禁；
- 不在运行中自动切换“表现更好”的模型；
- 不实现完整 OMS、交易所状态机、经纪商回报对账或灾备集群；
- 不把迟到后补跑、手动补数据或历史回放标为真正前瞻；
- 不以单一 Sharpe、收益率或健康分数决定晋级；
- 不在 v3 顺手加入协方差优化、商业风险模型或复杂冲击成本模型；
- 不修改受保护 `core` 结构体来承载部署、订单或账户状态。

## 4. 证据等级与不可冒充规则

### 4.1 运行证据等级

| 等级 | 条件 | 允许用途 |
|---|---|---|
| `catch_up_replay` | 进程错过决策/成交时点，事后用历史数据补跑 | 恢复诊断，不计入真正前瞻门禁 |
| `delayed_paper` | 模型在日期前冻结，但数据到达时间不可证明或运行在收盘后批处理 | 运行稳定性和方向观察 |
| `live_paper` | 部署在事件前已激活，数据按时到达，决策和成交按真实时间顺序持久化 | 纸面前瞻证据 |
| `prospective_observed` | `live_paper` 完整完成观察期且无模型变更，满足证据完整性 | 可进入人工晋级评审 |

运行等级和统计结论相互独立。`live_paper + failed` 是有效的失败证据；`catch_up_replay + passed` 也不能晋级。

### 4.2 最弱证据原则

最终证据等级取以下最弱者：

- 输入因子与组合验证；
- 数据来源与实际接收时间；
- 决策、成交和估值阶段是否按时执行；
- 是否发生模型或协议变更；
- 是否发生无法解释的恢复、人工修订或账本差异。

### 4.3 不可回填冒充

- 错过决策时点后可以生成诊断性补跑，但 `decisionMode=catch_up`；
- 补跑结果使用独立 ID，不能改写原本的 `missed` 周期；
- 后补 `receivedAt` 无效；接收时间必须由接入层在首次落地时记录；
- 人工导入的数据必须记录操作者、导入时间、来源和理由；
- 任何模型配置变更都终止当前证据序列。

## 5. 核心对象

### 5.1 对象关系

```text
FactorModel revision
  └─ PortfolioValidation
       └─ ModelRelease
            ├─ ReleaseManifest
            ├─ PromotionPolicy
            └─ PaperDeployment
                 ├─ TradingDayRun[]
                 │    ├─ DataReadinessSnapshot
                 │    ├─ DecisionSnapshot
                 │    ├─ PaperOrder[] / PaperFill[]
                 │    ├─ DailyLedger
                 │    └─ ReconciliationResult
                 ├─ MonitoringEvent[]
                 └─ ProspectiveEvidence
```

### 5.2 ModelRelease

`ModelRelease` 是“允许进入前瞻纸面观察”的不可变发布单，至少绑定：

- model ID、revision、model hash；
- portfolio validation ID、结论和证据等级；
- 代码版本、因子实现版本、数据合同版本；
- 股票池、基准、交易日历和时区；
- 信号/决策/最早成交/估值时点；
- 成本、滑点、成交、公司行为和容量告警口径；
- 风险限额、暂停条件、恢复策略；
- 前瞻观察期、最小有效日、门禁和人工审批记录；
- release hash。

Release 不是可编辑配置。任何变化产生新 release。

### 5.3 PaperDeployment

一个 deployment 表示某个 release 在某个纸面账户上的连续观察区间：

```text
draft → ready → active → paused → active
                    ├─> completed
                    ├─> rejected
                    ├─> superseded
                    └─> error_hold
```

`paused` 只停止新决策和新订单；已有纸面持仓如何估值、是否允许只减仓必须由冻结策略定义。状态转换由后端校验并写审计事件。

### 5.4 TradingDayRun

以 `deploymentId + tradingDate` 为幂等键。每个交易日按阶段推进：

```text
scheduled
  → data_ready | data_blocked
  → decision_frozen | decision_skipped
  → orders_planned
  → execution_observing
  → marked
  → reconciled
  → sealed
```

阶段只能单向推进；修正通过追加事件和新版本完成，不原地改历史事实。

## 6. 本地运行架构

### 6.1 模块化单体

v3 延续本地模块化单体，不引入微服务。建议新增：

```text
internal/papertrade/
  clock / calendar / supervisor
  data_gate / decision / venue
  orders / account / ledger
  risk / reconciliation / monitoring
  evidence / stores
```

`cmd/lab` 在交互模式中承载 `PaperSupervisor`；增加不自动打开浏览器的 headless 启动方式，供用户保持本地进程运行。两种模式使用同一套领域代码和 Store。

### 6.2 单写者

同一纸面状态目录同一时间只允许一个 Supervisor 写入：

- 启动时获取带进程信息和租约时间的本地锁；
- 无法获取时进入只读并明确显示当前 owner；
- 不凭过期 PID 直接抢锁，需验证租约和安全接管条件；
- UI 多标签页不能各自启动一个日循环。

### 6.3 时钟与时间语义

所有领域逻辑依赖注入的 `Clock`，生产纸面运行使用系统时钟，测试使用可控时钟。统一记录：

- 交易所业务时区；
- UTC 持久时间；
- 本机墙上时间和单调计时仅用于运行测量；
- `scheduledAt`、`startedAt`、`decidedAt`、`observedAt`、`sealedAt`。

发现系统时间大幅跳变时暂停新决策，不能继续生成看似按时的证据。

## 7. 每日运行协议

### 7.1 阶段顺序

```text
交易日前检查
  → 数据新鲜度与完整性门禁
  → t 日收盘后冻结信号和目标组合
  → t+1 进入成交观察窗
  → 纸面订单状态推进
  → 收盘估值与公司行为
  → 账户/订单/持仓/现金对账
  → 监控与证据封存
```

阶段优先级固定。同一时间到达的数据、暂停请求、订单事件和估值事件按稳定规则排序，并写入 manifest。

### 7.2 数据门禁

决策前生成不可变 `DataReadinessSnapshot`：

- 应到与实到数据集；
- 最大 eventAt、availableAt、receivedAt；
- 代码覆盖、股票池版本、停复牌和公司行为覆盖；
- 重复、缺口、负价、零量、异常修订；
- 与 release 要求的版本是否一致；
- `ready | degraded | blocked` 及每项理由。

默认只有 `ready` 允许生成新订单。`degraded` 是否只减仓或完全跳过必须在 release 中选择，不能运行时临时决定。

### 7.3 决策冻结

`DecisionSnapshot` 保存完整输入 hash、每只股票因子变换、合成分数、目标权重、约束调整、当前纸面持仓和订单计划。写入成功后才允许进入订单阶段。

同一部署同一交易日只能有一个正式决策。重新计算只能用于一致性校验；结果不同必须触发 determinism 告警和 `error_hold`。

### 7.4 错过运行

进程重启后 Supervisor 扫描交易日 watermark：

- 尚未到决策时点：按正常流程继续；
- 已错过决策但未到成交窗：可以生成 `delayed_paper`，前提是数据接收时间仍可证明；
- 已错过成交观察：正式周期记为 `missed`，可另建 `catch_up_replay`；
- 不自动把所有空档补成 live_paper。

## 8. 纸面订单与成交

### 8.1 订单生命周期

v3 定义自己的纸面订单事实，不复用 `core.Trade`：

```text
planned → accepted → working
                  ├─> partially_filled → filled
                  ├─> cancelled
                  ├─> rejected
                  └─> expired
```

每个状态由不可变事件驱动。重复事件通过稳定 event ID 去重；非法状态转换进入隔离和对账，不静默忽略。

### 8.2 PaperVenue

PaperVenue 只消费按 receivedAt 顺序到达的行情：

- 市价意图按 release 定义的下一个可成交观测处理；
- 不允许用整根日 K 的 High/Low 推断盘中成交顺序；
- 只有日线数据时，使用明确的 delayed close/open 纸面口径并降低证据等级；
- 成交量参与率不足时部分成交或拒绝，规则冻结；
- 滑点和费用沿用 release 中的固定模型；
- 纸面成交不向任何外部地址发送。

### 8.3 未知结果

本地纸面成交通常可确定，但进程在事件写入中途崩溃时仍可能出现“状态未知”。恢复时先读取 intent、event log 和 ledger 对账；在证明前，不重复生成成交。

## 9. 纸面账户、账本与对账

### 9.1 单一账户写者

每个 paper account 只有 Supervisor 内部 Account Coordinator 可以改变：

- cash；
- reserved cash / shares；
- lots 和 sellable shares；
- fees；
- realized/unrealized P&L；
- NAV 和权益。

策略、UI 和监控均只能发意图或读取快照，不能直接写余额。

### 9.2 不可变账本

每个资金、持仓和费用变化写双向可解释 ledger entry，并带来源事件。修正使用冲销和新分录，不改旧分录。

### 9.3 每日对账

至少验证：

```text
期初现金 + 现金流入 - 现金流出 = 期末现金
期初持仓 + 买入 - 卖出 + 公司行为 = 期末持仓
现金 + 持仓市值 = 账户权益
期初权益 + 市场损益 + 已实现损益 - 费用 = 期末权益
订单累计成交量 <= 订单数量
账户账本持仓 = 组合持仓快照
```

任一不平立即进入 `error_hold`，停止新风险，只允许诊断和按协议减仓模拟。

## 10. 运行风险与暂停控制

### 10.1 运行前风险门禁

- 单日新增名义金额；
- 单票和行业权重；
- 总持仓数和现金下限；
- 单日/滚动换手；
- 订单价格和数量合理性；
- 与上一正式目标的异常跳变；
- 数据质量和模型/代码 hash；
- 累计回撤、成本和执行偏离告警。

门槛来自 release。超限默认拒绝该意图或暂停部署，不自动放宽。

### 10.2 持久暂停开关

暂停状态必须落盘并在重启后保持。操作记录操作者、原因和时间。恢复需要重新通过数据、对账和 release 一致性检查。

v3 的暂停只影响纸面系统，但其行为和审计为未来真实交易控制做演练。

## 11. 漂移与运行监控

### 11.1 监控维度

| 维度 | 示例 |
|---|---|
| 数据 | 延迟、缺失、修订、覆盖、股票池变化 |
| 因子 | 缺失率、分布、极值、横截面秩稳定度 |
| 模型 | 分数分布、因子实际权重、相关结构 |
| 组合 | 持仓数、集中度、行业/市值暴露、现金 |
| 执行 | 未成交、部分成交、滑点、目标/实际偏离 |
| 风险 | 回撤、换手、成本、限额触发 |
| 性能 | IC、组合收益、基准差、历史预期区间 |
| 运行 | 心跳、阶段耗时、失败、重试、锁和磁盘 |

### 11.2 基线和阈值

漂移基线绑定 v2 验证报告或 deployment 早期冻结训练基线。阈值进入 release，不根据近期表现自动漂移。

告警状态至少区分 `info | warning | blocking | resolved`，同时记录首次、最近发生、次数、影响范围和恢复动作。告警不是自动改模型的指令。

### 11.3 阶段性查看

用户可以随时查看结果，但若据此修改模型、门禁或数据处理：

- 当前 deployment 标记 `superseded` 或 `completed`；
- 原有证据保留；
- 新配置生成新 release/deployment；
- 两段证据不可合并伪装为一段连续前瞻样本。

## 12. ProspectiveEvidence 与治理

### 12.1 证据包

一个证据包至少包含：

- release/deployment 身份和 hash；
- 计划观察期、实际有效日、missed/degraded 日；
- 每日 readiness、decision、orders、fills、ledger 和 reconciliation hash；
- 数据/因子/组合/执行/风险/性能漂移；
- 所有告警、暂停、恢复和人工操作；
- 毛/净绩效及与 v2 预期区间比较；
- 门禁逐项结果和限制；
- evidence class 和最终 verdict。

### 12.2 治理结论

```text
continue_observation
passed_paper
failed_paper
insufficient
operational_error
superseded
```

`passed_paper` 只表示通过冻结纸面门禁，并不授权实盘。进入真实资金阶段必须另做市场、券商、账户、权限、订单、对账、恢复和安全设计，并由用户显式批准。

### 12.3 建议门禁类型

门禁全部可配置：

- 最小有效 live_paper 交易日和再平衡次数；
- missed/degraded 比例；
- 决策确定性与账本对账零差异；
- 数据/因子/组合漂移边界；
- 目标/实际偏离、未成交和成本边界；
- 最大回撤和风险限额触发；
- 相对 v2 预期区间与基准的稳定性；
- 严重告警是否全部解决；
- 是否发生模型或协议变更。

统计门槛和运行门槛必须同时满足。

## 13. 持久化与恢复

### 13.1 建议目录

```text
data/lab/model-releases/<release-id>.json
data/lab/paper-deployments/<deployment-id>.json
data/lab/paper-accounts/<account-id>/
data/lab/paper-events/<deployment-id>/<trading-date>.jsonl
data/lab/paper-ledger/<account-id>.jsonl
data/lab/paper-checkpoints/<deployment-id>.json
data/lab/prospective-evidence/<evidence-id>.json
output/paper/<deployment-id>/...
```

事件日志先追加并 fsync，再更新 checkpoint。checkpoint 只是加速索引，不是事实来源；损坏后可由事件重建。

### 13.2 恢复原则

- 重启先验证 release、事件链、账本和 checkpoint hash；
- 以持久 watermark 恢复，不从 UI 状态猜测；
- 重放只重建内部状态，不重新产生外部副作用；
- 重复 schedule/market event/order event 幂等；
- 损坏或缺失无法自动证明时进入 `error_hold`；
- 备份/恢复后保留原 ID 和审计链，不克隆成新的前瞻事实。

## 14. API 与后台任务边界

v3 不把长期 deployment 塞进现有单批次 Runner。新增 `PaperSupervisor` 管理持续状态；Runner 仍处理有起止的分析/回测任务。

建议 API：

- `/api/model-releases`：创建、列表、详情；
- `/api/paper-deployments`：创建、状态转换、列表、详情；
- `/api/paper-days`：交易日运行、阶段和证据；
- `/api/paper-orders`、`/api/paper-fills`、`/api/paper-ledger`：只读审计；
- `/api/paper-alerts`：列表、确认、解决说明；
- `/api/prospective-evidence`：生成、列表、详情；
- `/api/paper-supervisor/status`：心跳、锁、watermark 和下个阶段。

高影响状态转换采用服务端确认和幂等键。API 不提供任意修改账本、成交或 sealed 交易日的接口。

## 15. Lab 产品与交互设计

### 15.1 新页面

在现有页签后增加 `07 前瞻观察`。它是运行工作台，不是另一张回测报表：

```text
部署列表
  → 部署详情
      ├─ 今日运行
      ├─ 交易日账本
      ├─ 订单与成交
      ├─ 对账
      ├─ 漂移与告警
      └─ 前瞻证据
```

### 15.2 视觉签名：交易日审计轨

延续 v2 的“证据脊柱”，在部署详情中变成横向/纵向自适应的“交易日审计轨”：

```text
数据就绪 → 决策冻结 → 订单计划 → 成交观察 → 收盘估值 → 对账封存
```

每个节点显示真实时间、状态、证据等级和阻塞原因。轨道只是事实导航，不用连续动画模拟市场，也不以全绿掩盖降级日。

### 15.3 状态与反馈

- 持续部署不显示永久 spinner；显示最近心跳、当前阶段、下个计划时点和证据新鲜度；
- 批次进度条继续只属于有总量的任务；
- `paused`、`data_blocked`、`missed`、`error_hold` 使用持久页内状态和恢复动作，不用一次性 toast 代替；
- 暂停、恢复、终止和创建新 release 使用服务端确认；严重操作使用应用内确认对话框；
- 不确定完成时先查询操作状态，禁止重复提交；
- 所有历史事件按交易所时区显示，并同时保留精确时间；
- 列表使用服务端分页，过滤、排序、页码和 deployment 深链进入 URL；
- stale 内容保留可读并标注时间，旧请求不能覆盖新 deployment；
- 表格窄屏保持代码/日期/状态等标识列，比较数据横向滚动；
- sealed 事实只读，修正显示为新事件，不提供看似可编辑的控件。

### 15.4 控制面行为

- `创建部署`：从 release 详情进入，保存后回部署列表并聚焦新行；
- `暂停`：说明停止哪些新动作、已有持仓如何处理；成功后状态持久化；
- `恢复`：先展示数据、对账和 release 一致性检查，全部通过才恢复；
- `终止观察`：不可恢复地结束该 deployment，但不删除证据；使用明确确认；
- `生成证据包`：长任务，允许离开后返回；完成后进入只读详情；
- `新建 release`：不在旧 deployment 上原地编辑。

### 15.5 设计上下文

实施 UI 时更新 `DESIGN.md` 和 `UX-CONTRACT.md`：

- 不改变“冷静的研究工作台”身份、现有系统字体和色彩语义；
- 钴蓝用于选择/主操作，红涨绿跌只表达 A 股收益，琥珀表达警告，阻塞错误使用既有危险语义；
- 记录持续任务、状态转换、确认、焦点、stale、离线、恢复和审计时间线契约；
- 复用现有表格、状态栏、表单和原生 select 决策；
- 真实浏览器验证键盘、失败恢复、窄屏、200% 缩放、后台恢复和 reduced motion。

## 16. 安全与运行边界

- 服务默认只绑定回环地址；无鉴权时不得暴露局域网或公网；
- v3 代码中不存在真实券商 endpoint、credential 或 send-order adapter；
- 纸面输入、状态目录和导出路径均做根目录约束；
- 操作日志不记录密钥、本机隐私路径或不必要环境变量；
- 本地控制 API 防重复、限制请求体和数组规模；
- 磁盘不足、只读、锁丢失或时间异常时停止新风险并告警；
- 导出证据包可校验 hash，但不宣称防恶意篡改；真正合规不可抵赖留待独立设计。

## 17. 验收标准

1. 只有通过 v2 准入的固定 model revision 才能创建正式 release。
2. release、deployment、交易日和证据包均有不可变身份、版本和 hash。
3. `catch_up_replay`、`delayed_paper`、`live_paper` 不会混淆或升格。
4. 同一 deployment/date 重复触发不会重复决策、订单、成交或账本。
5. 数据门禁按 receivedAt 判断真实可用性；缺失时默认不交易。
6. 决策快照在订单前持久化，重算不一致会阻塞部署。
7. PaperVenue 不使用未来行情决定过去成交，日线降级口径明确。
8. 纸面订单生命周期、T+1、现金、预留、费用和持仓不变量有金标准测试。
9. 每日账户、订单、持仓、现金和组合快照完全对账；差异进入 error_hold。
10. 暂停状态重启后仍有效，恢复必须重新通过门禁。
11. 错过运行会留下 missed 事实，补跑不会冒充 live_paper。
12. 漂移覆盖数据、因子、模型、组合、执行、风险、性能和运行健康。
13. 查看阶段结果后修改模型会创建新 release/deployment，不拼接前瞻证据。
14. `passed_paper` 同时通过统计和运行门禁，但不授权实盘。
15. PaperSupervisor 与现有批次 Runner 生命周期分离，进程重启可由事件和 watermark 恢复。
16. UI 完整覆盖持续状态、阻塞、离线/stale、确认、恢复、键盘和窄屏。
17. 旧因子研究、组合研究和策略回测 API/页面保持兼容。
18. 相关测试、故障注入、静态检查、UI 严格审计和真实浏览器关键路径通过。

## 18. 第四阶段边界

v3 完成且积累足够 `prospective_observed` 证据后，才可以单独设计真实资金阶段。第四阶段至少需要重新决策：

- 首个市场、账户类型、券商和订单类型；
- 身份认证、密钥托管、权限和双人审批；
- 真实 OMS、券商回报、未知发送结果和订单对账；
- 账户唯一写者、资金/持仓预留和不可变账本；
- durable kill switch、恢复、灾备和人工操作手册；
- 实时行情授权、SLA、监控、告警和支持责任；
- 小资金灰度、损失预算和逐级放量门禁。

这些均不属于 v3，也不能由 `passed_paper` 自动触发。
