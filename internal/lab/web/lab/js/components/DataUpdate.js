import { Api } from '../api.js';

export default {
  name: 'DataUpdate',
  data() { return {status: null, loading: true, error: '', timer: null, disposed: false}; },
  computed: {
    bubbleText() {
      if (this.error || this.status?.readError) return '异常';
      return this.loading ? '读取中' : '只读';
    },
    benchmarkText() {
      if (this.loading) return '读取中…';
      if (this.error || this.status?.readError) return '读取失败';
      return this.status?.benchmarkDate || '暂无日线';
    },
    bubbleLabel() { return `行情数据，${this.bubbleText}，上证指数最新日线 ${this.benchmarkText}，展开详情`; },
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
    async poll() {
      try {
        const status = await Api.dataUpdateStatus();
        if (this.disposed) return;
        this.status = status;
        this.error = '';
      } catch {
        if (!this.disposed) this.error = '无法读取行情状态';
      } finally {
        if (!this.disposed) {
          this.loading = false;
          this.timer = setTimeout(() => this.poll(), 10000);
        }
      }
    },
  },
  template: `
<div class="data-update-widget">
  <button ref="bubble" type="button" class="data-update-bubble" popovertarget="data-update-panel" @click.prevent="toggleDetails"
    :data-fresh="bubbleText === '异常' ? 'false' : null" :aria-label="bubbleLabel" :title="bubbleLabel">
    <span class="data-update-dot" aria-hidden="true"></span><span>行情</span><span class="data-update-badge">{{ bubbleText }}</span>
  </button>
  <section ref="panel" id="data-update-panel" class="data-update" popover="auto" aria-labelledby="data-update-title">
    <div class="data-update-head">
      <strong id="data-update-title">行情数据</strong>
      <button type="button" class="data-update-close" popovertarget="data-update-panel" popovertargetaction="hide"
        @click.prevent="closeDetails" aria-label="收起行情详情" autofocus>×</button>
    </div>
    <p class="hint">行情由外部项目更新。</p>
    <div class="hint data-update-time" role="status">上证指数最新日线<br>
      <time v-if="!error && !status?.readError && status?.benchmarkDate" :datetime="status.benchmarkDate">{{ benchmarkText }}</time>
      <span v-else>{{ benchmarkText }}</span>
    </div>
    <p v-if="error || status?.readError" class="hint" role="status">{{ error || status.readError }}</p>
  </section>
</div>`,
};
