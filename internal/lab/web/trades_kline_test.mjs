import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

const read = p => fs.readFileSync(new URL(p, import.meta.url), 'utf8');
const moduleURL = s => 'data:text/javascript;base64,' + Buffer.from(s).toString('base64');
const Vue = await import(moduleURL(read('./lab/js/vendor/vue.esm-browser.prod.js')));
const format = await import(moduleURL(read('./lab/js/format.js')));
const state = {}, Api = {};
globalThis.window = {matchMedia: () => ({matches:false})};
let available, chart, mounts;
globalThis.tradesKlineTest = {...Vue, ...format, state, Api,
  mountChart: () => {mounts++; return available ? chart : null;}, dropChart() {}};
const source = read('./lab/js/components/Tab3Trades.js').replace(/^import (\{.*\}) from .*;$/gm,
  'const $1 = globalThis.tradesKlineTest;');
const {default: Trades} = await import(moduleURL(source));
const trade = (code = 'sh600000', virtual = false) => ({code, variant:'策略', buyTime:'2025-01-02 15:00:00',
  sellTime:'2025-01-03 09:31:00', buyPrice:10.5, sellPrice:11.5, rate:9.52, profit:100,
  quantity:100, holdingDays:1, virtual});
const bars = [{date:'2025-01-02', open:10, close:10.5, low:9, high:12, volume:100, dif:0.2, dea:0.1, macd:0.1},
  {date:'2025-01-03', open:11, close:12, low:10, high:13, volume:200, dif:-0.2, dea:-0.1, macd:-0.1}];
function instance() {
  available = true; mounts = 0;
  chart = {options: [], setOption(o) {this.options.push(o);}};
  state.currentReport = {variants:[{name:'策略', trades:[trade()]}]}; state.selectedVariant = null;
  Api.kline = async () => bars;
  const ctx = {...Trades.data(), ...Trades.setup(), scrolls:0,
    $nextTick: async fn => fn?.(), $refs:{klineChart:{}, klinePanel:{scrollIntoView(){ctx.scrolls++;}, focus(){}}}};
  for (const [k, fn] of Object.entries(Trades.methods)) ctx[k] = fn.bind(ctx);
  for (const [k, fn] of Object.entries(Trades.computed)) Object.defineProperty(ctx, k, {get: () => fn.call(ctx)});
  return ctx;
}
function elements(v, predicate) {
  if (Array.isArray(v)) return v.flatMap(child => elements(child, predicate));
  if (!v || typeof v !== 'object') return [];
  return [...(predicate(v) ? [v] : []), ...elements(v.children, predicate)];
}

test('连续选择交易复用图表，B/S 使用实际成交价，图表容器没有 Vue 管理的子节点', async () => {
  const ctx = instance();
  await ctx.showKline(trade()); await ctx.showKline(trade('sz000001'));
  assert.equal(ctx.klineErr, ''); assert.equal(ctx.klineLoading, false);
  assert.equal(mounts, 1); assert.equal(chart.options.length, 2); assert.equal(ctx.scrolls, 0);
  assert.match(chart.options[1].title.text, /sz000001/);
  const markers = chart.options[1].series[0].markPoint.data;
  assert.deepEqual(markers.map(m => m.coord), [['2025-01-02',10.5], ['2025-01-03',11.5]]);
  assert.deepEqual(markers.map(m => m.value), ['B','S']);
  const render = Vue.compile(Trades.template, {decodeEntities:s=>s, hoistStatic:false});
  for (const loading of [true,false]) {
    ctx.klineLoading = loading;
    const tree = render(ctx, []);
    const [host] = elements(tree, v => v.props?.ref === 'klineChart');
    assert.ok(host); assert.equal(host.children, null);
    assert.equal(elements(tree, v => v.type === 'button' && v.props?.['aria-controls'] === 'tradeKlinePanel').length, 1);
  }
});

test('日K、成交量、MACD 共用缩放和十字光标，窄屏选择后定位图表', async () => {
  const ctx = instance();
  window.matchMedia = () => ({matches:true});
  try { await ctx.showKline(trade()); }
  finally { window.matchMedia = () => ({matches:false}); }
  assert.equal(ctx.scrolls, 1);
  const o = chart.options[0];
  assert.equal(o.grid.length, 3);
  assert.deepEqual(o.dataZoom.map(z => z.xAxisIndex), [[0,1,2], [0,1,2]]);
  assert.deepEqual(o.axisPointer.link, [{xAxisIndex:'all'}]);
  for (const [name, data, axis] of [['成交量',[100,200],1], ['MACD',[0.1,-0.1],2], ['DIF',[0.2,-0.2],2], ['DEA',[0.1,-0.1],2]]) {
    const s = o.series.find(s => s.name === name);
    assert.deepEqual(s.data, data); assert.equal(s.xAxisIndex, axis); assert.equal(s.yAxisIndex, axis);
  }
  const histogram = o.series.find(s => s.name === 'MACD');
  assert.equal(histogram.itemStyle.color({value:0.1}), '#ef4444');
  assert.equal(histogram.itemStyle.color({value:-0.1}), '#22c55e');
});

test('空行情和图表异常明确报错并可重试，不在 nextTick 中丢失错误', async () => {
  const ctx = instance();
  Api.kline = async () => [];
  await ctx.showKline(trade());
  assert.match(ctx.klineErr, /暂无日K/); assert.equal(mounts, 0);
  Api.kline = async () => bars; available = false;
  await ctx.showKline(trade()); assert.match(ctx.klineErr, /图表组件未加载/);
  available = true;
  chart.setOption = () => {throw new Error('render failed');};
  await ctx.showKline(trade()); assert.equal(ctx.klineErr, 'render failed');
  chart.setOption = o => chart.options.push(o);
  await ctx.showKline(trade()); assert.equal(ctx.klineErr, '');
});

test('期末估值不冒充实际卖出，缺少交易日行情时不捏造标记', async () => {
  const ctx = instance();
  await ctx.showKline(trade('sh600000', true));
  const marker = chart.options.at(-1).series[0].markPoint.data[1];
  assert.equal(marker.value, '估'); assert.equal(marker.symbol, 'diamond');
  const missing = {...trade(), sellTime:'2025-01-04 15:00:00'};
  await ctx.showKline(missing);
  assert.equal(chart.options.at(-1).series[0].markPoint.data.length, 1);
  assert.match(ctx.klineWarning, /卖出当日缺少日K/);
});
