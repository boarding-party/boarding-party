// The run studio's split between the stage and the panels: the owner drags
// it, and the width is remembered in this browser.

export const PANEL_MIN = 320; // the tabs still fit
export const STAGE_MIN = 360; // the player and its timeline stay usable

// clampPanel is a panel width that leaves both sides usable in a window
// total pixels wide, or null when there is no width to keep.
export function clampPanel(w, total) {
  if (!Number.isFinite(w)) return null;
  return Math.max(PANEL_MIN, Math.min(w, total - STAGE_MIN));
}
