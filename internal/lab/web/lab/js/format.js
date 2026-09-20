// 展示层格式化助手：只影响显示，JSON/提交始终用原始值。
// unit 取值见 strategies/factor/registry.go（ratio / multiple / 其他原值）。

// esc HTML 转义（Vue 模板自动转义，仅文本拼接等少数场景使用）。
export const esc = s => String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));

// sig3 3 位有效数字去尾零（0.123→0.123，1234→1230，1e-7→1e-7）。
export const sig3 = v => String(Number(v.toPrecision(3)));

// fmtFactorVal 因子值展示（unit 感知）：ratio ×100 加 %、multiple 加 ×、
// 其余原值。
export function fmtFactorVal(v, unit) {
  if (v == null || !isFinite(v)) return '—';
  if (unit === 'ratio') return sig3(v * 100) + '%';
  if (unit === 'multiple') return sig3(v) + '×';
  return sig3(v);
}

// groupRangeLabel 组的值域标签：分位模式=全样本累计 min~max（历史分布，
// 非固定边界）；bins 模式=固定边界 (lower, upper]，开放端 ±∞；旧数据两者皆无。
export function groupRangeLabel(g, unit) {
  if (g.factorMin != null && g.factorMax != null) {
    return `[${fmtFactorVal(g.factorMin, unit)} ~ ${fmtFactorVal(g.factorMax, unit)}]`;
  }
  if (g.lower != null || g.upper != null) {
    return `(${g.lower == null ? '-∞' : fmtFactorVal(g.lower, unit)}, ${g.upper == null ? '+∞' : fmtFactorVal(g.upper, unit)}]`;
  }
  return '';
}

// groupChartCategories 优先把真实因子值范围作为横轴主标签；只有旧报告缺少
// 数值统计时才回退 Q 编号。边缘组补“低值组/高值组”，不要求用户理解 Q 编号。
export function groupChartCategories(gs, unit, showRange) {
  return gs.map((g, i) => {
    const head = i === 0 ? `${g.label} 最低` : i === gs.length - 1 ? `${g.label} 最高` : g.label;
    const range = showRange ? groupRangeLabel(g, unit) : '';
    if (!range) return head;
    const edge = i === 0 ? '低值组' : i === gs.length - 1 ? '高值组' : '';
    return edge ? range + '\n' + edge : range;
  });
}

// factorAxisNumber / factorAxisLabel 只改变展示单位：ratio 用百分比横轴，
// 报告和策略条件仍保留原始小数；multiple 显示 ×，其他因子保持原值。
export function factorAxisNumber(v, unit) {
  return unit === 'ratio' ? v * 100 : v;
}

export function factorAxisLabel(v, unit) {
  if (v == null || !isFinite(v)) return '—';
  if (unit === 'ratio') return sig3(+v) + '%';
  if (unit === 'multiple') return sig3(+v) + '×';
  return sig3(+v);
}

// ma 简单移动平均（K 线 MA 线）。
export function ma(v, p) {
  return v.map((_, i) => {
    if (i + 1 < p) return null;
    let s = 0;
    for (let j = i - p + 1; j <= i; j++) s += Number(v[j] || 0);
    return Number((s / p).toFixed(3));
  });
}

// —— 06 组合研究格式化（显示层；正式结论一律来自后端） ——
export const pfPct = (v, d) => { if (v == null || !isFinite(v)) return '—'; return (v * 100).toFixed(d == null ? 2 : d) + '%'; };
export const pfNum = (v, d) => { if (v == null || !isFinite(v)) return '—'; return (+v.toFixed(d == null ? 4 : d)); };
export const pfCls = v => v == null || !isFinite(v) || v === 0 ? '' : (v > 0 ? 'pos' : 'neg');
export const pfShort = (s, n) => s ? (s.length > (n || 14) ? s.slice(0, (n || 14)) + '…' : s) : '—';
export const pfShortId = id => id ? id.replace(/^[a-z]{2}_\d{8}T\d{9}Z_/, '') : '';
export const pfErrMsg = e => (e && e.message) || '未知错误';

// —— 因子命名 ——
// 目录 name 是默认参数实例名（如 N日动量(20)）；选择器只展示稳定的因子类型名。
export function factorHasDays(e) {
  return !e.parameterLabel.startsWith('无参数');
}

export function factorTypeName(e) {
  const suffix = `(${e.defaultDays})`;
  return e.name.endsWith(suffix) ? e.name.slice(0, -suffix.length) : e.name;
}

export function factorInstanceName(e, days) {
  return factorHasDays(e) ? `${factorTypeName(e)}(${days})` : e.name;
}

// groupingSummaryText 分组方向摘要（后端 summary → 一句话文案）。
export function groupingSummaryText(summary) {
  const DIR = {
    ascending: '因子值越大，未来收益越高',
    descending: '因子值越大，未来收益越低',
    mixed: '各组收益方向不一致',
    flat: '各组收益接近，因子区分度弱',
    insufficient: '结果不足：分组数据不完整，无方向结论',
  };
  const sum = summary || {};
  let txt = DIR[sum.direction] || sum.direction || '';
  if (sum.monotonic && (sum.direction === 'ascending' || sum.direction === 'descending')) txt += '（单调）';
  if (sum.spread != null && isFinite(sum.spread)) txt += `；最高组−最低组收益差 ${(sum.spread * 100).toFixed(2)}%`;
  return txt;
}

// currentGrouping 取当前展示档的分组结果：有 allGroupings（quantile 新报告）
// 时按 n 查表；否则回退请求档（旧报告/bins）。
export function currentGrouping(rep, n) {
  const all = Array.isArray(rep.allGroupings) && rep.allGroupings.length ? rep.allGroupings : null;
  if (all) {
    const hit = all.find(s => s.groups === n);
    if (hit) {
      return {
        groups: hit.stats && hit.stats.length ? hit.stats : null,
        quintiles: hit.quintiles,
        summary: hit.summary || {},
        n: hit.groups,
      };
    }
    const first = all[0];
    return {
      groups: first.stats && first.stats.length ? first.stats : null,
      quintiles: first.quintiles,
      summary: first.summary || {},
      n: first.groups,
    };
  }
  return {
    groups: Array.isArray(rep.groups) && rep.groups.length ? rep.groups : null,
    quintiles: rep.quintiles,
    summary: rep.summary || {},
    n: (rep.grouping && rep.grouping.groups) || 5,
  };
}

// analysisYearsForChart 返回升序且年份有效的年度结果；旧报告无 years 时为空。
export function analysisYearsForChart(rep) {
  if (!Array.isArray(rep.years)) return [];
  return rep.years.filter(y => Number.isInteger(y.year)).slice().sort((a, b) => a.year - b.year);
}

// groupFactors 按目录类别分组（保持目录首次出现顺序），供 optgroup 渲染；
// 类别是后端目录字段，是因子分类的唯一事实源。
export function groupFactors(catalog) {
  const groups = [];
  const map = new Map();
  (catalog || []).forEach(e => {
    const category = e.category || '其他';
    if (!map.has(category)) {
      const g = {category, entries: []};
      map.set(category, g);
      groups.push(g);
    }
    map.get(category).entries.push(e);
  });
  return groups;
}

// filterText 区间条件的显示文案：API/存储用原始比例，显示层 ×100。
// catalog 为因子目录（state.factorCatalog），用于反查 unit 与真实参数名。
export function filterText(f, catalog) {
  if (!f) return '';
  const fent = (catalog || []).find(x => x.kind === f.kind);
  const unit = fent ? fent.unit : '';
  const fmt = v => v == null ? '—' : (unit === 'ratio' ? sig3(v * 100) + '%' : sig3(v));
  // 因子名显示类型名 + 实际参数（days 是真实分析参数），不用目录默认实例名
  const nm = fent ? factorInstanceName(fent, f.days) : `${f.kind}(${f.days})`;
  if (f.operator === 'gte') return nm + '≥' + fmt(f.min);
  if (f.operator === 'lte') return nm + '≤' + fmt(f.max);
  if (f.operator === 'between') return nm + '∈[' + fmt(f.min) + ',' + fmt(f.max) + ']';
  return nm;
}
