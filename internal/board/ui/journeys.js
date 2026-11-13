// Journeys: the recorded journeys a replay plays back without a model, and
// a journey's own page with its code, revisions, decisions and replays.
//
// Journey code is agent-written and untrusted: it is shown only as text
// through htm/Preact, never as markup.
import { useState } from './vendor/hooks.module.js';
import { html, api, fmtWhen, runHref, usePoll, confirmDialog } from './lib.js';

const POLL_MS = 15000;
const MAX_DIFF_LINES = 2000;
const CONTEXT_LINES = 3; // unchanged lines kept around each change

export const journeyHref = (id) => `#/journey/${encodeURIComponent(id)}`;

const STATE = new Map([
  ['live', { label: 'Live', tag: 'ok' }],
  ['paused', { label: 'Paused', tag: 'idle' }],
  ['repairing', { label: 'Repairing', tag: 'wait' }],
]);

function StateTag({ state }) {
  const s = STATE.get(state) || { label: state, tag: 'idle' };
  return html`<span class=${'tag ' + s.tag}>${s.label}</span>`;
}

const record = (j) => (j.passed + j.failed ? `${j.passed} passed, ${j.failed} failed` : 'Not replayed yet');

// RecordBar is the share of replays that passed, as a slim two-tone bar.
function RecordBar({ j }) {
  const n = j.passed + j.failed;
  if (!n) return html`<span class="recbar none" aria-hidden="true"></span>`;
  return html`<span class="recbar" aria-hidden="true"><span style=${{ width: `${(100 * j.passed) / n}%` }}></span></span>`;
}

export function JourneysPage() {
  const [list, setList] = useState(null);
  const [err, setErr] = useState('');
  usePoll(async (stale) => {
    try {
      const d = await api('api/journeys');
      if (stale()) return null;
      setList(d || []); setErr('');
    } catch (e) {
      if (stale()) return null;
      setErr(e.message);
    }
    return POLL_MS;
  }, []);
  return html`
    <section class="page">
      <h1>Journeys</h1>
      <p class="lede">The steps an AI run recorded for a persona's story. A replay plays them back in a browser with fresh test data and no model, so it costs nothing.</p>
      ${err ? html`<div class="note bad" role="alert">Could not load the journeys: ${err}</div>` : null}
      ${list === null && !err ? html`<p class="muted">Loading…</p>` : null}
      ${list && list.length === 0 ? html`<div class="empty"><b>No journeys yet.</b>An AI run records one when it finishes a story.</div>` : null}
      ${list && list.length ? html`
        <div class="card flush">
          <ul class="journeys">
            ${list.map((j) => html`<li key=${j.id}>
              <a class="jrow" href=${journeyHref(j.id)}>
                <${StateTag} state=${j.state} />
                <span class="who"><b>${j.persona}</b> ${j.story}</span>
                <span class="small muted">${j.liveRev ? `Revision ${j.liveRev}` : 'No live revision'}</span>
                <span class="rec small"><${RecordBar} j=${j} /> ${record(j)}</span>
                <span class="when small muted">${fmtWhen(j.lastReplay) ? `Replayed ${fmtWhen(j.lastReplay)}` : ''}</span>
              </a></li>`)}
          </ul>
        </div>` : null}
    </section>`;
}

// lineDiff compares two texts line by line (longest common subsequence) and
// returns [{op: ' ' | '+' | '-', text}], or null when either side has more
// than MAX_DIFF_LINES lines.
export function lineDiff(a, b) {
  const x = a.split('\n'), y = b.split('\n');
  if (x.length > MAX_DIFF_LINES || y.length > MAX_DIFF_LINES) return null;
  let pre = 0;
  while (pre < x.length && pre < y.length && x[pre] === y[pre]) pre++;
  let suf = 0;
  while (suf < x.length - pre && suf < y.length - pre && x[x.length - 1 - suf] === y[y.length - 1 - suf]) suf++;
  const xs = x.slice(pre, x.length - suf), ys = y.slice(pre, y.length - suf);
  const n = xs.length, m = ys.length, w = m + 1;
  const L = new Uint16Array((n + 1) * w); // L[i][j]: LCS of xs[i:] and ys[j:]
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      L[i * w + j] = xs[i] === ys[j] ? L[(i + 1) * w + j + 1] + 1 : Math.max(L[(i + 1) * w + j], L[i * w + j + 1]);
    }
  }
  const out = x.slice(0, pre).map((text) => ({ op: ' ', text }));
  let i = 0, j = 0;
  while (i < n && j < m) {
    if (xs[i] === ys[j]) { out.push({ op: ' ', text: xs[i] }); i++; j++; }
    else if (L[(i + 1) * w + j] >= L[i * w + j + 1]) out.push({ op: '-', text: xs[i++] });
    else out.push({ op: '+', text: ys[j++] });
  }
  while (i < n) out.push({ op: '-', text: xs[i++] });
  while (j < m) out.push({ op: '+', text: ys[j++] });
  return out.concat(x.slice(x.length - suf).map((text) => ({ op: ' ', text })));
}

// hunks folds long unchanged stretches of a diff, keeping CONTEXT_LINES on
// each side of a change.
function hunks(d) {
  const keep = d.map(() => false);
  d.forEach((l, k) => {
    if (l.op === ' ') return;
    for (let t = Math.max(0, k - CONTEXT_LINES); t <= Math.min(d.length - 1, k + CONTEXT_LINES); t++) keep[t] = true;
  });
  const out = [];
  for (let k = 0; k < d.length;) {
    if (keep[k]) { out.push(d[k]); k++; continue; }
    let e = k;
    while (e < d.length && !keep[e]) e++;
    out.push({ fold: e - k });
    k = e;
  }
  return out;
}

function Diff({ before, after }) {
  const d = lineDiff(before, after);
  if (!d) return html`<p class="muted small">Too long to compare here.</p>`;
  if (!d.some((l) => l.op !== ' ')) return html`<p class="muted small">Same code as the revision before.</p>`;
  return html`<pre class="diff">${hunks(d).map((l, k) => (l.fold
    ? html`<span key=${k} class="dl fold">${l.fold} unchanged ${l.fold === 1 ? 'line' : 'lines'}</span>`
    : html`<span key=${k} class=${'dl' + (l.op === '+' ? ' add' : l.op === '-' ? ' del' : '')}>${l.op} ${l.text}</span>`))}</pre>`;
}

function Revision({ r, prev, live }) {
  const [open, setOpen] = useState(false);
  return html`<li>
    <div class="revhead">
      <b>Revision ${r.n}</b>
      ${r.kind === 'bug' ? html`<span class="tag bad">Bug${r.bugStep ? `, step ${r.bugStep}` : ''}</span>` : html`<span class="tag idle">Full</span>`}
      ${r.n === live ? html`<span class="tag ok">Live</span>` : null}
      <span class="small muted">${fmtWhen(r.created)}</span>
      ${r.runId ? html`<a class="small" href=${runHref(r.runId)}>The run that wrote it</a>` : null}
    </div>
    ${r.reason ? html`<p class="revwhy">${r.reason}</p>` : null}
    ${r.n === 1 ? html`<p class="small muted">First revision.</p>`
      : html`<button type="button" class="btn ghost small" aria-expanded=${open} onClick=${() => setOpen(!open)}>
          ${open ? 'Hide changes' : `Changes from revision ${r.n - 1}`}</button>
        ${open ? (prev ? html`<${Diff} before=${prev.code} after=${r.code} />` : html`<p class="muted small">Revision ${r.n - 1} is gone.</p>`) : null}`}
  </li>`;
}

const RESULT = (r) => (r.state === 'done' ? ['ok', 'Passed'] : r.state === 'failed' ? ['bad', 'Failed'] : r.state === 'waiting' ? ['wait', 'Waiting']
  : r.state === 'stuck' ? ['bad', 'Stuck'] : ['run', 'Running']);

// Strip is the last replays as a row of squares, oldest on the left.
function Strip({ runs }) {
  if (!runs.length) return null;
  return html`<div class="strip" role="img" aria-label=${`Last ${runs.length} replays: ${runs.filter((r) => r.state === 'done').length} passed`}>
    ${[...runs].reverse().map((r) => html`<a key=${r.id} href=${runHref(r.id)} class=${'sq ' + RESULT(r)[0]} title=${`${RESULT(r)[1]}, ${fmtWhen(r.started)}`}></a>`)}
  </div>`;
}

export function JourneyPage({ id }) {
  const [data, setData] = useState(null);
  const [runs, setRuns] = useState([]);
  const [err, setErr] = useState('');
  const [missing, setMissing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState(null); // {ok, text} after an action
  const [tick, setTick] = useState(0);
  const [replayRev, setReplayRev] = useState(0);
  const [backRev, setBackRev] = useState(0);

  usePoll(async (stale) => {
    try {
      const [d, rs] = await Promise.all([api(`api/journeys/${encodeURIComponent(id)}`), api(`api/runs?journey=${encodeURIComponent(id)}`)]);
      if (stale()) return null;
      setData(d); setRuns(rs || []); setErr('');
    } catch (e) {
      if (stale()) return null;
      if (e.status === 404) { setMissing(true); return null; }
      setErr(e.message);
    }
    return POLL_MS;
  }, [id, tick]);

  const act = async (path, opts, done) => {
    setBusy(true); setMsg(null);
    try {
      const out = await api(`api/journeys/${encodeURIComponent(id)}${path}`, opts);
      setMsg({ ok: true, text: done(out) });
      setTick((t) => t + 1);
    } catch (e) {
      setMsg({ ok: false, text: e.message });
    } finally {
      setBusy(false);
    }
  };
  const post = (body) => ({ method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });

  if (missing) {
    return html`<section class="page"><p class="crumb"><a href="#/journeys">Journeys</a></p>
      <div class="empty"><b>Journey not found</b>It may have been deleted. <a href="#/journeys">Back to journeys</a>.</div></section>`;
  }
  if (!data) {
    return html`<section class="page"><p class="crumb"><a href="#/journeys">Journeys</a></p>
      ${err ? html`<div class="note bad">Could not load the journey: ${err}</div>` : html`<p class="muted">Loading…</p>`}</section>`;
  }

  const { journey: j, revisions = [], decisions = [] } = data;
  const live = revisions.find((r) => r.n === j.liveRev);
  const byN = new Map(revisions.map((r) => [r.n, r]));
  const rollbackable = revisions.filter((r) => r.kind === 'full' && r.n !== j.liveRev);
  const pickReplay = byN.has(replayRev) ? replayRev : (live ? live.n : (revisions[0] && revisions[0].n) || 0);
  const pickBack = rollbackable.some((r) => r.n === backRev) ? backRev : (rollbackable[0] && rollbackable[0].n) || 0;
  const paused = j.state === 'paused';

  const remove = async () => {
    const ok = await confirmDialog(`Delete ${j.persona}'s journey for ${j.story}? Its revisions go and no more replays run. Its decisions stay on record.`,
      { confirm: 'Delete journey', danger: true });
    if (!ok) return;
    setBusy(true); setMsg(null);
    try {
      await api(`api/journeys/${encodeURIComponent(id)}`, { method: 'DELETE' });
      location.hash = '#/journeys';
    } catch (e) {
      setMsg({ ok: false, text: e.message });
      setBusy(false);
    }
  };
  const rollback = async () => {
    const ok = await confirmDialog(`Make revision ${pickBack} live again? Its code is saved as a new revision.`, { confirm: 'Roll back' });
    if (ok) act('/rollback', post({ revision: pickBack }), (rev) => `Revision ${pickBack} is live again as revision ${rev && rev.n}.`);
  };

  return html`
    <section class="page wide">
      <p class="crumb"><a href="#/journeys">Journeys</a> / ${j.id}</p>
      <h1>${j.persona}, ${j.story}</h1>
      <p class="lede"><${StateTag} state=${j.state} />
        ${' '}${j.liveRev ? `Live revision ${j.liveRev}.` : 'No live revision yet.'} ${record(j)}${fmtWhen(j.lastReplay) ? `, last replay ${fmtWhen(j.lastReplay)}` : ''}.</p>
      ${err ? html`<div class="note bad banner">Lost contact with the director: ${err}. Retrying.</div>` : null}
      <div class="issuelayout">
        <div class="stack issueside">
          <div class="card jactions">
            <h2>Actions</h2>
            <div class="row">
              <button type="button" class="btn" disabled=${busy} onClick=${() => act(paused ? '/resume' : '/pause', { method: 'POST' },
                () => (paused ? 'Resumed: scheduled replays run again.' : 'Paused: no replays run until you resume it.'))}>
                ${paused ? 'Resume' : 'Pause'}</button>
              <button type="button" class="btn danger-ghost" disabled=${busy} onClick=${remove}>Delete</button>
            </div>
            <div class="field">
              <label for="j-replay">Replay a revision</label>
              <div class="row nowrap">
                <select id="j-replay" value=${pickReplay} disabled=${busy || paused || !revisions.length} onChange=${(e) => setReplayRev(Number(e.currentTarget.value))}>
                  ${revisions.map((r) => html`<option key=${r.n} value=${r.n}>Revision ${r.n}${r.n === j.liveRev ? ' (live)' : r.kind === 'bug' ? ' (bug)' : ''}</option>`)}
                </select>
                <button type="button" class="btn primary" disabled=${busy || paused || !pickReplay}
                  onClick=${() => act('/replay', post({ revision: pickReplay }), () => 'Replay queued.')}>Replay</button>
              </div>
              ${paused ? html`<small class="muted">Resume the journey to replay it.</small>` : null}
            </div>
            ${rollbackable.length ? html`<div class="field">
              <label for="j-back">Roll back to</label>
              <div class="row nowrap">
                <select id="j-back" value=${pickBack} disabled=${busy} onChange=${(e) => setBackRev(Number(e.currentTarget.value))}>
                  ${rollbackable.map((r) => html`<option key=${r.n} value=${r.n}>Revision ${r.n}</option>`)}
                </select>
                <button type="button" class="btn" disabled=${busy || !pickBack} onClick=${rollback}>Roll back</button>
              </div>
            </div>` : null}
            ${msg ? html`<div class=${'note ' + (msg.ok ? 'ok' : 'bad')} role=${msg.ok ? 'status' : 'alert'}>${msg.text}</div>` : null}
          </div>
          <div class="card">
            <h2>Recent replays</h2>
            ${runs.length ? html`
              <${Strip} runs=${runs} />
              <ul class="jruns">${runs.slice(0, 8).map((r) => html`<li key=${r.id}>
                <span class=${'tag ' + RESULT(r)[0]}>${RESULT(r)[1]}</span>
                <a href=${runHref(r.id)}>Revision ${r.revision}</a>
                <span class="small muted">${fmtWhen(r.started)}</span>
                ${r.outcome && r.state !== 'done' ? html`<span class="small muted jout">${r.outcome.split('\n')[0]}</span>` : null}
              </li>`)}</ul>` : html`<p class="muted small">No replays yet.</p>`}
          </div>
        </div>
        <div class="stack">
          <div class="card">
            <h2>${live ? `Live code, revision ${live.n}` : 'Code'}</h2>
            ${live ? html`<pre class="code">${live.code}</pre>` : html`<p class="muted small">Only bug revisions so far: nothing replays on a schedule yet.</p>`}
          </div>
          <div class="card">
            <h2>Revisions</h2>
            <ul class="revs">${revisions.map((r) => html`<${Revision} key=${r.n} r=${r} prev=${byN.get(r.n - 1)} live=${j.liveRev} />`)}</ul>
          </div>
          <div class="card">
            <h2>Decisions</h2>
            ${decisions.length ? html`<ol class="jdecisions">${decisions.map((d) => html`<li key=${d.id}>
              <span class="dwhen small muted">${fmtWhen(d.created)}</span>
              <span class="dwhat"><b>${d.kind}</b> <span class="small muted">by ${d.actor || 'unknown'}${d.revBefore !== d.revAfter ? `, revision ${d.revBefore || 'none'} to ${d.revAfter || 'none'}` : ''}</span>
                ${d.runId ? html` <a class="small" href=${runHref(d.runId)}>Run</a>` : null}</span>
              ${d.reason ? html`<span class="dwhy">${d.reason}</span>` : null}
            </li>`)}</ol>` : html`<p class="muted small">No decisions yet.</p>`}
          </div>
        </div>
      </div>
    </section>`;
}
