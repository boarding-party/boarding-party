// Project editor: one draft shown as sections (general, sites and hosts,
// inbox, test files, roles and a page per role). Saves the whole project
// with PUT /api/project (JSON); "Import YAML" posts a pasted file as-is.
//
// Field names must match internal/project/project.go exactly: the director
// rejects unknown fields.
import { useState, useEffect, useRef } from './vendor/hooks.module.js';
import { html, api, confirmDialog, dialogOpen } from './lib.js';

// ---- model --------------------------------------------------------------
// The draft carries a stable id on every repeatable row so inputs keep focus
// while rows are added and removed. toJSON strips them.

let seq = 0;
const nid = () => `r${++seq}`;

const row = (v = '') => ({ id: nid(), v });
const newPersona = () => ({ id: nid(), name: '', profile: {}, traits: [], identity: [] });
const newStory = () => ({ id: nid(), name: '', goal: '', model: '', handoffs: [] });
const newRole = () => ({ id: nid(), name: '', description: '', canWatchRecordings: false, activity: '', personas: [newPersona()], stories: [newStory()] });

// A story's handoffs are kept as the draft ids of the stories they point
// at, so renaming a role or story carries through and removing one drops
// the reference; toJSON turns them back into names.
function fromProject(p) {
  p = p || {};
  const sites = (p.sites || []).map(row);
  const d = {
    name: p.name || '',
    sites: sites.length ? sites : [row()],
    extraHosts: (p.extraHosts || []).map(row),
    model: p.model || '',
    nodePool: p.nodePool || '',
    timezone: p.timezone || '',
    dailyBudget: p.dailyBudget != null ? String(p.dailyBudget) : '',
    // Not edited here, but carried so a save never drops it.
    resources: p.resources || null,
    allowFrom: [...((p.inbox && p.inbox.allowFrom) || [])],
    authServId: (p.inbox && p.inbox.authServId) || '',
    roles: (p.roles || []).map((r) => ({
      id: nid(),
      name: r.name || '',
      description: r.description || '',
      canWatchRecordings: !!r.canWatchRecordings,
      activity: r.activity || '',
      personas: (r.personas || []).map((pe) => ({
        id: nid(),
        name: pe.name || '',
        profile: { ...(pe.profile || {}) },
        traits: [...(pe.traits || [])],
        identity: Object.entries(pe.identity || {}).map(([k, v]) => ({ id: nid(), k, v: String(v) })),
      })),
      stories: (r.stories || []).map((s) => ({ id: nid(), name: s.name || '', goal: s.goal || '', model: s.model || '', handoffs: s.handoffs || [] })),
    })),
  };
  const ids = new Map();
  for (const r of d.roles) for (const s of r.stories) ids.set(JSON.stringify([r.name, s.name]), s.id);
  for (const r of d.roles) {
    for (const s of r.stories) s.handoffs = s.handoffs.map((h) => ids.get(JSON.stringify([h.role, h.story]))).filter(Boolean);
  }
  return d;
}

// storyTargets maps each story's draft id to its role and story names.
function storyTargets(roles) {
  const m = new Map();
  for (const r of roles) for (const s of r.stories) m.set(s.id, { role: r.name.trim(), story: s.name.trim() });
  return m;
}

const personaHasContent = (p) => !!(p.name.trim() || p.traits.length || Object.values(p.profile).some(Boolean) || p.identity.some((x) => x.k.trim() || x.v.trim()));
const storyHasContent = (s) => !!(s.name.trim() || s.goal.trim());

// identityWarnings returns a message per identity row that won't save as
// typed: a value without a name is dropped, and a repeated name keeps only
// the last value.
function identityWarnings(rows) {
  const last = new Map();
  rows.forEach((x, i) => { const k = x.k.trim(); if (k) last.set(k, i); });
  return rows.map((x, i) => {
    const k = x.k.trim();
    if (!k) return x.v.trim() ? 'This field needs a name, or it won\'t be saved.' : '';
    if (last.get(k) !== i) return `"${k}" appears again below; only the last one is saved.`;
    return '';
  });
}

const vals = (rows) => rows.map((x) => x.v.trim()).filter(Boolean);

function toJSON(d) {
  const targets = storyTargets(d.roles);
  const handoffs = (s) => s.handoffs.map((id) => targets.get(id)).filter((t) => t && t.role && t.story);
  return {
    name: d.name.trim(),
    sites: vals(d.sites),
    extraHosts: vals(d.extraHosts),
    model: d.model.trim(),
    nodePool: d.nodePool.trim(),
    ...(d.timezone.trim() ? { timezone: d.timezone.trim() } : {}),
    ...(d.dailyBudget.trim() !== '' && isFinite(Number(d.dailyBudget)) ? { dailyBudget: Number(d.dailyBudget) } : {}),
    ...(d.resources ? { resources: d.resources } : {}),
    ...(d.allowFrom.length || d.authServId.trim() ? { inbox: { allowFrom: d.allowFrom, ...(d.authServId.trim() ? { authServId: d.authServId.trim() } : {}) } } : {}),
    roles: d.roles.map((r) => ({
      name: r.name.trim(),
      description: r.description.trim(),
      canWatchRecordings: r.canWatchRecordings,
      ...(r.activity.trim() ? { activity: r.activity.trim() } : {}),
      personas: r.personas.map((pe) => ({
        name: pe.name.trim(),
        ...(Object.values(pe.profile).some(Boolean) ? { profile: Object.fromEntries(Object.entries(pe.profile).filter(([, v]) => v)) } : {}),
        traits: pe.traits,
        identity: Object.fromEntries(pe.identity.filter((x) => x.k.trim()).map((x) => [x.k.trim(), x.v])),
      })),
      stories: r.stories.map((s) => {
        const h = handoffs(s);
        return { name: s.name.trim(), goal: s.goal.trim(), ...(s.model ? { model: s.model } : {}), ...(h.length ? { handoffs: h } : {}) };
      }),
    })),
  };
}

// A staging-ish word as a whole label or dash-separated part of one:
// staging.shop.com, shop-dev.example.co, qa.app.io. Not "devices.com".
const STAGING_LABEL = /(^|[.-])(staging|stage|test|dev|uat|qa|preview)([.-]|$)/;

// siteWarning flags sites that may not be staging. It never blocks a save;
// the director still enforces https://.
function siteWarning(s) {
  s = s.trim();
  if (!s) return '';
  let u;
  try { u = new URL(s); } catch (_) { return 'This is not a full URL. Make sure this is a staging site.'; }
  if (u.protocol !== 'https:') return 'This is not https://. Make sure this is a staging site.';
  const host = u.hostname.toLowerCase();
  const labels = host.split('.');
  const noTLD = labels.length > 1 ? labels.slice(0, -1).join('.') : host;
  if (host.startsWith('www.') || !STAGING_LABEL.test(noTLD)) {
    return 'This looks like a production address. Make sure this is a staging site.';
  }
  return '';
}

// ---- page ---------------------------------------------------------------

export function ProjectPage({ section = 'general', roleN = '', onSaved, onDirty }) {
  const [state, setState] = useState('loading'); // loading | ready | error
  const [loadErr, setLoadErr] = useState('');
  const [isNew, setIsNew] = useState(false);
  const [draft, setDraft] = useState(null);
  const [baseline, setBaseline] = useState('');
  const [saving, setSaving] = useState(false);
  const [saveErr, setSaveErr] = useState('');
  const [toast, setToast] = useState('');
  const [importOpen, setImportOpen] = useState(false);
  const profileFields = useProfileFields();
  const top = useRef(null);

  const adopt = (p) => {
    const d = fromProject(p);
    setDraft(d);
    setBaseline(JSON.stringify(toJSON(d)));
    setIsNew(!p);
  };

  const load = async () => {
    try {
      adopt(await api('/api/project'));
      setState('ready');
    } catch (e) {
      if (e.status === 404) { adopt(null); setState('ready'); return; }
      setLoadErr(e.message); setState('error');
    }
  };

  useEffect(() => { load(); }, []);

  const dirty = !!draft && JSON.stringify(toJSON(draft)) !== baseline;

  useEffect(() => { onDirty(dirty); }, [dirty]);
  useEffect(() => () => onDirty(false), []);

  useEffect(() => {
    if (!dirty) return undefined;
    const warn = (e) => { e.preventDefault(); e.returnValue = ''; };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [dirty]);

  useEffect(() => {
    if (!toast) return undefined;
    const t = setTimeout(() => setToast(''), 3000);
    return () => clearTimeout(t);
  }, [toast]);

  // edit applies fn to a copy of the draft.
  const edit = (fn) => setDraft((d) => { const c = structuredClone(d); fn(c); return c; });

  const save = async (e) => {
    if (e) e.preventDefault();
    if (saving) return;
    setSaving(true); setSaveErr('');
    try {
      await api('/api/project', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(toJSON(draft)),
      });
      // The server canonicalises each role's activity; show what it kept.
      let next = draft;
      try {
        const saved = await api('/api/project');
        const roles = (saved && saved.roles) || [];
        if (roles.length === draft.roles.length) {
          next = structuredClone(draft);
          next.roles.forEach((r, i) => { r.activity = roles[i].activity || ''; });
          setDraft(next);
        }
      } catch (_) { /* the save worked; keep what was typed */ }
      setBaseline(JSON.stringify(toJSON(next)));
      setIsNew(false);
      setToast('Project saved');
      onSaved();
    } catch (e2) {
      setSaveErr(e2.message);
      if (top.current) top.current.scrollIntoView({ behavior: 'smooth', block: 'start' });
    } finally {
      setSaving(false);
    }
  };

  // Cmd-S / Ctrl-S saves. Enter in a single-line field never does: it would
  // PUT the whole project on a stray keypress.
  const saveRef = useRef(save);
  saveRef.current = save;
  const readyRef = useRef(false);
  readyRef.current = state === 'ready' && !!draft;
  useEffect(() => {
    const key = (e) => {
      if ((e.metaKey || e.ctrlKey) && !e.altKey && e.key.toLowerCase() === 's') {
        e.preventDefault(); // never the browser's "save page", even when we skip
        if (dialogOpen() || !readyRef.current) return;
        saveRef.current();
      }
    };
    window.addEventListener('keydown', key);
    return () => window.removeEventListener('keydown', key);
  }, []);
  const noEnterSubmit = (e) => {
    if (e.key === 'Enter' && e.target instanceof HTMLInputElement) e.preventDefault();
  };

  const imported = async () => {
    setImportOpen(false);
    setSaveErr('');
    await load();
    setToast('Project imported');
    onSaved();
  };

  if (state === 'loading') return html`<section class="page"><h1>Project</h1><p class="muted">Loading…</p></section>`;
  if (state === 'error') {
    return html`<section class="page"><h1>Project</h1>
      <div class="note bad">Could not load the project: ${loadErr}</div>
      <div style="margin-top:12px"><button class="btn" type="button" onClick=${() => { setState('loading'); load(); }}>Try again</button></div></section>`;
  }

  const cur = toJSON(draft);
  const base = baseline ? JSON.parse(baseline) : {};
  const changed = (...keys) => keys.some((k) => JSON.stringify(cur[k]) !== JSON.stringify(base[k]));
  const roleChanged = (i) => JSON.stringify(cur.roles[i]) !== JSON.stringify((base.roles || [])[i]);
  const roleIndex = Number(roleN) - 1;
  const role = section === 'roles' && roleN ? draft.roles[roleIndex] : null;

  const addRole = () => {
    const n = draft.roles.length + 1;
    edit((d) => { d.roles.push(newRole()); });
    location.hash = `#/project/roles/${n}`;
  };
  const removeRole = async (r) => {
    const busy = r.name.trim() || r.description.trim() || r.personas.some(personaHasContent) || r.stories.some(storyHasContent);
    if (busy && !(await confirmDialog(`Remove the role ${r.name.trim() ? `"${r.name.trim()}"` : 'without a name'} with its personas and stories?`, { confirm: 'Remove role', danger: true }))) return;
    edit((d) => { d.roles.splice(d.roles.findIndex((x) => x.id === r.id), 1); });
    location.hash = '#/project/roles';
  };

  let body;
  if (section === 'sites') {
    body = html`<div class="card">
        <h2>Sites and hosts</h2>
        <${ListField} label="Sites" hint="Staging URLs the cast may open, starting with https://. Never point it at production."
          rows=${draft.sites} placeholder="https://staging.example.com" addLabel="Add site" warn=${siteWarning}
          onChange=${(fn) => edit((d) => fn(d.sites))} />
        <${ListField} label="Extra hosts" hint="Other hosts the site loads from or links to, such as fonts, a CDN, a sign-in or payment provider. Runs can reach only these and the sites. Hosts you allow from a run's blocked list land here; remove one to block it again."
          rows=${draft.extraHosts} placeholder="cdn.example.com" addLabel="Add host"
          onChange=${(fn) => edit((d) => fn(d.extraHosts))} />
      </div>`;
  } else if (section === 'inbox') {
    body = html`<${InboxCard} allowFrom=${draft.allowFrom} authServId=${draft.authServId} onChange=${(fn) => edit((d) => fn(d))} />`;
  } else if (section === 'files') {
    body = html`<${FilesCard} personas=${savedPersonas(baseline)} />`;
  } else if (section === 'roles' && roleN) {
    body = role
      ? html`<p class="crumb"><a href="#/project/roles">Roles</a></p>
        <${RoleCard} key=${role.id} role=${role} n=${roleIndex + 1} profileFields=${profileFields} roles=${draft.roles}
          onChange=${(fn) => edit((d) => fn(d.roles[roleIndex]))} onRemove=${() => removeRole(role)} />`
      : html`<div class="empty"><b>No such role</b>It may have been removed. <a href="#/project/roles">See all roles</a>.</div>`;
  } else if (section === 'roles') {
    body = html`<div class="card">
        <div class="row between"><h2>Roles</h2><button class="btn" type="button" onClick=${addRole}>Add role</button></div>
        <p class="hint">A role is a kind of user, such as a customer or an admin. Each has personas who play it and stories they try to finish.</p>
        ${draft.roles.length
          ? html`<table class="roles">
              <thead><tr><th>Role</th><th>Personas</th><th>Stories</th></tr></thead>
              <tbody>${draft.roles.map((r, i) => html`<tr key=${r.id}>
                <td><a href=${`#/project/roles/${i + 1}`}>${r.name.trim() || 'Unnamed role'}</a>${roleChanged(i) ? html` <span class="mark" title="Unsaved changes"></span>` : null}
                  ${r.description.trim() ? html`<div class="small muted">${r.description.trim()}</div>` : null}</td>
                <td>${r.personas.map((p) => p.name.trim()).filter(Boolean).join(', ') || html`<span class="muted">None</span>`}</td>
                <td>${r.stories.map((x) => x.name.trim()).filter(Boolean).join(', ') || html`<span class="muted">None</span>`}</td>
              </tr>`)}</tbody></table>`
          : html`<div class="empty"><b>No roles yet</b>Add one, then give it at least one persona and one story.</div>`}
      </div>`;
  } else {
    body = html`<div class="card">
        <h2>General</h2>
        <div class="fields">
          <div class="field">
            <label for="pj-name">Project name</label>
            <input id="pj-name" value=${draft.name} placeholder="shop-staging"
              onInput=${(e) => { const v = e.currentTarget.value; edit((d) => { d.name = v; }); }} />
          </div>
          <div class="field">
            <label for="pj-model">Model</label>
            <${ModelSelect} id="pj-model" value=${draft.model} empty="Claude Code's default"
              onChange=${(v) => edit((d) => { d.model = v; })} />
            <span class="hint">The model runs use unless a story picks its own.</span>
          </div>
          <div class="field">
            <label for="pj-pool">Node pool</label>
            <input id="pj-pool" value=${draft.nodePool} placeholder="boardingparty.io/run=true"
              onInput=${(e) => { const v = e.currentTarget.value; edit((d) => { d.nodePool = v; }); }} />
            <span class="hint">Node label for the pool runs use, as key=value. The pool needs the same key=value as a NoSchedule taint. Leave empty to run on any node.</span>
          </div>
          <div class="field">
            <label for="pj-budget">Daily budget</label>
            <input id="pj-budget" type="number" min="0" step="0.5" value=${draft.dailyBudget} placeholder="5"
              onInput=${(e) => { const v = e.currentTarget.value; edit((d) => { d.dailyBudget = v; }); }} />
            <span class="hint">Spend cap in US dollars per day. Running runs finish; new ones wait until midnight.</span>
          </div>
          <div class="field">
            <label for="pj-tz">Time zone</label>
            <input id="pj-tz" value=${draft.timezone} placeholder="Europe/London"
              onInput=${(e) => { const v = e.currentTarget.value; edit((d) => { d.timezone = v; }); }} />
          </div>
        </div>
      </div>`;
  }

  const link = (href, label, on, edited, cls = '') => html`<a href=${href} class=${[cls, on ? 'on' : ''].join(' ').trim()} aria-current=${on ? 'page' : undefined}>
      <span class="label">${label}</span>${edited ? html`<span class="mark" title="Unsaved changes"></span>` : null}</a>`;

  return html`
    <form class="page editor" onSubmit=${save} onKeyDown=${noEnterSubmit} ref=${top}>
      <div class="row between head">
        <div>
          <h1>${isNew ? 'Set up your project' : 'Project'}</h1>
          <p class="lede">The site the cast may open, and who is in it. Save stores every section at once.</p>
        </div>
        <button class="btn" type="button" aria-expanded=${importOpen} onClick=${() => setImportOpen(!importOpen)}>Import YAML</button>
      </div>

      ${saveErr ? html`<div class="note bad banner" role="alert"><b>Not saved.</b> ${saveErr}</div>` : null}
      ${importOpen ? html`<${ImportYAML} dirty=${dirty} onDone=${imported} onCancel=${() => setImportOpen(false)} />` : null}

      <div class="projgrid">
        <nav class="subnav" aria-label="Project sections">
          ${link('#/project/general', 'General', section === 'general', changed('name', 'model', 'nodePool', 'timezone', 'dailyBudget'))}
          ${link('#/project/sites', 'Sites and hosts', section === 'sites', changed('sites', 'extraHosts'))}
          ${link('#/project/inbox', 'Inbox', section === 'inbox', changed('inbox'))}
          ${link('#/project/files', 'Test files', section === 'files', false)}
          ${link('#/project/roles', html`Roles <span class="count">${draft.roles.length}</span>`, section === 'roles' && !roleN,
            JSON.stringify(cur.roles) !== JSON.stringify(base.roles || []))}
          ${draft.roles.map((r, i) => link(`#/project/roles/${i + 1}`, r.name.trim() || 'Unnamed role',
            section === 'roles' && roleIndex === i, roleChanged(i), 'child'))}
          <div class="savebox">
            <button class="btn primary" type="submit" disabled=${saving}>${saving ? 'Saving…' : 'Save'}</button>
            <span class="small muted">${dirty ? 'Unsaved changes' : 'All saved'}</span>
          </div>
        </nav>
        <div class="stack">${body}</div>
      </div>
      ${toast ? html`<div class="toast" role="status">${toast}</div>` : null}
    </form>`;
}

// ---- pieces -------------------------------------------------------------

function ListField({ label, hint, rows, placeholder, addLabel, warn, onChange }) {
  return html`
    <div class="field">
      <label>${label}</label>
      ${hint ? html`<span class="hint">${hint}</span>` : null}
      <div class="list">
        ${rows.map((x, i) => {
          const w = warn ? warn(x.v) : '';
          return html`<div key=${x.id}>
            <div class="row nowrap">
              <input value=${x.v} placeholder=${placeholder} aria-label=${label + ' ' + (i + 1)}
                onInput=${(e) => { const v = e.currentTarget.value; onChange((l) => { l[i].v = v; }); }}
                onKeyDown=${(e) => {
                  if (e.key !== 'Enter') return;
                  e.preventDefault();
                  // Enter moves to the next row, adding one after a filled last row.
                  const list = e.currentTarget.closest('.list');
                  if (i === rows.length - 1) {
                    if (!e.currentTarget.value.trim()) return;
                    onChange((l) => { l.push(row()); });
                  }
                  setTimeout(() => { const n = list && list.querySelectorAll('input')[i + 1]; if (n) n.focus(); }, 0);
                }} />
              <button class="btn icon" type="button" title="Remove" aria-label=${'Remove ' + (x.v || 'row')}
                onClick=${() => onChange((l) => { l.splice(i, 1); })}>×</button>
            </div>
            ${w ? html`<div class="warn">${w}</div>` : null}
          </div>`;
        })}
      </div>
      <div><button class="btn ghost" type="button" onClick=${() => onChange((l) => { l.push(row()); })}>${addLabel}</button></div>
    </div>`;
}

function RoleCard({ role, n, profileFields, roles, onChange, onRemove }) {
  const id = `role-${role.id}`;
  // Stories of every other role, by name, that this role's stories can hand off to.
  const targets = roles.filter((r) => r.id !== role.id).flatMap((r) => r.stories
    .filter((s) => s.name.trim())
    .map((s) => ({ id: s.id, label: `${r.name.trim() || 'Unnamed role'} · ${s.name.trim()}` })));
  return html`
    <div class="card role">
      <div class="row between nowrap">
        <div class="field grow">
          <label for=${id + '-name'}>Role ${n}</label>
          <input id=${id + '-name'} class="strong" value=${role.name} placeholder="Customer"
            onInput=${(e) => { const v = e.currentTarget.value; onChange((r) => { r.name = v; }); }} />
        </div>
        <button class="btn danger-ghost" type="button" onClick=${onRemove}>Remove role</button>
      </div>
      <div class="field">
        <label for=${id + '-desc'}>Description</label>
        <textarea id=${id + '-desc'} rows="2" value=${role.description} placeholder="Who they are and what they want from the site."
          onInput=${(e) => { const v = e.currentTarget.value; onChange((r) => { r.description = v; }); }}></textarea>
      </div>
      <div class="field">
        <label for=${id + '-activity'}>Activity</label>
        <input id=${id + '-activity'} value=${role.activity} placeholder="every 30 min, 09:00–17:30"
          onInput=${(e) => { const v = e.currentTarget.value; onChange((r) => { r.activity = v; }); }} />
        <span class="hint">How often personas in this role act on their own. Examples: daily at 09:00 · every 2 h, 09:00–17:30 · 2–4 times a day, 07:00–23:00. Leave empty to run them only by hand or hand-off.</span>
      </div>
      <label class="check">
        <input type="checkbox" checked=${role.canWatchRecordings}
          onChange=${(e) => { const v = e.currentTarget.checked; onChange((r) => { r.canWatchRecordings = v; }); }} />
        <span>Can watch other roles' recordings <span class="hint">For a supervisor role that reviews what others did. Not used by runs yet; leave it off.</span></span>
      </label>

      <div class="cols">
        <div>
          <h3>Personas</h3>
          <p class="hint">Made-up people who play this role. A run picks one persona and one story.</p>
          ${role.personas.length ? null : html`<p class="small muted">No personas. Runs need at least one.</p>`}
          ${role.personas.map((p, i) => html`<${PersonaRow} key=${p.id} persona=${p} fields=${profileFields}
              onChange=${(fn) => onChange((r) => fn(r.personas[i]))}
              onRemove=${async () => {
                if (personaHasContent(p) && !(await confirmDialog(`Remove the persona ${p.name.trim() ? `"${p.name.trim()}"` : 'without a name'}?`, { confirm: 'Remove persona', danger: true }))) return;
                onChange((r) => { r.personas.splice(r.personas.findIndex((x) => x.id === p.id), 1); });
              }} />`)}
          <button class="btn ghost" type="button" onClick=${() => onChange((r) => { r.personas.push(newPersona()); })}>Add persona</button>
        </div>
        <div>
          <h3>Stories</h3>
          <p class="hint">Goals, not steps. Any persona in this role can play any story; the agent finds the way.</p>
          ${role.stories.length ? null : html`<p class="small muted">No stories. Runs need at least one.</p>`}
          ${role.stories.map((s, i) => html`<${StoryRow} key=${s.id} story=${s} targets=${targets}
              onChange=${(fn) => onChange((r) => fn(r.stories[i]))}
              onRemove=${async () => {
                if (storyHasContent(s) && !(await confirmDialog(`Remove the story ${s.name.trim() ? `"${s.name.trim()}"` : 'without a name'}?`, { confirm: 'Remove story', danger: true }))) return;
                onChange((r) => { r.stories.splice(r.stories.findIndex((x) => x.id === s.id), 1); });
              }} />`)}
          <button class="btn ghost" type="button" onClick=${() => onChange((r) => { r.stories.push(newStory()); })}>Add story</button>
        </div>
      </div>
    </div>`;
}

// Profile fields come from the director so the lists stay in one place.
function useProfileFields() {
  const [fields, setFields] = useState([]);
  useEffect(() => {
    let live = true;
    api('/api/profile-fields').then((f) => { if (live && Array.isArray(f)) setFields(f); }).catch(() => {});
    return () => { live = false; };
  }, []);
  return fields;
}

function ProfileGrid({ profile, fields, onChange }) {
  if (!fields.length) return null;
  return html`
    <div class="kv">
      <span class="sublabel">Profile</span>
      <div class="fields">
        ${fields.map((f) => {
          const cur = profile[f.key] || '';
          const known = !cur || (f.options || []).some((o) => o.value === cur);
          return html`<label class="field">${f.label}
            <select title=${f.hint || ''} value=${cur}
              onChange=${(e) => { const v = e.currentTarget.value; onChange((p) => { if (v) p.profile[f.key] = v; else delete p.profile[f.key]; }); }}>
              <option value="" selected=${!cur}>Not set</option>
              ${(f.options || []).map((o) => html`<option key=${o.value} value=${o.value} selected=${cur === o.value}>${o.label}</option>`)}
              ${known ? null : html`<option value=${cur} selected>${cur} (unknown)</option>`}
            </select></label>`;
        })}
      </div>
      <span class="hint">The agent behaves like this person. Device sets the browser's screen size when the run starts.</span>
    </div>`;
}

function PersonaRow({ persona, fields, onChange, onRemove }) {
  const id = `p-${persona.id}`;
  const warns = identityWarnings(persona.identity);
  return html`
    <div class="sub">
      <div class="row nowrap">
        <input id=${id} class="strong" value=${persona.name} placeholder="Persona name, such as Priya" aria-label="Persona name"
          onInput=${(e) => { const v = e.currentTarget.value; onChange((p) => { p.name = v; }); }} />
        <button class="btn icon" type="button" title="Remove persona" aria-label=${'Remove persona ' + persona.name}
          onClick=${onRemove}>×</button>
      </div>
      <${ProfileGrid} profile=${persona.profile} fields=${fields} onChange=${onChange} />
      <${Tags} tags=${persona.traits} label="Other traits (optional)" placeholder="Optional. Enter adds"
        onChange=${(fn) => onChange((p) => fn(p.traits))} />
      <span class="hint">Anything the lists don't cover, such as distracted or left-handed.</span>
      <div class="kv">
        <span class="sublabel">Identity</span>
        <span class="hint">Details the persona types into forms. Field on the left, value on the right. Use made-up data.</span>
        ${persona.identity.map((x, i) => html`<div key=${x.id}><div class="row nowrap">
          <input class="k" value=${x.k} placeholder="field, such as email" aria-label="Identity field"
            onInput=${(e) => { const v = e.currentTarget.value; onChange((p) => { p.identity[i].k = v; }); }} />
          <input value=${x.v} placeholder="priya@example.test" aria-label=${'Value for ' + (x.k || 'field')}
            onInput=${(e) => { const v = e.currentTarget.value; onChange((p) => { p.identity[i].v = v; }); }} />
          <button class="btn icon" type="button" title="Remove field" aria-label=${'Remove ' + (x.k || 'field')}
            onClick=${() => onChange((p) => { p.identity.splice(i, 1); })}>×</button>
        </div>${warns[i] ? html`<div class="warn">${warns[i]}</div>` : null}</div>`)}
        <div><button class="btn ghost small" type="button" onClick=${() => onChange((p) => { p.identity.push({ id: nid(), k: '', v: '' }); })}>Add identity field</button></div>
      </div>
    </div>`;
}

function StoryRow({ story, targets, onChange, onRemove }) {
  return html`
    <div class="sub">
      <div class="row nowrap">
        <input class="strong" value=${story.name} placeholder="Story name, such as first-invoice" aria-label="Story name"
          onInput=${(e) => { const v = e.currentTarget.value; onChange((s) => { s.name = v; }); }} />
        <button class="btn icon" type="button" title="Remove story" aria-label=${'Remove story ' + story.name}
          onClick=${onRemove}>×</button>
      </div>
      <textarea rows="3" value=${story.goal} placeholder="In their words: I need to bill a client £450 + VAT and download the invoice as a PDF." aria-label="Goal"
        onInput=${(e) => { const v = e.currentTarget.value; onChange((s) => { s.goal = v; }); }}></textarea>
      <label class="field">Model
        <${ModelSelect} value=${story.model} empty="Project's model" onChange=${(v) => onChange((s) => { s.model = v; })} />
      </label>
      <${HandsOffTo} story=${story} targets=${targets} onChange=${onChange} />
    </div>`;
}

// HandsOffTo picks the other roles' stories this story starts when it ends.
function HandsOffTo({ story, targets, onChange }) {
  const toggle = (tid, on) => onChange((s) => {
    s.handoffs = s.handoffs.filter((x) => x !== tid);
    if (on) s.handoffs.push(tid);
  });
  return html`
    <fieldset class="handoffs">
      <legend>Hands off to</legend>
      ${targets.length
        ? html`<div class="picks">${targets.map((t) => html`
            <label class="check" key=${t.id}>
              <input type="checkbox" checked=${story.handoffs.includes(t.id)}
                onChange=${(e) => toggle(t.id, e.currentTarget.checked)} />
              <span>${t.label}</span>
            </label>`)}</div>`
        : html`<p class="small muted">No other role has a story yet.</p>`}
      <span class="hint">When this story ends, or waits for one of these, the director starts it for the first persona of that role.</span>
    </fieldset>`;
}

// MODELS are the models offered in the editor, cheapest first. A model the
// project already names but this list lacks stays selectable.
const MODELS = [
  { value: 'claude-haiku-5-5', label: 'Haiku 5.5, cheapest' },
  { value: 'claude-sonnet-5-5', label: 'Sonnet 5.5' },
  { value: 'claude-opus-5-5', label: 'Opus 5.5, most thorough' },
];

function ModelSelect({ id, value, empty, onChange }) {
  const known = !value || MODELS.some((m) => m.value === value);
  return html`<select id=${id} value=${value || ''} onChange=${(e) => onChange(e.currentTarget.value)}>
      <option value="" selected=${!value}>${empty}</option>
      ${MODELS.map((m) => html`<option key=${m.value} value=${m.value} selected=${value === m.value}>${m.label}</option>`)}
      ${known ? null : html`<option value=${value} selected>${value}</option>`}
    </select>`;
}

// Tags edits a list of short strings: Enter or comma adds, Backspace on an
// empty input removes the last one.
function Tags({ tags, label, placeholder, more = 'Add trait', onChange }) {
  const [text, setText] = useState('');
  // add reads the input itself: a key can arrive before the render that
  // would have updated text.
  const addAll = (parts) => {
    const fresh = [...new Set(parts.map((t) => t.trim()).filter(Boolean))].filter((t) => !tags.includes(t));
    if (fresh.length) onChange((l) => { for (const t of fresh) if (!l.includes(t)) l.push(t); });
  };
  const add = (e) => {
    addAll(e.currentTarget.value.split(','));
    e.currentTarget.value = '';
    setText('');
  };
  // A pasted or typed "a, b, c" becomes tags; the part after the last comma
  // stays in the input.
  const input = (e) => {
    const v = e.currentTarget.value;
    if (!v.includes(',')) { setText(v); return; }
    const parts = v.split(',');
    const rest = parts.pop();
    addAll(parts);
    e.currentTarget.value = rest;
    setText(rest);
  };
  return html`
    <div class="tags" aria-label=${label}>
      ${tags.map((t, i) => html`<span class="chip" key=${i + ':' + t}>${t}
        <button type="button" aria-label=${'Remove ' + t} onClick=${() => onChange((l) => { l.splice(i, 1); })}>×</button></span>`)}
      <input value=${text} placeholder=${tags.length ? more : placeholder} aria-label=${'Add to ' + label}
        onInput=${input}
        onKeyDown=${(e) => {
          if (e.key === 'Enter') { e.preventDefault(); add(e); }
          else if (e.key === 'Backspace' && !e.currentTarget.value && tags.length) onChange((l) => { l.pop(); });
        }}
        onBlur=${add} />
    </div>`;
}

function ImportYAML({ dirty, onDone, onCancel }) {
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const go = async () => {
    if (!text.trim()) { setErr('Paste a project YAML file first.'); return; }
    if (dirty && !(await confirmDialog('Importing replaces the project and drops your unsaved changes. Continue?', { confirm: 'Import and replace', danger: true }))) return;
    setBusy(true); setErr('');
    try {
      await api('/api/project', { method: 'POST', headers: { 'Content-Type': 'application/yaml' }, body: text });
      await onDone();
    } catch (e) {
      setErr(e.message);
    } finally {
      setBusy(false);
    }
  };
  return html`
    <div class="card import">
      <h2>Import YAML</h2>
      <p class="small muted" style="margin:0 0 8px">Paste a project file, the same format as <code>boardingparty apply -f project.yaml</code>. It replaces the whole project.</p>
      <textarea rows="10" class="mono" value=${text} spellcheck="false" aria-label="Project YAML"
        placeholder=${'name: shop-staging\nsites:\n  - https://staging.example.com\nroles:\n  - name: Customer\n    personas:\n      - name: Sam Jones\n    stories:\n      - name: Buy a gift\n        goal: Buy a gift under £30 and have it sent to a friend.'}
        onInput=${(e) => setText(e.currentTarget.value)}></textarea>
      ${err ? html`<div class="note bad" role="alert"><b>Not imported.</b> ${err}</div>` : null}
      <div class="row" style="margin-top:10px">
        <button class="btn primary" type="button" disabled=${busy} onClick=${go}>${busy ? 'Importing…' : 'Import'}</button>
        <button class="btn" type="button" onClick=${onCancel}>Cancel</button>
      </div>
    </div>`;
}

// ---- files ----------------------------------------------------------------
// Test files personas can attach in the site. Independent of the project
// draft: each upload and delete goes to the director straight away.

// savedPersonas lists the persona names in the saved project (the baseline
// JSON): the director only accepts a file for a persona it already knows.
function savedPersonas(baseline) {
  try {
    return [...new Set(JSON.parse(baseline).roles.flatMap((r) => (r.personas || []).map((p) => p.name)).filter((n) => n && n.trim()))];
  } catch (_) { return []; }
}

const MAX_FILE = 25 << 20, MAX_TOTAL = 200 << 20;

const fmtSize = (n) => (n === 0 ? '0 B' : n >= 1048576 ? `${(n / 1048576).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`);

// fileName turns a picked file's name into one the director accepts.
const fileName = (n) => n.toLowerCase().replace(/[^a-z0-9._-]+/g, '-').replace(/^[^a-z0-9]+/, '').slice(0, 64);

const fmtUploaded = (s) => {
  const d = new Date(s);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString('en-GB', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
};

const who = (scope) => (scope === 'project' ? 'Everyone' : scope.replace(/^persona:/, ''));

function FilesCard({ personas }) {
  const [list, setList] = useState(null);
  const [loadErr, setLoadErr] = useState('');
  const [file, setFile] = useState(null);
  const [name, setName] = useState('');
  const [scope, setScope] = useState('project');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const input = useRef(null);

  const load = async () => {
    try { setList(await api('/api/files')); setLoadErr(''); } catch (e) { setLoadErr(e.message); }
  };
  useEffect(() => { load(); }, []);
  // A persona removed from the project can no longer be chosen.
  useEffect(() => {
    if (scope !== 'project' && !personas.includes(scope.slice('persona:'.length))) setScope('project');
  }, [personas.join('\n')]);

  const pick = (e) => {
    const f = e.currentTarget.files && e.currentTarget.files[0];
    setFile(f || null); setName(f ? fileName(f.name) : ''); setErr('');
  };

  const upload = async () => {
    if (!file || busy) return;
    if (file.size > MAX_FILE) { setErr(`This file is ${fmtSize(file.size)}, over the 25 MiB limit.`); return; }
    const used = (list || []).filter((f) => !(f.name === name && f.scope === scope)).reduce((a, f) => a + f.size, 0);
    if (list && used + file.size > MAX_TOTAL) { setErr('Adding this file would exceed the 200 MiB total; delete some files first.'); return; }
    setBusy(true); setErr('');
    try {
      await api(`/api/files/${encodeURIComponent(name)}?scope=${encodeURIComponent(scope)}`, {
        method: 'PUT', headers: { 'Content-Type': file.type || 'application/octet-stream' }, body: file,
      });
      setFile(null); setName('');
      if (input.current) input.current.value = '';
      await load();
    } catch (e) { setErr(e.message); } finally { setBusy(false); }
  };

  const remove = async (f) => {
    if (!(await confirmDialog(`Delete ${f.name} (${who(f.scope)})? Personas lose access to it straight away.`, { confirm: 'Delete file', danger: true }))) return;
    setErr('');
    try {
      await api(`/api/files/${encodeURIComponent(f.name)}?scope=${encodeURIComponent(f.scope)}`, { method: 'DELETE' });
      await load();
    } catch (e) { setErr(e.message); }
  };

  return html`
    <div class="card" id="files-card">
      <h2>Files</h2>
      <p class="hint">Test files personas can attach in the site, such as a logo or a CSV. Use made-up content.</p>
      ${loadErr ? html`<div class="note bad" role="alert">Could not load the files: ${loadErr}</div>` : null}
      ${list && list.length ? html`<table class="filetab"><thead><tr><th>Name</th><th>Who can use it</th><th>Size</th><th>Uploaded</th><th></th></tr></thead>
        <tbody>${list.map((f) => html`<tr key=${f.scope + '/' + f.name}>
          <td class="nm">${f.name}</td><td>${who(f.scope)}</td><td>${fmtSize(f.size)}</td><td>${fmtUploaded(f.uploaded)}</td>
          <td class="act"><button class="btn danger-ghost" type="button" onClick=${() => remove(f)}>Delete</button></td></tr>`)}</tbody></table>`
        : list ? html`<p class="small muted">No files yet.</p>` : loadErr ? null : html`<p class="small muted">Loading…</p>`}
      <div class="fileup">
        <div class="field">
          <label for="fl-file">File</label>
          <input id="fl-file" type="file" ref=${input} onChange=${pick} />
        </div>
        <div class="field">
          <label for="fl-name">Name in the pod</label>
          <input id="fl-name" value=${name} disabled=${!file} placeholder="logo.png"
            onInput=${(e) => setName(e.currentTarget.value)} />
        </div>
        <div class="field">
          <label for="fl-who">Who can use it</label>
          <select id="fl-who" value=${scope} onChange=${(e) => setScope(e.currentTarget.value)}>
            <option value="project">Everyone</option>
            ${personas.map((p) => html`<option key=${p} value=${'persona:' + p}>${p}</option>`)}
          </select>
        </div>
        <button class="btn primary" type="button" disabled=${!file || !name || busy} onClick=${upload}>${busy ? 'Uploading…' : 'Upload'}</button>
      </div>
      ${personas.length ? null : html`<p class="hint">To limit a file to one persona, add the persona and save the project first.</p>`}
      ${err ? html`<div class="note bad" role="alert">${err}</div>` : null}
    </div>`;
}

// ---- inbox ----------------------------------------------------------------
// One mailbox for all personas. The mailbox itself is saved straight away
// with "Save and test" (never with the project, which must not carry the
// password); "Accept mail from" and "Verifying server" belong to the project
// draft and save with it.

const fmtChecked = (s) => {
  const d = new Date(s);
  if (Number.isNaN(d.getTime()) || d.getUTCFullYear() < 2000) return '';
  return d.toLocaleString('en-GB', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
};

// The app password field is a text input masked with CSS where the browser
// supports it: a type=password field makes browsers offer to generate or save
// a password, which is wrong for a mailbox app password.
const maskByCSS = typeof CSS !== 'undefined' && CSS.supports && CSS.supports('-webkit-text-security', 'disc');
const eye = html`<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/></svg>`;
const eyeOff = html`<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10.6 5.1A10.4 10.4 0 0 1 12 5c6.5 0 10 7 10 7a17.6 17.6 0 0 1-2.6 3.6M6.6 6.6C3.8 8.5 2 12 2 12s3.5 7 10 7a9.7 9.7 0 0 0 5.4-1.6"/><path d="M9.9 9.9a3 3 0 0 0 4.2 4.2"/><path d="M3 3l18 18"/></svg>`;

function InboxCard({ allowFrom, authServId, onChange }) {
  const [mb, setMb] = useState(null); // the saved mailbox, as the director reports it
  const [loadErr, setLoadErr] = useState('');
  const [host, setHost] = useState('');
  const [port, setPort] = useState('993');
  const [user, setUser] = useState('');
  const [pass, setPass] = useState('');
  const [showPass, setShowPass] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  const adopt = (m) => {
    setMb(m);
    setHost(m.host || ''); setPort(String(m.port || 993)); setUser(m.username || ''); setPass('');
  };
  const load = async () => {
    try { adopt(await api('/api/mailbox')); setLoadErr(''); } catch (e) { setLoadErr(e.message); }
  };
  useEffect(() => { load(); }, []);
  // The status line follows the poller, so refresh it while the page is open.
  useEffect(() => {
    const t = setInterval(async () => {
      if (document.hidden || busy) return;
      try { const m = await api('/api/mailbox'); setMb((cur) => (cur ? { ...cur, status: m.status } : cur)); } catch (_) { /* keep the last status */ }
    }, 10000);
    return () => clearInterval(t);
  }, [busy]);

  const saved = !!(mb && mb.host);
  const save = async () => {
    if (busy) return;
    const n = Number(port);
    if (!host.trim() || !user.trim()) { setErr('Enter the IMAP server and the username.'); return; }
    if (!Number.isInteger(n) || n < 1 || n > 65535) { setErr('The port must be a number from 1 to 65535.'); return; }
    setBusy(true); setErr('');
    try {
      adopt(await api('/api/mailbox', {
        method: 'PUT', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ host: host.trim(), port: n, username: user.trim(), password: pass.replace(/\s+/g, '') }),
      }));
    } catch (e) { setErr(e.message); } finally { setBusy(false); }
  };
  const remove = async () => {
    if (!(await confirmDialog('Remove the mailbox? Personas stop receiving mail. Mail already received stays on the Inbox page.', { confirm: 'Remove mailbox', danger: true }))) return;
    setBusy(true); setErr('');
    try { await api('/api/mailbox', { method: 'DELETE' }); await load(); } catch (e) { setErr(e.message); } finally { setBusy(false); }
  };

  const st = mb && mb.status;
  const line = !st ? null
    : st.state === 'ok' ? { cls: 'ok', text: `Connected${fmtChecked(st.checked) ? ', checked ' + fmtChecked(st.checked) : ''}` }
    : st.state === 'error' ? { cls: 'bad', text: `Can't connect: ${st.error || 'unknown error'}` }
    : { cls: '', text: 'Not connected' };

  return html`
    <div class="card" id="inbox-card">
      <h2>Inbox</h2>
      <p class="hint">One mailbox for all personas. Each persona gets mail at its own address, such as cast+john@yourdomain.com. Use a mailbox nobody uses for real work.</p>
      ${loadErr ? html`<div class="note bad" role="alert">Could not load the mailbox: ${loadErr}</div>` : null}
      ${line ? html`<p class="mbstatus"><span class=${'dot ' + line.cls}></span>${line.text}</p>` : null}
      <div class="mbform">
        <div class="field">
          <label for="mb-host">IMAP server</label>
          <input id="mb-host" value=${host} placeholder="imap.gmail.com" autocomplete="off" onInput=${(e) => setHost(e.currentTarget.value)} />
        </div>
        <div class="field">
          <label for="mb-port">Port</label>
          <input id="mb-port" value=${port} inputmode="numeric" onInput=${(e) => setPort(e.currentTarget.value)} />
        </div>
        <div class="field">
          <label for="mb-user">Username</label>
          <input id="mb-user" value=${user} placeholder="cast@yourdomain.com" autocomplete="off" autocapitalize="off" spellcheck=${false}
            data-1p-ignore data-lpignore="true" data-bwignore data-form-type="other" onInput=${(e) => setUser(e.currentTarget.value)} />
        </div>
        <div class="field">
          <label for="mb-pass">App password</label>
          <div class="secret">
            <input id="mb-pass" type=${maskByCSS || showPass ? 'text' : 'password'} class=${showPass ? '' : 'masked'} value=${pass}
              autocomplete="off" autocapitalize="off" autocorrect="off" spellcheck=${false}
              data-1p-ignore data-lpignore="true" data-bwignore data-form-type="other"
              placeholder=${mb && mb.passwordSet ? 'Saved' : ''} onInput=${(e) => setPass(e.currentTarget.value)} />
            <button class="btn icon" type="button" aria-pressed=${showPass} aria-controls="mb-pass"
              title=${showPass ? 'Hide password' : 'Show password'} aria-label=${showPass ? 'Hide password' : 'Show password'}
              onClick=${() => setShowPass((v) => !v)}>${showPass ? eyeOff : eye}</button>
          </div>
          <span class="hint">Spaces are removed when you save.</span>
        </div>
      </div>
      <div class="row" style="margin-top:12px">
        <button class="btn primary" type="button" disabled=${busy} onClick=${save}>${busy ? 'Checking…' : 'Save and test'}</button>
        ${saved ? html`<button class="btn danger-ghost" type="button" disabled=${busy} onClick=${remove}>Remove</button>` : null}
      </div>
      ${err ? html`<div class="note bad" role="alert">${err}</div>` : null}

      <div class="field" style="margin-top:16px">
        <label>Accept mail from</label>
        <span class="hint">Senders or domains your site mails from, such as smesolutions.uk. Mail from anyone else is kept out of the personas' reach.</span>
        <${Tags} tags=${allowFrom} label="Accept mail from" placeholder="smesolutions.uk" more="Add sender"
          onChange=${(fn) => onChange((d) => fn(d.allowFrom))} />
      </div>
      <div class="field" style="margin-top:14px">
        <label for="mb-auth">Verifying server <span class="hint">(optional)</span></label>
        <input id="mb-auth" value=${authServId} placeholder="mx.google.com" autocomplete="off"
          onInput=${(e) => { const v = e.currentTarget.value; onChange((d) => { d.authServId = v; }); }} />
        <span class="hint">The server name your mail provider puts in Authentication-Results. Leave empty for Gmail and Google Workspace. Mail whose sender isn't verified by that server is kept out.</span>
      </div>
    </div>`;
}
