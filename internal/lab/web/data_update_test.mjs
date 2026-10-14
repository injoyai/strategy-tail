import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

const read = path => fs.readFileSync(new URL(path, import.meta.url), 'utf8');
const moduleURL = source => 'data:text/javascript;base64,' + Buffer.from(source).toString('base64');
const Vue = await import(moduleURL(read('./lab/js/vendor/vue.esm-browser.prod.js')));
const api = globalThis.dataUpdateTestApi = {};
const source = read('./lab/js/components/DataUpdate.js')
  .replace("import { state } from '../store.js';", 'const state = {};')
  .replace("import { Api } from '../api.js';", 'const Api = globalThis.dataUpdateTestApi;');
const {default: DataUpdate, isMarketDataFresh} = await import(moduleURL(source));
function instance(status, loading = false) {
  const ctx = {...DataUpdate.data(), status, loading, now: Date.parse('2026-10-14T20:00:00+08:00'), state: {}, startTask() {}};
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

test('上海收盘后至自然日次日开盘前，只有该窗口内更新才显示已更新', () => {
  const cases = [
    ['2026-10-14T15:00:00+08:00', '2026-10-14T15:00:00+08:00', true],
    ['2026-10-14T14:59:59+08:00', '2026-10-14T20:00:00+08:00', false],
    ['2026-10-14T16:00:00+08:00', '2026-10-14T23:59:59+08:00', true],
    ['2026-10-14T16:00:00+08:00', '2026-10-15T00:00:00+08:00', true],
    ['2026-10-15T02:00:00+08:00', '2026-10-15T09:29:59+08:00', true],
    ['2026-10-14T16:00:00+08:00', '2026-10-15T09:30:00+08:00', false],
    ['2026-10-15T10:00:00+08:00', '2026-10-15T14:00:00+08:00', false],
    ['2026-10-14T16:00:00+08:00', '2026-10-15T15:00:00+08:00', false],
    ['2026-10-14T16:00:00+08:00', '2026-10-14T15:59:59+08:00', false],
    ['2026-12-31T16:00:00+08:00', '2027-01-01T09:29:59+08:00', true],
    ['2026-10-14T08:00:00Z', '2026-10-15T01:29:59Z', true],
  ];
  for (const [lastSuccess, now, expected] of cases) {
    assert.equal(isMarketDataFresh({lastSuccess}, Date.parse(now)), expected, `${lastSuccess} / ${now}`);
  }
});

test('部分成功可单独满足新鲜度；跳过、失败结束时间、无效或未来记录不能冒充更新', () => {
  const now = Date.parse('2026-10-14T20:00:00+08:00');
  assert.equal(isMarketDataFresh({lastPartialSuccess: finished}, now), true);
  assert.equal(isMarketDataFresh({lastSuccess: success, lastPartialSuccess: finished}, now), true);
  assert.equal(isMarketDataFresh({state: 'done', skipped: true, finishedAt: finished}, now), false);
  assert.equal(isMarketDataFresh({state: 'error', finishedAt: finished}, now), false);
  assert.equal(isMarketDataFresh({lastSuccess: 'invalid', lastPartialSuccess: 'invalid'}, now), false);
  assert.equal(isMarketDataFresh({lastSuccess: finished, lastSuccessError: '无法读取'}, now), false);
  assert.equal(isMarketDataFresh({lastSuccessError: '无法读取', lastPartialSuccess: finished}, now), true);
  assert.equal(isMarketDataFresh({lastPartialSuccess: '2026-10-14T21:00:00+08:00'}, now), false);
  assert.equal(isMarketDataFresh(null, now), false);
  const ctx = instance({lastPartialSuccess: finished, config: {}});
  ctx.now = Date.parse('2026-10-15T09:29:59+08:00');
  assert.equal(ctx.bubbleText, '已更新');
  ctx.now = Date.parse('2026-10-15T09:30:00+08:00');
  assert.equal(ctx.bubbleText, '未更新');
});

test('即使状态读取失败，现有轮询仍推进时钟，让开盘后的颜色及时失效', async () => {
  const ctx = instance({lastPartialSuccess: finished, config: {}});
  ctx.now = Date.parse('2026-10-15T09:29:59+08:00');
  assert.equal(ctx.fresh, true);
  const originalNow = Date.now;
  Date.now = () => Date.parse('2026-10-15T09:30:00+08:00');
  api.dataUpdateStatus = async () => {throw new Error('offline');};
  try {
    await ctx.poll();
    assert.equal(ctx.fresh, false);
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
  assert.equal(ctx.bubbleText, '25%');
  assert.ok(!text(render(ctx)).includes('最近检查结束'));
  ctx.status.total = 0;
  assert.equal(ctx.bubbleText, '更新中');
  ctx.status = {state: 'error', failed: 2, total: 6400, done: 6400, lastSuccess: success, lastPartialSuccess: finished, config: {}};
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
  assert.equal(ctx.bubbleText, '未更新');
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
