# 设计：移除 RiskConfig，卖出逻辑统一由 Seller 组合

## 背景与问题

回测引擎存在两套并行的"卖出/风控"机制，职责重复：

1. **Seller 接口**（可组合、策略化）：已有 `sell.A止盈止损`、`sell.A持仓N天`、`sell.Or` 组合器。AGENT.md 也要求"组合策略使用 `sell.Or`，不要自己写遍历"。
2. **RiskConfig 层**（引擎内硬编码）：`core.RiskConfig` 用 `if` 在 `Backtest.Do()` 的 `checkRiskAndSell` 里单独判断止损/止盈/持仓天数/追踪止损——即用户指出的"代码中判断"。

后果：止损/止盈/持仓天数既有 Seller 实现又被引擎重复实现；`TrailingStop` 无 Seller 对应；`main.go` 传 `RiskConfig{}` 被 `applyDefaults` 静默替换为默认值，与"风控关闭"注释矛盾。

## 目标

引擎不再包含任何 per-position 风控判断，所有卖出条件（含风控）由 `Seller` 组合而来。

## 方案

### 1. 引擎层 `core/backtest.go`
- 删除 `Backtest.Risk` 字段、`applyDefaults` 的 risk 分支、`Run()` 的风控日志、`checkRiskAndSell`、`maxProfitRate` 跟踪。
- `Do()` 只剩一条卖出路径：分钟级循环调用 `this.Sell(...)`。顺序：买入信号 → 分钟级卖出（风控 Seller + 策略 Seller 均在此）。
- `executeSell` 保留（分钟循环与期末虚拟平仓共用）。

### 2. 类型层 `core/types.go`
- 删除 `RiskConfig`、`DefaultRiskConfig`、所有 `Has*` 方法。
- `PositionConfig`（MaxPositions/MaxPerCode/SharesPerLot）保留——买入侧仓位管理，不是卖出条件，不属于 Seller，不在本次范围。
- `Cost` 保留。

### 3. 新增 Seller `A追踪止损`（`strategies/sell/sell_trailing_stop.go`）
- 无状态：接收 `(code, dks, buy)`，定位买入日，扫描 `[买入日, 今天]` 的最高收盘价（peak），计算从 peak 的回撤，回撤 ≥ 阈值则触发。
- 比原 `RiskConfig` 实现更精确（原仅日收盘价，现含当前分钟）。
- 配套测试 `sell_trailing_stop_test.go`。
- 现有 `A止盈止损`、`A持仓N天` 已覆盖其余规则，无需改动。

### 4. 配置层
- `config.yaml`：保留 `backtest.risk.*`（stop_loss/take_profit/max_holding_days/trailing_stop），删 `max_drawdown`。
- `common.go`：`LoadBacktestConfig` 移除 `risk` 返回值与读取块（不放在公共 config 里）。
- `cmd/backtest/`：新增配置读取 + 构造（如 `config.go` 的 `loadRiskSeller()`），读 `backtest.risk.*` 构造 `sell.Or{A止盈止损, A持仓N天, A追踪止损}`。
  - 原因：`core` 不能 import `strategies/sell`（循环依赖），故 Seller 构造必须在 `core` 之外。
- `cmd/backtest/main.go`：`Seller: sell.Or{common.MACDSeller, loadRiskSeller()}`。

### 5. 测试与文档
- `core/backtest_test.go`：把用 `RiskConfig` 的用例（止损/止盈/持仓天数）改写为用 `A止盈止损`/`A持仓N天` Seller。
- 新增 `A追踪止损` 测试。
- `AGENT.md` §2、§6.3：写入"风控即 Seller"组合规范（见下）。

## AGENT.md 规范（防止后续 agent 再犯）

- §2 增加一条：风控规则一律实现为 `Seller` 并用 `sell.Or` 组合，禁止在引擎里硬编码 `if`。
- §6.3 替换原 RiskConfig 段为"风控即 Seller"：列出风控规则 → Seller 映射表、组合方式、统一分钟级求值、新增风控规则=新写 Seller 而非扩展引擎。

## 行为变化
- 风控类卖出改为分钟级精度（可盘内触发）——已确认。
- 优先级：`Or` 中风控 Seller 在前、策略 Seller 在后，保留"风控优先保护"。
- T+1：分钟循环已有跳过逻辑，统一适用。
- 顺带修复 `main.go` 的 `RiskConfig{}` 被 `applyDefaults` 静默替换的 bug；重构后显式组合，行为与 config 默认值一致。

## 不在范围
- `PositionConfig.MaxPerCode/MaxPositions` 当前在 `Do()` 未强制执行——买入侧问题，本次不动。

## 涉及文件
- `core/backtest.go`、`core/types.go`、`core/backtest_test.go`
- `strategies/sell/sell_trailing_stop.go`（新）、`sell_trailing_stop_test.go`（新）
- `common.go`、`cmd/backtest/main.go`、`cmd/backtest/config.go`（新）
- `config/config.yaml`
- `AGENT.md`
