import {test, expect} from '@playwright/test';

for (const [width, dark] of [[1440, true], [390, true], [900, false]]) {
  test(`买卖标记 ${width}px ${dark ? '暗色' : '浅色'}：极值、同日多笔、缩放和边界`, async ({page}, info) => {
    await page.setViewportSize({width, height: 900});
    await page.route('https://cdn.jsdelivr.net/**', route => route.fulfill({contentType: 'text/javascript', body: ''}));
    const errors=[];page.on('pageerror', e=>errors.push(e.message));
    await page.goto('/?tab=trades');
    await page.waitForFunction(()=>!!window.StrategyKline);
    await page.evaluate(dark => {
      document.querySelector('#app').style.display='none';
      document.body.style.padding='16px';
      const el=document.createElement('div');el.id='markerFixture';
      el.style.cssText=`width:min(900px,100%);height:580px;background:${dark ? '#0d1626' : '#fff'};margin:auto`;
      document.body.append(el);
      window.markerFrames={};
      const register=klinecharts.registerOverlay;
      klinecharts.registerOverlay=template=>register({...template,createPointFigures(params) {
        const figures=template.createPointFigures(params), m=params.overlay.extendData;
        const badge=figures.find(f=>f.key==='badge'),dot=figures.find(f=>f.key==='price');
        if(badge) window.markerFrames[m.markerIndex]={box:badge.attrs,dot:dot.attrs,kind:m.kind,price:m.price,
          edge:params.yAxis.convertToPixel(m.value),label:figures.find(f=>f.key==='label').attrs.text,bounding:params.bounding};
        else delete window.markerFrames[m.markerIndex];
        return figures;
      }});
      const rows=Array.from({length:80},(_,day)=>{
        const close=5.2-day*0.008+Math.sin(day/3)*0.06;
        return {day,open:close+0.04,close,high:close+0.12,low:close-0.1,volume:1000,dif:0.01,dea:0.005,hist:0.005};
      });
      Object.assign(rows[50],{open:5.1,close:5.05,low:4.2,high:5.2});rows[55].high=5.8;
      window.markerFixture=StrategyKline.mount(el,{dark});
      markerFixture.setData({identity:'marker-fixture',showDate:false,rows,markers:[
        {day:50,price:5.07,kind:'buy'},{day:50,price:5.08,kind:'buy'},
        {day:55,price:4.72,kind:'sell'},{day:55,price:4.73,kind:'est'}]});
    }, dark);
    const chart=page.locator('#markerFixture');
    await expect.poll(()=>page.evaluate(()=>Object.keys(markerFrames).length)).toBe(4);
    async function verify() {
      const frames=await page.evaluate(()=>Object.values(markerFrames));
      expect(frames).toHaveLength(4);
      for(const f of frames) {
        expect(f.box.x).toBeGreaterThanOrEqual(0);
        expect(f.box.x+f.box.width).toBeLessThanOrEqual(f.bounding.width);
        expect(f.box.y).toBeGreaterThanOrEqual(f.bounding.width < 480 ? 50 : 34);
        expect(f.box.y+f.box.height).toBeLessThanOrEqual(f.bounding.height);
        if(f.kind==='buy') expect(f.box.y).toBeGreaterThanOrEqual(f.edge+12);
        else expect(f.box.y+f.box.height).toBeLessThanOrEqual(f.edge-12);
      }
      for(let i=0;i<frames.length;i++) for(let j=i+1;j<frames.length;j++) {
        const a=frames[i].box,b=frames[j].box;
        expect(a.x+a.width<=b.x||b.x+b.width<=a.x||a.y+a.height<=b.y||b.y+b.height<=a.y).toBe(true);
      }
      expect(frames.map(f=>f.price)).toEqual([5.07,5.08,4.72,4.73]);
    }
    await verify();
    const before=await chart.evaluate(el=>klinecharts.init(el).getBarSpace().bar);
    await chart.focus();await page.keyboard.press('+');
    await expect.poll(()=>chart.evaluate(el=>klinecharts.init(el).getBarSpace().bar)).toBeGreaterThan(before);
    await verify();
    await info.attach(`markers-${width}-${dark}`,{body:await chart.screenshot(),contentType:'image/png'});
    expect(errors).toEqual([]);
  });
}
