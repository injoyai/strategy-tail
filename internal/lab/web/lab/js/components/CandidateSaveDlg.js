// CandidateSaveDlg：保存为候选对话框（01 页“保存为候选”触发）。
// 迁移自 index.html openCandidateDlg/saveCandidate/candidateUseModeChange。
// 等频默认“仅保存观察结果”且阈值为空，不自动读取分组边界；bins 只展示断点建议。
// 一次保存动作固定 requestId：网络失败重试复用，服务端拒绝时保留输入。
import { state } from '../store.js';
import { Api } from '../api.js';
import { dlgMixin } from './dlgMixin.js';

export default {
  name: 'CandidateSaveDlg',
  mixins: [dlgMixin],
  data() {
    return {
      name: '',
      useMode: 'observe',
      rangeOp: 'gte',
      rangeMin: '',
      rangeMax: '',
      notes: '',
      msg: {text: '', ok: true},
      saving: false,
    };
  },
  computed: {
    unit() {
      const rep = state.currentAnalysis;
      return rep && rep.factor ? rep.factor.unit : '';
    },
    rangeUnit() {
      return this.unit === 'ratio'
        ? '按百分比填写（5 = 5%），提交转原始比例' : '填写因子原始值';
    },
    useHint() {
      const rep = state.currentAnalysis;
      if (!rep || !(rep.grouping && rep.grouping.mode === 'bins' && Array.isArray(rep.grouping.cuts))) return '';
      const cuts = rep.grouping.cuts.map(c => this.unit === 'ratio' ? +(c * 100).toFixed(2) : c).join('、');
      return `固定区间分析的建议断点：${cuts}（需确认后使用，不会自动填入）`;
    },
  },
  watch: {
    // 表单输入变化即视为“新保存动作”的边界：重新发起保存时生成新 requestId
    name() { state.candidateRequestId = null; },
    useMode() { state.candidateRequestId = null; },
    rangeOp() { state.candidateRequestId = null; },
    rangeMin() { state.candidateRequestId = null; },
    rangeMax() { state.candidateRequestId = null; },
    notes() { state.candidateRequestId = null; },
    open(v) {
      if (!v) { state.candidateRequestId = null; this.msg = {text: '', ok: true}; }
    },
  },
  methods: {
    openDlg() {
      const rep = state.currentAnalysis;
      if (!rep || rep.analysisVersion < 3 || !rep.analysisId || !(rep.factor && rep.factor.implementationVersion > 0)) {
        state.factorMsg = {text: '该分析为旧版本报告，请重新运行分析后再保存', ok: false};
        return;
      }
      this.useMode = 'observe';
      this.name = '';
      this.rangeOp = 'gte';
      this.rangeMin = '';
      this.rangeMax = '';
      this.notes = '';
      this.msg = {text: '', ok: true};
      state.candidateRequestId = null; // 新保存动作
      this.open = true;
      this.$nextTick(() => this.$refs.nameEl && this.$refs.nameEl.focus());
    },
    parseRawThreshold(str) {
      const v = parseFloat(str);
      if (!isFinite(v)) return null;
      return this.unit === 'ratio' ? +(v / 100).toFixed(6) : v;
    },
    async saveCandidate() {
      const rep = state.currentAnalysis;
      if (!rep || rep.analysisVersion < 3 || !rep.analysisId || !(rep.factor && rep.factor.implementationVersion > 0)) {
        this.msg = {text: '该分析为旧版本报告，请重新运行分析后再保存', ok: false};
        return;
      }
      const name = this.name.trim();
      if (!name) {
        this.msg = {text: '请填写候选名称', ok: false};
        this.$nextTick(() => this.$refs.nameEl && this.$refs.nameEl.focus());
        return;
      }
      const use = {mode: this.useMode};
      if (use.mode === 'range') {
        const op = this.rangeOp;
        const min = this.parseRawThreshold(this.rangeMin);
        const max = this.parseRawThreshold(this.rangeMax);
        if (op !== 'lte' && min == null) { this.msg = {text: '请填写下限', ok: false}; return; }
        if (op !== 'gte' && max == null) { this.msg = {text: '请填写上限', ok: false}; return; }
        if (op === 'between' && min > max) { this.msg = {text: '下限不能大于上限', ok: false}; return; }
        use.filter = {kind: rep.kind, days: rep.factor.days,
          factorVersion: rep.factor.implementationVersion, operator: op};
        if (op !== 'lte') use.filter.min = min;
        if (op !== 'gte') use.filter.max = max;
      }
      if (!state.candidateRequestId) state.candidateRequestId = crypto.randomUUID();
      const body = {requestId: state.candidateRequestId, analysisId: rep.analysisId, name, use,
        notes: this.notes.trim()};
      this.saving = true;
      try {
        const d = await Api.createCandidate(body);
        state.candidateRequestId = null;
        state.candidateSavedFor = rep.analysisId; // “查看候选”按钮按 analysisId 显示
        state.candidatesDirty++;                  // 通知 05 页刷新
        this.closeDlg();
      } catch (e) {
        this.msg = {text: e.message, ok: false}; // 保留输入与 requestId 供重试
      } finally {
        this.saving = false;
      }
    },
  },
  template: `
<dialog ref="dlg" aria-labelledby="candidateDlgTitle">
  <div class="dlg-head">
    <h3 id="candidateDlgTitle">保存为候选</h3>
    <button class="dlg-close" @click="closeDlg" aria-label="关闭">×</button>
  </div>
  <div class="candidate-form" style="margin-top:0">
    <div class="cf-row"><label for="candName">候选名称</label>
      <input type="text" id="candName" ref="nameEl" v-model="name" maxlength="80" placeholder="如 20日动量观察候选" aria-describedby="candFormNote">
    </div>
    <div class="cf-row"><label for="candUseMode">使用方式</label>
      <select id="candUseMode" v-model="useMode" aria-describedby="candFormNote">
        <option value="observe" selected>仅保存观察结果</option>
        <option value="range">配置固定区间后保存</option>
      </select>
      <span class="hint" style="margin:0" id="candUseHint">{{ useHint }}</span>
    </div>
    <div class="cf-row" v-show="useMode === 'range'">
      <label for="candRangeOp">区间条件</label>
      <select id="candRangeOp" v-model="rangeOp" style="width:104px">
        <option value="gte">≥ 至少</option>
        <option value="lte">≤ 至多</option>
        <option value="between">介于</option>
      </select>
      <input type="number" v-model="rangeMin" step="any" placeholder="下限" aria-label="区间下限">
      <input type="number" v-model="rangeMax" step="any" placeholder="上限" aria-label="区间上限">
      <span class="hint" style="margin:0" id="candRangeUnit">{{ rangeUnit }}</span>
    </div>
    <div class="cf-row"><label for="candNotes">备注</label>
      <textarea class="resize-none" id="candNotes" v-model="notes" maxlength="2000" placeholder="可选：记录观察理由、待验证假设等"></textarea>
    </div>
    <div class="cf-row">
      <div class="cf-actions">
        <button class="primary" @click="saveCandidate" :disabled="saving" :aria-busy="String(saving)">保存候选</button>
        <button class="secondary" @click="closeDlg">取消</button>
        <span class="hint" style="margin:0" id="candFormNote">等频分组边界是跨日累计分布，不会自动转成固定阈值；保存为候选不代表样本外验证通过。</span>
      </div>
    </div>
    <div class="msg" :class="msg.ok ? 'ok' : 'err'" role="status" aria-live="polite">{{ msg.text }}</div>
  </div>
</dialog>
  `,
};
