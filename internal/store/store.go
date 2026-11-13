// Package store persists the director's state in SQLite.
package store

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/boarding-party/boardingparty/internal/project"
)

//go:embed schema.sql
var schema string

var ErrNotFound = errors.New("not found")

// RunReplay is the Kind of a run that replays a journey revision.
const RunReplay = "replay"

type RunState string // "pending","running","done","stuck","failed","waiting"

const (
	RunPending RunState = "pending"
	RunRunning RunState = "running"
	RunDone    RunState = "done"
	RunStuck   RunState = "stuck"
	RunFailed  RunState = "failed"
	RunWaiting RunState = "waiting"
)

// Final reports whether the run has stopped for good (a waiting run has
// stopped too; a later run continues it).
func (s RunState) Final() bool {
	switch s {
	case RunDone, RunStuck, RunFailed, RunWaiting:
		return true
	}
	return false
}

type Run struct {
	ID       string    `json:"id"`
	Token    string    `json:"token"`
	Persona  string    `json:"persona"`
	Role     string    `json:"role"`
	Story    string    `json:"story"`
	State    RunState  `json:"state"`
	Outcome  string    `json:"outcome"` // agent's summary or failure reason
	Session  string    `json:"session"` // tiny Session name
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished"`

	Chain string `json:"chainId"`
	// Kind is "" for an AI run and RunReplay for a replay of a journey
	// revision, which runs with no model.
	Kind      string          `json:"kind,omitempty"`
	JourneyID string          `json:"journeyId,omitempty"`
	Revision  int             `json:"revision,omitempty"`
	Seed      int64           `json:"seed,omitempty"`
	Follows   string          `json:"follows,omitempty"` // run that caused this one
	Via       string          `json:"via,omitempty"`     // "" new, "continue", "handoff"
	Context   json.RawMessage `json:"context,omitempty"`
	// ReplayResult is a replay run's result as bp-replay reported it.
	ReplayResult json.RawMessage `json:"replayResult,omitempty"`
	// Profile is the persona's profile when the run started.
	Profile project.Profile `json:"profile,omitzero"`
	// Claim is a replay run's one-time code: its pod's command carries it
	// in place of the token, and redeeming it once gives the pod its URLs.
	// Only CreateRun writes it; it is never read back into a Run or shown.
	Claim string `json:"-"`
}

// RunContext is what a chained run is told about the run before it.
type RunContext struct {
	Kind        string        `json:"kind"` // "continue" | "handoff" | "message" | "calendar" | "verify" | "lead"
	WaitedFor   string        `json:"waitedFor,omitempty"`
	Arrived     string        `json:"arrived,omitempty"`
	MessageID   string        `json:"messageId,omitempty"`
	PreviousRun string        `json:"previousRun"`
	Previous    string        `json:"previous"` // that run's outcome summary
	From        *ContextFrom  `json:"from,omitempty"`
	Event       *ContextEvent `json:"event,omitempty"`
	// Verify is the issue this run checks a fix for. A continuation keeps it.
	Verify *ContextVerify `json:"verify,omitempty"`
	// Reason is why a lead run woke (kind "lead").
	Reason string `json:"reason,omitempty"`
	// Replay is the failed replay a copilot run decides about (kind
	// "replay_failed").
	Replay *ContextReplay `json:"replay,omitempty"`
	// Resume carries a replay on after its journey's waits: the steps before
	// wait After are skipped and each wait gets its arrival.
	Resume *ContextResume `json:"resume,omitempty"`
}

// ContextResume is where a continued replay picks its journey up.
type ContextResume struct {
	After    int              `json:"after"`
	Arrivals []ContextArrival `json:"arrivals"`
}

// ContextArrival is what ended one of a journey's waits.
type ContextArrival struct {
	Detail    string `json:"detail"`
	MessageID string `json:"messageId,omitempty"`
}

// ContextReplay is the replay a copilot run was called for: which revision
// failed where, with which inputs, and why the copilot was called.
type ContextReplay struct {
	JourneyID string         `json:"journeyId"`
	Revision  int            `json:"revision"`
	Step      int            `json:"step,omitempty"`
	StepName  string         `json:"stepName,omitempty"`
	Error     string         `json:"error,omitempty"`
	Seed      int64          `json:"seed"`
	Inputs    map[string]any `json:"inputs,omitempty"`
	Why       string         `json:"why"`
}

// ContextVerify is the issue a fix check looks for again.
type ContextVerify struct {
	IssueID  string `json:"issueId"`
	Summary  string `json:"summary"`
	Step     string `json:"step,omitempty"`
	Evidence string `json:"evidence,omitempty"`
	// JourneyID, Revision and JourneyStep are set on a fix check by replay:
	// the revision replayed and the step where the problem showed.
	JourneyID   string `json:"journeyId,omitempty"`
	Revision    int    `json:"revision,omitempty"`
	JourneyStep int    `json:"journeyStep,omitempty"`
	// ReplayNote is what a fix-check replay saw before it handed the check
	// to an AI run (untrusted: it quotes the site).
	ReplayNote string `json:"replayNote,omitempty"`
	// Repeat marks the replay that runs a failure at JourneyStep again with
	// the same inputs.
	Repeat bool `json:"repeat,omitempty"`
}

// ContextEvent is the calendar entry a calendar-triggered run was started for.
type ContextEvent struct {
	UID      string    `json:"uid,omitempty"`
	Title    string    `json:"title"`
	Start    time.Time `json:"start"`
	Location string    `json:"location,omitempty"`
	URL      string    `json:"url,omitempty"`
}

type ContextFrom struct {
	Persona string `json:"persona"`
	Role    string `json:"role"`
	Story   string `json:"story"`
	Summary string `json:"summary"`
}

type Bug struct {
	ID       string    `json:"id"`
	RunID    string    `json:"run_id"`
	Summary  string    `json:"summary"`
	Step     string    `json:"step"`
	Evidence string    `json:"evidence"`
	Severity string    `json:"severity"`
	Note     string    `json:"note"`
	Created  time.Time `json:"created"`
	// IssueID is the issue this report belongs to. On AddBug it may name an
	// existing issue to attach to; empty or unknown opens a new one.
	IssueID string `json:"issue_id"`
	// JourneyID, JourneyRev and JourneyStep are the journey revision and
	// step that show this report's problem (empty and 0: none). The first
	// link wins.
	JourneyID   string `json:"journeyId,omitempty"`
	JourneyRev  int    `json:"journeyRev,omitempty"`
	JourneyStep int    `json:"journeyStep,omitempty"`
}

type Store struct{ db *sql.DB }

// Open opens (creating if needed) the database at path and applies the schema.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One writer connection avoids SQLITE_BUSY between our own goroutines.
	db.SetMaxOpenConns(1)
	legacy, err := messagesLacksMailbox(db)
	if err != nil {
		db.Close()
		return nil, err
	}
	if legacy { // the unique key changed; SQLite cannot alter it, so move the table aside
		if _, err := db.Exec(`ALTER TABLE messages RENAME TO messages_old; DROP INDEX IF EXISTS messages_persona_received`); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate messages: %w", err)
		}
	}
	if err := renameJourneyToChain(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate chain: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if _, err := addColumns(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate runs: %w", err)
	}
	// After addColumns: on an old database bugs.issue_id exists only now.
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS bugs_issue ON bugs(issue_id)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("index bugs: %w", err)
	}
	if legacy {
		if _, err := db.Exec(`INSERT INTO messages(` + msgCols + `, mailbox) SELECT ` + msgCols + `, '' FROM messages_old; DROP TABLE messages_old`); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate messages: %w", err)
		}
	}
	return &Store{db: db}, nil
}

// messagesLacksMailbox reports whether messages exists without the mailbox column.
func messagesLacksMailbox(db *sql.DB) (bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info('messages')`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	n, has := 0, false
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		n++
		has = has || name == "mailbox"
	}
	return n > 0 && !has, rows.Err()
}

// renameJourneyToChain moves a database from before "journey" meant a
// recorded browser journey: the chain of runs lived in columns called
// journey. Runs before the schema so the schema's indexes find the new names.
func renameJourneyToChain(db *sql.DB) error {
	for _, c := range []struct{ table, from, to string }{
		{"runs", "journey", "chain"}, {"queue", "journey", "chain"}, {"issues", "check_journey", "check_chain"},
	} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, c.table, c.from).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE ` + c.table + ` RENAME COLUMN ` + c.from + ` TO ` + c.to); err != nil {
			return fmt.Errorf("%s.%s: %w", c.table, c.from, err)
		}
	}
	_, err := db.Exec(`DROP INDEX IF EXISTS runs_journey`)
	return err
}

type addedColumn struct{ name, ddl string }

// runsAddedColumns are the runs columns added after the first release.
var runsAddedColumns = []addedColumn{
	{"chain", "chain TEXT NOT NULL DEFAULT ''"},
	{"follows", "follows TEXT NOT NULL DEFAULT ''"},
	{"via", "via TEXT NOT NULL DEFAULT ''"},
	{"context", "context TEXT NOT NULL DEFAULT ''"},
	{"followed", "followed INTEGER NOT NULL DEFAULT 0"},
	{"kind", "kind TEXT NOT NULL DEFAULT ''"},
	{"journey_id", "journey_id TEXT NOT NULL DEFAULT ''"},
	{"revision", "revision INTEGER NOT NULL DEFAULT 0"},
	{"seed", "seed INTEGER NOT NULL DEFAULT 0"},
	{"replay_result", "replay_result TEXT NOT NULL DEFAULT ''"},
	{"claim", "claim TEXT NOT NULL DEFAULT ''"},
	{"profile", "profile TEXT NOT NULL DEFAULT ''"},
}

// journeysAddedColumns are the journeys columns added after the table first shipped.
var journeysAddedColumns = []addedColumn{
	{"flaky_streak", "flaky_streak INTEGER NOT NULL DEFAULT 0"},
	{"blocked_step", "blocked_step INTEGER NOT NULL DEFAULT 0"},
	{"blocked_bug", "blocked_bug TEXT NOT NULL DEFAULT ''"},
	{"copilot_at", "copilot_at INTEGER NOT NULL DEFAULT 0"},
}

// queueAddedColumns are the queue columns added after the table first shipped.
var queueAddedColumns = []addedColumn{
	{"kind", "kind TEXT NOT NULL DEFAULT ''"},
	{"journey_id", "journey_id TEXT NOT NULL DEFAULT ''"},
	{"revision", "revision INTEGER NOT NULL DEFAULT 0"},
	{"seed", "seed INTEGER NOT NULL DEFAULT 0"},
}

// waitsAddedColumns are the waits columns added after the table first shipped.
var waitsAddedColumns = []addedColumn{
	{"after_id", "after_id TEXT NOT NULL DEFAULT ''"},
}

// bugsAddedColumns are the bugs columns added after the table first shipped.
var bugsAddedColumns = []addedColumn{
	{"issue_id", "issue_id TEXT NOT NULL DEFAULT ''"},
	{"journey_id", "journey_id TEXT NOT NULL DEFAULT ''"},
	{"journey_rev", "journey_rev INTEGER NOT NULL DEFAULT 0"},
	{"journey_step", "journey_step INTEGER NOT NULL DEFAULT 0"},
}

// addColumns adds any missing runs and waits columns to a database made by an
// older version. Runs that already exist become their own chain and count
// as followed, so the scheduler does not act on history.
func addColumns(db *sql.DB) (added bool, err error) {
	// One transaction: a crash between the ALTERs and the backfill must not
	// leave old runs unfollowed. SQLite DDL is transactional.
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	addedRuns, err := addMissing(tx, "runs", runsAddedColumns)
	if err != nil {
		return false, err
	}
	if addedRuns["followed"] {
		if _, err := tx.Exec(`UPDATE runs SET followed = 1, chain = CASE chain WHEN '' THEN id ELSE chain END`); err != nil {
			return false, err
		}
	}
	if addedRuns["replay_result"] {
		// Replays ended before they were followed are history, not failures to act on.
		if _, err := tx.Exec(`UPDATE runs SET followed = 1 WHERE kind = 'replay'`); err != nil {
			return false, err
		}
	}
	addedWaits, err := addMissing(tx, "waits", waitsAddedColumns)
	if err != nil {
		return false, err
	}
	addedBugs, err := addMissing(tx, "bugs", bugsAddedColumns)
	if err != nil {
		return false, err
	}
	addedQueue, err := addMissing(tx, "queue", queueAddedColumns)
	if err != nil {
		return false, err
	}
	addedJourneys, err := addMissing(tx, "journeys", journeysAddedColumns)
	if err != nil {
		return false, err
	}
	if addedBugs["issue_id"] {
		// Each existing report becomes its own open issue, under the bug's id.
		if _, err := tx.Exec(`INSERT INTO issues(id, summary, status, created, updated)
			SELECT id, summary, 'open', created, created FROM bugs; UPDATE bugs SET issue_id = id`); err != nil {
			return false, err
		}
	}
	// Created here, not in schema.sql: on an old database the column exists only now.
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS runs_chain ON runs(chain)`); err != nil {
		return false, err
	}
	return len(addedRuns)+len(addedWaits)+len(addedBugs)+len(addedQueue)+len(addedJourneys) > 0, tx.Commit()
}

// addMissing adds the columns table lacks and returns the names it added. A
// table that does not exist is left alone.
func addMissing(tx *sql.Tx, table string, cols []addedColumn) (map[string]bool, error) {
	rows, err := tx.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	added := map[string]bool{}
	if len(have) == 0 {
		return added, nil
	}
	for _, c := range cols {
		if have[c.name] {
			continue
		}
		if _, err := tx.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + c.ddl); err != nil {
			return nil, err
		}
		added[c.name] = true
	}
	return added, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) PutProject(p project.Project) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO project(id, body) VALUES(1, ?)
		ON CONFLICT(id) DO UPDATE SET body = excluded.body`, string(b))
	return err
}

// PersonaState returns nil if the persona has no saved state.
func (s *Store) PersonaState(persona string) (json.RawMessage, error) {
	var st string
	err := s.db.QueryRow(`SELECT state FROM persona_state WHERE persona = ?`, persona).Scan(&st)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(st), nil
}

func (s *Store) SavePersonaState(persona string, state json.RawMessage) error {
	_, err := s.db.Exec(`INSERT INTO persona_state(persona, state) VALUES(?, ?)
		ON CONFLICT(persona) DO UPDATE SET state = excluded.state`, persona, string(state))
	return err
}

func ts(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

func fromTS(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n).UTC()
}

const runCols = `id, token, persona, role, story, state, outcome, session, started, finished, chain, follows, via, context,
	kind, journey_id, revision, seed, replay_result, profile`

func scanRun(sc interface{ Scan(...any) error }) (Run, error) {
	var r Run
	var st string
	var started, finished int64
	var ctx, result, profile string
	if err := sc.Scan(&r.ID, &r.Token, &r.Persona, &r.Role, &r.Story, &st, &r.Outcome, &r.Session, &started, &finished,
		&r.Chain, &r.Follows, &r.Via, &ctx, &r.Kind, &r.JourneyID, &r.Revision, &r.Seed, &result, &profile); err != nil {
		return Run{}, err
	}
	if profile != "" {
		_ = json.Unmarshal([]byte(profile), &r.Profile) // a profile that no longer parses is shown as none
	}
	if ctx != "" {
		r.Context = json.RawMessage(ctx)
	}
	if result != "" {
		r.ReplayResult = json.RawMessage(result)
	}
	r.State = RunState(st)
	r.Started, r.Finished = fromTS(started), fromTS(finished)
	return r, nil
}

func (s *Store) CreateRun(r Run) error { return s.CreateRunFrom(r, "") }

// CreateRunFrom creates the run and, when queueID is set, consumes that queue
// row in the same transaction. It returns ErrNotFound (creating nothing) if
// the row was already gone.
func (s *Store) CreateRunFrom(r Run, queueID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if queueID != "" {
		res, err := tx.Exec(`DELETE FROM queue WHERE id = ?`, queueID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
	}
	profile := ""
	if r.Profile != (project.Profile{}) {
		b, err := json.Marshal(r.Profile)
		if err != nil {
			return err
		}
		profile = string(b)
	}
	if _, err := tx.Exec(`INSERT INTO runs(`+runCols+`, claim) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.Token, r.Persona, r.Role, r.Story, string(r.State), r.Outcome, r.Session, ts(r.Started), ts(r.Finished),
		r.Chain, r.Follows, r.Via, string(r.Context), r.Kind, r.JourneyID, r.Revision, r.Seed, string(r.ReplayResult), profile, r.Claim); err != nil {
		return err
	}
	return tx.Commit()
}

// RedeemClaim spends an active run's claim and returns the run. A claim
// already spent, unknown, or of a run no longer active is ErrNotFound.
func (s *Store) RedeemClaim(claim string) (Run, error) {
	if claim == "" {
		return Run{}, ErrNotFound
	}
	var id string
	err := s.db.QueryRow(`UPDATE runs SET claim = '' WHERE claim = ? AND state IN (?, ?) RETURNING id`,
		claim, string(RunPending), string(RunRunning)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	return s.RunByID(id)
}

func (s *Store) RunByToken(token string) (Run, error) {
	r, err := scanRun(s.db.QueryRow(`SELECT `+runCols+` FROM runs WHERE token = ?`, token))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return r, err
}

func (s *Store) RunByID(id string) (Run, error) {
	r, err := scanRun(s.db.QueryRow(`SELECT `+runCols+` FROM runs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return r, err
}

func (s *Store) UpdateRun(r Run) error {
	res, err := s.db.Exec(`UPDATE runs SET token=?, persona=?, role=?, story=?, state=?, outcome=?, session=?, started=?, finished=? WHERE id=?`,
		r.Token, r.Persona, r.Role, r.Story, string(r.State), r.Outcome, r.Session, ts(r.Started), ts(r.Finished), r.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetRunSession records the launched session and moves a pending run to
// running. It returns false if the run was no longer pending (e.g. already finished).
func (s *Store) SetRunSession(id, session string) (bool, error) {
	res, err := s.db.Exec(`UPDATE runs SET session=?, state='running' WHERE id=? AND state='pending'`, session, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// FinishRun atomically moves a pending or running run to a final state.
// It returns false if the run was not active (already finished or unknown).
func (s *Store) FinishRun(id string, state RunState, outcome string, finished time.Time) (bool, error) {
	return s.FinishRunChecked(id, state, outcome, finished, nil)
}

// FinishRunChecked is FinishRun with the verdict of a fix check run; a run
// that checks a fix settles its issue in the same transaction.
func (s *Store) FinishRunChecked(id string, state RunState, outcome string, finished time.Time, v *Verdict) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE runs SET state=?, outcome=?, finished=? WHERE id=? AND state IN ('pending','running')`,
		string(state), outcome, ts(finished), id)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	if err := settleCheck(tx, id, state, v, finished); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) queryRuns(q string, args ...any) ([]Run, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Runs returns the newest runs first.
func (s *Store) Runs(limit int) ([]Run, error) {
	return s.queryRuns(`SELECT `+runCols+` FROM runs ORDER BY started DESC, rowid DESC LIMIT ?`, limit)
}

// JourneyRuns returns a journey's replay runs, newest first.
func (s *Store) JourneyRuns(journeyID string, limit int) ([]Run, error) {
	return s.queryRuns(`SELECT `+runCols+` FROM runs WHERE journey_id = ? ORDER BY started DESC, rowid DESC LIMIT ?`, journeyID, limit)
}

// FollowUps returns the runs that follow runID, oldest first.
func (s *Store) FollowUps(runID string) ([]Run, error) {
	return s.queryRuns(`SELECT `+runCols+` FROM runs WHERE follows = ? ORDER BY started, rowid`, runID)
}

// ActiveRuns returns runs that are pending or running.
func (s *Store) ActiveRuns() ([]Run, error) {
	return s.queryRuns(`SELECT ` + runCols + ` FROM runs WHERE state IN ('pending','running') ORDER BY started`)
}

// RunsInChain returns every run of a chain, oldest first.
func (s *Store) RunsInChain(chain string) ([]Run, error) {
	return s.queryRuns(`SELECT `+runCols+` FROM runs WHERE chain = ? ORDER BY started, rowid`, chain)
}

// Unfollowed returns done or waiting runs the scheduler has not yet acted on,
// oldest finished first.
// LastRunVia returns persona's newest run of story that came via via, or
// ErrNotFound.
func (s *Store) LastRunVia(persona, story, via string) (Run, error) {
	r, err := scanRun(s.db.QueryRow(`SELECT `+runCols+` FROM runs WHERE persona = ? AND story = ? AND via = ?
		ORDER BY started DESC, rowid DESC LIMIT 1`, persona, story, via))
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return r, err
}

// UnfollowedReplays returns the ended replay runs the scheduler has not
// acted on yet, oldest first.
func (s *Store) UnfollowedReplays() ([]Run, error) {
	return s.queryRuns(`SELECT ` + runCols + ` FROM runs WHERE state IN ('done','failed','stuck') AND followed = 0 AND kind = 'replay' ORDER BY finished, rowid`)
}

// SetReplayResult keeps a replay run's result on the run.
func (s *Store) SetReplayResult(runID string, result json.RawMessage) error {
	res, err := s.db.Exec(`UPDATE runs SET replay_result = ? WHERE id = ?`, string(result), runID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Unfollowed() ([]Run, error) {
	// Replays are never followed: they spend nothing and must cause no spend.
	return s.queryRuns(`SELECT ` + runCols + ` FROM runs WHERE state IN ('done','waiting') AND followed = 0 AND kind = '' ORDER BY finished, rowid`)
}

// BugCounts returns how many bugs each of the runs reported; runs with none
// are left out.
func (s *Store) BugCounts(runIDs []string) (map[string]int, error) {
	out := map[string]int{}
	if len(runIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(runIDs))
	for i, id := range runIDs {
		args[i] = id
	}
	rows, err := s.db.Query(`SELECT run_id, COUNT(*) FROM bugs WHERE run_id IN (?`+strings.Repeat(",?", len(runIDs)-1)+`) GROUP BY run_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

func (s *Store) AddBug(b Bug) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertBug(tx, b); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Bugs(runID string) ([]Bug, error) {
	rows, err := s.db.Query(`SELECT `+bugCols+` FROM bugs WHERE run_id = ? ORDER BY created, rowid`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bug
	for rows.Next() {
		b, err := scanBug(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
