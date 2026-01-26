// playwright.global-setup.js
const { spawn } = require('child_process');
const net = require('net');
const fs = require('fs');

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
  const serverProcess = spawn('go', [
    'run', './cmd/mobileshell/main.go', 'run', '--port', String(port)
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
  process.env.SERVER_URL = `http://localhost:${port}`;
  config.projects.forEach(p => p.use.baseURL = process.env.SERVER_URL);
};
