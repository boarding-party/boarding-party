// Studio: the run page as a session replay. It fills the window: a slim top
// bar, the stage (player and timeline) fixed in place, and tabbed panels
// beside it, each scrolling on its own. The page itself never scrolls on a
// wide screen; on a narrow one the panels stack under the stage. The owner
// drags the line between stage and panels to share the width.
import { useRef, useState, useEffect } from './vendor/hooks.module.js';
import { html } from './lib.js';
import { clampPanel, PANEL_MIN } from './studio-split.js';

const WIDTH_KEY = 'bp.studio.panel';
const STEP = 32; // one arrow key press

function remembered() {
  try { return clampPanel(parseFloat(localStorage.getItem(WIDTH_KEY)), window.innerWidth); } catch (_) { return null; }
}
function remember(w) {
  try { localStorage.setItem(WIDTH_KEY, String(Math.round(w))); } catch (_) { /* this browser keeps nothing */ }
}

// Split is the line between stage and panels: drag it, or focus it and use
// the arrow keys. It reports the panel's width in pixels.
function Split({ panel, width, onWidth }) {
  const drag = (e) => {
    if (e.button !== 0) return;
    e.preventDefault();
    const el = e.currentTarget;
    el.setPointerCapture(e.pointerId);
    const move = (m) => onWidth(clampPanel(window.innerWidth - m.clientX, window.innerWidth));
    const up = () => { el.removeEventListener('pointermove', move); el.removeEventListener('pointerup', up); el.removeEventListener('pointercancel', up); };
    el.addEventListener('pointermove', move);
    el.addEventListener('pointerup', up);
    el.addEventListener('pointercancel', up);
  };
  const key = (e) => {
    const now = width ?? (panel.current ? panel.current.offsetWidth : PANEL_MIN);
    const next = e.key === 'ArrowLeft' ? now + STEP : e.key === 'ArrowRight' ? now - STEP : e.key === 'Home' ? Infinity : e.key === 'End' ? 0 : null;
    if (next === null) return;
    e.preventDefault();
    onWidth(clampPanel(next, window.innerWidth));
  };
  return html`<div class="studio-split" role="separator" aria-orientation="vertical" aria-label="Resize the panels"
    aria-valuenow=${width == null ? null : Math.round(width)} tabindex="0" title="Drag to resize" onPointerDown=${drag} onKeyDown=${key}
    onDblClick=${() => onWidth(null)}></div>`;
}

// tabs: [{id, label, count?, bad?, body}]. tab is the open tab's id; onTab
// opens another. stage is null for a run with no browser (a lead).
export function Studio({ top, banners, stage, tabs, tab, onTab }) {
  const list = useRef(null);
  const panel = useRef(null);
  const [width, setWidth] = useState(remembered);
  const resize = (w) => { setWidth(w); if (w == null) { try { localStorage.removeItem(WIDTH_KEY); } catch (_) { /* nothing kept */ } } else remember(w); };
  useEffect(() => {
    if (width == null) return undefined;
    const fit = () => setWidth((w) => (w == null ? w : clampPanel(w, window.innerWidth)));
    window.addEventListener('resize', fit);
    return () => window.removeEventListener('resize', fit);
  }, [width == null]);
  const open = tabs.find((t) => t.id === tab) || tabs[0];
  const key = (e) => {
    const i = tabs.indexOf(open);
    const next = e.key === 'ArrowRight' ? i + 1 : e.key === 'ArrowLeft' ? i - 1 : e.key === 'Home' ? 0 : e.key === 'End' ? tabs.length - 1 : null;
    if (next === null) return;
    e.preventDefault();
    const t = tabs[(next + tabs.length) % tabs.length];
    onTab(t.id);
    requestAnimationFrame(() => list.current && list.current.querySelector(`#tab-${t.id}`)?.focus());
  };
  return html`
    <section class=${'studio' + (stage ? '' : ' nostage')}>
      ${top}
      <div class="studio-banners">${banners}</div>
      <div class="studio-body" style=${stage && width != null ? { '--panel-w': width + 'px' } : null}>
        ${stage ? html`<div class="studio-stage">${stage}</div>` : null}
        <div class="studio-panel" ref=${panel}>
          ${stage ? html`<${Split} panel=${panel} width=${width} onWidth=${resize} />` : null}
          <div class="studio-tabs" role="tablist" aria-label="Run details" ref=${list} onKeyDown=${key}>
            ${tabs.map((t) => html`<button key=${t.id} id=${'tab-' + t.id} type="button" role="tab"
              aria-selected=${t === open} aria-controls=${'pane-' + t.id} tabindex=${t === open ? 0 : -1}
              class=${t === open ? 'on' : ''} onClick=${() => onTab(t.id)}>${t.label}${t.count != null
                ? html` <span class=${'tabcount' + (t.bad ? ' bad' : '')}>${t.count}</span>` : null}</button>`)}
          </div>
          <div class="studio-pane" role="tabpanel" id=${'pane-' + open.id} aria-labelledby=${'tab-' + open.id} tabindex="0">
            ${open.body}
          </div>
        </div>
      </div>
    </section>`;
}
