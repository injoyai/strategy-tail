import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

const state = {}, runtime = {pollTimer: null}, Api = {};
globalThis.taskStatusTest = {state, runtime, Api, TASK_LABEL: {analysis: '因子分析'}};
const source = fs.readFileSync(new URL('./lab/js/app.js', import.meta.url), 'utf8')
  .replace(/^import (\w+) from .*;$/gm, 'const $1 = {};')
  .replace(/^import (\{.*\}) from .*;$/gm, 'const $1 = globalThis.taskStatusTest;')
  .replace('createApp(App).mount(\'#app\');', 'export default App;');
const {default: App} = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));

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
      state.dataUpdating = false;
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
