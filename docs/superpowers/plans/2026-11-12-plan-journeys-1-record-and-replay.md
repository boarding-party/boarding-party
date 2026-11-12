# Journeys 1: record and replay — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every AI run saves a journey revision (or a reason it did not), and a person can replay a journey on the board as a run with no model.

**Architecture:** tiny gains a `command` session agent that runs a given command with no model and no agent credentials, keeping the pod hardening, egress proxy, pool and limits. The director stores journeys, revisions and decisions in SQLite; AI runs write a journey module, self-replay it with `bp-replay` in their own pod and save it through a new MCP tool. A replay is a run of kind `replay` launched as a command session running `bp-replay`, which fetches the revision from the director and reports the result back.

**Tech Stack:** Go (director, tiny controller), SQLite (modernc), Node + Playwright in the worker image, Preact/htm board.

**Spec:** `docs/superpowers/specs/2026-11-12-journeys-design.md`. This plan covers: the tiny command agent, renaming the old "journey" to chain, the journey entity with revisions and decisions, recording on AI runs (full and bug revisions, confirmed, not recorded), replay runs started by hand. Plans 2 and 3 cover scheduling, the copilot, waits inside journeys, generated data and edge cases, fix checks by replay, and the Journeys board pages.

## Global Constraints

- Commits: `git -c user.name="Tiny Systems" -c user.email="dev@tinysystems.io" commit`, no Co-Authored-By or Generated trailers. Never commit `.idea/`. No `replace` directives in go.mod.
- Push to main is approved for boardingparty and tiny. Don't bypass CI.
- tiny repo: `/Users/mtrofimenko/Work/Current/tiny-systems/tiny`. Its working tree has an unrelated change in `images/agent/Dockerfile`: never stage it.
- kubectl/tiny/boardingparty against the cluster: prefix `CLOUDSDK_ACTIVE_CONFIG_NAME=tinyplatform CLOUDSDK_CORE_ACCOUNT=hello@tinysystems.io` and pass `--context gke_tinyplatform_europe-west2-a_cluster-1`. No direct kubectl writes; cluster changes only through `tiny init` and `boardingparty setup`.
- Tests: `go test -race ./...` in each repo.
- UI copy: GB English; the user-facing word is "journey", never "script". Replays are runs.
- Journey and revision code is agent-written and untrusted: the board renders it only as text.
- Every journey decision is a stored row with a reason. Nothing about journeys is decided silently.

## Review Focus

1. A replay pod that dies or hangs before `bp-replay` reports: the run must end failed with "The replay stopped without a result", not stay running. (Task 6 test `TestReplayWithoutResultFails`.)
2. An existing database with the old `journey` columns: data moves to `chain` intact and the director starts. (Task 2 test `TestOpenRenamesJourneyToChain`.)
3. A bug revision whose self-replay did not fail at its declared step, or a full revision whose self-replay failed: refused, nothing stored. (Task 4 test `TestSaveJourneyRefusesUnprovenRevision`.)
4. A replay asked for a journey whose persona or story is no longer in the project, or a revision number that does not exist: refused with a clear error, no run made. (Task 6 test `TestStartReplayRefusesStaleJourney`.)
5. A command session never sees the agent credentials secret. (Task 1 test `TestCommandSessionHasNoAgentSecret`.)

---

### Task 1: tiny `command` session agent, released

**Files (tiny repo):**
- Modify: `api/v1alpha1/session_types.go` (Agent enum, new `Command` field, CEL rule), regenerate deepcopy and CRD (`make generate manifests`)
- Modify: `internal/workload/workload.go:273-490` (`buildPodSpec`)
- Modify: `images/agent/entrypoint.sh`
- Test: `internal/workload/workload_test.go`, `api/v1alpha1` CEL test if the repo has one for MCPServer rules (follow it)

**Interfaces:**
- Produces: `SessionSpec.Agent` accepts `"command"`; `SessionSpec.Command []string` (`json:"command,omitempty"`, MaxItems 32). CEL: `self.agent == 'command'` requires a non-empty `command`; `command` set requires agent `command`.
- Produces: release tag `v0.15.0` with images published by `publish.yml`.

- [ ] **Step 1: Write failing workload tests**

```go
func TestCommandSessionRunsTheCommand(t *testing.T) {
	// Session{Agent:"command", Command:[]string{"bp-replay","--journey-url=http://d/j/t"}}
	// agent container: Command == ["/tiny/entrypoint.sh"], Args == spec.Command,
	// env TINY_AGENT=command; same SecurityContext, proxy env, NodeSelector,
	// Tolerations, RuntimeClassName and Resources as a claude session built from the same spec.
}
func TestCommandSessionHasNoAgentSecret(t *testing.T) {
	// no EnvFrom entry naming AgentEnvSecret (or the verified env secret) on the agent container;
	// a claude session built in the same test does have it.
}
```

- [ ] **Step 2: Run, expect FAIL** — `go test ./internal/workload -run CommandSession` fails to compile (no `Command` field).

- [ ] **Step 3: Implement.** Add the field and enum value; in `buildPodSpec`, when `Agent == "command"`, set the agent container `Args` to `Spec.Command` and leave the credentials `EnvFrom` out; everything else unchanged. In `entrypoint.sh`, when `AGENT=command`: skip the claude/codex checks, and if `$WORKSPACE/.tiny/command-done` does not exist run `"$@"`, write its exit code to that file, then `exec sleep infinity` (so a Deployment restart never runs the command twice and the pod stays for log inspection until the owner deletes the session).

- [ ] **Step 4: Run** `go test -race ./...` and `make manifests generate` with no diff left. Expected: PASS.

- [ ] **Step 5: Commit, tag, release.** Commit only the files above (check `git show --stat HEAD`, `images/agent/Dockerfile`'s unrelated change must not be in it). Push main, tag `v0.15.0`, push the tag, wait for `publish.yml` and `release.yml` to succeed (`gh run watch`).

---

### Task 2: rename the old "journey" (chain of runs) to chain

**Files (boardingparty):**
- Modify: `internal/store/schema.sql`, `internal/store/store.go` (Run, migration), `internal/store/queue.go`, `internal/store/issues.go`, `internal/sched/sched.go`, `internal/sched/cond.go`, `internal/runs/runs.go`, `internal/runs/issues.go`, `internal/api/api.go`, `internal/devdirector/devdirector.go`, and their tests
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces: `store.Run.Chain string` (`json:"chainId"`), `store.Queued.Chain string` (`json:"chainId"`), `Store.RunsInChain(chain string)`, issues column `check_chain`. The API's existing `out["chain"]` list keeps its name.

- [ ] **Step 1: Failing migration test**

```go
func TestOpenRenamesJourneyToChain(t *testing.T) {
	// build a database with the current release's schema (runs.journey, queue.journey,
	// issues.check_journey, index runs_journey) holding one run r1 with journey "j1"
	// and one issue with check_journey "j1"; close; store.Open it.
	// RunByID("r1").Chain == "j1"; the issue's check chain is "j1";
	// pragma_table_info('runs') has chain and no journey; index runs_chain exists.
}
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement.** Before `db.Exec(schema)` in `Open`, rename columns that exist under the old name (`ALTER TABLE ... RENAME COLUMN journey TO chain` for runs and queue, `check_journey TO check_chain` for issues) and `DROP INDEX IF EXISTS runs_journey`; schema.sql and `runsAddedColumns` use the new names; index `runs_chain`. Rename the Go fields and every use; update comments that say "journey" to "chain".
- [ ] **Step 4: Run** `go test -race ./...` — PASS; `grep -rni journey internal --include=*.go` shows only comments that will refer to the new journeys (none yet).
- [ ] **Step 5: Commit** "store: the chain of runs is a chain, freeing 'journey'".

---

### Task 3: journeys, revisions and decisions in the store

**Files:**
- Modify: `internal/store/schema.sql`
- Create: `internal/store/journeys.go`, `internal/store/journeys_test.go`

**Interfaces:**
- Produces:

```go
type JourneyState string // "live", "paused", "repairing"
type Journey struct { ID, Persona, Story string; State JourneyState; LiveRev int; Created time.Time
	Passed, Failed int; LastReplay, LastExplored time.Time }
type RevisionKind string // "full", "bug"
type Revision struct { JourneyID string; N int; Kind RevisionKind; Code, Reason, RunID string; Parent int
	BugID string; BugStep int; Created time.Time } // BugID, BugStep only for kind bug
type Decision struct { ID, JourneyID, RunID, Kind, Reason, Actor string; Evidence []string
	RevBefore, RevAfter int; Created time.Time }

func (s *Store) JourneyFor(persona, story string) (Journey, error)          // ErrNotFound when none
func (s *Store) Journey(id string) (Journey, error)
func (s *Store) Journeys() ([]Journey, error)                                 // by persona, story
func (s *Store) AddRevision(persona, story string, r Revision, d Decision) (Journey, Revision, error)
func (s *Store) Revisions(journeyID string) ([]Revision, error)                // newest first
func (s *Store) Revision(journeyID string, n int) (Revision, error)            // n=0: the live one
func (s *Store) AddDecision(d Decision) error
func (s *Store) Decisions(journeyID string) ([]Decision, error)                // newest first
func (s *Store) CountReplay(journeyID string, passed bool, at time.Time) error
```

Decision kinds used in this plan: `recorded`, `bug revision recorded`, `confirmed`, `not recorded`, `replayed`. `Actor` is `model` for agent decisions, else the X-BP-Actor value or `owner`.

- [ ] **Step 1: Failing tests**

```go
func TestAddRevisionCreatesJourneyAndGoesLive(t *testing.T)   // first full revision: journey created, State live, LiveRev 1, decision row stored with RevAfter 1
func TestBugRevisionNeverGoesLive(t *testing.T)               // full rev 1, then bug rev 2: LiveRev stays 1; Revision(id,0).N == 1; Revision(id,2).Kind == bug
func TestBugRevisionOnNewJourneyLeavesNoLive(t *testing.T)    // first revision is bug: journey exists, LiveRev 0, Revision(id,0) is ErrNotFound
func TestRevisionNumbersOnlyGrow(t *testing.T)                // three revisions numbered 1,2,3; Parent of 3 is the live rev at the time
func TestDecisionsKeepEvidenceAndOrder(t *testing.T)
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement.** Tables `journeys`, `journey_revisions` (primary key journey_id, n), `journey_decisions`; unique (persona, story) on journeys; evidence stored as JSON. `AddRevision` and its decision in one transaction.
- [ ] **Step 4: Run** `go test -race ./internal/store` — PASS.
- [ ] **Step 5: Commit** "store: journeys with revisions and decisions".

---

### Task 4: AI runs record journeys

**Files:**
- Modify: `internal/mcpserver/server.go` (`next_assignment` result, `report_bug` result, new tools, `finish_run` gate), `internal/runs/runs.go` or create `internal/runs/journeys.go`, `internal/launcher/prompt.go`
- Test: `internal/mcpserver/server_test.go`, `internal/runs/journeys_test.go`

**Interfaces:**
- Consumes: Task 3 store API.
- Produces (runs package):

```go
type SaveJourney struct { Code, Reason string; Kind store.RevisionKind; BugID string; BugStep int
	SelfReplay ReplayResult }
type ReplayResult struct { Status string /* passed|failed */; Step int; StepName, Error string; Seed int64; Inputs map[string]any; DurationMs int }
func (s *Service) SaveJourney(token string, in SaveJourney) (store.Revision, error)
func (s *Service) JourneyDecision(token, kind, reason string) error   // kind: confirmed | not recorded
var ErrUnproven = errors.New(...)        // self-replay does not prove the revision
var ErrNoJourneyDecision = errors.New(...)
```

- MCP: `next_assignment` gains `journey: {revision, code} | null` (the live revision for this persona and story). `report_bug` returns the bug id (if it does not already). New tools `save_journey {code, reason, kind, bug_id, bug_step, self_replay}` and `journey_decision {kind, reason}`. `finish_run` for a browser run (not a lead run) returns `ErrNoJourneyDecision` until the run has saved a revision or made a decision.
- Limits: code at most 256 KiB, reason at most 2000 characters (refuse beyond).

- [ ] **Step 1: Failing tests**

```go
func TestSaveJourneyStoresProvenFullRevision(t *testing.T)   // self_replay passed -> revision kind full live; decision recorded, actor model, run id set
func TestSaveJourneyRefusesUnprovenRevision(t *testing.T)    // full + failed: ErrUnproven; bug + passed: ErrUnproven; bug failed at step 3 with bug_step 5: ErrUnproven; nothing stored
func TestSaveJourneyBugRevisionLinksTheBug(t *testing.T)     // bug + failed at bug_step: stored, BugID set, LiveRev unchanged
func TestFinishRunNeedsJourneyDecision(t *testing.T)          // finish_run done without save/decision -> error naming save_journey and journey_decision; after journey_decision confirmed -> ok; lead run exempt
func TestNextAssignmentCarriesLiveJourney(t *testing.T)
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement.** A bug revision is proven when the self-replay status is failed and its step equals `bug_step`. Prompt: insert a step between today's 6 and 7: write `/workspace/journey.mjs` in the format below from the Playwright code the pw tools reported, replace values that change between runs with `data` expressions, add `expect` checks for what was seen, run `bp-replay --journey /workspace/journey.mjs --self` until it passes (or fails exactly at the bug's step for a bug revision, with `--expect-fail-step N`), then call `save_journey` with the printed result. If the assignment's live journey already does what this run did, call `journey_decision` confirmed with a one-line reason instead. If no passing journey can be made, `journey_decision` not recorded with the reason. Never put the uploadURL, tokens or server URLs in the journey. Journey format (copy into the prompt):

```js
export default async function journey({ page, data, bp, expect }) {
  await bp.step('Open the invoice form', async () => { await page.goto('https://…'); });
}
```

- [ ] **Step 4: Run** `go test -race ./...` — PASS.
- [ ] **Step 5: Commit** "runs: AI runs save a journey revision or say why not".

---

### Task 5: `bp-replay` in the worker image

**Files:**
- Create: `worker/bp-replay.mjs`, `worker/bp-journey-lib.mjs`, `worker/smoke/replay-smoke.sh` (or extend the existing smoke)
- Modify: `worker/Dockerfile` (install, `bp-replay` on PATH), `worker/README.md`

**Interfaces:**
- Produces CLI:
  - `bp-replay --journey <file> --self [--seed N] [--expect-fail-step N] [--device KEY]` — self-replay inside an AI run's pod: fresh browser context, prints one JSON `ReplayResult` line (Task 4 shape) to stdout, exit 0 when the result proves the revision.
  - `bp-replay --journey-url URL --report-url URL --upload-url URL --profile-url URL --live-url URL --console-url URL [--seed N] [--device KEY]` — a replay run: GET the code from `--journey-url`, restore the profile from `--profile-url`, run, upload `failed-step.png`, `trace.zip`, a `.webm` video to `--upload-url`, save the profile back, PUT the `ReplayResult` JSON to `--report-url`.
- `bp` helpers in this plan: `bp.step(name, fn)` (1-based step counter, failure records step and name), `bp.money(n)` (GBP `£1,234.56`). `data` in this plan: `data.seed`, `data.int(min,max)`, `data.pick(list)`, `data.plusEmail(base)` (adds `-<seed>` to the plus part), every drawn value recorded in `inputs`. Generators arrive in Plan 3. `bp.waitFor` throws "waits arrive in a later version" (Plan 2).
- Browser: import Playwright from the `@playwright/mcp` install so the `humanise.mjs`-patched core is used; launch Chromium with the same flags as `pw-config.json` (CDP 9222 on 127.0.0.1) and the same device mapping as `bp-browser`'s `--bp-device`; start `bp-screen.mjs` and `bp-console.mjs` with the live and console URLs in replay-run mode.

- [ ] **Step 1: Failing smoke** — `worker/smoke/replay-smoke.sh` builds the image, serves a static page with a button and a total, and runs `bp-replay --self` on a two-step journey: expects `{"status":"passed"...}`; on a journey whose second step clicks a missing button: expects `"status":"failed","step":2` and `--expect-fail-step 2` exits 0.
- [ ] **Step 2: Run, expect FAIL** (no `bp-replay`).
- [ ] **Step 3: Implement** per the interface.
- [ ] **Step 4: Run the smoke** — PASS. `docker build` succeeds for amd64.
- [ ] **Step 5: Commit** "worker: bp-replay runs a journey without a model".

---

### Task 6: replay runs

**Files:**
- Modify: `internal/store/schema.sql`, `internal/store/store.go` (Run fields + `runsAddedColumns`), `internal/runs/runs.go` (+ `internal/runs/replay.go`), `internal/launcher/tiny.go`, `internal/api/api.go` (worker routes), `go.mod` (tiny `v0.15.0`)
- Test: `internal/runs/replay_test.go`, `internal/launcher/tiny_test.go`, `internal/api/replay_test.go`

**Interfaces:**
- Consumes: Task 1 `Agent:"command"`, `Command`; Task 3 store; Task 5 CLI flags; Task 4 `ReplayResult`.
- Produces:

```go
// store.Run gains: Kind string `json:"kind,omitempty"` ("" AI run, "replay");
// JourneyID string `json:"journeyId,omitempty"`; Revision int `json:"revision,omitempty"`; Seed int64 `json:"seed,omitempty"`
func (s *Service) StartReplay(ctx context.Context, journeyID string, revision int, seed int64, actor string) (store.Run, error) // revision 0 = live, seed 0 = random
func (s *Service) FinishReplay(token string, res ReplayResult, now time.Time) error
var ErrStaleJourney = errors.New(...)  // persona or story gone, or no such revision
```

- Worker routes: `GET /journey/{token}` (the revision's code, text/plain, only while the run is active), `PUT /replay/{token}` (ReplayResult JSON, 64 KiB cap).
- Outcome text: passed → state done, outcome "Passed"; failed → state failed, outcome `Failed at step N: <name>` plus the error on a second line. `CountReplay` updates the journey. A user-started replay stores a decision row of kind `replayed` with the actor and the revision (spec: user actions are rows too).
- Launcher: `Start` for `Kind == "replay"` creates a session with `Agent: "command"`, `Command: ["bp-replay", ...flags from Task 5]`, no Task, no MCP servers, same image, resources, pool, runtime class; keep the existing pruned-field checks and add one: if the stored session's `Agent` is not `command`, stop it and return "tiny on this cluster is older than v0.15 and cannot run replays; update it with tiny init".
- Gate: replays pass `Gate` only for pause, not for budget (spec: replays still run when the budget is spent).

- [ ] **Step 1: Failing tests**

```go
func TestStartReplayRunsTheLiveRevision(t *testing.T)     // run Kind replay, Revision == LiveRev, Seed != 0, persona/story from the journey
func TestStartReplayRefusesStaleJourney(t *testing.T)     // story removed from project -> ErrStaleJourney; revision 9 of 2 -> ErrStaleJourney; no run created
func TestFinishReplayPassedAndFailed(t *testing.T)        // outcomes exactly "Passed" and "Failed at step 4: Fill the business\n<error>"; journey Passed/Failed counters move
func TestReplayWithoutResultFails(t *testing.T)           // session Phase Failed or gone with no PUT /replay -> run failed, outcome "The replay stopped without a result"
func TestReplayIgnoresBudgetButNotPause(t *testing.T)
func TestLauncherStartsReplayAsCommandSession(t *testing.T) // Agent command, Command[0] bp-replay, flags carry the run token URLs, no MCP servers, pool and runtime class kept
```

- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement.** Bump tiny to `v0.15.0` first and fix any compile breaks from v0.14 in the launcher. The "stopped without a result" path reuses the existing reconcile of runs whose session ended (find it in `internal/runs`), branching on `Kind`.
- [ ] **Step 4: Run** `go test -race ./...` — PASS.
- [ ] **Step 5: Commit** "runs: replay a journey as a run with no model".

---

### Task 7: replay from the board

**Files:**
- Modify: `internal/api/api.go` (admin routes), `internal/board/ui/app.js` (StartRun dialog, runs list and run page), `internal/board/ui/styles.css`
- Test: `internal/api/replay_test.go`

**Interfaces:**
- Consumes: Task 6 `StartReplay`, Task 3 `JourneyFor`, `Revisions`.
- Produces: `GET /api/journeys?persona=&story=` → `[{id, persona, story, state, liveRev, revisions:[{n, kind, reason, created, runId}]}]`; `POST /api/journeys/{id}/replay {revision?:int, seed?:int}` → the run (201), 409 with the error text on `ErrStaleJourney`, actor from `X-BP-Actor`.
- UI: Start run dialog gains "How" — **AI run** (default) or **Replay journey** — shown only when the chosen persona and story have a journey; Replay shows a revision select (newest first, live one marked "live", bug revisions listed as "bug revision N"). Runs list and cards label replays "Replay · revision N" with no Watch-the-agent wording; run page for a replay shows journey revision, seed and the drawn inputs, and the failed step's screenshot.

- [ ] **Step 1: Failing API tests** `TestReplayRouteStartsRun`, `TestReplayRouteRefusesStale` (409 body contains the reason).
- [ ] **Step 2: Run, expect FAIL.**
- [ ] **Step 3: Implement**, following the existing dialog and run-page patterns (frontend-design skill; keep the existing visual system).
- [ ] **Step 4: Run** `go test -race ./...` — PASS; check the dialog and a replay run on the dev director (`go run ./cmd/boardingparty board --dev-director --no-open --port 8798`).
- [ ] **Step 5: Commit** "board: replay a journey from Start run".

---

### Task 8: deploy and prove it end to end

- [ ] **Step 1:** Upgrade tiny on the test cluster with the v0.15.0 CLI: `tiny init -n boardingparty-test` (with the prefix and context from Global Constraints). Expected: controller and CRD at v0.15.0 (`kubectl get crd sessions... -o jsonpath` shows `command` in the schema).
- [ ] **Step 2:** Push boardingparty main, wait for images, `boardingparty setup -n boardingparty-test --image-tag sha-<sha7>`.
- [ ] **Step 3:** Start an AI run of John Smith's invoice story on the test board. Expected: the run finishes with a revision recorded (decision row "recorded", revision 1 live) or a "not recorded" decision with a reason.
- [ ] **Step 4:** Replay revision 1 from Start run. Expected: a replay run, $0 cost, outcome "Passed" (or "Failed at step N" with screenshot), journey counters updated, no model usage file.
- [ ] **Step 5:** Report the two runs' links and the outcome.
