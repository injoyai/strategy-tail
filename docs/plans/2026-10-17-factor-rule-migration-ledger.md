# 因子规则统一迁移台账（T0）

- 日期：2026-10-17
- 基线提交：`261cb71`（工作区仅 docs 改动）
- 依据计划文档：`docs/plans/2026-10-17-factor-rule-unification.md`（第 2/3/4 节、附录 A/B/C）
- 数据来源：`internal/lab/builder/catalog.go`、`strategies/buy/*`、`strategies/sell/*`、`strategies/factor/*`、`internal/lab/testdata/condition_baseline.json`（73 项前缀信号哈希，只读引用）

## 0. 阅读约定（适用于全表）

- 参数换算：UI 传入为浮点；`ratioArg`（catalog.go:16-24）把"比率类"参数按 `/100` 换算（UI 百分数 → 内部比例）；`percentArg`（catalog.go:26-32）、`intArg`、`floatArg`、`stringsArg` 不换算。价格单位为厘（`protocol.Price`），显示为元；流通市值为亿元。
- "当前调用方"基础盘（全表共用，表内不重复）：`internal/lab/builder/catalog.go` 工厂（行号见第 1 节映射）→ `internal/lab/builder/spec.go:323`（买入树）/`spec.go:360`（卖出/风控树）派发；`internal/lab/condition_equivalence_test.go:21 TestConditionFactorEquivalence` 对全部 73 项做前缀信号哈希对拍（`internal/lab/testdata/condition_baseline.json` 73 项全覆盖）；`internal/lab/builder/catalog_test.go:30 TestBuilderCatalogFactoryCoverage` 覆盖全部工厂。表内只列 builder 之外的额外调用方与专有测试。
- "旧类型/方法"列给出真实类型定义 `file:line`；工厂注册行号见第 1 节。
- 状态列记录迁移结果；最终验收缺口与评审修复见 §6.5、§7。

## 1. ID 覆盖核对

- `internal/lab/builder/catalog.go` 实际注册：`buyFactories` 44 个（catalog.go:534-846）、`sellFactories` 29 个（catalog.go:848-1011），合计 **73 个**，与 grep 提取的注册键逐条核对一致。
- 与计划文档附录 A（40 项基础比较）+ B（19 项时序）+ C（14 项持仓）= 73 项逐一核对：**完全一致，无缺漏、无多余**。
- `internal/lab/testdata/condition_baseline.json` 共 73 个键（grep `"buy_|sell_|risk_"` 计数 = 73），与注册 ID 一一对应。
- 未发现第 74 个形如 `buy_/sell_/risk_` 的注册字符串。备注两处易混淆但不构成差异：
  - `strategies/sell/sell_rsi_test.go:246-249` 内部表驱动键 `"risk_low"` 是测试用例名，非注册 ID。
  - 目录外旧组件（如 `A阴线收回`、`A突破N天新高` buy_price.go:217、`MACD负柱缩短/顺滑/平滑`、`N天前符合/近N天符合` buy_window.go、`A底顶部抬升` buy_底部抬升.go:52、`SellRSI` sell_rsi.go、`SellAt` sell_at.go、`SellTPSL`（旧）、`A时间止损/缩量滞涨`）均**未注册**进 catalog，也不在 73 项内，归"未入目录清单"（第 3 节）。
- 工厂注册行号映射（供"当前调用方"引用）：buy_all:534, buy_board_cyb:535, buy_board_kcb:536, buy_board_bj:537, buy_etf:538, buy_price:539, buy_market_cap:547, buy_price_above_ma:555, buy_stand_above_ma:559, buy_close_above_ma:570, buy_ma_up:574, buy_ma_bull:590, buy_pullback_ma:594, buy_macd_negative:609, buy_macd_rising:621, buy_macd_positive:640, buy_macd_reversal:648, buy_macd_golden_cross:664, buy_macd_above_zero:668, buy_rsi_low:672, buy_rsi_turn_up:680, buy_rsi_range:684, buy_rsi_oversold_rebound:696, buy_volume_surge:704, buy_volume_double:712, buy_volume_shrink:724, buy_turnover_min:732, buy_limit_up_hit:736, buy_filter_limit_up:737, buy_close_gt_prev_close:738, buy_close_gt_prev_open:739, buy_break_high:740, buy_solid_bull:744, buy_solid_bear:748, buy_recent_gain_below:752, buy_day_gain_range:760, buy_bottom_up:768, buy_atr_range:772, buy_boll_lower:784, buy_boll_lower_volume:792, buy_boll_lower_rsi:808, buy_bias_oversold:824, buy_zscore_oversold:832, buy_donchian_lower:840, sell_macd_reversal:848, sell_macd_down_after_buy:860, sell_macd_dead_cross_negative:872, sell_break_ma:876, sell_below_short_ma:884, sell_ma_dead_cross:888, sell_long_ma_down:896, sell_back_to_ma:900, sell_rsi_overbought:904, sell_rsi_recover:912, sell_back_to_boll_mid:920, sell_bias_zero:924, sell_zscore_zero:928, sell_back_to_donchian_mid:932, sell_big_fall:936, sell_volume_big_bear:940, risk_takeprofit_stoploss:948, risk_fixed_stop:956, risk_trailing_stop:960, risk_atr_stop:964, risk_profit:972, risk_fixed_tp:976, risk_trailing_takeprofit:980, risk_atr_takeprofit:988, risk_holding_days:996, risk_holding_days_tail:1000, risk_break_buy_price:1008, risk_no_gap_up:1009, risk_break_buy_open:1010。

## 2. 迁移台账（73 行）

### 附录 A：基础比较（40 项）

| ID | 角色 | 旧类型/方法(file:line) | 参数及单位 | 默认/零值语义 | 公式及引用窗口 | 预热要求 | 无效输入行为 | 持仓依赖 | 当前调用方 | 目标原语(暂定) | 测试证据 | 状态 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| buy_all | 买入 | A全部（buy_logic.go:110） | 无 | 无 | Buy 恒 true（buy_logic.go:115-117） | 0 | 空前缀也 true | 无 | 仅 builder（无参工厂） | 原生谓词(全部买入) | condition_baseline 在列 | 已迁移（T2 确认原语，保留原生实现） |
| buy_board_cyb | 买入 | A创业板（buy_types.go:10） | 无 | 无 | AddPrefix 后前缀 `sz30` 且 len==8（buy_types.go:14-18） | 0 | 未 AddPrefix 或其他前缀 → false | 无 | 仅 builder | 原生谓词(板块) | condition_baseline 在列 | 已迁移（T2 确认原语，保留原生实现） |
| buy_board_kcb | 买入 | A科创板（buy_types.go:23） | 无 | 无 | 前缀 `sh68` 且 len==8（buy_types.go:27-31） | 0 | 同上 | 无 | 仅 builder | 原生谓词(板块) | condition_baseline 在列 | 已迁移（T2 确认原语，保留原生实现） |
| buy_board_bj | 买入 | A北证板（buy_types.go:36） | 无 | 无 | 前缀 `bj` 且 len==8（buy_types.go:40-44） | 0 | 同上 | 无 | 仅 builder | 原生谓词(板块) | condition_baseline 在列 | 已迁移（T2 确认原语，保留原生实现） |
| buy_etf | 买入 | ETF（buy_types.go:49） | 无 | 无 | `protocol.IsETF(code)`（buy_types.go:54-56；Name 注释误写"北交所"，实际按 ETF 判定） | 0 | 非 ETF → false | 无 | 仅 builder | 原生谓词(ETF) | condition_baseline 在列 | 已迁移（T2 确认原语，保留原生实现） |
| buy_price | 买入 | A价格（buy_price.go:26，Buy :44-58） | min/max float64，元 | 目录默认 2/120；组件内 0=不限（:47-50）；`A现价` 是 A价格 别名（buy_price.go:19） | 最新收盘（`f.A价格("close")`，market.go:10-32，protocol.Price 厘）与阈值比较 | 1 根 | 空 K 线 A价格 返回 NaN，但 Buy 先判 `len(dks)==0` → false；NaN 不会命中 | 无 | common.go:84（A现价=2-120）、cmd/screen/strategies.go:27；template.go:35（0-120） | 比较(收盘价区间) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_market_cap | 买入 | A流通市值（buy_ma.go:14，Buy :32-43） | min/max float64，亿元 | 0=不限且 Name 显示"流通市值"；max<min 不再校验（仅目录范围 0-1e9）；负阈值不生效（`Min>0`/`Max>0` 才判） | `f.A流通市值{}.Value`（factor/market.go:52，FloatValue/1e8 亿元）与阈值比较；引用当日 | 1 根 | 空 K 线 Value=NaN → 两比较均 false | 无 | common.go:83/105（400-）、cmd/screen/strategies.go:28/51（≤30）、script symbols.go:33（≥50）；template.go:34 | 比较(流通市值区间) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_price_above_ma | 买入 | A现价大于N日均线（buy_volume.go:175，Buy :180-194） | period int，天 | 目录默认 20；组件内无默认（<=0 直接比较） | 整数比较：今收盘 protocol.Price(厘) > `f.A均线(day).Price(dks)`（technical.go:20-64，窗口=dks 全部） | period 根 | len(dks)<day → false；ma<=0 → false | 无 | common.go:90（25）；template.go:39（30） | 比较(整数价格守恒) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_close_above_ma | 买入 | A收盘高于均线（buy_trend_volume.go:13，Buy :19-33） | period int，天 | 目录默认 20；组件内无默认 | 浮点比较：today.Close.Float64() > `f.A均线(p).Value`（元，NaN 当窗口不足） | period 根 | len(dks)<p → false；NaN 比较 false → false | 无 | 仅 builder | 比较(浮点) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_ma_bull | 买入 | A均线多头排列（buy_trend_volume.go:394，Buy :400-426） | periods intList，天 | 组件内空/nil → [5,10,20]（:403-407）；目录默认 [5,10,20] | `f.A均线(pi).Value` 逐级严格递减 MA0>MA1>…；窗口=dks 全部 | maxP 根 | len(dks)<maxP → false；NaN 时 `>` 链 false → false | 无 | cmd/screen/strategies.go:31（[5,10,20]）；script symbols.go:35 | AND(多因子递减比较) | condition_baseline 在列 | 已迁移（T2 部分内核委托：相邻 MA 序列比较经 NewAnd，单周期退化保留门面） |
| buy_rsi_low | 买入 | RSI（buy_rsi.go:15，Buy :25-43） | period int 天；threshold float64 | 目录默认 14/20（阈值范围 [1,99]，0 会被目录拒收）；组件内无默认 | `f.RSI值(p).Value`（浮点，technical.go:170-201）< threshold（严格） | period+1 根 | len<period+1 → false；NaN 比较 false | 无 | cmd/screen/strategies.go:47（50/1-99）；sell_rsi_test.go 无关 | 比较(RSI) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_rsi_range | 买入 | RSI区间（buy_trend_volume.go:108，Buy :114-144） | period int 天；min/max float64 | 组件内 0→min=30、0→max=50（:119-124）；目录默认同样 30/50 | RSI 浮点 ∈ [min,max]（含边界） | period+1 根 | len<period+1 → false；NaN 比较 false | 无 | 仅 builder | 区间(RSI) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_volume_surge | 买入 | A成交量放大（buy_trend_volume.go:68，Buy :74-101） | period int 天；ratio float64 比例 | 目录默认 5/1.5；组件内无默认 | 今日成交量 > `f.A前N日均量(p)`（technical.go:70-100，不含今日）×ratio；基准 <=0 → false | period+1 根 | len<period+1 → false | 无 | 仅 builder | 比较(量比) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_volume_double | 买入 | A倍量（buy_volume.go:101，Buy :107-156） | minRatio/maxRatio 比例；baseDays int 天 | 目录默认 2/0/1；组件内 0=不限上限（仅 max>min 才判 :119-122）；Name 显示"倍量" | 今日量/`f.A前N日均量(base)`（不含今日）>minRatio 且（生效时）<maxRatio；基准 <=0 → false | base+1 根 | len<base+1 → false | 无 | 仅 builder | 比较(量比区间) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_volume_shrink | 买入 | A缩量（buy_volume.go:293，Buy :299-320） | days int 天；ratio float64 比例 | 目录默认 5/0.8；组件内无默认 | 今日量 < `f.A前N日均量(days)`（不含今日）×ratio；基准 <=0 → false | days+1 根 | len<days+1 → false | 无 | buy_alias_test.go:25（TestA缩量） | 比较(量比) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_turnover_min | 买入 | A成交额大于（buy_trend_volume.go:274） | min float64，元（A成交额为原始元） | 目录默认 5e7；组件内 0→默认 5000 万（:277-279） | `f.A成交额{}.Value`（technical.go:102-114，当日成交额元）>= min | 1 根 | len==0 → false；NaN 比较 false | 无 | 仅 builder | 比较(成交额下限) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_limit_up_hit | 买入 | A涨停（buy_price.go:93，Buy :100-118） | 无 | 无 | AddPrefix；len==8 时：sh60/sz00 涨幅>=9.8%；sh68/sz30/bj92 >=19.8%；默认 false | 1 根 | 未 AddPrefix/长度≠8 → false | 无 | cmd/screen/strategies.go:23（A涨停 + A过滤涨停 组合） | 比较涨跌幅+原生谓词组合 | condition_baseline 在列；注意 Name 与 A过滤涨停 重复（风险 §5.1） | 已迁移（T2 内核委托） |
| buy_filter_limit_up | 买入 | A过滤涨停（buy_price.go:63，Buy :69-88） | 无 | 无 | 同上口径反向：sh60/sz00 <9.8%；sh68/sz30/bj92 <19.8% | 1 根 | 同上 | 无 | common.go:85/107/119；script symbols.go:38；template.go:36 | 比较涨跌幅+原生谓词组合 | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_close_gt_prev_close | 买入 | A收盘大于昨收（buy_收盘大于昨收.go:16，Buy :20-24） | 无 | 无 | 今收盘 > 昨收盘（`f.A价格`，dks[n-2]）；严格大于 | 2 根 | len<2 → false | 无 | 仅 builder | 比较(今收/昨收) | condition_baseline 在列 + buy_收盘大于昨收_test.go（5 用例） | 已迁移（T2 内核委托） |
| buy_close_gt_prev_open | 买入 | A收盘大于昨开（buy_收盘大于昨开.go:16，Buy :20-24） | 无 | 无 | 今收盘 > 昨开盘；严格大于 | 2 根 | len<2 → false | 无 | 仅 builder | 比较(今收/昨开) | condition_baseline 在列 + buy_收盘大于昨开_test.go（5 用例） | 已迁移（T2 内核委托） |
| buy_break_high | 买入 | A突破N日高点（buy_trend_volume.go:366，Buy :372-389） | period int 天 | 目录默认 20；组件内无默认 | 今日收盘 > `f.A最高价(p).Value(dks[:n-1])`（不含今日的近 p 日最高，technical.go:206-246 浮点元） | period+1 根 | len<period+1 → false；prevHigh=NaN 时 `>` false | 无 | 仅 builder；目录外同名旧件 A突破N天新高（buy_price.go:217，含今日、用 High）勿混淆 | 比较(历史窗口极值) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_solid_bull | 买入 | A实体阳线（buy_trend_volume.go:431，Buy :437-454） | minBodyRatio ratio（UI%→内部比例） | 目录默认 0（描述"0=只要收阳"）；**组件内 0→0.5**（:440-443），与目录描述冲突（风险 §5.2） | 收阳 close>open 且 `f.A实体振幅占比`（technical.go:262-286）>= minBodyRatio | 1 根 | len==0 → false；high==low → 占比 NaN → false | 无 | 仅 builder | AND(原生K线+比较) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_solid_bear | 买入 | A实体阴线（buy_实体阴线.go:22，Buy :33-55） | minBodyRatio ratio | 目录默认 0；组件内 MinBodyRatio<=0 → 只要收阴（:34-38，与目录一致，和 A实体阳线 口径不同） | 收阴 close<open 且（生效时）A实体振幅占比 >= ratio | 1 根 | len==0 → false；NaN 占比 → false | 无 | buy_实体阴线_test.go（5 用例） | AND(原生K线+比较) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_day_gain_range | 买入 | A单日涨幅范围（buy_price.go:179，Buy :185-211） | min/max float64，百分数原值（percentArg 不换算） | 目录默认 -5/7；组件内 0=不限（`!=0` 才判，负下限生效） | `f.A单日涨幅{}.RiseRate()`（technical.go:148-165，当日涨跌幅%）∈[min,max]（含边界） | 1 根 | len==0 → false；NaN 比较 false | 无 | 仅 builder | 区间(当日涨跌幅) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_atr_range | 买入 | ATR波动率范围（buy_trend_volume.go:327，Buy :333-361） | period int 天；minPct/maxPct 百分数 | 目录默认 14/0/5；组件内 0→min=0.5、0→max=5.0（:336-343）；max<min 时仅 min 生效 | ATR/今收×100 ∈ [min,max]（含边界）；ATR=`f.ATR值(p)`（technical.go:288-320，不含今日窗口） | period+1 根 | len<period+1 → false；close<=0 → false；NaN 比较 false | 无 | 仅 builder | 区间(ATR%) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_boll_lower | 买入 | A布林下轨（buy_meanrevert.go:19，Buy :25-50） | period int 天；std 倍数 | 目录默认 20/2；组件内 0→20、0→2（factor.A布林下轨 technical.go:116 内部 multiple 0→2） | 今收盘 <= 下轨 MA-std×σ（浮点元）；窗口=dks 全部 | period 根 | len<p → false；σ=0 时下轨 NaN → false | 无 | 仅 builder | 比较(布林带) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_boll_lower_volume | 买入 | A布林下轨缩量（buy_meanrevert.go:182，Buy :188-233） | period/std/volDays 天；volRatio 比例 | 目录默认 20/2/5/0.6；组件内 0→各自默认 | 下轨条件 且 今日量 <= `f.A前N日均量(vd)`（不含今日）×volRatio；基准<=0 → false | max(period, volDays+1) 根 | len 不足任一 → false | 无 | 仅 builder | AND(比较+量比) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_boll_lower_rsi | 买入 | A布林下轨RSI超卖（buy_meanrevert.go:245，Buy :251-291） | period/std 天·倍；rp int 天；rt float64 | 目录默认 20/2/14/30；组件内 0→默认 | 下轨条件 且 浮点 `f.RSI值(rp)` <= rt | max(period, rp+1) 根 | len 不足 → false；NaN 比较 false | 无 | 仅 builder | AND(比较+比较) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_bias_oversold | 买入 | A乖离超卖（buy_meanrevert.go:60，Buy :66-95） | period int 天；minBias 百分数 | 目录默认 20/-7；组件内 0→-7（:70-74，**0 会被覆盖，无法表达 0 阈值**） | bias=(今收-MA)/MA×100 <= minBias；MA<=0 → false | period 根 | len<p → false；NaN 比较 false | 无 | 仅 builder | 比较(乖离率) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_zscore_oversold | 买入 | AZScore超卖（buy_meanrevert.go:106，Buy :112-137） | period int 天；minZ 标准差倍数 | 目录默认 20/-2；组件内 0→-2（:116-120） | z=(今收-MA)/σ <= minZ；σ=0 → NaN → false | period 根 | len<p → false | 无 | 仅 builder | 比较(ZScore) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| buy_donchian_lower | 买入 | A唐奇安下沿（buy_meanrevert.go:147，Buy :153-170） | period int 天 | 目录默认 20；组件内 0→20 | 今收盘(厘) <= `f.A最低价(p).Price(dks[:n-1])`（不含今日，protocol.Price 厘整数比较） | period+1 根 | len<p+1 → false | 无 | 仅 builder | 比较(历史极值,整数) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| sell_back_to_ma | 卖出 | A回到均线（sell_meanrevert.go:60，Sell :66-85） | period int 天 | 目录默认 20；组件内 0→20 | 今收 >= `f.A均线(p).Value`；MA<=0 → false | period 根 | len<p → false；NaN 比较 false | 无 | 仅 builder | 比较(浮点) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| sell_rsi_overbought | 卖出 | A_RSI超买（sell_trend_volume.go:51，Sell :57-77） | period int 天；threshold float64 | 目录默认 14/75；组件内 0→14、0→75 | 浮点 RSI > threshold（严格） | period+1 根 | len<period+1 → false；NaN 比较 false | 无 | 仅 builder | 比较(RSI) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| sell_rsi_recover | 卖出 | ARSI恢复（sell_meanrevert.go:199，Sell :205-230） | period int 天；threshold float64 | 目录默认 14/50；组件内 0→14、0→50 | **整数** `f.A整数RSI值(p)`（extend.RSI 0-100 口径）>= threshold；与买入侧浮点 RSI 不同源（风险 §5.4） | period+1 根 | len<p+1 → false | 无 | 仅 builder | 比较(整数RSI) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| sell_back_to_boll_mid | 卖出 | A回到布林中轨（sell_meanrevert.go:26，Sell :32-51） | period int 天 | 目录默认 20；组件内 0→20 | 今收 >= `f.A均线(p).Value`（即中轨）；MA<=0 → false | period 根 | len<p → false | 无 | 仅 builder；cmd/screen/strategies.go:62（ss.A回到布林中轨 20） | 比较(均线) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| sell_bias_zero | 卖出 | A乖离归零（sell_meanrevert.go:95，Sell :101-121） | period int 天 | 目录默认 20；组件内 0→20 | bias=(今收-MA)/MA×100 >= 0；MA<=0 → false | period 根 | len<p → false；NaN 比较 false | 无 | 仅 builder | 比较(乖离率) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| sell_zscore_zero | 卖出 | AZScore归零（sell_meanrevert.go:130，Sell :136-152） | period int 天 | 目录默认 20；组件内 0→20 | z=(今收-MA)/σ >= 0；σ=0 → NaN → false | period 根 | len<p → false | 无 | 仅 builder | 比较(ZScore) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| sell_back_to_donchian_mid | 卖出 | A回到唐奇安中轨（sell_meanrevert.go:162，Sell :168-189） | period int 天 | 目录默认 20；组件内 0→20 | mid=(HHV+LLV)/2，`f.A最高价/最低价(p).Value(dks)` **含今日**；mid<=0 → false；今收 >= mid | period 根 | len<p → false | 无 | 仅 builder | 比较(历史极值均值) | condition_baseline 在列；与 buy_donchian_lower 的"不含今日"口径相反（风险 §5.5） | 已迁移（T2 内核委托） |
| sell_big_fall | 卖出 | A单日跌幅大于（sell_trend_volume.go:112，Sell :118-133） | max float64 百分数 | 目录默认 7；组件内 0→7（:122-124） | `RiseRate()` < -max（严格；RiseRate 为当日涨跌幅%） | 1 根 | len==0 → false；NaN 比较 false | 无 | 仅 builder | 比较(当日跌幅) | condition_baseline 在列 | 已迁移（T2 内核委托） |
| sell_volume_big_bear | 卖出 | A放量大阴线（sell_trend_volume.go:300，Sell :306-333） | volumeRatio 比例；fallPct 百分数 | 目录默认 1.5/2.0；组件内 0→1.5、0→2.0 | 收阴 close<open 且 RiseRate <= -fallPct 且 今日量 > `f.A前N日均量(5)`（不含今日）×ratio；基准<=0 → false；量窗口硬编码 5 天 | 6 根 | n<6 → false | 无 | 仅 builder | AND(原生K线+跌幅+量比) | condition_baseline 在列 | 已迁移（T2 内核委托） |

### 附录 B：时序类（19 项）

| ID | 角色 | 旧类型/方法(file:line) | 参数及单位 | 默认/零值语义 | 公式及引用窗口 | 预热要求 | 无效输入行为 | 持仓依赖 | 当前调用方 | 目标原语(暂定) | 测试证据 | 状态 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| buy_stand_above_ma | 买入 | A站上N日均线（buy_above_ma.go:17，Buy :34-51） | period int 天；days int 天 | 目录默认 20/5；组件内 0→20、0→5（:23-30 与 :35-42 两处默认） | `f.MA上方连续天数{Period,Days}`（factor/temporal.go:18-48，今收>MA 严格，计数含今日）>= days（:50，浮点比较） | period+days 根 | len<period+days → false | 无 | cmd/screen/strategies.go:36（A站上N日均线 5/10）；script symbols.go:34（10/3） | 连续段(上方) | condition_baseline 在列 + buy_above_ma_test.go（TestA站上N日均线_数据不足/未站上/站上/持续/默认值，5 用例） | 已迁移（T3 内核委托） |
| buy_ma_up | 买入 | MAUp（buy_volume.go:259，Buy :265-286） | period int 天；lookback int 天；minSlope 相对斜率 | 目录默认 20/0/0；组件内 0→20、0→lookback=5（:270-273）；minSlope=0 不设限 | `f.MA最弱日斜率`（factor/temporal.go:52-86，相对斜率）>0 且 >=minSlope（buy_volume.go:276-282）；NaN 斜率 → false | period+lookback 根 | len<period+lookback → false | 无 | common.go:93（30/0/0.02）；template.go:41-42（30、20）；script symbols.go:30 | 时序窗口(斜率>0) | condition_baseline 在列；无专有单测 | 已迁移（T3 内核委托） |
| buy_pullback_ma | 买入 | A回踩N日均线（buy_pullback_ma.go:22，Buy :43-115） | period int 天；supportPeriod int 天；minTouchDays int 天 | 组件内 period 0→5（:44-47）、touchDays 0→3（:49-52）、SupportPeriod 0=不校验支撑（:37/:103） | 四条件 AND（:69-112）：昨收<=昨 MA、今收>今 MA 且>昨收、近 touchDays 天（不含今日）至少 1 天最低价<=当日 MA（**逐日重算 MA**，:89-97）、SupportPeriod>0 时窗口最低价>=MA(SupportPeriod)（以 windowStart-1 为末端取值，:104-108） | max(period,supportPeriod)+touchDays+1 根（minLen 见 :55-61） | n<minLen → false；windowStart<0 时截到 0 | 无 | 仅 builder | AND(时序窗口+逐日比较) | condition_baseline 在列 + buy_pullback_ma_test.go（5 用例） | 已迁移（T3 内核委托） |
| buy_macd_negative | 买入 | MACD负数（buy_macd.go:308，Buy :314-360） | fast/slow/signal int；minDays/maxDays int 天 | 目录默认 12/26/9/1/0；组件内 0→12/26/9、0→minDays=1、maxDays<=0 或 <min → 不限 | `f.NegativeStreak`（factor/macd_shape.go:64-71）>= minDays（<=maxDays）；hist 末段连续 <0 | slow+signal 根（且 >=minDays） | n<slow+signal 或 n<minDays → false | 无 | common.go:88（MinDays:5）；template.go:37（6/0） | 连续段(负值) | condition_baseline 在列；无专有单测（buy_macd_test.go 测的是目录外形态组件） | 已迁移（T3 内核委托） |
| buy_macd_rising | 买入 | MACD连涨（buy_macd.go:423，Buy :429-513） | minDays/maxDays int 天；lookback int 天 | 目录默认 1/0/0；组件内 0→minDays=1、maxDays<=0 不限、lookback 0=不校验 | 今 hist > 昨 hist 必须（:456-459）；`f.RisingStreak` >= minDays 且 <=maxDays（生效时）；lookback>0 时起点前 hist 需为窗口最大（不小于窗口任一值，:470-478） | slow+signal 且 >=minDays+1 根 | n 不足 → false | 无 | template.go:38（2/2/0） | 连续段(上升)+时序窗口 | condition_baseline 在列 + TestMACDShapeFactorConditions"reversal"用例覆盖 macd_rising_streak 组合（macd_shape_migration_test.go:83-86）；macd_shape_baseline 不含本组件 | 已迁移（T3 内核委托） |
| buy_macd_positive | 买入 | MACD正数（buy_macd.go:368，Buy :374-404） | fast/slow/signal int；days int 天 | 目录默认 12/26/9/1；组件内 0→默认、0→days=1 | `f.PositiveStreak`（factor/macd_shape.go:74-81）>= days；hist 末段连续 >0 | slow+signal 根（且 >=days） | n<slow+signal 或 n<days → false | 无 | 仅 builder | 连续段(正值) | condition_baseline 在列；无专有单测（macd_shape_baseline 不含本组件） | 已迁移（T3 内核委托） |
| buy_macd_reversal | 买入 | MACD反转（buy_macd.go:21，Buy :27-89） | fast/slow/signal int；minLookback/maxLookback int 天；minDiff float64 | 目录默认 12/26/9/20/0/0；组件内 0→默认、maxLookback<=0 或 <min → 不限、minDiff 0=不限 | 今 hist > 昨 hist+minDiff 且 昨日为近 k 日窗口最低：`f.SeriesPosition(hist[n-1-k:n-1])==0`（factor/macd_shape.go:11-24，末值==窗口最低，**平价也算 0**）；k∈[minLookback,end] | slow+signal 根 | n<2 或 n<slow+signal → false | 无 | common.go:106/127（MACDBuyer/MACDBaseBuyer 内嵌）；script symbols.go:19/36；template.go:45 | 时序窗口(谷底位置)+连续段 | condition_baseline 在列 + **macd_shape_baseline**（TestMACDShapeMigrationBaseline "trough"/"trough_diff"，macd_shape_migration_test.go:17-59）+ TestMACDShapeFactorConditions"reversal"（等价于 macd_trough_position+macd_rising_streak 组合，:83-86） | 已迁移（T3 内核委托） |
| buy_macd_golden_cross | 买入 | MACD金叉（buy_trend_volume.go:180，Buy :186-201） | fast/slow/signal int 天 | 目录默认 12/26/9；组件内 0→默认 | 昨 hist<=0 且 今 hist>0（跨零即金叉，不看 DIF） | slow+signal 根 | n<slow+signal → false | 无 | 仅 builder | 交叉(零轴) | condition_baseline 在列 | 已迁移（T3 内核委托） |
| buy_macd_above_zero | 买入 | MACD零轴上方（buy_trend_volume.go:206，Buy :212-227） | fast/slow/signal int 天 | 目录默认 12/26/9；组件内 0→默认 | 今 hist > 0 | slow+signal 根 | n<slow+signal → false | 无 | 仅 builder | 比较(hist>0) | condition_baseline 在列 | 已迁移（T3 内核委托） |
| buy_rsi_turn_up | 买入 | RSI拐头（buy_rsi.go:50，Buy :56-74） | period int 天 | 目录默认 14；组件内 0→14 | 今 RSI(浮点，dks 全窗) > 昨 RSI（dks[:n-1]） | period+2 根 | len<period+2 → false；NaN 比较 false | 无 | 仅 builder | 时序窗口(环比) | condition_baseline 在列 | 已迁移（T3 内核委托） |
| buy_rsi_oversold_rebound | 买入 | RSI超卖回升（buy_trend_volume.go:150，Buy :156-175） | period int 天；threshold float64 | 目录默认 14/30；组件内 0→14、0→30 | 昨 RSI < threshold 且 今 RSI >= threshold（浮点） | period+2 根 | len<period+2 → false | 无 | 仅 builder | 时序窗口(状态切换) | condition_baseline 在列 | 已迁移（T3 内核委托） |
| buy_recent_gain_below | 买入 | A近N日涨幅小于（buy_price.go:148，Buy :154-177） | days int 天；max float64 百分数 | 目录默认 5/15；组件内 0→days=5、0→max=15（percentArg 不换算） | `riseRateNDays`（buy_price.go:125-145，dks[n-1-days] 收盘→今收 涨幅%）< max（严格）；startPrice<=0 或窗口不足 → ok=false → false | days+1 根 | len<days+1 → false | 无 | 仅 builder | 时序窗口(区间收益) | condition_baseline 在列 | 已迁移（T3 内核委托） |
| buy_bottom_up | 买入 | A底部抬升（buy_底部抬升.go:17，Buy :23-48；趋势点复用 buy_bottom_up.go:17-91 findTrendUpPoints/isTrendUpPoint） | window int 天 | 目录默认 8；组件内 0→8 | 近 60 日内两个上升低点（isTrendUpPoint 前后各 window 根均更高）且后低点>前低点且间有更高高点 | **硬编码 60 根**（buy_底部抬升.go:27） | len<60 → false；window<=0 → 默认 8 | 无 | 仅 builder；A底顶部抬升（buy_底部抬升.go:52）共用 findTrendUpPoints | 时序窗口(枢轴低点) | condition_baseline 在列 + buy_底部抬升_test.go（6 用例） | 已迁移（T3 内核委托） |
| sell_macd_reversal | 卖出 | ss.MACD反转（sell_macd.go:24，Sell :30-80） | fast/slow/signal int；lookback int 天；minDiff float64 | 目录默认 12/26/9/20/0；组件内 0→默认、0→lookback=20、minDiff 0=不限 | 今 hist < 昨 hist-minDiff 且 昨 hist == [n-1-lookback, n-2] 窗口最大值（严格相等，sell_macd.go:56-63） | slow+signal 根（组件无预检，len<2 直接比较） | n<2 → false；hist 长度≠len(dks) → false | 无 | common.go:100（MACDSeller）；template.go:46 | 时序窗口(峰顶位置) | condition_baseline 在列；无专有单测（macd_shape_baseline 仅覆盖 buy 包形态组件，不含本组件） | 已迁移（T3 内核委托） |
| sell_macd_dead_cross_negative | 卖出 | A_MACD死叉且DIF为负（sell_trend_volume.go:81，Sell :87-107） | fast/slow/signal int 天 | 目录默认 12/26/9；组件内 0→默认 | 昨 hist>0 且 今 hist<=0 且 `f.MACDDIF值`（technical.go:202-204）<0 | slow+signal 根 | n<slow+signal → false | 无 | 仅 builder | 交叉(零轴)+比较(DIF) | condition_baseline 在列 | 已迁移（T3 内核委托） |
| sell_break_ma | 卖出 | A收盘跌破均线（sell_trend_volume.go:15，Sell :21-46） | period int 天；days int 天 | 目录默认 20/3；组件内 0→20、0→3 | `f.MA下方连续天数`（factor/temporal.go:118-148，今收<=MA 含相等）>= days | period+days 根 | len<period+days → false | 无 | script symbols.go:41（ss.A收盘跌破均线 20/2） | 连续段(下方) | condition_baseline 在列 | 已迁移（T3 内核委托） |
| sell_below_short_ma | 卖出 | A跌破短期均线（sell_bluechip_pullback.go:15，Sell :21-38） | period int 天 | 目录默认 10；组件内 0→10 | 今收 < `f.A均线(p).Value`（严格） | period 根 | len<period → false；MA<=0/NaN → false | 无 | 仅 builder | 比较(浮点) | condition_baseline 在列 | 已迁移（T3 内核委托） |
| sell_ma_dead_cross | 卖出 | A均线死叉（sell_bluechip_pullback.go:103，Sell :109-140） | short/long int 天 | 目录默认 5/20；组件内 0→5、0→20 | 今日 shortMA<longMA 且 昨日 shortMA>=longMA（`f.A均线.Value` 浮点） | long+1 根 | len<long+1 → false | 无 | 仅 builder | 交叉(两均线) | condition_baseline 在列 | 已迁移（T3 内核委托） |
| sell_long_ma_down | 卖出 | LongMADown（sell_ma.go:14，Sell :20-32） | lookback int 天 | 目录默认 5；组件内 0→5 | !maUp(250) 或 !maUp(60)；maUp 用 `f.MA最弱日增量`（factor/temporal.go:90-116，绝对增量 >0）；先拦截 len<250+lookback | 250+lookback 根 | len<250+lookback → false（先拦截，避免 NaN 误触发） | 无 | 仅 builder | AND(时序窗口斜率) | condition_baseline 在列 | 已迁移（T3 内核委托） |

### 附录 C：持仓类（14 项）

| ID | 角色 | 旧类型/方法(file:line) | 参数及单位 | 默认/零值语义 | 公式及引用窗口 | 预热要求 | 无效输入行为 | 持仓依赖 | 当前调用方 | 目标原语(暂定) | 测试证据 | 状态 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| sell_macd_down_after_buy | 卖出 | MACD买入后连跌（sell_macd.go:88，Sell :94-148） | afterDays int 天；days int 天 | 目录默认 0/1；组件内 0→afterDays=1、0→days=1 | 逆序按日期串找买入日索引（sell_macd.go:131-137，**逆序匹配**）；n-1-buyIdx>=afterDays；`f.FallingStreak(hist)`（factor/macd_shape.go:84-91）>= days | days+1 根 | n<days+1 → false；买入日未找到或不足 afterDays → false | 需买入日索引（buy.Time 逆序匹配） | 仅 builder | 历史引用(买入日)+连续段(下降) | condition_baseline 在列 + sell_macd_test.go（TestMACD买入后连跌* 4 用例） | 已迁移（T4 内核委托） |
| risk_takeprofit_stoploss | 风控 | A止盈止损（sell_tpsl_new.go:18，Sell :24-76） | takeProfit/stopLoss ratio（UI%→内部比例） | 目录默认 0.1/0.05；组件内 tp<=0&&sl<=0 → false；Name 仅显示>0 侧 | 顺序找首个 dks[i].Time==buyDate（sell_tpsl_new.go:51-56，**顺序匹配**）；扫 buyIdx+1..n-1（**不含买入日**）任一天 (close-buy)/buy >= tp 或 <= -sl | 无 | len==0、buyPrice<=0、买入日未找到 → false | 需 buy.Price + 买入日索引 | script symbols.go:20/43（ss.A止盈止损）；cmd/screen/strategies.go:57 | 历史引用(买入日区间极值)+持仓指标 | condition_baseline 在列 + sell_tpsl_new_test.go（4 用例） | 已迁移（T4 内核委托） |
| risk_fixed_stop | 风控 | A固定止损（sell_trend_volume.go:190，Sell :196-212） | pct ratio | 目录默认 0.05；组件内 0→0.05（:200-202） | 仅当前日：(今收-buy)/buy <= -pct | 1 根 | len==0、buyPrice<=0 → false | 需 buy.Price | 仅 builder | 持仓指标(收益阈值) | condition_baseline 在列 | 已迁移（T4 内核委托） |
| risk_trailing_stop | 风控 | A追踪止损（sell_trailing_stop.go:16，Sell :22-68） | drawdown ratio | 目录默认 0.05；Name 将 0 显示为 0.05（:20-26）；**执行时 Drawdown<=0 直接 false**（:29-31，风险 §5.3） | 顺序找买入日（:43-48）；峰值=max(买入日(含)之后最高收盘)；(今收-峰值)/峰值 <= -drawdown | 无 | len==0、buyPrice<=0、未找到买入日 → false；峰值未高于买价时也可能触发 | 需 buy.Price + 买入日索引 | script symbols.go:22（ss.A追踪止损） | 历史引用(峰值回撤)+持仓指标 | condition_baseline 在列 + sell_trailing_stop_test.go（5 用例） | 已迁移（T4 内核委托） |
| risk_atr_stop | 风控 | A_ATR止损（sell_trend_volume.go:218，Sell :224-247） | period int 天；multiple 倍数 | 目录默认 14/2.5；组件内 0→14、0→2.5 | 仅当前日：今收 < buy - multiple×`f.ATR值(p)`（不含今日窗口） | period+1 根 | n<period+1、buyPrice<=0 → false | 需 buy.Price | 仅 builder | 持仓指标(ATR 距离) | condition_baseline 在列 | 已迁移（T4 内核委托） |
| risk_profit | 风控 | A盈利（sell_profit.go:13） | pct ratio | 目录默认 0.05；组件内 0→0.05（:17-19） | 仅当前日：(今收-buy)/buy >= pct | 1 根 | len==0、buyPrice<=0 → false | 需 buy.Price | 仅 builder | 持仓指标(收益阈值) | condition_baseline 在列 | 已迁移（T4 内核委托） |
| risk_fixed_tp | 风控 | A固定止盈（sell_bluechip_pullback.go:43） | pct ratio | 目录默认 0.05；组件内 0→0.05 | 与 A盈利 公式完全相同（:49-63，重复实现） | 1 根 | 同 A盈利 | 需 buy.Price | 仅 builder | 持仓指标(收益阈值) | condition_baseline 在列 | 已迁移（T4 内核委托） |
| risk_trailing_takeprofit | 风控 | A移动止盈（sell_trend_volume.go:253，Sell :259-294） | activate ratio；retreat ratio | 目录默认 0.05/0.5；组件内 0→0.05、0→0.5 | 从末尾回扫至 dks[i].Time.Before(buy.Time) 为止（**含买入日当天**，:277-284）；峰值=maxProfit 只取 High>buy 的正收益；curProfit(收盘) <= maxProfit×(1-retreat) 时卖 | 无 | len==0、buyPrice<=0 → false；maxProfit<activate → false | 需 buy.Price（含买入日 Time 判断） | 仅 builder；与 A追踪止损 的峰值口径不同（High/含买入日 vs Close，风险 §5.6） | 历史引用(峰值回撤)+持仓指标 | condition_baseline 在列 | 已迁移（T4 内核委托） |
| risk_atr_takeprofit | 风控 | A_ATR止盈（sell_bluechip_pullback.go:69，Sell :75-98） | period int 天；multiple 倍数 | 目录默认 14/2.0；组件内 0→14、0→2.0 | 仅当前日：今收 >= buy + multiple×ATR(p) | period+1 根 | n<period+1、buyPrice<=0 → false | 需 buy.Price | 仅 builder | 持仓指标(ATR 距离) | condition_baseline 在列 | 已迁移（T4 内核委托） |
| risk_holding_days | 风控 | A持仓N天（sell_holding_days.go:16，Sell :22-52） | days int 天 | 目录默认 5；组件内 0→5（:26-28） | 顺序找首个 `!dks[i].Time.Before(buy.Time)`（:39-44）；持仓天数=len-buyIdx-1 >= days | 无 | len==0、未找到买入日 → false | 需买入日 Time 索引 | script symbols.go:24/44（ss.A持仓N天） | 持仓指标(天数) | condition_baseline 在列 + sell_holding_days_test.go（5 用例） | 已迁移（T4 内核委托） |
| risk_holding_days_tail | 风控 | A持仓N天尾盘（sell_holding_days_tail.go:20，Sell :26-75） | days int 天；time 字符串 HH:MM:SS | 目录默认 1/"14:55:00"；组件内 0→days=1、空→"14:55:00"（:26-32） | 持仓天数同上；>=days 后：当日 Time.Format("15:04:05")=="00:00:00" → true（**日线退化恒卖**，:59-61）；否则字符串 >= sellTime（本地时区，风险 §5.7） | 无 | len==0、未找到买入日 → false | 需买入日 Time 索引 | 仅 builder | 持仓指标(天数+时刻判断) | condition_baseline 在列 + sell_holding_days_tail_test.go（10 用例） | 已迁移（T4 内核委托） |
| risk_break_buy_price | 风控 | A买入次日跌破买入价（sell_break_buy_price.go:13，Sell :19-33） | 无 | 无 | 仅当 dks[n-2].DateOnly==buyDate（次日当天）时：今收(厘) < buy.Price（protocol.Price 整数比较） | 2 根 | n<2 → false；次日之后恒 false | 需 buy.Price（隐含"n-2 为买入日"） | 仅 builder | 持仓指标(时刻判断) | condition_baseline 在列 + sell_break_buy_price_test.go（4 用例） | 已迁移（T4 内核委托） |
| risk_no_gap_up | 风控 | A买入次日未高开（sell_no_gap_up.go:13，Sell :19-33） | 无 | 无 | 仅次日当天：今开 <= 昨收（dks[n-2].Close，厘） | 2 根 | n<2 → false；次日之后恒 false | 需买入日（隐含 n-2==买入日） | 仅 builder | 持仓指标(时刻判断) | condition_baseline 在列 + sell_no_gap_up_test.go（4 用例） | 已迁移（T4 内核委托） |
| risk_break_buy_open | 风控 | A跌破买入日开盘（sell_break_open.go:13，Sell :19-39） | 无 | 无 | 今日 DateOnly==buyDate → false（当日不卖）；**逆序**找买入日（:33-37）；今收(厘) < 买入日开盘 | 1 根 | len==0、未找到买入日 → false | 需买入日 Time 索引 | 仅 builder | 历史引用(买入日)+持仓指标 | condition_baseline 在列 | 已迁移（T4 内核委托） |

### 2.1 T2 修订与勘误（2026-10-17，Wave1 落地时以实际代码为准）

- **buy_price**：公式列为浮点元比较（`Close.Float64()`），非 protocol.Price 厘。
- **buy_market_cap**：默认参数时 `Name()` 返回 "Null"；目录参数范围 0–100000（非 0-1e9）；空 K 线在 len==0 早退 false；口径 = `FloatValue().Float64()`（厘→元 ÷1000）÷1e8 = 亿元。
- **buy_limit_up_hit / buy_filter_limit_up**：RiseRate 口径 = (Close-Last)/Last×100；Last==0 退化对开盘价（原台账未记）。畸形数据（Last=0 且 Open=0 且 Close>0 → +Inf）：旧实现 true，迁移后按计划 3.4 "非有限值不触发" → false；baseline 未覆盖该场景。
- **buy_price_above_ma**：实际为浮点元比较（非整数厘比较）；**无 ma<=0 守卫**（该数据路径不可达）；默认 day==0→20（原台账漏记）。
- **buy_volume_double**：组件内另有 min==0→2、base==0→1 归一；基准×ratio 乘法替代旧除法（代数等价，1-ULP 舍入边界理论存在，快照与基线未触发）。
- **buy_volume_shrink**：默认 Days==0→5、Ratio==0→0.8（原台账漏记）。
- **buy_rsi_low**：NaN 分支不可达（len<period+1 先返回）；窗口充足时 RSI 恒 0..100（平价=50）。
- **buy_all / buy_board_cyb / buy_board_kcb / buy_board_bj / buy_etf**：Buy 实际行号 116-117 / 16-19 / 29-32 / 42-45 / 55-57；buy_etf 的 Name 注释误写"北交所"已在代码修正，且不 AddPrefix（protocol.IsETF 直判）。
- **buy_solid_bear**：占比守卫在 :48、注释 :21。
- **新增测试证据**：strategies/buy/buy_price_rule_test.go（28 快照）、strategies/buy/buy_volume_rule_test.go、strategies/buy/buy_predicate_rule_test.go。

### 2.2 T2 Wave2 勘误（2026-10-17）

- **buy_close_above_ma**：默认 Period 0→20（台账漏记）。
- **buy_volume_surge**：组件内默认 0→5（基准天数）、0→1.5（倍数）。
- **buy_rsi_range**：漏记 Period 0→14；min>max 时内核构造失败→false（与旧恒 false 一致）。
- **buy_turnover_min**：语义与台账一致。
- **buy_atr_range**：台账"max<min 仅 min 生效"有误——旧实现两边界同施、恒 false；迁移后 NewRange 构造失败→false 等价；ATR/今收比值换算经 OperandFunc（内核无算术 DSL）。
- **buy_break_high**：默认 0→20（台账漏记）；恰等于 N 天最高、不含今日、High 口径均与旧一致。
- **buy_ma_bull**：相邻 MAValue 严格递增经 NewAnd 委托；单周期退化（p≤0 恒真、仅预热门槛）无内核有效性原语，保留门面并注释——迁移后行为与旧一致。
- **buy_solid_bull**：收阳=ClosePrice Gt OpenPrice（厘级整数比较，价格可表示范围内与旧浮点比较等价）+ OperandFunc 包 A实体振幅占比 Ge 阈值；MinBodyRatio<=0"只要收阳"退化保留门面。
- **buy_boll_lower_volume**：第二条件为缩量（Volume Le PrevAvgVolumeScaled），同序短路。
- **buy_bias_oversold**：ma_bias 注册表公式 (Close/MA-1)×100 与旧合同 (Close-MA)/MA×100 存在 1-ULP 恰等翻转（量化 23679/99320 组合）；胶水 BiasPercent 已改回旧合同公式保证逐位一致，注册表因子口径不动。
- **buy_day_gain_range**：目录默认实为 0/0（0=不限），台账"-5/7"有误；Last=0 且 Open=0 畸形数据 NaN/+Inf 按计划 §3.4 → false（与涨停族一致）。
- **新增测试证据**：strategies/buy/buy_trend_rule_test.go、strategies/buy/buy_meanrevert_rule_test.go。

### 2.3 T2 Wave3（卖出侧）勘误（2026-10-17）

- **sell_back_to_ma**：Sell 实为 sell_meanrevert.go:72-85；规则 = And(MA>0 守卫, 今收(元) Ge MA)。
- **sell_rsi_overbought**：RSIFloat（CalcRSI 浮点口径），严格大于（恰等不触发）；Sell :64-77。
- **sell_rsi_recover**：RSIInt（extend 整数截断口径），Ge 含恰等；整数/浮点两口径差异已在快照固化（[10,11,9]/p2 → 33 vs 33.333）。
- **sell_back_to_boll_mid**：Sell :38-51；中轨即 MA；调用方 cmd/screen/strategies.go 实为 :50。
- **sell_bias_zero**：卖侧确有 ma≤0 守卫；BiasPercent 已为旧合同公式 (Close-MA)/MA×100。
- **sell_zscore_zero**：无 ma 守卫（σ=0 → NaN → false）。
- **sell_back_to_donchian_mid**：中轨=(HHV+LLV)/2，含今日 High/Low 口径（与买侧唐奇安下轨相反，§5.5 属实）；mid≤0 → 无效值；extend.LLV 的零值跳过语义台账补记。
- **sell_big_fall**：RiseRate 严格 < -max（恰等不触发）；Last=0 退化对开盘价。
- **sell_volume_big_bear**：同序短路 And(收<开 厘级整数 → RiseRate ≤ -fallPct → 今量 > 前5日均量(不含今日)×ratio)；旧 NaN RiseRate 分支因阴线前置检查不可达，迁移无差异。
- **新增测试证据**：strategies/sell/sell_rule_test.go（9 快照，双向对拍）。
- **临时产物清理**：/tmp/p7-migration-backup.patch 已删除。

### 2.4 T3 勘误（2026-10-18）

- **附录 B 19 项状态全部落账（buy 13 + sell 6）**：全部 Buy/Sell 方法体已改为构造 `strategies/rule` 内核规则并 `Eval(rule.NewInput(...))` 委托（买入侧 buy=nil、卖出侧传 &buy），门面保留类型/字段/Name()/参数归一与默认值兜底。其中 13 项为先前批次完成，本轮逐文件核实代码态并一并落账：buy_macd.go 反转/负数/正数/连涨、buy_trend_volume.go 金叉/零轴上方/RSI超卖回升、sell_macd.go 反转、sell_trend_volume.go 死叉DIF负/收盘跌破均线、sell_bluechip_pullback.go 跌破短期均线/均线死叉、sell_ma.go LongMADown。
- **buy_recent_gain_below 行号勘误**：Buy 实为 buy_price.go:190-211（台账记 :154-177）；公式列 `riseRateNDays` 实定义于 buy_trend_volume.go:313-325（台账误记 buy_price.go:125-145），迁移时已删除该函数（唯一代码引用 buy_price.go:200，其余为文档与内核溯源注释）。内核 `GainPercent`（operand_temporal.go:143-162）与旧函数公式次序逐位一致；GainPercent 自身 n<days+1 → Invalid 已覆盖旧 len<days+1 预检，A近N日涨幅小于无需显式 MinKlines。
- **GainPercent 构造期校验差异（刻意改进）**：days<=0 → 构造 err → Buy false；旧 riseRateNDays 对 days<=-2 会索引越界 panic。其余无效口径（startPrice<=0 → Invalid 不触发）与旧 ok=false 保守一致。
- **buy_bottom_up 文件互换（台账 :88 与风险 §15 同错）**：A底部抬升 类型实为 buy_bottom_up.go:17（Buy :29 起），findTrendUpPoints/isTrendUpPoint/isBottomUpShape 在 buy_底部抬升.go:23/:62 起；台账两处把两文件互换。迁移形态：`NewMinKlines(60)` 预热门 + `NewRuleFunc` 逃逸口在 in.Klines 上复刻专用形态逻辑（findTrendUpPoints → 低点/高点数量判定 → isBottomUpShape）+ `NewAnd`。
- **内核溯源注释指向已删函数**：operand_temporal.go:138 注释引用旧 `riseRateNDays`（已随迁移删除）。内核冻结不改，此处存档说明。
- **maUp 帮助函数现状**：buy 侧定义 buy_volume.go:310（4 参含 minSlope），2 调用方保留（buy_trend_volume.go:61 A均线向上、buy_shanghai_index.go:56 A上证N日均线向上，均不属 T3 范围）；sell 侧定义 sell_ma.go:61（3 参）经 LongMADown 内核 MAIncre 委托后零调用方成孤儿，已列入 §3.3 待删（删除需用户确认）。
- **buy_macd_reversal / buy_macd_rising OperandFunc 包装**：反转的"昨柱+MinDiff"算术经 OperandFunc 保持旧运算次序（内核无算术 DSL）；连涨的 lookback 峰值校验经门面用 f.RisingStreak 定位 startIdx + WindowMin 在截断前缀 dks[:startIdx+1] 上求值；两者均显式 NewMinKlines（MACD 族短前缀仍给有限值，无效值兜不住）。
- **验证状态披露（未验证内容）**：宿主 go/docker 执行通道故障（RunCommand 路由错、MCP Shell 审批故障、后台沙箱 dyld 架构崩溃），本轮以 GetDiagnostics 全量检查 7 个改动文件（零诊断）为替代编译检查，非完整验证。探针 TestT3bProbe（strategies/buy/t3b_probe_test.go，6 个旧实现 oracle × 32 种子 × 参数网格对拍 + 38 边界用例）与三条 docker 验证（buy 包测试；rule+buy+sell+lab 全量；gofmt -l）延后，通道恢复即补跑。相关测试文件（buy_macd_rule_test.go、sell_ma_rule_test.go、macd_shape_migration_test.go、sell_macd_test.go 等）本轮未重复运行。**（2026-10-18 通道恢复后已补跑完成，探针转为永久对拍，见 §2.5 T4 勘误）**

### 2.5 T4 勘误（2026-10-18）

- **附录 C 14 项状态落账**：全部 Buy/Sell 方法体已改为构造 `strategies/rule` 内核规则并 `Eval(rule.NewInput(...))` 委托（卖出侧传 `&buy`），门面保留类型/字段/Name()/参数归一与默认值兜底。新增两文件对拍：strategies/sell/sell_t4_position_test.go（8 项价格类：risk_profit / risk_fixed_tp / risk_fixed_sl / risk_atr_stop / risk_atr_takeprofit / risk_trailing_takeprofit / risk_trailing_stop / risk_takeprofit_stoploss）与 strategies/sell/sell_t4_holding_test.go（6 项时序/定位类：risk_holding_days / risk_holding_days_tail / risk_break_buy_price / risk_no_gap_up / risk_break_buy_open / sell_macd_down_after_buy）。测试 helper 统一 t4 前缀，oracle 逐语句内联旧实现，逐前缀对拍 + triggered 守卫，断言先按旧实现固定、迁移后原样通过。
- **通道恢复后首次真实编译，暴露并修复 3 处既存缺陷**（此前 T2/T3 以 GetDiagnostics"零诊断"代替编译，覆盖不到跨包构建期类型错误与 NaN 边界）：
  1. buy_rsi.go:75 `rule.RefOperand(1, rule.RSIFloat(p))` 用于单值上下文——`RefOperand` 返回 `(Operand, error)`，编译器报"assignment mismatch"，整个 buy 包无法编译（连带 sell 测试构建失败）；已改为显式取错误并短路 `return false`。
  2. sell_break_open.go:26 `rule.NewRuleFunc(...)` 单值赋值——返回 `(Rule, error)`，编译失败；已改为 `notBuyDay, err := ...` + 错误短路。
  3. buy_volume.go `MAUp` 斜率判定语义回归：旧式 `slope>0 && !(slope<minSlope)` 对 `minSlope=NaN` 恒真（视为无涨速下限）；迁移改用 `slope Ge Literal(FloatValue(NaN))` 后因内核"无效右操作数不触发"变 false，违反 internal/lab/temporal_condition_test.go「weakest slope threshold」基线（`{math.NaN(), true}`）。严格 Compare 无法表达该退化语义，已以 `NewRuleFunc` 复用内核 `MASlope` 操作数原样还原旧表达式。
- **台账公式列与实现口径核实（无行为差异）**：risk_trailing_stop 台账写 `(今收-峰值)/峰值 <= -drawdown`，迁移取 `(峰值-今收)/峰值 >= drawdown`——IEEE 减法/除法符号对称逐位等价；risk_takeprofit_stoploss 台账写 `dks[i].Time==buyDate`，迁移为日期串相等——对拍夹具 `buy.Time` 恒取 `ks[idx].Time`，两口径结果一致；sell_macd_down_after_buy 台账记 `f.FallingStreak` 于 factor/macd_shape.go:84-91，实际现位于 factor/temporal.go:38-44（路径过时，函数未变）。
- **A_ATR 窗口口径**：`f.ATR值(p).Value` 于 `len(dks)<p+1 → NaN`，TR 窗口 i 从 `len-p` 到 `len-1`（prev=`dks[i-1].Close`）；对拍边界以「四价同值序列 TR=|今收-昨收|」构造可控 ATR（14×10000+2000 跳变 → ATR=2000/14）。
- **验证（2026-10-18，docker golang:1.25-alpine）**：`go build ./...` 通过；`go test ./... -count=1` 全 17 包全绿（含 T4 对拍 14 项、internal/lab、strategies/buy/sell/rule）；`gofmt -l strategies/buy strategies/sell` 仅 1 处既存未格式化文件 strategies/buy/buy_downside_stabilize_test.go（非本次改动，未触碰）。t3v 延后的三条验证（buy 包；rule+buy+sell+lab 全量；gofmt -l）随之完成：临时探针 strategies/buy/t3b_probe_test.go（TestT3bProbe，自带 `t.Fatalf` 断言，非仅打印）已转为永久对拍 strategies/buy/buy_ma_rule_test.go（TestBuyT3B组件对拍，覆盖 A站上N日均线 / MAUp / RSI拐头 / A回踩N日均线 / A底部抬升 / A近N日涨幅小于 六组件），buy 包测试原样通过。

### 2.6 T5 收口（2026-10-18）

- **范围与文件核实**：计划 T5 列 spec.go／catalog.go／filter.go／strategy_spec.go；实际 `internal/lab/builder/` 仅 5 个 Go 文件（filter.go、spec.go、catalog.go、template.go、catalog_test.go），**无 strategy_spec.go**——简单模式 StrategySpec 定义在 template.go，规划文件名为笔误。
- **builder 是薄映射层；因子过滤门面已补齐内核委托（评审修正）**：三类节点各自复用唯一实现来源，builder 本身不承载任何公式：
  - `condition` 节点经 [spec.go](file:///Users/injoy/项目/strategy-tail/internal/lab/builder/spec.go#L322-L332) 的 `buyFactories`／`sellFactories` 按 ID 映射到对应 buy／sell 组件构造器（这些门面已委托 rule 内核），spec.go 仅做版本／名称／树规模（8 层 64 节点）／角色／参数校验，不含阈值公式。
  - `factor` 节点经 [filter.go](file:///Users/injoy/项目/strategy-tail/internal/lab/builder/filter.go#L95-L115) 的 `FactorFilterSpec.Build` 仍返回 `sb.A因子过滤`，数值由已绑定的 `f.BuildWithData` 提供；门面经 `rule.EvalFactorRange` 直接复用有限区间的 `Range.contains`（`NewFactorRange` 编译接口保留），Go 无穷边界使用显式兼容规则。builder 的 operator→区间映射（gte/lte/between，单边界限 ±1e9）不变。
  - 分组节点复用 `sb.And/Or` 与 `ss.And/Or`（条件树分组载体，非组件）。
  - 结论：评审前 `A因子过滤.Buy` 仍直接比较阈值，不能以 builder 无公式推断已经统一；本次已删除该门面的独立区间判断，普通 condition 与 factor 均进入 rule 内核。
- **合同测试齐备且随全量回归通过**：builder_acceptance_test.go（TestBuilderTemplateTradesMatchCLI、TestBuilderGroupingAndIndependentStop、TestBuilderDraftRunAndSnapshotIsolation）、builder_factor_test.go（TestBuilderFactorConditionsShareStrategyRules、TestBuilderFactorValidation、TestBuilderFactorTreeAndPersistence、TestBuilderFactorRunKeepsFrozenSnapshot）、condition_equivalence_test.go（TestConditionFactorEquivalence）、builder/catalog_test.go（TestBuilderCatalogFactoryCoverage，覆盖 73 工厂）、builder_test.go（Spec ValidateMode/Tree、CombinatorSemantics、Store CRUD/FailClosed、TemplateMACDEquivalent、TemplatesCatalog、BuilderAPI）。
- **验证（2026-10-18，docker golang:1.25-alpine，宿主沙箱覆盖执行）**：`go test ./internal/lab/... ./strategies/rule/... -count=1` → internal/lab（14.246s）、internal/lab/builder、internal/lab/factorstats、internal/lab/script、strategies/rule **全部 ok**。73 项目录合同未变（condition_baseline 未重生成）；无前端改动，无需新增表达式编辑器。
- **T5 验收对照**：73/73 可运行（catalog 工厂覆盖测试通过）；组件目录与研究因子目录合同未变；前端无需新增表达式编辑器——全部满足，T5 完成。

## 3. 未入目录清单

**目录内基线**：73 个工厂引用 44 个 buy 类型 + 29 个 sell 类型（internal/lab/builder/catalog.go:533-1011）。

### 3.1 迁移（T6；因子等价基线已冻结，internal/lab/factor_migration_test.go「21 个目录外策略」）

| 符号 | 定义 | 引用证据 | 说明 |
|---|---|---|---|
| A长期均线多头 / A收盘价站上长期均线 / A浅回调至均线 / A企稳信号 / A缩量企稳 / A年线向上 / A非连续下跌 | buy_bluechip_pullback.go:13,53,89,164,207,241,278 | factor_migration_test.go:31-40 | 蓝筹回调族，基线冻结待因子化 |
| A指数站上MA / A上证N日均线向上 | buy_index_filter.go:74; buy_shanghai_index.go:14 | factor_migration_test.go:44-47 | 需指数行情，Lab 暂不接入（catalog.go:498-500） |
| A突破N天新高 / A近N天最高价 | buy_price.go:217,243 | factor_migration_test.go:49-50 | 与 buy_break_high 重复但基线已冻结 |
| MACD顺滑 | buy_macd.go:245 | factor_migration_test.go:51；macd_shape_factor_test.go:28-29 | 生产零引用，仅等价测试 |
| MACD负柱缩短 / MACD平滑 | buy_macd.go:536,173 | macd_shape_migration_test.go:30-37；macd_shape_factor_test.go:27,30 | 因子等价迁移对象 |
| A乖离率小于 | buy_trend_volume.go:233 | 全仓零引用 | 与 A乖离超卖 语义重叠，T6 一并定夺（待核实） |

### 3.2 兼容保留

| 符号 | 定义 | 引用证据 | 保留原因/语义差异 |
|---|---|---|---|
| buy.And/Or/Not、sell.And/Or | buy_logic.go:10,53,94；sell_logic.go:10,38 | spec.go:333,342,370,379；runner.go:150-170；common.go:82,98；symbols.go:31-33,48-49 | 条件树分组与脚本组合核心载体，非"组件" |
| buy.Strategy | buy_strategy.go:11 | cmd/screen/screen.go:407,410；internal/lab/runner.go:736 | 命名包装，供 Explain 识别 |
| A现价（=A价格别名） | buy_price.go:19 | common.go:84,106；cmd/backtest/main.go:85,103,122；internal/lab/macd_backtest.go:112 | 历史别名，调用方可平移到 A价格 |
| SellAt / SellRSI / SellTPSL | sell_at.go:17,58；sell_rsi.go:14 | 零 Go 引用；仅 catalog.go:504-506 字符串、sell_rsi_test.go、计划文档:184 | 计划明确"不自动转换或删除，列为旧接口兼容例外"；扩展 Seller 签名（分钟线回调）、旧 SellTPSL 会回看买入日前历史 |
| A阴线收回 | buy_tail_pullback.go:25 | symbols.go:35；strategies/script/matrix.go:22-43；script/testdata/strategy.go:10；lab/presets_test.go:59-80 | 脚本符号在用 |
| A因子过滤（buy） | factor_filter.go:14 | builder/filter.go:95-112；spec.go:357（sell 版内嵌 buy 版）；symbols.go:40 | 条件树因子节点实现载体 |
| A因子TopN | factor_top.go:16 | symbols.go:41；lab/walk_test.go:35-44；factor_e2e_test.go:29；core/cross_section.go:8-23 | 组合研究路径，计划:184 保留 |
| N天前符合 | buy_window.go:64 | cmd/screen/strategies.go:24,38,68 | 实时筛选在用；目录不入条件树（catalog.go:501） |
| A通达信倍量 | buy_volume.go:15 | cmd/screen/strategies.go:24,38,68；factor_migration_test.go:48 | 实时筛选在用 + 基线冻结 |
| A指数多头排列 | buy_index_filter.go:15 | cmd/screen/strategies.go:61；cmd/backtest/main.go:25；factor_migration_test.go:42 | 实时筛选 + 受保护草稿在用 |
| MACD转红 | buy_macd.go:620 | cmd/backtest/main.go:125（受保护草稿） | 草稿策略在用 |

### 3.3 无引用待删（rg 全仓含测试零引用，删除前仍需用户确认）

| 符号 | 定义 | 备注 |
|---|---|---|
| BuyCloseBelowMA / BreakMA | buy_volume.go:200,225 | 仅 catalog.go:481-482 字符串；无测试 |
| A阳线 | buy_volume.go:531 | 仅 catalog.go:476；与实体阳线 0 等价 |
| A均线向上 | buy_trend_volume.go:39 | 仅 catalog.go:474；与 buy_ma_up 重复 |
| 近N天符合类型 + A近N天符合() | buy_window.go:25,10 | 仅自测试；N天前符合另有实时用途不受影响 |
| A价格小于 / A价格大于 / A流通市值小于 | buy_price.go:11,15；buy_ma.go:10 | 仅定义处 |
| sell.MACD 参数结构体 | sell_macd.go:11 | 全仓无匹配 |
| sell.maUp（帮助函数） | ~~sell_ma.go:61~~ | **T6 已删除**（2026-10-18）：T3 后 LongMADown 改内核 MAIncre 委托，rg 全仓含测试零调用方，计划 T6"已无引用的私有算法可删除"授权；随删 `f` 未用导入 |

### 3.4 待核实（零生产引用但有测试/产品意图牵连，删除需确认）

| 符号 | 定义 | 证据 | 疑点 |
|---|---|---|---|
| MACD正数缓降最低点 | buy_macd.go:98 | 仅自身 + catalog.go:493 | catalog 标"本次未接入" |
| A下跌企稳倍量 | buy_downside_stabilize.go:38 | 仅自测试 11 例 + catalog.go:484 | 有完整行为测试但未入迁移基线 |
| A底顶部抬升 | buy_底部抬升.go:18 | 仅自测试 + catalog.go:485 | catalog"语义待确认"；helpers 与 A底部抬升 共用 |
| A时间止损 / A缩量滞涨 | sell_trend_volume.go:139；sell_bluechip_pullback.go:148 | 仅自身 + catalog.go:507-508 | catalog"语义待确认/未接入" |

### 3.5 专项清单

- **Yaegi 脚本符号（internal/lab/script/symbols.go:23-69）**：buy: And, Or, Not, A阴线收回, A价格, A流通市值, A过滤涨停, A因子过滤, A因子TopN, MAUp, A均线多头排列；sell: And, Or, A持仓N天, A止盈止损；factor: N日动量/N日斜率/N日波动/N日振幅/N日高低位/K值 + Build。
- **cmd/screen 实时筛选（strategies.go + screen.go:407-410）**：目录内同类型直用（A科创板/创业板/北证板/流通市值/涨停/布林下轨/RSI/MAUp/MACD反转、sell.A回到布林中轨）；目录外：N天前符合、A通达信倍量、A指数多头排列、buy.And/Or、buy.Strategy、A全部（screen_test.go:55）。
- **common.go 组装（:82-120）**：MACDBuyer=sell 侧 buy.And{A流通市值, A现价, A过滤涨停, MACD反转, MACD负数, A现价大于N日均线, MAUp×2}；MACDSeller=sell.Or{MACD反转}；MACDBaseBuyer、BaseBuyer{A价格, A过滤涨停}。全部目录内类型（A现价为别名）。
- **私有帮助函数**：buy 侧全部有引用，无可删候选（buy：maUp buy_volume.go:314（buy_shanghai_index.go:56、buy_trend_volume.go:61 调用）、countDoubleVolume/previousDoubleVolumeIndex、defaultMACDParams、findTrendUpPoints 等 buy_底部抬升.go:62-230、isBottomUpShape、stabilizeSignal/shrinkStable；sell：defaultMACDParams sell_trend_volume.go:174）。**sell 侧 maUp（sell_ma.go:61）为唯一零引用私有帮助函数，T6 已删除；buy 侧 riseRateNDays 已随 T3 迁移删除（见 §2.4）**。
- **脚本/实时筛选与 73 项共用类型**：A价格↔buy_price、A流通市值↔buy_market_cap、A过滤涨停↔buy_filter_limit_up、MAUp↔buy_ma_up、A均线多头排列↔buy_ma_bull、A持仓N天↔risk_holding_days、A止盈止损↔risk_takeprofit_stoploss、buy/sell 的 And/Or↔条件树分组节点（spec.go:333-379）。脚本独占目录外：A阴线收回、Not、A因子过滤、A因子TopN。cmd/screen 不经目录工厂直接实例化，3 个类型无目录对应。
- **检索方法与排除项**：`^type [A-Z\p{Han}]`、`^func [A-Z\p{Han}]` 列符号；rg 全仓含 _test.go；已排除 Yaegi 符号注册、catalog unavailable 字符串 ID、reflect（仅 symbols.go/probe 测试）、文档提及；core/diagnoser 目录不存在（实际 core/diagnose.go:22 仅注释提及）；internal/researchrun、internal/portfolioresearch、cmd/lab 对 strategies/{buy,sell} 零引用。
- **限制**：前端 JSON/静态资源中的组件 ID 字符串未逐一扫描（不影响 Go 符号结论）；cmd/backtest/main.go 受保护草稿仅列引用未评估。

### 3.6 T6 收口（2026-10-18）

- **未入目录项归属与证据（逐项归类）**：
  - §3.1 迁移对象（21 项目录外策略）：已全部"因子等价基线已冻结"——internal/lab/factor_migration_test.go `TestAdditionalConditionFactorEquivalence` 冻结 21 键信号哈希，strategies/buy/macd_shape_migration_test.go `TestMACDShapeMigrationBaseline` 冻结 MACD 形态族（窗口 1/4/40/400）。其中 MACD 形态族另有 `TestMACDShapeFactorConditions` 以"旧组件 == 因子过滤/And 组合"逐前缀对拍证明因子等价；蓝筹回调族数值已由 `f.A均线*` 承担（[buy_bluechip_pullback.go](file:///Users/injoy/项目/strategy-tail/strategies/buy/buy_bluechip_pullback.go#L38-L47)）。这些项不在 73 项目录内、无 Lab 运行入口，本轮以冻结基线保证零行为漂移，**不强行注入账内 rule 委托**（其计算已复用 factor，再包一层 rule 只会制造与因子等价的第二套实现，违反计划"不制造第二套规则逻辑"）。
  - §3.2 兼容保留（11 类）：脚本组合载体 buy/sell And/Or/Not、实时筛选与受保护草稿引用、别名（A现价）与目录载体（A因子过滤/因子TopN、N天前符合），逐条列明保留原因。
  - §3.3 无引用待删（8 项）：**sell.maUp 已随 T6 删除**（零引用私有帮助函数，计划 T6"已无引用的私有算法可删除"授权）；其余导出旧类型（BuyCloseBelowMA/BreakMA/A阳线/A均线向上/近N天符合类型/A价格小于·大于/A流通市值小于/sell.MACD 结构体）按计划"旧公开类型只有在仓库调用、脚本符号及用户已保存脚本兼容性均有证据时才提出独立删除决定"**暂留**，列为旧接口兼容例外。
  - §3.4 待核实（4 项）：零生产引用但有测试/产品意图牵连（MACD正数缓降最低点、A下跌企稳倍量、A底顶部抬升、A时间止损/A缩量滞涨），保留待独立变更，不计作已统一。
  - §3.5 专项：Yaegi 脚本符号、cmd/screen 实时筛选、common 装配、私有帮助函数全部有归属与证据。
- **无新规则逻辑分叉**：buy/sell 组件门面（T2–T4）与 builder（T5）唯一求值来源为 `strategies/rule` 内核与 `strategies/factor` 因子；目录外组件复用同一 factor 数值（如蓝筹族 `f.A均线`）或保留冻结基线，未新增第二套公式。
- **旧接口兼容例外（不计作已统一，按计划保留）**：SellAt／SellRSI（扩展 Seller 签名、分钟线回调）与旧 SellTPSL（会回看买入日前历史）——零 Go 引用，仅 catalog.go:504-506 字符串与文档；A因子TopN（横截面组合研究路径）；cmd/backtest/main.go（受保护草稿，经兼容门面继续编译，未改写）。
- **验证（2026-10-18，docker golang:1.25-alpine，宿主沙箱覆盖执行）**：
  - `go build ./...` → **BUILD_OK**；`go vet ./...` → **VET_OK**。
  - `go test ./... -count=1` → **20 包全绿**（0 FAIL），含 cmd/screen（实时筛选离线样本 0.052s）、internal/lab/script（Yaegi 原生对拍 interp_test.go / probe_yaegi_binary_test.go 0.986s）、strategies/buy（0.260s）、strategies/sell（0.034s）、strategies/rule（0.003s）、internal/lab（14.199s）。
  - `gofmt -l strategies/sell strategies/buy` → 仅既存未格式化文件 buy_downside_stabilize_test.go（非本次改动，未触碰）。
- **T6 验收对照**：每个未入目录项有归属和证据；Go 构建、诊断（vet）通过；Yaegi 原生对拍通过；实时规则离线样本（cmd/screen 测试）通过；buy／sell 包保留必要门面且无新规则逻辑分叉——全部满足，T6 完成。

## 4. 基线记录（2026-10-17 实测，HEAD=261cb71，工作区仅 docs 改动）

| 检查 | 命令 / 环境 | 结果 |
|---|---|---|
| 全仓测试 | `docker golang:1.25-alpine`，TZ=Asia/Shanghai、CGO_ENABLED=0、`go test ./... -count=1` | **EXIT=0，18 包 ok，无 FAIL** |
| 构建 | 同环境 `go build ./...` | OK |
| 静态检查 | 同环境 `go vet ./...` | OK |
| 前端 | `node --test internal/lab/web/*.mjs` | **67/67 通过** |
| 部署脚本 | `python3 tests/deploy_test.py` | 7/7 OK |
| 工作区 | `git diff --check` | 干净（仅 CONTEXT.md、docs/README.md 修改与规划文档未跟踪，均与本迁移无关，保留） |
| 基线夹具 | internal/lab/testdata/condition_baseline.json（73 键）、macd_shape_baseline | 保留，不重新生成；非默认参数与反例夹具在 T2–T4 各批补齐，迁移期 oracle 为测试内参考实现快照，预计随各批对拍完成在 T7 前精简 |

- 本机 go 工具链不可用（goutil/oss 无 darwin 实现），Go 检查一律按 MEMORY 约定走 Docker。
- 竞态检测：本机 `-race` 不可用（MEMORY 2026-09-15 记录），Docker alpine 无 cgo 亦不支持；T1 引入共享内核后如环境允许再补，否则按计划第 7 节披露未检查。

## 5. 语义风险与对拍要点

以下为本次盘点中独立核对源码后认定的易错语义点（均附 file:line 证据）：

1. **A涨停/A过滤涨停 的 Name 重复**：`A涨停.Name()` 返回"过滤涨停"（strategies/buy/buy_price.go:95-97），与 `A过滤涨停.Name()`（buy_price.go:65-67）完全相同。迁移后若以 Name 诊断/落日志，两者无法区分；且涨停判定阈值写死 9.8/19.8，与真实涨停价（四舍五入）不同源。
2. **A实体阳线 0 阈值与目录描述冲突**：目录/工厂默认 `MinBodyRatio=0` 并声称"0=只要收阳"（catalog.go:744-747 描述），但组件实现把 0 替换为 0.5（buy_trend_volume.go:440-443）；而 `A实体阴线` 对 0 的处理是"只要收阴"（buy_实体阴线.go:34-38）。同一"0"语义在阳线/阴线两侧不同，迁移到通用规则时若统一为"0=不启用"会改变 A实体阳线 现网行为（condition_baseline 会立刻对拍失败）。
3. **A追踪止损 Drawdown=0 的 Name 与执行不一致**：Name 把 0 显示为默认 0.05（sell_trailing_stop.go:20-26），执行却是 `Drawdown<=0 → return false`（:29-31），即 0=关闭而非默认。迁移时"0 取默认"与"0 关闭"二选一都会改语义，必须保留执行语义并以 condition_baseline 为准。
4. **卖出侧 RSI 与买入侧 RSI 不同源**：`ARSI恢复` 用整数 `A整数RSI值`（sell_meanrevert.go:212-213，extend.RSI 0-100），而 `buy_rsi_low`/`A_RSI超买`/`RSI区间` 用浮点 `RSI值`（factor/technical.go:170-201）。同一 period 下两者数值可差出整数个单位，统一为一种 RSI 前必须先对拍阈值边界（尤其整数 RSI 的截断方向）。
5. **唐奇安窗口"含/不含今日"口径相反**：`buy_donchian_lower` 用 `dks[:n-1]`（不含今日，buy_meanrevert.go:160-162），`sell_back_to_donchian_mid` 的 HHV/LLV 却用完整 `dks`（含今日，sell_meanrevert.go:172-175）；`buy_break_high` 也不含今日（buy_trend_volume.go:375-379），而目录外旧件 `A突破N天新高`（buy_price.go:217）含今日。时序窗口原语必须显式声明"是否含今日"。
6. **买入后峰值/峰值回撤族三种口径并存**：A追踪止损=买入日(含)之后的最高**收盘**（sell_trailing_stop.go:50-57）；A移动止盈=回扫到 `Time.Before(buy.Time)` 为止的最高**最高价**且含买入日当天、maxProfit 只记正收益（sell_trend_volume.go:277-290）；A止盈止损/固定止损/盈利=仅当日收盘。统一为"峰值回撤"原语时需参数化：价格源（Close/High）、是否含买入日、是否只记正收益。
7. **买入日索引定位算法不一致**：A止盈止损与 A追踪止损为**顺序首次匹配**（sell_tpsl_new.go:51-56；sell_trailing_stop.go:43-48）；A持仓N天/尾盘为首个 `!Time.Before`（sell_holding_days.go:39-44；sell_holding_days_tail.go:52-57）；MACD买入后连跌与 A跌破买入日开盘为**逆序匹配**（sell_macd.go:131-137；sell_break_open.go:33-37）；A买入次日跌破买入价/未高开不定位、直接假设 `dks[n-2]` 为买入日（sell_break_buy_price.go:23-25；sell_no_gap_up.go:23-25）。同一天多次买入信号时首次/末次口径会分叉。
8. **A持仓N天尾盘 的时区与日线退化**：sell 判定用 `dks[n-1].Time.Format("15:04:05")`（本地时区，sell_holding_days_tail.go:55-61），`00:00:00`（日线数据无时刻）直接视为尾盘成立；分钟线下才做 `>= "14:55:00"` 字符串比较。迁移后若改为结构化时刻判断，必须保留"日线退化"分支与本地时区行为，否则条件基线哈希会变。
9. **零值默认的"0 会被覆盖"陷阱（负数阈值不可表达）**：`A乖离超卖` 0→-7（buy_meanrevert.go:70-74）、`AZScore超卖` 0→-2（:116-120）、`A单日跌幅大于` 0→7（sell_trend_volume.go:122-124）、`A布林下轨`系列 0→默认 multiple/std。这些组件无法通过 0 表达"阈值为 0"；而 `A单日涨幅范围` 用 `!=0` 判定（buy_price.go:193-197）可以传负值。通用规则需区分"未传/0/负值"。
10. **maxDays/MaxLookback 的"小于即不限"语义**：`MACD负数` maxDays<=0 或 <minDays 时不限（buy_macd.go:330-334）、`MACD连涨` 同（:441-446）、`buy.MACD反转` maxLookback<=0 或 <min 不限（buy_macd.go:35-40）。若迁移为对称区间 [min,max] 原语，`max=0` 必须显式映射为"正无穷"。
11. **MACD反转 的 SeriesPosition 平价归零**：`SeriesPosition` 定义"末值==窗口最低返回 0"，且 hi==lo（全平）也返回 0（factor/macd_shape.go:20-22）。`buy.MACD反转` 判 `==0` 时全平窗口也算"昨日为谷底"（buy_macd.go:49-64）；`sell.MACD反转` 是直接扫窗口最大值并要求严格相等（sell_macd.go:56-63），平价同样命中。但 buy 侧基于比例、sell 侧基于极值扫描，二者对 NaN/浮点误差的敏感度不同，不能简单互换实现。
12. **整数价格与浮点比较混用**：`buy_price_above_ma`、`buy_donchian_lower`、`risk_break_buy_price`、`risk_no_gap_up`、`risk_break_buy_open` 走 `protocol.Price`（厘，int64）比较（buy_volume.go:186-191；buy_meanrevert.go:158-163；sell_break_buy_price.go:27-31；sell_no_gap_up.go:27-31；sell_break_open.go:38），而 `buy_close_above_ma`、`sell_back_to_ma`、`A回到布林中轨` 等走 Float64 元比较。同一"价格 vs 均线"语义存在厘/元、整数/浮点两套实现，统一时截断方向必须与旧实现对齐。
13. **MAUp 与 LongMADown 用不同斜率因子且 NaN 极性相反**：MAUp 用相对斜率 `MA最弱日斜率`（buy_volume.go:276-282），LongMADown 用绝对增量 `MA最弱日增量`（sell_ma.go:27-29）；两者在窗口不足时均返回 NaN，但 buy 侧 NaN→不买入，sell 侧先拦截 `len<250+lookback`（sell_ma.go:21-24）避免 `!NaN>0` 误判为卖出。迁移后若预检顺序改变，LongMADown 的"数据不足"行为会翻转。
14. **A回踩N日均线 的"触及"判定逐日重算均线**：近 touchDays 天内"最低价<=当日 MA"中的 MA 是按 `dks[:i+1]` 逐日重算的（buy_pullback_ma.go:89-97），不是用同一条固定均线；支撑条件 MA(SupportPeriod) 的取值末端被钳制为 `max(windowStart-1, supportPeriod-1)`（:104-107）。通用规则若用"固定均线+历史引用"表达会改变触及/支撑的判定值。
15. **A底部抬升 硬编码 60 根预热**：buy_底部抬升.go:27 直接要求 len>=60，且趋势点算法复用 buy_bottom_up.go:17-91（前 window 根/后 window 根均需更高，末段数据不足时 false）。它是 73 项中唯一窗口外硬编码预热的组件，迁移到"时序窗口"原语时需保留固定 60 的预热门槛。

## 6. T7 最终验收与交接（2026-10-18）

### 6.1 验证矩阵与命令结果

docker `golang:1.25-alpine`，宿主沙箱覆盖执行，TZ=Asia/Shanghai、CGO_ENABLED=0、GOTOOLCHAIN=local：

| 检查 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build ./...` | **BUILD_OK** |
| 静态检查 | `go vet ./...` | **VET_OK** |
| Go 全仓测试 | `go test ./... -count=1` | **20 包全绿，0 FAIL** |
| 前端 | `node --test internal/lab/web/*.mjs` | **67/67 通过**（0 fail，143.8ms） |
| 部署脚本 | `python3 tests/deploy_test.py` | **Ran 7 tests，OK** |
| 工作区 | `git diff --check` | **exit 0**（无空白错误） |
| 格式化 | `gofmt -l strategies/sell strategies/buy` | 仅既存未格式化文件 buy_downside_stabilize_test.go（非本次改动） |

逐层对照计划 §7：
- **数值与规则**：strategies/rule 单测（value/compare/operand/temporal/position）随全绿；精度/单位/预热/输入不可变/无效数据边界用例于 T1 建立。
- **原组件信号**：TestConditionFactorEquivalence、condition_baseline（73 键）、macd_shape_baseline 均**未重生成**并通过；非默认参数与手写边界见 T2–T4 各 `*_rule_test.go`。
- **组合与因子条件**：TestBuilderFactorConditionsShareStrategyRules、混合树、独立风控与缺省参数；旧 Go Not 单列（buy_logic）。
- **逐股执行**：core/backtest_equiv_test.go `TestDo重构与原版等价` 通过；该测试对拍引擎实现，T4 对拍仅覆盖组件信号，二者都不能替代旧／新规则的逐笔交易对拍。
- **账户执行**：`TestAccountCashBudgetLotsAndDailyValuation` 等（先卖后买、资金不足、跨年、缺日/分钟、Virtual、取消）通过。
- **保存及报告**：TestBuilderDraftRunAndSnapshotIsolation、TestBuilderFactorRunKeepsFrozenSnapshot、账户报告往返、历史报告与策略 JSON 夹具通过。
- **兼容入口**：TestBuilderTemplateTradesMatchCLI、MACD 默认规则对拍、Yaegi LoadScript/BuyerParity（interp_test.go / probe_yaegi_binary_test.go）、实时筛选 cmd/screen 离线输入通过。

### 6.2 收口核查

- **重复生产算法是否真正删除**：T2–T4 迁移已将旧逐语句公式改为委托 rule 内核（原公式删除）；T3 删除 buy 侧 riseRateNDays；T6 删除 sell 侧零引用私有 maUp。rg 复核 buy/sell 无第二套公式；目录外件复用 factor 数值（如蓝筹族 `f.A均线`）或以冻结基线保留。
- **所有旧 ID 是否映射**：73 项目录 ID 全部映射到工厂（builder catalog 工厂覆盖测试通过），condition_baseline 73 键哈希不变。
- **持仓指标是否误入研究目录**：`strategies/rule` 无 Catalog/Register 引用，未向 factor.Catalog 注册；factor 包仅含行情数值因子（无持仓指标）。满足计划 §3.1"持仓指标不自动注册到研究用 factor.Catalog"。
- **是否新增隐式回退**：无。门面迁移在构造期报错即返回 false/error，未添加"求值失败自动回退旧实现"开关（计划 §8 禁止）。
- **是否意外改动真实数据／冻结产物**：`git status` 仅含迁移代码（strategies/buy、strategies/sell、新建 strategies/rule 与 `*_rule_test.go`）与文档（docs/plans、MEMORY、CONTEXT、docs/README）；无 data/lab/strategies 改动，无 testdata 基线（condition_baseline.json、macd_shape_baseline.json、factor_migration_baseline.json）改动，未改旧报告/候选/冻结研究产物。

### 6.3 性能基线（2026-10-18，docker golang:1.25-alpine arm64）

| 基准 | 结果 |
|---|---|
| BenchmarkMACDPrefixes/250 | 109782 ns/op，261057 B/op，216 allocs/op |
| BenchmarkMACDPrefixes/1000 | 1968042 ns/op，4269026 B/op，966 allocs/op |
| BenchmarkAuditPortfolio（20 因子 × 300 股 × 750 交易日，账户路径） | 1.643 sec/op，peakMB 1213，endNav 1667282，artifactBytes ≈2.90MB |

- 说明：上表是迁移后的历史基线，并未对比旧规则。旧实现仍可从 Git 和测试 oracle 获取；评审已确认重复前缀计算造成显著退化，不能根据结构体轻量推断热循环没有分配放大。本次补充的旧／新 MACD 卖出微基准见 §7；完整分钟循环仍未验证。

### 6.4 完成清单

- T0–T6 全部完成，逐批证据见本台账 §1–§3。
- 73/73 目录组件已迁移到 rule 内核委托（T2 40 + T3 19 + T4 14 = 73），无未迁移或静默降级。
- 受保护核心类型（Buyer／Seller／Buy／Trade／Backtest／PositionConfig／Cost）、BuilderSpec version=1 及三种节点、FactorFilterSpec 字段未改；持久格式兼容。

### 6.5 旧接口兼容例外与未验证项

以下未验证项是首次收口记录，后续正式验收见 §9，修复复验及当前限制见 §10。

- **旧接口兼容例外**（不计作已统一，按计划保留）：SellAt／SellRSI（扩展 Seller 签名）、旧 SellTPSL（回看买入日前历史）、A因子TopN（横截面组合路径）、cmd/backtest/main.go（受保护草稿）。
- **未入目录待核实项**（§3.4）保留待独立变更。
- **§3.3 导出旧类型**（BuyCloseBelowMA/BreakMA/A阳线/A均线向上/近N天符合类型/A价格小于·大于/A流通市值小于/sell.MACD 结构体）暂留，删除需独立授权。
- **未验证项**：完整分钟循环及同股多持仓性能；两条引擎全参数旧／新规则逐笔交易矩阵。§8 的有限样本对拍已通过，不能替代完整矩阵。新增 MACD 并发数值测试已通过，但 `-race` 未完成：原生 Darwin 构建被既有依赖 `github.com/injoyai/goutil/oss` 的 `undefined: UserDataDir` 阻断，离线 Alpine 镜像无 C 编译器。

### 6.6 计划状态

[统一迁移计划](./2026-10-17-factor-rule-unification.md) 的 T0–T6 保持完成，T7 改为待验收，缺口见 §6.5。未部署、未推送。


## 7. 代码评审修复（2026-10-18）

- **倍量边界**：`55/50 == 1.1`，但 `50*1.1 > 55`；上界 `63/45 == 1.4` 也不能改写为乘法。新增 `rule.VolumeRatio` 沿用 `factor.A前N日均量` 的浮点累计及除法次序，倍量上下界均比较量比，保留默认值及上限退化条件。上下界反例、缺历史、零／负基准均已覆盖。
- **MACD 调用内复用**：`NewInput` 创建独立的惰性 MACD 作用域，截断前缀共享只读因果序列；参数切换重新计算，互斥保护并发读取。下一次 Buy／Sell 重新构造作用域，同代码／同切片／同长度的分钟更新不会复用上次结果。直接构造的 `Input` 保留按可见前缀即时求值。逐前缀、参数切换、并发、同切片分钟更新测试通过。
- **因子节点**：`buy.A因子过滤.Buy` 原先委托 `rule.NewFactorRange`，现按 §8 改为 `rule.EvalFactorRange`，有限区间仍由共用 `Range.contains` 判断；nil／空输入／NaN／反向区间不触发，±1e9 是真实边界。Go 无穷边界在 rule 内使用显式兼容分支，保留旧 IEEE 比较；未修改核心结构、`FactorFilterSpec.Build` 返回类型、JSON 或冻结基线。
- **验证**：Go 1.25 Alpine 离线容器中 `go test ./... -count=1`、`go build ./...`、`go vet ./...` 均通过。新增倍量回归测试已先在修复前确认失败。前端和部署代码未改，本轮未重复其测试。

`go test ./strategies/sell -run '^$' -bench '^BenchmarkMACDReversal$' -benchmem -benchtime=500ms -count=3`，Linux arm64、Go 1.25、1000 根 K 线、默认 20 日窗口，旧 oracle 与当前实现使用相同夹具：

| 实现 | 耗时 | 分配字节／次 | 分配次数／次 |
| --- | --- | --- | --- |
| 旧算法（本次复测） | 4420–5221 ns/op | 8192 | 1 |
| 评审时的规则实现 | 101321–104376 ns/op | 约 189194 | 41 |
| 修复后规则实现 | 5036–5053 ns/op | 8909 | 19 |

以上是单次 Seller 完整分支的微基准，不能推算为全市场分钟回测的总耗时保证。

## 8. 纯因子过滤零分配优化（2026-10-18）

- **根因**：`A因子过滤.Buy` 每次构造 `Range`、边界指针和取值闭包，并通过 `NewInput` 分配该路径不使用的 MACD 作用域。动量基准每次分配 240B／7 个对象。
- **实现**：`rule.EvalFactorRange` 直接计算因子值，以栈上的边界复用 `Range.contains`。`Range.Eval` 也使用该方法，因此有限区间仍只有一份边界判断。门面传入不带 MACD 作用域的 `Input`；不改动 MACD 求值作用域、不建立跨调用缓存。`NewFactorRange` 编译接口、门面字段、Name、JSON 与 ±1e9 的真实边界保持不变；无穷边界仍走显式兼容分支。
- **永久测试**：`TestFactorFilterNoAllocations` 覆盖有限及无穷边界，先确认优化前失败（7 allocs），优化后为 0；`TestFactorRangeLegacyBoundaries` 同时对拍直接求值、编译规则与旧 IEEE 比较，覆盖零、NaN、±Inf、反向区间与 ±1e9。`BenchmarkFactorFilter` 固化 50 根行情、5 日动量、[0,1e9] 的同输入基准。
- **完整验证**：Go 1.25 Alpine 离线容器中 `go test ./... -count=1`、`go build ./...`、`go vet ./...` 均通过；`gofmt -l`（本轮修改文件）、`git diff --check` 无输出。前端、部署及核心结构未改，本轮不重复其检查。

同一容器中先用优化前工作区快照覆盖源码运行基准，再顺序运行当前源码；命令均为 `go test ./strategies/buy -run '^$' -bench '^BenchmarkFactorFilter$' -benchmem -benchtime=400ms -count=2`（前者附临时 `-overlay`）：

| 实现 | 耗时 | 分配字节／次 | 分配次数／次 |
| --- | --- | --- | --- |
| 本轮优化前快照 | 92.09–110.9 ns/op | 240 | 7 |
| 本轮优化后 | 7.501–7.510 ns/op | 0 | 0 |

零分配指过滤判断本身的额外开销，底层因子自身需要的计算和分配另计。`BenchmarkMACDReversal/Rule` 同环境复测为 5157 ns/op、8909 B/op、19 allocs/op，与上一轮相近。

### 两条引擎的样本交易对拍

从基线 `261cb71` 提取旧 buy／sell 门面，在临时测试模块中与当前规则分别运行相同引擎。合成 320 根日行情，测试倍量、MACD、动量因子三类组合，各覆盖无分钟线及含分钟线（部分日期缺分钟数据），统一成本、仓位和账户预算：

- 逐股路径：6 组，合计 206 笔交易逐字段相等。
- 账户路径：6 组，合计 388 笔交易、每组 320 个每日账户点逐字段相等。
- 合计 12 组、594 笔交易通过。测试位于临时模块／overlay，未将完整旧生产算法复制进仓库；这只是有限合成样本，不等于全参数交易矩阵或全市场分钟性能验收。T7 仍保留 §6.5 的缺口。


## 9. T7 正式验收（2026-10-18）

**结论：未通过。** 全量普通 Go 测试／构建／vet、前端 67 项及部署脚本 7 项通过。通过临时 C 工具链补齐竞态环境后，规则和 Lab 相关检查通过，core 进度条竞争导致失败。734 组接受配置覆盖全部 73 个组件，其中 window=500 在 60 根行情时越界；其余 5,864 组旧新引擎对拍、23,645 笔交易及 67,436 个账户点相等。另独立复现多持仓当天实际卖出，违反 T+1。

阻塞位置：`core/backtest.go:253` 的 T+1 判断依赖被分钟循环改写的时间戳；`strategies/buy/buy_底部抬升.go:77` 缺少窗口越界保护；`core/backtest.go:146` 并发修改共享进度前缀。上述问题路径与 Git 基线一致，但仍阻止最终验收。生产代码、配置与冻结夹具未改，310 个检查文件 SHA-256 不变。

5000 个股票任务 × 240 分钟 × 两笔持仓的合成逐股负载通过，旧新交易一致；旧规则 3.8189 秒、当前规则 4.4713 秒（+17.1%），累计分配 18.69／20.72 GB。该负载隔离执行热路径，不含真实全市场数据库加载，不据此宣称性能预算通过。

完整报告和可重复的最小失败用例：[验收报告](../../output/acceptance/factor-rule-2026-10-18/report.md)。T7 保持未完成，须修复三项并复验。§6.5 原先的竞态环境限制已在此次验收解除，现在是实际失败，不应再标记为未验证。

## 10. 阻塞修复与复验（2026-10-18）

**三项阻塞已修复，功能与并发验收通过。** T+1 使用固定交易日；逐日私有行情快照避免分钟时间污染调用方输入，内部历史快照和 Virtual 口径保留；共享形态函数在窗口历史不足时返回无形态，覆盖 Buy／Explain／Annotate；进度条只保留启动时设置的年份前缀，逐股错误日志保留代码。

新增回归先复现失败，再验证通过。逐日参考实现仅同步修正 T+1，其他成交语义仍逐笔对拍；独立预期用例约束多持仓当天不卖、次日可卖以及输入不变。没有更新冻结信号 hash。

- `go test ./... -count=1`、`go build ./...`、`go vet ./...` 通过。
- core、strategies、builder、script、screen 的竞态检查通过；原进度条失败用例连续 20 次通过；Lab Account／Builder／Condition／Temporal 集成竞态通过。
- 73 个组件、734 组接受配置、5,872 组引擎对拍、23,480 笔交易和 67,528 个账户点相等。旧规则参考仅补已证实的共享窗口越界保护，完整参考差异在报告目录中；有限参数网格不代表穷举，6 个组件在该矩阵中未触发正信号。
- 5000 股 × 240 分钟 × 两笔持仓负载通过，每版 2,405,000 次 Seller 调用、10,000 笔 Virtual 交易相等；旧规则 3.8302 秒，当前规则 4.4492 秒，仍高约 16.2%。该开销尚存，未认定性能预算或实盘验收通过。
- 受保护接口、持久格式、真实配置和冻结夹具未变；`git diff --check` 通过。前端及部署脚本未改，沿用 §9 的通过记录，本轮未重复运行。未提交、部署或推送。

本轮按既定合成样本完成 T7 的功能、并发与负载验证，保留旧接口兼容例外和性能限制。真实数据库 I/O、PIT 外部数据、实盘负载未验证。完整证据：[修复复验报告](../../output/acceptance/factor-rule-fixes-2026-10-18/report.md)。

## 11. 全仓竞态修复正式接入（2026-10-18）

扩大到全仓竞态后额外发现的 Lab 内存采样器及 TDX 回调列表竞争已处理。采样器在读取峰值前等待后台协程退出，正常及失败清理由同一停止操作负责；行情测试关闭连接后等待客户端结束，避免跨测试复用仍在读的缓冲区。

本轮经用户授权接入本地依赖补丁。根 `go.mod` 当时通过相对 replace 使用上游 v1.2.20 的本地快照，仅在 `maps/wait/map_wait.go` 为回调注册和分发共用互斥锁。根 Dockerfile 在模块下载前复制本地依赖。来源、校验与差异见历史提交 `69d1371` 中的 `third_party/README.md`。

本次均使用实际根 `go.mod`，无临时 modfile／overlay：全量普通测试、构建、vet、全仓 `-race` 通过；行情空响应用例 `-race -count=100` 通过；部署脚本 7 项通过；真实 Dockerfile 构建本地检查镜像 `strategy-tail-lab:base-race-fix-20261018` 成功。未替换现有服务、推送镜像或提交代码。

原先隔离补丁通过而正式依赖未接入的状态现已结束，旧失败／待授权报告保留作历史。配置、冻结夹具、受保护接口未变。此前约 17% 的合成负载性能开销和真实数据库／实盘未验证的边界仍保留；本轮不重新声称执行前端、参数矩阵或分钟负载。完整记录见 [正式接入复验报告](../../output/acceptance/integrated-base-race-fix-2026-10-18/report.md)。


## 12. 分钟规则热路径优化（2026-10-18）

通过 CPU／分配剖析确认逐次构造历史引用、窗口、闭包与诊断名称是额外开销来源。时序操作数改用不可变参数对象、名称按需生成；MACD 窗口一次取得当前作用域内的只读序列，参数切换保留旧切片供并发读取。标量盈利条件经共用 `EvalCompare` 和持仓收益率操作数求值，0B／0 alloc，不复制公式或比较边界。MACD 反转不再让无用 Buy 指针逃逸。

新名称和数值边界保持不变，原有 MACD／EMA 数值实现、运算顺序、预热与窗口包含性不变，不新增跨分钟缓存或全局参数表。永久测试补齐直接比较边界、盈利条件零分配及多参数／多前缀 MACD 窗口并发。

本轮同负载优化前旧／新 3.8051／4.3531 秒（约 +14.4%；历史多次约 +16–17%）。优化后三轮分别取中位数旧／新 3.8364／3.9352 秒，差距约 +2.6%；逐对差距约 +0.4%–4.5%，不宣称统计显著性或零开销。当前实现分配次数从约 5912 万降至 2542 万（−57%），累计分配从 20.72 降至 19.46 GiB（旧版 18.69 GiB）。

`go test ./...`、build、vet、全仓 `-race` 通过；73 组件、734 组接受配置、5,872 组引擎、23,480 笔交易及 67,528 个账户点相等；5000 股／240 分钟／两笔持仓完整负载三轮通过，每版 Seller 2,405,000 次、Virtual 交易 10,000 笔相等。基线及 profile 见 [性能优化复验报告](../../output/acceptance/minute-rule-optimization-2026-10-18/report.md)。

旧参考仍保留前次明示的形态窗口安全修正，有限网格非全参数穷举，6 个组件未触发正信号。仍有额外规则对象分配；真实数据库 I/O、PIT 与实盘未测。本轮未重复前端／部署脚本／镜像构建，未部署或提交，原冻结夹具未更新。

## 13. 期末 Virtual 日线估值修复（2026-10-18）

独立于旧新对拍的[继续验收](../../output/acceptance/final-2026-10-18/report.md)复现了此前兼容保留的 P1：期末读取内部 `full` 的最后执行快照，旧仓提前卖出后，当天 15:00／12 元买入的新仓可能被按 09:31／11 元估值。即使提供完整收盘分钟线也会发生。§10 的 Virtual 口径保留结论由本节修正，历史报告保留作证据。

生产逻辑只将期末 `last` 改为原始 `dks[len(dks)-1]`，恢复 PROJECT_RULES §6.5 的末日日收盘合同；实际分钟成交、T+1、输入隔离、历史执行视图及滑点／费用算法保持不变。`DoLegacy` 参考算法会改写输入，因此在执行前保存原始末日日线，只明确校正 T+1 与本次估值缺陷，继续逐笔比较所有字段。

新增 `TestDoVirtualUsesFinalDailyClose`：缺分钟、分钟均无效、未平仓最后分钟早于收盘、旧仓提前卖出且新仓当日买入四种场景，分别运行零费用与默认费用，共 8 个永久回归；同时检查真实成交仍按分钟执行，Virtual 时间不早于买入，价格与净收入沿用成本模型。修复前 4 个子用例失败，修复后全部通过；上轮两个独立失败探针也均通过，未修改探针期望或冻结基线。

全量 `go test ./... -count=1`、build、vet、全仓 `-race` 和真实 Dockerfile 构建通过。73 组件、734 组接受配置、5,872 组引擎对拍、23,480 笔交易及 67,528 个账户点相等。5000 股／240 分钟／两笔持仓负载通过，每版 Seller 调用 2,405,000 次、10,000 笔 Virtual 交易相等。本轮单次旧／新耗时 4.0183／3.9379 秒，仅作负载复验，不据此声称新的性能提升。

本轮确认的 P1 已关闭，合成样本功能与并发复验通过。受影响的历史回测结果需要重跑，未改写已有报告；真实数据库、PIT 与实盘未测，未重复前端／部署脚本／TDX 100 次回归，未部署或提交。完整证据见[期末估值修复报告](../../output/acceptance/virtual-daily-close-fix-2026-10-18/report.md)。

## 14. 提交与部署验收（2026-10-18）

用户授权提交并部署。代码提交 `69d1371` 包含本次统一规则内核、回测修复、永久测试、授权依赖补丁与维护文档，共 197 个文件。提交前仅调整规划末尾空行和补丁文件的无上下文格式，暂存区差异检查通过；没有改变已验收的业务逻辑或依赖实现。

通过现有 `deploy.sh` 构建并部署 `strategy-lab:latest`，保持宿主机 `80 → 8765`、原数据与脚本挂载及配置只读挂载。运行二进制与 §13 已验收镜像的 SHA-256 相等；容器运行、重启次数 0、无 OOM／panic／fatal，旧容器 `strategy-lab-previous-20261018T193009-98281` 已停止并保留。

16 个页面／API 与 6 个已有详情读取通过；2 个策略、16 份旧回测、3 个候选及配置／脚本摘要在切换前后保持一致。另用本地真实行情对 3 只股票运行 2025 年因子／组件／MACD／持仓期限混合策略：3 完成、0 跳过、729 笔交易、7 笔 Virtual，成交顺序与 T+1 检查通过，Virtual 时间与数据库末日日线 Unix 在上海时区的时间一致。新增独立报告，16 份旧报告 JSON 未覆盖；浏览器确认策略加载、报告与收益图可见。

代码与部署冒烟已通过；本轮补充了本地真实数据库小样本验证，§13 的“未提交／部署／读取真实数据库”仅代表该节当时状态。仍未运行全市场负载、验证外部 PIT 数据、更新行情或注入故障回滚，不宣称实盘验收通过。证据见[部署验收报告](../../output/acceptance/deploy-2026-10-18/report.md)。
