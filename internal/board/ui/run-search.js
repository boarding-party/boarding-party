// runMatches says whether a run fits the runs search q: its persona, role,
// story or outcome holds q, or its ID starts with it (the short IDs people
// copy from an address).
export function runMatches(run, q) {
  const s = q.trim().toLowerCase();
  if (!s) return true;
  return (run.id || '').toLowerCase().startsWith(s)
    || `${run.persona} ${run.role || ''} ${run.story} ${run.outcome || ''}`.toLowerCase().includes(s);
}
