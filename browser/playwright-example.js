// Example: drive the visible (windowed) Chrome that is already running in
// this container, proxied through goper. Run with:
//   docker compose exec chrome node /app/playwright-example.js
const { chromium } = require('playwright');

(async () => {
  const browser = await chromium.connectOverCDP('http://127.0.0.1:9222');
  const context = browser.contexts()[0] || (await browser.newContext());
  const page = context.pages()[0] || (await context.newPage());

  await page.goto('https://httpbin.org/anything', {
    waitUntil: 'domcontentloaded',
    timeout: 30000,
  });

  const text = await page.evaluate(() => document.body.innerText);
  console.log('Response body:', text.slice(0, 500));

  await browser.close();
})().catch((err) => {
  console.error('playwright-example failed:', err);
  process.exit(1);
});
