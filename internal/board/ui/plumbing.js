// withoutPlumbing drops the agent's housekeeping from a run's narration:
// finding tools, titling the session, starting the recording and trace, page
// snapshots it reads for itself, its to-do list and the files it reads back.
// None of that is something the persona did on the site, so it only buries
// the steps a reviewer is looking for. Thoughts always stay.
const PLUMBING = new Set(['ToolSearch', 'set_title', 'next_assignment', 'browser_start_video', 'browser_stop_video',
  'browser_start_tracing', 'browser_stop_tracing', 'browser_snapshot', 'TodoWrite', 'Read']);

export function withoutPlumbing(entries) {
  return entries.filter((e) => e.kind !== 'action' || !PLUMBING.has(e.text.split(':')[0].trim()));
}
