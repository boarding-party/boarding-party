// LoginWaitCard asks the owner for a login a waiting run needs. The kind and
// note come from the persona, so they are shown as text only.
import { useState } from './vendor/hooks.module.js';
import { html, api } from './lib.js';

export function LoginWaitCard({ run, wait, onSaved }) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState('');
  const [done, setDone] = useState(false);
  const save = async (e) => {
    e.preventDefault();
    if (busy || !username.trim() || !password) return;
    setBusy(true); setErr('');
    try {
      await api(`api/runs/${encodeURIComponent(run.id)}/login`, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: username.trim(), password }),
      });
      setDone(true); setPassword('');
      if (onSaved) onSaved();
    } catch (e2) { setErr(e2.message); } finally { setBusy(false); }
  };
  return html`
    <form class="card loginwait" onSubmit=${save}>
      <h2>Waiting for a login: ${wait.login}</h2>
      <p class="small">${run.persona} needs an account it can't make itself${wait.note ? html`, to ${wait.note}` : ''}.
        Give one and the run carries on within a minute. Personas of this kind use it from now on.</p>
      ${done ? html`<div class="note ok" role="status">Saved. The run carries on shortly.</div>` : html`
        <div class="row loginrow">
          <input aria-label="Username or email" placeholder="Username or email" value=${username} autocomplete="off" spellcheck=${false}
            data-1p-ignore data-lpignore="true" data-bwignore onInput=${(e) => setUsername(e.currentTarget.value)} />
          <input aria-label="Password" placeholder="Password" type="password" value=${password} autocomplete="new-password"
            data-1p-ignore data-lpignore="true" data-bwignore onInput=${(e) => setPassword(e.currentTarget.value)} />
          <button class="btn primary" type="submit" disabled=${busy || !username.trim() || !password}>${busy ? 'Saving…' : 'Save and carry on'}</button>
        </div>`}
      ${err ? html`<div class="note bad" role="alert">${err}</div>` : null}
    </form>`;
}
