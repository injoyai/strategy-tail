import { Api } from '../api.js';
import { stockLabel } from '../format.js';
import { state } from '../store.js';

const exchangeNames = {sh: '沪市', sz: '深市', bj: '北交所'};
const breadthFields = [
  {key: 'up', label: '上涨'},
  {key: 'flat', label: '平盘'},
  {key: 'down', label: '下跌'},
  {key: 'unclassified', label: '缺少可比价格'},
];

const integer = new Intl.NumberFormat('zh-CN');
const decimal = new Intl.NumberFormat('zh-CN', {minimumFractionDigits: 2, maximumFractionDigits: 2});
const marketClock = new Intl.DateTimeFormat('zh-CN', {timeZone: 'Asia/Shanghai', hourCycle: 'h23',
  hour: '2-digit', minute: '2-digit', second: '2-digit'});

export default {
  name: 'MarketDashboard',
  props: {active: Boolean},
  data() {
    return {snapshot: null, request: null, timer: null, loading: false, error: '', ranking: 'amount', limit: 10, breadthFields};
  },
  computed: {
    stocks() {
      return this.snapshot?.stocks;
    },
    rankedStocks() {
      return this.stocks?.rankings[this.ranking].slice(0, this.limit) ?? [];
    },
    intraday() {
      return this.snapshot?.source === 'tdx';
    },
    marketLabel() {
      if (!this.intraday) return 'A 股日线';
      return this.snapshot.phase === 'trading' && !this.snapshot.notice && !this.error ? 'A 股实时行情' : 'A 股盘中快照';
    },
  },
  watch: {
    active: {
      immediate: true,
      handler(active) {
        if (active) this.load();
        else this.stopUpdates();
      },
    },
  },
  mounted() {
    document.addEventListener('visibilitychange', this.onVisibilityChange);
  },
  beforeUnmount() {
    document.removeEventListener('visibilitychange', this.onVisibilityChange);
    this.stopUpdates();
  },
  methods: {
    onVisibilityChange() {
      if (document.hidden) this.stopUpdates();
      else if (this.active) this.load();
    },
    stopUpdates() {
      clearTimeout(this.timer);
      this.timer = null;
      this.request?.abort();
      this.request = null;
      this.loading = false;
    },
    async load() {
      if (!this.active || document.hidden || this.loading) return;
      clearTimeout(this.timer);
      this.timer = null;
      const request = new AbortController();
      this.request = request;
      this.loading = true;
      let delay = 0;
      try {
        const snapshot = await Api.marketDashboard(request.signal);
        if (this.request === request) {
          this.snapshot = snapshot;
          this.error = '';
          delay = snapshot.refreshAfterMs;
        }
      } catch (err) {
        if (this.request === request) {
          this.error = err.message || '行情读取失败，请重试';
          delay = this.snapshot?.phase === 'trading' || !this.snapshot ? 15000 : this.snapshot.refreshAfterMs;
        }
      } finally {
        if (this.request === request) {
          this.request = null;
          this.loading = false;
          if (delay > 0) this.timer = setTimeout(this.load, delay);
        }
      }
    },
    exchangeName(code) { return exchangeNames[code]; },
    stockName(code) { return stockLabel(code, state.stockNames); },
    count(value) { return integer.format(value); },
    captured(value) { return marketClock.format(new Date(value)); },
    price(value) { return value == null ? '—' : decimal.format(value); },
    amount(value) { return value > 0 ? decimal.format(value / 1e8) : '—'; },
    change(value) {
      if (value == null) return '—';
      return (value > 0 ? '+' : '') + decimal.format(value) + '%';
    },
    direction(value) { return value > 0 ? 'pos' : value < 0 ? 'neg' : ''; },
  },
  template: `
<section class="market-dashboard" aria-label="A 股行情">
  <div class="market-toolbar">
    <div class="market-session">
      <strong>{{ marketLabel }}</strong>
      <time v-if="snapshot?.date" :datetime="snapshot.date">{{ snapshot.date }}</time>
      <span class="market-source">{{ intraday ? '通达信' : 'MySQL 行情库' }}</span>
      <span v-if="intraday && snapshot.capturedAt" class="market-source">
        采集于 <time :datetime="snapshot.capturedAt">{{ captured(snapshot.capturedAt) }}</time>
      </span>
      <span v-if="intraday && snapshot.phase === 'break'" class="market-source">午间休市</span>
    </div>
    <button type="button" class="secondary" :disabled="loading" @click="load">{{ error ? '重试读取' : '刷新行情' }}</button>
  </div>
  <div class="market-feedback">
    <p v-if="loading && !snapshot" class="hint" role="status">正在读取行情…</p>
    <p v-else-if="error" class="msg err" role="alert">
      {{ error }}<span v-if="snapshot">。下方仍显示上次读取的行情。</span>
    </p>
    <p v-else-if="snapshot?.notice" class="hint" role="status">{{ snapshot.notice }}</p>
    <p v-else-if="snapshot && !snapshot.date" class="hint" role="status">行情库暂无上证指数交易日记录。</p>
  </div>

  <div class="market-content" :aria-busy="String(loading && !snapshot)">
    <section class="card market-index-panel" aria-labelledby="market-index-title">
      <h2 id="market-index-title">主要指数</h2>
      <div class="market-index-grid">
        <div class="market-index-quote" v-for="index in snapshot?.indexes ?? []" :key="index.code">
          <div class="market-index-name"><strong>{{ index.name }}</strong><span>{{ index.code }}</span></div>
          <div class="market-index-numbers">
            <strong>{{ price(index.close) }}</strong>
            <span :class="direction(index.change)">{{ change(index.change) }}</span>
          </div>
          <p v-if="!index.date || index.date !== snapshot.date" class="hint market-index-date">
            <time v-if="index.date" :datetime="index.date">{{ index.date }}</time>
            <span v-else>{{ intraday ? '暂无实时报价' : '暂无日线' }}</span>
          </p>
        </div>
        <div v-if="!snapshot" class="empty market-index-empty">{{ loading ? '正在读取指数行情' : '暂无指数行情' }}</div>
      </div>
    </section>

    <div class="market-panels">
      <section class="card market-breadth-panel" aria-labelledby="market-breadth-title">
        <h2 id="market-breadth-title">沪深北成交概况</h2>
        <template v-if="stocks?.total">
          <dl class="market-totals">
            <div><dt>成交额</dt><dd>{{ amount(stocks.amount) }} <small v-if="stocks.withAmount">亿元</small></dd></div>
            <div><dt>有成交股票</dt><dd>{{ count(stocks.total) }} <small>只</small></dd></div>
          </dl>
          <div class="market-breadth-bar" aria-hidden="true">
            <span v-for="item in breadthFields" :key="item.key" :data-side="item.key" :style="{flexGrow: stocks[item.key]}"></span>
          </div>
          <dl class="market-breadth-legend">
            <div v-for="item in breadthFields" :key="item.key">
              <dt :class="direction(item.key === 'up' ? 1 : item.key === 'down' ? -1 : 0)">{{ item.label }}</dt>
              <dd>{{ count(stocks[item.key]) }}</dd>
            </div>
          </dl>
          <div class="detail-wrap market-table-wrap" role="region" aria-label="分市场成交概况" tabindex="0">
            <table aria-label="沪深北各市场成交额和涨跌家数">
              <thead><tr><th scope="col">市场</th><th scope="col">成交额（亿元）</th><th scope="col">上涨</th><th scope="col">下跌</th></tr></thead>
              <tbody>
                <tr v-for="row in stocks.exchanges" :key="row.exchange">
                  <th scope="row">{{ exchangeName(row.exchange) }}</th>
                  <td>{{ amount(row.amount) }}</td>
                  <td class="pos">{{ row.total ? count(row.up) : '—' }}</td>
                  <td class="neg">{{ row.total ? count(row.down) : '—' }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <p class="hint" v-if="intraday && snapshot.stockQuotes < snapshot.stockUniverse">
            收到 {{ count(snapshot.stockQuotes) }} / {{ count(snapshot.stockUniverse) }} 只股票报价，成交概况和榜单按已收到的有效数据统计。
          </p>
          <p class="hint" v-if="stocks.withAmount < stocks.total">成交额缺失 {{ count(stocks.total - stocks.withAmount) }} 只，合计及榜单按有效金额统计。</p>
        </template>
        <p v-else class="empty">{{ loading ? '正在读取成交概况' : '暂无当日有效成交记录' }}</p>
      </section>

      <section class="card market-rank-panel" aria-labelledby="market-rank-title">
        <div class="market-rank-toolbar">
          <h2 id="market-rank-title">个股排行</h2>
          <div class="filters market-rank-filters">
            <label>
              <span class="market-sr-only">榜单类型</span>
              <select v-model="ranking">
                <option value="amount">成交额榜</option>
                <option value="up">涨幅榜</option>
                <option value="down">跌幅榜</option>
              </select>
            </label>
            <label>
              <span class="market-sr-only">显示名次</span>
              <select v-model.number="limit">
                <option :value="10">前 10 名</option>
                <option :value="20">前 20 名</option>
                <option :value="50">前 50 名</option>
              </select>
            </label>
          </div>
        </div>
        <div v-if="rankedStocks.length" class="detail-wrap market-table-wrap market-ranking" role="region" aria-label="个股排行表格" tabindex="0">
          <table aria-label="当前个股排行">
            <thead>
              <tr>
                <th scope="col">排名</th><th scope="col">股票</th><th scope="col">{{ intraday ? '最新价（元）' : '收盘（元）' }}</th>
                <th scope="col" :aria-sort="ranking === 'up' ? 'descending' : ranking === 'down' ? 'ascending' : undefined">涨跌幅</th>
                <th scope="col" :aria-sort="ranking === 'amount' ? 'descending' : undefined">成交额（亿元）</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(row, i) in rankedStocks" :key="row.code">
                <td>{{ i + 1 }}</td>
                <th scope="row">{{ stockName(row.code) }}</th>
                <td>{{ price(row.close) }}</td>
                <td :class="direction(row.change)">{{ change(row.change) }}</td>
                <td>{{ amount(row.amount) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-else class="empty">{{ loading ? '正在读取个股排行' : '当前榜单暂无数据' }}</p>
        <p v-if="stocks?.total" class="hint">
          {{ count(rankedStocks.length) }} 只 · {{ intraday ? '涨跌幅按实时行情的昨收计算' : '个股涨跌幅按未复权收盘价计算' }}<span v-if="snapshot.previousDate">，对比 {{ snapshot.previousDate }}</span>。
        </p>
      </section>
    </div>
  </div>
</section>
  `,
};
