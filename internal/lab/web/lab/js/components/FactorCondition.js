import { useId } from '../vendor/vue.esm-browser.prod.js';
import { factorHasDays, factorTypeName, groupFactors } from '../format.js';

export function newFactorCondition(entry) {
  return {kind: entry.kind, days: entry.defaultDays, factorVersion: entry.implementationVersion,
    operator: 'between', min: null, max: null};
}

export function factorConditionError(c, catalog) {
  const entry = catalog.find(e => e.kind === c?.kind);
  if (!entry) return {field: 'kind', message: '因子目录中没有该因子，请重新选择或重试加载目录'};
  if (!Number.isInteger(c.days) || c.days <= 0) return {field: 'days', message: '请填写正整数天数'};
  const version = c.factorVersion ?? 0;
  if (!Number.isInteger(version) || version < 0 || (version > 0 && version !== entry.implementationVersion)) {
    return {field: 'kind', message: '因子实现已更新，请确认阈值后使用当前版本'};
  }
  if (!['gte', 'lte', 'between'].includes(c.operator)) return {field: 'operator', message: '请选择比较方式'};
  for (const key of c.operator === 'gte' ? ['min'] : c.operator === 'lte' ? ['max'] : ['min', 'max']) {
    if (!Number.isFinite(c[key])) return {field: key, message: `请填写有效${key === 'min' ? '下限' : '上限'}`};
  }
  if (c.operator === 'between' && c.min > c.max) return {field: 'min', message: '下限不能大于上限'};
  return null;
}

export default {
  name: 'FactorCondition',
  props: {condition: {type: Object, required: true}, catalog: {type: Array, required: true}},
  setup() { return {prefix: 'factor-condition-' + useId(), factorTypeName}; },
  computed: {
    entry() { return this.catalog.find(e => e.kind === this.condition.kind); },
    groups() { return groupFactors(this.catalog); },
    hasDays() { return this.entry ? factorHasDays(this.entry) : true; },
    isRatio() { return this.entry?.unit === 'ratio'; },
    error() { return factorConditionError(this.condition, this.catalog); },
    fields() {
      const fields = this.hasDays ? [{key: 'days', label: this.entry?.parameterLabel || '回看天数'}] : [];
      if (this.condition.operator !== 'lte') fields.push({key: 'min', label: '下限'});
      if (this.condition.operator !== 'gte') fields.push({key: 'max', label: '上限'});
      return fields;
    },
    stale() { return this.entry && this.error?.field === 'kind'; },
  },
  methods: {
    fieldError(key) { return this.error?.field === key ? this.error.message : ''; },
    id(key) { return this.prefix + '-' + key; },
    selectFactor(ev) {
      const entry = this.catalog.find(e => e.kind === ev.target.value);
      if (entry) Object.assign(this.condition, newFactorCondition(entry));
    },
    useCurrentVersion() {
      this.condition.factorVersion = this.entry.implementationVersion;
      this.$nextTick(() => this.$el.querySelector('select')?.focus());
    },
    displayValue(key) {
      const value = this.condition[key];
      if (value == null) return '';
      return key !== 'days' && this.isRatio && typeof value === 'number' ? +(value * 100).toPrecision(12) : value;
    },
    input(key, ev) {
      const text = ev.target.value.trim();
      if (ev.target.validity?.badInput) this.condition[key] = '';
      else if (text === '') this.condition[key] = null;
      else this.condition[key] = key !== 'days' && this.isRatio ? +text / 100 : +text;
    },
  },
  template: `
    <div class="bnode-factor-fields">
      <div class="bnode-param bnode-param-wide">
        <label class="bnode-param-lbl" :for="id('kind')">因子</label>
        <div class="bnode-field">
          <select :id="id('kind')" :value="condition.kind" @change="selectFactor"
            :aria-invalid="String(!!fieldError('kind'))" :aria-describedby="fieldError('kind') ? id('kind') + '-error' : undefined">
            <option v-if="!entry" :value="condition.kind">{{ condition.kind || '请选择因子' }}（不可用）</option>
            <optgroup v-for="g in groups" :key="g.category" :label="g.category">
              <option v-for="e in g.entries" :key="e.kind" :value="e.kind">{{ factorTypeName(e) }}</option>
            </optgroup>
          </select>
          <p v-if="fieldError('kind')" :id="id('kind') + '-error'" class="builder-field-error">{{ fieldError('kind') }}</p>
          <button v-if="stale" type="button" class="secondary" @click="useCurrentVersion">使用当前版本</button>
        </div>
      </div>
      <div class="bnode-param">
        <label class="bnode-param-lbl" :for="id('operator')">比较</label>
        <div class="bnode-field">
          <select :id="id('operator')" v-model="condition.operator"
            :aria-invalid="String(!!fieldError('operator'))" :aria-describedby="fieldError('operator') ? id('operator') + '-error' : undefined">
            <option value="between">介于</option><option value="gte">≥ 至少</option><option value="lte">≤ 至多</option>
          </select>
          <p v-if="fieldError('operator')" :id="id('operator') + '-error'" class="builder-field-error">{{ fieldError('operator') }}</p>
        </div>
      </div>
      <div class="bnode-param" v-for="field in fields" :key="field.key">
        <label class="bnode-param-lbl" :for="id(field.key)">{{ field.label }}<span v-if="field.key !== 'days' && isRatio" class="bnode-unit">%</span></label>
        <div class="bnode-field">
          <input type="number" :id="id(field.key)" :step="field.key === 'days' ? 1 : 'any'" :min="field.key === 'days' ? 1 : undefined"
            :value="displayValue(field.key)" @input="input(field.key, $event)"
            :aria-invalid="String(!!fieldError(field.key))" :aria-describedby="fieldError(field.key) ? id(field.key) + '-error' : undefined">
          <p v-if="fieldError(field.key)" :id="id(field.key) + '-error'" class="builder-field-error">{{ fieldError(field.key) }}</p>
        </div>
      </div>
    </div>
  `,
};
