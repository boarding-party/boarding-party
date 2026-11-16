// PageView: the real page at the playhead, from the run's Playwright trace.
// The vendored trace viewer's service worker (served under trace/) draws
// each snapshot; this frame shows the one for the action nearest the
// playhead, at the persona's viewport, scaled to fit. When the browser will
// not start the worker (a cross-origin frame can refuse it), it says so and
// offers the full viewer in a new tab. The frame is sandboxed as the viewer's
// own is: a snapshot's forms, popups and top-level links do nothing. A new
// step's snapshot loads in a hidden frame behind the shown one and takes its
// place once loaded, so playing through steps does not blank the page.
import { useState, useEffect, useRef } from './vendor/hooks.module.js';
import { html } from './lib.js';
import { nearest } from './studio-time.js';

const START_MS = 10000; // for the worker to start
const SLOW_MS = 60000; // the first snapshot downloads and reads the whole trace

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
  const [state, setState] = useState('starting'); // starting | ready | failed (the browser refused the worker)
  const [loaded, setLoaded] = useState({ src: '', v: '' }); // how a snapshot's frame ended up: ok | slow | missing | error
  const [attempt, setAttempt] = useState(0);
  const [onScreen, setOnScreen] = useState(''); // the src of the frame on screen
  const box = useRef(null);
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
  // The viewer's snapshot page marks pointX/pointY, where a click landed.
  const at = a && a.point ? `&pointX=${a.point.x}&pointY=${a.point.y}` : '';
  const src = a ? `trace/snapshot/${encodeURIComponent(a.callId)}?trace=${encodeURIComponent(traceURL)}&phase=${encodeURIComponent(a.phase)}${at}` : '';
  // A snapshot slow to arrive (a big trace on a slow link) says so and can
  // be retried; one the worker cannot find, or its error, says that.
  const load = loaded.src === src ? loaded.v : 'loading'; // the current snapshot's
  useEffect(() => {
    if (state !== 'ready' || !src || load !== 'loading') return undefined;
    const t = setTimeout(() => setLoaded((l) => (l.src === src ? l : { src, v: 'slow' })), SLOW_MS);
    return () => clearTimeout(t);
  }, [state, src, attempt]);
  const onLoad = (s, el) => {
    if (s !== src) return; // a step already left behind
    const doc = el.contentDocument;
    const type = doc ? doc.contentType : '';
    // Typing needs no scrolling, so a field can sit off screen in its
    // snapshot: bring the highlighted target into view. (A click's target is
    // already in view, under its marked point.)
    const t = type === 'text/html' && !a.point && doc.querySelector('[__playwright_target__]');
    if (t) {
      const r = t.getBoundingClientRect();
      if (r.bottom < 0 || r.top > el.contentWindow.innerHeight) t.scrollIntoView({ block: 'center' });
    }
    setLoaded({ src: s, v: type === 'application/json' ? 'error' : type !== 'text/html' ? 'missing' : 'ok' });
    setOnScreen(s);
  };
  const retry = () => { setOnScreen(''); setLoaded({ src: '', v: '' }); setAttempt((n) => n + 1); };
  // The frame on screen stays first and the loading one is added after it,
  // so neither moves in the page (a moved frame loads again).
  const behind = onScreen && onScreen !== src;
  const srcs = behind ? [onScreen, src] : [src];
  const waiting = load === 'loading' && !behind;

  if (state === 'failed') {
    return html`<div class="pageview msg"><p>This browser would not open the trace here.</p>
      <p><a href=${viewer} target="_blank" rel="noopener">Open full trace</a></p></div>`;
  }
  if (!a) return html`<div class="pageview msg"><p>The trace has no page snapshots.</p></div>`;
  const note = {
    slow: html`<p>The trace is still loading. Big traces take a while.</p>
      <p><button type="button" class="btn" onClick=${retry}>Retry</button> <a href=${viewer} target="_blank" rel="noopener">Open full trace</a></p>`,
    missing: html`<p>The trace has no snapshot for this step.</p>`,
    error: html`<p>The trace could not be read here.</p><p><a href=${viewer} target="_blank" rel="noopener">Open full trace</a></p>`,
  }[load];
  return html`
    <div class="pageview" ref=${box}>
      ${state === 'ready' ? srcs.map((s) => html`<iframe key=${s + '#' + attempt} src=${s} title=${'The page at: ' + a.title} referrerpolicy="no-referrer" sandbox="allow-same-origin allow-scripts"
        onLoad=${(e) => onLoad(s, e.currentTarget)} class=${s !== src || load === 'ok' ? '' : 'loading'}
        style=${{ width: size.w + 'px', height: size.h + 'px', transform: `translate(${fit.x}px, ${fit.y}px) scale(${fit.s})` }}></iframe>`) : null}
      ${note ? html`<div class="pageview-wait msg">${note}</div>` : waiting ? html`<p class="pageview-wait">Opening the page…</p>` : null}
      <span class="pageview-step">${a.title}</span>
    </div>`;
}
