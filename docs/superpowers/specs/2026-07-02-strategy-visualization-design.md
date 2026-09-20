# 策略可视化功能设计

## 目标

让用户对策略选出的股票进行可视化验证：输入股票代码和策略名，打开浏览器看到 K 线图，策略识别出的关键点（如 H1/L1/H2/L2）直接标注在图上，同时显示诊断树，一眼判断策略行为是否符合预期。

## 背景

当前项目策略实现 `core.Buyer` 接口（`Buy(code, dks) bool`），只有 `cmd/diagnoser` 输出文字诊断树，无法直观看到策略在 K 线图上识别了哪些点。用户需要一个可视化工具来验证策略行为。

## 架构

```
cmd/visualize (CLI入口)
  ├── 解析参数 (code, strategy, date)
  ├── 拉取日线 (复用 common.Pull.DayKlines)
  ├── 调 Buyer.Buy() 判断命中
  ├── 调 Visualizer.Annotate() 拿标注点 (如策略实现了)
  ├── 调 Diagnoser 拿诊断树
  ├── 数据序列化为 JSON
  ├── 注入 embed 的 HTML 模板
  └── lorca 打开浏览器

core/visualize.go
  ├── Annotation 结构体
  └── Visualizer 接口

strategies/buy/buy_trend_up.go
  └── A底部抬升.Annotate() 实现 (复用 findTrendUpPoints)
```

## 组件设计

### 1. core/visualize.go — 标注数据结构

```go
// Annotation 是K线图上的一个标注点
type Annotation struct {
    Index int       // 在 dks 中的索引
    Time  time.Time // 时间，用于图表X轴对齐
    Price float64   // 标注价格（Y轴位置）
    Label string    // 显示文字，如 "H1"、"买入"
    Color string    // 点颜色，如 "#ef4444"（红）、"#22c55e"（绿）
    Note  string    // 补充说明，悬浮显示，如 "高点 12.50"
}

// Visualizer 策略可选实现，返回要在K线图上标注的关键点。
// 未实现此接口的策略只显示裸K线 + 诊断树。
type Visualizer interface {
    Annotate(code string, dks extend.Klines) []Annotation
}
```

### 2. A底部抬升.Annotate() — 第一个实现

直接复用已抽出的 `findTrendUpPoints(dks, window)`：
- 高点用红色 `#ef4444`，标签 H2（最新）、H1（次新）
- 低点用绿色 `#22c55e`，标签 L2（最新）、L1（次新）
- Note 写明价格和日期，如 "高点 12.50 @ 2026-06-10"

### 3. cmd/visualize — CLI 命令

**用法：**
```
go run ./cmd/visualize -code sz000988 -strategy 顶底抬升
```

**参数：**
- `-code`：股票代码（必填）
- `-strategy`：策略名（必填），对应策略注册表中的 key
- `-date`：可选，截止日期，默认今天

**策略注册表：** `map[string]core.Buyer`，在 main 中初始化。首批注册：
- `"顶底抬升"` → `buy.A底部抬升{}`
- `"MACD"` → `common.MACDBuyer`

**流程：**
1. 解析参数
2. `common.Pull.DayKlines(code, start, end)` 拉近 1 年日线
3. `buyer.Buy(code, dks)` 判断是否命中
4. 类型断言 `buyer.(core.Visualizer)`，如成功则 `Annotate(code, dks)` 拿标注
5. `core.Diagnoser{Buyer, GetDayKlines}.Check(code, date)` 拿诊断树
6. 序列化 K 线数据 + 标注 + 诊断结果为 JSON
7. 注入 embed HTML 模板，写临时文件
8. `lorca.Run` 打开浏览器

### 4. 图表方案 — lightweight-charts + embed

- **图表库：** TradingView [lightweight-charts](https://github.com/tradingview/lightweight-charts) v4，专业金融 K 线图，单文件约 40KB
- **离线可用：** JS 文件下载到 `cmd/visualize/assets/`，用 `//go:embed` 打包进二进制
- **HTML 模板：** 也 embed，包含：
  - 左侧：K 线图（蜡烛图）+ 标注点（用 markers API）
  - 右侧：诊断树面板（递归渲染 ✓/✗）
  - 顶部：股票代码、策略名、命中状态
- **数据注入：** Go 端把 JSON 写入 HTML 的 `<script>` 标签内，前端 JS 读取后渲染

### 5. 错误处理

- 策略名不存在：列出可用策略名后退出
- 股票代码无效或无数据：报错退出
- 策略未实现 Visualizer：显示裸 K 线 + 诊断树，顶部提示"该策略未提供标注"

## 文件结构

| 文件 | 职责 |
|------|------|
| `core/visualize.go` | `Annotation` 结构体 + `Visualizer` 接口 |
| `cmd/visualize/main.go` | CLI 入口、参数解析、策略注册表、编排流程 |
| `cmd/visualize/template.go` | embed HTML 模板 + JSON 注入逻辑 |
| `cmd/visualize/assets/lightweight-charts.js` | 图表库（embed） |
| `strategies/buy/buy_trend_up.go` | 新增 `Annotate()` 方法 |

## 测试策略

- `core/visualize.go`：无逻辑，纯接口定义，不需要单独测试
- `A底部抬升.Annotate()`：已有 `findTrendUpPoints` 测试覆盖扫描逻辑，Annotate 只做格式转换，加一个测试验证返回的标注点数量和标签
- `cmd/visualize`：CLI 命令，手动验证为主（需要拉真实数据）

## 不做的事（YAGNI）

- 不做多股票对比
- 不做历史回测可视化（那是 backtest 的职责）
- 不做实时刷新（这是 screen 的职责）
- 不给所有现有策略补 Annotate，只做 A底部抬升，其他策略后续按需实现
