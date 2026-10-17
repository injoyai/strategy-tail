# strategy-tail 代码审查复核报告

- 初审与复核日期：2026-10-17。
- 范围：原报告的 51 个编号及其对应的当前修复稿，覆盖 core、strategies、internal/lab、internal/portfolioresearch、lib/extend、researchdata、cmd 与前端资源。
- 代码基准：`HEAD=809a8bf`，另核对本次复核时已有的未提交修改。原实现与修复稿分开判定；以下状态是复核快照，不代表后续版本。
- 初审记录曾称“7 路扫描、2 路交叉验证，共确认 51 条问题”；本次以源码、调用方、依赖源码和复现结果重新核实，不沿用该确认数量及 Critical/Major/Minor 统计。
- 保留 No.1–No.51 便于追踪。编号数量不等于独立缺陷数量，No.29/30 为同一根因；误报、待确认项、健壮性建议不计作已确认功能缺陷。
- 第 1–5 节保留首次文档复核快照；后续修复及验证结果见第 6 节，不以历史状态代表当前代码。

## 1. 结论与处理顺序

原清单可作为排查线索，不能直接作为批量修复指令。当前修复稿存在已复现的回归，应先处理下表，再验收其余修复。

| 优先处理项 | 已确认事实 | 最小处理方向 |
|---|---|---|
| No.8：分钟线时区修复反向 | SQLite 在项目使用的 xorm 中默认按 UTC 落库；改用本地时间比较后，边界刷新测试得到 4 行而非 3 行 | 保留 UTC 比较和最后一根 K 线刷新语义；不要过滤掉本应更新的边界记录 |
| No.4：外层 recover 不覆盖工作协程 | 研究执行器另起协程，工作协程的 panic 会越过 Runner 外层 recover，使进程退出 | 在实际执行回调的协程边界处理异常，将失败传回任务并正确收敛状态 |
| No.20：超时返回后仍执行脚本 | 当前超时会释放信号量，解释器协程仍可能执行，重复请求可累积后台工作 | 优先使用现有 Yaegi 的 `EvalWithContext`；取消生效前不能把任务视为已结束，仍需披露不能中断的宿主调用边界 |
| No.6：停止接口被错误豁免 | 跨站来源的 `POST /api/stop` 返回 200 并关闭任务通道 | 停止操作也必须通过同源检查；只读校验接口的豁免应另行评估 |
| No.50：回填改变合法零值 | `minHoldings=0` 经后端 `omitempty` 省略后，当前前端回填为 5，再次保存会改变模型约束 | 读取已有模型时将该字段缺失按 0 解释；新建表单的默认 5 单独保留 |
| No.17：误报驱动费用行为变更 | 原实现已在买卖两侧计算过户费，修复稿却额外将深市费用设为 0 | 先核实费用假设并纠正相关文档，不能以“补接费用”为由直接改变市场适用范围 |

## 2. 原清单逐项复核

“成立”表示原问题有源码或复现依据，不表示修复稿已验收。“已有修改”只描述代码快照；未专门复现的条目按静态调用链核对。

### 2.1 No.1–No.15

| 编号 | 位置 | 复核结论与证据 | 当前状态与处理方向 |
|---|---|---|---|
| No.1 | [analysis.go](../../internal/lab/analysis.go)，`binGroup` / `aggregateGroups` / `GroupingConfig.Validate` | **成立，纠正描述。** 原函数仅在超过全部切点时固定返回 4；零基组号要求默认 **5 组**才正确，并非 4 组。2–4 组可越界，超过 5 组会把最大区间误分到第 5 组。切点数必须为组数减一的校验原本已有。 | 修复稿已改为 `len(cuts)` 并补 2/4/20 组测试。最小修复不需要重复增加已有校验，也不应把非法配置静默钳制成正常分组结果。 |
| No.2 | [backtest.go](../../core/backtest.go)，`_backtest`；[forward_return.go](../../core/forward_return.go)，`scanYear` | **成立。** 所有数据都早于目标年份时，循环没有发生切分，`dks` 仍保留历史数据，随后错误参与目标年份计算。这里分割的是历史预热段和目标区间，不是持仓前后。 | 两处已有截断标记；应覆盖“仅历史、无目标年份数据”的回归场景。 |
| No.3 | [backtest.go](../../core/backtest.go)，`Run` / `_backtest` | **原描述错误，需限定。** 返回 nil 行情切片本身不会使基准计算崩溃；需要区别的是 `GetDayKlines` 函数字段为 nil，以及行情读取返回错误。正常有股票的回测也会在 `_backtest` 调用该函数，不能只保护基准分支便宣称全面处理 nil 数据源。 | 修复稿在基准分支加了函数判空和读取失败告警。若要保障空数据源调用，应在入口统一处理并测试，不能把 nil 切片作为复现条件。 |
| No.4 | [runner.go](../../internal/lab/runner.go)，任务启动；[researchrun/runner.go](../../internal/researchrun/runner.go)，工作协程 | **成立，原修复建议不完整。** 外层任务异常与内部工作协程异常必须分别处理，recover 不能跨协程捕获 panic。 | 外层已有 recover，但工作协程 panic 仍使测试进程退出，已复现；组合任务还需保证持久化状态正确结束，不能只改变内存状态。 |
| No.5 | [runner.go](../../internal/lab/runner.go)，`Stop` / 启动路径 | **并发关闭问题成立，部分推断撤回。** `select-default-close` 不能保证多个 Stop 只关闭一次。启动之间已有任务互斥锁，不能仅凭重新赋值就断言旧任务通道被丢失。 | 修复稿已使用原子发布的每任务控制结构和 `sync.Once`。应核对全部启动路径与任务切换，No.41 共用同一处理。 |
| No.6 | [server.go](../../internal/lab/server.go)，路由；[portfolio_handlers.go](../../internal/lab/portfolio_handlers.go) | **成立，撤回停止接口豁免建议。** 停止任务有副作用；原报告没有提供其已有其他防护的证据。 | 多个写端点已有同源包装，但 `/api/stop` 仍豁免；跨站请求停止任务已复现。 |
| No.7 | [server.go](../../internal/lab/server.go)，`handlePutScript`；[analysis_store.go](../../internal/lab/analysis_store.go)，`atomicWrite` | **成立。** 原直接覆盖写入可让并发读取方看到未写完的脚本。 | 已复用原子替换；共享固定临时文件名仍需核对并发保存，使用同目录唯一临时文件或明确串行化，不能只加 fsync 就宣称并发安全。 |
| No.8 | [pull-kline.go](../../lib/extend/pull-kline.go)，`updateMinuteKlineYear` | **原时区结论不成立，修复稿引入回归。** xorm v1.3.11 区分 `TZLocation` 与 `DatabaseTZ`，SQLite 的后者默认 UTC。边界记录删除后重新插入是刷新最后一根行情的既有流程，不能直接禁止相等时间重新插入。 | 本地时间比较使边界刷新测试出现重复行；仅恢复原 UTC 删除语句，同一测试通过。见第 3 节。 |
| No.9 | [api.go](../../cmd/screen/api.go)，诊断接口；[screen.go](../../cmd/screen/screen.go)，`Diagnose` | **入口校验与错误回显问题成立。** `AddPrefix` 不替代代码格式校验，底层路径拼接继续使用 code；但本次没有验证任意文件读取或完整攻击链，不能直接采用该安全结论。 | 已有代码格式校验和通用错误响应；日志也应避免输出敏感数据。 |
| No.10 | [main.go](../../cmd/screen/main.go)；[screen.go](../../cmd/screen/screen.go)，`init` | **条件性锁冲突风险，降低确定性。** 原实现为同一文件创建两个引擎；依赖的 `NewSqlite` 将每个引擎连接数限制为 1，但两者不共享队列。并发访问可能锁冲突，双引擎本身不等于必然故障。 | 已改为复用引擎；本次未做真实锁冲突负载复现，不保留“必然报 database is locked”的表述。 |
| No.11 | [buy_window.go](../../strategies/buy/buy_window.go)，`N天前符合.Buy` | **成立。** 原循环漏掉闭区间 `To` 端点。 | 已包含端点，并保留 `i >= 1`，避免把空切片传入内部 Buyer；应补闭区间行为断言。 |
| No.12 | [sell_ma.go](../../strategies/sell/sell_ma.go)，`LongMADown.Sell` / `maUp` | **成立。** 预热不足时 `maUp=false`，取反被当成卖出。默认参数下需要 250+5 根。 | 已增加预热判断，但 Lab 的 `sell_long_ma_down` 条件基线失败；需用行为测试确认新语义后更新基线，不能直接替换哈希求绿。 |
| No.13 | [Tab6Portfolio.js](../../internal/lab/web/lab/js/components/Tab6Portfolio.js)，`data` / `modelErr` | **成立，纠正绝对表述。** 属性未声明时，赋值不能自行驱动响应式更新；不能说错误条“永不显示”，其他响应式更新仍可能触发重新渲染。 | 已补 `modelErr: ''`。 |
| No.14 | [Tab6Portfolio.js](../../internal/lab/web/lab/js/components/Tab6Portfolio.js)，模型、实验和验证详情请求 | **成立，纠正后果。** 共用序号会让不同流程相互使有效请求过期，主要后果是有效响应被丢弃，不能描述为序号检查失效导致旧响应覆盖新响应。 | 已拆分模型、运行和验证详情序号。 |
| No.15 | [Tab5Candidates.js](../../internal/lab/web/lab/js/components/Tab5Candidates.js)，`loadCandidates` | **成立。** 切换归档显示等操作的响应乱序，可以使旧列表覆盖新筛选结果。 | 已加独立序号，并在成功与失败回调中检查。 |

### 2.2 No.16–No.34

| 编号 | 位置 | 复核结论与证据 | 当前状态与处理方向 |
|---|---|---|---|
| No.16 | [analyze.go](../../core/analyze.go)，`Analyze`；[analyze_html.go](../../core/analyze_html.go)，资金曲线 | **成立。** 原实现按买入顺序累加已实现盈亏；回撤时长用索引差，实际上是交易笔数，不能称交易日数。 | 已按卖出时间排序并改日历天差。日历天、交易日和恢复前水下时长是不同口径，需明确对外契约后验证，不能混称。 |
| No.17 | [cost.go](../../core/cost.go)，`BuyCost` / `SellIncome` / `TransferFeeForCode` | **撤回“过户费未接入”。** 原函数均已计算 `turnover * TransferFeeRate`；辅助函数未使用并不等于费用未计算。 | 修复稿改变了公开方法参数和深市费用行为。应先核实项目费用假设，并处理与实际收费说明的冲突，不能按原误报继续接入。见第 4 节。 |
| No.18 | [types.go](../../core/types.go)，`DefaultCost` | **注释问题成立。** 原费率、滑点和最低佣金说明与实际返回值不一致。 | 已修改注释；其中“仅沪市收过户费”依赖 No.17 的费用决策，不能因写入注释就视为已确认事实。 |
| No.19 | [walkforward.go](../../core/walkforward.go)，训练分母；[backtest.go](../../core/backtest.go)，`_backtest` | **当前调用链不支持原缺陷结论。** `_backtest` 始终返回 `result, nil`，加载失败在内部跳过；原报告假设的错误分支无法用当前实现触发。 | 已增加的成功年份计数不能识别真实加载失败。若要处理失败年份，需先定义和传递覆盖/失败信息，不能只改分母。 |
| No.20 | [interp.go](../../internal/lab/script/interp.go)，`LoadScript` | **无执行期限问题成立，撤回泄漏可接受的建议。** 当前超时后释放信号量，却不终止执行；已安装 Yaegi v0.16.1 有 `EvalWithContext`，取消死循环测试通过。 | 应使用现有取消能力并验证资源收敛。当前新增并发测试返回 `int`，违反 `[]core.Variant` 契约，属于测试输入错误，不能归因为并发加载坏了。 |
| No.21 | [runner.go](../../internal/lab/runner.go)，`saveReport` | **归类为持久化增强。** 原实现已有临时文件加 rename，不能据缺少 fsync 推导出正常读取会拿到半文件；失败清理可减少残留。 | 已复用 `atomicWrite`。与 No.36 共同核对；只同步文件而未同步父目录，不能宣称完整的断电持久性保证。 |
| No.22 | [reports.go](../../internal/lab/reports.go)，`handleDeleteReport`；[runner.go](../../internal/lab/runner.go)，启动路径 | **撤回“状态机卡死”。** 删除旧的最新报告可能把 error 清成 idle，但启动取决于任务锁而非该状态值，仍可再次启动。 | 是否保留错误展示可作为独立状态语义问题讨论；没有卡死复现前，不执行原建议作为故障修复。 |
| No.23 | [pull-kline.go](../../lib/extend/pull-kline.go)，`DayKline`；[common.go](../../common.go)，调用方 | **契约改进，非已证实功能故障。** 数据不足返回 `(nil, nil)` 属实，现有实际调用方已有保护。 | 已增加哨兵错误；需保持与空行情读取的整体契约一致，不把该改进算作已发生的崩溃。 |
| No.24 | [screen.go](../../cmd/screen/screen.go)，`updateRealtime` / `getHistoryDayKlines` | **成立。** 历史 map 写侧持锁，原读侧未持同一锁，定时刷新与实时刷新可能并发。 | 已加读锁；这只解决 map 访问，不代表浅拷贝后共享 Kline 指针的所有并发访问均已验收。 |
| No.25 | [runtime_path.go](../../runtime_path.go)，`withRuntimeRoot` | **成立。** 主操作和恢复 cwd 同时失败时，原代码丢失恢复错误。 | 已使用 `errors.Join` 保留两者。 |
| No.26 | [sqlite_store.go](../../researchdata/sqlite_store.go)，`Range`；[contract.go](../../researchdata/contract.go)，`View` | **成立。** 查询、扫描、遍历错误被转成空结果；View 返回值没有错误通道，上层无法区分无数据与数据库失败。 | 当前仍存在。日志只改善可观测性，不能解决调用方区分失败的契约；是否扩接口需单独评估影响，不能静默扩大修改。 |
| No.27 | [model_kline.go](../../lib/extend/model_kline.go)，`REF` / `HHV` / `LLV` | **降为健壮性建议。** 越界输入可 panic，但已查当前 HHV/LLV 调用方有窗口检查，REF 无实际调用，缺少产品流程可达的越界案例。 | 先明确前置条件和无效返回语义，不盲目返回 0 掩盖调用错误。 |
| No.28 | [report_pdf.go](../../cmd/market-regime/report_pdf.go)，`pdfFontPath` | **成立。** 硬编码 Windows 字体路径限制其他平台 PDF 导出。 | 仍存在。最小修复需有明确可用字体和缺失错误处理，不假定候选字体在所有系统上都存在。 |
| No.29 | [buy_price.go](../../strategies/buy/buy_price.go)，`A近N日涨幅小于.Buy`；[buy_trend_volume.go](../../strategies/buy/buy_trend_volume.go)，辅助函数 | **成立。** 数据不足或起始价无效时原函数返回 0，被阈值条件当成合法涨幅，放行买入。 | 已改为显式有效性返回；Lab 的 `buy_recent_gain_below` 条件基线失败，需补行为断言后同步预期。 |
| No.30 | [buy_trend_volume.go](../../strategies/buy/buy_trend_volume.go)，`riseRateNDays` | **合并到 No.29，不独立计数。** 这是该条件的同一辅助函数和同一根因，没有第二条独立调用链。 | 随 No.29 一次修复与验证。 |
| No.31 | [buy_volume.go](../../strategies/buy/buy_volume.go)，包级 `HHV` / `LLV` | **行为属实，运行影响未证实。** 辅助函数原排序确实改变输入顺序，但当前仓库无实际调用。 | 已改成复制后排序；优先评估删除未使用重复辅助函数，不将其列作已发生的数据污染。 |
| No.32 | [buy_底部抬升.go](../../strategies/buy/buy_底部抬升.go)，字段注释 / `withDefaults` | **注释问题成立，原数值说明错误。** 注释称 Window 默认 8、MaxGainMultiple 默认 5%，实现分别是 12 和倍数 20；不是“20/30 日与12/20 日”。 | 注释尚需修正，区分窗口天数与涨幅倍数，不能为了匹配错误注释改策略。 |
| No.33 | [buy_底部抬升.go](../../strategies/buy/buy_底部抬升.go)，`isTrendUpPoint` | **待明确策略语义。** 右侧窗口会截短属实，但等待完整右窗口与允许近期未完全确认的极值点是不同策略，现有证据不足以认定哪种才正确。 | 暂不修改；先明确极值确认规则并给出应命中/不命中的行情案例。 |
| No.34 | [buy_window.go](../../strategies/buy/buy_window.go)，两个窗口包装器 | **成立。** 负 From 可令前缀长度超过切片；最近 Days 天原循环会多扫描一天。 | 已处理非正 From 并调整扫描次数；负值回退默认值还是拒绝，应与参数入口保持一致。 |

### 2.3 No.35–No.51

| 编号 | 位置 | 复核结论与证据 | 当前状态与处理方向 |
|---|---|---|---|
| No.35 | [factor_label.go](../../internal/lab/factor_label.go)，`labelReturn` / `LabelCoverage` | **成立。** 原不可交易桶没有产出路径，无法披露设计要求的此类跳过原因。 | 已加零成交量过滤。该修改改变标签样本集合，需要验证覆盖计数与既有标签政策；不能把该枚举当无用代码直接删掉。 |
| No.36 | [analysis_store.go](../../internal/lab/analysis_store.go)，`atomicWrite` | **持久化增强，与 No.21 同类。** 原已有 rename 原子替换，缺失的是文件同步和失败清理。 | 已增加 Sync 和清理；同步文件不等于同步父目录，固定临时文件名的并发约束也需明确。不要为避免“跨修复冲突”复制同一写入逻辑。 |
| No.37 | [factor_validation.go](../../internal/lab/factor_validation.go)，`validateFreezeEvidenceClass` | **日期边界问题成立，纠正位置与方案。** 实际问题是 `time.Parse` 将 LastDataDate 解释为 UTC，随后与冻结时刻比较；不是泛指 time.Date 构造。 | 已改 `ParseInLocation(..., time.Local)`，对上海部署可修正边界；跨宿主统一应使用明确的市场日历时区，不能把 Local 称为固定时区。 |
| No.38 | [factor_validation_store.go](../../internal/lab/factor_validation_store.go)，`List` | **成立。** 先按状态等筛选再派生 SupersededBy，新替代记录被筛掉时，返回的旧记录会缺少替代关系。 | 当前仍存在。全量读取并派生关系后再筛选输出，增加“旧记录保留、新替代记录被筛掉”的断言。 |
| No.39 | [factor_model_store.go](../../internal/lab/factor_model_store.go)，`Create` / `Archive` | **待明确归档契约。** 归档后仍能追加 revision 属实，但当前归档注释和测试只约定列表隐藏、保留历史，没有明确归档即禁止编辑。 | 暂不按确定缺陷加锁；先确认归档后的可编辑和可运行规则，再统一入口。 |
| No.40 | [factor_model.go](../../internal/lab/factor_model.go)，`assembleFactorModel` | **需限定为快照摘要语义问题。** 顶层 DataSnapshot 只取首个因子，可能不足以代表混合输入；各因子的 DataSnapshot 仍保存在 ValidatedFactors 中，并非全部丢失。 | 明确是否允许混合快照及顶层摘要含义；单纯 Warnf 不能解决错误摘要或可重复性要求，不先行添加笼统告警。 |
| No.41 | [portfolio_runner.go](../../internal/lab/portfolio_runner.go)，`startPortfolioGoroutine` | **与 No.5 同类。** 通道字段与 Stop 的访问必须统一控制，不需要另建一套停止机制。 | 已接入共享 runControl；与 No.5 一起验收，不宣称两个修复组互不依赖。 |
| No.42 | [portfolio_runner.go](../../internal/lab/portfolio_runner.go)，`StartPortfolioExperiment`；[portfolio_experiment_store.go](../../internal/lab/portfolio_experiment_store.go)，状态迁移 | **成立。** 启动只接受 queued；进程退出留下的持久化 running 记录没有恢复路径，重启后该实验不可再次启动。 | 当前仍存在。恢复必须确认记录已无存活执行者，再标记失败或显式中断，不能重置仍由其他进程执行的记录。 |
| No.43 | [redundancy.go](../../internal/portfolioresearch/redundancy.go)，`leaveOneOut` | **成立。** map 迭代决定均值累加顺序，可产生末位浮点差异，违反项目逐位可重复性要求；不是宣称产生显著数值误差。 | 当前仍存在。按日期排序后同时收集 full/loo 值，再计算均值，补重复运行一致性验证。 |
| No.44 | [diagnose.js](../../cmd/screen/web/diagnose.js)，`renderChart` | **成立。** 原每次创建观察器而不释放，生命周期比图表长。 | 已保存观察器引用，并在重建与关闭时 disconnect；“模块级重复创建”的原描述改为函数调用时重复创建。 |
| No.45 | [Tab6Portfolio.js](../../internal/lab/web/lab/js/components/Tab6Portfolio.js)，`loadPfExperiments` / `loadPfValidations` | **成立。** 列表分页或筛选响应乱序可覆盖新结果。 | 已加两个独立列表序号；与 No.14 的详情序号分开。 |
| No.46 | [Tab6Portfolio.js](../../internal/lab/web/lab/js/components/Tab6Portfolio.js) | **撤回当前定位。** 页面没有原报告所指拖拽逻辑，无法建立输入、代码位置与后果的证据链。 | 提供真实文件与复现步骤后再审核；不将其他页面的拖拽自动归入本条。 |
| No.47 | [app.js](../../cmd/screen/web/app.js)，`loadStrategies` | **成立。** 原异常 JSON 响应可被当成数组交给后续渲染。 | 已检查 HTTP 成功状态与数组类型。 |
| No.48 | [app.js](../../cmd/screen/web/app.js)，策略卡；[diagnose.js](../../cmd/screen/web/diagnose.js)，错误展示 | **危险插值属实，攻击可达性需限定。** key 被拼进事件代码、错误被拼进 HTML；策略 key 当前主要来自服务端配置，未证明完整外部攻击链。 | 已对所列 key 使用白名单、对错误文本做 HTML 转义。HTML 转义不能替代事件内 JS 字符串转义，优先使用事件绑定；未宣称全部 XSS 入口已覆盖。 |
| No.49 | [api.js](../../internal/lab/web/lab/js/api.js)；[CandidateEditDlg.js](../../internal/lab/web/lab/js/components/CandidateEditDlg.js) | **稳健性建议，非已证实冲突处理失效。** 当前后端冲突文案匹配原前端判断；按文案识别状态容易随文案变动而失效。 | 已附带 HTTP status 并按 409 判断，改进合理。 |
| No.50 | [Tab6Portfolio.js](../../internal/lab/web/lab/js/components/Tab6Portfolio.js)，`fillPfForm`；[types.go](../../internal/portfolioresearch/types.go)，`PortfolioPolicy` | **原“缺失字段处理粗糙”未给失败案例；当前修改引入明确回归。** Go 的 MinHoldings=0 会省略 JSON 字段，回填为 5 改变模型行为。 | 已用真实回填方法断言复现。已有模型缺失此字段须恢复为 0；成本字段没有相同的 omitempty 契约，不能一概套用默认值。 |
| No.51 | [diagnose.js](../../cmd/screen/web/diagnose.js)，`loadDiagnose` / `closeDiagnose` | **成立。** 快速切换诊断时旧响应能改写新结果。 | 已加请求序号；关闭时尚未使在途请求失效，迟到响应仍可能重建隐藏图表，应一并验证生命周期。 |

## 3. 验证记录与限制

以下是本会话上一轮代码复核实际执行的结果，不是本轮文档修改后重新运行的结果。临时复现通过 `/tmp` 文件或 Go overlay 注入，未写入项目源码；Docker 使用只读源码挂载、临时测试数据和禁网配置，没有访问生产服务。

### 3.1 现有回归测试

Go 验证使用已有 `golang:1.25-alpine` 镜像、`TZ=Asia/Shanghai`、`CGO_ENABLED=0`、`GOPROXY=off`、`GOTOOLCHAIN=local`，执行：

```bash
go test ./core ./strategies/buy ./strategies/sell ./internal/lab ./lib/extend ./researchdata ./internal/portfolioresearch ./cmd/screen -count=1
```

| 范围 | 实际结果 |
|---|---|
| core、strategies/buy、strategies/sell、researchdata、internal/portfolioresearch、cmd/screen | 通过 |
| internal/lab | 未通过：TestConditionFactorEquivalence 的 buy_recent_gain_below、sell_long_ma_down 基线不符；TestLoadScriptSerializesConcurrentLoads 使用错误返回类型，报“Strategy() 返回类型异常: int” |
| lib/extend | 未通过：TestUpdateMinuteKlineYearRefreshesBoundaryBar 得到 4 行，预期 3 行 |
| 前端 `node --test internal/lab/web/*.mjs` | 63 项通过；不覆盖本次所有竞态和回填回归 |

策略行为有意改变时，应先添加能解释边界语义的行为断言，再同步失效基线；不应只替换基线哈希、跳过或弱化有效测试。

### 3.2 定向复现

| 复现 | 结果与证明范围 |
|---|---|
| No.8：仅通过 overlay 把分钟线删除语句恢复为原 `Time=?`、UTC 格式化，运行同一边界刷新测试 | 通过。证明当前本地时间改法引入该重复行；不等于验证全部行情更新场景 |
| No.4：外层 defer Runner.recoverTaskPanic，内部 ForEachCodeData 的工作协程触发 panic | 测试进程仍因工作协程 panic 退出。证明外层恢复不能保护内部协程；这是隔离复现，不是线上服务崩溃记录 |
| No.6：httptest 发送带跨站 Origin 与 Sec-Fetch-Site 的 POST /api/stop | HTTP 200，任务停止通道被关闭。证明服务端未拒绝该来源；未进行真实浏览器攻击演练 |
| No.20：已安装解释器执行死循环，使用 EvalWithContext 和 50ms context 期限 | 返回 context.DeadlineExceeded。证明依赖存在取消能力；不保证任意阻塞的编译侧宿主调用均可中断 |
| No.50：直接调用当前 fillPfForm，模型输入省略合法零值 minHoldings | 断言回填值为 5。证明当前表单改变了后端零值语义；没有保存或改动真实模型 |

未运行全仓测试、竞态检测、真实浏览器端到端验收、断电持久性测试或生产负载测试。静态发现与隔离复现的证明范围不能扩展为上述检查已通过。

## 4. 费用假设与既有边界

No.17 原结论应撤回：`HEAD` 中的 BuyCost/SellIncome 已计入过户费，未使用的 TransferFeeForCode 只是辅助函数。修复稿把方法增加 code 参数并按市场过滤费用，属于行为和接口变更，不能描述为补上漏算。

[深交所关于降低 A 股交易结算收费标准的说明（2015-07-01）](https://www.szse.cn/aboutus/trends/news/t20150701_518630.html)明确提到沪深两市过户费统一收取。因此“深市不收”不能仅凭旧辅助函数或项目注释确定。具体回测时期、实际收费标准及项目是否刻意采用简化成本假设，应明确后再统一代码、测试和正式文档；本报告不替代历史费率建模决策，也不授权修改受保护接口。

其余边界：

- `Do()` 分钟级路径的指针副作用存在历史等价性约束，不借本清单清理；多变体数据隔离按已有 researchrun 路径核对。
- ProfitFactor 在**无亏损且有盈利**时为正无穷；不是原附录所称“亏损时为 +Inf”。该数值语义与报告是否能序列化是两个问题。
- Virtual 期末估值、CrossSectionRank 当前规模下的实现、Build 对未知类型返回 nil，不能只凭实现形态新增缺陷，需分别核对契约与真实输入。
- 归档编辑规则、极值右窗口规则、混合数据快照规则没有足够依据时，先补定义和案例，不能用防御性补丁自行决定业务语义。

## 5. 修复协作与验收

撤回“6 组文件互不相交、可独立并行修复”的结论。至少存在这些依赖：

| 关联编号 | 必须共同处理的范围 |
|---|---|
| No.5 / No.41 | Runner、组合任务及其他启动路径共享停止控制结构 |
| No.7 / No.21 / No.36 | 脚本保存、报告保存共用 atomicWrite，需统一原子性、并发和持久性约束 |
| No.29 / No.30 | 同一条件与辅助函数，合并修复；同步行为测试与 Lab 基线 |
| No.12 | 卖出策略预热语义与 Lab 条件基线一起验收 |
| No.17 / No.18 | 费用模型、全部调用方、测试、注释及项目费用约定 |
| No.13 / No.14 / No.45 / No.50 | 同一前端组件及 store；详情、列表和表单序列化分别验证 |

建议验收顺序：先纠正第 1 节的误修与未完成修复；再处理有证据的遗留缺陷（如 No.38、No.42、No.43）；最后按任务范围处理健壮性和文档改进。每项验收保留具体输入、预期行为和检查结果，不以清单数量减少或某个测试包通过代替行为验证。

## 6. 后续审核与优化（2026-10-17）

本轮针对其他 agent 修复后的四项审核结论继续修复，并修正基线测试暴露的同一实验生命周期内的取消分支。保留已有协作者修改，未迁移目录、变更核心结构字段或增加依赖。

| 问题 | 本轮处理与行为验证 |
|---|---|
| 同卖出时间的交易随并发收集顺序改变回撤 | 年度统计和 HTML 逐笔曲线共用确定性结算排序。`TestAnalyzeSettlementOrderIndependent` 覆盖买入时间、代码、完全同身份三种并列条件及输入置换；修复前可复现同一批交易回撤 120/60 不一致，修复后统计和 HTML 一致。 |
| No.20：超时返回并不证明底层宿主调用退出 | 脚本执行超时后保留加载令牌直到重启，不继续累积执行。`TestLoadScriptTimeoutKeepsGate` 分别覆盖解释执行死循环和阻塞宿主调用；正常、错误及并发加载测试仍保留。限制：不能自动恢复加载，也不能强制终止已阻塞的宿主调用。 |
| No.42：启动恢复误伤其他实例的活跃实验 | 执行与恢复共用每实验 OS 文件锁，恢复获取锁后重新读状态。`TestPortfolioRecoverySkipsLiveExperiment` 验证另一执行器恢复时原任务继续完成，`TestPortfolioRunLockAcrossProcesses` 验证跨进程互斥及释放，已有孤儿记录恢复测试继续通过。旧版实例未使用文件锁，升级前应停止旧实例。 |
| 费用接口文档与代码不符 | `PROJECT_RULES.md` 对齐现有两参数 `BuyCost`/`SellIncome` 和沪深统一计费假设，不修改接口、费率或费用计算。 |
| 变换阶段取消被记录为失败 | 基线 `TestAuditCancelVsCompleteRace` 暴露偶发 failed；新增 `TestPortfolioCancelDuringTransform` 确定性复现“截面变换失败: 已停止”，修正为持久化 cancelled。 |

最终验证使用禁网 Docker Linux / Go 1.25 环境、上海时区及临时测试数据：`go test ./... -count=1` 全部通过；取消、活跃实验恢复及跨进程锁四项定向测试连续运行 20 轮通过；`node --test internal/lab/web/*.mjs` 67 项通过；`python3 tests/deploy_test.py` 5 项通过；Windows 版 lab 测试二进制交叉编译通过。未进行部署、真实数据回测、浏览器验收、竞态检测或 Windows 运行验证。
