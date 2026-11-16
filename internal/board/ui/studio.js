// Studio: the run page as a session replay. It fills the window: a slim top
// bar, the stage (player and timeline) fixed in place, and tabbed panels
// beside it, each scrolling on its own. The page itself never scrolls on a
// wide screen; on a narrow one the panels stack under the stage.
import { useRef } from './vendor/hooks.module.js';
import { html } from './lib.js';

// tabs: [{id, label, count?, bad?, body}]. tab is the open tab's id; onTab
// opens another. stage is null for a run with no browser (a lead).
export function Studio({ top, banners, stage, tabs, tab, onTab }) {
  const list = useRef(null);
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
      <div class="studio-body">
        ${stage ? html`<div class="studio-stage">${stage}</div>` : null}
        <div class="studio-panel">
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
