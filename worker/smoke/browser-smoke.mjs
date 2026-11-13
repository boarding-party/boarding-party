// Smoke (in the image): bp-browser starts Playwright MCP behind the tool gate
// with the persona's settings. A keyboard-only persona with reduced motion
// gets no click tool, types into the focused field, and the page sees the
// reduced-motion preference.
import http from 'node:http';
import { spawn } from 'node:child_process';
import { readFileSync } from 'node:fs';

const fail = (m) => { console.error('FAIL ' + m); process.exit(1); };

const page = `<!doctype html><title>form</title>
<label>Name <input id="n"></label>
<p id="m"></p>
<script>document.getElementById('m').textContent = matchMedia('(prefers-reduced-motion: reduce)').matches ? 'motion reduced' : 'motion full';</script>`;
const srv = http.createServer((req, res) => { res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' }); res.end(page); });
await new Promise((r) => srv.listen(0, '127.0.0.1', r));
const url = `http://127.0.0.1:${srv.address().port}/`;

const p = spawn(process.env.BP_BROWSER || '/opt/bp/bp-browser', ['--bp-assistive=keyboard-only', '--bp-display=reduced-motion',
  '--headless', '--isolated', '--no-sandbox', '--browser=chromium', '--output-dir=' + (process.env.BP_OUT || '/workspace/out')], { stdio: ['pipe', 'pipe', 'inherit'] });
const replies = new Map();
let buf = '';
p.stdout.on('data', (d) => {
  buf += d;
  for (let i; (i = buf.indexOf('\n')) >= 0;) {
    const line = buf.slice(0, i); buf = buf.slice(i + 1);
    let m;
    try { m = JSON.parse(line); } catch { fail('non-protocol output on stdout: ' + line); }
    if (m.id !== undefined) replies.set(m.id, m);
  }
});
let id = 0;
const ask = (method, params) => {
  const n = ++id;
  p.stdin.write(JSON.stringify({ jsonrpc: '2.0', id: n, method, params }) + '\n');
  return new Promise((resolve, reject) => {
    const t0 = Date.now();
    const tick = () => (replies.has(n) ? resolve(replies.get(n)) : Date.now() - t0 > 60000 ? reject(new Error(`no reply to ${method}`)) : setTimeout(tick, 20));
    tick();
  });
};
const call = async (name, args) => {
  const r = await ask('tools/call', { name, arguments: args });
  return { error: !!r.result?.isError, text: (r.result?.content || []).map((c) => c.text || '').join('\n') };
};

await ask('initialize', { protocolVersion: '2025-06-18', capabilities: {}, clientInfo: { name: 'smoke', version: '1' } });
p.stdin.write(JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized' }) + '\n');
const names = (await ask('tools/list', {})).result.tools.map((t) => t.name);
for (const t of ['browser_click', 'browser_type', 'browser_resize', 'browser_evaluate']) if (names.includes(t)) fail(`${t} is listed for a keyboard-only persona`);
for (const t of ['browser_press_key', 'browser_type_focused', 'browser_snapshot']) if (!names.includes(t)) fail(`${t} is missing`);

if ((await call('browser_navigate', { url })).error) fail('could not open the page');
const click = await call('browser_click', { element: 'Name', target: 'e1' });
if (!click.error || !/keyboard/.test(click.text)) fail('a click was not refused: ' + click.text);
if ((await call('browser_press_key', { key: 'Tab' })).error) fail('Tab failed');
const typed = await call('browser_type_focused', { text: 'Zoë £5' });
if (typed.error) fail('typing failed: ' + typed.text);
const snap = await call('browser_snapshot', {});
if (!snap.text.includes('Zoë £5')) fail('the typed text is not in the field: ' + snap.text.slice(0, 800));
if (!snap.text.includes('motion reduced')) fail('the page does not see reduced motion: ' + snap.text.slice(0, 800));

const settings = JSON.parse(readFileSync('/tmp/bp-pw/settings.json', 'utf8'));
if (settings.assistive !== 'keyboard-only' || settings.display !== 'reduced-motion') fail('settings not recorded: ' + JSON.stringify(settings));

p.kill(); srv.close();
console.log('browser ok');
process.exit(0);
