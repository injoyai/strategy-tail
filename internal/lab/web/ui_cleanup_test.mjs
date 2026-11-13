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

test('通用任务保留折叠的错误入口', () => {
  const ctx = {...App.data(), ...App.methods};
  for (const [key, fn] of Object.entries(App.computed)) Object.defineProperty(ctx, key, {get: () => fn.call(ctx)});
  const render = () => compile(App.template)(ctx, []);
  App.methods.renderTaskProgress.call(ctx, {task: 'backtest', state: 'error', error: '读取报告失败'});
  const details = elements(render(), 'details');
  assert.equal(details.length, 1);
  assert.ok(!details[0].props.open);
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

function navigationContext() {
  const ctx = {...App.data(), $refs: {}, $nextTick() {}};
  for (const [key, fn] of Object.entries(App.methods)) ctx[key] = fn.bind(ctx);
  for (const [key, fn] of Object.entries(App.computed)) Object.defineProperty(ctx, key, {get: () => fn.call(ctx)});
  return ctx;
}

test('左侧主入口覆盖全部页面，二级页签仅展示当前工作区', () => {
  const ctx = navigationContext();
  assert.deepEqual(ctx.groups.map(g => g.title), ['策略回测', '因子研究', '盘感练习', '选股策略']);
  assert.deepEqual(ctx.groups.flatMap(g => g.tabs), constants.TABS.map(t => t.n));
  const previous = state.activeTab;
  try {
    for (const group of ctx.groups) {
      state.activeTab = group.tabs[0];
      const tree = compile(App.template)(ctx, []);
      const [nav] = elements(tree, 'nav');
      assert.equal(elements(nav, 'button').filter(b => b.props.id?.startsWith('nav-')).length, 4);
      assert.equal(elements(nav, 'button').filter(b => b.props['aria-current'] === 'location').length, 1);
      const secondary = elements(tree, 'button').filter(b => b.props?.role === 'tab');
      assert.deepEqual(secondary.map(b => Number(b.props['data-tab'])), group.tabs.length > 1 ? group.tabs : []);
      assert.equal(secondary.filter(b => b.props['aria-selected'] === 'true').length, group.tabs.length > 1 ? 1 : 0);
      const panels = elements(tree, 'div').filter(v => v.props?.id === 'tab7');
      assert.equal(panels[0].props.role, 'region');
      assert.equal(panels[0].props['aria-labelledby'], 'nav-practice');
    }
  } finally {state.activeTab = previous;}
});

test('跨工作区返回上次页面，旧链接和组内键盘切换保持可用', () => {
  const previous = {activeTab: state.activeTab, location: globalThis.location, history: globalThis.history, document: globalThis.document};
  let url, focused;
  globalThis.location = {pathname: '/', search: ''};
  globalThis.history = {replaceState(_state, _title, next) {url = next;}};
  globalThis.document = {getElementById(id) {return {focus() {focused = id;}};}};
  const ctx = navigationContext();
  try {
    for (const t of constants.TABS) {
      ctx.switchTab(t.n);
      assert.equal(ctx.activeGroup.tabs.includes(t.n), true);
      assert.equal(url, t.n === 1 ? '/' : '?tab=' + constants.TAB_SLUG[t.n]);
    }
    ctx.switchTab(3);
    ctx.switchTab(5);
    ctx.switchGroup(ctx.groups[0]);
    assert.equal(state.activeTab, 3);
    ctx.switchGroup(ctx.groups[1]);
    assert.equal(state.activeTab, 5);
    const key = k => ctx.onTabsKeydown({key: k, preventDefault() {}});
    key('End'); assert.equal(state.activeTab, 6); assert.equal(focused, 'tabbtn6');
    key('ArrowRight'); assert.equal(state.activeTab, 4); assert.equal(focused, 'tabbtn4');
    key('ArrowLeft'); assert.equal(state.activeTab, 6);
    key('Home'); assert.equal(state.activeTab, 4);
    ctx.switchTab(999); assert.equal(state.activeTab, 4);
    ctx.switchTab(7);
    globalThis.location.search = '?tab=practice&p=session123';
    ctx.switchTab(7);
    assert.equal(url, '?tab=practice&p=session123');
    globalThis.location.search = '?tab=portfolio&model=model123&revision=2';
    ctx.switchTab(6);
    assert.equal(url, '?tab=portfolio&model=model123&revision=2');
  } finally {
    state.activeTab = previous.activeTab;
    for (const name of ['location', 'history', 'document']) {
      if (previous[name] === undefined) delete globalThis[name]; else globalThis[name] = previous[name];
    }
  }
});

test('出题所有阶段跨页复用唯一全局任务栏及停止、重试和关闭操作', () => {
  const ctx = navigationContext();
  const render = compile(App.template);
  const previous = {activeTab: state.activeTab, taskDock: state.taskDock};
  const snapshots = [
    {state: 'running', phase: 'prepare'},
    {state: 'running', phase: 'scan', doneCodes: 2, totalCodes: 4},
    {state: 'running', phase: 'save'},
    {state: 'done'}, {state: 'stopped'}, {state: 'error', error: '保存失败'},
    {state: 'connection-error', error: '连接中断'},
  ];
  try {
    for (const snapshot of snapshots) {
      ctx.renderTaskProgress({task: 'practice_prepare', ...snapshot});
      for (const tab of [7, 1, 4]) {
        state.activeTab = tab;
        const tree = render(ctx, []);
        const docks = elements(tree, 'section').filter(v => v.props?.ref === 'statusbar');
        assert.equal(docks.length, 1);
        assert.equal(docks[0].props.class, 'statusbar', '出题与其他任务使用相同布局类');
        assert.equal(elements(tree, 'progress').length, 1, '页面只渲染一个全局任务进度条');
        const [main] = elements(tree, 'main');
        assert.ok(main.props.class.includes('has-task-dock'));
        assert.ok(!main.props.class.includes('practice-page'), '全局浮层出现时沿用内容避让间距');
        const actions = elements(docks[0], 'button').map(v => v.children);
        assert.equal(actions.includes('停止任务'), snapshot.state === 'running');
        assert.equal(actions.includes('重试读取'), snapshot.state === 'connection-error');
        assert.equal(actions.includes('关闭'), snapshot.state !== 'running');
      }
    }
  } finally {Object.assign(state, previous);}
});
