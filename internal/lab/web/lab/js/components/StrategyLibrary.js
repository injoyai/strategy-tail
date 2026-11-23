// 策略库：已保存搭建器策略的独立列表页。
// 列表读取 GET /api/builder/strategies（含 spec 快照）；加载编辑进入 01 自由搭建，
// 运行使用库中快照且不改写草稿，复制创建新身份，删除走独立接口且不可恢复。
import { state } from '../store.js';
import { Api } from '../api.js';
import { ACCOUNT_CONFIG } from '../config.js';
import { dlgMixin } from './dlgMixin.js';

export default {
  name: 'StrategyLibrary',
  mixins: [dlgMixin],
  inject: ['switchTab', 'startTask', 'openRunConfig'],
  setup() { return {state}; },
  data() {
    return {
      strategies: [],
      loading: false,
      loadError: '',
      actionError: '',
      msg: {text: '', ok: true},
      filter: '',
      busyId: '',
      pendingDelete: null,
      pendingLoad: null,
      seq: 0,
    };
  },
  computed: {
    filtered() {
      const q = this.filter.trim().toLowerCase();
      if (!q) return this.strategies;
      return this.strategies.filter(s => (s.name + ' ' + s.id).toLowerCase().includes(q));
    },
    draftDirty() {
      return !!state.builderSpec && JSON.stringify(state.builderSpec) !== state.builderBaseline;
    },
  },
  mounted() { this.load(); },
  methods: {
    async load() {
      const seq = ++this.seq;
      this.loading = true;
      this.loadError = '';
      try {
        const res = await Api.builderStrategies();
        if (seq !== this.seq) return;
        this.strategies = res.strategies || [];
        state.builderStrategies = this.strategies;
      } catch (e) {
        if (seq === this.seq) this.loadError = '策略列表读取失败：' + e.message;
      } finally {
        if (seq === this.seq) this.loading = false;
      }
    },
    updatedAt(s) {
      const d = new Date(s.updatedAt);
      return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', {hour12: false});
    },
    setMsg(text, ok = true) { this.msg = {text, ok}; },
    requestLoad(s) {
      if (this.busyId) return;
      if (this.draftDirty) {
        this.pendingLoad = s;
        this.open = true;
        return;
      }
      this.loadIntoEditor(s);
    },
    async loadIntoEditor(s) {
      if (this.busyId) return;
      this.busyId = s.id;
      this.actionError = '';
      try {
        const st = (await Api.builderStrategy(s.id)).strategy;
        state.builderSpec = st.spec;
        state.builderId = st.id;
        state.builderVersion = st.version;
        state.builderBaseline = JSON.stringify(st.spec);
        state.runMode = 'builder';
        state.builderMsg = {text: `已加载「${st.name}」（版本 ${st.version}）：修改后运行不影响库中定义。`, ok: true};
        this.switchTab(1);
      } catch (e) {
        this.actionError = '加载失败：' + e.message;
      } finally {
        this.busyId = '';
      }
    },
    replaceDraft() {
      const s = this.pendingLoad;
      this.open = false;
      this.pendingLoad = null;
      if (s) this.loadIntoEditor(s);
    },
    async copy(s) {
      if (this.busyId) return;
      this.busyId = s.id;
      this.actionError = '';
      try {
        const spec = JSON.parse(JSON.stringify(s.spec));
        const suffix = '（副本）';
        const base = Array.from(s.name);
        spec.name = (base.length + suffix.length > 80 ? base.slice(0, 80 - suffix.length).join('') : s.name) + suffix;
        const st = (await Api.createBuilderStrategy(spec)).strategy;
        this.setMsg(`已复制为「${st.name}」（版本 ${st.version}）`);
        await this.load();
      } catch (e) {
        this.actionError = '复制失败：' + e.message;
      } finally {
        this.busyId = '';
      }
    },
    runStrategy(s) {
      if (state.taskRunning || this.busyId) return;
      const spec = JSON.parse(JSON.stringify(s.spec));
      this.openRunConfig('builder', () => this.submitRun(spec, s.name));
    },
    async submitRun(spec, name) {
      const c = state.runCfg;
      const cfg = {startYear: +c.startYear, endYear: +c.endYear, sampleMode: c.sampleMode,
        builder: spec, account: ACCOUNT_CONFIG};
      if (c.sampleMode === 'random') cfg.sampleSize = +c.sampleSize;
      if (c.sampleMode === 'codes') {
        cfg.sampleCodes = c.sampleCodes.split(',').map(x => x.trim()).filter(Boolean);
        if (!cfg.sampleCodes.length) { this.setMsg('指定代码样本为空', false); return false; }
      }
      try {
        await Api.runBuilder(cfg);
        this.setMsg(`已提交「${name}」回测，进度见任务卡。`);
        this.startTask('backtest');
        return true;
      } catch (e) {
        this.setMsg(e.message, false);
        return false;
      }
    },
    requestDelete(s) {
      if (this.busyId) return;
      this.pendingDelete = s;
      this.open = true;
    },
    async confirmDelete() {
      const s = this.pendingDelete;
      if (!s || this.busyId) return;
      this.busyId = s.id;
      this.actionError = '';
      this.open = false;
      try {
        await Api.deleteBuilderStrategy(s.id);
        this.pendingDelete = null;
        if (state.builderId === s.id) {
          state.builderId = '';
          state.builderVersion = 0;
          state.builderBaseline = JSON.stringify(state.builderSpec);
          state.builderMsg = {text: '当前编辑的策略已从策略库删除；再次保存会创建新策略。', ok: true};
        }
        this.setMsg(`已删除「${s.name}」`);
        await this.load();
      } catch (e) {
        this.actionError = '删除失败：' + e.message;
      } finally {
        this.busyId = '';
      }
    },
    closeDlg() {
      this.open = false;
      this.pendingDelete = null;
      this.pendingLoad = null;
    },
  },
  template: `
<div class="card strategy-library">
  <div class="strategy-library-tools">
    <label for="strategyLibFilter">搜索</label>
    <input id="strategyLibFilter" type="search" v-model="filter" placeholder="按名称或 ID 搜索">
    <span class="hint" role="status">{{ filtered.length }} / {{ strategies.length }} 条</span>
    <button type="button" class="secondary" :disabled="loading" @click="load">刷新</button>
  </div>
  <p v-if="loading" class="hint" role="status">正在读取策略库…</p>
  <p v-if="loadError" class="builder-field-error" role="alert">{{ loadError }}
    <button type="button" class="secondary" @click="load">重试</button>
  </p>
  <p v-if="actionError" class="builder-field-error" role="alert">{{ actionError }}</p>
  <div v-if="!loading && !loadError && !filtered.length" class="empty">
    {{ strategies.length ? '没有匹配的策略。' : '策略库为空：在「回测配置 → 自由搭建」保存策略后会显示在这里。' }}
  </div>
  <div v-else class="strategy-library-table-wrap" tabindex="0" aria-label="策略库表格">
    <table class="strategy-library-table">
      <thead><tr>
        <th scope="col">策略名称</th><th scope="col">版本</th><th scope="col">最近更新</th><th scope="col">操作</th>
      </tr></thead>
      <tbody>
        <tr v-for="s in filtered" :key="s.id" :class="{'is-busy': busyId === s.id}">
          <td>
            <span class="strategy-library-name">{{ s.name }}</span>
            <code class="strategy-library-id">{{ s.id }}</code>
          </td>
          <td>v{{ s.version }}</td>
          <td><time :datetime="s.updatedAt">{{ updatedAt(s) }}</time></td>
          <td class="strategy-library-actions">
            <button type="button" class="secondary" :disabled="!!busyId" @click="requestLoad(s)">加载编辑</button>
            <button type="button" class="secondary" :disabled="!!busyId" @click="copy(s)">复制</button>
            <button type="button" class="secondary" :disabled="!!busyId || state.taskRunning" @click="runStrategy(s)">运行</button>
            <button type="button" class="danger" :disabled="!!busyId" @click="requestDelete(s)">删除</button>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
  <div class="msg" :class="msg.ok ? 'ok' : 'err'" role="status" aria-live="polite">{{ msg.text }}</div>

  <dialog ref="dlg" aria-labelledby="strategyLibDlgTitle" @cancel.prevent="closeDlg">
    <h3 id="strategyLibDlgTitle">{{ pendingDelete ? '删除策略' : '替换当前草稿' }}</h3>
    <p v-if="pendingDelete">将删除「{{ pendingDelete.name }}」（v{{ pendingDelete.version }}），此操作不可恢复。</p>
    <p v-else>当前草稿有未保存修改。加载「{{ pendingLoad ? pendingLoad.name : '' }}」将替换这些内容，已保存的策略不受影响。</p>
    <div class="editor-actions">
      <button type="button" class="secondary" autofocus @click="closeDlg">取消</button>
      <button v-if="pendingDelete" type="button" class="danger" @click="confirmDelete">删除</button>
      <button v-else type="button" class="danger" @click="replaceDraft">放弃修改并加载</button>
    </div>
  </dialog>
</div>
  `,
};
