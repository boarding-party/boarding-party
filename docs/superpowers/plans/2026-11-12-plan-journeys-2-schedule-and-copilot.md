# Journeys 2: scheduled replays, the copilot and waits — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Scheduled runs become replays once a story has a live journey, a failed replay is checked once more and then handed to an AI copilot that decides bug or new revision, journeys can wait for email or time, and every journey action by a person or the director is a decision row.

**Architecture:** Replays go through the existing queue (queue items gain kind, journey, revision and seed), so scheduled replays, flake checks, continuations and board replays all start the same way. A new scheduler phase follows finished replays: same-seed retry, flaky count, copilot hand-off (an AI run with context kind `replay_failed`). The copilot answers through a new MCP tool `decide_journey`. Waits inside journeys stop `bp-replay` with a `waiting` result; the director parks the replay like an AI run's wait and the continuation is a replay of the same revision that skips to after that wait.

**Tech Stack:** Go (director), SQLite (modernc), Node + Playwright in the worker image, Preact/htm board (API only in this plan; pages are Plan 3).

**Spec:** `docs/superpowers/specs/2026-11-12-journeys-design.md`. Plan 1 (`docs/superpowers/plans/2026-11-12-plan-journeys-1-record-and-replay.md`) is done and deployed. This plan covers: Scheduling, the flake guard, The copilot, waits (`bp.waitFor`), the Audit trail for people's journey actions (pause, resume, roll back, delete, replay) with their API. Plan 3 covers generated data and edge cases, fix checks by replay, and the Journeys and Journey board pages.

## Global Constraints

- Commits: `git -c user.name="Tiny Systems" -c user.email="dev@tinysystems.io" commit`, no Co-Authored-By or Generated trailers. Never commit `.idea/`. No `replace` directives in go.mod.
- Push to main is approved. Don't bypass CI. Check `git show --stat HEAD` before every push.
- Cluster commands: prefix `CLOUDSDK_ACTIVE_CONFIG_NAME=tinyplatform CLOUDSDK_CORE_ACCOUNT=hello@tinysystems.io` and pass `--context gke_tinyplatform_europe-west2-a_cluster-1`. No direct kubectl writes; deploy with the scratchpad `deploy.sh <sha7>`.
- Tests: `go test -race ./...`; worker smoke tests run in CI (`worker/smoke/*.mjs`).
- UI and API copy: GB English; "journey", never "script". Replays are runs.
- Journey code is untrusted: rendered only as text; never holds a token (Plan 1's 64-hex check stays on every path that stores code).
- Every decision about a journey is a row with a reason. Actors: `model`, `director` (new, for the scheduler's own decisions), or the person (`X-BP-Actor`, else `local`).
- A replay never spends: it never wakes a lead, never counts toward a lead's runs, never hands off.
- Pausing a journey never falls back to AI runs. When unsure, the copilot reports a bug.

## Review Focus

1. A replay that keeps failing at the step of a bug the copilot reported, while that issue is open: no new copilot run, a `still failing at a known bug` row each time. (Task 4 `TestKnownBugDoesNotCallCopilotAgain`.)
2. A copilot run that fails, gets stuck or times out without `decide_journey`: the journey must not stay `repairing` for ever. (Task 5 `TestCopilotWithoutDecisionReturnsJourneyLive`.)
3. Day's budget spent: scheduled replays still queue and start; AI items wait without blocking replays queued behind them. (Task 1 `TestDrainStartsReplayPastOverBudgetItem`, Task 3 `TestFireReplaysWhenOverBudget`.)
4. A journey paused or deleted while its replay is queued: the item is dropped at start, no run. (Task 1 `TestQueuedReplayOfPausedJourneyIsDropped`, Task 2 `TestDeleteJourneyDropsQueuedReplays`.)
5. A journey that waits for an email which arrives while the replay is still running, before the wait is recorded: it still counts. (Task 6 `TestReplayEmailWaitCountsMailFromTheRun`.)

---

### Task 1: replays go through the queue

**Files:**
- Modify: `internal/store/queue.go`, `internal/store/store.go` (`addColumns`: queue `kind`, `journey_id`, `revision`, `seed`)
- Modify: `internal/runs/replay.go`, `internal/runs/runs.go` (`StartQueued`), `internal/sched/sched.go` (`drain`)
- Modify: `internal/api/replay.go` (`POST /api/journeys/{id}/replay`), `internal/board/ui` (Start run dialog: the replay answer is now a queued item), `internal/devdirector`
- Test: `internal/store/queue_test.go`, `internal/runs/replay_test.go`, `internal/sched/sched_test.go`, `internal/api/replay_test.go`

**Interfaces:**
- Produces: `store.Queued` fields `Kind` (same type as `Run.Kind`), `JourneyID string`, `Revision int`, `Seed int64` (JSON `kind`, `journeyId`, `revision`, `seed`). Revision 0 means "the live revision when it starts".
- Produces: `func (s *Service) EnqueueReplay(journeyID string, revision int, seed int64, actor string, now time.Time) (store.Queued, error)` — validates like Plan 1's `StartReplay` (`ErrStaleJourney`, seed 1..`MaxSeed`), draws a random seed at enqueue when 0 (so the queued item already says what it will replay), records decision `replayed` (actor, reason `Replay of revision N asked`, `RunID` empty).
- Produces: `StartQueued` starts a replay item as a replay (pause gate only, no agent key, no lead gate). A replay item whose journey is gone, paused, or has no such revision fails with an error wrapping `ErrInvalid`, so `drain` drops it.
- Removes: `StartReplay` (its tests move to `EnqueueReplay` + `StartQueued`; keep every assertion of Plan 1's replay tests).

- [ ] **Step 1: Failing tests.** `TestQueueKeepsReplayFields` (store round trip of the four fields). `TestEnqueueReplayThenStart`: enqueue revision 0 seed 0 → item has seed in 1..MaxSeed; `StartQueued` → run `Kind replay`, `Revision` = live, same seed, decision `replayed` with actor. `TestQueuedReplayOfPausedJourneyIsDropped`: set journey paused after enqueue → `StartQueued` error `errors.Is(err, ErrInvalid)`, no run. `TestDrainStartsReplayPastOverBudgetItem`: queue [AI item, replay item], spend over budget → one tick starts the replay, the AI item stays queued. API: `TestReplayJourneyQueues` → 202 `{"queued": {...}}`, 404 unknown journey, 409 stale.
- [ ] **Step 2: Run** `go test -race ./internal/store ./internal/runs ./internal/sched ./internal/api` — Expected: compile failure on the new fields and `EnqueueReplay`.
- [ ] **Step 3: Implement.** In `drain`, `ErrOverBudget` moves on to the next item (only replays can pass it); `ErrPaused` and `ErrBusy` still end the tick. Board: after a replay request show the run as queued (same toast the AI "Start" shows today). Dev director: fake replays follow the new path.
- [ ] **Step 4: Run** `go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `runs: replays queue like any run`.

---

### Task 2: people's journey actions, each a decision

**Files:**
- Modify: `internal/store/journeys.go`, `internal/runs/journeys.go`, `internal/api/replay.go` (or a new `internal/api/journeys.go`, moving `/api/journeys` handlers there)
- Test: `internal/store/journeys_test.go`, `internal/runs/journeys_test.go`, `internal/api/journeys_test.go`

**Interfaces:**
- Produces (store): `SetJourneyState(id string, to JourneyState, d Decision) error` (ErrNotFound); `Rollback(id string, n int, d Decision) (Journey, Revision, error)` — copies full revision n as a new full revision that goes live, `Parent` = old live; a bug revision or missing n is an error; `DeleteJourney(id string, d Decision) error` — deletes the journey, its revisions and any queued item with that `journey_id`, keeps every decision and inserts `d` (kind `deleted`) in the same transaction.
- Produces (runs): `PauseJourney`, `ResumeJourney(id, actor string) error`, `RollbackJourney(id string, n int, actor string) (store.Revision, error)`, `DeleteJourney(id, actor string) error`. Decision kinds and reasons: `paused` "Paused by <actor>", `resumed` "Resumed by <actor>" (state → live), `rolled back` "Rolled back to revision N by <actor>", `deleted` "Deleted by <actor>". Pausing does not stop a copilot run already running (its decision still lands; the journey stays paused).
- Produces (API, actor from `X-BP-Actor` else `local`): `GET /api/journeys/{id}` → `{journey, revisions, decisions}`; `POST /api/journeys/{id}/pause`, `/resume`, `/rollback` `{"revision": N}`; `DELETE /api/journeys/{id}`. 404 unknown, 400 bad revision.

- [ ] **Step 1: Failing tests.** `TestRollbackCopiesOldCode` (rev 3 = code of rev 1, live 3, reason as above, decision rev_before 2 → rev_after 3). `TestRollbackRefusesBugRevision`. `TestDeleteJourneyKeepsDecisions`. `TestDeleteJourneyDropsQueuedReplays`. `TestPauseResumeRows`. API: each route's status codes and that the decision actor is the `X-BP-Actor` value.
- [ ] **Step 2: Run** `go test -race ./internal/store ./internal/runs ./internal/api` — Expected: FAIL (undefined).
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `journeys: pause, resume, roll back and delete, each recorded`.

---

### Task 3: the scheduler replays

**Files:**
- Modify: `internal/project/project.go` (+ validation, canonical form), `internal/sched/source.go` (`fire`, `pick`)
- Test: `internal/project/project_test.go`, `internal/sched/sched_test.go`

**Interfaces:**
- Produces: `project.Role.Explore string` (`json:"explore,omitempty"`), `func ParseExplore(s string) (time.Duration, error)` accepting `daily` (24h), `weekly` (168h), `every N days` (1–90), `every N h|hour|hours` (1–2160); empty = weekly. Validation stores the canonical text (`daily`, `weekly`, `every 3 days`, `every 12 h`). `func (r Role) ExploreEvery() time.Duration`.
- Produces: `project.Story.Replay *bool` (`json:"replay,omitempty"`), `func (s Story) Replays() bool` (nil = true).
- Consumes: Task 1 replay queue items.

Rules, in `fire` for each fire and story candidate:
- Journey of (persona, story) `paused` or `repairing`: the story is not eligible (try the next candidate). Never an AI run instead.
- Replay when the fire has no `Context` (only the activity schedule; message and calendar fires stay AI runs), `story.Replays()`, the journey is `live` with `LiveRev > 0`, and `now - LastExplored < role.ExploreEvery()`. Queue a replay item (revision 0, seed 0 drawn at enqueue as in Task 1, reason `Scheduled replay: <activity>`). Otherwise an AI run as today.
- Paused board: nothing fires (as today). Over budget: only stories that would replay are eligible; a fire with none is settled as skipped.

- [ ] **Step 1: Failing tests.** `TestParseExplore` (table incl. `every 0 days`, `monthly`, `every 91 days` refused; canonical round trip). `TestFireQueuesReplayWithinExploreInterval`, `TestFireQueuesAIRunWhenExploreIntervalPassed`, `TestFireSkipsPausedAndRepairingJourney` (next candidate story taken), `TestFireReplaysWhenOverBudget`, `TestFireAIRunWhenStoryOptsOut` (`replay: false`), `TestMessageFireIsAIRun`.
- [ ] **Step 2: Run** `go test -race ./internal/project ./internal/sched` — Expected: FAIL.
- [ ] **Step 3: Implement.** Replace the `gated` short-circuit with: paused → as today; over budget → pass an `onlyReplays` flag to `pick`.
- [ ] **Step 4: Run** `go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `sched: scheduled runs replay stories that have a live journey`.

---

### Task 4: after a replay — flake guard and copilot hand-off

**Files:**
- Modify: `internal/store/schema.sql` + `addColumns` (journeys `flaky_streak`, `blocked_step`, `blocked_bug`, `copilot_at`; runs `replay_result`), `internal/store/journeys.go`, `internal/store/store.go`
- Modify: `internal/runs/replay.go` (`FinishReplay` stores the result on the run)
- Create: `internal/sched/replays.go`; Modify: `internal/sched/sched.go` (`Tick`)
- Test: `internal/store/journeys_test.go`, `internal/sched/replays_test.go`

**Interfaces:**
- Produces: `store.Journey` fields `FlakyStreak int`, `BlockedStep int`, `BlockedBug string`, `CopilotAt time.Time`. `store.Run.ReplayResult json.RawMessage` (`json:"replayResult,omitempty"`), written by `FinishReplay` (the artifact stays too).
- Produces: `store.ContextReplay{JourneyID string; Revision, Step int; StepName, Error string; Seed int64; Inputs map[string]any; Why string}` on `RunContext.Replay` (`json:"replay,omitempty"`); context kind `replay_failed`.
- Produces: `func (s *Store) UnfollowedReplays() ([]Run, error)` (kind replay, state done/failed/stuck, followed 0); `func (s *Store) FollowReplay(runID string, f ReplayFollow) error` — one transaction: mark followed, insert `f.Next`, insert `f.Decision` if set, update the journey's `State` (if non-empty), `FlakyStreak` (if non-nil), `BlockedStep/BlockedBug` (if `ClearBlocked`), `CopilotAt` (if non-zero).
- Constants in `internal/sched/replays.go`: `FlakyLimit = 3`, `CopilotEvery = 24 * time.Hour`, via values `"flake check"` and `"copilot"`.

`followReplays(now)` runs in `Tick` after `followAll`. Per unfollowed replay r of journey j (journey gone: just mark followed):

| r | Then |
|---|---|
| passed, via ≠ flake check | `FlakyStreak` 0, clear blocked. No row. |
| failed, via ≠ flake check, at `j.BlockedStep` while issue of `j.BlockedBug` is open or confirmed | row `still failing at a known bug` (director). No retry, no copilot. |
| failed, via ≠ flake check, otherwise | queue a replay: same journey, revision and seed, via `flake check`, follows r, chain r's, reason "Checking the failure again with the same inputs". |
| passed, via flake check | `FlakyStreak`+1, row `flaky` (director, "Failed at step N, passed when run again with the same inputs"). At `FlakyLimit`: copilot with why "flaky three times in a row", streak 0. |
| failed, via flake check | copilot. |
| stuck (its wait never came) | copilot. |

Copilot: journey paused → row `copilot not called` "the journey is paused". `now - CopilotAt < CopilotEvery` → row `copilot not called` "one copilot run per journey per day; the last was <time>". Else state `repairing`, `CopilotAt` now, row `copilot called` (director, reason = the failure line), queue an AI run of the persona and story: via `copilot`, follows r, chain r's, reason "A replay failed: <outcome first line>", context `{kind: replay_failed, previousRun: r.ID, previous: outcome, replay: ContextReplay{...from r.ReplayResult}}`.

- [ ] **Step 1: Failing tests** in `internal/sched/replays_test.go`, one per table row plus: `TestKnownBugDoesNotCallCopilotAgain`, `TestFlakeCheckFailureNeverRetriesAgain`, `TestCopilotOncePerDay`, `TestPausedJourneyGetsNoCopilot`, `TestReplaysNeverHandOff` (story with handoffs, passed replay → nothing queued but nothing for handoffs).
- [ ] **Step 2: Run** `go test -race ./internal/store ./internal/sched` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `sched: a failed replay is checked again, then goes to the copilot`.

---

### Task 5: the copilot run and `decide_journey`

**Files:**
- Modify: `internal/runs/journeys.go`, `internal/store/journeys.go` (`Decision.Explored`), `internal/mcpserver/server.go` (+ `journeys_test.go`), `internal/launcher/prompt.go` (+ test), `internal/sched/replays.go` (copilot settle)
- Test: `internal/runs/journeys_test.go`, `internal/mcpserver/journeys_test.go`, `internal/launcher/prompt_test.go`, `internal/sched/replays_test.go`

**Interfaces:**
- Produces: `store.Decision.Explored bool` (`json:"-"`). `AddRevision` and `AddDecision` move `last_explored` only when it is true (replaces Plan 1's `Actor == model` rule). `SaveJourney` and `JourneyDecision` set it; copilot decisions never do (spec: copilot runs do not reset the interval).
- Produces: `type Decide struct{ Kind, Reason, WhyNotBug, BugID string; Evidence []string; Code string; SelfReplay ReplayResult }` and `func (s *Service) DecideJourney(token string, in Decide) (store.Decision, error)`.
  - Kinds: `bug`, `site changed`, `journey weakness`, `flaky`, `input refused correctly`. Only `journey weakness` is allowed outside a copilot run (context kind `replay_failed`), and never on lead runs or fix checks.
  - `bug`: `BugID` must be a bug this run reported. Journey → live, live revision unchanged, `BlockedStep` = the context's step, `BlockedBug` = BugID.
  - The other four: `Code` + `SelfReplay` must prove a full revision (Plan 1 rules, token check included); stored with `AddRevision` using the decision kind; journey → live, blocked cleared, `FlakyStreak` 0. `site changed` needs `WhyNotBug`, stored in the reason as a second paragraph "Why this is not a bug: …".
  - Evidence: up to 10 strings, each up to 500 characters (artifact names or what was compared).
- Produces: MCP tool `decide_journey` (args `kind`, `reason`, `evidence`, `why_not_bug`, `bug_id`, `code`, `self_replay`). On a copilot run `save_journey` and `journey_decision` refuse with "this is a copilot run: decide with decide_journey", and `finish_run` with outcome waiting is refused. The Plan 1 finish gate (`CheckJourneyDecided`) is what makes a copilot decide before finishing.
- Produces: prompt section for context kind `replay_failed`, replacing step 7 for that run. It must say: a replay of this journey failed; here are the revision, the failed step, the error, the seed and inputs (from the context) and the live code (already in the assignment); reproduce it in the browser first; then call `decide_journey` with one kind (list them with one line each, as in the spec's "The copilot"); a revision needs the bp-replay self-replay passing; `site changed` must say why it is not a bug; **when unsure, report the bug and decide `bug`**.
- Produces: in `followReplays`' phase, `settleCopilots(now)`: for every `repairing` journey whose newest via-`copilot` run is final and left no decision, state → live with row `copilot gave no decision` (director, reason = that run's outcome first line).

- [ ] **Step 1: Failing tests.** runs: `TestDecideBugKeepsRevisionAndBlocksStep`, `TestDecideSiteChangedNeedsWhyNotBug`, `TestDecideRevisionMustBeProven`, `TestDecideOnlyWeaknessOutsideCopilot`, `TestCopilotDecisionDoesNotResetExplore`. MCP: `TestCopilotRefusesSaveJourneyAndWaiting`, `TestDecideJourneyTool`. Prompt: `TestCopilotPromptSaysWhenUnsureBug` (the replay_failed prompt contains the step, the error, the seed and "when unsure"). sched: `TestCopilotWithoutDecisionReturnsJourneyLive`.
- [ ] **Step 2: Run** `go test -race ./internal/runs ./internal/mcpserver ./internal/launcher ./internal/sched` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `copilot: a failed replay gets an AI decision with its reason`.

---

### Task 6: waits inside journeys

**Files:**
- Modify: `worker/bp-journey-lib.mjs`, `worker/bp-replay.mjs`, `worker/smoke/replay-smoke.mjs` (+ a lib unit smoke if one exists)
- Modify: `internal/runs/replay.go`, `internal/runs/journeys.go` (`ReplayResult.Wait`), `internal/api/replay.go` (`PUT /replay/{token}`, `GET /journey/{token}`), `internal/sched/sched.go` (`resolve`, `stuck`), `internal/store/store.go` (`RunContext.Resume`), `internal/store/messages.go`, `internal/launcher/prompt.go` (step 7 text)
- Test: `internal/runs/replay_test.go`, `internal/api/replay_test.go`, `internal/sched/sched_test.go`, smoke

**Interfaces:**
- Produces (worker): `bp.waitFor({ email: { from?, subject? } | time: { after }, within? })`. A first replay reaching the k-th `waitFor` (counting from 1) ends with `{status: "waiting", step: <steps completed>, wait: {n: k, until: {email|time, timeout: within}}}`. `{ handoff }` throws "a replay cannot wait for a hand-off". With a resume `{after: k, arrivals: [...]}`, `bp.step` calls before the k-th `waitFor` are skipped (counted, not run), and `waitFor` number i ≤ k returns `arrivals[i-1]`: `{ detail, message?: { from, subject, text, links }, link(re) }` where `link(re)` returns the first link matching `re` or throws. Self mode (`--self`) stops at the first `waitFor` and prints `waiting`.
- Produces (director): `ReplayResult.Wait *ReplayWait{N int; Until json.RawMessage}`. `FinishReplay` refuses `waiting`; new `func (s *Service) FinishReplayWaiting(token string, res ReplayResult, w store.Wait, now time.Time) error` parks the replay (`FinishRunWaiting`, outcome "Waiting for <Describe>", result stored on the run). `api.putReplay` parses `Until` with `sched.ParseWait` (handoff refused) and, for email, checks the persona has an address and sets `AfterID` to the persona's newest message received before the replay run started (`store.NewestMessageBefore(persona string, t time.Time) (string, error)`).
- Produces: `store.ContextResume{After int; Arrivals []ContextArrival}`, `ContextArrival{Detail, MessageID string}` on `RunContext.Resume` (`json:"resume,omitempty"`). In `resolve`, a replay run's continuation is a replay item: same journey, revision and seed, `Resume.After` = the wait's n, `Arrivals` = the previous resume's arrivals plus this one. A replay's stuck wait records no bug finding (Task 4 sends it to the copilot).
- Changes: `GET /journey/{token}` returns JSON `{code, resume?: {after, arrivals: [{detail, message?: {from, subject, text, links}}]}}` (`links` = every http(s) URL in the message's text and HTML hrefs, at most 50). bp-replay reads it.
- `SaveJourney` accepts a full revision whose self-replay is `waiting` (proven up to its first wait). Prompt step 7: "waits: use `await bp.waitFor(...)` where the story waits for an email or time; draw `data` values outside steps so a resumed replay draws the same values".

- [ ] **Step 1: Failing tests.** Smoke: a journey with `waitFor({ time: { after: '1m' } })` between two steps reports `waiting` n 1 after step 1; served again with `resume {after: 1, arrivals: [{detail: 'time came'}]}` it skips step 1, runs step 2 and passes; a `link(/confirm/)` on an arrival with links returns the match. Go: `TestReplayWaitParksAndContinuesAsReplay` (waiting result → run waiting with open wait; met → queued replay item, same seed, resume after 1), `TestReplayHandoffWaitRefused`, `TestReplayEmailWaitCountsMailFromTheRun`, `TestReplayStuckWaitHasNoFinding`, `TestJourneyFetchCarriesResume`, `TestSaveJourneyAcceptsWaitingSelfReplay`.
- [ ] **Step 2: Run** `go test -race ./internal/... && node worker/smoke/replay-smoke.mjs` (with `bp-replay` on PATH as CI does) — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the same — Expected: PASS.
- [ ] **Step 5: Commit** `journeys: replays wait for email or time and carry on`.

---

### Task 7: deploy and prove it on the cluster

- [ ] **Step 1:** Push main; wait for CI and the image build (`gh run view`, not `watch`). Deploy `sha-<7>` with `deploy.sh`; director 1/1 ready.
- [ ] **Step 2:** Through the board tab (one-time key): `POST /api/journeys/{id}/pause` and `/resume` on John Smith's first-invoice journey; `GET /api/journeys/{id}` shows both rows with the actor.
- [ ] **Step 3:** Start one John Smith first-invoice AI run from the board (the step Plan 1 left open). Expected: a full revision goes live (decision `recorded`).
- [ ] **Step 4:** Give the role `explore: weekly` and an activity due soon; wait for the scheduler. Expected: a queued item `Scheduled replay: …`, a replay run with $0 usage, outcome `Passed` (or, if it fails, a `flake check` replay follows with the same seed and then a copilot run — record what happened).
- [ ] **Step 5:** If no replay failed in step 4, prove the flake and copilot path in the dev director (fake launcher: a fake replay that fails twice) and say plainly that the cluster did not exercise it. Never change the target site to force a failure.
- [ ] **Step 6:** Report with screenshots, decision rows and costs.
