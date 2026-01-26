// scripts/show-playwright-report.js
const path = require('path');
const fs = require('fs');

const reportDir = path.resolve(__dirname, '../playwright-report');
const indexHtml = path.join(reportDir, 'index.html');

if (fs.existsSync(indexHtml)) {
  console.log('Playwright HTML report generated:');
  console.log('file://' + indexHtml);
} else {
  console.error('Playwright HTML report not found:', indexHtml);
  process.exit(1);
}
