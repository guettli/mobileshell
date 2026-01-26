// playwright.global-setup.js
const { spawn, spawnSync } = require('child_process');
const net = require('net');
const fs = require('fs');
const path = require('path');
const os = require('os');

function getFreePort() {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.listen(0, () => {
      const port = srv.address().port;
      srv.close(() => resolve(port));
    });
    srv.on('error', reject);
  });
}

module.exports = async (config) => {
  const port = await getFreePort();

  // Create a temporary state directory for the test
  const stateDir = fs.mkdtempSync(path.join(os.tmpdir(), 'mobileshell-test-'));

  // Add the test password using echo and pipe
  const password = process.env.PASSWORD || 'test-password-123456789012345678901234567890';
  const addPasswordResult = spawnSync('sh', [
    '-c',
    `echo -n "${password}" | go run ./cmd/mobileshell/main.go add-password --state-dir "${stateDir}" --from-stdin`
  ], {
    encoding: 'utf-8',
    stdio: ['pipe', 'inherit', 'pipe']
  });

  if (addPasswordResult.status !== 0) {
    console.error('Failed to add password:', addPasswordResult.stderr);
    throw new Error('Failed to add test password');
  }

  const serverProcess = spawn('go', [
    'run', './cmd/mobileshell/main.go', 'run',
    '--port', String(port),
    '--state-dir', stateDir
  ], {
    stdio: 'inherit',
    env: { ...process.env, PORT: String(port) },
  });

  // Wait for server to be ready
  await new Promise((resolve, reject) => {
    const maxWait = 10000;
    const start = Date.now();
    (function check() {
      const sock = net.connect(port, '127.0.0.1', () => {
        sock.end();
        resolve();
      });
      sock.on('error', () => {
        if (Date.now() - start > maxWait) reject(new Error('Server did not start in time'));
        else setTimeout(check, 200);
      });
    })();
  });

  // Write server info for teardown
  fs.writeFileSync('.playwright-server-pid', String(serverProcess.pid));
  fs.writeFileSync('.playwright-state-dir', stateDir);
  process.env.SERVER_URL = `http://localhost:${port}`;
  config.projects.forEach(p => p.use.baseURL = process.env.SERVER_URL);
};
