import { expect, test } from '@playwright/test';

for (const width of [1440, 375]) {
  test(`只读行情状态面板可在 ${width}px 查看`, async ({page}) => {
    await page.setViewportSize({width, height: 900});
    await page.route('**/api/market-data/status', route => route.fulfill({
      json: {benchmarkDate: '2026-11-10'},
    }));
    await page.goto('/');
    const bubble = page.getByRole('button', {name: /行情数据，只读/});
    await bubble.focus();
    await page.keyboard.press('Enter');
    const panel = page.getByRole('region', {name: '行情数据'});
    await expect(panel).toBeVisible();
    await expect(panel).toContainText('2026-11-10');
    await expect(panel.getByRole('button')).toHaveCount(1);
    await page.keyboard.press('Escape');
    await expect(panel).toBeHidden();
    await expect(bubble).toBeFocused();
  });
}
