import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

const read = p => fs.readFileSync(new URL(p, import.meta.url), 'utf8');
const url = s => 'data:text/javascript;base64,' + Buffer.from(s).toString('base64');
const {stockLabel, taskProgress, esc} = await import(url(read('./lab/js/format.js')));
const names = {sh600000: '浦发银行', sz000001: '平安银行', sh000001: '上证指数'};

test('完整代码区分市场和指数，缺少名称时保留代码，不猜测证券身份', () => {
  for (const [code, name] of Object.entries(names)) assert.equal(stockLabel(code, names), `${code} ${name}`);
  for (const code of ['sh999999', '000001', '2026-10-20', '窗口 1/3']) assert.equal(stockLabel(code, names), code);
  assert.equal(stockLabel('sh600000'), 'sh600000');
  assert.equal(stockLabel(null), '');
  assert.equal(stockLabel('toString', names), 'toString');
  assert.equal(esc(stockLabel('sh600000', {sh600000: '<img onerror="x">'})), 'sh600000 &lt;img onerror=&quot;x&quot;&gt;');
});

test('任务进度补充名称，日期和窗口进度沿用原文', () => {
  assert.equal(taskProgress({phase:'backtesting',currentCode:'sh600000'}, names).current, '当前股票 sh600000 浦发银行');
  assert.equal(taskProgress({phase:'simulating',currentCode:'2026-10-20'}, names).current, '当前日期 2026-10-20');
  assert.equal(taskProgress({phase:'execute',currentCode:'窗口 1/3'}, names).current, '当前 窗口 1/3');
});
