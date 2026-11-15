import { expect, test } from '@playwright/test';

for (const width of [1440, 390]) {
  test(`${width}px 滚动 IC 实验校验并提交独立训练、测试区间`, async ({page}) => {
    await page.setViewportSize({width, height: 900});
    await page.route('https://cdn.jsdelivr.net/**', route => route.fulfill({contentType: 'text/javascript', body: ''}));
    const model = {
      modelId: 'fm_20261114T150100000Z_99aabbcc', revision: 1, modelHash: 'a'.repeat(64),
      evidenceClass: 'exploratory', combination: {method: 'rolling_ic_weight'},
    };
    await page.route(/\/api\/factor-models(?:\?.*)?$/, route => route.fulfill({
      json: {models: [model], total: 1, page: 1, pageSize: 8},
    }));
    const submissions = [];
    await page.route(/\/api\/portfolio-experiments(?:\?.*)?$/, async route => {
      if (route.request().method() === 'POST') {
        submissions.push(route.request().postDataJSON());
        return route.fulfill({status: 201, json: {created: true}});
      }
      return route.fulfill({json: {experiments: {items: [], total: 0}}});
    });
    await page.goto('/?tab=portfolio');
    await page.locator('#pfModelTable').getByRole('button', {name: '实验', exact: true}).click();
    const dlg = page.getByRole('dialog', {name: '创建组合实验（运行）'});
    await dlg.getByLabel('研究区间起').fill('2024-01-01');
    await dlg.getByLabel('研究区间止').fill('2025-12-31');
    await dlg.getByLabel('训练区间起').fill('2024-01-01');
    await dlg.getByLabel('训练区间止').fill('2025-01-02');
    await dlg.getByLabel('测试区间起').fill('2025-01-02');
    await dlg.getByLabel('测试区间止').fill('2025-12-31');
    await dlg.getByRole('button', {name: '创建实验', exact: true}).click();
    await expect(dlg.getByRole('status')).toContainText('训练结束日须早于测试开始日');
    await expect(dlg.getByLabel('测试区间起')).toBeFocused();
    expect(submissions).toHaveLength(0);
    expect(await dlg.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    await dlg.getByLabel('训练区间止').fill('2024-12-31');
    await dlg.getByRole('button', {name: '创建实验', exact: true}).click();
    await expect(dlg).toBeHidden();
    expect(submissions).toHaveLength(1);
    expect(submissions[0]).toMatchObject({
      trainRange: {start: '2024-01-01', end: '2024-12-31'},
      testRange: {start: '2025-01-02', end: '2025-12-31'},
    });
  });
}
