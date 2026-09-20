// Tab3Trades：04 交易明细与K线（6 筛选器 / 交易表 / K线弹层）。
// 从 index.html 迁移：renderTab3/filteredTrades/renderTrades/showKline。
// filteredTrades 为 computed，筛选器输入即重算；K线为组件内状态（切换 tab 保留）。
import { watch } from '../vendor/vue.esm-browser.prod.js';
import { state } from '../store.js';
import { Api } from '../api.js';
import { ma } from '../format.js';
import { mountChart, dropChart } from '../echarts.js';

export default {
  name: 'Tab3Trades',
  inject: ['switchTab'],
  setup() {
    return { state };
  },
  data() {
    return {
      fVariant: '',
      fCode: '',
      fFrom: '',
      fTo: '',
      fRateMin: '',
      fRateMax: '',
      klineTrade: null,   // 当前展示的 K 线交易（null=未展开）
      klineLoading: false,
      klineErr: '',
      klineC: null,
    };
  },
  computed: {
    rep() { return state.currentReport; },
    variantNames() {
      const r = state.currentReport;
      return r ? r.variants.map(v => v.name) : [];
    },
    filteredTrades() {
      const vName = this.fVariant, code = this.fCode.trim(),
        from = this.fFrom, to = this.fTo,
        rMin = parseFloat(this.fRateMin), rMax = parseFloat(this.fRateMax);
      const rows = [];
      const rep = state.currentReport;
      if (!rep) return rows;
      for (const v of rep.variants) {
        if (vName && v.name !== vName) continue;
        for (const t of v.trades) {
          if (code && !t.code.includes(code)) continue;
          if (from && t.buyTime.slice(0, 10) < from) continue;
          if (to && t.buyTime.slice(0, 10) > to) continue;
          if (!isNaN(rMin) && t.rate < rMin) continue;
          if (!isNaN(rMax) && t.rate > rMax) continue;
          rows.push({...t, variant: v.name});
        }
      }
      return rows;
    },
    // 与后端一致：最多渲染 2000 行，防止大报告卡死
    visibleTrades() { return this.filteredTrades.slice(0, 2000); },
    noTradesText() {
      return this.filteredTrades.length
        ? '（结果超过 2000 条，仅显示前 2000 条，可调整筛选缩小范围）'
        : '无匹配交易';
    },
  },
  mounted() {
    // 进入本页：同步变体下拉（保留上次选择/来自 03 的行选择），并修正图表尺寸
    watch(() => state.activeTab, n => {
      if (n !== 3) return;
      this.$nextTick(() => {
        this.syncVariantFilter();
        if (this.klineC) this.klineC.resize();
      });
    });
  },
  unmounted() {
    dropChart(this.klineC);
  },
  methods: {
    // 变体筛选下拉：优先 03 页选中行，其次保留当前值
    syncVariantFilter() {
      const opts = this.variantNames;
      if (state.selectedVariant && opts.includes(state.selectedVariant)) this.fVariant = state.selectedVariant;
      else if (this.fVariant && opts.includes(this.fVariant)) { /* 保留 */ }
      else this.fVariant = '';
    },
    async showKline(t) {
      this.klineTrade = t;
      this.klineLoading = true;
      this.klineErr = '';
      // 拉取买入前30天到卖出后5天
      const from = new Date(t.buyTime); from.setDate(from.getDate() - 30);
      const to = new Date(t.sellTime); to.setDate(to.getDate() + 5);
      const fmt = d => d.toISOString().slice(0, 10);
      try {
        const ks = await Api.kline(t.code, fmt(from), fmt(to));
        this.klineLoading = false;
        this.$nextTick(() => this.renderKline(t, ks));
      } catch (e) {
        this.klineLoading = false;
        this.klineErr = e.message;
      }
    },
    renderKline(t, ks) {
      const el = this.$refs.klineChart;
      if (!el) return;
      if (!this.klineC) { this.klineC = mountChart(el); if (!this.klineC) return; }
      const dates = ks.map(k => k.date);
      const values = ks.map(k => [k.open, k.close, k.low, k.high]);
      const closes = ks.map(k => k.close);
      const vols = ks.map(k => k.volume);
      const dateMap = new Map(ks.map(k => [k.date, k]));
      const mk = (time, type, price) => {
        const k = dateMap.get(time.slice(0, 10));
        const bp = k ? (type === '买' ? k.low : k.high) : price;
        const isBuy = type === '买';
        return {name: type, coord: [time.slice(0, 10), bp], value: isBuy ? 'B' : 'S',
          symbol: 'triangle', symbolRotate: isBuy ? 0 : 180, symbolSize: 14, symbolOffset: [0, isBuy ? 12 : -12],
          itemStyle: {color: isBuy ? '#ef4444' : '#22c55e'},
          label: {show: true, formatter: isBuy ? 'B' : 'S', color: '#fff', fontSize: 10, offset: [0, isBuy ? 4 : -4]}};
      };
      this.klineC.setOption({
        backgroundColor: 'transparent',
        title: {text: `${t.code}（${t.variant} · 收益 ${t.rate.toFixed(2)}%）`, left: 12, top: 6, textStyle: {fontSize: 13, color: '#e9eef8'}},
        tooltip: {trigger: 'axis', axisPointer: {type: 'cross'}},
        legend: {top: 6, right: 10, data: ['日K', 'MA5', 'MA10', 'MA20']},
        dataZoom: [{type: 'inside', xAxisIndex: [0, 1]}, {show: true, xAxisIndex: [0, 1], type: 'slider', bottom: 8}],
        grid: [{left: 60, right: 30, top: 50, height: '55%'}, {left: 60, right: 30, top: '74%', height: '14%'}],
        xAxis: [
          {type: 'category', data: dates, boundaryGap: false},
          {type: 'category', gridIndex: 1, data: dates, boundaryGap: false, axisLabel: {show: false}},
        ],
        yAxis: [
          {scale: true, splitArea: {show: true}},
          {scale: true, gridIndex: 1, splitNumber: 2, axisLabel: {show: false}, splitLine: {show: false}},
        ],
        series: [
          {name: '日K', type: 'candlestick', data: values,
            itemStyle: {color: '#ef4444', color0: '#22c55e', borderColor: '#ef4444', borderColor0: '#22c55e'},
            markPoint: {data: [mk(t.buyTime, '买', t.buyPrice), mk(t.sellTime, '卖', t.sellPrice)]}},
          {name: 'MA5', type: 'line', data: ma(closes, 5), symbol: 'none', lineStyle: {width: 1, color: '#f59e0b'}},
          {name: 'MA10', type: 'line', data: ma(closes, 10), symbol: 'none', lineStyle: {width: 1, color: '#8b5cf6'}},
          {name: 'MA20', type: 'line', data: ma(closes, 20), symbol: 'none', lineStyle: {width: 1, color: '#3b82f6'}},
          {name: '成交量', type: 'bar', xAxisIndex: 1, yAxisIndex: 1, data: vols,
            itemStyle: {color: p => values[p.dataIndex] && values[p.dataIndex][1] >= values[p.dataIndex][0] ? '#ef4444' : '#22c55e'}},
        ],
      }, true);
    },
  },
  template: `
<div class="card">
  <h3>交易明细与K线</h3>
  <div class="filters">
    <select v-model="fVariant" aria-label="变体筛选">
      <option value="">全部变体</option>
      <option v-for="nm in variantNames" :key="nm" :value="nm">{{ nm }}</option>
    </select>
    <input type="text" v-model="fCode" placeholder="代码" aria-label="代码筛选">
    <input type="date" v-model="fFrom" title="买入日期从" aria-label="买入日期从">
    <input type="date" v-model="fTo" title="买入日期到" aria-label="买入日期到">
    <input type="number" v-model="fRateMin" placeholder="收益率≥%" step="0.1" style="width:90px" aria-label="收益率下限">
    <input type="number" v-model="fRateMax" placeholder="收益率≤%" step="0.1" style="width:90px" aria-label="收益率上限">
  </div>
  <div class="detail-wrap">
    <table>
      <thead><tr><th>变体</th><th>代码</th><th>买入时间</th><th>买入价</th><th>卖出时间</th><th>卖出价</th><th>数量</th><th>盈亏(元)</th><th>收益率%</th><th>持仓天</th></tr></thead>
      <tbody>
        <template v-if="visibleTrades.length">
          <tr v-for="(t, i) in visibleTrades" :key="i" @click="showKline(t)">
            <td>{{ t.variant }}</td>
            <td>{{ t.code }}</td>
            <td>{{ t.buyTime }}</td>
            <td>{{ t.buyPrice.toFixed(2) }}</td>
            <td>{{ t.sellTime }}</td>
            <td>{{ t.sellPrice.toFixed(2) }}</td>
            <td>{{ t.quantity }}</td>
            <td :class="t.profit >= 0 ? 'pos' : 'neg'">{{ t.profit.toFixed(0) }}</td>
            <td :class="t.rate >= 0 ? 'pos' : 'neg'">{{ t.rate.toFixed(2) }}</td>
            <td>{{ t.holdingDays }}</td>
          </tr>
        </template>
        <tr v-else><td colspan="10" class="empty">{{ noTradesText }}</td></tr>
      </tbody>
    </table>
  </div>
  <div v-show="klineTrade" ref="klineChart" class="chart-kline">
    <div v-if="klineLoading" class="empty">加载K线中…</div>
    <div v-else-if="klineErr" class="empty">K线加载失败: {{ klineErr }}</div>
  </div>
</div>
  `,
};
