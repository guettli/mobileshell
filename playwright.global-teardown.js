// playwright.global-teardown.js
const fs = require('fs');

module.exports = async () => {
  try {
    const pid = fs.readFileSync('.playwright-server-pid', 'utf-8');
    process.kill(Number(pid), 'SIGTERM');
    fs.unlinkSync('.playwright-server-pid');
  } catch (e) {
    // Ignore if file does not exist or process already dead
  }
};
