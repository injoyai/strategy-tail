# UX-CONTRACT.md — 06 组合研究页 UI 契约（只记录 UI 后果与来源）

> 日期：2026-09-19
> 范围：`internal/lab/web/lab/index.html` 新增“06 组合研究”页签的 UI 行为契约。
> 本文件**只记录 UI 后果与来源**（设计 §13 / §12.2 与 Task 9 API），不复制业务规则；
> 业务规则（模型 hash、证据降级、门禁结论、归因口径）见正式设计 §5-§11 与
> `internal/portfolioresearch`、`internal/lab`。

## 1. 页面定位（来源：设计 §13.1）

- 新增第 6 个页签“06 组合研究”，位于“05 候选因子”之后；不改动既有 5 页签的
  URL 行为与任何功能。
- 页面只做“把已有验证证据组装成冻结模型，并检查其样本外组合表现”，不展示脱离
  证据的“魔法总分”（来源：§13.1/§13.4）。

## 2. 状态枚举 → UI 呈现（来源：`internal/portfolioresearch/types.go`、`portfolio_runner.go`、Task 8/9）

| 枚举（后端） | UI 呈现 |
|---|---|
| 实验状态 `queued/running/completed/failed/cancelled/insufficient` | `RunState` chip：排队/运行/完成/失败/已取消/证据不足；`completed` 才提供产物下载与报告渲染 |
| 验证状态 `created/completed` | chip：进行中/已完成；`created` 可发起运行，`completed` 展示 verdict |
| 验证结论 `passed/failed/insufficient/error` | verdict chip：通过（绿）/未通过（红）/证据不足（琥珀）/执行错误（红）；`passed` 旁文案“只表示通过冻结协议，不表示未来盈利保证” |
| 证据等级 `exploratory/retrospective/prospective` | chip：探索性（琥珀）/回溯/前瞻；探索性输入在脊柱节点标“降级” |
| 因子方向 `higher_is_better/lower_is_better` | 文字“越高越好/越低越好”（颜色不表达方向） |
| 任务阶段 `queued/loading_data/transform/combine/target/execute/metrics/attribution/finalize`（来源：`/api/status` `phase`） | 全局状态栏阶段文案：排队中/加载数据/截面变换/合成分数/目标组合/组合执行/组合指标/收益归因/发布产物 |
| 任务进度 `done/total=-1`（来源：`/api/status` 组合分支） | 不确定进度（`indeterminate`），不显示伪造百分比；已知后显示 `done/total` 与百分比 |

## 3. 证据脊柱 → 数据来源（来源：设计 §13.3、`FactorModel`/实验/验证 API）

| 节点 | 数据来源 | 点击定位 |
|---|---|---|
| 验证因子 | 当前模型 `validatedFactors`（数量、方向、证据等级、实现版本） | 模型编辑“输入证据”区 |
| 变换 | 当前模型 `transformPipeline`（missing/winsorize/neutralize/standardize 摘要） | “变换”区 |
| 合成分数 | 当前模型 `combination`（method；rolling_ic 时附 windowYears/shrinkage/maxAbsWeight/fallback） | “合成”区 |
| 目标权重 | 当前模型 `portfolioPolicy`（selection/topN|topQuantile/cashBuffer 等） | “组合”区 |
| 实际持仓 | 该模型最近实验 `status` + `quality.avgHoldings`（来源：`/api/portfolio-experiments` 列表/详情） | 运行详情 |
| 样本外结论 | 该模型最新验证 `verdict`（来源：`/api/portfolio-validations` 列表） | 验证详情 |

节点状态三态：`ok`（数据齐全）/`empty`（无数据）/`degraded`（存在降级：证据等级
exploratory、实验 failed/insufficient、验证 failed）。节点是审计关系，不做装饰动画，
不代替详细表格（来源：§13.3）。

## 4. 交互契约（来源：设计 §13.4）

- **原生 `<select>`**：模型/运行/验证页所有下拉均为原生 select，接受平台弹层外观。
- **表格排序**：排序触发器为 `<button class="pf-sortbtn">` 并维护 `aria-sort`
  （ascending/descending/none）；模型列表可排：状态、revision、证据等级、因子数、
  最近验证。
- **服务端分页**：实验/验证列表使用服务端 `page/pageSize`（来源：§12.2、Task 7/8
  Store）；模型 API（`GET /api/factor-models`）由 Task 9 提供为全量列表（无分页参数），
  模型列表对全量做本地分页并把 `page` 写入 URL 恢复。页码越界由后端返回空 items+真实 total。
- **URL 恢复**：`?tab=portfolio&model=&revision=&run=&val=&filter=&sort=&page=`。
  `run` 为实验 ID、`val` 为验证 ID；`filter` 编码模型 ID/状态筛选，`sort` 编码 `key:dir`。
- **stale 保护**：后台刷新保留旧内容并在区域顶部标 stale 标签；不允许旧请求覆盖用户
  当前选择（见 §5）。
- **错误与重试**：错误显示在所属区域并提供重试按钮；服务端错误后保留用户输入
  （表单不重置，requestId 同一次动作复用）。
- **焦点**：创建模型完成/保存新 revision 后聚焦模型列表新行“编辑”按钮并提示；
  运行完成聚焦运行详情标题；错误恢复聚焦重试按钮；无 `alert/confirm/prompt`。
- **窄屏**：表格容器横向滚动；模型/实验/验证列表标识列（ID 列）`position:sticky`
  保留，不静默隐藏业务字段。

## 5. 请求/幂等/失败恢复（来源：Task 9 API 语义、候选页既有模式）

- 模型创建/追加 revision：`POST /api/factor-models`，`requestId` 为幂等键；
  表单输入变化重置 requestId，网络失败重试复用（同候选因子页 `candidateRequestId` 模式）。
  编辑已有模型 → 携带 `modelId + baseRevision`（最新 revision）追加新 revision，不原地覆盖。
- 实验/验证创建：`POST /api/portfolio-experiments` / `/api/portfolio-validations`，
  同样以 `requestId` 幂等；创建成功为长任务提交，随后 `POST /api/portfolio-runs/{id}/start`
  （202 accepted）。
- 取消：`POST /api/portfolio-runs/{id}/stop`（复用共享 stopCh）；运行/验证取消后从
  Store 持久状态恢复，不发布正式报告（来源：§14）。
- 浏览器断开：服务端任务继续，重连后从 `/api/status` 与持久记录恢复（来源：§14）。

## 6. API 缺口（Task 9 未暴露，页面不越权实现）

- **模型归档/恢复**：`FactorModelStore.Archive/ArchivedAt` 存在但 Task 9 **未注册
  任何归档路由**，`GET /api/factor-models` 也不返回归档标记字段；故模型列表不提供
  归档/恢复操作，模型“状态”列改为展示**最近验证结论**（从 `/api/portfolio-validations`
  按 modelId 聚合最新一条），并设“状态”筛选。
- **模型列表分页**：`GET /api/factor-models` 无分页参数，模型列表本地分页（见 §4）。
- **v1 验证枚举**：无 `GET /api/factor-validations` 路由，页面不枚举 v1 验证；组合输入
  证据的可选项聚合自**已有模型的 `validatedFactors`**（带 `validationId`），候选库
  （`/api/factor-candidates`）仅作参照并提示“候选需先完成 v1 验证后才能作为输入证据”。
- **基准**：`Benchmark.Available=false`（Task 9 未接入），运行详情基准区显示不可用原因，
  不阻止绝对收益展示（来源：设计 §9.5）。

## 7. 完成门槛自查（来源：Task 10 完成门槛）

- 页面术语与设计/API 一致；无静态假数据；无浏览器端重新计算正式结论（verdict/指标/
  归因全部渲染后端返回值）。
- 正式结论（verdict、指标、归因）只从 `/api/portfolio-experiments/{id}/artifacts/report.json`
  与 `/api/portfolio-validations/{id}` 读取并渲染。
