// Network: the requests the page made, from the run's trace summary
// (GET api/runs/{id}/trace). A row opens its headers and seeks the player to
// when the request started. URLs arrive without queries; credential headers
// arrive redacted. Everything here is the site's text: render it as text.
import { useState } from './vendor/hooks.module.js';
import { html, parseTime } from './lib.js';

const clock = (secs) => `${Math.floor(secs / 60)}:${String(Math.floor(secs % 60)).padStart(2, '0')}`;
const NS_PER_MS = 1e6; // Go's time.Duration is nanoseconds

function fmtMs(ns) {
  const ms = ns / NS_PER_MS;
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${Math.round(ms)} ms`;
}

function split(url) {
  try {
    const u = new URL(url);
    return { host: u.host, path: u.pathname || '/' };
  } catch (_) {
    return { host: '', path: url };
  }
}

const bad = (r) => r.failed || r.status >= 400;

// trace is {state: 'none'|'live'|'loading'|'error'|'ok', summary, error}.
export function NetworkPanel({ trace, started, onSeek }) {
  const [open, setOpen] = useState(-1);
  const [badOnly, setBadOnly] = useState(false);
  if (trace.state !== 'ok') {
    return html`<p class="muted small pane-empty">${{
      none: 'This run has no trace, so there are no requests to show.',
      live: 'Requests appear here when the run ends and its trace arrives.',
      loading: 'Reading the trace…',
      error: `The trace could not be read: ${trace.error}`,
    }[trace.state]}</p>`;
  }
  const t0 = parseTime(started);
  const all = trace.summary.network || [];
  const failed = all.filter(bad).length;
  const rows = all.map((r, i) => ({ r, i })).filter(({ r }) => !badOnly || bad(r));
  return html`
    <div class="net">
      <div class="row between net-head">
        <span class="small muted">${all.length === 1 ? '1 request' : `${all.length} requests`}</span>
        ${failed ? html`<label class="small muted"><input type="checkbox" checked=${badOnly} onChange=${(e) => setBadOnly(e.currentTarget.checked)} /> Failed only (${failed})</label>` : null}
      </div>
      ${rows.length ? html`<ul class="netlist">${rows.map(({ r, i }) => {
        const { host, path } = split(r.url);
        const at = Date.parse(r.start);
        return html`<li key=${i} class=${(bad(r) ? 'bad ' : '') + (open === i ? 'open' : '')}>
          <button type="button" aria-expanded=${open === i} onClick=${() => { setOpen(open === i ? -1 : i); if (Number.isFinite(at)) onSeek(at); }}>
            <span class="net-status">${r.failed && !r.status ? 'failed' : r.status}</span>
            <span class="net-method">${r.method}</span>
            <span class="net-url" title=${r.url}><span class="net-path">${path}</span><span class="net-host">${host}</span></span>
            <span class="net-meta">${t0 && Number.isFinite(at) ? clock(Math.max(0, (at - t0) / 1000)) : ''} · ${fmtMs(r.duration || 0)}</span>
          </button>
          ${open === i ? html`<div class="net-detail">
            <p class="net-full">${r.url}</p>
            ${r.type ? html`<p class="small muted">${r.type}</p>` : null}
            <${Headers} title="Request headers" h=${r.requestHeaders} />
            <${Headers} title="Response headers" h=${r.responseHeaders} />
          </div>` : null}
        </li>`;
      })}</ul>` : html`<p class="muted small pane-empty">No requests to show.</p>`}
    </div>`;
}

function Headers({ title, h }) {
  const rows = Object.entries(h || {}).sort((a, b) => a[0].localeCompare(b[0]));
  if (!rows.length) return null;
  return html`<h3>${title}</h3>
    <dl class="facts wide net-headers">${rows.map(([k, v]) => html`<dt key=${'k' + k}>${k}</dt><dd key=${'v' + k}>${v}</dd>`)}</dl>`;
}
