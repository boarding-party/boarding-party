// Makes trace.zip, the fixture for the trace summary tests: a page with an
// iframe, one request that 404s and one that works, a click and a navigation.
// Run: node make-trace.mjs <path to a playwright-core package.json> [chrome path]
import http from 'node:http';
import { createRequire } from 'node:module';
const require = createRequire(process.argv[2]);
const { chromium } = require('playwright-core');
const exe = process.argv[3] || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';

const pages = {
  '/': `<!doctype html><title>Shop</title><h1>Shop</h1>
    <iframe src="/frame" title="Pay" style="width:300px;height:80px"></iframe>
    <button onclick="fetch('/api/missing?token=secret').then(()=>fetch('/api/ok')).then(()=>document.body.append('done'))">Load</button>
    <a href="/next">Next</a>`,
  '/frame': '<!doctype html><button>Pay now</button>',
  '/next': '<!doctype html><title>Next</title><p>Next page</p>',
  '/api/ok': '{"ok":true}',
};
const srv = http.createServer((q, r) => {
  const path = q.url.split('?')[0];
  if (!(path in pages)) { r.writeHead(404).end('nope'); return; }
  r.writeHead(200, { 'Content-Type': path.startsWith('/api') ? 'application/json' : 'text/html' }).end(pages[path]);
}).listen(0);
await new Promise((r) => srv.on('listening', r));
const base = `http://127.0.0.1:${srv.address().port}`;

const b = await chromium.launch({ executablePath: exe, headless: true });
const ctx = await b.newContext({ viewport: { width: 640, height: 400 } });
await ctx.tracing.start({ screenshots: false, snapshots: true });
const page = await ctx.newPage();
await page.goto(base + '/');
await page.frameLocator('iframe').getByRole('button', { name: 'Pay now' }).click();
await page.getByRole('button', { name: 'Load' }).click();
await page.getByText('done').waitFor();
await page.getByRole('link', { name: 'Next' }).click();
await page.getByText('Next page').waitFor();
await ctx.tracing.stop({ path: new URL('./trace.zip', import.meta.url).pathname });
await b.close();
srv.close();
