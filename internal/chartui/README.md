# 共享 K 线图

`kline.js` 是 Lab、选股诊断与 HTML 导出的唯一 K 线适配器。统计图仍使用 ECharts。

## 固定版本与来源

- KLineChart **10.1.0**，来自 npm 官方发布包：
  `https://registry.npmjs.org/klinecharts/-/klinecharts-10.1.0.tgz`
- 原始文件：`dist/umd/klinecharts.min.js`，原样保存为
  `vendor/klinecharts-10.1.0.min.js`，不手动修改。
- 文件 SHA-256：`428b7c3bff6a74e121b4da8093bd0187d1c81b9fae8826f1143097280667a8a1`。
- `vendor/LICENSE`、`NOTICE` 和 `LICENSE-lightweight-charts` 随包保留。
  独立报告和 screen 合并脚本包含许可证与归属声明；Lab 通过静态资源路由提供原文。
- 升级时同时核对实际发布包的类型声明、许可证及浏览器行为；不混用 9.x API。

## 数据与生命周期

`StrategyKline.mount(element, {dark, macd, maPeriods})` 创建实例，
`setData({rows, markers, showDate, identity, title, view})` 替换完整可见数据，返回未能定位的标记。
实例提供 `resize()`、`zoom(scale)`、`fitContent()`、`clear()` 和幂等 `dispose()`。

- `rows` 为按交易日递增的 OHLC／volume，以及可选 `ma5/ma10/ma20/dif/dea/hist`。
  普通行情使用 `date`；盲练使用 `day`，并显式传 `showDate:false`。
- `hist` 兼容 Lab 接口的 `macd` 字段，直接使用服务端数值与空值；不调用内置 MACD。
  已提供的均线（包括空值）不重算；未提供均线的旧页面沿用窗口内计算。
- `markers` 使用 `date` 或 `day` 定位，`price` 是实际价格，
  `kind` 区分 `buy/sell/est/signal`；同日多笔成交独立保留。
  标记采用“蜡烛外标签＋方向箭头＋成交价圆点／引线”：买入在下、卖出与估值在上；
  文字底色不透明，红／绿只标边框和图形，文字沿用主题高对比颜色。
  标签避让其宽度覆盖的邻近影线，同日与相邻标签优先纵向错开，缩放、平移和尺寸变化后重新投影。
  Y 轴为标签预留像素空间，不改变 OHLC、实际成交价或指标数据；视窗外标记不固定在边缘。
  该布局参考 [TradingView 的 aboveBar/belowBar 系列标记](https://tradingview.github.io/lightweight-charts/tutorials/how_to/series-markers)，
  使用 KLineChart 10.1.0 的自定义覆盖物实现；没有更换图表组件。
- `identity` 区分股票／题目／交易窗口，切换时恢复默认视窗；
  相同身份更新保留缩放。回放只传当前日期以前的数据，回退不能只移动视窗。
- 交易明细传 `view:'all'`，按主图实际宽度展示整个已加载窗口，左右保留少量空白；
  长区间可使用不足一像素的柱间距，不截断或补拉数据。默认全程状态随尺寸变化适配；
  用户缩放／平移后保留视图，`fitContent()` 恢复全程。其他调用方保留默认最近区间。
- Vue 调用经 Lab 的 `js/kline.js` 包装器使用 `markRaw`。
  宿主内没有 Vue 管理的提示节点；卸载先销毁，再替换 DOM。

独立未来收益报告的冻结输入只包含前导／命中／后续样本，MACD 在该窗口上复用后端公式；
它没有完整历史预热数据。此限制与 Lab 的全历史预热接口不同，不能将窗口指标当作策略全历史值。
已生成的历史 HTML 不自动改写；重新导出后才使用新组件。

## 验证

```sh
node --test internal/chartui/*test.mjs internal/lab/web/*.mjs
TZ=Asia/Shanghai go test ./...
go build ./...
```

浏览器验收覆盖交易明细、盘感匿名图、逐日回放前进／倒退、screen 诊断、三种独立 HTML，
以及 1440px 桌面、390px 窄屏和键盘缩放。验收使用虚构数据；统计图的现有 CDN 在独立报告测试中以桩代替。
发布仍需重建 Go 二进制／镜像以更新嵌入资源。
