package main

// renderHTML 将 JSON 数据注入 HTML 模板，返回完整的可浏览器渲染的 HTML 字符串。
func renderHTML(jsonData []byte) string {
	return `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>策略可视化</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500;600&display=swap" rel="stylesheet">
<script src="https://unpkg.com/lightweight-charts@4.1.3/dist/lightweight-charts.standalone.production.js"></script>
<style>
  :root {
    --bg: #0a0e14;
    --panel: #111721;
    --panel-2: #161d2a;
    --border: #1e2838;
    --border-light: #2a3548;
    --text: #c5cdd9;
    --text-muted: #6b7588;
    --text-dim: #4a5468;
    --accent: #f59e0b;
    --accent-dim: rgba(245, 158, 11, 0.12);
    --green: #10b981;
    --green-dim: rgba(16, 185, 129, 0.10);
    --red: #f43f5e;
    --red-dim: rgba(244, 63, 94, 0.10);
    --mono: 'JetBrains Mono', 'SF Mono', 'Consolas', monospace;
    --sans: -apple-system, "Microsoft YaHei", "PingFang SC", sans-serif;
  }
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: var(--sans); background: var(--bg); color: var(--text); overflow: hidden; }

  /* ── 顶部状态栏 ── */
  #header {
    display: flex; align-items: center; gap: 20px;
    padding: 10px 24px; height: 52px;
    background: var(--panel); border-bottom: 1px solid var(--border);
  }
  #header .code {
    font-family: var(--mono); font-size: 18px; font-weight: 600;
    color: var(--text); letter-spacing: 0.5px;
  }
  #header .sep { color: var(--text-dim); font-size: 14px; }
  #header .strategy {
    font-size: 14px; color: var(--accent); font-weight: 500;
    padding: 3px 10px; background: var(--accent-dim); border-radius: 3px;
  }
  #header .badge {
    margin-left: auto; padding: 4px 14px; border-radius: 3px;
    font-size: 13px; font-weight: 600; letter-spacing: 1px;
  }
  .badge.hit { background: var(--green); color: #042f1e; }
  .badge.miss { background: var(--red); color: #2a0610; }

  /* ── 主体布局 ── */
  #main { display: flex; height: calc(100vh - 52px); }
  #chart-wrap { flex: 1; display: flex; flex-direction: column; }
  #legend {
    display: flex; gap: 16px; padding: 8px 20px;
    background: var(--panel); border-bottom: 1px solid var(--border);
    font-size: 12px; color: var(--text-muted);
  }
  #legend .item { display: flex; align-items: center; gap: 6px; }
  #legend .dot {
    width: 8px; height: 8px; border-radius: 50%; display: inline-block;
  }
  #legend .dot.h { background: var(--red); }
  #legend .dot.l { background: var(--green); }
  #legend .mono { font-family: var(--mono); color: var(--text); }
  #chart-container { flex: 1; position: relative; }

  /* ── 诊断面板 ── */
  #diagnosis-panel {
    width: 360px; min-width: 360px;
    background: var(--panel); border-left: 1px solid var(--border);
    display: flex; flex-direction: column; overflow: hidden;
  }
  #diagnosis-panel .panel-head {
    padding: 14px 18px 10px;
    font-size: 11px; font-weight: 600; text-transform: uppercase;
    letter-spacing: 2px; color: var(--text-muted);
    border-bottom: 1px solid var(--border);
  }
  #diag-tree { flex: 1; overflow-y: auto; padding: 8px 0; }

  /* 规则明细 */
  #explain-section { border-bottom: 1px solid var(--border); max-height: 45%; overflow-y: auto; }
  #explain-section .panel-head { border-bottom: 1px solid var(--border); }
  .explain-row {
    display: flex; align-items: flex-start; gap: 8px;
    padding: 6px 18px; font-size: 12px; line-height: 1.5;
    transition: background 0.12s;
  }
  .explain-row:hover { background: var(--panel-2); }
  .explain-row .icon {
    width: 14px; height: 14px; flex-shrink: 0; margin-top: 2px;
    display: flex; align-items: center; justify-content: center;
    font-size: 10px; font-weight: 700; border-radius: 3px;
  }
  .explain-row .icon.pass { background: var(--green); color: #042f1e; }
  .explain-row .icon.fail { background: var(--red); color: #2a0610; }
  .explain-row .body { flex: 1; min-width: 0; }
  .explain-row .name { color: var(--text); font-weight: 500; }
  .explain-row .detail {
    display: block; margin-top: 2px;
    font-family: var(--mono); font-size: 11px; color: var(--text-muted);
    word-break: break-all;
  }

  /* 诊断节点 */
  .diag-row {
    display: flex; align-items: center; gap: 8px;
    padding: 5px 18px; font-size: 13px; line-height: 1.5;
    cursor: default; transition: background 0.12s;
  }
  .diag-row:hover { background: var(--panel-2); }
  .diag-row .icon {
    width: 16px; height: 16px; flex-shrink: 0;
    display: flex; align-items: center; justify-content: center;
    font-size: 11px; font-weight: 700; border-radius: 3px;
  }
  .diag-row .icon.pass { background: var(--green); color: #042f1e; }
  .diag-row .icon.fail { background: var(--red); color: #2a0610; }
  .diag-row .icon.composite { background: var(--border-light); color: var(--text-muted); }
  .diag-row .name { flex: 1; color: var(--text); }
  .diag-row .name.muted { color: var(--text-muted); }
  .diag-children { border-left: 1px solid var(--border-light); margin-left: 25px; }

  /* 无标注提示 */
  .no-annot {
    margin: 8px 18px; padding: 10px 12px;
    background: var(--panel-2); border-radius: 4px;
    color: var(--text-muted); font-size: 12px;
  }

  /* 滚动条 */
  ::-webkit-scrollbar { width: 6px; }
  ::-webkit-scrollbar-track { background: transparent; }
  ::-webkit-scrollbar-thumb { background: var(--border-light); border-radius: 3px; }
  ::-webkit-scrollbar-thumb:hover { background: var(--text-dim); }
</style>
</head>
<body>

<div id="header">
  <span class="code" id="title-code"></span>
  <span class="sep">/</span>
  <span class="strategy" id="title-strategy"></span>
  <span class="badge" id="status-badge"></span>
</div>

<div id="main">
  <div id="chart-wrap">
    <div id="legend">
      <div class="item"><span class="dot h"></span><span class="mono">H1 / H2</span><span>高点</span></div>
      <div class="item"><span class="dot l"></span><span class="mono">L1 / L2</span><span>低点</span></div>
      <div class="item" id="annot-count"></div>
    </div>
    <div id="chart-container"></div>
  </div>
  <div id="diagnosis-panel">
    <div id="explain-section" style="display:none">
      <div class="panel-head">规则明细</div>
      <div id="explain-list"></div>
    </div>
    <div class="panel-head">诊断树</div>
    <div id="diag-tree"></div>
  </div>
</div>

<script>
const DATA = ` + "`" + string(jsonData) + "`" + `;

function init() {
  const data = JSON.parse(DATA);

  // ── 头部 ──
  document.getElementById('title-code').textContent = data.code;
  document.getElementById('title-strategy').textContent = data.strategy;
  const badge = document.getElementById('status-badge');
  badge.textContent = data.matched ? '命中' : '未命中';
  badge.className = 'badge ' + (data.matched ? 'hit' : 'miss');

  // 标注计数
  const annotCount = (data.annotations || []).length;
  document.getElementById('annot-count').textContent =
    annotCount > 0 ? annotCount + ' 个标注点' : '无标注';

  // ── 图表 ──
  const chart = LightweightCharts.createChart(
    document.getElementById('chart-container'),
    {
      layout: {
        background: { color: '#0a0e14' },
        textColor: '#6b7588',
        fontFamily: 'JetBrains Mono, monospace',
        fontSize: 11,
      },
      grid: {
        vertLines: { color: 'rgba(30, 40, 56, 0.5)' },
        horzLines: { color: 'rgba(30, 40, 56, 0.5)' },
      },
      timeScale: {
        borderColor: '#1e2838',
        timeVisible: false,
        rightOffset: 4,
      },
      rightPriceScale: {
        borderColor: '#1e2838',
        scaleMargins: { top: 0.08, bottom: 0.22 },
      },
      crosshair: {
        mode: LightweightCharts.CrosshairMode.Normal,
        vertLine: { color: '#2a3548', width: 1, labelBackgroundColor: '#f59e0b' },
        horzLine: { color: '#2a3548', width: 1, labelBackgroundColor: '#f59e0b' },
      },
    }
  );

  // 蜡烛图
  const candleSeries = chart.addCandlestickSeries({
    upColor: '#10b981', downColor: '#f43f5e',
    borderUpColor: '#10b981', borderDownColor: '#f43f5e',
    wickUpColor: '#10b98180', wickDownColor: '#f43f5e80',
  });
  candleSeries.setData(data.klines.map(k => ({
    time: k.time, open: k.open, high: k.high, low: k.low, close: k.close
  })));

  // 成交量
  const volumeSeries = chart.addHistogramSeries({
    priceFormat: { type: 'volume' },
    priceScaleId: 'vol',
  });
  chart.priceScale('vol').applyOptions({
    scaleMargins: { top: 0.82, bottom: 0 },
  });
  volumeSeries.setData(data.klines.map(k => ({
    time: k.time,
    value: k.volume,
    color: k.close >= k.open ? 'rgba(16,185,129,0.25)' : 'rgba(244,63,94,0.25)'
  })));

  // 标注 markers
  if (data.annotations && data.annotations.length > 0) {
    const markers = data.annotations
      .map(a => ({
        time: formatDate(a.Time),
        position: a.Label.startsWith('H') ? 'aboveBar' : 'belowBar',
        color: a.Color,
        shape: 'circle',
        text: a.Label,
        size: 2,
      }))
      .sort((a, b) => a.time.localeCompare(b.time));
    candleSeries.setMarkers(markers);
  }

  chart.timeScale().fitContent();

  // ── 规则明细 ──
  if (Array.isArray(data.explain) && data.explain.length > 0) {
    const section = document.getElementById('explain-section');
    const list = document.getElementById('explain-list');
    section.style.display = 'block';
    for (const step of data.explain) {
      const row = document.createElement('div');
      row.className = 'explain-row';
      const icon = document.createElement('span');
      icon.className = 'icon ' + (step.matched ? 'pass' : 'fail');
      icon.textContent = step.matched ? '✓' : '✗';
      row.appendChild(icon);
      const body = document.createElement('div');
      body.className = 'body';
      const name = document.createElement('span');
      name.className = 'name';
      name.textContent = step.name;
      body.appendChild(name);
      if (step.detail) {
        const detail = document.createElement('span');
        detail.className = 'detail';
        detail.textContent = step.detail;
        body.appendChild(detail);
      }
      row.appendChild(body);
      list.appendChild(row);
    }
  }

  // ── 诊断树 ──
  renderDiagnosis(data.diagnosis, document.getElementById('diag-tree'), 0);
}

function formatDate(t) {
  if (typeof t === 'string') return t.substring(0, 10);
  return t;
}

function renderDiagnosis(node, parent, depth) {
  const row = document.createElement('div');
  row.className = 'diag-row';
  row.style.paddingLeft = (18 + depth * 20) + 'px';

  const hasChildren = node.Children && node.Children.length > 0;
  const icon = document.createElement('span');
  icon.className = 'icon ' + (hasChildren ? 'composite' : (node.Matched ? 'pass' : 'fail'));
  icon.textContent = hasChildren ? (node.Matched ? '✓' : '✗') : (node.Matched ? '✓' : '✗');
  if (hasChildren) {
    icon.style.background = node.Matched ? 'var(--green)' : 'var(--red)';
    icon.style.color = node.Matched ? '#042f1e' : '#2a0610';
  }
  row.appendChild(icon);

  const name = document.createElement('span');
  name.className = 'name' + (hasChildren ? '' : ' muted');
  name.textContent = node.Name;
  row.appendChild(name);

  parent.appendChild(row);

  if (hasChildren) {
    const childWrap = document.createElement('div');
    childWrap.className = 'diag-children';
    parent.appendChild(childWrap);
    for (const child of node.Children) {
      renderDiagnosis(child, childWrap, depth + 1);
    }
  }
}

init();
</script>
</body>
</html>`
}
