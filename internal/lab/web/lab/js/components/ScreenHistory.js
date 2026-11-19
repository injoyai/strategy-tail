import { state } from '../store.js';
import { Api } from '../api.js';
import { formatTradePerformance } from '../format.js';
import { mountTradeTable } from '../trade-table.js';
import { dlgMixin } from './dlgMixin.js';

export default {
  name: 'ScreenHistory',
  mixins: [dlgMixin],
  props: {active: Boolean, mode: {type: String, required: true}, strategy: {type: String, required: true},
    strategies: {type: Array, required: true}, revision: Number, saleRevision: Number},
  emits: ['diagnose'],
  inject: ['startTask'],
  data() {
    return {data: null, loading: false, error: '', rebuildError: '', lookback: '', busy: false, seq: 0, timer: null,
      page: 1, days: '', status: '', code: '', search: '', tag: '', tagSearch: '',
      table: null, tableError: ''};
  },
  computed: {
    taskRunning() { return state.taskRunning; },
    selectedStrategy() { return this.strategies.find(item => item.id === this.strategy); },
    canRebuild() { return !!this.selectedStrategy && !this.sellMode && !this.busy && !this.taskRunning; },
    query() { return {mode: this.mode, ...(this.sellMode ? {page: this.page} : {}),
      strategy: this.strategy, days: this.days, status: this.status, code: this.code, tag: this.tag}; },
    stats() { return this.data?.stats; },
    performance() { return formatTradePerformance(this.stats); },
    sellMode() { return this.mode === 'sell'; },
  },
  watch: {
    active(value) {
      this.seq++;
      this.loading = false;
      clearTimeout(this.timer);
      if (value) this.load();
      else {
        this.closeDlg();
        this.dropTable();
      }
    },
    revision() { if (this.active) this.load(); },
    saleRevision() { if (this.active) this.load(); },
    strategy() {
      this.closeDlg();
      this.data = null;
      this.error = '';
      this.page = 1;
      this.dropTable();
    },
    mode() {
      this.closeDlg();
      this.data = null;
      this.error = '';
      this.status = '';
      this.days = '';
      this.tag = '';
      this.tagSearch = '';
      this.page = 1;
    },
    query() { if (this.active) this.load(); },
  },
  mounted() { if (this.active) this.load(); },
  beforeUnmount() {
    this.seq++;
    clearTimeout(this.timer);
    this.dropTable();
  },
  methods: {
    dropTable() {
      this.table?.destroy();
      this.table = null;
    },
    syncTable() {
      if (!this.$refs.table || !this.active) return;
      try {
        if (!this.table) this.table = mountTradeTable(this.$refs.table, row => this.$emit('diagnose', row),
          error => { this.tableError = error.message; }, {screen: true});
        const names = new Map(this.strategies.map(item => [item.id, item.name]));
        this.table.setData((this.data?.rows || []).map(row => ({
          ...row, _rowId: [row.strategy, row.code, row.buyTime].join('/'),
          variant: row.strategyName || names.get(row.strategy) || row.strategy,
          stockLabel: [row.code, row.name].filter(Boolean).join(' '),
          tagText: row.tags?.join(' · ') || '',
          lastPrice: row.lastPrice || null, sellPrice: row.sellTime ? row.sellPrice : null,
          rate: row.sellTime || row.lastPrice > 0 ? row.rate : null,
        })));
        this.tableError = '';
      } catch (error) {
        this.tableError = error.message;
      }
    },
    async load(reportFailure = false) {
      clearTimeout(this.timer);
      const seq = ++this.seq;
      this.loading = true;
      try {
        const next = await Api.screenHistory(this.query);
        if (seq !== this.seq) return;
        this.error = '';
        this.data = next;
        if (this.sellMode && this.page !== next.page) this.page = next.page;
        await this.$nextTick();
        if (seq === this.seq) this.syncTable();
      } catch (error) {
        if (seq === this.seq) {
          this.error = error.message;
          if (reportFailure === true) throw error;
        }
      } finally {
        if (seq === this.seq) {
          this.loading = false;
          if (this.active) this.timer = setTimeout(this.load, 10000);
        }
      }
    },
    openRebuild() {
      if (!this.canRebuild) return;
      this.lookback = '';
      this.rebuildError = '';
      this.open = true;
    },
    cancelRebuild() {
      if (!this.busy) this.closeDlg();
    },
    async rebuild() {
      if (!this.canRebuild) return;
      this.busy = true;
      this.rebuildError = '';
      try {
        await Api.refreshScreenHistory(this.selectedStrategy.id, this.lookback);
        this.closeDlg();
        this.startTask('screen_history', {phase: 'preparing'});
        if (this.active && !this.sellMode) await this.load();
      } catch (error) {
        this.rebuildError = error.message;
      } finally {
        this.busy = false;
      }
    },
    applySearch() {
      this.page = 1;
      this.code = this.search.trim();
      this.tag = this.tagSearch.trim();
    },
    clearSearch(field) {
      this[field] = '';
      this.applySearch();
      this.$refs[field]?.focus();
    },
  },
  template: `
<section v-show="active" class="card screen-results" :class="{'has-rows': data?.total}" :aria-busy="loading || !!data?.running">
  <div class="screen-results-toolbar">
    <form class="screen-history-filters" novalidate @submit.prevent="applySearch">
      <label v-if="!sellMode">买入日期 <select v-model="days"><option value="">全部</option>
        <option v-for="days in [15,30,60,120,240]" :key="days" :value="days">近 {{ days }} 天</option>
      </select></label>
      <label v-if="!sellMode">状态 <select v-model="status"><option value="">全部</option>
        <option value="sold">已卖出</option><option value="open">持有</option>
      </select></label>
      <label>股票 <input ref="search" v-model="search" type="text" placeholder="代码或名称"></label>
      <button v-if="search" type="button" class="secondary" aria-label="清除股票筛选" @click="clearSearch('search')">清除</button>
      <label v-if="!sellMode">标签 <input ref="tagSearch" v-model="tagSearch" type="text" placeholder="完整标签"></label>
      <button v-if="!sellMode && tagSearch" type="button" class="secondary" aria-label="清除标签筛选" @click="clearSearch('tagSearch')">清除</button>
      <button type="submit" class="secondary">筛选</button>
    </form>
    <button v-if="!sellMode" type="button" class="secondary screen-rebuild" aria-haspopup="dialog"
      :title="selectedStrategy ? undefined : '请先从左侧选择策略'"
      :disabled="!canRebuild" @click="openRebuild">重算历史</button>
  </div>
  <div v-if="stats?.total || stats?.unpriced || (selectedStrategy && data?.through)" class="screen-summary">
    <p v-if="selectedStrategy && data?.through" class="hint">上次重算截至 {{ data.through }} · 回看 {{ data.lookback }} 个交易日</p>
    <p v-if="stats?.total || stats?.unpriced" class="hint" :class="{'screen-history-performance': !sellMode}" role="status">
      已卖出 {{ stats.closed ?? stats.total }} 笔
      <template v-if="stats.marked"> · 持仓估值 {{ stats.marked }} 笔</template>
      <template v-if="stats.unpriced"> · 缺价 {{ stats.unpriced }} 笔</template>
      · 胜率 <strong>{{ performance.winRate }}</strong> · 盈亏比 <strong>{{ performance.profitFactor }}</strong>
      <template v-if="stats.closed ?? stats.total"> · 平均持有 {{ data.avgHoldingDays.toFixed(1) }} 天</template>
      · 未扣交易成本
    </p>
  </div>
  <p v-if="data?.archiveError" class="builder-field-error" role="alert">{{ data.archiveError }}</p>
  <p v-if="data?.priceError" class="builder-field-error" role="status">{{ data.priceError }}</p>
  <p v-if="error" class="builder-field-error" role="alert">{{ error }} <button type="button" class="secondary" @click="load">重试读取</button></p>
  <p v-if="tableError" class="builder-field-error" role="alert">{{ tableError }} <button type="button" class="secondary" @click="syncTable">重试表格</button></p>
  <details v-if="selectedStrategy && data?.failedCodes?.length" class="ui-help">
    <summary>{{ data.failedCodes.length }} 只股票行情读取失败</summary>
    <p><template v-for="(code, index) in data.failedCodes" :key="code">{{ index ? '、' : '' }}<stock-chart-button :code="code" /></template></p>
  </details>
  <div v-show="data?.total > 0" ref="table" class="trade-table screen-signal-table" aria-label="策略信号表格"></div>
  <p v-if="!data?.total && !data?.running && !error" class="screen-empty" role="status">{{ loading ? '正在读取记录…' : sellMode ? '今日暂无已入库的卖点。' : '当前条件下暂无交易记录。' }}</p>
  <div v-if="sellMode && data?.total" class="screen-pagination">
    <span>共 {{ data.total }} 条 · 第 {{ data.page }} / {{ data.pages }} 页</span>
    <button type="button" class="secondary" :disabled="page <= 1 || loading" @click="page--">上一页</button>
    <button type="button" class="secondary" :disabled="page >= data.pages || loading" @click="page++">下一页</button>
  </div>
</section>
<teleport to="body">
  <dialog ref="dlg" aria-labelledby="screen-rebuild-title" aria-describedby="screen-rebuild-scope" @cancel.prevent="cancelRebuild">
    <div class="dlg-head"><h3 id="screen-rebuild-title">重算历史</h3></div>
    <form novalidate @submit.prevent="rebuild" :aria-busy="busy">
      <p id="screen-rebuild-scope" class="hint">本次仅重算「{{ selectedStrategy?.name }}」的历史交易。</p>
      <div class="form-row">
        <label for="screen-rebuild-range">回算范围</label>
        <select id="screen-rebuild-range" v-model="lookback" autofocus :disabled="busy" @change="rebuildError = ''">
          <option value="">{{ data?.defaultLookback ? '最近 ' + data.defaultLookback + ' 个交易日（默认）' : '默认范围' }}</option>
          <option v-for="days in [15, 30, 60, 120, 240]" :key="days" :value="days">最近 {{ days }} 个交易日</option>
        </select>
      </div>
      <p v-if="rebuildError" class="builder-field-error" role="alert">{{ rebuildError }}</p>
      <div class="screen-actions screen-editor-actions">
        <button type="button" class="secondary" :disabled="busy" @click="cancelRebuild">取消</button>
        <button type="submit" class="primary" :disabled="!canRebuild">开始重算</button>
      </div>
    </form>
  </dialog>
</teleport>
  `,
};
