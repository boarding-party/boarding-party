// Lead page: a lead's spend, the personas it made, and its decisions.
//
// Children, reasons and decisions are agent-written and untrusted: render
// them only as text through htm/Preact.
import { useState } from './vendor/hooks.module.js';
import { html, api, confirmDialog, fmtWhen, runHref, usePoll } from './lib.js';

const POLL_MS = 15000;

export const leadHref = (name) => `#/leads/${encodeURIComponent(name)}`;

const money = (n) => '$' + (Number(n) || 0).toFixed(2);
const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

export function LeadPage({ name }) {
  const [data, setData] = useState(null);
  const [err, setErr] = useState('');
  const [missing, setMissing] = useState(false);
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const [tick, setTick] = useState(0);

  usePoll(async (stale) => {
    try {
      const d = await api(`/api/leads/${encodeURIComponent(name)}`);
      if (stale()) return null;
      setData(d); setErr('');
    } catch (e) {
      if (stale()) return null;
      if (e.status === 404) { setMissing(true); return null; }
      setErr(e.message);
    }
    return POLL_MS;
  }, [name, tick]);

  const runNow = async () => {
    setBusy(true); setNote('');
    try {
      await api(`/api/leads/${encodeURIComponent(name)}/wake`, { method: 'POST' });
      setNote('Queued. The lead runs when nothing else is running.');
    } catch (e) {
      setNote(e.status === 409 ? 'The lead is already queued or running.' : e.message);
    } finally {
      setBusy(false);
    }
  };

  if (missing) {
    return html`<section class="page"><div class="empty"><b>${name} is not a lead</b>Tick a lead power on the persona in <a href="#/project/roles">Project</a>, then save.</div></section>`;
  }
  if (!data) {
    return html`<section class="page">${err ? html`<div class="note bad">Could not load the lead: ${err}</div>` : html`<p class="muted">Loading…</p>`}</section>`;
  }
  const { lead, usage, children = [], log = [] } = data;
  const l = lead.lead || {};
  const budget = l.dailyBudget != null ? l.dailyBudget : 5;
  const runs = l.dailyRuns || 10;
  return html`
    <section class="page">
      <div class="row between">
        <h1>${lead.name}</h1>
        <button type="button" class="btn primary" disabled=${busy} onClick=${runNow}>Run now</button>
      </div>
      <p class="lede">Lead. Spent ${money(usage.spent)} of ${money(budget)} today, ${usage.runs} of ${runs} child runs.</p>
      ${note ? html`<div class="note" role="status">${note}</div>` : null}
      ${err ? html`<div class="note bad banner">Lost contact with the director: ${err}. Retrying.</div>` : null}
      ${l.brief ? html`<p class="small">${l.brief}</p>` : null}
      <div class="stack">
        <div class="card">
          <h2>Personas it made</h2>
          ${children.length ? html`<ul class="children">${children.map((c) => html`<li key=${c.name}>
              <${Child} c=${c} lead=${lead.name} onChange=${() => setTick((t) => t + 1)} /></li>`)}</ul>`
            : html`<p class="muted small">None yet. The lead makes personas when it runs.</p>`}
        </div>
        <div class="card">
          <h2>Decisions</h2>
          ${log.length ? html`<ul class="decisions">${log.map((a) => html`<li key=${a.id}>
              <span class="muted">${a.runId ? html`<a href=${runHref(a.runId)}>${fmtWhen(a.created)}</a>` : fmtWhen(a.created)}</span>
              <span>${a.action}</span>
              <span>${a.subject}</span>
              <span class="why">${a.reason}</span></li>`)}</ul>`
            : html`<p class="muted small">No decisions yet.</p>`}
        </div>
      </div>
    </section>`;
}

function Child({ c, lead, onChange }) {
  const [editing, setEditing] = useState(false);
  const [traits, setTraits] = useState('');
  const [goals, setGoals] = useState([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const open = () => {
    setTraits((c.persona.traits || []).join(', '));
    setGoals((c.stories || []).map((s) => s.goal));
    setErr(''); setEditing(true);
  };
  const adopt = async (e) => {
    e.preventDefault();
    setBusy(true); setErr('');
    const persona = { ...c.persona, traits: traits.split(',').map((t) => t.trim()).filter(Boolean) };
    const stories = (c.stories || []).map((s, i) => ({ ...s, goal: (goals[i] || '').trim() }));
    try {
      await api(`/api/children/${encodeURIComponent(c.name)}`, {
        method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ persona, stories }) });
      onChange();
    } catch (e2) {
      setErr(e2.message);
    } finally {
      setBusy(false);
    }
  };
  const remove = async () => {
    if (!(await confirmDialog(`Delete ${c.name}? Its runs stay on the Runs page.`, { confirm: 'Delete persona', danger: true }))) return;
    setBusy(true); setErr('');
    try {
      await api(`/api/children/${encodeURIComponent(c.name)}`, { method: 'DELETE' });
      onChange();
    } catch (e) {
      setErr(e.message);
      setBusy(false);
    }
  };

  return html`
    <div class="head">
      <b>${c.name}</b>
      ${c.stopped ? html`<span class="tag idle">Stopped</span>` : null}
      <span class="small muted">${plural(c.runs, 'run', 'runs')}, ${plural(c.bugs, 'bug', 'bugs')}</span>
    </div>
    <span class="small muted">Made by ${lead}: ${c.reason}</span>
    <span class="small">${(c.stories || []).map((s) => s.name).join(', ')}</span>
    ${editing ? html`
      <form class="adopt" onSubmit=${adopt}>
        <label class="field"><span class="sublabel">Traits (comma separated)</span>
          <input value=${traits} onInput=${(e) => setTraits(e.currentTarget.value)} /></label>
        ${(c.stories || []).map((s, i) => html`<label key=${s.name} class="field"><span class="sublabel">${s.name}</span>
          <textarea rows="2" value=${goals[i] || ''} onInput=${(e) => { const v = e.currentTarget.value; setGoals((g) => g.map((x, j) => (j === i ? v : x))); }}></textarea></label>`)}
        <span class="hint">Adopting makes this persona yours. The lead stops changing it.</span>
        <div class="row"><button type="submit" class="btn primary" disabled=${busy}>Adopt</button>
          <button type="button" class="btn" onClick=${() => setEditing(false)}>Cancel</button></div>
      </form>`
      : html`<div class="row">
          <button type="button" class="btn small" disabled=${busy} onClick=${open}>Edit and adopt</button>
          <button type="button" class="btn small ghost" disabled=${busy} onClick=${remove}>Delete</button></div>`}
    ${err ? html`<div class="note bad" role="alert">${err}</div>` : null}`;
}
