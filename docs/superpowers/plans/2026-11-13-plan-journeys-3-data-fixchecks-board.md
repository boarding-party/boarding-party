# Journeys Plan 3: generated data, fix checks by replay, board pages

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replays draw seeded UK test data with edge cases mixed in, a fix check of a bug linked to a journey is a replay, and the board gets Journeys and Journey pages.

**Architecture:** Generators live in a new worker module `bp-data.mjs`, reached through the existing `data` object. They have no dependencies, so a seed draws the same values on every image. Bug reports gain a durable link to a journey revision and step. `markFixed` queues a replay for a linked report, and the replay's result becomes the fix check's verdict. Board pages are a new `journeys.js`, served by the existing journey API plus two small additions.

**Tech Stack:** Go director, SQLite (modernc), Node + Playwright worker, Preact/htm board.

**Spec:** `docs/superpowers/specs/2026-11-12-journeys-design.md`, sections "Generated data", "Fix checks" and "Board". Plans 1 and 2 are done and deployed.

## Global Constraints

- The UI word is "journey", never "script". GB English in all copy.
- Commits use `git -c user.name="Tiny Systems" -c user.email="dev@tinysystems.io"`, with no Co-Authored-By or Generated trailers. Push to main. Never commit `.idea/`. No Go `replace` directives.
- Go tests run with `-race`: `go test -race ./...`.
- Board work uses the frontend-design skill and keeps the existing visual system: compact functional UI, `px-3 py-2 text-sm` density, no new fonts.
- Agent-written journey code is untrusted. The board shows it only as text (htm text nodes, never `innerHTML` or `dangerouslySetInnerHTML`).
- Emails in generated data are only plus-addresses on the persona's inbox (`data.plusEmail`, already shipped). Phone numbers come only from Ofcom's drama range (`07700 900000` to `07700 900999`).
- Edge cases go into 30% of replays, one or two per replay (spec, "my call").

## Review Focus

1. **A journey whose own run was refused an edge case, then excluded it:** the same seed must draw every other value exactly as before. Tested in Task 1 (`exclude keeps other draws`).
2. **The continuation of a fix-check replay that waits:** the continuation must still carry the verify context and settle the issue. Tested in Task 4 (`TestFixCheckReplayContinuationSettles`).
3. **A fix-check replay dropped from the queue (journey deleted or paused after Mark fixed):** the issue must not stay "verifying" for ever. Tested in Task 4 (`TestDroppedFixCheckReplaySettles`).
4. **An issue whose newest report has no journey link but an older one does:** the replay check uses the linked report. Tested in Task 4 (`TestMarkFixedPrefersLinkedReport`).
5. **A revision diff of a very long journey:** the page stays responsive. Above 2000 lines on either side it shows "Too long to compare here." instead of diffing. Checked in Task 6, step 4.

---

### Task 1: Generated data and edge cases (worker)

**Files:**
- Create: `worker/bp-data.mjs`
- Modify: `worker/bp-journey-lib.mjs` (`makeData`, `makeBp`), `worker/bp-replay.mjs` (`seedOf` in self mode), `worker/Dockerfile` (COPY line), `worker/README.md` (helpers paragraph)
- Test: `worker/smoke/journey-lib-smoke.mjs`

**Interfaces:**
- Produces: `data.*` generators (below), `edgeReplay(seed) → boolean` exported from `bp-data.mjs`, `bp.totalWithVat(lines, rate = 0.2) → number`, `bp.ukDate(iso) → 'DD/MM/YYYY'`. Inputs record every value under `<name><n>`. When an edge case is used, `inputs.edges` lists `"<name><n>:<id>"`, for example `"quantity2:zero"`.

Generators. All take an options object. Every one that can carry an edge case accepts `exclude: [ids]` and `edges: false`.

| call | returns | edge ids |
|---|---|---|
| `data.personName()` | `"Amira Patel"` | `apostrophe` ("Siobhan O'Brien"), `accents` ("Zoë Ågren"), `leading-space` ("  Tom Hale") |
| `data.businessName({ size })` | `size` is `'sole'` ("Hale Plumbing"), `'small'` (default, "Mill & Kerr Ltd") or `'firm'` (a long "… Holdings Limited") | `long` (exactly 120 characters), `leading-space`, `line-break` ("Hale & Sons\nPlumbing") |
| `data.address()` | `{ line1, town, postcode }`, town and postcode from one real pair | `line-break` in line1 ("Flat 2\n14 Mill Lane") |
| `data.postcode()` | `"LS1 4AP"` | none |
| `data.phone()` | `"07700 900123"` | none |
| `data.vatNumber()` | `"GB"` plus 9 digits passing HMRC's mod-97 check | none |
| `data.companyNumber()` | 8 digits | none |
| `data.price({ min = 1, max = 500, round = false })` | 2-decimal number (whole pounds when `round`) | `tiny` (0.01), `huge` (999999.99) |
| `data.quantity({ min = 1, max = 10 })` | integer | `zero` (0) |
| `data.date({ from = -30, to = 30 })` | ISO date, days relative to today in Europe/London | `leap-day` (the 29 February nearest today) |
| `data.dateRange({ from = -30, to = 0, minDays = 7, maxDays = 30 })` | `{ start, end }` ISO dates | `reversed` (end before start) |
| `data.lineItems({ min = 1, max = 4, round = false, maxPrice = 500 })` | `[{ description, quantity, price }]` | its own `description` edges `line-break` and `leading-space`, plus the edges of its `quantity` and `price` draws |

Edge mixing (the order of draws decides reproducibility, so it is pinned here):
- Normal values come from the existing stream `rng(seed)`. Edge decisions come from a second stream, `rng(seed ^ 0x2545F491)`.
- On its first use, the edge stream decides whether this is an edge replay (`r < 0.3`) and its budget (`r < 0.5` gives 1, otherwise 2). `edgeReplay(seed)` returns the first of these.
- An edge-capable call always draws its normal value first. It then always takes two numbers from the edge stream: `a`, then `b`. If this is an edge replay, budget remains, `a < 0.5`, and at least one id is left after `exclude` and `edges: false`, the call returns `ids[floor(b * ids.length)]`'s value and spends one from the budget.
- So excluding a case never moves any other value drawn from the same seed.

- [ ] **Step 1: Write failing smoke checks** in `journey-lib-smoke.mjs`:
  - **same seed:** two `makeData(42)` objects running the same sequence (`personName`, `businessName({size:'firm'})`, `address`, `phone`, `vatNumber`, `companyNumber`, `lineItems({max:6})`, `date`, `dateRange`) give identical `inputs`.
  - **shapes:** phone matches `/^07700 900\d{3}$/`; the VAT number passes a mod-97 checker written in the smoke; the company number matches `/^\d{8}$/`; the `long` edge is exactly 120 characters; `date` matches `/^\d{4}-\d{2}-\d{2}$/` and lies within `from..to` of today.
  - **rate:** across seeds 1 to 2000, `edgeReplay` is true for 25% to 35% of them. Every edge replay that makes 6 or more edge-capable calls uses 1 or 2 edges, and a non-edge replay uses 0.
  - **exclude keeps other draws:** find a seed whose first `quantity()` gives `zero`. With `quantity({exclude:['zero']})` it must give a non-zero quantity, and every other key in `inputs` (`edges` aside) must be equal.
  - **edges: false:** never yields an edge.
  - **helpers:** `bp.totalWithVat([{quantity:2, price:10}], 0.2) === 24` and `bp.ukDate('2026-02-09') === '09/02/2026'`.
- [ ] **Step 2: Run and see it fail.** Run: `node worker/smoke/journey-lib-smoke.mjs`. Expected: an error that `data.personName` is not a function.
- [ ] **Step 3: Implement `bp-data.mjs`.** It exports `edgeReplay(seed)` and `generators(nextNormal, nextEdge, keep, edgeKeep)`. The name, street, town/postcode and business word lists are plain arrays in the file, with at least 40 entries per list. Then spread the generators into `makeData` and add `totalWithVat` and `ukDate` to `makeBp`. In `bp-replay.mjs` self mode with no `--seed`, redraw (at most 100 times) until `!edgeReplay(seed)`. The AI proves the happy path; scheduled replays and `--seed N` keep whatever their seed decides. Add `bp-data.mjs` to the Dockerfile's COPY line.
- [ ] **Step 4: Run and see it pass.** Run `node worker/smoke/journey-lib-smoke.mjs`; expected: `ok journey-lib`. Docker hangs on this Mac, so CI runs the image smokes.
- [ ] **Step 5: Commit** `worker: seeded UK test data with edge cases for journeys`.

### Task 2: Teach generators to the recording and copilot prompts

**Files:**
- Modify: `internal/launcher/prompt.go` (step 7 text, `copilotStep`)
- Test: `internal/launcher/prompt_test.go`

**Interfaces:** consumes the generator names and edge ids from Task 1. Produces prompt text only.

- [ ] **Step 1: Write a failing test**, `TestPromptTeachesGenerators`. For a normal run, `Prompt(r)` contains `data.lineItems(`, `data.businessName({ size`, `exclude` and `fits the persona`. For a `replay_failed` run, `Prompt(r)` contains `input refused correctly` and `exclude: ['`.
- [ ] **Step 2: Run** `go test -race ./internal/launcher -run TestPromptTeachesGenerators`. Expected: FAIL.
- [ ] **Step 3: Edit the prompt text.**
  - **Recording (step 7):** list the generators in one sentence. Tell the agent to choose options that fit the persona, for example a sole trader `size: 'sole'`, `lineItems({ max: 1, round: true })`; a bookkeeper `size: 'firm'`, `lineItems({ min: 3, max: 8 })`. The persona's own identity (name, business, email) stays fixed from the assignment.
  - **Copilot:** for **input refused correctly**, add the edge id from `context.replay.inputs.edges` to that call's `exclude`, for example `data.quantity({ exclude: ['zero'] })`.
- [ ] **Step 4: Run** `go test -race ./internal/launcher`. Expected: PASS.
- [ ] **Step 5: Commit** `launcher: teach journeys the data generators`.

### Task 3: Link bug reports to journey revisions and steps

**Files:**
- Modify: `internal/store/schema.sql`, `internal/store/store.go` (`bugsAddedColumns`, `Bug`), `internal/store/issues.go` (`bugCols`, `scanBug`), `internal/store/journeys.go` (`AddRevision`, the decision path that applies `BlockBug`)
- Test: `internal/store/journeys_test.go`

**Interfaces:**
- Produces: `Bug.JourneyID string` (`json:"journeyId,omitempty"`), `Bug.JourneyRev int` (`json:"journeyRev,omitempty"`), `Bug.JourneyStep int` (`json:"journeyStep,omitempty"`). The bugs table gains columns `journey_id TEXT NOT NULL DEFAULT ''`, `journey_rev INTEGER NOT NULL DEFAULT 0` and `journey_step INTEGER NOT NULL DEFAULT 0`. A helper `linkBug(tx *sql.Tx, bugID, journeyID string, rev, step int) error` sets them only while `journey_id = ''` (the first link wins).

- [ ] **Step 1: Write failing tests.**
  - `TestBugRevisionLinksItsReport`: `AddRevision` of kind bug with `BugID: "b1", BugStep: 3` leaves `IssueReports` / `Bug b1` with `JourneyID == j.ID`, `JourneyRev == rev.N` and `JourneyStep == 3`.
  - `TestCopilotBugDecisionLinksReport`: the decision path with `Resolves`, `BlockStep: 4`, `BlockBug: "b2"` links b2 to the journey's live revision and step 4.
  - `TestBugLinkFirstWins`: a second link attempt leaves the first link in place.
- [ ] **Step 2: Run** `go test -race ./internal/store -run 'Bug.*Link|Links'`. Expected: FAIL to compile on `JourneyID`.
- [ ] **Step 3: Implement** the columns, the struct fields, the scan, and `linkBug` called in both transactions.
- [ ] **Step 4: Run** `go test -race ./internal/store ./internal/api ./internal/mcpserver`. Expected: PASS.
- [ ] **Step 5: Commit** `store: link bug reports to the journey step that shows them`.

### Task 4: Fix checks by replay

**Files:**
- Modify: `internal/store/store.go` (`ContextVerify`), `internal/store/issues.go` (`Verdict`, `settleCheck`), `internal/runs/issues.go` (`markFixed`), `internal/runs/replay.go` (`FinishReplay`, new `replayVerdict`), `internal/sched/replays.go` (`followReplay` skips fix checks), `internal/launcher/prompt.go` (verify text mentions `replayNote`)
- Test: `internal/runs/fixcheck_test.go` (new), `internal/sched/replays_test.go`

**Interfaces:**
- Consumes: `Bug.JourneyID/JourneyRev/JourneyStep` (Task 3); `Service.replayable`, `NewSeed`, `Store.StartCheck` (existing).
- Produces:
  - `ContextVerify` gains `JourneyID string` (`json:"journeyId,omitempty"`), `Revision int` (`json:"revision,omitempty"`), `JourneyStep int` (`json:"journeyStep,omitempty"`) and `ReplayNote string` (`json:"replayNote,omitempty"`).
  - `Verdict` gains `Next *Queued`. When it is set, `settleCheck` inserts it in the same transaction (its `Chain` equals the check's chain) and leaves the issue verifying.
  - `replayVerdict(v store.ContextVerify, res ReplayResult) (verdict store.Verdict, handOver bool)`.

Rules (from the spec, "Fix checks"), with step N = `JourneyStep`:
- failed at step N: still there. Reached = true, StillThere = true, Note `"The replay failed at step N again: <stepName>"`.
- passed, or failed at a step after N: gone. Reached = true, Note `"The replay got past step N."`.
- anything else (failed before N, no result, timeout): the check hands over to today's AI fix check. That means the same persona and story, a verify context with `ReplayNote` `"A replay of revision R stopped at step M (<stepName>) before step N: <error, clipped 300>"`, and the same chain.
- **Ruling, for review:** the spec says "any other failure goes to the copilot". For a fix check, the AI fix-check run plays that role. It retraces the story and can report again or save a new bug revision. The journey copilot repairs live revisions only, and a bug revision is never live.
- Every settled replay check writes a journey decision: kind `fix check`, actor `director`, reason `"Issue <id>: Gone"`, `"Still there"` or `"handed to an AI run"`.
- A fix-check replay (context kind `verify`) is marked followed by `followReplays` with no flake check and no copilot.

- [ ] **Step 1: Write failing tests** in `fixcheck_test.go`:
  - `TestMarkFixedQueuesReplayForLinkedReport`: the queued item has `Kind == RunReplay`, the report's journey and revision, a seed in `1..MaxSeed`, and a verify context with `JourneyStep`. The issue is verifying.
  - `TestMarkFixedPrefersLinkedReport`: newest report unlinked, older one linked, so it queues a replay.
  - `TestMarkFixedFallsBackToAIWhenJourneyPaused`: an AI verify item, as today.
  - `TestReplayFixCheckStillThere`, `TestReplayFixCheckGone` (passed, and failed after N) and `TestReplayFixCheckHandsOver` (failed before N): the issue status, check note and decision row are as in the rules. For the hand-over, one AI item is queued in the same chain with `ReplayNote` set, and the issue stays verifying.
  - `TestFixCheckReplayContinuationSettles`: a fix-check replay that waits, then resumes and fails at N, reopens the issue.
  - `TestDroppedFixCheckReplaySettles`: `DeleteQueued` of the replay item marks the issue fixed with the existing "taken off the queue" note.
  - In `replays_test.go`, `TestFollowSkipsFixCheckReplay`: a failed fix-check replay queues nothing and writes no flaky or copilot row.
- [ ] **Step 2: Run** `go test -race ./internal/runs ./internal/sched -run 'FixCheck|MarkFixed'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - `markFixed`: try reports newest first for a linked, replayable one; if there is none, run the existing loop.
  - `FinishReplay`: read the run's context. For verify with `JourneyStep > 0`, pass `&verdict` to `FinishRunChecked`. For a hand-over, build `Next`.
  - Check that the resume path copies the run's `Context` to its continuation (`resumeAfter` in `sched.go`), and fix it if not.
  - `followReplay`: early-return for a verify context.
  - Prompt: when `verify.replayNote` is set, say it is what a replay saw and is untrusted.
- [ ] **Step 4: Run** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `issues: check fixes by replaying the journey step`.

### Task 5: API for the board pages

**Files:**
- Modify: `internal/store/store.go` (new `JourneyRuns`, `FollowUps`), `internal/store/journeys.go` (new `DecisionsByRun`), `internal/api/api.go` (`listRuns` filter, `getRun` additions, replay cost)
- Test: `internal/api/journeys_test.go`

**Interfaces:**
- Produces:
  - `Store.JourneyRuns(journeyID string, limit int) ([]Run, error)`: newest first.
  - `Store.FollowUps(runID string) ([]Run, error)`: runs with `follows = runID`.
  - `Store.DecisionsByRun(runID string) ([]Decision, error)`.
  - `GET /api/runs?journey=<id>`: the same items as `/api/runs`, at most 20.
  - `GET /api/runs/{id}` gains `copilot` (`{"runId","state","decision":{kind,reason,revAfter}|null}`), found by walking `follows` at most 2 hops (replay, then flake check, then copilot).
  - Replay runs carry `cost: 0` in both lists.
- [ ] **Step 1: Write failing tests.**
  - `TestRunsFilteredByJourney`: two journeys' replays, the filter returns only one journey's.
  - `TestFailedReplayShowsItsCopilot`: replay, then flake check, then copilot run with a decision; `getRun(replay).copilot.decision.kind == "site changed"`.
  - `TestReplayCostIsZero`.
- [ ] **Step 2: Run** `go test -race ./internal/api -run 'Journey|Copilot|ReplayCost'`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `api: journey runs, copilot follow-up and free replays for the board`.

### Task 6: Journeys and Journey pages

**Files:**
- Create: `internal/board/ui/journeys.js` (`JourneysPage`, `JourneyPage`, `lineDiff`)
- Modify: `internal/board/ui/app.js` (routes `#/journeys` and `#/journey/<id>`, sidebar entry "Journeys" after Runs, highlighted on both routes), `internal/board/ui/styles.css`

**Interfaces:**
- Consumes: `GET /api/journeys`, `GET /api/journeys/{id}`, `POST /api/journeys/{id}/{pause|resume|rollback|replay}`, `DELETE /api/journeys/{id}`, `GET /api/runs?journey=` (Task 5); `confirmDialog`, `api`, `fmtWhen`, `runHref` from `lib.js`.
- Produces: `journeyHref(id) → '#/journey/<id>'`, exported for Task 7.

Content (spec "Board"):
- **Journeys page:** one row per journey with persona, story, state (Live / Paused / Repairing), revision, replay record ("41 passed, 2 failed"), last replay. Empty state: "No journeys yet. An AI run records one when it finishes a story."
- **Journey page:**
  - The live revision's code, read only, in a `<pre>` text node.
  - Revisions, newest first, each with kind, reason, created and the run that wrote it. Each one expands to a line diff against the previous revision (n − 1, as the spec says); revision 1 shows "First revision".
  - The decision timeline: kind, reason, actor, time, revisions before and after.
  - Recent replays, linking to the run pages.
  - Actions: Pause/Resume; Replay with a revision picker (newest first; success shows "Replay queued"); Roll back with a revision picker of full revisions other than the live one, behind a confirm; Delete behind a confirm saying revisions go and decisions stay, then go to `#/journeys`.

- [ ] **Step 1: Load the frontend-design skill.** Write the short token and layout plan, kept within the existing visual system.
- [ ] **Step 2: Implement `lineDiff(a, b) → [{op: ' '|'+'|'-', text}]`** (LCS over lines). If either side has more than 2000 lines, return `null`; the page then shows "Too long to compare here."
- [ ] **Step 3: Build both pages and the routes.**
- [ ] **Step 4: Verify** with `go run ./cmd/boardingparty board --dev-director --no-open` and Chrome:
  - Screenshot both pages at 1280 px and 390 px wide.
  - Pause, resume, replay and roll back show their result.
  - Delete returns to the list.
  - A journey whose code contains `<img src=x onerror=alert(1)>` renders it as text.
  - A pasted 2500-line revision shows "Too long to compare here."
  - `go test -race ./internal/board` passes.
- [ ] **Step 5: Commit** `board: Journeys and Journey pages`.

### Task 7: Run, replay and bug page links

**Files:**
- Modify: `internal/board/ui/app.js` (`RunPage`, `ReplayCard`, runs list cost), `internal/board/ui/bugs.js` (`Report`)

**Interfaces:**
- Consumes: `copilot` and `cost: 0` (Task 5); `Bug.journeyId/journeyRev/journeyStep` (Task 3); `journeyHref` (Task 6).
- [ ] **Step 1: Make the changes.**
  - Replay runs show the cost as "$0" in the runs list, the run cards and the run page.
  - The run page of a replay links its journey ("Journey: ann · checkout, revision 3" to `journeyHref`).
  - A failed replay with a `copilot` shows "Copilot: <state>" linking the copilot run. When the copilot has decided, it adds the decision kind and reason.
  - `ReplayCard` gets a button "Replay with these inputs" (POST replay with the run's revision and seed). It shows "Replay queued" or the error.
  - A bug report with `journeyId` shows "Journey revision R, step S", linking the Journey page.
- [ ] **Step 2: Verify** with the dev director and Chrome: screenshots of a failed replay's run page, the button queuing a replay, and an issue page showing the journey link.
- [ ] **Step 3: Commit** `board: link replays, copilots and bug reports to their journeys`.

### Task 8: Deploy and prove on the cluster

- [ ] **Step 1:** Push; wait for CI to pass; Flux rolls out the new director and worker tags. Check both with `kubectl --context gke_tinyplatform_europe-west2-a_cluster-1 -n <bp ns> get deploy bp-director -o jsonpath='{..image}'`, prefixed with `CLOUDSDK_ACTIVE_CONFIG_NAME=tinyplatform CLOUDSDK_CORE_ACCOUNT=hello@tinysystems.io`.
- [ ] **Step 2:** Start one replay of the cluster project's live journey from the board. Expected:
  - the run's inputs show generated values;
  - the pod's command holds only `--claim-url` and `--seed`;
  - the run passes or fails with a recorded step.
- [ ] **Step 3:** Open the Journeys and Journey pages on the cluster board and screenshot them.
- [ ] **Step 4:** Do not mark a real issue fixed just to prove the fix check: the end-to-end proof is Task 4's tests. Say so in the report.
