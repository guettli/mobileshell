// tests/mobileshell.spec.ts

import { expect, test } from '@playwright/test';

const PASSWORD = process.env.PASSWORD || 'test-password-123456789012345678901234567890';


async function login(page, testID: string) {
  await page.goto('/login', {
    headers: { 'X-Test-ID': testID },
  });
  await page.fill('input[type="password"]', PASSWORD);
  await page.click('button[type="submit"]');
  // HTMX will swap the body content but not change the URL
  // Wait for a known element on the workspaces page to appear
  await page.waitForSelector('[hx-post*="hx-create"]', { timeout: 8000 });
  // Verify we're logged in by checking for workspace elements
  await expect(page.locator('h5:has-text("Create New Workspace")')).toBeVisible();
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
    await page.fill('input[name="directory"]', '/tmp');
    await page.click('button[type="submit"]');
    await expect(page).toHaveURL(/workspaces\//);
    // Execute command
    await page.fill('input[name="command"]', 'echo "Hello from Playwright"');
    await page.click('button[type="submit"]');
    // Wait for the command to finish and appear in the UI
    // The output is loaded lazily, so we need to scroll or wait for it to load
    await page.waitForTimeout(2000); // Give time for process to finish and WebSocket to update
    // Look for the output in a pre or code block
    const outputLocator = page.locator('pre:has-text("Hello from Playwright"), code:has-text("Hello from Playwright"), div:has-text("Hello from Playwright")').first();
    await expect(outputLocator).toBeVisible({ timeout: 10000 });
  });
});
