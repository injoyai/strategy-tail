# 前瞻纸面交易与模型治理 v3 实施计划

> 日期：2026-09-18  
> 状态：Ready after v1/v2 admission gates  
> 上游设计：`docs/superpowers/specs/2026-09-18-prospective-paper-trading-v3-design.md`  
> 实施边界：只实现本地纸面部署、真实时间前瞻证据、监控、对账和治理；不连接券商、不发送真实订单、不处理真实资金

## 1. 交付目标

把通过 v2 的固定组合模型转成能够持续接受真实时间检验的本地纸面系统：

```text
PortfolioValidation passed
  → immutable ModelRelease
  → PaperDeployment
  → PaperSupervisor daily cycle
  → data readiness
  → frozen decision
  → observed-market paper fills
  → ledger + reconciliation
  → drift + alerts
  → ProspectiveEvidence
  → continue | passed_paper | failed_paper | insufficient | operational_error
```

完成后必须能够证明：

1. 模型在观察期开始前已经冻结；
2. 每个交易日的数据在什么时间真实到达；
3. 决策和成交只使用当时已经可见的信息；
4. 停机、重复触发、迟到数据和写入中断没有制造重复事实；
5. 纸面账户每天可以从事件与账本重建并对账；
6. 运行异常、漂移和人工操作完整保留；
7. 事后补跑没有冒充真正的 live paper；
8. 修改模型会结束旧证据序列，而不是继续拼接。

## 2. 不可突破边界

1. **没有真实外部副作用。** 不加入任何券商 SDK、endpoint、credential、真实订单发送或账户查询。
2. **不修改受保护核心结构。** 不扩展 `core.Trade`、`Backtest`、`Cost`、`PositionConfig`、`Buyer`、`Seller` 承载纸面状态。
3. **PaperSupervisor 不复用批次 Runner。** 持续部署与有界分析任务有不同生命周期。
4. **事实先持久化，再推进阶段。** 决策、订单意图、成交、账本和状态转换均先写入不可变事实。
5. **幂等不是“查一下有没有”。** 每个交易日、阶段、订单和事件都有稳定业务键和状态机约束。
6. **未知 fail closed。** 数据时效、时间、锁、账本或状态不确定时停止新风险。
7. **证据不可升格。** missed、catch-up、delayed 和 live 分开累计。
8. **模型不在线学习。** 权重和门禁按 release 冻结；任何变更创建新 release。
9. **本地单写者。** 多进程、多标签页不得同时写同一纸面账户。
10. **计划不代表已实现。** 每个阶段必须由代码、测试、运行和浏览器证据验收。

## 3. 开工门禁与阻塞决策

### 3.1 必须验证的上游事实

Task 0 必须从当前实现确认，而不是只读设计文档：

- v1 validation 身份、证据等级和 hash；
- v2 model/revision、experiment、validation、target/order/fill/ledger/report 契约；
- v2 次日成交、T+1、费用和会计金标准；
- v2 组合验证至少存在可用于夹具的 passed/failed/insufficient 样例；
- 当前 `cmd/lab` 生命周期、启动参数、HTTP 绑定和 `/api/status` 语义；
- 当前数据源是否能记录 `receivedAt`，以及日线/分钟线的实际到达方式。

未满足项必须作为上游任务补齐，不能在 v3 创建同名影子字段绕过。

### 3.2 实施前必须作出的环境决定

以下值不能由计划编造默认答案；Task 0 将其记录为配置或显式 blocked：

- 纸面运行的交易所业务时区和日历来源；
- 本机应保持运行的时间窗；
- 当前数据源支持 `live_paper` 还是只能 `delayed_paper`；
- 缺数据时 `block` 还是允许只减仓模拟；
- 日线数据的明确纸面成交口径；
- 纸面账户初始资金和观察期；
- 单节点锁安全接管等待时间；
- 磁盘保留和备份策略。

这些参数都进入 release/deployment，不写成隐藏常量。

## 4. 目标目录与模块边界

若实施时没有更强的仓库既有约定，采用：

```text
internal/papertrade/
  types.go                 # IDs、值对象、枚举、状态
  clock.go                 # 系统/测试时钟
  calendar.go              # 交易日与阶段计划
  supervisor.go            # 持续调度与恢复
  lock.go                  # 本地单写者租约
  data_gate.go             # readiness snapshot
  decision.go              # 调 v2 模型并冻结决策
  order.go                 # 纸面订单状态机
  venue.go                 # PaperVenue
  account.go               # 唯一账户写者
  ledger.go                # 不可变账本
  risk.go                  # 运行前风险与暂停
  reconcile.go             # 每日对账
  monitor.go               # 漂移和告警
  evidence.go              # 前瞻证据和治理门禁
  event_store.go
  checkpoint_store.go

internal/lab/
  model_release*.go
  paper_deployment*.go
  paper_handlers.go
  paper_adapter.go
  web/lab/index.html

data/lab/model-releases/
data/lab/paper-deployments/
data/lab/paper-events/
data/lab/paper-ledger/
data/lab/paper-checkpoints/
data/lab/prospective-evidence/
output/paper/
```

`internal/papertrade` 不依赖 HTTP/DOM；Lab 负责上游适配、配置、API 和页面。纸面领域只能通过明确接口读取 v2 冻结模型，不能导入 handler 或解析页面请求。

## 5. 交付顺序

```text
Task 0  上游准入、数据能力和运行决策
  ├─> Task 1  领域类型、Clock、Calendar、ID
  ├─> Task 2  Event Store、Checkpoint、单写者锁
  └─> Task 3  ModelRelease / Deployment Store
          │
          ├─> Task 4  Data Readiness Gate
          ├─> Task 5  决策冻结与幂等日循环
          └─> Task 6  纸面订单与 PaperVenue
                    └─> Task 7  Account / Ledger / Reconciliation
                              ├─> Task 8  Risk / Pause / Recovery
                              ├─> Task 9  Drift / Alert / Evidence
                              └─> Task 10 PaperSupervisor 集成
                                         └─> Task 11 API 与 headless 运行
                                                   └─> Task 12 UX 契约与页面
                                                             └─> Task 13 故障注入和端到端
                                                                       └─> Task 14 文档、观察期与发布门禁
```

Task 4、6 的纯函数可在基础类型稳定后并行。Task 5/7/10 涉及共享事件和阶段语义，必须顺序集成。`server.go`、`index.html`、`DESIGN.md`、`UX-CONTRACT.md` 由单一集成人修改。

## Task 0：验证准入、冻结基线和完成决策表

**目标**：确定 v3 能证明什么，尤其不能把缺少 receivedAt 的数据称为实时前瞻。

**动作**：

1. 读取 `PROJECT_RULES.md`、`MEMORY.md`、v1/v2 正式设计和当前代码。
2. 记录 `git status --short`，保护当前未提交的 `internal/researchrun/*`、Lab HTML 和其他协作者改动。
3. 运行上游最小测试，确认 model/validation/execution/accounting 契约。
4. 用真实数据接入路径画出 eventAt/availableAt/receivedAt 来源表。
5. 创建 passed/failed/insufficient/exploratory 的上游兼容夹具。
6. 完成 §3.2 决策表；未决高影响项标为 blocked，不用示例值冒充决定。
7. 形成准入报告，明确当前最高能产生 `delayed_paper` 还是 `live_paper`。

**建议基线命令**：

```powershell
git status --short
go test ./internal/lab ./internal/researchrun ./internal/portfolioresearch
```

包不存在时按上游实际路径调整，不把“路径不存在”算成测试通过。

**完成门槛**：每一前置契约都有实现/测试证据；数据时间能力没有模糊项。

## Task 1：定义领域类型、Clock、Calendar 与稳定身份

**文件**：`types.go`、`clock.go`、`calendar.go` 及测试。

**动作**：

1. 定义 release/deployment/day/order/event/ledger/evidence 稳定 ID。
2. 定义 evidence class、deployment state、day stage、order state、alert severity 和 verdict。
3. 所有时间保存 UTC instant，同时携带业务时区和 trading date。
4. `Clock` 提供 Now/After 或计划接口；测试时钟可显式推进，不使用真实 sleep。
5. `Calendar` 提供交易日、阶段时点和前后交易日，不按自然日推算。
6. 固定同一时刻事件优先级，并进入 run manifest。

**测试**：节假日、跨年、夏令时不适用/适用边界、时钟回拨/前跳、同刻排序、ID 确定性、非法状态枚举。

**完成门槛**：测试无需等待墙上时间；交易日不会由 `date + 1` 猜测。

## Task 2：实现 Event Store、Checkpoint 与单写者锁

**文件**：`event_store.go`、`checkpoint_store.go`、`lock.go` 及测试。

**动作**：

1. JSONL 事件包含 schema version、event ID、aggregate ID、sequence、occurredAt、recordedAt、payload hash 和前一事件 hash。
2. 追加写完成并持久化后才返回成功；部分尾记录恢复时隔离。
3. checkpoint 保存已应用 sequence/watermark/state hash，但可由事件重建。
4. 对同 event ID 或幂等键去重；payload 不同则冲突而不是覆盖。
5. 本地锁记录 owner、PID、启动标识、租约和心跳。
6. 安全接管先验证进程/租约/Store 状态；模糊时只读。

**故障测试**：截断记录、重复事件、sequence 跳跃、hash 链断裂、checkpoint 超前/落后、锁进程崩溃、两个 writer 竞争、磁盘写失败。

**完成门槛**：重放得到相同 state hash；不可能有两个已获写权限的 Supervisor。

## Task 3：实现 ModelRelease、PaperDeployment 与 Store

**文件**：Lab 的 release/deployment 类型、Store 和测试。

**动作**：

1. release 创建时由服务端重新读取并验证 v2 model/validation/hash。
2. 规范化全部运行、数据、成交、风险、观察和治理参数后生成 release hash。
3. deployment 绑定 release、paper account、计划区间和初始资金。
4. 状态转换使用显式命令和审计事件，不允许客户端直接覆盖状态。
5. 修改任何 release 字段产生新 release；旧 deployment 不换绑。
6. 采用只追加 revision/事件、原子索引更新和安全路径。

**测试**：篡改上游结论、失效 hash、证据降级、重复创建幂等、并发状态转换、非法恢复、归档/退役后历史可读、路径穿越。

**完成门槛**：浏览器请求不能把 failed v2 模型包装成正式 release。

## Task 4：实现数据接入时间与 Data Readiness Gate

**文件**：`data_gate.go`、Lab data adapter 及测试。

**动作**：

1. 为每条接入事实保存 eventAt、availableAt、receivedAt、source、version 和 revision。
2. 在首次本地落地时生成 receivedAt；历史导入明确 `importedAt`，不伪造。
3. 按 release 生成应到数据清单和截止时点。
4. 检查覆盖、延迟、重复、缺口、异常价格/成交量、股票池、停复牌、公司行为和版本。
5. 输出不可变 readiness snapshot、输入 manifest/hash 和 ready/degraded/blocked。
6. 门禁结果决定是否正常交易、只减仓或跳过，策略来自 release。

**测试**：按时、迟到、修订、重复、部分股票缺失、错误日历、数据源版本变化、手工导入、receivedAt 缺失、进程停机后补数据。

**完成门槛**：没有 receivedAt 的路径绝不会生成 live_paper。

## Task 5：实现决策冻结与幂等 TradingDayRun

**文件**：`decision.go`、日循环 reducer/测试。

**动作**：

1. 以 deployment+date 创建唯一 TradingDayRun。
2. reducer 控制 scheduled→sealed 单向阶段转换。
3. readiness ready 后调用 v2 固定模型，生成完整 DecisionSnapshot。
4. 保存因子输入、变换、分数、目标、约束、当前账户和代码/data hash。
5. 决策先持久化，成功后才生成订单意图。
6. 同一输入重算只用于 determinism check；不同则 alert + error_hold。
7. 明确 missed、skipped、catch_up 的独立事实和证据等级。

**测试**：重复 schedule、阶段乱序、blocked data、写入中断、重算一致/不一致、停止后恢复、跨交易日状态污染。

**完成门槛**：同一日期最多一个正式 decision ID；订单都能引用它。

## Task 6：实现纸面订单状态机与 PaperVenue

**文件**：`order.go`、`venue.go` 及测试。

**动作**：

1. 定义 PaperOrder/PaperOrderEvent/PaperFill，不复用 `core.Trade`。
2. 将目标与当前实际持仓差转成买卖意图，稳定排序并分配 ID。
3. 实现 accepted/working/partial/filled/cancelled/rejected/expired 合法转换。
4. PaperVenue 只按 observed/received event 顺序推进成交。
5. 实现 T+1、停牌、涨跌停、整手、参与率、滑点、费用和到期。
6. 日线模式使用明确降级策略；禁止 High/Low 推断盘中先后。
7. 每次部分成交先写 fill event，再交账户应用。

**金标准测试**：

- 次日开盘首个合格观测成交；
- 分钟事件顺序改变会按真实顺序改变结果；
- 不能用未来 Low 获得更好买价；
- 部分成交、多事件成交、到期；
- 涨停买/跌停卖/停牌；
- T+1 可售、整手、费用；
- 重复 market/order event 不重复 fill；
- 崩溃发生在 fill 写入前后均可恢复。

**完成门槛**：任何 fill 都有早于或等于它的已持久化行情证据。

## Task 7：实现 Account Coordinator、Ledger 与每日对账

**文件**：`account.go`、`ledger.go`、`reconcile.go` 及测试。

**动作**：

1. Account Coordinator 是 cash/reservation/lot/fees/NAV 唯一写者。
2. 买单先预留现金，卖单先预留可售股；取消/拒绝/到期释放。
3. 每个 fill 生成平衡、可追溯的 ledger entries。
4. 公司行为以独立事件进入账户，不静默改股数/成本。
5. 每日 mark 后执行现金、持仓、订单、费用、权益和组合快照对账。
6. 修正使用 reversing entries，不修改历史账本。
7. reconciliation 失败产生 blocking alert 和 error_hold。

**不变量测试**：现金非负、预留不超余额、成交不超订单、卖出不超可售、账本借贷/流入流出平衡、NAV 恒等式、事件重放一致。

**完成门槛**：任意 sealed 日可以只用事件和行情重建相同账户 hash。

## Task 8：实现运行风险、持久暂停和恢复门禁

**文件**：`risk.go`、暂停/恢复命令及测试。

**动作**：

1. 在订单 accepted 前检查新增金额、单票/行业、持仓、现金、换手、价格/数量、目标跳变和 drawdown。
2. 每项输出 allow/reject/pause 和具体证据。
3. pause 写持久事件，重启后默认保持。
4. 定义 paused 时估值、已有 working orders 和只减仓策略。
5. resume 重新执行 data/release/reconciliation/time/lock 健康检查。
6. terminate 结束 deployment，证据只读，不删除持仓历史。

**测试**：多个限额同时触发、边界值、重复暂停、重启后暂停、恢复失败、过期 release、账本未平、系统时钟异常、operator reason 缺失。

**完成门槛**：没有任何代码路径可以绕过 risk 直接让意图进入 working。

## Task 9：实现漂移、告警和 ProspectiveEvidence

**文件**：`monitor.go`、`evidence.go` 及测试。

**动作**：

1. 从 v2 报告冻结数据/因子/模型/组合/执行/风险/性能基线。
2. 计算当前窗口漂移、样本数和适用性，避免小样本输出假精确。
3. 告警按稳定 fingerprint 去重，记录首次/最近/次数/影响和 resolution。
4. blocking 告警联动 pause，但不自动改模型。
5. evidence builder 汇总有效、degraded、missed、catch-up 日和完整审计链。
6. 门禁同时检查统计与运行条件，生成治理 verdict。
7. 模型/release 变更使旧 deployment superseded，禁止跨 release 合并。

**测试**：小样本 insufficient、阈值边界、重复告警、resolved 后复发、观察期未满、operational error、模型变更、只有 catch-up 表现良好也不能 passed_paper。

**完成门槛**：每个 verdict 都有逐项 gate result，不存在客户端“标记通过”。

## Task 10：实现 PaperSupervisor 调度、恢复与健康状态

**文件**：`supervisor.go` 及测试。

**动作**：

1. 启动顺序：获取锁→验证 Store→重放→对账→恢复 deployment→安排下一阶段。
2. 按 Clock/Calendar 调度，不使用长时间不可取消 sleep。
3. 阶段执行带取消、截止、幂等键和结构化结果。
4. 重启扫描 watermark，严格区分 normal/delayed/missed/catch-up。
5. 心跳保存 owner、lastStage、lastSuccess、nextScheduled、watermark 和健康状态。
6. 关闭先停止新阶段，等待安全点，持久化后释放锁。
7. Runner 和 Supervisor 不共享“全局 running”语义；二者可按资源策略共存。

**测试**：可控时钟跨阶段、启动中断、关闭中断、重复定时器、长暂停、错过多日、锁丢失、Store 损坏、恢复后不重发成交。

**完成门槛**：测试全程不用真实等待；任一崩溃点恢复后业务事实至多一次。

## Task 11：接入 Lab API 与 headless 运行模式

**文件**：`paper_adapter.go`、`paper_handlers.go`；最小修改 Lab 启动/路由文件。

**动作**：

1. 适配 v2 模型执行、研究数据、行情、股票池、基准和日历。
2. 为 `cmd/lab` 增加明确 headless/no-browser 参数；同一启动路径创建 Supervisor。
3. 默认继续绑定 127.0.0.1；无鉴权时拒绝非回环的纸面控制面。
4. 实现 release/deployment/day/order/fill/ledger/alert/evidence/status API。
5. 控制命令使用服务端 idempotency key 和 version/expected-state。
6. 列表服务端分页；审计事实只读；sealed 日无修改 API。
7. `/api/status` 保持批次任务兼容，纸面持续状态使用独立 endpoint。
8. 进程被另一个 writer 占用时 Lab 进入只读并显示 owner/心跳。

**API 测试**：创建、重复提交、版本冲突、非法状态转换、暂停/恢复/终止、重启读取、只读 owner、分页、路径穿越、非回环配置、旧 API 回归。

**完成门槛**：控制面没有任意账本/成交写接口，旧 Lab 客户端不被新状态破坏。

## Task 12：更新设计上下文并实现“07 前瞻观察”

**前置**：完整读取实施时的 `DESIGN.md`、`UX-CONTRACT.md`、运行 CSS/组件和相邻组合研究页面。

**文档动作**：

1. 在 `DESIGN.md` 记录交易日审计轨及持续运行状态表现，不重做视觉身份。
2. 在 `UX-CONTRACT.md` 记录创建部署、暂停、恢复、终止、证据生成、冲突、stale、只读和焦点结果。
3. 明确持续部署不是批次 spinner；全局状态栏仍只表达有界工作。
4. 复用现有 token、按钮、表格、原生 select、告警和响应式规则。

**页面动作**：

1. 新增 `07 前瞻观察`，保留 01–06 的导航和深链。
2. 部署列表展示状态、release、evidence class、最近心跳、最近封存日和阻塞告警。
3. 详情首屏显示“交易日审计轨”、证据新鲜度、下个计划阶段和控制操作。
4. 子视图展示订单/成交、账户/账本、对账、漂移/告警和证据门禁。
5. sealed 事实明确只读；correction 作为新事件展示。
6. 页面从 supervisor status 恢复，不依赖浏览器一直打开。
7. URL 保存 deployment/date/view/filter/sort/page；后台刷新取消或忽略 stale 请求。

**交互状态**：

- initial loading、empty、no-results、active、paused、data_blocked、missed、degraded、stale、read-only-owner、error_hold、completed；
- 暂停/恢复/终止对话框在服务端确认前不关闭；
- 超时后先查询命令状态，禁止直接重复高影响操作；
- 持久异常使用页内 banner/alert，toast 只确认已完成动作；
- sortable header 使用按钮和 `aria-sort`；
- 表单 `noValidate`、真实 label、错误关联、首错焦点和重复提交保护；
- 选择器继续显式接受原生平台弹层；
- 窄屏比较表横向滚动，标识和操作不静默消失；
- reduced motion 下审计轨立即更新，不使用循环动画。

**浏览器矩阵**：正常运行、迟到数据、暂停/恢复失败、命令超时但成功、只读 owner、Supervisor 离线、stale 恢复、空部署、键盘、窄屏、短视口、200% 缩放和 reduced motion。

**完成门槛**：UI 不在客户端推导正式状态或 verdict；持续运行状态和恢复动作清晰可达。

## Task 13：故障注入、端到端、性能与严格审计

### 13.1 确定性端到端

使用可控 Clock、合成 Calendar、行情事件和两个交易日以上场景：

1. v2 passed 模型创建 release/deployment；
2. readiness ready 后冻结决策；
3. 次日部分成交、T+1、费用、收盘估值；
4. 账本和组合对账后 sealed；
5. 生成漂移、告警和 evidence；
6. 重启重放得到完全相同的 state hash。

### 13.2 故障注入矩阵

在以下边界注入崩溃/错误：

- readiness 写入前后；
- decision 写入前后；
- order planned/accepted 之间；
- fill event 与 ledger apply 之间；
- mark 与 reconciliation 之间；
- checkpoint 写入中；
- pause 命令超时；
- 磁盘满、Store 只读、锁丢失、系统时间跳变。

每个场景验证：无重复成交、无丢失账本、状态可解释、风险 fail closed、恢复不会升格证据。

### 13.3 性能与保留

测量事件重放、日循环、列表分页、证据生成、Store 大小和内存。设置可配置 retention/compaction 只能压缩派生索引，不能删除审计事实或改变 hash 链。

### 13.4 UI 严格审计

1. 运行 frontend-design-premium strict audit；
2. 检查 `DESIGN.md`/`UX-CONTRACT.md`/runtime token/兄弟页面漂移；
3. 搜索 native dialogs、非语义点击、无取消请求、无 label、无 `aria-sort`、表格 scroll 泄漏和永久 spinner；
4. 运行项目可访问性、本地化、语法/类型和浏览器检查；
5. 验证失败、恢复、离线、stale、键盘和窄屏，不只 happy path。

### 13.5 建议工程命令

```powershell
gofmt -w internal/papertrade internal/lab
go test ./internal/papertrade ./internal/lab
go test ./...
go vet ./...
```

以实际包为准，如实记录基线失败；不全仓格式化、不改无关文件。

**完成门槛**：设计 18 项 AC 都有测试或运行证据；结构检查不冒充真实时间运行证明。

## Task 14：文档、观察期启动与发布门禁

**文档**：

- 本地 headless 启动、停止、备份、恢复和日志位置；
- 数据到达时间与 evidence class 判定；
- 纸面成交、费用、参与率和日线降级口径；
- 暂停、恢复、error_hold 和人工操作手册；
- 账本/对账/证据包复核步骤；
- `passed_paper` 明确不等于实盘授权。

**观察期启动前演练**：

1. 合成时钟完整运行至少两个交易日；
2. 使用真实数据源进行无订单影子观察，确认到达时间和日历；
3. 执行一次正常关闭/恢复和一次故障恢复；
4. 验证备份后可在隔离目录重建相同 hash；
5. 确认回环绑定、单写者锁、磁盘空间和暂停控制；
6. 人工签署 release/deployment 参数后才开始正式观察期。

**发布门禁**：

- 上游 v1/v2 准入有当前实现证据；
- 数据源能力决定的最高 evidence class 被明确披露；
- 无券商、真实账户或外部订单代码路径；
- 所有故障注入满足至多一次业务事实和 fail closed；
- 每日重建、对账、证据分级和 governance gates 通过；
- UI 严格审计和真实浏览器矩阵通过；
- 旧 Lab 功能和 API 回归通过；
- 最终 diff 无调试旁路、占位状态、密钥和意外产物。

## 6. 测试矩阵

| 层 | 必测内容 | 失败风险 |
|---|---|---|
| Time | Clock、Calendar、时区、跳时 | 证据时间错误 |
| Store | append、hash、重放、checkpoint | 事实丢失或篡改不明 |
| Lock | 单写者、租约、接管 | 重复决策/成交 |
| Release | 上游 hash、冻结配置、证据降级 | 非法模型进入观察 |
| Data | receivedAt、覆盖、迟到、修订 | 前视或假前瞻 |
| Day | 阶段 reducer、幂等、missed | 重复/乱序运行 |
| Venue | 行情顺序、部分成交、T+1、费用 | 纸面成交失真 |
| Account | 预留、现金、lots、ledger | 资产事实错误 |
| Reconcile | 订单/持仓/现金/NAV | 错误未被阻断 |
| Risk | 限额、pause、resume | 风险旁路 |
| Monitor | 漂移、告警、去重、resolution | 异常不可见 |
| Evidence | 等级、有效日、门禁、变更断链 | 错误晋级 |
| Supervisor | 调度、重启、错过、关闭 | 运行不可靠 |
| API/UI | 冲突、超时、只读、stale、恢复 | 操作误导 |

## 7. Work Order 与并行边界

若后续采用多人/多代理实施，先明确所有权：

| Work Order | 独占范围 | 前置 | 禁止触碰 |
|---|---|---|---|
| WO-A | types/clock/calendar/event/checkpoint/lock | Task 0 | UI 和业务指标 |
| WO-B | data gate/decision/order/venue | A 类型稳定 | Lab handler |
| WO-C | account/ledger/reconcile/risk | A 事件契约 | `core` 受保护结构 |
| WO-D | monitor/evidence/release/deployment stores | v2 adapter | UI 计算正式 verdict |
| WO-E | supervisor/API/headless | B/C/D 稳定 | 改写批次 Runner 语义 |
| WO-F | DESIGN/UX/Web/浏览器验证 | API 稳定 | 客户端复制领域状态机 |

共享热点由单一集成人修改。当前未提交的 `internal/researchrun/runner.go`、`runner_test.go` 和 `internal/lab/web/lab/index.html` 属于用户/其他协作者工作，开始实施时必须先重新检查并融合，禁止覆盖或回滚。

## 8. 验收追踪

| 设计 AC | 实施任务 | 主要证据 |
|---|---|---|
| AC1 上游准入 | 0、3 | compatibility fixtures |
| AC2 不可变身份 | 2、3、9 | hash/event/store 测试 |
| AC3 证据不混淆 | 4、5、9 | evidence class 测试 |
| AC4 日级幂等 | 2、5、10 | duplicate/fault tests |
| AC5 receivedAt 门禁 | 4 | data gate fixtures |
| AC6 决策先持久化 | 5 | crash-boundary tests |
| AC7 无未来成交 | 6 | ordered market-event goldens |
| AC8 订单/账户不变量 | 6、7 | state/ledger goldens |
| AC9 每日对账 | 7 | rebuild/reconcile hash |
| AC10 持久暂停 | 8、10 | restart/resume tests |
| AC11 missed 不冒充 | 5、10 | downtime/catch-up tests |
| AC12 全维漂移 | 9 | monitor fixtures |
| AC13 模型变更断链 | 3、9 | supersede tests |
| AC14 paper 不授权 live | 9、14 | verdict/API/docs |
| AC15 Supervisor 独立 | 10、11 | lifecycle/API regression |
| AC16 UI 状态完整 | 12、13 | browser matrix |
| AC17 旧功能兼容 | 11、13 | regression tests |
| AC18 完整验证 | 13、14 | exact commands/evidence |

## 9. 阶段里程碑

### M0：可重放基础

Task 0–3。能够创建 release/deployment，事件和 checkpoint 可重建，尚不生成订单。

### M1：确定性纸面日循环

Task 4–7。合成时钟下完成数据→决策→成交→账本→对账。

### M2：风险、恢复与证据

Task 8–10。暂停、故障恢复、漂移和治理结论完整。

### M3：可操作产品

Task 11–13。API、headless、页面、故障注入和浏览器验证完成。

### M4：正式前瞻观察

Task 14。完成真实数据影子观察和人工 release 签署后，开始固定期限 deployment。观察期间不改模型；发现需要修改即终止并新建 release。

## 10. 完成定义

只有同时满足以下条件，v3 才算实施完成：

- 至少一个合成时钟 deployment 从 release 到 evidence 全链通过；
- 至少一个真实数据源影子观察证明其最高 evidence class；
- 每个交易日都能从不可变事件重建相同账户与状态 hash；
- 故障注入没有重复成交、负现金、超卖或未解释账本差异；
- missed/catch-up/delayed/live 的 UI、API 和门禁一致；
- 暂停和 error_hold 在重启后仍然有效；
- 正式观察期间模型和 release 不可变；
- 没有任何真实订单、真实账户或券商连接能力；
- 所有验证结果按实际运行范围陈述，未运行项明确披露；
- `AGENTS.md` 和受保护核心结构未修改。
