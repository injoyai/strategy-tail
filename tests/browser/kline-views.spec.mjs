import {test, expect} from '@playwright/test';

const modes = [
  {name:'盲练', kind:'practice'},
  {name:'复盘', kind:'review'},
  {name:'回放', kind:'replay'},
  {name:'诊断', kind:'screen', macd:false},
  {name:'交易导出', kind:'report', dark:false, macd:false},
  {name:'年度及未来收益报告', kind:'report', dark:false},
];

for (const width of [1440,390]) for (const mode of modes) {
  test(`${width}px ${mode.name}：滚轮和键盘缩小增加K线，全程及数据边界`, async ({page}, info) => {
    await page.setViewportSize({width,height:900});
    await page.route('https://cdn.jsdelivr.net/**', route => route.fulfill({contentType:'text/javascript',body:''}));
    await page.goto('/?tab=trades');
    await page.waitForFunction(() => !!window.StrategyKline);
    const count = await page.evaluate(async mode => {
      const {practiceKlineData} = await import('/js/practice.js');
      const {klineVisible} = await import('/js/replay.js');
      document.querySelector('#app').style.display='none';
      document.body.style.padding='12px';
      const host=document.createElement('div');host.id='zoomFixture';
      host.style.cssText='width:min(100%,1000px);height:600px;margin:auto';
      host.style.background=mode.dark===false?'white':'#0d1626';
      document.body.append(host);
      const rows=Array.from({length:2000},(_,i)=>{
        const close=10+Math.sin(i/30);
        return {day:i-1800,date:new Date(Date.UTC(2020,0,1+i)).toISOString().slice(0,10),
          open:close-0.1,close,high:close+0.2,low:close-0.2,volume:1000,dif:0.2,dea:0.1,hist:0.1};
      });
      let data={rows,identity:mode.kind};
      if(mode.kind==='practice'||mode.kind==='review') {
        data=practiceKlineData({days:rows.slice(0,mode.kind==='practice'?1801:1821),
          identity:mode.kind,showDate:mode.kind==='review'});
      } else if(mode.kind==='replay') {
        data={...data,rows:klineVisible(rows,rows[1800].date)};
      }
      window.zoomFixture=StrategyKline.mount(host,mode);
      zoomFixture.setData(data);
      window.zoomData=data;
      return data.rows.length;
    },mode);
    const host=page.locator('#zoomFixture');
    const snapshot=()=>host.evaluate(el=>{
      const c=klinecharts.init(el),range=c.getVisibleRange();
      return {count:c.getDataList().length,visible:range.to-range.from,from:range.from,to:range.to,space:c.getBarSpace().bar};
    });
    await expect.poll(async()=>(await snapshot()).count).toBe(count);
    const initial=await snapshot();expect(initial.visible).toBeLessThan(200);
    await host.hover({position:{x:100,y:100}});await page.mouse.wheel(0,300);
    await expect.poll(async()=>(await snapshot()).visible).toBeGreaterThan(initial.visible);
    await host.focus();
    for(let i=0;i<32;i++) await page.keyboard.press('-');
    await expect.poll(async()=>(await snapshot()).visible).toBeGreaterThan(1000);
    await page.evaluate(()=>zoomFixture.fitContent());
    await expect.poll(async()=>{const s=await snapshot();return s.from===0&&s.to===s.count;}).toBe(true);
    expect((await snapshot()).space).toBeLessThan(1);
    if(mode.kind==='practice') {
      const labels=await host.evaluate(el=>klinecharts.init(el).getDataList().map(x=>x.label));
      expect(labels[0]).toBe('前1800日');expect(labels.at(-1)).toBe('信号日');
      expect(labels.some(x=>/\d{4}-\d{2}-\d{2}/.test(x))).toBe(false);
    }
    if(mode.kind==='replay') {
      await page.evaluate(()=>zoomFixture.setData({...zoomData,rows:zoomData.rows.slice(0,1601)}));
      await expect.poll(async()=>(await snapshot()).count).toBe(1601);
    }
    expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
    await host.hover({position:{x:20,y:20}});
    const screenshot=await host.screenshot({path:`output/browser-tests/kline-views/${mode.kind}-${mode.macd===false?'two':'three'}-${width}.png`});
    await info.attach(`${mode.kind}-${width}`,{body:screenshot,contentType:'image/png'});
  });
}
