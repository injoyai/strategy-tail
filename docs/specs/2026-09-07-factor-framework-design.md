# 因子框架设计（Factor Framework）

- 日期：2026-09-07
- 状态：已确认（用户逐节评审通过）
- 范围：三期完整交付——因子抽象与因子库、横截面选股、IC/分位收益研究工具，接入 Strategy Lab

## 1. 背景与目标

现有架构是事件驱动的布尔策略体系（`Buyer.Buy() bool` / `Seller.Sell() bool`）：
单标的时序形态触发，无法表达"数值度量"与"横截面排序"。

成熟量化平台（Qlib、Alphalens 等）的因子体系 = 每股每天一个数值 + 横截面比较 +
IC/分位收益等统计检验。本项目引入轻量因子层补齐三个痛点：

1. **指标计算无复用单元**：各 Buyer 内部手写 EMA/MA/动量，口径无一致性保证。
2. **数值过滤表达力弱**：财务/价量数据接进 Buyer 后只能做布尔判断。
3. **缺因子有效性度量**：无法回答"这个指标对 future return 有没有预测力"。

### 已确认的关键决策

| 决策点 | 结论 |
|---|---|
| 使用场景 | 研究 + 过滤为主；不做独立组合轮动回测引擎 |
| 财务因子前视偏差 | 价量因子优先；财务因子仅"最新快照"模式用于实盘过滤，历史 IC 分析明确排除并标注原因 |
| 架构方案 | Go 组件式因子层（方案 A）；不做 qlib 式表达式 DSL（Yaegi 脚本已覆盖自定义逻辑） |
| 概念边界 | Factor 是度量（float64），Buyer 是决策（bool）；因子过滤 Buyer 是桥 |

## 2. 一期：Factor 接口与因子库

### 2.1 Factor 接口（`core/factor.go`，与 Buyer/Seller 平级）

```go
type Factor interface {
    Name() string
    // dks 为截至当日的 K 线序列（引擎保证无前视）
    // 数据不足返回 NaN（与数值 0 区分，下游统一按"无效"处理）
    Value(code string, dks extend.Klines) float64
}
```

### 2.2 因子库（`strategies/factor/`，按族分文件，中文类型名）

参考 Alpha158 族划分，收核心约 25 个因子。参数风格与 `A持仓N天{Days}`
一致（单值 Days；如需扫描区间，调用侧循环，因子本身不引入 MinDays/MaxDays——
这是 Buyer 参数约定，因子是纯函数无状态）。

| 文件 | 族 | 因子 |
|---|---|---|
| momentum.go | 动量 | `N日动量{Days}`、`均线偏离{Days}`、`N日斜率{Days}` |
| volatility.go | 波动 | `N日波动{Days}`、`N日振幅{Days}` |
| volume.go | 量能 | `量比{Days}`、`量分位{Days}`、`放量占比{Days}` |
| kbar.go | K线形态 | `实体幅度`、`上影占比`、`下影占比` |
| position.go | 位置 | `N日高低位{Days}`、`RSV{Days}` |
| corr.go | 量价相关 | `量价相关{Days}` |

注册表 `All() []core.Factor`（各因子默认参数实例）供前端枚举与 Yaegi 脚本引用。
构造走结构体字面量（Yaegi 已支持），不提供反射式 Build。

### 2.3 因子过滤 Buyer（`strategies/buy/factor_filter.go`）

```go
type A因子过滤 struct {
    Factor core.Factor
    Min, Max float64 // 闭区间 [Min, Max]
}
```

- 因子值 NaN → false；否则返回 `Min <= v && v <= Max`。
- 单边开放语义：调用侧用极端值表达（Min=-1e9 / Max=1e9），不引入指针。
- 与 `And`/`Or` 组合子无缝组合；现有 `Do()` 引擎零改动。

## 3. 二期：横截面选股

### 3.1 A因子TopN（`strategies/buy/factor_top.go`）

```go
type A因子TopN struct {
    Factor core.Factor
    N      int  // 每日选前 N
    Asc    bool // false=降序取最大（动量类）；true=升序取最小（小市值类）
}
```

### 3.2 横截面上下文注入（`core/cross_section.go`）

`A因子TopN` 单股视角看不到其他股票，排名由引擎侧喂入：

```go
// 回测入口每日调用：当日样本池按某因子排好序的代码集合
// key = 因子名（Name() 含参数，如 "N日动量(20)"），不同因子的排名互不覆盖
core.SetCrossSection(day time.Time, key string, ranked []string)
```

- `A因子TopN.Buy()` 以自身 `Factor.Name()` 为 key 查当日快照前 N。
- **多变体安全**：回测入口收集全部变体中 TopN Buyer 使用的去重因子集合，
  每日对每个因子各算一次排名；不同因子的快照按 key 隔离，互不覆盖。
  同一因子被多个变体复用时只算一次。
- **不改 `Do()` 主循环**：`backtest_tail` / lab runner 在外层逐日循环调用
  `SetCrossSection`（每股 K 线只读一次的性能模式不变）。
- 停牌/数据不足的票算不出因子值 → 不参与排名 → 自动出局。
- 快照并发安全（`sync.RWMutex` 保护 `map[string]map[time.Time]map[string]int`，
  key=因子名，value=日期→(代码→名次)）。

## 4. 三期：IC 与分位收益分析

### 4.1 分析引擎（`internal/lab/analysis.go`）

数据流：样本池 × 日期区间 → 逐股逐日因子值矩阵（每股 K 线只读一次，
内存循环，复用 lab runner 性能模式）→ 与未来 N 日收益对齐。

指标：

- Pearson IC、Spearman Rank IC（逐日横截面；当日因子值 NaN 的股票剔除）
- IC 均值 / 标准差 / IR（IC 均值÷IC 标准差）/ IC 正率 / t 检验
- 分位收益：每日按因子值分 5 组（Q1=因子值最低 20%，Q5=最高 20%），
  输出各组平均未来收益、多空对冲（Q5−Q1，即高因子组减低因子组）
- 未来收益窗口 N 可配（默认 1/5/20 日三档）；窗口末不足数据的尾部日期剔除

财务因子因 tdx 快照限制不进入历史 IC 分析（前视偏差），UI 中明确标注。

### 4.2 Lab 前端集成

- 新增"因子研究" Tab：选因子（下拉，来自 `All()`）+ 参数 + 年份区间 +
  未来收益窗口 → 提交分析 → 结果页。
- ECharts：IC 时序图（逐日 IC 柱 + 累计 IC 曲线）、分位收益柱状图、
  IC 分布直方图。
- 与现有回测 Tab 并列，共用样本池选择（all/random/codes）。

### 4.3 落盘（沿用 trades_export 模式）

`output/factor/<因子名>/ic_<年份区间>.csv`（逐日 IC 明细）+ `report.html`
（汇总图表）。分析任务与回测任务共用单任务互斥锁。

## 5. Yaegi 脚本集成

`internal/lab/symbols.go` 的 `ProjectSymbols()` 补注册 `strategies/factor` 包，
脚本内直接组合：

```go
&buy.And{
    &factor.N日动量{Days: 20},
    &buy.A因子过滤{Factor: &factor.量比{Days: 5}, Min: 2, Max: 10},
}
```

## 6. 交付边界

**做**：Factor 接口、25 个价量因子、`A因子过滤`、`A因子TopN` + 横截面注入、
IC/分位收益引擎、Lab 因子研究 Tab、落盘、测试。

**不做**：表达式 DSL、组合轮动回测引擎（仓位管理/定期调仓/成本模型）、
财务因子历史 IC、因子加权组合优化、因子值持久化存储（全内存实时计算）。

## 7. 测试策略

- 因子库：每因子 2 组用例——构造数据手算期望值；数据不足返回 NaN。
- `A因子过滤`：区间边界 / NaN / And-Or 组合。
- `A因子TopN`：3 只票构造因子值，验证排名选择、Asc 方向、数据不足剔除、
  双因子快照按 key 隔离互不覆盖。
- IC 引擎：完全正相关数据 IC=1、随机数据 IC≈0、分组单调性验证数学正确性。

## 8. 2026-09-17 扩展：多类型研究数据与上下文因子

原设计第 1.1 节基于 TDX 只有当前财务快照的限制，禁止财务因子进入历史 IC。该限制继续适用于没有发布日期、修订历史和可信可得时间的数据，但不再禁止具备严格时点证据的财务、基本面、公告及替代数据。

### 8.1 数据契约

`researchdata.Provider` 声明数据目录并拉取规范化记录。供应商原始字段必须在适配器中转换成稳定的 Dataset ID 与字段定义，因子不得直接依赖供应商字段名。

每条 `researchdata.Record` 必须包含：

- `Key`：同一业务事实跨修订稳定的键；
- `EventAt`：财报期末、公告事件或指标所属时间；
- `AvailableAt`：该修订最早可被策略获知的时间；
- `Source`、`Revision`：来源和修订追溯；
- `Values`、`Attributes`：数值字段和分类属性。

PIT 查询同时要求 `EventAt <= AsOf` 和 `AvailableAt <= AsOf`。同一 `Key` 多个修订只返回查询时点已可见的最新修订。缺少 `AvailableAt` 的记录 fail closed。

### 8.2 因子兼容层

现有 K 线因子继续实现 `core.Factor`。多类型数据因子实现：

```go
type ContextFactor interface {
    Name() string
    ValueAt(FactorContext) float64
}
```

`FactorContext` 提供 `Code`、`AsOf`、K 线前缀和 `researchdata.View`。`core.Contextual` 把旧因子接入上下文研究链；`core.BindContextFactor` 把上下文因子接回现有 Buyer、TopN 和回测契约。数据缺失仍统一返回 `math.NaN()`。

通用实现包括：`最新字段`、`字段变化率`、`事件计数`。大规模全市场历史应实现持久化 `researchdata.View`；内存 Store 仅用于有界研究和测试。

### 8.3 严格边界

- 当前快照、缺少发布日期或修订历史的数据只能用于当前截面，不得用于严格 PIT 历史 IC/回测。
- 日线 K 线时间作为保守 `AsOf`；盘中公告因子必须由研究任务提供明确决策时间，不能自行假设收盘前可见。
- 数据源、数据集版本、覆盖率和 PIT 质量必须进入后续研究报告元数据。
- 端到端：Lab API 触发一次真实分析，验证落盘产物。
