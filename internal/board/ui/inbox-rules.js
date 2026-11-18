// Pure rules for the inbox: how a message's date reads, the persona's
// avatar, and where the open message sits in the list. No DOM, so node:test
// covers them.

// when parses a message time; Go's zero time (year 1) means not set.
function when(s) {
  if (!s) return null;
  const d = new Date(s);
  return isNaN(d) || d.getUTCFullYear() < 2000 ? null : d;
}

const sameDay = (a, b) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
const time = (d) => d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' });

// mailDate is the list's date, as a mail client shows it: the time today,
// the day and month this year, with the year before that.
export function mailDate(s, now = new Date()) {
  const d = when(s);
  if (!d) return '';
  if (sameDay(d, now)) return time(d);
  const opts = { day: 'numeric', month: 'short' };
  if (d.getFullYear() !== now.getFullYear()) opts.year = 'numeric';
  return d.toLocaleDateString('en-GB', opts);
}

// mailFull is the open message's date: weekday, date and time.
export function mailFull(s, now = new Date()) {
  const d = when(s);
  if (!d) return '';
  const opts = { weekday: 'short', day: 'numeric', month: 'short' };
  if (d.getFullYear() !== now.getFullYear()) opts.year = 'numeric';
  return d.toLocaleDateString('en-GB', opts).replace(',', '') + ', ' + time(d);
}

// initial is the letter on a persona's avatar.
export function initial(name) {
  const c = [...(name || '').trim()][0];
  return c ? c.toUpperCase() : '?';
}

// AVATAR_TONES are the board's tag colours; a name always gets the same one.
export const AVATAR_TONES = ['run', 'ok', 'wait', 'sup', 'bad', 'idle'];

export function avatarTone(name) {
  let h = 0;
  for (const c of name || '') h = (h * 31 + c.codePointAt(0)) >>> 0;
  return AVATAR_TONES[h % AVATAR_TONES.length];
}

// position is the open message's place in the newest-first list, with the
// ids either side; null when the list does not hold it.
export function position(list, id) {
  const i = (list || []).findIndex((m) => m.id === id);
  if (i < 0) return null;
  return { n: i + 1, of: list.length, newer: i > 0 ? list[i - 1].id : '', older: i + 1 < list.length ? list[i + 1].id : '' };
}
