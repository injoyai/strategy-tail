import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

// 策略库列表页：搜索、加载（含未保存草稿保护）、复制、运行、删除。
const moduleURL = source => 'data:text/javascript;base64,' + Buffer.from(source).toString('base64');
const read = path => fs.readFileSync(new URL(path, import.meta.url), 'utf8');
const Vue = await import(moduleURL(read('./lab/js/vendor/vue.esm-browser.prod.js')));
const state = globalThis.strategyLibState = Vue.reactive({});
const api = globalThis.strategyLibApi = {};
globalThis.strategyLibVue = Vue;
globalThis.strategyLibAccount = (await import(moduleURL(read('./lab/js/config.js')
  .replace("import { state } from './store.js';", 'const state = globalThis.strategyLibState;')))).ACCOUNT_CONFIG;
globalThis.strategyLibDialog = (await import(moduleURL(read('./lab/js/components/dlgMixin.js')))).dlgMixin;
const source = read('./lab/js/components/StrategyLibrary.js')
  .replace("import { state } from '../store.js';", 'const state = globalThis.strategyLibState;')
  .replace("import { Api } from '../api.js';", 'const Api = globalThis.strategyLibApi;')
  .replace("import { ACCOUNT_CONFIG } from '../config.js';", 'const ACCOUNT_CONFIG = globalThis.strategyLibAccount;')
  .replace("import { dlgMixin } from './dlgMixin.js';", 'const dlgMixin = globalThis.strategyLibDialog;');
const {default: Library} = await import(moduleURL(source));

function instance() {
  for (const key of Object.keys(state)) delete state[key];
  Object.assign(state, {
    builderSpec: {version: 1, name: '草稿', buy: null},
    builderId: '', builderVersion: 0,
    builderBaseline: JSON.stringify({version: 1, name: '草稿', buy: null}),
    builderStrategies: [], runMode: 'builder', builderMsg: {text: '', ok: true}, taskRunning: false,
    runCfg: {startYear: 2024, endYear: 2025, sampleMode: 'all', sampleSize: 50, sampleCodes: ''},
  });
  for (const key of Object.keys(api)) delete api[key];
  const ctx = {...Library.mixins[0].data(), ...Library.data(), state, $refs: {}, $nextTick: f => f()};
  for (const [key, fn] of Object.entries({...Library.mixins[0].methods, ...Library.methods})) ctx[key] = fn.bind(ctx);
  for (const [key, fn] of Object.entries(Library.computed)) Object.defineProperty(ctx, key, {get: () => fn.call(ctx)});
  ctx.switchTab = n => {ctx.switched = n;};
  ctx.startTask = t => {ctx.started = t;};
  ctx.openRunConfig = (mode, run) => {ctx.configMode = mode; ctx.run = run;};
  return ctx;
}

const saved = (id, name, version = 1) => ({id, name, version,
  createdAt: '2026-11-01T01:00:00Z', updatedAt: '2026-11-02T02:00:00Z',
  spec: {version: 1, name, buy: {type: 'condition', componentId: 'buy_all'}, holdToEnd: true}});

test('列表按名称或 ID 搜索，更新时间可显示', () => {
  const ctx = instance();
  ctx.strategies = [saved('st_a', '动量低波'), saved('st_b', '均线反转')];
  assert.equal(ctx.filtered.length, 2);
  ctx.filter = '动量';
  assert.deepEqual(ctx.filtered.map(s => s.id), ['st_a']);
  ctx.filter = 'ST_B';
  assert.deepEqual(ctx.filtered.map(s => s.id), ['st_b']);
  ctx.filter = '无匹配';
  assert.equal(ctx.filtered.length, 0);
  assert.notEqual(ctx.updatedAt(ctx.strategies[0]), '—');
  assert.equal(ctx.updatedAt({updatedAt: 'bad'}), '—');
});

test('加载已保存策略进入自由搭建并设置版本基线', async () => {
  const ctx = instance();
  const target = saved('st_load', '待加载', 3);
  api.builderStrategy = async id => ({strategy: target});
  await ctx.loadIntoEditor(target);
  assert.deepEqual(state.builderSpec, target.spec);
  assert.equal(state.builderId, 'st_load');
  assert.equal(state.builderVersion, 3);
  assert.equal(state.builderBaseline, JSON.stringify(target.spec));
  assert.equal(state.runMode, 'builder');
  assert.equal(ctx.switched, 1);
  assert.match(state.builderMsg.text, /已加载/);
});

test('未保存草稿时加载需确认，取消不替换', () => {
  const ctx = instance();
  const target = saved('st_prompt', '新策略');
  let loaded = null;
  ctx.loadIntoEditor = s => {loaded = s;};
  ctx.filter = '';
  state.builderSpec = {version: 1, name: '有修改', buy: null};
  ctx.requestLoad(target);
  assert.equal(ctx.open, true);
  assert.equal(ctx.pendingLoad, target);
  ctx.closeDlg();
  assert.equal(ctx.open, false);
  assert.equal(loaded, null);
  assert.equal(ctx.pendingLoad, null);
  ctx.requestLoad(target);
  ctx.replaceDraft();
  assert.equal(ctx.open, false);
  assert.equal(loaded, target);
});

test('复制以新身份创建，名称加副本并限制 80 字', async () => {
  const ctx = instance();
  const long = '策'.repeat(80);
  const target = saved('st_copy', long, 2);
  let received = null;
  api.createBuilderStrategy = async spec => {
    received = spec;
    return {strategy: {id: 'st_new', name: spec.name, version: 1}};
  };
  api.builderStrategies = async () => ({strategies: []});
  await ctx.copy(target);
  assert.equal(Array.from(received.name).length, 80);
  assert.match(received.name, /（副本）$/);
  assert.notEqual(received, target.spec);
  assert.equal(target.spec.name, long);
  assert.match(ctx.msg.text, /已复制/);
});

test('删除需确认，成功刷新并解除当前编辑身份', async () => {
  const ctx = instance();
  const target = saved('st_del', '待删除');
  let deleted = '';
  api.deleteBuilderStrategy = async id => {deleted = id;};
  api.builderStrategies = async () => ({strategies: []});
  state.builderId = 'st_del';
  ctx.requestDelete(target);
  assert.equal(ctx.open, true);
  await ctx.confirmDelete();
  assert.equal(deleted, 'st_del');
  assert.equal(ctx.open, false);
  assert.equal(state.builderId, '');
  assert.equal(state.builderVersion, 0);
  assert.match(ctx.msg.text, /已删除/);
  assert.match(state.builderMsg.text, /再次保存会创建新策略/);
});

test('运行使用库中快照提交，不修改当前草稿', async () => {
  const ctx = instance();
  const target = saved('st_run', '库中策略');
  const draft = state.builderSpec;
  let received = null;
  api.runBuilder = async cfg => {received = cfg;};
  ctx.runStrategy(target);
  assert.equal(ctx.configMode, 'builder');
  assert.equal(state.builderSpec, draft);
  const ok = await ctx.run();
  assert.equal(ok, true);
  assert.equal(received.startYear, 2024);
  assert.deepEqual(received.builder, target.spec);
  assert.deepEqual(received.account, globalThis.strategyLibAccount);
  assert.equal(ctx.started, 'backtest');
  assert.match(ctx.msg.text, /已提交/);
});

test('指定代码为空时不提交运行请求', async () => {
  const ctx = instance();
  state.runCfg.sampleMode = 'codes';
  state.runCfg.sampleCodes = ' , ';
  let calls = 0;
  api.runBuilder = async () => {calls++;};
  ctx.runStrategy(saved('st_empty', '空样本'));
  const ok = await ctx.run();
  assert.equal(ok, false);
  assert.equal(calls, 0);
  assert.match(ctx.msg.text, /样本为空/);
});

test('列表失败显示错误并保留搜索状态', async () => {
  const ctx = instance();
  ctx.filter = '动量';
  api.builderStrategies = async () => {throw new Error('离线');};
  await ctx.load();
  assert.match(ctx.loadError, /离线/);
  assert.equal(ctx.filter, '动量');
  api.builderStrategies = async () => ({strategies: [saved('st_ok', '动量')]});
  await ctx.load();
  assert.equal(ctx.loadError, '');
  assert.equal(ctx.strategies.length, 1);
});

test('模板可渲染，包含搜索与操作入口', () => {
  const ctx = instance();
  assert.doesNotThrow(() => Vue.compile(Library.template, {decodeEntities: s => s, hoistStatic: false})(ctx, []));
});
