# Run Studio Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the run page into a full-window session replay studio: fixed player, captions of the model's thoughts, a Video | Page toggle backed by the Playwright trace, and tabbed scrolling panels.

**Architecture:** The worker extracts thoughts and actions from Claude Code's transcript and PUTs them to a new director endpoint, stored as artifact `narration.json`. The director summarises `trace.zip` in Go (actions, network, page URLs) behind a cached endpoint. The board vendors Playwright's trace viewer under `/trace/` for snapshots only. The run page is rebuilt as a studio layout in new UI modules.

**Tech Stack:** Go 1.26 (director, board), Node ESM (worker), preact + htm without a bundler (board UI), Playwright trace viewer (vendored, pinned to the worker's `@playwright/mcp` bundled `playwright-core`).

**Spec:** `docs/superpowers/specs/2026-11-16-run-studio-design.md`

## Global Constraints

- Commit as `git -c user.name="Tiny Systems" -c user.email="dev@tinysystems.io"`, no Co-Authored-By / Generated trailers. Never commit `.idea/`. No Go `replace`.
- GB English in all user-facing copy.
- Server, site and model text render only as text through htm (never `innerHTML`, never `dangerouslySetInnerHTML`).
- Board CSP unchanged for every path outside `/trace/`.
- Narration: text cut to 500 characters, at most 2000 entries per run; kinds `thought` and `action` only.
- Action summaries cut to 200 characters; Bash actions show the tool name only; any text containing the run's director URLs is dropped.
- Caption shows the latest thought at or before the playhead while it is less than 6 s old.
- Right panel 380–440px on desktop; under 900px wide the page stacks; 16px side gutter on phones; no page scroll on desktop.
- Tabs, in order: Thoughts (default), Bugs, Console, Network, Info. Lead runs: Thoughts, Info, no player.
- Worker changes are proven by the CI worker smoke (Docker on this Mac hangs); pure Node smoke scripts may also run locally with `node`.

## Review Focus

1. A run token leaking into narration through a Bash command, an upload URL quoted in assistant text, or a tool input — expect none reach `narration.json` (Task 1 test `token never in narration`).
2. A long run with more than 2000 transcript entries — expect the worker to keep the newest 2000 and the director to accept them (Task 1 test `keeps newest 2000`, Task 2 test `TestNarrationBounds`).
3. A missing, truncated or non-trace `trace.zip`, or one zipped with a `traces/` folder prefix — expect a clear 404/422 for the first two and a correct summary for the prefixed one (Task 4 tests).
4. Thoughts stamped before the video's first frame or after its end — expect them clamped onto the time bar, never off it or NaN (Task 7 test `placeOnVideo clamps`).
5. The board framed by the cloud, where the browser refuses the trace service worker — expect Page view to say so and offer Open full trace while Network and the URL line still work (Task 8 step: message path checked by forcing registration failure).

---

### Task 1: Worker narration extractor and reporter

**Files:**
- Create: `worker/bp-narration.mjs`
- Modify: `worker/bp-usage.mjs` (read lines once, feed both usage and narration; PUT narration to `--narration-url`)
- Create: `worker/smoke/narration-smoke.mjs`
- Modify: `worker/Dockerfile` (copy the new module next to bp-usage), `.github/workflows/ci.yml` (run the smoke in the worker-smoke job)

**Interfaces:**
- Produces: `export function narrationOf(line: string, secrets: string[]): Entry[]` where `Entry = {at: string /* RFC3339 */, kind: 'thought'|'action', text: string}`; `export function capEntries(list: Entry[], max = 2000): Entry[]` keeping the newest. bp-usage sends the full capped list as JSON to `PUT <narration-url>` when it changed.
- `--narration-url=<director>/narration/<token>` arrives on the bp-browser command line (Task 2 adds it). `secrets` = every `--*-url=` value bp-browser received (they all carry the token).

- [ ] **Step 1: Write the smoke** `worker/smoke/narration-smoke.mjs`, plain `node:assert`, exits non-zero on failure. Cases:
  - assistant line with text block `"I'll try the postcode field"` and `timestamp` → one `thought` with that text and `at` = the timestamp.
  - assistant line with `tool_use` `{name:'mcp__pw__browser_click', input:{element:'Pay button', ref:'e12'}}` → one `action` with text `browser_click: Pay button`.
  - `tool_use` Bash with `input.command` containing `http://bp-director.x.svc:8080/artifacts/TOKEN123/` → `action` text exactly `Bash`.
  - text block quoting `http://bp-director.x.svc:8080/files/TOKEN123/` with that URL in `secrets` → `token never in narration`: no entry text contains `TOKEN123`.
  - text of 900 characters → cut to 500.
  - user lines, tool_result lines, malformed JSON → no entries.
  - `keeps newest 2000`: `capEntries` of 2500 entries returns the last 2000 in order.
- [ ] **Step 2: Run it** — `node worker/smoke/narration-smoke.mjs`. Expected: fails on the missing module import.
- [ ] **Step 3: Implement** `worker/bp-narration.mjs` with the two exports. Tool names: strip the `mcp__<server>__` prefix. Summary: first of `input.element`, `input.url`, `input.text`, `input.key`, else empty; `name: summary` cut to 200. Thought entries with only whitespace are dropped. Drop any entry whose text contains a secret after cutting.
- [ ] **Step 4: Wire into bp-usage.mjs**: same read loop calls `count(line)` and pushes `narrationOf(line, secrets)`; dedupe by transcript line `uuid` + block index. Exit on 404 already covers both reports. Skip the narration PUT when the flag is absent.
- [ ] **Step 5: Run** `node worker/smoke/narration-smoke.mjs` — Expected: `narration smoke ok`. Add the CI step `docker run --rm --user 1000 bp-worker:smoke node /opt/bp/smoke/narration-smoke.mjs`.
- [ ] **Step 6: Commit** `worker: narrate the agent's thoughts and actions from its transcript`.

### Task 2: Director narration endpoint and storage

**Files:**
- Create: `internal/runs/narration.go`, `internal/runs/narration_test.go`
- Modify: `internal/api/api.go` (worker route `PUT /narration/{token}`, `getRun` adds `narration`), `internal/api/narration_test.go` (create)
- Modify: `internal/launcher/tiny.go` `browserArgs` (add `--narration-url=`+DirectorURL+`/narration/`+token), its test

**Interfaces:**
- Produces: `const NarrationName = "narration.json"`; `type NarrationEntry struct { At time.Time \`json:"at"\`; Kind string \`json:"kind"\`; Text string \`json:"text"\` }`; `func (s *Service) SaveNarration(token string, n []NarrationEntry, now time.Time) error` (replaces the stored list; same active-or-within-`usageGrace` rule as `SaveUsage`; replays → `ErrNotActive`); `func (s *Service) Narration(runID string) ([]NarrationEntry, error)` (nil when absent); `var ErrBadNarration`.
- `getRun` JSON gains `"narration": [...]` when present.

- [ ] **Step 1: Tests first** — `TestNarrationBounds`: 2000 entries accepted; 2001 → `ErrBadNarration`; text of 501 runes → bad; kind `"shout"` → bad; zero `At` → bad. `TestNarrationAfterGrace`: run finished 3 min ago → `ErrNotActive`. API: unknown field → 400; good body → 204 and `GET /api/runs/{id}` returns it; wrong token → 404; body over 2 MiB → 400. Launcher: `browserArgs` contains `--narration-url=http://d/narration/<token>`.
- [ ] **Step 2: Run** `go test ./internal/runs ./internal/api ./internal/launcher` — Expected: compile failures on the missing names.
- [ ] **Step 3: Implement**; map `ErrBadNarration` to 400 in `fail`. Body limit 2 MiB, `DisallowUnknownFields`.
- [ ] **Step 4: Run** `go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `director: keep the agent's narration with the run`.

### Task 3: Prompt — narrate, and zip the trace without a folder prefix

**Files:** Modify `internal/launcher/prompt.go`, its test.

- [ ] **Step 1: Test** `TestRunPromptNarrates`: run prompt contains `before each browser action, write one short sentence in your persona's own voice` and `cd /workspace/out/traces && zip -r ../trace.zip .`.
- [ ] **Step 2: Run** `go test ./internal/launcher` — Expected: FAIL.
- [ ] **Step 3: Edit step 3** of `runPrompt` to add: "Think aloud: before each browser action, write one short sentence in your persona's own voice, first person, saying what you try and why. It is shown to the owner beside the recording." Edit step 5's zip example to the prefix-free command.
- [ ] **Step 4: Run** — Expected: PASS. **Step 5: Commit** `launcher: personas think aloud; trace zips load in the viewer`.

### Task 4: Go trace summary

**Files:**
- Create: `internal/tracesum/tracesum.go`, `internal/tracesum/tracesum_test.go`, `internal/tracesum/testdata/trace.zip`, `internal/tracesum/testdata/make-trace.mjs` (how the fixture was made)
- Modify: `internal/api/api.go` (`GET /api/runs/{id}/trace`), `internal/api/trace_test.go` (create)

**Interfaces:**
- Produces:
  ```go
  type Summary struct {
      Actions []Action   `json:"actions"`
      Network []Resource `json:"network"`
  }
  type Action struct {
      Title string    `json:"title"`
      PageURL string  `json:"pageUrl,omitempty"`
      Start time.Time `json:"start"`
      End time.Time   `json:"end"`
      Error string    `json:"error,omitempty"`
      Snapshot string `json:"snapshot,omitempty"` // the "after" snapshot name, else "before"
      PageID string   `json:"pageId,omitempty"`
  }
  type Resource struct {
      Method, URL, Type string // URL without query or fragment
      Status int
      Start time.Time
      Duration time.Duration
      Failed bool
      RequestHeaders, ResponseHeaders map[string]string
  } // json tags lower camel
  func Read(r io.ReaderAt, size int64) (Summary, error)
  var ErrNotTrace = errors.New("not a Playwright trace")
  const SummaryName = "trace-summary.json"
  ```
- Monotonic trace times map to wall clock through the `context-options` event's `wallTime` and `monotonicTime`.
- Endpoint: 404 when no `trace.zip`; 422 with `ErrNotTrace` text for a bad zip; otherwise the summary, computed once and kept as artifact `trace-summary.json` (recomputed if `trace.zip` is newer). Entries named `*.trace` / `*.network` at any folder depth are read (a `traces/` prefix works); `resources/` is never read. Per-entry uncompressed read cap 64 MiB; header values cut to 1 KiB; at most 5000 resources.

- [ ] **Step 1: Make the fixture** with local Chrome and the npx-cached `playwright-core` (same major as the worker): a `data:` page that contains an iframe with a button, one `fetch` that 404s and one that succeeds, a click, a navigation; tracing with `screenshots: true, snapshots: true`; zip under 200 KB. Keep `make-trace.mjs` in testdata.
- [ ] **Step 2: Tests** — fixture → ≥3 actions, ordered by start, each `Start` within the fixture's wall time; one resource `Status 404` with `Failed` false and one `Status 200`; query strings absent from URLs; a copy re-zipped under `traces/` gives the same summary; truncated zip → error; zip with no `.trace` → `ErrNotTrace`. API: no artifact → 404; bad zip → 422; good → 200 and a second call reads the cached artifact.
- [ ] **Step 3: Run** `go test ./internal/tracesum ./internal/api` — Expected: FAIL (missing package).
- [ ] **Step 4: Implement** with `archive/zip` and a line scanner (1 MiB max line).
- [ ] **Step 5: Run** `go test -race ./...` — Expected: PASS. **Step 6: Commit** `director: summarise a run's trace for the board`.

### Task 5: Vendored trace viewer and `/trace/` headers

**Files:**
- Create: `scripts/vendor-traceviewer.sh`, `internal/board/ui/trace/` (vendored files + `VERSION` with two lines: `@playwright/mcp <v>` and `playwright-core <v>`), `internal/board/ui/trace/LICENSE` (Apache-2.0 from the package)
- Modify: `internal/board/frame.go` (add `TraceHeaders`), `cmd/director/main.go` (use it for paths under `/trace/`), `internal/board/proxy.go` (`ModifyResponse`: paths under `/trace/` get `TraceHeaders`, not `apiHeaders`), `internal/board/frame_test.go`, `internal/board/server_test.go`

**Interfaces:**
- Produces: `func TraceHeaders(h http.Header, frameAncestors string)` — `SecurityHeaders` then CSP `default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; style-src 'self' 'unsafe-inline'; font-src 'self' data:; worker-src 'self'; connect-src 'self'; frame-ancestors 'self' <frameAncestors without 'none'>`, and no `X-Frame-Options`. Plus `Service-Worker-Allowed: /trace/` on `sw.bundle.js`.

- [ ] **Step 1: Script** — `scripts/vendor-traceviewer.sh <mcp-version>`: reads the exact `playwright-core` dependency of `@playwright/mcp@<v>` with `npm view`, `npm pack`s it into a temp dir, copies `package/lib/vite/traceViewer/*` and `LICENSE` into `internal/board/ui/trace/`, writes `VERSION`. Run it with the version from `worker/Dockerfile` `ARG PLAYWRIGHT_MCP_VERSION`.
- [ ] **Step 2: Tests** — `TestTraceViewerMatchesWorker`: `VERSION` line 1 equals `@playwright/mcp ` + the Dockerfile ARG. `TestTraceHeaders`: CSP has `worker-src 'self'`, `frame-ancestors 'self'` (and the cloud origin when given), no `X-Frame-Options`; the board UI path `/` keeps today's CSP and `DENY`. Proxy test: a director response for `/trace/sw.bundle.js` keeps the trace CSP; `/` and `/api/runs` still get `SecurityHeaders`.
- [ ] **Step 3: Run** `go test ./internal/board ./cmd/director` — Expected: FAIL.
- [ ] **Step 4: Implement**. **Step 5: Run** `go test -race ./...` — PASS. **Step 6: Commit** `board: host Playwright's trace viewer under /trace/`.

### Task 6: Dev director sample data

**Files:** Modify `internal/devdirector/devdirector.go` (embed the Task 4 fixture and a sample narration; put `trace.zip` and `narration.json` on finished sample runs, narration growing while running), its test.

- [ ] **Step 1: Test** — a finished dev run lists `trace.zip`, and `GET /api/runs/{id}` returns ≥5 narration entries spanning the sample video's length.
- [ ] **Step 2–4:** run (FAIL), implement, run `go test -race ./internal/devdirector` (PASS). **Step 5: Commit** `devdirector: sample runs carry narration and a trace`.

### Task 7: Studio layout

**Files:**
- Create: `internal/board/ui/studio.js` (`Studio` layout: `TopBar`, player slot, `Tabs`, panel slot; lead variant), `internal/board/ui/thoughts.js` (`ThoughtsPanel`, `Caption`), `internal/board/ui/network.js` (`NetworkPanel`), `internal/board/ui/info.js` (Outcome, Details, Files, Replay, Chain, Persona, Blocked hosts moved out of `app.js`), `internal/board/ui/studio-time.js` (pure, no imports), `internal/board/uitest/studio-time.test.mjs`
- Modify: `internal/board/ui/app.js` (`RunPage` renders `Studio`; `App` hides the `<aside>` on the run route), `internal/board/ui/timeline.js` (thought marks, activity shading from narration actions), `internal/board/ui/styles.css`, `.github/workflows/ci.yml` (`node --test internal/board/uitest/` in the test job)

**Interfaces:**
- Produces in `studio-time.js`: `placeOnVideo(atMs, videoStartMs, durationS) -> seconds in [0, durationS]`; `captionAt(narration, playheadMs, windowMs = 6000) -> entry | null` (latest `thought` with `at <= playhead` and `playhead - at < windowMs`); `nearest(list, ms, key) -> index` (last item with `key(item) <= ms`, else 0).
- `Studio({ run, data, player, tabs })` where `tabs = [{ id, label, count?, body }]`; tab state kept in the URL param `tab`.
- Consumes: `data.narration` (Task 2), `GET /api/runs/{id}/trace` (Task 4) fetched once when the run has `trace.zip`.

- [ ] **Step 1: Tests** in `studio-time.test.mjs` (`node:test`): `placeOnVideo clamps` (before start → 0, after end → duration, NaN inputs → 0); `captionAt` returns the latest thought within 6 s, ignores actions, returns null at 6 s; `nearest` on empty list → 0.
- [ ] **Step 2: Run** `node --test internal/board/uitest/` — Expected: FAIL (module missing). **Step 3: Implement `studio-time.js`**; run — PASS.
- [ ] **Step 4: Build the layout** per spec Layout. Every row with a time seeks the player (`toVideo` / frames as today). Existing timeline, video, LiveView, FrameView logic moves unchanged. Network rows: method, path, status, type, duration; 4xx/5xx and failed marked; row expands headers inline. Restart and lost-contact banners sit under the top bar. Replay runs' Thoughts tab lists the trace summary's actions (the journey steps) instead of narration. Live runs caption the newest thought. Files hides `narration.json` and `trace-summary.json` (like `usage.json`) and swaps the `show-trace` hint for the Open full trace link. Tabs are a `role="tablist"` with arrow keys; space plays/pauses when focus is not in an input. The player area is dark in both themes.
- [ ] **Step 5: Check** with `go run ./cmd/boardingparty board --dev-director --no-open`, start a run, screenshot a running and a finished run at 1440×900 and 390×844 (`$S/shot.mjs`). Expected: no page scrollbar on desktop; panel scrolls on its own; caption visible over the video while playing; phone stacks with 16px gutters; lead run page shows Thoughts + Info only. Fix and re-shoot until it does.
- [ ] **Step 6: Run** `go test -race ./... && node --test internal/board/uitest/` — PASS. **Step 7: Commit** `board: the run page becomes a session studio`.

### Task 8: Page view

**Files:** Create `internal/board/ui/pageview.js`; modify `studio.js` (Video | Page toggle, URL line), `styles.css`.

**Interfaces:**
- `PageView({ runId, summary, playheadMs })`: registers `/trace/sw.bundle.js` with scope `/trace/`, then shows an iframe on the vendored viewer's snapshot URL for `summary.actions[nearest(...)]` (`snapshot`, `pageId`, `trace` = `/api/runs/<id>/artifacts/trace.zip`). The exact snapshot URL form is read from the vendored `sw.bundle.js`.
- On registration failure or a snapshot that will not load within 10 s: "This browser would not open the trace here." plus the Open full trace link (`/trace/index.html?trace=<artifact URL>`, new tab). The toggle is disabled with "The trace arrives when the run ends." while live or without `trace.zip`.

- [ ] **Step 1: Build** it. **Step 2: Check** on the dev board: Page shows the fixture's iframe content sharp; moving the playhead changes the snapshot; Open full trace opens the viewer with the trace loaded. Force failure (temporarily register a bad path in devtools) and see the message. Screenshot both.
- [ ] **Step 3: Run** `go test -race ./... && node --test internal/board/uitest/` — PASS. **Step 4: Commit** `board: Page view shows the trace snapshot at the playhead`.

### Task 9: Ship

- [ ] Push `main`; wait for CI green (all jobs, including the new narration smoke and `node --test`).
- [ ] Check no bp-run or session pods are active, then deploy with `deploy.sh <sha7>`; open boardingparty-test's board and check a finished run's studio, Page view and Network.
- [ ] Ask the owner before starting a paid run to see live narration.
