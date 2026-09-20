# 多因子组合研究 v2 实施计划

> 日期：2026-09-18  
> 状态：Ready after v1 admission gate  
> 上游设计：`docs/superpowers/specs/2026-09-18-multifactor-portfolio-v2-design.md`  
> 前置设计：`docs/superpowers/specs/2026-09-18-factor-validation-v1-design.md`  
> 实施边界：只实现可审计多因子合成、long-only 目标组合、独立组合执行、报告和滚动样本外验证；不实现自动寻优、商业风险模型或实盘交易

## 1. 交付目标

把 v1 产生的冻结因子验证证据转成可复现的组合研究链：

```text
ValidatedFactorRef[]
  → TransformPipeline
  → equal_weight_rank / rolling_ic_weight
  → long-only TargetPortfolio
  → t+1 open PortfolioExecution
  → gross/net PortfolioReport
  → non-overlapping PortfolioValidation
```

完成后系统必须能够回答：

1. 组合使用了哪些已验证因子版本，以及最弱证据等级是什么；
2. 每日原始值经过什么变换和方向处理后形成分数；
3. 多个因子是否冗余，各自带来了什么边际信息；
4. 分数如何形成目标权重，约束如何改变了目标；
5. 停牌、涨跌停、T+1、整手、成本和现金怎样改变了实际持仓；
6. 毛收益和净收益差多少，组合风险与回撤如何；
7. 哪些因子、股票、行业、成本和执行偏离贡献了结果；
8. 模型在冻结后的非重叠测试窗是否通过预先声明的门禁。

## 2. 开工门禁

### 2.1 必须先验证的 v1 契约

实施者不得按文件名假定 v1 已完成。Task 0 必须以测试/API/持久化样例确认：

- `FactorValidation` 可按 ID 读取且绑定不可变候选 revision；
- `ResearchProtocol` 包含信号、成交和收益观察时点；
- validation 有 `model/data/code` 类版本信息和机器可读证据等级；
- 历史股票池、PIT、复权和可交易质量可读取；
- 试验账本可以登记成功、失败和取消；
- 上游状态枚举及 hash 已经稳定。

若某项缺失：

- 只允许先实现不依赖该项的纯函数和夹具；
- 不得在 v2 私自定义第二套同义 v1 契约；
- 在 v1 所有者处补齐后再继续集成；
- 暂不支持的路径必须 fail closed。

### 2.2 基线保护

实施开始时记录：

```powershell
git status --short
go test ./internal/lab ./internal/researchrun ./strategies/factor/...
```

若基线本来失败，保存原始错误并区分新增失败。不得清理、回滚或覆盖现有未提交修改，尤其是共享热点 `internal/lab/web/lab/index.html`。

## 3. 实施原则

1. **新建组合领域，不扩张逐票回测。** `core.Backtest`、`Buyer`、`Seller` 保持不变。
2. **纯函数先行。** 变换、合成、约束、会计和门禁先用小样本测试锁定，再接数据和 UI。
3. **训练/测试边界由类型和调用链表达。** 训练器只接收训练视图；执行器只接收已冻结模型。
4. **目标与实际分离。** `TargetPortfolio`、`OrderIntent`、`Fill`、`Holding` 分开存储。
5. **缺失 fail closed。** 不将 NaN、未知可交易性、缺少公司行为或基准静默替换为可用事实。
6. **结果不可变。** 模型 revision、实验和验证使用服务端 ID、hash、tmp+rename 和安全路径。
7. **资源上限可配置。** 因子数、股票数、日期跨度、并发和产物大小不能写成无解释的永久硬上限。
8. **门禁是协议，不是金融真理。** 默认模板可修改，冻结后才执行。
9. **API 先于 UI。** 页面只渲染后端真实状态，不实现一套浏览器端研究语义。
10. **文档目录被忽略。** 若需提交本计划和设计，必须由用户明确决定是否 `git add -f`。

## 4. 目标目录与所有权

目录名以实施时仓库现状为准；若没有冲突，采用：

```text
internal/portfolioresearch/
  types.go                 # 纯领域类型和枚举
  transform.go             # 截面变换
  combine.go               # 合成和滚动权重
  redundancy.go            # 相关、覆盖、边际 IC、留一法
  target.go                # 排名、等权和目标约束
  execution.go             # 组合执行状态机
  accounting.go            # 现金、持仓、净值和对账
  metrics.go               # 组合指标
  attribution.go           # 预测/组合归因
  validation.go            # 非重叠滚动验证和门禁

internal/lab/
  factor_model.go
  factor_model_store.go
  portfolio_experiment.go
  portfolio_experiment_store.go
  portfolio_validation.go
  portfolio_validation_store.go
  portfolio_runner.go
  portfolio_handlers.go
  web/lab/index.html

data/lab/factor-models/
data/lab/portfolio-experiments/
data/lab/portfolio-validations/
output/portfolio/
```

`internal/portfolioresearch` 不依赖 HTTP、DOM 或本地文件布局；`internal/lab` 负责适配上游验证、数据源、存储、Runner 和 API。

## 5. 交付顺序

```text
Task 0  v1 准入和兼容夹具
  ├─> Task 1  领域类型、版本和模型 Store
  ├─> Task 2  截面变换
  └─> Task 3  合成、滚动权重与冗余诊断
          │
          ├─> Task 4  目标组合与约束
          └─> Task 5  组合执行与会计
                    └─> Task 6  指标、基准与归因
                              ├─> Task 7  实验账本和产物 Store
                              └─> Task 8  滚动样本外验证
                                        └─> Task 9  Runner/API
                                                  └─> Task 10 UX 契约与页面
                                                            └─> Task 11 端到端、性能与审计
                                                                      └─> Task 12 文档、迁移和发布门禁
```

Task 2、3 的纯函数可以在 Task 1 类型稳定后并行；Task 4、5 必须先约定共享类型。Task 9、10 顺序集成，避免同时修改 `server.go`、`runner.go` 和单页 HTML。

## Task 0：验证 v1 准入与建立兼容夹具

**目标**：确认上游事实，避免 v2 建在假定接口上。

**读取**：

- v1 设计、实施计划和实际实现；
- `PROJECT_RULES.md`、`DESIGN.md`、已有 `MEMORY.md`；
- validation/candidate/analysis/store/runner/status 相关源码和测试；
- `internal/researchrun` 的历史股票池、未来观察和 PIT 数据合同。

**动作**：

1. 写一份测试夹具，固定一个 passed、一个 insufficient、一个 exploratory validation。
2. 为每个夹具记录 candidate revision、factor instance、implementation version、direction、evidence class、data snapshot 和 hash。
3. 建立适配层接口草案；禁止让 `internal/portfolioresearch` 直接解析 v1 JSON。
4. 明确哪些字段是正式契约，哪些只是当前实现细节。
5. 记录现有未提交文件和共享热点，不修改 `AGENTS.md`。

**测试**：兼容夹具可以读取；旧版本或缺字段返回明确 `unsupported/insufficient`，不 panic、不补造事实。

**完成门槛**：形成逐项准入表。未满足的 v1 条目有负责人/前置任务，v2 不绕过。

## Task 1：领域类型、规范化 hash 与 FactorModel Store

**文件**：

- 新建 `internal/portfolioresearch/types.go`；
- 新建 `internal/lab/factor_model.go`、`factor_model_store.go` 及测试。

**动作**：

1. 定义稳定枚举：证据等级、变换、合成、调仓、运行状态、门禁状态和未成交原因。
2. 定义 `ValidatedFactorRef`、`TransformPipeline`、`CombinationSpec`、`PortfolioPolicy`、`ExecutionSpec`、`FactorModel`。
3. 规范化日期、浮点、map 顺序和缺省值后计算 `modelHash`。
4. 创建 revision 时重新读取并校验所有上游 validation hash；不接受客户端自报证据等级。
5. 实现只追加 revision、原子写和安全路径；归档不删除历史引用。
6. 设置可配置资源限制，并让错误包含具体字段和允许范围。

**测试**：

- 相同语义、不同 JSON map 顺序得到同一 hash；
- 任一因子、方向、变换、约束或成本变化导致 hash 变化；
- 客户端篡改 evidence class 被拒绝；
- 并发 revision 不覆盖；
- 路径穿越、重复 ID、损坏 JSON 和原子写失败安全处理；
- 旧 revision 仍可读取。

**完成门槛**：模型身份和证据降级规则由后端唯一生成。

## Task 2：实现截面变换流水线

**文件**：`transform.go`、`transform_test.go`。

**动作**：

1. 输入使用带日期、代码、值和质量元数据的当日截面。
2. 按固定顺序实现缺失策略、分位/MAD 去极值、中性化、rank/z-score 和方向统一。
3. 中性化依赖一个小型风险暴露接口，只接收 signalAt 时已可用的数据。
4. 每步输出样本数、缺失数、截断数、回归秩、降级和质量事件。
5. 保留原始值与最终值的关联索引，供审计抽样使用。

**金标准测试**：

- 5–10 只股票手算 rank、z-score、方向反转；
- NaN、Inf、全相同值、极小截面和并列值；
- 中位数只使用当日截面；
- 分位边界不读取未来日期；
- 中性化矩阵奇异和行业/市值缺失；
- 输入顺序变化不改变按代码对齐的输出。

**完成门槛**：禁止全样本 Min-Max、未来填充和 NaN→0 隐式转换。

## Task 3：实现合成、滚动 IC 权重和冗余诊断

**文件**：`combine.go`、`redundancy.go` 及测试。

**动作**：

1. 实现等权秩合成，明确缺失时 exclude、当日中位数或可用权重重归一化。
2. 实现滚动 IC 权重训练器：只读取训练视图，包含收缩、权重上限、归一化和不足 fallback。
3. 权重快照保存训练起止日、样本数、原始估计、收缩值、最终权重和 fallback 原因。
4. 实现逐日截面 Spearman 相关、覆盖交并、边际/残差 IC 和 leave-one-factor-out 编排。
5. 相关门槛只生成 warning；删因子必须通过新模型 revision。

**泄漏测试**：

- 修改测试窗收益不改变测试窗开始前已生成的权重；
- 在训练窗末尾追加一天，只影响之后权重；
- 测试窗反向不能自动翻转因子方向；
- 数据不足时严格执行冻结 fallback。

**完成门槛**：每个交易日的分数可以拆解为因子值 × 实际权重，且权重来源可追溯。

## Task 4：实现目标组合与约束管线

**文件**：`target.go`、`target_test.go`。

**动作**：

1. 从合成分数选择 Top N 或 top quantile，稳定处理并列。
2. 生成等权初始目标和现金缓冲。
3. 依序实施单票、行业、可交易预检、换手预算、整手与现金可行化。
4. 每步输出 `ConstraintAdjustment`，包含前值、后值、原因和受影响股票。
5. 无解时返回结构化 `insufficient`，不自动放宽。
6. 同时输出理想目标和约束后目标，供归因比较。

**测试场景**：

- 分数并列、候选不足、行业集中、单票上限；
- 现有持仓导致换手预算不足；
- 低价/高价股票整手取整；
- 现金不足、现金缓冲、零可交易候选；
- 约束顺序固定且结果确定；
- 权重和、现金和最小持仓数不变量。

**完成门槛**：目标组合不含 NaN/负权重，约束冲突有可解释失败。

## Task 5：实现组合执行状态机与每日会计

**文件**：`execution.go`、`accounting.go` 及测试。

**动作**：

1. 定义 `PortfolioState`、`OrderIntent`、`Fill`、`Rejection`、`HoldingLot`、`DailyLedger`。
2. 信号只生成下一交易日意图；执行日先卖后买。
3. 实现 T+1 可售数量、停牌、涨跌停、上市/退市、整手、费用、滑点和现金检查。
4. 默认未成交意图日终失效，下一重平衡日重新计算。
5. 每日按统一口径估值，记录公司行为和数据质量事件。
6. 实现逐日资产对账和确定性事件顺序。

**金标准场景**：

- 两股票卖出释放现金后买入第三只；
- 当日买入不能当日卖；
- 停牌持仓保留、涨停买不到、跌停卖不掉；
- 部分现金只执行部分目标；
- 佣金最低额、印花税方向和整手；
- 退市/缺价按协议失败或降级；
- 同一输入重复运行订单、成交、持仓、净值完全一致。

**不变量**：

```text
cash >= 0
holdingShares >= 0
sellShares <= sellableShares
endEquity = cash + sum(markedHoldings)
beginEquity + pnl - fees = endEquity
```

**完成门槛**：所有不变量逐日通过；未来价格不参与当日成交资格。

## Task 6：实现指标、基准和两层归因

**文件**：`metrics.go`、`attribution.go` 及测试。

**动作**：

1. 输出毛/净 NAV、收益、年化波动、Sharpe、Sortino、回撤/持续期和 Calmar。
2. 基准可用时输出超额收益、跟踪误差和信息比率；缺失时字段为 unavailable 并说明原因。
3. 输出换手、成本拖累、现金、持仓数、集中度、未成交和目标偏离。
4. 预测归因组合 Task 3 的单因子/合成/边际 IC 与留一法。
5. 组合归因输出股票、行业、现金、成本和执行偏离贡献。
6. 明确数值恒等式和舍入容差，禁止把统计分解称为因果归因。

**测试**：常数净值、全正/全负、单次回撤、缺基准、零方差、成本前后、贡献求和、跨年年化和缺失交易日。

**完成门槛**：展示指标均可由保存的基础时序复算，贡献在容差内加总到组合收益。

## Task 7：实现实验账本、报告清单与产物 Store

**文件**：`portfolio_experiment*.go` 及测试。

**动作**：

1. 实验创建时登记 family、model revision/hash、区间、参数变体和试验计数。
2. 状态机固定为 queued/running/completed/failed/cancelled/insufficient。
3. 写入 report、nav、orders、trades、holdings 后生成 manifest 和文件 hash，最后原子发布主记录。
4. 失败、取消和无有效样本同样保留元数据。
5. Store 只暴露安全相对产物名，不返回本机绝对路径。
6. 列表采用服务端分页和稳定排序。

**测试**：崩溃中断、部分文件、hash 不一致、重复完成、取消竞态、非法下载名、分页边界和大列表。

**完成门槛**：不存在“主记录 completed 但产物不完整”的可见状态。

## Task 8：实现非重叠滚动 PortfolioValidation

**文件**：`validation.go`、`internal/lab/portfolio_validation*.go` 及测试。

**动作**：

1. 创建验证时冻结模型 revision/hash、窗口规则、门禁、基准和证据要求。
2. 构建非重叠测试窗；每窗只使用其开始前允许的数据训练滚动权重。
3. 每窗保存模型/权重快照、报告引用、质量事件和门禁结果。
4. 汇总窗口稳定性、净收益、回撤、成本、换手、偏离和基线增量。
5. 后端生成 `passed | failed | insufficient | error`，客户端不能指定。
6. 任一输入证据降级时，输出等级取最弱值。
7. 修改模型或门禁必须创建新验证，旧测试窗标为已见数据。

**泄漏与状态测试**：

- 外层测试收益变化不影响该窗权重/门禁配置；
- 测试窗互不重叠；
- 可用窗口不足为 insufficient，不是 failed；
- 执行错误为 error，不伪装统计失败；
- 测试后改门禁生成新 validation ID；
- exploratory 输入不能得到正式 passed。

**完成门槛**：任一结论可逐窗解释，且没有测试后选择路径。

## Task 9：接入数据、Runner、状态和 HTTP API

**文件**：`portfolio_runner.go`、`portfolio_handlers.go`；按现状最小修改 `runner.go`、`server.go` 和状态类型。

**动作**：

1. 在 Lab 适配 v1 validation、历史股票池、因子计算、行情、风险暴露、基准和交易日历。
2. 复用 `researchrun` 数据边界，不在 HTTP handler 重写逐年逐票加载循环。
3. Runner 增加组合 experiment/validation 任务、取消检查和阶段进度。
4. `/api/status` 在总量未知时返回不确定进度；已知后报告 done/total 和阶段。
5. 实现 model、experiment、validation 的创建/列表/详情和安全产物读取。
6. 输入校验限制日期、因子数、Top N、权重、成本、窗口和产物规模。
7. 长任务 API 返回 accepted + task ID；任务终态从持久 Store 恢复。

**API 测试**：正常创建、非法 validation、证据降级、重复提交、忙碌冲突、取消、重启后读取、分页/排序、路径穿越、旧客户端兼容。

**完成门槛**：UI 所需语义均来自 API；handler 不含统计或交易核心逻辑。

## Task 10：建立 UX 契约并实现“06 组合研究”

**前置**：重新读取当时的 `DESIGN.md`、UI 代码和正式设计；若 `UX-CONTRACT.md` 仍缺失，从技能模板创建并只记录 UI 后果与来源。

**文档动作**：

1. 更新 `DESIGN.md`：页签、证据脊柱、模型/运行/验证状态、响应式和 token 映射。
2. 在 `UX-CONTRACT.md` 记录模型创建 revision、归档、运行、取消、重试、列表恢复、焦点和失败恢复。
3. 不新建第二套颜色、字体、按钮、表格、状态栏或 toast 体系。

**页面动作**：

1. 新增 `06 组合研究`，保留现有五页和 URL 行为。
2. 模型列表使用服务端分页；状态、revision、证据等级、因子数和最近验证可排序。
3. 模型编辑按“输入证据—变换—合成—组合—执行—门禁”分区，保存产生新 revision。
4. 中央证据脊柱展示真实节点状态并定位到对应内容。
5. 运行详情展示目标/实际、毛/净、净值、风险、执行质量和产物。
6. 归因详情展示相关/覆盖/边际 IC/留一法、股票/行业/成本/偏离贡献。
7. 验证详情展示逐窗门禁、最终结论、证据等级、限制和输入 hash。
8. 复用全局状态栏；对 stale 请求使用取消或序列保护。

**交互要求**：

- 原生 select 的平台弹层外观是显式选择；不宣称可控制其打开态几何；
- 可排序表头使用按钮和 `aria-sort`；
- loading/empty/no-results/running/partial/stale/error/done 均有稳定布局；
- 长表在窄屏横向滚动并保留标识列，不静默隐藏业务字段；
- URL 恢复 tab/model/revision/run/filter/sort/page；
- 后端错误保留用户输入，重试不会重复创建；
- 键盘焦点、busy/disabled 和 reduced-motion 行为明确；
- 不使用 alert/confirm/prompt、可点击 div 或虚假按钮。

**浏览器矩阵**：成功、失败、慢请求、stale 覆盖、运行取消、空列表、损坏产物提示、键盘、窄屏、短视口、200% 缩放和 reduced motion。

**完成门槛**：页面术语与设计/API 一致；不存在静态假数据或浏览器端重新计算正式结论。

## Task 11：端到端、性能、安全和 UI 严格审计

**端到端固定案例**：

1. 两个已验证因子，6–10 只股票，跨多个调仓日；
2. 包含 NaN、行业缺失、停牌、涨跌停、T+1、整手和费用；
3. 生成模型 revision、experiment、全部产物和 validation；
4. 手工复核至少一个交易日的变换、分数、目标、成交、现金、净值和贡献；
5. 修改一个测试窗未来收益，证明此前权重和成交不变；
6. 重启 Store 后仍能读取和校验 hash。

**性能基准**：定义代表性因子数、股票数、年份和机器环境，记录峰值内存、总时长和产物大小。若超限，优先流式处理和有界缓存，不通过降低证据精度掩盖。

**安全检查**：路径穿越、超大参数、非有限浮点、损坏 JSON、下载名、并发覆盖、取消竞态和日志敏感信息。

**UI 审计**：

1. 运行 frontend-design-premium 严格项目审计；
2. 检查 `DESIGN.md`、`UX-CONTRACT.md`、运行 token 和兄弟工作流漂移；
3. 对变更文件搜索 native dialogs、非语义点击、无 stale 保护请求、无障碍排序和表格滚动泄漏；
4. 运行项目已有可访问性、本地化、类型/语法和浏览器检查；
5. 保存准确命令、结果和未覆盖矩阵。

**建议命令**（以实施后的真实包为准）：

```powershell
gofmt -w internal/portfolioresearch internal/lab
go test ./internal/portfolioresearch ./internal/lab ./internal/researchrun
go test ./...
go vet ./...
```

不得把全仓失败归因于本任务前先做差分诊断；也不得只跑局部测试就宣称全仓通过。

## Task 12：文档、迁移、发布和回滚门禁

**文档**：

- 更新 Lab 使用说明、API/报告版本、指标口径和限制；
- 给出等权秩基线的最小可复现实例；
- 说明 evidence class、passed 含义、毛/净、目标/实际和归因限制；
- 在正式架构文档记录为何新建组合引擎而不改 `core.Backtest`；
- 仅记录长期有效且已验证的项目记忆；不写临时进度。

**迁移**：

- 新功能默认关闭或隐藏，直到 v1 准入和 Store 迁移验证通过；
- 旧分析、候选、策略回测和旧 URL 无需迁移即可使用；
- 新记录独立版本化；未知字段和旧版本 fail closed；
- 不对历史候选自动生成“已验证模型”。

**发布门禁**：

1. 16 条设计验收标准逐项有证据；
2. 所有小样本金标准和泄漏测试通过；
3. 每日会计恒等式和归因加总通过；
4. 样本外门禁由后端生成，证据等级不升格；
5. API 兼容、重启恢复和路径安全通过；
6. UI 严格审计和真实浏览器关键矩阵通过；
7. 最终 diff 无无关修改、调试代码、占位符和意外产物。

**回滚**：关闭组合研究入口和新建任务，不删除新 Store；历史结果继续只读。不得通过回滚覆盖用户或其他协作者的未提交修改。

## 6. 测试矩阵

| 层 | 必测内容 | 失败含义 |
|---|---|---|
| Model | hash、revision、证据降级、资源限制 | 身份或审计不可信 |
| Transform | 缺失、去极值、中性化、rank/z-score、方向 | 分数语义错误或泄漏 |
| Combine | 等权、滚动权重、fallback、分解 | 合成不可复现 |
| Redundancy | 相关、覆盖、边际 IC、留一法 | 互补性结论不可信 |
| Target | Top N、并列、约束、换手、整手 | 目标权重不可解释 |
| Execution | t+1、先卖后买、T+1、不可交易、费用 | 回测不可交易 |
| Accounting | 现金、持仓、净值、逐日恒等式 | 组合收益错误 |
| Metrics | 毛/净、回撤、基准、归因加总 | 报告误导 |
| Validation | 非重叠窗、无泄漏、门禁、状态 | 样本外结论无效 |
| Store/API | 原子写、恢复、分页、路径、兼容 | 历史或接口不可靠 |
| UI | 状态、键盘、窄屏、URL、stale、错误恢复 | 产品不可审计或不可用 |

## 7. Work Order 与并行边界

若后续由多人/多代理实施，必须先分配非重叠 Work Order：

| Work Order | 独占范围 | 可依赖 | 禁止触碰 |
|---|---|---|---|
| WO-A | `internal/portfolioresearch` 变换/合成/冗余 | Task 1 类型 | Lab UI/API |
| WO-B | 目标、执行、会计、指标、归因 | Task 1 类型 | `core.Backtest` |
| WO-C | model/experiment/validation Store | v1 adapter | UI 和纯统计实现 |
| WO-D | Runner/API 集成 | A/B/C 稳定接口 | 未经协调改共享类型 |
| WO-E | DESIGN/UX-CONTRACT/Web | API 已稳定 | 浏览器端复制业务计算 |

共享热点 `types.go`、`runner.go`、`server.go`、`DESIGN.md` 和 `index.html` 由单一集成人按顺序合并。任何人看到重叠未提交修改时先理解并融合，不得覆盖或回滚。

## 8. 验收追踪

| 设计 AC | 实施任务 | 主要证据 |
|---|---|---|
| AC1 v1 准入 | 0、1、8 | 准入表、adapter 测试 |
| AC2 全链追溯 | 1、7、8 | model/report/manifest hash |
| AC3 截面无未来 | 2 | 金标准和日期边界测试 |
| AC4 合成无泄漏 | 3 | 手算与泄漏测试 |
| AC5 冗余诊断 | 3、6 | 相关/覆盖/边际/留一报告 |
| AC6 约束可解释 | 4 | adjustment ledger |
| AC7 可交易执行 | 5 | 状态机场景测试 |
| AC8 每日对账 | 5 | accounting invariants |
| AC9 毛净与偏离 | 6 | 指标/归因测试 |
| AC10 测试窗隔离 | 8 | outer-window 泄漏测试 |
| AC11 后端结论 | 8、9 | API 与门禁测试 |
| AC12 证据不升格 | 1、8 | degradation 测试 |
| AC13 旧接口兼容 | 9、12 | 回归测试 |
| AC14 完整 UI 状态 | 10、11 | 浏览器矩阵 |
| AC15 文档一致 | 10、12 | DESIGN/UX/API 交叉审查 |
| AC16 工程验证 | 11、12 | 命令和结果记录 |

## 9. 实施完成定义

只有同时满足以下条件，才能把 v2 标记为完成：

- v1 准入不是文档声明，而是当前实现和测试事实；
- 从 validation 到最终 verdict 的每个对象都有不可变身份和版本；
- 等权基线、滚动权重、目标约束、执行和会计均有手算小样本；
- 没有测试窗参与训练、选因子、调参或改门禁；
- 目标持仓、实际持仓、毛收益、净收益和执行偏离同时可见；
- failed、insufficient、error、cancelled 不被合并成“无结果”；
- 旧功能仍可用，新功能可关闭且不会删除历史；
- 局部与全仓验证结果如实记录，未验证风险明确；
- UI 已经过真实浏览器而非只读源码检查；
- 最终差异保持在授权范围内，`AGENTS.md` 未修改。
