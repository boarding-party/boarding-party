# Run studio: the run page as a session replay

Approved layout: 2026-11-16, in chat. Inspired by LogRocket's session replay.

## Why

The run page today is a long scrolling page of cards. The owner wants it to
feel like watching a real session: the recording fixed in place, everything
else in tabbed panels beside it, the page never scrolling as a whole. They
also want to hear the persona "think aloud" while watching, and to see the
real page (sharp, iframes included) at any moment, not only the video.

## Goals

- The run page fills the window. No page scroll; only panel contents scroll.
  The global menu (`<nav aria-label="Main">` and the leads nav) is hidden on
  this page; a slim top bar replaces it.
- The player stays fixed: video for an ended run, live frames while running
  (today's `LiveView` / `FrameView` / `<video>` logic, unchanged in behaviour).
- A time bar under the player: activity shading, marks for bugs, console
  errors and thoughts; click or drag to scrub (today's `Timeline` behaviour).
- Thoughts: the model's own words, shown as a caption over the player at the
  moment it said them, and as a feed in a panel.
- A **Video | Page** toggle: Page shows the Playwright trace snapshot nearest
  the playhead — the real page as it was, sharp, with iframes.
- Panel tabs on the right, each scrolling on its own; every row with a time
  seeks the player: **Thoughts** (default), **Bugs**, **Console**,
  **Network**, **Info**.

## Non-goals

- A live console a developer can type into (dropped by the owner).
- Replacing the video. The video stays the true record; the trace snapshot is
  per action, not continuous, and cannot show `<video>`.
- A JS build step for the board. It stays preact + htm, no bundler.
- Changing other pages' look.

## Anti-goals

- A page that scrolls as a whole on desktop.
- Losing anything today's run page shows (outcome, details, files, replay
  section, chain, persona, blocked hosts, restart banner, cost, turns).
- Thoughts that render site or model text as HTML.

## Layout

```
┌─ ← Runs   Maya (low vision) on Checkout  ● running  4m12  £0.31 ─────────┐
│ https://site/checkout                    [Video | Page] │ Thoughts Bugs 3 │
│ ┌─────────────────────────────────────────────────────┐ │ Console 12      │
│ │              recording, fitted, dark edges          │ │ Network  Info   │
│ │   "Trying the postcode field next"  (caption)       │ │ (own scroll)    │
│ └─────────────────────────────────────────────────────┘ │                 │
│ ▶ 1x  0:58/4:12  ▁▃▅▂▁▇▃ ▌bug ▌error ┊thought          │                 │
└──────────────────────────────────────────────────────────────────────────┘
```

- Top bar: back to Runs, persona on story, state tag, duration, cost, turns,
  and today's readout items. Banners (lost contact, restarted) sit under it.
- URL line: the page URL at the playhead, from the trace; hidden when unknown.
- Player area: dark, the recording fitted inside (letterboxed), never cropped.
- Right panel: about 380–440px wide on desktop. Under ~900px wide the page
  stacks: top bar, player, time bar, then the tabbed panel filling the rest
  of the screen height, its content scrolling. 16px side gutter on phones.
- Keyboard: tabs are a proper tablist (arrow keys), space plays/pauses when
  focus is not in an input.
- Keep the board's existing visual system (colours, type, tokens in
  `styles.css`); the player area may be dark in both themes.

## Thoughts

Source: Claude Code's transcript, which `worker/bp-usage.mjs` already reads
for token usage. Each assistant message's text blocks are thoughts; each
`tool_use` block is an action (tool name, plus a short summary of its input
— e.g. the element description for a click — cut to 200 characters). Bash
inputs carry the run token in the upload and profile URLs: Bash actions are
shown by tool name only, never their command, and any text matching the
run's URLs is dropped. Each entry carries the transcript line's `timestamp`.

- The worker sends the running list to the director while it grows, like
  usage: `PUT /narration/{token}` (worker mux, token in the URL, never
  logged). Body: `[{"at": RFC3339, "kind": "thought"|"action", "text": "..."}]`.
- Bounds: text cut to 500 characters, at most 2000 entries per run; the
  director refuses bigger bodies and unknown fields (as the console endpoint
  does). Reports after the run ends get the same grace as usage.
- Stored as artifact `narration.json`, hidden from Files like `usage.json`.
  The run page API returns it as `narration`.
- Prompt: add to the run prompt (`internal/launcher/prompt.go`) a rule that
  before each browser action the agent writes one short sentence, in the
  persona's own voice, first person, saying what they try and why. The
  owner's words: "like speaking aloud".
- Replay runs have no model: no thoughts; their Thoughts tab shows the
  journey's steps from the trace instead.
- Caption: over the bottom of the player, the latest thought at or before the
  playhead, shown while it is less than ~6 s old; respects reduced motion
  (no slide animation). Live runs show the newest thought.
- All text renders as text (htm escapes it). It is untrusted: it can quote
  the site.

## Page view and Network (Playwright trace)

Every agent run uploads `trace.zip` at the end (prompt step 5); replays upload
it too (`worker/bp-replay.mjs`). Live runs have no trace yet: the Page toggle
and the Network tab say the trace arrives when the run ends.

- Vendor Playwright's trace viewer (`playwright-core/lib/vite/traceViewer`,
  about 1.6 MB, Apache-2.0) into the board, served under `/trace/`. Its
  version must be the one the worker uses: the `playwright-core` bundled by
  the `@playwright/mcp` pin in `worker/Dockerfile` (bp-replay loads that same
  copy through `BP_PW_ROOT`; the base image's Playwright is not it): a viewer older than the
  trace format cannot read it. A script updates the vendored copy; a test
  fails when the vendored version and the worker's pin differ.
- Two readers, so the panels survive when the service worker cannot start
  (cross-origin cloud frame, partitioned worker, a Playwright route change).
  My call.
  - **Go reader (director):** reads the `.trace` and `.network` entries of
    the run's `trace.zip` and returns a trace summary: actions (title, page
    URL, wall-clock start and end, snapshot names), network resources, page
    URLs. Served same-origin with the run page data, computed once and kept
    as an artifact (hidden from Files). Network, the URL line and replay
    steps use only this.
  - **Service worker (browser):** used for snapshots only. Page view loads
    the run's `trace.zip` through the vendored viewer's service worker and
    shows the snapshot nearest the playhead in an iframe.
  Trace times map to the run's wall clock through the context's wall time;
  the video is already on that clock (`run.firstFrame` is the pod's own
  frame time, the same clock as the transcript).
- An **Open full trace** link opens the complete viewer in a new tab.
- Network tab: the Go trace summary's resources — method, URL (query
  dropped, as the console does), status, type, duration, at time; failed and
  4xx/5xx marked; a row opens its headers and timings inline; rows seek.
- The URL line comes from the Go trace summary.
- The board's CSP and frame rules must allow this only under `/trace/` (the
  service worker, its snapshot frames: today every response sends
  `X-Frame-Options: DENY` and `frame-ancestors 'none'`, which blocks even a
  same-origin snapshot iframe). Today's rules for every other path
  stay as they are.

## Lead runs

Lead runs (`LEAD_STORY`) have no browser: no video, trace, console or
network. They get the studio without a player: top bar, then the tabbed
panel filling the page with **Thoughts** (narration applies: leads run
Claude Code too) and **Info**. My call.

## Info tab

Everything today's right column and lower cards show that is not Bugs or
Console: Outcome, Details, Files (with the trace hint replaced by the Open
full trace link), Replay, Chain, Persona, Blocked hosts.

## Constraints

- Commits as `Tiny Systems <dev@tinysystems.io>`, no trailers. No Go
  `replace`. Never commit `.idea/`.
- GB English in all copy.
- Board CSP blocks inline scripts; keep it that way outside `/trace/`.
- The board can be framed by the cloud (`frame-ancestors`). A service worker
  inside a cross-origin frame may be partitioned or refused by the browser;
  when Page view cannot start, it says so and offers Open full trace.
- Worker changes are proven by the CI worker smoke (Docker on the owner's Mac
  hangs); board changes by Go tests and screenshots of a real run.

## Left to the builder

- How exactly to drive the service worker for snapshots in the pinned
  version, and whether the agent's `traces/` folder zipped with its
  folder prefix loads in the viewer (if not, the worker or prompt zips it so
  it does).
- Activity shading source (action density from narration, else trace).
- Visual details inside the board's system.

## Risks

- The viewer's service worker routes are internal to Playwright and can
  change between versions; the version pin plus a smoke check that loads a
  real trace and gets a snapshot is the guard.
- Agent runs that crash before step 5 have no trace: Page and Network say so.
