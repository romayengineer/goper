// Detects whether Chromium offers to translate a page. Used by the e2e
// translate test. Prints "OFFER:present" or "OFFER:absent".
//
// The translate bubble is browser chrome (not reachable via page DOM/AX), but
// chrome://translate-internals records an ignored/denied count per language
// whenever an offer was shown and dismissed. A fresh profile visiting a
// Spanish page produces a non-zero "es" count.
const { chromium } = require('playwright');

const TARGET =
  process.env.TRANSLATE_TARGET_URL || 'https://es.wikipedia.org/wiki/Wikipedia:Portada';

(async () => {
  const browser = await chromium.connectOverCDP('http://127.0.0.1:9222');
  const context = browser.contexts()[0];

  // 1. Visit the foreign-language page and let language detection run.
  const page = context.pages()[0] || (await context.newPage());
  await page.goto(TARGET, { waitUntil: 'domcontentloaded', timeout: 30000 });
  await page.waitForTimeout(10000);

  // 2. Navigate away to dismiss the translate offer (counted as "ignored").
  await page.goto('about:blank');
  await page.waitForTimeout(1000);

  // 3. Read the translate prefs from chrome://translate-internals.
  const prefs = await context.newPage();
  await prefs.goto('chrome://translate-internals/', { timeout: 5000 });
  await prefs.waitForTimeout(1500);
  const dump = await prefs.evaluate(() => {
    const text = (document.body && document.body.innerText) || '';
    const s = text.split('Dump')[1] || '';
    const end = s.split('Override')[0] || s;
    return end.trim();
  });
  await prefs.close();

  // 4. A non-zero ignored/denied count for "es" means an offer was shown.
  const esCount = (dump.match(/"es":\s*(\d+)/) || [])[1];
  const offered = esCount !== undefined && Number(esCount) > 0;
  console.log(`OFFER:${offered ? 'present' : 'absent'}`);
  console.log(`es count: ${esCount === undefined ? '0' : esCount}`);

  await browser.close();
})().catch((e) => {
  console.error('translate-probe failed:', e);
  process.exit(1);
});
