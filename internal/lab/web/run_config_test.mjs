import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

const read = p => fs.readFileSync(new URL(p, import.meta.url), 'utf8');
const moduleURL = s => 'data:text/javascript;base64,' + Buffer.from(s).toString('base64');
const state = globalThis.runConfigTestState = {};
globalThis.runConfigTestAccount = (await import(moduleURL(read('./lab/js/config.js').replace("import { state } from './store.js';", 'const state = globalThis.runConfigTestState;')))).ACCOUNT_CONFIG;
globalThis.runConfigTestMixin = (await import(moduleURL(read('./lab/js/components/dlgMixin.js')))).dlgMixin;
globalThis.runConfigTestDividendStocks = (await import(moduleURL(read('./lab/js/constants.js')))).DIVIDEND_STOCKS;
const {default: Dialog} = await import(moduleURL(read('./lab/js/components/RunConfigDlg.js')
  .replace("import { state } from '../store.js';", 'const state = globalThis.runConfigTestState;')
  .replace("import { ACCOUNT_CONFIG } from '../config.js';", 'const ACCOUNT_CONFIG = globalThis.runConfigTestAccount;')
  .replace("import { DIVIDEND_STOCKS } from '../constants.js';", 'const DIVIDEND_STOCKS = globalThis.runConfigTestDividendStocks;')
  .replace("import { dlgMixin } from './dlgMixin.js';", 'const dlgMixin = globalThis.runConfigTestMixin;')));
function instance(mode = 'builder', run = async () => true) {
  Object.assign(state, {taskRunning: false, builderMsg: {text: ''}, runMsg: {text: ''},
    runCfg: {startYear: 2025, endYear: 2026, sampleMode: 'all', sampleSize: 50, sampleCodes: '',
      holdingOn: true, holdingDays: 1, tpOn: false, takeProfit: 10, slOn: false, stopLoss: 8}});
  const ctx = {...Dialog.mixins[0].data(), ...Dialog.data(), ...Dialog.setup(), focused: 0,
    $nextTick: f => f(), $el: {querySelector: () => ({focus: () => ctx.focused++})}};
  for (const [k, f] of Object.entries(Dialog.methods)) ctx[k] = f.bind(ctx);
  ctx.openDlg(mode, run);
  return ctx;
}

test('仅确认才提交配置，取消和 Escape 均不改变共享参数', async () => {
  let calls = 0;
  const ctx = instance('builder', async () => {calls++; return true;});
  ctx.cfg.startYear = 2024;
  ctx.closeDlg();
  assert.equal(state.runCfg.startYear, 2025);
  await ctx.confirm();
  assert.equal(calls, 0);
  ctx.openDlg('builder', async () => {calls++; return true;});
  ctx.cfg.sampleMode = 'random'; ctx.cfg.sampleSize = 12;
  let prevented = false;
  ctx.cancel({preventDefault() {prevented = true;}});
  assert.equal(prevented, true);
  assert.equal(state.runCfg.sampleMode, 'all');
  assert.equal(calls, 0);
});

test('校验年份、随机数量、空代码以及因子卖出规则，错误保留输入', async () => {
  let calls = 0;
  const ctx = instance('simple', async () => {calls++; return true;});
  const cases = [
    [{startYear: 2025.5}, 'startYear'],
    [{endYear: 2024}, 'endYear'],
    [{endYear: new Date().getFullYear() + 1}, 'endYear'],
    [{sampleMode: 'random', sampleSize: 0.5}, 'sampleSize'],
    [{sampleMode: 'codes', sampleCodes: ' , '}, 'sampleCodes'],
    [{holdingOn: false, tpOn: false, slOn: false}, 'holdingOn'],
    [{holdingDays: 1.5}, 'holdingDays'],
    [{tpOn: true, takeProfit: -1}, 'takeProfit'],
  ];
  for (const [changes, key] of cases) {
    ctx.cfg = {...state.runCfg, ...changes};
    await ctx.confirm();
    assert.equal(ctx.errorKey, key);
    assert.equal(ctx.open, true);
    assert.equal(ctx.cfg[key], changes[key] ?? state.runCfg[key]);
  }
  ctx.cfg = {...state.runCfg, slOn: true, stopLoss: NaN};
  await ctx.confirm(); assert.equal(ctx.errorKey, 'stopLoss');
  ctx.mode = 'builder'; ctx.cfg = {...state.runCfg, startYear: 1989};
  await ctx.confirm(); assert.equal(ctx.errorKey, 'startYear');
  assert.equal(calls, 0);
  assert.ok(ctx.focused > cases.length);
});

test('确认期间阻止重复、关闭和 Escape；成功保留参数并关闭弹窗', async () => {
  let calls = 0, finish;
  const ctx = instance('builder', () => {calls++; return new Promise(resolve => {finish = resolve;});});
  ctx.cfg.sampleMode = 'codes'; ctx.cfg.sampleCodes = 'sh600000,sz000001';
  ctx.cfg.holdingOn = false; // 搭建器的卖出规则由条件树管理。
  const pending = ctx.confirm();
  assert.equal(state.runCfg.sampleCodes, 'sh600000,sz000001');
  await ctx.confirm();
  ctx.closeDlg(); ctx.cancel({preventDefault() {}});
  assert.equal(ctx.open, true); assert.equal(calls, 1);
  finish(true); await pending;
  assert.equal(ctx.open, false); assert.equal(ctx.submitting, false);
});

test('提交失败保留选择并可重试，共享任务占用时不启动', async () => {
  let calls = 0;
  const ctx = instance('simple', async () => {calls++; state.runMsg.text = '服务不可用';});
  ctx.cfg.sampleMode = 'random'; ctx.cfg.sampleSize = 15;
  await ctx.confirm();
  assert.equal(ctx.error, '服务不可用'); assert.equal(ctx.open, true);
  assert.equal(ctx.cfg.sampleSize, 15); assert.equal(ctx.submitting, false);
  state.taskRunning = true;
  await ctx.confirm(); assert.equal(calls, 1); assert.match(ctx.error, /任务运行中/);
  state.taskRunning = false;
  ctx.run = async () => {calls++; throw new Error('网络断开');};
  await ctx.confirm(); assert.equal(ctx.error, '网络断开'); assert.equal(ctx.open, true);
  ctx.run = async () => true;
  await ctx.confirm(); assert.equal(ctx.open, false);
});

test('红利组选项提交20只完整代码，两种回测模式共用，取消不修改共享配置', async () => {
  const expected = ['sh601398', 'sh601939', 'sh601288', 'sh601988', 'sh601328', 'sh601166',
    'sh601088', 'sh601225', 'sh600188', 'sh601898', 'sh600900', 'sh600886', 'sh601006',
    'sh600028', 'sh601857', 'sh600019', 'sh601668', 'sz000895', 'sh600941', 'sh601728'];
  assert.deepEqual(globalThis.runConfigTestDividendStocks.map(s => s.code), expected);
  for (const mode of ['builder', 'simple']) {
    let submitted;
    const ctx = instance(mode, async () => {submitted = structuredClone(state.runCfg); return true;});
    ctx.sampleChoice = 'dividend'; ctx.selectSample();
    assert.equal(state.runCfg.sampleMode, 'all');
    assert.equal(ctx.cfg.sampleMode, 'codes');
    assert.deepEqual(ctx.cfg.sampleCodes.split(','), expected);
    await ctx.confirm();
    assert.equal(submitted.sampleMode, 'codes');
    assert.deepEqual(submitted.sampleCodes.split(','), expected);
    ctx.openDlg(mode, async () => true);
    assert.equal(ctx.sampleChoice, 'dividend');
    ctx.sampleChoice = 'sh601166'; ctx.selectSample(); ctx.closeDlg();
    assert.equal(state.runCfg.sampleCodes, expected.join(','));
  }
});

test('单只红利股与自选代码、随机和全部范围互相切换，不残留预设模式', async () => {
  const ctx = instance();
  for (const stock of globalThis.runConfigTestDividendStocks) {
    ctx.sampleChoice = stock.code; ctx.selectSample();
    assert.equal(ctx.cfg.sampleMode, 'codes');
    assert.equal(ctx.cfg.sampleCodes, stock.code);
    assert.equal(ctx.validate(), true);
  }
  await ctx.confirm(); ctx.openDlg('builder', async () => true);
  assert.equal(ctx.sampleChoice, 'sh601728');
  for (const choice of ['random', 'all', 'codes']) {
    ctx.sampleChoice = choice; ctx.selectSample();
    assert.equal(ctx.cfg.sampleMode, choice);
  }
  assert.equal(ctx.cfg.sampleCodes, 'sh601728');
});
