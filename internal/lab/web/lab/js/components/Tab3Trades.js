// Tab3Trades：交易明细与日K、成交量、MACD 联动图。
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
      klineWarning: '',
      klineC: null,
      klineRequest: 0,
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
    watch(() => state.currentReport, () => this.resetReport());
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
    this.klineRequest++;
    dropChart(this.klineC);
  },
  methods: {
    resetReport() {
      this.klineRequest++;
      this.klineTrade = null; this.klineLoading = false; this.klineErr = ''; this.klineWarning = '';
      dropChart(this.klineC); this.klineC = null;
      this.syncVariantFilter();
    },
    // 变体筛选下拉：优先 03 页选中行，其次保留当前值
    syncVariantFilter() {
      const opts = this.variantNames;
      if (state.selectedVariant && opts.includes(state.selectedVariant)) this.fVariant = state.selectedVariant;
      else if (this.fVariant && opts.includes(this.fVariant)) { /* 保留 */ }
      else this.fVariant = '';
    },
    async showKline(t) {
      const request = ++this.klineRequest;
      this.klineTrade = t;
      this.klineLoading = true;
      this.klineErr = ''; this.klineWarning = '';
      try {
        await this.$nextTick();
        if (request !== this.klineRequest) return;
        if (window.matchMedia('(max-width: 900px)').matches) {
          this.$refs.klinePanel?.scrollIntoView({block: 'start', behavior: 'auto'});
          this.$refs.klinePanel?.focus({preventScroll: true});
        }
        // 拉取买入前30天到卖出后5天。
        const from = new Date(t.buyTime); from.setDate(from.getDate() - 30);
        const to = new Date(t.sellTime); to.setDate(to.getDate() + 5);
        const fmt = d => d.toISOString().slice(0, 10);
        const ks = await Api.kline(t.code, fmt(from), fmt(to));
        if (request !== this.klineRequest) return;
        if (!ks.length) throw new Error('该区间暂无日K数据，请先更新行情后重试');
        this.klineLoading = false;
        await this.$nextTick();
        if (request === this.klineRequest) this.renderKline(t, ks);
      } catch (e) {
        if (request !== this.klineRequest) return;
        this.klineLoading = false;
        this.klineErr = e.message;
      }
    },
    renderKline(t, ks) {
      const el = this.$refs.klineChart;
      if (!el) return;
      if (!this.klineC) this.klineC = mountChart(el);
      if (!this.klineC) throw new Error('图表组件未加载，请刷新页面后重试');
      const dates = ks.map(k => k.date);
      const values = ks.map(k => [k.open, k.close, k.low, k.high]);
      const closes = ks.map(k => k.close);
      const vols = ks.map(k => k.volume);
      const dateSet = new Set(dates), missing = [];
      const mk = (time, type, price) => {
        if (!dateSet.has(time.slice(0, 10))) { missing.push(type); return null; }
        const isBuy = type === '买入', virtual = type === '期末估值';
        const label = isBuy ? 'B' : virtual ? '估' : 'S';
        return {name: `${type} ${time} · ${price.toFixed(2)}`, coord: [time.slice(0, 10), price], value: label,
          symbol: virtual ? 'diamond' : 'triangle', symbolRotate: isBuy ? 0 : 180, symbolSize: 14,
          itemStyle: {color: virtual ? '#94a3b8' : isBuy ? '#ef4444' : '#22c55e'},
          label: {show: true, formatter: label, color: '#fff', fontSize: 10, offset: [0, isBuy ? 12 : -12]}};
      };
      const markers = [mk(t.buyTime, '买入', t.buyPrice), mk(t.sellTime, t.virtual === true ? '期末估值' : '卖出', t.sellPrice)].filter(Boolean);
      this.klineWarning = missing.length ? `${missing.join('、')}当日缺少日K，未绘制对应标记；时间与价格见上方交易信息。` : '';
      this.klineC.setOption({
        backgroundColor: 'transparent',
        title: {text: `${t.code}（${t.variant} · 收益 ${t.rate.toFixed(2)}%）`, left: 12, top: 6, textStyle: {fontSize: 13, color: '#e9eef8'}},
        tooltip: {trigger: 'axis', axisPointer: {type: 'cross'}},
        axisPointer: {link: [{xAxisIndex: 'all'}]},
        legend: {top: 28, right: 10, data: ['日K', 'MA5', 'MA10', 'MA20']},
        dataZoom: [{type: 'inside', xAxisIndex: [0, 1, 2]}, {show: true, xAxisIndex: [0, 1, 2], type: 'slider', bottom: 8}],
        grid: [{left: 54, right: 20, top: 72, height: '38%'},
          {left: 54, right: 20, top: '57%', height: '12%'},
          {left: 54, right: 20, top: '77%', height: '12%'}],
        graphic: [
          {type: 'text', left: 54, top: '53%', style: {text: '成交量', fill: '#94a3b8', fontSize: 11}},
          {type: 'text', left: 54, top: '73%', style: {text: 'MACD(12,26,9) · 柱=DIF−DEA', fill: '#94a3b8', fontSize: 11}},
        ],
        xAxis: [
          {type: 'category', data: dates, boundaryGap: false, axisLabel: {show: false}},
          {type: 'category', gridIndex: 1, data: dates, boundaryGap: false, axisLabel: {show: false}},
          {type: 'category', gridIndex: 2, data: dates, boundaryGap: false},
        ],
        yAxis: [
          {scale: true, splitArea: {show: true}},
          {scale: true, gridIndex: 1, splitNumber: 2, axisLabel: {show: false}, splitLine: {show: false}},
          {scale: true, gridIndex: 2, splitNumber: 2, axisLabel: {fontSize: 10}, splitLine: {show: false}},
        ],
        series: [
          {name: '日K', type: 'candlestick', data: values,
            itemStyle: {color: '#ef4444', color0: '#22c55e', borderColor: '#ef4444', borderColor0: '#22c55e'},
            markPoint: {data: markers}},
          {name: 'MA5', type: 'line', data: ma(closes, 5), symbol: 'none', lineStyle: {width: 1, color: '#f59e0b'}},
          {name: 'MA10', type: 'line', data: ma(closes, 10), symbol: 'none', lineStyle: {width: 1, color: '#8b5cf6'}},
          {name: 'MA20', type: 'line', data: ma(closes, 20), symbol: 'none', lineStyle: {width: 1, color: '#3b82f6'}},
          {name: '成交量', type: 'bar', xAxisIndex: 1, yAxisIndex: 1, data: vols,
            itemStyle: {color: p => values[p.dataIndex] && values[p.dataIndex][1] >= values[p.dataIndex][0] ? '#ef4444' : '#22c55e'}},
          {name: 'MACD', type: 'bar', xAxisIndex: 2, yAxisIndex: 2, data: ks.map(k => k.macd),
            itemStyle: {color: p => p.value >= 0 ? '#ef4444' : '#22c55e'}},
          {name: 'DIF', type: 'line', xAxisIndex: 2, yAxisIndex: 2, data: ks.map(k => k.dif), symbol: 'none', lineStyle: {width: 1, color: '#f59e0b'}},
          {name: 'DEA', type: 'line', xAxisIndex: 2, yAxisIndex: 2, data: ks.map(k => k.dea), symbol: 'none', lineStyle: {width: 1, color: '#8b5cf6'}},
        ],
      }, true);
    },
  },
  template: `
<div class="trades-layout">
 <div class="card trades-list">
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
      <thead><tr><th>变体</th><th>代码</th><th>买入时间</th><th>买入价</th><th>卖出时间</th><th>卖出价</th><th>数量</th><th>盈亏(元)</th><th>收益率%</th><th>持仓天</th><th>成交类型</th></tr></thead>
      <tbody>
        <template v-if="visibleTrades.length">
          <tr v-for="(t, i) in visibleTrades" :key="i" @click="showKline(t)"
            :class="{selected: klineTrade && klineTrade.code === t.code && klineTrade.variant === t.variant && klineTrade.buyTime === t.buyTime && klineTrade.sellTime === t.sellTime}">
            <td>{{ t.variant }}</td>
            <td><button type="button" class="secondary trade-kline-button" :aria-label="'查看 ' + t.code + ' ' + t.buyTime + ' 交易K线'" aria-controls="tradeKlinePanel" @click.stop="showKline(t)">{{ t.code }} · K线</button></td>
            <td>{{ t.buyTime }}</td>
            <td>{{ t.buyPrice.toFixed(2) }}</td>
            <td>{{ t.sellTime }}</td>
            <td>{{ t.sellPrice.toFixed(2) }}</td>
            <td>{{ t.quantity }}</td>
            <td :class="t.profit >= 0 ? 'pos' : 'neg'">{{ t.profit.toFixed(0) }}</td>
            <td :class="t.rate >= 0 ? 'pos' : 'neg'">{{ t.rate.toFixed(2) }}</td>
            <td>{{ t.holdingDays }}</td>
            <td>{{ t.virtual === true ? '期末估值（Virtual）' : t.virtual === false ? '实际成交' : '未记录' }}</td>
          </tr>
        </template>
        <tr v-else><td colspan="11" class="empty">{{ noTradesText }}</td></tr>
      </tbody>
    </table>
  </div>
 </div>
  <section ref="klinePanel" id="tradeKlinePanel" class="card trade-kline-panel" tabindex="-1" aria-labelledby="tradeKlineTitle" :aria-busy="klineLoading">
    <h3 id="tradeKlineTitle">{{ klineTrade ? klineTrade.code + ' · 交易日K与买卖点' : '交易日K与买卖点' }}</h3>
    <p v-if="!klineTrade" class="empty trade-kline-placeholder">选择一笔交易，查看日K、成交量、MACD 与买卖点</p>
    <template v-if="klineTrade">
      <p class="hint">买入：{{ klineTrade.buyTime }} · {{ klineTrade.buyPrice.toFixed(2) }} 元（B）；{{ klineTrade.virtual === true ? '期末估值' : '卖出' }}：{{ klineTrade.sellTime }} · {{ klineTrade.sellPrice.toFixed(2) }} 元（{{ klineTrade.virtual === true ? '估' : 'S' }}）</p>
    </template>
    <p v-if="klineLoading" class="empty" role="status">加载K线中…</p>
    <div v-else-if="klineErr" class="warnbar" role="alert">K线加载失败：{{ klineErr }} <button type="button" class="secondary" @click="showKline(klineTrade)">重试</button></div>
    <p v-if="klineWarning" class="hint" role="status">{{ klineWarning }}</p>
    <!-- ECharts 独占容器，加载与错误提示不得放在其中。 -->
    <div v-show="klineTrade" :style="{visibility: klineLoading || klineErr ? 'hidden' : 'visible'}" ref="klineChart" class="chart-kline" role="img" aria-label="交易日K、成交量、MACD；B为买入、S为卖出、估为期末估值，准确时间与价格见上方"></div>
  </section>
</div>
  `,
};
