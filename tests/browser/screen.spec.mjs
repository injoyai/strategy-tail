import { test, expect } from '@playwright/test';

test.beforeEach(async ({page}) => {
  await page.route('https://cdn.jsdelivr.net/**', route => route.fulfill({contentType: 'text/javascript', body: ''}));
});

test('配置多套策略、合并命中、按策略查看并保留配置', async ({page}) => {
  await page.goto('/?tab=screen');
  await expect(page.getByRole('heading', {level: 1})).toHaveText('选股策略');
  await expect(page.getByRole('checkbox', {name: /^MACD精选\s*内置$/})).not.toBeChecked();
  await expect(page.getByRole('button', {name: '立即选股'})).toBeDisabled();

  // Keep the actual service for config writes. The browser fixture has no market
  // attached, so inject two synthetic hits for the results presentation.
  const own = ['价格筛选 A', '价格筛选 B'];
  for (const name of own) {
    await page.getByRole('button', {name: '新建策略'}).click();
    const dlg = page.getByRole('dialog');
    await dlg.getByRole('textbox', {name: '策略名称'}).fill(name);
    const save = dlg.getByRole('button', {name: '保存策略'});
    await save.click();
    await expect(dlg.getByRole('alert')).toHaveText('请添加条件或删除空分组');
    await dlg.getByRole('button', {name: '+ 条件', exact: true}).click();
    const component = dlg.getByRole('combobox', {name: /选择组件/});
    await component.selectOption('buy_price');
    await dlg.getByRole('spinbutton', {name: /最低价/}).fill('5');
    await dlg.getByRole('checkbox', {name: '保存后启用'}).check();
    await save.click();
    await expect(dlg).toBeHidden();
  }

  await page.route('**/api/screen', async route => {
    if (route.request().method() !== 'GET') return route.continue();
    const response = await route.fetch();
    const payload = await response.json();
    const enabled = payload.config.strategies.filter(strategy => strategy.enabled);
    await route.fulfill({response, json: {...payload, running: false, error: '',
      result: {asOf: '2026-11-13 10:00:00', universe: 1, evaluated: 1, noSessionBars: 0, failed: 0,
        matches: enabled.length ? [{code: 'sh600000', name: '浦发银行', price: 12, rise: 1.25, time: '2026-11-13 09:59:00',
          hits: enabled.map(strategy => ({id: strategy.id}))}] : []}}});
  });
  await page.reload();
  await expect(page.getByText('2 套已启用')).toBeVisible();
  const table = page.getByRole('table', {name: /选股结果/});
  await expect(table.getByRole('row')).toHaveCount(2);
  await expect(table).toContainText('价格筛选 A');
  await expect(table).toContainText('价格筛选 B');
  await expect(table).toContainText('+1.25%');
  const filter = page.getByRole('combobox', {name: '策略', exact: true});
  await filter.focus();
  await filter.press('Space');
  await filter.press('Escape');
  await filter.selectOption({label: '价格筛选 A'});
  await expect(filter.locator('option:checked')).toHaveText('价格筛选 A');
  await expect(table.getByRole('row')).toHaveCount(2);

  await page.getByRole('button', {name: '编辑 价格筛选 A'}).click();
  let dlg = page.getByRole('dialog');
  await expect(dlg.getByRole('spinbutton', {name: /最低价/})).toHaveValue('5');
  await dlg.getByRole('textbox', {name: '策略名称'}).fill('修改后放弃');
  await page.keyboard.press('Escape');
  await expect(dlg.getByRole('button', {name: '继续编辑'})).toBeFocused();
  await dlg.getByRole('button', {name: '放弃修改'}).click();
  await expect(dlg).toBeHidden();
  await expect(page.getByRole('button', {name: '编辑 价格筛选 A'})).toBeFocused();

  await page.getByRole('button', {name: '删除 价格筛选 A'}).click();
  dlg = page.getByRole('dialog');
  await expect(dlg.getByRole('button', {name: '取消'})).toBeFocused();
  await dlg.getByRole('button', {name: '删除策略'}).click();
  await expect(dlg).toBeHidden();
  await expect(page.getByText('1 套已启用')).toBeVisible();
  const checkbox = page.getByRole('checkbox', {name: '价格筛选 B 自定义'});
  await checkbox.click();
  await expect(checkbox).not.toBeChecked();
  await expect(page.getByText('0 套已启用')).toBeVisible();
  await page.getByRole('button', {name: '删除 价格筛选 B'}).click();
  await page.getByRole('dialog').getByRole('button', {name: '删除策略'}).click();
  await expect(page.getByRole('dialog')).toBeHidden();
});

test('窄屏菜单、旧深链、状态失败与重试', async ({page}) => {
  await page.setViewportSize({width: 390, height: 844});
  let fail = true;
  await page.route('**/api/screen', route => fail
    ? route.fulfill({status: 503, json: {error: '测试读取失败'}})
    : route.continue());
  await page.goto('/?tab=trades');
  await expect(page.getByRole('heading', {level: 1})).toHaveText('交易明细');
  const menu = page.getByRole('button', {name: '菜单', exact: true});
  await menu.click();
  const nav = page.getByRole('navigation', {name: '主导航'});
  await expect(nav.getByRole('button', {name: '策略回测'})).toBeFocused();
  await nav.getByRole('button', {name: '选股策略'}).click();
  await expect(nav).toBeHidden();
  await expect(menu).toBeFocused();
  await expect(page).toHaveURL(/tab=screen$/);
  await expect(page.getByText(/读取选股状态失败/)).toBeVisible();
  fail = false;
  await page.getByRole('button', {name: '重试读取'}).click();
  await expect(page.getByRole('heading', {name: '策略列表'})).toBeVisible();
  await page.getByRole('button', {name: '新建策略'}).click();
  const dlg = page.getByRole('dialog');
  await dlg.getByRole('button', {name: '+ 条件', exact: true}).click();
  await expect(dlg.getByRole('combobox', {name: /选择组件/})).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  expect(await dlg.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
});
