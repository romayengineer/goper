// Benchmark Chrome text-rendering responsiveness over CDP.
//
// Typing: real per-keystroke processing through Chrome's input pipeline
// (keyboard.type sends keyDown/char/keyUp per character).
//
// NOTE: do not use page.screenshot() as a latency proxy here. Under
// --disable-gpu software compositing, captureScreenshot blocks ~2.6s waiting
// for a BeginFrame that is never produced on demand, while CPU sits idle —
// it measures a stall artifact, not interaction latency.
const { chromium } = require('playwright');

(async () => {
  const browser = await chromium.connectOverCDP('http://127.0.0.1:9222');
  const ctx = browser.contexts()[0];
  const page = ctx.pages()[0] || await ctx.newPage();

  const text =
    'the quick brown fox jumps over the lazy dog 0123456789 ABCDEFGHIJKLMNOPQRSTUVWXYZ ' +
    'the quick brown fox jumps over the lazy dog 0123456789 ABCDEFGHIJKLMNOPQRSTUVWXYZ ';
  const html =
    '<html><head><style>body{font-family:sans-serif;font-size:16px}</style></head>' +
    '<body><input id="i" style="width:420px;font-size:16px">' +
    '<p>' + text.repeat(12) + '</p></body></html>';

  await page.goto('data:text/html,' + encodeURIComponent(html));
  await page.focus('#i');
  await page.keyboard.type('warmup');

  const phrase = 'the quick brown fox jumps over the lazy dog 1234567890';
  const t0 = Date.now();
  await page.keyboard.type(phrase);
  const t1 = Date.now();
  console.log('TYPING_' + phrase.length + '_MS ' + (t1 - t0));
  console.log('PER_KEY_MS ' + ((t1 - t0) / phrase.length).toFixed(2));

  await browser.close();
})().catch(e => { console.error('BENCH_FAIL', e.message); process.exit(1); });
