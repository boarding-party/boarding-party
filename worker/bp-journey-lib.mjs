// What a journey module is handed: data (seeded values, each one recorded),
// bp (named steps and helpers) and expect. Kept free of Playwright so the
// pieces that decide a replay's inputs can be checked on their own.

import { generators, edgeStream } from './bp-data.mjs';

// rng is mulberry32: small, fast and the same on every machine for a seed.
export function rng(seed) {
  let a = Number(BigInt.asUintN(32, BigInt(seed))) || 1;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// makeData returns the journey's data helpers. Every value drawn lands in
// inputs under its helper's name and draw number (int1, pick2, …), so a
// replay can be read back and repeated from its seed. The edge cases a
// replay carries are listed in inputs.edges as "<name><n>:<id>". today
// (YYYY-MM-DD) fixes the day dates are drawn from.
export function makeData(seed, inputs, { today = '' } = {}) {
  if (today && !/^\d{4}-\d{2}-\d{2}$/.test(today)) throw new Error('makeData today must be YYYY-MM-DD');
  const next = rng(seed);
  const counts = {};
  const keep = (name, v) => {
    counts[name] = (counts[name] || 0) + 1;
    inputs[name + counts[name]] = v;
    return v;
  };
  const edgeKeep = (name, id) => {
    (inputs.edges ||= []).push(`${name}${counts[name]}:${id}`);
  };
  return {
    seed,
    ...generators(next, edgeStream(seed), keep, edgeKeep, today),
    int(min, max) {
      if (!Number.isInteger(min) || !Number.isInteger(max) || max < min) throw new Error('data.int(min, max) needs whole numbers with min <= max');
      return keep('int', min + Math.floor(next() * (max - min + 1)));
    },
    pick(list) {
      if (!Array.isArray(list) || list.length === 0) throw new Error('data.pick needs a non-empty list');
      return keep('pick', list[Math.floor(next() * list.length)]);
    },
    // plusEmail turns info@example.com into info+<seed>@example.com, or
    // info+megan@… into info+megan-<seed>@…: a fresh address each replay
    // that still reaches the persona's mailbox.
    plusEmail(base) {
      const m = /^([^@+\s]+)(\+[^@\s]*)?@([^@\s]+)$/.exec(String(base || ''));
      if (!m) throw new Error('data.plusEmail needs an email address');
      const tag = m[2] ? `${m[2]}-${seed}` : `+${seed}`;
      return keep('plusEmail', `${m[1]}${tag}@${m[3]}`);
    },
  };
}

// money formats an amount as GBP the way UK sites show it: £1,234.56.
export function money(n) {
  return '£' + Number(n).toLocaleString('en-GB', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

// StepFailed carries where a journey stopped.
export class StepFailed extends Error {
  constructor(step, name, cause) {
    super(cause && cause.message ? cause.message : String(cause));
    this.step = step;
    this.stepName = name;
  }
}

// WaitReached stops a replay at a journey's n-th bp.waitFor, after step
// steps; until is what it waits for, in the director's until shape.
export class WaitReached extends Error {
  constructor(n, step, until) {
    super(`waiting at wait ${n}`);
    this.n = n;
    this.step = step;
    this.until = until;
  }
}

// waitUntil checks a bp.waitFor argument and returns it as the director's
// until: exactly one of email {from, subject} or time {after | at}, and
// within as the timeout.
export function waitUntil(spec) {
  const s = spec || {};
  if (s.handoff) throw new Error('a replay cannot wait for a hand-off');
  if (!!s.email === !!s.time) throw new Error('bp.waitFor needs exactly one of email or time');
  const out = {};
  if (s.email) {
    const { from, subject } = s.email;
    if (!from && !subject) throw new Error('bp.waitFor email needs from or subject');
    out.email = { ...(from ? { from: String(from) } : {}), ...(subject ? { subject: String(subject) } : {}) };
  } else {
    const { after, at } = s.time;
    if (!after === !at) throw new Error('bp.waitFor time needs after or at');
    out.time = after ? { after: String(after) } : { at: String(at) };
  }
  if (s.within) out.timeout = String(s.within);
  return out;
}

// arrival is what a resumed bp.waitFor returns: what ended the wait and, for
// an email, the message with link(re) to pick a link from it.
function arrival(a, n) {
  if (!a) throw new Error(`bp.waitFor ${n}: the replay was resumed without its arrival`);
  return {
    detail: a.detail,
    message: a.message,
    link(re) {
      const links = (a.message && a.message.links) || [];
      const m = links.find((u) => (re instanceof RegExp ? re.test(u) : u.includes(String(re))));
      if (!m) throw new Error(`no link matching ${re} in the email`);
      return m;
    },
  };
}

// makeBp returns the journey's bp helpers. onFail runs once, at the failing
// step, before the error leaves it (the screenshot is taken there). resume
// ({after, arrivals}) picks a replay up after its journey's wait number
// after: earlier steps are counted, not run, and earlier waits return their
// arrivals. A wait past it stops the replay with WaitReached.
export function makeBp({ onFail, resume } = {}) {
  let n = 0;
  let waits = 0;
  const after = (resume && resume.after) || 0;
  const arrivals = (resume && resume.arrivals) || [];
  return {
    async step(name, fn) {
      if (typeof name !== 'string' || !name.trim() || typeof fn !== 'function') throw new Error('bp.step(name, fn) needs a name and a function');
      const at = ++n;
      if (waits < after) return undefined; // done before the wait this replay resumes after
      try {
        return await fn();
      } catch (e) {
        if (e instanceof StepFailed || e instanceof WaitReached) throw e; // a step inside a step: the inner one is where it failed
        if (onFail) await onFail(at, name).catch(() => {});
        throw new StepFailed(at, name, e);
      }
    },
    money,
    // totalWithVat is the sum of quantity × price over lines, plus VAT at
    // rate, to the penny.
    totalWithVat(lines, rate = 0.2) {
      const net = (lines || []).reduce((s, l) => s + Number(l.quantity) * Number(l.price), 0);
      return Math.round(net * (1 + rate) * 100) / 100;
    },
    // ukDate turns 2026-02-09 into 09/02/2026.
    ukDate(iso) {
      const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(iso || ''));
      if (!m) throw new Error('bp.ukDate needs a YYYY-MM-DD date');
      return `${m[3]}/${m[2]}/${m[1]}`;
    },
    async waitFor(spec) {
      const until = waitUntil(spec);
      const i = ++waits;
      if (i <= after) return arrival(arrivals[i - 1], i);
      throw new WaitReached(i, n, until);
    },
  };
}

// cleanError keeps an error readable and short; Playwright's call logs can
// run to pages.
export function cleanError(msg) {
  const s = String(msg || '').replace(/\u001b\[[0-9;]*m/g, '');
  const cut = s.indexOf('\nCall log:');
  return (cut >= 0 ? s.slice(0, cut) : s).trim().slice(0, 2000);
}

export const devices = {
  'iphone-15': 'iPhone 15',
  'iphone-15-pro-max': 'iPhone 15 Pro Max',
  'pixel-7': 'Pixel 7',
  'galaxy-s24': 'Galaxy S24',
  ipad: 'iPad (gen 11)',
  'ipad-mini': 'iPad Mini',
};
