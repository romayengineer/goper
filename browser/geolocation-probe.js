// Detects whether Chromium shows the geolocation permission prompt. Used by
// the e2e geolocation test. Prints PERM_STATE and POPUP.
//
// When the browser is set to "ask", a geolocation request keeps the permission
// bubble open and the getCurrentPosition callback stays pending
// (POPUP:present). When blocked by default, the callback fires immediately
// with PERMISSION_DENIED and no popup is shown (POPUP:absent).
const { chromium } = require('playwright');

const TARGET =
  process.env.GEOLOCATION_TARGET_URL || 'https://www.where-am-i.co/';

(async () => {
  const browser = await chromium.connectOverCDP('http://127.0.0.1:9222');
  const context = browser.contexts()[0];

  const page = context.pages()[0] || (await context.newPage());
  await page.goto(TARGET, { waitUntil: 'domcontentloaded', timeout: 30000 });
  await page.waitForTimeout(3000);

  const permState = await page
    .evaluate(() => navigator.permissions.query({ name: 'geolocation' }).then((s) => s.state).catch(() => 'error'))
    .catch(() => 'error');

  await page
    .evaluate(() => {
      window.__geoDone = false;
      window.__geoErr = null;
      try {
        navigator.geolocation.getCurrentPosition(
          () => { window.__geoDone = true; },
          (e) => { window.__geoDone = true; window.__geoErr = e && e.code; },
        );
      } catch (e) {
        window.__geoDone = true;
        window.__geoErr = 'SYNC:' + e.message;
      }
    })
    .catch(() => {});

  await page.waitForTimeout(4000);

  const result = await page
    .evaluate(() => ({ done: window.__geoDone, err: window.__geoErr }))
    .catch(() => ({ done: true, err: null }));

  console.log(`PERM_STATE:${permState}`);
  console.log(`POPUP:${result.done === false ? 'present' : 'absent'}`);

  await browser.close();
})().catch((e) => {
  console.error('geolocation-probe failed:', e);
  process.exit(1);
});
