// The inbox: mail the director has received for the personas, laid out like
// a mail client. A list of one-line rows; a click opens the message in its
// place. Everything here is text from strangers: shown as text only, and
// links never become anchors, so a click here can't use up a one-time link a
// persona still needs.
import { useState, useEffect, useRef } from './vendor/hooks.module.js';
import { html, api, usePoll, useParams, setParams } from './lib.js';
import { mailDate, mailFull, initial, avatarTone, position } from './inbox-rules.js';

const INBOX_POLL_MS = 10000;
const INBOX_LIMIT = 100;

const icon = (d) => html`<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d=${d} /></svg>`;
const backIcon = icon('M19 12H5M12 19l-7-7 7-7');
const newerIcon = icon('M15 18l-6-6 6-6');
const olderIcon = icon('M9 18l6-6-6-6');

function Avatar({ name, big }) {
  return html`<span class=${'avatar tone-' + avatarTone(name) + (big ? ' big' : '')} aria-hidden="true">${initial(name)}</span>`;
}

// names are the project's persona names, for the filter.
export function InboxPage({ names }) {
  // Persona, tab and the open message live in the address.
  const p = useParams();
  const persona = p.persona || '';
  const tab = p.tab === 'kept' ? 'kept' : 'received';
  const openID = p.m || '';
  const [msgs, setMsgs] = useState(null);
  const [err, setErr] = useState('');
  const loaded = useRef(false); // the first load for this view has happened
  const fromList = useRef(false); // the open message was opened from the list

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
  // A row is a link, so opening a message adds a history entry and the
  // browser's Back returns to the list; stepping between messages replaces
  // it, and the back arrow goes back when the list opened the message.
  const hrefOf = (id) => {
    const h = location.hash || '#/inbox', i = h.indexOf('?');
    const q = new URLSearchParams(i < 0 ? '' : h.slice(i + 1));
    q.set('m', id);
    return (i < 0 ? h : h.slice(0, i)) + '?' + q;
  };
  const opened = () => { fromList.current = true; };
  const close = () => {
    if (fromList.current) { fromList.current = false; history.back(); } else setParams({ m: '' });
  };

  return html`
    <section class="page wide">
      <h1>Inbox</h1>
      <p class="lede">Mail sent to the personas' addresses. The mailbox is set up on the <a href="#/project/inbox">Project</a> page.</p>
      <div class="mailbox card flush">
        <div class="mailbar">
          <label class="vh" for="ib-persona">Persona</label>
          <select id="ib-persona" value=${persona} onChange=${(e) => pick({ persona: e.currentTarget.value })}>
            <option value="" selected=${!persona}>All personas</option>
            ${names.map((n) => html`<option key=${n} value=${n} selected=${persona === n}>${n}</option>`)}
          </select>
          <div class="mailtabs" role="tablist">
            <button type="button" role="tab" aria-selected=${tab === 'received'} class=${tab === 'received' ? 'on' : ''} onClick=${() => pick({ tab: '' })}>Received</button>
            <button type="button" role="tab" aria-selected=${tab === 'kept'} class=${tab === 'kept' ? 'on' : ''} onClick=${() => pick({ tab: 'kept' })}>Kept out</button>
          </div>
        </div>
        ${err ? html`<div class="note bad" role="alert">Could not load the mail: ${err}</div>` : null}
        ${openID
          ? html`<${MessageView} id=${openID} summary=${(msgs || []).find((m) => m.id === openID)} at=${position(msgs, openID)} onClose=${close} />`
          : html`<${MailList} msgs=${msgs} kept=${tab === 'kept'} err=${err} hrefOf=${hrefOf} onOpen=${opened} />`}
      </div>
    </section>`;
}

function MailList({ msgs, kept, err, hrefOf, onOpen }) {
  if (msgs === null) return err ? null : html`<p class="muted mailnote">Loading…</p>`;
  if (!msgs.length) return html`<div class="mailnote mailempty"><b>${kept ? 'Nothing kept out.' : 'No mail yet.'}</b>${kept ? 'Mail from senders you did not accept appears here.' : 'Mail sent to a persona\'s address appears here within about 10 seconds.'}</div>`;
  const now = new Date();
  return html`
    <ul class="maillist">
      ${msgs.map((m) => html`
        <li key=${m.id}>
          <a href=${hrefOf(m.id)} class="mailrow" onClick=${(e) => { if (!e.metaKey && !e.ctrlKey && !e.shiftKey && e.button === 0) onOpen(); }}>
            <${Avatar} name=${m.persona} />
            <span class="mname">${m.persona || 'No persona'}</span>
            <span class="mline">
              <span class="msubj">${m.subject || '(no subject)'}</span>
              <span class="mprev">${kept ? ` – from ${m.from}` : ''}${m.preview ? ` – ${m.preview}` : ''}</span>
            </span>
            ${m.started ? html`<span class="tag run mtag" title=${'This message started the story ' + m.started}>Started ${m.started}</span>` : null}
            ${kept && m.filtered ? html`<span class="tag wait mtag">${m.filtered}</span>` : null}
            <time class="mdate" datetime=${m.received} title=${mailFull(m.received, now)}>${mailDate(m.received, now)}</time>
          </a>
        </li>`)}
    </ul>`;
}

// MessageView is one message in full. summary is the list's row when the
// list holds it (shown while the message loads); at is its place in the list.
function MessageView({ id, summary, at, onClose }) {
  const [m, setM] = useState(null);
  const [err, setErr] = useState('');
  useEffect(() => {
    let live = true;
    setM(null); setErr('');
    api(`api/messages/${encodeURIComponent(id)}`).then((x) => { if (live) setM(x); }).catch((e) => { if (live) setErr(e.message); });
    return () => { live = false; };
  }, [id]);
  const head = m || summary;
  const step = (to) => to && setParams({ m: to });
  return html`
    <div class="mailview">
      <div class="mvbar">
        <button type="button" class="iconbtn" onClick=${onClose} title="Back to the list" aria-label="Back to the list">${backIcon}</button>
        ${at ? html`
          <span class="mvpos">${at.n} of ${at.of}</span>
          <button type="button" class="iconbtn" disabled=${!at.newer} onClick=${() => step(at.newer)} title="Newer" aria-label="Newer message">${newerIcon}</button>
          <button type="button" class="iconbtn" disabled=${!at.older} onClick=${() => step(at.older)} title="Older" aria-label="Older message">${olderIcon}</button>` : null}
      </div>
      ${err ? html`<div class="note bad">Could not load the message: ${err}</div>` : null}
      ${head ? html`
        <h2 class="mvsubj">${head.subject || '(no subject)'}${summary && summary.started ? html` <span class="tag run">Started ${summary.started}</span>` : null}${head.filtered ? html` <span class="tag wait">${head.filtered}</span>` : null}</h2>
        <div class="mvhead">
          <${Avatar} name=${head.persona} big />
          <div class="mvwho">
            <div><b>${head.persona || 'No persona'}</b>${m && m.to && m.to.length ? html` <span class="muted">to ${m.to.join(', ')}</span>` : null}</div>
            <div class="small muted">from ${head.from}</div>
          </div>
          <time class="small muted" datetime=${head.received}>${mailFull(head.received)}</time>
        </div>` : null}
      ${!m && !err ? html`<p class="muted small mvbody">Loading…</p>` : null}
      ${m ? html`
        <div class="mvbody">${m.text || html`<span class="muted">(no text)</span>`}</div>
        ${m.links && m.links.length ? html`
          <div class="mvlinks">
            <b class="small">Links in this message</b>
            <ul>${m.links.map((l, i) => html`<li key=${i}>${l}</li>`)}</ul>
            <p class="small muted">Links aren't clickable here, so you don't use up a link the persona needs.</p>
          </div>` : null}` : null}
    </div>`;
}
