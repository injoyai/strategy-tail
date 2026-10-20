import { test, expect } from '@playwright/test';
import fs from 'node:fs/promises';

const bars = ['2025-01-02', '2025-01-03', '2025-01-06', '2025-01-07'].map((date, i) =>
  ({date, open: 10, close: 10 + i, low: 9, high: 14, volume: 1000, dif: 0.2, dea: 0.1, macd: 0.1}));
const grid = page => page.locator('.trade-table');
const rows = page => grid(page).locator('.tabulator-row');
const code = page => page.getByRole('textbox', {name: '代码筛选'});
const count = (page, n) => expect(page.getByText(`匹配 ${n} 笔交易`, {exact: true})).toBeVisible();
const chart = page => page.locator('#tradeKlinePanel .chart-kline');

test.beforeEach(async ({page}) => {
  // 仅可选 ECharts/GSAP CDN 离线；被测 Tabulator/KLineChart 均加载真实本地发布包。
  await page.route('https://cdn.jsdelivr.net/**', route => route.fulfill({contentType: 'text/javascript', body: ''}));
  await page.route('**/api/kline/**', route => route.fulfill({json: bars}));
});

async function openTrades(page) {
  await page.goto('/?tab=trades');
  await count(page, 2506);
  await expect(rows(page).first()).toBeVisible();
}

test('2506 笔完整浏览、虚拟渲染、筛选、清除与键盘排序', async ({page}) => {
  await openTrades(page);
  expect(await rows(page).count()).toBeLessThan(100);
  await grid(page).locator('.tabulator-tableholder').evaluate(el => {el.scrollTop = el.scrollHeight;});
  await expect(rows(page).filter({hasText: 'sh602504'})).toBeVisible();
  await grid(page).locator('.tabulator-tableholder').evaluate(el => {el.scrollTop = 0;});
  await code(page).fill('602504');
  await count(page, 1);
  await expect(rows(page)).toHaveCount(1);
  await expect(rows(page).first()).toContainText('期末估值（Virtual）');
  await page.getByRole('button', {name: '清除代码筛选'}).click();
  await count(page, 2506);
  await expect(code(page)).toBeFocused();
  const sort = page.getByRole('button', {name: '按收益率%排序', exact: true});
  await sort.focus(); await page.keyboard.press('Enter');
  await expect(grid(page).locator('[tabulator-field="rate"][role="columnheader"]')).toHaveAttribute('aria-sort', 'ascending');
  await page.keyboard.press('Enter');
  await expect(grid(page).locator('[tabulator-field="rate"][role="columnheader"]')).toHaveAttribute('aria-sort', 'descending');
  await expect(rows(page).first()).toContainText('sh602504');
  await page.getByRole('combobox', {name: '变体筛选'}).selectOption('策略B');
  await count(page, 1); await expect(rows(page).first()).toContainText('sz000001');
});

test('空结果、行情失败重试、按钮键盘联动与图表缩放', async ({page}) => {
  const errors = []; page.on('pageerror', e => errors.push(e.message));
  await openTrades(page);
  await code(page).fill('不存在'); await count(page, 0);
  await expect(grid(page).getByText('无匹配交易')).toBeVisible();
  let calls = 0;
  await page.route('**/api/kline/**', route => ++calls === 1
    ? route.fulfill({status: 503, json: {error: '测试行情暂不可用'}})
    : route.fulfill({json: bars}));
  await code(page).fill('600000'); await count(page, 1);
  await page.getByRole('button', {name: /查看 sh600000 .*交易K线/}).focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('alert')).toContainText('测试行情暂不可用');
  await page.getByRole('button', {name: '重试', exact: true}).click();
  await expect(chart(page).locator('canvas').first()).toBeVisible();
  const before = await chart(page).evaluate(el => klinecharts.init(el).getBarSpace().bar);
  await chart(page).focus(); await page.keyboard.press('+');
  await expect.poll(() => chart(page).evaluate(el => klinecharts.init(el).getBarSpace().bar)).toBeGreaterThan(before);
  expect(errors).toEqual([]);
});

test('较慢的旧行情响应不能覆盖新选择，重复切页不丢失表格', async ({page}) => {
  await openTrades(page);
  let release, started;
  const gate = new Promise(resolve => {release = resolve;});
  const requested = new Promise(resolve => {started = resolve;});
  await page.route('**/api/kline/sh600000?*', async route => {started(); await gate; await route.fulfill({json: bars});});
  await page.getByRole('button', {name: /查看 sh600000 .*交易K线/}).click();
  await requested;
  await page.getByRole('button', {name: /查看 sh600001 .*交易K线/}).click();
  await expect(chart(page).locator('canvas').first()).toBeVisible();
  const oldResponse = page.waitForResponse(response => new URL(response.url()).pathname === '/api/kline/sh600000');
  release(); await (await oldResponse).finished();
  await expect(page.locator('#tradeKlineTitle')).toHaveText('sh600001 · 交易日K与买卖点');
  expect(await chart(page).evaluate(el => klinecharts.init(el).getStyles().candle.tooltip.title.template)).toContain('sh600001');
  for (let i = 0; i < 2; i++) {
    await page.getByRole('tab', {name: '回测配置', exact: true}).click();
    await page.getByRole('tab', {name: '交易明细', exact: true}).click();
    await count(page, 2506); await expect(rows(page).first()).toBeVisible();
    await expect(chart(page).locator('canvas').first()).toBeVisible();
  }
});

test('Excel 导出失败保留筛选，重试下载真实后端工作簿', async ({page}) => {
  await openTrades(page);
  await code(page).fill('不存在'); await count(page, 0);
  await page.route('**/export.xlsx', route => route.fulfill({status: 500, json: {error: '测试导出失败'}}), {times: 1});
  const button = page.getByRole('button', {name: '导出完整报告 Excel'});
  await button.click();
  await expect(page.getByRole('alert')).toContainText('测试导出失败');
  await expect(code(page)).toHaveValue('不存在');
  const downloaded = page.waitForEvent('download');
  await page.getByRole('button', {name: '重试', exact: true}).click();
  const download = await downloaded;
  expect(download.suggestedFilename()).toMatch(/\.xlsx$/);
  const data = await fs.readFile(await download.path());
  expect(data.subarray(0, 2).toString()).toBe('PK');
  expect(data.length).toBeGreaterThan(20000);
  await expect(button).toBeEnabled();
});

test('切换空历史报告清除旧表格与K线', async ({page}) => {
  await openTrades(page);
  await page.getByRole('button', {name: /查看 sh600000 .*交易K线/}).click();
  await expect(chart(page).locator('canvas').first()).toBeVisible();
  await page.getByRole('tab', {name: '回测结果', exact: true}).click();
  const history = page.getByRole('combobox', {name: '历史回测'});
  await history.selectOption({index: 2});
  await expect(history).toBeEnabled();
  await page.getByRole('tab', {name: '交易明细', exact: true}).click();
  await count(page, 0); await expect(rows(page)).toHaveCount(0);
  await expect(page.getByText('选择一笔交易，查看日K、成交量、MACD 与买卖点')).toBeVisible();
});

for (const width of [1440, 390]) {
  test(`${width}px 完整交易区间、按钮缩放、键盘恢复与窗口变化`, async ({page}, info) => {
    await page.setViewportSize({width,height:1000});
    const longBars=Array.from({length:360},(_,i)=>{
      const date=new Date(Date.UTC(2025,0,1+i)).toISOString().slice(0,10);
      return {...bars[0],date,close:10+i/100};
    });
    await page.route('**/api/kline/**',route=>route.fulfill({json:longBars}));
    await openTrades(page);await code(page).fill('600000');
    await page.getByRole('button',{name:/查看 sh600000 .*交易K线/}).click();
    const host=chart(page),tools=page.getByRole('group',{name:'交易K线视图'});
    await expect(tools.getByRole('button',{name:'显示全程',exact:true})).toBeEnabled();
    const snapshot=()=>host.evaluate(el=>{
      const c=klinecharts.init(el);return {range:c.getVisibleRange(),space:c.getBarSpace().bar,
        count:c.getDataList().length,width:c.getSize('candle_pane','main').width};
    });
    const expectAll=async()=>{
      await expect.poll(async()=>{const s=await snapshot();return s.range.from<=0&&s.range.to>=s.count;}).toBe(true);
    };
    await expectAll();const initial=await snapshot();
    expect(initial.count).toBe(360);
    if(width===390) expect(initial.space).toBeLessThan(1);
    await tools.getByRole('button',{name:'放大K线',exact:true}).click();
    await expect.poll(async()=>(await snapshot()).space).toBeGreaterThan(initial.space);
    await tools.getByRole('button',{name:'缩小K线',exact:true}).click();
    await expect.poll(async()=>(await snapshot()).space).toBeCloseTo(initial.space,5);
    await host.focus();await page.keyboard.press('+');const manual=(await snapshot()).space;
    await page.setViewportSize({width:width===390?450:1280,height:1000});
    await expect.poll(async()=>(await snapshot()).width).not.toBe(initial.width);
    expect((await snapshot()).space).toBe(manual);
    const all=tools.getByRole('button',{name:'显示全程',exact:true});
    await all.focus();await page.keyboard.press('Enter');await expectAll();await expect(all).toBeFocused();
    await page.setViewportSize({width,height:1000});await expectAll();
    expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
    await info.attach(`kline-controls-${width}`,{body:await page.locator('#tradeKlinePanel').screenshot(),contentType:'image/png'});
  });

  test(`${width}px 布局与截图：滚动归表格，K线可见`, async ({page}, info) => {
    await page.setViewportSize({width, height: 900});
    await openTrades(page);
    await code(page).fill('600000'); await count(page, 1);
    await page.getByRole('button', {name: /查看 sh600000 .*交易K线/}).click();
    await expect(chart(page).locator('canvas').first()).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    expect(await grid(page).evaluate(el => el.clientHeight)).toBe(518);
    expect(await chart(page).evaluate(el => el.clientWidth)).toBeGreaterThan(250);
    await info.attach(`trades-${width}`, {body: await page.screenshot({fullPage: true}), contentType: 'image/png'});
    if (width === 390) await expect(page.locator('#tradeKlineTitle')).toBeInViewport();
  });
}
