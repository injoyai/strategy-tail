import { Api } from '../api.js';

const mainIndexCodes = new Set(['sh000001', 'sz399001', 'sz399006', 'sh000300']);
const exchangeNames = {sh: '沪市', sz: '深市', bj: '北交所'};
const breadthFields = [
  {key: 'down', label: '下跌'},
  {key: 'flat', label: '平盘'},
  {key: 'up', label: '上涨'},
];
const sectorGroups = [
  {key: 'industries', label: '行业', tableLabel: '行业涨幅排行'},
  {key: 'concepts', label: '概念', tableLabel: '概念涨幅排行'},
];
const distributionLabels = ['<−5%', '−5%～−3%', '−3%～−1%', '−1%～0%', '0%～1%', '1%～3%', '3%～5%', '>5%'];
const rankColumns = {
  close: {label: '收盘（元）', ascending: 'closeAsc', descending: 'closeDesc'},
  change: {label: '涨跌幅', ascending: 'changeAsc', descending: 'changeDesc'},
  amount: {label: '成交额（亿元）', ascending: 'amountAsc', descending: 'amount'},
};

const integer = new Intl.NumberFormat('zh-CN');
const decimal = new Intl.NumberFormat('zh-CN', {minimumFractionDigits: 2, maximumFractionDigits: 2});
const percent = new Intl.NumberFormat('zh-CN', {style: 'percent', maximumFractionDigits: 1, minimumFractionDigits: 1});
const marketClock = new Intl.DateTimeFormat('zh-CN', {timeZone: 'Asia/Shanghai', hourCycle: 'h23',
  hour: '2-digit', minute: '2-digit', second: '2-digit'});

export default {
  name: 'MarketDashboard',
  props: {active: Boolean},
  data() {
    return {snapshot: null, request: null, timer: null, loading: false, error: '',
      sectorSnapshot: null, sectorRequest: null, sectorError: false, sectorGroup: sectorGroups[0],
      sortKey: 'amount', sortDirection: 'descending', breadthFields, rankColumns, sectorGroups};
  },
  computed: {
    mainIndexes() {
      return this.snapshot?.indexes?.filter(index => mainIndexCodes.has(index.code)) ?? [];
    },
    stocks() {
      return this.snapshot?.stocks;
    },
    comparableCount() {
      const {up = 0, flat = 0, down = 0} = this.stocks ?? {};
      return up + flat + down;
    },
    upRate() {
      return this.comparableCount ? percent.format(this.stocks.up / this.comparableCount) : '—';
    },
    breadthBarStyle() {
      const down = 100 * this.stocks.down / this.comparableCount;
      const flat = 100 * (this.stocks.down + this.stocks.flat) / this.comparableCount;
      return {background: `linear-gradient(to right, var(--color-down) ${down}%, var(--muted) ${down}% ${flat}%, var(--color-up) ${flat}%)`};
    },
    distribution() {
      const counts = this.stocks?.changeDistribution;
      if (!this.comparableCount || counts?.length !== distributionLabels.length) return [];
      const maxCount = Math.max(1, ...counts);
      return distributionLabels.map((label, index) => ({
        label, count: counts[index], height: 100 * counts[index] / maxCount + '%',
        side: index < 4 ? 'down' : 'up',
      }));
    },
    rankedStocks() {
      const field = rankColumns[this.sortKey][this.sortDirection];
      return this.stocks?.rankings?.[field] ?? [];
    },
    rankedSectors() {
      return this.sectorSnapshot?.[this.sectorGroup.key] ?? [];
    },
    intraday() {
      return this.snapshot?.source === 'tdx';
    },
    marketLabel() {
      if (!this.intraday) return '日线';
      return this.snapshot.phase === 'trading' && !this.snapshot.notice && !this.error ? '实时行情' : '盘中快照';
    },
    sectorStatus() {
      if (this.sectorError) return this.sectorSnapshot ? '板块行情暂未更新，显示上次报价。' : '板块行情暂不可用。';
      return this.sectorSnapshot?.notice ?? '';
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
    sortRanks(key) {
      this.sortDirection = this.sortKey === key && this.sortDirection === 'descending' ? 'ascending' : 'descending';
      this.sortKey = key;
      this.$refs.stockRanks?.scrollTo(0, 0);
    },
    sortArrow(key) {
      if (this.sortKey !== key) return '↕';
      return this.sortDirection === 'ascending' ? '↑' : '↓';
    },
    selectSectorGroup(group) {
      if (group.key === this.sectorGroup.key) return;
      this.sectorGroup = group;
      this.$refs.sectorRanks?.scrollTo(0, 0);
    },
    onVisibilityChange() {
      if (document.hidden) this.stopUpdates();
      else if (this.active) this.load();
    },
    stopUpdates() {
      clearTimeout(this.timer);
      this.timer = null;
      this.request?.abort();
      this.request = null;
      this.sectorRequest?.abort();
      this.sectorRequest = null;
      this.loading = false;
    },
    async load() {
      if (!this.active || document.hidden || this.loading) return;
      clearTimeout(this.timer);
      this.timer = null;
      const request = new AbortController();
      this.request = request;
      this.loading = true;
      this.loadSectors();
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
    async loadSectors() {
      if (this.sectorRequest) return;
      const request = new AbortController();
      this.sectorRequest = request;
      try {
        const snapshot = await Api.marketDashboardSectors(request.signal);
        if (this.sectorRequest === request) {
          this.sectorSnapshot = snapshot;
          this.sectorError = false;
        }
      } catch {
        if (this.sectorRequest === request) this.sectorError = true;
      } finally {
        if (this.sectorRequest === request) this.sectorRequest = null;
      }
    },
    exchangeName(code) { return exchangeNames[code]; },
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
<section class="market-dashboard" aria-label="A 股行情" :aria-busy="String(loading && !snapshot)">
  <section class="card market-index-panel" aria-labelledby="market-index-title">
    <div class="market-panel-header">
      <h2 id="market-index-title">主要指数</h2>
      <div v-if="snapshot?.date" class="market-session" aria-label="行情时间">
        <span>{{ marketLabel }}</span>
        <time :datetime="snapshot.date">{{ snapshot.date }}</time>
        <span v-if="intraday">通达信</span>
        <span v-if="intraday && snapshot.capturedAt">
          采集于 <time :datetime="snapshot.capturedAt">{{ captured(snapshot.capturedAt) }}</time>
        </span>
        <span v-if="intraday && snapshot.phase === 'break'">午间休市</span>
      </div>
    </div>
    <div v-if="error" class="market-feedback">
      <p class="msg err" role="alert">{{ error }}<span v-if="snapshot">。仍显示上次读取的行情。</span></p>
      <button type="button" class="secondary" :disabled="loading" @click="load">重试读取</button>
    </div>
    <p v-else-if="snapshot?.notice" class="hint market-feedback" role="status">{{ snapshot.notice }}</p>
    <p v-else-if="snapshot && !snapshot.date" class="hint market-feedback" role="status">行情库暂无上证指数交易日记录。</p>

    <div class="market-index-grid">
      <div class="market-index-quote" v-for="index in mainIndexes" :key="index.code">
        <div class="market-index-name"><strong>{{ index.name }}</strong><span>{{ index.code }}</span></div>
        <div class="market-index-numbers">
          <strong>{{ price(index.close) }}</strong>
          <div class="market-index-meta">
            <span v-if="index.date" :class="direction(index.change)">{{ change(index.change) }}</span>
            <time v-if="index.date && index.date !== snapshot.date" :datetime="index.date">{{ index.date }}</time>
            <span v-else-if="!index.date" class="market-index-missing">{{ intraday ? '暂无实时报价' : '暂无日线' }}</span>
          </div>
        </div>
      </div>
      <p v-if="!mainIndexes.length" class="empty market-index-empty" role="status">{{ loading ? '正在读取行情…' : '暂无指数行情' }}</p>
    </div>
  </section>

  <div class="market-panels">
    <div class="market-overview">
      <section class="card market-distribution-panel" aria-labelledby="market-distribution-title">
        <div class="market-panel-header">
          <h2 id="market-distribution-title">涨跌分布</h2>
          <span v-if="comparableCount" class="hint">可比较 {{ count(comparableCount) }} 只</span>
        </div>
        <template v-if="comparableCount">
          <dl class="market-breadth-legend">
            <div v-for="item in breadthFields" :key="item.key">
              <dt>{{ item.label }}</dt>
              <dd :data-side="item.key">{{ count(stocks[item.key]) }}</dd>
            </div>
            <div>
              <dt>上涨率</dt>
              <dd>{{ upRate }}</dd>
            </div>
          </dl>
          <div class="market-breadth-bar" :style="breadthBarStyle" aria-hidden="true"></div>
        </template>
        <ul v-if="distribution.length" class="market-distribution" role="list" aria-label="各涨跌幅区间股票数">
          <li v-for="bucket in distribution" :key="bucket.label">
            <span class="market-bucket-label">{{ bucket.label }}</span>
            <div class="market-bucket-plot">
              <div class="market-bucket-bar" :style="{height: bucket.height}" :data-side="bucket.side"
                :data-empty="bucket.count === 0">
                <span>{{ count(bucket.count) }}<span class="market-sr-only"> 只</span></span>
              </div>
            </div>
          </li>
        </ul>
        <p v-else class="empty">{{ loading ? '正在读取分布数据' : '暂无涨跌分布数据' }}</p>
        <p v-if="stocks?.unclassified" class="hint">缺少可比价格 {{ count(stocks.unclassified) }} 只</p>
      </section>

      <section class="card market-breadth-panel" aria-labelledby="market-breadth-title">
        <h2 id="market-breadth-title">沪深北成交概况</h2>
        <template v-if="stocks?.total">
          <dl class="market-totals">
            <div><dt>成交额</dt><dd>{{ amount(stocks.amount) }} <small v-if="stocks.withAmount">亿元</small></dd></div>
            <div><dt>有成交股票</dt><dd>{{ count(stocks.total) }} <small>只</small></dd></div>
          </dl>
          <div class="market-table-wrap">
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
          <p class="hint" v-if="stocks.withAmount < stocks.total">成交额缺失 {{ count(stocks.total - stocks.withAmount) }} 只，成交额合计和排序按有效金额统计。</p>
        </template>
        <p v-else class="empty">{{ loading ? '正在读取成交概况' : '暂无当日有效成交记录' }}</p>
      </section>
    </div>

    <section class="card market-sector-panel" aria-labelledby="market-sectors-title">
      <div class="market-panel-header">
        <h2 id="market-sectors-title">热门板块</h2>
        <div class="market-sector-groups" role="group" aria-label="板块类别">
          <button v-for="group in sectorGroups" :key="group.key" type="button" class="secondary"
            :aria-pressed="group.key === sectorGroup.key" @click="selectSectorGroup(group)">{{ group.label }}</button>
        </div>
      </div>
      <div v-if="rankedSectors.length" ref="sectorRanks" class="market-table-wrap market-rank-scroll"
        role="region" aria-label="板块排行滚动区" tabindex="0">
        <table :aria-label="sectorGroup.tableLabel">
          <thead>
            <tr><th scope="col">排名</th><th scope="col">板块</th><th scope="col" aria-sort="descending">涨跌幅</th><th scope="col" title="含当日的五个交易日名次之和，越小排名越高">五日排名</th></tr>
          </thead>
          <tbody>
            <tr v-for="(sector, i) in rankedSectors" :key="sector.code">
              <td>{{ i + 1 }}</td>
              <th scope="row">
                <stock-chart-button :code="sector.code" :name="sector.name" class="secondary trade-kline-button" />
              </th>
              <td :class="direction(sector.change)">{{ change(sector.change) }}</td>
              <td :title="sector.fiveDaySum == null ? '五日数据不足' : '五日名次合计：' + sector.fiveDaySum">{{ sector.fiveDayRank ?? '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-else class="empty" role="status">
        {{ sectorSnapshot ? '当前类别暂无有效报价' : (sectorStatus || '正在读取板块行情…') }}
      </p>
      <p v-if="sectorSnapshot" class="hint">
        通达信 <time :datetime="sectorSnapshot.date">{{ sectorSnapshot.date }}</time> · 五日排名含当日，缺失显示 —
        <span v-if="sectorSnapshot.received < sectorSnapshot.expected">
          · 覆盖 {{ count(sectorSnapshot.received) }} / {{ count(sectorSnapshot.expected) }}
        </span>
      </p>
      <p v-if="sectorSnapshot && sectorStatus" class="hint" role="status">{{ sectorStatus }}</p>
    </section>

    <section class="card market-rank-panel" aria-labelledby="market-rank-title">
      <div class="market-panel-header">
        <h2 id="market-rank-title">个股排行</h2>
      </div>
      <div v-if="stocks?.total" ref="stockRanks" class="market-table-wrap market-ranking market-rank-scroll"
        role="region" aria-label="个股排行滚动区" tabindex="0">
        <table aria-label="当前个股排行">
          <thead>
            <tr>
              <th scope="col">排名</th><th scope="col">股票</th>
              <th v-for="(column, key) in rankColumns" :key="key" scope="col" :aria-sort="key === sortKey ? sortDirection : undefined">
                <button type="button" class="pf-sortbtn" @click="sortRanks(key)">
                  <span>{{ key === 'close' && intraday ? '最新价（元）' : column.label }}</span>
                  <span class="market-sort-arrow" aria-hidden="true">{{ sortArrow(key) }}</span>
                </button>
              </th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(row, i) in rankedStocks" :key="row.code">
              <td>{{ i + 1 }}</td>
              <th scope="row"><stock-chart-button :code="row.code" class="secondary trade-kline-button" /></th>
              <td>{{ price(row.close) }}</td>
              <td :class="direction(row.change)">{{ change(row.change) }}</td>
              <td>{{ amount(row.amount) }}</td>
            </tr>
            <tr v-if="!rankedStocks.length">
              <td colspan="5" class="market-rank-empty">当前排序列暂无数据</td>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-else class="empty">{{ loading ? '正在读取个股排行' : '当前榜单暂无数据' }}</p>
      <p v-if="stocks?.total" class="hint">
        {{ intraday ? '涨跌幅较行情昨收' : '涨跌幅按未复权收盘价计算' }}<span v-if="snapshot.previousDate">，对比 {{ snapshot.previousDate }}</span>。
      </p>
    </section>
  </div>
</section>
  `,
};
