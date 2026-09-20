// ECharts 实例登记与配色常量。
// echarts 全局对象由 index.html 的 vendor 脚本提供；不可用时 mountChart
// 返回 null，各调用方须降级为表格/文本展示，页面功能保持可用。
import { markRaw } from './vendor/vue.esm-browser.prod.js';

export const charts = []; // 全部实例：窗口 resize 时统一 resize()

export function mountChart(el, useDark = true) {
  if (!el || typeof echarts === 'undefined') return null;
  // markRaw：实例严禁进入 Vue 响应式系统。调用方把它存入 data 后会被深代理，
  // 代理壳注入 echarts 内部模型后，第二次起的 resize()/setOption() 会在
  // echarts 内部抛 TypeError（且发生在 nextTick 回调里被静默吞没），图表从此不再重绘。
  const c = markRaw(echarts.init(el, useDark ? 'dark' : undefined));
  charts.push(c);
  return c;
}

export function dropChart(c) {
  const i = charts.indexOf(c);
  if (i >= 0) charts.splice(i, 1);
  if (c) c.dispose();
}

window.addEventListener('resize', () => charts.forEach(c => c.resize()));

// A 股语义：红涨绿跌（仅收益语义；分组柱、涨跌、盈亏同源）。
export const CHART_COLORS = {pos: '#ef4444', neg: '#22c55e'};

// K 线 MA 线配色。
export const MA_COLORS = {ma5: '#f59e0b', ma10: '#8b5cf6', ma20: '#3b82f6'};

// 净值图：毛策略 / 净策略。
export const NAV_COLORS = {gross: '#4c8dff', net: '#ecd69c'};

// 年度对比调色板：年份是类别，不借用红涨绿跌语义；配合实/虚/点线辅助辨认。
export const ANNUAL_PALETTE = ['#d8b45a','#4c8dff','#a78bfa','#38bdf8','#f59e0b','#f472b6','#94a3b8','#2dd4bf','#818cf8','#c084fc','#67e8f9'];
