// Package store persists the director's state in SQLite.
package store

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/boarding-party/boardingparty/internal/project"
)

//go:embed schema.sql
var schema string

var ErrNotFound = errors.New("not found")

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

	Journey string          `json:"journey"`
	Follows string          `json:"follows,omitempty"` // run that caused this one
	Via     string          `json:"via,omitempty"`     // "" new, "continue", "handoff"
	Context json.RawMessage `json:"context,omitempty"`
}

// RunContext is what a chained run is told about the run before it.
type RunContext struct {
	Kind        string       `json:"kind"` // "continue" | "handoff"
	WaitedFor   string       `json:"waitedFor,omitempty"`
	Arrived     string       `json:"arrived,omitempty"`
	MessageID   string       `json:"messageId,omitempty"`
	PreviousRun string       `json:"previousRun"`
	Previous    string       `json:"previous"` // that run's outcome summary
	From        *ContextFrom `json:"from,omitempty"`
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
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if _, err := addColumns(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate runs: %w", err)
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

// runsAddedColumns are the runs columns added after the first release.
var runsAddedColumns = []struct{ name, ddl string }{
	{"journey", "journey TEXT NOT NULL DEFAULT ''"},
	{"follows", "follows TEXT NOT NULL DEFAULT ''"},
	{"via", "via TEXT NOT NULL DEFAULT ''"},
	{"context", "context TEXT NOT NULL DEFAULT ''"},
	{"followed", "followed INTEGER NOT NULL DEFAULT 0"},
}

// addColumns adds any missing runs columns to a database made by an older
// version. Runs that already exist become their own journey and count as
// followed, so the scheduler does not act on history.
func addColumns(db *sql.DB) (added bool, err error) {
	// One transaction: a crash between the ALTERs and the backfill must not
	// leave old runs unfollowed. SQLite DDL is transactional.
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT name FROM pragma_table_info('runs')`)
	if err != nil {
		return false, err
	}
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return false, err
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return false, err
	}
	rows.Close()
	addedFollowed := false
	for _, c := range runsAddedColumns {
		if have[c.name] {
			continue
		}
		if _, err := tx.Exec(`ALTER TABLE runs ADD COLUMN ` + c.ddl); err != nil {
			return false, err
		}
		added = true
		addedFollowed = addedFollowed || c.name == "followed"
	}
	if addedFollowed {
		if _, err := tx.Exec(`UPDATE runs SET followed = 1, journey = CASE journey WHEN '' THEN id ELSE journey END`); err != nil {
			return false, err
		}
	}
	// Created here, not in schema.sql: on an old database the column exists only now.
	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS runs_journey ON runs(journey)`); err != nil {
		return false, err
	}
	return added, tx.Commit()
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

func (s *Store) Project() (project.Project, error) {
	var body string
	err := s.db.QueryRow(`SELECT body FROM project WHERE id = 1`).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Project{}, ErrNotFound
	}
	if err != nil {
		return project.Project{}, err
	}
	var p project.Project
	err = json.Unmarshal([]byte(body), &p)
	return p, err
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

const runCols = `id, token, persona, role, story, state, outcome, session, started, finished, journey, follows, via, context`

func scanRun(sc interface{ Scan(...any) error }) (Run, error) {
	var r Run
	var st string
	var started, finished int64
	var ctx string
	if err := sc.Scan(&r.ID, &r.Token, &r.Persona, &r.Role, &r.Story, &st, &r.Outcome, &r.Session, &started, &finished,
		&r.Journey, &r.Follows, &r.Via, &ctx); err != nil {
		return Run{}, err
	}
	if ctx != "" {
		r.Context = json.RawMessage(ctx)
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
	if _, err := tx.Exec(`INSERT INTO runs(`+runCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.Token, r.Persona, r.Role, r.Story, string(r.State), r.Outcome, r.Session, ts(r.Started), ts(r.Finished),
		r.Journey, r.Follows, r.Via, string(r.Context)); err != nil {
		return err
	}
	return tx.Commit()
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
	res, err := s.db.Exec(`UPDATE runs SET state=?, outcome=?, finished=? WHERE id=? AND state IN ('pending','running')`,
		string(state), outcome, ts(finished), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
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

// ActiveRuns returns runs that are pending or running.
func (s *Store) ActiveRuns() ([]Run, error) {
	return s.queryRuns(`SELECT ` + runCols + ` FROM runs WHERE state IN ('pending','running') ORDER BY started`)
}

// RunsInJourney returns every run of a journey, oldest first.
func (s *Store) RunsInJourney(journey string) ([]Run, error) {
	return s.queryRuns(`SELECT `+runCols+` FROM runs WHERE journey = ? ORDER BY started, rowid`, journey)
}

// Unfollowed returns done or waiting runs the scheduler has not yet acted on,
// oldest finished first.
func (s *Store) Unfollowed() ([]Run, error) {
	return s.queryRuns(`SELECT ` + runCols + ` FROM runs WHERE state IN ('done','waiting') AND followed = 0 ORDER BY finished, rowid`)
}

func insertBug(x execer, b Bug) error {
	_, err := x.Exec(`INSERT INTO bugs(id, run_id, summary, step, evidence, severity, note, created) VALUES(?,?,?,?,?,?,?,?)`,
		b.ID, b.RunID, b.Summary, b.Step, b.Evidence, b.Severity, b.Note, ts(b.Created))
	return err
}

func (s *Store) AddBug(b Bug) error { return insertBug(s.db, b) }

func (s *Store) Bugs(runID string) ([]Bug, error) {
	rows, err := s.db.Query(`SELECT id, run_id, summary, step, evidence, severity, note, created FROM bugs WHERE run_id = ? ORDER BY created, rowid`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Bug
	for rows.Next() {
		var b Bug
		var c int64
		if err := rows.Scan(&b.ID, &b.RunID, &b.Summary, &b.Step, &b.Evidence, &b.Severity, &b.Note, &c); err != nil {
			return nil, err
		}
		b.Created = fromTS(c)
		out = append(out, b)
	}
	return out, rows.Err()
}
