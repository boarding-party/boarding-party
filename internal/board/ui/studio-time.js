// Time helpers for the run studio. Pure, no imports, so node can test them
// (internal/board/uitest). Times are Unix milliseconds unless named seconds.

// placeOnVideo is where a moment falls on a video that started at
// videoStartMs and lasts durationS seconds, clamped onto it: a thought said
// before the first frame sits at 0, one after the end at the end.
export function placeOnVideo(atMs, videoStartMs, durationS) {
  if (!Number.isFinite(atMs) || !Number.isFinite(videoStartMs) || !Number.isFinite(durationS) || durationS <= 0) return 0;
  return Math.min(durationS, Math.max(0, (atMs - videoStartMs) / 1000));
}

// captionAt is the thought to caption at playheadMs: the latest one said at
// or before it, while it is less than windowMs old.
export function captionAt(narration, playheadMs, windowMs = 6000) {
  let best = null;
  for (const e of narration || []) {
    if (e.kind !== 'thought') continue;
    const t = Date.parse(e.at);
    if (!(t <= playheadMs)) continue;
    if (!best || t >= best.t) best = { t, e };
  }
  return best && playheadMs - best.t < windowMs ? best.e : null;
}

// nearest is the index of the last item whose key is at or before ms, or 0.
export function nearest(list, ms, key) {
  let at = 0;
  for (let i = 0; i < (list || []).length; i++) if (key(list[i]) <= ms) at = i;
  return at;
}
