// Console: what a run's browser logged (console errors and warnings,
// uncaught exceptions, failed requests), recorded by the worker over CDP.
//
// Every text here comes from the site under test and is untrusted: render it
// only as text through htm/Preact.
import { useState, useEffect } from './vendor/hooks.module.js';
import { html, parseTime } from './lib.js';

const SHOWN = 50;
// BEFORE_BUG_MS is how far back from a bug report its console errors are looked for.
const BEFORE_BUG_MS = 30000;

// fenced reports a request the network fence stopped; the Blocked hosts card
// already lists those hosts, so the console only counts them.
export function fenced(e, blocked) {
  if (e.kind !== 'network' || !e.text.includes('ERR_TUNNEL_CONNECTION_FAILED') || !e.url) return false;
  try { return blocked.includes(new URL(e.url).hostname); } catch { return false; }
}

const KIND = { console: 'Console', exception: 'Exception', log: 'Browser', network: 'Network' };

function offset(at, started) {
  const t = parseTime(at), s = parseTime(started);
  if (!t || !s) return '';
  const secs = Math.max(0, Math.round((t - s) / 1000));
  const m = Math.floor(secs / 60), r = secs % 60;
  return `+${String(m).padStart(2, '0')}:${String(r).padStart(2, '0')}`;
}

function Row({ e, started, id, lit }) {
  return html`<li class=${'con ' + e.level + (lit ? ' lit' : '')} id=${id}>
    <span class="con-at">${offset(e.at, started)}</span>
    <span class=${'tag ' + (e.level === 'error' ? 'bad' : 'wait')}>${KIND[e.kind] || e.kind}</span>
    <span class="con-text">${e.text}${e.url && !e.text.includes(e.url) ? html`<span class="con-url">${e.url}</span>` : null}</span>
  </li>`;
}

// focus is {i, n}: the raw index of an event to show and highlight, and a
// counter so picking the same event again highlights it again.
export function ConsoleCard({ events: raw, blocked = [], started, active, focus }) {
  const [all, setAll] = useState(false);
  const [errorsOnly, setErrorsOnly] = useState(false);
  const [lit, setLit] = useState(-1);
  const events = raw.map((e, i) => ({ ...e, i })).filter((e) => !fenced(e, blocked));
  useEffect(() => {
    if (!focus) return;
    const e = raw[focus.i];
    if (!e) return;
    if (errorsOnly && e.level !== 'error') setErrorsOnly(false);
    setAll(true);
    setLit(focus.i);
    if (!focus.quiet) requestAnimationFrame(() => {
      const el = document.getElementById('con-' + focus.i);
      if (el) el.scrollIntoView({ block: 'center', behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' });
    });
    const t = setTimeout(() => setLit(-1), 2400);
    return () => clearTimeout(t);
  }, [focus]);
  const fencedCount = raw.length - events.length;
  const list = errorsOnly ? events.filter((e) => e.level === 'error') : events;
  const shown = all ? list : list.slice(0, SHOWN);
  const errors = events.filter((e) => e.level === 'error').length;
  return html`
    <div class="card" id="console">
      <div class="row between"><h2>Console</h2>
        ${events.length ? html`<label class="small muted con-filter"><input type="checkbox" checked=${errorsOnly}
          onChange=${(ev) => setErrorsOnly(ev.currentTarget.checked)} /> Errors only (${errors})</label>` : null}</div>
      ${events.length
        ? html`<ul class="console">${shown.map((e) => html`<${Row} key=${e.i} e=${e} started=${started} id=${'con-' + e.i} lit=${e.i === lit} />`)}</ul>
          ${list.length > shown.length ? html`<button type="button" class="btn small ghost" onClick=${() => setAll(true)}>Show all ${list.length}</button>` : null}`
        : html`<p class="muted small" style="margin:0">${active ? 'Nothing logged so far.' : 'The page logged no errors or warnings.'}</p>`}
      ${fencedCount ? html`<p class="muted small con-fenced">${fencedCount === 1 ? '1 request stopped by the network fence is' : `${fencedCount} requests stopped by the network fence are`} left out; see Blocked hosts.</p>` : null}
    </div>`;
}

// ConsoleBefore lists the console errors in the 30 seconds before a bug was
// reported, the likely cause of what the persona saw.
export function ConsoleBefore({ bug, events, blocked = [], started }) {
  const at = parseTime(bug.created);
  if (!at || !events || !events.length) return null;
  const near = events.filter((e) => {
    const t = parseTime(e.at);
    return e.level === 'error' && !fenced(e, blocked) && t && t <= at && at - t <= BEFORE_BUG_MS;
  });
  if (!near.length) return null;
  return html`<details class="con-before">
    <summary>${near.length === 1 ? '1 console error' : `${near.length} console errors`} just before this report</summary>
    <ul class="console">${near.map((e, i) => html`<${Row} key=${i} e=${e} started=${started} />`)}</ul>
  </details>`;
}
