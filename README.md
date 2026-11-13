# strategy-tail

本项目是面向 A 股的本地策略研究与回测工具。当前主线是组合式 `Buyer` / `Seller` 策略、历史回测、因子研究、结果分析和策略实验室。本项目不是实盘交易系统。

概念说明见[因子研究与回测搭建](docs/guides/research-and-backtest-concepts.md)，统一术语见[业务词汇表](CONTEXT.md)。完整资料见[文档导航](docs/README.md)，代码入口与运行目录见[项目目录说明](docs/guides/project-layout.md)。

回测条件的底层数值计算复用 `strategies/factor` 中的因子，条件组件保留比较、时序和组合规则；拆分范围与兼容口径见上述概念文档。

## 当前架构

```text
MySQL K 线、代码与股本             MySQL 历史研究数据
        ↓                                  ↓
lib/extend K 线适配             researchdata PIT 数据契约与视图
        └──────────────────────┬───────────┘
                               ↓
                   core.Factor / ContextFactor
        ↓
strategies/factor 数值因子 + strategies/buy + strategies/sell 组合策略
        ↓
core.Backtest / Stats / Analyze
        ↓
internal/researchrun 多变体研究执行
        ↓
cmd/* 命令、internal/lab Web 实验室、output/* 报告
```

目录职责：

| 目录 | 职责 |
| --- | --- |
| `core/` | 稳定的回测、交易、统计、绩效和审计契约 |
| `strategies/buy/` | 实现 `core.Buyer` 的买入条件与组合子 |
| `strategies/sell/` | 实现 `core.Seller` 的卖出和风控条件 |
| `strategies/factor/` | 价量 `core.Factor`、多数据 `core.ContextFactor` 与因子目录 |
| `strategies/util/` | MACD、RSI 等策略共用计算 |
| `lib/extend/` | TDX 协议数据到策略 K 线的转换 |
| `internal/marketstore/` | MySQL 行情与研究数据的只读查询 |
| `researchdata/` | PIT 记录、MySQL 只读视图与内存测试数据 |
| `internal/researchrun/` | 多变体回测的数据加载、并发、取消、隔离和覆盖率 |
| `internal/lab/` | 本地 Web 策略实验室 |
| `internal/lab/builder/` | 搭建器组件目录、条件树与共用因子条件的校验和构建 |
| `internal/lab/factorstats/` | IC/HAC、衰减、分组换手与中性化的纯计算 |
| `cmd/` | 可执行入口；只应定义具体任务和展示，不再复制公共回测循环 |
| `config/` | MySQL 连接、成本和仓位配置 |
| `docs/` | 使用与开发指南、设计、实施计划和审查记录；从 `docs/README.md` 检索 |
| `tests/` | 部署等外围测试；Go 测试与被测包放在一起 |
| `data/` | 选股交易记录与 Lab 持久状态，不进入 Git |
| `output/` | CSV、HTML、PDF 等生成产物，不进入 Git |
| `bin/` | 本地编译产物，不进入 Git |

## 核心契约

买入策略实现：

```go
type Buyer interface {
    Name() string
    Buy(code string, dks extend.Klines) bool
}
```

卖出及风控策略实现：

```go
type Seller interface {
    Name() string
    Sell(code string, dks extend.Klines, buy Buy) bool
}
```

策略通过 `buy.And`、`buy.Or`、`buy.Not` 和 `sell.Or` 组合。止盈、止损、持有期和追踪止损属于 `Seller`，不应硬编码进回测引擎。

`core.Backtest.Do()` 在分钟级卖出检查中使用内部快照，后续交易日的历史使用原始日线，不修改调用方的行情数据。多组合研究可使用 `internal/researchrun.Run()`，由执行层统一加载、隔离数据并统计覆盖。

## 推荐入口

| 目的 | 命令 | 说明 |
| --- | --- | --- |
| 浏览器策略实验 | `go run ./cmd/lab` | 默认监听 `0.0.0.0:8765`，端口占用时自动选择空闲端口；也可用 Docker 部署（见下） |
| 实时筛选服务 | `go run ./cmd/screen` | 使用 `config/config.yaml` 中的服务配置 |

各命令的参数、配置归属和副作用见[命令说明](cmd/README.md)。原 `diagnoser`、`screen-realtime`、`screen-kline`、`future`、`market-regime` 为固定参数的诊断、演示或专项研究命令，已移除；通用的 `core.Diagnoser`、`core.Screen` 和 `core.ForwardReturnAnalysis` 保留，已有数据与报告不迁移。

回测与实验室“全量/随机”样本只枚举 MySQL 中已存主板日线证券，保留退市代码并排除指数。未找到已有日线股票时直接报错。选股使用 MySQL 证券目录和已入库的当日分钟线。

组合研究从 MySQL 的 `universe_members` 读取历史成员。缺表或空表时，使用 MySQL 日线覆盖的股票集合，成员历史标记为 `unverified`，只支持探索级证据。

### Docker 部署策略实验室

策略实验室除 `go run ./cmd/lab` 本地直跑外，可通过 Docker 部署（需已安装 Docker、Python 3，且 `docker` 在 PATH 中）：

macOS / Linux：

```bash
./deploy.sh
```

- `deploy.sh` 在构建前和切换前检查 `/api/status`。回测运行中或状态不可读时停止部署。切换时停止并重命名旧容器为 `strategy-lab-previous-<时间>-<进程号>`，保留原数据挂载。新容器通过组件接口及 `/api/market-data/status` 的 MySQL 读取检查后就绪，启动失败或 30 秒内未就绪时恢复旧容器。旧容器和失败容器保留，不自动清理。部署期间请勿启动新任务。
- `deploy.sh` 默认绑定 `0.0.0.0`，绑定地址可通过 `BIND` 覆盖。
- Docker 默认映射宿主机 `80` 到容器 `8765`，本机访问 `http://localhost/`；可用 `PORT=8765 ./deploy.sh` 指定其他宿主机端口。本地 `go run ./cmd/lab` 仍默认使用 8765。
- `./data`、`./output` 与 `./config`（只读）通过卷挂载进容器，容器重建后数据不丢。

### Lab 策略搭建与行情状态

打开 Lab 默认进入“回测配置 → 自由搭建”，提供买入条件、卖出信号和独立风控，可手动编辑或加载已保存策略。可以切换到因子组合回测，也可以查看含原策略参数的历史报告。

自由搭建与因子组合分别通过 `POST /api/builder/run` 和 `POST /api/strategy/run` 提交。高级脚本、MACD 参数回测和内置模板接口已下线。

组合实验执行期间持有每实验文件锁。启动恢复只将未被执行者持锁的 `running` 记录标记为失败；进程退出后锁由操作系统释放，锁文件保留。升级时应先停止旧版实例：不使用该锁的旧版执行者无法被识别为存活任务。

选择年份和股票范围后点击“运行回测”，完成后自动打开“回测结果”；“本次回测规则”显示报告保存的条件树，“交易明细”查看逐笔成交。成本与单票仓位使用 `config/config.yaml`。

左下角“行情”气泡显示 MySQL 中上证指数最新日线的日期，并区分读取中、暂无记录和读取失败。行情与研究数据由外部项目维护。最新指数日期只代表该指数的日线覆盖。选股的盘中信号取该股票最新入库分钟，更新速度取决于外部项目的写入频率。

### 选股策略

从 Lab 左侧打开「选股策略」。四套 Screen 内置规则可独立勾选，首次使用时默认关闭。自定义策略支持组合现有买入组件和因子条件，按 AND／OR 嵌套并调整参数，也可复制已保存回测策略的买入条件。内置规则可启停，自定义规则可编辑和删除。

保存后自动扫描已启用的策略，也可手动点击「立即选股」。每只股票只显示一行，保留全部命中策略与内置标签。扫描使用 MySQL 已入库的当日分钟线，收盘日线可用时优先使用日线。界面显示行情时间和样本覆盖。启用状态会随配置保存，Lab 重启后会重新扫描。

运行口径、配置文件和接口见[选股指南](docs/guides/stock-screening.md)。

### 自由搭建策略

“回测配置 → 自由搭建”支持买入与卖出信号的 AND／OR 嵌套、同组件多实例，以及独立风控列表。独立风控只接受单条条件，任一成立即可触发卖出，成交仍遵守 T+1。均线周期列表用逗号分隔（如 `5,10,20`），非法项明确报错，不能自动删除后运行。买入和卖出信号可清空；空配置不能直接运行，基准买入或持有至期末须显式选择。

策略只有点击保存才写入 `data/lab/strategies/`；直接回测使用当前草稿。“另存为”保存当前草稿为新身份，沿用输入的名称，不覆盖源策略。版本冲突保留输入；保存期间的新编辑也保留，并提示再次保存。新建或加载其他策略会在丢失未保存修改前提示。

条件和子分组左侧的 ⠿ 手柄可拖动排序，蓝色插入线显示放置位置；也可点击 ↑／↓ 或在手柄上按上下方向键。排序限定在同一分组内，独立风控在自身列表内排序；参数和分组关系保留，点击保存后保留顺序。

条件说明通过问号悬停提示查看，也支持键盘聚焦、触屏点击和 Escape 收起，不占用配置行的额外高度。

编辑区使用通栏布局；顶部将策略名称与策略库放在一起，新建、保存、另存为组成管理工具栏。点击“运行回测”后选择年份与股票范围，再点击“开始回测”提交；因子组合在同一弹窗设置卖出规则。取消不启动任务，已选运行配置保留；提交失败保留选择供重试。

“股票范围”可选“红利参考股（20只）”或名单中的单只股票（显示“代码 名称”），选中后可继续编辑代码。名单为人工研究样本，不表示实时股息率排名或历史指数成员；仅改变回测股票范围，不会自动修改策略、费用、账户参数或计入分红。

“本次回测规则（报告快照）”展示当次完整条件树、参数、独立风控和持有选择，不读取策略库的新版本。参数缺省显示“未设置”，不替换为添加条件时的表单默认值。交易明细区分实际成交、期末估值（Virtual）及旧报告未记录的类型。写入和回测接口校验同源、请求大小、代码格式、组件角色、树规模与参数。

Web 的自由搭建与因子组合回测统一使用账户资金约束：初始资金 10 万元，最多同时持有 5 只，每笔买入预算 2 万元（含费用）。信号按股票代码升序执行，单票只持有一笔；资金不足时减少买入股数，按 100 股取整。同日先执行已有持仓的卖出，再按收盘信号买入，卖出资金可用于当天买入，跨年保留现金与持仓。

对比页展示每日账户累计收益率 `(现金 + 持仓收盘市值) / 初始资金 - 1`，与相同区间上证指数首日收盘买入持有的价格涨幅对比；超额收益以百分点显示。每个变体有独立账户。策略计入既有费用及滑点，指数不扣交易成本；期末 Virtual 记录仅估值，不扣虚构卖出费用。因子组合的“成交数比”只比较资金约束下的实际成交笔数，不代表信号保留率。

账户结果和资金规则随 `report.json` 保存，CSV/HTML 继续导出逐笔成交统计。历史报告没有账户数据时保留原逐笔累计图并提示重新回测；旧 API 请求缺少 `account` 时继续使用原逐笔回测。分钟行情缺失时沿用原日线收盘卖出判断并披露数量；持仓日线缺失时沿用上一收盘价估值并披露数量，指数缺少股票交易日时拒绝生成对比。未新增分红现金流、涨跌停排队和容量模型。

### 回测逐日回放与盘感练习

有完整过程记录的账户报告，在“回测结果”点击“逐日回放”，按交易日查看账户、持仓、收益图、实际成交及命中原因。支持暂停、前后步进、日期滑块与调速；点击持仓或成交会暂停，切换页签也会暂停。回放隐藏当前日期之后的信息，期末未平仓只显示估值；历史报告没有过程记录时须重新回测。行情和原因随本次报告保存，之后更新行情或策略不重新解释旧结果。

“盘感练习”选择已保存策略与年份后随机抽取历史买入机会，只使用策略买入侧。每题预算 2 万元、费用随开题冻结、零滑点、100 股取整，按当前已揭示日的收盘价买卖；买入当天不能卖出，每题只买一次、全部卖出一次。练习中隐藏股票身份与真实日期，卖出后可继续观察，结束后查看复盘或重练，也可点“开始下一题”沿用本题策略与年份随机出新题（采用策略当前保存版本，原题保留）。最后一根行情仍可作出买卖决定，再点击“行情用尽，结束本题”收口；未平仓不虚构卖出。状态保存后才生效，网络中断可用原请求重试，刷新后恢复；存储按单 Lab 实例使用。

练习结束后自动打开本题分析报告，可切换 K 线复盘或下载文本报告。报告展示净盈亏、按买入总成本计算的交易收益率、按整题预算计算的资金回报、实际费用、现金与持仓市值、等待与持有天数、收盘浮盈亏和每日总资金最大回撤；逐日资金明细可展开。未交易不计零收益，未平仓仅估值、不预扣卖出费用；结束后额外复盘行情不计入收益。旧题直接基于冻结快照生成报告，无需重新出题。

出题最多并行扫描 4 只股票，仍扫描完整股票池以保证命中机会等概率抽取；进度栏展示实际扫描数，支持停止，刷新或切页后可继续查看。准备、扫描及保存使用各自阶段，数据量未知时不估算百分比。

详见[回放需求](docs/specs/2026-10-18-backtest-replay-requirements.md)、[盘感需求](docs/specs/2026-10-18-trading-practice-requirements.md)及[审核验收边界](docs/reviews/2026-10-19-replay-practice-acceptance.md)。

隔离回归验证：

```bash
node --test internal/lab/web/*.mjs
python3 tests/deploy_test.py
# macOS / Linux，Go 1.25.4+；同级 ../tdx 必须是配套的 tdx/v2 工作区
TZ=Asia/Shanghai go test ./internal/lab/... ./strategies/buy ./strategies/sell ./internal/researchrun
go build ./...
go vet ./internal/lab/...
```

部署测试使用 Docker 替身，不操作真实容器；Go 测试使用临时行情与报告目录。自动化检查不代替桌面、窄屏和键盘的浏览器验收。


## 多变体研究执行

临时策略验证使用项目已配置的只读行情、独立临时目录和统一报告格式。调用示例见[临时策略回测指南](docs/guides/ad-hoc-backtest.md)。

新增矩阵实验应将策略差异声明为 `researchrun.Variant`，然后调用 `researchrun.Run()`；调用方继续负责结果排序、业务文案和具体报告。

`internal/lab` 使用该执行层。旧逐股回测按年度分别调用，保持“单年缺数只排除该代码当年”的历史样本口径。

执行报告始终包含数据覆盖：

- `Requested`：请求的股票数；
- `Completed`：所有请求年份均成功加载并完成计算的股票数；
- `Skipped`：因日线、分钟线或目标年份数据缺失而整股排除的数量；
- `Failures`：排除代码、年份、阶段和原因。

当前兼容规则是：任一请求年份加载失败，就从所有变体中排除该股票。报告结论必须同时披露覆盖率，不能只展示交易指标。

## 配置与数据

`common.LoadBacktestConfig()` 从 `config/config.yaml` 读取成本和逐股仓位。回测年份和卖出规则由运行请求指定，Lab 账户回测使用上证指数 `sh000001` 作为基准。

公共配置由根包固定从 `RuntimeRoot()/config/config.yaml` 加载，启动目录不会改变配置来源。沿用文件优先、同名环境变量补缺的顺序；Lab 地址仍由 `LAB_ADDR` 控制。配置中已移除无读取方的 `pull.spec`、`screen.goroutines` 和 `backtest.risk.*`：卖出和风控在策略或运行请求中配置。

回测统计默认使用扣除滑点、佣金、印花税后的净收益：完整交易按
`(SellIncome-BuyCost)/BuyCost` 计算；只有缺少成本字段的历史记录才兼容回退到
原始买卖价格收益。胜率、盈亏比、资金曲线、风险指标和报告明细使用同一口径。

`core.Analyze()` 是纯计算入口，不会写入磁盘。标准 `Backtest.Run()` 会显式导出
`output/backtest/<year>.csv` 和汇总 `trades.html`；GridSearch、Walk-Forward
等分析流程只消费指标，不再在循环中覆盖回测报告。

运行时根目录默认从当前目录向上查找最近的 `go.mod`。从仓库外启动构建后的程序时，可显式设置 `STRATEGY_TAIL_ROOT`。Lab 从项目根目录启动，策略库和选股配置等文件保存在当前工作目录的 `data/lab/`。

命令自身的运行文件也遵循同一边界：`cmd/screen` 的交易数据库和本地 Web
资源按运行时根目录解析，从命令子目录启动不会在源码目录中生成额外的
`data/` 或 `output/`。

导入根包只读取配置。临时回测通过 `common.OpenResearchMarket()` 打开只读行情库，
现有应用通过 `common.Initialize()` 或 `common.MustInitialize()` 初始化完整运行时。
行情和研究数据由项目外维护，日常研究使用已入库的数据。

非 K 线研究数据由外部维护方写入 MySQL，通过 `researchdata.View` 提供时点查询。记录包含以下字段。

- `EventAt`：财报期末、公告事件或指标所属时间；
- `AvailableAt`：策略当时最早能够看到该记录的时间；
- `Key`：跨修订稳定的业务主键；
- `Source` 与 `Revision` 用于追溯来源和记录修订。

历史查询同时要求 `EventAt <= AsOf` 与 `AvailableAt <= AsOf`；缺少
`AvailableAt` 的记录会被拒绝，修订数据按查询时点选择当时最新版本，不能用当前快照回填历史。

所有数据库、构建产物和报告均为本地运行状态，不是源码事实来源。正式判断顺序是：当前代码与测试、配置、正式文档、`MEMORY.md`、历史实验记录。

## 因子框架状态

新增因子前阅读[因子开发与接入规范](docs/guides/factor-development.md)，按其中的接口、注册、版本和测试清单交付。

收益偏度 `return_skew` 当前实现版本为 2：将恒定收益的浮点舍入噪声视为无效值；版本 1 的策略条件须重新分析并确认阈值后使用当前版本。

价量因子继续实现兼容接口 `core.Factor`。需要财务、基本面、公告或替代数据的因子实现
`core.ContextFactor`，从 `FactorContext.Data` 按 `FactorContext.AsOf` 查询数据。
`core.Contextual` 把现有价量因子接入新研究链路；`core.BindContextFactor` 把上下文因子接回
现有 Buyer、TopN 和回测契约。因此新增数据类型不需要修改 `Buyer`、`Seller` 或 `Backtest.Do()`。

通用数据因子位于 `strategies/factor/data.go`：

```text
最新字段      财务/基本面/估值等最新可见数值
字段变化率    不同期财务或其他时序字段的变化率
事件计数      指定时间窗内公告/事件数量，可按属性分类过滤
```

外部写入方通过稳定的数据集 ID 和字段名提供研究数据。Lab Runner 从 MySQL 查询记录，测试或有界任务可注入内存 `researchdata.Store`。

### 历史估值数据

`valuation.daily` 来自外部维护方写入的 MySQL `research_records`。注册的估值因子包括 `pe_ttm`、`pe_static`、`pb_mrq`、`ps_ttm`、`pcf_ocf_ttm` 和 `peg`。历史信号只能读取当时可见的记录。表结构与缺数行为见 [MySQL 数据源](docs/guides/mysql-storage.md#研究数据)。

## 已知边界

- `core.Stats()` 对完整成交按 `BuyCost/SellIncome` 计算含费收益率，旧记录缺少成本时才回退原始价格；不会自动剔除虚拟成交，训练汇总调用前只传入真实已平仓交易。
- `PositionConfig.MaxPositions` 已配置但当前回测只实际约束 `MaxPerCode`，不要把报告解读为已执行账户级全局仓位上限。
- `AuditLookAhead()` 是成交价与 K 线一致性检查，不等于完整的严格时点可见性证明。
- 上下文因子的 PIT 正确性依赖供应商提供可信的 `AvailableAt`；只有当前快照、没有发布日期或修订历史的数据不能用于严格历史 IC/回测。
- MySQL 数据覆盖和最新入库日期必须随实验结果披露。

## 开发与验证

开始工作前阅读 `AGENTS.md`、`PROJECT_RULES.md` 和 `MEMORY.md`。其中当前代码、配置和测试优先于历史记忆或本地设计草稿。

常用检查：

```powershell
go test ./...
go vet ./...
$goFiles = rg --files -g '*.go'
gofmt -l $goFiles
git diff --check
```

新增策略应有可重复的 K 线单测；修改 `core.Backtest.Do()` 必须保持 `core/backtest_equiv_test.go` 的新旧引擎差分测试通过。

策略回测、因子分析和组合研究共用后台任务锁，同时只运行一个任务。任务进度在右下角展示。

回测进度依次显示准备数据、加载行情（股票数）、模拟交易（交易日数）和生成报告。百分比标为“本阶段”，阶段切换重新计数。准备和生成报告期间显示不定进度，保存成功后显示完成。逐股回测使用“执行回测”阶段。

因子分析、组合运行和组合验证也使用相同规则。因子股票处理结束后，继续显示 IC 统计、分组收益、年度结果和报告生成等阶段。组合任务分别显示变换、合成、模拟交易、指标、归因与保存。后台完成后若页面仍在下载报告，任务栏显示“读取结果”，读取成功才显示完成。读取失败可单独重试。

盘感练习以题库组织：选择策略和年份先建立固定快照题库，再开始一次训练。训练内“开始下一题”直接复用题库且不重复；“结束本题”保留训练，“结束训练并生成报告”统计本次正式作答。最后一题结束自动生成报告，支持下载与逐题复盘。旧记录在“历史单题 / 重练”中保留，不自动归入训练。详见[题库训练约定](docs/plans/2026-10-19-practice-training.md)。

### 本地 tdx v2 依赖

当前版本通过仓库内 `go.work` 使用同级 `../tdx` 的 v2 源码。运行前确认该目录的模块路径为 `github.com/injoyai/tdx/v2`，并执行 `go list -m github.com/injoyai/tdx/v2` 检查工作区解析。价格与金额使用浮点元值。历史策略均线精度变化见 [tdx v2 迁移记录](docs/guides/tdx-v2-migration.md)。

`./deploy.sh` 会将本地 SDK 作为 Docker 命名构建上下文传入；手动构建使用 `docker build --build-context tdx=../tdx -t strategy-lab .`。

运行前在私有 `config/mysql.yaml` 中配置 `pull.mysql`，或通过同名环境变量注入。缺少配置会报错。应用从同一 MySQL 读取行情、股票池及研究数据，外部项目负责同步。详见 [MySQL 配置](docs/guides/mysql-storage.md#连接配置) 和 [源时间与策略口径](docs/guides/source-time-storage.md)。
