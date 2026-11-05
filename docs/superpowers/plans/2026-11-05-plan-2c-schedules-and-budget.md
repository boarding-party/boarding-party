# Plan 2c: Activity Schedules, Daily Budget, Pause and Stop — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Personas act on their own on each role's activity pattern, spend is capped per day, and a human can pause the cast and stop a run.

**Architecture:** A new `internal/activity` package parses a role's plain activity text ("every 30 min, 09:00–17:30") into a pattern that yields jittered due times. The scheduler gains a phase that queues each due persona's next story. `runs.Service` gains a single start gate (paused, over budget) used by manual starts and the scheduler alike; spend is priced from the usage reports workers already send. The board shows spend vs budget, Pause/Resume and Stop run in the run strip.

**Tech Stack:** Go 1.24 stdlib (`time`, `math/rand/v2`), SQLite via the existing store, Preact + htm board (no build step).

**Spec:** `docs/superpowers/specs/2026-10-29-boardingparty-design.md` (§2 anti-goal "Silent spend", §3.2 schedule trigger, §5, §5.1, §6.3).

## Global Constraints

- Deciding to start a run uses no model calls (§3.2, §5.1).
- "The daily budget is a soft cap: runs report cost on finish; once over, no new runs start and running ones finish. Overspend is bounded by N × one run." (§6.3) — usage reports during the run count too.
- "Hitting the daily budget pauses the cast and says so." (§2) — the board says it in the run strip.
- "Pause = no new assignments, running ones finish. Stop = workers deleted." (§5)
- Stuck stories: "the persona stops trying that story until a human nudges it" (§5.1). A manual run of that story is the nudge.
- GB English in all user-facing copy; sentence case; plain words, no "assignment"/"trigger" jargon in the UI.
- Commits: identity `Tiny Systems <dev@tinysystems.io>`, no Co-Authored-By or Generated trailers, never commit `.idea/`, no `replace` directives.
- Message and calendar triggers are out of scope (Plan 2d). Actions-per-hour token bucket and data caps are out of scope.

## Review Focus

1. Director down for hours, then back: a persona long overdue gets one run, not a backlog — test `TestScheduleOverdueQueuesOnce` (Task 4).
2. Role activity edited while a due time is stored: the new pattern applies at once — test `TestScheduleActivityChangeResets` (Task 4).
3. DST change night in Europe/London: `daily at 09:00` stays at 09:00 local — test `TestNextDailyAcrossDST` (Task 1).
4. Over budget while runs are queued: queued items stay and start after local midnight — test `TestDrainOverBudgetKeepsQueue` (Task 4).
5. A model id the price table does not know (dated id, future model): priced at the dearest known price, never free — test `TestCostUnknownModelIsDearest` (Task 3).

---

## File Structure

- Create `internal/activity/activity.go` — `Pattern`, `Parse`, `Pattern.Next`. Pure, no I/O.
- Create `internal/activity/activity_test.go`.
- Modify `internal/project/project.go` — `Project.Timezone`, `Project.DailyBudget`, `Role.Activity`, helpers, validation.
- Create `internal/runs/cost.go` — price table, `Cost`, spend today, `Gate`, `Status`.
- Create `internal/runs/cost_test.go`.
- Modify `internal/runs/runs.go` — gate in `start`, `StopRun`.
- Modify `internal/store/schema.sql`, create `internal/store/settings.go`, `internal/store/dues.go` (+ tests) — `settings` and `persona_due` tables, `RunsSince`, `LastRun`.
- Modify `internal/sched/sched.go` — `schedule` phase, gate in `drain`; create `internal/sched/schedule_test.go`.
- Modify `internal/api/api.go` — `GET /api/status`, `PUT /api/pause`, `POST /api/runs/{id}/stop`, `cost` on a run, error mapping.
- Modify `internal/board/ui/app.js`, `project.js`, `styles.css` — run strip, Up next "Later", cost row, project fields.

---

### Task 1: Activity patterns

**Files:**
- Create: `internal/activity/activity.go`
- Test: `internal/activity/activity_test.go`

**Interfaces:**
- Produces:
  - `type Pattern struct` (fields unexported; zero value = no schedule)
  - `func Parse(s string) (Pattern, error)` — `""` returns zero Pattern, nil.
  - `func (p Pattern) IsZero() bool`
  - `func (p Pattern) Next(after time.Time, loc *time.Location, u func() float64) time.Time` — `u` returns [0,1); `u()==0.5` means no jitter.
  - `func (p Pattern) String() string` — canonical text, e.g. `every 30 min, 09:00–17:30`.

Grammar (case-insensitive, `-` and `–` both accepted, spaces flexible):
- `daily at HH:MM[, HH:MM…]` — up to 8 times. Jitter ±10 min.
- `every N min|mins|minutes|h|hour|hours[, HH:MM–HH:MM]` — 15 min ≤ N ≤ 24 h; window default 00:00–24:00. Jitter ±20 % of N.
- `N times a day[, HH:MM–HH:MM]` or `N–M times a day[, …]` — 1 ≤ N ≤ M ≤ 48; window default 08:00–20:00.
- Window end must be after start; `24:00` allowed only as an end.
- Every error message is: `activity "<input>": use "daily at 09:00", "every 30 min, 09:00–17:30" or "2–4 times a day, 07:00–23:00"`.

Next algorithm (all wall-clock maths with `time.Date(..., loc)` so DST is honoured; `j := 2*u()-1` in [-1,1)):
- daily: T = first listed time strictly after `after` (today, else tomorrow's first); return T + j×10 min; if that is ≤ after, return T.
- every: c = after + N + j×0.2N. If c lies outside the window of its day, c = start of the next window + u()×0.2N.
- times a day: gap = window ÷ ((N+M)/2). If `after` is inside today's window, c = after + gap×(0.5+u()); otherwise c = start of the next window + gap×u(). If c is past that window's end, c = next day's window start + gap×u().
- Result is always > after.

- [ ] **Step 1: Write failing tests**

```go
var london, _ = time.LoadLocation("Europe/London")
func half() float64 { return 0.5 }
func at(s string) time.Time { t, _ := time.ParseInLocation("2006-01-02 15:04", s, london); return t }

func TestParseRejects(t *testing.T) {
	for _, s := range []string{"sometimes", "every 5 min", "every 25 h", "daily at 25:00",
		"0 times a day", "5–2 times a day", "every 30 min, 18:00–09:00", "daily at 24:00", "49 times a day"} {
		if _, err := Parse(s); err == nil || !strings.Contains(err.Error(), `use "daily at 09:00"`) {
			t.Errorf("Parse(%q) err = %v", s, err)
		}
	}
}

func TestParseCanonical(t *testing.T) {
	for in, want := range map[string]string{
		"":                               "",
		"Daily at 9:00":                  "daily at 09:00",
		"daily at 09:00, 17:30":          "daily at 09:00, 17:30",
		"every 30 mins, 09:00-17:30":     "every 30 min, 09:00–17:30",
		"every 2 hours":                  "every 2 h",
		"2-4 times a day, 07:00 - 23:00": "2–4 times a day, 07:00–23:00",
		"3 times a day":                  "3 times a day, 08:00–20:00",
	} {
		p, err := Parse(in)
		if err != nil || p.String() != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, p.String(), err, want)
		}
	}
}

func TestNextDaily(t *testing.T) {
	p, _ := Parse("daily at 09:00, 17:30")
	if got := p.Next(at("2026-11-05 08:00"), london, half); !got.Equal(at("2026-11-05 09:00")) { t.Fatal(got) }
	if got := p.Next(at("2026-11-05 09:00"), london, half); !got.Equal(at("2026-11-05 17:30")) { t.Fatal(got) }
	if got := p.Next(at("2026-11-05 18:00"), london, half); !got.Equal(at("2026-11-06 09:00")) { t.Fatal(got) }
	early := p.Next(at("2026-11-05 08:00"), london, func() float64 { return 0 })
	if !early.Equal(at("2026-11-05 08:50")) { t.Fatal(early) }
}

func TestNextDailyAcrossDST(t *testing.T) {
	p, _ := Parse("daily at 09:00")
	got := p.Next(at("2026-10-24 10:00"), london, half) // clocks go back 25 Oct
	if got.In(london).Format("2006-01-02 15:04") != "2026-10-25 09:00" { t.Fatal(got) }
}

func TestNextEveryStaysInWindow(t *testing.T) {
	p, _ := Parse("every 30 min, 09:00–17:30")
	if got := p.Next(at("2026-11-05 10:00"), london, half); !got.Equal(at("2026-11-05 10:30")) { t.Fatal(got) }
	if got := p.Next(at("2026-11-05 17:20"), london, half); !got.Equal(at("2026-11-06 09:03")) { t.Fatal(got) } // 09:00 + 0.5×6 min
	if got := p.Next(at("2026-11-05 06:00"), london, half); !got.Equal(at("2026-11-05 09:03")) { t.Fatal(got) }
}

func TestNextTimesADay(t *testing.T) {
	p, _ := Parse("2–4 times a day, 08:00–20:00") // gap 4 h
	if got := p.Next(at("2026-11-05 09:00"), london, half); !got.Equal(at("2026-11-05 13:00")) { t.Fatal(got) }
	if got := p.Next(at("2026-11-05 19:00"), london, half); !got.Equal(at("2026-11-06 10:00")) { t.Fatal(got) }
	if got := p.Next(at("2026-11-05 05:00"), london, half); !got.Equal(at("2026-11-05 10:00")) { t.Fatal(got) }
}

func TestNextAlwaysAfter(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, s := range []string{"daily at 09:00", "every 15 min", "every 1 h, 09:00–10:00", "48 times a day, 00:00–24:00"} {
		p, _ := Parse(s)
		after := at("2026-11-05 09:55")
		for i := 0; i < 1000; i++ {
			if got := p.Next(after, london, r.Float64); !got.After(after) { t.Fatalf("%s: %v not after %v", s, got, after) }
		}
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/activity/` — expect FAIL (package missing).
- [ ] **Step 3: Implement** `activity.go` per the grammar and algorithm above (a small hand-written parser over the normalised string; no regexp-heavy cleverness needed but regexps are fine).
- [ ] **Step 4: Run** `go test ./internal/activity/` — expect PASS.
- [ ] **Step 5: Commit** `feat: parse role activity patterns into jittered due times`

---

### Task 2: Project fields

**Files:**
- Modify: `internal/project/project.go`
- Test: `internal/project/project_test.go`

**Interfaces:**
- Consumes: `activity.Parse` (Task 1).
- Produces:
  - `Project.Timezone string \`json:"timezone,omitempty"\`` — IANA name.
  - `Project.DailyBudget *float64 \`json:"dailyBudget,omitempty"\`` — USD.
  - `Role.Activity string \`json:"activity,omitempty"\``.
  - `const DefaultTimezone = "Europe/London"`, `const DefaultDailyBudget = 5.0`, `const MaxDailyBudget = 1000.0`.
  - `func (p Project) Location() *time.Location` — loaded zone, `DefaultTimezone` when empty.
  - `func (p Project) Budget() float64` — `DefaultDailyBudget` when nil.
  - `func (r Role) Pattern() activity.Pattern` — parsed (validation guarantees success).

Validation (in the existing validate path): unknown timezone → `timezone "<x>" is not a time zone, such as Europe/London`; budget ≤ 0 or > 1000 → `dailyBudget must be more than 0 and at most 1000 (US dollars)`; bad activity → `role "<name>": <activity.Parse error>`. Store the canonical `Pattern.String()` back into `Role.Activity` during normalisation so the board shows the tidy form.

- [ ] **Step 1: Failing tests** `TestProjectTimezoneAndBudget` (defaults: `Location().String()=="Europe/London"`, `Budget()==5`; `timezone: Mars/Base` and `dailyBudget: 0` and `dailyBudget: 1001` each rejected with the messages above) and `TestRoleActivity` (`activity: every 30 mins, 09:00-17:30` loads as `every 30 min, 09:00–17:30`; `activity: sometimes` rejected with an error containing `role "buyer": activity "sometimes"`).
- [ ] **Step 2: Run** `go test ./internal/project/` — FAIL.
- [ ] **Step 3: Implement.** The director image must have tzdata: add `import _ "time/tzdata"` in `cmd/director/main.go`.
- [ ] **Step 4: Run** `go test ./internal/project/ ./cmd/...` — PASS.
- [ ] **Step 5: Commit** `feat: project time zone, daily budget and role activity`

---

### Task 3: Cost, spend today and the start gate

**Files:**
- Create: `internal/runs/cost.go`, `internal/runs/cost_test.go`, `internal/store/settings.go`, `internal/store/settings_test.go`
- Modify: `internal/store/schema.sql`, `internal/store/store.go`, `internal/runs/runs.go`

**Interfaces:**
- Consumes: `Project.Location`, `Project.Budget` (Task 2); existing `Service.Usage(runID) (*Usage, error)`.
- Produces:
  - store: table `settings(key TEXT PRIMARY KEY, value TEXT NOT NULL)`; `func (s *Store) Paused() (bool, error)`; `func (s *Store) SetPaused(paused bool) error`; `func (s *Store) RunsSince(t time.Time) ([]Run, error)` (started ≥ t).
  - runs:
    - `func Cost(u Usage) float64` — USD.
    - `func DayStart(now time.Time, loc *time.Location) time.Time` — local midnight, as UTC.
    - `func (s *Service) SpentSince(t time.Time) (float64, error)`
    - `var ErrPaused = errors.New("the cast is paused; resume it to start runs")`
    - `var ErrOverBudget = errors.New("today's budget is spent")` — returned wrapped: `fmt.Errorf("%w: $%.2f of $%.2f; runs start again at midnight (%s) or when you raise the budget", ErrOverBudget, spent, budget, loc)`.
    - `func (s *Service) Gate(now time.Time) error` — `ErrPaused`, wrapped `ErrOverBudget`, or nil.
    - `start` calls `Gate(time.Now())` right after `find` succeeds, before the fence and busy checks, so a gated manual start is refused, never queued.

Price table, USD per million tokens (in, out, cache read, cache write at 1.25× for the 5-minute TTL), matched by `strings.HasPrefix(model, key)`:

| key | in | out | cache read | cache write |
|---|---|---|---|---|
| `claude-haiku-5-5` | 0.10 | 0.50 | 0.01 | 0.125 |
| `claude-sonnet-5-5` | 2 | 10 | 0.20 | 2.50 |
| `claude-opus-5-5` | 4 | 20 | 0.40 | 5.00 |

Unknown model or a usage with empty `ByModel` is priced at the dearest row (opus).

- [ ] **Step 1: Failing tests**

```go
func TestCostHaiku(t *testing.T) {
	u := Usage{ByModel: map[string]Counts{"claude-haiku-5-5": {Input: 42, Output: 7691, CacheRead: 1067295, CacheWrite: 55534}}}
	if got := Cost(u); math.Abs(got-0.02146) > 0.0001 { t.Fatal(got) }
}
func TestCostUnknownModelIsDearest(t *testing.T) {
	c := Counts{Output: 1_000_000}
	if got := Cost(Usage{ByModel: map[string]Counts{"claude-next-9": c}}); got != 20 { t.Fatal(got) }
	if got := Cost(Usage{Counts: c}); got != 20 { t.Fatal(got) }
	if got := Cost(Usage{ByModel: map[string]Counts{"claude-haiku-5-5-20261101": c}}); got != 0.5 { t.Fatal(got) }
}
func TestDayStartLondon(t *testing.T) { // 00:30 BST on 1 Jul is 23:30 UTC on 30 Jun
	loc, _ := time.LoadLocation("Europe/London")
	now := time.Date(2026, 6, 30, 23, 30, 0, 0, time.UTC)
	if got := DayStart(now, loc); !got.Equal(time.Date(2026, 6, 30, 23, 0, 0, 0, time.UTC)) { t.Fatal(got) }
}
func TestGate(t *testing.T) {
	svc, _ := newSvc(t) // budget default 5
	// a run started today with $6 of opus output
	... create run started now, SaveUsage-equivalent artifact {ByModel: {"claude-opus-5-5": {Output: 300_000}}}
	if err := svc.Gate(time.Now()); !errors.Is(err, ErrOverBudget) || !strings.Contains(err.Error(), "$6.00 of $5.00") { t.Fatal(err) }
	if _, err := svc.Start(ctx, "ann", "checkout"); !errors.Is(err, ErrOverBudget) { t.Fatal(err) }
	svc.Store.SetPaused(true)
	if err := svc.Gate(time.Now()); !errors.Is(err, ErrPaused) { t.Fatal(err) }
}
func TestGateIgnoresYesterday(t *testing.T) { /* same $6 run started 25 h ago: Gate == nil */ }
```
Plus store `TestPausedRoundTrip` (default false; set true; set false) and `TestRunsSince`.

- [ ] **Step 2: Run** `go test ./internal/runs/ ./internal/store/` — FAIL.
- [ ] **Step 3: Implement.** Paused is checked before budget (cheaper, and the clearer message). The existing busy tests must still pass (Gate is nil for them).
- [ ] **Step 4: Run** `go test ./internal/...` — PASS.
- [ ] **Step 5: Commit** `feat: price runs, cap daily spend, pause gate`

---

### Task 4: Scheduler — due personas and the gate

**Files:**
- Create: `internal/store/dues.go`, `internal/store/dues_test.go`, `internal/sched/schedule_test.go`
- Modify: `internal/store/schema.sql`, `internal/sched/sched.go`, `internal/sched/sched_test.go` (fake starter gains `Gate`)

**Interfaces:**
- Consumes: `Role.Pattern`, `Project.Location` (Task 2); `ErrPaused`, `ErrOverBudget`, `Service.Gate` (Task 3); existing `Store.Enqueue`, `Store.Queue`, `Store.ActiveRuns`, `Store.OpenWaits`, `Store.RunByID`.
- Produces:
  - store: table `persona_due(persona TEXT PRIMARY KEY, activity TEXT NOT NULL, due INTEGER NOT NULL, next_story INTEGER NOT NULL DEFAULT 0)`.
  - `type Due struct { Persona, Activity string; At time.Time; NextStory int }`
  - `func (s *Store) Dues() (map[string]Due, error)`, `func (s *Store) SetDue(d Due) error`, `func (s *Store) DeleteDuesExcept(personas []string) error`
  - `func (s *Store) LastRun(persona, story string) (Run, error)` — newest by started; `ErrNotFound` if none.
  - `Starter` interface gains `Gate(now time.Time) error`.
  - `Scheduler.Rand func() float64` — nil = `rand.Float64`.
  - `Tick` = `followAll, resolveAll, schedule, drain`.

`schedule(now)`:
1. Load project; `DeleteDuesExcept(all persona names)`.
2. `gated := s.Runs.Gate(now) != nil`.
3. For each role with non-zero `Pattern()`, each persona:
   - No row, or `row.Activity != role.Activity`: `SetDue{At: pattern.Next(now, loc, Rand)}` (NextStory 0), continue.
   - `row.At` after now: continue.
   - Due. If not gated and the persona has no active run and no queued item: walk stories from `NextStory` (wrapping) and pick the first that has no open wait owned by this persona for that story and whose `LastRun` is not `stuck`. If one is found, `Enqueue(Queued{ID: newID, Persona, Role, Story, Reason: "Scheduled: " + role.Activity, Created: now})` and set NextStory to the index after it.
   - Always `SetDue{At: pattern.Next(now, …), NextStory}` — a due time missed while paused, over budget or busy is skipped, never caught up (§5.1).
4. Roles without a pattern are left alone (their personas run only by hand or hand-off).

`drain`: `ErrPaused` and `ErrOverBudget` from `StartQueued` keep the item and end the tick quietly (no log line per tick).

- [ ] **Step 1: Failing tests** (in `schedule_test.go`, project YAML with role `patient` activity `every 1 h` and two stories `book`, `cancel`; fixed clock; `Rand: func() float64 { return 0.5 }`):
  - `TestScheduleFirstSightSetsDue` — first tick: no queue item, `Dues()["Ada"].At == now+1h`.
  - `TestScheduleQueuesRoundRobin` — advance 1 h: queue holds `book` with reason `Scheduled: every 1 h`; start it (fake starter), finish done, advance 1 h: next item is `cancel`.
  - `TestScheduleOverdueQueuesOnce` — advance 10 h: exactly one item; due = now+1h.
  - `TestScheduleSkipsBusyPersona` — persona already queued: no second item, due still moves on.
  - `TestScheduleSkipsStuckAndWaitingStories` — last `book` run stuck: `cancel` is chosen; both blocked: nothing queued.
  - `TestScheduleGatedSkips` — fake `Gate` returns `runs.ErrPaused`: due passes, nothing queued, due moves on.
  - `TestScheduleActivityChangeResets` — change role activity to `daily at 09:00`: due recomputed to next 09:00 local.
  - `TestDrainOverBudgetKeepsQueue` — `StartQueued` returns wrapped `runs.ErrOverBudget`: item stays, `Tick` returns nil.
- [ ] **Step 2: Run** `go test ./internal/sched/ ./internal/store/` — FAIL.
- [ ] **Step 3: Implement.** `runs.Service` already satisfies the widened `Starter` after Task 3.
- [ ] **Step 4: Run** `go test ./...` — PASS.
- [ ] **Step 5: Commit** `feat: scheduler queues due personas on their role's activity`

---

### Task 5: API — status, pause, stop, cost

**Files:**
- Modify: `internal/api/api.go`, `internal/runs/runs.go`, `internal/runs/cost.go`
- Test: `internal/api/status_test.go`, `internal/runs/runs_test.go`

**Interfaces:**
- Consumes: Tasks 3–4.
- Produces:
  - `func (s *Service) Status(now time.Time) (Status, error)` with
    ```go
    type Status struct {
        Paused      bool      `json:"paused"`
        SpentToday  float64   `json:"spentToday"`
        DailyBudget float64   `json:"dailyBudget"`
        OverBudget  bool      `json:"overBudget"`
        Timezone    string    `json:"timezone"`
        ResetsAt    time.Time `json:"resetsAt"` // next local midnight
        Due         []DueView `json:"due"`      // scheduled personas, soonest first
    }
    type DueView struct { Persona string `json:"persona"`; Role string `json:"role"`; At time.Time `json:"at"` }
    ```
  - `func (s *Service) StopRun(ctx context.Context, id string) error` — active run → finished `failed` with outcome `Stopped from the board.`, session stopped through the same tail as `Finish` (artifacts kept, `OnFinish` called); not active → `ErrNotActive`.
  - Routes: `GET /api/status`; `PUT /api/pause` body `{"paused": bool}` → 204; `POST /api/runs/{id}/stop` → 204, 404 when not active.
  - `getRun` adds `"cost": <float>` when usage exists.
  - `fail`/`startRun`: `ErrPaused` and `ErrOverBudget` → 409 with the error text.
  - Pausing stops nothing; it only gates starts (§5). Resuming pokes the scheduler (`Service.OnFinish` is the poke hook; call it after `SetPaused(false)`).
- [ ] **Step 1: Failing tests** `TestStatusReportsSpendAndPause`, `TestPauseBlocksStart` (409, body contains `paused`), `TestStopRun` (run becomes failed with the outcome text; second stop 404), `TestGetRunCost`.
- [ ] **Step 2: Run** `go test ./internal/api/ ./internal/runs/` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./...` — PASS.
- [ ] **Step 5: Commit** `feat: status, pause and stop run endpoints`

---

### Task 6: Board

**Files:**
- Modify: `internal/board/ui/app.js`, `internal/board/ui/project.js`, `internal/board/ui/styles.css`
- Test: `internal/board/board_test.go` (only if it checks embedded files; otherwise manual check in Step 3)

**Interfaces:**
- Consumes: Task 5 endpoints.

Copy (exact):
- Run strip, right of the active-run stat: `Today $0.42 of $5.00` (two decimals); over budget: tag `Budget reached` and text `No new runs until 00:00`; paused: tag `Paused`.
- Buttons in the strip: `Pause` / `Resume` (Pause asks `confirmDialog('Pause the cast? Running runs finish; nothing new starts until you resume.', { confirm: 'Pause' })`); `Stop run` when a run is active (`confirmDialog('Stop this run now? Its recording so far is kept.', { confirm: 'Stop run', danger: true })`).
- Up next card: a `Later` list from `status.due`: `John Smith · 14:32` (fmtWhen), at most 5, then `and N more`.
- Run Details: row `Cost` → `≈ $0.02` (`< $0.01` below a cent).
- Project → General: `Daily budget` (number input, step 0.5, placeholder `5`, help `Spend cap in US dollars per day. Running runs finish; new ones wait until midnight.`) and `Time zone` (text input, placeholder `Europe/London`).
- Role page: `Activity` text input, placeholder `every 30 min, 09:00–17:30`, help `How often personas in this role act on their own. Examples: daily at 09:00 · every 2 h, 09:00–17:30 · 2–4 times a day, 07:00–23:00. Leave empty to run them only by hand or hand-off.`
- The board polls `/api/status` with the runs list (same cadence).

- [ ] **Step 1: Implement** the above, reusing existing components (`confirmDialog`, `.tag`, `.field`, the General and role section forms in `project.js`).
- [ ] **Step 2: Run** `go test ./internal/board/` and `node --check internal/board/ui/app.js internal/board/ui/project.js`.
- [ ] **Step 3: Check in a browser** against the dev director (`go run ./cmd/boardingparty board --dev` or the repo's existing dev path) at desktop and 390 px width: strip wraps without horizontal scroll.
- [ ] **Step 4: Commit** `feat: board shows spend, pause, stop and scheduled personas`

---

### Task 7: Live check (controller)

- [ ] Push, wait for CI, `boardingparty setup -n boardingparty-test --context gke_tinyplatform_europe-west2-a_cluster-1 --image-tag sha-<short>`.
- [ ] Set Customer role activity `every 15 min`; confirm the board shows John Smith under Later; let one scheduled run start and finish; then clear the activity.
- [ ] Set `dailyBudget: 0.01` (already spent today): manual start refused with the budget message; strip shows `Budget reached`; restore the budget.
- [ ] Pause → start refused; Resume → start works; Stop run on an active run → failed `Stopped from the board.`
