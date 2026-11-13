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
function run(args) {
  return new Promise((resolve) => {
    const p = spawn('bp-replay', args, { stdio: ['ignore', 'pipe', 'inherit'] });
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

// Run mode: the code comes from the director; the result and evidence go back.
journeyCode = passing;
const urls = (p) => `${base}/${p}/${token}`;
r = await run([`--journey-url=${urls('journey')}`, `--report-url=${urls('replay')}`, `--upload-url=${urls('artifacts')}/`,
  `--profile-url=${urls('profile')}`, '--seed=11']);
const report = got['PUT /replay/TOKEN'];
if (r.code !== 0 || !report) fail('run mode sent no report: ' + r.out);
const rep = JSON.parse(report.toString());
if (rep.status !== 'passed' || rep.seed !== 11) fail('run mode report: ' + report);
if (!Object.keys(got).some((k) => k.startsWith('PUT /artifacts/TOKEN/') && k.endsWith('.webm'))) fail('no video uploaded: ' + Object.keys(got));
if (!got['PUT /artifacts/TOKEN/trace.zip']) fail('no trace uploaded');
if (!got['PUT /profile/TOKEN']) fail('profile not saved back');

// The journey runs apart from bp-replay: its argv and env carry no token.
journeyCode = `export default async function journey({ bp }) {
  await bp.step('Look around', async () => {
    const seen = JSON.stringify([process.argv, process.env]);
    if (seen.includes('${token}')) throw new Error('journey saw the run token');
  });
}`;
for (const k of Object.keys(got)) delete got[k];
r = await run([`--journey-url=${urls('journey')}`, `--report-url=${urls('replay')}`, `--upload-url=${urls('artifacts')}/`,
  `--profile-url=${urls('profile')}`]);
const leakRep = JSON.parse(got['PUT /replay/TOKEN']?.toString() || '{}');
if (leakRep.status !== 'passed') fail('the journey could see the run token: ' + JSON.stringify(leakRep));

// A failed step leaves its screenshot.
journeyCode = `export default async function journey({ page, bp }) {
  await bp.step('Open the shop', async () => { await page.goto('${base}/'); });
  await bp.step('Press pay', async () => { await page.getByRole('button', { name: 'Pay now' }).click({ timeout: 2000 }); });
}`;
for (const k of Object.keys(got)) delete got[k];
r = await run([`--journey-url=${urls('journey')}`, `--report-url=${urls('replay')}`, `--upload-url=${urls('artifacts')}/`,
  `--profile-url=${urls('profile')}`]);
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
r = await run([`--journey-url=${urls('journey')}`, `--report-url=${urls('replay')}`, `--upload-url=${urls('artifacts')}/`, `--profile-url=${urls('profile')}`]);
const waitRep = JSON.parse(got['PUT /replay/TOKEN']?.toString() || '{}');
if (waitRep.status !== 'waiting' || waitRep.step !== 1 || waitRep.wait?.n !== 1 || waitRep.wait.until?.email?.subject !== 'Confirm' || waitRep.wait.until.timeout !== '2h') {
  fail('waiting replay: ' + JSON.stringify(waitRep));
}
journeyResume = { after: 1, arrivals: [{ detail: 'email "Confirm"', message: { from: 'shop', subject: 'Confirm', text: '', links: [`${base}/help`, `${base}/confirm?t=1`] } }] };
for (const k of Object.keys(got)) delete got[k];
r = await run([`--journey-url=${urls('journey')}`, `--report-url=${urls('replay')}`, `--upload-url=${urls('artifacts')}/`, `--profile-url=${urls('profile')}`]);
const resumedRep = JSON.parse(got['PUT /replay/TOKEN']?.toString() || '{}');
if (resumedRep.status !== 'passed') fail('resumed replay: ' + JSON.stringify(resumedRep));

srv.close();
console.log('ok replay');
