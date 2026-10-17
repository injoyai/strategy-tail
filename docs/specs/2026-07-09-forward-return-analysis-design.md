# 买入信号未来N天收益分析

## 概述

新增一个**独立于 Backtest 引擎**的功能模块,用于评估买入策略信号本身的质量:当策略触发买入信号后,统计未来 N 天的收益率分布。默认 N = {1, 3, 5, 10, 15, 20, 30} 天。

与 Backtest 的区别:不涉及卖出策略、成本模型、仓位管理,只关心"信号触发后股价怎么走"。

## 架构

### 文件结构

```
core/forward_return.go        ← 分析引擎(数据结构 + 算法 + 输出)
cmd/forward/main.go           ← 独立CLI入口
```

### 依赖关系

* `Buyer` 接口 — 信号检测

* `GetDayKlines` — 日线数据拉取

* `extend.Klines` — K线数据类型

* `bar.NewCoroutine` — 并发处理

* **不依赖**: Seller, Cost, PositionConfig, Backtest

## 数据结构

```go
// ForwardReturn 单次买入信号的未来收益记录
type ForwardReturn struct {
    Code     string
    BuyTime  time.Time
    BuyPrice protocol.Price
    Returns  map[int]float64 // N天 -> 收益率%,(dks[i+N].Close - BuyPrice) / BuyPrice * 100
}

// ForwardReturnSummary 按N天汇总的统计
type ForwardReturnSummary struct {
    Days         int
    Count        int     // 有效信号数(有足够未来数据的)
    AvgReturn    float64 // 平均收益率%
    MedianReturn float64 // 中位数收益率%
    WinRate      float64 // 正收益占比%
    MaxReturn    float64 // 最大收益率%
    MinReturn    float64 // 最小收益率%
}

// ForwardReturnAnalysis 独立分析器,与 Backtest 无关
type ForwardReturnAnalysis struct {
    Buyer
    Codes        []string
    Years        []int
    GetDayKlines GetDayKlines
    ForwardDays  []int // 默认 {1,3,5,10,15,20,30}
    Goroutines   int
}
```

## 执行流程

```
ForwardReturnAnalysis.Run()
  ├── 对每个年份 year:
  │     ├── hisStart = year-2年6月1日, start = year年1月1日, end = year年12月31日
  │     └── 并发遍历 Codes (bar.NewCoroutine, Goroutines 控制并发数):
  │           ├── 拉取日线 dks = GetDayKlines(code, hisStart, end)
  │           ├── 分离 his (start之前) 和 dks (start起)
  │           ├── 遍历 dks[i]:
  │           │     ├── 构建 ls = his + dks[:i] + dks[i]  (与 Backtest.Do 一致)
  │           │     ├── 若 Buyer.Buy(code, ls) == true:
  │           │     │     ├── buyPrice = dks[i].Close
  │           │     │     ├── 对每个 N in ForwardDays:
  │           │     │     │     └── 若 i+N < len(dks): returns[N] = (dks[i+N].Close - buyPrice) / buyPrice * 100
  │           │     │     └── 记录 ForwardReturn
  │           │     └── (不检查仓位上限,统计所有信号)
  │           └── 返回该股票的 ForwardReturn 列表
  ├── 汇总所有年份的 ForwardReturn
  ├── PrintForwardReturnSummary() — 控制台打印
  └── ExportForwardReturnHTML() — 生成HTML报告
```

### 信号构建一致性

ls 的构建方式与 `Backtest.Do()` 完全一致:

```go
_his := joinKlines(his, dks[:i]...)
ls := joinKlines(_his, dks[i])
```

确保买入信号检测结果与回测引擎一致。

## 性能设计

| 维度   | 说明                                           |
| ---- | -------------------------------------------- |
| 并发   | 复用 `bar.NewCoroutine`,与 Backtest 相同的并发模型     |
| 数据IO | 每只股票只拉一次日线,与 Backtest 相同                     |
| 计算   | 未来收益通过 `dks[i+N]` 数组索引,O(1) per signal per N |
| 总量   | 假设1000只股票 × 100信号/只 × 7个N值 = 70万次运算,毫秒级      |
| 内存   | 每条 ForwardReturn 约100字节,10万条约10MB            |

### 不做的优化(YAGNI)

* 不缓存 K 线到磁盘

* 不做跨年数据预加载

* 不做增量计算

## 输出

### 控制台

```
买入信号未来N天收益分析: [策略名]

  N天   信号数   平均收益   中位数    胜率     最大收益   最大亏损
    1    1200     0.32%    0.15%   52.3%      9.80%    -8.20%
    3    1198     0.85%    0.42%   54.1%     18.50%   -12.30%
    5    1195     1.23%    0.55%   55.8%     25.10%   -15.40%
   10    1180     2.10%    0.80%   57.2%     42.30%   -22.10%
   15    1160     2.85%    1.10%   58.5%     55.20%   -28.30%
   20    1140     3.42%    1.25%   59.1%     68.50%   -32.10%
   30    1100     4.10%    1.50%   60.3%     95.30%   -38.50%
```

### HTML报告

独立文件 `output/forward_returns.html`,包含:

1. 汇总统计表格
2. 平均收益率随N天变化折线图
3. 胜率随N天变化折线图
4. 每个N天的收益率分布柱状图

## CLI入口

`cmd/forward/main.go`:

```go
func main() {
    codes := common.GetAllCodes()
    _, _, years, _, _ := common.LoadBacktestConfig()

    core.ForwardReturnAnalysis{
        Buyer:        TestBuy,          // 复用backtest中的策略定义
        Codes:        codes,
        Years:        years,
        GetDayKlines: common.Pull.DayKlines,
        ForwardDays:  core.DefaultForwardDays(),
        Goroutines:   common.DefaultGoroutines * 2,
    }.Run()
}
```

## 边界处理

* **数据不足**: 信号触发日 + N 超出 dks 范围时,该 N 天的收益不记录(跳过),不影响其他 N 天

* **空结果**: 无信号时打印提示,不生成HTML

* **ForwardDays为空**: 使用 `DefaultForwardDays()` 兜底

* **Goroutines <= 0**: 默认使用 `common.DefaultGoroutines`

## 不在范围内

* 不集成到 Backtest.Run() 中

* 不修改 Backtest / Do() / \_backtest() 的任何代码

* 不涉及卖出策略或成本模型

* 不修改现有HTML报告

