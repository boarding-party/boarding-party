// What a journey module is handed: data (seeded values, each one recorded),
// bp (named steps and helpers) and expect. Kept free of Playwright so the
// pieces that decide a replay's inputs can be checked on their own.

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
// replay can be read back and repeated from its seed.
export function makeData(seed, inputs) {
  const next = rng(seed);
  const counts = {};
  const keep = (name, v) => {
    counts[name] = (counts[name] || 0) + 1;
    inputs[name + counts[name]] = v;
    return v;
  };
  return {
    seed,
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

// makeBp returns the journey's bp helpers. onFail runs once, at the failing
// step, before the error leaves it (the screenshot is taken there).
export function makeBp({ onFail } = {}) {
  let n = 0;
  return {
    async step(name, fn) {
      if (typeof name !== 'string' || !name.trim() || typeof fn !== 'function') throw new Error('bp.step(name, fn) needs a name and a function');
      const at = ++n;
      try {
        return await fn();
      } catch (e) {
        if (e instanceof StepFailed) throw e; // a step inside a step: the inner one is where it failed
        if (onFail) await onFail(at, name).catch(() => {});
        throw new StepFailed(at, name, e);
      }
    },
    money,
    async waitFor() {
      throw new Error('waits arrive in a later version: bp.waitFor cannot run in a replay yet');
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
