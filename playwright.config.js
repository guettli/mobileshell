// playwright.config.js
// Basic Playwright config for running tests

/** @type {import('@playwright/test').PlaywrightTestConfig} */


const config = {
  timeout: 10000,
  fullyParallel: true,
  workers: undefined, // Use all CPU cores
  use: {
    baseURL: process.env.SERVER_URL,
    headless: true,
    ignoreHTTPSErrors: true,
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    trace: 'on',
  },
  globalSetup: require.resolve('./playwright.global-setup.js'),
  globalTeardown: require.resolve('./playwright.global-teardown.js'),
  reporter: [
    ['list'],
    ['html', { open: 'never', outputFolder: 'playwright-report' }],
  ],
};

module.exports = config;
