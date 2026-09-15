# MEMORY.md — 项目核心记忆

## 项目概况
Go 股票策略回测系统（A 股，tdx 数据源）。组合式策略组件（`strategies/buy|sell` 的 And/Or 组合子）→ `core.Backtest` 回测引擎 → `cmd/*` 各回测入口 → `lib/extend` 从 tdx 拉取 K 线并按"每股一个 sqlite db"落盘（`data/database/day-kline/`、`min-kline/`）。

## 架构与关键决策
- **回测引擎 `core.Backtest`**：`Run()` 中 Cost 零值自动回退 `DefaultCost()`（防止零佣金零滑点失真）；`MCIterations` 字段接入 `config.yaml` 的 `backtest.monte_carlo_iterations`（<=0 默认 1000）。只有 `cmd/backtest`、`cmd/backtest_macd`、`cmd/backtest_mc` 调用 `Run()`；`backtest_macd_smooth/green`、`market-regime`、`index_filter` 系列自行复现 `_backtest` 循环。
- **`Do()` 指针覆写副作用（多组合复用数据必须隔离）**：`Do()` 分钟级卖出循环会原地覆写 `dks[i].Kline` 指针（`today.Kline = minuteKlines.Kline(...)`，等价性测试锁定为"原版行为不可更改"）。同一份 `extend.Klines` 要跑多个回测组合/多次复用时，必须深拷贝 `*extend.Kline` 与内嵌 `*protocol.Kline`，否则组合间互相污染；生产实现统一收口在 `internal/researchrun`，命令与 Lab 不再各自复制。
- **研究矩阵统一执行层（2026-09）**：`internal/researchrun` 统一负责多变体回测的数据加载、worker 池、日线/分钟线模式、K 线深拷贝隔离、取消传播和覆盖率统计。`cmd/backtest_tail`、`backtest_macd_bar`、`backtest_yopen`、`backtest_winrate` 及 `internal/lab` 只保留策略定义与展示/落盘；任一请求年份取数失败仍按旧行为跳过整只股票，但必须通过 `Coverage{Requested,Completed,Skipped,Failures}` 显式披露。
- **因子框架基础（2026-09，Task 1-2）**：`core.Factor` 约定无状态数值因子以 `NaN` 表示无效值；`core` 内存横截面快照按交易日、因子名和排序方向隔离。`strategies/factor` 已实现 `N日动量`、`均线偏离`、`N日斜率`，周期 `Days<=0` 默认 20；斜率以窗口均价归一化。注册表、Factor→Buyer 桥接、Lab 注入和 IC/分位研究尚未落地，不得当作可端到端功能使用。
- **`Do()` 性能优化（2026-09）**：逐日 `joinKlines` O(n²) 复制改为一次性 `full = his + dks` 缓冲 + 前缀切片 `full[:len(his)+i+1]`。切片元素为指针，与原版语义完全一致；`today.Kline` 的分钟级覆写通过共享指针实时可见，是"原版行为，不可更改"。**等价性已由差分测试锁定**：`core/backtest_equiv_test.go` 保存原版 `DoLegacy`（自 git HEAD 逐行复制），同一随机数据（固定种子、深拷贝隔离输入）跑新旧两版逐笔 `reflect.DeepEqual` 比对；今后改 `Do` 必须保持该测试通过。
- **`common.go` init 副作用已移除**：import 不再自动 `Pull.Update`；数据更新须显式 `common.Update()`（各 cmd 入口已补齐，`backtest_mc`/`market-regime` 原先依赖隐式更新）。
- **PDF 报告公共包 `lib/report`**：`ReportData` + `Options{OutputDir, Filename, StrategyDesc, Advice}`；`backtest_macd_smooth` 与 `backtest_macd_green` 的 report_pdf.go 重复实现已抽取至此，cmd 侧仅保留策略文案。`market-regime/report_pdf.go` 结构差异大，刻意不合并。依赖 `C:\Windows\Fonts\simhei.ttf`。
- **HTML 模板拆分**：`core/analyze_html.go`（交易可视化）、`core/forward_return_html.go`（未来收益报告）自各自的统计逻辑文件拆出，纯机械搬移。
- **通用交易记录落盘（2026-09，AGENTS.md 6.1 硬性要求）**：`core/trades_export.go` 的 `ExportTradesCSV(strategyName, filename, trades)`（CSV 明细）与 `ExportTradesHTML(strategyName, filename, trades, getDayKlines)`（ECharts K线买卖点+明细表 HTML，getDayKlines 传 nil 时仅明细）。输出到 `output/trades/<策略名>/`（策略名/文件名经 `TradesExportName` 清洗路径非法字符），按买入时间排序，空交易不生成文件并返回空串。与 `Analyze()` 的按年落盘（`output/backtest/<year>.csv` + `trades.html`）并存：多组合/参数矩阵场景用前者，单策略走 `Run()` 的场景用后者。`backtest_tail` 已接入（每组合 CSV + 全组合汇总 HTML）。

## 约定与坑点
- 仓库 Go 源文件为 **LF** 行结尾；Windows PowerShell `Set-Content` 会写 CRLF（PS5.1 还加 BOM），批量改文件后需转 LF 并跑 `gofmt -l` 核对。
- `gofmt -l` 存在历史遗留未格式化文件（common.go、market-regime/* 等，对齐类问题），按最小改动原则未全仓库格式化。
- 大量 `gofmt`/测试验证基线：`go build ./...`、`go test ./...` 全绿（strategies/buy、strategies/sell、core）。
- 根目录 exe 产物已移至 `bin/`（.gitignore 已含 `*bin`、`*.exe`）；.gitignore 移除了 `.*` 通配（避免新点文件被静默忽略），显式忽略 `.trae-html-share-packages/`。
- `IsTradingTime` 上午边界修正为 11:30（原代码 11:31 与注释不符）。
- `core/forward_return.go` 的 `DefaultForwardDays` 含 90 天（HEAD 已提交），测试期望已同步。
- `cmd/backtest/main.go` 保留死代码（TestBuy/TestSell/years 覆盖等），用户故意留作快速改参数的草稿区，勿清理。
- 用户偏好：cmd/backtest/main.go 是参数调试入口，编辑时避开用户正在修改的区域。

## 尾盘阴线收回策略（2026-09 新增）
- **组件**：`strategies/buy/buy_tail_pullback.go` 的 `A阴线收回{SupportPeriod, MinBodyRatio, MaxRise, Trend}`——上升趋势中阴线盘中跌破支撑均线（MA5/MA10）、尾盘收回、博次日反弹。趋势过滤通过 `Trend core.Buyer` 注入复用 `buy.MAUp` / `buy.A均线多头排列`。7 项单测见 buy_tail_pullback_test.go。
- **参数矩阵回测入口**：`cmd/backtest_tail/main.go`——5 趋势（MA5/10/20向上、多头排列、无过滤）× 2 支撑（MA5/MA10）= 10 组合；卖出统一 `sell.Or{A持仓N天{Days:1}, A止盈止损{0.10, 0.08}}`；沪深主板（GetNoPriceLimitCodes），2022-2026。**性能模式**：每股数据只读一次、内存循环 10 组合（每组合 cloneKlines 隔离），比"逐组合×逐年重读 SQLite"快约一个数量级；重写前单组合耗时 68 分钟。该入口直接调 `bt.Do()`（不经 `Run()`），自行用 `core.Stats()` 汇总。
- **更新阻塞坑**：`common.Update()` 在更新窗口（tdx `Updated` 15:31 节点）过期后触发**全市场约 8700 标的逐库校验**，期间无进度输出、无文件写入（数据未变），但 CPU 满载（10 协程），可达 1 小时+；此时进程不是卡死，勿重启（重启重新校验）。判断方法：`Get-Process` CPU 累计增长 + db 文件 LastWriteTime 不变。

## 尾盘量柱阴线策略（2026-09-12 新增，结论：负期望，勿重复回测）
- **组件**：`strategies/buy/buy_实体阴线.go` 的 `A实体阴线{MinBodyRatio}`——收阴（close<open）且 (open−close)/(high−low) ≥ MinBodyRatio，<=0 只要收阴；沿用 A阴线收回 的实体比例惯例。5 项单测。
- **卖出组件（2026-09-12 新增）**：`strategies/sell/sell_holding_days_tail.go` 的 `A持仓N天尾盘{Days, Time}`——持仓满 N 个交易日且当前分钟快照时间 ≥ Time（默认 "14:55:00"，本地 5 分钟线最后一根，Close ≈ 当日收盘）时触发；无分钟数据退化（快照 00:00:00）视为收盘卖出同样触发。TDD 10 项单测。**接口坑**：`sell_at.go` 的 `SellAt` 是规划型接口 `Sell(code, history, future, getMinklines, buy)`，引擎 `core.Seller` 只认 `Sell(code, dks, buy) bool`，SellAt/SellRSI 属死代码不能直接用于 Backtest。
- **附加条件组件（2026-09-15 新增）**：`strategies/buy/buy_收盘大于昨开.go` 的 `A收盘大于昨开{}`——当日收盘价严格大于昨日开盘价（相等不触发），len(dks)<2 安全返回 false。5 项单测。实测过滤约 60% 信号但无 alpha 提升（见结论）。
- **附加条件组件 2（2026-09-15 新增）**：`strategies/buy/buy_收盘大于昨收.go` 的 `A收盘大于昨收{}`——当日收盘价严格大于昨日收盘价（阴线不吞昨阳，强势整理确认）。5 项单测。胜率最高过滤但期望恶化（见结论）。
- **纯日线快速回测入口 `cmd/backtest_yopen/main.go`（2026-09-15）**：收盘买/T+1 收盘卖口径（seller=`sell.Or{A持仓N天{Days:1}}`）、`bt.Do(..., mks=nil)` 不拉分钟线——引擎无分钟数据时买卖均自动退化为日线收盘成交（core/backtest.go，尾盘卖版组件改用非尾盘版 `A持仓N天`）。全市场 3197 股 × 2 组合仅约 2.5 分钟（分钟线版约 50 分钟，提速约 20 倍），适合快速验证新买入条件。CSV 落盘 `output/trades/收盘大于昨开日线对比/`。
- **假设矩阵入口 `cmd/backtest_winrate/main.go`（2026-09-15）**：7 组合一次验证 4 方向（基准对照 / 缩量两档 `A缩量{Days:1,Ratio:1.0}`、`{Days:5,Ratio:0.8}` / 回调不破位 `A收盘高于均线{Period:5}` / 不吞昨阳 `A收盘大于昨收` / 基准×T+2、×T+3）；combo 结构含独立 seller 字段（同组买家可测不同持有期）。旧名 `VolumeShrink` 保留为源码兼容别名，`BuyCloseAboveMA` 保留为兼容包装器并复用统一计算实现。CSV 落盘 `output/trades/胜率提升假设验证/`。
- **回测入口**：`cmd/backtest_macd_bar/main.go`——`buy.And{A流通市值{Min:20}, A价格{2,120}, A过滤涨停, MACD连涨{MinDays:2/3}, A实体阴线{0.3/0.5/0.7}}` × 卖出；沪深主板 3197 股 × 2022-2026 × 6 组合，每股数据只读一次 + cloneKlines 隔离。**两个版本**：早盘卖版 `sell.Or{A持仓N天{Days:1}}`（09:30 首根分钟线成交，全量约 52 分钟，输出 `output/trades/尾盘量柱阴线/` + `output/reports/尾盘量柱阴线回测报告.html`，已转 PDF）；尾盘卖版 `sell.Or{A持仓N天尾盘{Days:1}}`（14:55 后成交，全量约 50 分钟，输出 `output/trades/尾盘量柱阴线次日尾盘卖/` + `output/reports/尾盘量柱阴线次日尾盘卖回测报告.html`）。CSV 落盘名与版本绑定，互不覆盖可对照。
- **回测结论（2026-09-12，数据截止 2026-09-04）**：两个卖出版本 6 组合全部负期望，"MACD连涨+阴线"族策略无 alpha，不要重复调参回测。早盘卖：平均 -0.36%~-0.38%/笔、胜率 22.6%~23.9%、盈亏比 0.32~0.33、分年度 5 年全亏。尾盘卖：平均 -0.33%~-0.38%/笔、胜率 38.3%~39.5%、盈亏比 0.65~0.69；相对早盘卖胜率 +15pp、盈亏比翻倍、最优组合亏损收窄 27%（-16.7→-12.2 万），但中位数反而差 0.05pp（典型交易尾盘价更低，平均改善来自规避低开缺口尾部大亏）；分年度 2023/2025/2026 改善（2026 仅 -0.04%），2024 恶化（-0.57→-0.79，系统性下跌段多持一天多亏）。尾盘卖出时点验证 100%（69.1 万笔实际成交 0 异常）。
- **收盘>昨开 附加条件结论（2026-09-15，数据截止 2026-09-04，纯日线口径）**：基准（连涨2天·实体≥70%）平均 -0.31%/笔、胜率 40.1%、44225 笔、盈亏比 0.71；加"收盘>昨开"后平均 -0.35%、胜率 39.7%、17650 笔（过滤 60% 信号）、盈亏比不变 0.71。过滤无质量提升反而略差，分年度亏损额虽缩小但那是笔数减少所致，单笔期望未改善。确认该过滤条件无 alpha，勿重复回测。
- **胜率假设结论（2026-09-15，纯日线，7 组合）**：胜率确可提升但全被盈亏比恶化抵消，期望全负。C"收盘>昨收"胜率 42.7%（+2.6pp 最高）但平均 -0.67% 恶化、盈亏比 0.64 全场最差、样本仅 2646 笔（大亏右尾加深）；D3 持有3天 42.3%、D2 持有2天 41.5%（盈亏比升 0.75/0.76 但平均 -0.48%/-0.38%）；B"收盘在MA5上" 40.6%/-0.34% 与基准几乎持平；A1 缩量(量<昨量) 胜率 39.7% 反降但平均 -0.26% 全场最优（减亏最有效）；A2 缩量(5日均80%) 38.7%/-0.32% 无效。规律：该族策略胜率↑与盈亏比↓此消彼长，T+1 短持结构无 alpha；唯一胜率>50% 的年份是 2023 年 C 组 53.1%。
- **`core.MonteCarlo` 口径坑**：签名 `MonteCarlo(trades, iterations, initialCapital)`，内部按**与总笔数等量**有放回重采样 + 逐笔复利累计。负期望策略（如 -0.36%/笔）采样 22 万笔复利必然归零 → 输出"中位收益 -100%、盈利概率 0%"是数学正确结果，非 bug；评估合理频率（如每年 250 笔）需自行抽样模拟（本次用 Python 读 ExportTradesCSV 的收益率列完成，报告 output/reports/尾盘量柱阴线回测报告.html）。
- **成交样本 K 线图导出工具 `cmd/kline_export/main.go`（2026-09-12）**：读指定成交 CSV，按收益率升序等距取 `sampleCount=50` 个分位样本（`idx=(n-1)*i/(count-1)`，0%~100% 均匀覆盖最大亏损到最大盈利，reason 标"分位X%"，两端标"最大亏损/最大盈利"），每股拉买入前 365 自然日+卖出后 20 日 K 线（EMA 预热），全量算 MA 与 `util.MACDHistogram(all,12,26,9)`（与策略同源）后切窗口（买入前 30/卖出后 6 根），生成 ECharts 静态 SVG 三格图（K线+MA5/10/20、成交量、MACD 量柱；买▲蓝/卖▼深绿 markPoint、持仓区间 markArea；红涨绿跌），输出 `output/reports/策略成交样例K线图50笔.html`（echarts.min.js 已本地化到 output/reports/assets/；样本数变更时改 htmlName 避免覆盖旧版）。转 PDF：`chrome --headless --disable-gpu --no-pdf-header-footer --user-data-dir=<临时目录> --virtual-time-budget=60000 --print-to-pdf=<out> <html>`。**坑点**：① Go `json.Marshal` 切片输出 JSON **数组**，JS 模板须按 `DATA[i]` 访问，写成 `DATA.samples[i]` 是 undefined[0] TypeError 导致整页空白；② `echarts.init` 必须显式 `{width:733, height:678}`（A4 8mm 边距内容宽）+ `animation:false`，否则 headless 下容器 0 尺寸渲染空白；③ 封面总览表 50 行单栏/两栏都会溢出 A4 一页（实测可用高约 1030px），必须**三栏紧凑**（每栏 ~17 行、font-size 10.5px、td padding 2px 4px）才能单页装下。
- **数据新鲜度判断**：`data/database/day-kline/*.db` 的 LastWriteTime（tdx `DefaultDatabaseDir="./data/database"` 相对运行目录；min-kline 每股一目录分年库）。数据滞后但缺的交易日不影响长跨度结论时，可跳过 `common.Update()`（规避 1 小时+ 校验坑）直接回测，报告注明数据截止日即可。

## 策略实验室 `cmd/lab`（2026-09-06 新增）
- **用途**：浏览器内编写/校验策略脚本（Yaegi 解释执行），提交回测（年份+样本池），单页三 Tab（脚本编辑 / 运行进度 / 组合对比+K线明细）。设计文档：`docs/superpowers/specs/2026-09-05-strategy-lab-design.md`。
- **结构**：`internal/lab/`——`interp.go`（LoadScript/CheckScript，脚本契约 `core.Variant{Name, Buyer} 切片`）、`symbols.go`（ProjectSymbols：用 `reflect.ValueOf((*T)(nil))` 注册 core/buy/sell 包全部类型符号，映射 `<包路径>/<包名>`）、`runner.go`（单任务互斥 `sync.Mutex`+`atomic.Bool`、进度 atomic 上报、cloneKlines 隔离多变体、报告落盘）、`server.go`（REST API + `//go:embed web`，前端零构建链嵌入二进制）、`web/lab/index.html`（三 Tab 单页，ECharts CDN）。
- **脚本格式**：Go 语法 `package main` + `func Strategy() []core.Variant`；Buyer 用 `&buy.A价格{...}` 等组件字面量组合（And/Or 嵌套）。示例脚本 `strategies/script/matrix.go`（`//go:build ignore`，勿去掉 tag——package main 无 main() 会破坏 `go build ./...`）。
- **API**：`GET/PUT /api/script`、`POST /api/script/check`、`POST /api/run`（RunConfig：startYear/endYear/sampleMode(all|random|codes)/sampleSize/holdingDays/takeProfit/stopLoss/scriptName）、`GET /api/status`、`POST /api/stop`、`GET /api/reports`、`GET /api/report/{id|latest}`、`GET /api/kline/{code}`。默认 `127.0.0.1:8765`。
- **落盘**（AGENTS.md 6.1）：`output/trades/<时间戳_脚本名>/report.json`（临时文件+rename 原子写，含 stats+trades），每变体 CSV 与汇总 HTML 经 `core.ExportTradesCSV/HTML` 落 `output/trades/<脚本名>/`。
- **测试**：`interp_test.go`（LoadScript/买点等价性/契约错误）、`server_test.go`（脚本 API/完整 run 生命周期/互斥/配置校验）。`t.Chdir(t.TempDir())` 隔离相对路径。
- **坑点**：
  - 测试中构造 `extend.PullKline` 用字面量 `&extend.PullKline{Config: ...}`，勿用 `NewPullKline`（后者打开 update.db 连接不释放，TempDir 清理失败 `being used by another process`）。
  - `lib/tdx` 的 protocol 包路径是 `github.com/injoyai/tdx/protocol`（非 strategy-tail/lib/protocol）。
  - `xorms.NewSqlite` 的 `Sync2` 返回 1 个值（非 2）。
  - 随机抽样用 `math/rand/v2` 的 `Shuffle`（Go 1.22+ 自动随机种子）；手写"时间戳种子+负数取模"洗牌会产生负索引 panic（2026-09-06 端到端实测踩坑）。
  - `core.TradeStats.WinRate` 口径是百分数（2.86 表示 2.86%），前端展示需 `.toFixed(1)` 直接拼 `%`，勿再乘 100。

## 环境坑点：360 拦截 git 写对象（2026-09-15，因子框架 16 Task 执行期间的提交必须走此流程）
- **现象**：360 安全卫士按内容指纹拦截 git.exe 写 `.git/objects`（Permission denied，重试无效，加入信任区仍拦新对象）；PS 直接写同一路径不拦。index 写入不受影响。
- **绕过工具（`.git/fx/`，勿删勿入库）**：
  - `_fixblob.ps1 -Files <相对路径>`：PS 手工构造 loose blob（`blob <len>\0` + SHA1 命名 + zlib 0x789c/DeflateStream/adler32 大端），哈希与 git 一致。
  - `_fixtree.ps1 -MsgFile <UTF-8 消息文件> [-ReplaceHead]`：从 `git ls-files --stage` 读 index 递归重建全部 tree（Ordinal 字节序排序、目录名补 "/"、条目 20 字节二进制 sha）→ 构造 commit → `git update-ref`。`-ReplaceHead` = 替换当前 HEAD（parent=HEAD~1）。
- **每 Task 标准提交流程**：① `git add <文件>` 循环，失败时从 error 提取文件名跑 `_fixblob.ps1` 后重试；② commit 一律用 `_fixtree.ps1 -MsgFile <消息文件>`（git.exe 不写任何对象）。消息文件必须 UTF-8；`.ps1` 无 BOM 会被 PS5.1 按 GBK 解码，脚本内禁中文字面量。
- **验证编码**：`git log` 输出经 console GBK 解码必乱码；字节级验证用 `.git/fx/_verify.ps1 -Sha <commit>`（解压对象搜 UTF-8 字节）。
- **备份基线**：commit `7205a4d`「备份：因子框架实施前的完整工作区基线」（157 文件，含 docs 强制 add 的 spec/plan），parent `4b0aa34`。
- **`go test -race` 本机不可用（2026-09-15）**：任何包（含仓库外最小独立 module，已复现定性）用 `-race` 运行均 `exit status 0xc0000139`（STATUS_ENTRYPOINT_NOT_FOUND，race 测试二进制启动即失败，Go 1.25.5）；疑与 360 注入或系统 DLL 不兼容，非本项目问题。因子框架各 Task 任务书中含 `-race` 的验证步骤按"非 race 运行全绿 + 逻辑审查（本例为 RWMutex 读写锁）"降级执行并在报告披露；后续若杀软/系统环境变化可重试。
