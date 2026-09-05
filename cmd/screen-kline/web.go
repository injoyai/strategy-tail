package main

const indexHTML = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>策略选股K线展示</title>
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
    --green: #10b981;
    --red: #f43f5e;
    --mono: 'JetBrains Mono', 'SF Mono', 'Consolas', monospace;
    --sans: -apple-system, "Microsoft YaHei", "PingFang SC", sans-serif;
  }
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: var(--sans); background: var(--bg); color: var(--text); overflow-x: hidden; }

  /* ── 顶部控制栏 ── */
  #header {
    display: flex; align-items: center; gap: 16px;
    padding: 10px 24px; height: 56px;
    background: var(--panel); border-bottom: 1px solid var(--border);
    position: sticky; top: 0; z-index: 100;
  }
  #header .title {
    font-size: 15px; font-weight: 600; color: var(--text);
    letter-spacing: 0.5px;
  }
  #header .strategy-tag {
    font-size: 12px; color: var(--accent); font-weight: 500;
    padding: 3px 10px; background: rgba(245,158,11,0.12); border-radius: 3px;
  }
  #header .spacer { flex: 1; }
  #header label { font-size: 13px; color: var(--text-muted); }
  #header input[type="date"] {
    background: var(--panel-2); border: 1px solid var(--border-light);
    color: var(--text); padding: 6px 10px; border-radius: 4px;
    font-family: var(--mono); font-size: 13px; outline: none;
  }
  #header input[type="date"]:focus { border-color: var(--accent); }
  #header button {
    background: var(--accent); color: #1a1208; border: none;
    padding: 7px 18px; border-radius: 4px; font-size: 13px;
    font-weight: 600; cursor: pointer; transition: opacity 0.15s;
  }
  #header button:hover { opacity: 0.85; }
  #header button:disabled { opacity: 0.4; cursor: not-allowed; }
  #header .count {
    font-size: 13px; color: var(--text-muted); font-family: var(--mono);
  }

  /* ── 股票网格 ── */
  #grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(480px, 1fr));
    gap: 1px;
    background: var(--border);
    padding: 1px;
  }
  .stock-card {
    background: var(--panel);
    display: flex; flex-direction: column;
    overflow: hidden;
  }
  .stock-card .card-header {
    display: flex; align-items: center; gap: 10px;
    padding: 8px 14px;
    background: var(--panel-2); border-bottom: 1px solid var(--border);
  }
  .stock-card .code {
    font-family: var(--mono); font-size: 14px; font-weight: 600;
    color: var(--text); letter-spacing: 0.5px;
  }
  .stock-card .price {
    font-family: var(--mono); font-size: 13px; color: var(--text-muted);
  }
  .stock-card .rise {
    font-family: var(--mono); font-size: 12px; font-weight: 600;
    padding: 2px 8px; border-radius: 3px;
  }
  .stock-card .rise.up { background: rgba(16,185,129,0.15); color: var(--green); }
  .stock-card .rise.down { background: rgba(244,63,94,0.15); color: var(--red); }
  .stock-card .chart-container {
    height: 220px; position: relative;
  }

  /* ── 加载/空状态 ── */
  #loading {
    display: flex; align-items: center; justify-content: center;
    height: 60vh; font-size: 16px; color: var(--text-muted);
  }
  #loading .spinner {
    width: 24px; height: 24px; margin-right: 12px;
    border: 3px solid var(--border-light); border-top-color: var(--accent);
    border-radius: 50%; animation: spin 0.8s linear infinite;
  }
  @keyframes spin { to { transform: rotate(360deg); } }
  #empty {
    display: none; align-items: center; justify-content: center;
    height: 60vh; font-size: 15px; color: var(--text-muted);
  }
</style>
</head>
<body>

<div id="header">
  <span class="title">策略选股K线</span>
  <span class="strategy-tag" id="strategy-name"></span>
  <span class="spacer"></span>
  <label>截止日期</label>
  <input type="date" id="date-input" />
  <button id="screen-btn" onclick="doScreen()">筛选</button>
  <span class="count" id="count-text"></span>
</div>

<div id="loading">
  <div class="spinner"></div>
  <span>正在选股...</span>
</div>

<div id="empty">该日期无命中股票</div>

<div id="grid"></div>

<script>
const STRATEGY = "MACD负柱缩短+转红";
document.getElementById('strategy-name').textContent = STRATEGY;

// 默认日期为今天
const today = new Date();
const todayStr = today.getFullYear() + '-' +
  String(today.getMonth()+1).padStart(2,'0') + '-' +
  String(today.getDate()).padStart(2,'0');
document.getElementById('date-input').value = todayStr;

const charts = [];

function doScreen() {
  const date = document.getElementById('date-input').value;
  if (!date) return;
  const btn = document.getElementById('screen-btn');
  btn.disabled = true;
  btn.textContent = '筛选中...';

  // 清除旧图表
  charts.forEach(c => c.remove());
  charts.length = 0;
  document.getElementById('grid').innerHTML = '';
  document.getElementById('empty').style.display = 'none';
  document.getElementById('loading').style.display = 'flex';
  document.getElementById('count-text').textContent = '';

  fetch('/api/screen?date=' + date)
    .then(r => r.json())
    .then(data => {
      document.getElementById('loading').style.display = 'none';
      document.getElementById('count-text').textContent = data.length + ' 只命中';
      if (data.length === 0) {
        document.getElementById('empty').style.display = 'flex';
        return;
      }
      renderStocks(data);
    })
    .catch(err => {
      document.getElementById('loading').style.display = 'none';
      alert('请求失败: ' + err);
    })
    .finally(() => {
      btn.disabled = false;
      btn.textContent = '筛选';
    });
}

function renderStocks(stocks) {
  const grid = document.getElementById('grid');
  for (const s of stocks) {
    const card = document.createElement('div');
    card.className = 'stock-card';

    const header = document.createElement('div');
    header.className = 'card-header';

    const code = document.createElement('span');
    code.className = 'code';
    code.textContent = s.code;
    header.appendChild(code);

    const price = document.createElement('span');
    price.className = 'price';
    price.textContent = '¥' + s.price.toFixed(2);
    header.appendChild(price);

    if (s.rise !== 0) {
      const rise = document.createElement('span');
      rise.className = 'rise ' + (s.rise >= 0 ? 'up' : 'down');
      rise.textContent = (s.rise >= 0 ? '+' : '') + s.rise.toFixed(2) + '%';
      header.appendChild(rise);
    }

    const chartDiv = document.createElement('div');
    chartDiv.className = 'chart-container';

    card.appendChild(header);
    card.appendChild(chartDiv);
    grid.appendChild(card);

    const chart = LightweightCharts.createChart(chartDiv, {
      layout: {
        background: { color: '#111721' },
        textColor: '#6b7588',
        fontFamily: 'JetBrains Mono, monospace',
        fontSize: 10,
      },
      grid: {
        vertLines: { color: 'rgba(30,40,56,0.4)' },
        horzLines: { color: 'rgba(30,40,56,0.4)' },
      },
      timeScale: {
        borderColor: '#1e2838',
        timeVisible: false,
        rightOffset: 2,
      },
      rightPriceScale: {
        borderColor: '#1e2838',
        scaleMargins: { top: 0.08, bottom: 0.25 },
      },
      crosshair: { mode: 0 },
      width: chartDiv.clientWidth,
      height: 220,
    });

    const candle = chart.addCandlestickSeries({
      upColor: '#10b981', downColor: '#f43f5e',
      borderUpColor: '#10b981', borderDownColor: '#f43f5e',
      wickUpColor: '#10b98180', wickDownColor: '#f43f5e80',
    });
    candle.setData(s.klines.map(k => ({
      time: k.time, open: k.open, high: k.high, low: k.low, close: k.close
    })));

    const vol = chart.addHistogramSeries({
      priceFormat: { type: 'volume' },
      priceScaleId: 'vol',
    });
    chart.priceScale('vol').applyOptions({ scaleMargins: { top: 0.82, bottom: 0 } });
    vol.setData(s.klines.map(k => ({
      time: k.time, value: k.volume,
      color: k.close >= k.open ? 'rgba(16,185,129,0.25)' : 'rgba(244,63,94,0.25)'
    })));

    chart.timeScale().fitContent();
    charts.push(chart);
  }
}

// 响应窗口缩放
window.addEventListener('resize', () => {
  charts.forEach((c, i) => {
    const container = document.querySelectorAll('.chart-container')[i];
    if (container) c.applyOptions({ width: container.clientWidth });
  });
});

// 启动时自动选股
doScreen();
</script>
</body>
</html>`
