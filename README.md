# strategy-tail

本项目是面向 A 股的本地策略研究与回测工具。当前主线是组合式 `Buyer` / `Seller` 策略、历史回测、因子研究、结果分析和策略实验室。本项目不是实盘交易系统。

## 当前架构

```text
TDX / SQLite K 线            财务 / 基本面 / 公告 / 其他供应商
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
| `lib/extend/` | TDX K 线读取与本地 SQLite 存储适配 |
| `researchdata/` | 供应商无关的数据目录、PIT 记录、注册路由和只读视图 |
| `internal/researchrun/` | 多变体回测的数据加载、并发、取消、隔离和覆盖率 |
| `internal/lab/` | 本地 Web 策略实验室及 Yaegi 脚本执行 |
| `cmd/` | 可执行入口；只应定义具体任务和展示，不再复制公共回测循环 |
| `config/` | 数据更新、成本、仓位、默认年份和基准配置 |
| `output/` | CSV、HTML、PDF 等生成产物，不进入 Git |

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

`core.Backtest.Do()` 为兼容历史行为，会在分钟级卖出检查中覆写当日 K 线指针。多组合或重复运行同一份数据时，不要直接调用 `Do()` 并共享输入；使用 `internal/researchrun.Run()`，由它统一完成深拷贝隔离。

## 推荐入口

| 目的 | 命令 | 说明 |
| --- | --- | --- |
| 更新本地行情 | `go run ./cmd/update` | 可能触发全市场校验或联网更新，耗时较长 |
| 浏览器策略实验 | `go run ./cmd/lab` | 默认监听 `127.0.0.1:8765`，端口占用时自动选择空闲端口；也可用 Docker 部署（见下） |
| 主回测草稿 | `go run ./cmd/backtest` | 作者用于快速调参的工作区，不做顺手重构 |
| 实时筛选服务 | `go run ./cmd/screen` | 使用 `config/config.yaml` 中的服务配置 |
| 单次实时筛选 | `go run ./cmd/screen-realtime` | 命令行输出匹配结果 |
| 单股条件诊断 | `go run ./cmd/diagnoser` | 检查代码中配置的 Buyer 是否命中 |
| 历史筛选页面 | `go run ./cmd/screen-kline` | 启动指定日期的本地筛选页面 |
| 前向收益研究 | `go run ./cmd/future` | 运行当前代码中配置的前向收益分析 |

### Docker 部署策略实验室

策略实验室除 `go run ./cmd/lab` 本地直跑外，可通过 Docker 部署（`deploy.ps1`，需 Docker Desktop 已安装且 `docker` 在 PATH 中）：

```powershell
.\deploy.ps1
```

- 脚本流程：构建 `strategy-lab` 镜像 → 移除旧容器 → 启动新容器 → 容器内自检 `/api/factors`，就绪后输出访问地址（默认 `http://localhost:8765`）。
- 默认仅绑定 `127.0.0.1`；需要局域网访问时使用 `.\deploy.ps1 -Bind 0.0.0.0`（服务无鉴权，自行评估风险）。
- `./data`、`./output`、`./strategies/script` 与 `./config`（只读）通过卷挂载进容器，容器重建后数据不丢。

以下目录是带固定研究假设的实验入口，不是通用产品命令：

- `cmd/market-regime`
- `cmd/future`

运行这些命令前必须先阅读文件头部，确认年份、数据截止日、是否调用 `common.Update()`、日线/分钟线成交口径和输出目录。不要把某个实验入口的硬编码参数提升为项目默认值。

## 多变体研究执行

新增矩阵实验应将策略差异声明为 `researchrun.Variant`，然后调用 `researchrun.Run()`；调用方继续负责结果排序、业务文案和具体报告。

`cmd/market-regime` 与 `internal/lab` 已使用该执行层。跨年入口按年度分别调用，保持“单年缺数只排除该代码当年”的历史样本口径；大盘状态报告同时披露按股票×年份统计的数据覆盖。

执行报告始终包含数据覆盖：

- `Requested`：请求的股票数；
- `Completed`：所有请求年份均成功加载并完成计算的股票数；
- `Skipped`：因日线、分钟线或目标年份数据缺失而整股排除的数量；
- `Failures`：排除代码、年份、阶段和原因。

当前兼容规则是：任一请求年份加载失败，就从所有变体中排除该股票。报告结论必须同时披露覆盖率，不能只展示交易指标。

## 配置与数据

`common.LoadBacktestConfig()` 从 `config/config.yaml` 读取成本、仓位、默认年份、基准和蒙特卡洛次数。具体实验可以显式覆盖年份或卖出规则，但必须在入口注释和报告中披露。

回测统计默认使用扣除滑点、佣金、印花税后的净收益：完整交易按
`(SellIncome-BuyCost)/BuyCost` 计算；只有缺少成本字段的历史记录才兼容回退到
原始买卖价格收益。胜率、盈亏比、资金曲线、风险指标和报告明细使用同一口径。

`core.Analyze()` 是纯计算入口，不会写入磁盘。标准 `Backtest.Run()` 会显式导出
`output/backtest/<year>.csv` 和汇总 `trades.html`；GridSearch、Walk-Forward
等分析流程只消费指标，不再在循环中覆盖回测报告。

运行时根目录默认从当前目录向上查找最近的 `go.mod`，因此从
`internal/lab` 等子目录运行测试时，TDX 配置与数据库仍固定解析到项目根目录，
不会在源码包内生成 `data/`。构建后的程序若从仓库外启动，可显式设置
`STRATEGY_TAIL_ROOT`；`pull.database` 使用绝对路径时保持不变，可将大型行情库
放在仓库之外。

命令自身的运行文件也遵循同一边界：`cmd/screen` 的交易数据库和本地 Web
资源、`cmd/market-regime` 的 HTML/PDF 报告都按运行时根目录解析，从命令子目录
启动不会在源码目录中生成额外的 `data/` 或 `output/`。

导入根包不会打开数据库。所有 `cmd/*` 可执行入口在 `main()` 开始时调用
`common.MustInitialize()`；把根包作为库使用时，应先调用可返回错误的
`common.Initialize()`。`common.Update()` 会兜底初始化后再执行显式行情更新。

数据更新已经是显式操作。仅 `common.Update()` 或主动调用它的命令会更新行情；导入根包不会自动更新。部分历史实验为了复现实验数据截止日会刻意跳过更新。

非 K 线研究数据通过 `researchdata.Provider` 暴露目录和拉取能力，通过
`researchdata.Store`（或其他 `researchdata.View` 实现）提供时点查询。每条记录必须同时记录：

- `EventAt`：财报期末、公告事件或指标所属时间；
- `AvailableAt`：策略当时最早能够看到该记录的时间；
- `Key`：跨修订稳定的业务主键；
- `Source` 与数据集 `Version`：供应商和标准化口径追溯信息。

历史查询同时要求 `EventAt <= AsOf` 与 `AvailableAt <= AsOf`；缺少
`AvailableAt` 的记录会被拒绝，修订数据按查询时点选择当时最新版本，不能用当前快照回填历史。

所有数据库、构建产物和报告均为本地运行状态，不是源码事实来源。正式判断顺序是：当前代码与测试、配置、正式文档、`MEMORY.md`、历史实验记录。

## 因子框架状态

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

新增真实供应商时，应先把供应商字段规范化为稳定 Dataset ID/Field，再构造因子；不要让供应商原始字段名进入策略。`researchdata.Store` 是有界研究和测试实现，全市场长期历史可换成数据库实现，只要保持 `researchdata.View` 契约。
默认 Lab Runner 使用 `common.ResearchData`；运行时初始化后它指向本地 SQLite `View`，测试或有界任务仍可注入内存 `Hub`。供应商适配器注册并加载数据后，无需修改 Runner 即可把同一 PIT 视图交给上下文因子。

### 历史估值数据

`cmd/valuation-sync` 从东方财富估值分析公开历史接口同步 A 股日频估值，规范化为
`valuation.daily` 并写入 `research.valuation.database`（默认
`data/research/valuation.db`）。Lab 启动时只读取本地库，不会隐式联网更新。

```powershell
# 先用少量股票验证；代码可写 sh600519、600519.SH 或裸 6 位代码
go run ./cmd/valuation-sync -codes sh600519,sz000001 -start 2020-01-01

# 明确要求后才同步当前本地股票列表，避免误触发全市场长任务
go run ./cmd/valuation-sync -all -start 2020-01-01 -workers 4
```

当前注册的历史估值因子包括 `pe_ttm`、`pe_static`、`pb_mrq`、`ps_ttm`、
`pcf_ocf_ttm` 和 `peg`；同一数据集还保留收盘价、总/流通市值及总/流通股本。
供应商记录的交易日按当日 15:00 视为可见，因此适用于“收盘信号、下一交易日执行”
研究。由于公开历史接口不提供原始修订日志，记录会标记
`pit_status=unverified_revision_history`，严格 PIT 验证不得把它提升为已验证证据。

## 已知边界

- `core.Stats()` 当前按 `BuyPrice/SellPrice` 计算收益率，`Trade.Profit()` 按 `BuyCost/SellIncome` 计算实际成本收益率；启用完整费用后两者可能不同。改变该口径会影响历史报告，需要单独决策和迁移验证。
- `PositionConfig.MaxPositions` 已配置但当前回测只实际约束 `MaxPerCode`，不要把报告解读为已执行账户级全局仓位上限。
- `AuditLookAhead()` 是成交价与 K 线一致性检查，不等于完整的严格时点可见性证明。
- 上下文因子的 PIT 正确性依赖供应商提供可信的 `AvailableAt`；只有当前快照、没有发布日期或修订历史的数据不能用于严格历史 IC/回测。
- 本地 TDX 数据覆盖和更新时间必须随实验结果披露。

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
