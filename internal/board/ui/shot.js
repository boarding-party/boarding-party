// Shot: a screenshot viewer for pages of any height. Full-page captures can
// be 5000px tall, so the picture sits in a fixed-height window that scrolls,
// with a rail beside it showing the whole page and where the window is.
// "Full size" opens the same viewer over the page at the image's own width.
import { useState, useRef, useEffect, useCallback } from './vendor/hooks.module.js';
import { html } from './lib.js';

const RAIL = 44; // px wide

function useSize(img) {
  const [size, setSize] = useState(null);
  const onLoad = (e) => { const i = e.currentTarget; setSize({ w: i.naturalWidth, h: i.naturalHeight }); };
  return [size, onLoad];
}

// Viewer is the window plus rail. height is the window's height in px or a
// CSS length; natural shows the image at its own width (capped to the box).
function Viewer({ src, alt, height, natural, size, onLoad, onError }) {
  const win = useRef(null);
  const [view, setView] = useState({ top: 0, frac: 1, ch: 0 });
  const measure = useCallback(() => {
    const w = win.current;
    if (!w || !w.scrollHeight) return;
    setView({ top: w.scrollTop / w.scrollHeight, frac: Math.min(1, w.clientHeight / w.scrollHeight), ch: w.clientHeight });
  }, []);
  useEffect(() => {
    const w = win.current;
    if (!w) return;
    const ro = new ResizeObserver(measure);
    ro.observe(w);
    return () => ro.disconnect();
  }, [measure]);
  useEffect(measure, [size, measure]);

  const tall = view.frac < 0.98;
  // Dragging or clicking the rail centres the window on that point.
  const drag = (e) => {
    const rail = e.currentTarget.querySelector(".shot-map").getBoundingClientRect();
    e.preventDefault();
    const go = (y) => {
      const w = win.current;
      const f = Math.min(1, Math.max(0, (y - rail.top) / rail.height));
      w.scrollTop = f * w.scrollHeight - w.clientHeight / 2;
    };
    go(e.clientY);
    const move = (ev) => go(ev.clientY);
    const up = () => { removeEventListener('pointermove', move); removeEventListener('pointerup', up); };
    addEventListener('pointermove', move);
    addEventListener('pointerup', up);
  };
  const key = (e) => {
    const w = win.current, step = w.clientHeight * 0.9;
    if (e.key === 'ArrowDown' || e.key === 'PageDown') { w.scrollTop += step; e.preventDefault(); }
    if (e.key === 'ArrowUp' || e.key === 'PageUp') { w.scrollTop -= step; e.preventDefault(); }
    if (e.key === 'Home') { w.scrollTop = 0; e.preventDefault(); }
    if (e.key === 'End') { w.scrollTop = w.scrollHeight; e.preventDefault(); }
  };
  // The rail's picture keeps the page's proportions inside the rail: as wide
  // as the rail for a short page, narrower for a very tall one.
  const ratio = size ? size.h / size.w : 1;
  const mapW = Math.min(natural ? 64 : RAIL, view.ch / ratio), mapH = mapW * ratio;
  return html`
    <div class=${'shot-view' + (tall ? ' tall' : '')} style=${{ '--h': typeof height === 'number' ? height + 'px' : height }}>
      <div class=${'shot-win' + (natural ? ' natural' : '')} ref=${win} onScroll=${measure} tabindex="0" aria-label=${alt + ' (scrolls)'}>
        <img src=${src} alt=${alt} onLoad=${(e) => { onLoad(e); measure(); }} onError=${onError} decoding="async" />
      </div>
      ${tall && size ? html`
        <div class="shot-rail" onPointerDown=${drag} onKeyDown=${key} role="scrollbar" tabindex="0"
          aria-orientation="vertical" aria-valuenow=${Math.round(view.top * 100)} aria-valuemin="0" aria-valuemax="100"
          aria-label="Position in the screenshot">
          <div class="shot-map" style=${{ width: mapW + 'px', height: mapH + 'px' }}>
            <img src=${src} alt="" draggable="false" />
            <span class="shot-lens" style=${{ top: view.top * 100 + '%', height: view.frac * 100 + '%' }}></span>
          </div>
        </div>` : null}
    </div>`;
}

export function Shot({ src, alt, height = 300, href }) {
  const [size, onLoad] = useSize();
  const [missing, setMissing] = useState(false);
  const box = useRef(null);
  if (missing) return null;
  const open = () => box.current && box.current.showModal();
  return html`
    <figure class="shot">
      <${Viewer} src=${src} alt=${alt} height=${height} size=${size} onLoad=${onLoad} onError=${() => setMissing(true)} />
      <figcaption>
        ${size ? html`<span class="muted">${size.w} × ${size.h} px${size.h > size.w * 2 ? ', full page' : ''}</span>` : html`<span></span>`}
        <button type="button" class="link" onClick=${open}>Full size</button>
        <a href=${href || src} target="_blank" rel="noopener" class="muted">Open file</a>
      </figcaption>
      <dialog class="shotbox" ref=${box} onClick=${(e) => { if (e.target === box.current) box.current.close(); }}>
        <div class="shotbox-bar"><span>${alt}</span>
          <form method="dialog"><button class="btn small">Close</button></form></div>
        <${Viewer} src=${src} alt=${alt} height="calc(100vh - 96px)" natural size=${size} onLoad=${onLoad} />
      </dialog>
    </figure>`;
}
