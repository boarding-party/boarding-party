// Bugs page: one list of issues across all runs, and an issue's own page
// with its reports and the triage buttons.
//
// Issue and report text is agent-written and untrusted: render it only as
// text through htm/Preact.
import { useState } from './vendor/hooks.module.js';
import { html, api, fmtWhen, parseTime, artifactURL, runHref, issueHref, usePoll, useParams, setParams, SEVERITY_TAG, SHOT_TOKEN } from './lib.js';
import { Shot } from './shot.js';
import { journeyHref } from './journeys.js';

const POLL_MS = 15000;
const CHECK_POLL_MS = 5000; // while a fix check is under way

const STATUS = new Map([
  ['open', { label: 'Open', tag: 'bad' }],
  ['confirmed', { label: 'Confirmed', tag: 'wait' }],
  ['fixed', { label: 'Fixed', tag: 'sup' }],
  ['verifying', { label: 'Checking the fix', tag: 'run' }],
  ['closed', { label: 'Closed', tag: 'ok' }],
  ['not_a_bug', { label: 'Not a bug', tag: 'idle' }],
]);

function StatusTag({ status }) {
  const s = STATUS.get(status) || { label: status, tag: 'idle' };
  return html`<span class=${'tag ' + s.tag}>${s.label}</span>`;
}

// Groups in the order the list shows them; closed and not a bug sit behind a toggle.
const GROUPS = [
  { title: 'Open', has: (i) => i.status === 'open' },
  { title: 'Confirmed', has: (i) => i.status === 'confirmed' },
  { title: 'Fixed', has: (i) => i.status === 'fixed' || i.status === 'verifying' },
];
const DONE = (i) => i.status === 'closed' || i.status === 'not_a_bug';

const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

// openCount is the number for the sidebar: issues that still need a look.
export const openCount = (issues) => (issues || []).filter((i) => i.status === 'open').length;

export function BugsPage() {
  const [issues, setIssues] = useState(null);
  const [err, setErr] = useState('');
  const params = useParams();
  const showDone = params.done === '1'; // in the address, so a link keeps it

  usePoll(async (stale) => {
    try {
      const d = await api('api/issues');
      if (stale()) return null;
      setIssues((d && d.issues) || []); setErr('');
    } catch (e) {
      if (stale()) return null;
      setErr(e.message);
    }
    return POLL_MS;
  }, []);

  const done = (issues || []).filter(DONE);
  return html`
    <section class="page wide">
      <h1>Bugs</h1>
      <p class="lede">Problems your personas found, across all runs. A report of the same problem joins its issue. Mark an issue fixed and a persona checks it again.</p>
      <${Value} days=${params.days === '30' ? 30 : 7} by=${params.by === 'story' ? 'story' : 'persona'} open=${params.spend === '1'} />
      ${err ? html`<div class="note bad" role="alert">Could not load the bugs: ${err}</div>` : null}
      ${issues === null && !err ? html`<p class="muted">Loading…</p>` : null}
      ${issues && issues.length === 0 ? html`<div class="empty"><b>No bugs yet.</b>Bugs your personas report show up here.</div>` : null}
      ${issues && issues.length ? html`
        <div class="stack">
          ${GROUPS.map((g) => {
            let list = issues.filter(g.has);
            if (g.title === 'Open') list = [...list.filter((i) => parseTime(i.reopened)), ...list.filter((i) => !parseTime(i.reopened))];
            return list.length ? html`<${IssueGroup} key=${g.title} title=${g.title} list=${list} />` : null;
          })}
          ${done.length ? html`
            <div>
              <button type="button" class="btn ghost small" aria-expanded=${showDone} onClick=${() => setParams({ done: !showDone })}>
                ${showDone ? 'Hide' : 'Show'} closed and not a bug (${done.length})</button>
            </div>
            ${showDone ? html`<${IssueGroup} title="Closed and not a bug" list=${done} />` : null}` : null}
        </div>` : null}
    </section>`;
}

const VALUE_POLL_MS = 60000;
const money = (n) => '$' + (Number(n) || 0).toFixed(2);

// Value says what the cast spent lately and what it found for it. Closed it
// is one line; open it shows where the money went: the dearest personas or
// stories first, each with its share of the spend, so one that spends and
// finds nothing stands out.
function Value({ days, by, open }) {
  const [v, setV] = useState(null);
  usePoll(async (stale) => {
    try {
      const d = await api(`api/value?days=${days}`);
      if (!stale()) setV(d);
    } catch (_) { /* the bugs list reports a dead board; this panel just stays away */ }
    return VALUE_POLL_MS;
  }, [days]);
  if (!v || !v.runs || v.spent < 0.01) return null; // nothing spent, nothing to weigh
  const rows = (by === 'story' ? v.stories : v.personas) || [];
  const top = rows.reduce((m, r) => Math.max(m, r.spent), 0);
  const toggle = () => setParams({ spend: !open });
  if (!open) return html`
    <button type="button" class="card valuebar" aria-expanded="false" onClick=${toggle}>
      <ul class="readout">
        <li>Last ${days} days</li>
        <li><b>${money(v.spent)}</b> on ${plural(v.runs, 'run', 'runs')}</li>
        <li><b>${plural(v.bugs, 'new bug', 'new bugs')}</b></li>
        ${v.bugs ? html`<li><b class="perbug">${money(v.perBug)}</b> a bug</li>` : null}
      </ul>
      <span class="valuemore">Where it went</span>
    </button>`;
  return html`
    <div class="card value">
      <div class="valuehead">
        <p class="valuesum">
          In the last ${days} days your personas spent <b>${money(v.spent)}</b> on ${plural(v.runs, 'run', 'runs')} and found <b>${plural(v.bugs, 'new bug', 'new bugs')}</b>${v.bugs ? html`: <b class="perbug">${money(v.perBug)}</b> a bug` : ''}.
        </p>
        <div class="row">
          <div class="viewtoggle" role="group" aria-label="Period">
            ${[7, 30].map((n) => html`<button type="button" key=${n} class=${days === n ? 'on' : ''} aria-pressed=${days === n}
              onClick=${() => setParams({ days: n === 7 ? null : n })}>${n} days</button>`)}
          </div>
          <button type="button" class="btn ghost small" aria-expanded="true" onClick=${toggle}>Hide</button>
        </div>
      </div>
      <div class="viewtoggle" role="group" aria-label="Split by">
        ${['persona', 'story'].map((k) => html`<button type="button" key=${k} class=${by === k ? 'on' : ''} aria-pressed=${by === k}
          onClick=${() => setParams({ by: k === 'persona' ? null : k })}>By ${k}</button>`)}
      </div>
      <div class="tablewrap">
        <table class="valuetab">
          <thead><tr><th>${by === 'story' ? 'Story' : 'Persona'}</th><th class="num">Runs</th><th>Spent</th><th class="num">New bugs</th><th class="num">Per bug</th></tr></thead>
          <tbody>
            ${rows.slice(0, 10).map((r) => html`<tr key=${r.name}>
              <td>${r.name || html`<span class="muted">${by === 'story' ? 'No story' : 'No persona'}</span>`}</td>
              <td class="num">${r.runs}</td>
              <td><div class="spent"><span class="share" aria-hidden="true"><i style=${{ width: (top ? (r.spent / top) * 100 : 0) + '%' }}></i></span>${money(r.spent)}</div></td>
              <td class="num">${r.bugs || html`<span class="muted">0</span>`}</td>
              <td class="num">${r.bugs ? money(r.spent / r.bugs) : r.spent >= 0.01 ? html`<span class="nofind">None found</span>` : html`<span class="muted">—</span>`}</td>
            </tr>`)}
          </tbody>
        </table>
      </div>
      ${rows.length > 10 ? html`<p class="hint">The 10 dearest of ${rows.length}.</p>` : null}
    </div>`;
}

function IssueGroup({ title, list }) {
  return html`
    <div class="card flush">
      <h2 class="issuegroup">${title} <span class="count">${list.length}</span></h2>
      <ul class="issues">
        ${list.map((i) => html`<li key=${i.id}>
          <a class="issue" href=${issueHref(i.id)}>
            <${StatusTag} status=${i.status} />
            <span class="sum">${i.summary || 'No summary'}</span>
            <span class="when small muted">${i.lastSeen ? `Seen ${fmtWhen(i.lastSeen)}` : ''}</span>
            <span class="meta small muted">
              ${parseTime(i.reopened) ? html`<span class="reopened">Reopened</span>` : null}
              ${plural(i.reports, 'report', 'reports')}${i.personas && i.personas.length ? `, by ${i.personas.join(', ')}` : ''}
            </span>
          </a></li>`)}
      </ul>
    </div>`;
}

// checkLook says how the last fix check ended, from its note.
function checkLook(note) {
  if (note.startsWith('Gone:')) return 'ok';
  if (note.startsWith('Still there:')) return 'bad';
  return '';
}

export function IssuePage({ id }) {
  const [data, setData] = useState(null);
  const [err, setErr] = useState('');
  const [missing, setMissing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [asking, setAsking] = useState(false); // the "not a bug" reason box is open
  const [reason, setReason] = useState('');
  const [actErr, setActErr] = useState('');
  const [tick, setTick] = useState(0);

  usePoll(async (stale) => {
    try {
      const d = await api(`api/issues/${encodeURIComponent(id)}`);
      if (stale()) return null;
      setData(d); setErr('');
      return d.issue.status === 'verifying' ? CHECK_POLL_MS : POLL_MS;
    } catch (e) {
      if (stale()) return null;
      if (e.status === 404) { setMissing(true); return null; }
      setErr(e.message);
      return POLL_MS;
    }
  }, [id, tick]);

  const set = async (status, why = '') => {
    setBusy(true); setActErr('');
    try {
      await api(`api/issues/${encodeURIComponent(id)}/status`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ status, reason: why }) });
      setAsking(false); setReason('');
      setTick((t) => t + 1);
    } catch (e) {
      setActErr(e.message);
    } finally {
      setBusy(false);
    }
  };

  if (missing) {
    return html`<section class="page"><p class="crumb"><a href="#/bugs">Bugs</a></p>
      <div class="empty"><b>Bug not found</b><a href="#/bugs">Back to bugs</a>.</div></section>`;
  }
  if (!data) {
    return html`<section class="page"><p class="crumb"><a href="#/bugs">Bugs</a></p>
      ${err ? html`<div class="note bad">Could not load the bug: ${err}</div>` : html`<p class="muted">Loading…</p>`}</section>`;
  }

  const { issue, reports = [], checkBy } = data;
  const st = issue.status;
  const couldNot = (issue.checkNote || '').startsWith('Could not check');
  const verifying = st === 'verifying';

  return html`
    <section class="page wide">
      <p class="crumb"><a href="#/bugs">Bugs</a> / ${issue.id}</p>
      <h1>${issue.summary || 'No summary'}</h1>
      <p class="lede"><${StatusTag} status=${st} />
        ${parseTime(issue.reopened) ? html` <span class="reopened">Reopened ${fmtWhen(issue.reopened)}</span>` : null}
        ${' '}${plural(issue.reports, 'report', 'reports')}${issue.lastSeen ? `, last seen ${fmtWhen(issue.lastSeen)}` : ''}.</p>
      ${err ? html`<div class="note bad banner">Lost contact with the director: ${err}. Retrying.</div>` : null}
      <div class="issuelayout">
      <div class="stack issueside">
        ${verifying ? html`<div class="verdict run">A persona is checking the fix. This page updates when the check ends.</div>`
          : issue.checkNote ? html`<div class=${'verdict ' + checkLook(issue.checkNote)}><b>Last fix check</b>${issue.checkNote}</div>` : null}
        ${st === 'not_a_bug' && issue.reason ? html`<div class="verdict"><b>Not a bug</b>${issue.reason}</div>` : null}
        <div class="card">
          <div class="row">
            ${st !== 'confirmed' ? html`<button type="button" class="btn" disabled=${busy} onClick=${() => set('confirmed')}>Confirm</button>` : null}
            ${st !== 'not_a_bug' ? html`<button type="button" class="btn" disabled=${busy} aria-expanded=${asking} onClick=${() => setAsking(!asking)}>Not a bug</button>` : null}
            ${!verifying && st !== 'closed' ? html`<button type="button" class="btn primary" disabled=${busy} onClick=${() => set('fixed')}>
              ${st === 'fixed' && couldNot ? 'Check again' : 'Mark fixed'}</button>` : null}
            ${st === 'closed' || st === 'not_a_bug' ? html`<button type="button" class="btn" disabled=${busy} onClick=${() => set('open')}>Reopen</button>` : null}
          </div>
          ${asking ? html`
            <form class="reasonbox" onSubmit=${(e) => { e.preventDefault(); set('not_a_bug', reason.trim()); }}>
              <div class="field">
                <label for="nab-reason">Why is it not a bug?</label>
                <input id="nab-reason" maxlength="500" value=${reason} placeholder="For example: the invoice number is the site's default"
                  onInput=${(e) => setReason(e.currentTarget.value)} />
                <small class="muted">Personas see this and stop reporting it.</small>
              </div>
              <div class="row"><button type="submit" class="btn primary" disabled=${busy}>Mark not a bug</button>
                <button type="button" class="btn" onClick=${() => setAsking(false)}>Cancel</button></div>
            </form>` : null}
          ${actErr ? html`<div class="note bad" role="alert">${actErr}</div>` : null}
          <p class="small muted" style="margin:10px 0 0">${verifying ? 'Changing the status now ignores the check under way.'
            : checkBy === 'replay' ? 'Mark fixed replays the journey step that shows it. No model runs unless the replay cannot tell.'
            : checkBy === 'none' ? 'Nobody can check this fix: the persona or story that found it is gone. Mark fixed marks it fixed without a check.'
            : 'Mark fixed queues the story that found it, so a persona can look for it again.'}</p>
        </div>
      </div>
        <div class="card">
          <h2>Reports</h2>
          <ul class="bugs">${reports.map((r) => html`<li key=${r.id}><${Report} r=${r} /></li>`)}</ul>
        </div>
      </div>
    </section>`;
}

function Report({ r }) {
  const tok = SHOT_TOKEN.exec(r.evidence || '');
  return html`<div class=${'report' + (tok ? ' with-shot' : '')}>
    <div class="bug">
      <span class=${'tag ' + (SEVERITY_TAG.get((r.severity || '').toLowerCase()) || 'idle')}>${r.severity || 'unrated'}</span>
      <span class="sum">${r.summary}</span>
      <span class="small muted">${fmtWhen(r.created)}</span>
      <span class="meta"><a href=${runHref(r.run_id)}>${r.persona || 'Unknown persona'}, ${r.story || 'unknown story'}</a></span>
      ${r.step ? html`<span class="meta"><b>Step:</b> ${r.step}</span>` : null}
      ${r.journeyId ? html`<span class="meta"><a href=${journeyHref(r.journeyId)}>Journey revision ${r.journeyRev}, step ${r.journeyStep}</a></span>` : null}
      ${r.evidence ? html`<span class="meta"><b>Evidence:</b> ${r.evidence}</span>` : null}
      ${r.note ? html`<span class="meta"><b>Note:</b> ${r.note}</span>` : null}
    </div>
    ${tok ? html`<div class="report-shot"><${Shot} src=${artifactURL(r.run_id, tok[0])} alt=${'Screenshot for: ' + r.summary} height=${520} /></div>` : null}
  </div>`;
}
