#!/usr/bin/env node

import { JSDOM } from 'jsdom';
import { strict as assert } from 'assert';

// Get server URL from environment
const SERVER_URL = process.env.SERVER_URL || 'http://localhost:22123';
const PASSWORD = process.env.PASSWORD || 'test-password-123456789012';

console.log(`Testing MobileShell server at ${SERVER_URL}`);

// Helper to make HTTP requests
async function request(method, path, options = {}) {
  const url = `${SERVER_URL}${path}`;
  const response = await fetch(url, {
    method,
    headers: {
      'Content-Type': 'application/x-www-form-urlencoded',
      ...options.headers,
    },
    body: options.body,
    redirect: 'manual',
  });

  return {
    status: response.status,
    headers: Object.fromEntries(response.headers.entries()),
    text: await response.text(),
  };
}

// Helper to parse HTML and check for HTMX attributes
function parseHTML(html) {
  const dom = new JSDOM(html, {
    url: SERVER_URL,
  });
  return dom.window.document;
}

// Test flow
async function runTests() {
  let sessionCookie = null;

  try {
    // Login
    console.log('Logging in...');
    console.log(`Password length: ${PASSWORD.length}`);
    const loginResponse = await request('POST', '/login', {
      body: `password=${encodeURIComponent(PASSWORD)}`,
    });

    if (loginResponse.status !== 302 && loginResponse.status !== 303) {
      console.error('Login failed. Response status:', loginResponse.status);
      console.error('Response body:', loginResponse.text.substring(0, 500));
    }

    assert.ok([302, 303].includes(loginResponse.status), `Login should redirect (got ${loginResponse.status})`);
    assert.ok(['/', '/workspaces'].includes(loginResponse.headers.location), `Should redirect to / or /workspaces (got ${loginResponse.headers.location})`);

    // Extract session cookie
    const setCookie = loginResponse.headers['set-cookie'];
    assert.ok(setCookie, 'Should set session cookie');
    sessionCookie = setCookie.split(';')[0];
    console.log('✓ Login successful');

    // Get workspaces page
    console.log('\nFetching workspaces page...');
    const workspacesResponse = await request('GET', '/workspaces', {
      headers: { Cookie: sessionCookie },
    });

    assert.equal(workspacesResponse.status, 200, 'Should get workspaces page');
    assert.ok(workspacesResponse.text.includes('hx-post'), 'Page should contain HTMX attributes');
    console.log('✓ Workspaces page loaded');

    // Verify HTMX attributes in HTML
    console.log('\nVerifying HTMX attributes...');
    const doc = parseHTML(workspacesResponse.text);

    // Check for HTMX form submission
    const createForm = doc.querySelector('[hx-post*="hx-create"]');
    assert.ok(createForm, 'Should have workspace creation form with hx-post');

    // Verify all hx-target selectors have matching elements
    const elementsWithTarget = doc.querySelectorAll('[hx-target]');
    for (const elem of elementsWithTarget) {
      const targetSelector = elem.getAttribute('hx-target');
      const targetExists = doc.querySelector(targetSelector);
      assert.ok(targetExists, `hx-target="${targetSelector}" should have matching element in DOM`);
    }

    console.log('✓ HTMX attributes found in page');

    // Create workspace via API (simulating HTMX hx-post)
    const workspaceName = `test-workspace-${Date.now()}`;
    const createWorkspaceResponse = await request('POST', '/workspaces/hx-create', {
      headers: {
        Cookie: sessionCookie,
        'HX-Request': 'true',
      },
      body: `name=${encodeURIComponent(workspaceName)}&directory=/tmp&pre_command=`,
    });

    assert.equal(createWorkspaceResponse.status, 200, 'Should create workspace');

    // HTMX redirect header should be set on success
    const hxRedirect = createWorkspaceResponse.headers['hx-redirect'];
    assert.ok(hxRedirect, 'Should have HX-Redirect header on successful creation');
    assert.ok(hxRedirect.includes('/workspaces/'), 'HX-Redirect should point to workspace');

    // Extract workspace ID from HX-Redirect header
    const workspaceMatch = hxRedirect.match(/\/workspaces\/([^\/]+)/);
    assert.ok(workspaceMatch, 'Should have workspace ID in redirect URL');
    const workspaceId = workspaceMatch[1];
    console.log(`✓ Workspace created: ${workspaceName} (ID: ${workspaceId})`);

    // Navigate to workspace and get the page
    console.log('\nLoading workspace page...');
    const workspacePageResponse = await request('GET', `/workspaces/${workspaceId}`, {
      headers: { Cookie: sessionCookie },
    });

    assert.equal(workspacePageResponse.status, 200, 'Should load workspace page');
    assert.ok(workspacePageResponse.text.includes('hx-execute'), 'Should have execute form');

    // Verify all hx-target selectors have matching elements in workspace page
    const workspaceDoc = parseHTML(workspacePageResponse.text);
    const workspaceTargets = workspaceDoc.querySelectorAll('[hx-target]');
    for (const elem of workspaceTargets) {
      const targetSelector = elem.getAttribute('hx-target');
      const targetExists = workspaceDoc.querySelector(targetSelector);
      assert.ok(targetExists, `Workspace page: hx-target="${targetSelector}" should have matching element in DOM`);
    }

    console.log('✓ Workspace page loaded');

    // Execute a command via HTMX
    console.log('\nExecuting command via HTMX...');
    const executeResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
      headers: {
        Cookie: sessionCookie,
        'HX-Request': 'true',
      },
      body: 'command=echo "Hello from JSDOM test"',
    });

    assert.equal(executeResponse.status, 200, 'Should execute command');
    assert.ok(executeResponse.text.includes('echo'), 'Response should show the command');

    // Extract process ID from the response
    const processMatch = executeResponse.text.match(/processes\/([^\/]+)\/hx-output/);
    assert.ok(processMatch, 'Should have process output link');
    const processId = processMatch[1];
    console.log(`✓ Command executed (Process ID: ${processId})`);

    // Wait for command to complete and verify output
    console.log('\nWaiting for command output...');
    let outputFound = false;
    for (let i = 0; i < 10; i++) {
      await new Promise(resolve => setTimeout(resolve, 500));

      const outputResponse = await request('GET', `/workspaces/${workspaceId}/processes/${processId}/hx-output`, {
        headers: {
          Cookie: sessionCookie,
          'HX-Request': 'true',
        },
      });

      if (outputResponse.text.includes('Hello from JSDOM test')) {
        outputFound = true;
        console.log('✓ Command output visible:');
        console.log('  Output contains: "Hello from JSDOM test"');

        // Verify output has proper structure (either badge class or process info)
        assert.ok(outputResponse.text.length > 50, 'Output should have meaningful content');
        break;
      }
    }

    assert.ok(outputFound, 'Should find command output within timeout');

    // Test finished processes endpoint (pagination)
    console.log('\nTesting finished processes (pagination)...');
    const finishedProcessesResponse = await request('GET', `/workspaces/${workspaceId}/hx-finished-processes?offset=0&limit=10`, {
      headers: {
        Cookie: sessionCookie,
        'HX-Request': 'true',
      },
    });

    assert.equal(finishedProcessesResponse.status, 200, 'Should get finished processes');
    assert.ok(finishedProcessesResponse.text.includes('echo') || finishedProcessesResponse.text.includes('No finished processes'),
      'Should show finished process or empty state');
    console.log('✓ Pagination endpoint works');

    // Test process transition from running to finished
    console.log('\nTesting process transition from running to finished...');

    // Execute a short-lived command
    const shortCommandResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
      headers: {
        Cookie: sessionCookie,
        'HX-Request': 'true',
      },
      body: 'command=sleep 0.1',
    });

    assert.equal(shortCommandResponse.status, 200, 'Should execute sleep command');
    const sleepProcessMatch = shortCommandResponse.text.match(/processes\/([^\/]+)\/hx-output/);
    assert.ok(sleepProcessMatch, 'Should have sleep process output link');
    const sleepProcessId = sleepProcessMatch[1];
    console.log(`  Command started (Process ID: ${sleepProcessId})`);

    // Poll JSON endpoint to see the process while it's running
    let foundInRunning = false;
    for (let i = 0; i < 5; i++) {
      await new Promise(resolve => setTimeout(resolve, 50));

      const runningCheck = await request('GET', `/workspaces/${workspaceId}/json-process-updates`, {
        headers: {
          Cookie: sessionCookie,
        },
      });

      const runningData = JSON.parse(runningCheck.text);
      const hasNewProcess = runningData.updates && runningData.updates.some(u =>
        u.status === 'new' && (u.id === sleepProcessId || u.html.includes('sleep'))
      );

      if (hasNewProcess) {
        foundInRunning = true;
        console.log('  ✓ Process found in running processes');
        break;
      }
    }

    // Wait for the process to complete
    await new Promise(resolve => setTimeout(resolve, 200));

    // Now poll JSON endpoint to check if process reports as finished
    let foundFinished = false;
    let processMovedToFinished = false;

    for (let i = 0; i < 10; i++) {
      await new Promise(resolve => setTimeout(resolve, 500));

      const updateCheck = await request('GET', `/workspaces/${workspaceId}/json-process-updates?process_ids=${sleepProcessId}`, {
        headers: {
          Cookie: sessionCookie,
        },
      });

      const updateData = JSON.parse(updateCheck.text);

      // Check if JSON endpoint reports the process as finished
      const finishedUpdate = updateData.updates && updateData.updates.find(u =>
        u.id === sleepProcessId && u.status === 'finished'
      );

      if (finishedUpdate) {
        foundFinished = true;
        console.log('  ✓ JSON endpoint reports process as finished');

        // Check if it appears in finished processes list
        const finishedCheck = await request('GET', `/workspaces/${workspaceId}/hx-finished-processes?offset=0`, {
          headers: {
            Cookie: sessionCookie,
            'HX-Request': 'true',
          },
        });

        if (finishedCheck.text.includes(sleepProcessId) || finishedCheck.text.includes('sleep 0.1')) {
          processMovedToFinished = true;
          console.log('  ✓ Process appears in finished processes');
          break;
        }
      }
    }

    assert.ok(processMovedToFinished, 'Process should transition from running to finished');
    if (foundInRunning) {
      assert.ok(foundFinished, 'JSON endpoint should report process as finished');
    }
    console.log('✓ Process transition works correctly');

    console.log('\n✅ All tests passed!');
    process.exit(0);

  } catch (error) {
    console.error('\n❌ Test failed:', error.message);
    console.error(error.stack);
    process.exit(1);
  }
}

runTests();
