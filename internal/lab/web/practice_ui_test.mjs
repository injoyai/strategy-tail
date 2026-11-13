import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';
await import(new URL('../../chartui/kline.js', import.meta.url));
const K = globalThis.StrategyKline;


// 盘感练习前端测试：practice.js 纯函数、页签契约、Tab7Practice 交互逻辑
// （命令幂等、请求幂等、preparing 轮询发现失败、重练另建记录）、app.js
// 页签与任务分流集成。全部基于源码改写的 data: URL 导入，不发真实请求。
const read = p => fs.readFileSync(new URL(p, import.meta.url), 'utf8');
const moduleURL = s => 'data:text/javascript;base64,' + Buffer.from(s).toString('base64');
const Vue = await import(moduleURL(read('./lab/js/vendor/vue.esm-browser.prod.js')));
const practice = await import(moduleURL(read('./lab/js/practice.js').replace("'./format.js'", JSON.stringify(moduleURL(read('./lab/js/format.js'))))));
const constants = await import(moduleURL(read('./lab/js/constants.js')));
const format = await import(moduleURL(read('./lab/js/format.js')));

const state = {}, Api = {};
let lastUrl = '';
let chartFactory = () => null;
globalThis.window = {matchMedia: () => ({matches: false})};
globalThis.location = {search: '', pathname: '/'};
globalThis.history = {replaceState(_s, _t, url) {lastUrl = url;}};
globalThis.practiceTest = {...Vue, ...practice, ...format, state, Api,
  mountKline: el => chartFactory(el), dropKline(c) {c?.dispose?.();}};

const Tab7Src = read('./lab/js/components/Tab7Practice.js')
  .replace(/^import (\{.*\}) from .*;$/gm, 'const $1 = globalThis.practiceTest;');
const {default: Tab7} = await import(moduleURL(Tab7Src));

const appSrc = read('./lab/js/app.js')
  .replace(/^import (\w+) from .*;$/gm, 'const $1 = {};')
  .replace(/^import (\{.*\}) from .*;$/gm, 'const $1 = globalThis.practiceTest;')
  .replace("createApp(App).mount('#app');", 'export default App;');
globalThis.practiceTest.taskProgress = format.taskProgress;
globalThis.practiceTest.TABS = constants.TABS;
globalThis.practiceTest.NAV_GROUPS = constants.NAV_GROUPS;
globalThis.practiceTest.TAB_SLUG = constants.TAB_SLUG;
globalThis.practiceTest.TAB_NUM = constants.TAB_NUM;
globalThis.practiceTest.TASK_LABEL = constants.TASK_LABEL;
globalThis.practiceTest.animatePanel = () => {};
globalThis.practiceTest.animateStatus = () => {};
globalThis.practiceTest.initMotion = () => {};
globalThis.practiceTest.runtime = {pollTimer: null};
const {default: App} = await import(moduleURL(appSrc));

// Tab7Practice 组件实例：data + setup 展开、methods 绑定、computed 求值。
function instance(over = {}) {
  const ctx = {
    ...Tab7.setup(), ...Tab7.data(), training: null, historyOnly: false, initialSessionId: '',
    $nextTick: async fn => fn?.(),
    $refs: {klineChart: {}, reviewChart: {}},
    startTask() {},
    ...over,
  };
  for (const [k, fn] of Object.entries(Tab7.methods)) ctx[k] = fn.bind(ctx);
  for (const [k, fn] of Object.entries(Tab7.computed)) Object.defineProperty(ctx, k, {get: () => fn.call(ctx)});
  return ctx;
}

// —— practice.js 纯函数 ——

test('练习阶段、仓位、结果与失败原因的文案映射覆盖后端常量', () => {
  assert.equal(practice.practicePhaseLabel('preparing'), '出题中');
  assert.equal(practice.practicePhaseLabel('active'), '进行中');
  assert.equal(practice.practicePhaseLabel('finished'), '已结束');
  assert.equal(practice.practicePhaseLabel('failed'), '出题失败');
  assert.equal(practice.practicePhaseLabel('cancelled'), '已取消');
  assert.equal(practice.practiceStageLabel('waiting'), '等待买入');
  assert.equal(practice.practiceStageLabel('holding'), '持仓中');
  assert.equal(practice.practiceStageLabel('sold'), '已卖出');
  assert.equal(practice.practiceEndReasonLabel('skipped'), '放弃买入，本题结束');
  assert.equal(practice.practiceEndReasonLabel('data_end'), '行情已用尽，本题自动结束');
  assert.equal(practice.practiceEndReasonLabel('finished_untraded'), '手动结束（未交易）');
  assert.equal(practice.practiceEndReasonLabel('finished_open'), '手动结束（持仓按当日收盘估值，未虚构卖出）');
  assert.equal(practice.practiceEndReasonLabel('finished_sold'), '手动结束（已卖出）');
  assert.ok(practice.practiceFailReasonLabel('no_match').includes('没有命中机会'));
  assert.ok(practice.practiceFailReasonLabel('strategy').includes('策略'));
  assert.ok(practice.practiceFailReasonLabel('data').includes('行情'));
  assert.ok(practice.practiceFailReasonLabel('interrupted').includes('中断'));
  assert.equal(practice.practiceActionLabel('buy'), '买入');
  assert.equal(practice.practiceActionLabel('sell'), '卖出');
  // 未知值原样返回，不抛错不吞阶段。
  assert.equal(practice.practicePhaseLabel('other'), 'other');
});

test('相对日标签不暴露真实日期，幂等 ID 非空唯一，格式化容忍缺失', () => {
  assert.equal(practice.practiceDayLabel(0), '信号日');
  assert.equal(practice.practiceDayLabel(-3), '前3日');
  assert.equal(practice.practiceDayLabel(5), '+5');
  const ids = new Set();
  for (let i = 0; i < 50; i++) {
    const id = practice.newCommandId();
    assert.ok(id && id.length >= 8);
    ids.add(id);
  }
  assert.equal(ids.size, 50);
  assert.equal(practice.fmtMoney(null), '—');
  assert.equal(practice.fmtMoney(NaN), '—');
  assert.equal(practice.fmtMoney(123.456), '123.46');
  assert.equal(practice.fmtPct(null), '—');
  assert.equal(practice.fmtPct(12.345), '+12.35%');
  assert.equal(practice.fmtMoney(1, true), '+1.00');
  assert.equal(practice.fmtPct(Infinity), '—');
  assert.equal(practice.pnlClass(1), 'pos');
  assert.equal(practice.pnlClass(-1), 'neg');
  assert.equal(practice.pnlClass(0), '');
});

test('盲练 K 线横轴为相对日序号且不含真实日期，复盘横轴为真实日期', () => {
  const days = [
    {day: -1, open: 10, high: 11, low: 9, close: 10.5, volume: 100, ma5: null, ma10: null, ma20: null, dif: null, dea: null, hist: null},
    {day: 0, date: '2024-05-06', open: 10.5, high: 11, low: 10, close: 10.8, volume: 120, ma5: 10.4, ma10: null, ma20: null, dif: 0.1, dea: 0.05, hist: 0.05},
  ];
  const blind = practice.practiceKlineData({days, markers: [{day: 0, price: 10.8, kind: 'buy'}]});
  assert.deepEqual(K.normalize(blind).data.map(d=>d.label), ['前1日', '信号日']);
  assert.ok(!JSON.stringify(K.normalize(blind)).includes('2024-05-06'), '盲练不得包含真实日期');
  assert.equal(K.normalize(blind).missing.length, 0);
  const review = practice.practiceKlineData({days, markers: [], showDate: true, title: 'sh600000 · 信号日 2024-05-06'});
  assert.deepEqual(K.normalize(review).data.map(d=>d.label), ['前1日', '2024-05-06']);
  assert.ok(review.title.includes('sh600000'));
});

test('买卖与估值标记按已揭示日定位，越界标记进入 missing 不绘制', () => {
  const day = (d, close) => ({day: d, open: close, high: close, low: close, close, volume: 1,
    ma5: null, ma10: null, ma20: null, dif: null, dea: null, hist: null});
  const days = [day(0, 10), day(1, 11)];
  const hits = practice.practiceKlineData({days, markers: [
    {day: 0, price: 10, kind: 'buy'}, {day: 1, price: 11, kind: 'sell'}, {day: 1, price: 11, kind: 'est'}]});
  const marks = K.normalize(hits).markers;
  assert.equal(marks.length, 3);
  assert.equal(marks[0].label, 'B');
  assert.equal(marks[0].kind, 'buy');
  assert.equal(marks[1].label, 'S');
  assert.equal(marks[1].kind, 'sell');
  assert.equal(marks[2].label, '估');
  assert.equal(marks[2].kind, 'est');
  const miss = practice.practiceKlineData({days, markers: [{day: 9, price: 10, kind: 'sell'}]});
  assert.equal(K.normalize(miss).markers.length, 0);
  assert.equal(K.normalize(miss).missing.length, 1);
});

// —— 页签契约（既有编号与 URL 行为保持） ——

test('页签 1–6 编号与 slug 不变，07 为盘感练习', () => {
  assert.deepEqual(Object.entries(constants.TAB_SLUG).map(([k, v]) => [Number(k), v]),
    [[1, 'strategy'], [2, 'compare'], [3, 'trades'], [4, 'factor'], [5, 'candidates'], [6, 'portfolio'], [7, 'practice'], [8, 'screen']]);
  assert.equal(constants.TAB_NUM.practice, 7);
  assert.deepEqual(constants.TABS.map(t => t.n), [1, 2, 3, 4, 5, 6, 7, 8]);
  assert.equal(constants.TASK_LABEL.practice_prepare, '盘感练习出题');
});

test('切到 07 保留不透明会话 ID 参数，切其他页签不携带', () => {
  globalThis.location = {search: '?tab=practice&p=abc123', pathname: '/'};
  const ctx = instance();
  ctx.switchTab = App.methods.switchTab.bind({...App.data(), state, $nextTick: fn => fn?.(), $refs: {}});
  ctx.switchTab(7);
  assert.equal(state.activeTab, 7);
  assert.ok(lastUrl.includes('tab=practice'), lastUrl);
  assert.ok(lastUrl.includes('p=abc123'), lastUrl);
  ctx.switchTab(2);
  assert.equal(state.activeTab, 2);
  assert.equal(lastUrl, '?tab=compare');
});

test('practice_prepare 完成分流到 07 页钩子，不读取回测报告', async () => {
  const tab7 = {calls: [], async practiceOnTaskDone(id) {this.calls.push(id);}};
  const ctx = {
    ...App.data(), state, switchTab() {},
    $refs: {tab7},
    $nextTick: async fn => fn?.(),
  };
  for (const [k, fn] of Object.entries(App.methods)) ctx[k] = fn.bind(ctx);
  Api.status = async () => ({state: 'done', task: 'practice_prepare', runId: 'px', progress: 100});
  Api.latestReport = async () => {throw new Error('不应读取回测报告');};
  await ctx.pollStatusOnce();
  assert.deepEqual(tab7.calls, ['px']);
  assert.equal(state.taskDock.title, '盘感练习出题已完成');
});

// —— Tab7Practice 交互逻辑 ——

test('动作命令网络中断保留同一命令编号，重试不产生新命令', async () => {
  const sent = [];
  Api.practiceAction = async (id, cmd) => {
    sent.push({id, cmd});
    throw new Error('网络中断'); // fetch 失败无 status
  };
  const vm = instance({sess: {id: 'p1', phase: 'active', revision: 4, stage: 'waiting', days: [], currentDay: 0}});
  await vm.doAction('buy');
  assert.equal(sent.length, 1);
  assert.ok(vm.pendingCmd);
  assert.equal(vm.pendingCmd.commandId, sent[0].cmd.commandId);
  assert.equal(vm.pendingCmd.expectedRevision, 4);
  assert.equal(vm.pendingCmd.action, 'buy');
  assert.ok(vm.actionMsg.text.includes('未确认'));
  assert.ok(vm.actionMsg.text.includes('不会重复成交'));
  Api.practiceAction = async (id, cmd) => {
    sent.push({id, cmd});
    return {...vm.sess, revision: 5, stage: 'holding', quantity: 200, buyDay: 0};
  };
  await vm.doAction('buy');
  assert.equal(sent.length, 2);
  assert.equal(sent[1].cmd.commandId, sent[0].cmd.commandId, '重试必须复用同一 commandId');
  assert.equal(vm.pendingCmd, null);
  assert.equal(vm.sess.revision, 5);
  assert.equal(vm.actionMsg, null);
});

test('409 版本冲突放弃待确认命令并刷新服务端权威状态', async () => {
  let reloads = 0;
  Api.practiceAction = async () => {
    const e = new Error('版本冲突');
    e.status = 409;
    throw e;
  };
  const vm = instance({sess: {id: 'p1', phase: 'active', revision: 3, stage: 'waiting', days: []}});
  vm.loadSession = async () => { reloads++; };
  await vm.doAction('sell');
  assert.equal(vm.pendingCmd, null);
  assert.equal(reloads, 1);
});

test('重复点击被 actionBusy 挡住，非活跃会话不提交', async () => {
  let calls = 0;
  Api.practiceAction = async () => new Promise(r => setTimeout(() => { calls++; r({}); }, 5));
  const vm = instance({sess: {id: 'p1', phase: 'active', revision: 1, stage: 'waiting', days: []}});
  const first = vm.doAction('next');
  const second = vm.doAction('next');
  await first;
  await second;
  assert.equal(calls, 1);
  const done = instance({sess: {id: 'p2', phase: 'finished', days: []}});
  await done.doAction('buy');
  assert.equal(calls, 1, '已结束会话不得提交命令');
});

test('出题请求网络中断保留请求键，重试同键且成功后清空并载入会话', async () => {
  const reqs = [];
  state.builderStrategies = [{id: 's1', name: '策略A', version: 2}];
  Api.practiceStart = async req => {
    reqs.push(req);
    throw new Error('断网');
  };
  const vm = instance({pStrategyId: 's1', pFromYear: 2020, pToYear: 2024, pRequestId: ''});
  await vm.startPractice();
  assert.ok(vm.pRequestId, '网络失败应保留请求键');
  assert.equal(vm.startMsg.ok, false);
  const first = vm.pRequestId;
  const loads = [];
  vm.loadSession = async id => { loads.push(id); };
  Api.practiceStart = async req => { reqs.push(req); return {sessionId: 'p9', started: true}; };
  await vm.startPractice();
  assert.equal(reqs.length, 2);
  assert.equal(reqs[0].requestId, reqs[1].requestId, '重试必须复用同一 requestId');
  assert.equal(reqs[1].requestId, first);
  assert.equal(reqs[1].strategyId, 's1');
  assert.equal(reqs[1].strategyVersion, 2);
  assert.equal(reqs[1].fromYear, 2020);
  assert.equal(reqs[1].toYear, 2024);
  assert.deepEqual(loads, ['p9']);
  assert.equal(vm.pRequestId, '');
});

test('出题 409 清空请求键并强制刷新策略列表', async () => {
  state.builderStrategies = [{id: 's1', name: '旧', version: 1}];
  let refreshed = 0;
  Api.practiceStart = async () => {
    const e = new Error('策略版本已更新，请刷新后重试');
    e.status = 409;
    throw e;
  };
  Api.builderStrategies = async () => { refreshed++; return {strategies: [{id: 's2', name: '新', version: 1}]}; };
  const vm = instance({pStrategyId: 's1', pFromYear: 2020, pToYear: 2024, pRequestId: 'r1'});
  await vm.startPractice();
  assert.equal(vm.pRequestId, '');
  assert.equal(refreshed, 1);
  assert.deepEqual(state.builderStrategies.map(s => s.id), ['s2']);
});

test('preparing 轮询发现失败即停止并保留失败原因，错误不清会话', async () => {
  Api.practice = async () => ({id: 'p2', phase: 'failed', failReason: 'no_match'});
  const vm = instance({sess: {id: 'p2', phase: 'preparing', failReason: ''}});
  vm.startPoll();
  assert.ok(vm.pollTimer);
  await vm.pollOnce();
  assert.equal(vm.sess.phase, 'failed');
  assert.equal(vm.sess.failReason, 'no_match');
  assert.equal(vm.pollTimer, null, '离开 preparing 后必须停止轮询');
  // 轮询网络失败：只记 pollErr，不清 sess、不停轮询。
  Api.practice = async () => { throw new Error('超时'); };
  const run = instance({sess: {id: 'p3', phase: 'preparing'}, pollTimer: setInterval(() => {}, 60000)});
  await run.pollOnce();
  assert.equal(run.sess.phase, 'preparing');
  assert.ok(run.pollErr.includes('超时'));
  clearInterval(run.pollTimer);
  run.stopPoll();
});

test('重练请求网络中断保留请求键，成功后切换新会话并刷新历史', async () => {
  const retries = [];
  let hist = 0;
  Api.practiceRetry = async (id, rid) => {
    retries.push({id, rid});
    throw new Error('断网');
  };
  const vm = instance({sess: {id: 'p1', phase: 'finished', days: []}});
  vm.loadHistory = () => { hist++; };
  await vm.retryById('p1');
  assert.ok(vm.retryRequestId);
  const first = vm.retryRequestId;
  Api.practiceRetry = async (id, rid) => {
    retries.push({id, rid});
    return {id: 'p2', phase: 'active', stage: 'waiting', revision: 1, days: [], currentDay: 0,
      canBuy: true, canNext: true, canSkip: true, canFinish: true};
  };
  await vm.retryById('p1');
  assert.equal(retries.length, 2);
  assert.equal(retries[0].rid, retries[1].rid, '重试必须复用同一 requestId');
  assert.equal(retries[1].id, 'p1');
  assert.equal(vm.sess.id, 'p2');
  assert.equal(vm.view, 'session');
  assert.equal(vm.retryRequestId, '');
  assert.equal(hist, 1);
  assert.equal(vm.retryMsg.ok, true);
});

test('会话标记与评估行按仓位投影，来源全部为服务端数据', () => {
  const day = (d, close) => ({day: d, open: close, high: close, low: close, close, volume: 1,
    ma5: null, ma10: null, ma20: null, dif: null, dea: null, hist: null});
  const base = {id: 'p1', phase: 'active', stage: 'holding', revision: 2, currentDay: 3, days: [day(0, 10), day(1, 11), day(2, 12), day(3, 13)],
    buyDay: 0, sellDay: null, quantity: 200, buyCostYuan: 2000, sellIncomeYuan: 0,
    eval: {realizedPnl: 0, realizedPct: 0, holdingDays: 3, maxFloatProfit: 600, maxFloatProfitPct: 30,
      maxFloatLoss: -100, maxFloatLossPct: -5, openPnl: 600, openPct: 30}};
  const vm = instance({sess: base});
  assert.equal(vm.evalRows.length, 4);
  assert.ok(vm.evalRows[0].label === '浮动盈亏' && vm.evalRows[0].cls === 'pos');
  assert.equal(vm.positionText.includes('200 股'), true);
  const marks = vm.sessionMarkers();
  assert.deepEqual(marks, [{day: 0, price: 10, kind: 'buy'}], '持仓中不画估值标记');
  // 未平仓结束：以结束日收盘补估值标记。
  vm.sess = {...base, phase: 'finished', endReason: 'finished_open'};
  assert.deepEqual(vm.sessionMarkers(), [{day: 0, price: 10, kind: 'buy'}, {day: 3, price: 13, kind: 'est'}]);
  // 已卖出：B/S 双标记，无估值。
  vm.sess = {...base, stage: 'sold', sellDay: 3, sellIncomeYuan: 2500,
    eval: {...base.eval, realizedPnl: 500, realizedPct: 25, openPnl: 0, openPct: 0}};
  assert.equal(vm.evalRows.length, 4);
  assert.ok(vm.evalRows.some(r => r.label === '已实现盈亏' && r.cls === 'pos'));
  assert.deepEqual(vm.sessionMarkers().map(m => m.kind), ['buy', 'sell']);
});

test('Tab7 模板可编译且命令按钮随服务端 canX 能力渲染', () => {
  const render = Vue.compile(Tab7.template, {decodeEntities: s => s, hoistStatic: false});
  assert.equal(typeof render, 'function');
  // 服务端能力字段直接驱动按钮禁用：模板不得绕过 canBuy/canSell/canNext/canSkip/canFinish。
  for (const key of ['canBuy', 'canSell', 'canNext', 'canSkip', 'canFinish']) {
    assert.ok(Tab7.template.includes(`sess.${key}`), `模板应使用服务端能力字段 ${key}`);
  }
  // setup 必须暴露模板直接调用的 helper（缺失会导致渲染期 "xxx is not a function"）。
  const setupKeys = Object.keys(Tab7.setup());
  for (const name of ['fmtMoney', 'fmtPct', 'fmtTime', 'pnlClass', 'practicePhaseLabel', 'practiceStageLabel',
    'practiceEndReasonLabel', 'practiceFailReasonLabel', 'practiceActionLabel', 'practiceDayLabel']) {
    assert.ok(setupKeys.includes(name), `setup 必须暴露模板函数 ${name}`);
  }
  assert.equal(typeof Tab7.methods.practiceOnTaskDone, 'function', '组件需暴露任务完成钩子');
});

test('有练习历史时模板可实际渲染，并保留返回出题入口', () => {
  state.builderStrategies = [];
  const vm = instance({history: [{id: 'p-history', strategyName: '策略', fromYear: 2024,
    toYear: 2024, phase: 'finished', stage: 'waiting', endReason: 'skipped', updatedAt: '2026-10-19T00:00:00Z'}]});
  const render = Vue.compile(Tab7.template, {decodeEntities: s => s, hoistStatic: false});
  assert.doesNotThrow(() => render(vm, []), '有历史记录时也必须能调用时间格式化函数');
  assert.ok(Tab7.template.includes("training ? '返回题库' : '返回出题'"));
  vm.sess = {stage: 'waiting', eval: {maxFloatProfit: 0, maxFloatLoss: 0}};
  assert.deepEqual(vm.evalRows, [], '未交易不能显示虚构的零收益评价');
});

test('买卖或结束成功后刷新历史，单纯推进不反复读取全列表', () => {
  const vm = instance({sess: {id: 'p1', phase: 'active', stage: 'waiting', revision: 1}});
  let reads = 0;
  vm.loadHistory = () => reads++;
  vm.applySession({id: 'p1', phase: 'active', stage: 'holding', revision: 2, days: []});
  vm.applySession({id: 'p1', phase: 'active', stage: 'holding', revision: 3, days: []});
  assert.equal(reads, 1);
  vm.applySession({id: 'p1', phase: 'finished', stage: 'holding', revision: 4, days: []});
  assert.equal(reads, 2);
});

test('后台读取练习不能改变其他页签 URL，返回练习时恢复题目深链', () => {
  state.activeTab = 2;
  state.practiceCurrentId = 'p-restored';
  globalThis.location = {search: '?tab=compare', pathname: '/'};
  lastUrl = '?tab=compare';
  instance().syncUrl('p-restored');
  assert.equal(lastUrl, '?tab=compare');
  App.methods.switchTab.call({...App.data(), state, $refs: {}, $nextTick: () => {}}, 7);
  assert.equal(new URLSearchParams(lastUrl.slice(1)).get('p'), 'p-restored');
});

test('出题尚未返回时切换题目，旧出题响应不强制切题', async () => {
  state.builderStrategies = [{id: 's1', version: 1}];
  let resolveStart;
  Api.practiceStart = () => new Promise(resolve => { resolveStart = resolve; });
  const vm = instance({pStrategyId: 's1'});
  vm.loadHistory = () => {};
  const start = vm.startPractice();
  vm.seq++;
  vm.sess = {id: 'p-selected', phase: 'active', revision: 1};
  let loaded;
  vm.loadSession = async id => { loaded = id; };
  resolveStart({sessionId: 'p-new'});
  await start;
  assert.equal(vm.sess.id, 'p-selected');
  assert.equal(loaded, undefined);
});

test('复盘请求未返回时切题，新题复盘仍可加载', async () => {
  let resolveReview;
  Api.practiceReview = () => new Promise(resolve => { resolveReview = resolve; });
  const vm = instance({sess: {id: 'p1', phase: 'finished', revision: 2}});
  const pending = vm.openReview();
  vm.applySession({id: 'p2', phase: 'finished', revision: 2, days: []});
  resolveReview({code: 'old'});
  await pending;
  assert.equal(vm.reviewLoading, false);
  assert.equal(vm.review, null);
  Api.practiceReview = async id => ({id, code: 'new'});
  await vm.openReview();
  assert.equal(vm.review.code, 'new');
});

test('切换会话后旧动作和读取响应不能恢复旧题，较旧版本不能覆盖新状态', async () => {
  let resolveAction;
  Api.practiceAction = () => new Promise(r => {resolveAction = r;});
  const vm = instance({sess: {id: 'p1', phase: 'active', revision: 1, stage: 'waiting', days: []}});
  const p = vm.doAction('buy');
  let resolveRead;
  Api.practice = () => new Promise(r => {resolveRead = r;});
  const load = vm.loadSession('p2');
  resolveAction({id: 'p1', phase: 'active', revision: 2, stage: 'holding', days: []});
  await p;
  assert.equal(vm.sessLoading, true, '旧题动作响应不能结束新题加载');
  resolveRead({id: 'p2', phase: 'active', revision: 3, stage: 'waiting', days: []});
  await load;
  vm.applySession({id: 'p2', phase: 'active', revision: 2, stage: 'waiting', days: []});
  assert.equal(vm.sess.revision, 3);
});

test('重练尚未返回时选择另一题，重练结果不能把当前题切走', async () => {
  let resolveRetry;
  Api.practiceRetry = () => new Promise(r => {resolveRetry = r;});
  const vm = instance({sess: {id: 'p1', phase: 'finished', revision: 5, days: []}});
  vm.loadHistory = () => {};
  const retry = vm.retryById('p1');
  Api.practice = async () => ({id: 'p2', phase: 'active', revision: 1, days: []});
  await vm.loadSession('p2');
  resolveRetry({id: 'p3', phase: 'active', revision: 1, days: []});
  await retry;
  assert.equal(vm.sess.id, 'p2');
});

test('切换图表宿主后重新创建实例，末日仍可请求行情用尽结束', () => {
  let mounts = 0, drops = 0;
  chartFactory = el => ({getDom: () => el, setData() {return [];}, dispose() {drops++;}, id: ++mounts});
  const days = [{day: 0, open: 10, high: 11, low: 9, close: 10, volume: 100, ma5: null, ma10: null, ma20: null, dif: 0, dea: 0, hist: 0}];
  const vm = instance({sess: {id: 'p1', phase: 'active', stage: 'waiting', days, canNext: false}});
  vm.renderKline();
  vm.$refs.klineChart = {};
  vm.renderKline();
  assert.equal(mounts, 2); assert.equal(drops, 1);
  chartFactory = () => null;
  const render = Vue.compile(Tab7.template, {decodeEntities: s => s, hoistStatic: false});
  const tree = render(vm, []);
  const find = v => Array.isArray(v) ? v.flatMap(find) : v && typeof v === 'object' ? [v, ...find(v.children)] : [];
  const next = find(tree).find(v => v.type === 'button' && typeof v.children === 'string' && /行情用尽/.test(v.children));
  assert.ok(!find(tree).some(v => v.type === 'StockInfo'), '盲练不得挂载可泄露身份的股票估值请求');
  assert.ok(next, '末日应有明确结束入口');
  assert.equal(next.props.disabled, false);
});

test('买卖标记离开影线，提示仍披露真实成交价，缩放不改指标数据', () => {
  const days = [{day:0,open:10,close:10.5,low:9,high:12,volume:100,
    ma5:10,ma10:10,ma20:10,dif:0.0002,dea:0.0001,hist:0.0001}];
  const normalized = K.normalize(practice.practiceKlineData({days, markers:[
    {day:0,price:10.5,kind:'buy'},{day:0,price:10.8,kind:'sell'},{day:0,price:10.5,kind:'est'}]}));
  const [buy,sell,est] = normalized.markers;
  assert.equal(buy.value,9); assert.equal(sell.value,12); assert.equal(est.value,12);
  assert.match(buy.text,/买入 · 10\.50 元/); assert.match(sell.text,/卖出 · 10\.80 元/);
  assert.match(est.text,/期末估值/);
  assert.equal(normalized.data[0].hist,0.0001);
  assert.equal(normalized.data[0].dif,0.0002);
  assert.equal(normalized.data[0].dea,0.0001);
});

test('练习与复盘保留空图表宿主，历史和成交明细按需展开', () => {
  const ctx = instance({sess: {id: 'one', phase: 'finished', stage: 'waiting', days: []},
    view: 'review', review: {code: 'sh600000', days: [], trades: [], coverage: {scanned: 1, withData: 1, hits: 1}}});
  const render = Vue.compile(Tab7.template, {decodeEntities: s => s, hoistStatic: false});
  const walk = (v, predicate) => !v || typeof v !== 'object' ? [] : Array.isArray(v)
    ? v.flatMap(x => walk(x, predicate)) : [...(predicate(v) ? [v] : []), ...walk(v.children, predicate)];
  const tree = render(ctx, []);
  const [host] = walk(tree, v => v.props?.ref === 'reviewChart');
  assert.equal(host.children, null);
  const [history] = walk(tree, v => v.type === 'details' && v.props?.class?.includes('practice-history'));
  assert.equal(history.props.open, false);
  assert.equal(walk(tree, v => v.type === 'details' && v.props?.class?.includes('practice-trades')).length, 1);
});

test('恢复出题时重新启动全局进度轮询，准备中响应更新参数但不重启任务', async () => {
 const tasks=[];
 const vm=instance({startTask:(...args)=>tasks.push(args)});
 vm.loadHistory=()=>{};
 vm.applySession({id:'preparing-id',phase:'preparing',revision:1,strategyName:'测试策略',fromYear:2020,toYear:2024});
 assert.deepEqual(tasks,[['practice_prepare',{phase:'prepare',runId:'preparing-id'}]]);
 assert.ok(vm.pollTimer);
 Api.practice=async()=>({id:'preparing-id',phase:'preparing',revision:1,strategyName:'测试策略',fromYear:2020,toYear:2024});
 await vm.pollOnce();
 assert.equal(vm.sess.fromYear,2020);
 assert.equal(tasks.length,1);
 vm.stopPoll();
});

test('历史单题不再提供重新扫描的下一题入口，连续练习由训练页负责', () => {
 assert.equal(Tab7.methods.nextPractice,undefined);
 const vm=instance({historyOnly:true,sess:{id:'old',phase:'finished',stage:'waiting',days:[]}});
 const render=Vue.compile(Tab7.template,{decodeEntities:s=>s,hoistStatic:false});
 const all=v=>Array.isArray(v)?v.flatMap(all):v&&typeof v==='object'?[v,...all(v.children)]:[];
 const buttons=all(render(vm,[])).filter(v=>v.type==='button');
 assert.ok(!buttons.some(v=>/开始下一题/.test(v.children)));
 assert.ok(buttons.some(v=>v.children==='同题重练'));
});

const reportReview = () => ({id:'report-1',code:'sh600000',strategyName:'测试策略',endReason:'finished_sold',
 eval:{realizedPnl:89,realizedPct:8.8557,holdingDays:2,maxFloatProfit:195,maxFloatProfitPct:19.4029,maxFloatLoss:-5,maxFloatLossPct:-0.4975},
 report:{traded:true,status:'sold',startDate:'2024-01-02',endDate:'2024-01-05',budget:20000,buyCost:1005,sellIncome:1094,fees:11,cash:20089,positionValue:0,equity:20089,accountPct:0.445,waitDays:1,maxDrawdown:106,maxDrawdownPct:0.52488,
 points:[{day:0,date:'2024-01-02',equity:20000,pnl:0,pct:0}]},trades:[{date:'2024-01-03',action:'buy',price:10,quantity:100,amount:1005,fee:5}]});

test('报告与下载共用服务端收益口径，未交易及未平仓有明确说明', () => {
 const review=reportReview();
 const sections=practice.practiceReportSections(review);
 assert.equal(sections[0].rows[0].value,'+89.00 元');
 assert.equal(sections[0].rows[2].value,'+0.45%');
 const text=practice.practiceReportText(review);
 assert.match(text,/sh600000/);
 assert.match(text,/手续费合计：11.00 元/);
 assert.match(text,/期末总资金：20089.00 元/);
 review.report.traded=false;review.report.status='untraded';
 assert.match(practice.practiceReportSummary(review),/未发生买入/);
 assert.equal(practice.practiceReportSections(review)[0].rows[0].value,'不适用（未交易）');
 review.report.traded=true;review.report.status='open';
 assert.match(practice.practiceReportSummary(review),/未预扣尚未发生的卖出费用/);
 assert.equal(practice.practiceReportText(null),'');
});

test('结束题目自动打开报告，报告和K线切换后只在图表模式初始化ECharts', async () => {
 const vm=instance({sess:{id:'p1',phase:'active',revision:1,stage:'sold',days:[]}});
 vm.applySession=s=>{vm.sess=s;};
 let reports=0;
 vm.openReview=async()=>{reports++;};
 Api.practiceAction=async()=>({id:'p1',phase:'finished',revision:2});
 await vm.doAction('finish');
 assert.equal(reports,1);
 let charts=0;vm.renderReview=()=>{charts++;};
 vm.showReviewMode('report');
 assert.equal(charts,0);
 vm.showReviewMode('chart');
 assert.equal(charts,1);
});

test('报告可实际渲染、图表宿主保持空节点，报告缺失时保留提示', () => {
 const vm=instance({sess:{id:'report-1',phase:'finished',stage:'sold',days:[]},view:'review',review:reportReview()});
 const render=Vue.compile(Tab7.template,{decodeEntities:s=>s,hoistStatic:false});
 const all=v=>Array.isArray(v)?v.flatMap(all):v&&typeof v==='object'?[v,...all(v.children)]:[];
 const tree=all(render(vm,[]));
 assert.ok(tree.some(v=>v.type==='button'&&v.children==='下载报告'));
 assert.ok(tree.some(v=>v.type==='dd'&&v.children==='+89.00 元'));
 assert.equal(tree.find(v=>v.props?.ref==='reviewChart').children,null);
 vm.reviewMode='chart';
 assert.ok(all(render(vm,[])).some(v=>v.type==='StockInfo'&&v.props.code===vm.review.code), '结束后复盘才显示股票估值');
 vm.review.report=null;
 assert.doesNotThrow(()=>render(vm,[]));
});

test('练习和复盘K线悬浮提示均显示较前收涨跌幅，缩放不改变基准', () => {
 const days=[{day:-1,open:10,close:10,low:9,high:11,volume:100},{day:0,date:'2024-01-02',open:12,close:11,low:10,high:12,volume:200}];
 for(const showDate of [false,true]){
  const normalized=K.normalize(practice.practiceKlineData({days,showDate}));
  const values=K.legends({current:normalized.data[1]});
  assert.equal(values.find(x=>x.title==='涨跌幅（较前收）').value.text,'+10.00%');
  if(!showDate)assert.ok(!JSON.stringify(values).includes('2024-01-02'));
 }
});
