// Tab2Compare：03 组合对比（变体汇总 / 多条件对照摘要 / 累计收益与胜率图）。
// 从 index.html 迁移：renderCmp/renderTab2/renderCharts。
// 行点击 → state.selectedVariant + switchTab(3)；图表按 activeTab 可见时渲染。
import { watch } from '../vendor/vue.esm-browser.prod.js';
import { state } from '../store.js';
import { factorTypeName, formatBuilderSpec, formatMacdParameters } from '../format.js';
import { Api } from '../api.js';
import { mountChart, dropChart, ANNUAL_PALETTE, NAV_COLORS } from '../echarts.js';
import ReportHistory from './ReportHistory.js';

export default {
  name: 'Tab2Compare',
  components: {ReportHistory},
  inject: ['switchTab'],
  setup() {
    return { state, factorTypeName };
  },
  data() {
    return { trendC: null, winC: null };
  },
  computed: {
    rep() { return state.currentReport; },
    accountMode() { return !!this.rep?.config.account; },
    benchmarkReturn() { return this.rep?.benchmark?.at(-1)?.return ?? null; },
    accountWarnings() {
      return this.vs.flatMap(v => {
        const a = v.account;
        if (!a) return [];
        const messages = [];
        if (a.missingMinuteDays) messages.push(`${v.name}：${a.missingMinuteDays} 个持仓交易日缺少分钟行情，卖出按日线收盘判断`);
        if (a.staleValuationDays) messages.push(`${v.name}：${a.staleValuationDays} 个持仓交易日缺少日线，沿用上一收盘价估值`);
        return messages;
      });
    },
    builderRules() {
      return formatBuilderSpec(state.currentReport?.config.builder, state.builderCatalog, state.factorCatalog);
    },
    macdParameters() {
      return formatMacdParameters(state.currentReport?.config.macd);
    },
    reportMeta() {
      const r = state.currentReport;
      if (!r) return '';
      return `${r.config.startYear}-${r.config.endYear} · ${new Date(r.finishedAt).toLocaleString()}`;
    },
    // 对照摘要仅简单模式报告（source==='simple' 且带 comparison）显示
    showCmp() {
      const r = state.currentReport;
      return !!(r && r.source === 'simple' && r.comparison);
    },
    cmp() { return state.currentReport && state.currentReport.comparison ? state.currentReport.comparison : null; },
    byName() {
      const m = {};
      (state.currentReport ? state.currentReport.variants : []).forEach(v => { m[v.name] = v; });
      return m;
    },
    // 账户报告按账户收益排序；历史逐笔报告保留原排序。
    vs() {
      const r = state.currentReport;
      if (!r) return [];
      const rank = v => this.accountMode ? (this.accountReturn(v) ?? -1e9) : (v.stats.total ? v.stats.avgProfit : -1e9);
      return [...r.variants].sort((a, b) => rank(b) - rank(a));
    },
    coverageText() {
      const cov = state.currentReport && state.currentReport.coverage;
      const unit = state.currentReport?.source === 'macd' ? '股票×年份' : '股票';
      return cov ? `样本覆盖（${unit}）：请求 ${cov.requested} · 完成 ${cov.completed} · 跳过 ${cov.skipped}` : '';
    },
  },
  mounted() {
    // 面板可见（v-show）后容器才有尺寸，图表必须在此后才 init
    watch(() => state.activeTab, n => { if (n === 2) this.renderNow(); });
    watch(() => state.currentReport, () => {
      this.loadBuilderCatalog();
      if (state.activeTab === 2) this.renderNow();
    });
    this.loadBuilderCatalog();
    if (state.activeTab === 2 && state.currentReport) this.renderNow();
  },
  unmounted() {
    dropChart(this.trendC);
    dropChart(this.winC);
  },
  methods: {
    accountReturn(v) { return v.account?.points?.at(-1)?.return ?? null; },
    excessReturn(v) {
      const r = this.accountReturn(v);
      return r != null && this.benchmarkReturn != null ? r - this.benchmarkReturn : null;
    },
    returnFmt(r) { return r == null ? '-' : r.toFixed(2); },
    returnCls(r) { return r == null || r === 0 ? '' : r > 0 ? 'pos' : 'neg'; },
    async loadBuilderCatalog() {
      if (!state.currentReport?.config.builder || state.builderCatalog.length) return;
      try { state.builderCatalog = (await Api.builderComponents()).components || []; }
      catch { /* 目录不可用时仍按冻结的组件标识展示规则与参数。 */ }
    },
    statLine(v) {
      if (!v || !v.stats || !v.stats.total) return '无交易';
      const s = v.stats;
      return `胜率 ${s.winRate.toFixed(2)}% · 平均 ${s.avgProfit.toFixed(2)}% · 盈亏比 ${s.profitFactor == null ? '+Inf' : s.profitFactor.toFixed(2)}`;
    },
    factorLabel(f) {
      const cat = state.factorCatalog.find(e => e.kind === f.kind);
      return `${cat ? factorTypeName(cat) : f.kind}（${f.days} 日）`;
    },
    statFmt(v, key, d) {
      const s = v && v.stats;
      if (!s || !s.total) return '-';
      const x = s[key];
      return x == null || !isFinite(x) ? String(x == null ? '-' : x) : x.toFixed(d);
    },
    statCls(v, key, thr) {
      const s = v && v.stats;
      if (!s || !s.total) return '';
      return s[key] >= thr ? 'pos' : 'neg';
    },
    statPF(v) {
      const s = v && v.stats;
      if (!s || !s.total) return '-';
      return s.profitFactor == null ? '+Inf' : s.profitFactor.toFixed(2);
    },
    profitWan(v) {
      if (!v.stats.total) return null;
      return v.trades.reduce((sum, t) => sum + t.profit, 0) / 1e4;
    },
    selectVariant(v) {
      state.selectedVariant = v.name;
      this.switchTab(3);
    },
    renderNow() {
      this.$nextTick(() => this.renderCharts(this.vs));
    },
    renderCharts(vs) {
      const trendEl = this.$refs.trendChart;
      const winEl = this.$refs.winChart;
      if (!trendEl || !winEl) return;
      // 账户曲线来自服务器每日估值，历史报告仍使用原逐笔累计口径。
      const series = this.accountMode ? vs.filter(v => v.account?.points?.length).map((v, i) => ({
        name: v.name, type: 'line', showSymbol: false, lineStyle: {width: 1.5},
        color: ANNUAL_PALETTE[(i + 1) % ANNUAL_PALETTE.length],
        data: v.account.points.map(p => [p.date, p.return]),
      })) : vs.filter(v => v.stats.total > 0).map(v => {
        const ts = [...v.trades].sort((a, b) => a.buyTime.localeCompare(b.buyTime));
        let acc = 0;
        return {name: v.name, type: 'line', showSymbol: false, lineStyle: {width: 1.5},
          data: ts.map(t => { acc += t.rate; return [t.buyTime, Number(acc.toFixed(2))]; })};
      });
      if (this.accountMode && this.rep.benchmark?.length) {
        series.push({name: '上证指数（买入持有）', type: 'line', showSymbol: false,
          lineStyle: {width: 2, type: 'dashed'}, color: NAV_COLORS.net,
          data: this.rep.benchmark.map(p => [p.date, p.return])});
      }
      if (!this.trendC) { this.trendC = mountChart(trendEl); if (!this.trendC) return; }
      this.trendC.setOption({
        animation: !this.accountMode,
        backgroundColor: 'transparent',
        title: {text: this.accountMode ? '账户累计收益率' : '逐笔收益率累计（非资金曲线）', left: 10, top: 6, textStyle: {fontSize: 13, color: '#e9eef8'}},
        tooltip: {trigger: 'axis', valueFormatter: v => `${Number(v).toFixed(2)}%`},
        legend: {top: 30, right: 10, type: 'scroll'},
        grid: {left: 50, right: this.accountMode ? 50 : 20, top: 60, bottom: 40},
        xAxis: this.accountMode ? {type: 'category', data: this.rep.benchmark?.map(p => p.date) || [],
          boundaryGap: false, axisLabel: {hideOverlap: true}} : {type: 'time'},
        yAxis: {scale: true, axisLabel: {formatter: '{value}%'}},
        series,
      }, true);
      if (!this.winC) { this.winC = mountChart(winEl); if (!this.winC) return; }
      this.winC.setOption({
        backgroundColor: 'transparent',
        title: {text: '胜率%', left: 10, top: 6, textStyle: {fontSize: 13, color: '#e9eef8'}},
        tooltip: {formatter: p => `${p.name}：${Number(p.value).toFixed(2)}%`},
        grid: {left: 50, right: 20, top: 60, bottom: 80},
        xAxis: {type: 'category', data: vs.map(v => v.name), axisLabel: {rotate: 30, fontSize: 11}},
        yAxis: {max: 100},
        series: [{type: 'bar', data: vs.map(v => v.stats.winRate),
          itemStyle: {color: p => p.value >= 50 ? '#ef4444' : '#22c55e'},
          label: {show: true, position: 'top', fontSize: 11, formatter: p => Number(p.value).toFixed(2)}}],
      }, true);
    },
  },
  template: `
<report-history />
<div class="card">
  <h3>{{ rep?.source === 'macd' ? '回测统计' : '变体汇总' }} <span class="hint">{{ reportMeta }}</span></h3>

  <!-- 多条件对照摘要：仅简单模式报告 -->
  <div v-if="showCmp && cmp">
    <div class="cmp-hero">
      <div class="cmp-box base">
        <div class="t">{{ cmp.baselineVariant }}</div>
        <div class="big">{{ cmp.baselineTrades }} 笔</div>
        <div class="sub">{{ statLine(byName[cmp.baselineVariant]) }}</div>
      </div>
      <div class="cmp-box combo">
        <div class="t">{{ cmp.combinedVariant }}</div>
        <div class="big">{{ cmp.combinedTrades }} 笔 · {{ accountMode ? '成交数比' : '信号保留率' }} {{ cmp.retentionRate == null ? '-' : (cmp.retentionRate * 100).toFixed(2) + '%' }}</div>
        <div class="sub">{{ statLine(byName[cmp.combinedVariant]) }}</div>
      </div>
    </div>
    <div class="cmp-contrib">
      <h4>{{ accountMode ? '条件对照（各变体按相同资金规则独立回测）' : '条件贡献（单条件 = 基准 + 单独追加该条件）' }}</h4>
      <table>
        <thead><tr><th>#</th><th>条件</th><th>交易数</th><th>{{ accountMode ? '成交数比' : '信号保留率' }}</th><th>胜率%</th><th>平均%</th><th>盈亏比</th></tr></thead>
        <tbody>
          <tr v-for="(f, i) in (cmp.factorVariants || [])" :key="i">
            <td>{{ i + 1 }}</td>
            <td>{{ factorLabel(f) }}</td>
            <td>{{ f.trades }}</td>
            <td>{{ f.retentionRate == null ? '-' : (f.retentionRate * 100).toFixed(2) + '%' }}</td>
            <td :class="statCls(byName[f.variant], 'winRate', 50)">{{ statFmt(byName[f.variant], 'winRate', 2) }}</td>
            <td :class="statCls(byName[f.variant], 'avgProfit', 0)">{{ statFmt(byName[f.variant], 'avgProfit', 2) }}</td>
            <td>{{ statPF(byName[f.variant]) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
  <div v-if="coverageText" class="coverage">{{ coverageText }}</div>
  <p v-if="rep && !accountMode" class="hint" role="status">历史报告未记录账户资金；重新运行回测后可查看账户收益率与上证指数对比。</p>
  <details v-if="accountMode" class="macd-parameters">
    <summary>账户：{{ rep.config.account.initialCapital / 10000 }} 万元 · 最多 {{ rep.config.account.maxPositions }} 只 · 每笔预算 {{ rep.config.account.orderBudget / 10000 }} 万元</summary>
    <p>每笔预算含买入费用，资金不足时减少股数，按 100 股取整；同日先卖后买，买入信号按代码升序执行，单票只持有一笔。跨年保留现金与持仓，未卖出持仓按每日收盘价估值。</p>
    <p>账户收益率 =（现金 + 持仓市值）÷ 初始资金 − 1。上证指数以回测首个交易日收盘价为基准，按同期买入持有价格涨幅计算；指数不扣交易成本，策略含费用与滑点。超额列为两者之差（百分点）。</p>
    <p v-if="showCmp">各变体使用独立账户；成交数比是实际成交笔数相对基准的比例，不代表买入信号筛选比例。</p>
    <p>沿用现有行情和策略成交假设，未新增分红现金流、涨跌停排队和成交容量模型；该结果为模拟账户收益。</p>
  </details>
  <div v-if="accountWarnings.length" class="warnbar" role="status"><ul><li v-for="message in accountWarnings" :key="message">{{ message }}</li></ul></div>
  <details v-if="rep?.coverage.skipped > 0" class="macd-parameters">
    <summary>查看跳过原因（{{ rep.coverage.skipped }}）</summary>
    <ul><li v-for="f in rep.coverage.failures" :key="f.code + ':' + f.year">{{ f.code }} · {{ f.year }} · {{ f.message }}</li></ul>
  </details>

  <details v-if="macdParameters.length" class="macd-parameters">
    <summary>本次回测参数</summary>
    <dl><template v-for="p in macdParameters" :key="p.label"><dt>{{ p.label }}</dt><dd>{{ p.value }}</dd></template></dl>
  </details>
  <details v-if="builderRules" class="macd-parameters">
    <summary>本次回测规则（报告快照）</summary>
    <pre class="builder-snapshot">{{ builderRules }}</pre>
  </details>

  <div class="detail-wrap">
    <table>
      <thead><tr><th>#</th><th>变体</th><th v-if="accountMode">账户收益%</th><th v-if="accountMode">上证收益%</th><th v-if="accountMode">超额(百分点)</th><th>笔数</th><th>胜率%</th><th>单笔平均%</th><th>盈亏比</th><th>最大%</th><th>最小%</th><th>收益额(万)</th></tr></thead>
      <tbody>
        <template v-if="vs.length">
          <tr v-for="(v, i) in vs" :key="v.name"
            :class="{selected: state.selectedVariant === v.name}"
            @click="selectVariant(v)">
            <td>{{ i + 1 }}</td>
            <td>{{ v.name }}</td>
            <td v-if="accountMode" :class="returnCls(accountReturn(v))">{{ returnFmt(accountReturn(v)) }}</td>
            <td v-if="accountMode" :class="returnCls(benchmarkReturn)">{{ returnFmt(benchmarkReturn) }}</td>
            <td v-if="accountMode" :class="returnCls(excessReturn(v))">{{ returnFmt(excessReturn(v)) }}</td>
            <td>{{ v.stats.total }}</td>
            <td :class="v.stats.total ? (v.stats.winRate >= 50 ? 'pos' : 'neg') : ''">{{ v.stats.total ? v.stats.winRate.toFixed(2) : '-' }}</td>
            <td :class="v.stats.total ? (v.stats.avgProfit >= 0 ? 'pos' : 'neg') : ''">{{ v.stats.total ? v.stats.avgProfit.toFixed(2) : '-' }}</td>
            <td>{{ statPF(v) }}</td>
            <td :class="v.stats.total ? 'pos' : ''">{{ v.stats.total ? v.stats.maxProfit.toFixed(2) : '-' }}</td>
            <td :class="v.stats.total ? 'neg' : ''">{{ v.stats.total ? v.stats.maxLoss.toFixed(2) : '-' }}</td>
            <td :class="profitWan(v) != null ? (profitWan(v) >= 0 ? 'pos' : 'neg') : ''">{{ profitWan(v) != null ? profitWan(v).toFixed(1) : '-' }}</td>
          </tr>
        </template>
        <tr v-else><td :colspan="accountMode ? 12 : 9" class="empty">暂无报告，先在回测配置中运行回测</td></tr>
      </tbody>
    </table>
  </div>

  <div class="charts2">
    <div ref="trendChart" class="chart-block" role="img" :aria-label="accountMode ? '账户累计收益率与上证指数对比，期末数值见上方表格' : '逐笔收益率累计，非资金曲线'"></div>
    <div ref="winChart" class="chart-block"></div>
  </div>
</div>
  `,
};
