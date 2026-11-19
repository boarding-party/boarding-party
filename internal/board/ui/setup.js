// Setup page: a new project's owner gives the site and the stories, and the
// board makes a first-run lead that writes the personas and starts them.
import { useState, useEffect } from './vendor/hooks.module.js';
import { html, api } from './lib.js';
import { AgentKeyCard, MailboxFields } from './project.js';
import { leadHref } from './leads.js';
import { parseStories, costLine, loginRowsValid, MAX_STORIES, MAX_STORY, MAX_PERSONAS, DEFAULT_PERSONAS } from './setup-rules.js';

// FIRST_RUN_LEAD is the lead Start makes (project.FirstRunLead).
const FIRST_RUN_LEAD = 'First-run lead';

const PLACEHOLDER = 'A small business owner signs up and creates their first invoice\nAn accountant adds a client and exports a VAT return';

// onDone reloads the app's project after Start.
export function SetupPage({ onDone }) {
  const [state, setState] = useState(null); // {needed, stagingURL, keySet, mailbox: null | {saved}}
  const [loadErr, setLoadErr] = useState('');
  const [site, setSite] = useState('');
  const [text, setText] = useState('');
  const [personas, setPersonas] = useState(DEFAULT_PERSONAS);
  const [budget, setBudget] = useState('5');
  const [logins, setLogins] = useState([]);
  const [mailSaved, setMailSaved] = useState(false);
  const [touched, setTouched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');

  useEffect(() => {
    (async () => {
      try {
        const s = await api('api/setup');
        const key = await api('api/agent-key').catch(() => null);
        // No inbox service means mail is not available at all: no step.
        const mb = await api('api/mailbox').catch(() => null);
        // A director without a key store (key == null) has nothing to ask for.
        setState({ ...s, keySet: !key || key.kind === 'api-key' || key.kind === 'subscription', mailbox: mb ? { saved: !!mb.host } : null });
        setMailSaved(!!(mb && mb.host));
        setSite((cur) => cur || s.stagingURL || '');
      } catch (e) { setLoadErr(e.message); }
    })();
  }, []);

  if (loadErr) return html`<section class="page setup"><h1>Set up</h1><div class="note bad" role="alert">Could not load: ${loadErr}</div></section>`;
  if (!state) return html`<section class="page setup"><h1>Set up</h1><p class="muted">Loading…</p></section>`;
  if (!state.needed) {
    return html`<section class="page setup"><h1>Set up</h1>
      <p class="lede">This project is already set up. Change its roles, personas and stories on the <a href="#/project">Project</a> page.</p></section>`;
  }

  const { stories, errors } = parseStories(text);
  const n = Number(budget);
  const budgetErr = !(n > 0) || n > 1000 ? 'Money per day must be more than 0 and at most 1000 US dollars.' : '';
  const siteErr = /^https:\/\/[^/\s]+/.test(site.trim()) ? '' : 'The site must start with https://.';
  const loginErr = loginRowsValid(logins);
  const problem = siteErr || errors[0] || budgetErr || loginErr;

  const setLogin = (i, k, v) => setLogins((ls) => ls.map((l, j) => (j === i ? { ...l, [k]: v } : l)));
  const start = async () => {
    setTouched(true);
    if (problem || busy) return;
    setBusy(true); setErr('');
    try {
      await api('api/setup', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ site: site.trim(), stories, personas, dailyBudget: n,
          logins: logins.map((l) => ({ kind: l.kind.trim(), username: l.username.trim(), password: l.password })) }),
      });
      if (onDone) await onDone();
      location.hash = leadHref(FIRST_RUN_LEAD);
    } catch (e) { setErr(e.message); setBusy(false); }
  };

  return html`
    <section class="page setup">
      <h1>Set up</h1>
      <p class="lede">Tell the board your site and what has to work there. It writes ${personas === 1 ? 'a persona' : personas + ' personas'} of different ages, devices and confidence, and starts them. You can change them afterwards.</p>
      <div class="stack setupform">
        <div class="card">
          <div class="field">
            <label for="su-site">Site</label>
            <input id="su-site" value=${site} placeholder="https://staging.example.com" autocomplete="off" spellcheck=${false}
              onInput=${(e) => setSite(e.currentTarget.value)} />
            <span class="hint">The staging site the personas use. Use one you don't mind them signing up to.</span>
            ${touched && siteErr ? html`<span class="ferr">${siteErr}</span>` : null}
          </div>
        </div>
        ${state.keySet ? null : html`<${AgentKeyCard} />`}
        ${state.mailbox && !state.mailbox.saved ? html`
          <div class="card">
            <h2>Mailbox</h2>
            <p class="hint">One mailbox for all personas, so they can confirm sign-ups and reset passwords. You can skip it and add it later on the Project page.</p>
            <${MailboxFields} onSaved=${() => setMailSaved(true)} />
            ${mailSaved ? null : html`<p class="note">Without a mailbox, stories that need an email, such as sign-up or password reset, stop at the email step.</p>`}
          </div>` : null}
        <div class="card">
          <div class="field">
            <label for="su-stories">Stories</label>
            <span class="hint">What a person does on your site, one per line, in plain words. Up to ${MAX_STORIES}, each up to ${MAX_STORY} characters.</span>
            <textarea id="su-stories" rows="5" value=${text} placeholder=${PLACEHOLDER} onInput=${(e) => setText(e.currentTarget.value)}></textarea>
            <span class="hint">${stories.length} of ${MAX_STORIES}</span>
            ${(touched || stories.length > MAX_STORIES || errors[0] !== 'Write at least one story.') && errors[0] ? html`<span class="ferr">${errors[0]}</span>` : null}
          </div>
          <div class="row setuprow">
            <div class="field">
              <label for="su-personas">Personas</label>
              <select id="su-personas" value=${String(personas)} onChange=${(e) => setPersonas(Number(e.currentTarget.value))}>
                ${Array.from({ length: MAX_PERSONAS }, (_, i) => i + 1).map((k) => html`<option key=${k} value=${String(k)} selected=${k === personas}>${k}</option>`)}
              </select>
            </div>
            <div class="field">
              <label for="su-budget">Money per day (US dollars)</label>
              <input id="su-budget" value=${budget} inputmode="decimal" onInput=${(e) => setBudget(e.currentTarget.value)} />
            </div>
          </div>
          <p class="hint">${budgetErr && touched ? html`<span class="ferr">${budgetErr}</span>` : costLine(n > 0 ? n : 0, personas, Math.max(stories.length, 1))}</p>
        </div>
        <div class="card">
          <h2>Logins <span class="hint">(optional)</span></h2>
          <p class="hint">Accounts the personas can't make themselves, such as an admin. A run that needs one you didn't give waits, and asks you for it.</p>
          ${logins.map((l, i) => html`
            <div class="row loginrow" key=${i}>
              <input aria-label="Kind of user" placeholder="Kind of user, such as admin" value=${l.kind} onInput=${(e) => setLogin(i, 'kind', e.currentTarget.value)} />
              <input aria-label="Username or email" placeholder="Username or email" value=${l.username} autocomplete="off" spellcheck=${false}
                data-1p-ignore data-lpignore="true" data-bwignore onInput=${(e) => setLogin(i, 'username', e.currentTarget.value)} />
              <input aria-label="Password" placeholder="Password" type="password" value=${l.password} autocomplete="new-password"
                data-1p-ignore data-lpignore="true" data-bwignore onInput=${(e) => setLogin(i, 'password', e.currentTarget.value)} />
              <button class="btn ghost" type="button" onClick=${() => setLogins((ls) => ls.filter((_, j) => j !== i))}>Remove</button>
            </div>`)}
          <button class="btn" type="button" onClick=${() => setLogins((ls) => [...ls, { kind: '', username: '', password: '' }])}>Add login</button>
          ${touched && loginErr ? html`<p class="ferr">${loginErr}</p>` : null}
        </div>
        <div class="row">
          <button class="btn primary" type="button" disabled=${busy} onClick=${start}>${busy ? 'Starting…' : 'Start'}</button>
          <a href="#/project" class="small">Set up by hand instead</a>
        </div>
        ${err ? html`<div class="note bad" role="alert">${err}</div>` : null}
      </div>
    </section>`;
}
