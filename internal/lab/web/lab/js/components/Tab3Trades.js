// Tab3Trades：交易明细与日K、成交量、MACD 联动图。
// 从 index.html 迁移：renderTab3/filteredTrades/renderTrades/showKline。
// filteredTrades 为 computed，筛选器输入即重算；K线为组件内状态（切换 tab 保留）。
import { watch } from '../vendor/vue.esm-browser.prod.js';
import { state } from '../store.js';
import { Api } from '../api.js';
import { stockLabel } from '../format.js';
import { mountKline, dropKline } from '../kline.js';
import { mountTradeTable } from '../trade-table.js';
import { StockInfo } from './StockInfo.js';

export default {
  name: 'Tab3Trades',
  components: { StockInfo },
  inject: ['switchTab'],
  setup() {
    return { stockLabel, state };
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
      tradeTable: null,
      tableErr: '',
      exportBusy: false,
      exportError: '',
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
      for (const [vi, v] of rep.variants.entries()) {
        if (vName && v.name !== vName) continue;
        for (const [ti, t] of v.trades.entries()) {
          if (code && !t.code.includes(code)) continue;
          if (from && t.buyTime.slice(0, 10) < from) continue;
          if (to && t.buyTime.slice(0, 10) > to) continue;
          if (!isNaN(rMin) && t.rate < rMin) continue;
          if (!isNaN(rMax) && t.rate > rMax) continue;
          rows.push({...t, stockLabel: stockLabel(t.code, state.stockNames), variant: v.name, _rowId: `${vi}:${ti}`});
        }
      }
      return rows;
    },
  },
  mounted() {
    watch(() => state.currentReport, () => this.resetReport());
    watch(() => state.stockNames, () => {if (this.klineC && !this.klineLoading && !this.klineErr) this.showKline(this.klineTrade);});
    watch(() => this.filteredTrades, () => this.refreshTrades());
    if (state.activeTab === 3) this.refreshTrades();
    // 进入本页：同步变体下拉（保留上次选择/来自 03 的行选择），并修正图表尺寸
    watch(() => state.activeTab, n => {
      if (n !== 3) return;
      this.$nextTick(() => {
        this.syncVariantFilter();
        this.refreshTrades();
        this.tradeTable?.resize();
        if (this.klineC) this.klineC.resize();
      });
    });
  },
  unmounted() {
    this.klineRequest++;
    dropKline(this.klineC);
    this.tradeTable?.destroy();
  },
  methods: {
    refreshTrades() {
      if (state.activeTab !== 3) return;
      this.tableErr = '';
      try {
        if (!this.tradeTable) this.tradeTable = mountTradeTable(this.$refs.tradeTable, t => this.showKline(t), e => {this.tableErr = e.message;});
        this.tradeTable.setData(this.filteredTrades);
      } catch (e) {this.tableErr = e.message;}
    },
    async exportReport() {
      if (!this.rep?.id || this.exportBusy) return;
      const id = this.rep.id;
      this.exportBusy = true; this.exportError = '';
      try {await Api.exportReport(id);}
      catch (e) {if (this.rep?.id === id) this.exportError = 'Excel 导出失败：' + e.message;}
      finally {this.exportBusy = false;}
    },
    resetReport() {
      this.klineRequest++;
      this.klineTrade = null; this.klineLoading = false; this.klineErr = ''; this.klineWarning = '';
      dropKline(this.klineC); this.klineC = null;
      this.exportError = '';
      this.tradeTable?.select(null);
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
      this.tradeTable?.select(t._rowId);
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
      if (!this.klineC) this.klineC = mountKline(el);
      if (!this.klineC) throw new Error('图表组件未加载，请刷新页面后重试');
      const markers = [
        {date: t.buyTime.slice(0, 10), price: t.buyPrice, kind: 'buy', text: `买入 ${t.buyTime} · ${t.buyPrice.toFixed(2)} 元`},
        {date: t.sellTime.slice(0, 10), price: t.sellPrice, kind: t.virtual === true ? 'est' : 'sell',
          text: `${t.virtual === true ? '期末估值' : '卖出'} ${t.sellTime} · ${t.sellPrice.toFixed(2)} 元`},
      ];
      const missing = this.klineC.setData({rows: ks, markers, view: 'all', identity: `${t.code}/${t.buyTime}/${t.sellTime}`,
        title: `${stockLabel(t.code, state.stockNames)}（${t.variant} · 收益 ${t.rate.toFixed(2)}%）`});
      this.klineWarning = missing.length ? `${missing.map(m => m.kind === 'buy' ? '买入' : m.kind === 'est' ? '期末估值' : '卖出').join('、')}当日缺少日K，未绘制对应标记；时间与价格见上方交易信息。` : '';
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
    <input ref="codeFilter" type="text" v-model="fCode" placeholder="代码" aria-label="代码筛选">
    <button v-if="fCode" type="button" class="secondary" aria-label="清除代码筛选" @click="fCode = ''; $nextTick(() => $refs.codeFilter.focus())">清除</button>
    <input type="date" v-model="fFrom" title="买入日期从" aria-label="买入日期从">
    <input type="date" v-model="fTo" title="买入日期到" aria-label="买入日期到">
    <input type="number" v-model="fRateMin" placeholder="收益率≥%" step="0.1" style="width:90px" aria-label="收益率下限">
    <input type="number" v-model="fRateMax" placeholder="收益率≤%" step="0.1" style="width:90px" aria-label="收益率上限">
  </div>
  <div class="trade-table-tools">
    <span class="hint" role="status">{{ rep ? '匹配 ' + filteredTrades.length + ' 笔交易' : '请先运行回测或从组合对比中选择历史结果' }}</span>
    <button type="button" class="secondary" :disabled="!rep?.id || exportBusy" :aria-busy="exportBusy" @click="exportReport">导出完整报告 Excel</button>
  </div>
  <p class="hint">Excel 包含全部变体与交易，不受当前筛选影响。</p>
  <p v-if="exportBusy" class="hint" role="status">正在生成 Excel…</p>
  <p v-if="exportError" class="msg err" role="alert">{{ exportError }} <button type="button" class="secondary" @click="exportReport">重试</button></p>
  <p v-if="tableErr" class="msg err" role="alert">表格加载失败：{{ tableErr }} <button type="button" class="secondary" @click="refreshTrades">重试</button></p>
  <div ref="tradeTable" class="trade-table" aria-label="交易明细"></div>
 </div>
  <section ref="klinePanel" id="tradeKlinePanel" class="card trade-kline-panel" tabindex="-1" aria-labelledby="tradeKlineTitle" :aria-busy="klineLoading">
    <h3 id="tradeKlineTitle">{{ klineTrade ? stockLabel(klineTrade.code, state.stockNames) + ' · 交易日K与买卖点' : '交易日K与买卖点' }}</h3>
    <p v-if="!klineTrade" class="empty trade-kline-placeholder">选择一笔交易，查看日K、成交量、MACD 与买卖点</p>
    <template v-if="klineTrade">
      <StockInfo :code="klineTrade.code" :history-date="klineTrade.buyTime.slice(0,10)" history-label="买入日" />
      <p class="hint">买入：{{ klineTrade.buyTime }} · {{ klineTrade.buyPrice.toFixed(2) }} 元（B）；{{ klineTrade.virtual === true ? '期末估值' : '卖出' }}：{{ klineTrade.sellTime }} · {{ klineTrade.sellPrice.toFixed(2) }} 元（{{ klineTrade.virtual === true ? '估' : 'S' }}）</p>
    </template>
    <div v-if="klineTrade" class="kline-tools" role="group" aria-label="交易K线视图">
      <button type="button" class="secondary" aria-label="缩小K线" :disabled="!klineC || klineLoading || !!klineErr" @click="klineC?.zoom(0.8)">− 缩小</button>
      <button type="button" class="secondary" aria-label="放大K线" :disabled="!klineC || klineLoading || !!klineErr" @click="klineC?.zoom(1.25)">＋ 放大</button>
      <button type="button" class="secondary" :disabled="!klineC || klineLoading || !!klineErr" @click="klineC?.fitContent()">显示全程</button>
    </div>
    <p v-if="klineLoading" class="empty" role="status">加载K线中…</p>
    <div v-else-if="klineErr" class="warnbar" role="alert">K线加载失败：{{ klineErr }} <button type="button" class="secondary" @click="showKline(klineTrade)">重试</button></div>
    <p v-if="klineWarning" class="hint" role="status">{{ klineWarning }}</p>
    <!-- KLineChart 独占容器，加载与错误提示不得放在其中。 -->
    <div v-show="klineTrade" :style="{visibility: klineLoading || klineErr ? 'hidden' : 'visible'}" ref="klineChart" class="chart-kline" role="img" aria-label="交易日K、成交量、MACD；B为买入、S为卖出、估为期末估值，准确时间与价格见上方"></div>
  </section>
</div>
  `,
};
