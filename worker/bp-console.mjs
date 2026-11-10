// Records what the run's browser logs (console errors and warnings, uncaught
// exceptions, failed requests) over CDP and PUTs it to the director in
// batches. Logs go to stderr only; it never exits on its own.
const arg = process.argv.find((a) => a.startsWith('--console-url='));
const consoleUrl = arg ? arg.slice('--console-url='.length) : '';
if (!consoleUrl) {
  console.error('bp-console: --console-url is required');
  process.exit(2);
}
const log = (...a) => console.error(new Date().toISOString(), 'bp-console:', ...a);

const CDP = 'http://127.0.0.1:' + (process.env.BP_CDP_PORT || '9222');
const BATCH = 200; // the director refuses more in one PUT
const MAX_BUFFER = 1000;
const MAX_TEXT = 2000;
const MAX_REPEATS = 5; // the same message past this many times is dropped

const socks = new Map(); // target id -> WebSocket
const requests = new Map(); // CDP requestId -> {method, url}
const repeats = new Map();
let buffer = [];
let over = false;
let dropped = 0;
let lastErr = '';

function logOnce(msg) {
  if (msg !== lastErr) { lastErr = msg; log(msg); }
}

// cleanUrl drops the query and fragment: they can carry tokens from emails.
function cleanUrl(u) {
  if (!u) return '';
  try {
    const p = new URL(u);
    if (p.protocol !== 'http:' && p.protocol !== 'https:') return p.protocol;
    return p.origin + p.pathname;
  } catch {
    return '';
  }
}

function argText(a) {
  if (a.value !== undefined) return typeof a.value === 'string' ? a.value : JSON.stringify(a.value);
  return a.description || a.unserializableValue || a.type || '';
}

function toEvent(m) {
  const p = m.params || {};
  switch (m.method) {
    case 'Runtime.consoleAPICalled': {
      const level = p.type === 'error' || p.type === 'assert' ? 'error' : p.type === 'warning' ? 'warning' : '';
      if (!level) return null;
      return { level, kind: 'console', text: (p.args || []).map(argText).join(' '), url: p.stackTrace?.callFrames?.[0]?.url };
    }
    case 'Runtime.exceptionThrown': {
      const d = p.exceptionDetails || {};
      return { level: 'error', kind: 'exception', text: d.exception?.description || d.text || 'Uncaught exception',
        url: d.url || d.stackTrace?.callFrames?.[0]?.url };
    }
    case 'Log.entryAdded': {
      const e = p.entry || {};
      // Network entries repeat what the Network events already say.
      if ((e.level !== 'error' && e.level !== 'warning') || e.source === 'network') return null;
      return { level: e.level, kind: 'log', text: e.text || '', url: e.url };
    }
    case 'Network.requestWillBeSent':
      if (requests.size > 5000) requests.clear();
      requests.set(p.requestId, { method: p.request?.method || 'GET', url: p.request?.url || '' });
      return null;
    case 'Network.responseReceived': {
      const st = p.response?.status || 0;
      const req = requests.get(p.requestId);
      if (st < 400) return null;
      return { level: st >= 500 ? 'error' : 'warning', kind: 'network',
        text: `${req?.method || 'GET'} ${st} ${cleanUrl(p.response?.url)}`, url: p.response?.url };
    }
    case 'Network.loadingFailed': {
      if (p.canceled || p.errorText === 'net::ERR_ABORTED') return null;
      const req = requests.get(p.requestId);
      requests.delete(p.requestId);
      return { level: 'error', kind: 'network',
        text: `${req?.method || 'GET'} failed: ${p.blockedReason || p.errorText || 'unknown error'} ${cleanUrl(req?.url)}`, url: req?.url };
    }
    default:
      return null;
  }
}

function record(ev) {
  const text = String(ev.text || '').slice(0, MAX_TEXT);
  const url = cleanUrl(ev.url);
  const key = ev.level + ev.kind + text + url;
  const n = (repeats.get(key) || 0) + 1;
  if (repeats.size > 5000) repeats.clear();
  repeats.set(key, n);
  if (n > MAX_REPEATS) return;
  if (buffer.length >= MAX_BUFFER) { dropped++; return; }
  buffer.push({ at: new Date().toISOString(), level: ev.level, kind: ev.kind, text, ...(url ? { url } : {}) });
}

function attach(t) {
  const sock = new WebSocket(t.webSocketDebuggerUrl);
  socks.set(t.id, sock);
  let id = 0;
  sock.onopen = () => {
    for (const method of ['Runtime.enable', 'Log.enable', 'Network.enable']) sock.send(JSON.stringify({ id: ++id, method }));
  };
  sock.onmessage = (msg) => {
    let m;
    try { m = JSON.parse(msg.data); } catch { return; }
    if (!m.method) return;
    const ev = toEvent(m);
    if (ev) record(ev);
  };
  sock.onclose = () => { if (socks.get(t.id) === sock) socks.delete(t.id); };
  sock.onerror = () => {};
}

async function poll() {
  if (over) return;
  try {
    const res = await fetch(CDP + '/json/list', { signal: AbortSignal.timeout(5000) });
    const list = (await res.json()).filter((t) => t.type === 'page' && t.webSocketDebuggerUrl);
    for (const t of list) if (!socks.has(t.id)) attach(t);
    lastErr = '';
  } catch (e) {
    logOnce('cdp not ready: ' + (e.cause?.code || e.message));
  }
}

async function flush() {
  if (over || buffer.length === 0) return;
  const batch = buffer.slice(0, BATCH);
  try {
    const res = await fetch(consoleUrl, { method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(batch), signal: AbortSignal.timeout(5000) });
    if (res.status === 404) {
      over = true; // the run is over; nothing more will be taken
      log('run over, stopping');
      for (const s of socks.values()) try { s.close(); } catch {}
      return;
    }
    buffer = buffer.slice(batch.length);
    if (!res.ok) logOnce('director said ' + res.status + '; batch dropped');
    if (dropped) { log('dropped', dropped, 'events: buffer full'); dropped = 0; }
  } catch (e) {
    logOnce('send failed: ' + (e.cause?.code || e.message)); // keep the batch and retry
  }
}

setInterval(poll, 2000);
setInterval(flush, 2000);
poll();
