import assert from 'node:assert/strict';
import test from 'node:test';
await import(new URL('../../chartui/kline.js', import.meta.url));
const {legends, normalize} = globalThis.StrategyKline;
const bar = (day,close) => ({day,open:99,high:100,low:1,close,volume:100});
const text = current => legends({current}).find(x=>x.title==='涨跌幅（较前收）').value;

test('涨跌幅按相邻前收计算、红涨绿跌，首根不捏造基准', () => {
 const {data}=normalize({showDate:false,rows:[bar(0,10),bar(1,11),bar(2,9.9),bar(3,9.9)]});
 assert.deepEqual(text(data[0]),{text:'—',color:'#94a3b8'});
 assert.deepEqual(text(data[1]),{text:'+10.00%',color:'#ef4444'});
 assert.deepEqual(text(data[2]),{text:'-10.00%',color:'#22c55e'});
 assert.deepEqual(text(data[3]),{text:'0.00%',color:'#94a3b8'});
 assert.equal(legends({current:null}).length,0);
 assert.ok(legends({current:data[1]}).some(x=>x.title==='开盘'&&x.value==='99.00'));
});

test('匿名提示只含相对日，成交价保持原值，文本由画布直接绘制', () => {
 const normalized=normalize({showDate:false,rows:[{...bar(-1,10),date:'2024-01-01'},{...bar(0,12),date:'2024-01-02'}],
  markers:[{day:0,price:12,kind:'buy'}]});
 assert.ok(!JSON.stringify(normalized).includes('2024-01'));
 const values=legends({current:normalized.data[1]});
 assert.equal(values[0].value,'信号日');
 assert.ok(values.some(x=>x.title==='B'&&x.value.text.includes('12.00 元')));
});
