import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';
const read = p => fs.readFileSync(new URL(p, import.meta.url), 'utf8');
const url = s => 'data:text/javascript;base64,' + Buffer.from(s).toString('base64');
const Vue = await import(url(read('./lab/js/vendor/vue.esm-browser.prod.js')));
const practice = await import(url(read('./lab/js/practice.js').replace("'./format.js'", JSON.stringify(url(read('./lab/js/format.js'))))));
// Install the test boundary before importing modules that use it.
const state = {activeTab:7, builderStrategies:[]}, Api = {};
const format = await import(url(read('./lab/js/format.js')));
globalThis.trainingTest = {...practice, ...Vue, ...format, state, Api, Tab7Practice: {}};
const helpers = await import(url(read('./lab/js/training.js').replace(/^import (\{.*\}) from .*;$/gm,'const $1 = globalThis.trainingTest;')) + '#ready');
Object.assign(globalThis.trainingTest, helpers);
const source = read('./lab/js/components/Tab7Training.js').replace(/^import (\{.*\}|\w+) from .*;$/gm,'const $1 = globalThis.trainingTest;').replace('const Tab7Practice = globalThis.trainingTest;', 'const {Tab7Practice} = globalThis.trainingTest;');
const {default: Tab} = await import(url(source));
globalThis.location = {search:'?tab=practice',pathname:'/'};
let lastUrl = '';
globalThis.history = {replaceState(_a,_b,u){lastUrl=u;}};
function instance(over={}) {
 const vm = {...Tab.setup(),...Tab.data(),$refs:{},$nextTick:async fn=>fn?.(),startTask(){},...over};
 for(const [k,f] of Object.entries(Tab.methods))vm[k]=f.bind(vm);
 for(const [k,f]of Object.entries(Tab.computed))Object.defineProperty(vm,k,{get:()=>f.call(vm)});
 return vm;
}
const active = (revision=1, question='q1') => ({id:'training1',phase:'active',bankId:'bank1',revision,practiced:1,total:5,session:{id:question,phase:'finished',revision:2}});
const report = () => ({...active(),phase:'finished',strategyName:'固定策略',strategyVersion:2,fromYear:2020,toYear:2026,startedAt:'2026-10-19T01:00:00Z',endedAt:'2026-10-19T02:00:00Z',bankTotal:100,practiced:5,closed:3,untraded:1,open:1,wins:2,losses:1,breakeven:0,winRate:66.66667,averageReturn:8.3333,realizedPnl:45,fees:10,positionValue:120,openPnl:20,questions:[{id:'q',code:'sh600000',status:'open',signalDate:'2024-01-02',endDate:'2024-01-03',buyCost:100,sellIncome:0,realizedPnl:0,positionValue:120,openPnl:20,fees:4,returnPct:20,holdingDays:1}]});

test('训练模板在题库、准备、练习、报告、钻取、历史、失败状态可实际渲染',()=>{
 const render=Vue.compile(Tab.template,{decodeEntities:s=>s,hoistStatic:false});
 for(const over of [{},{bank:{id:'b',phase:'preparing'}},{bank:{id:'b',phase:'ready',coverage:{}}},{bank:{id:'b',phase:'failed',failReason:'no_match'}},{pane:'training',train:active()},{pane:'training',train:{...active(),phase:'finished'},report:report()},{pane:'training',train:{...active(),phase:'finished'},drill:{id:'q',phase:'finished'}},{pane:'history',trainings:[active()]},{pane:'legacy'}])assert.doesNotThrow(()=>render(instance(over),[]));
});
test('下一题网络中断保留命令，重试复用；不读取最新策略或重扫',async()=>{
 const vm=instance({train:active()});let cmds=[];vm.loadCatalog=()=>{};
 Api.trainingAction=async(id,c)=>{cmds.push(c);throw new Error('offline')};
 await vm.trainingAction('next_question');const first=vm.pending;
 assert.equal(first.questionId,'q1');assert.equal(first.expectedRevision,1);
 Api.trainingAction=async(id,c)=>{cmds.push(c);return active(2,'q2')};
 await vm.trainingAction('next_question');assert.deepEqual(cmds[0],cmds[1]);assert.equal(vm.train.session.id,'q2');assert.equal(vm.pending,null);
});
test('旧训练或旧版本响应不能覆盖较新训练，离开页面后晚到命令不跳转',async()=>{
 const vm=instance({train:active(5,'q5')});vm.acceptTraining(active(2,'q2'));assert.equal(vm.train.revision,5);
 vm.acceptTraining({...active(9),id:'other'});assert.equal(vm.train.id,'training1');
 let resolve;Api.trainingAction=()=>new Promise(r=>resolve=r);vm.loadCatalog=()=>{};
 const waiting=vm.trainingAction('next_question');vm.home();resolve(active(6,'q6'));await waiting;assert.equal(vm.train,null);assert.equal(vm.pane,'banks');
});
test('结束训练需明确二次点击，报告只接受已结束训练',async()=>{
 const vm=instance({train:active()});vm.loadCatalog=()=>{};let requests=0, reports=0;
 Api.trainingAction=async()=>{requests++;return {...active(2),phase:'finished'}};
 Api.trainingReport=async()=>{reports++;return report()};
 await vm.loadReport();assert.equal(reports,0);
 await vm.trainingAction('end_training');assert.equal(requests,0);assert.equal(vm.confirmEnd,true);
 await vm.trainingAction('end_training');assert.equal(requests,1);assert.equal(vm.train.phase,'finished');assert.equal(reports,1);
});
test('报告下载与页面共用服务端统计，无成交比率显示不适用',()=>{
 const r=report();const text=helpers.trainingReportText(r);
 assert.match(text,/练习 5 题/);assert.match(text,/66\.67%/);assert.match(text,/未平仓估值/);assert.match(text,/费用含|费用包含|手续费包含/);
 r.winRate=null;r.averageReturn=null;assert.equal(helpers.trainingMetrics(r)[1].value,'不适用');assert.equal(helpers.trainingMetrics(r)[2].value,'不适用');
});
test('URL 恢复训练和题库，切到别页时后台结果不篡改当前导航',()=>{
 const vm=instance();state.activeTab=7;vm.url('t','training1');assert.match(lastUrl,/t=training1/);assert.equal(state.practiceTrainingId,'training1');
 state.activeTab=1;const previous=lastUrl;vm.url('bank','bank1');assert.equal(lastUrl,previous);assert.equal(state.practiceBankId,'bank1');state.activeTab=7;
});
test('建库请求网络失败后同参数复用请求键；新训练请求也保持幂等',async()=>{
 state.builderStrategies=[{id:'s',version:1}];const vm=instance({strategyId:'s'});vm.loadCatalog=()=>{};const keys=[];
 Api.createPracticeBank=async req=>{keys.push(req.requestId);throw new Error('offline')};await vm.createBank();await vm.createBank();assert.equal(keys[0],keys[1]);
 const starts=[];Api.startTraining=async req=>{starts.push(req.requestId);throw new Error('offline')};await vm.startTraining('b');await vm.startTraining('b');assert.equal(starts[0],starts[1]);
});
