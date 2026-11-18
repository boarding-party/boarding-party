# Bug grouping Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An issue is read once: the bugs list shows severity, screenshot, where, how often and what needs attention; the issue page shows one main report in full and every report as a short marked row; the owner can pick the main report and split one out.

**Architecture:** Store gains `bugs.page`, `bugs.difference`, `bugs.match` and `issues.main_bug`; issue reads gain aggregates (severity, first seen, stories, page/step and screenshot of the main report, last "different" time). `report_bug` takes `page` and `difference`; known issues carry page, step, severity. Two new board routes (make main, split). The UI's pure rules (attention tag, row marking) live in a small module with node tests; `bugs.js` renders them.

**Tech Stack:** Go 1.x, SQLite (modernc), MCP go-sdk, Preact + htm (vendored), node:test.

**Spec:** `docs/superpowers/specs/2026-11-18-bug-grouping-design.md`

## Global Constraints

- Agent-written text (summary, step, evidence, page, difference) renders only as text through htm/Preact.
- GB English, sentence case, the board's existing tokens (`--bad`, `--wait`, `--idle-soft`, `.tag bad|wait|idle|run|sup|ok`).
- Severity order: high > medium > low > anything else. Issue severity = highest among its reports.
- Attention tag precedence: "Seen again after fix" > "New today" (first report within rolling 24 h) > "Something different" (a report marked different within rolling 24 h). At most one.
- No change to triage statuses, fix checks, digest, cloud cards.
- Commit as `git -c user.name="Tiny Systems" -c user.email="dev@tinysystems.io" commit`, no trailers. Never commit `.idea/`.
- Tests: `go test -race ./...` and `node --test 'internal/board/uitest/*.test.mjs'` from the repo root.

## Review Focus

1. An existing board database (issues with several reports, no new columns) opens: every issue gets the earliest report as main; old reports show no marking and no page line. Test: `TestMainBackfill` (Task 1).
2. Splitting out, or the lead joining away, an issue's main report: the next-oldest becomes main and the title follows it. Tests: `TestSplitMainMovesTitle`, `TestJoinAwayMainReport` (Task 2).
3. Split while the old issue is being fix-checked: old status, reopened, updated and check chain stay; new issue open. Test: `TestSplitKeepsOldIssueState` (Task 2).
4. A persona passes a full URL with a query or fragment as `page` (could hold a token): only the path is stored. Test: `TestReportBugPagePathOnly` (Task 3).
5. `difference` without `same_as` is refused; `same_as` naming an unknown issue opens a new issue whose row is "First report", never "Different". Tests: `TestReportBugDifferenceNeedsSameAs` (Task 3), `TestUnknownSameAsIsNoMatch` (Task 1).

---

### Task 1: Store — columns, marking on insert, issue aggregates

**Files:**
- Modify: `internal/store/schema.sql` (bugs, issues tables)
- Modify: `internal/store/store.go` (`Bug`, `bugsAddedColumns`, new `issuesAddedColumns`, `addColumns` backfill)
- Modify: `internal/store/issues.go` (`bugCols`, `scanBug`, `insertBug`, `issueSelect`, `queryIssues`, `IssueReports`, `Issue`)
- Test: `internal/store/issues_test.go`

**Interfaces:**
- Produces:
  - `Bug` fields `Page string \`json:"page,omitempty"\``, `Difference string \`json:"difference,omitempty"\``, `Match string \`json:"match,omitempty"\``.
  - Constants `MatchSame = "same"`, `MatchDifferent = "different"`, `MatchJoined = "joined"` (empty = no marking).
  - `Issue` fields `Main string \`json:"main"\``, `Severity string \`json:"severity"\``, `FirstSeen time.Time \`json:"firstSeen,omitzero"\``, `Page string \`json:"page,omitempty"\``, `Step string \`json:"step,omitempty"\``, `Stories []string \`json:"stories"\``, `DifferentAt time.Time \`json:"differentAt,omitzero"\``, `Shot *IssueShot \`json:"shot,omitempty"\``.
  - `type IssueShot struct { RunID string \`json:"runId"\`; Name string \`json:"name"\` }`.
  - `shotName(evidence string) string` — first match of `(?i)\bbug-\d+\.(?:png|jpe?g)\b` (same rule as the UI's `SHOT_TOKEN`), or "".
  - `Report` gains the new Bug fields through embedding.

- [ ] **Step 1: Write the failing tests** in `internal/store/issues_test.go`:

```go
func TestAttachMarksSameOrDifferent(t *testing.T) {
	// b1 opens issue; b2 same_as with empty Difference; b3 same_as with Difference "on /quote too"
	// IssueReports: b1.Match=="" ; b2.Match==MatchSame ; b3.Match==MatchDifferent && b3.Difference=="on /quote too"
	// Page round-trips: b1 Page "/invoice" stored and read back.
}

func TestUnknownSameAsIsNoMatch(t *testing.T) {
	// AddBug{IssueID:"nope", Difference:"x"} opens a new issue; stored Match=="" and Difference=="".
}

func TestIssueAggregates(t *testing.T) {
	// runs r1 (Ada, first-invoice), r2 (Bob, check-invoice)
	// b1 r1 low,  Page "/invoice", Step "Add line", Evidence "no image", Created t0
	// b2 r2 high, same_as, Difference "VAT 0%", Evidence "bug-2.png: x", Created t0+1h
	// is := onlyIssue: Main=="b1", Severity=="high", FirstSeen==t0, Page=="/invoice", Step=="Add line",
	// Stories==["check-invoice","first-invoice"] (sorted), DifferentAt==t0+1h,
	// Shot=={RunID:"r2", Name:"bug-2.png"} (main has no image: newest report with one).
}

func TestIssueShotPrefersMain(t *testing.T) {
	// b1 Evidence "bug-1.png", b2 (newer, same_as) Evidence "bug-2.png": Shot.Name=="bug-1.png".
	// Third case: no report has an image: Shot==nil.
}

func TestMainBackfill(t *testing.T) {
	// Build an old-shape DB the way TestBugsBackfill does (raw CREATE TABLE issues/bugs
	// without main_bug, page, difference, match), insert issue "i1" with bugs
	// "late" (created t0+1h, rowid first) and "early" (created t0). Open it with Open().
	// Issue("i1").Main=="early"; IssueReports rows have Match=="" and Page=="".
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd /Users/mtrofimenko/Work/boardingparty && go test ./internal/store -run 'TestAttachMarks|TestUnknownSameAs|TestIssueAggregates|TestIssueShot|TestMainBackfill'`
Expected: FAIL to compile (unknown fields `Page`, `Match`, `Main`…).

- [ ] **Step 3: Implement**
  - Schema: bugs gets `page TEXT NOT NULL DEFAULT ''`, `difference TEXT NOT NULL DEFAULT ''`, `match TEXT NOT NULL DEFAULT ''`; issues gets `main_bug TEXT NOT NULL DEFAULT ''`. Add the three to `bugsAddedColumns`; add `issuesAddedColumns = []addedColumn{{"main_bug", "main_bug TEXT NOT NULL DEFAULT ''"}}` and call `addMissing(tx, "issues", issuesAddedColumns)` in `addColumns`. Order matters: the existing `addedBugs["issue_id"]` backfill inserts issues, so run the main_bug backfill after it: when `main_bug` was added, or always cheaply, `UPDATE issues SET main_bug = (SELECT id FROM bugs b WHERE b.issue_id = issues.id ORDER BY b.created, b.rowid LIMIT 1) WHERE main_bug = ''`.
  - `insertBug`: new issue sets `main_bug = b.ID` and stores `Match=""`, `Difference=""`; attached sets `Match = MatchDifferent` when `strings.TrimSpace(b.Difference) != ""`, else `MatchSame`. Callers clip; insert does not.
  - `issueSelect` adds: `i.main_bug`; severity `(SELECT b.severity FROM bugs b WHERE b.issue_id = i.id ORDER BY CASE lower(b.severity) WHEN 'high' THEN 0 WHEN 'medium' THEN 1 WHEN 'low' THEN 2 ELSE 3 END, b.created DESC LIMIT 1)` with COALESCE ''; `MIN(b.created)`; main's `page`, `step`, `run_id`, `evidence`; `MAX(created) WHERE match='different'`; distinct stories (same `char(31)` pattern as personas). In Go: `Shot` from main's evidence via `shotName`; when empty, one query `SELECT run_id, evidence FROM bugs WHERE issue_id=? ORDER BY created DESC, rowid DESC` and take the first with a `shotName`.
  - `bugCols`/`scanBug`/`IssueReports` read the three new columns.

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/store`
Expected: PASS (all, including existing).

- [ ] **Step 5: Commit**

```bash
git add internal/store/schema.sql internal/store/store.go internal/store/issues.go internal/store/issues_test.go
git -c user.name="Tiny Systems" -c user.email="dev@tinysystems.io" commit -m "store: main report, page, difference and issue aggregates"
```

---

### Task 2: Store — make main, split, lead join marks

**Files:**
- Modify: `internal/store/issues.go` (add `SetMain`, `SplitReport`, `fixMain`, `ErrLastReport`)
- Modify: `internal/store/leads.go:241-268` (`JoinReport`)
- Test: `internal/store/issues_test.go`, `internal/store/leads_test.go`

**Interfaces:**
- Consumes: Task 1 columns and `Issue.Main`.
- Produces:
  - `var ErrLastReport = errors.New("the issue has only this report")`
  - `func (s *Store) SetMain(issueID, bugID string) error` — ErrNotFound when the bug is not in the issue. Sets `main_bug` and `summary` = that bug's summary. `updated` unchanged.
  - `func (s *Store) SplitReport(issueID, bugID string, now time.Time) (newIssueID string, err error)` — ErrNotFound when the bug is not in the issue; ErrLastReport when the issue has one report.
  - `func fixMain(tx *sql.Tx, issueID string) error` — if `main_bug` is not one of the issue's bugs and the issue has bugs: main = earliest (created, rowid), summary = its summary.

- [ ] **Step 1: Write the failing tests**

```go
func TestSetMain(t *testing.T) {
	// b1 "counter wrong" (main), b2 "counter off by one" same_as. SetMain(id,"b2"):
	// Issue.Main=="b2", Summary=="counter off by one", Updated unchanged.
	// SetMain(id,"ghost") == ErrNotFound; SetMain(otherIssue,"b2") == ErrNotFound.
}

func TestSplitReport(t *testing.T) {
	// b1 main, b2 same_as (Difference "on mobile", journey link JourneyID "j1"), b3 same_as.
	// nid, err := SplitReport(id, "b2", t0+5h)
	// new issue: Status open, Created==Updated==t0+5h, Main=="b2", Summary==b2.Summary, Reports==1
	// b2 in new issue: Match=="" , Difference=="" , JourneyID=="j1"
	// old issue: Reports==2, Main=="b1", Summary unchanged.
}

func TestSplitRefusesLastReport(t *testing.T) {
	// one-report issue: SplitReport == ErrLastReport; bug not in issue == ErrNotFound.
}

func TestSplitMainMovesTitle(t *testing.T) {
	// b1 "A" main (t0), b2 "B" (t0+1h), b3 "C" (t0+2h). SplitReport(id,"b1"):
	// old issue Main=="b2", Summary=="B".
}

func TestSplitKeepsOldIssueState(t *testing.T) {
	// old issue reopened and verifying with check_chain "c1" (use verifyRun/StartCheck helpers).
	// After split: old Status==IssueVerifying, Reopened, Updated and CheckChain unchanged.
}
```

In `leads_test.go`:

```go
func TestJoinMarksJoined(t *testing.T) {
	// JoinReport(b, target): moved bug Match==MatchJoined, Difference=="".
}

func TestJoinAwayMainReport(t *testing.T) {
	// issue X: b1 "A" main, b2 "B". JoinReport("b1", Y): X.Main=="b2", X.Summary=="B"; Y.Main unchanged.
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/store -run 'TestSetMain|TestSplit|TestJoinMarks|TestJoinAway'`
Expected: FAIL to compile (`SetMain`, `SplitReport`, `ErrLastReport` undefined).

- [ ] **Step 3: Implement** in one transaction each. `SplitReport` inserts the new issue with `newIssueID()`, status open, created/updated `now`, `main_bug` = bug; moves the bug with `match=''`, `difference=''`; calls `fixMain(tx, issueID)`; leaves the old issue's status, reopened, updated, check fields alone. `JoinReport` sets `match='joined', difference=''` on the moved bug and calls `fixMain(tx, old)` before the empty-issue delete.

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/store`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/issues.go internal/store/leads.go internal/store/issues_test.go internal/store/leads_test.go
git -c user.name="Tiny Systems" -c user.email="dev@tinysystems.io" commit -m "store: make a report main, split one into a new issue"
```

---

### Task 3: report_bug page and difference; richer known issues; prompt

**Files:**
- Modify: `internal/mcpserver/server.go` (`knownIssue`, `bugArgs`, `report_bug` handler, assignment fill)
- Modify: `internal/launcher/prompt.go:44` (step 6)
- Modify: `README.md:86` (report_bug row)
- Test: `internal/mcpserver/issues_test.go`, `internal/launcher/tiny_test.go` (prompt phrases list)

**Interfaces:**
- Consumes: `Bug.Page`, `Bug.Difference`, `Issue.Page/Step/Severity`.
- Produces: tool args `page` ("the path of the page where it happened, from the browser's page URL; leave out the query") and `difference` ("only with same_as: empty if it is the same as the known issue, else one line on what differs: another page, value, step, device or a new symptom"). `knownIssue` gains `Page`, `Step`, `Severity` (json `page`, `step`, `severity`, omitempty).
  - `const maxPage = 300`, `const maxDifference = 300`.
  - `func pagePath(raw string) string` — `url.Parse`; keep `.Path` only (drops scheme, host, query, fragment); on parse error cut at the first `?` or `#`; trim spaces; clip to `maxPage`.

- [ ] **Step 1: Write the failing tests**

```go
func TestReportBugPagePathOnly(t *testing.T) {
	// report_bug page "https://shop.test/invoice/new?key=abc#top" stores Page=="/invoice/new".
	// page "/quote?id=7" stores "/quote".
}

func TestReportBugDifferenceNeedsSameAs(t *testing.T) {
	// difference "x" without same_as: isErr, text contains "difference goes only with same_as".
}

func TestReportBugDifferenceOneLine(t *testing.T) {
	// same_as known, difference "on mobile\nalso the total" stored as "on mobile also the total";
	// a 1000-char difference is clipped to maxDifference.
}

func TestNextAssignmentKnownIssuesWhere(t *testing.T) {
	// an issue whose main report has Page "/invoice", Step "Add line", Severity "high":
	// knownIssues[0]["page"]=="/invoice", ["step"]=="Add line", ["severity"]=="high".
}
```

Add `"same_as and difference"` and `"page"` style phrases the prompt must carry to the `tiny_test.go` phrase list: `"pass page"`, `"difference"`.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/mcpserver ./internal/launcher -run 'TestReportBugPage|TestReportBugDifference|TestNextAssignmentKnownIssuesWhere|TestPrompt|TestTiny'`
Expected: FAIL (unknown args ignored, fields empty; phrases missing).

- [ ] **Step 3: Implement.** Handler: refuse `difference` without `same_as`; replace `\r`/`\n` runs with one space, trim, `store.Clip(…, maxDifference)`; `Page: pagePath(in.Page)`. Assignment: `Page: k.Page, Step: store.Clip(k.Step, maxKnownIssueText), Severity: k.Severity`. Prompt step 6 adds, after the same_as sentence: "Pass page: the path of the page where you saw it (from the page URL your browser shows, without the query). With same_as, also say in difference, in one line, what differs from the known issue (another page, value, step, device or a new symptom), or leave difference empty when it is the same. knownIssues give each issue's page, step and severity to match on." Keep the existing not_a_bug sentence, but point it at difference instead of note.

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/mcpserver ./internal/launcher`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcpserver/server.go internal/mcpserver/issues_test.go internal/launcher/prompt.go internal/launcher/tiny_test.go README.md
git -c user.name="Tiny Systems" -c user.email="dev@tinysystems.io" commit -m "report_bug: page and difference; known issues say where"
```

---

### Task 4: Board API — make main and split

**Files:**
- Modify: `internal/api/issues.go` (handlers `putIssueMain`, `splitIssue`)
- Modify: `internal/api/api.go:114-116` (routes)
- Modify: `internal/api/actor.go` (`auditActions`)
- Test: `internal/api/issues_test.go`

**Interfaces:**
- Consumes: `Store.SetMain`, `Store.SplitReport`, `store.ErrLastReport`, `store.ErrNotFound`.
- Produces:
  - `POST /api/issues/{id}/main` body `{"report":"<bug id>"}` → 204; 404 report not in issue; 415 non-JSON; 400 bad JSON. Audit `issue.main`.
  - `POST /api/issues/{id}/split` body `{"report":"<bug id>"}` → 200 `{"id":"<new issue id>"}`; 404 report not in issue; 409 with text "an issue with one report cannot be split" on ErrLastReport. Audit `issue.split`.

- [ ] **Step 1: Write the failing test**

```go
func TestIssueMainAndSplitAPI(t *testing.T) {
	// two reports b1 (main), b2 same_as.
	// POST main {"report":"b2"} == 204; GET issue: issue.main=="b2".
	// POST main {"report":"ghost"} == 404.
	// POST split {"report":"b1"} == 200, body id != old id; GET new issue reports == [b1].
	// POST split again on old issue (one report left) == 409.
	// Audit log has "issue.main" and "issue.split" entries (read like TestAudit… in actor_test.go does).
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/api -run TestIssueMainAndSplitAPI`
Expected: FAIL (404 from the mux for unknown routes).

- [ ] **Step 3: Implement** following `putIssueStatus`: media type check, `http.MaxBytesReader(w, r.Body, 4<<10)`, `fail(w, err)` for the rest (it already maps ErrNotFound to 404 — confirm in `api.go`; add the 409 case explicitly).

- [ ] **Step 4: Run tests**

Run: `go test -race ./internal/api`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/issues.go internal/api/api.go internal/api/actor.go internal/api/issues_test.go
git -c user.name="Tiny Systems" -c user.email="dev@tinysystems.io" commit -m "api: make a report main, split a report into a new issue"
```

---

### Task 5: UI rules — attention tag and row marking

**Files:**
- Create: `internal/board/ui/issue-rules.js` (no imports; pure)
- Test: `internal/board/uitest/issue-rules.test.mjs`

**Interfaces:**
- Produces:
  - `export const DAY_MS = 86400000`
  - `export function attention(issue, now)` → `{ label, tag } | null`. `issue.reopened` set and status not `closed`/`not_a_bug` → `{ label: 'Seen again after fix', tag: 'bad' }`; else `firstSeen` within `DAY_MS` of `now` → `{ label: 'New today', tag: 'run' }`; else `differentAt` within `DAY_MS` → `{ label: 'Something different', tag: 'sup' }`; else null. Times are ISO strings or absent; `Date.parse` and treat NaN as absent.
  - `export function marking(report, earliestId)` → `{ kind, text }`, precedence: `match === 'different'` → `{ kind: 'different', text: 'Different: ' + difference }`; `report.id === earliestId` → `{ kind: 'first', text: 'First report' }`; `'joined'` → `{ kind: 'joined', text: 'Same as before (joined by the lead)' }`; `'same'` → `{ kind: 'same', text: 'Same as before' }`; else `{ kind: '', text: '' }`.
  - `export function earliestOf(reports)` → id of the report with the smallest `created` (ties: last in the newest-first array).
  - `export function severityOf(s)` → `'high' | 'medium' | 'low' | ''` (lower-cased; `critical` maps to `high`).

- [ ] **Step 1: Write the failing tests**

```js
// node --test 'internal/board/uitest/*.test.mjs'
test('attention: reopened beats new beats different', ...);   // all three set → 'Seen again after fix'
test('attention: reopened but closed is not seen again', ...); // status closed, firstSeen old → null
test('attention: new today is rolling 24 hours', ...);        // firstSeen now-23h → 'New today'; now-25h → null
test('attention: something different within a day', ...);    // differentAt now-2h, firstSeen old → 'Something different'
test('marking: different stays visible even as the earliest', ...); // match different + earliest → kind 'different'
test('marking: first, joined, same, none', ...);
test('earliestOf picks the oldest created', ...);
```

- [ ] **Step 2: Run to verify it fails**

Run: `node --test 'internal/board/uitest/issue-rules.test.mjs'`
Expected: FAIL (module not found).

- [ ] **Step 3: Implement** `internal/board/ui/issue-rules.js`.

- [ ] **Step 4: Run tests**

Run: `node --test 'internal/board/uitest/*.test.mjs'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/board/ui/issue-rules.js internal/board/uitest/issue-rules.test.mjs
git -c user.name="Tiny Systems" -c user.email="dev@tinysystems.io" commit -m "board: rules for the attention tag and report marking"
```

---

### Task 6: UI — bugs list row, issue page, dev data

**Files:**
- Modify: `internal/board/ui/bugs.js` (`IssueGroup`, `IssuePage`, `Report`)
- Modify: `internal/board/ui/styles.css` (`.issue*` block near line 301, `.report*` near 314)
- Modify: `internal/devdirector/devdirector.go:266` (fake run's bug joins the previous fake bug's issue)

**Interfaces:**
- Consumes: Task 1 issue/report JSON (`main, severity, firstSeen, page, step, stories, differentAt, shot{runId,name}`, report `page, difference, match`), Task 4 routes, Task 5 `attention`, `marking`, `earliestOf`, `severityOf`.

- [ ] **Step 1: Bugs list row** (agreed mockup `bugs-list-v2.html`): `<a class="issue">` becomes a flex row: `.sevbar.sev-high|sev-medium|sev-low|sev-none` (4 px, colours `--bad`, `--wait`, `--line`); `.thumb` 168×105, `<img loading="lazy" src=${artifactURL(shot.runId, shot.name)} alt="">` with `object-fit: cover; object-position: top`, or an empty `.thumb.none` of the same size; body: top line severity tag (`SEVERITY_TAG`, label capitalised: High/Medium/Low, "Unrated" when empty) + status tag only outside the Open group + `attention()` tag; title clamped to two lines (`-webkit-line-clamp: 2`); muted line `page · step "…"` (each part only when set); muted line `personas · stories a, b` ("story" when one); right column: count (large), "report"/"reports", "First seen <fmtWhen>", "Last seen <fmtWhen>". Under 640 px: thumbnail 96×60, right column drops under the body as one line.

- [ ] **Step 2: Issue page.** Header: h1 title; lede = severity tag, status tag, "Seen again after fix <when>" when reopened, then "N reports by M personas · first <when> · last <when>". Main card (`reports.find(r => r.id === issue.main) || reports[reports.length-1]`): persona + story (link `runHref`) and time; "Where" page and step; "What happens" evidence; note; journey link as today; big screenshot on the right (existing `Shot`, height 520). Reports card: one `<button class="reprow" aria-expanded>` per report, newest first: persona, story, `marking()` text (`.reprow.different` tinted `--sup-soft`, difference in full), "Main" tag on the main report, time. Open row shows today's `Report` body under it plus, for a non-main report, `Make main` and, when `reports.length >= 2`, `Split into new issue`. Make main: POST `/main`, then refetch. Split: POST `/split`, then `location.hash = issueHref(id)`. Errors show in a `.note.bad` under the buttons. Triage panel unchanged.

- [ ] **Step 3: Dev data.** In the fake run, before `AddBug`, find the newest issue whose summary is the fake bug's summary (`Store.Issues()`); when found, set `IssueID` to it and `Page: "/"`; every second such report gets `Difference: "Also on the second list"`. So the dev board shows one grouped issue with same and different rows.

- [ ] **Step 4: Verify.** `go vet ./... && go test -race ./... && node --test 'internal/board/uitest/*.test.mjs'` → all PASS. Then `sh $S/devboard.sh`, start three fake runs on the dev board, and screenshot `#/bugs` and the issue page at 1440 and 390 px with the Playwright harness (`BP_PW_ROOT=$S/pwroot/…`, pattern of `$S/oshot.mjs`). Expected: one issue row with severity bar, thumbnail, "New today", "3 reports"; issue page with main card, one tinted "Different: Also on the second list" row, Make main and Split working (split row lands on a new issue page).

- [ ] **Step 5: Commit**

```bash
git add internal/board/ui/bugs.js internal/board/ui/styles.css internal/devdirector/devdirector.go
git -c user.name="Tiny Systems" -c user.email="dev@tinysystems.io" commit -m "board: issue rows with severity and screenshot; issue page reads once"
```
