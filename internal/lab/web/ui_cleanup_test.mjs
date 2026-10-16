import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

const read = path => fs.readFileSync(new URL(path, import.meta.url), 'utf8');
const moduleURL = source => 'data:text/javascript;base64,' + Buffer.from(source).toString('base64');
const Vue = await import(moduleURL(read('./lab/js/vendor/vue.esm-browser.prod.js')));
globalThis.cleanupTestVue = Vue;
const {state} = await import(moduleURL(read('./lab/js/store.js')
  .replace("import { reactive } from './vendor/vue.esm-browser.prod.js';", 'const {reactive} = globalThis.cleanupTestVue;')));
const constants = await import(moduleURL(read('./lab/js/constants.js')));
const format = await import(moduleURL(read('./lab/js/format.js')));
globalThis.cleanupTestGlobals = {...Vue, ...constants, ...format, state, runtime: {}, Api: {}};
const importStubs = s => s.replace(/^import (\w+) from .*;$/gm, 'const $1 = {};')
  .replace(/^import (\{.*\}) from .*;$/gm, 'const $1 = globalThis.cleanupTestGlobals;');
const {default: App} = await import(moduleURL(importStubs(read('./lab/js/app.js'))
  .replace("createApp(App).mount('#app');", 'export default App;')));
const {default: Run} = await import(moduleURL(importStubs(read('./lab/js/components/Tab1Run.js'))));
const compile = template => Vue.compile(template, {decodeEntities: s => s, hoistStatic: false,
  onError: error => {throw error;}});
function elements(v, type) {
  if (Array.isArray(v)) return v.flatMap(c => elements(c, type));
  if (!v || typeof v !== 'object') return [];
  return [...(v.type === type ? [v] : []), ...elements(v.children, type)];
}

test('默认自由搭建，配置模式只保留自由搭建和因子组合，移除脚本编辑器', () => {
  assert.equal(state.runMode, 'builder');
  const ctx = {state, ...Run.data(), ...Run.setup(), ...Run.methods};
  for (const [key, fn] of Object.entries(Run.computed)) Object.defineProperty(ctx, key, {get: () => fn.call(ctx)});
  const tree = compile(Run.template)(ctx, []);
  const [modeGroup] = elements(tree, 'div').filter(v => v.props?.role === 'group');
  const buttons = elements(modeGroup, 'button');
  assert.deepEqual(buttons.map(v => v.children), ['自由搭建', '因子组合']);
  assert.deepEqual(buttons.map(v => v.props['aria-pressed']), ['true', 'false']);
  for (const mode of ['builder', 'simple']) {
    state.runMode = mode;
    assert.equal(elements(compile(Run.template)(ctx, []), 'textarea').length, 0);
  }
  state.runMode = 'builder';
});

test('因子入口只打开运行配置，切换模式后确认仍运行因子组合', async () => {
  const previous = state.runMode;
  const calls = [];
  let mode, confirm;
  const ctx = {buildSpec: () => ({}), openRunConfig: (m, run) => {mode = m; confirm = run;},
    runSimple: async () => {calls.push('simple');}};
  try {
    state.taskRunning = false;
    state.runMode = 'simple';
    Run.methods.requestRun.call(ctx);
    assert.equal(mode, 'simple');
    assert.equal(calls.length, 0);
    state.runMode = 'builder';
    await confirm();
    assert.deepEqual(calls, ['simple']);
    confirm = null; ctx.buildSpec = () => null; state.runMode = 'simple';
    Run.methods.requestRun.call(ctx); assert.equal(confirm, null);
    state.taskRunning = true;
    Run.methods.requestRun.call(ctx); assert.equal(confirm, null);
  } finally {state.runMode = previous; state.taskRunning = false;}
});

test('行情失败详情归气泡所有，通用任务保留错误入口', () => {
  const ctx = {...App.data(), ...App.methods};
  const render = () => compile(App.template)(ctx, []);
  for (const task of ['data_update', 'backtest']) {
    App.methods.renderTaskProgress.call(ctx, {task, state: 'error', doneCodes: 2, totalCodes: 4, progress: 50, error: 'download failed'});
    assert.equal(state.taskDock.state, 'error');
    const details = elements(render(), 'details');
    assert.equal(details.length, task === 'data_update' ? 0 : 1);
    assert.ok(details.every(v => !v.props.open));
  }
  App.methods.renderTaskProgress.call(ctx, {task: 'data_update', state: 'connection-error', error: '状态未知'});
  assert.equal(elements(render(), 'details').length, 1);
  assert.equal(state.taskDock.connectionError, true);
});

test('旧参数报告保留开关和百分比口径', () => {
  const parameters = {enabled: {trend1: true, floatCap: true}, minFloatCap: 400, trendPeriod1: 30, trendMinSlope1: 0.0005, maxPrice: 120};
  const result = format.formatMacdParameters(parameters);
  assert.equal(result.find(p => p.label === '流通市值下限').value, '400 亿元');
  assert.equal(result.find(p => p.label === '均线一最小日斜率').value, '0.05 %');
  assert.equal(result.find(p => p.label === '价格过滤（条件）').value, '未勾选');
  assert.ok(!result.some(p => p.label === '价格上限'));
  assert.equal(format.formatMacdParameters(null).length, 0);
  delete parameters.enabled;
  assert.equal(format.formatMacdParameters(parameters).find(p => p.label === '价格上限').value, '120 元');
});

test('所有页面和对话框模板通过 Vue 编译', () => {
  const dir = new URL('./lab/js/components/', import.meta.url);
  for (const file of fs.readdirSync(dir).filter(f => f.endsWith('.js'))) {
    const source = fs.readFileSync(new URL(file, dir), 'utf8');
    for (const [, template] of source.matchAll(/template: `([\s\S]*?)`/g)) assert.doesNotThrow(() => compile(template), file);
  }
});
