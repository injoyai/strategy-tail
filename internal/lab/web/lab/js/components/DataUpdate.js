import { state } from '../store.js';
import { Api } from '../api.js';
import { taskProgress } from '../format.js';

export function marketDataState(status, now = Date.now()) {
  if (!status) return '待确认';
  // 上海为 UTC+8；使用 UTC 字段计算日界线，避免浏览器本地时区影响。
  const hour = 3600000, offset = 8 * hour;
  const local = new Date(now + offset);
  const midnight = Date.UTC(local.getUTCFullYear(), local.getUTCMonth(), local.getUTCDate()) - offset;
  if ([0, 6].includes(local.getUTCDay())) return '休市';
  if (status.tradingDate !== local.toISOString().slice(0, 10) || typeof status.isTradingDay !== 'boolean') return '待确认';
  if (!status.isTradingDay) return '休市';
  const close = midnight + 15.5 * hour;
  if (now < close) return '未收盘';
  const fresh = [!status.lastSuccessError && status.lastSuccess, status.lastPartialSuccess].some(value => {
    const updated = Date.parse(value);
    return updated >= close && updated <= now;
  });
  return fresh ? '已更新' : '未更新';
}

export default {
  name: 'DataUpdate',
  inject: ['startTask'],
  setup() { return {state}; },
  data() { return {status: null, now: Date.now(), loading: true, submitting: false, saving: false, error: '', notice: '', timeError: false, enabled: false, time: '15:45', initialized: false, timer: null, disposed: false}; },
  computed: {
    marketState() { return marketDataState(this.status, this.now); },
    fresh() { return this.marketState === '已更新' ? true : this.marketState === '未更新' ? false : null; },
    partialSuccess() {
      const s = this.status;
      return s?.state === 'error' && !s.skipped && s.total > 0 && s.done === s.total && s.failed > 0 && s.failed < s.total;
    },
    running() { return this.status?.state === 'running'; },
    progressInfo() {
      const s = this.status || {};
      return taskProgress({task: 'data_update', phase: s.stage, unit: s.stage === 'valuation' ? 'snapshots' : 'symbols',
        done: s.stageDone ?? s.done, total: s.stageTotal ?? s.total, currentCode: s.code});
    },
    progressPercent() {
      return this.progressInfo.percent;
    },
    bubbleText() {
      if (this.error || this.status?.lastSuccessError) return '异常';
      if (this.loading) return '读取中';
      if (this.running) return {checking: '检查中', day: '日线更新', minute: '分钟更新', valuation: '估值更新', finalize: '保存中'}[this.progressInfo.phase] || '更新中';
      if (!this.status) return '异常';
      return this.marketState;
    },
    bubbleLabel() { return `行情数据：${this.bubbleText}。${this.running && this.progressPercent != null ? '本阶段 ' + this.progressPercent + '%。' : ''}${this.status?.finishedAt ? '最近检查：' + this.checkResult + '。' : ''}最后完整更新（上海时间）：${this.lastUpdateText}。点击展开详情`; },
    lastUpdateText() {
      if (!this.status) return this.loading ? '读取中…' : '读取失败';
      if (this.status.lastSuccessError) return '读取失败';
      return this.status.lastSuccess ? this.displayTime(this.status.lastSuccess) : '暂无完整成功记录';
    },
    checkResult() {
      if (this.partialSuccess) return `部分成功（失败 ${this.status.failed}/${this.status.total}）`;
      if (this.status?.state === 'error') return this.status.failed > 0 ? `部分失败（${this.status.failed}/${this.status.total}）` : '失败';
      if (this.status?.skipped) return '已跳过';
      return this.status?.state === 'done' ? '成功' : '已结束';
    },
  },
  mounted() { this.poll(); },
  unmounted() { this.disposed = true; clearTimeout(this.timer); },
  methods: {
    toggleDetails() {
      this.$refs.bubble.focus();
      this.$refs.panel.togglePopover();
    },
    closeDetails() {
      this.$refs.panel.hidePopover();
      this.$refs.bubble.focus();
    },
    displayTime(value) {
      const date = new Date(value);
      return Number.isNaN(date.getTime()) ? '时间记录无效' : date.toLocaleString('zh-CN', {timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23'});
    },
    applyStatus(s) {
      const wasRunning = state.dataUpdating;
      this.status = s;
      state.dataUpdating = s.state === 'running';
      state.taskRunning = state.dataUpdating || state.status?.state === 'running' || !!state.taskDock?.readingResults;
      if (!this.initialized) { this.enabled = s.config.enabled; this.time = s.config.time; this.initialized = true; }
      if (state.dataUpdating && !wasRunning) this.startTask('data_update');
    },
    async poll() {
      clearTimeout(this.timer);
      try {
        const s = await Api.dataUpdateStatus();
        if (this.disposed) return;
        this.applyStatus(s);
        this.error = '';
      } catch (e) { if (!this.disposed) this.error = '无法读取更新状态：' + e.message; }
      finally {
        if (!this.disposed) {
          this.now = Date.now();
          this.loading = false;
          this.timer = setTimeout(() => this.poll(), this.running ? 2000 : 10000);
        }
      }
    },
    async update() {
      if (this.submitting || state.taskRunning || this.running) return;
      this.submitting = true;
      this.error = '';
      this.notice = '';
      try { await Api.updateData(); this.startTask('data_update'); await this.poll(); }
      catch (e) { this.notice = '启动更新失败：' + e.message; }
      finally { this.submitting = false; }
    },
    async save() {
      if (this.saving) return;
      this.timeError = false;
      if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(this.time)) { this.timeError = true; this.notice = '请输入有效的更新时间，例如 15:45'; this.$refs.time.focus(); return; }
      this.saving = true;
      this.notice = '';
      try { this.applyStatus(await Api.configureDataUpdate({enabled: this.enabled, time: this.time})); this.notice = '自动更新设置已保存'; }
      catch (e) { this.notice = '保存设置失败：' + e.message; }
      finally { this.saving = false; }
    },
  },
  template: `
<div class="data-update-widget">
<button ref="bubble" type="button" class="data-update-bubble" popovertarget="data-update-panel" @click.prevent="toggleDetails" :data-fresh="String(fresh)" :aria-label="bubbleLabel" :title="bubbleLabel">
  <span class="data-update-dot" aria-hidden="true"></span><span>行情</span><span class="data-update-badge">{{ bubbleText }}</span>
</button>
<section ref="panel" id="data-update-panel" class="data-update" popover="auto" aria-labelledby="data-update-title">
  <div class="data-update-head"><strong id="data-update-title">行情数据</strong>
    <button type="button" class="data-update-close" popovertarget="data-update-panel" popovertargetaction="hide" @click.prevent="closeDetails" aria-label="收起行情详情" autofocus>×</button>
  </div>
  <div class="hint data-update-time" role="status">最后完整更新（上海时间）<br><time v-if="status?.lastSuccess && !status?.lastSuccessError" :datetime="status.lastSuccess">{{ lastUpdateText }}</time><span v-else>{{ lastUpdateText }}</span></div>
  <div class="data-update-toolbar">
    <span v-if="running" class="hint" role="status">{{ progressInfo.label }}{{ progressPercent == null ? '…' : ' · 本阶段 ' + progressPercent + '%' }}</span>
    <span v-else class="hint">{{ status?.finishedAt ? checkResult : '尚无检查记录' }}</span>
    <button type="button" class="secondary" @click="update" :disabled="loading || !status || submitting || state.taskRunning || running" :aria-busy="String(submitting || running)">{{ running ? '更新中…' : '手动更新' }}</button>
  </div>
  <div v-if="running && status?.startedAt" class="hint data-update-time">本次开始：<time :datetime="status.startedAt">{{ displayTime(status.startedAt) }}</time></div>
  <div v-else-if="status?.finishedAt" class="hint data-update-time" role="status">最近检查结束：<time :datetime="status.finishedAt">{{ displayTime(status.finishedAt) }}</time></div>
  <div v-if="running || status?.total > 0" class="data-update-progress">
    <progress v-if="running" class="progress-inner" :class="{indeterminate: progressPercent == null}" :max="100" :value="progressPercent"
      :aria-label="progressInfo.label + '进度'" :aria-valuetext="progressInfo.counts || progressInfo.label + '进行中'"></progress>
    <span v-if="running" class="hint">{{ [progressInfo.counts, progressInfo.current, '累计失败 ' + (status.failed || 0)].filter(Boolean).join(' · ') }}</span>
    <span v-else class="hint">{{ status.done }}/{{ status.total }} · 失败 {{ status.failed || 0 }}</span>
  </div>
  <div v-if="status?.lastSuccessError" class="msg err" role="alert">{{ status.lastSuccessError }}</div>
  <div v-if="error" class="msg err" role="alert">{{ error }} <button type="button" class="secondary" @click="poll">重新读取</button></div>
  <div class="data-update-options">
  <details v-if="status?.error" class="ui-help error-details">
    <summary>{{ status.state==='error' ? '错误详情 · 可手动重试' : '错误详情' }}</summary>
    <div class="msg err">{{ status.error }}</div>
  </details>
  <details class="data-update-settings">
    <summary>自动更新：{{ status?.config.enabled ? '已开启 · 每日 ' + status.config.time : '未开启' }}</summary>
    <form novalidate @submit.prevent="save" class="data-update-config">
      <label class="checkbox-label"><input type="checkbox" v-model="enabled">开启自动更新</label>
      <label for="data-update-time">上海时间</label><input ref="time" id="data-update-time" type="time" v-model="time" required :aria-invalid="String(timeError)" :aria-describedby="timeError ? 'data-update-notice' : undefined" @input="timeError = false">
      <button type="submit" class="secondary" :disabled="saving || loading || !status" :aria-busy="String(saving)">保存设置</button>
      <span class="hint">仅服务运行时生效，建议收盘后更新。</span>
    </form>
  </details>
  </div>
  <div v-if="notice" id="data-update-notice" class="msg" role="status" aria-live="polite">{{ notice }}</div>
  <div v-if="status?.scheduleNote" class="hint">{{ status.scheduleNote }}</div>
</section>
</div>
  `,
};
