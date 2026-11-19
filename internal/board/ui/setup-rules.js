// Pure rules for the setup page: reading the stories box, what the first
// day can run, and checking the logins. No DOM, so node:test covers them.
// The limits match the director's (project.MaxFirstRun*).

export const MAX_STORIES = 5;
export const MAX_STORY = 200;
export const MAX_PERSONAS = 6;
export const DEFAULT_PERSONAS = 4;
export const MAX_RUNS = 30;

// parseStories reads the stories box: one story per line.
export function parseStories(text) {
  const lines = (text || '').split('\n');
  const stories = [];
  const errors = [];
  lines.forEach((l) => {
    const s = l.trim();
    if (!s) return;
    stories.push(s);
    if ([...s].length > MAX_STORY && !errors.length) errors.push(`Story ${stories.length} is longer than ${MAX_STORY} characters.`);
  });
  if (!stories.length) errors.push('Write at least one story.');
  else if (stories.length > MAX_STORIES) errors.unshift(`At most ${MAX_STORIES} stories.`);
  return { stories, errors: errors.slice(0, 1) };
}

// runsPerDay is how many persona runs the first-run lead may start a day.
export function runsPerDay(personas, stories) {
  return Math.min(personas * stories, MAX_RUNS);
}

// costLine says what the day's money and runs allow.
export function costLine(budget, personas, stories) {
  const n = runsPerDay(personas, stories);
  return `Up to $${budget} and ${n} ${n === 1 ? 'run' : 'runs'} a day, shared with the lead's own short runs. Runs past either limit wait until the next day.`;
}

// loginRowsValid returns the first problem with the logins, or ''.
export function loginRowsValid(rows) {
  const seen = new Set();
  for (const r of rows || []) {
    const kind = (r.kind || '').trim();
    if (!kind || !(r.username || '').trim() || !r.password) return 'Each login needs a kind of user, a username and a password.';
    const k = kind.toLowerCase();
    if (seen.has(k)) return `Two logins are for "${k}". Keep one per kind of user.`;
    seen.add(k);
  }
  return '';
}
