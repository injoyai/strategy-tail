// Tab1Builder：02 自由搭建模式（策略搭建器）。
// 组件目录驱动：可运行组件与参数定义全部来自 GET /api/builder/components，
// 前端不做组件硬编码。条件树与后端 BuilderSpec 同形（group/condition 嵌套），
// 卖出侧 = 卖出信号树 + 独立风控列表（风控先求值）+ 显式持有至期末。
// 已保存策略与编辑草稿分离：加载→修改→运行不改动库中定义，显式保存才落盘；
// 版本冲突（409）保留当前输入等待重新处理。
import { state } from '../store.js';
import { Api } from '../api.js';
import { ACCOUNT_CONFIG } from '../config.js';
import { useId, shallowReactive } from '../vendor/vue.esm-browser.prod.js';
import { dlgMixin } from './dlgMixin.js';
import FactorCondition, { newFactorCondition, factorConditionError } from './FactorCondition.js';

const nodeDrag = shallowReactive({node: null, parent: null, target: null, after: false});
// 实例标识只用于视图；排序后保留输入与焦点，不写入策略 JSON。
const nodeKeys = new WeakMap();
let nextNodeKey = 0;
function nodeKey(node) {
  if (!nodeKeys.has(node)) nodeKeys.set(node, ++nextNodeKey);
  return nodeKeys.get(node);
}

export function reorderNode(parent, node, to) {
  const from = parent.indexOf(node);
  if (from < 0 || !Number.isInteger(to) || to < 0 || to >= parent.length || from === to) return false;
  parent.splice(from, 1);
  parent.splice(to, 0, node);
  return true;
}

const orderControls = `
  <div v-if="removable && parent.length > 1" class="bnode-order" role="group" :aria-label="'调整顺序：' + nodeLabel">
    <button type="button" class="secondary bnode-drag-handle" draggable="true"
      :aria-label="'拖动排序：' + nodeLabel" title="拖动排序，也可按上下方向键移动"
      @dragstart.stop="startDrag($event)" @dragend.stop="clearDrag"
      @keydown.up.prevent="moveSelf(-1)" @keydown.down.prevent="moveSelf(1)">⠿</button>
    <button type="button" class="secondary" :aria-label="'上移：' + nodeLabel" title="上移"
      :disabled="index === 0" @click="moveSelf(-1)">↑</button>
    <button type="button" class="secondary" :aria-label="'下移：' + nodeLabel" title="下移"
      :disabled="index === parent.length - 1" @click="moveSelf(1)">↓</button>
  </div>`;

// 组件默认参数（目录 Default → params 对象；undefined 缺省留给后端零值语义）。
function defaultParams(entry) {
  const p = {};
  for (const def of entry.params || []) {
    if (def.default !== undefined && def.default !== null) p[def.key] = def.default;
  }
  return p;
}

// 保留非法输入，直到用户修正；不得删除列表项后改变实际规则。
export function paramError(def, value) {
  if (value == null) return '';
  if (def.type === 'text') {
    return def.pattern && !new RegExp(def.pattern).test(value) ? `请按 ${def.desc} 填写` : '';
  }
  if (def.type === 'intList') {
    if (!Array.isArray(value)) return '请输入逗号分隔的整数周期，如 5,10,20';
    if (value.length === 1 || value.length > 64) return '请填写 2～64 个周期，或留空使用默认值';
    for (const item of value) {
      const error = paramError({...def, type: 'int'}, item);
      if (error) return error;
    }
    return '';
  }
  if (typeof value !== 'number' || !Number.isFinite(value)) return '请输入有效数字';
  if (def.type === 'int' && !Number.isInteger(value)) return '请输入整数';
  if (def.min != null && value < def.min) return `不能小于 ${def.min}`;
  if (def.max != null && value > def.max) return `不能大于 ${def.max}`;
  return '';
}

function nodeError(node, role) {
  if (role === 'risk' && node.type !== 'condition') return '独立风控不能分组，请分别添加每条风控';
  if (node.type === 'factor') return node.factor ? '' : '因子条件缺少定义，请删除后重新添加';
  if (node.type === 'group') return node.children.length ? '' : '请添加条件或删除空分组';
  const entry = state.builderCatalog.find(e => e.id === node.componentId);
  if (!entry || entry.role !== role) return '请选择该区域支持的组件';
  return '';
}

export function treeErrors(node, role) {
  if (!node) return [];
  const error = nodeError(node, role);
  if (error) return [error];
  if (node.type === 'group') return node.children.flatMap(child => treeErrors(child, role));
  if (node.type === 'factor') {
    const error = factorConditionError(node.factor, state.factorCatalog || []);
    return error ? [error.message] : [];
  }
  const entry = state.builderCatalog.find(e => e.id === node.componentId);
  return (entry.params || []).flatMap(def => {
    const error = paramError(def, node.params?.[def.key]);
    return error ? [`${entry.name} · ${def.label}：${error}`] : [];
  });
}

// 数值输入通用处理：空值删除键（后端按缺省处理），非空转数字。
function onNumberParam(node, def, ev) {
  const v = ev.target.value.trim();
  if (ev.target.validity?.badInput) node.params[def.key] = '';
  else if (v === '') delete node.params[def.key];
  else node.params[def.key] = +v;
}

// BuilderNodeEdit 递归条件节点编辑器。
// node：树节点；role：该位置要求的组件角色（buy/sell_signal/risk）；
// parent/index/removable：自身在父列表中的位置（删除用），根节点不可删。
export const NodeEdit = {
  name: 'BuilderNodeEdit',
  props: {
    node: {type: Object, required: true},
    role: {type: String, required: true},
    parent: {type: Array, required: true},
    index: {type: Number, required: true},
    removable: {type: Boolean, default: false},
  },
  setup() { return {paramPrefix: 'builder-param-' + useId()}; },
  data() { return {nodeDrag}; },
  beforeUnmount() {
    if (nodeDrag.node === this.node || nodeDrag.target === this.node) this.clearDrag();
  },
  computed: {
    factorCatalog() { return state.factorCatalog || []; },
    nodeError() { return nodeError(this.node, this.role); },
    entry() { return state.builderCatalog.find(e => e.id === this.node.componentId) || null; },
    entryGroups() {
      const m = new Map();
      for (const e of state.builderCatalog) {
        if (e.role !== this.role) continue;
        if (!m.has(e.category)) m.set(e.category, []);
        m.get(e.category).push(e);
      }
      return [...m.entries()].map(([category, items]) => ({category, items}));
    },
    isGroup() { return this.node.type === 'group'; },
    nodeLabel() { return this.isGroup ? '分组' : (this.node.type === 'factor' ? '因子条件' : this.entry?.name || '条件'); },
  },
  methods: {
    showHelp() {
      const tip = this.$refs.help, trigger = this.$refs.helpTrigger;
      if (tip.matches(':popover-open')) return;
      tip.showPopover();
      const rect = trigger.getBoundingClientRect();
      const view = trigger.ownerDocument.defaultView;
      const left = Math.max(12, Math.min(rect.right - tip.offsetWidth, view.innerWidth - tip.offsetWidth - 12));
      const top = rect.bottom + tip.offsetHeight <= view.innerHeight - 12 ? rect.bottom : Math.max(12, rect.top - tip.offsetHeight);
      tip.style.left = left + 'px';
      tip.style.top = top + 'px';
    },
    hideHelp() {
      if (!this.$refs.helpTrigger.matches(':focus') && !this.$refs.help.matches(':hover')) this.$refs.help.hidePopover();
    },
    nodeKey,
    moveSelf(offset) { this.moveNode(this.node, this.index + offset); },
    moveNode(node, to) {
      const focused = this.$el?.ownerDocument.activeElement;
      if (!reorderNode(this.parent, node, to)) {
        // 非法落点（越界或与当前位置相同）：轻量提示，不改变顺序
        state.builderMsg = {text: '未调整顺序：目标位置无效或与当前位置相同。', ok: true};
        return;
      }
      this.$nextTick(() => {
        if (focused?.isConnected) {
          const control = focused.disabled ? focused.parentElement.querySelector('.bnode-drag-handle') : focused;
          control?.focus({preventScroll: true});
        }
      });
      const name = node.type === 'group' ? '分组' : node.type === 'factor' ? '因子条件' : (state.builderCatalog.find(e => e.id === node.componentId)?.name || '条件');
      state.builderMsg = {text: `已将「${name}」移到第 ${to + 1} 项；保存后保留新顺序。`, ok: true};
    },
    startDrag(ev) {
      nodeDrag.node = this.node;
      nodeDrag.parent = this.parent;
      nodeDrag.target = null;
      ev.dataTransfer.effectAllowed = 'move';
      ev.dataTransfer.setData('text/plain', this.nodeLabel);
      ev.dataTransfer.setDragImage(this.$el, 24, 24);
    },
    clearDrag() { Object.assign(nodeDrag, {node: null, parent: null, target: null, after: false}); },
    dragOver(ev) {
      if (!this.removable || nodeDrag.parent !== this.parent || nodeDrag.node === this.node) {
        nodeDrag.target = null;
        return;
      }
      ev.preventDefault();
      ev.dataTransfer.dropEffect = 'move';
      const rect = this.$el.getBoundingClientRect();
      nodeDrag.target = this.node;
      nodeDrag.after = ev.clientY >= rect.top + rect.height / 2;
    },
    dragLeave(ev) {
      if (nodeDrag.target === this.node && !this.$el.contains(ev.relatedTarget)) nodeDrag.target = null;
    },
    dropNode(ev) {
      if (nodeDrag.parent !== this.parent || nodeDrag.target !== this.node) return;
      ev.preventDefault();
      const from = this.parent.indexOf(nodeDrag.node);
      const to = this.index + (nodeDrag.after ? 1 : 0) - (from < this.index ? 1 : 0);
      this.moveNode(nodeDrag.node, to);
      this.clearDrag();
    },
    removeSelf() { this.parent.splice(this.index, 1); },
    setOp(e) { this.node.op = e.target.value; },
    onCompChange(e) {
      const ne = state.builderCatalog.find(x => x.id === e.target.value);
      if (!ne) return;
      this.node.componentId = ne.id;
      this.node.params = defaultParams(ne);
    },
    addCond() {
      const first = state.builderCatalog.find(e => e.role === this.role);
      if (!first) return;
      this.node.children.push({type: 'condition', componentId: first.id, params: defaultParams(first)});
    },
    addFactor() {
      const first = this.factorCatalog[0];
      if (!first || this.role === 'risk') return;
      this.node.children.push({type: 'factor', factor: newFactorCondition(first)});
      this.$nextTick(() => this.$el?.querySelector('.bnode-children > :last-child select')?.focus());
    },
    addGroup() {
      const first = state.builderCatalog.find(e => e.role === this.role);
      if (!first) return;
      this.node.children.push({type: 'group', op: 'and',
        children: [{type: 'condition', componentId: first.id, params: defaultParams(first)}]});
    },
    onParamInput(def, ev) {
      this.node.params ||= {};
      if (def.type === 'text') {
        const v = ev.target.value;
        if (v === '') delete this.node.params[def.key];
        else this.node.params[def.key] = v;
      } else if (def.type === 'intList') {
        const v = ev.target.value.trim();
        if (v === '') { delete this.node.params[def.key]; return; }
        const parts = v.split(/[,，\s]+/);
        const arr = parts.map(Number);
        this.node.params[def.key] = parts.every(Boolean) && arr.every(Number.isInteger) ? arr : v;
      } else {
        onNumberParam(this.node, def, ev);
      }
    },
    fieldError(def) { return paramError(def, this.node.params?.[def.key]); },
    paramId(def) { return this.paramPrefix + '-' + def.key; },
    paramValue(def) {
      const v = this.node.params?.[def.key];
      if (def.type === 'intList') return Array.isArray(v) ? v.join(',') : (v == null ? '' : String(v));
      return v == null ? '' : v;
    },
  },
  template: `
  <div class="bnode" :class="[isGroup ? 'bnode-group' : 'bnode-cond', {
    'bnode-dragging': nodeDrag.node === node,
    'bnode-drop-before': nodeDrag.target === node && !nodeDrag.after,
    'bnode-drop-after': nodeDrag.target === node && nodeDrag.after}]"
    @dragover.stop="dragOver($event)" @dragleave.stop="dragLeave($event)" @drop.stop="dropNode($event)">
    <p v-if="nodeError" :id="paramPrefix + '-node-error'" class="builder-field-error">{{ nodeError }}</p>
    <template v-if="isGroup">
      <div class="bnode-head">
        ${orderControls}
        <span class="bnode-tag">分组</span>
        <select class="bnode-op" :value="node.op" @change="setOp($event)"
          :aria-invalid="String(!!nodeError)" :aria-describedby="nodeError ? paramPrefix + '-node-error' : undefined"
          :aria-label="'分组组合方式（' + (node.op === 'and' ? '全部满足' : '任一满足') + '）'">
          <option value="and">全部满足（AND）</option>
          <option value="or">任一满足（OR）</option>
        </select>
        <button v-if="removable" type="button" class="cond-del" aria-label="删除该分组" @click="removeSelf">删除</button>
      </div>
      <div class="bnode-children">
        <template v-for="(c, i) in node.children" :key="nodeKey(c)">
          <builder-node-edit :node="c" :role="role" :parent="node.children" :index="i" :removable="true" />
        </template>
      </div>
      <div class="bnode-actions">
        <button type="button" class="secondary" @click="addCond">+ 条件</button>
        <button v-if="role !== 'risk'" type="button" class="secondary" @click="addFactor" :disabled="!factorCatalog.length">+ 因子条件</button>
        <button type="button" class="secondary" @click="addGroup">+ 子分组</button>
      </div>
    </template>
    <template v-else-if="node.type === 'factor'">
      <div class="bnode-head bnode-condition-row">
        ${orderControls}
        <factor-condition v-if="node.factor" :condition="node.factor" :catalog="factorCatalog" />
        <button v-if="removable" type="button" class="cond-del" aria-label="删除因子条件" @click="removeSelf">删除</button>
      </div>
    </template>
    <template v-else>
      <div class="bnode-head bnode-condition-row">
        ${orderControls}
        <select class="bnode-comp" :value="node.componentId" @change="onCompChange($event)"
          :aria-invalid="String(!!nodeError)" :aria-describedby="nodeError ? paramPrefix + '-node-error' : undefined"
          :aria-label="'选择组件（' + (entry ? entry.name : '') + '）'">
          <optgroup v-for="g in entryGroups" :key="g.category" :label="g.category">
            <option v-for="e in g.items" :key="e.id" :value="e.id">{{ e.name }}</option>
          </optgroup>
        </select>
        <template v-if="entry">
          <div class="bnode-param" :class="{'bnode-param-wide': def.type === 'text' || def.type === 'intList'}" v-for="def in entry.params" :key="def.key">
            <label class="bnode-param-lbl" :for="paramId(def)">{{ def.label }}<span v-if="def.unit" class="bnode-unit">{{ def.unit }}</span></label>
            <div class="bnode-field">
              <input v-if="def.type === 'text' || def.type === 'intList'" type="text" :id="paramId(def)" :value="paramValue(def)"
                @input="onParamInput(def, $event)" :placeholder="def.desc || ''" :title="def.desc || ''"
                :aria-invalid="String(!!fieldError(def))" :aria-describedby="fieldError(def) ? paramId(def) + '-error' : undefined">
              <input v-else type="number" :id="paramId(def)" :step="def.step || (def.type === 'int' ? 1 : 'any')"
                :min="def.min != null ? def.min : undefined" :max="def.max != null ? def.max : undefined"
                :value="paramValue(def)" @input="onParamInput(def, $event)" :title="def.desc || ''"
                :aria-invalid="String(!!fieldError(def))" :aria-describedby="fieldError(def) ? paramId(def) + '-error' : undefined">
              <p v-if="fieldError(def)" :id="paramId(def) + '-error'" class="builder-field-error">{{ fieldError(def) }}</p>
            </div>
          </div>
        </template>
        <button v-if="removable" type="button" class="cond-del" :aria-label="'删除条件 ' + (entry ? entry.name : '')" @click="removeSelf">删除</button>
        <span class="bnode-help" v-if="entry && entry.desc" @mouseenter="showHelp" @mouseleave="hideHelp">
          <button ref="helpTrigger" type="button" class="bnode-help-trigger" :aria-label="'条件说明：' + entry.name"
            :aria-describedby="paramPrefix + '-help'" @focus="showHelp" @blur="hideHelp" @click="showHelp">?</button>
          <span ref="help" :id="paramPrefix + '-help'" class="bnode-tooltip" role="tooltip" popover="auto"><span>{{ entry.desc }}</span></span>
        </span>
      </div>
    </template>
  </div>
  `,
};
NodeEdit.components = {BuilderNodeEdit: NodeEdit, FactorCondition};

export default {
  name: 'Tab1Builder',
  mixins: [dlgMixin],
  components: {BuilderNodeEdit: NodeEdit},
  inject: ['startTask', 'openRunConfig'],
  setup() { return {state}; },
  data() { return {catalogReady: false, loadingCatalog: false, factorCatalogError: '', submitting: false, nameError: '', sideErrors: {buy: '', sell: ''}, replaceAction: null}; },
  created() { if (!state.builderSpec) this.newStrategy(); },
  computed: {
    spec() { return state.builderSpec; },
    dirty() { return !!state.builderSpec && JSON.stringify(state.builderSpec) !== state.builderBaseline; },
  },
  mounted() {
    this.init();
  },
  methods: {
    nodeKey,
    closeDlg() {
      this.open = false;
      this.replaceAction = null;
      if (this.$refs.strategySel) this.$refs.strategySel.value = state.builderId;
    },
    requestReplace(action) {
      if (state.builderBusy) return;
      if (this.dirty) { this.replaceAction = action; this.open = true; }
      else action();
    },
    replaceDraft() {
      const action = this.replaceAction;
      this.open = false;
      this.replaceAction = null;
      action?.();
    },
    async init() {
      if (this.loadingCatalog) return;
      this.loadingCatalog = true;
      if (!state.builderSpec) this.newStrategy();
      try {
        const [components, factors] = await Promise.allSettled([Api.builderComponents(), Api.factors()]);
        if (components.status === 'rejected') throw components.reason;
        state.builderCatalog = components.value.components || [];
        state.builderUnavailable = components.value.unavailable || [];
        if (factors.status === 'fulfilled') {
          state.factorCatalog = factors.value;
          this.factorCatalogError = '';
        } else this.factorCatalogError = '因子目录加载失败：' + factors.reason.message + '。已有组件条件仍可编辑。';
        this.catalogReady = true;
        state.builderMsg = {text: '', ok: true};
      } catch (e) { state.builderMsg = {text: '组件目录加载失败: ' + e.message, ok: false}; return; }
      finally { this.loadingCatalog = false; }
      this.loadStrategies();
    },
    async loadStrategies() {
      try { state.builderStrategies = (await Api.builderStrategies()).strategies || []; }
      catch (e) { state.builderMsg = {text: '策略列表加载失败: ' + e.message, ok: false}; }
    },
    newStrategy() {
      if (state.builderBusy) return;
      state.builderSpec = {version: 1, name: '', buy: null, sellSignal: null, holdToEnd: false, risks: []};
      state.builderId = '';
      state.builderVersion = 0;
      state.builderBaseline = JSON.stringify(state.builderSpec);
      state.builderMsg = {text: '已新建空白策略：添加买入与卖出条件后才能回测。', ok: true};
      this.nameError = '';
      this.sideErrors = {buy: '', sell: ''};
    },
    addBuyRoot() {
      const first = state.builderCatalog.find(e => e.role === 'buy');
      if (!first) return;
      state.builderSpec.buy = {type: 'group', op: 'and',
        children: [{type: 'condition', componentId: first.id, params: defaultParams(first)}]};
      this.sideErrors.buy = '';
    },
    addSellRoot() {
      const first = state.builderCatalog.find(e => e.role === 'sell_signal');
      if (!first) return;
      state.builderSpec.sellSignal = {type: 'group', op: 'or',
        children: [{type: 'condition', componentId: first.id, params: defaultParams(first)}]};
      this.sideErrors.sell = '';
    },
    addFactorRoot(side) {
      const entry = state.factorCatalog?.[0];
      if (!entry) return;
      state.builderSpec[side] = {type: 'group', op: side === 'buy' ? 'and' : 'or',
        children: [{type: 'factor', factor: newFactorCondition(entry)}]};
      this.sideErrors[side === 'buy' ? 'buy' : 'sell'] = '';
      this.$nextTick(() => this.$refs[side + 'Tree']?.$el.querySelector('.bnode-factor-fields select')?.focus());
    },
    addRisk() {
      const first = state.builderCatalog.find(e => e.role === 'risk');
      if (!first) return;
      (state.builderSpec.risks ||= []).push({type: 'condition', componentId: first.id, params: defaultParams(first)});
      this.sideErrors.sell = '';
    },
    removeRisk(i) { state.builderSpec.risks.splice(i, 1); },
    async loadStrategy() {
      const sel = this.$refs.strategySel;
      const id = sel && sel.value;
      if (!id || state.builderBusy) return;
      const draft = state.builderSpec;
      const before = JSON.stringify(draft);
      state.builderBusy = true;
      try {
        const st = (await Api.builderStrategy(id)).strategy;
        if (state.builderSpec !== draft || JSON.stringify(draft) !== before) {
          if (this.$refs.strategySel) this.$refs.strategySel.value = state.builderId;
          state.builderMsg = {text: '加载期间内容已修改，保留当前草稿。请再次选择要加载的策略。', ok: false};
          return;
        }
        state.builderSpec = st.spec;
        state.builderId = st.id;
        state.builderVersion = st.version;
        state.builderBaseline = JSON.stringify(st.spec);
        state.builderMsg = {text: `已加载「${st.name}」（版本 ${st.version}）：修改后运行不影响库中定义，显式保存才更新。`, ok: true};
      } catch (e) { state.builderMsg = {text: e.message, ok: false}; }
      finally { state.builderBusy = false; }
    },
    focusError() { this.$nextTick(() => this.$el.querySelector('[aria-invalid="true"]')?.focus()); },
    validateLocal(forRun = true) {
      const s = state.builderSpec;
      this.nameError = !s.name.trim() ? '请填写策略名称（保存与回测都需要）' : '';
      this.sideErrors = {
        buy: forRun && !s.buy ? '请添加买入条件，或选择「全部样本买入」' : '',
        sell: forRun && !s.sellSignal && !s.risks?.length && !s.holdToEnd ? '请添加卖出信号、风控或选择「持有至期末」' : '',
      };
      return [this.nameError, ...Object.values(this.sideErrors),
        ...treeErrors(s.buy, 'buy'), ...treeErrors(s.sellSignal, 'sell_signal'),
        ...(s.risks || []).flatMap(r => treeErrors(r, 'risk'))].filter(Boolean);
    },
    async saveStrategy(asNew = false) {
      if (state.builderBusy) return;
      const errs = this.validateLocal(false);
      if (errs.length) { state.builderMsg = {text: errs.join('\n'), ok: false}; this.focusError(); return; }
      const draft = state.builderSpec;
      const spec = JSON.parse(JSON.stringify(draft));
      spec.version = 1;
      state.builderBusy = true;
      try {
        const st = state.builderId && !asNew
          ? (await Api.updateBuilderStrategy(state.builderId, spec, state.builderVersion)).strategy
          : (await Api.createBuilderStrategy(spec)).strategy;
        if (state.builderSpec !== draft) return;
        state.builderId = st.id;
        state.builderVersion = st.version;
        state.builderBaseline = JSON.stringify(spec);
        const changed = JSON.stringify(draft) !== JSON.stringify(spec);
        state.builderMsg = {text: `已保存「${st.name}」（版本 ${st.version}）` +
          (changed ? '；保存期间的新编辑仍在草稿中，请再次保存。' : ''), ok: true};
        this.loadStrategies();
      } catch (e) {
        state.builderMsg = {text: '保存未完成，当前内容未丢失：' + e.message, ok: false};
      } finally { state.builderBusy = false; }
    },
    saveAsNew() { return this.saveStrategy(true); },
    requestRun() {
      if (this.submitting || state.taskRunning || !this.catalogReady) return;
      const errs = this.validateLocal();
      if (errs.length) { state.builderMsg = {text: errs.join('\n'), ok: false}; this.focusError(); return; }
      this.openRunConfig('builder', () => this.runBuilder());
    },
    async runBuilder() {
      if (this.submitting || state.taskRunning || !this.catalogReady) return;
      state.builderMsg = {text: '', ok: true};
      const errs = this.validateLocal();
      if (errs.length) { state.builderMsg = {text: errs.join('\n'), ok: false}; this.focusError(); return; }
      const c = state.runCfg;
      if (!(+c.startYear > 0) || !(+c.endYear > 0) || +c.startYear > +c.endYear) {
        state.builderMsg = {text: '时间范围无效：开始年份不能大于结束年份', ok: false};
        return;
      }
      const cfg = {startYear: +c.startYear, endYear: +c.endYear, sampleMode: c.sampleMode,
        builder: JSON.parse(JSON.stringify(state.builderSpec)), account: ACCOUNT_CONFIG};
      if (cfg.sampleMode === 'random') cfg.sampleSize = +c.sampleSize;
      if (cfg.sampleMode === 'codes') {
        cfg.sampleCodes = c.sampleCodes.split(',').map(s => s.trim()).filter(Boolean);
        if (!cfg.sampleCodes.length) { state.builderMsg = {text: '指定代码样本为空', ok: false}; return; }
      }
      this.submitting = true;
      try {
        await Api.runBuilder(cfg);
        state.builderMsg = {text: '回测已提交', ok: true};
        this.startTask('backtest');
        return true;
      } catch (e) { state.builderMsg = {text: e.message, ok: false}; }
      finally { this.submitting = false; }
    },
  },
  template: `
  <div>
    <div v-if="spec" class="builder-editor">
      <div v-if="!catalogReady || factorCatalogError" class="editor-actions" role="status">
        <span class="hint">{{ loadingCatalog ? '正在加载目录…' : factorCatalogError || '组件目录尚未加载' }}</span>
        <button type="button" class="secondary" @click="init" :disabled="loadingCatalog">重试加载</button>
      </div>

      <div class="builder-toolbar">
        <div class="builder-fields">
        <div class="form-row builder-name"><label for="builderName">策略名称 <span class="builder-save-status">{{ state.builderId ? (dirty ? '有修改' : '已保存') + ' · v' + state.builderVersion : '未保存草稿' }}</span></label>
          <input id="builderName" type="text" v-model="spec.name" :aria-invalid="String(!!nameError)" :aria-describedby="nameError ? 'builder-name-error' : undefined" @input="nameError = ''" maxlength="80" placeholder="如：MACD反转-试验">
          <p v-if="nameError" id="builder-name-error" class="builder-field-error">{{ nameError }}</p>
        </div>
        <div class="form-row"><label for="strategySel">策略库</label>
          <select id="strategySel" ref="strategySel" :disabled="state.builderBusy" @change="requestReplace(loadStrategy)" aria-label="加载已保存策略">
            <option value="">选择已保存策略…</option>
            <option v-for="s in state.builderStrategies" :key="s.id" :value="s.id">{{ s.name }}（v{{ s.version }}）</option>
          </select>
        </div>
        </div>
        <div class="builder-actionbar">
        <div class="builder-actions" role="group" aria-label="策略管理">
          <button type="button" class="secondary" @click="requestReplace(newStrategy)" :disabled="state.builderBusy">新建</button>
          <button type="button" class="secondary" @click="saveStrategy()" :disabled="state.builderBusy || !catalogReady" :aria-busy="String(state.builderBusy)">保存</button>
          <button type="button" class="secondary" @click="saveAsNew" :disabled="state.builderBusy || !catalogReady">另存为</button>
        </div>
          <button type="button" class="primary" @click="requestRun" :disabled="state.taskRunning || submitting || !catalogReady" :aria-busy="String(submitting || state.taskRunning)">运行回测</button>
        </div>
      </div>

      <div class="subhead">买入条件</div>
      <button v-if="spec.buy" type="button" class="secondary" @click="spec.buy = null">清空买入条件</button>
      <p v-if="sideErrors.buy" id="builder-buy-error" class="builder-field-error">{{ sideErrors.buy }}</p>
      <builder-node-edit v-if="spec.buy" ref="buyTree" :node="spec.buy" role="buy" :parent="[]" :index="-1" />
      <div v-else class="empty" style="padding:12px">
        买入条件为空（不能回测）。
        <button type="button" class="secondary" @click="addBuyRoot" :disabled="!catalogReady" :aria-invalid="String(!!sideErrors.buy)" :aria-describedby="sideErrors.buy ? 'builder-buy-error' : undefined">添加买入条件</button>
        <button type="button" class="secondary" @click="addFactorRoot('buy')" :disabled="!catalogReady || !state.factorCatalog?.length" aria-label="添加因子买入条件">添加因子条件</button>
      </div>

      <div class="subhead">卖出信号</div>
      <button v-if="spec.sellSignal" type="button" class="secondary" @click="spec.sellSignal = null">清空卖出信号</button>
      <p v-if="sideErrors.sell" id="builder-sell-error" class="builder-field-error">{{ sideErrors.sell }}</p>
      <builder-node-edit v-if="spec.sellSignal" ref="sellSignalTree" :node="spec.sellSignal" role="sell_signal" :parent="[]" :index="-1" />
      <div v-else class="empty" style="padding:12px">
        未设置卖出信号。
        <button type="button" class="secondary" @click="addSellRoot" :disabled="!catalogReady" :aria-invalid="String(!!sideErrors.sell)" :aria-describedby="sideErrors.sell ? 'builder-sell-error' : undefined">添加卖出信号</button>
        <button type="button" class="secondary" @click="addFactorRoot('sellSignal')" :disabled="!catalogReady || !state.factorCatalog?.length" aria-label="添加因子卖出信号">添加因子条件</button>
      </div>

      <div class="subhead">独立风控</div>
      <div v-for="(r, i) in spec.risks" :key="nodeKey(r)" class="risk-row">
        <builder-node-edit :node="r" role="risk" :parent="spec.risks" :index="i" :removable="true" />
      </div>
      <div class="editor-actions">
        <button type="button" class="secondary" @click="addRisk" :disabled="!catalogReady">+ 添加风控</button>
        <label class="hold-label"><input type="checkbox" v-model="spec.holdToEnd" @change="sideErrors.sell = ''"> 持有至期末（虚拟估值）</label>
      </div>
      <details class="ui-help"><summary>搭建说明</summary><p class="hint">回测使用当前草稿，保存才更新策略库。全部样本回测可添加“基准 → 全部样本买入”。风控任一触发即卖出，优先于卖出信号，成交遵循 T+1；期末估值不算实际成交。</p></details>

      <div class="msg" :class="state.builderMsg.ok ? 'ok' : 'err'" role="status" aria-live="polite">{{ state.builderMsg.text }}</div>

      <details class="builder-unavailable">
        <summary>暂未接入的组件（{{ state.builderUnavailable.length }}）</summary>
        <ul>
          <li v-for="u in state.builderUnavailable" :key="u.id"><b>{{ u.name }}</b>：{{ u.reason }}</li>
        </ul>
      </details>
    </div>
    <dialog ref="dlg" aria-labelledby="builder-discard-title" aria-describedby="builder-discard-info" @cancel.prevent="closeDlg">
      <h3 id="builder-discard-title">替换当前草稿</h3>
      <p id="builder-discard-info">当前修改尚未保存。继续将替换这些内容，已保存的策略不受影响。</p>
      <div class="editor-actions">
        <button type="button" class="secondary" autofocus @click="closeDlg">保留草稿</button>
        <button type="button" class="danger" @click="replaceDraft">放弃修改并继续</button>
      </div>
    </dialog>
  </div>
  `,
};
