# 命中点 K 线可视化功能设计

> 日期: 2026-07-11
> 改造目标: `cmd/forward` → `cmd/future`

## 概述

将 `cmd/forward` 目录更名为 `cmd/future`，在保留原有"未来 N 天收益统计"功能的基础上，新增**命中点 K 线可视化**功能。用户选择某个策略后，扫描过去 N 个交易日（默认 60）的命中点，每个命中点在网页上以独立卡片展示前 N 天 + 命中日 + 后 N 天的 K 线图，并支持多维度筛选。

## 用户需求

- 查看某策略（如通达信倍量）在一段历史时间内的所有命中点
- 每个命中点展示 K 线图：前 N 天 + 命中日（高亮）+ 后 N 天
- K 线图叠加成交量、MACD 技术指标
- 显示买入标注（可选卖出标注）
- 保留命中后未来收益统计
- 支持按股票代码 / 收益率范围 / 时间范围 / 年份筛选命中点

## 改动范围

### 文件变更

| 操作 | 文件 | 说明 |
|------|------|------|
| 重命名 | `cmd/forward/` → `cmd/future/` | 整个目录 |
| 修改 | `cmd/future/main.go` | 新增参数字段，策略可配置 |
| 修改 | `core/forward_return.go` | 扩展结构体、新增 K 线可视化导出 |
> **已确认**: 全项目无任何 import 引用 `cmd/forward`，`TestBuy` 变量在各命令目录中各自重新定义。重命名不影响其他代码。

### 数据结构扩展

#### `ForwardReturnAnalysis` 新增字段

```go
type ForwardReturnAnalysis struct {
    Buyer
    Codes        []string
    Years        []int
    GetDayKlines GetDayKlines
    ForwardDays  []int
    Goroutines   int
    // 新增 ↓
    SingleCode   string // 可选：仅扫描单只股票，为空则全市场
    KlineBefore  int    // 命中点前显示的K线天数，默认 10
    KlineAfter   int    // 命中点后显示的K线天数，默认 10
}
```

#### `ForwardReturn` 新增字段

```go
type ForwardReturn struct {
    Code     string
    BuyTime  time.Time
    BuyPrice protocol.Price
    Returns  map[int]float64
    // 新增 ↓
    KlineIndex int             // 命中日在 dks 中的索引
    HitsBefore extend.Klines   // 命中前 N 天 K 线
    HitsAfter  extend.Klines   // 命中后 N 天 K 线（含命中日本身后的部分）
    CodeName   string          // 股票名称
    Year       int             // 命中年份
}
```

### `Scan` 扩展

`Scan` 方法在碰撞命中点时，额外收集命中日前后 N 天 K 线数据：
```go
if !this.Buy(code, ls) { continue }

// ... 原有逻辑 ...

// 新增：收集命中日前后 K 线
before := this.KlineBefore
after := this.KlineAfter
if before <= 0 { before = 10 }
if after <= 0 { after = 10 }

var hitsBefore, hitsAfter extend.Klines
start := i - before
if start < 0 { start = 0 }
hitsBefore = append(extend.Klines{}, dks[start:i]...)

end := i + after + 1
if end > len(dks) { end = len(dks) }
hitsAfter = append(extend.Klines{}, dks[i+1:end]...)

result = append(result, ForwardReturn{
    Code:       code,
    BuyTime:    today.Time,
    BuyPrice:   buyPrice,
    Returns:    returns,
    KlineIndex: i,
    HitsBefore: hitsBefore,
    HitsAfter:  hitsAfter,
    CodeName:   "", // 后续填充
    Year:       today.Time.Year(),
})
```

### `Run` 扩展

`Run` 方法：
1. 如果 `SingleCode` 非空，只扫描该股票
2. 扫描完毕后，调用新增的 `exportHitVisualHTML` 生成 K 线可视化 HTML

### HTML 报告结构

```
output/future/future_report.html
  ├─ (保留) 汇总统计表
  ├─ (保留) 平均收益率折线图
  ├─ (保留) 胜率折线图
  ├─ (保留) 收益分布直方图
  └─ (新增) 命中点 K 线可视化
      ├─ 筛选工具栏
      │   ├─ 股票代码下拉框
      │   ├─ 年份下拉框
      │   ├─ 收益率范围（最小/最大输入框）
      │   └─ 时间范围（起止日期）
      └─ 命中点卡片列表
          └─ 每张卡片
              ├─ 标题：代码 名称 · 命中日期 · N天收益率
              ├─ K 线主图（前 N + 命中日 + 后 N）
              ├─ 成交量副图
              └─ MACD 副图
```

#### 筛选逻辑（前端 JS）

前端从嵌入的 JSON 中读取所有命中点数据，支持：
- 按代码筛选（下拉框，包含"全部"选项）
- 按年份筛选（下拉框，包含"全部年份"选项）
- 按收益率范围筛选（min/max 输入框，使用命中后 N=5 天的收益率）
- 按日期范围筛选（起止日期选择器）

筛选条件变化时实时重新渲染卡片列表。

#### 单个命中点卡片

```html
<div class="hit-card">
  <div class="hit-header">代码 名称 · 2025-03-15 · 5日收益: +3.2%</div>
  <div id="hit-chart-{index}" style="width:100%;height:320px"></div>
</div>
```

ECharts 配置：
- `grid` 3 行：主 K 线 60%、成交量 15%、MACD 15%
- `xAxis` 3 个共享 category 数据
- `series` 日 K、MA5、MA10、成交量、MACD
- `markPoint` 标注命中日点位
- 命中日 K 线用特殊样式（粗边框或背景色）

#### 性能考虑

如果命中点过多（全市场扫描 5000+ 股票 × 60 天可能产生数百到上千个命中点），前端渲染数百张 ECharts 会卡顿。方案：
- 默认每页显示 20 张卡片，分页加载
- 懒加载：使用 IntersectionObserver，卡片进入视口时才初始化 ECharts

## 运行方式

```bash
# 全市场扫描，默认策略 = 通达信倍量
go run ./cmd/future

# 在 main.go 中可配置：
#   SingleCode: "sz000001"  — 仅扫描指定股票
#   KlineBefore: 10         — 命中前显示 10 天
#   KlineAfter: 10          — 命中后显示 10 天
#   TestBuy: buy.And{...}   — 可配置策略
```

输出文件:
- `output/future/future_report.html` — 完整报告（统计 + K 线可视化）

## 验收标准

1. `go run ./cmd/future` 能正常运行，无编译错误
2. 生成的 HTML 文件能正确展示原有统计图表
3. 命中点 K 线卡片正确显示前后 N 天 K 线，命中日高亮
4. K 线图叠加成交量和 MACD 副图
5. 买入标注正确显示在命中日
6. 四种筛选功能正常工作
7. 指定 `SingleCode` 时仅扫描该股票
8. 原有 `cmd/backtest_mc` 中 `TestBuy` 引用同步更新