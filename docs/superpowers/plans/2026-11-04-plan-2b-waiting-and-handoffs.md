# Plan 2b: Waiting and Handoffs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A run that needs something not there yet (an email, another role's part, a later time) ends `waiting` with a machine-checkable condition; the director checks those conditions without model calls, queues the follow-up run, and hands stories off between roles, so one booking can travel patient → clinician → patient as a chain of runs.

**Architecture:** `finish_run(outcome: waiting, until: …)` ends the run like any other (session stopped, no pod) and stores one row in a new `waits` table. A new package `internal/sched` holds the condition rules (parse, describe, check) and a `Scheduler` goroutine that ticks every 30 s: it turns finished runs into handoff follow-ups, resolves open waits (met → queue a continuation, timeout → stuck with a finding), and starts the oldest queued item when no run is active. The queue is a SQLite table; runs carry a `journey` id (shown on the board as a "chain"), `follows`/`via` links and the `context` handed to the agent by `next_assignment`.

**Tech Stack:** Go 1.26, SQLite (modernc, existing `internal/store`), `github.com/modelcontextprotocol/go-sdk/mcp` (existing), Preact board UI (`internal/board/ui`). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-29-boardingparty-design.md` (§3 Story/Handoff/Assignment/Run, §3.2 handoff trigger, §4.3 tools, §5, §5.1, §5.2). Role activity schedules, budgets and message-triggered stories are Plan 2c.

## Global Constraints

- Go, no new third-party deps unless unavoidable; no `replace` directives in go.mod.
- Commits: identity "Tiny Systems <dev@tinysystems.io>", no Co-Authored-By or Generated trailers, never commit `.idea/`.
- User-facing copy GB English, plain words. No "journey" or "assignment" on the board: say "chain", "follows run X", "Up next".
- Never log run tokens or URLs containing them.
- Existing behaviour (manual start, egress fence check before start, blocked hosts, usage reporting, live view) keeps working; full `go test ./...` and `go vet ./...` green at every task end.
- Deciding whether to start a run uses no model calls (spec §3.2, §5.1). Agents never poll a page or inbox in a loop.
- One run at a time stays (`runs.ErrBusy`); the scheduler only starts queued work when idle, manual "Start run" behaves exactly as today.

## Decisions (rulings made while planning)

- A wait is stored per run (`waits.run_id` primary key). The waiting run keeps state `waiting` after its wait is met (the board shows it as "continued"); on timeout the run moves `waiting → stuck` and gets a bug row carrying the finding text, so it shows in the run's bug list.
- Email waits start at `finish_run` (the binding rule). To close the gap where the email lands while the agent is wrapping up, the prompt tells the agent to check `messages_list` before waiting. `message_wait` (Plan 2a) stays for an email due within two minutes.
- A handoff wait must name a handoff its story declares; anything else is a tool error listing the declared ones. Handoffs go to another role only (same-role handoff rejected, matching the board's "other roles' stories" picker).
- Each `(role, story)` is handed off at most once per chain, and a chain holds at most `sched.MaxChainRuns = 10` runs (runs + queued). Beyond that the wait goes stuck with "the chain reached 10 runs". This stops A→B→A loops spending tokens.
- Time waits: `after` 1m–7d, `at` from now+1m to now+7d. `timeout` 1m–7d, default 24h, ignored for time waits.
- Handoff to a role with no personas: a waiting run's wait goes stuck at once ("nobody in role <X> to hand off to"); after a `done` run it is only logged.
- Queued items whose persona or story no longer exists are dropped (logged) when they reach the head of the queue. The board can remove a queued item (× button, `DELETE /api/queue/{id}`).
- The dev director seeds a static waiting run, a chain and one queued item, and runs no scheduler, so the seeded rows stay put for UI checks.

## Review Focus

1. The director restarts between `FinishRun` and the handoff enqueue, or between starting a queued item and removing it: no handoff lost, no item started twice (follow-ups use the `followed` flag in one transaction; `CreateRunFrom` deletes the queue row and inserts the run in one transaction). Tests: `TestFollowUpAtomicAcrossReopen`, `TestCreateRunFromConsumesOnce`.
2. A project edit removes or renames a role, story or persona that an open wait or queued item points at: the scheduler must not crash or loop; the queued item is dropped, a handoff wait whose target vanished times out normally. Test: `TestTickDropsInvalidQueued`.
3. Existing databases: old runs must not suddenly fan out handoffs when the user adds `handoffs` to a story (migration sets `followed = 1` and `journey = id` on existing rows). Test: `TestMigrateExistingRunsNotFollowed`.
4. Two waits are met in the same tick, or a wait is met while a run is active: both continuations are queued in order, neither starts until idle, each wait is resolved at most once even if two ticks overlap. Test: `TestTickResolvesEachWaitOnce`.
5. Renaming a role or story in the board editor while another story hands off to it: the editor updates the reference instead of saving a project that fails validation. Checked in Task 9's browser check.

---

### Task 1: Story handoffs in the project

**Files:**
- Modify: `internal/project/project.go`, `internal/project/project_test.go`

**Interfaces:**
- Produces:
  - `type Handoff struct { Role string `json:"role"`; Story string `json:"story"` }`
  - `Story.Handoffs []Handoff `json:"handoffs,omitempty"``
  - `func (p Project) FindStory(role, story string) (Story, bool)`
  - `func (p Project) Role(name string) (Role, bool)`

- [ ] **Step 1: Write failing tests** in `project_test.go`:
  - `TestHandoffsLoad`: YAML with roles `patient` (story `book`, `handoffs: [{role: clinician, story: approve}]`) and `clinician` (story `approve`) loads; `p.Roles[0].Stories[0].Handoffs == []Handoff{{"clinician","approve"}}`; `LoadJSON` of the same project marshalled back gives an equal value.
  - `TestHandoffsValidate` table, each error message must contain the quoted text:
    - unknown role → `hands off to unknown role "nurse"`
    - unknown story in a known role → `hands off to unknown story "triage" of role "clinician"`
    - same role → `hands off to its own role`
    - duplicate entry → `hands off to clinician/approve twice`
    - empty role or story → `handoff needs a role and a story`
  - `TestFindStory`: found / not found.
- [ ] **Step 2:** Run `go test ./internal/project`; expect FAIL (undefined `Handoff`).
- [ ] **Step 3: Implement.** Handoff checks run in a second pass after the role loop (they need every role). Message prefix as existing: `role %q: story %q: …`.
- [ ] **Step 4:** `go test ./internal/project` PASS.
- [ ] **Step 5:** Commit `feat: stories declare handoffs to other roles`.

### Task 2: Store — run chain columns and the queue

**Files:**
- Modify: `internal/store/schema.sql`, `internal/store/store.go`
- Create: `internal/store/queue.go`, `internal/store/queue_test.go`
- Modify: `internal/store/store_test.go`

**Interfaces:**
- Produces:
  - `const RunWaiting RunState = "waiting"`; `func (s RunState) Final() bool` (done, stuck, failed, waiting).
  - `Run` gains `Journey string `json:"journey"``, `Follows string `json:"follows,omitempty"`` (run that caused this one), `Via string `json:"via,omitempty"`` (`""` new, `"continue"`, `"handoff"`), `Context json.RawMessage `json:"context,omitempty"``.
  - `type RunContext struct { Kind string `json:"kind"` /* "continue" | "handoff" */; WaitedFor string `json:"waitedFor,omitempty"`; Arrived string `json:"arrived,omitempty"`; MessageID string `json:"messageId,omitempty"`; PreviousRun string `json:"previousRun"`; Previous string `json:"previous"` /* that run's outcome summary */; From *ContextFrom `json:"from,omitempty"` }`, `type ContextFrom struct { Persona, Role, Story, Summary string }` (lower-camel JSON tags).
  - `type Queued struct { ID, Persona, Role, Story, Journey, Follows, Via string; Context json.RawMessage; Reason string /* board text, e.g. "Handed off by Ada (book)" */; Created time.Time }` (lower-camel JSON tags).
  - `func (s *Store) Enqueue(q Queued) error` (ID required).
  - `func (s *Store) Queue() ([]Queued, error)`: FIFO by `created, rowid`.
  - `func (s *Store) DeleteQueued(id string) error`: `ErrNotFound` when missing.
  - `func (s *Store) CreateRunFrom(r Run, queueID string) error`: one transaction; with `queueID != ""` deletes that row first and returns `ErrNotFound` (run not created) if it was gone. `CreateRun(r)` becomes `CreateRunFrom(r, "")`.
  - `func (s *Store) RunsInJourney(journey string) ([]Run, error)`: oldest first.
  - `func (s *Store) Unfollowed() ([]Run, error)`: runs with state `done` or `waiting` and `followed = 0`, oldest finished first.
  - `func (s *Store) FollowUp(runID string, next []Queued) error`: one transaction; sets `followed = 1` and inserts `next`.
  - Schema: runs columns `journey TEXT NOT NULL DEFAULT ''`, `follows TEXT NOT NULL DEFAULT ''`, `via TEXT NOT NULL DEFAULT ''`, `context TEXT NOT NULL DEFAULT ''`, `followed INTEGER NOT NULL DEFAULT 0`, index `runs_journey(journey)`. Table `queue(id TEXT PRIMARY KEY, persona, role, story, journey, follows, via, context, reason TEXT NOT NULL DEFAULT '', created INTEGER NOT NULL DEFAULT 0)`.
  - Migration helper `func addColumns(db *sql.DB) (added bool, err error)` in `store.go`: for each missing runs column (via `pragma_table_info('runs')`) runs `ALTER TABLE runs ADD COLUMN …`; when it added `followed`, runs `UPDATE runs SET followed = 1, journey = CASE journey WHEN '' THEN id ELSE journey END`. Called in `Open` after the schema.

- [ ] **Step 1: Write failing tests:**
  - `TestMigrateExistingRunsNotFollowed`: create a DB with the old runs schema (copy of today's `CREATE TABLE runs` in the test) and one done run, `Open` it → `Unfollowed()` empty, `RunByID` has `Journey == ID`. Opening twice is a no-op.
  - `TestRunChainFieldsRoundTrip`: `CreateRun` with Journey/Follows/Via/Context → `RunByID` equal; `RunsInJourney` returns both runs of a journey oldest first and none of another.
  - `TestQueueFIFOAndReopen`: enqueue a, b, c with the same `Created` → `Queue()` order a, b, c; close and reopen the store → same order.
  - `TestCreateRunFromConsumesOnce`: `CreateRunFrom(r1, "a")` ok and `a` gone; `CreateRunFrom(r2, "a")` → `ErrNotFound` and `RunByID(r2.ID)` → `ErrNotFound`.
  - `TestFollowUpAtomicAcrossReopen`: a done run is in `Unfollowed`; `FollowUp(id, [q])` → not in `Unfollowed`, `q` queued; after reopen the same holds.
  - `TestUnfollowedOnlyDoneAndWaiting`: stuck, failed, running runs absent.
  - `TestDeleteQueuedMissing` → `ErrNotFound`.
- [ ] **Step 2:** `go test ./internal/store`; FAIL.
- [ ] **Step 3:** Implement. `FinishRun` keeps its `state IN ('pending','running')` guard. `runCols`/`scanRun` grow the four new fields (`context` '' ↔ nil RawMessage).
- [ ] **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat: store run chains and a run queue`.

### Task 3: Store — waits

**Files:**
- Modify: `internal/store/schema.sql`
- Create: `internal/store/waits.go`, `internal/store/waits_test.go`

**Interfaces:**
- Consumes: Task 2 `Queued`, `RunWaiting`.
- Produces:
  - `type WaitStatus string`; `WaitOpen = "open"`, `WaitMet = "met"`, `WaitStuck = "stuck"`.
  - `type Wait struct { RunID string `json:"runId"`; Kind string `json:"kind"` /* "email" | "handoff" | "time" */; From string `json:"from,omitempty"`; Subject string `json:"subject,omitempty"`; Role string `json:"role,omitempty"`; Story string `json:"story,omitempty"`; At time.Time `json:"at,omitzero"`; Began time.Time `json:"began"`; Deadline time.Time `json:"deadline"`; Status WaitStatus `json:"status"`; Detail string `json:"detail,omitempty"`; Ended time.Time `json:"ended,omitzero"` }`
  - `func (s *Store) FinishRunWaiting(id, outcome string, finished time.Time, w Wait) (bool, error)`: one transaction: the `FinishRun` conditional update to `waiting` and, only if it won, insert `w` with `RunID = id`, `Began = finished`, `Status = open`.
  - `func (s *Store) OpenWaits() ([]Wait, error)`: oldest `began` first.
  - `func (s *Store) WaitFor(runID string) (Wait, error)`; `func (s *Store) WaitsFor(runIDs []string) (map[string]Wait, error)`.
  - `type Resolution struct { Status WaitStatus; Detail string; Ended time.Time; Next *Queued; Finding *Bug }`
  - `func (s *Store) ResolveWait(runID string, res Resolution) (bool, error)`: one transaction: `UPDATE waits … WHERE run_id=? AND status='open'`; if no row changed return false and do nothing else; else insert `Next` (if any), insert `Finding` (if any), and for `WaitStuck` also `UPDATE runs SET state='stuck' WHERE id=? AND state='waiting'`.
  - Table `waits(run_id TEXT PRIMARY KEY, kind, sender, subject, role, story TEXT NOT NULL DEFAULT '', at, began, deadline INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '', ended INTEGER NOT NULL DEFAULT 0)`, index `waits_status(status)`.

- [ ] **Step 1: Write failing tests:**
  - `TestFinishRunWaitingOnlyActive`: running run → true, run state `waiting`, `WaitFor` status open with `Began == finished`; second call → false and still one wait row; a done run → false, no wait row.
  - `TestResolveWaitAtMostOnce`: first `ResolveWait(met, Next: q)` → true and `q` queued; second → false, queue still has one item.
  - `TestResolveWaitStuckMovesRunAndFiles`: Resolution stuck with Finding → run state `stuck`, `Bugs(runID)` has the finding, `OpenWaits()` empty.
  - `TestWaitsForMap`: two runs, one with a wait → map has one key.
- [ ] **Step 2:** `go test ./internal/store`; FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat: store run waits`.

### Task 4: Runs service — waiting finish and queued starts

**Files:**
- Modify: `internal/runs/runs.go`, `internal/runs/watch.go`, `internal/runs/runs_test.go`, `internal/runs/watch_test.go`, `internal/cli/ops.go` (+ its test if one covers `terminal`)

**Interfaces:**
- Consumes: Task 2 `CreateRunFrom`, `Queued`, `RunState.Final`; Task 3 `FinishRunWaiting`, `Wait`.
- Produces:
  - `func (s *Service) FinishWaiting(ctx context.Context, token string, w store.Wait, outcome string) error`: same tail as `Finish` (keep last frame, keep blocked hosts, stop session) via a shared `func (s *Service) ended(ctx context.Context, r store.Run, finished time.Time) error`.
  - `func (s *Service) StartQueued(ctx context.Context, q store.Queued) (store.Run, error)`: like `Start` but the run takes `q.Journey`, `q.Follows`, `q.Via`, `q.Context`, and is created with `CreateRunFrom(r, q.ID)`. Returns `store.ErrNotFound` if the item was already taken. Same fence, busy, session and pod checks as `Start` (share one `start(ctx, persona, story string, q *store.Queued)`).
  - `Start` (manual) sets `Journey = run ID`.
  - `Service.OnFinish func()`: called (if set) after any run leaves the active states: `Finish`, `FinishWaiting`, and `Reconcile` when it fails a run.
  - `ErrBadState` text becomes `"finish state must be done, stuck or failed"` unchanged; `Finish` still rejects `waiting` (it needs a condition).
  - `reap` treats every `Final()` state as finished. CLI `terminal(state)` includes `"waiting"`.

- [ ] **Step 1: Write failing tests** (existing fake launcher in `runs_test.go`):
  - `TestFinishWaitingStopsSession`: start a run, `FinishWaiting` → state `waiting`, launcher `Stop` called once with the session, `OnFinish` called once, wait row open.
  - `TestFinishRejectsWaiting`: `Finish(…, store.RunWaiting, …)` → `ErrBadState`.
  - `TestStartQueuedCarriesChain`: enqueue `q{Journey:"j1", Follows:"r0", Via:"continue", Context:…}` → run has those fields, queue empty.
  - `TestStartQueuedBusyKeepsItem`: with an active run → `ErrBusy`, item still queued.
  - `TestStartQueuedFenceKeepsItem`: Fence returns error → `ErrNoFence`, item still queued.
  - `TestManualStartNewJourney`: `Start` → `Journey == ID`, `Via == ""`.
  - `TestReapStopsWaitingSession` (watch_test): a session whose run is `waiting` is stopped.
- [ ] **Step 2:** `go test ./internal/runs ./internal/cli`; FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat: runs can end waiting and start from the queue`.

### Task 5: Wait conditions

**Files:**
- Create: `internal/sched/cond.go`, `internal/sched/cond_test.go`

**Interfaces:**
- Consumes: Task 1 `Project.FindStory`; Task 2/3 store types; `store.Messages` (Plan 2a, `Since` filters on stored time, unfiltered only).
- Produces:
  - Constants: `MaxWait = 7 * 24 * time.Hour`, `DefaultTimeout = 24 * time.Hour`, `MinWait = time.Minute`.
  - `type Until struct { Email *EmailCond `json:"email,omitempty"`; Handoff *HandoffCond `json:"handoff,omitempty"`; Time *TimeCond `json:"time,omitempty"`; Timeout string `json:"timeout,omitempty"` }`, `type EmailCond struct { From, Subject string }`, `type HandoffCond struct { Role, Story string }`, `type TimeCond struct { At, After string }` — lower-case JSON tags plus `jsonschema` descriptions (this struct is the MCP argument type in Task 7).
  - `func ParseWait(u Until, p project.Project, r store.Run, now time.Time) (store.Wait, error)`: exactly one of email/handoff/time; returns Kind, fields, `Deadline` (`now+timeout`, or the time itself for time waits). Error texts (tool-visible, pinned by tests):
    - none or several → `until needs exactly one of email, handoff or time`
    - email with both empty → `email needs from or subject`
    - handoff not declared by the run's story → `story "book" does not hand off to nurse/triage; it hands off to: clinician/approve` (or `…; it hands off to nobody` when none)
    - time with both or neither → `time needs at or after`
    - bad `at` → `time.at must be RFC3339, such as 2026-11-04T15:00:00Z`; `at` before now+1m or after now+7d → `time.at must be between 1 minute and 7 days from now`
    - `after`/`timeout` not a Go duration, or outside 1m–7d → `time.after must be a duration from 1m to 168h, such as 30m` / `timeout must be a duration from 1m to 168h, such as 8h`
    - `timeout` on a time wait is ignored.
  - `func Describe(w store.Wait) string`: plain words, pinned: email `an email from "x" with "y" in the subject` (omit the missing half: `an email from "x"`, `an email with "y" in the subject`); handoff `clinician to finish "approve"`; time `14:00 on 5 Nov 2026 (UTC)` (`15:04 on 2 Jan 2006 (UTC)`).
  - `type Arrival struct { Detail string; MessageID string }`
  - `func Met(st *store.Store, w store.Wait, r store.Run, now time.Time) (Arrival, bool, error)` (`r` is the waiting run: persona and journey come from it):
    - email: newest-first scan of `Messages{Persona: run persona, Since: w.Began, Limit: 200}`, oldest match wins; case-insensitive substring on From and Subject (both must match when both set). Detail `email "<subject>" from <from>`.
    - handoff: first run in `RunsInJourney(r.Journey)` with `Role == w.Role`, `Story == w.Story`, `State == done`, `Finished > w.Began`. Detail `<persona> finished "<story>": <first line of outcome, max 200 chars>`.
    - time: `!now.Before(w.At)`. Detail `time reached`.
  - `func StuckText(w store.Wait) string`: `waited <dur> for <Describe(w)>; it never happened`, `dur` = `Deadline − Began` formatted by `fmtDur` (`24h`, `30m`, `1h30m`, `7d` for 168h).

- [ ] **Step 1: Write failing tests** (real store in `t.TempDir()`):
  - `TestParseWaitExactlyOne`, `TestParseWaitEmailNeedsField`, `TestParseWaitHandoffMustBeDeclared` (both error texts), `TestParseWaitTimeBounds` (`after: 30s` rejected, `after: 30m` → Deadline now+30m, `after: 169h` rejected, `at` 2 days ahead accepted, `at` in the past rejected), `TestParseWaitTimeoutDefaultAndCap` (none → +24h, `8h` → +8h, `200h` rejected, time wait with `timeout: 1h` → Deadline = the time).
  - `TestDescribe` for the five shapes above.
  - `TestMetEmail`: message for this persona stored before `Began` ignored; one after with subject `Your booking is APPROVED` matches `subject: "approved"`; other persona's and filtered messages ignored; `from` + `subject` both required when both set.
  - `TestMetHandoff`: done run of clinician/approve in the same journey after Began → met with detail naming the persona; same run in another journey, or finished before Began, or state stuck → not met.
  - `TestMetTime`: one second before `At` false, at `At` true.
  - `TestStuckText`: `waited 24h for clinician to finish "approve"; it never happened`.
- [ ] **Step 2:** `go test ./internal/sched`; FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat: wait conditions`.

### Task 6: Scheduler and handoff fan-out

**Files:**
- Create: `internal/sched/sched.go`, `internal/sched/sched_test.go`
- Modify: `cmd/director/main.go` (+ `main_test.go` only if it builds the service graph)

**Interfaces:**
- Consumes: Task 4 `StartQueued`, `OnFinish`; Task 5 `Met`, `Describe`, `StuckText`; Task 2/3 store methods.
- Produces:
  - `const MaxChainRuns = 10`; `const DefaultEvery = 30 * time.Second`.
  - `type Starter interface { StartQueued(ctx context.Context, q store.Queued) (store.Run, error) }`
  - `type Scheduler struct { Store *store.Store; Runs Starter; Every time.Duration /* 0 = DefaultEvery */; Now func() time.Time /* nil = time.Now */; NewID func() string /* nil = 16 random hex bytes */ }`
  - `func (s *Scheduler) Run(ctx context.Context)`: `Tick` at once, then every `Every` or on `Poke`, until ctx ends; logs tick errors (never tokens).
  - `func (s *Scheduler) Poke()`: non-blocking, coalesced.
  - `func (s *Scheduler) Tick(ctx context.Context) error`, three phases in order, serialised by a mutex:
    1. **Follow-ups.** For each `Unfollowed()` run: compute handoff items with `followUps`, then `FollowUp(run.ID, items)`. A `done` run hands off to every declared handoff of its story; a `waiting` run with a handoff wait hands off to that target only; other waits hand off nothing. Skip a target already handed off in this chain (a run or queued item with `Via == "handoff"` and the same role/story in the journey). Target persona = the role's first persona. No persona → for a waiting run `ResolveWait(stuck, Detail: "nobody in role <X> to hand off to", Finding)`; for a done run, log. Chain cap: if runs + queued in the journey ≥ `MaxChainRuns`, enqueue nothing and (waiting run) resolve stuck with `the chain reached 10 runs`.
       Handoff item: `Via "handoff"`, `Follows` = run ID, same `Journey`, `Context` = `RunContext{Kind:"handoff", PreviousRun: run.ID, Previous: run.Outcome, From: &ContextFrom{run.Persona, run.Role, run.Story, run.Outcome}}`, `Reason` = `Handed off by <persona> (<story>)`.
    2. **Waits.** For each `OpenWaits()`: load its run; `Met` → `ResolveWait(met, Detail, Next: continuation)`; else `now ≥ Deadline` → `ResolveWait(stuck, Detail: StuckText(w), Finding: &Bug{Summary: StuckText(w), Step: "waiting", Severity: "medium", Note: run.Outcome})`. Continuation: same persona/role/story/journey, `Via "continue"`, `Follows` = waiting run ID, `Context` = `RunContext{Kind:"continue", WaitedFor: Describe(w), Arrived: a.Detail, MessageID: a.MessageID, PreviousRun, Previous: run.Outcome}`, `Reason` = `Continues after <Arrived>`. Chain cap applies here too (stuck with `the chain reached 10 runs`).
    3. **Drain.** Take `Queue()[0]`; `StartQueued`: success or a launch failure that left a failed run → done for this tick; `ErrBusy` or `ErrNoFence` → stop, keep the item; `runs.ErrInvalid` → `DeleteQueued`, log `dropped queued <persona>/<story>: <err>`, try the next item; `store.ErrNotFound` → try the next item. At most one run started per tick.
  - Director wiring in `cmd/director/main.go`: `sch := &sched.Scheduler{Store: st, Runs: svc}`, `svc.OnFinish = sch.Poke`, `go sch.Run(ctx)` after the startup reconcile.

- [ ] **Step 1: Write failing tests** with a fake clock, a real store and a fake `Starter` (records calls; can return `ErrBusy`/`ErrInvalid`):
  - `TestTickContinuesWhenEmailArrives`: waiting run with email wait; tick → nothing queued; store a matching message; tick → one item `Via "continue"`, context `arrived` names the subject and `messageId` is set, Starter called with it.
  - `TestTickTimesOutToStuck`: advance clock past Deadline → run state `stuck`, one bug whose summary is `StuckText`, queue empty, Starter not called.
  - `TestTickTimeWait`: `after: 30m`; at +29m nothing; at +30m continuation queued with `arrived: "time reached"`.
  - `TestHandoffFanOutAndBack`: project patient/book → clinician/approve. Patient run ends waiting on `handoff {clinician, approve}` → tick queues clinician run (persona = clinician role's first persona, context `from.persona == "Ada"`, `from.summary` = patient outcome) and starts it; that run finishes done → tick queues the patient continuation in the same journey whose context `arrived` names the clinician persona.
  - `TestDoneFansOutDeclaredHandoffs`: story with two declared handoffs ends done → two items, FIFO order = declaration order.
  - `TestHandoffOncePerChain`: a second waiting run in the same journey on the same handoff → no second clinician item.
  - `TestChainCap`: journey with 10 runs, wait met → stuck with `the chain reached 10 runs`, nothing queued.
  - `TestHandoffNoPersonaStuck`: target role with no personas → wait stuck with `nobody in role "clinician" to hand off to`.
  - `TestTickBusyKeepsQueue`: Starter returns `ErrBusy` → item stays, next tick retries.
  - `TestTickDropsInvalidQueued`: head item `ErrInvalid` → deleted, next item started in the same tick.
  - `TestTickResolvesEachWaitOnce`: two waits met, two concurrent `Tick` calls → exactly two items queued, in wait order.
  - `TestPokeRunsTick`: `Run` with `Every = time.Hour`, `Poke()` → a queued item starts within 1 s.
- [ ] **Step 2:** `go test ./internal/sched`; FAIL.
- [ ] **Step 3:** Implement; wire the director.
- [ ] **Step 4:** `go test -race ./internal/sched && go vet ./... && go test ./...` PASS.
- [ ] **Step 5:** Commit `feat: scheduler resolves waits and hands stories off`.

### Task 7: Agent tools and prompt

**Files:**
- Modify: `internal/mcpserver/server.go`, `internal/mcpserver/server_test.go`, `internal/launcher/prompt.go`, `internal/launcher/tiny_test.go`

**Interfaces:**
- Consumes: Task 5 `Until`, `ParseWait`; Task 4 `FinishWaiting`; Task 2 `Run.Context`.
- Produces:
  - `finishArgs` gains `Until *sched.Until `json:"until,omitempty"``; outcome description `done, stuck, failed or waiting`. Rules: `waiting` without `until` → `outcome waiting needs until: exactly one of email, handoff or time`; `until` with another outcome → `until applies only to outcome waiting`; email wait while `mailUnavailable(mail) != ""` → that message; email wait for a persona without `identity.email` → `this persona has no email address, so it can't wait for email`; else `ParseWait` errors verbatim; success → `FinishWaiting`, text `waiting for <Describe(w)>`.
  - `assignment` gains `Context json.RawMessage `json:"context"`` (`null` for a fresh run) and `story.Handoffs []handoffInfo `json:"handoffs"`` with `type handoffInfo struct { Role, Story, Goal string }` (lower-case tags), empty list when none.
  - Tool description of `finish_run`: `Finish your run. outcome is done, stuck, failed or waiting. For waiting, set until to exactly one of email {from, subject}, handoff {role, story} or time {at or after}, plus an optional timeout. Call this last.`
  - Prompt edits (exact text):
    - Append to step 2: ` If the assignment has a context, read it first: this run either continues an earlier run of yours (previous says where it stopped, arrived what came in) or picks up a hand-off from another persona (from says who and what they did). The browser is fresh, so sign in again with the identity details.`
    - Replace the last paragraph with: `If the story needs something that is not there yet (an email, another role doing their part, or a later time), never poll a page or the inbox in a loop. Finish what you can, wrap up as in steps 4 to 7, then call finish_run with outcome waiting and until set to exactly one of email {from, subject}, handoff {role, story} (only one listed in story.handoffs) or time {at or after}, plus timeout if it should give up before 24h. Before waiting for an email, check messages_list: it may already be there. Use message_wait only for an email due within two minutes. If there is nothing you can do at all, call finish_run with outcome stuck and say why. Do not poll or wait in a loop for something to change.`

- [ ] **Step 1: Write failing tests:**
  - `TestFinishWaitingEmail`: persona with identity.email and mailbox ok → run `waiting`, wait kind email, reply starts `waiting for an email`.
  - `TestFinishWaitingValidation` table: no until; until on `done`; email with mailbox nil; email without identity.email; undeclared handoff → each a tool error with the text above, run still active.
  - `TestFinishWaitingHandoff` and `TestFinishWaitingTime` (`after: 30m`) succeed.
  - `TestNextAssignmentContext`: run created with `Context` → assignment `context.kind == "continue"`; fresh run → `"context":null`; `story.handoffs` lists `{clinician, approve, <goal>}`.
  - `TestPromptMentionsFinishAndNoPolling`: add `"outcome waiting"`, `"never poll a page or the inbox"`, `"check messages_list"`, `"read it first"`, `"sign in again"`, `"story.handoffs"` to the phrase list; keep `"Do not poll"`.
- [ ] **Step 2:** `go test ./internal/mcpserver ./internal/launcher`; FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat: agents end runs waiting and pick up where they stopped`.

### Task 8: Board API

**Files:**
- Modify: `internal/api/api.go`, `internal/api/api_test.go`

**Interfaces:**
- Consumes: Task 2/3 store reads; Task 5 `Describe`.
- Produces:
  - `GET /api/runs` items gain `wait` (`store.Wait` plus `waitingFor` = `Describe(w)`), omitted when the run has none. One `WaitsFor` query per request.
  - `GET /api/runs/{id}` adds `wait` (same shape), `chain` (every run in the journey, oldest first: `{id, persona, role, story, state, started, follows, via}`; omitted when the run is alone), `queued` (queue items of the journey), `context` already on `run`.
  - `GET /api/queue` → `[{id, persona, role, story, follows, via, reason, created}]` (always a JSON array, `[]` when empty).
  - `DELETE /api/queue/{id}` → 204; unknown → 404.

- [ ] **Step 1: Write failing tests:** `TestRunsListShowsWait` (`waitingFor` equals `Describe`, no `token` field anywhere in the body); `TestRunDetailChain` (three runs, two in one journey → chain of two, follows/via set; lone run has no `chain` key); `TestQueueListAndDelete` (FIFO order, delete 204 then 404, list empty array `[]`).
- [ ] **Step 2:** `go test ./internal/api`; FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat: board API shows waits, chains and the queue`.

### Task 9: Dev director seed and board UI

**Files:**
- Modify: `internal/devdirector/devdirector.go`, `internal/devdirector/devdirector_test.go`, `internal/board/ui/app.js`, `internal/board/ui/project.js`, `internal/board/ui/styles.css`

**Interfaces:**
- Consumes: Task 8 routes; Task 1 `handoffs`.
- Produces:
  - Dev director `func seedChain(st *store.Store, p project.Project, now time.Time) error`, called in `start` after the project is saved, using the first role's first persona and story: a done run, a run following it (`Via "continue"`, same journey, done), a `waiting` run (email wait, subject `Booking confirmed`, deadline now+24h), and one queued item following the waiting run (`Reason` `Handed off by Ada (todos)`). No scheduler in the dev director.
  - Board, runs page: **Up next** card above the runs table when the queue is non-empty: rows "persona · story · reason · queued <time>" with a × (confirm dialog "Remove this run from the queue?"). Runs table: state tag `waiting` (new `.tag.hold`, using existing tokens, distinct from `stuck`) with a muted second line `Waiting for <waitingFor> · gives up <fmtWhen(deadline)>`; a met wait shows tag `continued`.
  - Run page **Details**: `Waiting for` (with `until <time>` while open, or what arrived / why it stopped), `Continues` (link to `follows` when `via == "continue"`), `Handed off by` (link when `via == "handoff"`), `Continued by` (links from `chain` runs whose `follows` is this run). A **Chain** card when `chain` exists: ordered list of runs (persona · story · state tag, current one bold) and queued items marked "Up next".
  - Project editor: `StoryRow` gets a **Hands off to** multi-select (checkbox list, compact) of every other role's stories, labelled `<role> · <story>`, hint `When this story ends, or waits for one of these, the director starts it for the first persona of that role.` The draft keeps `handoffs` (`toDraft`/`fromDraft` in project.js); renaming a role or story updates references in other stories; removing one drops them.
  - Copy: GB English, plain words, no "journey" or "assignment".

- [ ] **Step 1: Failing test:** `TestDevDirectorSeedsChain`: `GET /api/runs` has one `waiting` run with `waitingFor` containing `Booking confirmed`; `GET /api/queue` has one item; the continued run's detail has a `chain` of two.
- [ ] **Step 2:** `go test ./internal/devdirector`; FAIL. Implement seed; PASS.
- [ ] **Step 3:** Implement the UI against `go run ./cmd/boardingparty board --dev-director --no-open --port 18905`.
- [ ] **Step 4: Browser check** (Claude in Chrome, desktop and phone width): Up next card and ×, waiting row line, run page Details links and Chain card, Hands off to picker saves and reloads, renaming the target story updates the picker. Close the tab and stop the server after.
- [ ] **Step 5:** `go vet ./... && go test ./...` PASS.
- [ ] **Step 6:** Commit `feat: board shows waiting runs, chains and what is up next`.

### Task 10: Live check (controller, after push)

- [ ] Push, wait for CI green, user restarts `boardingparty board`.
- [ ] On smesolutions.uk: a story that triggers an email and waits for it (`until: {email: {subject: …}}`). Confirm the run ends `waiting`, its pod is gone, the email shows under Inbox, and within ~30 s a continuation is queued and starts with `context.arrived` naming the email.
- [ ] Two-role handoff on the same project (or a test project with two roles): the first run ends waiting on the handoff, the second role's run starts with `context.from` naming the first persona, and its `done` queues the continuation. Board shows the chain.
