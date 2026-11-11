// Tab1Run：回测配置；运行参数由共享弹窗确认。
// 状态提升：simpleConds/presetId/specName/runCfg/focusCondReq 全部在 store（跨页共享）。
import { watch } from '../vendor/vue.esm-browser.prod.js';
import { state } from '../store.js';
import { Api } from '../api.js';
import { factorTypeName, groupFactors } from '../format.js';
import { buildConfig, ACCOUNT_CONFIG } from '../config.js';
import Tab1Builder from './Tab1Builder.js';

export default {
  name: 'Tab1Run',
  components: { Tab1Builder },
  inject: ['switchTab', 'startTask', 'openRunConfig'],
  setup() {
    return { state, factorTypeName, groupFactors };
  },
  data() {
    return {
      presetInfos: [],       // GET /api/strategy-presets
      inputRefs: {},         // `${idx}:${field}` -> 条件卡输入元素（校验失败聚焦）
    };
  },
  computed: {
    factorGroups() { return groupFactors(state.factorCatalog); },
    presetInfo() { return this.presetInfos.find(p => p.id === state.presetId) || null; },
    presetDescText() {
      const p = this.presetInfo;
      return p
        ? `${p.description}。它会作为基础买入条件，并与下方因子条件同时满足。`
        : '不附加市值、价格、涨停或均线回调等预设条件；买入只由下方因子条件决定，对照基准为全部样本。';
    },
    sampleSizeVisible() { return state.runCfg.sampleMode === 'random'; },
    sampleCodesVisible() { return state.runCfg.sampleMode === 'codes'; },
  },
  mounted() {
    this.loadPresets();
    // 因子目录由 01 页加载；就绪后创建默认条件卡（只一次）
    watch(() => state.factorCatalog, cat => {
      if (cat.length) this.ensureDefaultConds();
    }, {immediate: true});
    // 跨页定位请求：01/05 写入 simpleConds → switchTab(1) → 写 focusCondReq
    watch(() => state.focusCondReq, req => {
      if (req) this.focusCond(req);
    });
  },
  methods: {
    setMode(mode) { state.runMode = mode; },
    // —— 预设 ——
    async loadPresets() {
      try { this.presetInfos = await Api.strategyPresets(); }
      catch (e) { state.runMsg = {text: '预设策略加载失败: ' + e.message, ok: false}; }
    },

    // —— 条件卡 ——
    condEntry(c) { return state.factorCatalog.find(e => e.kind === c.kind); },
    isRatioCond(c) { const e = this.condEntry(c); return !!(e && e.unit === 'ratio'); },
    condLabel(c) { const e = this.condEntry(c); return e ? factorTypeName(e) : c.kind; },
    defaultCond() {
      const e = state.factorCatalog[0];
      return {kind: e.kind, days: e.defaultDays, operator: 'between', min: null, max: null,
        factorVersion: e.implementationVersion || 0, hl: false, nb: false, err: '', errField: ''};
    },
    ensureDefaultConds() {
      if (state.condsInitialized || !state.factorCatalog.length) return;
      state.simpleConds.push(this.defaultCond());
      state.condsInitialized = true;
    },
    addCond() {
      state.simpleConds.push(this.defaultCond());
      const idx = state.simpleConds.length - 1;
      this.$nextTick(() => {
        const el = this.inputRefs[`${idx}:days`];
        if (el) { el.focus(); el.closest('.cond-card')?.scrollIntoView({block: 'nearest'}); }
      });
    },
    delCond(i) {
      state.simpleConds.splice(i, 1);
      const target = Math.min(i, state.simpleConds.length - 1);
      this.$nextTick(() => {
        if (!state.simpleConds.length) { this.$refs.btnAddCond?.focus(); return; }
        const el = this.inputRefs[`${target}:days`];
        if (el) el.focus();
      });
    },
    setInputRef(el, i, field) {
      const key = `${i}:${field}`;
      if (el) this.inputRefs[key] = el; else delete this.inputRefs[key];
    },
    onKindChange(c, e) {
      const ne = state.factorCatalog.find(x => x.kind === e.target.value);
      if (!ne) return;
      c.kind = ne.kind;
      c.days = ne.defaultDays;
      c.factorVersion = ne.implementationVersion || 0;
      c.err = ''; c.errField = '';
    },
    onDaysInput(c, e) { c.days = Math.floor(+e.target.value) || 0; c.err = ''; c.errField = ''; },
    onOpChange(c, e) { c.operator = e.target.value; c.err = ''; c.errField = ''; },
    onBoundInput(c, key, e) {
      const v = e.target.value.trim();
      c[key] = v === '' ? null : +v;
      c.err = ''; c.errField = '';
    },
    // 跨页定位：01/05 追加条件后高亮并聚焦对应卡
    focusCond(req) {
      state.runMode = 'simple';
      const idx = state.simpleConds.findIndex(c => c.kind === req.kind && c.days === req.days);
      state.simpleConds.forEach(c => { c.hl = false; });
      state.focusCondReq = null;
      if (idx >= 0) {
        state.simpleConds[idx].hl = true;
        this.$nextTick(() => {
          const el = this.inputRefs[`${idx}:days`];
          if (el) { el.focus(); el.closest('.cond-card')?.scrollIntoView({block: 'nearest'}); }
        });
      } else {
        this.$nextTick(() => this.$refs.btnAddCond?.focus());
      }
    },

    // —— 运行配置 / 提交 ——
    requestRun() {
      if (state.taskRunning) return;
      if (!this.buildSpec()) return;
      this.openRunConfig('simple', () => this.runSimple());
    },
    async runSimple() {
      const spec = this.buildSpec();
      if (!spec) return;
      try {
        const d = await Api.runStrategy(spec);
        const n = spec.factorFilters.length;
        const detail = n === 0 ? '仅基准（无因子条件）'
          : n === 1 ? '基准 + 1 个因子条件' : `基准 + ${n} 个单条件 + 因子组合`;
        state.runMsg = {text: `已提交 ${d.variants} 个变体（${detail}），运行进度见右下角任务卡。`, ok: true};
        this.startTask('backtest');
        return true;
      } catch (e) { state.runMsg = {text: e.message, ok: false}; }
    },
    // 收集并校验 StrategySpec；失败返回 null（错误贴近字段展示并聚焦第一个错误）
    buildSpec() {
      state.simpleConds.forEach(c => { c.err = ''; c.errField = ''; });
      let firstErr = null;
      const cardErr = (c, field, msg) => {
        c.err = msg; c.errField = field;
        if (!firstErr) firstErr = {c, field};
      };
      const cfg = buildConfig();
      const spec = {
        version: 1,
        name: (state.specName || '').trim(),
        basePresetId: state.presetId,
        factorFilters: [],
        exit: {
          holdingDays: state.runCfg.holdingOn ? Math.floor(+state.runCfg.holdingDays) : 0,
          takeProfit: state.runCfg.tpOn ? +state.runCfg.takeProfit / 100 : 0,
          stopLoss: state.runCfg.slOn ? +state.runCfg.stopLoss / 100 : 0,
        },
        run: {startYear: cfg.startYear, endYear: cfg.endYear, sampleMode: cfg.sampleMode, account: ACCOUNT_CONFIG},
      };
      if (cfg.sampleMode === 'random') spec.run.sampleSize = cfg.sampleSize;
      if (cfg.sampleMode === 'codes') spec.run.sampleCodes = cfg.sampleCodes;

      const seen = new Set();
      state.simpleConds.forEach(c => {
        const ent = this.condEntry(c);
        const isRatio = !!(ent && ent.unit === 'ratio');
        const conv = v => (isRatio ? +(v / 100).toFixed(6) : v); // 显示百分比 → 原始比例
        if (!(c.days >= 1)) {
          cardErr(c, 'days', '回看天数需为正整数');
        } else if (seen.has(c.kind + ':' + c.days)) {
          cardErr(c, 'days', '与其他条件重复：相同因子+天数只能出现一次');
        } else if (c.operator === 'between' && (c.min == null || c.max == null)) {
          cardErr(c, c.min == null ? 'min' : 'max', '区间条件需同时填写下限和上限');
        } else if (c.operator === 'gte' && c.min == null) {
          cardErr(c, 'min', '需填写下限');
        } else if (c.operator === 'lte' && c.max == null) {
          cardErr(c, 'max', '需填写上限');
        } else if (c.operator === 'between' && c.min > c.max) {
          cardErr(c, 'min', '下限不能大于上限');
        } else {
          seen.add(c.kind + ':' + c.days);
          const ft = {kind: c.kind, days: c.days, operator: c.operator,
            factorVersion: c.factorVersion || (ent ? (ent.implementationVersion || 0) : 0)};
          if (c.operator !== 'lte') ft.min = conv(c.min);
          if (c.operator !== 'gte') ft.max = conv(c.max);
          spec.factorFilters.push(ft);
        }
      });
      const errs = [];
      if (!spec.exit.holdingDays && !spec.exit.takeProfit && !spec.exit.stopLoss) {
        errs.push('卖出规则至少启用一项（持仓天数 / 止盈 / 止损）');
      }
      if (spec.run.startYear > spec.run.endYear) {
        errs.push('时间范围无效：开始年份不能大于结束年份');
      }
      if (spec.run.sampleMode === 'random' && !(spec.run.sampleSize > 0)) {
        errs.push('随机样本数需大于 0');
      }
      if (spec.run.sampleMode === 'codes' && !(spec.run.sampleCodes || []).length) {
        errs.push('指定代码样本为空');
      }
      if (errs.length) state.runMsg = {text: errs.join('\n'), ok: false};
      if (firstErr) {
        this.$nextTick(() => {
          const el = this.inputRefs[`${state.simpleConds.indexOf(firstErr.c)}:${firstErr.field}`];
          if (el) el.focus();
        });
        return null;
      }
      return spec;
    },
  },
  template: `
<div>
    <div class="mode-switch" role="group" aria-label="配置模式">
      <button type="button" :aria-pressed="String(state.runMode==='builder')" @click="setMode('builder')">自由搭建</button>
      <button type="button" :aria-pressed="String(state.runMode==='simple')" @click="setMode('simple')">因子组合</button>
    </div>
<div>
  <div class="card">

    <!-- 简单模式：声明式多条件策略 -->
    <template v-if="state.runMode==='simple'">
      <h3>简单配置</h3>
      <div class="form-row"><label for="presetSel">基础策略（可选）</label>
        <select id="presetSel" style="min-width:200px" v-model="state.presetId" aria-describedby="presetDesc">
          <option value="">不使用预设（仅因子条件）</option>
          <option v-for="p in presetInfos" :key="p.id" :value="p.id">{{ p.name }}</option>
        </select>
      </div>
      <div class="hint" id="presetDesc">{{ presetDescText }}</div>
      <ul class="preset-rules" v-if="presetInfo">
        <li v-for="(r, i) in (presetInfo.rules || [])" :key="i">{{ r }}</li>
      </ul>
      <div class="form-row"><label>策略名称</label>
        <input type="text" v-model="state.specName" maxlength="80" placeholder="可选，留空自动生成">
      </div>

      <div class="subhead">因子条件（数量不限，可为 0，全部并且 AND）</div>
      <div id="condCards">
        <template v-if="state.simpleConds.length">
          <template v-for="(c, i) in state.simpleConds" :key="i">
            <div v-if="i>0" class="and-sep">并且（AND）</div>
            <div class="cond-card" :class="{highlight: c.hl}">
              <div class="cond-head">
                <span class="cond-no">条件 {{ i + 1 }} · {{ condLabel(c) }}</span>
                <span v-if="c.nb" class="st-chip stale">尚未回测</span>
                <button type="button" class="cond-del" :aria-label="'删除条件 ' + (i + 1)" @click="delCond(i)">删除</button>
              </div>
              <div class="cond-grid">
                <span class="lbl">因子</span>
                <select :value="c.kind" @change="onKindChange(c, $event)" :aria-label="'条件 ' + (i + 1) + ' 因子'">
                  <optgroup v-for="g in factorGroups" :key="g.category" :label="g.category">
                    <option v-for="e in g.entries" :key="e.kind" :value="e.kind">{{ factorTypeName(e) }}</option>
                  </optgroup>
                </select>
                <span class="lbl">天数</span>
                <input type="number" min="1" style="width:64px" :value="c.days"
                  @input="onDaysInput(c, $event)" :class="{invalid: c.err && c.errField==='days'}"
                  :ref="el => setInputRef(el, i, 'days')" :aria-label="'条件 ' + (i + 1) + ' 回看天数'">
                <select :value="c.operator" @change="onOpChange(c, $event)" :aria-label="'条件 ' + (i + 1) + ' 比较方式'">
                  <option value="between">介于</option>
                  <option value="gte">≥ 至少</option>
                  <option value="lte">≤ 至多</option>
                </select>
                <template v-if="c.operator!=='lte'">
                  <span class="lbl">下限</span>
                  <input type="number" step="any" :placeholder="isRatioCond(c) ? '如 5 (=5%)' : '数值'"
                    :value="c.min" @input="onBoundInput(c, 'min', $event)"
                    :class="{invalid: c.err && c.errField==='min'}"
                    :ref="el => setInputRef(el, i, 'min')"
                    :aria-label="'条件 ' + (i + 1) + ' 下限' + (isRatioCond(c) ? '（百分比）' : '')">
                  <span v-if="isRatioCond(c)" class="lbl">%</span>
                </template>
                <template v-if="c.operator!=='gte'">
                  <span class="lbl">上限</span>
                  <input type="number" step="any" :placeholder="isRatioCond(c) ? '如 5 (=5%)' : '数值'"
                    :value="c.max" @input="onBoundInput(c, 'max', $event)"
                    :class="{invalid: c.err && c.errField==='max'}"
                    :ref="el => setInputRef(el, i, 'max')"
                    :aria-label="'条件 ' + (i + 1) + ' 上限' + (isRatioCond(c) ? '（百分比）' : '')">
                  <span v-if="isRatioCond(c)" class="lbl">%</span>
                </template>
              </div>
              <div class="hint" v-if="condEntry(c)">
                {{ isRatioCond(c)
                  ? '按百分比填写（输入 5 表示 5%），提交时转换为原始比例 0.05。原始值示例：' + condEntry(c).example
                  : '原始值示例：' + condEntry(c).example }}
              </div>
              <div class="cond-err">{{ c.err }}</div>
            </div>
          </template>
        </template>
        <div v-else class="empty" style="padding:12px">暂无条件：将只回测基础策略（未选预设时为全部样本基准）。</div>
      </div>
      <div class="editor-actions">
        <button class="secondary" ref="btnAddCond" @click="addCond">添加条件</button>
        <span class="hint" id="condLimitHint">当前 {{ state.simpleConds.length }} 个条件</span>
      </div>
      <div class="hint" style="margin-top:8px">口径说明：当前回测仍按当日数据判定并以同日价格成交；因子页的未来收益只用于因子评价，不代表可实现的组合收益。信号与次日成交的解耦将作为后续执行模型改造。</div>
    </template>

    <!-- 自由搭建：条件树策略搭建器 -->
    <tab1-builder v-else-if="state.runMode==='builder'" />

    <div class="editor-actions" style="margin-top:12px" v-show="state.runMode!=='builder'">
      <button class="primary" @click="requestRun" :disabled="state.taskRunning" :aria-busy="String(state.taskRunning)">运行回测</button>
    </div>
    <div class="msg" v-show="state.runMode!=='builder'" :class="state.runMsg.ok ? 'ok' : 'err'" role="status" aria-live="polite">{{ state.runMsg.text }}</div>
  </div>

</div>
</div>
  `,
};
