import {test,expect} from '@playwright/test';

const bars=[{date:'2025-01-02',open:10,close:10.5,high:11,low:9,volume:100,dif:0,dea:0,macd:0},
  {date:'2025-01-03',open:10.5,close:11,high:12,low:10,volume:120,dif:0.1,dea:0,macd:0.1}];
const fixture=code=>({code,asOf:'2026-10-20T15:00:00+08:00',metrics:[
  {key:'pe_ttm',value:15.3,source:'tdx',date:'2026-10-19'},
  {key:'pe_static',value:-12,source:'tdx',date:'2026-10-19'},
  {key:'dividend_yield',value:0,source:'tdx',date:'2026-10-19'},
  {key:'pb_mrq',value:null},{key:'total_market_cap',value:12000000000,source:'eastmoney',date:'2026-10-16'},
  {key:'float_market_cap',value:null}]});
const stock=page=>page.getByRole('region',{name:'股票估值信息'});
const historicalFixture=(code,date='2025-01-02')=>({code,metrics:[
  {key:'pe_ttm',value:8,source:'tdx',date},{key:'dividend_yield',value:3,source:'tdx',date}]});
test.beforeEach(async({page})=>{
  await page.route('https://cdn.jsdelivr.net/**',route=>route.fulfill({contentType:'text/javascript',body:''}));
  await page.route('**/api/kline/**',route=>route.fulfill({json:bars}));
});
async function open(page,code='sh600000') {
  await page.goto('/?tab=trades');
  await page.locator('.trade-table').getByRole('button',{name:new RegExp('查看 '+code+' .*交易K线')}).click();
  await expect(page.locator('#tradeKlinePanel canvas').first()).toBeVisible();
}
for(const width of [1440,390]) test(`${width}px 估值、来源日期与缺失值不覆盖K线`,async({page},info)=>{
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.setViewportSize({width,height:1000});
  await page.route('**/api/stock-info/**',route=>route.fulfill({json:new URL(route.request().url()).searchParams.has('asOf')?historicalFixture('sh600000'):fixture('sh600000')}));
  await open(page);
  await expect(stock(page)).toContainText('15.30 倍');await expect(stock(page)).toContainText('-12.00（亏损）');
  await expect(stock(page)).toContainText('0.00%');await expect(stock(page)).toContainText('120.00 亿');
  await expect(stock(page)).toContainText('不代表历史交易日');
  await expect(stock(page)).toContainText('通达信 · 2026-10-19；东方财富 · 2026-10-16');
  expect(await stock(page).locator('.stock-info-latest').allTextContents()).toEqual(['15.30 倍','-12.00（亏损）','0.00%','—','120.00 亿','—']);
  expect(await stock(page).locator('.stock-info-history').allTextContents()).toEqual(['8.00 倍','—','3.00%','—','—','—']);
  await expect(stock(page).getByLabel('历史估值日期',{exact:true})).toHaveValue('2025-01-02');
  expect(await stock(page).evaluate(el=>el.getBoundingClientRect().bottom <= document.querySelector('#tradeKlinePanel .chart-kline').getBoundingClientRect().top)).toBe(true);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  const help=stock(page).locator('summary');await help.focus();await page.keyboard.press('Enter');
  await expect(stock(page)).toContainText('股息率沿用通达信口径');
  await info.attach(`stock-info-${width}`,{body:await page.locator('#tradeKlinePanel').screenshot(),contentType:'image/png'});
  expect(errors).toEqual([]);
});

test('估值错误可重试且不阻断行情；换股不会显示旧响应',async({page})=>{
  let calls=0,release,started;
  const pending=new Promise(resolve=>{release=resolve;});const requested=new Promise(resolve=>{started=resolve;});
  await page.route('**/api/stock-info/**',async route=>{
    const code=new URL(route.request().url()).pathname.split('/').pop();
    if(code==='sh600001'){await route.fulfill({json:fixture(code)});return;}
    if(++calls===1){await route.fulfill({status:503,json:{error:'测试估值暂不可用'}});return;}
    started();await pending;await route.fulfill({json:fixture(code)});
  });
  await open(page);await expect(stock(page).getByRole('alert')).toContainText('测试估值暂不可用');
  await expect(page.getByRole('button',{name:'放大K线',exact:true})).toBeEnabled();
  await stock(page).getByRole('button',{name:'重试估值'}).click();await requested;
  await expect(stock(page)).toContainText('读取估值中');
  await page.locator('.trade-table').getByRole('button',{name:/查看 sh600001 .*交易K线/}).click();
  await expect(stock(page)).toContainText('15.30 倍');
  const oldResponse=page.waitForResponse(r=>new URL(r.url()).pathname==='/api/stock-info/sh600000');
  release();await(await oldResponse).finished();
  await expect(page.locator('#tradeKlineTitle')).toContainText('sh600001');
  await expect(stock(page)).toContainText('15.30 倍');
});

test('真实只读接口缺数据时显示空态，数值不冒充零',async({page})=>{
  await open(page);await expect(stock(page)).toContainText('暂无本地估值记录');
  expect(await stock(page).locator('.stock-info-latest').allTextContents()).toEqual(Array(6).fill('—'));
  expect(await stock(page).locator('.stock-info-history').allTextContents()).toEqual(Array(6).fill('—'));
  await expect(stock(page)).toContainText('未用最新估值回填');
  await stock(page).getByRole('button',{name:'重试估值'}).click();
  await expect(stock(page).getByRole('button',{name:'重试估值'})).toBeVisible();
});

test('历史日期可选择和恢复，历史缺失时最新列仍保留',async({page})=>{
  const cutoffs=[];
  await page.route('**/api/stock-info/**',route=>{
    const cutoff=new URL(route.request().url()).searchParams.get('asOf');if(cutoff)cutoffs.push(cutoff);
    return route.fulfill({json:cutoff?{code:'sh600000',metrics:[]}:fixture('sh600000')});
  });
  await open(page);await expect(stock(page)).toContainText('未用最新估值回填');
  const date=stock(page).getByLabel('历史估值日期',{exact:true});
  await date.fill('2025-01-03');
  await expect.poll(()=>cutoffs.includes('2025-01-03T15:00:00+08:00')).toBe(true);
  await expect(stock(page).locator('.stock-info-latest').first()).toHaveText('15.30 倍');
  await stock(page).getByRole('button',{name:'恢复买入日',exact:true}).click();
  await expect(date).toHaveValue('2025-01-02');
  await expect(stock(page).locator('.stock-info-history').first()).toHaveText('—');
});
