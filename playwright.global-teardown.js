// playwright.global-teardown.js
const fs = require('fs');
const { rmSync } = require('fs');

module.exports = async () => {
  try {
    const pid = fs.readFileSync('.playwright-server-pid', 'utf-8');
    process.kill(Number(pid), 'SIGTERM');
    fs.unlinkSync('.playwright-server-pid');
  } catch (e) {
    // Ignore if file does not exist or process already dead
  }

  try {
    const stateDir = fs.readFileSync('.playwright-state-dir', 'utf-8');
    rmSync(stateDir, { recursive: true, force: true });
    fs.unlinkSync('.playwright-state-dir');
  } catch (e) {
    // Ignore if file does not exist or directory already deleted
  }
};
