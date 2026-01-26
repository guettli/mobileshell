// tests/mobileshell.spec.ts

import { expect, test } from '@playwright/test';

const PASSWORD = process.env.PASSWORD || 'test-password-123456789012345678901234567890';


async function login(page, testID: string) {
  await page.goto('/login', {
    headers: { 'X-Test-ID': testID },
  });
  await page.fill('input[type="password"]', PASSWORD);
  await Promise.all([
    page.waitForNavigation(),
    page.click('button[type="submit"]'),
  ]);
  await expect(page).toHaveURL(/workspaces/);
}

test.describe.parallel('MobileShell basic flows', () => {
  test('Workspaces and HTMX', async ({ page }, testInfo) => {
    const testID = `workspaces-htmx-${testInfo.workerIndex}-${testInfo.repeatEachIndex}`;
    page.on('console', msg => console.log(`[browser][${testID}]`, msg.type(), msg.text()));
    page.on('pageerror', err => console.error(`[browser][${testID}] PAGE ERROR:`, err));
    await login(page, testID);
    await expect(page.locator('[hx-post*="hx-create"]')).toBeVisible();
    // Check for HTMX attributes
    const htmxElements = await page.locator('[hx-target]').all();
    for (const elem of htmxElements) {
      const targetSelector = await elem.getAttribute('hx-target');
      if (targetSelector) {
        await expect(page.locator(targetSelector)).toBeVisible();
      }
    }
  });

  test('Command execution', async ({ page }, testInfo) => {
    const testID = `command-execution-${testInfo.workerIndex}-${testInfo.repeatEachIndex}`;
    page.on('console', msg => console.log(`[browser][${testID}]`, msg.type(), msg.text()));
    page.on('pageerror', err => console.error(`[browser][${testID}] PAGE ERROR:`, err));
    await login(page, testID);
    // Create workspace
    const workspaceName = `pw-test-${Date.now()}`;
    await page.fill('input[name="name"]', workspaceName);
    await page.click('button[type="submit"]');
    await expect(page).toHaveURL(/workspaces\//);
    // Execute command
    await page.fill('input[name="command"]', 'echo "Hello from Playwright"');
    await page.click('button[type="submit"]');
    await expect(page.locator('text=echo "Hello from Playwright"')).toBeVisible();
    // Wait for output
    await expect(page.locator('text=Hello from Playwright')).toBeVisible({ timeout: 10000 });
  });
});
