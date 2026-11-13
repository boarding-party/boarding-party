// bp-pw-setup writes the run's Playwright MCP config: the base config
// (pw-config.json) with the browser context for the persona's device and
// accessibility settings and the project's language and time zone, and an
// init page that applies the persona's
// eyesight to every tab. It also records the settings, so bp-replay --self
// renders the journey the same way.
//
//   bp-pw-setup --out=DIR [--base=FILE] [--device=KEY] [--assistive=KEY] [--vision=KEY] [--display=KEY]
//               [--locale=TAG] [--timezone=ZONE]
//
// Writes DIR/pw-config.json, DIR/settings.json and, for an eyesight setting,
// DIR/bp-vision.cjs. Prints nothing on stdout.
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { join } from 'node:path';
import { contextOptions, visionType, SETTINGS } from './bp-a11y.mjs';

const opt = {};
for (const a of process.argv.slice(2)) {
  const m = /^--([a-z]+)=(.*)$/.exec(a);
  if (m) opt[m[1]] = m[2];
}
if (!opt.out) { console.error('bp-pw-setup: --out is required'); process.exit(2); }

const require = createRequire(process.env.BP_PW_ROOT || '/usr/lib/node_modules/@playwright/mcp/');
const { devices } = require('playwright-core');

let cfg = {};
try { cfg = JSON.parse(readFileSync(opt.base || '/opt/bp/pw-config.json', 'utf8')); } catch (e) { console.error('bp-pw-setup: no base config:', e.message); }
const settings = Object.fromEntries(SETTINGS.map((k) => [k, opt[k] || '']));
cfg.browser = { ...(cfg.browser || {}), contextOptions: { ...(cfg.browser?.contextOptions || {}), ...contextOptions(settings, devices) } };

mkdirSync(opt.out, { recursive: true });
const type = visionType(settings.vision);
if (type) {
  // Playwright MCP requires each init page and calls its default export
  // with every tab it opens.
  const init = join(opt.out, 'bp-vision.cjs');
  writeFileSync(init, `exports.default = async ({ page }) => {
  const s = await page.context().newCDPSession(page);
  await s.send('Emulation.setEmulatedVisionDeficiency', { type: ${JSON.stringify(type)} });
};
`);
  cfg.browser.initPage = [...(cfg.browser.initPage || []), init];
}
writeFileSync(join(opt.out, 'pw-config.json'), JSON.stringify(cfg));
writeFileSync(join(opt.out, 'settings.json'), JSON.stringify(settings));
