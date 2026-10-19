// replay.js — 03 逐日回放会话的纯状态与派生（plan §5）。
// 不依赖 Vue：步进/过滤/状态转换全部在此实现，组件只负责渲染与计时器。
// 单一日期索引（dateIdx）派生全部可见内容；未来日期信息一律截断（R03）。

// 播放速度（ms/步），只改变计时器间隔，不影响数据与状态。
export const SPEEDS = {slow: 1500, mid: 750, fast: 250};

// createSession 回放会话（存入 store.replay，跨页保留）。
// seq/klineSeq 为请求序列号：变体/股票/报告切换时递增，旧响应按序号与
// 会话身份双重比对丢弃。图表实例与计时器不入会话，由组件持有。
export function createSession() {
  return {
    variantIdx: 0,   // 报告原始变体序号（非界面排序位置）
    dateIdx: 0,      // 当前日期索引（全部可见内容的唯一来源）
    playing: false,
    speed: 'mid',
    autoPause: false, // 有成交日自动暂停（默认关）
    data: null,       // ReplayVariant 响应
    loading: false,
    error: '',
    seq: 0,
    code: '',         // 所选个股（''=未选）
    kline: null,      // K 线快照行（保存时旧快照，前端按日期截断）
    klineLoading: false,
    klineError: '',
    klineSeq: 0,
  };
}

export function pause(s) {
  if (s) s.playing = false;
}

// advance 前进一帧：先推进后检查——恢复播放永远先推进到下一交易日，
// “同日恢复不重复暂停”由此天然成立（R08）；推进进末日或命中成交日
// 自动暂停时立即停止（R01）。
export function advance(s) {
  const n = s?.data?.days?.length ?? 0;
  if (!n || !s.playing || s.loading || s.klineLoading || s.error || s.klineError) {
    if (s) s.playing = false;
    return;
  }
  if (s.dateIdx >= n - 1) {
    s.playing = false;
    return;
  }
  s.dateIdx++;
  const atEnd = s.dateIdx >= n - 1;
  if (atEnd || (s.autoPause && (s.data.days[s.dateIdx].trades?.length ?? 0) > 0)) {
    s.playing = false;
  }
}

// stepTo 跳转到指定日期：clamp 到有效区间并暂停（R02）。
export function stepTo(s, idx) {
  const n = s?.data?.days?.length ?? 0;
  if (!n) return;
  s.playing = false;
  s.dateIdx = Math.min(Math.max(0, idx), n - 1);
}

export function stepPrev(s) {
  stepTo(s, (s?.dateIdx ?? 0) - 1);
}

export function stepNext(s) {
  stepTo(s, (s?.dateIdx ?? 0) + 1);
}

// play 开始播放：无数据、末日、已在播放时为无操作。
export function play(s) {
  const n = s?.data?.days?.length ?? 0;
  if (!n || s.playing || s.loading || s.klineLoading || s.error || s.klineError || s.dateIdx >= n - 1) return;
  s.playing = true;
}

export function setSpeed(s, key) {
  if (s && SPEEDS[key]) s.speed = key;
}

export function setAutoPause(s, on) {
  if (s) s.autoPause = !!on;
}

// switchVariant 切换变体：暂停、保留日期索引、清数据与个股、递增序列号
// 使在途旧响应失效（R10）。
export function switchVariant(s, idx) {
  if (!s || idx === s.variantIdx) return;
  s.variantIdx = idx;
  s.playing = false;
  s.data = null;
  s.loading = false;
  s.error = '';
  s.code = '';
  s.kline = null;
  s.klineLoading = false;
  s.klineError = '';
  s.seq++;
  s.klineSeq++;
}

// selectStock 选择个股：暂停（R09）；代码未变化时不清除已加载数据，
// 当日其他成交的展示不受影响。
export function selectStock(s, code) {
  if (!s) return;
  s.playing = false;
  if (code === s.code) return;
  s.code = code;
  s.kline = null;
  s.klineLoading = false;
  s.klineError = '';
  s.klineSeq++;
}

// resetSession 报告切换时整体重置：新会话身份使全部在途响应失效，
// 日期回到首日、退出回放视图（R10）。
export function resetSession() {
  return createSession();
}

// —— 以下为 dateIdx 派生（R03：任何可见内容不得泄漏未来日期） ——

// visibleDays 截至 dateIdx 的每日帧。
export function visibleDays(s) {
  const days = s?.data?.days;
  if (!days?.length) return [];
  return days.slice(0, s.dateIdx + 1);
}

export function dayTrades(s) {
  return s?.data?.days?.[s?.dateIdx]?.trades ?? [];
}

// visibleTradesFor 个股截至 dateIdx 的全部成交（含日期），已卖出可回看。
export function visibleTradesFor(s, code) {
  const out = [];
  const days = s?.data?.days;
  if (!days) return out;
  const end = Math.min(s.dateIdx, days.length - 1);
  for (let i = 0; i <= end; i++) {
    for (const t of days[i].trades ?? []) {
      if (t.code === code) out.push({...t, date: days[i].date});
    }
  }
  return out;
}

// equitySeries 完整日期轴 + 截至 dateIdx 的累计收益%；
// 未来索引为 null（图上空白，不揭示最终收益）。初始资金 = 首日权益 − 首日盈亏
// （采集合同：首日 DayPnL = equity − 初始资金）；无法确定时不伪造数值。
export function equitySeries(s) {
  const data = s?.data;
  const days = data?.days;
  const dates = data?.dates ?? [];
  if (!dates.length || !days?.length) return {dates: [], values: [], initial: null};
  const initial = days[0].equity - days[0].dayPnl;
  const valid = Number.isFinite(initial) && initial > 0;
  const values = dates.map((_, i) => {
    if (i > s.dateIdx) return null;
    const d = days[i];
    if (!d || !valid || !Number.isFinite(d.equity)) return null;
    return Number(((d.equity / initial - 1) * 100).toFixed(3));
  });
  return {dates, values, initial: valid ? initial : null};
}

// klineVisible K 线快照按当前日期截断（row.date <= 当前日）；
// 快照自带的前导预热段不受影响。curDate 为空（数据未就绪）时返回空。
export function klineVisible(rows, curDate) {
  if (!rows?.length || !curDate) return [];
  return rows.filter(r => r.date <= curDate);
}

// reasonLines 成交原因（R 路径）翻译为规则描述；未登记的路径原样展示，
// 不伪造规则名。Reason 为真实成功路径的节点序列。
export function reasonLines(trade, rules) {
  const list = trade?.reason ?? [];
  if (!list.length) return [];
  const byPath = new Map((rules ?? []).map(r => [r.path, r]));
  return list.map(p => {
    const r = byPath.get(p);
    return r ? [...new Set([r.name, r.desc].filter(Boolean))].join('：') || p : p;
  });
}
