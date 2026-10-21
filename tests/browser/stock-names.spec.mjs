import {test, expect} from '@playwright/test';

const names = {sh600000: '浦发银行', sz000001: '平安银行'};
const bars = [{date:'2025-01-02',open:10,close:10.5,high:11,low:9,volume:100,dif:0,dea:0,macd:0},
  {date:'2025-01-03',open:10.5,close:11,high:12,low:10,volume:120,dif:0.1,dea:0,macd:0.1}];

test.beforeEach(async ({page}) => {
  await page.route('https://cdn.jsdelivr.net/**', route => route.fulfill({contentType:'text/javascript',body:''}));
  await page.route('**/api/kline/**', route => route.fulfill({json:bars}));
});

for (const width of [1440, 390]) {
  test(`名称延迟到达后更新表格和K线，${width}px 下代码请求与操作可用`, async ({page}) => {
    await page.setViewportSize({width,height:1000});
    const errors = []; page.on('pageerror', e => errors.push(e.message));
    let release;
    const ready = new Promise(resolve => {release = resolve;});
    await page.route('**/api/stock-names', async route => {await ready; await route.fulfill({json:names});});
    await page.goto('/?tab=trades');
    await expect(page.getByText('匹配 2506 笔交易', {exact:true})).toBeVisible();
    const grid = page.locator('.trade-table');
    const button = () => grid.getByRole('button', {name:/查看 sh600000 .*交易K线/});
    await expect(button()).toBeVisible();
    await button().focus(); await page.keyboard.press('Enter');
    await expect(page.locator('#tradeKlineTitle')).toHaveText('sh600000 · 交易日K与买卖点');
    release();
    await expect(button()).toContainText('sh600000 浦发银行');
    await expect(button()).toHaveAttribute('aria-label', /查看 sh600000 浦发银行 /);
    await expect.poll(() => button().evaluate(el => {
      const cell = el.closest('.tabulator-cell');
      return el.getBoundingClientRect().right <= cell.getBoundingClientRect().right;
    })).toBe(true);
    await expect(page.locator('#tradeKlineTitle')).toHaveText('sh600000 浦发银行 · 交易日K与买卖点');
    await page.getByRole('textbox', {name:'代码筛选',exact:true}).fill('600000');
    await expect(page.getByText('匹配 1 笔交易', {exact:true})).toBeVisible();
    await expect(button()).toContainText('浦发银行');
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
    expect(overflow).toBe(false);
    await page.screenshot({path:`output/browser-tests/stock-names-${width}.png`,fullPage:true});
    expect(errors).toEqual([]);
  });
}

test('目录失败和未知代码保留可用的代码展示', async ({page}) => {
  await page.route('**/api/stock-names', route => route.fulfill({status:503,json:{error:'名称暂不可用'}}));
  await page.goto('/?tab=trades');
  await expect(page.locator('.trade-table').getByRole('button', {name:/查看 sh600000 .*交易K线/})).toHaveText('sh600000 · K线');
  await page.route('**/api/stock-names', route => route.fulfill({json:names}));
  await page.reload();
  await page.getByRole('textbox', {name:'代码筛选',exact:true}).fill('602504');
  await expect(page.getByText('匹配 1 笔交易', {exact:true})).toBeVisible();
  await expect(page.locator('.trade-table').getByRole('button', {name:/查看 sh602504 .*交易K线/})).toHaveText('sh602504 · K线');
});
