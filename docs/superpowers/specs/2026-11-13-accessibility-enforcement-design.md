# Accessibility settings the browser enforces

Approved 2026-11-13 ("proceed with accessibility enforcement").

## Why

A persona's assistive setting was a sentence in the prompt. "Imagine 200%
zoom" is not what the page does at 200%, and a keyboard-only persona could
still click. Findings from those personas were guesses. The browser should
behave the way the persona's browser would, so a finding is evidence.

## What changes

Profile fields (single choice each, stable keys):

- Assistive technology (existing keys): `screen-reader`, `keyboard-only`, `zoom`.
- Eyesight (new, `vision`): `blurred`, `low-contrast`, `deuteranopia`,
  `protanopia`, `tritanopia`, `achromatopsia`.
- Display settings (new, `display`): `reduced-motion`, `high-contrast`, `dark`.
- Age stays role-play: no browser setting simulates it.

How each is enforced:

| Setting | Enforced by |
|---|---|
| Zoom | Viewport halved, device scale factor doubled: the page lays out as at 200% browser zoom |
| Keyboard only | The tool gate removes click, hover, drag, drop, select, fill, element typing and JS evaluation, refuses `javascript:` URLs, and adds `browser_type_focused`, which types into the focused element at a person's pace |
| Screen reader | Image responses omitted: the agent works from the accessibility snapshot only. Screenshots are still saved for evidence |
| Eyesight | Chrome's vision deficiency emulation on every page (screenshots and the live screen show it) |
| Display | Context options: `reducedMotion`, `forcedColors` + `contrast: more`, `colorScheme: dark` |

Every persona loses `browser_resize` and `browser_emulate_media`: the browser's
settings are the persona's, and the agent cannot undo them.

## Shape

- The director passes stable keys only (`--bp-device`, `--bp-assistive`,
  `--bp-vision`, `--bp-display` to bp-browser; `--device`, `--assistive`,
  `--vision`, `--display` to bp-replay). The mapping from keys to browser
  settings lives once, in `worker/bp-a11y.mjs`, used by both AI runs and
  replays. Desktop viewports move there too, so replays of a desktop persona
  get its real screen size (they had Playwright's 1280×720).
- `bp-browser` writes the Playwright MCP config for the run and starts
  Playwright MCP behind `bp-gate.mjs`, a stdio proxy that filters
  `tools/list` and refuses blocked `tools/call`s with a reason the agent can
  act on.
- `bp-browser` also records the persona's settings in
  `/tmp/bp-pw/settings.json`; `bp-replay --self` uses them, flags on its
  command line winning setting by setting, so an AI run's self-replay renders
  like the scheduled replay.

## Not doing

- Taking the snapshot away from eyesight personas: actions need its refs.
  The agent is told to judge legibility from the screenshots it sees.
- Simulating age, tremor or cognitive load.
- Holding a determined agent to the gate. The agent has a shell in the pod,
  so it could drive Chrome over its local CDP port or open a saved
  screenshot. The gate stops the model drifting back to clicking or looking;
  the prompt forbids going around it.
- Keyboard-only replays. A journey is code; its replay renders like the
  persona's browser but its clicks are the journey's own.
