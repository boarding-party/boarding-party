// Timeline: the run on its own clock, from start to finish (or now, while it
// runs). One lane pins the bugs the persona reported, one ticks the console
// errors and warnings the page logged. Every mark is a button: a bug jumps
// the video to its time and scrolls to the report, a console mark scrolls to
// that line.
import { useState, useEffect } from './vendor/hooks.module.js';
import { html, parseTime, SEVERITY_TAG } from './lib.js';
import { fenced } from './console.js';

const MAX_MARKS = 400;
const STEPS = [5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600];

function clock(secs) {
  const m = Math.floor(secs / 60), s = Math.floor(secs % 60);
  return `${m}:${String(s).padStart(2, '0')}`;
}

// ticks returns about six round times across span seconds.
function ticks(span) {
  const step = STEPS.find((s) => span / s <= 6) || 3600;
  const out = [];
  // Leave room for the end label.
  for (let t = 0; t <= span * 0.88; t += step) out.push(t);
  return out;
}

// frames (Unix ms) are the screen frames the director kept; with them the
// track scrubs: click or drag to see the screen at that moment (onScrub gets
// Unix ms), and scrub (Unix ms, or null for live) draws the playhead.
export function Timeline({ run, bugs, events, blocked, active, onBug, onEvent, frames, scrub, onScrub }) {
  const [now, setNow] = useState(Date.now());
  const [tip, setTip] = useState(null); // {x, text}
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [active]);
  const t0 = parseTime(run.started);
  if (!t0) return null;
  const t1 = parseTime(run.finished) || new Date(now);
  const span = Math.max(1, (t1 - t0) / 1000);
  const at = (iso) => {
    const t = parseTime(iso);
    if (!t) return null;
    const s = (t - t0) / 1000;
    return s >= 0 && s <= span + 1 ? Math.min(s, span) : null;
  };
  const pct = (s) => `${(s / span) * 100}%`;

  // Bugs reported together (often at the end of a run) sit side by side
  // around their shared time instead of on top of each other.
  const bugMarks = bugs.map((b, i) => ({ b, i, s: at(b.created) })).filter((m) => m.s !== null);
  const groups = [];
  for (const m of [...bugMarks].sort((a, b) => a.s - b.s)) {
    const g = groups[groups.length - 1];
    if (g && m.s - g[0].s <= span * 0.03) g.push(m); else groups.push([m]);
  }
  for (const g of groups) {
    const mid = g.reduce((t, m) => t + m.s, 0) / g.length;
    g.forEach((m, k) => {
      const shift = (k - (g.length - 1) / 2) * 20;
      m.left = `clamp(9px, calc(${(mid / span) * 100}% + ${shift}px), calc(100% - 9px))`;
    });
  }
  const conMarks = [];
  (events || []).forEach((e, i) => {
    if (conMarks.length >= MAX_MARKS || fenced(e, blocked)) return;
    const s = at(e.at);
    if (s !== null) conMarks.push({ e, i, s });
  });
  const errors = conMarks.filter((m) => m.e.level === 'error').length;
  // Before the browser's first logged activity the run was starting: a
  // machine, the images, the agent. Shade it so the gap reads as setup.
  const first = (events || []).reduce((min, e) => { const s = at(e.at); return s !== null && (min === null || s < min) ? s : min; }, null);

  const show = (s, text) => (ev) => {
    const box = ev.currentTarget.closest('.tl-lanes').getBoundingClientRect();
    const r = ev.currentTarget.getBoundingClientRect();
    setTip({ x: Math.min(Math.max(r.left + r.width / 2 - box.left, 80), box.width - 80), text: `${clock(s)}  ${text}` });
  };
  const hide = () => setTip(null);
  const canScrub = !!(frames && frames.length && onScrub);
  // Clicking or dragging on the lanes (not on a mark) scrubs to that time.
  const drag = (e) => {
    if (!canScrub || e.button !== 0 || e.target.closest('button')) return;
    const track = e.currentTarget.querySelector('.tl-track').getBoundingClientRect();
    const go = (x) => {
      const f = Math.min(1, Math.max(0, (x - track.left) / track.width));
      onScrub(t0.getTime() + f * span * 1000);
    };
    go(e.clientX);
    const move = (ev) => go(ev.clientX);
    const up = () => { removeEventListener('pointermove', move); removeEventListener('pointerup', up); };
    addEventListener('pointermove', move);
    addEventListener('pointerup', up);
  };
  const head = scrub != null ? Math.min(span, Math.max(0, (scrub - t0) / 1000)) : null;

  return html`
    <div class="tl" aria-label="Run timeline">
      <div class="tl-axis" aria-hidden="true">
        ${ticks(span).map((t) => html`<span key=${t} style=${{ left: pct(t) }}>${clock(t)}</span>`)}
        <span class="tl-end">${active ? 'now' : clock(span)}</span>
      </div>
      <div class=${'tl-lanes' + (canScrub ? ' scrubs' : '')} onMouseLeave=${hide} onPointerDown=${drag}>
        ${first !== null && first / span > 0.04 ? html`<div class="tl-setup" style=${{ width: `calc((100% - var(--tl-name)) * ${first / span})` }}
          title=${`Starting: ${clock(first)} before the browser's first activity`}><span>Starting</span></div>` : null}
        <div class="tl-lane">
          <span class="tl-name">Bugs <b>${bugs.length}</b></span>
          <div class="tl-track">
            ${bugMarks.map((m) => { const { b, i, s } = m; return html`
              <button key=${b.id || i} type="button" class=${'tl-bug ' + (SEVERITY_TAG.get((b.severity || '').toLowerCase()) || 'idle')}
                style=${{ left: m.left }} aria-label=${`Bug ${i + 1} at ${clock(s)}: ${b.summary}`}
                onMouseEnter=${show(s, b.summary)} onFocus=${show(s, b.summary)} onBlur=${hide}
                onClick=${() => onBug(i)}>${i + 1}</button>`; })}
          </div>
        </div>
        ${events ? html`<div class="tl-lane">
          <span class="tl-name">Console <b class=${errors ? 'bad' : ''}>${errors}</b></span>
          <div class="tl-track">
            ${conMarks.map(({ e, i, s }) => html`
              <button key=${i} type="button" class=${'tl-con ' + (e.level === 'error' ? 'bad' : 'wait')}
                style=${{ left: pct(s) }} aria-label=${`Console ${e.level} at ${clock(s)}`}
                onMouseEnter=${show(s, e.text)} onFocus=${show(s, e.text)} onBlur=${hide}
                onClick=${() => onEvent(i)}></button>`)}
          </div>
        </div>` : null}
        ${active && head === null ? html`<span class="tl-now" aria-hidden="true"></span>` : null}
        ${head !== null ? html`<span class="tl-head" aria-hidden="true" style=${{ left: `calc(var(--tl-name) + (100% - var(--tl-name)) * ${head / span})` }}><b>${clock(head)}</b></span>` : null}
        ${tip ? html`<div class="tl-tip" role="status" style=${{ left: tip.x + 'px' }}>${tip.text}</div>` : null}
      </div>
    </div>`;
}
