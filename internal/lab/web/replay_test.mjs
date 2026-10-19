import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

const read = p => fs.readFileSync(new URL(p, import.meta.url), 'utf8');
const moduleURL = s => 'data:text/javascript;base64,' + Buffer.from(s).toString('base64');
const Vue = await import(moduleURL(read('./lab/js/vendor/vue.esm-browser.prod.js')));
const R = await import(moduleURL(read('./lab/js/replay.js')));
const format = await import(moduleURL(read('./lab/js/format.js')));

// BacktestReplay 的 import 全部指向 globalThis.replayTest：replay.js 与 format.js
// 用真实实现，Vue 用真实 vendor，Api/state/mountChart 为测试桩（trades_kline_test 同模式）。
const state = Vue.reactive({activeTab: 2}), Api = {};
let available, chart, mounts;
globalThis.replayTest = {...R, ...format, ...Vue, state, Api,
  NAV_COLORS: {gross: '#4c8dff', net: '#ecd69c'},
  mountChart: () => {mounts++; return available ? chart : null;}, dropChart() {}};
const source = read('./lab/js/components/BacktestReplay.js')
  .replace(/^import (\{.*\}) from .*;$/gm, 'const $1 = globalThis.replayTest;')
  .replace(/^import \* as (\w+) from .*;$/gm, 'const $1 = globalThis.replayTest;');
const {default: Replay} = await import(moduleURL(source));

// —— 固定数据：3 个交易日，d1 买入 / d2 持仓 / d3 卖出 ——
const trade = (seq, code, side, extra = {}) => ({seq, positionId: 'p' + seq, code, side,
  time: '2025-01-02 14:55:00', price: 10, execPrice: 10.01, quantity: 900, fee: 5.01,
  minuteFallback: false, reason: ['buy.entry'], ...extra});
const holding = code => ({positionId: 'p1', code, buyTime: '2025-01-02 15:00:00', quantity: 900,
  buyPrice: 10, buyExecPrice: 10.01, totalCost: 9005, costPrice: 10.006, markPrice: 11.2,
  markDate: '2025-01-03', marketValue: 10080, floatingPnl: 1075, floatingRate: 11.94});
const day = (date, equity, dayPnl, trades = [], holdings = []) => ({date, equity, cash: equity,
  marketValue: 0, positionRatio: 0, dayPnl, holdings, trades});
const dates3 = ['2025-01-02', '2025-01-03', '2025-01-06'];
const data3 = (days = [
  day(dates3[0], 100000, 0, [trade(1, 'sh600000', 'buy')]),
  day(dates3[1], 101000, 1000, [], [holding('sh600000')]),
  day(dates3[2], 100500, -500, [trade(2, 'sh600000', 'sell', {execPrice: 9.99, reason: ['sell.exit']})]),
]) => ({version: 1, variantIndex: 0, variantName: '基线', dates: dates3, days, rules: [
  {path: 'buy.entry', role: 'buy', name: 'MA金叉', desc: 'MA5 上穿 MA20'}]});
const report3 = () => ({id: 'rep-1', variants: [{name: '基线', replay: {version: 1, available: true}},
  {name: '对照', replay: {version: 1, available: true}}],
  benchmark: [{date: dates3[0], return: 0.5}, {date: dates3[1], return: 1.2}, {date: dates3[2], return: -0.4}]});

function instance() {
  available = true; mounts = 0;
  chart = {options: [], cleared: 0, setOption(o) {this.options.push(o);}, clear() {this.cleared++;}};
  if (!state.replay) state.replay = R.createSession();
  const ctx = {...Replay.data(), ...Replay.setup(),
    $nextTick: async fn => fn?.(), $refs: {equityChart: {}, klineChart: {}}};
  for (const [k, fn] of Object.entries(Replay.methods)) ctx[k] = fn.bind(ctx);
  for (const [k, fn] of Object.entries(Replay.computed)) Object.defineProperty(ctx, k, {get: () => fn.call(ctx)});
  return ctx;
}
function elements(v, predicate) {
  if (Array.isArray(v)) return v.flatMap(child => elements(child, predicate));
  if (!v || typeof v !== 'object') return [];
  return [...(predicate(v) ? [v] : []), ...elements(v.children, predicate)];
}

test('R01/R02/R04 步进：首日/末日边界、clamp 暂停、往返一致、速度不碰数据', () => {
  const s = R.createSession();
  s.data = data3();
  R.stepPrev(s);
  assert.equal(s.dateIdx, 0); assert.equal(s.playing, false); // 首日不越界
  R.stepTo(s, 99);
  assert.equal(s.dateIdx, 2); // clamp 到末日
  R.play(s);
  assert.equal(s.playing, false); // 末日播放无操作
  R.stepNext(s);
  assert.equal(s.dateIdx, 2);
  R.stepTo(s, 0); R.stepNext(s);
  assert.equal(s.dateIdx, 1);
  R.stepPrev(s);
  assert.equal(s.dateIdx, 0);
  R.stepNext(s); R.stepNext(s);
  assert.equal(s.dateIdx, 2); // 前进→后退→前进一致（R04）
  s.playing = true;
  R.stepTo(s, 1);
  assert.equal(s.playing, false); assert.equal(s.dateIdx, 1); // 跳转即暂停（R02）
  R.setSpeed(s, 'fast');
  assert.equal(s.speed, 'fast'); assert.equal(s.data.days.length, 3);
  R.setSpeed(s, 'nope');
  assert.equal(s.speed, 'fast');
});

test('R01 播放推进到末日自动停', () => {
  const s = R.createSession();
  s.data = data3();
  R.stepTo(s, 0); R.play(s);
  R.advance(s);
  assert.equal(s.dateIdx, 1); assert.equal(s.playing, true);
  R.advance(s);
  assert.equal(s.dateIdx, 2); assert.equal(s.playing, false); // 到达末日即停
  R.advance(s);
  assert.equal(s.dateIdx, 2); // 末日不再推进
});

test('R03 派生数据不泄漏未来：可见帧、成交、收益曲线、K线、原因翻译', () => {
  const s = R.createSession();
  s.data = data3();
  s.dateIdx = 1;
  assert.deepEqual(R.visibleDays(s).map(d => d.date), dates3.slice(0, 2));
  assert.deepEqual(R.dayTrades(s), []); // 当日（d2）无成交
  const seen = R.visibleTradesFor(s, 'sh600000');
  assert.deepEqual(seen.map(t => t.date), [dates3[0]]); // d3 卖出不可见
  assert.equal(seen[0].seq, 1);
  const es = R.equitySeries(s);
  assert.deepEqual(es.dates, dates3); // 完整日期轴
  assert.deepEqual(es.values, [0, 1, null]); // 未来为 null，不揭示最终收益
  assert.equal(es.initial, 100000); // 首日权益 − 首日盈亏
  const bad = R.equitySeries({...s, data: {...data3(), dates: ['d1', 'd2'],
    days: [day('d1', 100, NaN), day('d2', 101, 1)]}});
  assert.deepEqual(bad.values, [null, null]); assert.equal(bad.initial, null); // 无法确定不伪造
  const kline = [{date: '2024-12-30'}, {date: dates3[0]}, {date: dates3[1]}, {date: dates3[2]}];
  assert.deepEqual(R.klineVisible(kline, dates3[1]).map(r => r.date),
    ['2024-12-30', dates3[0], dates3[1]]); // 预热保留、未来截断
  assert.deepEqual(R.klineVisible(kline, ''), []);
  assert.deepEqual(R.reasonLines(seen[0], s.data.rules), ['MA金叉：MA5 上穿 MA20']);
  assert.deepEqual(R.reasonLines({reason: ['unknown.path']}, s.data.rules), ['unknown.path']); // 未登记原样
  assert.deepEqual(R.reasonLines({reason: []}, s.data.rules), []);
});

test('R08 自动暂停：默认关、先推进后检查、同日恢复不重复暂停', () => {
  const s = R.createSession();
  assert.equal(s.autoPause, false); // 默认关
  const four = days => ({version: 1, variantIndex: 0, variantName: '基线',
    dates: ['d1', 'd2', 'd3', 'd4'], days, rules: []});
  s.data = four([
    day('d1', 100000, 0, [trade(1, 'sh600000', 'buy')]),
    day('d2', 101000, 1000, [trade(2, 'sh600000', 'sell')]),
    day('d3', 100500, -500),
    day('d4', 100500, 0),
  ]);
  R.play(s); R.advance(s); // 开关关：成交日继续播
  assert.equal(s.playing, true); assert.equal(s.dateIdx, 1);
  R.setAutoPause(s, true);
  R.advance(s); // d3 无成交且非末日 → 通过
  assert.equal(s.playing, true); assert.equal(s.dateIdx, 2);
  R.advance(s); // d4 有成交 → 停
  assert.equal(s.playing, false); assert.equal(s.dateIdx, 3);
  // 同日多点数据恢复场景：成交日后暂停，恢复必须先推进到下一交易日。
  const s2 = R.createSession();
  s2.autoPause = true;
  s2.data = data3([
    day('d1', 100000, 0, [trade(1, 'sh600000', 'buy'), trade(3, 'sz000001', 'buy')]), // 同日多点
    day('d2', 101000, 1000, [trade(2, 'sh600000', 'sell')]),
    day('d3', 100500, -500),
  ]);
  R.play(s2); R.advance(s2); // 推进到 d2（有成交）→ 暂停
  assert.equal(s2.playing, false); assert.equal(s2.dateIdx, 1);
  R.play(s2); R.advance(s2); // 恢复先推进到 d3（末日），不在 d2 重复暂停
  assert.equal(s2.dateIdx, 2); assert.equal(s2.playing, false); // 到达末日停止
  R.advance(s2);
  assert.equal(s2.dateIdx, 2);
});

test('R09 选择个股：暂停、同股不清快照、换股失效旧K线、当日成交不受影响', () => {
  const s = R.createSession();
  s.data = data3();
  s.playing = true; s.code = 'sh600000'; s.kline = [{date: 'x'}];
  R.selectStock(s, 'sh600000');
  assert.equal(s.playing, false); assert.ok(s.kline); // 同股：只暂停
  const kseq = s.klineSeq;
  R.selectStock(s, 'sz000001');
  assert.equal(s.kline, null); assert.equal(s.klineSeq, kseq + 1); // 换股：旧快照失效
  assert.deepEqual(R.dayTrades(s).map(t => t.seq), [1]); // 当日成交展示不受选择影响
});

test('R10 切变体：保留日期、清数据与个股、序列号使在途响应失效；同变体无操作', () => {
  const s = R.createSession();
  s.data = data3();
  s.dateIdx = 2; s.code = 'sh600000'; s.kline = [{date: 'x'}]; s.playing = true;
  const seq = s.seq, kseq = s.klineSeq;
  R.switchVariant(s, 1);
  assert.equal(s.variantIdx, 1); assert.equal(s.dateIdx, 2); // 保留日期
  assert.equal(s.data, null); assert.equal(s.code, ''); assert.equal(s.kline, null);
  assert.equal(s.playing, false);
  assert.equal(s.seq, seq + 1); assert.equal(s.klineSeq, kseq + 1);
  R.switchVariant(s, 1);
  assert.equal(s.seq, seq + 1); // 同变体不递增
  const fresh = R.resetSession();
  assert.notEqual(fresh, s); assert.equal(fresh.dateIdx, 0); assert.equal(fresh.data, null);
});

test('R10 组件加载：旧响应按序号与会话身份丢弃，错误与缺 id 显式提示', async () => {
  const ctx = instance();
  state.replay = R.createSession();
  state.currentReport = report3();
  let resolve1;
  Api.replay = () => new Promise(r => {resolve1 = r;});
  const p1 = ctx.load();
  R.switchVariant(state.replay, 1); // 在途请求随 seq 递增失效
  resolve1(data3());
  await p1;
  assert.equal(state.replay.data, null); // 旧响应丢弃
  Api.replay = async () => data3();
  await ctx.load();
  assert.equal(state.replay.data.days.length, 3); assert.equal(state.replay.loading, false);
  Api.replay = async () => {throw new Error('boom');};
  await ctx.load();
  assert.equal(state.replay.error, 'boom');
  state.currentReport = {};
  await ctx.load();
  assert.match(state.replay.error, /标识/);
});

test('R10 K线加载：旧响应按 klineSeq 丢弃', async () => {
  const ctx = instance();
  state.replay = R.createSession();
  state.currentReport = report3();
  state.replay.code = 'sh600000';
  let resolveK;
  Api.replayKline = () => new Promise(r => {resolveK = r;});
  const p = ctx.loadKline();
  R.selectStock(state.replay, 'sz000001');
  resolveK([{date: dates3[0]}]);
  await p;
  assert.equal(state.replay.kline, null);
});

test('播放 tick：先推进再安排下一步，暂停后 tick 无副作用', async () => {
  const ctx = instance();
  state.replay = R.createSession();
  state.replay.data = data3();
  state.replay.playing = true;
  let schedules = 0;
  ctx.schedule = () => schedules++;
  await ctx.tick();
  assert.equal(state.replay.dateIdx, 1); assert.equal(schedules, 1);
  state.replay.playing = false;
  await ctx.tick();
  assert.equal(state.replay.dateIdx, 1); assert.equal(schedules, 1);
});

test('挂载创建会话并自动加载，卸载暂停；退出回放暂停并还原结果区', async () => {
  state.replay = null;
  state.replayOn = true;
  state.currentReport = report3();
  Api.replay = async () => data3();
  const ctx = instance();
  const scope = Vue.effectScope();
  scope.run(() => Replay.mounted.call(ctx));
  await new Promise(r => setTimeout(r, 0));
  assert.equal(state.replay.data.days.length, 3);
  state.replay.playing = true;
  Replay.unmounted.call(ctx);
  assert.equal(state.replay.playing, false); // 离开回放区即暂停
  state.replay.playing = true;
  ctx.exitReplay();
  assert.equal(state.replayOn, false); assert.equal(state.replay.playing, false);
  scope.stop();
});

test('切换股票不会被旧请求的加载状态卡住，旧响应不能覆盖新股票', async () => {
  const ctx = instance();
  state.replay = R.createSession(); state.currentReport = report3();
  const pending = new Map();
  Api.replayKline = (_id, code) => new Promise(resolve => pending.set(code, resolve));
  ctx.pickStock('sh600000');
  assert.equal(state.replay.klineLoading, true);
  ctx.pickStock('sz000001');
  assert.equal(pending.has('sz000001'), true, '新股票必须发起自己的请求');
  pending.get('sz000001')([{date: dates3[0], close: 20}]);
  await new Promise(r => setTimeout(r, 0));
  pending.get('sh600000')([{date: dates3[0], close: 10}]);
  await new Promise(r => setTimeout(r, 0));
  assert.equal(state.replay.klineLoading, false);
  assert.equal(state.replay.kline[0].close, 20);
});

test('回放所在页签隐藏时立即暂停；加载或错误状态不能开始播放', async () => {
  const ctx = instance(), scope = Vue.effectScope();
  state.activeTab = 2; state.replay = R.createSession(); state.replay.data = data3();
  scope.run(() => Replay.mounted.call(ctx));
  state.replay.playing = true;
  state.activeTab = 3;
  await Vue.nextTick();
  assert.equal(state.replay.playing, false);
  clearTimeout(ctx.timer); scope.stop();
  state.activeTab = 2;
  for (const flag of ['loading', 'klineLoading', 'error', 'klineError']) {
    const s = R.createSession(); s.data = data3(); s[flag] = flag.endsWith('Error') || flag === 'error' ? '失败' : true;
    R.play(s);
    assert.equal(s.playing, false, flag);
  }
});

test('成交原因保留实际规则名和参数，不能被通用目录说明替换', () => {
  const rules = [{path: 'risk', name: '固定止损5%', desc: '持仓亏损达到阈值时卖出', params: {ratio: 5}}];
  const text = R.reasonLines({reason: ['risk']}, rules).join('');
  assert.match(text, /固定止损5%/);
});

test('点击个股与图表：暂停、同股不重复请求、收益图含上证对照且未来截断', async () => {
  const ctx = instance();
  state.replay = R.createSession();
  state.replay.data = data3();
  state.replay.dateIdx = 1;
  state.currentReport = report3();
  let klineReqs = 0;
  Api.replayKline = async () => {klineReqs++; return [{date: dates3[0]}];};
  await ctx.pickStock('sh600000');
  assert.equal(state.replay.playing, false); assert.equal(klineReqs, 1);
  await ctx.pickStock('sh600000');
  assert.equal(klineReqs, 1); // 已有快照不重复请求
  ctx.renderEquity();
  const o = chart.options.at(-1);
  assert.equal(o.series.length, 2); // 策略 + 上证
  assert.deepEqual(o.series[0].data, [0, 1, null]);
  assert.deepEqual(o.series[1].data, [0.5, 1.2, null]);
  assert.deepEqual(o.series[0].markLine.data, [{xAxis: 1}]);
  assert.equal(o.animation, false);
});

test('K线渲染截至当前日期，B/S 标记用成交价并披露原始价，预热保留', () => {
  const ctx = instance();
  state.replay = R.createSession();
  state.replay.data = data3();
  state.replay.dateIdx = 1;
  state.currentReport = report3();
  state.replay.code = 'sh600000';
  state.replay.kline = [
    {date: '2024-12-30', open: 9, close: 9.5, low: 8.9, high: 9.6, volume: 10, dif: 0, dea: 0, macd: 0},
    ...dates3.map(d => ({date: d, open: 10, close: 10.5, low: 9.9, high: 10.6, volume: 100, dif: 0.1, dea: 0.05, macd: 0.05}))];
  ctx.renderKline();
  const o = chart.options.at(-1);
  const dates = o.xAxis[0].data;
  assert.ok(dates.includes('2024-12-30')); // 预热段保留
  assert.ok(!dates.includes(dates3[2])); // 未来截断
  const markers = o.series[0].markPoint.data;
  assert.equal(markers.length, 1); // d1 买入；d3 卖出尚未发生
  assert.match(markers[0].name, /成交 10\.01（原始 10\.00）/);
  assert.equal(o.animation, false);
});

test('模板渲染：摘要 7 字段、成交/持仓披露列、无模块级引用残留', () => {
  const ctx = instance();
  state.replay = R.createSession();
  // 首日同时持有成交与持仓，保证两张披露表都渲染（spec §4.2/§4.3）。
  state.replay.data = data3([
    day(dates3[0], 100000, 0, [trade(1, 'sh600000', 'buy')], [holding('sh600000')]),
    day(dates3[1], 101000, 1000),
    day(dates3[2], 100500, -500),
  ]);
  state.replay.dateIdx = 0;
  state.currentReport = report3();
  assert.equal(ctx.atEnd, false);
  assert.equal(ctx.progressText, '1/3');
  // 回归护栏：模板不得引用模块级 import（Options API 模板不可见）。
  assert.ok(!Replay.template.includes('@click="R.'));
  const render = Vue.compile(Replay.template, {decodeEntities: s => s, hoistStatic: false});
  const tree = render(ctx, []);
  const summary = elements(tree, v => v.props?.class === 'replay-summary')[0];
  assert.equal(summary.children.filter(c => c && c.type === 'span').length, 7);
  const headers = elements(tree, v => v.type === 'th').map(v => v.children);
  for (const text of ['买入日期', '成本单价', '总成本(含费)', '成交时间', '原始价', '成交价', '触发原因']) {
    assert.ok(headers.includes(text), `缺少列: ${text}`);
  }
  assert.ok(elements(tree, v => v.type === 'input' && v.props?.type === 'range').length >= 1);
  state.replay.dateIdx = 1; // 无成交日 → 显式提示而非空表
  assert.equal(ctx.progressText, '2/3');
  const tree2 = render(ctx, []);
  assert.ok(elements(tree2, v => v.type === 'p' && v.children?.includes('当日无成交。')).length >= 1);
  state.replay.code = 'sh600000';
  const detail = render(ctx, []);
  assert.ok(elements(detail, v => typeof v.children === 'string' && v.children.includes('MA金叉')).length > 0, '持仓日也可查看此前成交原因');
  assert.equal(ctx.fmtPnL(10.5), '+10.50');
  assert.equal(ctx.fmtPnL(-0.1), '-0.10');
});

test('Tab2Compare 已接入回放入口与报告切换重置', () => {
  const src = read('./lab/js/components/Tab2Compare.js');
  assert.ok(src.includes("import BacktestReplay from './BacktestReplay.js'"));
  assert.ok(src.includes("import { createSession, resetSession } from '../replay.js'"));
  assert.ok(src.includes('<backtest-replay'), '缺少回放组件挂载');
  assert.ok(src.includes('resetReplay(); // 报告切换后旧回放会话整体失效'));
  assert.ok(src.includes('hasReplay'));
});
