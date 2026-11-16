// PageView: the real page at the playhead, from the run's Playwright trace.
// The vendored trace viewer's service worker (served under trace/) draws
// each snapshot; this frame shows the one for the action nearest the
// playhead, at the persona's viewport, scaled to fit. When the browser will
// not start the worker (a cross-origin frame can refuse it), it says so and
// offers the full viewer in a new tab.
import { useState, useEffect, useRef } from './vendor/hooks.module.js';
import { html } from './lib.js';
import { nearest } from './studio-time.js';

const START_MS = 10000;

let worker = null; // one registration per page load
function startWorker() {
  if (!worker) {
    worker = (async () => {
      if (!('serviceWorker' in navigator)) throw new Error('no service workers');
      const reg = await navigator.serviceWorker.register(new URL('trace/sw.bundle.js', location.href), { scope: new URL('trace/', location.href).pathname });
      if (reg.active) return;
      const sw = reg.installing || reg.waiting;
      await new Promise((ok, bad) => {
        const t = setTimeout(() => bad(new Error('the trace worker did not start')), START_MS);
        sw.addEventListener('statechange', () => {
          if (sw.state === 'activated') { clearTimeout(t); ok(); }
          if (sw.state === 'redundant') { clearTimeout(t); bad(new Error('the trace worker stopped')); }
        });
      });
    })();
    worker.catch(() => { worker = null; });
  }
  return worker;
}

// traceURL is the run's trace.zip as an absolute URL; viewer opens the full
// trace viewer; size is the persona's viewport {w, h}.
export function PageView({ actions, playhead, traceURL, viewer, size }) {
  const [state, setState] = useState('starting'); // starting | ready | failed
  const [loaded, setLoaded] = useState(false);
  const box = useRef(null);
  const frame = useRef(null);
  const [fit, setFit] = useState({ s: 1, x: 0, y: 0 });
  useEffect(() => {
    let live = true;
    startWorker().then(() => { if (live) setState('ready'); }, () => { if (live) setState('failed'); });
    return () => { live = false; };
  }, []);
  useEffect(() => {
    const el = box.current;
    if (!el) return undefined;
    const place = () => {
      const s = Math.min(el.clientWidth / size.w, el.clientHeight / size.h);
      setFit({ s, x: (el.clientWidth - size.w * s) / 2, y: (el.clientHeight - size.h * s) / 2 });
    };
    place();
    const ro = new ResizeObserver(place);
    ro.observe(el);
    return () => ro.disconnect();
  }, [size.w, size.h]);
  const shown = actions.filter((a) => a.callId && a.phase);
  const a = shown.length ? shown[nearest(shown, playhead ?? 0, (x) => Date.parse(x.start))] : null;
  const src = a ? `trace/snapshot/${encodeURIComponent(a.callId)}?trace=${encodeURIComponent(traceURL)}&phase=${encodeURIComponent(a.phase)}` : '';
  // A snapshot that never arrives, or arrives as the worker's JSON error,
  // counts as a failure too.
  useEffect(() => {
    if (state !== 'ready' || !src) return undefined;
    setLoaded(false);
    if (frame.current) delete frame.current.dataset.ok;
    const t = setTimeout(() => setState((s) => (s === 'ready' && !frame.current?.dataset.ok ? 'failed' : s)), START_MS);
    return () => clearTimeout(t);
  }, [state, src]);
  const onLoad = () => {
    const doc = frame.current && frame.current.contentDocument;
    if (doc && doc.contentType === 'application/json') { setState('failed'); return; }
    frame.current.dataset.ok = '1';
    setLoaded(true);
  };

  if (state === 'failed') {
    return html`<div class="pageview msg"><p>This browser would not open the trace here.</p>
      <p><a href=${viewer} target="_blank" rel="noopener">Open full trace</a></p></div>`;
  }
  if (!a) return html`<div class="pageview msg"><p>The trace has no page snapshots.</p></div>`;
  return html`
    <div class="pageview" ref=${box}>
      ${state === 'ready' ? html`<iframe ref=${frame} src=${src} title=${'The page at: ' + a.title} referrerpolicy="no-referrer"
        onLoad=${onLoad} class=${loaded ? '' : 'loading'}
        style=${{ width: size.w + 'px', height: size.h + 'px', transform: `translate(${fit.x}px, ${fit.y}px) scale(${fit.s})` }}></iframe>` : null}
      ${!loaded ? html`<p class="pageview-wait">Opening the page…</p>` : null}
      <span class="pageview-step">${a.title}</span>
    </div>`;
}
