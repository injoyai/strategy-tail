import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

const state = {}, runtime = {pollTimer: null}, Api = {};
const format = await import('data:text/javascript;base64,' + Buffer.from(fs.readFileSync(new URL('./lab/js/format.js', import.meta.url), 'utf8')).toString('base64'));
globalThis.taskStatusTest = {...format, state, runtime, Api, TASK_LABEL: {analysis: '因子分析', backtest: '策略回测', portfolio: '组合运行'}};
const source = fs.readFileSync(new URL('./lab/js/app.js', import.meta.url), 'utf8')
  .replace(/^import (\w+) from .*;$/gm, 'const $1 = {};')
  .replace(/^import (\{.*\}) from .*;$/gm, 'const $1 = globalThis.taskStatusTest;')
  .replace('createApp(App).mount(\'#app\');', 'export default App;');
const {default: App} = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));

test('因子统计、组合指标与保存阶段均清除上一阶段的满格进度', () => {
  for (const [task, phases] of [
    ['analysis', ['ic', 'grouping', 'aligning', 'diagnostics', 'reporting']],
    ['portfolio', ['transform', 'combine', 'metrics', 'attribution', 'finalize']],
    ['portfolio_validation', ['combine', 'metrics', 'finalize']],
  ]) {
    for (const phase of phases) {
      App.methods.renderTaskProgress({task, state: 'running', phase, done: -1, total: -1,
        doneCodes: 3200, totalCodes: 3200, progress: -1});
      assert.equal(state.taskDock.value, null, `${task}/${phase}`);
      assert.equal(state.taskDock.indeterminate, true);
      assert.equal(state.taskDock.percent, '进行中');
      assert.ok(!state.taskDock.detail.includes('3200'));
      assert.ok(state.taskDock.title.includes(format.taskProgress({task, phase}).label));
    }
  }
});

test('后台完成后继续显示读取结果，失败可重读且不宣称任务还在运行', async () => {
  const ctx = {...App.methods, switchTab() {}};
  runtime.prevRunning = true; runtime.lastTask = 'analysis';
  Api.status = async () => ({task: 'analysis', state: 'done', phase: 'reporting', done: -1, total: -1, progress: 100});
  let resolve;
  Api.latestAnalysis = () => new Promise(r => {resolve = r;});
  const pending = ctx.pollStatusOnce();
  await Promise.resolve();
  assert.equal(state.taskDock.title, '因子分析 · 读取结果');
  assert.equal(state.taskDock.value, null);
  assert.equal(state.taskDock.readingResults, true);
  assert.equal(state.taskRunning, true);
  resolve({analysisId: 'ready'});
  await pending;
  assert.equal(state.taskDock.title, '因子分析已完成');
  assert.equal(state.taskDock.value, 100);
  assert.equal(state.taskRunning, false);
  Api.latestAnalysis = async () => {throw new Error('读取超时');};
  await ctx.pollStatusOnce();
  assert.equal(state.taskDock.title, '结果读取失败');
  assert.equal(state.taskDock.current, '后台任务已完成，请重试读取结果');
  assert.equal(state.taskDock.connectionError, true);
  assert.equal(state.taskRunning, false);
  Api.latestAnalysis = async () => ({analysisId: 'retry'});
  await ctx.pollStatusOnce();
  assert.equal(state.currentAnalysis.analysisId, 'retry');
});

test('组合列表与详情使用当前阶段，不显示持久化的固定进度百分比', async () => {
  const src = fs.readFileSync(new URL('./lab/js/components/Tab6Portfolio.js', import.meta.url), 'utf8')
    .replace(/^import (\{.*\}) from .*;$/gm, 'const $1 = globalThis.taskStatusTest;');
  const {default: Portfolio} = await import('data:text/javascript;base64,' + Buffer.from(src).toString('base64'));
  const run = {experimentId: 'pe_test', status: 'running', progress: 92};
  state.status = {task: 'portfolio', state: 'running', runId: 'pe_test', phase: 'attribution', done: -1, total: -1};
  assert.equal(Portfolio.methods.progressText(run), '计算收益归因…');
  state.status = {...state.status, phase: 'execute', done: 1, total: 4, unit: 'trading_days'};
  assert.equal(Portfolio.methods.progressText(run), '模拟交易 · 本阶段 25%');
  state.status.runId = 'pe_other';
  assert.equal(Portfolio.methods.progressText(run), '运行中');
  assert.equal(Portfolio.methods.progressText({...run, status: 'completed'}), '已完成');
});

test('回测按阶段显示真实单位，阶段切换重置数量且清除旧股票', () => {
  const ctx = App.methods;
  const render = s => ctx.renderTaskProgress({state: 'running', task: 'backtest', config: {account: {}}, ...s});
  render({phase: 'preparing', done: -1, total: -1, progress: -1});
  assert.equal(state.taskDock.title, '策略回测 · 准备数据');
  assert.equal(state.taskDock.value, null);
  assert.equal(state.taskDock.indeterminate, true);
  render({phase: 'loading', done: 0, total: 2, progress: 0});
  assert.equal(state.taskDock.detail, '0/2 只股票');
  assert.equal(state.taskDock.percent, '本阶段 0%');
  render({phase: 'loading', done: 2, total: 2, progress: 100, currentCode: 'sh600001'});
  assert.equal(state.taskDock.current, '当前股票 sh600001');
  render({phase: 'simulating', done: 0, total: 3, progress: 0});
  assert.equal(state.taskDock.title, '策略回测 · 模拟交易');
  assert.equal(state.taskDock.detail, '0/3 个交易日');
  assert.equal(state.taskDock.percent, '本阶段 0%');
  assert.equal(state.taskDock.current, '');
  render({phase: 'simulating', done: 1, total: 3, progress: 100 / 3, currentCode: '2025-01-02'});
  assert.equal(state.taskDock.current, '当前日期 2025-01-02');
  assert.equal(state.taskDock.percent, '本阶段 33%');
  render({phase: 'reporting', done: -1, total: -1, progress: -1});
  assert.equal(state.taskDock.title, '策略回测 · 生成报告');
  assert.equal(state.taskDock.value, null);
  assert.equal(state.taskDock.indeterminate, true);
  assert.equal(state.taskDock.current, '');
  render({state: 'error', phase: 'reporting', done: -1, total: -1, progress: -1, error: '保存失败'});
  assert.equal(state.taskDock.percent, '失败');
  assert.equal(state.taskDock.indeterminate, false);
  assert.equal(state.taskDock.value, 0);
  assert.equal(state.taskDock.error, '保存失败');
  assert.equal(state.taskDock.detail, '生成报告');
  render({state: 'done', phase: 'reporting', done: -1, total: -1, progress: 100});
  assert.equal(state.taskDock.title, '策略回测已完成');
  assert.equal(state.taskDock.detail, '结果已就绪');
  assert.equal(state.taskDock.percent, '100%');
  assert.equal(state.taskDock.value, 100);
});

test('回测保持停止、断连、旧股票计数与参数回测单位', () => {
  const render = s => App.methods.renderTaskProgress({task: 'backtest', state: 'running', ...s});
  render({phase: 'simulating', state: 'stopped', done: 1, total: 3, progress: 100 / 3, currentCode: '2025-01-02'});
  assert.equal(state.taskDock.title, '策略回测已停止');
  assert.equal(state.taskDock.current, '已保留当前表单配置');
  assert.equal(state.taskDock.indeterminate, false);
  render({state: 'connection-error', error: '读取失败'});
  assert.equal(state.taskDock.title, '进度读取中断');
  assert.equal(state.taskDock.percent, '状态未知');
  assert.equal(state.taskDock.connectionError, true);
  assert.equal(state.taskDock.indeterminate, false);
  assert.equal(state.taskDock.current, '任务可能仍在后台运行');
  render({doneCodes: 1, totalCodes: 2, progress: 50});
  assert.equal(state.taskDock.detail, '1/2 只股票');
  render({phase: 'backtesting', done: 1, total: 4, progress: 25, config: {macd: {}}});
  assert.equal(state.taskDock.detail, '1/4 股票×年份');
  App.methods.renderTaskProgress({state: 'running', task: 'portfolio', phase: 'queued', done: -1, total: -1, progress: -1});
  assert.equal(state.taskDock.detail, '正在准备任务…');
  assert.equal(state.taskDock.value, null);
});

test('分析完成、主动停止和服务重启使用不同结果状态', async () => {
  const ctx = {...App.methods, switchTab(n) {this.tab = n;}};
  const report = {analysisId: 'new-analysis'};
  Api.latestAnalysis = async () => report;
  try {
    for (const [status, title, message] of [
      [{state: 'idle', task: 'backtest'}, '因子分析意外中断', '后台任务状态已重置'],
      [{state: 'idle', task: 'analysis'}, '因子分析已停止', '任务已停止'],
      [{state: 'done', task: 'analysis', progress: 100}, '因子分析已完成', '分析完成'],
      [{state: 'error', task: 'analysis', error: '报告落盘失败'}, '因子分析失败', '报告落盘失败'],
    ]) {
      runtime.prevRunning = true;
      runtime.lastTask = 'analysis';
      state.currentAnalysis = null;
      Api.status = async () => status;
      await ctx.pollStatusOnce();
      assert.equal(state.taskDock.title, title);
      assert.ok(state.factorMsg.text.includes(message));
      assert.equal(state.taskRunning, false);
      assert.equal(runtime.pollTimer, null);
      assert.equal(state.currentAnalysis, status.state === 'done' ? report : null);
      if (title.includes('意外中断')) {
        assert.equal(state.taskDock.state, 'error');
        assert.equal(state.taskDock.detail, '未取得本次任务结果');
        assert.ok(!state.taskDock.detail.includes('准备'));
      }
    }
    runtime.prevRunning = false;
    Api.status = async () => ({state: 'idle', task: 'backtest'});
    await ctx.pollStatusOnce();
    assert.equal(state.taskDock, null);
  } finally {clearInterval(runtime.pollTimer);}
});
