// Smoke: Chromium launched with the pw-config args + bp-screen streaming to a local sink.
import http from 'node:http';
import { spawn, spawnSync } from 'node:child_process';
import { readFileSync, readdirSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createRequire } from 'node:module';

const require = createRequire('/usr/lib/node_modules/@playwright/mcp/');
const { chromium, devices } = require('playwright-core');
const cfg = JSON.parse(readFileSync('/opt/bp/pw-config.json', 'utf8'));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const fail = (m) => { console.error('FAIL ' + m); process.exit(1); };

const frames = [];
const sink = http.createServer((req, res) => {
  const chunks = [];
  req.on('data', (c) => chunks.push(c));
  req.on('end', () => {
    frames.push({ at: Date.now(), body: Buffer.concat(chunks), method: req.method, ct: req.headers['content-type'] });
    res.writeHead(204, { 'X-Bp-Watchers': '1' });
    res.end();
  });
});
await new Promise((r) => sink.listen(0, '127.0.0.1', r));
const url = `http://127.0.0.1:${sink.address().port}/live/run-token`;

const dir = mkdtempSync(join(tmpdir(), 'bp-smoke-'));
const iphone = devices['iPhone 15'];
const browser = await chromium.launch({ args: cfg.browser.launchOptions.args });
const ctx = await browser.newContext({ ...iphone, recordVideo: { dir } });
const p1 = await ctx.newPage();
await p1.goto('data:text/html,<body style="background:red"><h1>one</h1>');

const screen = spawn('node', ['/opt/bp/bp-screen.mjs', `--live-url=${url}`], { stdio: ['ignore', 'inherit', 'pipe'] });
let screenLog = '';
screen.stderr.on('data', (d) => { screenLog += d; process.stderr.write(d); });
await sleep(3000); // poll interval 2s + attach
const p2 = await ctx.newPage();
await p2.goto('data:text/html,<body style="background:blue"><h1>two</h1>');
const secondOpened = Date.now();
await sleep(4000);

screen.kill();
await ctx.close();
await browser.close();
sink.close();

if (frames.length < 2) fail(`only ${frames.length} frames`);
for (const f of frames) {
  if (f.method !== 'PUT' || f.ct !== 'image/jpeg') fail('bad method/content-type');
  if (f.body[0] !== 0xff || f.body[1] !== 0xd8) fail('frame is not a JPEG');
}
const before = frames.filter((f) => f.at <= secondOpened);
const after = frames.filter((f) => f.at > secondOpened);
if (!after.length) fail('no frame after the second page opened');
if (!before.length) fail('no frame before the second page opened');
if (!after.some((f) => before.every((b) => !b.body.equals(f.body)))) fail('frames after page two equal earlier ones (no re-attach)');
const ids = new Set([...screenLog.matchAll(/attached to target (\w+)/g)].map((m) => m[1]));
if (ids.size < 2) fail(`bp-screen attached to ${ids.size} distinct targets, want 2`);

// Video size: context viewport must match Playwright's iPhone 15 viewport and the recording must be proportional to it.
const want = iphone.viewport;
const vp = ctx._options?.viewport || iphone.viewport;
if (vp.width !== want.width || vp.height !== want.height) fail(`viewport ${vp.width}x${vp.height}, want ${want.width}x${want.height}`);
const webm = readdirSync(dir).find((f) => f.endsWith('.webm'));
let videoNote = `viewport ${want.width}x${want.height} (no ffmpeg)`;
const ffdir = readdirSync('/ms-playwright').find((d) => d.startsWith('ffmpeg-'));
if (webm && ffdir) {
  const bin = readdirSync(join('/ms-playwright', ffdir)).find((f) => f.startsWith('ffmpeg'));
  const r = spawnSync(join('/ms-playwright', ffdir, bin), ['-i', join(dir, webm)], { encoding: 'utf8' });
  const m = /Video:.*?, (\d+)x(\d+)/.exec(r.stderr || '');
  if (!m) fail('cannot read video size');
  const [w, h] = [+m[1], +m[2]];
  if (Math.abs(w / h - want.width / want.height) > 0.01) fail(`video ${w}x${h} not ${want.width}x${want.height}-proportional`);
  videoNote = `video ${w}x${h}`;
}
console.log(`ok ${frames.length} frames; ${videoNote}`);

// ---- Case 2: the real wrapper end to end over stdio JSON-RPC -------------------------
// bp-browser starts bp-screen and execs Playwright MCP; stdout must carry only JSON-RPC.
const wf = [];
let wWatchers = '1';
let wStatus = 204;
const wsink = http.createServer((req, res) => {
  // The persona's files (bp-browser --files-url): a list, then each file.
  if (req.method === 'GET' && req.url === '/files/run-token/') {
    res.writeHead(200, { 'Content-Type': 'application/json' });
    res.end('["hello.txt"]\n');
    return;
  }
  if (req.method === 'GET' && req.url === '/files/run-token/hello.txt') {
    res.writeHead(200, { 'Content-Type': 'text/plain' });
    res.end('hello from the smoke');
    return;
  }
  const chunks = [];
  req.on('data', (c) => chunks.push(c));
  req.on('end', () => {
    wf.push({ at: Date.now(), body: Buffer.concat(chunks) });
    res.writeHead(wStatus, { 'X-Bp-Watchers': wWatchers });
    res.end();
  });
});
await new Promise((r) => wsink.listen(0, '127.0.0.1', r));
const wurl = `http://127.0.0.1:${wsink.address().port}/live/run-token`;

const outDir = mkdtempSync(join(tmpdir(), 'bp-out-'));
const wrap = spawn('/opt/bp/bp-browser', [
  `--live-url=${wurl}`, `--files-url=http://127.0.0.1:${wsink.address().port}/files/run-token/`, '--bp-device=pixel-7', '--headless', '--isolated', '--no-sandbox',
  '--browser=chromium', `--output-dir=${outDir}`,
], { stdio: ['pipe', 'pipe', 'pipe'] });
let wout = '';
const waiters = new Map();
wrap.stdout.on('data', (d) => {
  wout += d;
  let i;
  while ((i = wout.indexOf('\n')) >= 0) {
    const line = wout.slice(0, i).trim();
    wout = wout.slice(i + 1);
    if (!line) continue;
    let m;
    try { m = JSON.parse(line); } catch { fail('wrapper stdout carried non-JSON: ' + line.slice(0, 80)); }
    if (m.jsonrpc !== '2.0') fail('wrapper stdout line is not JSON-RPC: ' + line.slice(0, 80));
    if (m.id != null && waiters.has(m.id)) { waiters.get(m.id)(m); waiters.delete(m.id); }
  }
});
wrap.stderr.on('data', (d) => process.stderr.write(d));
wrap.on('exit', (c) => { if (c) fail('wrapper exited with ' + c); });
const rpc = (id, method, params) => new Promise((resolve, reject) => {
  const t = setTimeout(() => reject(new Error('timeout waiting for ' + method)), 60000);
  waiters.set(id, (m) => { clearTimeout(t); resolve(m); });
  wrap.stdin.write(JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n');
});
try {
  const init = await rpc(1, 'initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'smoke', version: '0' } });
  if (init.error) fail('initialize: ' + JSON.stringify(init.error));
  wrap.stdin.write(JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized' }) + '\n');
  const page = 'data:text/html,' + encodeURIComponent('<body><h1>wrapper</h1><script>setInterval(()=>{document.body.style.background="hsl("+(Date.now()/5%360)+",80%,50%)"},50)</script>');
  const nav = await rpc(2, 'tools/call', { name: 'browser_navigate', arguments: { url: page } });
  if (nav.error || nav.result?.isError) fail('browser_navigate: ' + JSON.stringify(nav).slice(0, 300));
} catch (e) { fail(String(e.message || e)); }

// bp-browser downloads the persona's files before starting the browser. The
// smoke needs a writable /workspace (CI mounts a tmpfs there); no skipping.
try {
  if (readFileSync('/workspace/files/hello.txt', 'utf8') !== 'hello from the smoke') fail('files: hello.txt has the wrong content');
  if (readdirSync('/workspace/files').some((n) => n.endsWith('.part'))) fail('files: a .part file was left behind');
  console.log('ok files downloaded');
} catch (e) { fail('files: ' + (e.message || e)); }

// Phase A: a watcher is present, so frames flow at the fast cadence.
await sleep(4000);
const a0 = wf.length;
if (a0 < 4) fail(`wrapper streamed only ${a0} frames with a watcher`);
if (!wf.every((f) => f.body[0] === 0xff && f.body[1] === 0xd8)) fail('wrapper frame is not a JPEG');
const aStart = wf.length;
await sleep(3000);
const aRate = wf.length - aStart;
if (aRate < 5) fail(`watched cadence too slow: ${aRate} PUTs in 3s`);

// Phase B: no watchers, so the cadence drops to the slow gap (about one PUT per 5s).
wWatchers = '0';
await sleep(1500); // let the in-flight response carry the new header
const bStart = wf.length;
await sleep(8000);
const bRate = wf.length - bStart;
if (bRate > 3) fail(`unwatched cadence too fast: ${bRate} PUTs in 8s`);
if (bRate < 1) fail('keepalive missing: no PUT in 8s while unwatched');

// Phase C: the director says the run is over (404); PUTs must stop.
// bp-screen only learns the watcher count from a 2xx response, so first let one PUT
// (up to 5 s away at the slow gap) bring the fast cadence back, then answer 404.
wWatchers = '1';
await sleep(6500);
wStatus = 404;
await sleep(1500);
const cStart = wf.length;
await sleep(8000);
if (wf.length !== cStart) fail(`${wf.length - cStart} PUTs after the director answered 404`);

wrap.removeAllListeners('exit');
wrap.kill();
wsink.close();
console.log(`ok wrapper: watched ${aRate}/3s, unwatched ${bRate}/8s, stopped on 404; stdout was JSON-RPC only`);
process.exit(0);
