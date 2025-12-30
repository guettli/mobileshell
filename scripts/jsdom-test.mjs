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
    // Step 1: Login
    console.log('Step 1: Logging in...');
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

    // Step 2: Get workspaces page
    console.log('\nStep 2: Fetching workspaces page...');
    const workspacesResponse = await request('GET', '/workspaces', {
      headers: { Cookie: sessionCookie },
    });

    assert.equal(workspacesResponse.status, 200, 'Should get workspaces page');
    assert.ok(workspacesResponse.text.includes('hx-post'), 'Page should contain HTMX attributes');
    console.log('✓ Workspaces page loaded');

    // Step 3: Verify HTMX attributes in HTML
    console.log('\nStep 3: Verifying HTMX attributes...');
    const doc = parseHTML(workspacesResponse.text);

    // Check for HTMX form submission
    const createForm = doc.querySelector('[hx-post*="hx-create"]');
    assert.ok(createForm, 'Should have workspace creation form with hx-post');
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

    // Step 4: Navigate to workspace and get the page
    console.log('\nStep 4: Loading workspace page...');
    const workspacePageResponse = await request('GET', `/workspaces/${workspaceId}`, {
      headers: { Cookie: sessionCookie },
    });

    assert.equal(workspacePageResponse.status, 200, 'Should load workspace page');
    assert.ok(workspacePageResponse.text.includes('hx-execute'), 'Should have execute form');
    console.log('✓ Workspace page loaded');

    // Step 5: Execute a command via HTMX
    console.log('\nStep 5: Executing command via HTMX...');
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

    // Step 6: Wait for command to complete and verify output
    console.log('\nStep 6: Waiting for command output...');
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

    // Step 7: Verify HTMX polling endpoint
    console.log('\nStep 7: Testing HTMX polling endpoint...');
    const runningProcessesResponse = await request('GET', `/workspaces/${workspaceId}/hx-running-processes`, {
      headers: {
        Cookie: sessionCookie,
        'HX-Request': 'true',
      },
    });

    assert.equal(runningProcessesResponse.status, 200, 'Should get running processes');
    // The process might be finished by now, but the endpoint should work
    console.log('✓ Polling endpoint works');

    // Step 8: Test finished processes endpoint (pagination)
    console.log('\nStep 8: Testing finished processes (pagination)...');
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

    console.log('\n✅ All tests passed!');
    process.exit(0);

  } catch (error) {
    console.error('\n❌ Test failed:', error.message);
    console.error(error.stack);
    process.exit(1);
  }
}

runTests();
