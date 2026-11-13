// bp-gate sits between the agent and Playwright MCP on stdio and holds the
// browser to the persona's settings: it hides the tools the persona cannot
// use from tools/list and refuses calls to them, with a reason the agent can
// act on, and adds the tools such a persona uses instead.
//
//   bp-gate --assistive=KEY -- playwright-mcp ARGS...
//
// For a screen reader it also passes --image-responses=omit, so no tool
// shows the agent the page.
//
// MCP on stdio is one JSON message per line. Nothing but protocol may reach
// stdout; logs go to stderr.
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';
import { toolRules } from './bp-a11y.mjs';

const argv = process.argv.slice(2);
const sep = argv.indexOf('--');
if (sep < 0 || sep === argv.length - 1) {
  console.error('bp-gate: usage: bp-gate [--assistive=KEY] -- COMMAND ARGS...');
  process.exit(2);
}
const assistive = (argv.slice(0, sep).find((a) => a.startsWith('--assistive=')) || '').slice('--assistive='.length);
const rules = toolRules(assistive);
const blocked = new Set(rules.block);

const ADDED = {
  browser_type_focused: {
    name: 'browser_type_focused',
    description: 'Type text into the element that has keyboard focus, key by key at a person\'s pace. Move focus first with browser_press_key (Tab, Shift+Tab, arrow keys).',
    inputSchema: {
      type: 'object',
      properties: {
        text: { type: 'string', description: 'Text to type' },
        submit: { type: 'boolean', description: 'Press Enter after typing' },
      },
      required: ['text'],
      additionalProperties: false,
    },
  },
};
const added = rules.add.map((n) => ADDED[n]);

// rewrite turns a call to an added tool into the Playwright MCP call that
// does it, or returns an error text. Typing keeps the 60-180 ms between keys
// that every other persona's typing has (humanise.mjs).
function rewrite(name, args) {
  if (name === 'browser_type_focused') {
    if (typeof args?.text !== 'string') return { error: 'text must be a string' };
    const enter = args.submit ? ` await page.keyboard.press('Enter');` : '';
    return { name: 'browser_run_code_unsafe', arguments: { code: `async (page) => { const delay = () => 60 + Math.random() * 120;`
      + ` for (const key of ${JSON.stringify(args.text)}) { await page.keyboard.type(key); await page.waitForTimeout(delay()); }${enter} }` } };
  }
  return null;
}

// sneaks reports a call that would do what a blocked tool does: a
// javascript: URL runs script in the page, so it is refused wherever script
// is.
function sneaks(name, args) {
  return name === 'browser_navigate' && blocked.has('browser_evaluate') && /^\s*javascript:/i.test(String(args?.url ?? ''));
}

const childArgs = argv.slice(sep + 2);
if (rules.imageResponses) childArgs.push(`--image-responses=${rules.imageResponses}`);
const child = spawn(argv[sep + 1], childArgs, { stdio: ['pipe', 'pipe', 'inherit'] });
child.on('error', (e) => { console.error('bp-gate: could not start the browser server:', e.message); process.exit(1); });
// Exit once the child's last reply is through, with its exit code.
let exitCode = null;
child.on('exit', (code, signal) => { exitCode = code ?? (signal ? 1 : 0); });
child.stdin.on('error', () => {}); // the child is gone; its exit ends us

const toClient = (m) => process.stdout.write(JSON.stringify(m) + '\n');
const toServer = (m) => child.stdin.write(JSON.stringify(m) + '\n');
const refuse = (id, text) => toClient({ jsonrpc: '2.0', id, result: { content: [{ type: 'text', text }], isError: true } });

const lists = new Set(); // ids of the client's tools/list requests

createInterface({ input: process.stdin }).on('line', (line) => {
  let m;
  try { m = JSON.parse(line); } catch { return child.stdin.write(line + '\n'); }
  if (m.method === 'tools/list' && m.id !== undefined) lists.add(m.id);
  if (m.method === 'tools/call' && m.id !== undefined) {
    const name = m.params?.name;
    if (blocked.has(name)) return refuse(m.id, `${name} is not available: ${rules.why}.`);
    if (sneaks(name, m.params?.arguments)) return refuse(m.id, `javascript: URLs are not available: ${rules.why}.`);
    if (added.some((t) => t.name === name)) {
      const r = rewrite(name, m.params?.arguments);
      if (r.error) return refuse(m.id, `${name}: ${r.error}.`);
      m = { ...m, params: { ...m.params, name: r.name, arguments: r.arguments } };
    }
  }
  toServer(m);
}).on('close', () => child.stdin.end());

createInterface({ input: child.stdout }).on('line', (line) => {
  let m;
  try { m = JSON.parse(line); } catch { return process.stdout.write(line + '\n'); }
  if (m.method === undefined && m.id !== undefined && lists.delete(m.id) && Array.isArray(m.result?.tools)) {
    m.result.tools = [...m.result.tools.filter((t) => !blocked.has(t.name)), ...added];
  }
  toClient(m);
}).on('close', () => {
  if (exitCode !== null) process.exit(exitCode);
  child.on('exit', (code, signal) => process.exit(code ?? (signal ? 1 : 0)));
});
