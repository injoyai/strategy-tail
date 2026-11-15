import { expect, test } from '@playwright/test';

for (const width of [1440, 390]) {
  test(`${width}px 因子回测可明确选择并提交评分方向`, async ({page}) => {
    await page.setViewportSize({width, height: 900});
    await page.route('https://cdn.jsdelivr.net/**', route => route.fulfill({contentType: 'text/javascript', body: ''}));
    await page.route('**/api/strategy/run', route => route.fulfill({
      status: 409, json: {error: '已有回测任务在运行'},
    }));
    await page.goto('/?tab=strategy');
    await page.getByRole('button', {name: '因子组合', exact: true}).click();
    await page.getByLabel('条件 1 因子', {exact: true}).selectOption('momentum');
    const direction = page.getByLabel('评分方向', {exact: true});
    await page.getByLabel('条件 1 比较方式', {exact: true}).selectOption('score_only');
    await expect(page.getByLabel(/条件 1 下限/)).toHaveCount(0);
    await page.getByRole('button', {name: '运行回测', exact: true}).click();
    await expect(direction).toBeFocused();
    await expect(direction).toHaveAttribute('aria-invalid', 'true');
    await expect(page.getByText('请选择评分方向')).toBeVisible();

    await direction.selectOption('higher_is_better');
    await expect(direction).toHaveValue('higher_is_better');
    await direction.selectOption('lower_is_better');
    await expect(direction).toHaveAttribute('aria-invalid', 'false');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.getByRole('button', {name: '运行回测', exact: true}).click();
    const submitted = page.waitForRequest(req => req.method() === 'POST' && req.url().endsWith('/api/strategy/run'));
    const dialog = page.getByRole('dialog', {name: '运行回测', exact: true});
    await dialog.getByRole('button', {name: '开始回测', exact: true}).click();
    expect((await submitted).postDataJSON()).toMatchObject({
      factorFilters: [],
      factorScores: [{kind: 'momentum', days: 20, direction: 'lower_is_better'}],
    });
    await expect(dialog.getByText('已有回测任务在运行')).toBeVisible();
    await dialog.getByRole('button', {name: '关闭运行配置'}).click();
    await expect(direction).toHaveValue('lower_is_better');

    await page.getByLabel('条件 1 比较方式', {exact: true}).selectOption('between');
    await page.getByLabel(/条件 1 下限/).fill('1');
    await page.getByLabel(/条件 1 上限/).fill('5');
    await page.getByRole('button', {name: '运行回测', exact: true}).click();
    const both = page.waitForRequest(req => req.method() === 'POST' && req.url().endsWith('/api/strategy/run'));
    await dialog.getByRole('button', {name: '开始回测', exact: true}).click();
    expect((await both).postDataJSON()).toMatchObject({
      factorFilters: [{kind: 'momentum', operator: 'between', min: 0.01, max: 0.05}],
      factorScores: [{kind: 'momentum', direction: 'lower_is_better'}],
    });
  });
}
