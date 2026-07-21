/* =========================================================
 * 选股系统 - 主逻辑
 * - HTTP 拉取策略列表 + 历史买卖点
 * - WebSocket 实时推送买点/卖点
 * - 策略卡片切换 + 标签多选筛选 + 日期/状态筛选
 * ========================================================= */

// ── 全局状态 ──
let strategies = [];           // 后端策略列表 [{key,name}]
let currentStrategy = ''; // 初始为空,加载策略列表后自动选第一个
let buyResults = [];           // 实时买点
let sellResults = [];          // 实时卖点
let historyResults = [];       // 历史买卖点(原始)
let ws = null;                 // WebSocket 实例
let reconnectTimer = null;
let searchText = '';           // 搜索关键词(代码/名称)

// 历史筛选状态
const historyFilter = {
  days: 30,        // 近多少天
  status: 'all',   // all / sold / holding
  tags: null,      // Set<string> 选中的标签, null=全选
};
const NO_TAG_KEY = '__NO_TAG__'; // 无标签占位

// ── 工具函数 ──

function fmtPct(v) {
  if (v === undefined || v === null || isNaN(v)) return '--';
  return (v >= 0 ? '+' : '') + v.toFixed(2) + '%';
}

function numClass(v) {
  if (v > 0) return 'num-up';
  if (v < 0) return 'num-down';
  return 'num-flat';
}

function esc(s) {
  if (!s) return '';
  return String(s).replace(/[&<>"']/g, c => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'
  }[c]));
}

function toTs(v) {
  const t = new Date(v || '').getTime();
  return isNaN(t) ? 0 : t;
}

// 格式化为 月-日 时:分, 如 "06-12 15:00"
function fmtMDHM(v) {
  if (!v) return '--';
  const d = new Date(v);
  if (isNaN(d)) return '--';
  const mm = String(d.getMonth() + 1).padStart(2, '0');
  const dd = String(d.getDate()).padStart(2, '0');
  const hh = String(d.getHours()).padStart(2, '0');
  const mi = String(d.getMinutes()).padStart(2, '0');
  return `${mm}-${dd} ${hh}:${mi}`;
}

// ── 策略过滤 ──

function matchStrategy(item) {
  if (!currentStrategy) return true; // 未选中策略时显示全部
  if (!item.strategy) return true;
  return item.strategy === currentStrategy;
}

function matchSearch(item) {
  if (!searchText) return true;
  const q = searchText.toLowerCase();
  return (item.code && item.code.toLowerCase().includes(q)) ||
         (item.name && item.name.toLowerCase().includes(q));
}

function matchAll(item) {
  return matchStrategy(item) && matchSearch(item);
}

// ── 策略卡片 ──

async function loadStrategies() {
  try {
    const resp = await fetch('/api/strategies');
    strategies = await resp.json();
  } catch (e) {
    console.error('加载策略列表失败', e);
    strategies = [];
  }
  // 自动选中第一个策略
  if (!currentStrategy && strategies.length > 0) {
    currentStrategy = strategies[0].key;
  }
  renderStrategyBar();
}

function renderStrategyBar() {
  const bar = document.getElementById('strategyBar');

  // 统计各策略的买点/历史/胜率/盈亏比
  function statsForKey(key) {
    const buys = key === 'all'
      ? buyResults.length
      : buyResults.filter(b => b.strategy === key).length;
    const hist = key === 'all'
      ? historyResults
      : historyResults.filter(r => r.strategy === key);
    const sold = hist.filter(r => r.sold);
    const win = sold.filter(r => (r.income || 0) > 0).length;
    const winRate = sold.length > 0 ? (win / sold.length * 100).toFixed(0) + '%' : '--';
    // 盈亏比 = 总盈利金额 / 总亏损金额
    let winSum = 0, lossSum = 0;
    for (const r of sold) {
      const rate = r.income || 0;
      if (rate > 0) winSum += rate;
      else lossSum += -rate;
    }
    let pf;
    if (sold.length === 0) pf = '--';
    else if (lossSum === 0) pf = '∞';
    else pf = (winSum / lossSum).toFixed(2);
    return { buys, histCount: hist.length, winRate, pf };
  }

  let html = '';
  for (const st of strategies) {
    const s = statsForKey(st.key);
    html += `
      <div class="strategy-card ${currentStrategy === st.key ? 'active' : ''}" onclick="selectStrategy('${st.key}')">
        <div class="strategy-name">${esc(st.name)}</div>
        <div class="strategy-count">买点 ${s.buys} · 历史 ${s.histCount} · 盈亏比 ${s.pf} · 胜率 ${s.winRate}</div>
      </div>
    `;
  }
  bar.innerHTML = html;
}

function selectStrategy(key) {
  currentStrategy = key;
  historyFilter.tags = null; // 重置标签筛选,按新策略重建
  renderStrategyBar();
  ensureHistoryTagFilters();
  renderBuyResults();
  renderSellResults();
  renderHistoryResults();
}

// ── 搜索 ──

function onSearch(input) {
  searchText = input.value.trim();
  renderBuyResults();
  renderSellResults();
  renderHistoryResults();
}

// 手动输入股票代码查看诊断
function openDiagnoseByInput() {
  const input = document.getElementById('searchInput');
  if (!input) return;
  const code = input.value.trim();
  if (!code) return;
  openDiagnose(code);
}

// ── 历史数据(HTTP) ──

async function loadHistory() {
  try {
    const resp = await fetch('/api/history');
    const data = await resp.json();
    historyResults = data.results || [];
    ensureHistoryTagFilters();
    renderStrategyBar();
    renderHistoryResults();
  } catch (e) {
    console.error('加载历史数据失败', e);
  }
}

// ── WebSocket 实时 ──

function connectWS() {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  ws = new WebSocket(`${proto}://${location.host}/ws`);

  ws.onopen = () => {
    updateWSStatus(true);
    if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
  };
  ws.onclose = () => {
    updateWSStatus(false);
    if (!reconnectTimer) { reconnectTimer = setTimeout(connectWS, 3000); }
  };
  ws.onerror = () => { ws.close(); };
  ws.onmessage = (event) => {
    try {
      const msg = JSON.parse(event.data);
      switch (msg.type) {
        case 'buy':
          buyResults = msg.results || [];
          renderStrategyBar();
          renderBuyResults();
          updateRefreshTime(msg.time);
          break;
        case 'sell':
          sellResults = msg.results || [];
          renderSellResults();
          loadHistory(); // 卖出后刷新历史
          updateRefreshTime(msg.time);
          break;
      }
    } catch (e) { console.error('解析WS消息失败', e); }
  };
}

function updateWSStatus(online) {
  const dot = document.querySelector('.ws-dot');
  const text = document.getElementById('wsStatusText');
  if (dot) dot.classList.toggle('offline', !online);
  if (text) text.textContent = online ? '实时连接' : '已断开';
}

function updateRefreshTime(t) {
  const el = document.getElementById('refreshTime');
  if (el && t) el.textContent = t;
}

// ── 渲染：买点 ──

function renderBuyResults() {
  const body = document.getElementById('buyBody');
  const countEl = document.getElementById('buyCount');
  const filtered = buyResults.filter(matchAll);
  if (countEl) countEl.textContent = filtered.length;

  if (filtered.length === 0) {
    body.innerHTML = emptyHTML('暂无买点信号');
    return;
  }

  let html = `<div class="table-wrap"><table class="stock-table"><thead><tr>
    <th>代码</th><th>名称</th><th>买入价</th><th>涨幅</th><th>标签</th>
  </tr></thead><tbody>`;
  for (const item of filtered) {
    const riseCls = numClass(item.buy_rise);
    html += `<tr onclick="openDiagnose('${esc(item.code)}')" style="cursor:pointer">
      <td><a class="stock-code">${esc(item.code)}</a></td>
      <td><a class="stock-name-link">${esc(item.name)}</a></td>
      <td>${item.buy_price ? item.buy_price.toFixed(2) : '--'}</td>
      <td class="${riseCls}">${fmtPct(item.buy_rise)}</td>
      <td>${renderTagBadges(item.tags)}</td>
    </tr>`;
  }
  html += `</tbody></table></div>`;
  body.innerHTML = html;
}

// ── 渲染：卖点 ──

function renderSellResults() {
  const body = document.getElementById('sellBody');
  const countEl = document.getElementById('sellCount');
  const filtered = sellResults.filter(matchAll);
  if (countEl) countEl.textContent = filtered.length;

  if (filtered.length === 0) {
    body.innerHTML = emptyHTML('暂无卖点信号');
    return;
  }

  let html = `<div class="table-wrap"><table class="stock-table"><thead><tr>
    <th>代码</th><th>名称</th><th>买入价</th><th>买入时间</th><th>卖出价</th><th>卖出时间</th><th>收益率</th>
  </tr></thead><tbody>`;
  for (const item of filtered) {
    const profitCls = numClass(item.income);
    html += `<tr onclick="openDiagnose('${esc(item.code)}')" style="cursor:pointer">
      <td><a class="stock-code">${esc(item.code)}</a></td>
      <td><a class="stock-name-link">${esc(item.name)}</a></td>
      <td>${item.buy_price ? item.buy_price.toFixed(2) : '--'}</td>
      <td>${item.buy_time ? fmtMDHM(item.buy_time) : '--'}</td>
      <td>${item.sell_price ? item.sell_price.toFixed(2) : '--'}</td>
      <td>${item.sell_time ? fmtMDHM(item.sell_time) : '--'}</td>
      <td class="${profitCls}">${fmtPct(item.income)}</td>
    </tr>`;
  }
  html += `</tbody></table></div>`;
  body.innerHTML = html;
}

// =========================================================
// 历史面板：筛选 + 统计 + 表格
// =========================================================

// 获取当前策略定义的标签 + "无标签"选项
function getAllTags() {
  const st = strategies.find(s => s.key === currentStrategy);
  const defined = (st && st.tags && st.tags.length > 0) ? st.tags.slice().sort((a, b) => a.localeCompare(b, 'zh-CN')) : [];
  return [...defined, NO_TAG_KEY];
}

// 渲染标签筛选按钮
function ensureHistoryTagFilters() {
  const wrap = document.getElementById('historyTagFilters');
  if (!wrap) return;
  const tags = getAllTags();
  if (!historyFilter.tags) {
    historyFilter.tags = new Set(tags);
  } else {
    // 过滤掉已不存在的标签
    historyFilter.tags = new Set(Array.from(historyFilter.tags).filter(t => tags.includes(t)));
  }
  let html = '<span class="filter-label">标签</span>';
  for (const t of tags) {
    const active = historyFilter.tags.has(t) ? ' active' : '';
    const label = t === NO_TAG_KEY ? '无标签' : t;
    html += `<button class="filter-btn${active}" onclick="toggleTag('${esc(t)}')">${esc(label)}</button>`;
  }
  wrap.innerHTML = html;
}

function toggleTag(tag) {
  if (!historyFilter.tags) historyFilter.tags = new Set(getAllTags());
  if (historyFilter.tags.has(tag)) {
    historyFilter.tags.delete(tag);
  } else {
    historyFilter.tags.add(tag);
  }
  ensureHistoryTagFilters();
  renderHistoryResults();
}

function setDaysFilter(days) {
  historyFilter.days = days;
  document.querySelectorAll('[data-days]').forEach(btn => {
    btn.classList.toggle('active', Number(btn.dataset.days) === days);
  });
  renderHistoryResults();
}

function setStatusFilter(status) {
  historyFilter.status = status;
  document.querySelectorAll('[data-status]').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.status === status);
  });
  renderHistoryResults();
}

// 获取筛选后的历史数据
function getFilteredHistory() {
  const nowTs = Date.now();
  const minTs = nowTs - historyFilter.days * 24 * 60 * 60 * 1000;
  const allTags = getAllTags();
  const selectedTags = historyFilter.tags || new Set(allTags);
  const allSelected = allTags.length === 0 || selectedTags.size === allTags.length;

  return historyResults.filter(r => {
    // 策略过滤
    if (!matchStrategy(r)) return false;
    // 搜索过滤
    if (!matchSearch(r)) return false;
    // 日期过滤
    if (toTs(r.buy_time) < minTs) return false;
    // 状态过滤
    if (historyFilter.status === 'sold' && !r.sold) return false;
    if (historyFilter.status === 'holding' && r.sold) return false;
    // 标签过滤
    const tags = (r.tags && r.tags.length) ? r.tags : [NO_TAG_KEY];
    if (!allSelected && !tags.some(t => selectedTags.has(t))) return false;
    return true;
  });
}

// 渲染历史面板(统计+表格)
function renderHistoryResults() {
  const body = document.getElementById('historyBody');
  const countEl = document.getElementById('historyCount');
  const filtered = getFilteredHistory();
  if (countEl) countEl.textContent = filtered.length;

  // 统计
  renderHistoryStats(filtered);

  if (filtered.length === 0) {
    body.innerHTML = emptyHTML(historyResults.length === 0 ? '暂无历史记录' : '当前筛选条件下暂无数据');
    return;
  }

  let html = `<div class="table-wrap"><table class="stock-table"><thead><tr>
    <th>代码</th><th>名称</th><th>买入时间</th><th>买入价</th><th>现价/卖价</th><th>卖出时间</th><th>收益率</th><th>标签</th><th>状态</th>
  </tr></thead><tbody>`;
  for (const item of filtered) {
    const profitCls = numClass(item.income);
    const curr = item.sell_price;
    const buyTime = fmtMDHM(item.buy_time);
    const sellTime = item.sold ? fmtMDHM(item.sell_time) : '--';
    const statusHTML = item.sold
      ? `<span class="tag-sold">已卖出</span>`
      : `<span class="tag-holding">持有中</span>`;
    html += `<tr onclick="openDiagnose('${esc(item.code)}')" style="cursor:pointer">
      <td><a class="stock-code">${esc(item.code)}</a></td>
      <td><a class="stock-name-link">${esc(item.name)}</a></td>
      <td>${buyTime}</td>
      <td>${item.buy_price ? item.buy_price.toFixed(2) : '--'}</td>
      <td>${curr ? curr.toFixed(2) : '--'}</td>
      <td>${sellTime}</td>
      <td class="${profitCls}">${fmtPct(item.income)}</td>
      <td>${renderTagBadges(item.tags)}</td>
      <td>${statusHTML}</td>
    </tr>`;
  }
  html += `</tbody></table></div>`;
  body.innerHTML = html;
}

// 渲染历史统计(胜率/盈亏比/持仓天数 + 按标签分拆)
function renderHistoryStats(results) {
  let totalCnt = 0, winCnt = 0;
  let winSum = 0, lossSum = 0, winN = 0, lossN = 0;
  let holdDaysSum = 0, holdDaysN = 0;
  const tagWinRate = {}; // tag -> {total, win}
  const tagPF = {};      // tag -> {winSum, lossSum, winN, lossN}

  for (const r of results) {
    const rate = r.income || 0;
    totalCnt++;
    const buyTs = toTs(r.buy_time);
    const endTs = r.sold ? toTs(r.sell_time) : Date.now();
    if (buyTs > 0 && endTs >= buyTs) {
      holdDaysSum += (endTs - buyTs) / (24 * 60 * 60 * 1000);
      holdDaysN++;
    }
    if (rate > 0) { winCnt++; winSum += rate; winN++; }
    else if (rate < 0) { lossSum += -rate; lossN++; }

    // 按标签分拆统计
    const tags = (r.tags && r.tags.length) ? r.tags : [NO_TAG_KEY];
    for (const t of tags) {
      if (!tagWinRate[t]) tagWinRate[t] = { total: 0, win: 0 };
      tagWinRate[t].total++;
      if (rate > 0) tagWinRate[t].win++;
      if (!tagPF[t]) tagPF[t] = { winSum: 0, lossSum: 0, winN: 0, lossN: 0 };
      if (rate > 0) { tagPF[t].winSum += rate; tagPF[t].winN++; }
      else if (rate < 0) { tagPF[t].lossSum += -rate; tagPF[t].lossN++; }
    }
  }

  // 总体胜率
  const winRateEl = document.getElementById('statWinRate');
  if (totalCnt > 0) {
    const w = winCnt / totalCnt * 100;
    winRateEl.innerHTML = `<span class="${w >= 50 ? 'num-up' : 'num-down'}">${w.toFixed(1)}%</span> <span class="stat-sub">${winCnt}/${totalCnt}</span>`;
  } else {
    winRateEl.textContent = '-';
  }

  // 标签胜率
  const tagWinEl = document.getElementById('statTagWinRate');
  tagWinEl.innerHTML = renderTagWinRate(tagWinRate);

  // 总体盈亏比
  const pfEl = document.getElementById('statProfitFactor');
  if (winN === 0 && lossN === 0) { pfEl.textContent = '-'; }
  else if (lossN === 0) { pfEl.innerHTML = `<span class="num-up">∞</span>`; }
  else if (winN === 0) { pfEl.innerHTML = `<span class="num-down">0.00</span>`; }
  else {
    const pf = winSum / lossSum;
    pfEl.innerHTML = `<span class="${pf >= 1 ? 'num-up' : 'num-down'}">${pf.toFixed(2)}</span>`;
  }

  // 标签盈亏比
  const tagPFEl = document.getElementById('statTagPF');
  tagPFEl.innerHTML = renderTagPF(tagPF);

  // 持仓天数
  const holdEl = document.getElementById('statHoldDays');
  if (holdDaysN > 0) {
    const d = holdDaysSum / holdDaysN;
    holdEl.innerHTML = `<span class="stat-val">${d >= 10 ? d.toFixed(1) : d.toFixed(2)}天</span>`;
  } else {
    holdEl.textContent = '-';
  }
}

function renderTagWinRate(stats) {
  const names = Object.keys(stats).sort();
  if (names.length === 0) return '';
  return names.map(t => {
    const s = stats[t];
    const w = s.total > 0 ? s.win / s.total * 100 : 0;
    const label = t === NO_TAG_KEY ? '无标签' : t;
    return `<span class="stat-item"><span class="stat-item-tag">${esc(label)}</span><span class="${w >= 50 ? 'num-up' : 'num-down'}">${w.toFixed(1)}%</span> <span class="stat-sub">${s.win}/${s.total}</span></span>`;
  }).join('');
}

function renderTagPF(stats) {
  const names = Object.keys(stats).sort();
  if (names.length === 0) return '';
  return names.map(t => {
    const s = stats[t];
    let val, cls;
    if (s.winN === 0 && s.lossN === 0) { val = '-'; cls = ''; }
    else if (s.lossN === 0) { val = '∞'; cls = 'num-up'; }
    else if (s.winN === 0) { val = '0.00'; cls = 'num-down'; }
    else { val = (s.winSum / s.lossSum).toFixed(2); cls = parseFloat(val) >= 1 ? 'num-up' : 'num-down'; }
    const label = t === NO_TAG_KEY ? '无标签' : t;
    return `<span class="stat-item"><span class="stat-item-tag">${esc(label)}</span><span class="${cls}">${val}</span></span>`;
  }).join('');
}

// ── 辅助渲染 ──

function renderStrategyBadges(arr) {
  if (!arr || arr.length === 0) return '';
  const nameMap = {
    'macd-premium': 'MACD',
    'macd-base': 'Base',
    'boll-rsi': 'BollRSI',
  };
  return arr.map(k => {
    const cls = k.toLowerCase().replace(/[^a-z0-9]/g, '-');
    const name = nameMap[k] || k;
    return `<span class="badge-strategy ${cls}">${esc(name)}</span>`;
  }).join('');
}

function renderTagBadges(arr) {
  if (!arr || arr.length === 0) return '';
  return arr.map(t => `<span class="badge-tag">${esc(t)}</span>`).join('');
}

function emptyHTML(text) {
  return `<div class="empty-state"><div class="icon">○</div><div>${esc(text)}</div></div>`;
}

// ── 初始化 ──

async function init() {
  await loadStrategies();
  await loadHistory();
  connectWS();
}

window.addEventListener('DOMContentLoaded', init);
