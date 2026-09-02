# MEMORY.md — 项目核心记忆

## 项目概况
Go 股票策略回测系统（A 股，tdx 数据源）。组合式策略组件（`strategies/buy|sell` 的 And/Or 组合子）→ `core.Backtest` 回测引擎 → `cmd/*` 各回测入口 → `lib/extend` 从 tdx 拉取 K 线并按"每股一个 sqlite db"落盘（`data/database/day-kline/`、`min-kline/`）。

## 架构与关键决策
- **回测引擎 `core.Backtest`**：`Run()` 中 Cost 零值自动回退 `DefaultCost()`（防止零佣金零滑点失真）；`MCIterations` 字段接入 `config.yaml` 的 `backtest.monte_carlo_iterations`（<=0 默认 1000）。只有 `cmd/backtest`、`cmd/backtest_macd`、`cmd/backtest_mc` 调用 `Run()`；`backtest_macd_smooth/green`、`market-regime`、`index_filter` 系列自行复现 `_backtest` 循环。
- **`Do()` 性能优化（2026-09）**：逐日 `joinKlines` O(n²) 复制改为一次性 `full = his + dks` 缓冲 + 前缀切片 `full[:len(his)+i+1]`。切片元素为指针，与原版语义完全一致；`today.Kline` 的分钟级覆写通过共享指针实时可见，是"原版行为，不可更改"。**等价性已由差分测试锁定**：`core/backtest_equiv_test.go` 保存原版 `DoLegacy`（自 git HEAD 逐行复制），同一随机数据（固定种子、深拷贝隔离输入）跑新旧两版逐笔 `reflect.DeepEqual` 比对；今后改 `Do` 必须保持该测试通过。
- **`common.go` init 副作用已移除**：import 不再自动 `Pull.Update`；数据更新须显式 `common.Update()`（各 cmd 入口已补齐，`backtest_mc`/`market-regime` 原先依赖隐式更新）。
- **PDF 报告公共包 `lib/report`**：`ReportData` + `Options{OutputDir, Filename, StrategyDesc, Advice}`；`backtest_macd_smooth` 与 `backtest_macd_green` 的 report_pdf.go 重复实现已抽取至此，cmd 侧仅保留策略文案。`market-regime/report_pdf.go` 结构差异大，刻意不合并。依赖 `C:\Windows\Fonts\simhei.ttf`。
- **HTML 模板拆分**：`core/analyze_html.go`（交易可视化）、`core/forward_return_html.go`（未来收益报告）自各自的统计逻辑文件拆出，纯机械搬移。

## 约定与坑点
- 仓库 Go 源文件为 **LF** 行结尾；Windows PowerShell `Set-Content` 会写 CRLF（PS5.1 还加 BOM），批量改文件后需转 LF 并跑 `gofmt -l` 核对。
- `gofmt -l` 存在历史遗留未格式化文件（common.go、market-regime/* 等，对齐类问题），按最小改动原则未全仓库格式化。
- 大量 `gofmt`/测试验证基线：`go build ./...`、`go test ./...` 全绿（strategies/buy、strategies/sell、core）。
- 根目录 exe 产物已移至 `bin/`（.gitignore 已含 `*bin`、`*.exe`）；.gitignore 移除了 `.*` 通配（避免新点文件被静默忽略），显式忽略 `.trae-html-share-packages/`。
- `IsTradingTime` 上午边界修正为 11:30（原代码 11:31 与注释不符）。
- `core/forward_return.go` 的 `DefaultForwardDays` 含 90 天（HEAD 已提交），测试期望已同步。
- `cmd/backtest/main.go` 保留死代码（TestBuy/TestSell/years 覆盖等），用户故意留作快速改参数的草稿区，勿清理。
- 用户偏好：cmd/backtest/main.go 是参数调试入口，编辑时避开用户正在修改的区域。
