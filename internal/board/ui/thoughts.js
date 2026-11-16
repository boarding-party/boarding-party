// Thoughts: what the persona said and did, beside the recording. The
// narration comes from the agent's own transcript (runs.NarrationName); a
// replay has no model, so its journey's steps stand in. Every row seeks the
// player; the row at the playhead is lit and kept in view.
//
// Narration text is untrusted (it can quote the site): render it as text.
import { useEffect, useRef } from './vendor/hooks.module.js';
import { html, parseTime } from './lib.js';
import { nearest } from './studio-time.js';

const clock = (secs) => `${Math.floor(secs / 60)}:${String(Math.floor(secs % 60)).padStart(2, '0')}`;

// entries: [{at, kind: 'thought'|'action', text}], oldest first. playhead is
// Unix ms or null; onSeek gets Unix ms.
export function ThoughtsPanel({ entries, started, playhead, onSeek, active, replay }) {
  const list = useRef(null);
  const t0 = parseTime(started);
  const times = entries.map((e) => Date.parse(e.at));
  const here = playhead != null && entries.length && times[0] <= playhead ? nearest(times, playhead, (t) => t) : -1;
  useEffect(() => {
    if (here < 0 || !list.current) return;
    const el = list.current.children[here];
    if (el) el.scrollIntoView({ block: 'nearest', behavior: 'auto' });
  }, [here]);
  if (!entries.length) {
    return html`<p class="muted small pane-empty">${active ? 'Nothing said yet. Thoughts appear here as the persona works.'
      : replay ? 'This replay has no steps to show yet. They come from the trace when the replay ends.'
      : 'No thoughts were recorded for this run.'}</p>`;
  }
  return html`
    <ol class="thoughts" ref=${list}>
      ${entries.map((e, i) => html`
        <li key=${i} class=${'th-' + e.kind + (i === here ? ' here' : '')}>
          <button type="button" onClick=${() => onSeek(times[i])} aria-current=${i === here ? 'true' : null}>
            <span class="th-time">${t0 && Number.isFinite(times[i]) ? clock(Math.max(0, (times[i] - t0) / 1000)) : ''}</span>
            <span class="th-text">${e.text}</span>
          </button>
        </li>`)}
    </ol>`;
}

// Caption is the thought at the playhead, over the bottom of the player.
export function Caption({ entry }) {
  if (!entry) return null;
  return html`<div class="caption" aria-live="polite"><p>${entry.text}</p></div>`;
}
