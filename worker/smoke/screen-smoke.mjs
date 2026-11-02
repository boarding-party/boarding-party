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
await p1.goto('data:text/html,<h1>one</h1>');

const screen = spawn('node', ['/opt/bp/bp-screen.mjs', `--live-url=${url}`], { stdio: ['ignore', 'inherit', 'inherit'] });
await sleep(3000); // poll interval 2s + attach
const p2 = await ctx.newPage();
await p2.goto('data:text/html,<h1>two</h1>');
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
if (!frames.some((f) => f.at > secondOpened)) fail('no frame after the second page opened');

// Video size: iPhone 15 viewport must be 390x844 and the recording proportional to it.
const vp = ctx._options?.viewport || iphone.viewport;
if (vp.width !== 390 || vp.height !== 844) fail(`viewport ${vp.width}x${vp.height}`);
const webm = readdirSync(dir).find((f) => f.endsWith('.webm'));
let videoNote = 'viewport 390x844 (no ffmpeg)';
const ffdir = readdirSync('/ms-playwright').find((d) => d.startsWith('ffmpeg-'));
if (webm && ffdir) {
  const bin = readdirSync(join('/ms-playwright', ffdir)).find((f) => f.startsWith('ffmpeg'));
  const r = spawnSync(join('/ms-playwright', ffdir, bin), ['-i', join(dir, webm)], { encoding: 'utf8' });
  const m = /Video:.*?, (\d+)x(\d+)/.exec(r.stderr || '');
  if (!m) fail('cannot read video size');
  const [w, h] = [+m[1], +m[2]];
  if (Math.abs(w / h - 390 / 844) > 0.01) fail(`video ${w}x${h} not 390x844-proportional`);
  videoNote = `video ${w}x${h}`;
}
console.log(`ok ${frames.length} frames; ${videoNote}`);
process.exit(0);
