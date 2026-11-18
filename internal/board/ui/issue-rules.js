// What the bugs list and an issue's page say about an issue and its
// reports. No imports, so node tests can load it.

export const DAY_MS = 86400000;

const at = (iso) => {
  const t = Date.parse(iso || '');
  return Number.isNaN(t) ? 0 : t;
};
const within = (iso, now) => {
  const t = at(iso);
  return t > 0 && now - t < DAY_MS;
};

// attention is the one tag that says an issue needs the owner, or null:
// back after a fix (until the owner triages it again), first reported in
// the last day, or reported as different in the last day, in that order.
export const seenAgain = (issue) => at(issue.reopened) > 0 && issue.status === 'open';

export function attention(issue, now) {
  if (seenAgain(issue)) {
    return { label: 'Seen again after fix', tag: 'bad' };
  }
  if (within(issue.firstSeen, now)) return { label: 'New today', tag: 'run' };
  if (within(issue.differentAt, now)) return { label: 'Something different', tag: 'sup' };
  return null;
}

// marking says how a report relates to its issue. A report that differs
// always says so, even as the issue's earliest.
export function marking(report, earliestId) {
  if (report.match === 'different') return { kind: 'different', text: 'Different: ' + (report.difference || '') };
  if (report.id === earliestId) return { kind: 'first', text: 'First report' };
  if (report.match === 'joined') return { kind: 'joined', text: 'Same as before (joined by the lead)' };
  if (report.match === 'same') return { kind: 'same', text: 'Same as before' };
  return { kind: '', text: '' };
}

// earliestOf is the id of the oldest report in a newest-first list.
export function earliestOf(reports) {
  let id = '', min = Infinity;
  for (const r of reports) {
    const t = at(r.created);
    if (t <= min) { min = t; id = r.id; }
  }
  return id;
}

const SEVERITIES = new Map([['high', 'high'], ['critical', 'high'], ['medium', 'medium'], ['low', 'low']]);

// severityOf is high, medium, low or '' for anything else.
export const severityOf = (s) => SEVERITIES.get((s || '').toLowerCase()) || '';
