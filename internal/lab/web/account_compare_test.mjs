import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

const read = path => fs.readFileSync(new URL(path, import.meta.url), 'utf8');
const moduleURL = source => 'data:text/javascript;base64,' + Buffer.from(source).toString('base64');
const Vue = await import(moduleURL(read('./lab/js/vendor/vue.esm-browser.prod.js')));
const state = {};
globalThis.accountCompareState = state;
const config = await import(moduleURL(read('./lab/js/config.js').replace("import { state } from './store.js';", 'const state = globalThis.accountCompareState;')));
globalThis.accountCompareGlobals = {...Vue, ...(await import(moduleURL(read('./lab/js/format.js')))),
  ...config, state, Api: {}, mountChart: () => ({setOption(option) {this.option = option;}}), dropChart() {},
  ANNUAL_PALETTE: ['#4c8dff', '#94a3b8'], NAV_COLORS: {net: '#ecd69c'}};
const stub = source => source.replace(/^import (\{.*\}) from .*;$/gm, 'const $1 = globalThis.accountCompareGlobals;')
  .replace(/^import (\w+) from .*;$/gm, 'const $1 = {};');
const {default: Compare} = await import(moduleURL(stub(read('./lab/js/components/Tab2Compare.js'))));
const {default: Run} = await import(moduleURL(stub(read('./lab/js/components/Tab1Run.js'))));
const {default: Dialog} = await import(moduleURL(stub(read('./lab/js/components/RunConfigDlg.js'))));

function instance(report) {
  Object.assign(state, {currentReport: report, builderCatalog: [], selectedVariant: null});
  const ctx = {...Compare.data(), ...Compare.setup(), $refs: {trendChart: {}, winChart: {}}};
  for (const [key, fn] of Object.entries(Compare.methods)) ctx[key] = fn.bind(ctx);
  for (const [key, fn] of Object.entries(Compare.computed)) Object.defineProperty(ctx, key, {get: () => fn.call(ctx)});
  return ctx;
}
const variant = (name, value, total = 1) => ({name, stats: {total, avgProfit: 99, winRate: 50, maxProfit: 99, maxLoss: -1},
  trades: total ? [{buyTime: '2025-01-02 15:00:00', rate: 99, profit: 100}] : [],
  account: {points: [{date: '2025-01-02', return: 0}, {date: '2025-01-03', return: value}], missingMinuteDays: 0, staleValuationDays: 0}});
const report = variants => ({config: {startYear: 2025, endYear: 2025, account: config.ACCOUNT_CONFIG}, variants,
  benchmark: [{date: '2025-01-02', return: 0}, {date: '2025-01-03', return: 10}], coverage: {skipped: 0}, finishedAt: '2025-01-03T15:00:00+08:00'});
const render = ctx => Vue.compile(Compare.template, {decodeEntities: s => s, hoistStatic: false})(ctx, []);
function elements(v, type) {
  if (Array.isArray(v)) return v.flatMap(c => elements(c, type));
  if (!v || typeof v !== 'object') return [];
  return [...(v.type === type ? [v] : []), ...elements(v.children, type)];
}

test('账户图只消费每日账户收益，指数共用区间，未交易账户也显示零收益', () => {
  const ctx = instance(report([variant('亏损', -2), variant('空仓', 0, 0), variant('盈利', 15)]));
  assert.deepEqual(ctx.vs.map(v => v.name), ['盈利', '空仓', '亏损']);
  ctx.renderCharts(ctx.vs);
  const option = ctx.trendC.option;
  assert.equal(option.title.text, '账户累计收益率');
  assert.equal(option.series.length, 4);
  assert.deepEqual(option.series[0].data, [['2025-01-02', 0], ['2025-01-03', 15]]);
  assert.equal(option.series[1].data.at(-1)[1], 0);
  assert.equal(option.series.at(-1).name, '上证指数（买入持有）');
  assert.equal(option.series.at(-1).lineStyle.type, 'dashed');
  assert.equal(ctx.excessReturn(ctx.vs[0]), 5);
  assert.equal(ctx.returnFmt(ctx.excessReturn(ctx.vs[2])), '-12.00');
  assert.equal(ctx.returnCls(0), '');
  const headers = elements(render(ctx), 'th').map(v => v.children);
  assert.ok(headers.includes('账户收益%'));
  assert.ok(headers.includes('超额(百分点)'));
});

test('历史报告保留原图并提示重跑，账户报告缺失资金曲线时不回退逐笔累计', () => {
  const v = variant('历史', 0); delete v.account;
  const old = report([v]); delete old.config.account; delete old.benchmark;
  const ctx = instance(old);
  ctx.renderCharts(ctx.vs);
  assert.equal(ctx.trendC.option.title.text, '逐笔收益率累计（非资金曲线）');
  assert.equal(ctx.trendC.option.series[0].data[0][1], 99);
  assert.ok(elements(render(ctx), 'p').some(v => v.children?.includes('重新运行回测')));
  state.currentReport = report([v]);
  ctx.renderCharts(ctx.vs);
  assert.equal(ctx.accountReturn(v), null);
  assert.equal(ctx.excessReturn(v), null);
  assert.equal(ctx.trendC.option.series.length, 1); // 仅指数，不伪造账户曲线。
});

test('行情缺失显式披露，空报告无误导指数曲线', () => {
  const v = variant('部分行情', 0); v.account.missingMinuteDays = 2; v.account.staleValuationDays = 1;
  const ctx = instance(report([v]));
  assert.equal(ctx.accountWarnings.length, 2);
  assert.equal(elements(render(ctx), 'li').length, 2);
  state.currentReport = null;
  ctx.renderCharts(ctx.vs);
  assert.equal(ctx.trendC.option.series.length, 0);
  assert.doesNotThrow(() => render(ctx));
});

test('因子和高级脚本提交冻结的资金规则，因子研究的样本配置保持独立', async () => {
  const submitted = [];
  const g = globalThis.accountCompareGlobals;
  g.Api.saveScript = async () => {};
  g.Api.runBacktest = async cfg => {submitted.push(cfg); return {variants: 1};};
  state.runCfg = {startYear: 2025, endYear: 2025, sampleMode: 'all', holdingOn: true, holdingDays: 1};
  const ctx = {script: '', startTask() {}};
  await Run.methods.runBacktest.call(ctx, 'adv');
  assert.deepEqual(submitted[0].account, {initialCapital: 100000, maxPositions: 5, orderBudget: 20000});
  state.simpleConds = []; state.specName = ''; state.presetId = '';
  const spec = Run.methods.buildSpec.call({});
  assert.deepEqual(spec.run.account, submitted[0].account);
  assert.deepEqual(Dialog.setup().account, submitted[0].account);
});
