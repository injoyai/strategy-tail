import { Api } from '../api.js';
import { state } from '../store.js';
import { dlgMixin } from './dlgMixin.js';
import { NodeEdit, treeErrors } from './Tab1Builder.js';

const pageSize = 30;
const clone = value => JSON.parse(JSON.stringify(value));
const newTree = () => ({type: 'group', op: 'and', children: []});

export default {
  name: 'Tab8Screen',
  mixins: [dlgMixin],
  components: {BuilderNodeEdit: NodeEdit},
  props: {active: {type: Boolean, required: true}},
  data() {
    return {
      config: null, result: null, running: false, pending: false, loading: false, timer: null,
      loadError: '', runError: '', actionError: '', strategyFilter: '', page: 1,
      draft: null, baseline: '', draftVersion: 0, formError: '', nameError: '',
      catalogLoading: false, catalogError: '', sourceStrategies: [], sourceError: '', sourceID: '',
      deleting: false, discard: false,
    };
  },
  computed: {
    strategies() { return this.config?.strategies || []; },
    enabled() { return this.strategies.filter(strategy => strategy.enabled); },
    byID() { return new Map(this.strategies.map(strategy => [strategy.id, strategy.name])); },
    filtered() {
      const rows = this.result?.matches || [];
      return this.strategyFilter ? rows.filter(row => row.hits.some(hit => hit.id === this.strategyFilter)) : rows;
    },
    pages() { return Math.max(1, Math.ceil(this.filtered.length / pageSize)); },
    shown() { return this.filtered.slice((this.page - 1) * pageSize, this.page * pageSize); },
    dirty() { return this.open && this.draft && !this.deleting && JSON.stringify(this.draft) !== this.baseline; },
    catalogReady() { return state.builderCatalog.length > 0; },
  },
  watch: {
    active: {immediate: true, handler(value) {
      clearTimeout(this.timer);
      if (value) this.refresh();
    }},
    strategyFilter() { this.page = 1; },
    pages(pages) { this.page = Math.min(this.page, pages); },
    dirty(value) {
      if (value) window.addEventListener('beforeunload', this.warnBeforeUnload);
      else window.removeEventListener('beforeunload', this.warnBeforeUnload);
    },
  },
  beforeUnmount() {
    clearTimeout(this.timer);
    window.removeEventListener('beforeunload', this.warnBeforeUnload);
  },
  methods: {
    warnBeforeUnload(event) { event.preventDefault(); event.returnValue = ''; },
    receive({config, result, running, error}) {
      this.config = config;
      this.result = result || null;
      this.running = running;
      this.runError = error || '';
      this.loadError = '';
      if (!this.enabled.some(strategy => strategy.id === this.strategyFilter)) this.strategyFilter = '';
    },
    schedule() {
      clearTimeout(this.timer);
      if (this.active && !this.pending) this.timer = setTimeout(this.refresh, this.running ? 1500 : 10000);
    },
    async refresh() {
      if (this.loading || this.pending) return;
      this.loading = true;
      try {
        const response = await Api.screen();
        if (!this.pending) this.receive(response);
      } catch (error) {
        this.loadError = '读取选股状态失败：' + error.message;
      } finally {
        this.loading = false;
        this.schedule();
      }
    },
    async run() {
      if (this.pending || this.loading || this.running) return;
      this.pending = true;
      this.actionError = '';
      try {
        this.receive(await Api.runScreen());
      } catch (error) {
        this.actionError = error.message;
      } finally {
        this.pending = false;
        this.schedule();
      }
    },
    async save(strategies, version) {
      if (this.pending || this.loading) return false;
      this.pending = true;
      try {
        this.receive(await Api.saveScreen({version, strategies}));
        return true;
      } catch (error) {
        if (this.open) this.formError = error.message;
        else this.actionError = error.message;
        return false;
      } finally {
        this.pending = false;
        this.schedule();
      }
    },
    toggle(strategy, event) {
      const enabled = event.target.checked;
      event.target.checked = strategy.enabled;
      this.actionError = '';
      this.save(this.strategies.map(item => item.id === strategy.id ? {...item, enabled} : item), this.config.version);
    },
    async loadCatalog() {
      if (this.catalogLoading) return;
      this.catalogLoading = true;
      this.catalogError = '';
      this.sourceError = '';
      try {
        const [components, factors, saved] = await Promise.allSettled([Api.builderComponents(), Api.factors(), Api.builderStrategies()]);
        if (components.status === 'fulfilled') state.builderCatalog = components.value.components || [];
        else if (!this.catalogReady) this.catalogError = '条件目录加载失败：' + components.reason.message;
        if (factors.status === 'fulfilled') state.factorCatalog = factors.value;
        if (saved.status === 'fulfilled') this.sourceStrategies = (saved.value.strategies || []).filter(item => item.spec?.buy);
        else this.sourceError = '回测策略列表加载失败：' + saved.reason.message;
      } finally {
        this.catalogLoading = false;
      }
    },
    openEditor(strategy = null, deleting = false) {
      this.draft = strategy ? clone(strategy) : {id: '', name: '', enabled: false, buy: newTree()};
      this.baseline = JSON.stringify(this.draft);
      this.draftVersion = this.config.version;
      this.formError = '';
      this.nameError = '';
      this.sourceID = '';
      this.discard = false;
      this.deleting = deleting;
      this.open = true;
      this.$nextTick(() => (deleting ? this.$refs.cancel : this.$refs.name)?.focus());
      if (!deleting) this.loadCatalog();
    },
    copyConditions() {
      const source = this.sourceStrategies.find(item => item.id === this.sourceID);
      if (!source) return;
      this.draft.buy = clone(source.spec.buy);
      if (!this.draft.name.trim()) this.draft.name = source.name;
      this.sourceID = '';
    },
    requestClose() {
      if (this.pending) return;
      if (this.dirty) {
        this.discard = true;
        this.$nextTick(() => this.$refs.keepEditing?.focus());
      } else this.closeDlg();
    },
    async submit() {
      if (this.pending || this.loading || this.discard) return;
      this.formError = '';
      this.nameError = this.deleting || this.draft.name.trim() ? '' : '请输入策略名称';
      if (this.nameError) {
        this.$refs.name.focus();
        return;
      }
      if (!this.deleting) {
        const errors = treeErrors(this.draft.buy, 'buy');
        if (errors.length) {
          this.formError = errors[0];
          this.$nextTick(() => this.$refs.dlg.querySelector('.bnode [aria-invalid="true"]')?.focus());
          return;
        }
      }
      const next = this.deleting ? this.strategies.filter(item => item.id !== this.draft.id)
        : this.draft.id ? this.strategies.map(item => item.id === this.draft.id ? this.draft : item)
        : [...this.strategies, this.draft];
      if (await this.save(next, this.draftVersion)) {
        this.closeDlg();
        if (this.deleting) this.$nextTick(() => this.$refs.addStrategy?.focus());
      }
    },
    riseText(value) { return (value > 0 ? '+' : '') + value.toFixed(2) + '%'; },
  },
  template: `
<div class="screen-page">
  <div class="screen-toolbar">
    <div class="screen-status" aria-live="polite">
      <span v-if="!config && !loadError">读取选股配置…</span>
      <template v-else-if="config">
        <strong>{{ enabled.length }} 套已启用</strong><span v-if="running">扫描中…</span>
        <span v-else-if="result">扫描时间 {{ result.asOf }}</span>
      </template>
    </div>
    <div class="screen-actions">
      <button ref="addStrategy" type="button" class="secondary" :disabled="!config || pending" @click="openEditor()">新建策略</button>
      <button type="button" class="primary" :disabled="!enabled.length || pending || loading || running"
        :aria-busy="running" @click="run">立即选股</button>
    </div>
  </div>

  <p v-if="loadError" class="builder-field-error screen-message" role="alert">{{ loadError }}
    <button type="button" class="secondary" :disabled="loading || pending" @click="refresh">重试读取</button>
  </p>
  <p v-if="actionError" class="builder-field-error screen-message" role="alert">{{ actionError }}</p>
  <p v-if="runError" class="builder-field-error screen-message" role="alert">{{ runError }}</p>

  <div v-if="config" class="screen-grid">
    <section class="card screen-library" aria-labelledby="screen-library-title">
      <h2 id="screen-library-title">策略列表</h2>
      <ul class="screen-strategies">
        <li v-for="strategy in strategies" :key="strategy.id">
          <label class="screen-strategy-label">
            <input type="checkbox" :checked="strategy.enabled" :disabled="pending || loading" @change="toggle(strategy, $event)">
            <span>{{ strategy.name }}<small>{{ strategy.buy ? '自定义' : '内置' }}</small></span>
          </label>
          <div v-if="strategy.buy" class="screen-actions">
            <button type="button" class="secondary" :aria-label="'编辑 ' + strategy.name" :disabled="pending" @click="openEditor(strategy)">编辑</button>
            <button type="button" class="secondary" :aria-label="'删除 ' + strategy.name" :disabled="pending" @click="openEditor(strategy, true)">删除</button>
          </div>
        </li>
      </ul>
    </section>

    <section class="card screen-results" aria-labelledby="screen-results-title" :aria-busy="running">
      <div class="screen-result-head">
        <h2 id="screen-results-title">选股结果 <span v-if="result" class="hint">{{ result.matches.length }} 只</span></h2>
        <label v-if="result" class="screen-result-filter">策略
          <select v-model="strategyFilter">
            <option value="">全部启用策略</option>
            <option v-for="strategy in enabled" :key="strategy.id" :value="strategy.id">{{ strategy.name }}</option>
          </select>
        </label>
      </div>
      <template v-if="result">
        <p class="hint screen-coverage">已评估 {{ result.evaluated }} / {{ result.universe }} 只 · 无当日可用行情 {{ result.noSessionBars }} 只
          <span v-if="result.failed"> · 日线读取失败 {{ result.failed }} 只</span>
        </p>
        <details v-if="result.failedCodes?.length" class="ui-help"><summary>读取失败的股票</summary><p>{{ result.failedCodes.join('、') }}</p></details>
      </template>
      <div v-if="shown.length" class="screen-table-wrap" tabindex="0" aria-label="选股结果表格">
        <table class="screen-table" aria-labelledby="screen-results-title">
          <thead><tr><th scope="col">股票</th><th scope="col">价格（元）</th><th scope="col">涨跌幅</th><th scope="col">行情时间</th><th scope="col">命中策略</th></tr></thead>
          <tbody><tr v-for="row in shown" :key="row.code">
            <th scope="row"><strong>{{ row.name || row.code }}</strong><small v-if="row.name">{{ row.code }}</small></th>
            <td>{{ row.price.toFixed(2) }}</td>
            <td :class="{'num-up': row.rise > 0, 'num-down': row.rise < 0}">{{ riseText(row.rise) }}</td>
            <td><time :datetime="row.time.replace(' ', 'T') + '+08:00'">{{ row.time }}</time></td>
            <td><ul class="screen-hits"><li v-for="hit in row.hits" :key="hit.id">{{ byID.get(hit.id) }}
              <small v-if="hit.tags?.length">{{ hit.tags.join(' · ') }}</small></li></ul></td>
          </tr></tbody>
        </table>
      </div>
      <div v-else class="screen-empty" role="status">
        <span v-if="running">正在读取行情并筛选股票…</span>
        <span v-else-if="!enabled.length">启用至少一套策略后开始选股。</span>
        <span v-else-if="result && !result.evaluated">今日暂无可用于选股的行情。</span>
        <span v-else-if="result && strategyFilter">当前策略没有命中股票。</span>
        <span v-else-if="result">本次扫描没有命中股票。</span>
        <span v-else-if="runError">本次扫描未生成结果。</span>
        <span v-else>点击「立即选股」运行已启用策略。</span>
      </div>
      <div v-if="pages > 1" class="screen-pagination">
        <span>共 {{ filtered.length }} 只 · 第 {{ page }} / {{ pages }} 页</span>
        <button type="button" class="secondary" :disabled="page <= 1" @click="page--">上一页</button>
        <button type="button" class="secondary" :disabled="page >= pages" @click="page++">下一页</button>
      </div>
    </section>
  </div>

  <teleport to="body">
    <dialog ref="dlg" class="screen-editor" aria-labelledby="screen-editor-title" @cancel.prevent="requestClose">
      <form v-if="draft" novalidate @submit.prevent="submit">
        <h2 id="screen-editor-title">{{ deleting ? '删除选股策略' : draft.id ? '编辑选股策略' : '新建选股策略' }}</h2>
        <template v-if="deleting"><p>删除「{{ draft.name }}」后，该策略将从列表移除。</p></template>
        <fieldset v-else class="screen-fields" :disabled="pending || discard">
          <label class="screen-name">策略名称
            <input ref="name" v-model="draft.name" type="text" required maxlength="80" :aria-invalid="!!nameError" :aria-describedby="nameError ? 'screen-name-error' : undefined">
          </label>
          <p v-if="nameError" id="screen-name-error" class="builder-field-error">{{ nameError }}</p>
          <label class="screen-enable"><input v-model="draft.enabled" type="checkbox">保存后启用</label>
          <div v-if="!draft.id && sourceStrategies.length" class="screen-import">
            <label>从回测策略复制条件
              <select v-model="sourceID">
                <option value="">选择已保存策略</option>
                <option v-for="strategy in sourceStrategies" :key="strategy.id" :value="strategy.id">{{ strategy.name }}</option>
              </select>
            </label>
            <button type="button" class="secondary" :disabled="!sourceID" @click="copyConditions">复制买入条件</button>
          </div>
          <h3>选股条件</h3>
          <p v-if="catalogLoading && !catalogReady" class="hint" role="status">读取条件目录…</p>
          <p v-if="catalogError || sourceError" class="builder-field-error" role="alert">{{ catalogError || sourceError }}
            <button type="button" class="secondary" :disabled="catalogLoading" @click="loadCatalog">重试目录</button>
          </p>
          <builder-node-edit v-if="catalogReady" :node="draft.buy" role="buy" :parent="[]" :index="0" />
        </fieldset>
        <p v-if="formError" class="builder-field-error screen-message" role="alert">{{ formError }}</p>
        <div v-if="discard" class="screen-discard" role="group" aria-label="未保存的修改">
          <p>当前修改尚未保存。放弃修改？</p>
          <div class="screen-actions">
            <button ref="keepEditing" type="button" class="secondary" @click="discard = false; $nextTick(() => $refs.name?.focus())">继续编辑</button>
            <button type="button" class="danger" @click="closeDlg">放弃修改</button>
          </div>
        </div>
        <div v-else class="screen-actions screen-editor-actions">
          <button ref="cancel" type="button" class="secondary" :disabled="pending" @click="requestClose">取消</button>
          <button type="submit" :class="deleting ? 'danger' : 'primary'" :disabled="pending || loading || (!deleting && !catalogReady)"
            :aria-busy="pending">{{ deleting ? '删除策略' : '保存策略' }}</button>
        </div>
      </form>
    </dialog>
  </teleport>
</div>
  `,
};
