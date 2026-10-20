import assert from 'node:assert/strict';
import test from 'node:test';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('./kline.js', import.meta.url), 'utf8');
const row = (day, close = 10) => ({day, open: 10, high: 12, low: 9, close, volume: 100, dif: 0.2, dea: 0.1, hist: 0.1});
function runtime() {
  const indicators = new Map(), overlays = new Map(), charts = [], observers = [];
  const lib = {registerIndicator(i) {indicators.set(i.name, i);}, registerOverlay(o) {overlays.set(o.name, o);},
    init(el, options) {
      const c = {el, options, data: [], markers: [], ids: [], resizes: 0, disposed: false,
        createIndicator(i) {this.ids.push(i);return i.name;}, setFormatter(f) {this.formatter = f;},
        setDataLoader(l) {this.loader = l;}, setSymbol() {}, setPeriod() {}, setStyles() {}, setPaneOptions() {},
        resetData() {this.loader.getBars({type:'init',callback:(d,more)=>{this.data=d;this.more=more;}});},
        createOverlay(o) {this.markers.push(o);}, removeOverlay() {this.markers=[];},
        resize() {assert.equal(this.disposed,false);this.resizes++;}, setBarSpace() {}, scrollToRealTime() {},
        scrollByDistance(d) {this.scrolled=d;}, zoomAtCoordinate(scale) {this.zoom=scale;}, scrollToDataIndex() {}};
      charts.push(c);return c;
    }, dispose(el) {charts.find(c=>c.el===el).disposed=true;}};
  const context = {klinecharts:lib, ResizeObserver:class {
    constructor(cb) {this.cb=cb;observers.push(this);}
    observe() {} disconnect() {this.disconnected=true;}
  }};
  vm.runInNewContext(source,context);
  const events = new Map(), el = {clientWidth:800,clientHeight:580,setAttribute() {},
    addEventListener(name,fn) {events.set(name,fn);},removeEventListener(name) {events.delete(name);}};
  return {...context.StrategyKline,context,lib,charts,indicators,overlays,observers,el,events};
}

test('共享图表绘制三窗、复用服务端指标并保留空值，MACD 正负都包含零轴', () => {
  const r=runtime(), chart=r.mount(r.el);
  chart.setData({identity:'p1',showDate:false,rows:[{...row(0),ma5:null,hist:null},row(1,11)]});
  const c=r.charts[0];
  assert.equal(c.ids.length,3);
  assert.equal(c.data[0].ma5,null); assert.equal(c.data[0].hist,null);
  const macd=r.indicators.get('STRATEGY_MACD');
  assert.equal(macd.calc(c.data),c.data);
  assert.equal(c.data[1].hist,0.1);
  assert.equal(macd.minValue,0);assert.equal(macd.maxValue,0);
  assert.equal(c.more,false); // Dragging cannot ask for unrevealed rows.
  assert.equal(c.options.styles.candle.tooltip.showRule,'follow_cross');
});

test('回退时重载已揭示前缀并删除未来标记；同日多笔成交不合并', () => {
  const r=runtime(), chart=r.mount(r.el), rows=[row(0),row(1),row(2)];
  const markers=[{day:0,price:10,kind:'buy'},{day:2,price:11,kind:'sell'},{day:2,price:11.2,kind:'sell'}];
  chart.setData({identity:'p',showDate:false,rows,markers});
  assert.equal(r.charts[0].markers.length,3);
  const missing=chart.setData({identity:'p',showDate:false,rows:rows.slice(0,1),markers});
  assert.equal(r.charts[0].data.length,1);assert.equal(r.charts[0].markers.length,1);assert.equal(missing.length,2);
  chart.clear();assert.equal(r.charts[0].data.length,0);assert.equal(r.charts[0].markers.length,0);
});

test('匿名日期、月份刻度和提示不携带真实身份；真实日期按上海时区转换', () => {
  const r=runtime(), chart=r.mount(r.el);
  chart.setData({identity:'opaque',showDate:false,rows:[{...row(0),date:'2024-01-01',code:'sh600000'}]});
  const c=r.charts[0];
  assert.equal(c.formatter.formatDate({timestamp:c.data[0].timestamp}),'信号日');
  assert.ok(!JSON.stringify(c.data).includes('2024-01-01'));assert.ok(!JSON.stringify(c.data).includes('sh600000'));
  assert.equal(r.normalize({rows:[{...row(0),date:'2024-01-01'}]}).data[0].timestamp,Date.parse('2024-01-01T00:00:00+08:00'));
});

test('隐藏尺寸不绘制，销毁取消监听，键盘缩放与点击输入互不干扰', () => {
  const r=runtime(), chart=r.mount(r.el), c=r.charts[0], initial=c.resizes;
  r.el.clientWidth=0;r.observers[0].cb();assert.equal(c.resizes,initial);
  r.el.clientWidth=390;r.observers[0].cb();assert.equal(c.resizes,initial+1);
  let prevented=0;
  r.events.get('keydown')({target:r.el,key:'+',preventDefault(){prevented++;}});assert.equal(c.zoom,1.15);
  r.events.get('keydown')({target:{},key:'-',preventDefault(){prevented++;}});assert.equal(c.zoom,1.15);
  assert.equal(prevented,1);
  chart.dispose();chart.dispose();r.observers[0].cb();assert.equal(c.disposed,true);
  assert.equal(r.observers[0].disconnected,true);assert.equal(r.events.size,0);
});

test('无组件时降级；无效时间、重复交易日和无效价格明确失败', () => {
  const r=runtime();r.context.klinecharts=undefined;assert.equal(r.mount(r.el),null);
  assert.throws(()=>r.normalize({showDate:false,rows:[row(0),row(0)]}),/日期/);
  assert.throws(()=>r.normalize({rows:[{...row(0),date:'invalid'}]}),/日期/);
  assert.throws(()=>r.normalize({showDate:false,rows:[{...row(0),close:NaN}]}),/价格/);
});
