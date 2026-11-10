// Smoke: Chromium launched with the pw-config args + bp-console sending what a
// page logs to a local sink.
import http from 'node:http';
import { spawn } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';

const require = createRequire('/usr/lib/node_modules/@playwright/mcp/');
const { chromium } = require('playwright-core');
const cfg = JSON.parse(readFileSync('/opt/bp/pw-config.json', 'utf8'));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const fail = (m) => { console.error('FAIL ' + m); process.exit(1); };

const PAGE = `<script>
console.log('quiet');
console.error('boom', 42);
console.warn('careful');
console.error('at ' + location.href + ' and https://x.test/a?code=secret-token#f');
fetch('/missing');
setTimeout(() => { throw new Error('kaput'); }, 10);
</script>`;
const got = [];
const sink = http.createServer((req, res) => {
  if (req.url.startsWith('/page')) { res.writeHead(200, { 'Content-Type': 'text/html' }); res.end(PAGE); return; }
  if (req.url === '/missing') { res.writeHead(404); res.end(); return; }
  const chunks = [];
  req.on('data', (c) => chunks.push(c));
  req.on('end', () => {
    if (req.method !== 'PUT' || req.url !== '/console/tok') fail('unexpected ' + req.method + ' ' + req.url);
    got.push(...JSON.parse(Buffer.concat(chunks).toString()));
    res.writeHead(204);
    res.end();
  });
});
await new Promise((r) => sink.listen(0, '127.0.0.1', r));
const base = `http://127.0.0.1:${sink.address().port}`;

const browser = await chromium.launch({ args: cfg.browser.launchOptions.args });
const page = await (await browser.newContext()).newPage();
await page.goto('data:text/html,<h1>start</h1>');
const rec = spawn('node', ['/opt/bp/bp-console.mjs', `--console-url=${base}/console/tok`], { stdio: ['ignore', 'inherit', 'inherit'] });
await sleep(3000); // poll 2 s + attach
await page.goto(`${base}/page?reset=secret-token`);
await sleep(5000); // flush every 2 s
rec.kill();
await browser.close();
sink.close();

const has = (pred, what) => { if (!got.some(pred)) fail(`missing ${what}: ${JSON.stringify(got)}`); };
has((e) => e.kind === 'console' && e.level === 'error' && e.text === 'boom 42', 'console.error');
has((e) => e.kind === 'console' && e.level === 'warning' && e.text === 'careful', 'console.warn');
has((e) => e.kind === 'exception' && e.text.includes('kaput'), 'uncaught exception');
has((e) => e.kind === 'network' && e.text === `GET 404 ${base}/missing`, '404 response');
if (got.some((e) => e.text.includes('quiet'))) fail('console.log recorded');
if (JSON.stringify(got).includes('secret-token')) fail('query string leaked: ' + JSON.stringify(got));
has((e) => e.kind === 'console' && e.text === `at ${base}/page and https://x.test/a`, 'URLs in text without their queries');
if (got.some((e) => !e.at || Number.isNaN(Date.parse(e.at)))) fail('bad timestamps');
console.log(`ok ${got.length} console events`);
process.exit(0);
