#!/usr/bin/env node

import { strict as assert } from 'assert';
import { JSDOM } from 'jsdom';

// Get server URL from environment
const SERVER_URL = process.env.SERVER_URL || 'http://localhost:22123';
const PASSWORD = process.env.PASSWORD || 'test-password-123456789012345678901234567890';
const SERVER_LOG = process.env.SERVER_LOG || '';

console.log(`Testing MobileShell server at ${SERVER_URL}`);

// Helper to make HTTP requests
async function request(method, path, options = {}) {
  const url = `${SERVER_URL}${path}`;
  const headers = {
    'Content-Type': 'application/x-www-form-urlencoded',
    ...options.headers,
  };

  // Add test context header if provided
  if (options.testID) {
    headers['X-Test-ID'] = options.testID;
  }

  const response = await fetch(url, {
    method,
    headers,
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

// Login helper
async function login() {
  const loginResponse = await request('POST', '/login', {
    body: `password=${encodeURIComponent(PASSWORD)}`,
  });

  assert.ok([302, 303].includes(loginResponse.status), `Login should redirect (got ${loginResponse.status})`);

  const setCookie = loginResponse.headers['set-cookie'];
  assert.ok(setCookie, 'Should set session cookie');
  return setCookie.split(';')[0];
}

// Create workspace helper
async function createWorkspace(sessionCookie, workspaceName, testID = null) {
  const createWorkspaceResponse = await request('POST', '/workspaces/hx-create', {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: `name=${encodeURIComponent(workspaceName)}&directory=/tmp&pre_command=`,
    testID,
  });

  assert.equal(createWorkspaceResponse.status, 200, 'Should create workspace');

  const hxRedirect = createWorkspaceResponse.headers['hx-redirect'];
  if (!hxRedirect) {
    console.error('Response headers:', createWorkspaceResponse.headers);
    console.error('Response body:', createWorkspaceResponse.text);
    throw new Error('Missing hx-redirect header in workspace creation response');
  }
  const workspaceMatch = hxRedirect.match(/\/workspaces\/([^\/]+)/);
  assert.ok(workspaceMatch, 'Should have workspace ID in redirect URL');

  return workspaceMatch[1];
}

// Test 1: Workspaces and HTMX
async function testWorkspacesAndHTMX() {
  const testName = 'Test 1: Workspaces and HTMX';
  const testID = 'test-1-workspaces-htmx';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  console.log('✓ Login successful');

  // Get workspaces page
  const workspacesResponse = await request('GET', '/workspaces', {
    headers: { Cookie: sessionCookie },
    testID,
  });

  assert.equal(workspacesResponse.status, 200, 'Should get workspaces page');
  assert.ok(workspacesResponse.text.includes('hx-post'), 'Page should contain HTMX attributes');

  // Verify HTMX attributes in HTML
  const doc = parseHTML(workspacesResponse.text);
  const createForm = doc.querySelector('[hx-post*="hx-create"]');
  assert.ok(createForm, 'Should have workspace creation form with hx-post');

  // Verify all hx-target selectors have matching elements
  const elementsWithTarget = doc.querySelectorAll('[hx-target]');
  for (const elem of elementsWithTarget) {
    const targetSelector = elem.getAttribute('hx-target');
    const targetExists = doc.querySelector(targetSelector);
    assert.ok(targetExists, `hx-target="${targetSelector}" should have matching element in DOM`);
  }

  // Create workspace
  const workspaceName = `test-workspace-${Date.now()}-1`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName, testID);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Navigate to workspace and verify
  const workspacePageResponse = await request('GET', `/workspaces/${workspaceId}`, {
    headers: { Cookie: sessionCookie },
    testID,
  });

  assert.equal(workspacePageResponse.status, 200, 'Should load workspace page');
  assert.ok(workspacePageResponse.text.includes('hx-execute'), 'Should have execute form');

  console.log(`✓ ${testName} passed`);
}

// Test 2: Command execution and output
async function testCommandExecution() {
  const testName = 'Test 2: Command execution';
  const testID = 'test-2-command-execution';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-2`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName, testID);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Execute a command via HTMX
  const executeResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: 'command=echo "Hello from JSDOM test"',
    testID,
  });

  assert.equal(executeResponse.status, 200, 'Should execute command');
  assert.ok(executeResponse.text.includes('echo'), 'Response should show the command');

  // Extract process ID
  const processMatch = executeResponse.text.match(/processes\/([^\/]+)\/hx-output/);
  assert.ok(processMatch, 'Should have process output link');
  const processId = processMatch[1];

  // Wait for command output
  let outputFound = false;
  for (let i = 0; i < 20; i++) {
    await new Promise(resolve => setTimeout(resolve, 500));

    const outputResponse = await request('GET', `/workspaces/${workspaceId}/processes/${processId}/hx-output`, {
      headers: {
        Cookie: sessionCookie,
        'HX-Request': 'true',
      },
      testID,
    });

    if (outputResponse.text.includes('Hello from JSDOM test')) {
      outputFound = true;
      break;
    }
  }

  assert.ok(outputFound, 'Should find command output within timeout');
  console.log(`✓ ${testName} passed`);
}

// Test 3: Process transitions
async function testProcessTransitions() {
  const testName = 'Test 3: Process transitions';
  const testID = 'test-3-process-transitions';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-3`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName, testID);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Execute a short-lived command
  const shortCommandResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: 'command=sleep 0.1',
    testID,
  });

  assert.equal(shortCommandResponse.status, 200, 'Should execute sleep command');
  const sleepProcessMatch = shortCommandResponse.text.match(/processes\/([^\/]+)\/hx-output/);
  assert.ok(sleepProcessMatch, 'Should have sleep process output link');
  const sleepProcessId = sleepProcessMatch[1];

  // Wait for the process to complete
  await new Promise(resolve => setTimeout(resolve, 500));

  // Check if process reports as finished
  let processMovedToFinished = false;

  for (let i = 0; i < 10; i++) {
    await new Promise(resolve => setTimeout(resolve, 500));

    const updateCheck = await request('GET', `/workspaces/${workspaceId}/json-process-updates?process_ids=${sleepProcessId}`, {
      headers: {
        Cookie: sessionCookie,
      },
      testID,
    });

    const updateData = JSON.parse(updateCheck.text);
    const finishedUpdate = updateData.updates && updateData.updates.find(u =>
      u.id === sleepProcessId && u.status === 'finished'
    );

    if (finishedUpdate) {
      // Check if it appears in finished processes list
      const finishedCheck = await request('GET', `/workspaces/${workspaceId}/hx-finished-processes?offset=0`, {
        headers: {
          Cookie: sessionCookie,
          'HX-Request': 'true',
        },
        testID,
      });

      if (finishedCheck.text.includes(sleepProcessId) || finishedCheck.text.includes('sleep 0.1')) {
        processMovedToFinished = true;
        break;
      }
    }
  }

  assert.ok(processMovedToFinished, 'Process should transition from running to finished');
  console.log(`✓ ${testName} passed`);
}

// Test 4: Per-process pages
async function testPerProcessPages() {
  const testName = 'Test 4: Per-process pages';
  const testID = 'test-4-per-process-pages';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-4`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName, testID);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Execute a long-running command
  const longCommandResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: 'command=sleep 10',
    testID,
  });

  assert.equal(longCommandResponse.status, 200, 'Should execute long command');
  const longProcessMatch = longCommandResponse.text.match(/processes\/([^\/]+)\/hx-output/);
  assert.ok(longProcessMatch, 'Should have process output link');
  const longProcessId = longProcessMatch[1];

  // Wait for the process to be available
  await new Promise(resolve => setTimeout(resolve, 500));

  // Get the running processes
  const runningCheckResponse = await request('GET', `/workspaces/${workspaceId}/json-process-updates`, {
    headers: {
      Cookie: sessionCookie,
    },
    testID,
  });

  const runningData = JSON.parse(runningCheckResponse.text);
  const runningUpdate = runningData.updates && runningData.updates.find(u =>
    u.id === longProcessId && (u.status === 'new' || u.status === 'running')
  );

  assert.ok(runningUpdate, 'Should find the running process in updates');
  assert.ok(runningUpdate.html, 'Running process should have HTML');

  // Parse the HTML to find the badge link
  const runningDoc = parseHTML(runningUpdate.html);
  const runningBadgeLink = runningDoc.querySelector('a[href*="/processes/"] .badge.bg-primary');
  assert.ok(runningBadgeLink, 'Running process badge should be inside a link');

  const runningLink = runningBadgeLink.closest('a');
  assert.ok(runningLink, 'Should have link element');
  assert.ok(runningLink.href.includes(`/processes/${longProcessId}`), 'Link should point to process page');

  // Follow the link to the per-process page
  const processPageUrl = new URL(runningLink.href).pathname;
  const processPageResponse = await request('GET', processPageUrl, {
    headers: {
      Cookie: sessionCookie,
    },
    testID,
  });

  assert.equal(processPageResponse.status, 200, 'Should load per-process page');
  assert.ok(processPageResponse.text.includes('Process Details'), 'Page should have "Process Details" heading');
  assert.ok(processPageResponse.text.includes('sleep 10'), 'Page should show the command');

  // Terminate the long process
  await request('POST', `/workspaces/${workspaceId}/processes/${longProcessId}/hx-send-signal`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: 'signal=15',
    testID,
  });

  console.log(`✓ ${testName} passed`);
}

// Test 5: Stdin input
async function testStdinInput() {
  const testName = 'Test 5: Stdin input';
  const testID = 'test-5-stdin-input';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-5`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName, testID);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Start cat command
  const catCommandResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: 'command=cat',
    testID,
  });

  assert.equal(catCommandResponse.status, 200, 'Should execute cat command');
  const catProcessMatch = catCommandResponse.text.match(/processes\/([^\/]+)\/hx-output/);
  assert.ok(catProcessMatch, 'Should have cat process output link');
  const catProcessId = catProcessMatch[1];

  // Wait for cat process to be ready
  await new Promise(resolve => setTimeout(resolve, 3000));

  // Send first input
  const stdin1Response = await request('POST', `/workspaces/${workspaceId}/processes/${catProcessId}/hx-send-stdin`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: 'stdin=foo1',
    testID,
  });

  assert.equal(stdin1Response.status, 200, 'Should send first stdin');

  // Wait for "foo1" to appear
  let foo1Found = false;
  for (let i = 0; i < 10; i++) {
    await new Promise(resolve => setTimeout(resolve, 500));

    const outputResponse = await request('GET', `/workspaces/${workspaceId}/processes/${catProcessId}/hx-output?type=combined`, {
      headers: {
        Cookie: sessionCookie,
        'HX-Request': 'true',
      },
      testID,
    });

    if (outputResponse.text.includes('foo1')) {
      foo1Found = true;
      break;
    }
  }

  assert.ok(foo1Found, 'Should find "foo1" in cat output');

  // Send second input
  const stdin2Response = await request('POST', `/workspaces/${workspaceId}/processes/${catProcessId}/hx-send-stdin`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: 'stdin=foo2',
    testID,
  });

  assert.equal(stdin2Response.status, 200, 'Should send second stdin');

  // Wait for "foo2" to appear
  let foo2Found = false;
  for (let i = 0; i < 10; i++) {
    await new Promise(resolve => setTimeout(resolve, 300));

    const outputResponse = await request('GET', `/workspaces/${workspaceId}/processes/${catProcessId}/hx-output?type=combined`, {
      headers: {
        Cookie: sessionCookie,
        'HX-Request': 'true',
      },
      testID,
    });

    if (outputResponse.text.includes('foo2')) {
      foo2Found = true;
      assert.ok(outputResponse.text.includes('foo1'), 'Both foo1 and foo2 should be in output');
      break;
    }
  }

  assert.ok(foo2Found, 'Should find "foo2" in cat output');

  // Terminate the cat process
  await request('POST', `/workspaces/${workspaceId}/processes/${catProcessId}/hx-send-signal`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: 'signal=15',
    testID,
  });

  console.log(`✓ ${testName} passed`);
}

// Test 6: Workspace editing
async function testWorkspaceEditing() {
  const testName = 'Test 6: Workspace editing';
  const testID = 'test-6-workspace-editing';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-6`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName, testID);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Navigate to edit page
  const editPageResponse = await request('GET', `/workspaces/${workspaceId}/edit`, {
    headers: { Cookie: sessionCookie },
    testID,
  });

  assert.equal(editPageResponse.status, 200, 'Should load edit workspace page');
  assert.ok(editPageResponse.text.includes('Edit Workspace'), 'Page should have "Edit Workspace" title');

  // Parse the edit page to verify form fields
  const editPageDoc = parseHTML(editPageResponse.text);
  const nameInput = editPageDoc.querySelector('input[name="name"]');
  const directoryInput = editPageDoc.querySelector('input[name="directory"]');
  const preCommandInput = editPageDoc.querySelector('textarea[name="pre_command"]');

  assert.ok(nameInput, 'Should have name input field');
  assert.ok(directoryInput, 'Should have directory input field');
  assert.ok(preCommandInput, 'Should have pre-command textarea field');
  assert.equal(nameInput.value, workspaceName, 'Name field should have current name');

  // Update the workspace
  const updatedName = `${workspaceName}-updated`;
  const updatedPreCommand = 'source .env';
  const updateResponse = await request('POST', `/workspaces/${workspaceId}/edit`, {
    headers: {
      Cookie: sessionCookie,
    },
    body: `name=${encodeURIComponent(updatedName)}&directory=/tmp&pre_command=${encodeURIComponent(updatedPreCommand)}`,
    testID,
  });

  assert.ok([302, 303].includes(updateResponse.status), `Should redirect after update (got ${updateResponse.status})`);

  // Verify the changes
  const workspaceAfterEditResponse = await request('GET', `/workspaces/${workspaceId}`, {
    headers: { Cookie: sessionCookie },
    testID,
  });

  assert.equal(workspaceAfterEditResponse.status, 200, 'Should load workspace page after edit');
  assert.ok(workspaceAfterEditResponse.text.includes(updatedName), 'Page should show updated workspace name');

  // Test validation: try to update with empty name
  const invalidUpdateResponse = await request('POST', `/workspaces/${workspaceId}/edit`, {
    headers: {
      Cookie: sessionCookie,
    },
    body: `name=&directory=/tmp&pre_command=`,
    testID,
  });

  assert.equal(invalidUpdateResponse.status, 200, 'Should return form with error (not redirect)');
  assert.ok(invalidUpdateResponse.text.includes('required'), 'Should show validation error message');

  console.log(`✓ ${testName} passed`);
}

// Test 7: File autocomplete
async function testFileAutocomplete() {
  const testName = 'Test 7: File autocomplete';
  const testID = 'test-7-file-autocomplete';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-7`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName, testID);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Create test files with a single command to ensure they all exist
  const setupCommand = 'touch test-file1.go test-file2.go readme.md && mkdir -p subdir && touch subdir/nested.go && mkdir -p deep/nested/path && touch deep/nested/path/deep-file.txt';

  const setupResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: `command=${encodeURIComponent(setupCommand)}`,
    testID,
  });

  // Extract process ID to wait for command completion
  const processMatch = setupResponse.text.match(/processes\/([^\/]+)\/hx-output/);
  if (processMatch) {
    const processId = processMatch[1];
    console.log('Setup process ID:', processId);

    // Wait for the setup command to complete
    let completed = false;
    for (let i = 0; i < 30; i++) {
      await new Promise(resolve => setTimeout(resolve, 300));

      const statusResponse = await request('GET', `/workspaces/${workspaceId}/json-process-updates?process_ids=${processId}`, {
        headers: {
          Cookie: sessionCookie,
        },
        testID,
      });

      const statusData = JSON.parse(statusResponse.text);
      const processUpdate = statusData.updates && statusData.updates.find(u => u.id === processId);

      if (i % 5 === 0 && !completed) {
        console.log(`Waiting for setup command... (${i * 300}ms, status: ${processUpdate ? processUpdate.status : 'not found'})`);
      }

      if (processUpdate && processUpdate.status === 'finished') {
        console.log('Setup command completed');
        completed = true;
        break;
      }
    }
    if (!completed) {
      console.log('WARNING: Setup command did not complete within timeout');
    }
  } else {
    console.log('WARNING: Could not extract process ID from setup response');
  }

  // Additional wait to ensure filesystem is synced
  await new Promise(resolve => setTimeout(resolve, 500));

  // Test 1: Simple wildcard pattern
  const simplePatternResponse = await request('GET', `/workspaces/${workspaceId}/files/autocomplete?pattern=${encodeURIComponent('*.go')}`, {
    headers: {
      Cookie: sessionCookie,
    },
    testID,
  });

  assert.equal(simplePatternResponse.status, 200, 'Should get autocomplete results');
  const simpleData = JSON.parse(simplePatternResponse.text);
  console.log('Autocomplete response:', JSON.stringify(simpleData, null, 2));
  console.log('Matches found:', simpleData.matches.length);
  if (simpleData.matches.length === 0) {
    // Debug: check what files exist in the workspace
    const listResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
      headers: {
        Cookie: sessionCookie,
        'HX-Request': 'true',
      },
      body: `command=${encodeURIComponent('ls -la')}`,
      testID,
    });
    console.log('Directory listing response:', listResponse.text.substring(0, 500));
  }
  assert.ok(simpleData.matches, 'Should have matches array');
  assert.ok(simpleData.matches.length >= 2, 'Should find at least 2 .go files');
  assert.ok(simpleData.matches.some(m => m.relative_path.includes('test-file1.go')), 'Should include test-file1.go');
  assert.ok(simpleData.matches.some(m => m.relative_path.includes('test-file2.go')), 'Should include test-file2.go');
  console.log(`✓ Simple wildcard pattern found ${simpleData.matches.length} files`);

  // Test 2: Recursive pattern with **
  const recursivePatternResponse = await request('GET', `/workspaces/${workspaceId}/files/autocomplete?pattern=${encodeURIComponent('**/*.go')}`, {
    headers: {
      Cookie: sessionCookie,
    },
    testID,
  });

  assert.equal(recursivePatternResponse.status, 200, 'Should get recursive autocomplete results');
  const recursiveData = JSON.parse(recursivePatternResponse.text);
  assert.ok(recursiveData.matches.length >= 3, 'Should find at least 3 .go files recursively');
  assert.ok(recursiveData.matches.some(m => m.relative_path.includes('subdir/nested.go')), 'Should include nested .go file');
  console.log(`✓ Recursive pattern found ${recursiveData.matches.length} files`);

  // Test 3: Verify JSON structure
  const firstMatch = recursiveData.matches[0];
  assert.ok(firstMatch.path, 'Match should have path');
  assert.ok(firstMatch.relative_path, 'Match should have relative_path');
  assert.ok(firstMatch.mod_time, 'Match should have mod_time');
  console.log('✓ JSON structure validated');

  // Test 4: Verify has_more and total_matches
  assert.ok(typeof recursiveData.has_more === 'boolean', 'Should have has_more boolean');
  assert.ok(typeof recursiveData.total_matches === 'number', 'Should have total_matches number');
  assert.ok(typeof recursiveData.timed_out === 'boolean', 'Should have timed_out boolean');
  console.log('✓ Response metadata validated');

  // Test 5: Empty pattern returns empty results
  const emptyPatternResponse = await request('GET', `/workspaces/${workspaceId}/files/autocomplete?pattern=`, {
    headers: {
      Cookie: sessionCookie,
    },
    testID,
  });

  const emptyData = JSON.parse(emptyPatternResponse.text);
  assert.equal(emptyData.matches.length, 0, 'Empty pattern should return no matches');
  console.log('✓ Empty pattern handled correctly');

  // Test 6: Pattern with subdirectory - use simpler pattern
  const subdirPatternResponse = await request('GET', `/workspaces/${workspaceId}/files/autocomplete?pattern=${encodeURIComponent('**/*.txt')}`, {
    headers: {
      Cookie: sessionCookie,
    },
    testID,
  });

  const subdirData = JSON.parse(subdirPatternResponse.text);
  assert.ok(subdirData.matches, 'Should have matches array');
  assert.ok(subdirData.matches.length >= 1, `Should find .txt files, got ${subdirData.matches.length} matches`);
  assert.ok(subdirData.matches.some(m => m.relative_path.includes('.txt')), 'Should find txt files');
  console.log(`✓ Found ${subdirData.matches.length} .txt files recursively`);

  console.log(`✓ ${testName} passed`);
}

// Test 8: Interactive Terminal with bash prompt
async function testInteractiveTerminal() {
  const testName = 'Test 8: Interactive Terminal';
  const testID = 'test-8-interactive-terminal';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-8`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName, testID);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Launch interactive terminal with bash
  const terminalExecuteResponse = await request('POST', `/workspaces/${workspaceId}/terminal-execute`, {
    headers: {
      Cookie: sessionCookie,
    },
    body: 'command=bash',
    testID,
  });

  assert.ok([302, 303].includes(terminalExecuteResponse.status), `Should redirect to terminal page (got ${terminalExecuteResponse.status})`);

  // Extract the redirect location
  const location = terminalExecuteResponse.headers['location'];
  assert.ok(location, 'Should have redirect location header');
  assert.ok(location.includes('/terminal'), 'Should redirect to terminal page');

  // Extract process ID from redirect URL
  const processMatch = location.match(/processes\/([^\/]+)\/terminal/);
  assert.ok(processMatch, 'Should have process ID in redirect URL');
  const processId = processMatch[1];

  // Load the terminal page
  const terminalPageResponse = await request('GET', location, {
    headers: { Cookie: sessionCookie },
    testID,
  });

  assert.equal(terminalPageResponse.status, 200, 'Should load terminal page');
  assert.ok(terminalPageResponse.text.includes('Interactive Terminal'), 'Page should have "Interactive Terminal" title');
  assert.ok(terminalPageResponse.text.includes('bash'), 'Page should show bash command');

  // Parse the page to find the WebSocket URL
  const terminalDoc = parseHTML(terminalPageResponse.text);
  const scriptContent = terminalPageResponse.text;
  const wsUrlMatch = scriptContent.match(/ws-terminal/);
  assert.ok(wsUrlMatch, 'Should have WebSocket terminal endpoint in script');

  // Test WebSocket connection using ws module
  const { WebSocket } = await import('ws');
  const protocol = 'ws:';
  const wsUrl = `${protocol}//${SERVER_URL.replace('http://', '').replace('https://', '')}/workspaces/${workspaceId}/processes/${processId}/ws-terminal`;

  console.log(`Connecting to WebSocket at ${wsUrl}`);

  const ws = new WebSocket(wsUrl, {
    headers: {
      'Cookie': sessionCookie,
    },
  });

  let promptReceived = false;
  let receivedData = '';

  // Wait for WebSocket to open
  await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      reject(new Error('WebSocket connection timeout'));
    }, 5000);

    ws.on('open', () => {
      clearTimeout(timeout);
      console.log('✓ WebSocket connected');

      // Send terminal size
      const sizeMsg = JSON.stringify({
        type: 'resize',
        cols: 80,
        rows: 24,
      });
      ws.send(sizeMsg);

      resolve();
    });

    ws.on('error', (err) => {
      clearTimeout(timeout);
      reject(err);
    });
  });

  // Listen for messages from the terminal
  const bashStartedPromise = new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      // Check if we received any data - if bash sent anything, it's running
      if (receivedData.length > 0) {
        // If we got data, bash is working
        console.log('✓ Received initial data from bash');

        // Check for bash completion errors
        if (receivedData.includes('bash: complete: command not found') ||
            receivedData.includes('bash: shopt: progcomp: invalid shell option name') ||
            receivedData.includes('bash: compgen: command not found')) {
          reject(new Error('Bash completion errors detected. Please configure bash to suppress these errors in your .bashrc'));
        }

        resolve();
      } else {
        reject(new Error(`No data received from bash within timeout.`));
      }
    }, 5000);

    ws.on('message', (data) => {
      const text = data.toString();
      receivedData += text;

      // Once we get any data, bash has started
      if (receivedData.length > 100) {
        clearTimeout(timeout);
        resolve();
      }
    });

    ws.on('close', () => {
      clearTimeout(timeout);
      reject(new Error('WebSocket closed before receiving bash output'));
    });

    ws.on('error', (err) => {
      clearTimeout(timeout);
      reject(err);
    });
  });

  await bashStartedPromise;
  console.log('✓ Bash started without completion errors');

  // Test sending input
  const inputMsg = JSON.stringify({
    type: 'input',
    data: 'echo "test-output"\n',
  });
  ws.send(inputMsg);

  // Wait for echo output
  const echoPromise = new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      reject(new Error('Echo output not received'));
    }, 5000);

    let echoReceived = false;
    ws.on('message', (data) => {
      const text = data.toString();
      if (text.includes('test-output') && !echoReceived) {
        echoReceived = true;
        clearTimeout(timeout);
        resolve();
      }
    });
  });

  await echoPromise;
  console.log('✓ Echo command output received');

  // Close WebSocket
  ws.close();

  console.log(`✓ ${testName} passed`);
}

// Test 9: Rerun command functionality
async function testRerunCommand() {
  const testName = 'Test 9: Rerun command';
  const testID = 'test-9-rerun-command';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-9`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName, testID);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Execute a unique command
  const uniqueMarker = `rerun-test-${Date.now()}`;
  const testCommand = `echo "${uniqueMarker}"`;

  const executeResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: `command=${encodeURIComponent(testCommand)}`,
    testID,
  });

  assert.equal(executeResponse.status, 200, 'Should execute first command');
  const firstProcessMatch = executeResponse.text.match(/processes\/([^\/]+)\/hx-output/);
  assert.ok(firstProcessMatch, 'Should have first process output link');
  const firstProcessId = firstProcessMatch[1];

  // Wait for the first process to complete
  await new Promise(resolve => setTimeout(resolve, 1000));

  // Verify process moved to finished
  let finishedProcessHtml = null;
  for (let i = 0; i < 10; i++) {
    await new Promise(resolve => setTimeout(resolve, 500));

    const finishedResponse = await request('GET', `/workspaces/${workspaceId}/hx-finished-processes?offset=0`, {
      headers: {
        Cookie: sessionCookie,
        'HX-Request': 'true',
      },
      testID,
    });

    if (finishedResponse.text.includes(firstProcessId) && finishedResponse.text.includes(uniqueMarker)) {
      finishedProcessHtml = finishedResponse.text;
      break;
    }
  }

  assert.ok(finishedProcessHtml, 'Process should appear in finished processes list');
  console.log('✓ First command completed and appears in finished processes');

  // Parse the finished processes HTML to find the rerun button
  const finishedDoc = parseHTML(finishedProcessHtml);
  const rerunForm = finishedDoc.querySelector(`form[hx-post*="hx-execute"]`);
  assert.ok(rerunForm, 'Should have rerun form in finished processes');

  const commandInput = rerunForm.querySelector('input[name="command"]');
  assert.ok(commandInput, 'Rerun form should have command input');
  assert.equal(commandInput.value, testCommand, 'Command input should contain the original command');

  const rerunButton = rerunForm.querySelector('button[type="submit"]');
  assert.ok(rerunButton, 'Should have rerun button');
  assert.ok(rerunButton.textContent.includes('Rerun'), 'Button should be labeled "Rerun"');

  // Verify button has proper accessibility attributes
  const titleAttr = rerunButton.getAttribute('title');
  assert.ok(titleAttr, 'Rerun button should have a title attribute for accessibility');
  assert.ok(titleAttr.includes('Rerun'), 'Title attribute should describe the rerun action');

  // Verify button has proper styling classes
  assert.ok(rerunButton.classList.contains('btn'), 'Rerun button should have btn class');
  assert.ok(rerunButton.classList.contains('btn-sm'), 'Rerun button should have btn-sm class');
  assert.ok(rerunButton.classList.contains('rerun-command-btn'), 'Rerun button should have rerun-command-btn class for easy identification');

  console.log('✓ Rerun button found with correct command and attributes');

  // Click the rerun button by submitting the form
  const hxTarget = rerunForm.getAttribute('hx-target');
  assert.ok(hxTarget, 'Rerun form should have hx-target attribute');

  const rerunResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: `command=${encodeURIComponent(testCommand)}`,
    testID,
  });

  assert.equal(rerunResponse.status, 200, 'Should execute rerun command');
  const secondProcessMatch = rerunResponse.text.match(/processes\/([^\/]+)\/hx-output/);
  assert.ok(secondProcessMatch, 'Should have second process output link');
  const secondProcessId = secondProcessMatch[1];

  // Verify we got a different process ID
  assert.notEqual(secondProcessId, firstProcessId, 'Rerun should create a new process instance');
  console.log('✓ Rerun created new process instance');

  // Wait for second process output
  let secondOutputFound = false;
  for (let i = 0; i < 10; i++) {
    await new Promise(resolve => setTimeout(resolve, 500));

    const outputResponse = await request('GET', `/workspaces/${workspaceId}/processes/${secondProcessId}/hx-output`, {
      headers: {
        Cookie: sessionCookie,
        'HX-Request': 'true',
      },
      testID,
    });

    if (outputResponse.text.includes(uniqueMarker)) {
      secondOutputFound = true;
      break;
    }
  }

  assert.ok(secondOutputFound, 'Rerun command should produce same output');
  console.log('✓ Rerun command executed successfully with same output');
}


// Test 10: File editor double save (issue #60)
async function testFileEditorDoubleSave() {
  const testName = 'Test 10: File editor double save';
  const testID = 'test-10-file-editor-double-save';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-10`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName, testID);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Create a test file
  const testFilePath = `/tmp/test-double-save-${Date.now()}.txt`;
  const createFileResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: `command=echo "initial content" > ${testFilePath}`,
    testID,
  });

  // Wait for file creation
  await new Promise(resolve => setTimeout(resolve, 500));

  // Read the file for editing
  const readResponse = await request('POST', `/workspaces/${workspaceId}/files/read`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: `file_path=${encodeURIComponent(testFilePath)}`,
    testID,
  });

  assert.equal(readResponse.status, 200, 'Should read file for editing');
  const readDoc = parseHTML(readResponse.text);
  const originalChecksumInput = readDoc.querySelector('#original_checksum');
  assert.ok(originalChecksumInput, 'Should have original_checksum hidden field');
  const originalChecksum = originalChecksumInput.value;
  console.log(`✓ File read with checksum: ${originalChecksum.substring(0, 8)}...`);

  // First save: modify the file
  const firstSaveResponse = await request('POST', `/workspaces/${workspaceId}/files/save`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: `file_path=${encodeURIComponent(testFilePath)}&content=${encodeURIComponent('first save content')}&original_checksum=${encodeURIComponent(originalChecksum)}`,
    testID,
  });

  assert.equal(firstSaveResponse.status, 200, 'First save should succeed');
  assert.ok(firstSaveResponse.text.includes('Success'), 'First save should show success message');

  // Extract the new checksum from the success response
  // The response should include the new checksum in an out-of-band swap element
  const firstSaveDoc = parseHTML(firstSaveResponse.text);
  const updatedChecksumInput = firstSaveDoc.querySelector('#original_checksum');
  assert.ok(updatedChecksumInput, 'Should have updated checksum in response');
  const newChecksum = updatedChecksumInput.value;
  console.log(`✓ First save succeeded, new checksum: ${newChecksum.substring(0, 8)}...`);

  // Second save: modify the file again WITHOUT reloading
  // Use the new checksum from the first save (simulating what htmx would do with the out-of-band swap)
  const secondSaveResponse = await request('POST', `/workspaces/${workspaceId}/files/save`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: `file_path=${encodeURIComponent(testFilePath)}&content=${encodeURIComponent('second save content')}&original_checksum=${encodeURIComponent(newChecksum)}`,
    testID,
  });

  assert.equal(secondSaveResponse.status, 200, 'Second save should return 200');

  // This is the bug: the second save should succeed, but currently it will show a conflict
  // After the fix, this should succeed
  if (secondSaveResponse.text.includes('Conflict Detected')) {
    console.log('✗ Bug reproduced: Second save shows false conflict');
        assert.fail('Second save should succeed but shows conflict (bug #60)');
  } else if (secondSaveResponse.text.includes('Success')) {
    console.log('✓ Second save succeeded (bug is fixed)');
  } else {
    assert.fail('Unexpected response from second save');
  }
  console.log(`✓ ${testName} passed`);
}

// Helper to extract logs for a specific test ID
async function extractTestLogs(testID) {
  if (!SERVER_LOG) {
    return [];
  }

  try {
    const fs = await import('fs');
    const content = await fs.promises.readFile(SERVER_LOG, 'utf8');
    const lines = content.split('\n');
    return lines.filter(line => line.includes(testID));
  } catch (error) {
    return [];
  }
}

// Wrapper to run test with log extraction on failure
async function runTestWithLogging(testFn, testName, testID) {
  try {
    await testFn();
  } catch (error) {
    console.error(`\n❌ ${testName} FAILED`);
    console.error(`Test ID: ${testID}`);
    console.error(`Error: ${error.message}`);

    // Extract and display relevant server logs
    const logs = await extractTestLogs(testID);
    if (logs.length > 0) {
      console.error(`\nRelevant server logs (${logs.length} lines):`);
      console.error('---');
      logs.forEach(log => console.error(log));
      console.error('---');
    }

    if (SERVER_LOG) {
      console.error(`\nTo see all logs for this test:`);
      console.error(`  grep '${testID}' ${SERVER_LOG}`);
    }

    throw error;
  }
}

// Main test runner
async function runTests() {
  try {
    console.log('Running tests in parallel...\n');

    const startTime = Date.now();

    // Run all tests in parallel
    await Promise.all([
      runTestWithLogging(testWorkspacesAndHTMX, 'Test 1: Workspaces and HTMX', 'test-1-workspaces-htmx'),
      runTestWithLogging(testCommandExecution, 'Test 2: Command execution', 'test-2-command-execution'),
      runTestWithLogging(testProcessTransitions, 'Test 3: Process transitions', 'test-3-process-transitions'),
      runTestWithLogging(testPerProcessPages, 'Test 4: Per-process pages', 'test-4-per-process-pages'),
      runTestWithLogging(testStdinInput, 'Test 5: Stdin input', 'test-5-stdin-input'),
      runTestWithLogging(testWorkspaceEditing, 'Test 6: Workspace editing', 'test-6-workspace-editing'),
      runTestWithLogging(testFileAutocomplete, 'Test 7: File autocomplete', 'test-7-file-autocomplete'),
      runTestWithLogging(testInteractiveTerminal, 'Test 8: Interactive Terminal', 'test-8-interactive-terminal'),
      runTestWithLogging(testRerunCommand, 'Test 9: Rerun command', 'test-9-rerun-command'),
      runTestWithLogging(testFileEditorDoubleSave, 'Test 10: File editor double save', 'test-10-file-editor-double-save'),
    ]);

    const duration = ((Date.now() - startTime) / 1000).toFixed(2);
    console.log(`\n✅ All tests passed in ${duration}s!`);
    process.exit(0);

  } catch (error) {
    console.error('\n❌ Test failed:', error.message);
    console.error(error.stack);
    process.exit(1);
  }
}

runTests();
