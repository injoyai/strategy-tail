# 交易表格、Excel 导出与浏览器回归

## 交易明细

交易明细使用本地嵌入的 Tabulator 6.6.1。报告 API 返回完整交易，表格通过虚拟滚动只渲染可视区域，不再截取前 2000 条。列标题支持点击或键盘 Enter/空格排序；原有变体、代码、买入日期和收益率筛选继续生效。代码按钮可用键盘打开 K 线，整行也可点击。

表格筛选、排序和滚动属于当前页面会话；切换页签保留，刷新页面重置。筛选不修改报告。行情错误可重试，旧请求不能覆盖之后选中的交易。

Tabulator 的原始 JS/CSS 和 MIT 许可证保存在 `internal/lab/web/lab/js/vendor/tabulator/`，视觉适配位于 `lab.css`，数据和 DOM 所有权由 `trade-table.js` 管理。Vue 不管理表格宿主的子节点，不启用 Tabulator 的双向数据代理。

## Excel 下载

在交易明细工具栏点击“导出完整报告 Excel”，会下载当前报告的**全部变体和全部交易**，不受页面筛选或排序影响。

后端使用 Excelize v2.11.0（BSD 3-Clause，要求 Go 1.25+），按需生成 XLSX：

- 策略参数：开始/完成时间、报告来源、运行配置、策略配置、覆盖情况和对比摘要的已保存快照；长 JSON 按序分段，避免 Excel 单元格静默截断。
- 绩效汇总：直接使用报告已保存的统计，不重新计算收益或胜率。
- 交易明细：代码与时间为文本，价格、金额和收益率为数值，显示两位小数但保留底层精度；实际成交与期末估值分别标注。
- 账户报告额外提供“账户净值”，有基准数据时提供“上证基准”，均直接投影原始数据；旧报告没有对应数据时不生成这些工作表。

接口为 `GET /api/report/{id}/export.xlsx`。需提供具体报告 ID，错误返回 JSON；生成成功后返回附件，不向服务器保存 XLSX 文件，也不修改历史报告。每张表冻结首行。单张工作表超过 1048575 条数据时明确报错，不截断交易。

## 浏览器回归

前端仍然直接使用浏览器 ESM。根目录 npm 依赖仅用于测试，无需前端编译或打包。

首次准备（Node.js 20+）：

```sh
npm ci
npx playwright install chromium
```

执行：

```sh
npm run test:browser
```

也可复用本机安装的 Chrome：

```sh
PLAYWRIGHT_CHROME=1 npm run test:browser
```

测试会编译 `tests/browser/server`，固定监听 `127.0.0.1:18765`；端口被占用时失败，不复用现有服务。服务器仅在临时目录创建合成报告，不初始化行情运行时或定时更新。报告、截图和失败 trace 保存到 `output/browser-tests/`。

覆盖 2506 笔交易的滚动与筛选、键盘排序和 K 线联动、行情失败重试、旧响应保护、页签切换、历史切换、Excel 错误与真实后端下载、1440px/390px 布局。Tabulator 和 KLineChart 使用真实本地发布包；行情由确定性夹具返回，可选 ECharts/GSAP CDN 被置为离线，验收不依赖网络或真实行情。

这套回归不启动回测、建库或训练，不代替这些后端流程的单元测试与真实数据验收。

## 上游来源

- [Tabulator 官方安装文档](https://www.tabulator.info/docs/6.x/install/)；本地包来自 npm `tabulator-tables@6.6.1`。
- [Excelize 官方文档](https://xuri.me/excelize/zh-hans/)。
- [Playwright 官方文档](https://playwright.dev/docs/intro)；开发依赖固定为 `@playwright/test@1.64.1`（Apache-2.0），由 `package-lock.json` 锁定。
