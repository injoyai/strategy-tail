import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

const read = path => fs.readFileSync(new URL(path, import.meta.url), 'utf8');
const moduleURL = source => 'data:text/javascript;base64,' + Buffer.from(source).toString('base64');
const Vue = await import(moduleURL(read('./lab/js/vendor/vue.esm-browser.prod.js')));
globalThis.dataUpdateTestFormat = await import(moduleURL(read('./lab/js/format.js')));
const api = globalThis.dataUpdateTestApi = {};
const source = read('./lab/js/components/DataUpdate.js')
  .replace("import { state } from '../store.js';", 'const state = {};')
  .replace("import { taskProgress } from '../format.js';", 'const {taskProgress} = globalThis.dataUpdateTestFormat;')
  .replace("import { Api } from '../api.js';", 'const Api = globalThis.dataUpdateTestApi;');
const {default: DataUpdate, marketDataState} = await import(moduleURL(source));
function instance(status, loading = false) {
  const ctx = {...DataUpdate.data(), status: status && {tradingDate: '2026-10-14', isTradingDay: true, ...status}, loading, now: Date.parse('2026-10-14T20:00:00+08:00'), state: {}, startTask() {}};
  for (const [key, fn] of Object.entries(DataUpdate.methods)) ctx[key] = fn.bind(ctx);
  for (const [key, fn] of Object.entries(DataUpdate.computed)) Object.defineProperty(ctx, key, {get: () => fn.call(ctx)});
  return ctx;
}
function text(v) {
  if (typeof v === 'string') return v;
  if (Array.isArray(v)) return v.map(text).join('');
  return v && typeof v === 'object' ? text(v.children) : '';
}
const render = ctx => Vue.compile(DataUpdate.template, {decodeEntities: s => s, hoistStatic: false})(ctx, []);
const success = '2026-10-12T08:00:00Z';
const finished = '2026-10-14T07:49:26Z';

test('日线、分钟线独立计数，估值和保存期间使用不定进度', () => {
  const ctx = instance({state: 'running', stage: 'day', done: 2, total: 5, stageDone: 2, stageTotal: 2, config: {}});
  assert.equal(ctx.progressPercent, 100);
  assert.equal(ctx.bubbleText, '日线更新');
  ctx.status = {...ctx.status, stage: 'minute', stageDone: 0};
  assert.equal(ctx.progressPercent, 0);
  assert.equal(ctx.progressInfo.counts, '0/2 个标的');
  assert.equal(ctx.bubbleText, '分钟更新');
  for (const stage of ['valuation', 'finalize']) {
    ctx.status = {...ctx.status, stage, done: 5, stageDone: 0, stageTotal: 0};
    assert.equal(ctx.progressPercent, null);
    assert.ok(!text(render(ctx)).includes('100%'));
    const nodes = v => Array.isArray(v) ? v.flatMap(nodes) : v && typeof v === 'object' ? [v, ...nodes(v.children)] : [];
    const progress = nodes(render(ctx)).find(v => v.type === 'progress');
    assert.equal(progress.props.value, null);
    assert.ok(progress.props.class.includes('indeterminate'));
  }
});

test('完整更新时间覆盖加载、无记录、读取失败和成功状态', () => {
  for (const [status, loading, expected] of [
    [null, true, '读取中…'],
    [null, false, '读取失败'],
    [{state: 'idle', config: {enabled: false}}, false, '暂无完整成功记录'],
    [{lastSuccess: success, config: {}, lastSuccessError: '无法读取行情库的完整更新时间'}, false, '读取失败'],
    [{lastSuccess: success, config: {}}, false, '2026/10/12 16:00:00'],
  ]) {
    const ctx = instance(status, loading);
    assert.equal(ctx.lastUpdateText, expected);
    assert.match(text(render(ctx)), /最后完整更新（上海时间）/);
    assert.ok(text(render(ctx)).includes(expected));
  }
});

test('部分成功显示结束时间和失败数，同时保留上次完整更新时间', () => {
  const ctx = instance({state: 'error', lastSuccess: success, lastPartialSuccess: finished, finishedAt: finished, done: 6400, failed: 2, total: 6400, config: {}});
  const displayed = text(render(ctx));
  assert.ok(displayed.includes('2026/10/12 16:00:00'));
  assert.ok(displayed.includes('最近检查结束：2026/10/14 15:49:26'));
  assert.ok(displayed.includes('部分成功（失败 2/6400）'));
  assert.equal(ctx.fresh, true);
  assert.equal(ctx.bubbleText, '已更新');
});

test('先判断交易日，15:30 后才判断当日行情是否更新', () => {
  const cases = [
    ['2026-10-17T20:00:00+08:00', true, finished, '休市'],
    ['2026-10-18T20:00:00+08:00', true, finished, '休市'],
    ['2026-10-01T20:00:00+08:00', false, finished, '休市'],
    ['2026-10-14T08:00:00+08:00', true, success, '未收盘'],
    ['2026-10-14T09:30:00+08:00', true, success, '未收盘'],
    ['2026-10-14T15:29:59+08:00', true, finished, '未收盘'],
    ['2026-10-14T15:30:00+08:00', true, '2026-10-14T15:30:00+08:00', '已更新'],
    ['2026-10-14T20:00:00+08:00', true, '2026-10-14T15:29:59+08:00', '未更新'],
    ['2026-10-14T23:59:59+08:00', true, finished, '已更新'],
    ['2026-10-15T00:00:00+08:00', true, finished, '未收盘'],
    ['2026-10-15T15:30:00+08:00', true, finished, '未更新'],
    ['2026-10-14T15:59:59+08:00', true, '2026-10-14T16:00:00+08:00', '未更新'],
  ];
  for (const [now, isTradingDay, lastSuccess, expected] of cases) {
    const tradingDate = now.slice(0, 10);
    const status = {tradingDate, isTradingDay, lastSuccess};
    assert.equal(marketDataState(status, Date.parse(now)), expected, now);
    const ctx = instance(status); ctx.now = Date.parse(now);
    assert.equal(ctx.bubbleText, expected);
    const neutral = ['休市', '未收盘'].includes(expected);
    assert.equal(ctx.fresh, neutral ? null : expected === '已更新');
    assert.equal(ctx.bubbleLabel.includes(expected), true);
    if (neutral) assert.equal(ctx.fresh, null, '中性状态不使用未更新的警告色');
  }
});

test('缺少或跨日失效的交易日信息显示待确认，不猜测工作日就是交易日', () => {
  const now = Date.parse('2026-10-14T20:00:00+08:00');
  for (const status of [null, {}, {tradingDate: '2026-10-13', isTradingDay: true},
    {tradingDate: '2026-10-14'}, {tradingDate: '2026-10-14', isTradingDay: null}]) {
    assert.equal(marketDataState(status, now), '待确认');
  }
});

test('部分成功可满足更新条件；跳过、失败结束时间、无效或未来记录不能冒充更新', () => {
  const now = Date.parse('2026-10-14T20:00:00+08:00');
  const cases = [
    [{lastPartialSuccess: finished}, '已更新'],
    [{lastSuccess: success, lastPartialSuccess: finished}, '已更新'],
    [{state: 'done', skipped: true, finishedAt: finished}, '未更新'],
    [{state: 'error', finishedAt: finished}, '未更新'],
    [{lastSuccess: 'invalid', lastPartialSuccess: 'invalid'}, '未更新'],
    [{lastSuccess: finished, lastSuccessError: '无法读取'}, '未更新'],
    [{lastSuccessError: '无法读取', lastPartialSuccess: finished}, '已更新'],
    [{lastPartialSuccess: '2026-10-14T21:00:00+08:00'}, '未更新'],
  ];
  for (const [status, expected] of cases) {
    assert.equal(marketDataState({tradingDate: '2026-10-14', isTradingDay: true, ...status}, now), expected);
  }
});

test('状态读取失败仍推进时钟，跨日后不沿用昨天的交易日及更新状态', async () => {
  const ctx = instance({lastPartialSuccess: finished, config: {}});
  ctx.now = Date.parse('2026-10-14T23:59:59+08:00');
  assert.equal(ctx.fresh, true);
  const originalNow = Date.now;
  Date.now = () => Date.parse('2026-10-15T00:00:00+08:00');
  api.dataUpdateStatus = async () => {throw new Error('offline');};
  try {
    await ctx.poll();
    assert.equal(ctx.fresh, null);
    assert.equal(ctx.marketState, '待确认');
    assert.ok(ctx.error.includes('offline'));
  } finally {
    clearTimeout(ctx.timer);
    Date.now = originalNow;
    delete api.dataUpdateStatus;
  }
});

test('跳过检查不会显示为刚刚成功更新，运行中显示开始时间', () => {
  const ctx = instance({state: 'done', skipped: true, lastSuccess: success, finishedAt: finished, config: {}});
  assert.equal(ctx.checkResult, '已跳过');
  assert.equal(ctx.lastUpdateText, '2026/10/12 16:00:00');
  ctx.status = {state: 'running', startedAt: finished, lastSuccess: success, config: {}};
  const displayed = text(render(ctx));
  assert.ok(displayed.includes('本次开始：2026/10/14 15:49:26'));
  assert.ok(!displayed.includes('最近检查结束'));
});

test('时间显示固定上海时区，损坏记录有明确文案', () => {
  const ctx = instance(null);
  assert.equal(ctx.displayTime('2026-10-13T16:00:00Z'), '2026/10/14 00:00:00');
  assert.equal(ctx.displayTime('invalid'), '时间记录无效');
});

test('无效自动更新时间在弹出详情中保留输入并关联错误、聚焦字段', async () => {
  const ctx = instance({config: {}});
  let focused = false;
  ctx.time = ''; ctx.$refs = {time: {focus() {focused = true;}}};
  await ctx.save();
  assert.equal(ctx.time, '');
  assert.equal(ctx.timeError, true);
  assert.equal(focused, true);
  assert.ok(text(render(ctx)).includes('请输入有效的更新时间'));
});

test('气泡显示真实更新进度、失败与读取异常，详情保持默认折叠', () => {
  const ctx = instance({state: 'idle', lastSuccess: success, config: {}});
  assert.equal(ctx.bubbleText, '未更新');
  assert.ok(ctx.bubbleLabel.includes('2026/10/12 16:00:00'));
  ctx.status = {state: 'running', total: 6400, done: 1600, startedAt: finished, finishedAt: success, config: {}};
  assert.equal(ctx.bubbleText, '更新中');
  assert.ok(ctx.bubbleLabel.includes('本阶段 25%'));
  assert.ok(!text(render(ctx)).includes('最近检查结束'));
  ctx.status.total = 0;
  assert.equal(ctx.bubbleText, '更新中');
  ctx.status = {tradingDate: '2026-10-14', isTradingDay: true, state: 'error', failed: 2, total: 6400, done: 6400, lastSuccess: success, lastPartialSuccess: finished, config: {}};
  assert.equal(ctx.bubbleText, '已更新');
  assert.equal(ctx.fresh, true);
  ctx.error = '无法读取更新状态：离线';
  assert.equal(ctx.bubbleText, '异常');
  const tree = render(ctx);
  const elements = v => Array.isArray(v) ? v.flatMap(elements) : v && typeof v === 'object' ? [v, ...elements(v.children)] : [];
  const nodes = elements(tree);
  const bubble = nodes.find(v => v.props?.popovertarget && v.props?.class === 'data-update-bubble');
  const panel = nodes.find(v => v.props?.id === bubble.props.popovertarget);
  assert.equal(bubble.type, 'button');
  assert.equal(bubble.props['aria-label'], ctx.bubbleLabel);
  assert.equal(bubble.props['data-fresh'], 'true');
  assert.equal(panel.props.popover, 'auto');
  assert.ok(!panel.props.open);
  assert.ok(text(panel).includes(ctx.error));
  ctx.error = ''; ctx.status = null; ctx.loading = true;
  assert.equal(ctx.bubbleText, '读取中');
  ctx.loading = false; assert.equal(ctx.bubbleText, '异常');
  ctx.status = {state: 'idle', config: {}};
  assert.equal(ctx.bubbleText, '待确认');
});

test('长错误详情默认收起，失败结果仍可见', () => {
  const ctx = instance({state: 'error', lastSuccess: success, finishedAt: finished, failed: 2, total: 6400,
    error: '日线响应为空，保留已有数据', config: {}});
  const tree = render(ctx);
  const find = v => {
    if (Array.isArray(v)) return v.flatMap(find);
    if (!v || typeof v !== 'object') return [];
    return [...(v.type === 'details' && v.props?.class.includes('error-details') ? [v] : []), ...find(v.children)];
  };
  const [details] = find(tree);
  assert.ok(details);
  assert.ok(!details.props.open);
  assert.ok(text(details).includes(ctx.status.error));
  assert.ok(text(tree).includes('部分失败（2/6400）'));
});
