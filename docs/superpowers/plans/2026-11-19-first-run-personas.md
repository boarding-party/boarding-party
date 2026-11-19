# First-run personas Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A new project's owner gives the site and plain-word stories; the board creates a first-run lead that makes 1–6 segment-spread personas and runs them, with optional logins up front and a "waiting for a login" unblock later.

**Architecture:** Everything rides on the existing lead. `POST /api/setup` writes one project file (site, a "First run" role with a lead persona, logins, budget, inbox); the scheduler's first wake starts the lead. Logins are project-level, named by kind; a persona points at one with `login`, and `store.Project()` overlays the login onto its identity. A new wait kind `login` parks a run until the owner saves a login on the run page.

**Tech Stack:** Go (director, SQLite store, MCP go-sdk), Preact/htm board UI with node:test rules, cloud Go service (separate repo).

**Spec:** `docs/superpowers/specs/2026-11-18-first-run-personas-design.md`

## Global Constraints

- Personas: default 4, 1 to 6; "6 at most" means live children (existing cap, `MaxChildren`).
- Stories on the setup screen: 1 to 5 lines, each at most 200 characters.
- Money per day: default $5 (`project.DefaultLeadBudget`), more than 0, at most 1000.
- Lead runs per day = personas × stories, at most 30.
- Lead brief fits `project.MaxLeadBrief` (2000).
- Role name "First run", lead persona name "First-run lead".
- The lead never reads or writes identity values; it sees login kinds only.
- GB English in all copy. Commits: `git -c user.name="Tiny Systems" -c user.email="dev@tinysystems.io"`, no trailers. Never commit `.idea/`.
- Every Go task ends green on `go vet ./... && go test -race ./...`; UI tasks also on `node --test 'internal/board/uitest/*.test.mjs'`.

## Review Focus

1. Setup on a project file that exists but has no roles (CLI user saved sites only): Start keeps its other fields (timezone, model, extraHosts) and adds to them. Test in Task 5.
2. Start pressed twice (double click, two tabs): the second gets 409 and no second lead appears. Test in Task 5.
3. Saving a login on a run that is not waiting, or waiting for an email: 409, nothing saved. Test in Task 6.
4. Mailbox username that is not an email address (plain IMAP user): the lead gets no email, Start and mailbox save still succeed. Tests in Tasks 5 and 6.
5. Site on a multi-part suffix (`https://staging.shop.example.co.uk`): `allowFrom` gets `example.co.uk`. Test in Task 1.

---

### Task 1: Project model: logins, child email, first-run brief

**Files:**
- Create: `internal/project/firstrun.go`, `internal/project/firstrun_test.go`
- Modify: `internal/project/project.go` (Project, Persona, validation), `internal/mcpserver/leads.go` (remove `childEmail`, call `project.ChildEmail`)

**Interfaces:**
- Produces:
  - `type Login struct { Kind string json:"kind"; Username string json:"username"; Password string json:"password" }`
  - `Project.Logins []Login json:"logins,omitempty"`; `Persona.Login string json:"login,omitempty"` (names a `Login.Kind`)
  - `func (p Project) LoginFor(kind string) (Login, bool)` (kind matched case-insensitively)
  - `func (p Project) LoginKinds() []string`
  - `func ChildEmail(leadEmail, name string) string` (moved verbatim from mcpserver)
  - `func SiteDomain(site string) string` (registrable domain via publicsuffix, "" when unparsable)
  - `func FirstRunBrief(stories []string, personas int) string`
  - consts `FirstRunRole = "First run"`, `FirstRunLead = "First-run lead"`, `MaxFirstRunStories = 5`, `MaxFirstRunStory = 200`, `MaxFirstRunPersonas = 6`, `DefaultFirstRunPersonas = 4`, `MaxFirstRunRuns = 30`, `MaxLoginKind = 40`, `MaxLoginField = 200`

- [ ] **Step 1: Write failing tests** in `firstrun_test.go`:
  - `TestLoginsValidate`: kind empty → error; kind of 41 chars → error; two kinds "Admin" and "admin" → error mentioning duplicate; username or password over 200 → error; a persona with `login: "owner"` and no such kind → error naming the persona; a valid login loads and `LoginFor("ADMIN")` finds it.
  - `TestChildEmail` (move the existing mcpserver test cases if any; else): `ChildEmail("bp+old@x.com","Priya Shah") == "bp+priya.shah@x.com"`, `ChildEmail("not an address","A") == ""`.
  - `TestSiteDomain`: `https://staging.shop.example.co.uk/app` → `example.co.uk`; `https://app.example.com` → `example.com`; `"::"` → `""`.
  - `TestFirstRunBriefFits`: 5 stories of 200 chars and 6 personas → `len(brief) <= MaxLeadBrief`; brief contains every story, the persona count, the words "age", "device", "tech confidence", and "login".
- [ ] **Step 2: Run** `go test ./internal/project/ -run 'Login|ChildEmail|SiteDomain|FirstRunBrief'` — Expected: FAIL, undefined symbols.
- [ ] **Step 3: Implement.** Validation lives in the existing project normalise path (same place `Inbox.normalise` is called); trim kind and username. `FirstRunBrief` is fixed English copy: the stories as a numbered list, then the rules from the spec's "segment spread" section (one child per persona up to the count; each kind of user a story needs gets at least one persona; spread age, device, tech confidence, at most one of reading/assistive/vision; if more kinds than personas, a persona takes several kinds' stories and you say so in the reason; put the kind of user in the child's traits; when a login kind fits a child's kind of user, pass it as `login` to child_create; queue one story per child, the next after it finishes).
- [ ] **Step 4: Run** `go vet ./... && go test -race ./internal/project/ ./internal/mcpserver/` — Expected: PASS.
- [ ] **Step 5: Commit** `project: logins by kind, child email and the first-run brief`.

### Task 2: Store: login overlay and login waits

**Files:**
- Modify: `internal/store/leads.go` (`Project()`), `internal/store/waits.go`, `internal/store/store.go` (`waitsAddedColumns`)
- Test: `internal/store/leads_test.go`, `internal/store/waits_test.go`

**Interfaces:**
- Consumes: `Project.LoginFor`, `Persona.Login` (Task 1)
- Produces:
  - `Wait.Login string json:"login,omitempty"`, `Wait.Note string json:"note,omitempty"`; columns `login`, `note` (TEXT NOT NULL DEFAULT '')
  - `func (s *Store) GiveLogin(runID string, at time.Time) (bool, error)`: sets `at` on an open `login` wait; false if none
  - `Project()` overlay: for every persona (hand-made and merged child) whose `Login` names a project login, the returned identity is a copy with `username` and `password` set from the login; other keys (email, name) kept.

- [ ] **Step 1: Write failing tests:** `TestProjectOverlaysLogin` (hand-made persona and a child both with `login: "admin"` get `identity["username"]`/`["password"]` from the login, email unchanged; `RawProject()` unchanged); `TestProjectLoginMissingKindNoOverlay` (child with a login kind not in the project keeps its generated identity); `TestGiveLogin` (open login wait → true and `At` set; second call → false; email wait → false; login/note round-trip through `FinishRunWaiting` and `OpenWaits`).
- [ ] **Step 2: Run** `go test ./internal/store/ -run 'Overlay|LoginMissing|GiveLogin'` — Expected: FAIL.
- [ ] **Step 3: Implement** (add columns to `waitCols` and the scan/insert paths).
- [ ] **Step 4: Run** `go vet ./... && go test -race ./internal/store/` — Expected: PASS.
- [ ] **Step 5: Commit** `store: login identity overlay and login waits`.

### Task 3: Login wait rule and persona prompt

**Files:**
- Modify: `internal/sched/cond.go`, `internal/launcher/prompt.go`
- Test: `internal/sched/cond_test.go`, `internal/launcher/prompt_test.go`

**Interfaces:**
- Consumes: `Wait.Login/Note`, `GiveLogin` sets `At` (Task 2)
- Produces: `Until.Login *LoginCond`; `type LoginCond struct { Kind string json:"kind"; Note string json:"note,omitempty" }`; `Describe` for login: `a login for "<kind>"`; `Met` for login: met when `!w.At.IsZero()`, `Arrival.Detail` = `login for "<kind>" given`.

- [ ] **Step 1: Write failing tests:** `TestParseWaitLogin` (kind required; kind over 40 → error; note over 200 → error; timeout honoured; "exactly one of email, handoff, time or login" when two set); `TestMetLogin` (not met with zero `At`; met after `At` set); `TestDescribeLogin`; `TestRunPromptTeachesLoginWait` (prompt contains `login {kind, note}` and tells the persona to wait for a login rather than invent an account or report a bug).
- [ ] **Step 2: Run** `go test ./internal/sched/ ./internal/launcher/ -run Login` — Expected: FAIL.
- [ ] **Step 3: Implement.** Prompt line 57: add "or login {kind, note} when the story needs an account you do not have and cannot make, such as an admin (kind is the kind of user, note says what for)". Login waits never come from replays (journeys cannot wait for a login; say so beside "A journey cannot wait for a hand-off").
- [ ] **Step 4: Run** `go vet ./... && go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `sched: a run can wait for a login`.

### Task 4: Lead sees login kinds; child_create takes a login

**Files:**
- Modify: `internal/mcpserver/leads.go`, `internal/launcher/prompt.go` (lead prompt line 66)
- Test: `internal/mcpserver/leads_test.go`

**Interfaces:**
- Consumes: `Project.LoginKinds`, `Persona.Login` (Task 1)
- Produces: `leadAssignment.Logins []string json:"logins"` (kinds only, never values); `childInfo.Login string json:"login,omitempty"`; `childArgs.Login string json:"login,omitempty"` on child_create and child_change.

- [ ] **Step 1: Write failing tests:** `TestLeadAssignmentListsLoginKindsOnly` (assignment JSON has `"logins":["admin"]` and contains neither the username nor the password); `TestChildCreateWithLogin` (login "Admin" → child persona `Login == "admin"` canonical kind; unknown kind → tool error listing the kinds); `TestChildChangeLogin` (sets and clears with "").
- [ ] **Step 2: Run** `go test ./internal/mcpserver/ -run 'LoginKinds|WithLogin|ChangeLogin'` — Expected: FAIL.
- [ ] **Step 3: Implement.** Lead prompt: "logins lists the kinds of user the owner gave an account for; pass one as login to child_create for a child of that kind. Children sharing a login can sign each other out, so give a login to as few children as the stories need."
- [ ] **Step 4: Run** `go vet ./... && go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `leads: login kinds in the assignment and on children`.

### Task 5: Setup API and staging URL from the cloud

**Files:**
- Create: `internal/runs/setup.go`, `internal/runs/setup_test.go`, `internal/api/setup.go`, `internal/api/setup_test.go`
- Modify: `internal/api/api.go` (routes), `internal/runs/runs.go` (Service field), `cmd/director/frame.go` and `cmd/director/main.go` (read `stagingURL` from bp-project)

**Interfaces:**
- Consumes: Task 1 consts, `FirstRunBrief`, `SiteDomain`, `Login`
- Produces:
  - `type SetupRequest struct { Site string json:"site"; Stories []string json:"stories"; Personas int json:"personas"; DailyBudget float64 json:"dailyBudget"; Logins []project.Login json:"logins" }`
  - `var ErrSetUp = errors.New("the project is already set up")`
  - `func (s *Service) Setup(ctx context.Context, req SetupRequest, mailbox string) error` (mailbox = saved IMAP username, "" when none)
  - `Service.StagingURL func(ctx context.Context) string` (nil-safe)
  - `GET /api/setup` → `{"needed": bool, "stagingURL": string}`; `POST /api/setup` → 204, 400 (`ErrInvalid` text), 409 (`ErrSetUp`)
  - bp-project key `stagingURL`

- [ ] **Step 1: Write failing tests** (`runs/setup_test.go` against a real store):
  - `TestSetupWritesFirstRunLead`: 2 stories, 4 personas, $7 → raw project has `sites`, role "First run" with persona "First-run lead", powers seeRuns+createPersonas+startRuns, `MaxChildren 4`, `DailyRuns 8`, `DailyBudget 7`, brief == `FirstRunBrief(stories, 4)`, project `DailyBudget` 7 (raised from default 5), `Inbox.AllowFrom == ["example.com"]` for site `https://staging.example.com`.
  - `TestSetupRunsCapped`: 5 stories × 6 personas → `DailyRuns 30`.
  - `TestSetupKeepsExistingFields`: existing project with timezone, model, extraHosts and no roles → all kept; existing `Inbox` kept as is; existing higher project budget not lowered.
  - `TestSetupRefusesSetUpProject`: project with a role → `ErrSetUp`; calling Setup twice → second `ErrSetUp`, one lead.
  - `TestSetupLeadEmailFromMailbox`: mailbox `bp@example.com` → lead identity email; mailbox `bpuser` → no email key, no error.
  - `TestSetupValidates`: 0 or 6 stories, a 201-char story, blank story, 0 or 7 personas, budget 0 or 1001, bad site → `ErrInvalid`.
  - `api/setup_test.go`: `TestSetupEndpoints` (GET needed true on empty, false after POST; stagingURL echoes `Service.StagingURL`; POST twice → 204 then 409).
- [ ] **Step 2: Run** `go test ./internal/runs/ ./internal/api/ -run Setup` — Expected: FAIL.
- [ ] **Step 3: Implement.** Setup builds the project, marshals and re-loads it with `project.LoadJSON` (one validation path), then `SaveProject`. "needed" = no project or no roles. Project name when new: the site's host. Director: generalise `frameSource` into a cached bp-project reader with `Value(ctx, key)`; `Frame` and `svc.StagingURL` both read through it.
- [ ] **Step 4: Run** `go vet ./... && go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `setup: start a new project from its site and stories`.

### Task 6: Give a login to a waiting run; fill emails when a mailbox arrives

**Files:**
- Create: `internal/runs/logins.go`, `internal/runs/logins_test.go`
- Modify: `internal/api/api.go` (route + wait view gets `login`, `note`), `internal/api/inbox.go` (`putMailbox` calls `FillEmails` after a successful save)
- Test: `internal/api/api_test.go`

**Interfaces:**
- Consumes: `GiveLogin` (Task 2), `ChildEmail`, `FirstRunLead` (Task 1)
- Produces:
  - `var ErrNoLoginWait = errors.New("this run is not waiting for a login")`
  - `func (s *Service) GiveLogin(ctx context.Context, runID, username, password string) error`: upserts project login for the wait's kind (replacing username/password of an existing kind), sets the waiting persona's `Login` (child: `UpdateChild`; hand-made: project file), then `Store.GiveLogin`.
  - `func (s *Service) FillEmails(ctx context.Context, address string) error`: if `address` parses as an email, every lead persona without an email gets it; every live child without an email whose lead has one gets `ChildEmail`.
  - `POST /api/runs/{id}/login` body `{username, password}` → 204, 400 (empty or over 200), 409 (`ErrNoLoginWait`).

- [ ] **Step 1: Write failing tests:** `TestGiveLoginResumes` (waiting child run → login saved under kind, child `Login` set, wait `At` set; the scheduler's next resolve queues a "continue" run); `TestGiveLoginHandmade` (persona in project file gets `login`); `TestGiveLoginRefused` (done run, email wait → `ErrNoLoginWait`, project unchanged); `TestFillEmails` (lead without email gets address; child gets plus-address; existing emails untouched; `bpuser` → no change); `TestWaitViewShowsLogin` (run JSON `wait.kind == "login"`, `login`, `note`); `TestLoginEndpoint` (204/400/409).
- [ ] **Step 2: Run** `go test ./internal/runs/ ./internal/api/ -run 'GiveLogin|FillEmails|WaitViewShowsLogin|LoginEndpoint'` — Expected: FAIL.
- [ ] **Step 3: Implement.** The password never appears in any response or log line.
- [ ] **Step 4: Run** `go vet ./... && go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `runs: unblock a run with a login; personas get email once a mailbox is saved`.

### Task 7: Board: setup page, login form, sidebar

**Files:**
- Create: `internal/board/ui/setup.js`, `internal/board/ui/setup-rules.js`, `internal/board/uitest/setup-rules.test.mjs`
- Modify: `internal/board/ui/app.js` (route `#/setup`, sidebar link and "need a login" count, run page login form), `internal/board/ui/project.js` (export `AgentKeyCard`; extract `MailboxFields` from `InboxCard` and export it), `internal/board/ui/styles.css`, `internal/cli/board.go` + `internal/devdirector/devdirector.go` (hidden `--dev-empty`: dev director with no project)

**Interfaces:**
- Consumes: `GET/POST /api/setup` (Task 5), `POST /api/runs/{id}/login` and `wait.kind/login/note` (Task 6), `GET /api/agent-key`, `GET /api/mailbox`
- Produces (setup-rules.js): `parseStories(text) -> {stories: string[], errors: string[]}` (one per line, trimmed, blanks dropped; errors for 0, more than 5, or a line over 200 with its line number); `runsPerDay(personas, stories) -> number` (min(personas×stories, 30)); `costLine(budget, personas, stories) -> string`; `loginRowsValid(rows) -> string` ("" or the first problem: missing field, duplicate kind).

- [ ] **Step 1: Write failing node tests** for the four rules with the spec's limits (5 lines ok, 6 → error "At most 5 stories"; 201-char line → error naming line 2; `runsPerDay(6,5) === 30`; `runsPerDay(4,2) === 8`; duplicate kind "Admin"/"admin" → error).
- [ ] **Step 2: Run** `node --test internal/board/uitest/setup-rules.test.mjs` — Expected: FAIL.
- [ ] **Step 3: Implement** rules, then the page in the spec's order (site, agent key if not saved, mailbox if not saved with the skip warning, stories, personas 1–6 default 4, logins rows, money per day default 5 with the cost line, Start, "Set up by hand instead" → `#/project`). After Start: `location.hash = leadHref('First-run lead')`. Sidebar "Not set up yet" and a project with no roles link to `#/setup`. Run page: when `wait.kind === 'login'` and open, a form (username, password, "Save and carry on") under the waiting line; on 204 reload the run. Sidebar Runs gets a second count `need a login` when any run waits for one. Lead page: a child whose latest run waits for a login shows "Waiting for a login: <kind>" linking to that run. `#/setup` on a project that is already set up shows one line saying so with a link to Project.
- [ ] **Step 4: Run** node suite + `go vet ./... && go test -race ./...`; then `--dev-empty` dev board and Playwright screenshots (setup page desktop and 390px, lead page after Start). Expected: tests pass; screenshots match the spec's order, no horizontal scroll at 390px.
- [ ] **Step 5: Commit** `board: setup page for a new project and a login form on waiting runs`.

### Task 8: Cloud writes the staging URL

**Files (repo boardingparty-cloud):**
- Modify: `internal/project/project.go` (`Register` data map gains `"stagingURL": stagingURL`; `writeProjectConfig` merges into existing `cur.Data` instead of replacing it)
- Test: `internal/project/project_test.go`

- [ ] **Step 1: Write failing tests:** `TestRegisterWritesStagingURL` (ConfigMap has `stagingURL`); `TestWriteProjectConfigKeepsOtherKeys` (an existing key not in the new map survives).
- [ ] **Step 2: Run** `go test ./internal/project/` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go vet ./... && go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `cloud: tell the board the staging URL`.

### Task 9: Ship and verify

- [ ] Push boardingparty main; wait for CI green. Check no bp-run or session pods in boardingparty-test; `sh $S/deploy.sh <sha7>`; wait for rollout.
- [ ] Live board (boardingparty-test is already set up): sidebar unchanged, `GET /api/setup` says `needed:false`, `#/setup` says the project is set up and links to Project. No run is started.
- [ ] Push cloud main; flux rolls it out; confirm the cloud pod runs the new image.
- [ ] Report; note that cloud-installed projects get the setup page once the cloud offers a director release containing it (release tagging not in this plan).
