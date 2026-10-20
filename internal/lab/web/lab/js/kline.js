import { markRaw } from './vendor/vue.esm-browser.prod.js';

export function mountKline(el) {
  const chart = globalThis.StrategyKline?.mount(el);
  return chart ? markRaw(chart) : null;
}

export function dropKline(chart) { chart?.dispose(); }
