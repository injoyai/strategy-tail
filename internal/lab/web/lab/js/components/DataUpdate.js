import { state } from '../store.js';
import { Api } from '../api.js';

export default {
  name: 'DataUpdate',
  inject: ['startTask'],
  setup() { return {state}; },
  data() { return {status: null, loading: true, submitting: false, saving: false, error: '', notice: '', enabled: false, time: '15:45', initialized: false, timer: null, disposed: false}; },
  computed: {
    running() { return this.status?.state === 'running'; },
  },
  mounted() { this.poll(); },
  unmounted() { this.disposed = true; clearTimeout(this.timer); },
  methods: {
    displayTime(value) { return value ? new Date(value).toLocaleString('zh-CN', {timeZone: 'Asia/Shanghai'}) : '尚无成功记录'; },
    applyStatus(s) {
      const wasRunning = state.dataUpdating;
      this.status = s;
      state.dataUpdating = s.state === 'running';
      state.taskRunning = state.dataUpdating || state.status?.state === 'running';
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
      if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(this.time)) { this.notice = '请输入有效的更新时间，例如 15:45'; this.$refs.time.focus(); return; }
      this.saving = true;
      this.notice = '';
      try { this.applyStatus(await Api.configureDataUpdate({enabled: this.enabled, time: this.time})); this.notice = '自动更新设置已保存'; }
      catch (e) { this.notice = '保存设置失败：' + e.message; }
      finally { this.saving = false; }
    },
  },
  template: `
<section class="card data-update" aria-labelledby="data-update-title">
  <div class="data-update-toolbar">
    <div><strong id="data-update-title">行情数据</strong><span class="hint">最近完整更新：{{ displayTime(status?.lastSuccess) }}</span></div>
    <button type="button" class="secondary" @click="update" :disabled="loading || !status || submitting || state.taskRunning || running" :aria-busy="String(submitting || running)">{{ running ? '更新中…' : '手动更新' }}</button>
  </div>
  <div v-if="loading" role="status" class="hint">正在读取更新状态…</div>
  <div v-if="running" class="hint" role="status">更新进度见公共任务栏；当前任务完成前，回测与研究任务已锁定。</div>
  <div v-else-if="status?.state==='done'" class="hint" role="status">{{ status.message || '数据检查完成' }}</div>
  <div v-if="status?.error" class="msg err" role="alert">{{ status.error }}<span v-if="status.state==='error'">。失败项可手动重试，已有数据不会整库清空。</span></div>
  <div v-if="error" class="msg err" role="alert">{{ error }} <button type="button" class="secondary" @click="poll">重新读取</button></div>
  <details class="data-update-settings">
    <summary>自动更新：{{ status?.config.enabled ? '已开启 · 每日 ' + status.config.time : '未开启' }}</summary>
    <form novalidate @submit.prevent="save" class="data-update-config">
      <label class="checkbox-label"><input type="checkbox" v-model="enabled">开启自动更新</label>
      <label for="data-update-time">上海时间</label><input ref="time" id="data-update-time" type="time" v-model="time" required>
      <button type="submit" class="secondary" :disabled="saving || loading || !status" :aria-busy="String(saving)">保存设置</button>
      <span class="hint">服务运行时在交易日检查；任务占用时延后更新，失败后可手动重试。建议收盘后更新。</span>
    </form>
  </details>
  <div class="msg" role="status" aria-live="polite">{{ notice }}</div>
  <div v-if="status?.scheduleNote" class="hint">{{ status.scheduleNote }}</div>
</section>
  `,
};
