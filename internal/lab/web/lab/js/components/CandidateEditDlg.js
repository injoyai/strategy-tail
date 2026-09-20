// CandidateEditDlg：编辑候选对话框（05 页“编辑”触发）。
// 迁移自 index.html openCandidateEdit/renderEditRange/saveCandidateEdit。
// 乐观并发：携带 expectedRevision，409 不自动覆盖，提示比较服务端最新版本。
import { state } from '../store.js';
import { Api } from '../api.js';
import { dlgMixin } from './dlgMixin.js';

export default {
  name: 'CandidateEditDlg',
  mixins: [dlgMixin],
  data() {
    return {
      edit: null,            // 正在编辑的候选（null=未打开）
      name: '',
      useMode: 'observe',
      rangeOp: 'gte',
      rangeMin: '',
      rangeMax: '',
      notes: '',
      hint: '',
      msg: {text: '', ok: true},
      saving: false,
    };
  },
  computed: {
    unit() { return this.edit && this.edit.factor ? this.edit.factor.unit : ''; },
    rangeUnit() {
      return this.unit === 'ratio'
        ? '按百分比填写（5 = 5%），提交转原始比例' : '填写因子原始值';
    },
  },
  watch: {
    open(v) { if (!v) this.edit = null; },
  },
  methods: {
    openDlg(c) {
      this.edit = c;
      state.editExpectedRevision = c.revision;
      this.name = c.name;
      this.useMode = (c.use && c.use.mode === 'range') ? 'range' : 'observe';
      const f = c.use && c.use.filter;
      if (this.useMode === 'range' && f) {
        this.rangeOp = f.operator;
        this.rangeMin = f.min == null ? '' : (this.unit === 'ratio' ? +(f.min * 100).toFixed(4) : f.min);
        this.rangeMax = f.max == null ? '' : (this.unit === 'ratio' ? +(f.max * 100).toFixed(4) : f.max);
      } else {
        this.rangeOp = 'gte';
        this.rangeMin = '';
        this.rangeMax = '';
      }
      this.notes = c.notes || '';
      this.hint = `当前修订 v${c.revision}，历史修订已保留。`;
      this.msg = {text: '', ok: true};
      this.open = true;
      this.$nextTick(() => this.$refs.nameEl && this.$refs.nameEl.focus());
    },
    parseRaw(str) {
      const v = parseFloat(str);
      if (!isFinite(v)) return null;
      return this.unit === 'ratio' ? +(v / 100).toFixed(6) : v;
    },
    async saveCandidateEdit() {
      const c = this.edit;
      if (!c) return;
      const name = this.name.trim();
      if (!name) {
        this.msg = {text: '请填写候选名称', ok: false};
        this.$nextTick(() => this.$refs.nameEl && this.$refs.nameEl.focus());
        return;
      }
      const use = {mode: this.useMode};
      if (use.mode === 'range') {
        const op = this.rangeOp;
        const min = this.parseRaw(this.rangeMin);
        const max = this.parseRaw(this.rangeMax);
        if (op !== 'lte' && min == null) { this.msg = {text: '请填写下限', ok: false}; return; }
        if (op !== 'gte' && max == null) { this.msg = {text: '请填写上限', ok: false}; return; }
        if (op === 'between' && min > max) { this.msg = {text: '下限不能大于上限', ok: false}; return; }
        use.filter = {kind: c.factor.kind, days: c.factor.days,
          factorVersion: c.factor.implementationVersion, operator: op};
        if (op !== 'lte') use.filter.min = min;
        if (op !== 'gte') use.filter.max = max;
      }
      const body = {expectedRevision: state.editExpectedRevision, name, use, status: c.status,
        notes: this.notes.trim()};
      this.saving = true;
      try {
        const d = await Api.updateCandidate(c.id, body);
        this.msg = {text: '已保存修订 v' + d.candidate.revision, ok: true};
        this.closeDlg();
        state.candidatesDirty++; // 通知 05 页刷新
      } catch (e) {
        // 409：保留输入，重新加载服务端记录并提示比较，不自动覆盖
        if (e.message && e.message.indexOf('重新加载') >= 0) {
          this.msg = {text: '该候选已被其他人更新：保留你的输入，请比较服务端最新版本后重试', ok: false};
          try {
            const d = await Api.candidate(c.id);
            state.editExpectedRevision = d.candidate.revision;
            this.hint = `服务端当前修订 v${d.candidate.revision}，请核对后再次保存。`;
          } catch (_) { /* 重载失败仅保留提示 */ }
        } else {
          this.msg = {text: e.message, ok: false};
        }
      } finally {
        this.saving = false;
      }
    },
  },
  template: `
<dialog ref="dlg" aria-labelledby="candEditTitle">
  <div class="dlg-head">
    <h3 id="candEditTitle">编辑候选</h3>
    <button class="dlg-close" @click="closeDlg" aria-label="关闭">×</button>
  </div>
  <div class="candidate-form" style="margin-top:0">
    <div class="cf-row"><label for="candEditName">名称</label>
      <input type="text" id="candEditName" ref="nameEl" v-model="name" maxlength="80">
    </div>
    <div class="cf-row"><label for="candEditUse">使用方式</label>
      <select id="candEditUse" v-model="useMode">
        <option value="observe">仅保存观察结果</option>
        <option value="range">配置固定区间后保存</option>
      </select>
    </div>
    <div class="cf-row" v-show="useMode === 'range'">
      <label for="candEditOp">区间条件</label>
      <select id="candEditOp" v-model="rangeOp" style="width:104px">
        <option value="gte">≥ 至少</option>
        <option value="lte">≤ 至多</option>
        <option value="between">介于</option>
      </select>
      <input type="number" v-model="rangeMin" step="any" aria-label="区间下限">
      <input type="number" v-model="rangeMax" step="any" aria-label="区间上限">
      <span class="hint" style="margin:0" id="candEditUnit">{{ rangeUnit }}</span>
    </div>
    <div class="cf-row"><label for="candEditNotes">备注</label>
      <textarea class="resize-none" id="candEditNotes" v-model="notes" maxlength="2000"></textarea>
    </div>
    <div class="cf-row">
      <div class="cf-actions">
        <button class="primary" @click="saveCandidateEdit" :disabled="saving" :aria-busy="String(saving)">保存修订</button>
        <button class="secondary" @click="closeDlg">取消</button>
        <span class="hint" style="margin:0" id="candEditHint">{{ hint }}</span>
      </div>
    </div>
    <div class="msg" :class="msg.ok ? 'ok' : 'err'" role="status" aria-live="polite">{{ msg.text }}</div>
  </div>
</dialog>
  `,
};
