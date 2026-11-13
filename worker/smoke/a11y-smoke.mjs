// Smoke: a persona's device and accessibility settings become browser
// settings (bp-a11y), the tool gate hides and refuses what the persona cannot
// use (bp-gate), and the run's Playwright MCP config carries them all
// (bp-pw-setup), eyesight included.
import { spawn } from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { contextOptions, toolRules, visionType, readSettings, GLOBAL_BLOCK } from '../bp-a11y.mjs';

const fail = (m) => { console.error('FAIL ' + m); process.exit(1); };
const eq = (a, b, what) => { if (JSON.stringify(a) !== JSON.stringify(b)) fail(`${what}: got ${JSON.stringify(a)}, want ${JSON.stringify(b)}`); };
const here = (p) => fileURLToPath(new URL(p, import.meta.url));

const require = createRequire(process.env.BP_PW_ROOT || '/usr/lib/node_modules/@playwright/mcp/');
const { chromium, devices } = require('playwright-core');

// Devices and zoom.
eq(contextOptions({}, devices), { viewport: { width: 1280, height: 800 } }, 'default desktop');
eq(contextOptions({ device: 'desktop-large' }, devices), { viewport: { width: 1920, height: 1080 } }, 'large desktop');
eq(contextOptions({ device: 'nonsense' }, devices), { viewport: { width: 1280, height: 800 } }, 'unknown device');
eq(contextOptions({ device: 'desktop-large', assistive: 'zoom' }, devices),
  { viewport: { width: 960, height: 540 }, deviceScaleFactor: 2 }, 'zoomed large desktop');
const phone = devices['iPhone 15'];
const zp = contextOptions({ device: 'iphone-15', assistive: 'zoom' }, devices);
eq([zp.viewport, zp.screen, zp.deviceScaleFactor, zp.isMobile, zp.hasTouch, zp.userAgent],
  [{ width: Math.round(phone.viewport.width / 2), height: Math.round(phone.viewport.height / 2) },
    { width: Math.round(phone.screen.width / 2), height: Math.round(phone.screen.height / 2) },
    phone.deviceScaleFactor * 2, true, true, phone.userAgent], 'zoomed phone');
if ('defaultBrowserType' in zp) fail('a device descriptor key that is no context option leaked');
eq(contextOptions({ device: 'iphone-15' }, devices).viewport, phone.viewport, 'phone');

// Display settings.
eq(contextOptions({ display: 'reduced-motion' }, devices).reducedMotion, 'reduce', 'reduced motion');
const hc = contextOptions({ display: 'high-contrast' }, devices);
eq([hc.forcedColors, hc.contrast], ['active', 'more'], 'high contrast');
eq(contextOptions({ display: 'dark' }, devices).colorScheme, 'dark', 'dark mode');
eq(contextOptions({ display: 'nonsense' }, devices), { viewport: { width: 1280, height: 800 } }, 'unknown display');

// Eyesight.
eq(['blurred', 'low-contrast', 'deuteranopia', 'protanopia', 'tritanopia', 'achromatopsia', 'none', 'x'].map(visionType),
  ['blurredVision', 'reducedContrast', 'deuteranopia', 'protanopia', 'tritanopia', 'achromatopsia', '', ''], 'vision types');

// Tool rules.
for (const t of ['browser_resize', 'browser_emulate_media']) if (!GLOBAL_BLOCK.includes(t)) fail(`${t} is not blocked for everyone`);
const plain = toolRules('');
eq([plain.block, plain.add, plain.imageResponses], [GLOBAL_BLOCK, [], ''], 'no assistive technology');
const kb = toolRules('keyboard-only');
for (const t of ['browser_click', 'browser_hover', 'browser_drag', 'browser_drop', 'browser_select_option', 'browser_fill_form',
  'browser_type', 'browser_evaluate', 'browser_run_code_unsafe', 'browser_resize']) {
  if (!kb.block.includes(t)) fail(`keyboard only does not block ${t}`);
}
for (const t of ['browser_press_key', 'browser_snapshot', 'browser_take_screenshot', 'browser_file_upload', 'browser_navigate']) {
  if (kb.block.includes(t)) fail(`keyboard only blocks ${t}`);
}
eq(kb.add, ['browser_type_focused'], 'keyboard only adds typing');
const sr = toolRules('screen-reader');
eq(sr.imageResponses, 'omit', 'screen reader sees no images');
if (sr.block.includes('browser_take_screenshot')) fail('screen reader cannot save bug screenshots');

// The gate in front of a fake MCP server.
const fake = join(mkdtempSync(join(tmpdir(), 'bp-gate-')), 'fake.mjs');
writeFileSync(fake, `import { createInterface } from 'node:readline';
const tools = ['browser_click', 'browser_press_key', 'browser_resize', 'browser_run_code_unsafe', 'browser_snapshot'].map((name) => ({ name, inputSchema: { type: 'object' } }));
createInterface({ input: process.stdin }).on('line', (l) => {
  const m = JSON.parse(l);
  if (m.id === undefined) return;
  const result = m.method === 'tools/list' ? { tools } : m.method === 'tools/call' ? { content: [{ type: 'text', text: 'called ' + JSON.stringify(m.params) }] } : {};
  process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id: m.id, result }) + '\\n');
});
`);
const gate = spawn('node', [here('../bp-gate.mjs'), '--assistive=keyboard-only', '--', 'node', fake], { stdio: ['pipe', 'pipe', 'inherit'] });
const replies = new Map();
let buf = '';
gate.stdout.on('data', (d) => {
  buf += d;
  for (let i; (i = buf.indexOf('\n')) >= 0;) { const m = JSON.parse(buf.slice(0, i)); buf = buf.slice(i + 1); replies.set(m.id, m); }
});
const ask = (id, method, params) => {
  gate.stdin.write(JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n');
  return new Promise((resolve, reject) => {
    const t0 = Date.now();
    const tick = () => (replies.has(id) ? resolve(replies.get(id)) : Date.now() - t0 > 5000 ? reject(new Error(`no reply to ${id}`)) : setTimeout(tick, 10));
    tick();
  });
};
const list = await ask(1, 'tools/list', {});
eq(list.result.tools.map((t) => t.name), ['browser_press_key', 'browser_snapshot', 'browser_type_focused'], 'gated tool list');
const refused = await ask('a', 'tools/call', { name: 'browser_click', arguments: { target: 'e3' } });
if (!refused.result?.isError || !/keyboard/.test(refused.result.content[0].text)) fail('a blocked click was not refused: ' + JSON.stringify(refused));
const pressed = await ask(2, 'tools/call', { name: 'browser_press_key', arguments: { key: 'Tab' } });
if (!pressed.result.content[0].text.includes('"browser_press_key"')) fail('an allowed call was not forwarded: ' + JSON.stringify(pressed));
const typed = await ask(3, 'tools/call', { name: 'browser_type_focused', arguments: { text: 'Tom\'s £5 "café"', submit: true } });
const sent = typed.result.content[0].text;
if (!sent.includes('browser_run_code_unsafe') || !sent.includes('keyboard.type') || !sent.includes('Enter')) fail('typing was not sent as keyboard.type: ' + sent);
const code = JSON.parse(sent.slice('called '.length)).arguments.code;
if (!code.includes(JSON.stringify('Tom\'s £5 "café"'))) fail('typed text not quoted safely: ' + code);
gate.kill();

// A screen reader's browser sends no images.
const argvFake = join(mkdtempSync(join(tmpdir(), 'bp-gate-')), 'argv.mjs');
writeFileSync(argvFake, 'console.log(JSON.stringify({ jsonrpc: "2.0", id: 0, result: { argv: process.argv.slice(2) } }));\n');
const sr2 = spawn('node', [here('../bp-gate.mjs'), '--assistive=screen-reader', '--', 'node', argvFake], { stdio: ['pipe', 'pipe', 'inherit'] });
let srOut = '';
sr2.stdout.on('data', (d) => { srOut += d; });
await new Promise((r) => sr2.on('exit', r));
if (!JSON.parse(srOut.split('\n')[0]).result.argv.includes('--image-responses=omit')) fail('screen reader images not omitted: ' + srOut);

// The run's config: device, zoom, display and eyesight in one file.
const out = mkdtempSync(join(tmpdir(), 'bp-pw-setup-'));
const setup = spawn('node', [here('../bp-pw-setup.mjs'), `--out=${out}`, '--device=desktop', '--assistive=zoom', '--vision=achromatopsia',
  '--display=reduced-motion', `--base=${here('../pw-config.json')}`], { stdio: ['ignore', 'inherit', 'inherit'] });
if (await new Promise((r) => setup.on('exit', r)) !== 0) fail('bp-pw-setup failed');
const cfg = JSON.parse(readFileSync(join(out, 'pw-config.json'), 'utf8'));
eq(cfg.browser.contextOptions, { viewport: { width: 640, height: 400 }, deviceScaleFactor: 2, reducedMotion: 'reduce' }, 'config context');
if (!cfg.browser.launchOptions.args.includes('--remote-debugging-port=9222')) fail('the base launch options were lost');
eq(readSettings(join(out, 'settings.json')), { device: 'desktop', assistive: 'zoom', vision: 'achromatopsia', display: 'reduced-motion' }, 'settings file');
eq(readSettings(join(out, 'missing.json')), {}, 'no settings file');

// The eyesight init page greys out a red box for achromatopsia.
const browser = await chromium.launch(process.env.BP_CHROMIUM ? { executablePath: process.env.BP_CHROMIUM } : {}); // BP_CHROMIUM: outside the image
const ctx = await browser.newContext(cfg.browser.contextOptions);
const pg = await ctx.newPage();
const init = createRequire(import.meta.url)(cfg.browser.initPage[0]);
await init.default({ page: pg });
await pg.setContent('<body style="margin:0"><div style="width:100px;height:100px;background:rgb(255,0,0)"></div></body>');
const shot = await pg.screenshot();
const px = await pg.evaluate(async (b64) => {
  const img = new Image(); img.src = 'data:image/png;base64,' + b64; await img.decode();
  const c = document.createElement('canvas'); c.width = img.width; c.height = img.height;
  const g = c.getContext('2d'); g.drawImage(img, 0, 0); return Array.from(g.getImageData(50, 50, 1, 1).data);
}, shot.toString('base64'));
await browser.close();
if (!(px[0] === px[1] && px[1] === px[2])) fail('achromatopsia left colour in the screenshot: ' + px);

console.log('a11y ok');
process.exit(0);
