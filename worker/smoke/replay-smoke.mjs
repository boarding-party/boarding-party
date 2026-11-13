// Smoke: bp-replay runs journeys with no model. Self mode (an AI run proving
// its journey) and run mode (a replay run reporting to the director), both
// against a local page and a fake director.
import http from 'node:http';
import { spawn } from 'node:child_process';
import { writeFileSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const fail = (m) => { console.error('FAIL ' + m); process.exit(1); };

const page = `<!doctype html><title>shop</title>
<label>Quantity <input id="q" type="number" value="1"></label>
<button id="add" onclick="document.getElementById('total').textContent='£'+(12*document.getElementById('q').value).toFixed(2)">Add</button>
<p>Total <span id="total" data-testid="total">£0.00</span></p>`;

const got = {}; // what the fake director received, by path
const token = 'a'.repeat(64);
const claims = new Set(); // one-time claims not yet spent
const helpers = new Set(); // live screen and console PUTs seen
let journeyCode = '';
let journeyResume = null;
const srv = http.createServer((req, res) => {
  const chunks = [];
  req.on('data', (c) => chunks.push(c));
  req.on('end', () => {
    const body = Buffer.concat(chunks);
    const path = req.url;
    if (req.method === 'GET' && path === '/') { res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' }); return res.end(page); }
    if (req.method === 'GET' && path === `/journey/${token}`) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      return res.end(JSON.stringify({ code: journeyCode, ...(journeyResume ? { resume: journeyResume } : {}) }));
    }
    if (req.method === 'GET' && path === `/profile/${token}`) { res.writeHead(204); return res.end(); }
    if (path.startsWith('/claim/')) {
      const code = path.slice('/claim/'.length);
      if (req.method !== 'POST' || !claims.delete(code)) { res.writeHead(404); return res.end(); }
      res.writeHead(200, { 'Content-Type': 'application/json' });
      return res.end(JSON.stringify({ journey: `/journey/${token}`, report: `/replay/${token}`, upload: `/artifacts/${token}/`,
        profile: `/profile/${token}`, live: `/live/${token}`, console: `/console/${token}` }));
    }
    if (req.method === 'PUT' && (path === `/live/${token}` || path === `/console/${token}`)) helpers.add(path.split('/')[1]);
    got[req.method + ' ' + path.replace(token, 'TOKEN')] = body;
    res.writeHead(204); res.end();
  });
});
await new Promise((r) => srv.listen(0, '127.0.0.1', r));
const base = `http://127.0.0.1:${srv.address().port}`;

const dir = mkdtempSync(join(tmpdir(), 'bp-replay-smoke-'));
const good = join(dir, 'good.mjs');
const missing = join(dir, 'missing.mjs');
const passing = `export default async function journey({ page, data, bp, expect }) {
  const q = data.int(1, 5);
  await bp.step('Open the shop', async () => { await page.goto('${base}/'); });
  await bp.step('Add the items', async () => {
    await page.getByLabel('Quantity').fill(String(q));
    await page.getByRole('button', { name: 'Add' }).click();
    await expect(page.getByTestId('total')).toHaveText(bp.money(12 * q));
  });
}`;
writeFileSync(good, passing);
writeFileSync(missing, `export default async function journey({ page, bp }) {
  await bp.step('Open the shop', async () => { await page.goto('${base}/'); });
  await bp.step('Press pay', async () => { await page.getByRole('button', { name: 'Pay now' }).click({ timeout: 2000 }); });
}`);
function run(args, env = {}) {
  return new Promise((resolve) => {
    const p = spawn(process.env.BP_REPLAY || 'bp-replay', args, { stdio: ['ignore', 'pipe', 'inherit'], env: { ...process.env, ...env } });
    let out = '';
    p.stdout.on('data', (d) => { out += d; });
    p.on('close', (code) => {
      const line = out.trim().split('\n').pop() || '';
      let res = null;
      try { res = JSON.parse(line); } catch { /* checked by caller */ }
      resolve({ code, res, out });
    });
  });
}

let r = await run(['--journey', good, '--self', '--seed', '7']);
if (r.code !== 0 || r.res?.status !== 'passed' || r.res.seed !== 7 || typeof r.res.inputs !== 'object') fail('good journey: ' + r.out);
const again = await run(['--journey', good, '--self', '--seed', '7']);
if (JSON.stringify(again.res.inputs) !== JSON.stringify(r.res.inputs)) fail('same seed drew different inputs');

r = await run(['--journey', missing, '--self']);
if (r.code === 0 || r.res?.status !== 'failed' || r.res.step !== 2 || r.res.stepName !== 'Press pay' || !r.res.error) fail('missing button: ' + r.out);
r = await run(['--journey', missing, '--self', '--expect-fail-step', '2']);
if (r.code !== 0 || r.res?.status !== 'failed' || r.res.step !== 2) fail('expect-fail-step 2: ' + r.out);
r = await run(['--journey', missing, '--self', '--expect-fail-step', '1']);
if (r.code === 0) fail('expect-fail-step 1 must not prove a failure at step 2');

// The persona's settings: zoom lays the page out at 200%, reduced motion
// reaches the page, and eyesight changes what screenshots show. Given on
// the command line, or recorded by bp-browser for an AI run's self-replay.
const looks = join(dir, 'looks.mjs');
writeFileSync(looks, `export default async function journey({ page, bp }) {
  await bp.step('Look at a red box', async () => {
    await page.setContent('<body style="margin:0"><div style="width:100px;height:100px;background:rgb(255,0,0)"></div></body>');
    const [w, dpr, reduced] = await page.evaluate(() => [innerWidth, devicePixelRatio, matchMedia('(prefers-reduced-motion: reduce)').matches]);
    if (w !== 640 || dpr !== 2 || !reduced) throw new Error('width, pixel ratio, reduced motion: ' + [w, dpr, reduced]);
    const px = await page.evaluate(async (b64) => {
      const img = new Image(); img.src = 'data:image/png;base64,' + b64; await img.decode();
      const c = document.createElement('canvas'); c.width = img.width; c.height = img.height;
      const g = c.getContext('2d'); g.drawImage(img, 0, 0); return Array.from(g.getImageData(50, 50, 1, 1).data);
    }, (await page.screenshot()).toString('base64'));
    if (!(px[0] === px[1] && px[1] === px[2])) throw new Error('colour left with no colour vision: ' + px);
  });
}`);
r = await run(['--journey', looks, '--self', '--assistive', 'zoom', '--display', 'reduced-motion', '--vision', 'achromatopsia']);
if (r.code !== 0 || r.res?.status !== 'passed') fail('settings on the command line: ' + r.out);
const recorded = join(dir, 'settings.json');
writeFileSync(recorded, JSON.stringify({ device: 'desktop', assistive: 'zoom', vision: 'achromatopsia', display: 'reduced-motion' }));
r = await run(['--journey', looks, '--self'], { BP_SETTINGS: recorded });
if (r.code !== 0 || r.res?.status !== 'passed') fail('recorded settings: ' + r.out);
r = await run(['--journey', looks, '--self', '--device', 'desktop'], { BP_SETTINGS: recorded });
if (r.code !== 0 || r.res?.status !== 'passed') fail('one flag dropped the other recorded settings: ' + r.out);
r = await run(['--journey', looks, '--self', '--assistive', 'none'], { BP_SETTINGS: recorded });
if (r.res?.status !== 'failed') fail('a flag on the command line must win over its recorded setting: ' + r.out);

// Run mode: bp-replay spends its one-time claim for its URLs; the code
// comes from the director; the result and evidence go back.
let nclaim = 0;
const claimed = (...extra) => {
  const code = `claim${++nclaim}`;
  claims.add(code);
  return run([`--claim-url=${base}/claim/${code}`, ...extra]);
};
journeyCode = passing;
r = await claimed('--seed=11');
const report = got['PUT /replay/TOKEN'];
if (r.code !== 0 || !report) fail('run mode sent no report: ' + r.out);
const rep = JSON.parse(report.toString());
if (rep.status !== 'passed' || rep.seed !== 11) fail('run mode report: ' + report);
if (!Object.keys(got).some((k) => k.startsWith('PUT /artifacts/TOKEN/') && k.endsWith('.webm'))) fail('no video uploaded: ' + Object.keys(got));
if (!got['PUT /artifacts/TOKEN/trace.zip']) fail('no trace uploaded');
if (!got['PUT /profile/TOKEN']) fail('profile not saved back');

// A spent or unknown claim gives nothing: bp-replay stops with no report.
for (const k of Object.keys(got)) delete got[k];
r = await run([`--claim-url=${base}/claim/spent`]);
if (r.code === 0 || got['PUT /replay/TOKEN']) fail('a spent claim still ran: ' + r.out);

// The journey can read every command line and environment in the pod, its
// own and bp-replay's and the live screen's: none carries the token.
journeyCode = `import { readdirSync, readFileSync } from 'node:fs';
export default async function journey({ bp }) {
  await bp.step('Look around', async () => {
    let seen = JSON.stringify([process.argv, process.env]);
    let n = 0;
    for (const pid of readdirSync('/proc').filter((d) => /^\\d+$/.test(d))) {
      for (const f of ['cmdline', 'environ']) {
        try { seen += readFileSync('/proc/' + pid + '/' + f, 'latin1'); n++; } catch { /* not ours to read */ }
      }
    }
    if (n < 4) throw new Error('read only ' + n + ' proc files');
    if (seen.includes('${token}')) throw new Error('journey saw the run token');
  });
}`;
for (const k of Object.keys(got)) delete got[k];
r = await claimed();
const leakRep = JSON.parse(got['PUT /replay/TOKEN']?.toString() || '{}');
if (leakRep.status !== 'passed') fail('the journey could see the run token: ' + JSON.stringify(leakRep));

// The live screen and the console recorder get their URLs on stdin and
// report as in AI runs.
journeyCode = `export default async function journey({ page, bp }) {
  await bp.step('Open the shop', async () => {
    await page.goto('${base}/');
    await page.evaluate(() => console.error('bp-smoke console line'));
    await page.waitForTimeout(6000);
  });
}`;
helpers.clear();
r = await claimed();
if (!helpers.has('live') || !helpers.has('console')) fail('helpers reported only ' + JSON.stringify([...helpers]));

// A failed step leaves its screenshot.
journeyCode = `export default async function journey({ page, bp }) {
  await bp.step('Open the shop', async () => { await page.goto('${base}/'); });
  await bp.step('Press pay', async () => { await page.getByRole('button', { name: 'Pay now' }).click({ timeout: 2000 }); });
}`;
for (const k of Object.keys(got)) delete got[k];
r = await claimed();
const failRep = JSON.parse(got['PUT /replay/TOKEN']?.toString() || '{}');
if (failRep.status !== 'failed' || failRep.step !== 2 || !got['PUT /artifacts/TOKEN/failed-step.png']) fail('failed replay: ' + JSON.stringify(failRep));

// Waits: a journey that waits stops there, and its continuation skips the
// steps before the wait and carries on with what arrived.
const waiting = join(dir, 'waiting.mjs');
const waitingCode = `export default async function journey({ page, bp }) {
  await bp.step('Open the shop', async () => { await page.goto('${base}/'); });
  const mail = await bp.waitFor({ email: { subject: 'Confirm' }, within: '2h' });
  await bp.step('Follow the link', async () => {
    const url = mail.link(/confirm/);
    if (url !== '${base}/confirm?t=1') throw new Error('picked ' + url);
    await page.goto('${base}/');
  });
}`;
writeFileSync(waiting, waitingCode);
r = await run(['--journey', waiting, '--self']);
if (r.code !== 0 || r.res?.status !== 'waiting' || r.res.step !== 1 || r.res.wait?.n !== 1) fail('self mode at a wait: ' + r.out);
journeyCode = waitingCode;
for (const k of Object.keys(got)) delete got[k];
r = await claimed();
const waitRep = JSON.parse(got['PUT /replay/TOKEN']?.toString() || '{}');
if (waitRep.status !== 'waiting' || waitRep.step !== 1 || waitRep.wait?.n !== 1 || waitRep.wait.until?.email?.subject !== 'Confirm' || waitRep.wait.until.timeout !== '2h') {
  fail('waiting replay: ' + JSON.stringify(waitRep));
}
journeyResume = { after: 1, arrivals: [{ detail: 'email "Confirm"', message: { from: 'shop', subject: 'Confirm', text: '', links: [`${base}/help`, `${base}/confirm?t=1`] } }] };
for (const k of Object.keys(got)) delete got[k];
r = await claimed();
const resumedRep = JSON.parse(got['PUT /replay/TOKEN']?.toString() || '{}');
if (resumedRep.status !== 'passed') fail('resumed replay: ' + JSON.stringify(resumedRep));

srv.close();
console.log('ok replay');
