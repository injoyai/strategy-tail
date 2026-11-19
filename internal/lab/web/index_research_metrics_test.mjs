import { test } from 'node:test';
import assert from 'node:assert/strict';
import { indexResearchMetrics } from './lab/js/index-research-metrics.js';

test('涨跌幅和 RSI 使用连续收盘价，缺失后重新预热', () => {
  const values = [100, ...Array.from({length: 14}, (_, i) => 101 + i), null, 100, 100, 100];
  const result = indexResearchMetrics(values.map(close => ({close})), 14);
  assert.equal(result[0].change, null);
  assert.equal(result[1].change, 1);
  assert.equal(result[13].rsi, null);
  assert.equal(result[14].rsi, 100);
  assert.equal(result[15].rsi, null);
  assert.equal(result[16].change, null);
  assert.equal(result[18].rsi, null);
  assert.equal(indexResearchMetrics([100, 100, 100].map(close => ({close})), 2)[2].rsi, 50);
  assert.equal(indexResearchMetrics([100, 90, 80].map(close => ({close})), 2)[2].rsi, 0);
});
