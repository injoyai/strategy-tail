// Tab4Factor：01 因子研究（概念卡 / 分析提交 / 分组收益图 / 年度稳定性 / 统计详情）。
// 从 index.html 迁移：loadFactors/renderConcept/renderFactorParameter/runAnalysis/
// analysisYears/renderAnalysis/renderGroupChart/renderAnnualGroupChart/
// renderGroupDetail/renderYearStability/renderRangeMeta/renderICChart/renderCoverage/
// addFactorToStrategy。
// 原命令式 renderAnalysis 拆为响应式 computed + watch 渲染图表；跨页“添加到策略条件”
// 通过 store.simpleConds + focusCondReq 与 02 页联动。
import { watch } from '../vendor/vue.esm-browser.prod.js';
import { state } from '../store.js';
import { Api } from '../api.js';
import { buildConfig } from '../config.js';
import {
  factorTypeName, factorInstanceName, factorHasDays, groupFactors,
  fmtFactorVal, groupRangeLabel, groupChartCategories,
  factorAxisNumber, factorAxisLabel,
  currentGrouping, analysisYearsForChart, groupingSummaryText,
} from '../format.js';
import { mountChart, dropChart } from '../echarts.js';
import { animateFactorResult } from '../motion.js';

export default {
  name: 'Tab4Factor',
  inject: ['switchTab', 'startTask', 'openCandidateDlg'],
  setup() {
    return { state, factorTypeName, factorHasDays, groupRangeLabel, fmtFactorVal };
  },
  data() {
    return {
      factorKind: '',        // 当前因子 kind
      factorDays: 20,        // 回看天数（无参数因子忽略）
      factorWindow: 1,       // 未来收益窗口 1-60 交易日
      factorStartYear: 2018, // 本页独立分析区间，不影响策略回测页
      factorEndYear: 2026,
      statOpen: false,       // 统计详情 <details> 展开态（展开后才 init IC 图）
      quintileC: null,
      icC: null,
    };
  },
  computed: {
    factorGroups() { return groupFactors(state.factorCatalog); },
    curFactor() { return state.factorCatalog.find(e => e.kind === this.factorKind) || null; },
    hasDays() { return !!this.curFactor && factorHasDays(this.curFactor); },
    factorDaysLabel() { return (this.curFactor && this.curFactor.parameterLabel) || '回看天数'; },
    fcName() {
      const e = this.curFactor;
      if (!e) return '';
      if (!factorHasDays(e)) return e.name;
      const days = +this.factorDays > 0 ? +this.factorDays : e.defaultDays;
      return factorInstanceName(e, days);
    },
    fcCat() { return this.curFactor ? this.curFactor.category : ''; },
    fcDesc() { return this.curFactor ? '定义：' + this.curFactor.description : ''; },
    fcExample() { return this.curFactor ? '原始值示例：' + this.curFactor.example : ''; },
    fcParam() {
      const e = this.curFactor;
      if (!e) return '';
      if (!factorHasDays(e)) return '参数：' + e.parameterLabel;
      const days = +this.factorDays > 0 ? +this.factorDays : e.defaultDays;
      return `参数（${e.parameterLabel}）：当前 ${days} 日，默认 ${e.defaultDays} 日`;
    },
    // —— 结果区 ——
    rep() { return state.currentAnalysis; },
    grp() {
      return state.currentAnalysis ? currentGrouping(state.currentAnalysis, state.currentGroupingN) : null;
    },
    unit() { return (state.currentAnalysis && state.currentAnalysis.factor) ? state.currentAnalysis.factor.unit : ''; },
    hasAllG() {
      return !!(state.currentAnalysis && Array.isArray(state.currentAnalysis.allGroupings) && state.currentAnalysis.allGroupings.length);
    },
    isBins() {
      return !!(state.currentAnalysis && state.currentAnalysis.grouping && state.currentAnalysis.grouping.mode === 'bins');
    },
    groupOptions() { const a = []; for (let g = 2; g <= 20; g++) a.push(g); return a; },
    groupCountVisible() { return this.hasAllG && !this.isBins; },
    chartYears() { return analysisYearsForChart(state.currentAnalysis); },
    scopeOptions() {
      const opts = [{value: 'overall', label: '全区间汇总'}];
      const years = this.chartYears;
      if (years.length) {
        opts.push({value: 'annual', label: '年度对比'});
        years.forEach(y => opts.push({value: 'year:' + y.year, label: y.year + ' 年'}));
      }
      return opts;
    },
    groupSwitchVisible() { return this.groupCountVisible || this.chartYears.length > 0; },
    groupSwitchHint() {
      return this.chartYears.length
        ? '全区间、年度对比和单年结果均使用同一批逐日观测，切换无需重新分析。'
        : '分析已完成，切换即时生效，无需重新分析。';
    },
    // 当前档分组结果（柱状图/明细表/摘要/年度共用同一档）
    overallGs() {
      const g = this.grp;
      if (!g) return null;
      const real = g.groups && g.groups.length ? g.groups : null;
      const legacyGs = Array.isArray(g.quintiles) && g.quintiles.length >= 2
        ? g.quintiles.map((v, i) => ({label: 'Q' + (i + 1), forwardReturn: v})) : null;
      return (real && real.length ? real : legacyGs) || null;
    },
    nGroups() {
      const g = this.grp;
      if (!g) return 5;
      const real = g.groups && g.groups.length ? g.groups : null;
      return this.overallGs ? this.overallGs.length : (real ? real.length : g.n);
    },
    annualSeries() {
      const years = this.chartYears;
      const nGroups = this.nGroups;
      if (!this.overallGs || !this.overallGs.length) return [];
      return years.map(y => {
        const yg = currentGrouping(y, state.currentGroupingN);
        const ygs = Array.isArray(yg.groups) && yg.groups.length === nGroups ? yg.groups : null;
        const complete = ygs && ygs.every(g =>
          typeof g.factorMean === 'number' && isFinite(g.factorMean) &&
          typeof g.forwardReturn === 'number' && isFinite(g.forwardReturn));
        return complete ? {year: y.year, groups: ygs} : null;
      }).filter(Boolean);
    },
    chartScopeResult() {
      const scope = state.currentChartScope;
      const summary = groupingSummaryText(this.grp ? this.grp.summary : {});
      if (scope === 'annual') {
        const arr = this.annualSeries;
        return {
          gs: this.overallGs,
          title: '全区间',
          summary: arr.length
            ? `年度对比：${arr.length} 个年份具备完整分组收益；横轴为各年每组的真实因子均值。`
            : '年度对比：当前报告未保存年度因子值，或年度有效样本不足；请重新运行分析。',
        };
      }
      if (scope.startsWith('year:')) {
        const year = +scope.slice(5);
        const yr = this.chartYears.find(y => y.year === year);
        const yg = yr ? currentGrouping(yr, state.currentGroupingN) : null;
        const gs = yg && Array.isArray(yg.groups) && yg.groups.length === this.nGroups ? yg.groups : null;
        return {
          gs,
          title: year + ' 年',
          summary: gs ? `${year} 年：${groupingSummaryText(yg.summary)}`
            : `${year} 年：当前报告未保存年度因子值，请重新运行分析。`,
        };
      }
      return {gs: this.overallGs, title: '全区间', summary};
    },
    chartGs() { return this.chartScopeResult.gs; },
    chartTitle() { return this.chartScopeResult.title; },
    quintileSummary() { return this.chartScopeResult.summary; },
    hasChart() {
      const scope = state.currentChartScope;
      const r = this.chartScopeResult;
      if (scope === 'annual') return !!(this.annualSeries.length && this.overallGs && this.overallGs.length);
      return !!(r.gs && r.gs.length && r.gs.every(g => typeof g.forwardReturn === 'number' && isFinite(g.forwardReturn)));
    },
    quintileNA() {
      const scope = state.currentChartScope;
      if (scope === 'annual') return '结果不足：当前报告没有可用于数值横轴的年度分组统计，请重新运行分析。';
      if (scope.startsWith('year:')) return '结果不足：当前报告没有该年的因子值范围，请重新运行分析。';
      return `结果不足：有效样本过少，无法把样本分成 ${this.nGroups} 组并计算分组收益。`;
    },
    // 组内值域/中位数/样本占比仅在全区间报告中保存
    showGroupDetail() {
      const g = this.grp;
      return state.currentChartScope === 'overall' && !!(g && g.groups && g.groups.length);
    },
    detailGroups() { const g = this.grp; return g && g.groups && g.groups.length ? g.groups : []; },
    covText() {
      const c = state.currentAnalysis && state.currentAnalysis.coverage;
      return c ? `样本覆盖：请求 ${c.requested} · 完成 ${c.completed} · 跳过 ${c.skipped}` : '';
    },
    skipped() {
      const c = state.currentAnalysis && state.currentAnalysis.coverage;
      return c ? (c.skipped || 0) : 0;
    },
    failures() {
      const c = state.currentAnalysis && state.currentAnalysis.coverage;
      return Array.isArray(c && c.failures) ? c.failures : [];
    },
    rangeMeta() {
      const rep = state.currentAnalysis;
      if (!rep) return [];
      const items = [];
      const r = rep.range || {};
      items.push({label: '分析区间：', value: (r.startYear > 0 && r.endYear > 0) ? `${r.startYear}–${r.endYear}` : '历史报告未记录'});
      items.push({label: '实际有效日期：', value: (rep.firstDataDate && rep.lastDataDate) ? `${rep.firstDataDate}–${rep.lastDataDate}` : '历史报告未记录'});
      if (rep.window > 0) items.push({label: '未来收益窗口：', value: `${rep.window} 个交易日`});
      const gp = rep.grouping || {};
      const g = this.grp;
      const gn = (g && g.groups && g.groups.length) ? g.groups.length : (g ? (g.n || gp.groups || 5) : 5);
      items.push({label: '分组：', value: gp.mode === 'bins' ? '固定区间' : `${gn} 组等频`});
      const MODE = {all: '全部股票', random: '随机抽样', codes: '指定代码'};
      if (r.sampleMode) {
        items.push({label: '样本：', value: (MODE[r.sampleMode] || r.sampleMode) + (r.sampleSize > 0 ? ` · ${r.sampleSize} 只` : '')});
      }
      return items;
    },
    hasYears() { return this.chartYears.length > 0; },
    yearNA() {
      const rep = state.currentAnalysis;
      return Array.isArray(rep && rep.years)
        ? '本次分析没有可用的年度观测（有效交易日不足或全部年份数据缺失）。'
        : '历史报告未记录年度拆分，仅展示全区间结果。';
    },
    edgeLabels() {
      const g = this.grp;
      const gs = g && g.groups;
      const nG = gs && gs.length ? gs.length : (g ? g.n : 5);
      const isBins = this.isBins;
      const first = gs && gs.length ? gs[0].label : (isBins ? 'B1' : 'Q1');
      const last = gs && gs.length ? gs[gs.length - 1].label : (isBins ? 'B' + nG : 'Q' + nG);
      return {first, last, spread: `${last}-${first}`};
    },
    yearRows() {
      const rep = state.currentAnalysis;
      const years = Array.isArray(rep && rep.years) ? rep.years : [];
      const DIR = {ascending: '正向', descending: '反向', mixed: '不一致', flat: '无区分', insufficient: '样本不足'};
      return years.map(y => {
        const st = y.stats || {};
        const ok = st.pairs >= 10; // 与后端 minPairs 同口径
        const yGrp = currentGrouping(y, state.currentGroupingN);
        const qs = Array.isArray(yGrp.quintiles) && yGrp.quintiles.length >= 2 ? yGrp.quintiles : null;
        const pct = v => (typeof v === 'number' && isFinite(v))
          ? {txt: (v * 100).toFixed(2) + '%', cls: v > 0 ? 'pos' : (v < 0 ? 'neg' : '')}
          : {txt: '样本不足', cls: 'na'};
        let cells;
        if (!ok) {
          cells = [0, 1, 2, 3, 4, 5].map(() => ({txt: '样本不足', cls: 'na'}));
        } else {
          const q1 = pct(qs && qs[0]);
          const q5 = pct(qs && qs[qs.length - 1]);
          const sp = pct(qs && qs[qs.length - 1] - qs[0]);
          cells = [
            {txt: st.mean.toFixed(4), cls: ''},
            {txt: st.std.toFixed(4), cls: ''},
            {txt: st.tStat.toFixed(2), cls: ''},
            q1, q5, sp,
          ];
        }
        const dir = (yGrp.summary || {}).direction;
        return {year: y.year, pairs: st.pairs || 0, cells, dir: DIR[dir] || dir || '样本不足', dirNa: !DIR[dir]};
      });
    },
    icStats() {
      const rep = state.currentAnalysis;
      if (!rep || !rep.stats) return '';
      const s = rep.stats;
      return `有效日 ${s.pairs} · IC 均值 ${s.mean.toFixed(4)} · 标准差 ${s.std.toFixed(4)} · t 值 ${s.tStat.toFixed(2)}`;
    },
    candidateSaveOk() {
      const rep = state.currentAnalysis;
      return !!(rep && rep.analysisVersion >= 3 && rep.analysisId &&
        rep.factor && rep.factor.implementationVersion > 0);
    },
    candidateSaveHint() {
      return this.candidateSaveOk
        ? ''
        : '该报告为旧版本（缺少 analysisId/实现版本），无法保存为候选，请重新运行分析。';
    },
  },
  mounted() {
    this.loadFactors();
    watch(() => this.factorKind, () => {
      const e = this.curFactor;
      if (e) this.factorDays = e.defaultDays;
    });
    // 分析完成/重新载入：修正图表视角后整体渲染结果区
    watch(() => state.currentAnalysis, () => {
      if (!state.currentAnalysis) return;
      if (!this.scopeOptions.some(o => o.value === state.currentChartScope)) state.currentChartScope = 'overall';
      this.renderFactorResult();
    });
    // 分组数/图表视角切换：预计算结果直接换档渲染，无需重新分析
    watch(() => [state.currentChartScope, state.currentGroupingN], () => this.renderFactorResult());
    // 统计详情展开后容器可见才 init IC 图
    watch(() => this.statOpen, open => {
      if (open && state.currentAnalysis) this.$nextTick(() => this.renderICChart());
    });
  },
  unmounted() {
    dropChart(this.quintileC);
    dropChart(this.icC);
  },
  methods: {
    async loadFactors() {
      try {
        state.factorCatalog = await Api.factors();
        if (!this.factorKind && state.factorCatalog.length) {
          const first = state.factorCatalog[0];
          this.factorKind = first.kind;
          this.factorDays = first.defaultDays;
        }
      } catch (e) { state.factorMsg = {text: e.message, ok: false}; }
    },
    // analysisYears 前端即时校验（与后端 validateYears 同口径）
    analysisYears() {
      const sy = +this.factorStartYear, ey = +this.factorEndYear;
      const now = new Date().getFullYear();
      if (!Number.isInteger(sy) || !Number.isInteger(ey) || sy <= 0 || ey <= 0) {
        return {err: '开始年份与结束年份必须为正整数'};
      }
      if (sy > ey) return {err: '时间范围无效：开始年份不能大于结束年份'};
      if (ey > now) return {err: `结束年份不能超过当前年份（${now}）`};
      return {startYear: sy, endYear: ey};
    },
    async runAnalysis() {
      state.factorMsg = {text: '', ok: true};
      const yr = this.analysisYears();
      if (yr.err) { state.factorMsg = {text: yr.err, ok: false}; return; }
      const win = +this.factorWindow;
      if (!Number.isInteger(win) || win < 1 || win > 60) {
        state.factorMsg = {text: '未来收益窗口无效：应为 1-60 交易日', ok: false}; return;
      }
      try {
        const cfg = buildConfig(); // 复用 02 样本配置
        cfg.startYear = yr.startYear; // 仅覆盖本次请求，不回写 02 回测年份
        cfg.endYear = yr.endYear;
        cfg.kind = this.factorKind;
        cfg.days = +this.factorDays;
        cfg.window = +this.factorWindow;
        cfg.grouping = {mode: 'quantile', groups: 5}; // 后端 quantile 始终预算全档
        await Api.analyze(cfg);
        this.startTask('analysis');
      } catch (e) { state.factorMsg = {text: e.message, ok: false}; }
    },
    // “添加到策略条件”：只传 kind/days/版本，不猜 min/max；重复添加聚焦原条件
    addFactorToStrategy() {
      const e = this.curFactor;
      if (!e) return;
      const days = +this.factorDays || e.defaultDays;
      const dup = state.simpleConds.some(c => c.kind === e.kind && c.days === days);
      if (!dup) {
        state.simpleConds.push({kind: e.kind, days, operator: 'between', min: null, max: null,
          factorVersion: e.implementationVersion || 0, hl: false, nb: false, err: '', errField: ''});
      }
      this.switchTab(1);
      state.focusCondReq = {kind: e.kind, days}; // 约定：先切页再写请求
      state.factorMsg = {text: `已${dup ? '定位到已有条件' : '添加条件'}：${factorInstanceName(e, days)}，请在“简单配置”中填写阈值。`, ok: true};
    },
    // 结果区整体渲染：分组图 + 结果揭示动画（面板可见后执行）
    renderFactorResult() {
      this.$nextTick(() => {
        if (!state.currentAnalysis) return;
        if (!this.scopeOptions.some(o => o.value === state.currentChartScope)) { state.currentChartScope = 'overall'; return; }
        if (this.hasChart) {
          if (state.currentChartScope === 'annual') this.renderAnnualGroupChart(this.unit, this.annualSeries, this.isBins);
          else this.renderGroupChart(this.chartGs, this.unit, this.chartTitle, true, this.isBins);
        }
        animateFactorResult(this.$refs.factorBody);
      });
    },
    renderGroupChart(gs, unit, scopeLabel, showRange, isBins) {
      const el = this.$refs.quintileChart;
      if (!el) return;
      if (!this.quintileC) { this.quintileC = mountChart(el); if (!this.quintileC) return; }
      else this.quintileC.resize();
      const cats = groupChartCategories(gs, unit, showRange);
      this.quintileC.setOption({
        backgroundColor: 'transparent',
        aria: {enabled: true},
        title: {text: `${gs.length} 组平均收益%（${scopeLabel} · ${isBins ? '固定区间' : '等频分组'}）`, left: 10, top: 6, textStyle: {fontSize: 13, color: '#e9eef8'}},
        tooltip: {trigger: 'axis', valueFormatter: v => v + '%'},
        grid: {left: 60, right: 20, top: 50, bottom: 56},
        xAxis: {type: 'category', data: cats, axisLabel: {interval: 0, fontSize: 10}},
        yAxis: {type: 'value', axisLabel: {formatter: '{value}%'}},
        series: [{
          type: 'bar',
          data: gs.map(g => +(g.forwardReturn * 100).toFixed(3)), // 显示乘 100；本页不提交该值
          itemStyle: {color: p => p.value >= 0 ? '#ef4444' : '#22c55e'},
          label: {show: true, position: 'top', fontSize: 11, formatter: p => p.value + '%'},
        }],
      }, true);
    },
    // 年度对比使用连续数值横轴：每个点的 x 是该年该组真实因子均值，y 是未来收益
    renderAnnualGroupChart(unit, annualSeries, isBins) {
      const el = this.$refs.quintileChart;
      if (!el) return;
      if (!this.quintileC) { this.quintileC = mountChart(el); if (!this.quintileC) return; }
      else this.quintileC.resize();
      const nGroups = annualSeries[0].groups.length;
      this.quintileC.setOption({
        backgroundColor: 'transparent',
        aria: {enabled: true},
        title: {text: `${nGroups} 组平均收益%（年度对比 · ${isBins ? '固定区间' : '等频分组'}）`, left: 10, top: 6, textStyle: {fontSize: 13, color: '#e9eef8'}},
        legend: {type: 'scroll', top: 32, left: 10, right: 10, textStyle: {color: '#c2ccdd', fontSize: 11}, pageTextStyle: {color: '#c2ccdd'}},
        tooltip: {trigger: 'item', formatter: p => {
          const d = p.data;
          return `${p.marker}${p.seriesName} · ${d.groupLabel}<br>` +
            `因子范围 ${d.range}<br>组均值 ${d.mean}<br>未来收益 ${d.value[1]}%`;
        }},
        grid: {left: 66, right: 24, top: 78, bottom: 58},
        xAxis: {
          type: 'value', name: unit === 'ratio' ? '因子值（%）' : (unit === 'multiple' ? '因子值（×）' : '因子值'),
          nameLocation: 'middle', nameGap: 34,
          axisLabel: {fontSize: 10, formatter: v => factorAxisLabel(v, unit)},
        },
        yAxis: {type: 'value', axisLabel: {formatter: '{value}%'}},
        series: annualSeries.map((s, i) => ({
          name: String(s.year), type: 'line', symbol: 'circle', symbolSize: 6,
          data: s.groups.map(g => ({
            value: [factorAxisNumber(g.factorMean, unit), +(g.forwardReturn * 100).toFixed(3)],
            groupLabel: g.label, range: groupRangeLabel(g, unit), mean: fmtFactorVal(g.factorMean, unit),
          })),
          lineStyle: {width: 2, type: i % 3 === 1 ? 'dashed' : (i % 3 === 2 ? 'dotted' : 'solid')},
          itemStyle: {color: ['#d8b45a','#4c8dff','#a78bfa','#38bdf8','#f59e0b','#f472b6','#94a3b8','#2dd4bf','#818cf8','#c084fc','#67e8f9'][i % 11]},
          emphasis: {focus: 'series'},
          markLine: i === 0 ? {silent: true, symbol: 'none', lineStyle: {color: 'rgba(255,255,255,.22)', type: 'dashed'}, data: [{yAxis: 0}]} : undefined,
        })),
      }, true);
    },
    renderICChart() {
      const rep = state.currentAnalysis;
      if (!rep) return;
      const el = this.$refs.icChart;
      if (!el) return;
      if (!this.icC) { this.icC = mountChart(el); if (!this.icC) return; }
      const days = rep.daily.map(d => d.date);
      const ics = rep.daily.map(d => d.ic);
      this.icC.setOption({
        backgroundColor: 'transparent',
        title: {text: '逐日 IC（Spearman 秩相关）', left: 10, top: 6, textStyle: {fontSize: 13, color: '#e9eef8'}},
        tooltip: {trigger: 'axis'},
        grid: {left: 60, right: 20, top: 50, bottom: 60},
        xAxis: {type: 'category', data: days},
        yAxis: {type: 'value', min: -1, max: 1},
        series: [{
          name: 'IC', type: 'line', data: ics, symbol: 'none',
          connectNulls: true, // 无效日（null）跨过补线，与导出 report.html 口径一致
          markLine: {silent: true, symbol: 'none', lineStyle: {type: 'dashed'}, data: [{yAxis: 0}]},
        }],
      }, true);
    },
  },
  template: `
<div class="card">
  <h3>因子研究</h3>
  <div class="form-row factor-controls">
    <label for="factorKind">因子</label>
    <select id="factorKind" style="min-width:220px" v-model="factorKind" aria-describedby="factorParamHelp">
      <optgroup v-for="g in factorGroups" :key="g.category" :label="g.category">
        <option v-for="e in g.entries" :key="e.kind" :value="e.kind">{{ factorTypeName(e) }}</option>
      </optgroup>
    </select>
    <label class="factor-inline-label" v-show="hasDays" for="factorDays">{{ factorDaysLabel }}</label>
    <input type="number" id="factorDays" v-show="hasDays" :disabled="!hasDays" v-model.number="factorDays" min="1" style="width:80px" aria-describedby="factorParamHelp">
    <label class="factor-inline-label" for="factorWindow">未来收益窗口</label>
    <input type="number" v-model.number="factorWindow" min="1" max="60" step="1" style="width:80px" aria-describedby="factorParamHelp">
    <label class="factor-inline-label" for="factorStartYear">开始年份</label>
    <input type="number" v-model.number="factorStartYear" min="2000" step="1" style="width:80px" aria-describedby="factorParamHelp">
    <label class="factor-inline-label" for="factorEndYear">结束年份</label>
    <input type="number" v-model.number="factorEndYear" min="2000" step="1" style="width:80px" aria-describedby="factorParamHelp">
  </div>
  <div class="hint factor-control-help" id="factorParamHelp">因子参数决定历史计算区间；未来收益窗口只用于评估因子之后的表现。开始/结束年份是本页独立的分析区间，不影响策略回测页。</div>
  <div class="editor-actions">
    <button class="primary" @click="runAnalysis" :disabled="state.taskRunning" :aria-busy="String(state.taskRunning)">开始分析</button>
  </div>
  <div class="msg" :class="state.factorMsg.ok ? 'ok' : 'err'" role="status" aria-live="polite">{{ state.factorMsg.text }}</div>

  <div class="concept" id="factorConcept" v-if="curFactor">
    <div class="concept-head"><span id="fcName">{{ fcName }}</span><span class="badge" id="fcCat">{{ fcCat }}</span></div>
    <div class="concept-line" id="fcDesc">{{ fcDesc }}</div>
    <div class="concept-line" id="fcParam">{{ fcParam }}</div>
    <div class="concept-line" id="fcExample">{{ fcExample }}</div>
    <div class="hint">因子不存在普遍有效的阈值：同一参数在不同市场、样本与周期下表现不同，请以本次样本的分组分布与统计显著性为准。</div>
  </div>

  <div v-if="state.currentAnalysis" ref="factorBody">
    <div class="coverage" id="rangeMeta" style="margin:14px 0 0">
      <span v-for="m in rangeMeta" :key="m.label">{{ m.label }}<b>{{ m.value }}</b></span>
    </div>
    <div class="filters" v-show="groupSwitchVisible" style="padding:10px 13px;margin-top:12px">
      <span v-show="groupCountVisible" style="display:contents">
        <label for="resultGroups" style="align-self:center;margin:0;width:auto">分组数</label>
        <select id="resultGroups" v-model.number="state.currentGroupingN" style="width:96px">
          <option v-for="g in groupOptions" :key="g" :value="g">{{ g }} 组</option>
        </select>
      </span>
      <label for="resultScope" style="align-self:center;margin:0;width:auto">图表视角</label>
      <select id="resultScope" v-model="state.currentChartScope" style="width:136px">
        <option v-for="o in scopeOptions" :key="o.value" :value="o.value">{{ o.label }}</option>
      </select>
      <span class="hint" id="groupSwitchHint" style="margin:0;align-self:center">{{ groupSwitchHint }}</span>
    </div>

    <div class="result-actions">
      <button class="secondary" @click="addFactorToStrategy">添加到策略条件</button>
      <button class="secondary" @click="openCandidateDlg" :disabled="!candidateSaveOk">保存为候选</button>
      <button type="button" class="secondary" @click="switchTab(5)" v-if="state.candidateSavedFor">查看候选 →</button>
      <span class="spacer"></span>
      <span class="hint">把该因子与参数（不含阈值）加入 ② 的简单配置，阈值由你在那里填写。</span>
      <span class="hint" v-if="!candidateSaveOk">{{ candidateSaveHint }}</span>
    </div>

    <div class="summary-line" id="quintileSummary">{{ quintileSummary }}</div>
    <div ref="quintileChart" class="chart-quintile" v-show="hasChart"></div>
    <div class="empty" id="quintileNA" v-show="!hasChart">{{ quintileNA }}</div>

    <template v-if="showGroupDetail">
      <div class="year-title" id="groupDetailTitle">分组明细</div>
      <div class="year-wrap" id="groupDetailWrap">
        <table id="groupDetail">
          <thead><tr><th>组</th><th>因子值范围</th><th>中位数</th><th>均值</th><th>样本占比</th><th>未来收益</th></tr></thead>
          <tbody>
            <tr v-for="g in detailGroups" :key="g.label">
              <td>{{ g.label }}</td>
              <td>{{ groupRangeLabel(g, unit) || '无有效样本' }}</td>
              <td>{{ g.factorMedian != null ? fmtFactorVal(g.factorMedian, unit) : '无有效样本' }}</td>
              <td>{{ g.factorMean != null ? fmtFactorVal(g.factorMean, unit) : '无有效样本' }}</td>
              <td>{{ g.countPct > 0 ? (g.countPct * 100).toFixed(1) + '%' : '无有效样本' }}</td>
              <td :class="g.forwardReturn != null ? (g.forwardReturn * 100 > 0 ? 'pos' : (g.forwardReturn * 100 < 0 ? 'neg' : '')) : 'na'">{{ g.forwardReturn != null ? (g.forwardReturn * 100).toFixed(2) + '%' : '无有效样本' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>

    <div class="coverage">
      <span id="covText">{{ covText }}</span>
      <details class="stat" v-if="failures.length" style="margin-top:0">
        <summary id="failSummary">失败明细（{{ failures.length }}）</summary>
        <ul id="failList"><li v-for="(f, i) in failures" :key="i">{{ f.code }} · {{ f.year }} · {{ f.stage }} · {{ f.message }}</li></ul>
      </details>
    </div>
    <div class="warnbar" v-show="skipped > 0">部分样本因数据缺失或无效被跳过，统计结论仅基于完成计算的样本。</div>

    <template v-if="hasYears">
      <div class="year-title" id="yearTitle">年度稳定性</div>
      <div class="year-wrap" id="yearWrap">
        <table id="yearStability">
          <thead><tr>
            <th>年份</th><th>有效日</th><th>IC 均值</th><th>IC 标准差</th><th>t 值</th>
            <th>{{ edgeLabels.first }}</th><th>{{ edgeLabels.last }}</th><th>{{ edgeLabels.spread }}</th><th>方向</th>
          </tr></thead>
          <tbody>
            <tr v-for="y in yearRows" :key="y.year">
              <td>{{ y.year }}</td>
              <td>{{ y.pairs }}</td>
              <td v-for="(c, i) in y.cells" :key="i" :class="c.cls">{{ c.txt }}</td>
              <td :class="y.dirNa ? 'na' : ''">{{ y.dir }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <div class="hint" id="yearBias">样本池说明：当前 all 股票池取自当前沪深主板代码列表，不是历史时点成分股，退市股票缺失，存在生存者偏差；后上市股票只从其有数据的年份起参与，缺失年份见失败明细。</div>
    </template>
    <div class="empty" v-show="!hasYears" id="yearNA">{{ yearNA }}</div>

    <details class="stat" :open="statOpen" @toggle="statOpen = $event.target.open">
      <summary>统计详情（IC、显著性、逐日 IC）</summary>
      <div id="icStats">{{ icStats }}</div>
      <div ref="icChart" class="chart-ic"></div>
    </details>
  </div>
  <div class="empty" v-else id="factorEmpty" role="status">暂无分析结果，选择因子后点击“开始分析”。</div>
</div>
  `,
};
