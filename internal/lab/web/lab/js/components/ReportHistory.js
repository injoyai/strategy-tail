import { watch } from '../vendor/vue.esm-browser.prod.js';
import { state } from '../store.js';
import { Api } from '../api.js';
import { dlgMixin } from './dlgMixin.js';

export default {
  name: 'ReportHistory',
  mixins: [dlgMixin],
  setup() { return {state}; },
  data() {
    return {reports: [], loading: false, loadingId: '', deleting: false,
      error: '', message: '', deleteError: '', target: null, request: 0, failedId: '', restoreAfterDelete: false};
  },
  computed: {
    selected() {
      const r = state.currentReport;
      return r ? this.reports.find(item => item.time === r.finishedAt && item.script === r.config.scriptName) : null;
    },
    busy() { return this.loading || !!this.loadingId || this.deleting; },
  },
  mounted() {
    this.refresh();
    watch(() => state.activeTab, n => { if (n === 2 || n === 3) this.refresh(); });
    watch(() => state.currentReport, () => { if (!this.busy) this.refresh(); });
  },
  beforeUnmount() { this.request++; },
  methods: {
    label(item) {
      return `${item.name || item.script || '回测'} · ${item.startYear}–${item.endYear} · ${new Date(item.time).toLocaleString('zh-CN', {timeZone: 'Asia/Shanghai', hour12: false})}`;
    },
    async refresh() {
      if (this.busy) return;
      this.loading = true; this.error = ''; this.failedId = '';
      try { this.reports = await Api.reports(); }
      catch (e) { this.error = '历史回测读取失败：' + e.message; }
      finally { this.loading = false; }
      if (!this.error && !state.currentReport && this.reports.length) await this.loadReport(this.reports[0].id);
    },
    async loadReport(id) {
      if (!id) return;
      const request = ++this.request, previous = state.currentReport;
      this.loadingId = id; this.error = ''; this.failedId = '';
      try {
        const report = await Api.report(id);
        if (request !== this.request || state.currentReport !== previous) return;
        state.currentReport = report;
        state.selectedVariant = null;
      } catch (e) {
        if (request === this.request) { this.error = '回测结果读取失败：' + e.message; this.failedId = id; }
      } finally {
        if (request === this.request) this.loadingId = '';
      }
    },
    retry() { return this.failedId ? this.loadReport(this.failedId) : this.refresh(); },
    openDelete() {
      if (!this.selected || this.busy || state.taskRunning) return;
      this.target = this.selected; this.deleteError = ''; this.open = true;
      this.restoreAfterDelete = false;
      this.$nextTick(() => this.$refs.cancelDelete?.focus());
    },
    closeDlg() { if (!this.deleting) this.open = false; },
    onDialogClose() {
      if (this.restoreAfterDelete) this.$nextTick(() => (this.reports.length ? this.$refs.historySelect : this.$refs.refreshHistory)?.focus());
    },
    async deleteReport() {
      if (!this.target || this.deleting) return;
      const id = this.target.id;
      this.deleting = true; this.deleteError = '';
      try {
        await Api.deleteReport(id);
        const isCurrent = this.selected?.id === id;
        this.reports = this.reports.filter(item => item.id !== id);
        if (isCurrent) { state.currentReport = null; state.selectedVariant = null; }
        this.message = '已删除该次回测结果及导出文件';
        if (isCurrent && this.reports.length) await this.loadReport(this.reports[0].id);
        this.restoreAfterDelete = true;
        this.open = false;
      } catch (e) { this.deleteError = '删除失败：' + e.message; }
      finally {
        this.deleting = false;
        if (this.deleteError) this.$nextTick(() => this.$refs.confirmDelete?.focus());
      }
    },
  },
  template: `
<section class="card report-history" aria-label="历史回测">
  <div class="form-row report-history-tools" :aria-busy="busy">
    <label for="report-history-select">历史回测</label>
    <select id="report-history-select" ref="historySelect" :value="loadingId || selected?.id || ''"
      :disabled="busy || !reports.length" :title="selected ? label(selected) : ''" @change="loadReport($event.target.value)">
      <option value="" disabled>{{ reports.length ? '选择回测结果' : '暂无历史回测' }}</option>
      <option v-for="item in reports" :key="item.id" :value="item.id">{{ label(item) }}</option>
    </select>
    <button ref="refreshHistory" type="button" class="secondary" :disabled="busy" @click="refresh">刷新列表</button>
    <button type="button" class="danger" :disabled="busy || !selected || state.taskRunning" :title="state.taskRunning ? '任务运行中，请完成后再删除' : ''" @click="openDelete">删除结果</button>
  </div>
  <div class="report-history-feedback" role="status" aria-live="polite">
    <span v-if="busy" class="hint">{{ deleting ? '正在删除…' : '正在读取回测结果…' }}</span>
    <span v-else-if="error" class="msg err">{{ error }} <button type="button" class="secondary" @click="retry">重试</button></span>
    <span v-else class="hint">{{ message || (reports.length ? '共 ' + reports.length + ' 次回测 · 时间为上海时间' : '暂无已保存的回测，请先运行一次回测') }}</span>
  </div>
</section>
<Teleport to="body">
  <dialog ref="dlg" aria-labelledby="deleteReportTitle" aria-describedby="deleteReportDescription" @cancel.prevent="closeDlg" @close="onDialogClose">
    <div class="dlg-head"><h3 id="deleteReportTitle">删除回测结果</h3></div>
    <p class="report-delete-name">{{ target ? label(target) : '' }}</p>
    <p id="deleteReportDescription">将永久删除该次回测结果及其 CSV、HTML 导出，无法撤销。策略配置和行情数据会保留。</p>
    <p v-if="deleteError" class="msg err" role="alert">{{ deleteError }}</p>
    <div class="row" :aria-busy="deleting">
      <button ref="cancelDelete" type="button" class="secondary" :disabled="deleting" @click="closeDlg">取消</button>
      <button ref="confirmDelete" type="button" class="danger" :disabled="deleting" @click="deleteReport">删除结果</button>
      <span v-if="deleting" class="hint" role="status">正在删除…</span>
    </div>
  </dialog>
</Teleport>
  `,
};
