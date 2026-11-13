// boardingparty board: runs list, start run, run detail.
//
// Everything the director sends is untrusted (bug text is agent-written), so
// it is only ever rendered as text through htm/Preact, which escapes it.
// Never use innerHTML or dangerouslySetInnerHTML here.
import { render } from './vendor/preact.module.js';
import { useState, useEffect, useRef, useCallback } from './vendor/hooks.module.js';
import { html, api, directorDown, confirmDialog, DialogHost, parseTime, sameDay, fmtWhen, artifactURL, runHref, usePoll, useParams, routeParams, setParams, PARAMS_EVENT, SEVERITY_TAG, SHOT_TOKEN } from './lib.js';
import { BugsPage, IssuePage, issueHref, openCount } from './bugs.js';
import { ProjectPage } from './project.js';
import { LeadPage, leadHref } from './leads.js';
import { Coverage, LEAD_STORY, isLead } from './coverage.js';
import { ConsoleCard, ConsoleBefore, fenced } from './console.js';
import { Timeline } from './timeline.js';
import { Shot } from './shot.js';
import { JourneysPage, JourneyPage } from './journeys.js';

const ACTIVE = new Set(['pending', 'running']);
// LOCAL is the CLI's own board, which listens on loopback only; anywhere
// else the board is served through the cloud.
const LOCAL = ['127.0.0.1', 'localhost', '[::1]'].includes(location.hostname);
// Inside the cloud's frame, tell the cloud page the route so its address
// follows the board and a run's link can be shared. Only the cloud may frame
// the board (frame-ancestors), so the route goes nowhere else.
if (window.parent !== window) {
  const tell = () => window.parent.postMessage({ type: 'bp:route', hash: location.hash }, '*');
  addEventListener('hashchange', tell);
  addEventListener(PARAMS_EVENT, tell);
  tell();
}
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
  const parts = (hash || '').replace(/^#\/?/, '').split('?')[0].split('/');
  if (parts[0] === 'project') {
    const section = ['general', 'sites', 'agent', 'inbox', 'files', 'roles'].includes(parts[1]) ? parts[1] : 'general';
    return { page: 'project', section, roleN: section === 'roles' && /^[1-9][0-9]*$/.test(parts[2] || '') ? parts[2] : '' };
  }
  if (parts[0] === 'inbox') return { page: 'inbox' };
  if (parts[0] === 'bugs' && parts[1]) {
    try { return { page: 'issue', id: decodeURIComponent(parts[1]) }; } catch (_) { /* fall through */ }
  }
  if (parts[0] === 'bugs') return { page: 'bugs' };
  if (parts[0] === 'leads' && parts[1]) {
    try { return { page: 'lead', id: decodeURIComponent(parts[1]) }; } catch (_) { /* fall through */ }
  }
  if (parts[0] === 'journeys') return { page: 'journeys' };
  if (parts[0] === 'journey' && parts[1]) {
    try { return { page: 'journey', id: decodeURIComponent(parts[1]) }; } catch (_) { /* fall through */ }
  }
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
      setProject(await api('api/project'));
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
      const q = (await api('api/queue')) || [];
      setQueue(q);
      return q;
    } catch (_) { return null; }
  }, []);

  // Like the queue, the status rides along; the last known one stays on error.
  const loadStatus = useCallback(async () => {
    try { setStatus(await api('api/status')); } catch (_) { /* keep the last status */ }
  }, []);

  const loadRuns = useCallback(async () => {
    try {
      const [rs] = await Promise.all([api('api/runs').then((x) => x || []), loadQueue(), loadStatus()]);
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

  // The personas each lead made live beside the project, not in it.
  const [children, setChildren] = useState({}); // lead name -> live children
  const leads = ((project && project.roles) || []).flatMap((r) => (r.personas || []).filter(isLead).map((p) => p.name));
  usePoll(async (stale) => {
    const next = {};
    for (const n of leads) {
      try {
        const d = await api(`api/leads/${encodeURIComponent(n)}`);
        next[n] = (d.children || []).filter((c) => !c.stopped);
      } catch (_) { /* optional: cards and coverage just lose "made by" */ }
    }
    if (stale()) return null;
    setChildren(next);
    return SIDEBAR_POLL_MS;
  }, [route.page, leads.join('\n')]);
  const full = withChildren(project, children);

  return html`
    <div class="app">
      <${Sidebar} route=${route} project=${project} runs=${runs} leads=${leads} children=${children} />
      <main>
        <${RunStrip} director=${director} active=${active} status=${status} onChange=${loadRuns} />
        ${route.page === 'run' ? html`<${RunPage} key=${route.id} id=${route.id} onChange=${loadRuns} />`
          : route.page === 'project' ? html`<${ProjectPage} section=${route.section} roleN=${route.roleN} onSaved=${loadProject} onDirty=${onDirty} />`
          : route.page === 'inbox' ? html`<${InboxPage} project=${project} />`
          : route.page === 'bugs' ? html`<${BugsPage} />`
          : route.page === 'issue' ? html`<${IssuePage} key=${route.id} id=${route.id} />`
          : route.page === 'lead' ? html`<${LeadPage} key=${route.id} name=${route.id} />`
          : route.page === 'journeys' ? html`<${JourneysPage} />`
          : route.page === 'journey' ? html`<${JourneyPage} key=${route.id} id=${route.id} />`
          : html`<${RunsPage} project=${full} projectErr=${projectErr} runs=${runs} runsErr=${runsErr}
                    queue=${queue} status=${status} active=${active} onStarted=${loadRuns} />`}
      </main>
      <${DialogHost} />
    </div>`;
}

// withChildren adds a role per persona a lead made, marked with its lead,
// the way the director merges them when it runs.
function withChildren(project, children) {
  if (!project) return project;
  const extra = Object.entries(children).flatMap(([lead, cs]) => cs.map((c) => ({
    name: c.name, madeBy: lead, personas: [c.persona || { name: c.name }], stories: c.stories || [] })));
  return extra.length ? { ...project, roles: [...(project.roles || []), ...extra] } : project;
}

function Sidebar({ route, project, runs, leads, children }) {
  const on = (p) => (route.page === p || (p === 'runs' && route.page === 'run') || (p === 'bugs' && route.page === 'issue')
    || (p === 'journeys' && route.page === 'journey') ? 'on' : '');
  const [open, setOpen] = useState(null);
  usePoll(async (stale) => {
    try {
      const d = await api('api/issues');
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
        <a href="#/journeys" class=${on('journeys')}>Journeys</a>
        <a href="#/bugs" class=${on('bugs')}>Bugs ${open ? html`<span class="count" title="Open bugs">${open}</span>` : null}</a>
        <a href="#/inbox" class=${on('inbox')}>Inbox</a>
        <a href="#/project" class=${on('project')}>Project</a>
      </nav>
      ${leads.length ? html`<nav aria-label="Leads" class="leads">
        <span class="sublabel">Leads</span>
        ${leads.map((n) => html`<a key=${n} href=${leadHref(n)} class=${route.page === 'lead' && route.id === n ? 'on' : ''}>${n}
          ${children[n] ? html`<span class="count" title="Personas it made">${children[n].length}</span>` : null}</a>`)}
      </nav>` : null}
      ${LOCAL ? html`<div class="foot">Local board. Close the terminal to stop it.</div>` : null}
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
    act(() => api('api/pause', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ paused: pausing }) }));
  };
  const stop = async () => {
    if (!(await confirmDialog('Stop this run now? Its recording so far is kept. The next run in Up next then starts; pause first to keep things quiet.', { confirm: 'Stop run', danger: true }))) return;
    act(() => api(`api/runs/${encodeURIComponent(active.id)}/stop`, { method: 'POST' }));
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
  const start = useParams().start === '1'; // in the address, so #/runs?start=1 opens the dialog
  const canStart = !!project && allPersonas(project).length > 0;
  return html`
    <section class="page wide">
      <div class="pagehead">
        <div>
          <h1>Runs</h1>
          <p class="lede">One persona working through one story on your staging site. Runs go one at a time.</p>
        </div>
        ${canStart ? html`<button class="btn primary" type="button" onClick=${() => setParams({ start: true })}>Start run</button>` : null}
      </div>
      ${canStart ? html`<${StartRunDialog} open=${start} project=${project} active=${active} status=${status} onStarted=${onStarted} />` : null}
      <div class="stack">
        ${projectErr ? html`<div class="note bad">Could not load the project: ${projectErr}</div>` : null}
        ${project === null
          ? html`<div class="empty"><b>Set up your project first</b>Runs need a site, a role, a persona and a story.
              <div style="margin-top:12px"><a class="btn primary" href="#/project">Set up your project</a></div></div>`
          : project ? html`<div class="runs-top">
              ${canStart ? null : html`<div class="empty"><b>Add a persona to start a run</b>
                Your project has no personas yet. <a href="#/project">Edit the project</a>.</div>`}
              ${queue.length || (status && status.due.length) ? html`<${UpNext} queue=${queue} due=${status ? status.due : []} zone=${status && status.timezone} onChange=${onStarted} />` : null}
            </div>` : null}
        ${runsErr ? html`<div class="note bad">Could not load runs: ${runsErr}</div>` : null}
        ${runs && runs.length === 0 && project !== null
          ? html`<div class="empty"><b>No runs yet.</b>Choose Start run to send the first persona in.</div>` : null}
        ${runs && runs.length > 0 ? html`<${RunsBoard} runs=${runs} project=${project} />` : null}
      </div>
    </section>`;
}

// StartRunDialog picks a persona and a story in a modal opened from the
// page head. Closing it, or starting a run, takes start=1 out of the address.
function StartRunDialog({ open, project, active, status, onStarted }) {
  const ref = useRef(null);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    if (open && !el.open) { el.showModal(); el.querySelector('select')?.focus(); }
    if (!open && el.open) el.close();
  }, [open]);
  // Every way out goes through the address; the effect above closes it.
  const close = () => setParams({ start: null });
  return html`
    <dialog ref=${ref} class="modal" aria-labelledby="sr-title"
        onCancel=${(e) => { e.preventDefault(); close(); }}
        onClick=${(e) => { if (e.target === ref.current) close(); }}>
      ${open ? html`<${StartRun} project=${project} active=${active} status=${status} onStarted=${onStarted} onClose=${close} />` : null}
    </dialog>`;
}

function StartRun({ project, active, status, onStarted, onClose }) {
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

  // A persona and story with a recorded journey can be replayed instead:
  // the same steps in a browser, with no model.
  const [journey, setJourney] = useState(null);
  const [how, setHow] = useState('ai');
  const [rev, setRev] = useState(0);
  useEffect(() => {
    let live = true;
    setJourney(null);
    if (!p || !s) return undefined;
    api(`api/journeys?persona=${encodeURIComponent(p)}&story=${encodeURIComponent(s)}`)
      .then((js) => { if (live) setJourney((js || []).find((j) => (j.revisions || []).length) || null); })
      .catch(() => {}); // no journeys to offer: an AI run, as before
    return () => { live = false; };
  }, [p, s]);
  const revisions = journey ? journey.revisions : [];
  const replay = how === 'replay' && revisions.length > 0;
  const pickedRev = revisions.some((r) => r.n === rev) ? rev : (journey && journey.liveRev) || (revisions[0] && revisions[0].n) || 0;
  const disabled = busy || !p || !s || (replay && !pickedRev);

  const submit = async (e) => {
    e.preventDefault();
    if (disabled) return;
    setBusy(true); setErr('');
    try {
      const run = replay
        ? await api(`api/journeys/${encodeURIComponent(journey.id)}/replay`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ revision: pickedRev }),
        })
        : await api('api/runs', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ persona: p, story: s }),
        });
      await onStarted();
      onClose();
      if (run && run.id) location.hash = runHref(run.id); // a queued run (every replay) has no id yet
    } catch (e2) {
      setErr(e2.message); // 400 invalid is shown verbatim
      onStarted();
    } finally {
      setBusy(false);
    }
  };

  return html`
    <form class="startrun" onSubmit=${submit}>
      <h2 id="sr-title">Start run</h2>
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
      ${goal ? html`<p class="goal">${goal}</p>` : null}
      ${revisions.length ? html`<div class="field">
        <span class="label" id="sr-how">How</span>
        <div class="viewtoggle" role="group" aria-labelledby="sr-how">
          <button type="button" class=${!replay ? 'on' : ''} aria-pressed=${!replay} disabled=${busy} onClick=${() => { setHow('ai'); setErr(''); }}>AI run</button>
          <button type="button" class=${replay ? 'on' : ''} aria-pressed=${replay} disabled=${busy} onClick=${() => { setHow('replay'); setErr(''); }}>Replay journey</button>
        </div>
      </div>` : null}
      ${replay ? html`<div class="field">
        <label for="sr-rev">Revision</label>
        <select id="sr-rev" value=${pickedRev} disabled=${busy} onChange=${(e) => { setRev(Number(e.currentTarget.value)); setErr(''); }}>
          ${revisions.map((r) => html`<option key=${r.n} value=${r.n}>${revLabel(r, journey.liveRev)}</option>`)}
        </select>
        <p class="hint">Plays the recorded steps back in a browser with fresh test data. No model, so it costs nothing.</p>
      </div>` : null}
      ${active ? html`<div class="note">A run is active (<a href=${runHref(active.id)}>${active.persona} · ${active.story}</a>). A new run waits in Up next and starts when it finishes.</div>` : null}
      ${err ? html`<div class="note bad" role="alert">${err.charAt(0).toUpperCase() + err.slice(1)}</div>` : null}
      <div class="row end">
        <button class="btn" type="button" onClick=${onClose}>Cancel</button>
        <button class="btn primary" type="submit" disabled=${disabled}>${busy ? (active ? 'Adding…' : 'Starting…') : active ? 'Add to queue' : replay ? 'Start replay' : 'Start run'}</button>
      </div>
    </form>`;
}

// revLabel names a journey revision in the replay picker.
const revLabel = (r, liveRev) => (r.kind === 'bug' ? `Bug revision ${r.n}` : r.n === liveRev ? `Revision ${r.n} (live)` : `Revision ${r.n}`);

// replayLabel is how runs lists name a replay run.
const replayLabel = (r) => `Replay · revision ${r.revision}`;

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
      await api(`api/queue/${encodeURIComponent(q.id)}`, { method: 'DELETE' });
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
      ${live && tick ? html`<img src=${`api/runs/${encodeURIComponent(id)}/live.jpg?t=${tick}`} alt="Live view of the run"
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
        : html`<span>No screen saved</span>`}
    </a>`;
}

// ---- runs board ----------------------------------------------------------
// The runs as cards grouped by day (or the compact table), with filters by
// state, persona, story, bugs and text. Filters last for the browser
// session; the view choice is remembered.

const STATE_FILTERS = [
  ['all', 'All', () => true],
  ['live', 'Live', (r) => ACTIVE.has(r.state)],
  ['waiting', 'Waiting', (r) => r.state === 'waiting'],
  ['done', 'Done', (r) => r.state === 'done'],
  ['problem', 'Failed or stuck', (r) => r.state === 'failed' || r.state === 'stuck'],
];
const VIEW_KEY = 'bp.runs.view';
const VIEWS = ['cards', 'list', 'coverage'];
const NO_FILTER = { state: 'all', persona: '', story: '', bugs: false, q: '' };

function stored(store, key, fallback) {
  try { const v = JSON.parse(store.getItem(key)); return v ?? fallback; } catch (_) { return fallback; }
}
function keep(store, key, v) { try { store.setItem(key, JSON.stringify(v)); } catch (_) { /* private mode */ } }

// dayLabel names the day a run started: Today, Yesterday, or the date.
function dayLabel(d) {
  const now = new Date();
  const y = new Date(now); y.setDate(now.getDate() - 1);
  if (sameDay(d, now)) return 'Today';
  if (sameDay(d, y)) return 'Yesterday';
  return d.toLocaleDateString('en-GB', { weekday: 'short', day: 'numeric', month: 'short', year: d.getFullYear() === now.getFullYear() ? undefined : 'numeric' });
}

function RunsBoard({ runs, project }) {
  // Filters and view live in the address; the view also stays the
  // browser's default.
  const p = useParams();
  const f = { state: STATE_FILTERS.some(([k]) => k === p.state) ? p.state : 'all',
    persona: p.persona || '', story: p.story || '', bugs: p.bugs === '1', q: p.q || '' };
  const view = VIEWS.includes(p.view) ? p.view : (VIEWS.includes(stored(localStorage, VIEW_KEY, '')) ? stored(localStorage, VIEW_KEY, '') : 'cards');
  const set = (patch) => setParams({ ...patch, ...('state' in patch ? { state: patch.state === 'all' ? '' : patch.state } : {}) });
  const pickView = (v) => { keep(localStorage, VIEW_KEY, v); setParams({ view: v }); };

  const personas = [...new Set(runs.map((r) => r.persona))].sort();
  const stories = [...new Set(runs.filter((r) => !f.persona || r.persona === f.persona).map((r) => r.story))].sort();
  const q = f.q.trim().toLowerCase();
  // Every filter but the state one, so each state chip can count its runs.
  const base = runs.filter((r) => (!f.persona || r.persona === f.persona) && (!f.story || r.story === f.story)
    && (!f.bugs || r.bugs > 0)
    && (!q || `${r.persona} ${r.role || ''} ${r.story} ${r.outcome || ''}`.toLowerCase().includes(q)));
  const test = (STATE_FILTERS.find(([k]) => k === f.state) || STATE_FILTERS[0])[2];
  const shown = base.filter(test);
  const filtered = f.state !== 'all' || f.persona || f.story || f.bugs || q;
  const madeBy = new Map(((project && project.roles) || []).filter((r) => r.madeBy).flatMap((r) => (r.personas || []).map((p) => [p.name, r.madeBy])));
  const coverage = view === 'coverage';

  const groups = [];
  for (const r of shown) {
    const d = parseTime(r.started);
    const label = d ? dayLabel(d) : 'Not started';
    const g = groups[groups.length - 1];
    if (g && g.label === label) g.runs.push(r); else groups.push({ label, runs: [r] });
  }

  return html`
    <div class="runsboard">
      <div class="filters" role="group" aria-label="Filter runs">
        ${coverage ? html`<span></span>` : html`<div class="chips">
          ${STATE_FILTERS.map(([k, label, t]) => {
            const n = base.filter(t).length;
            return html`<button key=${k} type="button" class=${'chip' + (f.state === k ? ' on' : '') + (k === 'live' && n ? ' live' : '')}
              aria-pressed=${f.state === k} onClick=${() => set({ state: k })}>${k === 'live' && n ? html`<span class="dot live"></span>` : null}${label} <span class="n">${n}</span></button>`;
          })}
          <button type="button" class=${'chip' + (f.bugs ? ' on' : '')} aria-pressed=${f.bugs} onClick=${() => set({ bugs: !f.bugs })}>With bugs</button>
        </div>`}
        <div class="pickers">
          ${coverage ? null : html`<select aria-label="Persona" value=${f.persona} onChange=${(e) => set({ persona: e.currentTarget.value, story: '' })}>
            <option value="">All personas</option>${personas.map((p) => html`<option key=${p} value=${p}>${p}</option>`)}
          </select>
          <select aria-label="Story" value=${f.story} onChange=${(e) => set({ story: e.currentTarget.value })}>
            <option value="">All stories</option>${stories.map((st) => html`<option key=${st} value=${st}>${st}</option>`)}
          </select>
          <input type="search" placeholder="Search outcomes" aria-label="Search outcomes" value=${f.q} onInput=${(e) => set({ q: e.currentTarget.value })} />`}
          <div class="viewtoggle" role="group" aria-label="View">
            <button type="button" class=${view === 'cards' ? 'on' : ''} aria-pressed=${view === 'cards'} onClick=${() => pickView('cards')}>Cards</button>
            <button type="button" class=${view === 'list' ? 'on' : ''} aria-pressed=${view === 'list'} onClick=${() => pickView('list')}>List</button>
            <button type="button" class=${coverage ? 'on' : ''} aria-pressed=${coverage} onClick=${() => pickView('coverage')}>Coverage</button>
          </div>
        </div>
      </div>
      ${coverage ? html`<${Coverage} project=${project} runs=${runs}
            onPick=${(persona, story) => set({ ...NO_FILTER, persona, story, view: 'cards' })} />`
        : !shown.length ? html`<div class="empty"><b>No runs match.</b>${filtered ? html`<button type="button" class="link" onClick=${() => set(NO_FILTER)}>Clear the filters</button>` : null}</div>`
        : view === 'list' ? html`<${RunsTable} runs=${shown} />`
        : groups.map((g) => html`<section key=${g.label} class="daygroup">
            <h2>${g.label} <span class="muted">${g.runs.length === 1 ? '1 run' : `${g.runs.length} runs`}</span></h2>
            <div class="runcards">${g.runs.map((r) => html`<${RunCard} key=${r.id} r=${r} madeBy=${madeBy.get(r.persona)} />`)}</div>
          </section>`)}
    </div>`;
}

function RunCard({ r, madeBy }) {
  const active = ACTIVE.has(r.state);
  const started = parseTime(r.started), finished = parseTime(r.finished);
  const took = started && finished ? fmtDuration(finished - started) : '';
  const out = firstLine(r.outcome);
  const lead = r.story === LEAD_STORY;
  const who = madeBy ? `made by ${madeBy}` : r.role && r.role !== r.persona ? r.role : '';
  return html`
    <article class=${'runcard' + (active ? ' live' : '') + (r.state === 'failed' || r.state === 'stuck' ? ' problem' : '')}
      onClick=${() => { location.hash = runHref(r.id); }}>
      <div class="rc-shot">
        ${lead ? html`<div class="rc-lead"><b>Lead run</b><span>Reads runs and decides what to test next. No browser.</span></div>`
          : active ? html`<${Thumb} key=${r.id} id=${r.id} stage=${r.stage} live=${r.live} />` : html`<${EndedThumb} key=${'end-' + r.id} id=${r.id} />`}
        <div class="rc-tags">
          <${StateTag} state=${r.state} wait=${r.wait} />
          ${r.kind === 'replay' ? html`<span class="tag idle">${replayLabel(r)}</span>` : null}
          ${r.bugs ? html`<span class="tag bad">${r.bugs === 1 ? '1 bug' : `${r.bugs} bugs`}</span>` : null}
          ${r.blocked ? html`<span class="tag wait" title="The network fence refused hosts the site tried to load. Open the run to allow them.">${r.blocked} blocked</span>` : null}
        </div>
      </div>
      <div class="rc-body">
        <div class="rc-who"><a href=${runHref(r.id)} onClick=${(e) => e.stopPropagation()}>${r.persona}</a>${who ? html` <span class="muted">${who}</span>` : null}</div>
        <div class="rc-meta"><span class="rc-story">${lead ? 'Lead run' : r.story}</span><span title=${fmtFull(r.started)}>${started ? started.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' }) : ''}</span>${took ? html`<span>${took}</span>` : null}${typeof r.cost === 'number' ? html`<span>${fmtCost(r.cost)}</span>` : null}</div>
        ${r.state === 'waiting' && r.wait && r.wait.status === 'open'
          ? html`<p class="rc-out wait">Waiting for ${r.wait.waitingFor}, gives up ${fmtWhen(r.wait.deadline)}</p>`
          : html`<p class="rc-out" title=${out}>${out || html`<span class="muted">${active ? 'In progress' : 'No outcome recorded'}</span>`}</p>`}
      </div>
    </article>`;
}

function RunsTable({ runs }) {
  const open = (id) => () => { location.hash = runHref(id); };
  return html`
    <div class="card flush tablewrap">
      <table>
        <thead><tr><th></th><th>Persona</th><th>Story</th><th>State</th><th>Started</th><th class="num">Cost</th><th>Outcome</th></tr></thead>
        <tbody>
          ${runs.map((r) => html`
            <tr class="click" key=${r.id} onClick=${open(r.id)}>
              <td class="thumbcell">${ACTIVE.has(r.state) ? html`<${Thumb} key=${r.id} id=${r.id} stage=${r.stage} live=${r.live} />` : html`<${EndedThumb} key=${'end-' + r.id} id=${r.id} />`}</td>
              <td><a href=${runHref(r.id)} onClick=${(e) => e.stopPropagation()}>${r.persona}</a>
                ${r.role ? html`<div class="small muted">${r.role}</div>` : null}</td>
              <td>${r.story}${ACTIVE.has(r.state) ? html` <a class="small" href=${runHref(r.id)} onClick=${(e) => e.stopPropagation()}>Watch</a>` : null}
                ${r.kind === 'replay' ? html`<div class="small muted">${replayLabel(r)}</div>` : null}</td>
              <td><${StateTag} state=${r.state} wait=${r.wait} />${r.blocked ? html` <span class="tag wait" title="The network fence refused hosts the site tried to load. Open the run to allow them.">${r.blocked} blocked</span>` : null}
                ${r.state === 'waiting' && r.wait && r.wait.status === 'open'
                  ? html`<div class="waitline small muted">Waiting for ${r.wait.waitingFor} · gives up ${fmtWhen(r.wait.deadline)}</div>` : null}</td>
              <td class="when" title=${fmtFull(r.started)}>${fmtWhen(r.started)}</td>
              <td class="when num">${typeof r.cost === 'number' ? fmtCost(r.cost) : '—'}</td>
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
  // Persona, tab and the open message live in the address.
  const p = useParams();
  const persona = p.persona || '';
  const tab = p.tab === 'kept' ? 'kept' : 'received';
  const openID = p.m || '';
  const setOpenID = (id) => setParams({ m: id });
  const [msgs, setMsgs] = useState(null);
  const [err, setErr] = useState('');
  const names = [...new Set(allPersonas(project).map((p) => p.name))];
  const loaded = useRef(false); // the first load for this view has happened

  // Re-poll while visible; a hidden tab waits and checks again, but the first
  // load of a view always goes ahead.
  usePoll(async (stale) => {
    if (document.hidden && loaded.current) return INBOX_POLL_MS;
    try {
      const q = `limit=${INBOX_LIMIT}` + (persona ? `&persona=${encodeURIComponent(persona)}` : '') + (tab === 'kept' ? '&filtered=1' : '');
      const list = (await api(`api/messages?${q}`)) || [];
      if (stale()) return null;
      setMsgs(list); setErr(''); loaded.current = true;
    } catch (e) {
      if (stale()) return null;
      setErr(e.message);
    }
    return INBOX_POLL_MS;
  }, [persona, tab]);

  const pick = (patch) => { loaded.current = false; setMsgs(null); setParams({ ...patch, m: '' }); };

  return html`
    <section class="page">
      <h1>Inbox</h1>
      <p class="lede">Mail sent to the personas' addresses. The mailbox is set up on the <a href="#/project/inbox">Project</a> page.</p>
      <div class="stack">
        <div class="row inboxbar">
          <div class="field">
            <label for="ib-persona">Persona</label>
            <select id="ib-persona" value=${persona} onChange=${(e) => pick({ persona: e.currentTarget.value })}>
              <option value="" selected=${!persona}>All</option>
              ${names.map((n) => html`<option key=${n} value=${n} selected=${persona === n}>${n}</option>`)}
            </select>
          </div>
          <div class="tabs" role="tablist">
            <button type="button" role="tab" aria-selected=${tab === 'received'} class=${tab === 'received' ? 'on' : ''} onClick=${() => pick({ tab: '' })}>Received</button>
            <button type="button" role="tab" aria-selected=${tab === 'kept'} class=${tab === 'kept' ? 'on' : ''} onClick=${() => pick({ tab: 'kept' })}>Kept out</button>
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
    api(`api/messages/${encodeURIComponent(id)}`).then((x) => { if (live) setM(x); }).catch((e) => { if (live) setErr(e.message); });
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
      const d = await api(`api/runs/${encodeURIComponent(id)}`);
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
  const src = `api/runs/${encodeURIComponent(id)}/live` + (nonce ? `?t=${nonce}` : '');
  return html`
    <img class="livefeed" src=${src} alt="Live view of the run" onLoad=${() => setShown(true)} onError=${lost}
      style=${shown ? '' : 'visibility:hidden'} />
    <span class="livetag"><span class="dot live"></span> Live</span>
    ${shown ? null : html`<${LiveStatus} stage=${stage} reason=${reason} started=${started} />`}`;
}

// frameAt returns the index of the last kept frame at or before ms (the
// first frame when ms is earlier than all of them).
function frameAt(frames, ms) {
  let lo = 0, hi = frames.length - 1, best = 0;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (frames[mid] <= ms) { best = mid; lo = mid + 1; } else hi = mid - 1;
  }
  return best;
}

// FrameView shows the kept frame at a moment of the run: scrolled back during
// a live run, or the replay of a run that has no recording.
function FrameView({ id, frames, at, started, active, playing, onPlay, onLive }) {
  const n = frameAt(frames, at == null ? frames[frames.length - 1] : at);
  const t0 = parseTime(started);
  const secs = t0 ? Math.max(0, Math.floor((frames[n] - t0) / 1000)) : 0;
  const clock = `${Math.floor(secs / 60)}:${String(secs % 60).padStart(2, '0')}`;
  const next = n + 1 < frames.length ? artifactFrame(id, n + 1) : '';
  return html`
    <img class="livefeed" src=${artifactFrame(id, n)} alt=${'The screen at ' + clock} />
    ${next ? html`<link rel="prefetch" href=${next} />` : null}
    <div class="framebar">
      <button type="button" class="btn small" onClick=${() => onPlay(!playing)}>${playing ? 'Pause' : 'Play'}</button>
      <span>${active ? 'Scrolled back to ' : 'Replay at '}${clock}</span>
      ${active ? html`<button type="button" class="btn small primary" onClick=${onLive}><span class="dot live"></span> Back to live</button>` : null}
    </div>`;
}

const artifactFrame = (id, n) => `api/runs/${encodeURIComponent(id)}/rewind/${n}`;

function RunPage({ id, onChange }) {
  const [data, setData] = useState(null);
  const [err, setErr] = useState('');
  const [missing, setMissing] = useState(false);
  const [duration, setDuration] = useState(0); // seconds, once the video's metadata is in
  const video = useRef(null);
  const lastState = useRef('');
  const failures = useRef(0);
  const [gaveUp, setGaveUp] = useState(false);
  const [lit, setLit] = useState(''); // bug id picked on the timeline
  const [conFocus, setConFocus] = useState(null);
  const [frames, setFrames] = useState([]); // kept screen frames, Unix ms
  const [scrub, setScrub] = useState(null); // Unix ms shown instead of live/end; null = live
  const [playing, setPlaying] = useState(false);
  const runActive = !!(data && data.run && ACTIVE.has(data.run.state));
  // ?t= is seconds into the run: a link opens there once, and scrubbing or
  // pausing the video writes it back so the address can be shared.
  const wantT = useRef(routeParams().t);
  useEffect(() => {
    const t = Number(wantT.current);
    if (wantT.current == null || !data) return;
    if (!(t >= 0)) { wantT.current = null; return; }
    const st = parseTime(data.run.started);
    const hasVideo = (data.artifacts || []).some((n) => n.toLowerCase().endsWith('.webm'));
    if (st && frames.length && (runActive || !hasVideo)) {
      wantT.current = null;
      setScrub(frames[frameAt(frames, st.getTime() + t * 1000)]);
    } else if (hasVideo && !runActive && video.current && duration) {
      wantT.current = null;
      video.current.currentTime = Math.min(t, duration);
    }
  }, [data, frames, duration, runActive]);
  useEffect(() => {
    if (wantT.current != null || playing || !data) return;
    const st = parseTime(data.run.started);
    if (scrub === null) setParams({ t: '' });
    else if (st) setParams({ t: Math.max(0, Math.round((scrub - st.getTime()) / 1000)) });
  }, [scrub, playing]);
  // The kept frames grow while the run is live; a finished run's are fixed.
  useEffect(() => {
    let live = true, t = 0;
    const load = async () => {
      try {
        const d = await api(`api/runs/${encodeURIComponent(id)}/rewind`);
        if (live) setFrames(d.frames || []);
      } catch (_) { /* no frames: scrubbing stays off */ }
      if (live && runActive) t = setTimeout(load, 2000);
    };
    load();
    return () => { live = false; clearTimeout(t); };
  }, [id, runActive]);
  // Playing steps through kept frames four times faster than they were taken.
  useEffect(() => {
    if (!playing || !frames.length) return;
    const t = setInterval(() => setScrub((cur) => {
      const n = frameAt(frames, cur == null ? frames[0] : cur) + 1;
      if (n < frames.length) return frames[n];
      setPlaying(false);
      return runActive ? null : frames[frames.length - 1];
    }), 250);
    return () => clearInterval(t);
  }, [playing, frames, runActive]);
  useEffect(() => { if (!lit) return; const t = setTimeout(() => setLit(''), 2400); return () => clearTimeout(t); }, [lit]);

  usePoll(async (stale) => {
    try {
      const d = await api(`api/runs/${encodeURIComponent(id)}`);
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
  const active = ACTIVE.has(run.state);
  const events = data.console || [];
  const blocked = data.blocked || [];
  const conErrors = events.filter((e) => e.level === 'error' && !fenced(e, blocked)).length;
  // Kept frames replace the video while live, or when there is no video.
  const framesMode = frames.length > 0 && (active || !webm);
  const toTime = (iso) => { const t = parseTime(iso); if (t) { setPlaying(false); setScrub(frames[frameAt(frames, t.getTime())]); } };
  const toBug = (i) => {
    const b = bugs[i];
    const secs = b && webm && !framesMode ? stampOf(b) : null;
    if (framesMode && b) toTime(b.created);
    if (secs !== null && (!duration || secs <= Math.ceil(duration))) seek(secs);
    setLit(b ? b.id : '');
    const el = document.getElementById('bug-' + i);
    if (el) el.scrollIntoView({ block: 'nearest', behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' });
  };
  const toEvent = (i) => {
    if (framesMode && events[i]) toTime(events[i].at);
    setConFocus((f) => ({ i, n: (f ? f.n : 0) + 1, quiet: framesMode }));
  };
  const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;
  const replay = run.kind === 'replay';

  return html`
    <section class="page runpage">
      <p class="crumb"><a href="#/runs">Runs</a> / ${run.id}</p>
      <header class="runhead">
        <h1>${run.persona} <span class="muted">on</span> ${run.story} <${StateTag} state=${run.state} wait=${wait} /></h1>
        <p class="runverdict">${run.outcome || (active ? (replay ? 'Replaying. The outcome appears when the journey ends.' : 'Running. The outcome appears when the persona finishes.') : 'No outcome recorded.')}</p>
        <ul class="readout">
          ${replay ? html`<li><b>${replayLabel(run)}</b></li>` : null}
          <li>${run.role || 'Persona'}</li>
          <li>Started ${fmtFull(run.started)}</li>
          ${took ? html`<li><b>${took}</b>${active ? ' so far' : ''}</li>` : null}
          ${typeof data.cost === 'number' ? html`<li><b>${fmtCost(data.cost)}</b></li>` : null}
          ${usage ? html`<li><b>${usage.turns || 0}</b> ${usage.turns === 1 ? 'turn' : 'turns'}</li>` : null}
          <li><a href="#bugs" onClick=${(e) => { e.preventDefault(); document.getElementById('bugs').scrollIntoView({ block: 'start' }); }}><b>${bugs.length}</b> ${bugs.length === 1 ? 'bug' : 'bugs'}</a></li>
          ${run.story !== LEAD_STORY ? html`<li><a href="#console" class=${conErrors ? 'bad-text' : ''} onClick=${(e) => { e.preventDefault(); document.getElementById('console').scrollIntoView({ block: 'start' }); }}><b>${conErrors}</b> console ${conErrors === 1 ? 'error' : 'errors'}</a></li>` : null}
          ${blocked.length ? html`<li><a href="#blocked" onClick=${(e) => { e.preventDefault(); document.getElementById('blocked').scrollIntoView({ block: 'start' }); }}>${plural(blocked.length, 'blocked host', 'blocked hosts')}</a></li>` : null}
        </ul>
      </header>
      ${err ? html`<div class="note bad banner">Lost contact with the director: ${err}. ${gaveUp ? 'Reload the page to try again.' : 'Retrying.'}</div>` : null}
      <div class="grid g-side">
        <div class="stack">
          <div class="recorder">
            <div class=${'player' + (active || framesMode ? ' live' : '')}>
              ${framesMode && (scrub !== null || !active) ? html`<${FrameView} id=${run.id} frames=${frames} at=${scrub} started=${run.started}
                  active=${active} playing=${playing} onPlay=${setPlaying} onLive=${() => { setPlaying(false); setScrub(null); }} />`
              : active ? html`<${LiveView} key=${run.id} id=${run.id} stage=${data.stage} reason=${data.stageReason} started=${run.started} />` : webm ? html`<video ref=${video} key=${webm} src=${artifactURL(run.id, webm)} controls preload="metadata" playsinline
                  onLoadedMetadata=${(e) => { const d = e.currentTarget.duration; setDuration(d > 0 ? d : 0); }}
                  onPause=${(e) => { if (wantT.current == null) setParams({ t: Math.round(e.currentTarget.currentTime) }); }}></video>`
                : html`<p>This run has no recording.</p>`}
            </div>
            <${Timeline} run=${run} bugs=${bugs} events=${run.story !== LEAD_STORY ? events : null} blocked=${blocked}
              active=${active} onBug=${toBug} onEvent=${toEvent}
              frames=${framesMode ? frames : null} scrub=${framesMode ? (scrub ?? (active ? null : frames[frames.length - 1])) : null}
              onScrub=${(ms) => { setPlaying(false); setScrub(frames[frameAt(frames, ms)]); }} />
          </div>
          <div class="card" id="bugs">
            <div class="row between"><h2>Bugs</h2><span class="small muted">${bugs.length && webm ? 'A bug with a time jumps the video there.' : ''}</span></div>
            ${bugs.length ? html`<ol class="bugs">${bugs.map((b, i) => html`<li key=${b.id} id=${'bug-' + i} class=${lit === b.id ? 'lit' : ''}>
              <${Bug} n=${i + 1} bug=${b} runId=${run.id} artifacts=${artifacts} duration=${webm ? duration : 0} onSeek=${seek} />
              <${ConsoleBefore} bug=${b} events=${events} blocked=${blocked} started=${run.started} /></li>`)}</ol>`
              : html`<p class="muted small" style="margin:0">${active ? 'No bugs reported so far.' : 'No bugs reported.'}</p>`}
          </div>
          ${run.story !== LEAD_STORY ? html`<${ConsoleCard} events=${events} blocked=${blocked} started=${run.started} active=${active} focus=${conFocus} />` : null}
        </div>
        <div class="stack">
          ${replay ? html`<${ReplayCard} run=${run} artifacts=${artifacts} />` : null}
          ${chain || queued ? html`<${Chain} links=${links} current=${run.id} queued=${queued || []} />` : null}
          ${blocked.length ? html`<${BlockedHosts} hosts=${blocked} />` : null}
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

// ReplayCard shows what a replay ran: the journey revision, its seed and
// the test data drawn from it, and the screen where it stopped. Values are
// shown as text: they come from agent-written code.
function ReplayCard({ run, artifacts }) {
  const has = (n) => artifacts.includes(n);
  const [res, setRes] = useState(null);
  const kept = has('replay-result.json');
  useEffect(() => {
    if (!kept) return undefined;
    let live = true;
    fetch(artifactURL(run.id, 'replay-result.json')).then((r) => (r.ok ? r.json() : null))
      .then((x) => { if (live) setRes(x); }).catch(() => {});
    return () => { live = false; };
  }, [run.id, kept]);
  const inputs = Object.entries((res && res.inputs) || {});
  return html`
    <div class="card replaycard">
      <h2>Replay</h2>
      <dl class="facts">
        <dt>Journey</dt><dd>Revision ${run.revision}</dd>
        <dt>Seed</dt><dd><code>${run.seed}</code></dd>
        ${res && res.durationMs ? html`<dt>Took</dt><dd>${fmtDuration(res.durationMs)}</dd>` : null}
      </dl>
      ${inputs.length ? html`<h3>Test data</h3>
        <dl class="facts inputs">${inputs.map(([k, v]) => html`<dt key=${'k' + k}>${k}</dt><dd key=${'v' + k}><code>${typeof v === 'string' ? v : JSON.stringify(v)}</code></dd>`)}</dl>`
        : res ? html`<p class="muted small">This journey drew no test data.</p>` : null}
      ${has('failed-step.png') ? html`<h3>Where it stopped</h3>
        <a href=${artifactURL(run.id, 'failed-step.png')} target="_blank" rel="noopener"><img class="failedstep" src=${artifactURL(run.id, 'failed-step.png')} alt="The screen at the step that failed" /></a>` : null}
    </div>`;
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
    api('api/project').then((p) => {
      if (live) setAllowed(new Set([...(p.extraHosts || []), ...(p.sites || []).map(hostOf)].map((h) => h.toLowerCase())));
    }).catch(() => {});
    return () => { live = false; };
  }, []);

  const allow = async (host) => {
    setBusy(host); setErr('');
    try {
      const p = await api('api/project');
      const extra = p.extraHosts || [];
      if (!extra.map((h) => h.toLowerCase()).includes(host)) {
        await api('api/project', { method: 'PUT', headers: { 'Content-Type': 'application/json' },
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
    <div class="card" id="blocked">
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
function Bug({ n, bug, runId, artifacts, duration, onSeek }) {
  const found = stampOf(bug);
  const secs = found !== null && duration > 0 && found <= Math.ceil(duration) ? found : null;
  const tok = SHOT_TOKEN.exec(bug.evidence || '');
  const shot = tok && artifacts.includes(tok[0]) ? tok[0] : '';
  return html`<div class=${'finding' + (shot ? ' with-shot' : '')}>
    <div class="finding-text">
      <div class="finding-head">
        <span class="finding-n">${n}</span>
        <span class=${'tag ' + (SEVERITY_TAG.get((bug.severity || '').toLowerCase()) || 'idle')}>${bug.severity || 'unrated'}</span>
        ${secs !== null ? html`<button type="button" class="stamp" title=${'Play from ' + fmtStamp(secs)} onClick=${() => onSeek(secs)}>▶ ${fmtStamp(secs)}</button>` : null}
        ${bug.issue_id ? html`<a class="small finding-issue" href=${issueHref(bug.issue_id)}>Open in Bugs</a>` : null}
      </div>
      <p class="finding-sum">${bug.summary}</p>
      <dl class="finding-meta">
        ${bug.step ? html`<dt>Step</dt><dd>${bug.step}</dd>` : null}
        ${bug.evidence ? html`<dt>Evidence</dt><dd>${bug.evidence}</dd>` : null}
        ${bug.note ? html`<dt>Note</dt><dd>${bug.note}</dd>` : null}
      </dl>
    </div>
    ${shot ? html`<${Shot} src=${artifactURL(runId, shot)} alt=${'Screenshot for bug ' + n + ': ' + bug.summary} height=${320} />` : null}
  </div>`;
}


// Clear the static "Loading…" placeholder: render() appends, it doesn't replace.
const root = document.getElementById('root');
root.replaceChildren();
render(html`<${App} />`, root);
