// practice.js：盘感练习前端纯逻辑（文案映射、相对日标签、行情数据投影）。
// 只处理视图与操作状态；成交、预算取整与费用一律以后端持久状态为准，前端不重算。

// —— 文案映射（与后端 practice.go 常量对应） ——

export function practicePhaseLabel(phase) {
  return {preparing: '出题中', active: '进行中', finished: '已结束', failed: '出题失败', cancelled: '已取消'}[phase] || phase;
}

export function practiceStageLabel(stage) {
  return {waiting: '等待买入', holding: '持仓中', sold: '已卖出'}[stage] || '';
}

export function practiceEndReasonLabel(reason) {
  return {
    skipped: '放弃买入，本题结束',
    data_end: '行情已用尽，本题自动结束',
    finished_untraded: '手动结束（未交易）',
    finished_open: '手动结束（持仓按当日收盘估值，未虚构卖出）',
    finished_sold: '手动结束（已卖出）',
  }[reason] || '';
}

export function practiceFailReasonLabel(reason) {
  return {
    no_match: '所选年份范围内没有命中机会，未能出题；可更换策略或年份后重试',
    strategy: '策略读取或求值失败，未能出题',
    data: '本地行情数据不足，未能出题',
    interrupted: '出题被中断，可重新开始',
    internal: '内部错误，未能出题',
  }[reason] || '出题失败';
}

export function practiceActionLabel(action) {
  return {buy: '买入', sell: '卖出'}[action] || action;
}

// 相对信号日的展示标签（盲练不暴露真实日期；负数为信号前背景日）。
export function practiceDayLabel(day) {
  if (day === 0) return '信号日';
  if (day < 0) return `前${-day}日`;
  return `+${day}`;
}

// 命令／请求幂等 ID：优先 crypto.randomUUID，非安全上下文降级为时间戳+随机数。
export function newCommandId() {
  try {
    if (globalThis.crypto && typeof globalThis.crypto.randomUUID === 'function') return globalThis.crypto.randomUUID();
  } catch (e) { /* 非安全上下文可能禁用 crypto，走降级 */ }
  return 'cmd-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 10);
}

// 盈亏着色（红正绿负，与图表配色一致）。
export function pnlClass(v) {
  return v > 0 ? 'pos' : v < 0 ? 'neg' : '';
}

export function fmtMoney(v, signed = false) {
  const n = Number(v);
  return v == null || !Number.isFinite(n) ? '—' : (signed && n > 0 ? '+' : '') + n.toFixed(2);
}

// 百分比展示：后端 eval 的 pct 字段已按 % 口径（×100）输出。
export function fmtPct(v) {
  const value = fmtMoney(v, true);
  return value === '—' ? value : value + '%';
}

// ISO 时间戳 → 本地时区短格式（列表与复盘展示用）。
export function fmtTime(iso) {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const p = n => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

// 盲练只把已揭示的相对日交给共享图表；真实日期只用于结束后的复盘。
export function practiceKlineData({days, markers = [], showDate = false, title = '', identity}) {
  const rows = days.map(({day, date, open, high, low, close, volume, ma5, ma10, ma20, dif, dea, hist}) => ({
    day, ...(showDate && date ? {date} : {}), open, high, low, close, volume, ma5, ma10, ma20, dif, dea, hist,
  }));
  return {rows, markers: markers.map(m => ({day: m.day, price: m.price, kind: m.kind, outside: true})), showDate, title, identity};
}

// 报告和文本下载共用同一套行文；金额、比率与回撤只消费服务端计算结果。
export function practiceReportSections(review) {
  const r = review?.report, e = review?.eval || {};
  if (!r) return [];
  const traded = r.traded, sold = r.status === 'sold';
  const money = v => fmtMoney(v) + ' 元';
  const signed = v => fmtMoney(v, true) + ' 元';
  const na = '不适用（未交易）';
  const pnl = sold ? e.realizedPnl : e.openPnl;
  return [
    {title: '收益与费用', rows: [
      {label: sold ? '已实现净盈亏' : '未平仓浮动盈亏', value: traded ? signed(pnl) : na, cls: traded ? pnlClass(pnl) : ''},
      {label: '交易收益率（买入总成本）', value: traded ? fmtPct(sold ? e.realizedPct : e.openPct) : na, cls: traded ? pnlClass(pnl) : ''},
      {label: '整题资金回报（初始预算）', value: traded ? fmtPct(r.accountPct) : na, cls: traded ? pnlClass(r.accountPct) : ''},
      {label: '实际手续费合计', value: money(r.fees)},
    ]},
    {title: '资金与持仓', rows: [
      {label: '初始预算', value: money(r.budget)},
      {label: '买入总成本（含费）', value: traded ? money(r.buyCost) : na},
      {label: '卖出净收入（扣费）', value: sold ? money(r.sellIncome) : traded ? '未卖出' : na},
      {label: '期末现金 / 持仓市值', value: `${money(r.cash)} / ${money(r.positionValue)}`},
      {label: '期末总资金', value: money(r.equity)},
    ]},
    {title: '持仓过程与风险', rows: [
      {label: '买入前等待 / 持有天数', value: traded ? `${r.waitDays} / ${e.holdingDays} 个交易日` : na},
      {label: '最大收盘浮盈', value: traded ? `${signed(e.maxFloatProfit)}（${fmtPct(e.maxFloatProfitPct)}）` : na, cls: traded ? pnlClass(e.maxFloatProfit) : ''},
      {label: '最大收盘浮亏', value: traded ? `${signed(e.maxFloatLoss)}（${fmtPct(e.maxFloatLossPct)}）` : na, cls: traded ? pnlClass(e.maxFloatLoss) : ''},
      {label: '整题资金最大回撤', value: traded ? `${money(r.maxDrawdown)}（${fmtMoney(r.maxDrawdownPct)}%）` : na},
    ]},
  ];
}

export function practiceReportSummary(review) {
  const r = review?.report;
  if (!r) return '分析报告暂不可用，请刷新页面后重试。';
  if (!r.traded) return '本题未发生买入，不计作一笔零收益成交；现金保留为初始预算。';
  return r.status === 'sold'
    ? '本题已全部卖出，净盈亏已扣除实际买卖手续费；卖出后的观察行情不改变已实现收益。'
    : '本题结束时仍持仓，仅按结束日收盘价估值；浮动盈亏不代表已实现收益，未预扣尚未发生的卖出费用。';
}

export function practiceReportText(review) {
  if (!review?.report) return '';
  const r = review.report;
  const lines = ['盘感练习分析报告', `策略：${review.strategyName}　股票：${review.code}`,
    `报告区间：${r.startDate} 至 ${r.endDate}`, `结束状态：${practiceEndReasonLabel(review.endReason)}`,
    practiceReportSummary(review)];
  for (const section of practiceReportSections(review)) {
    lines.push('', section.title, ...section.rows.map(row => `${row.label}：${row.value}`));
  }
  lines.push('', '成交明细');
  for (const t of review.trades || []) lines.push(`${t.date}　${practiceActionLabel(t.action)}　${fmtMoney(t.price)} 元 × ${t.quantity} 股　含费金额 ${fmtMoney(t.amount)} 元　手续费 ${fmtMoney(t.fee)} 元`);
  if (!review.trades?.length) lines.push('本题未产生成交。');
  lines.push('', '计算口径：总资金=现金+持仓市值。回撤按信号日至结束日的每日收盘总资金计算，包含已发生费用与初始预算。最大回撤金额与百分比各取自身最大值。',
    '报告只使用本题冻结快照及结束前操作，不将额外复盘行情计入收益，也不按事后最高点判定买卖对错。');
  return lines.join('\n');
}
