import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';
await import(new URL('../../chartui/kline.js', import.meta.url));
const K = globalThis.StrategyKline;


const read = p => fs.readFileSync(new URL(p, import.meta.url), 'utf8');
const moduleURL = s => 'data:text/javascript;base64,' + Buffer.from(s).toString('base64');
const Vue = await import(moduleURL(read('./lab/js/vendor/vue.esm-browser.prod.js')));
const format = await import(moduleURL(read('./lab/js/format.js')));
const state = {}, Api = {};
globalThis.window = {matchMedia: () => ({matches:false})};
let available, chart, mounts;
globalThis.tradesKlineTest = {...Vue, ...format, state, Api,
  mountKline: () => {mounts++; return available ? chart : null;}, dropKline() {}};
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
  chart = {options: [], setData(o) {this.options.push(o); return K.normalize(o).missing;}};
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
  assert.match(chart.options[1].title, /sz000001/);
  assert.equal(chart.options[1].view, 'all');
  const markers = K.normalize(chart.options[1]).markers;
  assert.deepEqual(markers.map(m => [m.timestamp,m.price]), [[Date.parse('2025-01-02T00:00:00+08:00'),10.5], [Date.parse('2025-01-03T00:00:00+08:00'),11.5]]);
  assert.deepEqual(markers.map(m => m.label), ['B','S']);
  const render = Vue.compile(Trades.template, {decodeEntities:s=>s, hoistStatic:false});
  for (const loading of [true,false]) {
    ctx.klineLoading = loading;
    const tree = render(ctx, []);
    const [host] = elements(tree, v => v.props?.ref === 'klineChart');
    assert.ok(host); assert.equal(host.children, null);
    const [tableHost] = elements(tree, v => v.props?.ref === 'tradeTable');
    assert.ok(tableHost); assert.equal(tableHost.children, null);
  }
});

test('K线工具按钮调用共享图表能力，加载与错误时禁用且不占图表宿主', async () => {
  const ctx=instance();await ctx.showKline(trade());
  const calls=[];chart.zoom=scale=>calls.push(scale);chart.fitContent=()=>calls.push('all');
  const render=Vue.compile(Trades.template,{decodeEntities:s=>s,hoistStatic:false});
  for(const [loading,error] of [[false,''],[true,''],[false,'failed']]) {
    ctx.klineLoading=loading;ctx.klineErr=error;
    const tree=render(ctx,[]), buttons=elements(tree,v=>v.type==='button' &&
      (['放大K线','缩小K线'].includes(v.props?.['aria-label']) || v.children==='显示全程'));
    assert.equal(buttons.length,3);
    assert.ok(buttons.every(b=>b.props.disabled===!!(loading||error)));
    if(!loading&&!error) for(const button of buttons) button.props.onClick();
  }
  assert.deepEqual(calls,[0.8,1.25,'all']);
});

test('日K、成交量、MACD 共用缩放和十字光标，窄屏选择后定位图表', async () => {
  const ctx = instance();
  window.matchMedia = () => ({matches:true});
  try { await ctx.showKline(trade()); }
  finally { window.matchMedia = () => ({matches:false}); }
  assert.equal(ctx.scrolls, 1);
  const o = chart.options[0];
  assert.deepEqual(o.rows, bars);
  const data = K.normalize(o).data;
  assert.deepEqual(data.map(d => d.volume), [100,200]);
  assert.deepEqual(data.map(d => [d.dif,d.dea,d.hist]), [[0.2,0.1,0.1],[-0.2,-0.1,-0.1]]);
});

test('空行情和图表异常明确报错并可重试，不在 nextTick 中丢失错误', async () => {
  const ctx = instance();
  Api.kline = async () => [];
  await ctx.showKline(trade());
  assert.match(ctx.klineErr, /暂无日K/); assert.equal(mounts, 0);
  Api.kline = async () => bars; available = false;
  await ctx.showKline(trade()); assert.match(ctx.klineErr, /图表组件未加载/);
  available = true;
  chart.setData = () => {throw new Error('render failed');};
  await ctx.showKline(trade()); assert.equal(ctx.klineErr, 'render failed');
  chart.setData = o => {chart.options.push(o);return [];};
  await ctx.showKline(trade()); assert.equal(ctx.klineErr, '');
});

test('期末估值不冒充实际卖出，缺少交易日行情时不捏造标记', async () => {
  const ctx = instance();
  await ctx.showKline(trade('sh600000', true));
  const marker = K.normalize(chart.options.at(-1)).markers[1];
  assert.equal(marker.label, '估'); assert.equal(marker.kind, 'est');
  const missing = {...trade(), sellTime:'2025-01-04 15:00:00'};
  await ctx.showKline(missing);
  assert.equal(K.normalize(chart.options.at(-1)).markers.length, 1);
  assert.match(ctx.klineWarning, /卖出当日缺少日K/);
});

test('交易明细K线提示共用较前收涨跌幅', async () => {
 const ctx=instance();await ctx.showKline(trade());
 const option=chart.options.at(-1);
 assert.equal(K.legends({current:K.normalize(option).data[1]}).find(x=>x.title==='涨跌幅（较前收）').value.text,'+14.29%');
});

test('表格不截断全量交易，组合筛选沿用日期和收益率的包含边界', () => {
  const ctx = instance();
  state.currentReport.variants[0].trades = Array.from({length: 2505}, (_, i) => trade('sh' + i));
  state.currentReport.variants.push({name: '另一策略', trades: [{...trade('sz000001'), rate: -5}]});
  assert.equal(ctx.filteredTrades.length, 2506);
  assert.equal(new Set(ctx.filteredTrades.map(t => t._rowId)).size, 2506);
  ctx.fVariant = '另一策略'; ctx.fRateMin = '-5'; ctx.fRateMax = '-5';
  ctx.fFrom = '2025-01-02'; ctx.fTo = '2025-01-02';
  assert.equal(ctx.filteredTrades.length, 1);
  ctx.fTo = '2025-01-01'; assert.equal(ctx.filteredTrades.length, 0);
  ctx.fTo = ''; ctx.fRateMax = '-6'; assert.equal(ctx.filteredTrades.length, 0);
  state.currentReport = null; assert.deepEqual(ctx.filteredTrades, []);
});

test('旧行情晚返回时不能覆盖后来交易的图表数据', async () => {
  const ctx = instance();
  let resolveOld, resolveNew;
  const oldResult = new Promise(resolve => {resolveOld = resolve;});
  const newResult = new Promise(resolve => {resolveNew = resolve;});
  Api.kline = code => code === 'old' ? oldResult : newResult;
  const oldRequest = ctx.showKline(trade('old'));
  await Promise.resolve();
  const newRequest = ctx.showKline(trade('new'));
  await Promise.resolve();
  resolveNew(bars); await newRequest;
  resolveOld([{...bars[0], close: 999}]); await oldRequest;
  assert.equal(ctx.klineTrade.code, 'new'); assert.equal(chart.options.length, 1);
  assert.match(chart.options[0].title, /new/); assert.deepEqual(chart.options[0].rows, bars);
});

test('Excel 导出绑定选中报告并防重复，失败不清空筛选，切报告忽略旧错误', async () => {
  const ctx = instance(); state.currentReport.id = 'report-A'; ctx.fCode = '600000';
  let reject, calls = [];
  Api.exportReport = id => {calls.push(id); return new Promise((_resolve, fail) => {reject = fail;});};
  const first = ctx.exportReport();
  assert.equal(ctx.exportBusy, true); await ctx.exportReport();
  assert.deepEqual(calls, ['report-A']);
  reject(new Error('offline')); await first;
  assert.match(ctx.exportError, /offline/); assert.equal(ctx.fCode, '600000');
  assert.equal(ctx.exportBusy, false);
  const second = ctx.exportReport();
  state.currentReport = {id: 'report-B', variants: []}; ctx.resetReport();
  reject(new Error('old failure')); await second;
  assert.equal(ctx.exportError, ''); assert.equal(ctx.exportBusy, false);
});

test('股票名称用于表格和K线标题，筛选、身份和请求仍保留原始代码', async () => {
  const ctx = instance();
  state.stockNames = {sh600000: '浦发银行'};
  try {
    const row = ctx.filteredTrades[0];
    assert.equal(row.stockLabel, 'sh600000 浦发银行');
    assert.equal(row.code, 'sh600000');
    let requestCode;
    Api.kline = async code => {requestCode = code; return bars;};
    await ctx.showKline(row);
    assert.equal(requestCode, 'sh600000');
    assert.match(chart.options.at(-1).title, /^sh600000 浦发银行（/);
    assert.match(chart.options.at(-1).identity, /^sh600000\//);
    state.stockNames = {};
    assert.equal(ctx.filteredTrades[0].stockLabel, 'sh600000');
    ctx.fCode = '600000'; assert.equal(ctx.filteredTrades.length, 1);
  } finally {delete state.stockNames;}
});
