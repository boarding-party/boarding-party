// Streams the Chromium screen (CDP screencast) to the director as JPEG PUTs.
// Logs go to stderr only; it never exits on its own.
const arg = process.argv.find((a) => a.startsWith('--live-url='));
const liveUrl = arg ? arg.slice('--live-url='.length) : '';
if (!liveUrl) {
  console.error('bp-screen: --live-url is required');
  process.exit(2);
}
let host = 'unknown';
try { host = new URL(liveUrl).host; } catch { console.error('bp-screen: bad live url'); process.exit(2); }
const log = (...a) => console.error(new Date().toISOString(), 'bp-screen:', ...a);

const CDP = 'http://127.0.0.1:' + (process.env.BP_CDP_PORT || '9222');
const FAST_MS = 250;
const SLOW_MS = 5000;

let ws = null;
let targetId = '';
let msgId = 0;
let pending = null; // newest screencast frame {data (base64), sessionId}; unacked until sent
let latest = null; // Buffer waiting to be sent (retry/keepalive)
let last = null; // last frame the director accepted (keepalive)
const seen = new Set();
let inflight = false;
let lastSend = 0;
let watchers = 0;
let over = false;
let lastErr = '';

function logOnce(msg) {
  if (msg !== lastErr) { lastErr = msg; log(msg); }
}

function attach(t) {
  if (ws) { try { ws.close(); } catch {} }
  const sock = new WebSocket(t.webSocketDebuggerUrl);
  ws = sock;
  targetId = t.id;
  sock.onopen = () => {
    log('attached to target', t.id);
    sock.send(JSON.stringify({ id: ++msgId, method: 'Page.startScreencast',
      params: { format: 'jpeg', quality: 60, maxWidth: 1280, maxHeight: 1280 } }));
  };
  sock.onmessage = (ev) => {
    let m;
    try { m = JSON.parse(ev.data); } catch { return; }
    if (m.method === 'Page.screencastFrame') {
      // Ack lazily (in tick/send): Chromium only encodes the next frame after an ack,
      // so this caps encoding at about one frame per send interval.
      pending = { data: m.params.data, sessionId: m.params.sessionId, sock };
    }
  };
  sock.onclose = () => { if (ws === sock) { ws = null; targetId = ''; } if (pending && pending.sock === sock) pending = null; };
  sock.onerror = () => {};
}

async function poll() {
  if (over) return;
  try {
    const res = await fetch(CDP + '/json/list', { signal: AbortSignal.timeout(5000) });
    const list = (await res.json()).filter((t) => t.type === 'page' && t.webSocketDebuggerUrl);
    if (list.length) {
      // /json/list order is not a contract: attach to the first page id not seen before;
      // if the attached page is gone, fall back to the first listed page.
      const fresh = list.find((t) => !seen.has(t.id));
      for (const t of list) seen.add(t.id);
      if (fresh) attach(fresh);
      else if (!ws || !list.some((t) => t.id === targetId)) attach(list[0]);
    }
    lastErr = '';
  } catch (e) {
    logOnce('cdp not ready: ' + (e.cause?.code || e.message));
  }
}

function ackPending() {
  const p = pending;
  pending = null;
  if (!p) return null;
  try {
    if (p.sock.readyState === 1) {
      p.sock.send(JSON.stringify({ id: ++msgId, method: 'Page.screencastFrameAck', params: { sessionId: p.sessionId } }));
    }
  } catch {}
  return Buffer.from(p.data, 'base64');
}

function stop() {
  over = true;
  pending = null;
  latest = null;
  if (ws) { try { ws.close(); } catch {} ws = null; }
}

async function send() {
  const body = latest;
  latest = null;
  inflight = true;
  lastSend = Date.now();
  try {
    const res = await fetch(liveUrl, { method: 'PUT', headers: { 'Content-Type': 'image/jpeg' }, body, signal: AbortSignal.timeout(5000) });
    await res.arrayBuffer().catch(() => {});
    if (res.status === 404 || res.status === 410) {
      log('director says run is over (' + res.status + '); stopping');
      stop();
    } else if (res.status === 413) {
      logOnce('director ' + host + ' rejected a frame as too large; dropping it');
    } else if (res.status >= 400) {
      logOnce('director ' + host + ' answered ' + res.status);
      if (!latest) latest = body;
    } else {
      lastErr = '';
      last = body;
      watchers = parseInt(res.headers.get('x-bp-watchers') || '0', 10) || 0;
    }
  } catch (e) {
    logOnce('send to ' + host + ' failed: ' + (e.cause?.code || e.message));
    if (!latest) latest = body;
  } finally {
    inflight = false;
  }
}

function tick() {
  if (over || inflight) return;
  const gap = watchers > 0 ? FAST_MS : SLOW_MS;
  if (Date.now() - lastSend < gap) return;
  // a fresh frame supersedes a queued retry
  if (pending) latest = ackPending();
  else if (!latest) {
    // idle page: re-send the last good frame so the director keeps it
    if (!last || Date.now() - lastSend < SLOW_MS) return;
    latest = last;
  }
  send();
}

log('starting, director host', host);
poll();
setInterval(poll, 2000);
setInterval(tick, 50);
process.on('uncaughtException', (e) => log('error', e.message));
process.on('unhandledRejection', (e) => log('error', String(e?.message || e)));
