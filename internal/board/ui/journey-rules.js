// Pure rules for the journeys list: a journey's title from its story, the
// steps a row shows, and the replay summaries. No DOM, so node:test covers
// them.

// storyOf is the goal and role of persona's story in the project; both are
// '' when the project no longer has it.
export function storyOf(project, persona, story) {
  for (const r of (project && project.roles) || []) {
    if (!(r.personas || []).some((p) => p.name === persona)) continue;
    const s = (r.stories || []).find((x) => x.name === story);
    if (s) return { goal: s.goal || '', role: r.name };
  }
  return { goal: '', role: '' };
}

// stepsShown is the first max steps and how many more; a single hidden step
// is shown rather than counted.
export function stepsShown(steps, max) {
  const s = steps || [];
  if (s.length <= max + 1) return { shown: s, more: 0 };
  return { shown: s.slice(0, max), more: s.length - max };
}

const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;

// summaryLine sums the list for the line above it.
export function summaryLine(journeys) {
  let passed = 0, failed = 0;
  for (const j of journeys) {
    passed += (j.record && j.record.weekPassed) || 0;
    failed += (j.record && j.record.weekFailed) || 0;
  }
  const head = plural(journeys.length, 'journey', 'journeys');
  const n = passed + failed;
  if (!n) return `${head} · no replays this week`;
  if (n === 1) return `${head} · 1 replay this week, ${passed ? 'passed' : 'failed'}`;
  return `${head} · ${n} replays this week, ${passed} passed`;
}

// recordLine is a journey's passes out of all its replays.
export const recordLine = (j) => (j.passed + j.failed ? `${j.passed} of ${j.passed + j.failed} passed` : 'Not replayed yet');
