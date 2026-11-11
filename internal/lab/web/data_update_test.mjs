import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

const read = path => fs.readFileSync(new URL(path, import.meta.url), 'utf8');
const moduleURL = source => 'data:text/javascript;base64,' + Buffer.from(source).toString('base64');
const Vue = await import(moduleURL(read('./lab/js/vendor/vue.esm-browser.prod.js')));
const {default: DataUpdate} = await import(moduleURL(read('./lab/js/components/DataUpdate.js')
  .replace("import { Api } from '../api.js';", 'const Api = {};')));
const render = Vue.compile(DataUpdate.template, {decodeEntities: s => s, hoistStatic: false});

const nodes = value => Array.isArray(value) ? value.flatMap(nodes) :
  value && typeof value === 'object' ? [value, ...nodes(value.children)] : [];

test('行情气泡只展示入库日期和读取状态', () => {
  for (const [status, loading, badge, detail] of [
    [null, true, '读取中', '读取中…'],
    [{}, false, '只读', '暂无日线'],
    [{benchmarkDate: '2026-11-11'}, false, '只读', '2026-11-11'],
    [{readError: '无法读取上证指数日线'}, false, '异常', '读取失败'],
  ]) {
    const ctx = {...DataUpdate.data(), ...DataUpdate.methods, status, loading};
    for (const [key, fn] of Object.entries(DataUpdate.computed)) {
      Object.defineProperty(ctx, key, {get: () => fn.call(ctx)});
    }
    assert.equal(ctx.bubbleText, badge);
    assert.equal(ctx.benchmarkText, detail);
    const tree = nodes(render(ctx, []));
    assert.equal(tree.filter(v => v.type === 'button').length, 2);
    assert.equal(tree.filter(v => v.type === 'form' || v.type === 'progress').length, 0);
    if (status?.benchmarkDate) assert.equal(tree.find(v => v.type === 'time').props.datetime, detail);
  }
});
