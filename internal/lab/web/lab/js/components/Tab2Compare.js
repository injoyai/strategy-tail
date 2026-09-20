// Tab2Compare：03 组合对比（变体汇总 / 多条件对照摘要 / 累计收益与胜率图）。
// 从 index.html 迁移：renderCmp/renderTab2/renderCharts。
// 行点击 → state.selectedVariant + switchTab(3)；图表按 activeTab 可见时渲染。
import { watch } from '../vendor/vue.esm-browser.prod.js';
import { state } from '../store.js';
import { factorTypeName } from '../format.js';
import { mountChart, dropChart } from '../echarts.js';

export default {
  name: 'Tab2Compare',
  inject: ['switchTab'],
  setup() {
    return { state, factorTypeName };
  },
  data() {
    return { trendC: null, winC: null };
  },
  computed: {
    rep() { return state.currentReport; },
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
    // 按 avgProfit 降序（无交易排 -1e9）
    vs() {
      const r = state.currentReport;
      if (!r) return [];
      return [...r.variants].sort((a, b) =>
        (b.stats.total ? b.stats.avgProfit : -1e9) - (a.stats.total ? a.stats.avgProfit : -1e9));
    },
    coverageText() {
      const cov = state.currentReport && state.currentReport.coverage;
      return cov ? `样本覆盖：请求 ${cov.requested} · 完成 ${cov.completed} · 跳过 ${cov.skipped}` : '';
    },
  },
  mounted() {
    // 面板可见（v-show）后容器才有尺寸，图表必须在此后才 init
    watch(() => state.activeTab, n => { if (n === 2) this.renderNow(); });
    watch(() => state.currentReport, () => { if (state.activeTab === 2) this.renderNow(); });
    if (state.activeTab === 2 && state.currentReport) this.renderNow();
  },
  unmounted() {
    dropChart(this.trendC);
    dropChart(this.winC);
  },
  methods: {
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
      // 累计收益曲线：每变体按买入时间排交易，累计收益率
      const series = vs.filter(v => v.stats.total > 0).map(v => {
        const ts = [...v.trades].sort((a, b) => a.buyTime.localeCompare(b.buyTime));
        let acc = 0;
        return {name: v.name, type: 'line', showSymbol: false, lineStyle: {width: 1.5},
          data: ts.map(t => { acc += t.rate; return [t.buyTime, Number(acc.toFixed(2))]; })};
      });
      if (!this.trendC) { this.trendC = mountChart(trendEl); if (!this.trendC) return; }
      this.trendC.setOption({
        backgroundColor: 'transparent',
        title: {text: '逐笔收益率累计（非资金曲线）', left: 10, top: 6, textStyle: {fontSize: 13, color: '#e9eef8'}},
        tooltip: {trigger: 'axis'},
        legend: {top: 6, right: 10, type: 'scroll'},
        grid: {left: 50, right: 20, top: 60, bottom: 40},
        xAxis: {type: 'time'},
        yAxis: {scale: true},
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
<div class="card">
  <h3>变体汇总 <span class="hint">{{ reportMeta }}</span></h3>

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
        <div class="big">{{ cmp.combinedTrades }} 笔 · 信号保留率 {{ cmp.retentionRate == null ? '-' : (cmp.retentionRate * 100).toFixed(2) + '%' }}</div>
        <div class="sub">{{ statLine(byName[cmp.combinedVariant]) }}</div>
      </div>
    </div>
    <div class="cmp-contrib">
      <h4>条件贡献（单条件 = 基准 + 单独追加该条件）</h4>
      <table>
        <thead><tr><th>#</th><th>条件</th><th>交易数</th><th>信号保留率</th><th>胜率%</th><th>平均%</th><th>盈亏比</th></tr></thead>
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
    <div class="coverage">{{ coverageText }}</div>
  </div>

  <div class="detail-wrap">
    <table>
      <thead><tr><th>#</th><th>变体</th><th>笔数</th><th>胜率%</th><th>平均%</th><th>盈亏比</th><th>最大%</th><th>最小%</th><th>收益额(万)</th></tr></thead>
      <tbody>
        <template v-if="vs.length">
          <tr v-for="(v, i) in vs" :key="v.name"
            :class="{selected: state.selectedVariant === v.name}"
            @click="selectVariant(v)">
            <td>{{ i + 1 }}</td>
            <td>{{ v.name }}</td>
            <td>{{ v.stats.total }}</td>
            <td :class="v.stats.total ? (v.stats.winRate >= 50 ? 'pos' : 'neg') : ''">{{ v.stats.total ? v.stats.winRate.toFixed(2) : '-' }}</td>
            <td :class="v.stats.total ? (v.stats.avgProfit >= 0 ? 'pos' : 'neg') : ''">{{ v.stats.total ? v.stats.avgProfit.toFixed(2) : '-' }}</td>
            <td>{{ v.stats.total ? (isFinite(v.stats.profitFactor) ? v.stats.profitFactor.toFixed(2) : '+Inf') : '-' }}</td>
            <td :class="v.stats.total ? 'pos' : ''">{{ v.stats.total ? v.stats.maxProfit.toFixed(2) : '-' }}</td>
            <td :class="v.stats.total ? 'neg' : ''">{{ v.stats.total ? v.stats.maxLoss.toFixed(2) : '-' }}</td>
            <td :class="profitWan(v) != null ? (profitWan(v) >= 0 ? 'pos' : 'neg') : ''">{{ profitWan(v) != null ? profitWan(v).toFixed(1) : '-' }}</td>
          </tr>
        </template>
        <tr v-else><td colspan="9" class="empty">暂无报告，先在 ② 运行回测</td></tr>
      </tbody>
    </table>
  </div>

  <div class="charts2">
    <div ref="trendChart" class="chart-block"></div>
    <div ref="winChart" class="chart-block"></div>
  </div>
</div>
  `,
};
