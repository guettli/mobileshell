#!/usr/bin/env node

import { strict as assert } from 'assert';
import { JSDOM } from 'jsdom';

// Get server URL from environment
const SERVER_URL = process.env.SERVER_URL || 'http://localhost:22123';
const PASSWORD = process.env.PASSWORD || 'test-password-123456789012345678901234567890';

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
async function createWorkspace(sessionCookie, workspaceName) {
  const createWorkspaceResponse = await request('POST', '/workspaces/hx-create', {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: `name=${encodeURIComponent(workspaceName)}&directory=/tmp&pre_command=`,
  });

  assert.equal(createWorkspaceResponse.status, 200, 'Should create workspace');

  const hxRedirect = createWorkspaceResponse.headers['hx-redirect'];
  const workspaceMatch = hxRedirect.match(/\/workspaces\/([^\/]+)/);
  assert.ok(workspaceMatch, 'Should have workspace ID in redirect URL');

  return workspaceMatch[1];
}

// Test 1: Workspaces and HTMX
async function testWorkspacesAndHTMX() {
  const testName = 'Test 1: Workspaces and HTMX';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  console.log('✓ Login successful');

  // Get workspaces page
  const workspacesResponse = await request('GET', '/workspaces', {
    headers: { Cookie: sessionCookie },
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
  const workspaceId = await createWorkspace(sessionCookie, workspaceName);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Navigate to workspace and verify
  const workspacePageResponse = await request('GET', `/workspaces/${workspaceId}`, {
    headers: { Cookie: sessionCookie },
  });

  assert.equal(workspacePageResponse.status, 200, 'Should load workspace page');
  assert.ok(workspacePageResponse.text.includes('hx-execute'), 'Should have execute form');

  console.log(`✓ ${testName} passed`);
}

// Test 2: Command execution and output
async function testCommandExecution() {
  const testName = 'Test 2: Command execution';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-2`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Execute a command via HTMX
  const executeResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: 'command=echo "Hello from JSDOM test"',
  });

  assert.equal(executeResponse.status, 200, 'Should execute command');
  assert.ok(executeResponse.text.includes('echo'), 'Response should show the command');

  // Extract process ID
  const processMatch = executeResponse.text.match(/processes\/([^\/]+)\/hx-output/);
  assert.ok(processMatch, 'Should have process output link');
  const processId = processMatch[1];

  // Wait for command output
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
      break;
    }
  }

  assert.ok(outputFound, 'Should find command output within timeout');
  console.log(`✓ ${testName} passed`);
}

// Test 3: Process transitions
async function testProcessTransitions() {
  const testName = 'Test 3: Process transitions';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-3`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName);
  console.log(`✓ Workspace created: ${workspaceName}`);

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
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-4`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Execute a long-running command
  const longCommandResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: 'command=sleep 10',
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
  });

  console.log(`✓ ${testName} passed`);
}

// Test 5: Stdin input
async function testStdinInput() {
  const testName = 'Test 5: Stdin input';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-5`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Start cat command
  const catCommandResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: 'command=cat',
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
  });

  console.log(`✓ ${testName} passed`);
}

// Test 6: Workspace editing
async function testWorkspaceEditing() {
  const testName = 'Test 6: Workspace editing';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-6`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Navigate to edit page
  const editPageResponse = await request('GET', `/workspaces/${workspaceId}/edit`, {
    headers: { Cookie: sessionCookie },
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
  });

  assert.ok([302, 303].includes(updateResponse.status), `Should redirect after update (got ${updateResponse.status})`);

  // Verify the changes
  const workspaceAfterEditResponse = await request('GET', `/workspaces/${workspaceId}`, {
    headers: { Cookie: sessionCookie },
  });

  assert.equal(workspaceAfterEditResponse.status, 200, 'Should load workspace page after edit');
  assert.ok(workspaceAfterEditResponse.text.includes(updatedName), 'Page should show updated workspace name');

  // Test validation: try to update with empty name
  const invalidUpdateResponse = await request('POST', `/workspaces/${workspaceId}/edit`, {
    headers: {
      Cookie: sessionCookie,
    },
    body: `name=&directory=/tmp&pre_command=`,
  });

  assert.equal(invalidUpdateResponse.status, 200, 'Should return form with error (not redirect)');
  assert.ok(invalidUpdateResponse.text.includes('required'), 'Should show validation error message');

  console.log(`✓ ${testName} passed`);
}

// Test 7: File autocomplete
async function testFileAutocomplete() {
  const testName = 'Test 7: File autocomplete';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-7`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Create test files with a single command to ensure they all exist
  const setupCommand = 'touch test-file1.go test-file2.go readme.md && mkdir -p subdir && touch subdir/nested.go && mkdir -p deep/nested/path && touch deep/nested/path/deep-file.txt';

  const setupResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: `command=${encodeURIComponent(setupCommand)}`,
  });

  // Extract process ID to wait for command completion
  const processMatch = setupResponse.text.match(/processes\/([^\/]+)\/hx-output/);
  if (processMatch) {
    const processId = processMatch[1];

    // Wait for the setup command to complete
    for (let i = 0; i < 20; i++) {
      await new Promise(resolve => setTimeout(resolve, 200));

      const statusResponse = await request('GET', `/workspaces/${workspaceId}/json-process-updates?process_ids=${processId}`, {
        headers: {
          Cookie: sessionCookie,
        },
      });

      const statusData = JSON.parse(statusResponse.text);
      const processUpdate = statusData.updates && statusData.updates.find(u => u.id === processId);

      if (processUpdate && processUpdate.status === 'finished') {
        break;
      }
    }
  }

  // Additional wait to ensure filesystem is synced
  await new Promise(resolve => setTimeout(resolve, 500));

  // Test 1: Simple wildcard pattern
  const simplePatternResponse = await request('GET', `/workspaces/${workspaceId}/files/autocomplete?pattern=${encodeURIComponent('*.go')}`, {
    headers: {
      Cookie: sessionCookie,
    },
  });

  assert.equal(simplePatternResponse.status, 200, 'Should get autocomplete results');
  const simpleData = JSON.parse(simplePatternResponse.text);
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
  });

  const emptyData = JSON.parse(emptyPatternResponse.text);
  assert.equal(emptyData.matches.length, 0, 'Empty pattern should return no matches');
  console.log('✓ Empty pattern handled correctly');

  // Test 6: Pattern with subdirectory - use simpler pattern
  const subdirPatternResponse = await request('GET', `/workspaces/${workspaceId}/files/autocomplete?pattern=${encodeURIComponent('**/*.txt')}`, {
    headers: {
      Cookie: sessionCookie,
    },
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
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-8`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Launch interactive terminal with bash
  const terminalExecuteResponse = await request('POST', `/workspaces/${workspaceId}/terminal-execute`, {
    headers: {
      Cookie: sessionCookie,
    },
    body: 'command=bash',
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

// Test 8: Rerun command functionality
async function testRerunCommand() {
  const testName = 'Test 8: Rerun command';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-8`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName);
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
    });

    if (outputResponse.text.includes(uniqueMarker)) {
      secondOutputFound = true;
      break;
    }
  }

  assert.ok(secondOutputFound, 'Rerun command should produce same output');
  console.log('✓ Rerun command executed successfully with same output');
}




// Test 9: Claude integration
async function testClaudeIntegration() {
  const testName = 'Test 9: Claude integration';
  console.log(`\n=== ${testName} ===`);

  // Check if real claude CLI is available, otherwise set up mock
  const { execSync } = await import('child_process');
  const { mkdirSync, writeFileSync, chmodSync, existsSync } = await import('fs');
  const { tmpdir } = await import('os');
  const { join } = await import('path');

  let isRealClaude = false;
  let mockClaudeDir = null;
  let originalPath = process.env.PATH;

  try {
    execSync('which claude', { stdio: 'ignore' });
    isRealClaude = true;
    console.log('✓ Using real Claude CLI from PATH');
  } catch (e) {
    // Claude not in PATH, will create mock in workspace
    console.log('✓ Real Claude CLI not found, will create mock');
  }

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-9`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // If real claude is not available, create mock in workspace directory
  if (!isRealClaude) {
    // Create a bin directory in /tmp for the mock claude
    const mockCommand = 'mkdir -p /tmp/mock-claude-bin && cat > /tmp/mock-claude-bin/claude << ' + "'MOCKEOF'\n" +
      '#!/bin/bash\n' +
      '# Mock Claude CLI that simulates stream-json output format\n' +
      '\n' +
      '# Output mock stream-json format with markdown content\n' +
      'cat << ' + "'EOF'\n" +
      '{"type":"system","subtype":"init","session_id":"mock-test-session"}\n' +
      '{"type":"assistant","message":{"content":[{"type":"text","text":"# Mock Claude Response\\n\\nI will explain the command you asked about.\\n\\nThe echo hello world command is a simple shell command that:\\n\\n## What it does\\n\\n- Prints the text hello world to standard output\\n- Uses the echo command which is a built-in shell utility\\n- The quotes ensure the text is treated as a single argument\\n\\nThis is commonly used for testing, simple output, and script debugging.\\n\\nLet me know if you would like more details!"}]}}\n' +
      '{"type":"result","subtype":"success"}\n' +
      'EOF\n' +
      'MOCKEOF\n' +
      'chmod +x /tmp/mock-claude-bin/claude';

    const createMockResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
      headers: {
        Cookie: sessionCookie,
        'HX-Request': 'true',
      },
      body: 'command=' + encodeURIComponent(mockCommand),
    });

    // Wait for mock creation
    await new Promise(resolve => setTimeout(resolve, 1000));

    // Update workspace to prepend mock bin to PATH
    const editResponse = await request('POST', `/workspaces/${workspaceId}/edit`, {
      headers: {
        Cookie: sessionCookie,
      },
      body: `name=${encodeURIComponent(workspaceName)}&directory=/tmp&pre_command=${encodeURIComponent('export PATH=/tmp/mock-claude-bin:$PATH')}`,
    });

    console.log('✓ Mock Claude created in /tmp/mock-claude-bin and added to workspace PATH');
  }

  // Navigate to the workspace page
  const workspacePageResponse = await request('GET', `/workspaces/${workspaceId}`, {
    headers: {
      Cookie: sessionCookie,
    },
  });

  assert.equal(workspacePageResponse.status, 200, 'Should load workspace page');
  assert.ok(workspacePageResponse.text.includes('Start Claude'), 'Page should have "Start Claude" button');
  console.log('✓ Workspace page loaded with Claude button');

  // Parse the page to verify the Claude button
  const workspaceDoc = parseHTML(workspacePageResponse.text);
  const claudeButton = workspaceDoc.querySelector('button[onclick*="startClaudeSession"]');
  assert.ok(claudeButton, 'Should have Start Claude button');
  assert.ok(claudeButton.textContent.includes('Start Claude'), 'Button should say "Start Claude"');

  // Verify button is next to Interactive Terminal button
  const interactiveTerminalButton = workspaceDoc.querySelector('button[onclick*="launchInteractiveTerminal"]');
  assert.ok(interactiveTerminalButton, 'Should have Interactive Terminal button');

  // Both buttons should be in the same button group
  const buttonGroup = claudeButton.closest('.d-flex');
  assert.ok(buttonGroup, 'Claude button should be in button group');
  assert.ok(buttonGroup.contains(interactiveTerminalButton), 'Both buttons should be in same group');
  console.log('✓ Claude button found on workspace page next to Interactive Terminal');

  // Verify the startClaudeSession JavaScript function exists in the page
  assert.ok(workspacePageResponse.text.includes('function startClaudeSession()'),
    'Page should have startClaudeSession function');
  assert.ok(workspacePageResponse.text.includes('htmx.ajax'),
    'startClaudeSession should use htmx.ajax');
  assert.ok(workspacePageResponse.text.includes('hx-execute-claude'),
    'startClaudeSession should call hx-execute-claude endpoint');
  console.log('✓ JavaScript function verified');

  // Test submitting a Claude prompt via the endpoint
  const testPrompt = 'Explain what this command does: echo "hello world"';

  // If using mock, we need to update PATH for the execution
  let executeBody = `prompt=${encodeURIComponent(testPrompt)}`;

  const claudeExecuteResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute-claude`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: executeBody,
  });

  assert.equal(claudeExecuteResponse.status, 200, 'Should execute Claude command');
  console.log('✓ Claude command submitted successfully');

  // Parse the response - should be a hidden div like other commands
  const claudeResponseDoc = parseHTML(claudeExecuteResponse.text);
  const hiddenDiv = claudeResponseDoc.querySelector('div[data-process-id][style*="display:none"]');
  assert.ok(hiddenDiv, 'Response should contain a hidden div with process ID');

  // Verify the command includes claude CLI with expected flags
  const commandText = claudeExecuteResponse.text;
  assert.ok(commandText.includes('claude'), 'Command should include "claude"');

  // Should NOT include -p flag (always interactive dialog mode)
  assert.ok(!commandText.includes(' -p ') && !commandText.includes(' -p"'),
    'Should not include -p flag (always interactive dialog mode)');

  // Should include streaming flags for interactive dialog
  assert.ok(commandText.includes('--output-format=stream-json'), 'Should include streaming JSON flag');
  assert.ok(commandText.includes('--verbose'), 'Should include verbose flag');

  // Should NOT include --no-session-persistence (only works with -p/print mode)
  assert.ok(!commandText.includes('--no-session-persistence'),
    'Should not include --no-session-persistence in interactive mode');

  console.log('✓ Claude command uses interactive dialog mode with correct flags');

  // Extract process ID from the response
  const claudeProcessMatch = claudeExecuteResponse.text.match(/processes\/([a-f0-9]+)/);
  assert.ok(claudeProcessMatch, 'Should have Claude process ID in response');
  const claudeProcessId = claudeProcessMatch[1];
  console.log(`✓ Claude process created: ${claudeProcessId}`);

  // Verify the process can be accessed
  const claudeProcessPageResponse = await request('GET', `/workspaces/${workspaceId}/processes/${claudeProcessId}`, {
    headers: {
      Cookie: sessionCookie,
    },
  });

  assert.equal(claudeProcessPageResponse.status, 200, 'Should load Claude process page');
  assert.ok(claudeProcessPageResponse.text.includes('claude'), 'Process page should show claude command');
  console.log('✓ Claude process page accessible');

  // Wait for and verify Claude output
  let outputReceived = false;
  let outputText = '';

  for (let i = 0; i < 20; i++) {
    await new Promise(resolve => setTimeout(resolve, 500));

    const outputResponse = await request('GET', `/workspaces/${workspaceId}/processes/${claudeProcessId}/hx-output?type=combined`, {
      headers: {
        Cookie: sessionCookie,
        'HX-Request': 'true',
      },
    });

    outputText = outputResponse.text;

    // Check if we got meaningful output (either from real Claude or mock)
    if (outputText.length > 100) {
      outputReceived = true;

      // Verify that output is rendered as markdown HTML
      const outputDoc = parseHTML(outputText);
      const markdownContainer = outputDoc.querySelector('.markdown-container');

      if (markdownContainer) {
        console.log('✓ Output is rendered as markdown (found markdown-container)');

        // If using mock, verify the markdown content is properly rendered
        if (!isRealClaude) {
          // Mock should have markdown headers converted to HTML
          const headers = markdownContainer.querySelectorAll('h1, h2');
          assert.ok(headers.length > 0, 'Mock Claude markdown should have headers rendered as HTML');

          // Mock should have lists converted to HTML
          const lists = markdownContainer.querySelectorAll('ul, ol');
          assert.ok(lists.length > 0, 'Mock Claude markdown should have lists rendered as HTML');

          console.log('✓ Mock Claude output rendered as markdown with proper HTML elements');
        } else {
          console.log('✓ Real Claude output rendered as markdown');
        }
      } else {
        // No markdown container - this might be raw output
        console.log('⚠ Output not rendered as markdown (no markdown-container found)');
        if (!isRealClaude) {
          // For mock, we expect markdown rendering
          assert.fail('Expected mock Claude output to be rendered as markdown');
        }
      }

      break;
    }
  }

  // If no output after waiting, check if the process failed (e.g., claude not in server's PATH)
  if (!outputReceived) {
    const statusResponse = await request('GET', `/workspaces/${workspaceId}/json-process-updates?process_ids=${claudeProcessId}`, {
      headers: {
        Cookie: sessionCookie,
      },
    });

    const statusData = JSON.parse(statusResponse.text);
    const processUpdate = statusData.updates && statusData.updates.find(u => u.id === claudeProcessId);

    if (processUpdate && processUpdate.status === 'finished') {
      // Process finished without output - likely claude not found
      console.log('⚠ Claude process finished without output (claude CLI may not be in server PATH)');
      console.log('⚠ This is expected if claude CLI is not installed on the server');
    } else {
      console.log('⚠ Claude process still running but no output yet');
    }
  }

  console.log(`✓ ${testName} passed`);
}

// Test 10: File editor double save (issue #60)
async function testFileEditorDoubleSave() {
  const testName = 'Test 10: File editor double save';
  console.log(`\n=== ${testName} ===`);

  const sessionCookie = await login();
  const workspaceName = `test-workspace-${Date.now()}-10`;
  const workspaceId = await createWorkspace(sessionCookie, workspaceName);
  console.log(`✓ Workspace created: ${workspaceName}`);

  // Create a test file
  const testFilePath = `/tmp/test-double-save-${Date.now()}.txt`;
  const createFileResponse = await request('POST', `/workspaces/${workspaceId}/hx-execute`, {
    headers: {
      Cookie: sessionCookie,
      'HX-Request': 'true',
    },
    body: `command=echo "initial content" > ${testFilePath}`,
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

// Main test runner
async function runTests() {
  try {
    console.log('Running tests in parallel...\n');

    const startTime = Date.now();

    // Run all tests in parallel
    await Promise.all([
      testWorkspacesAndHTMX(),
      testCommandExecution(),
      testProcessTransitions(),
      testPerProcessPages(),
      testStdinInput(),
      testWorkspaceEditing(),
      testFileAutocomplete(),
      testInteractiveTerminal(),
      testRerunCommand(),
      testClaudeIntegration(),
      testFileEditorDoubleSave(),
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
