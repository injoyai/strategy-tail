# 策略实验室（Strategy Lab）设计

> 日期: 2026-09-05
> 状态: 已与用户逐节确认
> 入口: `go run ./cmd/lab` → `http://localhost:PORT`

## 1. 概述

本地 Web 服务形态的策略实验室：在浏览器里编辑 **Go 策略脚本**（Yaegi 解释执行策略定义）、配置运行参数（时间范围/股票池/样本池/卖出规则）、触发回测、实时看进度，完成后在 Tab 工作流页面查看组合对比、交易明细与个股 K 线买卖点。

**核心分工**：Yaegi 只解释"策略定义"（脚本），回测引擎循环（数据拉取、逐日循环、统计、落盘）仍是现有编译代码——解释开销只发生在 `Buy()` 分发层，重计算全部由原生组件承担。

**不做的事**（YAGNI）：多任务并行、用户系统/鉴权、分布式、实盘接入、分钟线图、自选股收藏。

## 2. 背景与动机

- 用户跑完 `cmd/backtest_tail`（尾盘阴线收回策略参数矩阵）后，交易记录已按 AGENTS.md 6.1 落盘（`core.ExportTradesCSV` / `ExportTradesHTML`），但：
  - 静态 HTML 无法筛选、无法切组合、无法钻取；
  - 调整策略参数需要改 Go 代码 → 手动跑命令 → 等 50 分钟 → 再开文件。
- 已有可视化资产：`cmd/visualize`（lorca 单股诊断页）、`cmd/future`（命中点卡片报告）、`core/analyze_html.go`（交易可视化）、`core/trades_export.go`（本次新增的通用导出）。本设计是它们的"服务化 + 工作流化"升级，不是重写。

### 用户决策记录

| 决策点 | 选择 |
|---|---|
| 页面布局 | B · 三步工作流（Tab 页） |
| 技术形态 | 本地 Web 服务（非纯静态报告） |
| 服务能力 | 报告列表+查看、触发重跑、在线调参（三项全选） |
| 调参方式 | 通用策略实验室：界面直接写 Go 脚本定义策略 |
| 动态策略引擎 | Yaegi 解释器（脚本只提供策略，引擎用写好的编译代码） |
| 加速方式 | 支持样本池（全部/指定代码/随机 N 只） |

## 3. 架构

```
cmd/lab/main.go            # 入口：go run ./cmd/lab → 自动打开浏览器
internal/lab/              # 服务端（net/http 标准库，无框架）
  ├─ server.go             # REST API + 静态页服务（embed）
  ├─ runner.go             # 回测任务：单任务队列 + 进度上报 + 停止
  ├─ interp.go             # Yaegi 封装：加载脚本 → []core.Variant
  └─ symbols.go            # 项目包符号表（供脚本 import）
web/lab/index.html         # 前端单页（embed 进二进制）：三 Tab 工作流
strategies/script/matrix.go # 策略脚本（页面编辑器直接读写该文件）
```

依赖新增：`github.com/traefik/yaegi`（Go 解释器，Apache-2.0，traefik 官方维护）。

### 3.1 脚本契约

```go
// strategies/script/matrix.go —— 脚本只定义策略，引擎现成
package main

import (
    "github.com/injoyai/strategy-tail/core"
    sb "github.com/injoyai/strategy-tail/strategies/buy"
)

func Strategy() []core.Variant {
    return []core.Variant{
        {Name: "MA5向上·收回MA5", Buyer: sb.And{
            sb.A阴线收回{SupportPeriod: 5}, sb.MAUp{Period: 5}}},
        {Name: "MA5向上·收回MA10", Buyer: sb.And{
            sb.A阴线收回{SupportPeriod: 10}, sb.MAUp{Period: 5}}},
    }
}
```

- 新增 `core.Variant{Name string; Buyer core.Buyer}`（最小契约，一次返回多个 = 多组合对比）。
- 脚本可 import 项目全部现有组件，也可内联自定义逻辑；页面提示"重逻辑请组合现有组件"（内联循环被解释执行会慢）。
- 卖出规则/时间范围/股票池/样本池不属于脚本，是页面表单的"运行配置"。

### 3.2 运行流程

1. 页面编辑脚本 → PUT /api/script 保存到 `strategies/script/matrix.go`（单一事实源，无草稿态）。
2. POST /api/run：服务端 Yaegi 加载脚本（编译期报语法/类型错误）→ 校验通过后进入单任务队列。
3. 回测循环：复用 backtest_tail 的模式（逐股拉数据、cloneKlines 隔离、每股跑完全部变体、`core.Stats` 汇总），股票池/样本池/卖出规则来自运行配置。
4. 进度：worker 池加 atomic 计数器，前端轮询 GET /api/status（股数粒度）。
5. 完成：产物落盘（见 §6）→ 前端切到 Tab② 展示。

## 4. 前端：三 Tab 工作流

### Tab① 策略与运行
- 左侧：策略脚本编辑器（等宽字体 textarea 起步，可升级 CodeMirror CDN）+ [语法检查] [运行] 按钮；Yaegi 错误显示在编辑器下方。
- 右侧：运行配置表单——时间范围、股票池（沪深主板等，复用 `common.GetNoPriceLimitCodes` 的口径）、样本池（全部/指定代码列表/随机 N 只）、卖出规则（持仓天数、止盈%、止损%、启用开关）、本金/仓位/费率。
- 底部：运行状态条（进度条 + 当前股 + 完成变体数 + [停止]）。

### Tab② 组合对比
- 变体汇总表：笔数/胜率/平均%/中位%/盈亏比/总收益额，按平均收益排序，数字全部来自 `core.Stats()`。
- 图表：累计收益曲线叠加（每变体一条线）、胜率条形图。
- 行点击选中 → Tab③ 默认筛到该变体。

### Tab③ 交易明细与K线
- 筛选器：代码/日期范围/收益率区间/变体。
- 明细表（分页/虚拟滚动视数据量定）。
- 点行展开 ECharts 日K（MA5/10/20 + 成交量 + B/S 买卖点），图表配置复用 `core/trades_export.go` HTML 的实现；K 线数据走 GET /api/kline/{code}（`common.Pull.DayKlines`）。

前端为自包含 `index.html`（ECharts + Vue3 均 CDN 引入），`go:embed` 进二进制，无构建链。

## 5. REST API

| 方法 | 路径 | 作用 |
|---|---|---|
| GET | /api/script | 读当前脚本内容 |
| PUT | /api/script | 保存脚本 |
| POST | /api/script/check | Yaegi 干跑校验，返回编译错误 |
| POST | /api/run | 启动回测（body = 运行配置 JSON） |
| GET | /api/status | {state, progress, currentCode, variantsDone, startedAt, error} |
| POST | /api/stop | 停止当前任务 |
| GET | /api/report/latest | 最新完成报告 JSON |
| GET | /api/reports | 历史报告列表 |
| GET | /api/report/{id} | 指定报告 |
| GET | /api/kline/{code}?from=&to= | 日K 数据 |

约束：**同时只允许 1 个回测任务**（互斥锁）；同时只允许 1 个脚本文件（契约固定），未来多脚本再扩展。

## 6. 报告数据流

回测完成 → 双层产物：

1. **落盘**（AGENTS.md 6.1 硬性要求，离线可查）：
   ```
   output/trades/<时间戳>_<脚本名>/
     ├─ report.json        # {config, variants: [{name, stats, trades[]}]}
     ├─ <变体名>.csv        # 每变体一个，core.ExportTradesCSV
     └─ summary.html       # core.ExportTradesHTML 静态汇总
   ```
   run 目录名永不覆盖；`report.json` 用临时文件+rename 防半写。
2. **API 直出**：/api/report/latest 直接序列化内存数据；Tab②③ 渲染一律以 `report.json` 为数据源，**页面不算指标**，保证与命令行/CSV 口径一致（同源 `core.Stats`）。

## 7. 风险控制

| 风险 | 对策 |
|---|---|
| 脚本死循环/内联重逻辑慢 | 不做单次 `Buy()` 超时（goroutine 无法安全中断）；样本池先小后大 + 页面提示用现有组件组合 |
| Yaegi 符号表缺包/类型不兼容 | `symbols.go` 集中注册 `core`、`strategies/buy`、`strategies/sell`、`lib/extend`、`tdx/protocol`、`goutil` 常用包；首次接入逐个验证实际可用性，踩雷类型记入 MEMORY.md |
| 服务被关/崩溃 | 任务从零重跑（个人工具可接受）；report.json 原子写 |
| 同名 run 覆盖 | 目录名带时间戳，永不覆盖 |
| 编辑器丢代码 | PUT 即落盘，无草稿态 |
| 内存（逐股克隆×变体） | 沿用 backtest_tail 模式（每股跑完全部变体再释放）；变体数页面提示典型 2-10 |

## 8. 测试策略

- `internal/lab/interp_test.go`：示例脚本经 Yaegi 实例化 `A阴线收回`+`MAUp`+`And`，`Buy()` 判定与原生编译结果对拍。
- `internal/lab/server_test.go`：`httptest` 走 API 契约——脚本读写、check 报错、run→status→report 生命周期（微型数据）。
- 前端人工验收三 Tab。
- 端到端：`go run ./cmd/lab` → 界面跑 2 变体 × 随机 50 股 × 3 个月迷你回测 → Tab② 对比正确、`output/trades/` 产物齐全。

## 9. 验收标准

1. `go run ./cmd/lab` 启动即用，无构建步骤。
2. 界面编辑脚本 → 语法检查 → 运行 → 进度可见 → 完成出报告。
3. Tab② 数字与落盘 CSV/命令行口径一致。
4. Tab③ 可筛选交易并查看个股 K 线买卖点。
5. 历史报告列表可回看任意一次 run。
6. 全部产物落盘符合 AGENTS.md 6.1。

## 10. 明确的非目标

- 不做多任务并行回测。
- 不做鉴权/多用户/远程访问（仅 127.0.0.1）。
- 不做实盘、分钟级图、自选股管理。
- 不改现有 `cmd/backtest_tail` 行为（它仍是命令行全量验证入口）；lab 的回测循环复用同一套模式而非直接 import 该 cmd。
