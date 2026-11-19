// 使用连续的有效指数收盘价计算日涨跌幅和简单平均 RSI。
export function indexResearchMetrics(points, period = 14) {
  let previous = null;
  let changes = [];
  return points.map(({close}) => {
    if (!Number.isFinite(close) || close <= 0) {
      previous = null;
      changes = [];
      return {change: null, rsi: null};
    }
    if (previous === null) {
      previous = close;
      return {change: null, rsi: null};
    }
    const delta = close - previous;
    const change = delta / previous * 100;
    previous = close;
    changes.push(delta);
    if (changes.length > period) changes.shift();
    if (changes.length < period) return {change, rsi: null};
    const gain = changes.reduce((sum, x) => sum + Math.max(x, 0), 0);
    const loss = changes.reduce((sum, x) => sum + Math.max(-x, 0), 0);
    return {change, rsi: gain + loss === 0 ? 50 : 100 * gain / (gain + loss)};
  });
}
