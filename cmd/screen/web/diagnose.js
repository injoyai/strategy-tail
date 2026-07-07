/* =========================================================
 * 选股系统 - 诊断弹窗
 * - lightweight-charts K线图 + 标注
 * - 诊断树 + explain 逐步判定
 * 移植自 cmd/visualize/template.go
 * ========================================================= */

let diagChart = null;       // 当前图表实例
let diagVolumeSeries = null;
let diagCandleSeries = null;
let diagStrategies = [];    // 策略列表(从全局获取)

// ── 打开诊断弹窗 ──

function openDiagnose(code, strategy) {
  // 默认使用当前选中的策略卡片
  if (!strategy) {
    strategy = (typeof currentStrategy !== 'undefined' && currentStrategy !== 'all')
      ? currentStrategy : '';
  }

  const overlay = document.getElementById('diagOverlay');
  overlay.style.display = 'flex';

  // 填充策略下拉
  const sel = document.getElementById('diagStrategySelect');
  if (typeof strategies !== 'undefined') {
    diagStrategies = strategies;
  }
  let selHTML = `<option value="">全部策略</option>`;
  for (const st of diagStrategies) {
    selHTML += `<option value="${st.key}" ${st.key === strategy ? 'selected' : ''}>${st.name}</option>`;
  }
  sel.innerHTML = selHTML;

  sel.onchange = () => {
    const newStrategy = sel.value;
    loadDiagnose(code, newStrategy);
  };

  loadDiagnose(code, strategy);
}

// ── 关闭诊断弹窗 ──

function closeDiagnose() {
  const overlay = document.getElementById('diagOverlay');
  overlay.style.display = 'none';
  if (diagChart) {
    diagChart.remove();
    diagChart = null;
    diagCandleSeries = null;
    diagVolumeSeries = null;
  }
}

// ── 加载诊断数据 ──

async function loadDiagnose(code, strategy) {
  const chartWrap = document.getElementById('diagChart');
  const explainEl = document.getElementById('diagExplain');
  const treeEl = document.getElementById('diagTree');
  const codeEl = document.getElementById('diagCode');
  const nameEl = document.getElementById('diagName');
  const badgeEl = document.getElementById('diagBadge');

  // 加载中
  chartWrap.innerHTML = '<div class="loading"><div class="spinner"></div><div>加载诊断数据...</div></div>';
  explainEl.innerHTML = '';
  treeEl.innerHTML = '';

  try {
    const url = `/api/diagnose?code=${encodeURIComponent(code)}&strategy=${encodeURIComponent(strategy || '')}`;
    const resp = await fetch(url);
    const data = await resp.json();

    if (data.error) {
      chartWrap.innerHTML = `<div class="empty-state"><div class="icon">!</div><div>${data.error}</div></div>`;
      return;
    }

    // header
    codeEl.textContent = data.code;
    nameEl.textContent = data.name;
    if (data.matched) {
      badgeEl.textContent = '符合策略';
      badgeEl.className = 'diag-badge matched';
    } else {
      badgeEl.textContent = '不符合';
      badgeEl.className = 'diag-badge not-matched';
    }

    // 渲染K线图
    renderChart(data);

    // 渲染交易汇总
    renderTradeSummary(data.trades || []);

    // 渲染 explain
    renderExplain(data.explain || []);

    // 渲染诊断树
    renderDiagnosisTree(data.diagnosis, treeEl);

  } catch (e) {
    chartWrap.innerHTML = `<div class="empty-state"><div class="icon">!</div><div>加载失败: ${e.message}</div></div>`;
  }
}

// ── K线图渲染 (移植自 visualize template.go) ──

function renderChart(data) {
  const chartWrap = document.getElementById('diagChart');
  chartWrap.innerHTML = '';

  // 清理旧图表
  if (diagChart) {
    diagChart.remove();
    diagChart = null;
  }

  const chart = LightweightCharts.createChart(chartWrap, {
    layout: {
      background: { type: 'solid', color: '#0a0e16' },
      textColor: '#94a3b8',
      fontSize: 11,
    },
    grid: {
      vertLines: { color: 'rgba(35, 43, 61, 0.5)' },
      horzLines: { color: 'rgba(35, 43, 61, 0.5)' },
    },
    crosshair: {
      mode: LightweightCharts.CrosshairMode.Normal,
    },
    rightPriceScale: {
      borderColor: '#232b3d',
    },
    timeScale: {
      borderColor: '#232b3d',
      timeVisible: false,
    },
  });

  // 手机端判断
  const isMobile = chartWrap.clientWidth < 768;

  diagChart = chart;

  // 蜡烛图 - A股惯例: 涨红跌绿
  const candleSeries = chart.addCandlestickSeries({
    upColor: '#ef4444',
    downColor: '#22c55e',
    borderUpColor: '#ef4444',
    borderDownColor: '#22c55e',
    wickUpColor: '#ef4444',
    wickDownColor: '#22c55e',
  });
  diagCandleSeries = candleSeries;

  const candleData = (data.klines || []).map(k => ({
    time: k.time,
    open: k.open,
    high: k.high,
    low: k.low,
    close: k.close,
  }));
  candleSeries.setData(candleData);

  // 成交量
  const volumeSeries = chart.addHistogramSeries({
    color: 'rgba(59, 130, 246, 0.3)',
    priceFormat: { type: 'volume' },
    priceScaleId: 'volume',
  });
  diagVolumeSeries = volumeSeries;
  chart.priceScale('volume').applyOptions({
    scaleMargins: { top: 0.85, bottom: 0 },
  });

  const volumeData = (data.klines || []).map(k => ({
    time: k.time,
    value: k.volume,
    color: k.close >= k.open ? 'rgba(239, 68, 68, 0.3)' : 'rgba(34, 197, 94, 0.3)', // A股: 涨红跌绿
  }));
  volumeSeries.setData(volumeData);

  // 标注 markers — 买卖点用小三角,策略标注用小圆点
  const markers = (data.annotations || []).map(a => {
    const label = a.label || '';
    const color = a.color || '#3b82f6';
    const isBuy = label.includes('买');
    const isSell = label.includes('卖');
    // 将 ISO 时间统一为 YYYY-MM-DD,与K线数据对齐
    let t = a.time || '';
    if (typeof t === 'string' && t.includes('T')) t = t.split('T')[0];
    return {
      time: t,
      position: isBuy ? 'belowBar' : 'aboveBar',
      color: color,
      shape: isBuy ? 'arrowUp' : (isSell ? 'arrowDown' : 'circle'),
      size: 1,
      text: label,
    };
  }).filter(a => a.time); // 过滤掉无时间的标注

  // 去重: 同一天同一类型的标注只保留一个
  const seen = new Set();
  const dedupedMarkers = markers.filter(m => {
    const key = m.time + '|' + m.shape;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });

  // 按时间排序, lightweight-charts 要求 markers 时间有序
  dedupedMarkers.sort((a, b) => {
    if (a.time < b.time) return -1;
    if (a.time > b.time) return 1;
    return 0;
  });

  if (dedupedMarkers.length > 0) {
    try {
      candleSeries.setMarkers(dedupedMarkers);
    } catch (e) {
      console.warn('设置标注失败', e);
    }
  }

  // 自适应大小
  const resizeObserver = new ResizeObserver(() => {
    chart.applyOptions({
      width: chartWrap.clientWidth,
      height: chartWrap.clientHeight,
    });
  });
  resizeObserver.observe(chartWrap);

  // 设置默认可见K线数量: 手机约60根,桌面约120根
  const totalBars = (data.klines || []).length;
  const visibleBars = isMobile ? 60 : 120;
  if (totalBars > visibleBars) {
    chart.timeScale().setVisibleLogicalRange({
      from: totalBars - visibleBars,
      to: totalBars + 2,
    });
  } else {
    chart.timeScale().fitContent();
  }
}

// ── 交易汇总渲染 ──

function renderTradeSummary(trades) {
  const el = document.getElementById('diagTradeSummary');
  if (!el) return;

  if (!trades || trades.length === 0) {
    el.innerHTML = '';
    return;
  }

  // 统计
  let totalTrades = trades.length;
  let soldTrades = 0;
  let holdingTrades = 0;
  let winCount = 0;
  let totalProfit = 0; // 累计收益率(等额累加)
  let totalWin = 0;
  let totalLoss = 0;

  for (const t of trades) {
    if (t.sold) {
      soldTrades++;
      const rate = t.profit_rate || 0;
      totalProfit += rate;
      if (rate > 0) { winCount++; totalWin += rate; }
      else { totalLoss += -rate; }
    } else {
      holdingTrades++;
    }
  }

  const winRate = soldTrades > 0 ? (winCount / soldTrades * 100) : 0;
  const profitFactor = totalLoss > 0 ? (totalWin / totalLoss) : (totalWin > 0 ? Infinity : 0);
  const avgProfit = soldTrades > 0 ? (totalProfit / soldTrades) : 0;

  const pfText = isFinite(profitFactor) ? profitFactor.toFixed(2) : '∞';
  const profitCls = totalProfit >= 0 ? 'num-up' : 'num-down';
  const winRateCls = winRate >= 50 ? 'num-up' : 'num-down';

  let html = `<div class="trade-summary-header">
    <div class="summary-item"><span class="summary-label">交易次数</span><span class="summary-val">${totalTrades}</span></div>
    <div class="summary-item"><span class="summary-label">已卖出</span><span class="summary-val">${soldTrades}</span></div>
    <div class="summary-item"><span class="summary-label">持有中</span><span class="summary-val">${holdingTrades}</span></div>
    <div class="summary-item"><span class="summary-label">胜率</span><span class="summary-val ${winRateCls}">${soldTrades > 0 ? winRate.toFixed(1) + '%' : '--'}</span></div>
    <div class="summary-item"><span class="summary-label">盈亏比</span><span class="summary-val">${soldTrades > 0 ? pfText : '--'}</span></div>
    <div class="summary-item"><span class="summary-label">平均单笔</span><span class="summary-val ${avgProfit >= 0 ? 'num-up' : 'num-down'}">${soldTrades > 0 ? (avgProfit >= 0 ? '+' : '') + avgProfit.toFixed(2) + '%' : '--'}</span></div>
    <div class="summary-item"><span class="summary-label">累计收益</span><span class="summary-val ${profitCls}">${totalProfit >= 0 ? '+' : ''}${totalProfit.toFixed(2)}%</span></div>
  </div>`;

  // 交易明细表
  html += `<table><thead><tr>
    <th>#</th><th>买入时间</th><th>买入价</th><th>卖出时间</th><th>现价/卖价</th><th>收益率</th><th>状态</th>
  </tr></thead><tbody>`;

  for (let i = trades.length - 1; i >= 0; i--) {
    const t = trades[i];
    const rate = t.profit_rate || 0;
    const rateCls = rate >= 0 ? 'num-up' : 'num-down';
    const rateText = `${rate >= 0 ? '+' : ''}${rate.toFixed(2)}%`;
    const statusText = t.sold ? '已卖出' : '持有中';
    const statusCls = t.sold ? 'tag-sold' : 'tag-holding';
    const currText = t.curr_price ? t.curr_price.toFixed(2) : '--';
    html += `<tr>
      <td>${trades.length - i}</td>
      <td>${t.buy_time || '--'}</td>
      <td>${t.buy_price ? t.buy_price.toFixed(2) : '--'}</td>
      <td>${t.sold ? (t.sell_time || '--') : '--'}</td>
      <td>${currText}</td>
      <td class="${rateCls}">${rateText}</td>
      <td><span class="${statusCls}">${statusText}</span></td>
    </tr>`;
  }
  html += `</tbody></table>`;

  el.innerHTML = html;
}

// ── explain 渲染 ──

function renderExplain(explain) {
  const el = document.getElementById('diagExplain');
  const titleEl = document.getElementById('diagExplainTitle');
  if (!explain || explain.length === 0) {
    el.innerHTML = '';
    if (titleEl) titleEl.style.display = 'none';
    return;
  }
  if (titleEl) titleEl.style.display = '';

  let html = '';
  for (const step of explain) {
    const pass = step.matched;
    const icon = pass ? '✓' : '✗';
    const cls = pass ? 'pass' : 'fail';
    html += `
      <div class="explain-step">
        <div class="explain-icon ${cls}">${icon}</div>
        <div class="explain-content">
          <div class="explain-name">${esc(step.name)}</div>
          ${step.detail ? `<div class="explain-detail">${esc(step.detail)}</div>` : ''}
        </div>
      </div>
    `;
  }
  el.innerHTML = html;
}

// ── 诊断树渲染 ──

function renderDiagnosisTree(node, container) {
  if (!node) {
    container.innerHTML = '<div class="empty-state"><div>无诊断树数据</div></div>';
    return;
  }
  container.innerHTML = '';
  renderTreeNode(node, container, true);
}

function renderTreeNode(node, parent, isRoot) {
  if (!node) return;

  const nodeDiv = document.createElement('div');
  nodeDiv.className = 'diag-tree-node';

  const labelDiv = document.createElement('div');
  labelDiv.className = 'diag-tree-label ' + (node.matched ? 'matched' : 'not-matched');

  const icon = node.matched ? '✓' : '✗';
  const iconCls = node.matched ? 'pass' : 'fail';
  labelDiv.innerHTML = `<span class="tree-icon ${iconCls}">${icon}</span><span>${esc(node.name)}</span>`;

  nodeDiv.appendChild(labelDiv);

  if (node.children && node.children.length > 0) {
    const childContainer = document.createElement('div');
    childContainer.className = 'diag-tree-children';
    for (const child of node.children) {
      renderTreeNode(child, childContainer, false);
    }
    nodeDiv.appendChild(childContainer);
  }

  parent.appendChild(nodeDiv);
}

// ── 辅助 ──

function esc(s) {
  if (!s) return '';
  return String(s).replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
  }[c]));
}

// ESC 关闭弹窗
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') {
    const overlay = document.getElementById('diagOverlay');
    if (overlay && overlay.style.display !== 'none') {
      closeDiagnose();
    }
  }
});

// 点击遮罩关闭
document.addEventListener('DOMContentLoaded', () => {
  const overlay = document.getElementById('diagOverlay');
  if (overlay) {
    overlay.addEventListener('click', (e) => {
      if (e.target === overlay) closeDiagnose();
    });
  }
});
