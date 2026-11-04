// Token usage reporter: sums the model usage Claude Code writes to its
// transcripts and sends the running total to the director every few seconds
// while it changes. Started once per pod by bp-browser, like bp-screen.
// The URL carries the run token: it is never logged.
import { open, readdir, stat } from "node:fs/promises";
import { join } from "node:path";

const arg = process.argv.find((a) => a.startsWith("--usage-url="));
const url = arg && arg.slice("--usage-url=".length);
if (!url) process.exit(0);
const root = join(process.env.CLAUDE_CONFIG_DIR || join(process.env.HOME || "/workspace", ".claude"), "projects");

const files = new Map(); // path -> { offset, rest }
const seen = new Set(); // message ids already counted: one message spans several lines
const total = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, turns: 0 };
let sent = "";

async function transcripts(dir) {
  let out = [];
  for (const e of await readdir(dir, { withFileTypes: true }).catch(() => [])) {
    const p = join(dir, e.name);
    if (e.isDirectory()) out = out.concat(await transcripts(p));
    else if (e.name.endsWith(".jsonl")) out.push(p);
  }
  return out;
}

function count(line) {
  let m;
  try {
    m = JSON.parse(line);
  } catch {
    return;
  }
  const u = m && m.type === "assistant" && m.message && m.message.usage;
  if (!u) return;
  const id = m.message.id || m.requestId || m.uuid;
  if (seen.has(id)) return;
  seen.add(id);
  total.input += u.input_tokens || 0;
  total.output += u.output_tokens || 0;
  total.cacheRead += u.cache_read_input_tokens || 0;
  total.cacheWrite += u.cache_creation_input_tokens || 0;
  total.turns += 1;
}

async function read(path) {
  const f = files.get(path) || { offset: 0, rest: "" };
  const size = (await stat(path)).size;
  if (size <= f.offset) return;
  const fh = await open(path, "r");
  try {
    const buf = Buffer.alloc(size - f.offset);
    await fh.read(buf, 0, buf.length, f.offset);
    f.offset = size;
    const lines = (f.rest + buf.toString("utf8")).split("\n");
    f.rest = lines.pop(); // a line still being written
    lines.forEach(count);
  } finally {
    await fh.close();
    files.set(path, f);
  }
}

async function tick() {
  for (const p of await transcripts(root)) await read(p).catch(() => {});
  const body = JSON.stringify(total);
  if (body === sent || total.turns === 0) return;
  const res = await fetch(url, { method: "PUT", headers: { "Content-Type": "application/json" }, body }).catch(() => null);
  if (res && res.ok) sent = body;
  else if (res && res.status === 404) process.exit(0); // the run is over
  else console.error("bp-usage: report failed" + (res ? " (" + res.status + ")" : ""));
}

for (;;) {
  await tick();
  await new Promise((r) => setTimeout(r, 2000));
}
