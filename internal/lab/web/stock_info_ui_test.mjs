import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

const read = p => fs.readFileSync(new URL(p, import.meta.url), 'utf8');
const url = s => 'data:text/javascript;base64,' + Buffer.from(s).toString('base64');
const Api = {};globalThis.stockInfoTest = {Api};
const {StockInfo,stockMetricText} = await import(url(read('./lab/js/components/StockInfo.js')
  .replace(/^import (\{.*\}) from .*;$/gm,'const $1 = globalThis.stockInfoTest;')));
function instance(props={}) {
  const vm={...StockInfo.data(),code:'sz000100',asOf:'',...props};
  for(const [key,fn] of Object.entries(StockInfo.methods)) vm[key]=fn.bind(vm);
  for(const [key,fn] of Object.entries(StockInfo.computed)) Object.defineProperty(vm,key,{get:()=>fn.call(vm)});
  return vm;
}
const result=(code='sz000100',value=15.3)=>({code,metrics:[
  {key:'pe_ttm',value,source:'tdx',date:'2026-10-19',availableAt:'2026-10-19T16:00:00+08:00'},
  {key:'dividend_yield',value:0,source:'tdx',date:'2026-10-19'},
]});

test('估值显示区分缺失和零，股息率不重复乘100，市值按亿元展示',()=>{
  assert.equal(stockMetricText('pe_ttm',null),'—');
  assert.equal(stockMetricText('dividend_yield',0),'0.00%');
  assert.equal(stockMetricText('dividend_yield',1.9),'1.90%');
  assert.equal(stockMetricText('pe_ttm',15.3),'15.30 倍');
  assert.equal(stockMetricText('pe_ttm',-12),'-12.00（亏损）');
  assert.equal(stockMetricText('total_market_cap',12000000000),'120.00 亿');
});

test('估值读取独立加载，失败可重试，保留来源和数据日期',async()=>{
  const vm=instance();Api.stockInfo=async()=>{throw new Error('offline');};
  const request=vm.load();assert.equal(vm.loading,true);await request;
  assert.equal(vm.error,'offline');assert.equal(vm.loading,false);assert.equal(vm.sources.length,0);
  Api.stockInfo=async()=>result();await vm.load();
  assert.equal(vm.error,'');assert.equal(vm.metrics.length,6);
  assert.equal(vm.metrics[2].text,'0.00%');assert.equal(vm.metrics[3].text,'—');
  assert.deepEqual(vm.sources,['通达信 · 2026-10-19']);
  vm.info.metrics.push({key:'pb_mrq',value:2,source:'eastmoney',date:'2026-10-16',pitStatus:'unverified_revision_history'});
  assert.equal(vm.unverified,true);assert.deepEqual(vm.sources,['通达信 · 2026-10-19','东方财富 · 2026-10-16']);
});

test('换股立即清空旧估值，旧请求和组件销毁后的响应不能覆盖新视图',async()=>{
  const vm=instance();let release;
  Api.stockInfo=()=>new Promise(resolve=>{release=resolve;});const old=vm.load();
  vm.code='sh600000';Api.stockInfo=async()=>result('sh600000',8);await vm.load();
  release(result());await old;assert.equal(vm.info.code,'sh600000');assert.equal(vm.metrics[0].value,8);
  Api.stockInfo=()=>new Promise(resolve=>{release=resolve;});const pending=vm.load();
  assert.equal(vm.info,null);StockInfo.beforeUnmount.call(vm);release(result('sh600000'));await pending;assert.equal(vm.info,null);
});

test('回放明确传递上海收盘截止，错股响应报错而非混用数值',async()=>{
  const vm=instance({asOf:'2025-01-02T15:00:00+08:00'});let args;
  Api.stockInfo=async(...query)=>{args=query;return result();};await vm.load();
  assert.deepEqual(args,['sz000100','2025-01-02T15:00:00+08:00']);
  Api.stockInfo=async()=>result('sh600000');await vm.load();assert.match(vm.error,/与当前股票不符/);assert.equal(vm.info,null);
});

test('历史与最新独立查询和展示，历史缺失或失败不能回填最新值',async()=>{
  const vm=instance({historyDate:'2025-01-02',selectedDate:'2025-01-02'}),calls=[];
  Api.stockInfo=async(code,cutoff)=>{
    calls.push([code,cutoff]);return cutoff?{code,metrics:[]}:result(code,15.3);
  };
  await vm.load();assert.equal(vm.comparison,true);
  assert.deepEqual(calls,[['sz000100',''],['sz000100','2025-01-02T15:00:00+08:00']]);
  assert.equal(vm.metrics[0].text,'15.30 倍');assert.equal(vm.historicalMetrics[0].text,'—');
  Api.stockInfo=async(code,cutoff)=>{if(cutoff)throw new Error('historical offline');return result(code);};
  await vm.load();assert.equal(vm.error,'');assert.equal(vm.historicalError,'historical offline');
  assert.equal(vm.metrics[0].value,15.3);assert.equal(vm.historicalInfo,null);
  Api.stockInfo=async(code,cutoff)=>{if(!cutoff)throw new Error('latest offline');return result(code,8);};
  await vm.load();assert.equal(vm.error,'latest offline');assert.equal(vm.historicalError,'');
  assert.equal(vm.historicalMetrics[0].value,8);assert.equal(vm.info,null);
});

test('历史日期变化清除旧值并忽略旧日期响应，无效日期不请求历史',async()=>{
  const vm=instance({historyDate:'2025-01-02',selectedDate:'2025-01-02'});let release;
  Api.stockInfo=async(code,cutoff)=>cutoff?new Promise(resolve=>{release=resolve;}):result(code);
  const old=vm.load();vm.selectedDate='2025-01-03';
  Api.stockInfo=async(code,cutoff)=>result(code,cutoff?8:15.3);await vm.load();
  release(result('sz000100',9));await old;assert.equal(vm.historicalMetrics[0].value,8);
  for(const invalid of ['','2025-02-30','2999-01-01']){
    vm.selectedDate=invalid;let calls=[];Api.stockInfo=async(...args)=>{calls.push(args);return result();};
    await vm.load();assert.equal(vm.historicalAsOf,'');assert.deepEqual(calls,[['sz000100','']]);
    assert.equal(vm.historicalInfo,null);
  }
  vm.asOf='2025-01-03T15:00:00+08:00';assert.equal(vm.comparison,false,'回放即便带有历史默认日也不能请求最新数据');
});
