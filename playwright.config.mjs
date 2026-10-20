import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests/browser',
  outputDir: './output/browser-tests/results',
  reporter: [['list'], ['html', {outputFolder: 'output/browser-tests/report', open: 'never'}]],
  fullyParallel: true,
  use: {
    baseURL: 'http://127.0.0.1:18765',
    viewport: {width: 1440, height: 1000},
    locale: 'zh-CN', timezoneId: 'Asia/Shanghai', reducedMotion: 'reduce',
    trace: 'retain-on-failure', screenshot: 'only-on-failure',
    ...(process.env.PLAYWRIGHT_CHROME ? {channel: 'chrome'} : {}),
  },
  webServer: {
    command: 'go build -o output/browser-test-server ./tests/browser/server && output/browser-test-server',
    url: 'http://127.0.0.1:18765/api/reports',
    reuseExistingServer: false,
    timeout: 120000,
  },
});
