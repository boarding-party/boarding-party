# boardingparty Plan 3b: live screen and persona profiles

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** While a run is active, the board shows the agent's browser screen live. Personas get a structured profile (device, age, tech level, patience, reading, assistive tech) chosen from dropdowns, and the device is set when the browser starts, not by the agent.

**Architecture:** Playwright MCP keeps launching Chromium itself, with one extra launch arg that opens a CDP port on pod loopback. A small Node helper in the worker image (`bp-screen`) attaches to the active page over CDP, runs `Page.startScreencast`, and PUTs JPEG frames to the director's worker listener with the run token, like artifact uploads. The director keeps only the latest frame per run in memory and serves it to the board as an MJPEG stream (`multipart/x-mixed-replace`), which an `<img>` plays natively. The profile catalogue lives in Go (`internal/project/profile.go`); the board reads it from the admin API, so dropdowns and validation share one source.

**Tech Stack:** Go 1.26, Node 26 (in the worker image; global `WebSocket` and `fetch`), Playwright MCP 0.0.84, Preact + htm (vendored).

**Spec:** `docs/superpowers/specs/2026-10-29-boardingparty-design.md` §4.2 ("Behaviour traits (impatient, typos, screen reader, phone viewport) are applied by the worker: viewport/device emulation, typing style instructions, accessibility-tree navigation for screen-reader personas").

## Global Constraints

- Module `github.com/boarding-party/boardingparty`. Commits: author Tiny Systems <dev@tinysystems.io>, conventional subjects, no Co-Authored-By or "Generated with" trailers. Never commit `.idea/`.
- The admin API stays loopback-only behind the bearer token; workers reach only the `:8080` listener. Live frames come in on `:8080` (run token in the path) and go out only on the admin API.
- The director stores no frames on disk. Memory: at most one frame per run, frames ≤ 512 KiB, entries dropped 2 minutes after their last frame.
- The CDP port binds 127.0.0.1 inside the pod only; nothing exposes it.
- The agent's token use must not grow: the agent never sees or handles frames.
- UI: GB English, sentence case, compact layout, server text rendered as text only. Every dropdown has a "Not set" first option.
- Persona profile values are stable keys (lower-case, hyphenated); labels are display-only and may change.

## Review Focus

1. **The page the agent uses changes** (new tab, popup, navigation to another origin): `bp-screen` follows the newest page target within 2 s. Test in Task 1 smoke (open a second page, frames continue).
2. **Nobody is watching:** the worker drops to one frame every 5 s. Test in Task 2 (`X-Bp-Watchers: 0` with no subscribers, `1` with one).
3. **The board proxy buffers the stream:** the first frame reaches the browser before the upstream finishes. Test in Task 3.
4. **The run ends while someone watches:** the stream closes within 5 s and the UI swaps to the run detail with video. Test in Task 2 (stream ends when the run is no longer active).
5. **An old project with free-text traits only** (no `profile`): loads, saves, runs as before; device defaults to desktop 1280×800. Test in Task 4.

---

### Task 1: Worker screen helper and launch wrapper

**Files:** Create `worker/bp-browser` (sh), `worker/bp-screen.mjs`, `worker/pw-config.json`, `worker/smoke/screen-smoke.mjs`; modify `worker/Dockerfile`, `worker/README.md`.

**Interfaces — Produces:**
- MCP stdio command `bp-browser` taking `--live-url=<url>`, optional `--bp-device=<value>`, plus any Playwright MCP args. It starts `bp-screen` once per pod (pidfile `/tmp/bp-screen.pid`; skip if that pid is alive), strips both, maps `--bp-device` through a `case` table to `--device=<Playwright name>` (`iphone-15`→`iPhone 15`, `iphone-15-pro-max`→`iPhone 15 Pro Max`, `pixel-7`→`Pixel 7`, `galaxy-s24`→`Galaxy S24`, `ipad`→`iPad (gen 11)`, `ipad-mini`→`iPad Mini`; unknown values are ignored with a log line), and `exec`s `playwright-mcp --config /opt/bp/pw-config.json <args>`, quoting the device name.
- `pw-config.json`: `{"browser":{"launchOptions":{"args":["--remote-debugging-port=9222","--remote-debugging-address=127.0.0.1"]}}}`.
- `bp-screen.mjs --live-url=<url>`: polls `http://127.0.0.1:9222/json/list` every 2 s; attaches to the most recently listed `type: "page"` target over its `webSocketDebuggerUrl`; sends `Page.startScreencast {format:"jpeg", quality:60, maxWidth:1280, maxHeight:1280}`; acks every `Page.screencastFrame`; PUTs the decoded JPEG to `<live-url>` with `Content-Type: image/jpeg`, at most one PUT in flight, at most every 250 ms while the last response had `X-Bp-Watchers` > 0, else every 5 s. Errors (CDP not up yet, director 4xx/5xx, network) are logged and retried; it never exits on its own. On a 404 or 410 from the director (run over) it stops sending until the pod ends.

- [ ] **Step 1: Verify the CDP port coexists with Playwright's pipe.** Build the image locally (`docker build -t bp-worker:dev worker`). Write `worker/smoke/screen-smoke.mjs`: launch Chromium through the image's Playwright with the `pw-config.json` args and `devices['iPhone 15']`, open `data:text/html,<h1>one</h1>`, start `bp-screen.mjs` pointed at a local HTTP sink in the same script, open a second page with `<h1>two</h1>`, wait 4 s. Assert: ≥ 2 frames received, every body starts with `FF D8`, a frame arrived after the second page opened. Run: `docker run --rm -v $PWD/worker:/opt/bp bp-worker:dev node /opt/bp/smoke/screen-smoke.mjs`. Expected: `ok N frames`. If Playwright rejects the extra args, record the failure in the ledger and switch to `--cdp-endpoint` (launch Chromium in `bp-browser`), then repeat.
- [ ] **Step 2: In the same smoke, record a video with the iPhone 15 context** and assert its frame size is 390×844-proportional (read the first bytes of the `.webm` with `ffprobe` from `/ms-playwright/ffmpeg-*` if present; otherwise assert the context `viewport` equals 390×844). This replaces the grey-border problem with a checked fact.
- [ ] **Step 3: Implement `bp-browser`, `bp-screen.mjs`, copy them to `/opt/bp` and `/usr/local/bin/bp-browser` in the Dockerfile.** No npm dependencies.
- [ ] **Step 4: Re-run the smoke against the built image; it passes.** Add the smoke to CI as a step after the worker image build (`docker run … node /opt/bp/smoke/screen-smoke.mjs`), single-arch only.
- [ ] **Step 5: Commit** `feat(worker): stream the browser screen to the director`.

### Task 2: Live frames in the director

**Files:** Create `internal/live/hub.go`, `internal/live/hub_test.go`; modify `internal/api/api.go`, `internal/api/api_test.go`, `cmd/director/main.go`.

**Interfaces:**
- Produces `live.Hub`: `func NewHub() *Hub`; `func (h *Hub) Publish(runID string, frame []byte) (watchers int)`; `func (h *Hub) Subscribe(runID string) (frames <-chan []byte, cancel func())` — the channel has capacity 1, receives the latest frame immediately if one exists, and a slow reader only ever gets the newest frame; `func (h *Hub) Sweep(now time.Time)` drops runs with no frame for 2 minutes.
- Worker route `PUT /live/{token}`: `svc.Active(token)` (as `putArtifact`); body ≤ 512 KiB; must start with `FF D8` else 400; publishes; responds 204 with header `X-Bp-Watchers: <n>`. Inactive token: same status `putArtifact` returns for it.
- Admin route `GET /api/runs/{id}/live`: unknown run 404; run not active 410 `{"error":"run finished"}`; else 200 `Content-Type: multipart/x-mixed-replace; boundary=frame`, each part `--frame\r\nContent-Type: image/jpeg\r\nContent-Length: N\r\n\r\n<jpeg>\r\n`, flushed per part. Ends when the client goes, the run stops being active (checked every 5 s), or after 15 minutes.
- `api.Admin` and `api.Worker` take the hub: change both signatures to add `hub *live.Hub` as the last parameter; update `cmd/director/main.go` and `internal/devdirector`.

- [ ] **Step 1: Hub tests:** `TestSubscribeGetsLatestImmediately`, `TestSlowSubscriberGetsNewestOnly` (publish 3, read 1, it is the third), `TestPublishCountsWatchers` (0 then 1 after Subscribe, 0 after cancel), `TestSweepDropsIdleRuns`.
- [ ] **Step 2: API tests:** PUT with non-JPEG body → 400; > 512 KiB → 413; inactive token → same status as artifacts; PUT returns `X-Bp-Watchers: 0` then `1` while a GET stream is open; GET stream receives a part with the published bytes; GET for a finished run → 410; GET stream ends after the run is finished (use a short check interval injected through an unexported field on `server` in tests).
- [ ] **Step 3: Implement; run** `go test ./internal/live/ ./internal/api/ ./cmd/director/`. Expected: PASS.
- [ ] **Step 4: Commit** `feat(director): live frames from workers, streamed on the admin API`.

### Task 3: Board live view

**Files:** Modify `internal/board/server_test.go` (and `proxy.go` if the test fails), `internal/board/ui/app.js`, `internal/board/ui/styles.css`, `internal/devdirector/devdirector.go`.

- [ ] **Step 1: Test** `TestProxyStreamsWithoutBuffering`: fake upstream writes one multipart part, flushes, then blocks until the test reads it; the client must read the first part within 1 s. If it fails, set `FlushInterval: -1` on the reverse proxy.
- [ ] **Step 2: UI:** on run detail, when the run state is active, show `<img src="/api/runs/<id>/live">` in the video slot with a "Live" tag and a "Waiting for the first frame…" placeholder until `onLoad`; on `onError` or when polling sees the run finish, swap to the recorded video. Runs list: active runs get a "Watch" link to the detail page. Width fits the column; a phone screen is centred, never stretched.
- [ ] **Step 3: devdirector:** while a fake run is active, publish a generated JPEG (Go `image/jpeg` of a solid colour with the elapsed seconds drawn as blocks is enough) every 500 ms to the hub, so `board --dev-director` shows a moving live view.
- [ ] **Step 4: Manual check** with `go run ./cmd/boardingparty board --dev-director`: start a run, the live view moves, it switches to the video when the run ends. Screenshot in the report.
- [ ] **Step 5: Commit** `feat(ui): watch a run live`.

### Task 4: Persona profiles

**Files:** Create `internal/project/profile.go`, `internal/project/profile_test.go`; modify `internal/project/project.go`, `internal/api/api.go` (+test), `internal/mcpserver/server.go` (+test), `internal/launcher/prompt.go`.

**Interfaces — Produces:**
```go
type Profile struct {
    Device    string `json:"device,omitempty"`
    Age       string `json:"age,omitempty"`
    TechLevel string `json:"techLevel,omitempty"`
    Patience  string `json:"patience,omitempty"`
    Reading   string `json:"reading,omitempty"`
    Assistive string `json:"assistive,omitempty"`
}
type Option struct{ Value, Label, Guide string } // Guide: one sentence the agent reads
type Field  struct{ Key, Label, Hint string; Options []Option }
var ProfileFields []Field // order = form order
func (p Profile) Guidance() []string // one Guide per set field, in field order
func DeviceLaunch(device string) (playwrightDevice, viewport string) // exactly one non-empty
```
`Persona` gains `Profile Profile `json:"profile,omitempty"``. Validation: each set value must be one of its field's options (`persona "X": unknown device "y"`).

Catalogue (value — label):
- device (default `desktop`): `desktop` — Desktop 1280×800 (viewport `1280x800`); `desktop-large` — Large desktop 1920×1080 (`1920x1080`); `laptop-small` — Small laptop 1366×768 (`1366x768`); `iphone-15` — iPhone 15; `iphone-15-pro-max` — iPhone 15 Pro Max; `pixel-7` — Android phone (Pixel 7); `galaxy-s24` — Samsung Galaxy S24; `ipad` — iPad (`iPad (gen 11)`); `ipad-mini` — iPad Mini. Phone and tablet values map to the Playwright device of the same name.
- age: `18-24`, `25-34`, `35-49`, `50-64`, `65-plus` (label "65 or over").
- techLevel: `new` — New to the internet; `everyday` — Everyday user; `confident` — Confident; `expert` — Expert.
- patience: `patient` — Patient; `normal` — Normal; `impatient` — Gives up quickly.
- reading: `careful` — Reads everything; `skims` — Skims; `struggles` — Finds long text hard; `second-language` — English is a second language.
- assistive: `none` — None; `screen-reader` — Screen reader; `keyboard-only` — Keyboard only; `zoom` — Large text / zoom.

Each option's `Guide` is one sentence telling the agent how to behave (for example `screen-reader`: "Navigate only by the accessibility snapshot; never rely on how the page looks, and report anything a screen reader would miss."; `impatient`: "If something takes more than a few seconds or two tries, give up and finish the run as stuck, saying why."). The device options' Guide states the device ("You are on an iPhone 15.").

- [ ] **Step 1: Tests:** every option value matches `^[a-z0-9-]+$` and is unique within its field; every device value has exactly one of device/viewport from `DeviceLaunch`, and every Playwright device name is in this list (copied from Playwright 1.65 `devices`): `iPhone 15`, `iPhone 15 Pro Max`, `Pixel 7`, `Galaxy S24`, `iPad (gen 11)`, `iPad Mini`; `DeviceLaunch("")` returns viewport `1280x800`; unknown value rejected by `LoadJSON` with the message above; a project with only `traits` and no `profile` loads and round-trips unchanged (Review Focus 5); `Guidance()` order follows `ProfileFields`.
- [ ] **Step 2: API:** `GET /api/profile-fields` returns `ProfileFields` as JSON (`key`, `label`, `hint`, `options[{value,label}]`; Guide not sent). Add `profile` to the round-trip fixture.
- [ ] **Step 3: MCP:** `next_assignment` persona gains `profile` (the labels, keyed by field) and `guidance` (`Profile.Guidance()`); traits stay.
- [ ] **Step 4: Prompt:** replace "Behave according to the persona's traits." with "Behave as the persona's profile and guidance describe, and follow their traits. The browser is already set to the persona's device: never resize it." Update `TestPromptMentionsFinishAndNoPolling` neighbours if they assert on that sentence.
- [ ] **Step 5: Run** `go test ./...`. Expected: PASS. **Commit** `feat: structured persona profiles`.

### Task 5: Launch the browser as the persona's device, with live frames

**Files:** Modify `internal/launcher/tiny.go`, `internal/launcher/tiny_test.go`, `cmd/director/main.go` (pass nothing new; the live URL derives from `DirectorURL`).

- [ ] **Step 1: Test** `TestStartLaunchesPersonaDevice`. MCP args may not contain spaces (tiny's CRD pattern `^[^\s"'\\]+$`), so Playwright device names can't be passed directly. Persona with `device: iphone-15` → the `pw` MCP server is `Command: "bp-browser"` with arg `--bp-device=iphone-15`; `bp-browser` maps it (Task 1). Add `TestWorkerDeviceTableMatches`, which reads `../../worker/bp-browser` and checks every phone/tablet value of `DeviceLaunch` appears with its Playwright name. Desktop values and a persona without profile → `--viewport-size=<WxH>` from `DeviceLaunch`. Args always include `--live-url=<DirectorURL>/live/<token>`, and keep `--headless --isolated --no-sandbox --browser=chromium --output-dir=/workspace/out --caps=devtools`.
- [ ] **Step 2: Implement** (persona looked up from the project by `r.Role`/`r.Persona`, as `next_assignment` does). Run `go test ./internal/launcher/`. PASS.
- [ ] **Step 3: Commit** `feat(launcher): start the browser as the persona's device and stream it`.

### Task 6: Profile editor, docs, cluster check

**Files:** Modify `internal/board/ui/project.js`, `styles.css`, `README.md`, `examples/todomvc.yaml`.

- [ ] **Step 1: Persona form:** a "Profile" grid of selects (two columns, one on narrow screens) built from `GET /api/profile-fields`, each with its label above and "Not set" first; field hint as the select's `title` and a one-line hint under the grid: "The agent behaves like this person. Device sets the browser's screen size when the run starts." Traits become "Other traits (optional)" with hint "Anything the lists don't cover, such as distracted or left-handed." Profile round-trips through save; unknown values from the server are kept and shown as "<value> (unknown)".
- [ ] **Step 2: Example and README:** `examples/todomvc.yaml` persona Ada gets `profile: {device: desktop, techLevel: everyday}`; README project section documents `profile` with the field list and says live view exists.
- [ ] **Step 3: Cluster check** (controller, not implementer): after CI publishes the images, upgrade with `boardingparty board`, run John Smith with `device: iphone-15`, `techLevel: new`; confirm the live view moves, the video is phone-sized without grey borders, and the node is `bp-runs`.
- [ ] **Step 4: Commit** `feat(ui): persona profile editor`.
