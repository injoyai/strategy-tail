import assert from 'node:assert/strict';
import test from 'node:test';
import fs from 'node:fs';
import vm from 'node:vm';

const source = fs.readFileSync(new URL('./kline.js', import.meta.url), 'utf8');
const row = (day, close = 10) => ({day, open: 10, high: 12, low: 9, close, volume: 100, dif: 0.2, dea: 0.1, hist: 0.1});
function runtime() {
  const indicators = new Map(), overlays = new Map(), charts = [], observers = [];
  const lib = {registerIndicator(i) {indicators.set(i.name, i);}, registerOverlay(o) {overlays.set(o.name, o);},
    utils: {calcTextWidth: text => Array.from(text).reduce((n, c) => n + (c.charCodeAt(0) > 255 ? 12 : 7), 0)},
    init(el, options) {
      const c = {el, options, data: [], markers: [], ids: [], resizes: 0, disposed: false,
        createIndicator(i) {this.ids.push(i);return i.name;}, setFormatter(f) {this.formatter = f;},
        setDataLoader(l) {this.loader = l;}, setSymbol() {}, setPeriod() {}, setStyles() {}, setPaneOptions() {},
        resetData() {this.loader.getBars({type:'init',callback:(d,more)=>{this.data=d;this.more=more;}});},
        createOverlay(o) {this.markers.push(o);}, removeOverlay() {this.markers=[];},
        resize() {assert.equal(this.disposed,false);this.resizes++;}, setBarSpace() {}, getBarSpace() {return {bar:12};},
        overrideYAxis(options) {this.axisOptions=options;}, scrollToRealTime() {},
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

test('买卖标签避开蜡烛和邻近影线，以引线和圆点保留实际成交价，同日标记错开', () => {
  const r=runtime(), mounted=r.mount(r.el), bars=[row(0),{...row(1),low:7,high:14},row(2)];
  mounted.setData({showDate:false,rows:bars,markers:[
    {day:0,price:10.5,kind:'buy'},{day:0,price:10.6,kind:'buy'},
    {day:2,price:11,kind:'sell'},{day:2,price:11,kind:'est'}]});
  const chart=r.charts[0], figures=r.overlays.get('strategyTrade').createPointFigures;
  let offset=0, scale=10;
  const xAxis={convertToPixel:i=>80+i*12+offset};
  const yAxis={getRange:()=>({from:0,to:40/scale}),convertToPixel:v=>300-v*scale};
  const draw=overlay=>figures({coordinates:[{x:80,y:195}],overlay,chart,bounding:{width:400,height:400},xAxis,yAxis});
  const result=chart.markers.map(draw), badges=result.map(f=>f.find(x=>x.key==='badge').attrs);
  assert.ok(badges[0].y>=230+16,'标签避让邻近日K低点');
  assert.ok(badges[2].y+badges[2].height<=160-16,'卖出标签高于邻近影线');
  assert.ok(badges[1].y>=badges[0].y+badges[0].height+4,'同日买入不重叠');
  assert.ok(badges[3].y+badges[3].height+4<=badges[2].y,'同日卖出与估值不重叠');
  const dot=result[0].find(f=>f.key==='price'), label=result[0].find(f=>f.key==='label');
  assert.equal(dot.attrs.y,195); assert.equal(chart.markers[0].points[0].value,10.5);
  assert.match(label.attrs.text,/B 买入 10\.50/); assert.equal(label.styles.color,'#e9eef8');
  assert.equal(result[0].find(f=>f.key==='badge').styles.color,'#0d1626');
  assert.equal(result[0].find(f=>f.key==='leader').attrs.coordinates[0].y,195);
  assert.ok(result.every(f=>f.every(x=>x.ignoreEvent===true)),'标记不拦截图表拖拽和十字光标');
  offset=20; scale=12;
  const moved=draw(chart.markers[0]).find(f=>f.key==='price');
  assert.equal(moved.attrs.x,100); assert.equal(moved.attrs.y,174,'缩放后使用原价格重新投影');
  offset=-100; assert.equal(draw(chart.markers[0]).length,0,'不把视窗外标记钉在边缘');
});

test('浅色报告标签沿用浅色底与深色字，靠边标签保持在图内', () => {
  const r=runtime(), mounted=r.mount(r.el,{dark:false});
  mounted.setData({showDate:false,rows:[row(0)],markers:[{day:0,price:10,kind:'sell'}]});
  const chart=r.charts[0];
  const figures=r.overlays.get('strategyTrade').createPointFigures({chart,coordinates:[{}],overlay:chart.markers[0],
    bounding:{width:200,height:300},xAxis:{convertToPixel:()=>2},
    yAxis:{getRange:()=>({from:0,to:20}),convertToPixel:v=>250-v*10}});
  const badge=figures.find(f=>f.key==='badge');
  assert.ok(badge.attrs.x>=0); assert.ok(badge.attrs.x+badge.attrs.width<=200);
  assert.equal(badge.styles.color,'#fff');assert.equal(figures.find(f=>f.key==='label').styles.color,'#172234');
});
