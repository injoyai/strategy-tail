# 盘感练习复核与验收

日期：2026-10-19（Asia/Shanghai）。
范围：当前工作区盘感练习实现及其导航、后台任务和共享模块回归；按本地单用户、单服务实例评估。保留原有未提交修改，没有部署、提交或推送。

结论：本次确认的问题已直接修复，固定数据、HTTP／存储、前端及浏览器验收通过。全市场真实行情的扫描耗时和长期练习量下的存储性能未验证。

依据：[需求 P01–P12](../specs/2026-10-18-trading-practice-requirements.md)、[实施文档](../plans/2026-10-18-trading-practice.md)。此前[回放与练习验收报告](2026-10-19-replay-practice-acceptance.md)保留原记录，本报告补充此次独立复核结果。

## 1. 发现与修复

| 问题 | 修复与关键证据 |
| --- | --- |
| 有历史记录时页面渲染失败 | 模板调用的 `fmtTime` 未由 setup 暴露；真实执行模板可复现 `fmtTime is not defined`。补齐导出，并以非空历史实际渲染测试覆盖，不能只检查模板可编译 |
| 完成一题后缺少重新出题入口，历史状态不更新 | 增加“返回出题”；新会话、买卖或结束成功后刷新历史，单纯逐日推进不反复读取列表。浏览器完成原题后成功开始新题 |
| 收盘判断依赖宿主时区 | UTC 主机将上海 14:59 误判为已收盘。改用明确的上海时区；分别测试 14:59／15:00／16:00，并在 TZ=UTC 下复核 |
| 扫描最后一个命中时取消仍发布题目 | 回归中关闭停止令牌，原实现仍返回成功并保存 active。增加逐日、扫描结束与保存前的停止检查；现在返回停止并保存 cancelled，无可用行情发布 |
| 会话文件身份与目录不符仍可读取 | 替换文件内 ID 后原实现返回成功，后续写入有错误定位风险。在共享会话读取处校验目录与内容身份，Get 和请求幂等查找均使用该检查 |
| 初始背景未限定为约 120 个交易日 | 150 日预热案例返回 151 根行情。投影只返回信号日前 120 日及已揭示后续，保留完整历史计算出的指标；初始窗口为 121 根 |
| 复盘未提供手续费及不足 20 日的明确提示 | 增加每笔费用与实际额外行情日数，界面展示含费金额、手续费及不足提示；数值与现有 Cost 收支手算核对 |
| 旧出题响应覆盖后来选择的题目 | 出题请求绑定会话请求序号；用户已经切题时只刷新历史，不清空或切换当前会话。延迟响应测试修复前失败、修复后通过 |
| 切题后复盘一直加载 | 原复盘的请求失效后未清理 loading，导致新题不能发起复盘。切换身份时清除旧复盘状态并使其响应失效，覆盖两个交错请求 |
| 后台读取改写其他页签 URL，切回练习丢失深链 | 仅在练习页写练习 URL；切回时从当前会话恢复 p 参数。后台完成不改变当前其他页签地址，刷新仍能恢复题目 |
| 评价展示不完整 | 未交易不展示零收益评价；持仓中与未平仓结束展示持有交易日数；盈亏带正负号；历史打平交易保留零值；题目和历史显式标记重练 |

上述修复未修改受保护核心类型、生产依赖、真实行情、已保存策略或已有题目快照。

## 2. 验证结果

环境：macOS arm64，Go 1.27.1，Node 22.23.3；Go 主回归使用 TZ=Asia/Shanghai、GOPROXY=off、GOTOOLCHAIN=local。此轮未重新运行 Linux／Go 1.25 基线，既有报告中的结果不计作本轮证据。

| 检查 | 本轮结果 |
| --- | --- |
| `go test ./... -count=1` | 全仓通过，19 个含测试包通过；其余包无测试 |
| 最终代码 `go test ./internal/lab/... -count=1` | Lab、builder、factorstats、script 均通过 |
| `go test -race ./internal/lab -run '^TestPractice' -count=1` | 通过；本机竞争检测可运行 |
| `TZ=UTC go test ./internal/lab -run 'TestPractice(AuditShanghai\|ScanTrims)' -count=1` | 通过，采用上海收盘时刻，不依赖宿主时区 |
| `go build ./...` | 通过；最终前端已参与嵌入构建 |
| `go vet ./...`；最终代码 `go vet ./internal/lab/...` | 通过 |
| `node --test internal/lab/web/*.mjs` | 最终 109／109 通过 |
| Frontend Design Premium strict 审计 | 0 errors／warnings／unresolved |
| `git diff --check` | 通过 |
| 本地无头 Chrome，真实 Vue／ECharts，固定 API 数据 | 完整流程通过，页面脚本错误 0；1440／390／320 像素无页面横向溢出 |

浏览器覆盖：非空历史渲染、未交易状态、键盘买入、服务端已处理但响应丢失后的同命令重试、买入当天不可卖、推进后卖出、卖后继续观察且收益不变、切页深链恢复、结束确认、复盘费用与数据不足、返回练习图表、返回出题并开始新题、末日买入与未平仓结束。固定 API 仅验证界面，真实状态转换、费用和落盘由 Go／HTTP 测试验证。

新增 `practice_audit_test.go` 还覆盖：卖出后观察超过 20 日仍保持 active、已实现收益及持仓指标不变；结束后保留全部观察过程，再额外提供 20 日，而非把观察期限截为 20 日。

## 3. P01–P12 对照

| 验收项 | 证据 |
| --- | --- |
| P01 策略选择与买入侧 | 现有 ValidateBuySide、StartPractice／API 流程测试 |
| P02 随机与时点 | Reservoir、YearFilter、BuyerPanic 测试及前缀求值调用核查 |
| P03 盲练隔离 | API／快照隔离、提前 review 拒绝测试；浏览器卖后未结束仍匿名 |
| P04 逐日与等待 | WaitThenBuy、命令能力约束、浏览器逐日流程 |
| P05 资金与成交 | AffordableQuantity、ExactNumbers、FullFlow、BudgetShort 及同日卖出限制 |
| P06 卖后观察 | FullFlow、新增 LongObservation 及浏览器收益不变检查 |
| P07 结束与末日 | LastDayDecision、SkipFinishAndGuards 及浏览器末日买入、估值结束 |
| P08 复盘窗口 | 初始窗口、短窗口及长观察后额外 20 日测试 |
| P09 评价口径 | ExactNumbers、新增费用核对、前端未交易及持有天数测试 |
| P10 保存、恢复与重试 | StoreLifecycle、WriteFailureKeepsState、RevisionConflict、Idempotency、竞争检测及浏览器响应丢失重试 |
| P11 快照与重练 | SnapshotIsolation、Retry 及重练标识／延迟响应测试 |
| P12 数据与界面 | CorruptData、身份错配、取消／失败路径、非空历史、竞态、键盘及窄屏检查 |

## 4. 证据与限制

临时浏览器探针为 `/tmp/strategy-tail-practice-audit-browser.cjs`；截图为 `/tmp/strategy-tail-practice-audit-{1440,390,320}.png`；静态审计为 `/tmp/strategy-tail-practice-audit-premium.json`；全仓 Go 输出为 `/tmp/strategy-tail-practice-audit-go-full.log`。临时文件可能清理，仓库内新增回归测试及上述命令为可重跑依据。

未部署或操作正在使用的服务，未进行全市场真实行情长任务、长期历史量性能、Linux 基线复测或多浏览器／屏幕阅读器矩阵验收。盘感练习仍使用已确认的收盘价模拟及单实例存储口径。
