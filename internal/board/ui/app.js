// boardingparty board: runs list, start run, run detail.
//
// Everything the director sends is untrusted (bug text is agent-written), so
// it is only ever rendered as text through htm/Preact, which escapes it.
// Never use innerHTML or dangerouslySetInnerHTML here.
import { render } from './vendor/preact.module.js';
import { useState, useEffect, useRef, useCallback } from './vendor/hooks.module.js';
import { html, api, directorDown, confirmDialog, DialogHost, parseTime, fmtWhen, artifactURL, runHref, usePoll, SEVERITY_TAG, SHOT_TOKEN } from './lib.js';
import { BugsPage, IssuePage, issueHref, openCount } from './bugs.js';
import { ProjectPage } from './project.js';

const ACTIVE = new Set(['pending', 'running']);
const STATE_TAG = new Map([['pending', 'idle'], ['running', 'run'], ['done', 'ok'], ['stuck', 'wait'], ['failed', 'bad'], ['waiting', 'hold'], ['continued', 'idle']]);
const RETRY_MS = [5000, 10000, 30000]; // back-off after consecutive errors
const MAX_FINISHED_FAILURES = 5;
const RECONNECT_MS = 14 * 60 * 1000; // under the director's 15-minute stream cap
const ACTIVE_POLL_MS = 5000;
const IDLE_POLL_MS = 30000;
const SIDEBAR_POLL_MS = 30000;

// ---- helpers ------------------------------------------------------------

function fmtFull(s) {
  const d = parseTime(s);
  return d ? d.toLocaleString('en-GB', { day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' }) : '';
}

function fmtDuration(ms) {
  if (ms < 0 || !isFinite(ms)) return '';
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} m ${s % 60} s`;
  return `${Math.floor(m / 60)} h ${m % 60} m`;
}

const firstLine = (s) => (s || '').split('\n').find((l) => l.trim()) || '';

// stampOf finds the first m:ss, mm:ss or h:mm:ss in a bug's text and returns
// it in seconds, or null. The step is checked first: that is where the agent
// is asked to say when it happened.
const STAMP = /(?<![\d:])(?:(\d{1,2}):)?(\d{1,2}):([0-5]\d)(?![\d:])/;
function stampOf(bug) {
  for (const t of [bug.step, bug.summary, bug.evidence, bug.note]) {
    const m = STAMP.exec(t || '');
    if (m) return (Number(m[1] || 0) * 3600) + Number(m[2]) * 60 + Number(m[3]);
  }
  return null;
}

function fmtStamp(secs) {
  const h = Math.floor(secs / 3600), m = Math.floor((secs % 3600) / 60), s = secs % 60;
  const mm = String(m).padStart(2, '0'), ss = String(s).padStart(2, '0');
  return h ? `${h}:${mm}:${ss}` : `${mm}:${ss}`;
}


function allPersonas(project) {
  const out = [];
  for (const r of (project && project.roles) || []) {
    for (const p of r.personas || []) out.push({ name: p.name, role: r.name });
  }
  return out;
}

function storiesFor(project, personaName) {
  for (const r of (project && project.roles) || []) {
    if ((r.personas || []).some((p) => p.name === personaName)) return r.stories || [];
  }
  return [];
}

// ---- routing ------------------------------------------------------------

function parseRoute(hash) {
  const parts = (hash || '').replace(/^#\/?/, '').split('/');
  if (parts[0] === 'project') {
    const section = ['general', 'sites', 'inbox', 'files', 'roles'].includes(parts[1]) ? parts[1] : 'general';
    return { page: 'project', section, roleN: section === 'roles' && /^[1-9][0-9]*$/.test(parts[2] || '') ? parts[2] : '' };
  }
  if (parts[0] === 'inbox') return { page: 'inbox' };
  if (parts[0] === 'bugs' && parts[1]) {
    try { return { page: 'issue', id: decodeURIComponent(parts[1]) }; } catch (_) { /* fall through */ }
  }
  if (parts[0] === 'bugs') return { page: 'bugs' };
  if (parts[0] === 'runs' && parts[1]) {
    try { return { page: 'run', id: decodeURIComponent(parts[1]) }; } catch (_) { /* fall through */ }
  }
  return { page: 'runs' };
}

// useRoute follows the hash. While leaveGuard.current is true (unsaved
// project edits), leaving #/project asks first; on "Stay" the address goes
// back to where it was without another hashchange.
function useRoute(leaveGuard) {
  const [route, setRoute] = useState(() => parseRoute(location.hash));
  useEffect(() => {
    let shown = location.hash;
    let current = parseRoute(shown);
    const go = () => {
      shown = location.hash;
      current = parseRoute(shown);
      setRoute(current);
      window.scrollTo(0, 0);
    };
    const on = async () => {
      const next = parseRoute(location.hash);
      if (current.page === 'project' && next.page !== 'project' && leaveGuard.current) {
        const target = location.hash;
        history.replaceState(null, '', shown || '#/project');
        const leave = await confirmDialog('You have unsaved changes to the project. Leave and lose them?',
          { confirm: 'Leave without saving', cancel: 'Stay', danger: true });
        if (!leave) return;
        leaveGuard.current = false;
        history.replaceState(null, '', target);
      }
      go();
    };
    window.addEventListener('hashchange', on);
    return () => window.removeEventListener('hashchange', on);
  }, []);
  return route;
}

// ---- shell --------------------------------------------------------------

function App() {
  const unsaved = useRef(false); // the project editor has unsaved edits
  const route = useRoute(unsaved);
  const onDirty = useCallback((v) => { unsaved.current = v; }, []);
  const [project, setProject] = useState(undefined); // undefined loading, null none
  const [projectErr, setProjectErr] = useState('');
  const [runs, setRuns] = useState(null);
  const [runsErr, setRunsErr] = useState('');
  const [queue, setQueue] = useState([]);
  const [director, setDirector] = useState('unknown'); // unknown | ok | down
  const [status, setStatus] = useState(null); // /api/status: spend, pause, scheduled personas

  const loadProject = useCallback(async () => {
    try {
      setProject(await api('/api/project'));
      setProjectErr('');
    } catch (e) {
      if (e.status === 404) { setProject(null); setProjectErr(''); return; }
      setProjectErr(e.message);
    }
  }, []);

  // The queue rides along with the runs; if it can't be read, the last
  // known queue stays and the runs still show.
  const loadQueue = useCallback(async () => {
    try {
      const q = (await api('/api/queue')) || [];
      setQueue(q);
      return q;
    } catch (_) { return null; }
  }, []);

  // Like the queue, the status rides along; the last known one stays on error.
  const loadStatus = useCallback(async () => {
    try { setStatus(await api('/api/status')); } catch (_) { /* keep the last status */ }
  }, []);

  const loadRuns = useCallback(async () => {
    try {
      const [rs] = await Promise.all([api('/api/runs').then((x) => x || []), loadQueue(), loadStatus()]);
      setRuns(rs);
      setRunsErr('');
      setDirector('ok');
      return rs;
    } catch (e) {
      setRunsErr(e.message);
      setDirector(directorDown(e) ? 'down' : 'ok');
      return null;
    }
  }, [loadQueue, loadStatus]);

  // Poll quickly while a run is active or one is up next: the director starts
  // queued runs on its own.
  const queued = queue.length > 0;
  usePoll(async (stale) => {
    const rs = await loadRuns();
    if (stale()) return null;
    return rs && (rs.some((r) => ACTIVE.has(r.state)) || queued) ? ACTIVE_POLL_MS : IDLE_POLL_MS;
  }, [loadRuns, queued]);

  useEffect(() => { loadProject(); }, [loadProject, route.page]);

  const active = (runs || []).find((r) => ACTIVE.has(r.state));

  return html`
    <div class="app">
      <${Sidebar} route=${route} project=${project} runs=${runs} />
      <main>
        <${RunStrip} director=${director} active=${active} status=${status} onChange=${loadRuns} />
        ${route.page === 'run' ? html`<${RunPage} key=${route.id} id=${route.id} onChange=${loadRuns} />`
          : route.page === 'project' ? html`<${ProjectPage} section=${route.section} roleN=${route.roleN} onSaved=${loadProject} onDirty=${onDirty} />`
          : route.page === 'inbox' ? html`<${InboxPage} project=${project} />`
          : route.page === 'bugs' ? html`<${BugsPage} />`
          : route.page === 'issue' ? html`<${IssuePage} key=${route.id} id=${route.id} />`
          : html`<${RunsPage} project=${project} projectErr=${projectErr} runs=${runs} runsErr=${runsErr}
                    queue=${queue} status=${status} active=${active} onStarted=${loadRuns} />`}
      </main>
      <${DialogHost} />
    </div>`;
}

function Sidebar({ route, project, runs }) {
  const on = (p) => (route.page === p || (p === 'runs' && route.page === 'run') || (p === 'bugs' && route.page === 'issue') ? 'on' : '');
  const [open, setOpen] = useState(null);
  usePoll(async (stale) => {
    try {
      const d = await api('/api/issues');
      if (stale()) return null;
      setOpen(openCount(d && d.issues));
    } catch (_) { /* the count is optional */ }
    return SIDEBAR_POLL_MS;
  }, [route.page]);
  const site = (project && project.sites && project.sites[0]) || '';
  let host = site;
  try { host = site ? new URL(site).host : ''; } catch (_) { /* show it as written */ }
  return html`
    <aside>
      <div class="brand">boardingparty</div>
      ${project ? html`<div class="proj">${project.name || 'Unnamed project'}<small>${host || 'no site yet'}</small></div>`
        : project === null ? html`<div class="proj">No project yet<small><a href="#/project" style="color:inherit">Set one up</a></small></div>`
        : null}
      <nav aria-label="Main">
        <a href="#/runs" class=${on('runs')}>Runs ${runs ? html`<span class="count">${runs.length}</span>` : null}</a>
        <a href="#/bugs" class=${on('bugs')}>Bugs ${open ? html`<span class="count" title="Open bugs">${open}</span>` : null}</a>
        <a href="#/inbox" class=${on('inbox')}>Inbox</a>
        <a href="#/project" class=${on('project')}>Project</a>
      </nav>
      <div class="foot">Local board. Close the terminal to stop it.</div>
    </aside>`;
}

// fmtMoney writes US dollars with two decimals.
const fmtMoney = (n) => '$' + (Number(n) || 0).toFixed(2);

// fmtCost is a run's cost; a fraction of a cent reads as "< $0.01".
const fmtCost = (n) => (n > 0 && n < 0.005 ? '< $0.01' : '≈ ' + fmtMoney(n));

// fmtReset is a clock time in the project's zone: the budget reset, or when
// a scheduled persona acts next.
function fmtReset(iso, zone) {
  const d = parseTime(iso);
  if (!d) return 'midnight';
  const opts = { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' };
  try { return d.toLocaleTimeString('en-GB', { ...opts, timeZone: zone || undefined }); } catch (_) { return d.toLocaleTimeString('en-GB', opts); }
}

function RunStrip({ director, active, status, onChange }) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const act = async (fn) => {
    setBusy(true); setErr('');
    try { await fn(); } catch (e) { if (e.status !== 404) setErr(e.message); } finally { setBusy(false); onChange(); }
  };
  const togglePause = async () => {
    const pausing = !status.paused;
    if (pausing && !(await confirmDialog('Pause the cast? Running runs finish; nothing new starts until you resume.', { confirm: 'Pause' }))) return;
    act(() => api('/api/pause', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ paused: pausing }) }));
  };
  const stop = async () => {
    if (!(await confirmDialog('Stop this run now? Its recording so far is kept. The next run in Up next then starts; pause first to keep things quiet.', { confirm: 'Stop run', danger: true }))) return;
    act(() => api(`/api/runs/${encodeURIComponent(active.id)}/stop`, { method: 'POST' }));
  };
  const state = director === 'ok' ? { dot: 'ok', label: 'Director ready' }
    : director === 'down' ? { dot: 'bad', label: 'Director unreachable' }
    : { dot: '', label: 'Connecting…' };
  return html`
    <div class="runbar">
      <div class="runstate"><span class=${'dot ' + state.dot}></span>${state.label}</div>
      ${active
        ? html`<div class="stat"><span class="dot live" style="display:inline-block;margin-right:6px"></span>
            <a href=${runHref(active.id)}><b>${active.persona}</b></a> is on <b>${active.story}</b>
            <span class="muted"> · ${active.state} since ${fmtWhen(active.started)}</span></div>`
        : director === 'ok' ? html`<div class="stat">No run active</div>` : null}
      ${status ? html`
        <div class="stat spend">Today <b>${fmtMoney(status.spentToday)}</b>${status.dailyBudget > 0 ? html` of <b>${fmtMoney(status.dailyBudget)}</b>` : null}
          ${status.overBudget ? html` <span class="tag bad">Budget reached</span> No new runs until ${fmtReset(status.resetsAt, status.timezone)}` : null}
          ${status.paused ? html` <span class="tag hold">Paused</span>` : null}</div>` : null}
      <div class="grow"></div>
      ${err ? html`<span class="small bad-text" role="alert">${err}</span>` : null}
      ${status ? html`<button class="btn" type="button" disabled=${busy} onClick=${togglePause}>${status.paused ? 'Resume' : 'Pause'}</button>` : null}
      ${active ? html`<button class="btn danger-ghost" type="button" disabled=${busy} onClick=${stop}>Stop run</button>` : null}
      <a class="btn" href="#/project">Edit project</a>
    </div>`;
}

// ---- runs page ----------------------------------------------------------

function RunsPage({ project, projectErr, runs, runsErr, queue, status, active, onStarted }) {
  return html`
    <section class="page">
      <h1>Runs</h1>
      <p class="lede">One persona working through one story on your staging site. Runs go one at a time.</p>
      <div class="stack">
        ${projectErr ? html`<div class="note bad">Could not load the project: ${projectErr}</div>` : null}
        ${project === null
          ? html`<div class="empty"><b>Set up your project first</b>Runs need a site, a role, a persona and a story.
              <div style="margin-top:12px"><a class="btn primary" href="#/project">Set up your project</a></div></div>`
          : project ? html`<${StartRun} project=${project} active=${active} status=${status} onStarted=${onStarted} />` : null}
        ${runsErr ? html`<div class="note bad">Could not load runs: ${runsErr}</div>` : null}
        ${runs && runs.length === 0 && project !== null
          ? html`<div class="empty"><b>No runs yet.</b>Start one above.</div>` : null}
        ${queue.length || (status && status.due.length) ? html`<${UpNext} queue=${queue} due=${status ? status.due : []} zone=${status && status.timezone} onChange=${onStarted} />` : null}
        ${runs && runs.length > 0 ? html`<${RunsTable} runs=${runs} />` : null}
      </div>
    </section>`;
}

function StartRun({ project, active, status, onStarted }) {
  const personas = allPersonas(project);
  const [persona, setPersona] = useState('');
  const [story, setStory] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  // A pause or budget refusal is stale once that state changes.
  const gate = status ? `${status.paused}/${status.overBudget}` : '';
  useEffect(() => setErr(''), [gate]);

  // Keep the selection valid when the project changes.
  const personaOK = personas.some((p) => p.name === persona);
  const p = personaOK ? persona : (personas[0] ? personas[0].name : '');
  const stories = storiesFor(project, p);
  const s = stories.some((x) => x.name === story) ? story : (stories[0] ? stories[0].name : '');
  const goal = (stories.find((x) => x.name === s) || {}).goal;

  const roles = (project.roles || []).filter((r) => (r.personas || []).length);
  const disabled = busy || !p || !s;

  const submit = async (e) => {
    e.preventDefault();
    if (disabled) return;
    setBusy(true); setErr('');
    try {
      const run = await api('/api/runs', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ persona: p, story: s }),
      });
      await onStarted();
      if (run && run.id) location.hash = runHref(run.id); // a queued run has no id yet
    } catch (e2) {
      setErr(e2.message); // 400 invalid is shown verbatim
      onStarted();
    } finally {
      setBusy(false);
    }
  };

  if (!personas.length) {
    return html`<div class="empty"><b>Add a persona to start a run</b>
      Your project has no personas yet. <a href="#/project">Edit the project</a>.</div>`;
  }

  return html`
    <form class="card" onSubmit=${submit}>
      <h2>Start run</h2>
      <div class="startform">
        <div class="field">
          <label for="sr-persona">Persona</label>
          <select id="sr-persona" value=${p} disabled=${busy}
              onChange=${(e) => { setPersona(e.currentTarget.value); setStory(''); setErr(''); }}>
            ${roles.map((r) => html`<optgroup label=${r.name}>
              ${r.personas.map((x) => html`<option value=${x.name}>${x.name}</option>`)}
            </optgroup>`)}
          </select>
        </div>
        <div class="field">
          <label for="sr-story">Story</label>
          <select id="sr-story" value=${s} disabled=${busy || !stories.length}
              onChange=${(e) => { setStory(e.currentTarget.value); setErr(''); }}>
            ${stories.length ? stories.map((x) => html`<option value=${x.name}>${x.name}</option>`)
              : html`<option value="">No stories for this role</option>`}
          </select>
        </div>
        <button class="btn primary" type="submit" disabled=${disabled}>${busy ? (active ? 'Adding…' : 'Starting…') : (active ? 'Add to queue' : 'Start run')}</button>
      </div>
      ${goal ? html`<p class="goal">${goal}</p>` : null}
      ${active ? html`<div class="note">A run is active (<a href=${runHref(active.id)}>${active.persona} · ${active.story}</a>). A new run waits in Up next and starts when it finishes.</div>` : null}
      ${err ? html`<div class="note bad" role="alert">${err.charAt(0).toUpperCase() + err.slice(1)}</div>` : null}
    </form>`;
}

// shownState is what the board calls a run's state: a waiting run whose wait
// was met has been continued by a later run.
const shownState = (state, wait) => (state === 'waiting' && wait && wait.status === 'met' ? 'continued' : state);

function StateTag({ state, wait }) {
  const s = shownState(state, wait);
  return html`<span class=${'tag ' + (STATE_TAG.get(s) || 'idle')}>${s || 'unknown'}</span>`;
}

// UpNext lists the runs the director will start next, oldest first.
function UpNext({ queue, due, zone, onChange }) {
  const [busy, setBusy] = useState('');
  const [err, setErr] = useState('');
  const remove = async (q) => {
    if (!(await confirmDialog('Remove this run from the queue?', { confirm: 'Remove', danger: true }))) return;
    setBusy(q.id); setErr('');
    try {
      await api(`/api/queue/${encodeURIComponent(q.id)}`, { method: 'DELETE' });
    } catch (e) {
      if (e.status !== 404) setErr(e.message); // already started or gone: nothing to say
    } finally {
      setBusy('');
      onChange();
    }
  };
  return html`
    <div class="card upnext">
      <h2>Up next</h2>
      <ul>${queue.map((q) => html`
        <li key=${q.id}>
          <span class="what"><b>${q.persona}</b> · ${q.story}</span>
          <span class="why muted">${q.follows ? html`<a href=${runHref(q.follows)}>${q.reason}</a>` : q.reason}${' · '}<span title=${fmtFull(q.created)}>queued ${fmtWhen(q.created)}</span></span>
          <button class="btn icon" type="button" disabled=${busy !== ''} title="Remove from the queue"
            aria-label=${`Remove ${q.persona} · ${q.story} from the queue`} onClick=${() => remove(q)}>×</button>
        </li>`)}</ul>
      ${err ? html`<div class="note bad" role="alert">Could not remove the run: ${err}</div>` : null}
      ${due.length ? html`<h3 class="later">Later</h3>
        <ul>${due.slice(0, 5).map((d, i) => html`<li key=${i + d.persona}><span class="what">${d.persona} · ${fmtReset(d.at, zone)}</span>${d.reason ? html`<span class="why muted">${d.reason}</span>` : null}</li>`)}
          ${due.length > 5 ? html`<li class="muted">and ${due.length - 5} more</li>` : null}</ul>` : null}
    </div>`;
}

const STAGE_SHORT = { 'waiting-for-node': 'Starting machine', 'starting': 'Starting run', 'running': 'Getting ready', 'failing': 'Failing to start' };
const THUMB_MS = 3000;

// A small live picture of an active run. The runs list says (through its own
// poll) whether a frame exists and what stage the run is at; while one does,
// this loads live.jpg one at a time, and waits while the tab is hidden.
function Thumb({ id, stage, live }) {
  const [tick, setTick] = useState(0);
  const [ok, setOk] = useState(false);
  const timer = useRef(0);
  const on = useRef(false);
  // Wait, then ask for the next image; each load or error calls this again,
  // so requests never overlap.
  const schedule = () => {
    clearTimeout(timer.current);
    if (!on.current) return;
    timer.current = setTimeout(function go() {
      if (!on.current) return;
      if (document.hidden) { timer.current = setTimeout(go, THUMB_MS); return; }
      setTick((n) => n + 1);
    }, THUMB_MS);
  };
  useEffect(() => {
    on.current = !!live;
    if (live) setTick((n) => n + 1); else setOk(false);
    return () => { on.current = false; clearTimeout(timer.current); };
  }, [id, live]);
  return html`
    <a class="thumb" href=${runHref(id)} onClick=${(e) => e.stopPropagation()} title="Watch this run">
      ${live && tick ? html`<img src=${`/api/runs/${encodeURIComponent(id)}/live.jpg?t=${tick}`} alt="Live view of the run"
          style=${ok ? '' : 'display:none'}
          onLoad=${() => { setOk(true); schedule(); }} onError=${() => { setOk(false); schedule(); }} />` : null}
      ${live && ok ? null : html`<span>${STAGE_SHORT[stage] || 'Waiting'}</span>`}
    </a>`;
}

// The screen a finished run ended on, or a dark box when there is none.
function EndedThumb({ id }) {
  const [ok, setOk] = useState(true);
  return html`
    <a class="thumb" href=${runHref(id)} onClick=${(e) => e.stopPropagation()} title="Open this run">
      ${ok ? html`<img src=${artifactURL(id, 'last-frame.jpg')} alt="Last screen of the run" loading="lazy" onError=${() => setOk(false)} />`
        : html`<span>Session over</span>`}
    </a>`;
}

function RunsTable({ runs }) {
  const open = (id) => () => { location.hash = runHref(id); };
  return html`
    <div class="card flush tablewrap">
      <table>
        <thead><tr><th></th><th>Persona</th><th>Story</th><th>State</th><th>Started</th><th>Outcome</th></tr></thead>
        <tbody>
          ${runs.map((r) => html`
            <tr class="click" key=${r.id} onClick=${open(r.id)}>
              <td class="thumbcell">${ACTIVE.has(r.state) ? html`<${Thumb} key=${r.id} id=${r.id} stage=${r.stage} live=${r.live} />` : html`<${EndedThumb} key=${'end-' + r.id} id=${r.id} />`}</td>
              <td><a href=${runHref(r.id)} onClick=${(e) => e.stopPropagation()}>${r.persona}</a>
                ${r.role ? html`<div class="small muted">${r.role}</div>` : null}</td>
              <td>${r.story}${ACTIVE.has(r.state) ? html` <a class="small" href=${runHref(r.id)} onClick=${(e) => e.stopPropagation()}>Watch</a>` : null}</td>
              <td><${StateTag} state=${r.state} wait=${r.wait} />${r.blocked ? html` <span class="tag wait" title="The network fence refused hosts the site tried to load. Open the run to allow them.">${r.blocked} blocked</span>` : null}
                ${r.state === 'waiting' && r.wait && r.wait.status === 'open'
                  ? html`<div class="waitline small muted">Waiting for ${r.wait.waitingFor} · gives up ${fmtWhen(r.wait.deadline)}</div>` : null}</td>
              <td class="when" title=${fmtFull(r.started)}>${fmtWhen(r.started)}</td>
              <td class="outcome" title=${firstLine(r.outcome)}>${firstLine(r.outcome) || html`<span class="muted">${ACTIVE.has(r.state) ? 'In progress' : '—'}</span>`}</td>
            </tr>`)}
        </tbody>
      </table>
    </div>`;
}

// ---- inbox page ---------------------------------------------------------
// Mail the director has received for the personas. Everything here is text
// from strangers: shown as text only, and links never become anchors, so a
// click here can't use up a one-time link a persona still needs.

const INBOX_POLL_MS = 10000;
const INBOX_LIMIT = 100;

function InboxPage({ project }) {
  const [persona, setPersona] = useState('');
  const [tab, setTab] = useState('received'); // received | kept
  const [msgs, setMsgs] = useState(null);
  const [err, setErr] = useState('');
  const [openID, setOpenID] = useState('');
  const names = [...new Set(allPersonas(project).map((p) => p.name))];
  const loaded = useRef(false); // the first load for this view has happened

  // Re-poll while visible; a hidden tab waits and checks again, but the first
  // load of a view always goes ahead.
  usePoll(async (stale) => {
    if (document.hidden && loaded.current) return INBOX_POLL_MS;
    try {
      const q = `limit=${INBOX_LIMIT}` + (persona ? `&persona=${encodeURIComponent(persona)}` : '') + (tab === 'kept' ? '&filtered=1' : '');
      const list = (await api(`/api/messages?${q}`)) || [];
      if (stale()) return null;
      setMsgs(list); setErr(''); loaded.current = true;
    } catch (e) {
      if (stale()) return null;
      setErr(e.message);
    }
    return INBOX_POLL_MS;
  }, [persona, tab]);

  const pick = (fn) => { loaded.current = false; setMsgs(null); setOpenID(''); fn(); };

  return html`
    <section class="page">
      <h1>Inbox</h1>
      <p class="lede">Mail sent to the personas' addresses. The mailbox is set up on the <a href="#/project/inbox">Project</a> page.</p>
      <div class="stack">
        <div class="row inboxbar">
          <div class="field">
            <label for="ib-persona">Persona</label>
            <select id="ib-persona" value=${persona} onChange=${(e) => { const v = e.currentTarget.value; pick(() => setPersona(v)); }}>
              <option value="" selected=${!persona}>All</option>
              ${names.map((n) => html`<option key=${n} value=${n} selected=${persona === n}>${n}</option>`)}
            </select>
          </div>
          <div class="tabs" role="tablist">
            <button type="button" role="tab" aria-selected=${tab === 'received'} class=${tab === 'received' ? 'on' : ''} onClick=${() => pick(() => setTab('received'))}>Received</button>
            <button type="button" role="tab" aria-selected=${tab === 'kept'} class=${tab === 'kept' ? 'on' : ''} onClick=${() => pick(() => setTab('kept'))}>Kept out</button>
          </div>
        </div>
        ${err ? html`<div class="note bad" role="alert">Could not load the mail: ${err}</div>` : null}
        ${msgs === null && !err ? html`<p class="muted">Loading…</p>` : null}
        ${msgs && msgs.length === 0 ? html`<div class="empty"><b>${tab === 'kept' ? 'Nothing kept out.' : 'No mail yet.'}</b>${tab === 'kept' ? 'Mail from senders you did not accept appears here.' : 'Mail sent to a persona\'s address appears here within about 10 seconds.'}</div>` : null}
        ${msgs && msgs.length ? html`
          <div class="card flush tablewrap">
            <table class="mailtab">
              <thead><tr><th>Persona</th><th>From</th><th>Subject</th><th>Received</th>${tab === 'kept' ? html`<th>Reason</th>` : null}</tr></thead>
              <tbody>
                ${msgs.map((m) => html`
                  <tr class=${'click' + (openID === m.id ? ' sel' : '')} key=${m.id} tabindex="0" aria-expanded=${openID === m.id}
                      onClick=${() => setOpenID(openID === m.id ? '' : m.id)}
                      onKeyDown=${(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setOpenID(openID === m.id ? '' : m.id); } }}>
                    <td>${m.persona || html`<span class="muted">—</span>`}</td>
                    <td class="from">${m.from}</td>
                    <td class="subj">${m.subject || html`<span class="muted">(no subject)</span>`}${m.started ? html` <span class="tag run started">Started ${m.started}</span>` : null}</td>
                    <td class="when" title=${fmtFull(m.received)}>${fmtWhen(m.received)}</td>
                    ${tab === 'kept' ? html`<td><span class="tag wait">${m.filtered}</span></td>` : null}
                  </tr>
                  ${openID === m.id ? html`<tr class="detail" key=${m.id + ':open'}><td colspan=${tab === 'kept' ? 5 : 4}><${MessageBody} id=${m.id} /></td></tr>` : null}`)}
              </tbody>
            </table>
          </div>` : null}
      </div>
    </section>`;
}

function MessageBody({ id }) {
  const [m, setM] = useState(null);
  const [err, setErr] = useState('');
  useEffect(() => {
    let live = true;
    api(`/api/messages/${encodeURIComponent(id)}`).then((x) => { if (live) setM(x); }).catch((e) => { if (live) setErr(e.message); });
    return () => { live = false; };
  }, [id]);
  if (err) return html`<div class="note bad">Could not load the message: ${err}</div>`;
  if (!m) return html`<p class="muted small">Loading…</p>`;
  return html`
    <div class="msg">
      ${m.to && m.to.length ? html`<div class="small muted">To ${m.to.join(', ')}</div>` : null}
      <pre class="msgtext">${m.text || '(no text)'}</pre>
      ${m.links && m.links.length ? html`
        <div class="msglinks">
          <b class="small">Links in this message</b>
          <ul>${m.links.map((l, i) => html`<li key=${i}>${l}</li>`)}</ul>
          <p class="small muted">Links aren't clickable here, so you don't use up a link the persona needs.</p>
        </div>` : null}
    </div>`;
}

// ---- run detail ---------------------------------------------------------

// The live screen: a multipart JPEG stream shown in an <img>. The stream can
// end while the run is still going (the director caps one connection), so on
// an error we check the run and, if it is still active, connect again.
const STAGE_TEXT = {
  'waiting-for-node': ['Starting a machine for this run', 'Takes a minute or two when no machine is running.'],
  'starting': ['Starting the run', 'Pulling the browser image and starting the agent.'],
  'running': ['The agent is getting ready', 'The screen appears when it opens the browser.'],
};

// What the run is doing before its first frame; text only.
function LiveStatus({ stage, reason, started }) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(t); }, []);
  const t0 = started ? Date.parse(started) : NaN;
  const secs = Number.isFinite(t0) ? Math.max(0, Math.floor((now - t0) / 1000)) : null;
  const text = stage === 'failing' ? ['The run is failing to start', reason || ''] : STAGE_TEXT[stage];
  if (!text) return html`<p class="livewait">Waiting for the first frame…${secs === null ? '' : ' · ' + secs + ' s'}</p>`;
  return html`<div class="livewait livestatus"><p>${text[0]}${secs === null ? '' : ' · ' + secs + ' s'}</p><p class="muted small">${text[1]}</p></div>`;
}

function LiveView({ id, stage, reason, started }) {
  const [shown, setShown] = useState(false);
  const [nonce, setNonce] = useState(0);
  const timer = useRef(0);
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; clearTimeout(timer.current); };
  }, []);
  const stillActive = async () => {
    try {
      const d = await api(`/api/runs/${encodeURIComponent(id)}`);
      return !!(d && d.run && ACTIVE.has(d.run.state));
    } catch (e) { return true; /* director unreachable: keep trying while mounted */ }
  };
  const lost = async () => {
    setShown(false);
    clearTimeout(timer.current);
    const again = await stillActive();
    if (again && mounted.current) timer.current = setTimeout(() => { if (mounted.current) setNonce((n) => n + 1); }, 2000);
  };
  // A stream the director ends cleanly (its 15-minute cap) fires neither load
  // nor error, so the picture would freeze: reconnect on a timer instead.
  useEffect(() => {
    const t = setInterval(async () => {
      if (await stillActive() && mounted.current) setNonce((n) => n + 1);
    }, RECONNECT_MS);
    return () => clearInterval(t);
  }, [id]);
  const src = `/api/runs/${encodeURIComponent(id)}/live` + (nonce ? `?t=${nonce}` : '');
  return html`
    <img class="livefeed" src=${src} alt="Live view of the run" onLoad=${() => setShown(true)} onError=${lost}
      style=${shown ? '' : 'visibility:hidden'} />
    <span class="livetag"><span class="dot live"></span> Live</span>
    ${shown ? null : html`<${LiveStatus} stage=${stage} reason=${reason} started=${started} />`}`;
}

function RunPage({ id, onChange }) {
  const [data, setData] = useState(null);
  const [err, setErr] = useState('');
  const [missing, setMissing] = useState(false);
  const [duration, setDuration] = useState(0); // seconds, once the video's metadata is in
  const video = useRef(null);
  const lastState = useRef('');
  const failures = useRef(0);
  const [gaveUp, setGaveUp] = useState(false);

  usePoll(async (stale) => {
    try {
      const d = await api(`/api/runs/${encodeURIComponent(id)}`);
      if (stale()) return null;
      failures.current = 0;
      setData(d); setErr(''); setMissing(false); setGaveUp(false);
      const st = d && d.run ? d.run.state : '';
      const prev = lastState.current;
      lastState.current = st;
      if (prev && prev !== st) onChange();
      if (ACTIVE.has(st)) return ACTIVE_POLL_MS;
      // Just finished: look once more for files uploaded at the end.
      if (ACTIVE.has(prev)) return ACTIVE_POLL_MS;
      // A waiting run, or one with runs queued after it, can still change.
      return (d.wait && d.wait.status === 'open') || d.queued ? IDLE_POLL_MS : null;
    } catch (e) {
      if (stale()) return null;
      if (e.status === 404) { setMissing(true); return null; }
      setErr(e.message);
      const n = ++failures.current;
      // A finished run has nothing new to show; give up after a few tries.
      if (lastState.current && !ACTIVE.has(lastState.current) && n >= MAX_FINISHED_FAILURES) { setGaveUp(true); return null; }
      return RETRY_MS[Math.min(n, RETRY_MS.length) - 1];
    }
  }, [id]);

  if (missing) {
    return html`<section class="page"><p class="crumb"><a href="#/runs">Runs</a></p>
      <div class="empty"><b>Run not found</b>It may have been removed. <a href="#/runs">Back to runs</a>.</div></section>`;
  }
  if (!data) {
    return html`<section class="page"><p class="crumb"><a href="#/runs">Runs</a></p>
      ${err ? html`<div class="note bad">Could not load the run: ${err}</div>` : html`<p class="muted">Loading run…</p>`}</section>`;
  }

  const { run, bugs = [], artifacts = [], usage, wait, chain, queued } = data;
  const links = chain || [run];
  const byID = new Map(links.map((c) => [c.id, c]));
  const runLink = (rid) => {
    const c = byID.get(rid);
    return html`<a href=${runHref(rid)}>${c ? `${c.persona} · ${c.story}` : rid}</a>`;
  };
  const continuedBy = links.filter((c) => c.follows === run.id);
  const files = artifacts.filter((n) => n !== BLOCKED_NAME && n !== USAGE_NAME);
  const webm = artifacts.find((n) => n.toLowerCase().endsWith('.webm'));
  const started = parseTime(run.started), finished = parseTime(run.finished);
  const took = started ? fmtDuration((finished || new Date()) - started) : '';

  const seek = (secs) => {
    const v = video.current;
    if (!v) return;
    v.currentTime = secs;
    v.play().catch(() => {});
    v.focus();
  };

  return html`
    <section class="page">
      <p class="crumb"><a href="#/runs">Runs</a> / ${run.id}</p>
      <h1>${run.persona} · ${run.story} <${StateTag} state=${run.state} wait=${wait} /></h1>
      <p class="lede">${run.role ? `${run.role}. ` : ''}Started ${fmtFull(run.started)}${finished ? `, took ${took}` : took ? `, ${took} so far` : ''}.</p>
      ${err ? html`<div class="note bad banner">Lost contact with the director: ${err}. ${gaveUp ? 'Reload the page to try again.' : 'Retrying.'}</div>` : null}
      <div class="grid g-side">
        <div class="stack">
          <div class=${'player' + (ACTIVE.has(run.state) ? ' live' : '')}>
            ${ACTIVE.has(run.state) ? html`<${LiveView} key=${run.id} id=${run.id} stage=${data.stage} reason=${data.stageReason} started=${run.started} />` : webm ? html`<video ref=${video} key=${webm} src=${artifactURL(run.id, webm)} controls preload="metadata" playsinline
                onLoadedMetadata=${(e) => { const d = e.currentTarget.duration; setDuration(d > 0 ? d : 0); }}></video>`
              : html`<p>This run has no recording.</p>`}
          </div>
          <div class="card">
            <div class="row between"><h2>Bugs</h2><span class="small muted">${bugs.length && webm ? 'Click a bug with a time to jump to it.' : ''}</span></div>
            ${bugs.length ? html`<ul class="bugs">${bugs.map((b) => html`<li key=${b.id}><${Bug} bug=${b} runId=${run.id} artifacts=${artifacts} duration=${webm ? duration : 0} onSeek=${seek} /></li>`)}</ul>`
              : html`<p class="muted small" style="margin:0">${ACTIVE.has(run.state) ? 'No bugs reported so far.' : 'No bugs reported.'}</p>`}
          </div>
        </div>
        <div class="stack">
          <div class="card">
            <h2>Outcome</h2>
            ${run.outcome ? html`<p class="outcome-text">${run.outcome}</p>`
              : html`<p class="muted small" style="margin:0">${ACTIVE.has(run.state) ? 'Still running.' : 'No outcome recorded.'}</p>`}
          </div>
          ${chain || queued ? html`<${Chain} links=${links} current=${run.id} queued=${queued || []} />` : null}
          ${data.blocked && data.blocked.length ? html`<${BlockedHosts} hosts=${data.blocked} />` : null}
          <div class="card">
            <h2>Details</h2>
            <dl class="facts">
              <dt>Persona</dt><dd>${run.persona}</dd>
              ${run.role ? html`<dt>Role</dt><dd>${run.role}</dd>` : null}
              <dt>Story</dt><dd>${run.story}</dd>
              <dt>State</dt><dd><${StateTag} state=${run.state} wait=${wait} /></dd>
              ${wait ? html`<dt>Waiting for</dt><dd>${waitText(wait)}</dd>` : null}
              ${run.follows && run.via === 'continue' ? html`<dt>Continues</dt><dd>${runLink(run.follows)}</dd>` : null}
              ${run.follows && run.via === 'handoff' ? html`<dt>Handed off by</dt><dd>${runLink(run.follows)}</dd>` : null}
              ${continuedBy.length ? html`<dt>Continued by</dt><dd>${continuedBy.map((c, i) => html`${i ? ', ' : ''}${runLink(c.id)}`)}</dd>` : null}
              <dt>Started</dt><dd>${fmtFull(run.started) || '—'}</dd>
              <dt>Finished</dt><dd>${fmtFull(run.finished) || '—'}</dd>
              ${took ? html`<dt>Duration</dt><dd>${took}</dd>` : null}
              ${usage && usage.byModel ? html`<dt>Model</dt><dd>${fmtModels(usage.byModel)}</dd>` : null}
              ${usage ? html`<dt>Tokens</dt><dd>${fmtUsage(usage)}</dd>` : null}
              ${typeof data.cost === 'number' ? html`<dt>Cost</dt><dd>${fmtCost(data.cost)}</dd>` : null}
              ${run.session ? html`<dt>Session</dt><dd><code>${run.session}</code></dd>` : null}
            </dl>
          </div>
          <div class="card">
            <h2>Files</h2>
            ${files.length ? html`<ul class="files">${files.map((n) => html`
              <li key=${n}><a href=${artifactURL(run.id, n)} download=${n}>${n}</a>
                ${n === 'trace.zip' ? html`<span class="hint">Open it with <code>npx playwright show-trace trace.zip</code></span>` : null}</li>`)}</ul>`
              : html`<p class="muted small" style="margin:0">No files yet.</p>`}
          </div>
        </div>
      </div>
    </section>`;
}

// waitText says what a run waited for and how that ended.
function waitText(w) {
  if (w.status === 'open') return `${w.waitingFor}, until ${fmtFull(w.deadline)}`;
  if (w.status === 'met') return `${w.waitingFor}. Arrived: ${w.detail || 'yes'}`;
  return `${w.waitingFor}. Gave up: ${w.detail || 'it did not happen in time'}`;
}

// Chain lists the runs that followed one another, oldest first, and the
// runs queued to come after them. A waiting run that a later run continued
// shows as continued.
function Chain({ links, current, queued }) {
  const continued = new Set(links.filter((c) => c.via === 'continue' && c.follows).map((c) => c.follows));
  return html`
    <div class="card">
      <h2>Chain</h2>
      <ol class="chain">
        ${links.map((c) => html`
          <li key=${c.id} class=${c.id === current ? 'here' : ''}>
            ${c.id === current
              ? html`<b aria-current="page">${c.persona} · ${c.story}</b>`
              : html`<a href=${runHref(c.id)}>${c.persona} · ${c.story}</a>`}
            <${StateTag} state=${c.state} wait=${continued.has(c.id) ? { status: 'met' } : null} /></li>`)}
        ${queued.map((q) => html`
          <li key=${q.id} class="next"><span>${q.persona} · ${q.story}</span>
            <span class="tag idle">Up next</span></li>`)}
      </ol>
    </div>`;
}

// USAGE_NAME is the run's token count (runs.UsageName); the run page shows it
// in Details, not as a file.
const USAGE_NAME = 'usage.json';

// fmtUsage says how many tokens the agent read and wrote. Every turn re-reads
// the conversation so far, so most of what it reads comes from the cache,
// which costs a tenth of fresh input.
function fmtUsage(u) {
  const read = (u.input || 0) + (u.cacheRead || 0) + (u.cacheWrite || 0);
  const cached = read ? Math.round(((u.cacheRead || 0) / read) * 100) : 0;
  const turns = u.turns === 1 ? '1 turn' : `${u.turns || 0} turns`;
  return `${fmtCount(read)} read (${cached}% from cache), ${fmtCount(u.output || 0)} written, ${turns}`;
}

// fmtModels names the models that served the run, the busiest first; turn
// counts appear only when there is more than one.
function fmtModels(byModel) {
  const rows = Object.entries(byModel).sort((a, b) => (b[1].turns || 0) - (a[1].turns || 0));
  if (rows.length === 1) return rows[0][0];
  return rows.map(([m, c]) => `${m} (${c.turns === 1 ? '1 turn' : `${c.turns || 0} turns`})`).join(', ');
}

function fmtCount(n) {
  if (n >= 1e6) return `${(n / 1e6).toFixed(n >= 1e7 ? 0 : 1)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(n >= 1e4 ? 0 : 1)}k`;
  return String(n);
}

// BLOCKED_NAME is the director's saved list of refused hosts (runs.BlockedName);
// the run page shows it as its own card, not as a file.
const BLOCKED_NAME = 'blocked-hosts.json';
const MAX_BLOCKED = 50;

// BlockedHosts lists the hosts the network fence refused during the run. A
// page that loads fonts, scripts or payments from such a host looks broken
// to the persona. Allow adds the host to the project's extra hosts, which
// the director copies to the fence's allow-list.
function BlockedHosts({ hosts }) {
  const [allowed, setAllowed] = useState(() => new Set());
  const [busy, setBusy] = useState('');
  const [err, setErr] = useState('');
  useEffect(() => {
    let live = true;
    api('/api/project').then((p) => {
      if (live) setAllowed(new Set([...(p.extraHosts || []), ...(p.sites || []).map(hostOf)].map((h) => h.toLowerCase())));
    }).catch(() => {});
    return () => { live = false; };
  }, []);

  const allow = async (host) => {
    setBusy(host); setErr('');
    try {
      const p = await api('/api/project');
      const extra = p.extraHosts || [];
      if (!extra.map((h) => h.toLowerCase()).includes(host)) {
        await api('/api/project', { method: 'PUT', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ ...p, extraHosts: [...extra, host] }) });
      }
      setAllowed((a) => new Set(a).add(host));
    } catch (e) {
      setErr(e.message);
    } finally {
      setBusy('');
    }
  };

  const shown = hosts.slice(0, MAX_BLOCKED);
  return html`
    <div class="card">
      <h2>Blocked hosts</h2>
      <p class="small muted" style="margin-top:0">The network fence stopped the browser reaching these hosts, so parts of the site may not have loaded. Allow the ones the site needs, such as fonts, scripts or payments. The next run can reach them.</p>
      <ul class="files blocked">${shown.map((h) => html`
        <li key=${h}><code>${h}</code>
          ${allowed.has(h) ? html`<span class="small muted">Allowed</span>`
            : !allowable(h) ? html`<span class="small muted" title="IP addresses and internal names can't go on the allow-list">Not allowed</span>`
            : html`<button class="btn" type="button" disabled=${busy !== ''} onClick=${() => allow(h)}>${busy === h ? 'Allowing…' : 'Allow'}</button>`}</li>`)}</ul>
      ${hosts.length > shown.length ? html`<p class="small muted">${hosts.length - shown.length} more not shown.</p>` : null}
      ${err ? html`<div class="note bad">Could not allow the host: ${err}</div>` : null}
    </div>`;
}

// allowable mirrors the director's check (project.publicHostProblem) so the
// card never offers Allow for an IP address or an internal name.
const INTERNAL = /(^|\.)(internal|local|localhost|localdomain|svc|cluster\.local|arpa)$/;
function allowable(host) {
  if (!host.includes('.') || host.includes(':') || /(^|\.)(\d+|0x[0-9a-f]*)$/.test(host)) return false;
  if (/(^|[.-])\d{1,3}[.-]\d{1,3}[.-]\d{1,3}[.-]\d{1,3}([.-]|$)/.test(host)) return false;
  if (/(^|\.)(nip\.io|sslip\.io|xip\.io|traefik\.me|localtest\.me|lvh\.me|local\.gd|vcap\.me|lacolhost\.com)$/.test(host)) return false;
  return !INTERNAL.test(host);
}

function hostOf(site) {
  try { return new URL(site).hostname; } catch (_) { return ''; }
}


// A bug seeks only when its text carries a time that fits inside the
// recording; that also keeps clock times such as "Wed 16:00" from seeking.
// A live-recorded webm can report Infinity, which lets every time through.
function Bug({ bug, runId, artifacts, duration, onSeek }) {
  const found = stampOf(bug);
  const secs = found !== null && duration > 0 && found <= Math.ceil(duration) ? found : null;
  const seekable = secs !== null;
  const body = html`
    <span class=${'tag ' + (SEVERITY_TAG.get((bug.severity || '').toLowerCase()) || 'idle')}>${bug.severity || 'unrated'}</span>
    <span class="sum">${bug.summary}</span>
    ${secs !== null ? html`<span class="stamp">${fmtStamp(secs)}</span>` : html`<span></span>`}
    ${bug.step ? html`<span class="meta"><b>Step:</b> ${bug.step}</span>` : null}
    ${bug.evidence ? html`<span class="meta"><b>Evidence:</b> ${bug.evidence}</span>` : null}
    ${bug.note ? html`<span class="meta"><b>Note:</b> ${bug.note}</span>` : null}`;
  const issue = bug.issue_id ? html`<a class="bug-issue small" href=${issueHref(bug.issue_id)}>Open in Bugs</a>` : null;
  const tok = SHOT_TOKEN.exec(bug.evidence || '');
  const shot = tok && artifacts.includes(tok[0]) ? tok[0] : '';
  return html`${seekable
    ? html`<button type="button" class="bug seek" title=${'Jump to ' + fmtStamp(secs)} onClick=${() => onSeek(secs)}>${body}</button>`
    : html`<div class="bug">${body}</div>`}
    ${issue}
    ${shot ? html`<a class="bug-shot" href=${artifactURL(runId, shot)} target="_blank" rel="noopener" title="Open the full screenshot">
      <img src=${artifactURL(runId, shot)} alt=${'Screenshot for: ' + bug.summary} loading="lazy" /></a>` : null}`;
}


// Clear the static "Loading…" placeholder: render() appends, it doesn't replace.
const root = document.getElementById('root');
root.replaceChildren();
render(html`<${App} />`, root);
