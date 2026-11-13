// Smoke (in the image): bp-browser starts Playwright MCP behind the tool gate
// with the persona's settings, loaded the way Playwright MCP loads them.
// A keyboard-only persona with reduced motion gets no click tool (nor a
// javascript: URL), types into the focused field, and the page sees reduced
// motion. A zoomed persona with no colour vision gets a 200% layout and grey
// screenshots.
import http from 'node:http';
import { spawn } from 'node:child_process';
import { mkdtempSync, readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const fail = (m) => { console.error('FAIL ' + m); process.exit(1); };

const page = `<!doctype html><title>form</title>
<label>Name <input id="n"></label>
<p id="m"></p><p id="w"></p>
<div style="width:120px;height:120px;background:rgb(255,0,0)"></div>
<script>
document.getElementById('m').textContent = matchMedia('(prefers-reduced-motion: reduce)').matches ? 'motion reduced' : 'motion full';
document.getElementById('w').textContent = 'width ' + innerWidth;
</script>`;
const srv = http.createServer((req, res) => { res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' }); res.end(page); });
await new Promise((r) => srv.listen(0, '127.0.0.1', r));
const url = `http://127.0.0.1:${srv.address().port}/`;

// session starts bp-browser with settings and returns its MCP client.
async function session(settings, cwd) {
  const p = spawn(process.env.BP_BROWSER || '/opt/bp/bp-browser', [...settings,
    '--headless', '--isolated', '--no-sandbox', '--browser=chromium', '--output-dir=' + join(cwd, 'out')], { cwd, stdio: ['pipe', 'pipe', 'inherit'] });
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
  return { ask, call, kill: () => p.kill() };
}

// Keyboard only, reduced motion.
const kb = await session(['--bp-assistive=keyboard-only', '--bp-display=reduced-motion'], mkdtempSync(join(tmpdir(), 'bp-kb-')));
const names = (await kb.ask('tools/list', {})).result.tools.map((t) => t.name);
for (const t of ['browser_click', 'browser_type', 'browser_resize', 'browser_evaluate']) if (names.includes(t)) fail(`${t} is listed for a keyboard-only persona`);
for (const t of ['browser_press_key', 'browser_type_focused', 'browser_snapshot']) if (!names.includes(t)) fail(`${t} is missing`);
if ((await kb.call('browser_navigate', { url })).error) fail('could not open the page');
const click = await kb.call('browser_click', { element: 'Name', target: 'e1' });
if (!click.error || !/keyboard/.test(click.text)) fail('a click was not refused: ' + click.text);
const js = await kb.call('browser_navigate', { url: "javascript:document.getElementById('n').value='clicked'" });
if (!js.error || !/keyboard/.test(js.text)) fail('a javascript: URL was not refused: ' + js.text);
if ((await kb.call('browser_press_key', { key: 'Tab' })).error) fail('Tab failed');
const typed = await kb.call('browser_type_focused', { text: 'Zoë £5' });
if (typed.error) fail('typing failed: ' + typed.text);
let snap = await kb.call('browser_snapshot', {});
if (!snap.text.includes('Zoë £5')) fail('the typed text is not in the field: ' + snap.text.slice(0, 800));
if (!snap.text.includes('motion reduced')) fail('the page does not see reduced motion: ' + snap.text.slice(0, 800));
if (!snap.text.includes('width 1280')) fail('the default desktop is not 1280 wide: ' + snap.text.slice(0, 800));
const settings = JSON.parse(readFileSync('/tmp/bp-pw/settings.json', 'utf8'));
if (settings.assistive !== 'keyboard-only' || settings.display !== 'reduced-motion') fail('settings not recorded: ' + JSON.stringify(settings));
kb.kill();

// Zoom, no colour vision: the eyesight init page and the context options as
// Playwright MCP loads them from the run's config.
const dir = mkdtempSync(join(tmpdir(), 'bp-eye-'));
const eye = await session(['--bp-assistive=zoom', '--bp-vision=achromatopsia'], dir);
if ((await eye.call('browser_navigate', { url })).error) fail('could not open the page with zoom');
snap = await eye.call('browser_snapshot', {});
if (!snap.text.includes('width 640')) fail('zoom did not halve the layout width: ' + snap.text.slice(0, 800));
const shot = await eye.call('browser_take_screenshot', { filename: 'eye.png' });
if (shot.error) fail('screenshot failed: ' + shot.text);
eye.kill();
const png = readFileSync(join(dir, 'eye.png'));
const require = createRequire(process.env.BP_PW_ROOT || '/usr/lib/node_modules/@playwright/mcp/');
const browser = await require('playwright-core').chromium.launch(process.env.BP_CHROMIUM ? { executablePath: process.env.BP_CHROMIUM } : {});
const pg = await browser.newPage();
const red = await pg.evaluate(async (b64) => {
  const img = new Image(); img.src = 'data:image/png;base64,' + b64; await img.decode();
  const c = document.createElement('canvas'); c.width = img.width; c.height = img.height;
  const g = c.getContext('2d'); g.drawImage(img, 0, 0);
  const d = g.getImageData(0, 0, img.width, img.height).data;
  let n = 0;
  for (let i = 0; i < d.length; i += 4) if (d[i] > 200 && d[i + 1] < 80 && d[i + 2] < 80) n++;
  return n;
}, png.toString('base64'));
await browser.close();
if (red > 0) fail(`${red} red pixels in a screenshot with no colour vision`);

srv.close();
console.log('browser ok');
process.exit(0);
