import { fmtMoney, fmtPct, fmtTime } from './practice.js';

export const trainingResultLabel = status => ({sold: '已平仓', open: '未平仓', untraded: '未交易'}[status] || status);
export function trainingMetrics(r) {
  return [
    {label: '已实现净盈亏', value: fmtMoney(r.realizedPnl, true) + ' 元', pnl: r.realizedPnl},
    {label: '已平仓胜率', value: r.winRate == null ? '不适用' : r.winRate.toFixed(2) + '%'},
    {label: '平均交易收益率', value: r.averageReturn == null ? '不适用' : fmtPct(r.averageReturn), pnl: r.averageReturn},
    {label: '实际手续费', value: fmtMoney(r.fees) + ' 元'},
    {label: '未平仓估值', value: fmtMoney(r.positionValue) + ' 元'},
    {label: '未平仓浮动盈亏', value: fmtMoney(r.openPnl, true) + ' 元', pnl: r.openPnl},
  ];
}
export function trainingReportText(r) {
  const lines = ['训练分析报告', `训练：${r.id}`, `题库：${r.bankId}`,
    `策略：${r.strategyName}（v${r.strategyVersion}） · ${r.fromYear}–${r.toYear}`,
    `开始：${fmtTime(r.startedAt)} · 结束：${fmtTime(r.endedAt)}`,
    `题库 ${r.bankTotal} 题 · 本次练习 ${r.practiced} 题 · 已平仓 ${r.closed} · 未交易 ${r.untraded} · 未平仓 ${r.open}`,
    `盈利 ${r.wins} · 亏损 ${r.losses} · 打平 ${r.breakeven}`,
    ...trainingMetrics(r).map(x => `${x.label}：${x.value}`),
    '口径：每题独立 20000 元。胜率、平均交易收益率仅含真实已平仓题（含打平）。实际手续费包含未平仓买入费，已计入盈亏，不重复扣除。未平仓不虚构卖出。不统计重练、历史单题或其他训练。', '', '逐题结果'];
  r.questions.forEach((q, i) => lines.push(`${i + 1}. ${q.code} · 信号 ${q.signalDate} · 结束 ${q.endDate} · ${trainingResultLabel(q.status)} · 买入支出 ${fmtMoney(q.buyCost)} 元 · 卖出收入 ${fmtMoney(q.sellIncome)} 元 · 已实现 ${fmtMoney(q.realizedPnl, true)} 元 · 未平仓估值 ${fmtMoney(q.positionValue)} 元 · 浮动 ${fmtMoney(q.openPnl, true)} 元 · 收益率 ${q.returnPct == null ? '不适用' : fmtPct(q.returnPct)} · 实际费用 ${fmtMoney(q.fees)} 元 · 持仓 ${q.holdingDays} 个交易日`));
  if (r.coverage) lines.push('', `覆盖：股票 ${r.coverage.codes} · 有数据 ${r.coverage.withData} · 成功求值 ${r.coverage.succeeded} · 失败 ${r.coverage.failed} · 命中 ${r.coverage.hits}`, '完整匹配仅指成功读取、求值的数据范围。');
  return lines.join('\n');
}
