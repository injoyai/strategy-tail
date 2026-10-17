import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

// 验证 Tab6Portfolio.fillPfForm / pfDefaultForm 对组合策略字段缺失的回填语义：
// 后端 PortfolioPolicy.MinHoldings 带 omitempty（internal/portfolioresearch/types.go），
// 合法零值 0 序列化后整个字段缺失，读取已有模型回填时缺失须按 0 解释——回填默认 5 会在
// 再次保存时悄悄改写模型约束；新建表单（pfDefaultForm）的默认 5 单独保留。
// CostSpec 各成本字段无 omitempty，显式零值会正常输出，不能套用同一缺失语义。
const read = path => fs.readFileSync(new URL(path, import.meta.url), 'utf8');
const moduleURL = source => 'data:text/javascript;base64,' + Buffer.from(source).toString('base64');
const Vue = await import(moduleURL(read('./lab/js/vendor/vue.esm-browser.prod.js')));
globalThis.pfFormTestVue = Vue;
const {state} = await import(moduleURL(read('./lab/js/store.js')
  .replace("import { reactive } from './vendor/vue.esm-browser.prod.js';", 'const {reactive} = globalThis.pfFormTestVue;')));
const constants = await import(moduleURL(read('./lab/js/constants.js')));
const format = await import(moduleURL(read('./lab/js/format.js')));
globalThis.pfFormTestGlobals = {...Vue, ...constants, ...format, state, Api: {},
  mountChart: () => {}, dropChart: () => {}, NAV_COLORS: []};
const importStubs = s => s.replace(/^import (\w+) from .*;$/gm, 'const $1 = {};')
  .replace(/^import (\{.*\}) from .*;$/gm, 'const $1 = globalThis.pfFormTestGlobals;');
const {default: Tab6} = await import(moduleURL(importStubs(read('./lab/js/components/Tab6Portfolio.js'))));
const ctx = () => ({...Tab6.data(), ...Tab6.methods});

test('读取已有模型：minHoldings 字段缺失（omitempty 零值）按 0 解释，不再回填默认 5', () => {
  const c = ctx();
  Tab6.methods.fillPfForm.call(c, {portfolioPolicy: {}});
  assert.equal(c.form.minHoldings, 0);
});

test('读取已有模型：显式 minHoldings 值原样保留（含显式 0 与非零值）', () => {
  const c = ctx();
  Tab6.methods.fillPfForm.call(c, {portfolioPolicy: {minHoldings: 7}});
  assert.equal(c.form.minHoldings, 7);
  Tab6.methods.fillPfForm.call(c, {portfolioPolicy: {minHoldings: 0}});
  assert.equal(c.form.minHoldings, 0);
});

test('新建表单：pfDefaultForm 的 minHoldings 默认 5 保留', () => {
  const c = ctx();
  Tab6.methods.pfDefaultForm.call(c);
  assert.equal(c.form.minHoldings, 5);
});

test('成本字段无 omitempty 契约：显式零值保留，缺失才回退默认值', () => {
  const c = ctx();
  Tab6.methods.fillPfForm.call(c, {execution: {cost: {commissionRate: 0, stampDutyRate: 0, slippage: 0}}});
  assert.equal(c.form.commission, 0);
  assert.equal(c.form.stampDuty, 0);
  assert.equal(c.form.slippage, 0);
  Tab6.methods.fillPfForm.call(c, {});
  assert.equal(c.form.commission, 0.0003);
  assert.equal(c.form.stampDuty, 0.001);
  assert.equal(c.form.slippage, 0.01);
});
