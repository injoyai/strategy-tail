# 因子开发与接入规范

面向新增因子的人工开发者与 agent。先读根目录 `AGENTS.md`、`PROJECT_RULES.md` 及相关记忆，再按本文实现；当前源码和可复现测试是事实依据。本文整理现有接口，不引入新协议。

## 1. 确定交付范围

- **底层数值因子**：仅供已有条件组件复用，放在 `strategies/factor/`，实现接口并补数值测试即可；不会自动出现在研究页。
- **研究目录因子**：还须显式注册、补目录及接入测试，才能进入研究页、因子组合和搭建器的因子选择器。
- 因子只计算数值；阈值比较、AND/OR、买卖时序属于条件层，依赖持仓的止损止盈属于 `Seller`。参见[因子研究与回测搭建](research-and-backtest-concepts.md)。

开发前搜索已有因子和共用计算，复用 `strategies/factor/`、`strategies/util/`、`extend.Klines` 的实现。新增因子通常不需要修改 `Buyer`、`Seller`、回测引擎或公共类型。

## 2. 选择接口

仅依赖历史 K 线时，实现 [`core.Factor`](../../core/factor.go)：

```go
type Factor interface {
    Name() string
    Value(code string, dks extend.Klines) float64
}
```

需要财务、基本面、公告等数据时，实现 [`core.ContextFactor`](../../core/factor_context.go)：

```go
type ContextFactor interface {
    Name() string
    ValueAt(core.FactorContext) float64
}
```

`FactorContext` 提供 `Code`、`AsOf`、`Klines`、`Data researchdata.View`。已有价量因子通过 `core.Contextual` 适配；上下文因子通过 `core.BindContextFactor` 绑定数据后接入旧 `Factor` 调用链。不要复制一份算法分别维护两种接口。

| 目录工厂 | 用途与边界 |
| --- | --- |
| `factor.Build(kind, days)` | 构造价量 `Factor`；仅注册 `NewContext` 的因子返回 `nil` |
| `factor.BuildContext(kind, days)` | 研究链路统一入口；价量因子自动适配为 `ContextFactor` |
| `factor.BuildWithData(kind, days, view)` | 把目录因子绑定到数据视图，返回供条件、TopN 和回测使用的 `Factor` |

三个工厂对未知 `kind` 均返回 `nil`，调用方须检查。工厂中 `days<=0` 使用目录默认值；策略过滤的 `FactorFilterSpec` 要求 `days>0`，提交配置时写入实际窗口，不要依赖工厂的默认值规则放宽 API 校验。

可参考现有实现：[动量](../../strategies/factor/momentum.go)、[MACD 形态](../../strategies/factor/macd_shape.go)、[通用数据因子](../../strategies/factor/data.go)。

## 3. 数值与时点约定

每个新增因子的类型注释必须写明：公式、输出单位、大小值的数值含义、参数与默认值、窗口端点、最少历史长度、缺失或退化数据的处理。不要把未经研究的收益方向写成有效性承诺。

- **确定性与只读**：相同参数和输入得到相同结果；不修改 K 线、内嵌对象或数据视图，不保存跨股票可变状态。不要在求值时联网、读写数据库文件、使用当前时间或随机数；外部数据通过注入的 `View` 读取。研究任务会并发复用同一因子实例。
- **无前视**：`dks` 最后一根是判断当日，只能读取这个历史前缀；不得自行拉取完整未来序列。未来收益标签由研究流程单独计算。
- **窗口明确**：区分 N 根 K 线与 N 个收益间隔，写明含不含当日。现有 `N日动量{Days:N}` 需要 N+1 根，`均线偏离{Days:N}` 需要 N 根。价量窗口按交易日索引；事件因子的自然日窗口须明确标注。EMA 等额外预热期也要说明。
- **缺失值**：数据不足、必要字段缺失或计算未定义时返回 `math.NaN()`，不要用 0 代替，也不要输出无穷值。真实计数为 0 是合法数值。已有因子的特殊约定（如一字线影线占比为 0）须保留，修改需走版本变更。
- **单位明确**：比值用原始比例，例如 5% 返回 `0.05`；价格用 `protocol.Price.Float64()` 转为元。现有底层因子还包括成交量、金额、百分数和计数，不能一律按比值解释。调用方负责按定义比较，不能随意改缩放。
- **名称稳定**：`Name()` 应稳定且能区分影响数值的参数；窗口因子沿用 `名称(N)` 格式。横截面快照使用因子名及排序方向隔离，避免不同定义同名。

研究流程会按股票和交易日反复对历史前缀求值；对每次扫描全部历史或其他高开销实现，应在交付中说明复杂度与适用样本规模，先做小样本验证。

规则层的历史引用、MACD 和窗口操作数使用不可变参数对象，诊断名称按需生成。已取得的标量值可经 `rule.EvalCompare` 复用统一比较语义，避免为单次比较分配规则和 MACD 作用域；不要在门面复制公式或另写比较边界。MACD 窗口可一次取得当前 `Input` 作用域内的只读序列，按可见历史范围取值。该序列仅在本次求值内复用，不可跨分钟或跨股票缓存；参数切换须分配独立序列，不能覆写并发窗口可能仍在读取的旧缓冲。

外部数据必须通过 `input.Data.Latest/Range` 并传入 `input.AsOf`。底层 [`researchdata`](../../researchdata/contract.go) 要求稳定 Dataset ID、字段定义、来源和版本，记录提供 `Key`、`EventAt`、`AvailableAt`；历史读取同时按业务时间和可得时间截断，同一事实按可见修订去重。缺数据视图或必需输入时返回 `NaN`。

不要把最新快照回填历史，不要用报告期末代替发布日期。`AsOf` 来自调用方的判断时间，日内可见性须精确到时间与时区；数据源没有可信修订历史时，不能声称已经满足严格 PIT。参见 [README 的历史估值数据说明](../../README.md#历史估值数据)。

## 4. 注册研究目录

在 [`strategies/factor/registry.go`](../../strategies/factor/registry.go) 的显式 `registry` 列表尾部追加一项，保留已有项的顺序和 `Kind`。

| 字段 | 要求 |
| --- | --- |
| `Kind` | 唯一、稳定的小写下划线标识；保存的策略和研究记录依赖它 |
| `New` / `NewContext` | 按所选接口提供一个构造函数；上下文因子优先复用 `最新字段`、`字段变化率` 或 `事件计数` |
| `Default` | 正整数，目录 `DefaultDays` 与工厂默认值的共同来源；实现零值的默认语义须保持一致 |
| `Description` | 简洁说明公式或数值语义 |
| `Category` | 沿用已有分类：趋势与动量、波动、量能、规模与流动性、K线形态、位置、相关性、估值 |
| `ParameterLabel` | 说明 `days` 的真实含义；无参数因子写明“无参数”，此时 `Default:1`，构造函数忽略 `days` |
| `Unit` | 当前目录接受 `ratio`、`multiple`、`score`、`correlation`、`percent`；`percent` 的原始值 3 表示 3%，不再乘 100 |
| `Example` | 用具体数字解释原始值；不提供阈值推荐、评级或经验收益 |
| `ImplementationVersion` | 新因子从 1 开始，禁止省略或使用 0 |

目录 `Name` 由默认构造实例的 `Name()` 派生。窗口名称末尾的 `(N)` 须与 `Default` 一致；前端按 `ParameterLabel` 是否以“无参数”开头决定隐藏窗口输入，并从默认名称去除 `(N)` 得到选择器名称，接入后要核对展示。

注册后，`GET /api/factors` 和通用选择器读取同一目录，无须再硬编码一份前端列表。旧 Yaegi 脚本已暴露 `factor.Build`，可用它创建新注册的价量因子；只有确实需要脚本直接使用导出类型字面量时才更新 [`symbols.go`](../../internal/lab/script/symbols.go)。汉字开头的类型在 Go 中不导出；上下文工厂和数据视图目前没有通过该符号表暴露，不能把目录注册等同于上下文因子的脚本接入。

当前通用目录工厂只支持一个整数 `days`，没有任意参数字典。多参数实现可先作为底层因子由 Go 调用方明确构造；需要在研究界面配置多个参数时，先说明保存契约、校验、界面与兼容性的影响，再单独设计。输出单位不在现有五类内或需要新分类时，也须同步检查目录测试与前端格式化，不能用错误单位凑合注册。

## 5. 版本与兼容性

- 同一 `Kind` 下，公式、缩放、默认参数、窗口、预热或缺失值语义改变，须递增 `ImplementationVersion` 并检查已有候选、策略及研究快照。仅调整说明文字不改变数值时，无须递增。
- 完全不同的特征使用新 `Kind`；不要复用旧标识承载新含义，也不要为注册新因子修改已有项。
- 新策略使用目录当前正版本。`FactorFilterSpec` 的版本 0 是旧请求兼容路径，按当前实现运行；正版本不匹配会被拒绝，不应改成 0 绕过检查。
- 因子窗口参数描述计算范围；条件层的 `MinDays/MaxDays` 描述筛选范围。两者职责不同，不要把原条件的时序与范围判断塞入数值因子。

## 6. 测试与验收

按项目要求先写可重复的测试，再实现新因子。使用合成数据和临时目录，不依赖联网、真实行情更新或全市场回测。

1. 在同包 `*_test.go` 验证可手算数值、单位、默认及显式参数；覆盖空输入、刚好不足与刚好足够的历史长度，以及公式涉及的零分母、平价、零量或缺字段边界。可复用 [`momentum_test.go`](../../strategies/factor/momentum_test.go) 的 `mk`、`closes`、`wantNaN`、`wantVal`。
2. 按逐日历史前缀求值，并检查输入对象未被修改、重复求值一致；不要仅用实现自身计算期望值。上下文因子还须验证发布前不可见、发布时可见、未来修订不影响过去，参考 [`data_test.go`](../../strategies/factor/data_test.go) 和 [`store_test.go`](../../researchdata/store_test.go)。
3. 加入研究目录时，追加 [`registry_test.go`](../../strategies/factor/registry_test.go) 的 `wantKinds`；核对目录唯一性、顺序、元数据、默认名称、版本及相应工厂。同步更新 [`factor_e2e_test.go`](../../internal/lab/factor_e2e_test.go) 中目录数量断言，不删除既有检查。
4. 为新增目录因子补最小接入测试，确认目录 API、研究配置和 `FactorFilterSpec` 能接受它，且搭建器保存后能恢复参数及版本并正确求值。参考 [`macd_shape_factor_test.go`](../../internal/lab/macd_shape_factor_test.go)。价量因子核对 `Build` 与 `BuildContext` 数值一致；上下文因子核对 `BuildWithData` 的绑定行为。
5. 注册后检查桌面和窄屏的选择器、参数标签与单位展示；单纯追加现有元数据支持的因子不需要重写界面。

最小检查（在支持本项目的 Go 1.25.4+ 环境执行）：

```sh
go test ./strategies/factor
go test ./core -run 'Test(Contextual|BindContextFactor|DayOf)'
go test ./internal/lab -run 'Test(ServerFactorsAPI|ServerAnalysisAPI)'
git diff --check
```

此外运行新增接入测试；上下文因子运行 `go test ./researchdata`。修改共享算法或公共契约时，按项目要求扩大到 `go test ./...` 和相应买卖条件回归；格式化本次修改的 Go 文件。本机 macOS 的依赖 `goutil/oss` 缺少 darwin 实现，相关测试应在项目现有 Linux/Docker 环境执行，不能仅做交叉编译就报告测试通过。

交付时列明：因子定义、文件与目录项、版本、测试命令及结果、数据覆盖或性能限制。没有真实研究证据时，不宣称因子有效。

## 7. 多 agent 协作

分配任务时先明确每个因子的 `Kind`、公式与单位、参数和默认值、预热及边界语义、底层复用还是研究目录接入，以及负责的文件。以下可作为任务交付清单：

- 独立因子实现及对应数值、边界、时点测试。
- 研究目录的完整条目、版本与接入测试；仅底层复用时说明不注册。
- 实际执行的检查及结果，以及数据或性能限制。

各 agent 优先修改自己的实现与测试文件；共享的 `registry.go`、`registry_test.go`、API 数量断言及文档由指定集成人统一合并。集成人基于合并后的目录追加新项并运行检查，不覆盖他人的未提交改动，不让各分支独立计算的目录数量直接互相覆盖。单个开发者完成任务时自行承担集成工作。

不要为每个因子各建一套注册机制、前端列表或持久化层，也不要顺手修改公共接口、清理无关代码。
