// One-line summaries the project editor shows on its closed sections.

// MODELS are the models offered in the editor, cheapest first. A model the
// project already names but this list lacks stays selectable.
export const MODELS = [
  { value: 'claude-haiku-5-5', label: 'Haiku 5.5, cheapest', short: 'Haiku 5.5' },
  { value: 'claude-sonnet-5-5', label: 'Sonnet 5.5', short: 'Sonnet 5.5' },
  { value: 'claude-opus-5-5', label: 'Opus 5.5, most thorough', short: 'Opus 5.5' },
];

// scheduleSummary is a role's activity and fresh AI run in a sentence.
export function scheduleSummary(role) {
  const activity = role.activity.trim();
  const explore = role.explore.trim() || 'weekly';
  return `${activity ? 'Runs ' + activity : 'Runs by hand or hand-off'}. Fresh AI run ${explore}.`;
}

// profileSummary lists a persona's set profile values by their labels, in
// field order, then its traits.
export function profileSummary(profile, traits, fields) {
  const out = [];
  for (const f of fields) {
    const v = profile[f.key];
    if (!v) continue;
    const o = (f.options || []).find((x) => x.value === v);
    out.push(o ? o.label : v);
  }
  return [...out, ...traits].join(' · ');
}

// storyOptionsSummary names a story's options that are not the default, or ''.
export function storyOptionsSummary(story) {
  const out = [];
  if (story.model) {
    const m = MODELS.find((x) => x.value === story.model);
    out.push(m ? m.short : story.model);
  }
  if (story.handoffs.length) out.push(`hands off to ${story.handoffs.length}`);
  if (story.trigger.kind === 'message') out.push('starts on an email');
  if (story.trigger.kind === 'calendar') out.push('starts on a calendar event');
  if (story.freshBrowser) out.push('empty browser');
  if (!story.replay) out.push('no replays');
  return out.join(' · ');
}
