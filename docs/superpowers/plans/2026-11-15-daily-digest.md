# Daily Digest Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One plain-text email per cloud user each morning with the news from every project in their accounts, built from a new read-only director endpoint.

**Architecture:** The director (open-source repo `boarding-party/boardingparty`) gains `GET /api/digest?since=`, which decides what counts as news and returns it in a public type `pkg/digest.Digest`. The cloud (`boarding-party/cloud`) bumps its pin to the release with that type. A job there, run once a day under a Postgres advisory lock, reads each linked board over the existing forward, renders one email per user and sends it through the existing SMTP sender. The cloud stores only who got an email on which day, and when each project was last read.

**Tech Stack:** Go 1.26, SQLite (director store), Postgres via pgx (cloud), `text/template`, `net/smtp`.

**Spec:** `docs/superpowers/specs/2026-11-15-daily-digest-design.md`

## Global Constraints

- One plain-text email per user per day, at 07:30 Europe/London. Send nothing when none of a user's projects has news.
- Lists are capped at 10 lines per section, then "and N more" with a link to the list on the board.
- Every email carries an opt-out link and a `List-Unsubscribe` header. The account toggle turns it on and off.
- `since` is capped at 7 days back.
- The cloud calls the board with `X-BP-Actor: digest`.
- No run content in the email: no screenshots, page text, mailboxes, keys or site passwords. Titles and names only.
- Money in dollars, two decimals, as the board shows it.
- GB English in all copy.
- Commits: `git -c user.name="Tiny Systems" -c user.email="dev@tinysystems.io" commit`, with no trailers.
- No `replace` directives in go.mod.

## Rulings on the spec (made while planning)

- **Fix checks come from issues, not from `fix check` decisions.** Those decisions exist only for replay checks; AI fix checks leave none. The store sets `reopened` when a check finds the bug still there, and sets `check_note` to "Gone: …" with `status = closed` when it's gone.
  - Passed = closed, note starts with "Gone:", `updated` in the window.
  - Failed = `reopened` in the window.
  - Cost if wrong: a bug closed by hand after a passed check still shows as passed.
- **Journeys broke = decisions `copilot called` in the window.** That decision is written together with the `replay_failed` run context, so it is the spec's source.
- **The cloud keeps `projects.digest_at`**, the time the job last read that board. The spec's per-project `since` needs it. It's only a time, so the anti-goal holds.
- **The cloud keeps `digest_runs(day)`**, so a second replica or a restart later the same day does nothing. Without it, the job would read every board again for nothing.
- **Opt-out is a POST.**
  - GET `/digest/off?t=` shows a one-button page. Mail scanners follow GET links, and a GET that switched the email off would opt people out by accident.
  - POST does the change, and serves RFC 8058 one-click as well (`List-Unsubscribe-Post`).
  - The cross-origin guard lets this one path through, because the token is the authority.
- **The opt-out token** is `<user id>.<base64url HMAC-SHA256>`. The key is HMAC(seal key, "digest opt-out"); signing uses the current seal key and checking accepts any seal key.
- **The toggle goes on the team page**, because the cloud has no account page.
- **Subject wording:**
  - Counts are summed across projects.
  - With one project, its name goes in brackets; with several, "N projects".
  - Only non-zero counts appear, in this order: new bugs, fix checks failed, journeys broke, stuck runs. If none of those are non-zero, the subject is "boardingparty: news from <project or N projects>".

## Review Focus

1. **A board that's down for weeks** gets a "couldn't reach" line every day. Per the spec, that is news, so the line stays. Test: the unreachable-only user gets one line naming the cluster and the time it was last reached.
2. **A board running an older director** answers 404 to `/api/digest`. It has to show as "couldn't read: the board needs updating", not as a crash or a silent skip. Test in Task 4.
3. **A bad `since`** (unparseable, in the future, or more than 7 days back) gets 400, or is clamped to 7 days. Test in Task 3.
4. **A forged or tampered opt-out token, and a token for a user who no longer exists**, are refused, and nothing changes. Test in Task 7.
5. **A crash between sending and recording.** The `digest_sends` row has to be written before the next user is handled, so a restart resends at most one email. Test in Task 6 (a second run sends nothing).

---

## Part A: director (repo `/Users/mtrofimenko/Work/boardingparty`)

### Task 1: Public digest type and store queries

**Files:**
- Create: `pkg/digest/digest.go`
- Modify: `internal/store/journeys.go` (add `DecisionsSince`)
- Modify: `internal/store/store.go` (add `RunsFinishedSince`)
- Test: `internal/store/digest_test.go`

**Interfaces:**
- Produces, in `pkg/digest`:
  ```go
  const MaxItems = 10
  type List[T any] struct { Items []T `json:"items"`; More int `json:"more"` }
  type Bug struct { ID, Title, Severity string } // json: id,title,severity
  type FixCheck struct { IssueID, Title string; Passed bool } // issueId,title,passed
  type Journey struct { ID, Persona, Story string } // id,persona,story
  type Run struct { ID, Persona, Story, State string } // id,persona,story,state
  type Journeys struct { Broke, Repaired List[Journey] }
  type Runs struct { Finished int; Stuck List[Run] }
  type Digest struct {
      Since, Until time.Time
      NewBugs List[Bug]; FixChecks List[FixCheck]; Journeys Journeys; Runs Runs
      Spent, Budget float64; OverBudget, Paused bool
  }
  func Cap[T any](all []T) List[T]   // first MaxItems, More = rest; Items never nil
  func (d Digest) News() bool        // any item, any finished run, or OverBudget/Paused
  ```
  JSON field names are exactly as in the spec's response.
- Produces, in `store`:
  ```go
  func (s *Store) DecisionsSince(since time.Time, kinds ...string) ([]Decision, error) // created >= since, oldest first
  func (s *Store) RunsFinishedSince(since time.Time) ([]Run, error) // finished >= since, final states only, oldest first
  ```

- [ ] **Step 1: Write the failing tests** `TestDecisionsSinceFiltersKindAndTime` and `TestRunsFinishedSince`. Seed:
  - decisions of kind "copilot called" before and after `since`, plus one of kind "replayed" after it;
  - runs: one done before, one done after, one failed after, one still running.

  Assert that only the in-window rows of the asked kinds come back, oldest first. Also add `TestCapCountsTheRest` in `pkg/digest/digest_test.go`: 13 items give 10 items and More 3, and nil gives empty Items with More 0.
- [ ] **Step 2: Run them.** Command: `go test ./internal/store/ ./pkg/digest/ -run 'DecisionsSince|RunsFinishedSince|Cap'`. They should fail because the functions don't exist yet.
- [ ] **Step 3: Implement.**
  - `DecisionsSince` reuses the `decisions` scan, with `created >= ? AND kind IN (…)` and ascending order. Give `decisions()` an order argument, or build the query alongside it.
  - `RunsFinishedSince` uses `finished >= ? AND state IN ('done','failed','stuck')`.
- [ ] **Step 4: Run them.** Same command. They should pass.
- [ ] **Step 5: Commit.** `store: decisions and finished runs since a time; pkg/digest type`

### Task 2: `runs.Service.Digest`

**Files:**
- Create: `internal/runs/digest.go`
- Modify: `internal/runs/cost.go` (add `SpentBetween`)
- Test: `internal/runs/digest_test.go`

**Interfaces:**
- Consumes: Task 1.
- Produces:
  ```go
  func (s *Service) SpentBetween(from, to time.Time) (float64, error) // runs started in [from, to)
  func (s *Service) Digest(since, now time.Time) (digest.Digest, error)
  ```

Rules, from the spec and the rulings above:
- **NewBugs:** `FirstFinds(since)`. Title = issue summary; Severity = the severity of the first report (`IssueReports(id)[0]`).
- **FixChecks:**
  - Failed: issues with `reopened >= since`.
  - Passed: issues with status closed, `CheckNote` starting "Gone:", and `updated >= since`.
  - Oldest first.
- **Journeys.Broke:** `DecisionsSince(since, "copilot called")`, one entry per journey.
- **Journeys.Repaired:** `DecisionsSince(since, runs.DecideSiteChanged, runs.DecideWeakness, "rolled back")`, one entry per journey.
  - Persona and story come from `Journey(id)`.
  - Skip a deleted journey (ErrNotFound).
- **Runs:**
  - Finished = `len(RunsFinishedSince(since))`.
  - Stuck = those whose state is stuck or failed.
- **Spend:** `SpentBetween(yesterday's local midnight, today's local midnight)` in the project's timezone (`DayStart`). Budget = `p.Budget()`; OverBudget = spent >= budget; Paused = `Store.Paused()`.
- Until = now.

- [ ] **Step 1: Write the failing tests**, one per section, plus an empty one:
  - `TestDigestNewBugs`
  - `TestDigestFixChecks`: one passed and one failed.
  - `TestDigestJourneys`: one broke and one repaired, plus a "replayed" decision that must not show.
  - `TestDigestRuns`: 2 done and 1 stuck give Finished 3 and Stuck 1.
  - `TestDigestSpendIsYesterday`: a run yesterday costing $2 and one today costing $1 give Spent 2.
  - `TestDigestEmptyWindowHasNoNews`: `News()` is false and every Items is non-nil.
  - `TestDigestCarriesNoBodies`: marshal to JSON and assert the seeded bug evidence string and the run outcome text are absent.

  Seed through the store, as `internal/runs` tests already do.
- [ ] **Step 2: Run them.** Command: `go test ./internal/runs/ -run Digest`. They should fail.
- [ ] **Step 3: Implement.** Use `digest.Cap` for every list.
- [ ] **Step 4: Run them.** Same command. They should pass.
- [ ] **Step 5: Commit.** `runs: digest of a window: new bugs, fix checks, journeys, runs, spend`

### Task 3: `GET /api/digest`, then release

**Files:**
- Modify: `internal/api/api.go` (route next to `GET /api/value`, handler `getDigest`)
- Test: `internal/api/digest_test.go`

**Interfaces:**
- Consumes: `svc.Digest(since, now)`.
- Produces: `GET /api/digest?since=<RFC 3339>`, which answers `200 digest.Digest`.
  - No `since` means 24 hours ago.
  - More than 7 days back is clamped to 7 days.
  - An unparseable value, or one later than now, gets 400 with "since must be an RFC 3339 time in the past".

- [ ] **Step 1: Write the failing test.** `TestDigestEndpoint`:
  - 200 with a seeded new bug in `newBugs.items`;
  - `since` 30 days back answers with `since` about 7 days back;
  - `since=x` and `since=<tomorrow>` give 400.

  Use `setup` and `do` from `api_test.go`.
- [ ] **Step 2: Run it.** Command: `go test ./internal/api/ -run TestDigestEndpoint`. It should fail with 404.
- [ ] **Step 3: Implement the handler.**
- [ ] **Step 4: Run the whole suite.** Command: `go test -race ./...`. Everything should pass.
- [ ] **Step 5: Commit and push to main, then wait for CI.** Commit message: `api: GET /api/digest for the cloud's morning email`.
- [ ] **Step 6: Tag `v0.1.1` on that commit and push the tag**, once CI is green. Then deploy `boardingparty-test` with `KUBECONFIG=$S/kube-tiny.json bash $S/deploy.sh <sha7>`, after checking that no run is active. Check `GET /api/digest` through `bpmac board`.

## Part B: cloud (repo `/Users/mtrofimenko/Work/boardingparty-cloud`)

DB tests skip locally, because `CLOUD_TEST_DATABASE_URL` isn't set and Docker doesn't answer on this Mac. CI runs them against Postgres 17, so every task's DB tests are proven by the CI run of its push. Push after each task.

### Task 4: Pin bump, schema and reading a board's digest

**Files:**
- Modify: `go.mod`/`go.sum` (`github.com/boarding-party/boardingparty v0.1.1`)
- Modify: `internal/db/schema.sql`
- Create: `internal/proxy/digest.go`
- Test: `internal/proxy/digest_test.go`

Schema to add, using the same idempotent style:
```sql
alter table users add column if not exists digest boolean not null default true;
alter table projects add column if not exists digest_at timestamptz;
create table if not exists digest_sends (user_id text not null references users(id) on delete cascade,
	day date not null, sent_at timestamptz not null, primary key (user_id, day));
create table if not exists digest_runs (day date primary key, finished timestamptz not null);
```

**Interfaces:**
- Produces:
  ```go
  // ReadDigest reads p's board for the morning email. state is "ok" or a
  // Summary state ("unreachable", "no_board", "refused", "error"), plus
  // "outdated" when the board answers 404 (a director without the endpoint).
  func (h *Handler) ReadDigest(ctx context.Context, p project.Project, since time.Time) (digest.Digest, string)
  ```
  It reuses `upstream` and `read`, as user `auth.User{ID: "digest", Email: "digest"}`, so `X-BP-Actor` is `digest`.

- [ ] **Step 1: Write the failing tests.** `TestReadDigest` uses the fixture `newFix` with a board answering `/api/digest`:
  - check the state is ok and the decoded fields are right;
  - check the request carried `X-BP-Actor: digest` and a `since` query equal to the time passed in.

  `TestReadDigestOldBoard`: a 404 gives state "outdated".
- [ ] **Step 2: Run them.** Command: `go test ./internal/proxy/ -run ReadDigest`. They should fail.
- [ ] **Step 3: Implement.** Give `read` a way to report 404 distinctly, for example a sentinel `errNotFound`.
- [ ] **Step 4: Run them.** Command: `go vet ./... && go test ./...`. Everything should pass.
- [ ] **Step 5: Commit and push.** Message: `digest: read a board's digest; schema for sends and opt-out`. Check that CI is green, which also proves the schema applies twice.

### Task 5: Rendering the email

**Files:**
- Create: `internal/digest/render.go`
- Test: `internal/digest/render_test.go`, `internal/digest/testdata/email.golden`

**Interfaces:**
- Produces:
  ```go
  type Board struct {
      Name, Host string        // Host: "<slug>.<domain>"
      State string             // from ReadDigest
      Cluster string           // cluster name, for states other than ok
      LastReached *time.Time   // clusters.last_reached
      D digest.Digest
  }
  func (b Board) News() bool   // State != "ok" || b.D.News()
  // Render returns subject and body; offURL goes in the footer.
  func Render(boards []Board, offURL string, loc *time.Location) (subject, body string)
  ```
- The layout follows the spec's sample exactly. Section headings are "New bugs", "Fix checks", "Journeys" and "Runs"; a section with nothing is left out.
- "Spent yesterday: $1.20 of $5.00", then " (over budget)" or " (paused)" when true.
- The "couldn't reach" lines, by state:
  - unreachable: "Couldn't reach the board since 03:10. Check the cluster "<name>"." Use "Couldn't reach the board. …" when there is no time.
  - no_board: "No board is running in its namespace."
  - refused: "The board refused the cloud's key."
  - outdated: "The board needs updating to send a digest."
  - error: "Couldn't read the board."
- "and N more" links to `https://<host>/#/bugs`, `#/journeys` or `#/runs`.
- Footer: "Stop these emails: <offURL>".

- [ ] **Step 1: Write the failing tests.**
  - `TestRenderGolden`: two boards, one with every section and one unreachable, compared to `testdata/email.golden`, with `-update` to rewrite it.
  - `TestRenderSubject`: one project with 2 new bugs and 1 broken journey gives "boardingparty: 2 new bugs, 1 journey broke (Shop staging)"; two projects give "(2 projects)"; singular and plural are checked.
  - `TestRenderMore`: More 14 renders "and 14 more" with the list link.
- [ ] **Step 2: Run them.** Command: `go test ./internal/digest/ -run Render`. They should fail.
- [ ] **Step 3: Implement** with `text/template`, or a `strings.Builder` if that reads more simply.
- [ ] **Step 4: Run them with `-update` once, read the golden file by eye against the spec sample, then run without `-update`.** They should pass.
- [ ] **Step 5: Commit.** Message: `digest: render the morning email`.

### Task 6: The morning job

**Files:**
- Create: `internal/digest/job.go`
- Test: `internal/digest/job_test.go` (DB, uses `db.ForTest`)

**Interfaces:**
- Consumes: `ReadDigest` (behind an interface, so tests fake it), `Render`, and `Mailer`, which Task 7 adds to `auth`. Until then, declare it here as an interface:
  ```go
  type Reader interface { ReadDigest(ctx context.Context, p project.Project, since time.Time) (digest.Digest, string) }
  type Mailer interface { SendList(ctx context.Context, to, subject, body, offURL string) error }
  type Job struct {
      DB *db.DB; Boards Reader; Mail Mailer; Projects *project.Service
      Origin, Domain string; Sign func(userID string) string; Now func() time.Time
  }
  func (j *Job) RunOnce(ctx context.Context) error  // one day's job; no-op if digest_runs has today
  func (j *Job) Loop(ctx context.Context)           // checks every minute; runs once after 07:30 London
  ```

What `RunOnce` does, in order:
1. Take `pg_try_advisory_lock(<constant>)` on one pooled connection. If the lock isn't won, return nil. Release it on exit.
2. Set `day` = the London date of `Now()`. If `digest_runs` already has that day, return.
3. Read every project in the `projects` table once: `since` = `digest_at`, or 24 hours ago if that is null. Join the cluster name and `last_reached`.
4. Find the users with `digest = true`, then each user's projects through their memberships. Skip users who already have a `digest_sends` row for `day`.
5. For each user, build the boards that have news.
   - If there are none, move on.
   - Otherwise: `Render` it, send with `SendList`, and insert the `digest_sends` row straight after the send succeeds.
   - A send that fails is logged, and the job goes on to the next user.
6. Update `digest_at = until` for each project that read ok. Insert into `digest_runs`.

The opt-out link is `Origin + "/digest/off?t=" + Sign(userID)`.

- [ ] **Step 1: Write the failing tests:**
  - `TestJobSendsOneEmailPerUser`: one user in two accounts gets one email naming both projects.
  - `TestJobQuietUserGetsNothing`
  - `TestJobSecondRunSameDaySendsNothing`
  - `TestJobSkipsOptedOut`
  - `TestJobUnreachableBoardIsNews`: the line names the cluster.
  - `TestJobNextSinceIsLastRead`: after a run, the reader is called with since = the previous until.
  - `TestLoopWaitsFor0730`: the `Now` hook at 07:29 London does nothing, and at 07:31 it runs.
- [ ] **Step 2: Run them.** Command: `go test ./internal/digest/`. Locally they skip; confirm they compile with `go vet ./...`.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run `go vet ./... && go test ./...` locally, then commit, push and wait for CI.** CI runs the DB tests, which should pass.
- [ ] **Step 5: Commit.** Message: `digest: the morning job, once per day under an advisory lock`.

### Task 7: Opt-out, List-Unsubscribe, toggle, wiring

**Files:**
- Modify: `internal/auth/mail.go`: add `SendList` to `smtpSender` and `logSender`.
  - Extra headers: `List-Unsubscribe: <offURL>` and `List-Unsubscribe-Post: List-Unsubscribe=One-Click`.
  - Refuse an offURL that contains CR or LF.
- Create: `internal/digest/token.go`:
  ```go
  func Signer(keys map[string][]byte, current string) (sign func(string) string, verify func(string) (string, bool))
  ```
- Modify: `internal/web/web.go`, plus the templates `digestoff.html` and `team.html`.
  - Routes: `GET /digest/off`, `POST /digest/off` (one-click and button), and `POST /digest` (`signedIn`, sets the current user's `digest` from the form value `on`).
  - In `guard`, let `POST /digest/off` through without the Origin check.
  - The team page shows "Daily email: on/off" with a button.
- Modify: `cmd/cloud/main.go`: build the `Signer` from `cfg.SealKeys`, start `go job.Loop(ctx)`.
- Test: `internal/digest/token_test.go`, `internal/web/web_test.go`, `internal/auth/auth_test.go` (or a new `mail_test.go`).

- [ ] **Step 1: Write the failing tests.**
  - `TestTokenRoundTrip`
  - `TestTokenRefusesTamperedAndOtherKey`: change one character; sign with a key not in the set.
  - `TestTokenAcceptsOldKeyAfterRotation`
  - Web:
    - `TestDigestOffGetChangesNothing`
    - `TestDigestOffPostTurnsItOff`, with no Origin header, as RFC 8058 sends it.
    - `TestDigestOffRefusesForgedToken` (400, nothing changes)
    - `TestTeamPageToggle`
  - Mail: `TestSendListRefusesLineBreak`.
- [ ] **Step 2: Run them.** Command: `go test ./...`. They should fail; the DB ones skip locally.
- [ ] **Step 3: Implement and wire it up.**
- [ ] **Step 4: Run `go vet ./... && go test ./...`, then commit and push. CI should be green.** Message: `digest: one-click opt-out, team page toggle, start the job`.

### Task 8: Live check

- [ ] Before the cloud image deploys, which happens on its own through Flux's `main-N` ImagePolicy, check the `users` table in `bpcloud` with a read-only query. Every user with the digest on gets real email from 07:30 the next day. If anyone other than the owner is in that table, stop and tell the user before pushing Task 7.
- [ ] Once it's deployed: confirm the `bpcloud` pod logs show the job loop starting. Check that the `boardingparty-test` board answers `/api/digest` through the cloud by looking at the log line from a run. To run the job by hand once, start a one-off run, which needs a way in such as `cloud digest-now`. That is a small subcommand: add it to Task 7's `main.go` as `if len(os.Args) > 1 && os.Args[1] == "digest-now"`, run it with `kubectl exec`, and confirm before running, because it sends real email. Then read the email at hello@tinysystems.io.
