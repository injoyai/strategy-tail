import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

const read = p => fs.readFileSync(new URL(p, import.meta.url), 'utf8');
const moduleURL = source => 'data:text/javascript;base64,' + Buffer.from(source).toString('base64');
const Vue = await import(moduleURL(read('./lab/js/vendor/vue.esm-browser.prod.js')));
const state = {}, Api = {};
globalThis.reportHistoryTest = {...Vue, state, Api, dlgMixin: {}};
const source = read('./lab/js/components/ReportHistory.js').replace(/^import (\{.*\}) from .*;$/gm,
  'const $1 = globalThis.reportHistoryTest;');
const {default: History} = await import(moduleURL(source));
const report = id => ({config: {scriptName: 'builder'}, finishedAt: id, variants: []});
const item = id => ({id, time: id, script: 'builder', name: '策略一', startYear: '2025', endYear: '2026'});
function instance() {
  Object.assign(state, {currentReport: null, selectedVariant: 'old', taskRunning: false});
  for (const k of Object.keys(Api)) delete Api[k];
  const ctx = {...History.data(), ...History.setup(), open: false,
    $refs: {cancelDelete: {focus() {}}, historySelect: {focus() {}}, refreshHistory: {focus() {}}}, $nextTick: fn => fn()};
  for (const [k, fn] of Object.entries(History.methods)) ctx[k] = fn.bind(ctx);
  for (const [k, fn] of Object.entries(History.computed)) Object.defineProperty(ctx, k, {get: () => fn.call(ctx)});
  return ctx;
}
const deferred = () => {let resolve; const promise = new Promise(r => {resolve = r;}); return {promise, resolve};};

test('首次从磁盘历史加载最新结果，切换历史同时清除变体筛选', async () => {
  const ctx = instance();
  Api.reports = async () => [item('new'), item('old')];
  Api.report = async id => report(id);
  await ctx.refresh();
  assert.equal(ctx.selected.id, 'new');
  assert.equal(state.selectedVariant, null);
  await ctx.loadReport('old');
  assert.equal(ctx.selected.id, 'old');
  await ctx.refresh();
  assert.equal(ctx.selected.id, 'old'); // 刷新不把用户切回最新。
});

test('详情读取失败保留旧结果并重试原目标；旧响应不覆盖新的选择或完成报告', async () => {
  const ctx = instance();
  state.currentReport = report('old');
  Api.report = async () => {throw new Error('offline');};
  await ctx.loadReport('failed');
  assert.equal(state.currentReport.finishedAt, 'old');
  assert.match(ctx.error, /读取失败/);
  Api.report = async id => report(id);
  await ctx.retry();
  assert.equal(state.currentReport.finishedAt, 'failed');
  const first = deferred();
  Api.report = id => id === 'first' ? first.promise : Promise.resolve(report(id));
  const a = ctx.loadReport('first');
  await ctx.loadReport('second'); first.resolve(report('first')); await a;
  assert.equal(state.currentReport.finishedAt, 'second');
  const slow = deferred(); Api.report = () => slow.promise;
  const pending = ctx.loadReport('slow');
  state.currentReport = report('just-completed');
  slow.resolve(report('slow')); await pending;
  assert.equal(state.currentReport.finishedAt, 'just-completed');
  assert.equal(ctx.busy, false);
});

test('删除确认默认聚焦取消；失败保留对话框和结果，提交期间禁止重复删除与关闭', async () => {
  const ctx = instance(); ctx.reports = [item('one')]; state.currentReport = report('one');
  let focused = false;
  ctx.$refs.cancelDelete.focus = () => {focused = true;};
  ctx.openDelete(); assert.equal(ctx.open, true); assert.equal(focused, true);
  Api.deleteReport = async () => {throw new Error('busy');};
  await ctx.deleteReport();
  assert.equal(ctx.open, true); assert.match(ctx.deleteError, /删除失败/);
  assert.equal(state.currentReport.finishedAt, 'one');
  const pending = deferred(); let calls = 0;
  Api.deleteReport = () => {calls++; return pending.promise;};
  const deleting = ctx.deleteReport();
  await ctx.deleteReport(); ctx.closeDlg();
  assert.equal(calls, 1); assert.equal(ctx.open, true);
  pending.resolve({ok: true}); await deleting;
  assert.equal(ctx.open, false); assert.equal(ctx.reports.length, 0);
  assert.equal(state.currentReport, null); assert.equal(state.selectedVariant, null);
  assert.match(ctx.message, /已删除/);
});

test('删除后载入剩余最近记录，空态和确认模板可编译', async () => {
  const ctx = instance(); ctx.reports = [item('new'), item('old')]; state.currentReport = report('new');
  ctx.openDelete();
  let focusRestored = false;
  ctx.$refs.historySelect.focus = () => {assert.equal(ctx.busy, false); focusRestored = true;};
  Api.deleteReport = async () => ({ok: true}); Api.report = async id => report(id);
  await ctx.deleteReport();
  ctx.onDialogClose();
  assert.equal(ctx.selected.id, 'old');
  assert.equal(focusRestored, true);
  assert.deepEqual(ctx.reports.map(r => r.id), ['old']);
  const render = Vue.compile(History.template, {decodeEntities: s => s, hoistStatic: false});
  assert.doesNotThrow(() => render(ctx, []));
  ctx.reports = []; state.currentReport = null;
  assert.doesNotThrow(() => render(ctx, []));
  assert.match(History.template, /<Teleport to="body">/);
  assert.match(History.template, /@cancel.prevent="closeDlg"/);
});

test('报告 API 对 ID 编码并使用 DELETE 删除', async () => {
  const calls = [], previous = globalThis.fetch;
  globalThis.fetch = async (url, options) => {calls.push([url, options.method]); return {ok: true, json: async () => ({})};};
  try {
    const {Api: api} = await import(moduleURL(read('./lab/js/api.js')));
    await api.report('中文/one'); await api.deleteReport('中文/one');
    assert.deepEqual(calls, [['/api/report/' + encodeURIComponent('中文/one'), 'GET'], ['/api/report/' + encodeURIComponent('中文/one'), 'DELETE']]);
  } finally { globalThis.fetch = previous; }
});

test('切换或删除报告时清除旧 K 线，晚到响应不恢复旧成交图', async () => {
  globalThis.window = {matchMedia: () => ({matches:false})};
  Object.assign(globalThis.reportHistoryTest, {ma() {}, mountChart() {}, dropChart() {}});
  const source = read('./lab/js/components/Tab3Trades.js').replace(/^import (\{.*\}) from .*;$/gm,
    'const $1 = globalThis.reportHistoryTest;');
  const {default: Trades} = await import(moduleURL(source));
  const ctx = {...Trades.data(), ...Trades.setup(), $refs:{}, $nextTick: fn => fn?.()};
  for (const [k, fn] of Object.entries(Trades.methods)) ctx[k] = fn.bind(ctx);
  for (const [k, fn] of Object.entries(Trades.computed)) Object.defineProperty(ctx, k, {get: () => fn.call(ctx)});
  state.currentReport = report('old');
  let requests = 0;
  const delayed = deferred(); Api.kline = () => {requests++; return delayed.promise;};
  let renders = 0; ctx.renderKline = () => {renders++;};
  const reading = ctx.showKline({code:'sh600000', buyTime:'2025-01-02', sellTime:'2025-01-03'});
  await Promise.resolve(); assert.equal(requests, 1);
  state.currentReport = null; ctx.resetReport();
  delayed.resolve([]); await reading;
  assert.equal(renders, 0); assert.equal(ctx.klineTrade, null); assert.equal(ctx.klineLoading, false);
});
