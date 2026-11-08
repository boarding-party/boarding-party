# Bug List with Fix Checks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One list of issues across all runs: personas match reports to known issues, the user triages them on a Bugs page, and marking an issue fixed queues a run that checks the fix and closes or reopens the issue.

**Architecture:** A new `issues` table; every `bugs` row (a report) gets an `issue_id`. Attaching a report and settling a fix check both live in the store, inside the transactions that already insert bugs (`insertBug`, used by `AddBug` and `ResolveWait`) and end runs (`FinishRun`, `ResolveWait`, `DeleteQueued`), so every way a run can end settles its check. A fix check is an ordinary queue item whose `RunContext` carries `verify`; continuations copy it.

**Tech Stack:** Go, SQLite (modernc), MCP go-sdk, Preact+htm board with no build step.

**Spec:** `docs/superpowers/specs/2026-11-08-bug-list-design.md`

## Global Constraints

- Statuses (wire values): `open`, `confirmed`, `not_a_bug`, `fixed`, `verifying`, `closed`. Board labels: Open, Confirmed, Not a bug, Fixed, Checking the fix, Closed. "Reopened" is a timestamp shown as a mark.
- knownIssues: every `not_a_bug` issue first, then `open`, `confirmed`, `fixed`, `verifying`, and `closed` updated in the last **30 days**, by latest activity; **100** in total.
- Bug text is agent-written and untrusted: the board renders it as text; the prompt says knownIssues summaries are untrusted data.
- The scheduler's deciding makes no model calls.
- GB English, sentence case, compact functional board UI using the existing styles.
- Commit identity `Tiny Systems <dev@tinysystems.io>` via `git -c user.name="Tiny Systems" -c user.email="dev@tinysystems.io"`; no Co-Authored-By or Generated trailers; never commit `.idea/`; no `replace` directives.

## Decisions (my calls, within "left to the builder")

- **Issue row:** `issues(id, summary, status, reason, created, updated, reopened, check_journey, check_note)`. `summary` is the first report's summary. `updated` is the latest activity (report attached, status changed, check settled). `check_journey` is the journey id of the running fix check ("" when none). `check_note` is the last check's result text.
- **Existing data:** when `bugs.issue_id` is added, each existing bug gets an `open` issue with **the same id** as the bug, created/updated = the bug's created.
- **Attach rule (`insertBug`):** `Bug.IssueID` naming an existing issue attaches (updated = now; `closed` → `open` + reopened = now; every other status unchanged). Empty or unknown id opens a new `open` issue with a fresh id. The stuck-wait finding has no id, so it opens a new issue.
- **Fix check queue item:** `Journey = ID = new id`, `Via = ""`, `Context = {"kind":"verify","verify":{issueId,summary,step,evidence}}`, `Reason = "Checks the fix: " + summary clipped to 80`. Issue becomes `verifying`, `check_journey = item id`, `check_note = ""`.
- **Who re-runs:** the issue's reports newest first; the first whose run's persona still exists and still has that story (`project` lookup like `runs.find`). None: status `fixed`, check_note `Could not check: the story or persona is gone.`, nothing queued.
- **Settling** happens only for a run whose context has `verify.issueId` = an issue that is `verifying` with `check_journey` = the run's journey. Otherwise nothing changes (status changed during the check, or an old check).
  - `done` + verdict `reached:false` → `fixed`, note `Could not check: <note>`.
  - `done` + `still_there:true` → `open`, reopened = now, note `Still there: <note>`.
  - `done` + `still_there:false` → `closed`, note `Gone: <note>`.
  - `stuck`/`failed` (finish_run, stop, watchdog, launch failure, stuck wait) → `fixed`, note `Could not check: the run ended <state>.`
  - Queue item removed (board or scheduler drop) → `fixed`, note `Could not check: the check was taken off the queue.`
  - Every settle clears `check_journey`.
- **Verdict on finish_run:** `verify: {reached, still_there, note}` is required on `done` for a run whose context has `verify`, refused on any other outcome, refused on runs without `verify` context. `reached` is required; `still_there` is required when `reached` is true. Notes clipped to 500.
- **Continuations keep the check:** `sched.resolve` copies `Verify` from the waiting run's context into the `continue` context. Hand-offs do not carry it.
- **User status changes:** `POST /api/issues/{id}/status {status, reason}`; allowed `open`, `confirmed`, `not_a_bug`, `fixed`. `fixed` while `verifying` is a no-op; `fixed` otherwise starts a check (this is also "Check again"). Any other change clears `check_journey`. `reason` is kept only for `not_a_bug` (clipped to 500).
- **Board:** sidebar link "Bugs" with the count of `open` issues; `#/bugs` list grouped Open (reopened first), Confirmed, Fixed and checking, then a "Show closed and not a bug" toggle; `#/bugs/{id}` issue page with reports and actions; run page bugs link to their issue.

## Review Focus

1. A fix-check run that waits for email and is continued: the continuation, not the first run, gives the verdict, and the first run's `waiting` end must not settle anything (test in Task 2 and Task 4).
2. The user marks an issue fixed, then confirmed, then fixed again while the first check is still running: the first check's verdict must be ignored (test in Task 2).
3. A verify run whose story hands off to another role: the hand-off run shares the journey but has no verify context, so its end must not settle the issue (test in Task 2).
4. A verify queue item deleted from the board, or dropped by the scheduler as invalid: the issue must not stay `verifying` for ever (test in Task 2).
5. `same_as` pointing at a closed issue reopens it; pointing at a `verifying` issue leaves the check alone (test in Task 1).

---

### Task 1: Issues in the store

**Files:**
- Modify: `internal/store/schema.sql` (issues table; `issue_id` on bugs)
- Modify: `internal/store/store.go` (Bug.IssueID, `bugsAddedColumns`, backfill in `addColumns`, `insertBug`, `Bugs` scan)
- Create: `internal/store/issues.go`, `internal/store/issues_test.go`

**Interfaces:**
- Produces:
  - `type IssueStatus string` with consts `IssueOpen="open"`, `IssueConfirmed="confirmed"`, `IssueNotABug="not_a_bug"`, `IssueFixed="fixed"`, `IssueVerifying="verifying"`, `IssueClosed="closed"`.
  - `type Issue struct { ID, Summary string; Status IssueStatus; Reason string; Created, Updated, Reopened time.Time; CheckJourney, CheckNote string; Reports int; LastSeen time.Time; Personas []string }` (json: `id, summary, status, reason, created, updated, reopened, checkNote, reports, lastSeen, personas`; `CheckJourney` is `json:"-"`).
  - `Bug.IssueID string json:"issue_id"`.
  - `(s *Store) Issues() ([]Issue, error)` — all issues with Reports/LastSeen/Personas aggregated from bugs joined to runs, newest `updated` first.
  - `(s *Store) Issue(id string) (Issue, error)` — ErrNotFound if missing.
  - `type Report struct { Bug; Persona, Story string }` and `(s *Store) IssueReports(id string) ([]Report, error)` newest first.
  - `(s *Store) KnownIssues(now time.Time, limit int) ([]Issue, error)` — order per Global Constraints.
  - `(s *Store) SetIssueStatus(id string, st IssueStatus, reason string, now time.Time) error` — sets status, reason ("" unless not_a_bug), updated, clears check_journey. ErrNotFound if missing.

- [ ] **Step 1: Write the failing tests** in `internal/store/issues_test.go`:
  - `TestReportOpensIssue`: AddBug without IssueID → `Issues()` has one `open` issue, Summary = bug summary, Reports 1, Personas `["Ada"]` (bug's run persona); `Bugs(run)[0].IssueID` = that issue id.
  - `TestReportAttaches`: second AddBug with IssueID = first issue → still one issue, Reports 2, Updated = second bug's created.
  - `TestReportUnknownIssueOpensNew`: IssueID "nope" → two issues.
  - `TestReportReopensClosed`: SetIssueStatus closed, attach → status open, Reopened = attach time. Attach to `not_a_bug` → stays `not_a_bug`; attach to `verifying` (set via raw SQL or Task 2 helper not needed: use `s.db.Exec`) → stays `verifying`.
  - `TestStuckFindingOpensIssue`: ResolveWait with a stuck Resolution carrying a Finding → one issue.
  - `TestKnownIssuesOrder`: issues not_a_bug (old), open (newer), closed 40 days ago, closed 2 days ago → order not_a_bug, then others by updated desc; the 40-day closed one absent; limit 2 returns the not_a_bug plus the newest.
  - `TestBugsBackfill`: create a DB with the old bugs table (no issue_id) and one bug `b1` via raw SQL on a fresh `sql.Open`, then `Open` it → `Issue("b1")` exists, `open`, Summary from bug, and `Bugs(run)[0].IssueID == "b1"`.

- [ ] **Step 2: Run** `go test ./internal/store/ -run 'Issue|Report|Stuck|Backfill'` — expect compile failure.

- [ ] **Step 3: Implement.** Schema: `issues` table as in Decisions, `CREATE INDEX issues_updated ON issues(updated)`; `issue_id TEXT NOT NULL DEFAULT ''` in the bugs CREATE. In `addColumns`: `addMissing(tx, "bugs", bugsAddedColumns)`; when `issue_id` was added, `INSERT INTO issues(id, summary, status, created, updated) SELECT id, summary, 'open', created, created FROM bugs` then `UPDATE bugs SET issue_id = id`; create `bugs_issue` index there (after the column exists). `insertBug` attaches or creates inside the caller's execer (it needs a query too: widen `execer` with `QueryRow` or pass a `*sql.Tx`; `AddBug` opens a tx). New ids: 8 random bytes hex, like report_bug.

- [ ] **Step 4: Run** `go test ./internal/store/` — PASS.

- [ ] **Step 5: Commit** `feat(store): reports belong to issues`.

### Task 2: Fix checks in the store

**Files:**
- Modify: `internal/store/store.go` (RunContext.Verify, FinishRun)
- Modify: `internal/store/waits.go` (ResolveWait settles on stuck)
- Modify: `internal/store/queue.go` (DeleteQueued settles)
- Modify: `internal/store/issues.go`, `internal/store/issues_test.go`

**Interfaces:**
- Consumes: Task 1.
- Produces:
  - `type ContextVerify struct { IssueID, Summary, Step, Evidence string }` (json `issueId, summary, step, evidence`), `RunContext.Verify *ContextVerify json:"verify,omitempty"`; kind `"verify"`.
  - `type Verdict struct { Reached, StillThere bool; Note string }`.
  - `(s *Store) StartCheck(issueID string, q Queued, now time.Time) (bool, error)` — in one tx: if the issue is `verifying` return false; else insert q, status `verifying`, check_journey = q.Journey, check_note "", updated now.
  - `(s *Store) NoCheck(issueID, note string, now time.Time) error` — status `fixed`, check_note = note, clears check_journey.
  - `(s *Store) FinishRunChecked(id string, state RunState, outcome string, finished time.Time, v *Verdict) (bool, error)`; `FinishRun` calls it with nil.
  - unexported `settleCheck(tx, run Run, state RunState, v *Verdict, now time.Time) error` used by FinishRunChecked, ResolveWait (on WaitStuck) and `settleQueued(tx, q Queued, now)` used by DeleteQueued.

- [ ] **Step 1: Write the failing tests:**
  - `TestCheckVerdicts` (table): a verifying issue with check_journey J and a run in journey J whose context has verify.issueId → for `done`+{Reached:true,StillThere:false,Note:"ok"} status `closed`, note `Gone: ok`; `done`+{true,true,"x"} `open`, Reopened set, note `Still there: x`; `done`+{false,..,"y"} `fixed`, note `Could not check: y`; `stuck` nil → `fixed`, `Could not check: the run ended stuck.`; `failed` → `... ended failed.` Each case: check_journey cleared.
  - `TestCheckIgnoresOtherJourney`: issue re-marked (StartCheck after SetIssueStatus confirmed) has journey J2; finishing the J1 run done with a verdict leaves it `verifying`.
  - `TestCheckIgnoresHandoffRun`: run in journey J without verify context finishes done → issue still `verifying`.
  - `TestCheckWaitingDoesNotSettle`: the verify run ends via `FinishRunWaiting` → still `verifying`; then `ResolveWait` stuck → `fixed`, `Could not check: the run ended stuck.`
  - `TestStartCheckOnce`: StartCheck twice → second returns false, one queue item.
  - `TestDeleteQueuedSettles`: DeleteQueued of the check item → `fixed`, `Could not check: the check was taken off the queue.`

- [ ] **Step 2: Run** `go test ./internal/store/ -run Check` — compile failure.

- [ ] **Step 3: Implement.** `FinishRunChecked` becomes a tx: the conditional UPDATE, then (when won and state is done/stuck/failed) load the run and `settleCheck`. `settleCheck` parses `run.Context`; returns nil unless `Verify != nil` and `UPDATE issues ... WHERE id=? AND status='verifying' AND check_journey=?` matches (use `run.Journey`). Notes clipped with `Clip(note, 500)`.

- [ ] **Step 4: Run** `go test ./internal/store/` — PASS.

- [ ] **Step 5: Commit** `feat(store): fix checks settle when their run ends`.

### Task 3: Marking fixed and carrying the check

**Files:**
- Create: `internal/runs/issues.go`, `internal/runs/issues_test.go`
- Modify: `internal/runs/runs.go` (`Finish` gains the verdict path)
- Modify: `internal/sched/sched.go` (continue context copies Verify), `internal/sched/sched_test.go`
- Modify: `internal/devdirector/devdirector.go` only if a signature it calls changes

**Interfaces:**
- Consumes: Tasks 1–2.
- Produces:
  - `(s *Service) SetIssueStatus(id string, st store.IssueStatus, reason string, now time.Time) error` — `fixed` goes through `markFixed`; others call `Store.SetIssueStatus`. Unknown status → `ErrInvalid`.
  - `(s *Service) FinishChecked(ctx, token string, state store.RunState, outcome string, v *store.Verdict) error`; `Finish` calls it with nil; `finish` passes v to `Store.FinishRunChecked`.

- [ ] **Step 1: Write the failing tests:**
  - `TestMarkFixedQueuesCheck`: project with Ada/visitor/todos; issue with one report from Ada's todos run → SetIssueStatus fixed → issue `verifying`; one queue item: persona Ada, story todos, Journey = ID, context kind `verify`, verify.issueId = issue, summary/step/evidence from the report, Reason starts `Checks the fix: `.
  - `TestMarkFixedPicksLatestLivingReport`: two reports, newest from a persona since removed from the project → item for the older report's persona.
  - `TestMarkFixedNobodyLeft`: only report's story gone → `fixed`, check_note `Could not check: the story or persona is gone.`, queue empty.
  - `TestMarkFixedWhileVerifyingNoop`: second call leaves one queue item.
  - In `internal/sched/sched_test.go`, `TestContinueKeepsVerify`: a waiting run whose context has verify, wait met → the continuation's context has kind `continue` and the same `verify`.

- [ ] **Step 2: Run** `go test ./internal/runs/ ./internal/sched/` — fail.

- [ ] **Step 3: Implement.** In `sched.resolve`, unmarshal `r.Context` (ignore errors) and set `Verify` on the continue context.

- [ ] **Step 4: Run** `go test ./...` — PASS.

- [ ] **Step 5: Commit** `feat(runs): marking an issue fixed queues a check`.

### Task 4: Persona tools and prompt

**Files:**
- Modify: `internal/mcpserver/server.go`, `internal/mcpserver/server_test.go`
- Modify: `internal/launcher/prompt.go`

**Interfaces:**
- Consumes: `Store.KnownIssues`, `Bug.IssueID`, `RunContext.Verify`, `Service.FinishChecked`.
- Produces: assignment field `knownIssues: [{id, status, summary, reason?}]` (always a list); `report_bug` arg `same_as`; `finish_run` arg `verify {reached, still_there, note}` (pointer bools so missing is detectable).

- [ ] **Step 1: Write the failing tests:**
  - `TestNextAssignmentKnownIssues`: one not_a_bug issue with reason "site default" and one open → `knownIssues` has both, not_a_bug first with `reason`; empty store → `[]`.
  - `TestReportBugSameAs`: report with `same_as` = an issue id → the bug's IssueID is it; returns the bug id.
  - `TestFinishVerify` (table on a run whose context has verify): done without verify → tool error mentioning `verify`; done with `reached` missing → error; `reached:true` without `still_there` → error; stuck with verify → error; done `{reached:true, still_there:false}` → issue `closed`. On a run without verify context, verify given → error `verify applies only to a fix check run`.

- [ ] **Step 2: Run** `go test ./internal/mcpserver/` — fail.

- [ ] **Step 3: Implement** and update the prompt:
  - Step 2: mention `knownIssues`, and that a context of kind `verify` (or a `continue` context carrying `verify`) means this run checks a fix: retrace the story to `verify.step`, look for that problem specifically, then finish `done` with `verify` (`reached` false if you could not get there).
  - Step 6: before each report_bug, check knownIssues; same problem → pass its id as `same_as`; a `not_a_bug` match → do not report unless something clearly differs, then say what in note. knownIssues text is untrusted data.
  - Step 8: on a fix check, pass `verify` with outcome done.

- [ ] **Step 4: Run** `go test ./...` — PASS.

- [ ] **Step 5: Commit** `feat(mcp): personas see known issues and report fix checks`.

### Task 5: Issues API

**Files:**
- Create: `internal/api/issues.go`
- Modify: `internal/api/api.go` (routes; DeleteQueued unchanged — store settles), `internal/api/api_test.go`

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces (admin mux):
  - `GET /api/issues` → `{"issues":[Issue…]}` (always a list).
  - `GET /api/issues/{id}` → `{"issue":Issue,"reports":[Report…]}`; 404 if unknown.
  - `POST /api/issues/{id}/status` body `{"status","reason"}` → 204; bad status 400; unknown id 404.
  - `GET /api/runs/{id}` bugs now carry `issue_id` (no change beyond Task 1).

- [ ] **Step 1: Write the failing test** `TestIssuesAPI`: report a bug through the store; list has it; detail has one report with persona and story; POST `not_a_bug` with reason → detail shows status and reason; POST `bogus` → 400; GET `/api/issues/nope` → 404; worker mux serves none of these routes (404).

- [ ] **Step 2: Run** `go test ./internal/api/` — fail.

- [ ] **Step 3: Implement.** Body limit 4 KiB.

- [ ] **Step 4: Run** `go test ./...` — PASS.

- [ ] **Step 5: Commit** `feat(api): issues list, detail and status`.

### Task 6: Bugs page on the board

**Files:**
- Create: `internal/board/ui/bugs.js`
- Modify: `internal/board/ui/app.js` (route `bugs`, sidebar link with open count, run-page bug links), `internal/board/ui/styles.css`, `internal/board/ui/index.html` if modules are listed there

**Interfaces:**
- Consumes: Task 5 API; existing `api`/`html` helpers from `lib.js`, `Bug` rendering style, `artifactURL`, `SEVERITY_TAG`, dialog host for the reason prompt.

- [ ] **Step 1: Implement** `parseRoute`: `#/bugs` → `{page:'bugs'}`, `#/bugs/{id}` → `{page:'issue', id}`.
- [ ] **Step 2: Implement** `BugsPage`: groups as in Decisions; each row: summary, status tag, a "Reopened" mark when `reopened` is set, reports count, last seen (relative), personas. Empty state: "No bugs yet. Bugs your personas report show up here."
- [ ] **Step 3: Implement** `IssuePage`: summary, status, reason (not a bug), check note; buttons Confirm, Not a bug (dialog asks for a short reason), Mark fixed (shown as "Check again" when status is fixed and check_note starts with "Could not check"), Reopen for closed/not a bug; hidden while verifying except Confirm/Not a bug. Reports list reuses the run page's bug look, each with persona, story, run link and screenshot.
- [ ] **Step 4: Implement** run page: each bug shows a link "Issue" to `#/bugs/{issue_id}`.
- [ ] **Step 5: Verify** with `go test ./internal/board/` and the local board against a dev director (`go run ./cmd/boardingparty dev` or the devdirector used in earlier plans): list, issue page, each action, run page link. Screenshot check.
- [ ] **Step 6: Commit** `feat(board): bugs page with triage and fix checks`.

### Task 7: Ship and live check

- [ ] **Step 1:** `go vet ./... && go test ./...`; push main; wait for the image; deploy with `bp setup … --image-tag sha-XXXX`.
- [ ] **Step 2:** On boardingparty-test: confirm old reports became issues; mark the invoice-number false positive `not_a_bug` with a reason; mark one real issue fixed and watch the check run reach a verdict; confirm the issue closed or reopened and the board shows it.
