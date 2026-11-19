export default {
  name: 'ScreenStockLookup',
  props: {modelValue: {type: String, required: true}},
  emits: ['update:modelValue', 'diagnose'],
  data() { return {error: ''}; },
  computed: {
    code: {
      get() { return this.modelValue; },
      set(value) {
        this.error = '';
        this.$emit('update:modelValue', value);
      },
    },
  },
  methods: {
    clear() {
      this.code = '';
      this.$refs.code.focus();
    },
    submit() {
      const code = this.code.trim().toLowerCase();
      if (!/^(sh|sz|bj)?[0-9]{6}$/.test(code)) {
        this.error = '请输入六位股票代码，可带市场前缀';
        this.$refs.code.focus();
        return;
      }
      this.error = '';
      this.$emit('diagnose', code);
    },
  },
  template: `
<form class="screen-stock-lookup" novalidate @submit.prevent="submit">
  <label>股票代码
    <input ref="code" v-model="code" type="text" placeholder="如 sh600000" :aria-invalid="!!error"
      :aria-describedby="error ? 'screen-stock-error' : undefined">
  </label>
  <button v-if="code" type="button" class="secondary" aria-label="清除股票代码" @click="clear">清除</button>
  <button type="submit" class="secondary">查看K线</button>
  <span v-if="error" id="screen-stock-error" class="builder-field-error" role="alert">{{ error }}</span>
</form>
  `,
};
