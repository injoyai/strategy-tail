import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

const read = path => fs.readFileSync(new URL(path, import.meta.url), 'utf8');
const moduleURL = source => 'data:text/javascript;base64,' + Buffer.from(source).toString('base64');
const Vue = await import(moduleURL(read('./lab/js/vendor/vue.esm-browser.prod.js')));
globalThis.chartTestVue = Vue;
let observer;
globalThis.ResizeObserver = class {
  targets = new Set();
  constructor(callback) { this.callback = callback; observer = this; }
  observe(el) { this.targets.add(el); }
  unobserve(el) { this.targets.delete(el); }
};
const instances = new Map();
globalThis.echarts = {
  init(el) {
    const chart = {resizes: 0, disposed: false, getDom: () => el,
      resize() { assert.equal(this.disposed, false); this.resizes++; },
      dispose() { this.disposed = true; instances.delete(el); }};
    instances.set(el, chart);
    return chart;
  },
  getInstanceByDom: el => instances.get(el),
};
const {mountChart, dropChart, charts} = await import(moduleURL(read('./lab/js/echarts.js')
  .replace("import { markRaw } from './vendor/vue.esm-browser.prod.js';", 'const {markRaw} = globalThis.chartTestVue;')));
const resize = (target, width, height = 340) => observer.callback([{target, contentRect: {width, height}}]);

test('隐藏图表重新显示、容器缩放时重绘，零尺寸不重绘，销毁后停止监听', () => {
  const trendEl = {}, winEl = {};
  const trend = mountChart(trendEl), win = mountChart(winEl);
  assert.equal(Vue.isProxy(Vue.reactive({trend}).trend), false);
  assert.deepEqual(observer.targets, new Set([trendEl, winEl]));
  resize(trendEl, 0, 0);
  assert.equal(trend.resizes, 0);
  resize(trendEl, 677);
  resize(winEl, 677);
  assert.equal(trend.resizes, 1);
  assert.equal(win.resizes, 1);
  resize(trendEl, 340);
  resize(trendEl, 340, 0);
  resize(trendEl, 0, 0);
  resize(trendEl, 677);
  assert.equal(trend.resizes, 3);
  dropChart(trend);
  assert.equal(trend.disposed, true);
  assert.deepEqual(observer.targets, new Set([winEl]));
  assert.deepEqual(charts, [win]);
  resize(trendEl, 677); // 已排队的通知不能操作销毁实例。
  assert.equal(trend.resizes, 3);
  dropChart(win);
  dropChart(null);
  assert.equal(observer.targets.size, 0);
  assert.equal(charts.length, 0);
});

test('容器或 ECharts 缺失时保留文本降级', () => {
  assert.equal(mountChart(null), null);
  const previous = globalThis.echarts;
  delete globalThis.echarts;
  try { assert.equal(mountChart({}), null); }
  finally { globalThis.echarts = previous; }
  assert.equal(observer.targets.size, 0);
});
