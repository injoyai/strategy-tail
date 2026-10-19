// practice.js：盘感练习前端纯逻辑（文案映射、相对日标签、图表 option 构建）。
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

// —— 练习 K 线 option 构建（三 grid：日K／成交量／MACD，样式对齐 Tab3Trades） ——

// days: 服务端已揭示日 K（盲练无 date；复盘含 date）。markers: [{day, price, kind}]，
// kind: buy | sell | est。showDate=true（复盘）用真实日期作 x 轴；盲练用相对日标签。
// 返回 {option, missing}：missing 为未能定位的标记（对应日不在已揭示范围）。
export function practiceKlineOption({days, markers = [], showDate = false, title = ''}) {
  const cat = d => (showDate && d.date) ? d.date : practiceDayLabel(d.day);
  const cats = days.map(cat);
  const byDay = new Map(days.map(d => [d.day, d]));
  const marks = markers.map(m => {
    const k = byDay.get(m.day);
    if (!k) return null;
    const isBuy = m.kind === 'buy', est = m.kind === 'est';
    const label = isBuy ? 'B' : est ? '估' : 'S';
    return {name: `${isBuy ? '买入' : est ? '结束估值' : '卖出'} · ${m.price.toFixed(2)} 元`,
      coord: [cat(k), isBuy ? Math.min(k.low, m.price) : Math.max(k.high, m.price)], value: label,
      symbol: est ? 'diamond' : 'triangle', symbolRotate: isBuy ? 0 : 180, symbolSize: 16,
      symbolOffset: [0, isBuy ? 18 : -18],
      itemStyle: {color: est ? '#94a3b8' : isBuy ? '#ef4444' : '#22c55e'},
      label: {show: true, formatter: label, color: '#fff', fontSize: 11, fontWeight: 'bold', offset: [0, isBuy ? 15 : -15]}};
  });
  const missing = markers.filter((m, i) => !marks[i]);
  const values = days.map(d => [d.open, d.close, d.low, d.high]);
  const option = {
    backgroundColor: 'transparent',
    ...(title ? {title: {text: title, left: 12, top: 6, textStyle: {fontSize: 13, color: '#e9eef8'}}} : {}),
    tooltip: {trigger: 'axis', axisPointer: {type: 'cross'}},
    axisPointer: {link: [{xAxisIndex: 'all'}]},
    legend: [
      {top: title ? 26 : 0, right: 8, itemWidth: 16, itemHeight: 10, data: ['日K', 'MA5', 'MA10', 'MA20']},
      {top: '58%', right: 8, itemWidth: 14, itemHeight: 8, data: ['DIF', 'DEA']},
    ],
    dataZoom: [{type: 'inside', xAxisIndex: [0, 1, 2]}, {show: true, xAxisIndex: [0, 1, 2], type: 'slider', bottom: 0, height: 18, showDetail: false}],
    grid: [{left: 52, right: 16, top: title ? 58 : 30, bottom: '56%'},
      {left: 52, right: 16, top: '50%', height: '8%'},
      {left: 52, right: 16, top: '65%', bottom: 44}],
    graphic: [
      {type: 'text', left: 8, top: '46%', style: {text: '成交量', fill: '#c2ccdd', fontSize: 11}},
      {type: 'text', left: 8, top: '60%', style: {text: 'MACD(12,26,9)', fill: '#c2ccdd', fontSize: 11}},
    ],
    xAxis: [
      {type: 'category', data: cats, boundaryGap: false, axisLabel: {show: false}},
      {type: 'category', gridIndex: 1, data: cats, boundaryGap: false, axisLabel: {show: false}},
      {type: 'category', gridIndex: 2, data: cats, boundaryGap: false, axisLabel: {fontSize: 10, hideOverlap: true}},
    ],
    yAxis: [
      {scale: true, splitNumber: 4,
        min: v => v.min - (v.max - v.min || Math.abs(v.min) * 0.01 || 1) * 0.25,
        max: v => v.max + (v.max - v.min || Math.abs(v.max) * 0.01 || 1) * 0.25,
        splitArea: {show: true}},
      {scale: true, gridIndex: 1, splitNumber: 2, axisLabel: {show: false}, splitLine: {show: false}},
      {scale: true, gridIndex: 2, splitNumber: 3,
        // 只对当前可见指标留少量边距，避免自动整刻度把小幅 MACD 压扁。
        min: v => Math.min(0, v.min) - (v.max - v.min || Math.abs(v.max) || 0.01) * 0.1,
        max: v => Math.max(0, v.max) + (v.max - v.min || Math.abs(v.min) || 0.01) * 0.1,
        axisLabel: {fontSize: 11, formatter: v => Number(v.toPrecision(3)).toString()},
        splitLine: {show: true, lineStyle: {color: 'rgba(255,255,255,.08)'}}},
    ],
    series: [
      {name: '日K', type: 'candlestick', data: values,
        itemStyle: {color: '#ef4444', color0: '#22c55e', borderColor: '#ef4444', borderColor0: '#22c55e'},
        markPoint: {data: marks.filter(Boolean)}},
      {name: 'MA5', itemStyle: {color: '#f59e0b'}, type: 'line', data: days.map(d => d.ma5), symbol: 'none', lineStyle: {width: 1, color: '#f59e0b'}},
      {name: 'MA10', itemStyle: {color: '#8b5cf6'}, type: 'line', data: days.map(d => d.ma10), symbol: 'none', lineStyle: {width: 1, color: '#8b5cf6'}},
      {name: 'MA20', itemStyle: {color: '#3b82f6'}, type: 'line', data: days.map(d => d.ma20), symbol: 'none', lineStyle: {width: 1, color: '#3b82f6'}},
      {name: '成交量', type: 'bar', xAxisIndex: 1, yAxisIndex: 1, data: days.map(d => d.volume),
        itemStyle: {color: p => values[p.dataIndex] && values[p.dataIndex][1] >= values[p.dataIndex][0] ? '#ef4444' : '#22c55e'}},
      {name: 'MACD', type: 'bar', xAxisIndex: 2, yAxisIndex: 2, data: days.map(d => d.hist),
        markLine: {silent: true, symbol: 'none', label: {show: false}, lineStyle: {color: '#94a3b8', width: 1}, data: [{yAxis: 0}]},
        itemStyle: {color: p => p.value == null ? '#94a3b8' : p.value >= 0 ? '#ef4444' : '#22c55e'}},
      {name: 'DIF', itemStyle: {color: '#f59e0b'}, type: 'line', xAxisIndex: 2, yAxisIndex: 2, data: days.map(d => d.dif), symbol: 'none', lineStyle: {width: 2, color: '#f59e0b'}},
      {name: 'DEA', itemStyle: {color: '#8b5cf6'}, type: 'line', xAxisIndex: 2, yAxisIndex: 2, data: days.map(d => d.dea), symbol: 'none', lineStyle: {width: 2, color: '#8b5cf6'}},
    ],
  };
  return {option, missing};
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
