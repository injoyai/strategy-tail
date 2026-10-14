import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

const moduleURL = source => 'data:text/javascript;base64,' + Buffer.from(source).toString('base64');
const read = path => fs.readFileSync(new URL(path, import.meta.url), 'utf8');
const Vue = await import(moduleURL(read('./lab/js/vendor/vue.esm-browser.prod.js')));
const state = globalThis.builderTestState = Vue.reactive({});
const api = globalThis.builderTestApi = {};
globalThis.builderTestVue = Vue;
globalThis.builderTestDialog = (await import(moduleURL(read('./lab/js/components/dlgMixin.js')))).dlgMixin;
const source = read('./lab/js/components/Tab1Builder.js')
  .replace("import { state } from '../store.js';", 'const state = globalThis.builderTestState;')
  .replace("import { Api } from '../api.js';", 'const Api = globalThis.builderTestApi;')
  .replace("import { useId, shallowReactive } from '../vendor/vue.esm-browser.prod.js';", 'const {useId, shallowReactive} = globalThis.builderTestVue;')
  .replace("import { dlgMixin } from './dlgMixin.js';", 'const dlgMixin = globalThis.builderTestDialog;');
const {default: Builder, NodeEdit, paramError, reorderNode} = await import(moduleURL(source));
const {formatBuilderSpec} = await import(moduleURL(read('./lab/js/format.js')));
const catalog = [{id: 'buy_all', role: 'buy', name: '全部样本买入'}, {
  id: 'buy_ma_bull', role: 'buy', name: '均线多头排列', params: [
    {key: 'periods', label: '均线周期', type: 'intList', min: 1, max: 1000},
  ],
}];
function instance() {
  for (const key of Object.keys(state)) delete state[key];
  Object.assign(state, {builderCatalog: catalog, builderSpec: null, builderBusy: false,
    builderId: '', builderVersion: 0, builderStrategies: [], builderTemplates: [],
    builderUnavailable: [], builderMsg: {text: '', ok: true}, taskRunning: false});
  for (const key of Object.keys(api)) delete api[key];
  api.builderStrategies = async () => ({strategies: []});
  const ctx = {...Builder.mixins[0].data(), ...Builder.data(), state, $refs: {}, $nextTick: f => f(),
    $el: {querySelector: () => ({focus() {}})}};
  for (const [key, fn] of Object.entries({...Builder.mixins[0].methods, ...Builder.methods})) ctx[key] = fn.bind(ctx);
  for (const [key, fn] of Object.entries(Builder.computed)) Object.defineProperty(ctx, key, {get: () => fn.call(ctx)});
  Builder.created.call(ctx);
  state.builderSpec.name = '测试';
  state.builderSpec.buy = {type: 'condition', componentId: 'buy_all', params: {}};
  state.builderSpec.holdToEnd = true;
  return ctx;
}
function render(template, ctx) {
  return Vue.compile(template, {decodeEntities: s => s, hoistStatic: false})(ctx, []);
}
function elements(v, type, out = []) {
  if (!v || typeof v !== 'object') return out;
  if (v.type === type) out.push(v);
  if (Array.isArray(v.children)) v.children.forEach(c => elements(c, type, out));
  return out;
}
function editor(prefix) {
  const node = {type: 'condition', componentId: 'buy_ma_bull', params: {periods: [5, 10, 20]}};
  const ctx = {...NodeEdit.data(), node, parent: [node], index: 0, nodeLabel: catalog[1].name, entry: catalog[1], role: 'buy', entryGroups: [], isGroup: false,
    removable: true, nodeError: '', paramPrefix: prefix};
  for (const [key, fn] of Object.entries(NodeEdit.methods)) ctx[key] = fn.bind(ctx);
  return ctx;
}

function reorderEditor(node, parent) {
  const ctx = editor('reorder');
  Object.assign(ctx, {node, parent, index: parent.indexOf(node), $el: {
    ownerDocument: {}, getBoundingClientRect: () => ({top: 100, height: 80}), contains: () => false,
  }, $nextTick: f => f()});
  return ctx;
}
function dragEvent(y = 100) {
  return {clientY: y, prevented: false, preventDefault() {this.prevented = true;},
    dataTransfer: {setData() {}, setDragImage() {}}};
}

test('条件说明提示避让视口边缘，并在鼠标移入提示或触发器有焦点时保留', () => {
  let opened = false, focused = false, hovered = false;
  const tip = {offsetWidth: 320, offsetHeight: 100, style: {},
    matches: selector => selector === ':popover-open' ? opened : hovered,
    showPopover() {opened = true;}, hidePopover() {opened = false;}};
  const trigger = {matches: () => focused, ownerDocument: {defaultView: {innerWidth: 390, innerHeight: 844}},
    getBoundingClientRect: () => ({right: 30, top: 790, bottom: 818})};
  const ctx = {$refs: {help: tip, helpTrigger: trigger}};
  NodeEdit.methods.showHelp.call(ctx);
  assert.equal(opened, true);
  assert.deepEqual(tip.style, {left: '12px', top: '690px'});
  focused = true; NodeEdit.methods.hideHelp.call(ctx); assert.equal(opened, true);
  focused = false; hovered = true; NodeEdit.methods.hideHelp.call(ctx); assert.equal(opened, true);
  hovered = false; NodeEdit.methods.hideHelp.call(ctx); assert.equal(opened, false);
  trigger.getBoundingClientRect = () => ({right: 380, top: 200, bottom: 228});
  NodeEdit.methods.showHelp.call(ctx);
  assert.deepEqual(tip.style, {left: '58px', top: '228px'});
});

test('排序保留重复条件、分组和参数，拒绝越界且视图标识随节点稳定', () => {
  const a = {type: 'condition', componentId: 'buy_ma_bull', params: {periods: [5, 10]}},
    b = {type: 'condition', componentId: 'buy_ma_bull', params: {periods: '非法输入'}},
    group = {type: 'group', op: 'or', children: [a]};
  const parent = [a, b, group];
  const ctx = reorderEditor(a, parent), key = ctx.nodeKey(a);
  assert.equal(reorderNode(parent, a, 2), true);
  assert.deepEqual(parent, [b, group, a]);
  assert.equal(ctx.nodeKey(a), key);
  assert.notEqual(ctx.nodeKey(b), key);
  assert.equal(b.params.periods, '非法输入');
  assert.equal(group.children[0], a);
  assert.equal(reorderNode(parent, a, 0), true);
  for (const to of [-1, 3, 0, 1.5]) assert.equal(reorderNode(parent, a, to), false);
  assert.equal(reorderNode(parent, {}, 1), false);
  assert.deepEqual(parent, [a, b, group]);
  assert.deepEqual(Object.keys(a), ['type', 'componentId', 'params']);
});

test('拖动只在落下时提交，插入上下位置正确，跨组或取消不改变列表', () => {
  instance();
  const a = editor('a').node, b = editor('b').node, c = editor('c').node;
  const parent = [a, b, c];
  let source = reorderEditor(a, parent), target = reorderEditor(c, parent);
  source.startDrag(dragEvent());
  const over = dragEvent(175);
  target.dragOver(over);
  assert.equal(over.prevented, true);
  assert.deepEqual(parent, [a, b, c]);
  target.dropNode(over);
  assert.deepEqual(parent, [b, c, a]);
  assert.equal(source.nodeDrag.node, null);
  source = reorderEditor(a, parent); target = reorderEditor(b, parent);
  source.startDrag(dragEvent());
  target.dragOver(dragEvent(101)); target.dropNode(dragEvent());
  assert.deepEqual(parent, [a, b, c]);
  source.startDrag(dragEvent());
  const outside = reorderEditor(c, [c, editor('other').node]), denied = dragEvent();
  outside.dragOver(denied); outside.dropNode(denied);
  assert.equal(denied.prevented, false);
  source.clearDrag();
  assert.deepEqual(parent, [a, b, c]);
  source.startDrag(dragEvent());
  target = reorderEditor(c, parent); target.dragOver(dragEvent());
  source.clearDrag();
  assert.deepEqual(parent, [a, b, c]);
  assert.equal(target.nodeDrag.target, null);
});

test('上下移动有边界保护，草稿和保存请求保留排序后的顺序', async () => {
  const builder = instance();
  const a = editor('a').node, b = editor('b').node;
  a.params.periods = [5, 10]; b.params.periods = [20, 30];
  state.builderSpec.buy = {type: 'group', op: 'and', children: [a, b]};
  const ctx = reorderEditor(state.builderSpec.buy.children[0], state.builderSpec.buy.children);
  const buttons = elements(render(NodeEdit.template, ctx), 'button');
  assert.equal(buttons.find(v => v.props.title === '上移').props.disabled, true);
  buttons.find(v => v.props.title === '下移').props.onClick();
  assert.equal(state.builderSpec.buy.children[1].params.periods[0], 5);
  assert.equal(builder.dirty, true);
  let received;
  api.createBuilderStrategy = async spec => {
    received = spec;
    return {strategy: {id: 'sorted', name: spec.name, version: 1}};
  };
  await builder.saveStrategy();
  assert.deepEqual(received.buy.children.map(n => n.params.periods), [[20, 30], [5, 10]]);
  assert.equal(builder.dirty, false);
  assert.equal(ctx.nodeDrag.node, null);
});

test('排序恢复焦点，边界禁用按钮转移焦点到手柄，卸载清理拖动状态', () => {
  instance();
  const a = editor('a').node, b = editor('b').node, parent = [a, b];
  const ctx = reorderEditor(b, parent);
  let focused = 0;
  const handle = {focus: options => {assert.equal(options.preventScroll, true); focused++;}};
  ctx.$el.ownerDocument.activeElement = {isConnected: true, disabled: true,
    parentElement: {querySelector: () => handle}};
  ctx.moveSelf(-1);
  assert.equal(focused, 1);
  assert.deepEqual(parent, [b, a]);
  ctx.startDrag(dragEvent());
  NodeEdit.beforeUnmount.call(ctx);
  assert.equal(ctx.nodeDrag.node, null);
  ctx.removable = false;
  assert.equal(elements(render(NodeEdit.template, ctx), 'button').length, 0);
});

test('策略校验通过后选择运行配置，打开弹窗不会发送回测', async () => {
  const ctx = instance();
  ctx.catalogReady = true;
  let callback, calls = 0;
  ctx.openRunConfig = (mode, run) => {assert.equal(mode, 'builder'); callback = run;};
  ctx.runBuilder = async () => {calls++;};
  ctx.requestRun();
  assert.equal(calls, 0);
  assert.equal(typeof callback, 'function');
  await callback(); assert.equal(calls, 1);
  callback = null; state.builderSpec.name = '';
  ctx.requestRun(); assert.equal(callback, null); assert.ok(ctx.nameError);
  state.builderSpec.name = '测试'; state.taskRunning = true;
  ctx.requestRun(); assert.equal(callback, null);
});

test('初始渲染及目录失败可显示错误并重试', async () => {
  const ctx = instance();
  assert.doesNotThrow(() => render(Builder.template, ctx));
  api.builderComponents = async () => {throw new Error('离线');};
  await ctx.init();
  assert.match(state.builderMsg.text, /离线/);
  assert.equal(ctx.loadingCatalog, false);
  assert.equal(ctx.catalogReady, false);
  assert.doesNotThrow(() => render(Builder.template, ctx));
  api.builderComponents = async () => ({components: catalog});
  api.builderTemplates = async () => ({templates: []});
  await ctx.init();
  assert.equal(ctx.catalogReady, true);
  assert.equal(state.builderMsg.ok, true);
});

test('保存期间可继续编辑，防重复提交且不覆盖新输入', async () => {
  const ctx = instance();
  let finish, calls = 0;
  api.createBuilderStrategy = spec => {
    calls++;
    return new Promise(resolve => {finish = () => resolve({strategy: {id: 'saved', name: spec.name, version: 1, spec}});});
  };
  const pending = ctx.saveStrategy();
  state.builderSpec.name = '保存后的编辑';
  await ctx.saveStrategy();
  assert.equal(calls, 1);
  finish(); await pending;
  assert.equal(state.builderSpec.name, '保存后的编辑');
  assert.equal(state.builderId, 'saved');
  assert.match(state.builderMsg.text, /新编辑仍在草稿/);
  assert.equal(state.builderBusy, false);
});

test('版本冲突保留草稿，另存为创建新策略且不覆盖源策略', async () => {
  const ctx = instance();
  state.builderId = 'original'; state.builderVersion = 2;
  api.updateBuilderStrategy = async () => {throw new Error('版本冲突');};
  const draft = state.builderSpec;
  await ctx.saveStrategy();
  assert.equal(state.builderSpec, draft);
  assert.equal(state.builderVersion, 2);
  assert.match(state.builderMsg.text, /版本冲突/);
  let received;
  api.createBuilderStrategy = async spec => {
    received = spec;
    return {strategy: {id: 'copy', version: 1, name: spec.name, spec}};
  };
  await ctx.saveAsNew();
  assert.equal(received.name, '测试');
  assert.equal(state.builderId, 'copy');
});

test('列表输入保留非法项，文本输入与标签错误关联且实例 ID 独立', () => {
  instance();
  const ctx = editor('instance-one');
  const def = catalog[1].params[0];
  ctx.onParamInput(def, {target: {value: '5,foo,20'}});
  assert.equal(ctx.node.params.periods, '5,foo,20');
  assert.match(paramError(def, ctx.node.params.periods), /整数周期/);
  ctx.onParamInput(def, {target: {value: '5,10,'}});
  assert.equal(ctx.node.params.periods, '5,10,');
  ctx.onParamInput(def, {target: {value: '5,0,20'}});
  const first = elements(render(NodeEdit.template, ctx), 'input')[0].props;
  assert.equal(first.type, 'text');
  assert.equal(first['aria-invalid'], 'true');
  assert.equal(first['aria-describedby'], first.id + '-error');
  const second = elements(render(NodeEdit.template, editor('instance-two')), 'input')[0].props;
  assert.notEqual(first.id, second.id);
  ctx.onParamInput(def, {target: {value: '5，10，20'}});
  assert.deepEqual(ctx.node.params.periods, [5, 10, 20]);
  assert.equal(ctx.fieldError(def), '');
});

test('清空卖出树后可仅持有至期末，空分组仍明确报错', () => {
  const ctx = instance();
  state.builderSpec.sellSignal = {type: 'group', op: 'or', children: []};
  assert.match(ctx.validateLocal().join('\n'), /空分组/);
  const clear = elements(render(Builder.template, ctx), 'button').find(v => v.children === '清空卖出信号');
  assert.ok(clear);
  clear.props.onClick();
  assert.equal(state.builderSpec.sellSignal, null);
  assert.deepEqual(ctx.validateLocal(), []);
});

test('报告只展示冻结树，包含重复实例、未设置参数及显式持有选择', () => {
  const spec = {version: 1, name: '冻结报告', buy: {type: 'group', op: 'and', children: [
    {type: 'condition', componentId: 'buy_ma_bull', params: {periods: [20, 30, 60]}},
    {type: 'condition', componentId: 'buy_ma_bull'},
  ]}, holdToEnd: true};
  const before = JSON.stringify(spec);
  const text = formatBuilderSpec(spec, catalog);
  assert.match(text, /20,30,60/);
  assert.match(text, /未设置/);
  assert.match(text, /Virtual/);
  assert.equal(JSON.stringify(spec), before);
  assert.match(formatBuilderSpec(spec, []), /buy_ma_bull/);
});


test('替换未保存草稿须显式选择，取消保留原内容', () => {
  const ctx = instance();
  const draft = state.builderSpec;
  ctx.requestReplace(ctx.newStrategy);
  assert.equal(ctx.open, true);
  assert.equal(state.builderSpec, draft);
  ctx.closeDlg();
  assert.equal(state.builderSpec, draft);
  ctx.requestReplace(ctx.newStrategy);
  ctx.replaceDraft();
  assert.notEqual(state.builderSpec, draft);
  assert.equal(ctx.dirty, false);
});

test('Virtual、实际成交与旧报告缺失标记分开展示', async () => {
  instance();
  let source = read('./lab/js/components/Tab3Trades.js').replace(/^import .*;$/gm, '');
  source = 'const state=globalThis.builderTestState;\n' + source;
  const Trades = (await import(moduleURL(source))).default;
  const row = {variant: '样本', code: 'sh600000', buyTime: '2025-01-02', sellTime: '2025-01-03',
    buyPrice: 10, sellPrice: 11, profit: 100, rate: 10, quantity: 100, holdingDays: 1};
  const ctx = {...Trades.data(), visibleTrades: [{...row, virtual: true}, {...row, virtual: false}, row],
    variantNames: ['样本'], noTradesText: '', showKline() {}};
  const cells = elements(render(Trades.template, ctx), 'td').map(v => v.children);
  assert.ok(cells.includes('期末估值（Virtual）'));
  assert.ok(cells.includes('实际成交'));
  assert.ok(cells.includes('未记录'));
});

test('后端省略空 risks/params 时仍可添加风控并编辑参数', () => {
  const ctx = instance();
  state.builderCatalog = [...catalog, {id: 'risk_fixed_stop', role: 'risk', name: '固定止损'}];
  state.builderSpec = {version: 1, name: '模板', buy: {type: 'condition', componentId: 'buy_all'}};
  ctx.addRisk();
  assert.equal(state.builderSpec.risks[0].componentId, 'risk_fixed_stop');
  const nodeCtx = editor('empty-params');
  delete nodeCtx.node.params;
  assert.doesNotThrow(() => render(NodeEdit.template, nodeCtx));
  nodeCtx.onParamInput(catalog[1].params[0], {target: {value: '5,20'}});
  assert.deepEqual(nodeCtx.node.params.periods, [5, 20]);
});

test('回测请求待响应时不重复提交', async () => {
  const ctx = instance();
  ctx.catalogReady = true;
  state.runCfg = {startYear: 2025, endYear: 2025, sampleMode: 'all'};
  let finish, calls = 0, tasks = 0;
  ctx.startTask = () => tasks++;
  api.runBuilder = () => {calls++; return new Promise(resolve => {finish = resolve;});};
  const pending = ctx.runBuilder();
  await ctx.runBuilder();
  assert.equal(calls, 1);
  finish(); await pending;
  assert.equal(tasks, 1);
  assert.equal(ctx.submitting, false);
});
