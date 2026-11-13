package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// JourneyState says whether a journey's live revision is replayed.
type JourneyState string

const (
	JourneyLive      JourneyState = "live"
	JourneyPaused    JourneyState = "paused"
	JourneyRepairing JourneyState = "repairing"
)

// Journey is what one persona does for one story, as replayable code.
type Journey struct {
	ID           string       `json:"id"`
	Persona      string       `json:"persona"`
	Story        string       `json:"story"`
	State        JourneyState `json:"state"`
	LiveRev      int          `json:"liveRev"` // 0 while only bug revisions exist
	Created      time.Time    `json:"created"`
	Passed       int          `json:"passed"`
	Failed       int          `json:"failed"`
	LastReplay   time.Time    `json:"lastReplay"`
	LastExplored time.Time    `json:"lastExplored"` // last AI run that decided about it
	// FlakyStreak counts replays in a row that failed and then passed with
	// the same inputs.
	FlakyStreak int `json:"flakyStreak"`
	// BlockedStep and BlockedBug: replays fail at this step because of this
	// reported bug; while its issue is open, such a failure calls no copilot.
	BlockedStep int       `json:"blockedStep,omitempty"`
	BlockedBug  string    `json:"blockedBug,omitempty"`
	CopilotAt   time.Time `json:"copilotAt"` // last copilot call
}

// RevisionKind is full (the whole journey, may go live) or bug (reproduces
// one bug, never live).
type RevisionKind string

const (
	RevisionFull RevisionKind = "full"
	RevisionBug  RevisionKind = "bug"
)

// Revision is one version of a journey's code.
type Revision struct {
	JourneyID string       `json:"journeyId"`
	N         int          `json:"n"`
	Kind      RevisionKind `json:"kind"`
	Code      string       `json:"code"`
	Reason    string       `json:"reason"`
	RunID     string       `json:"runId"`
	Parent    int          `json:"parent"` // the live revision when this one was made
	BugID     string       `json:"bugId,omitempty"`
	BugStep   int          `json:"bugStep,omitempty"`
	Created   time.Time    `json:"created"`
}

// Decision is why something about a journey changed, or did not.
type Decision struct {
	ID        string    `json:"id"`
	JourneyID string    `json:"journeyId"`
	RunID     string    `json:"runId,omitempty"`
	Kind      string    `json:"kind"`
	Reason    string    `json:"reason"`
	Actor     string    `json:"actor"` // "model", or who acted on the board
	Evidence  []string  `json:"evidence"`
	RevBefore int       `json:"revBefore"`
	RevAfter  int       `json:"revAfter"`
	Created   time.Time `json:"created"`

	// Explored marks the journey explored at Created: a fresh AI run
	// decided. Copilot and people's decisions leave it.
	Explored bool `json:"-"`
	// Resolves ends a repair: the journey goes live (a paused one stays
	// paused), its flaky streak restarts and it is blocked at BlockStep by
	// BlockBug (0 and "": not blocked).
	Resolves  bool   `json:"-"`
	BlockStep int    `json:"-"`
	BlockBug  string `json:"-"`
}

const journeyCols = `id, persona, story, state, live_rev, created, passed, failed, last_replay, last_explored,
	flaky_streak, blocked_step, blocked_bug, copilot_at`

func scanJourney(sc interface{ Scan(...any) error }) (Journey, error) {
	var j Journey
	var created, replay, explored, copilot int64
	err := sc.Scan(&j.ID, &j.Persona, &j.Story, &j.State, &j.LiveRev, &created, &j.Passed, &j.Failed, &replay, &explored,
		&j.FlakyStreak, &j.BlockedStep, &j.BlockedBug, &copilot)
	if errors.Is(err, sql.ErrNoRows) {
		return Journey{}, ErrNotFound
	}
	if err != nil {
		return Journey{}, err
	}
	j.Created, j.LastReplay, j.LastExplored, j.CopilotAt = fromTS(created), fromTS(replay), fromTS(explored), fromTS(copilot)
	return j, nil
}

// JourneyFor returns persona's journey for story, or ErrNotFound.
func (s *Store) JourneyFor(persona, story string) (Journey, error) {
	return scanJourney(s.db.QueryRow(`SELECT `+journeyCols+` FROM journeys WHERE persona = ? AND story = ?`, persona, story))
}

// Journey returns one journey by id, or ErrNotFound.
func (s *Store) Journey(id string) (Journey, error) {
	return scanJourney(s.db.QueryRow(`SELECT `+journeyCols+` FROM journeys WHERE id = ?`, id))
}

// Journeys returns every journey, by persona then story.
func (s *Store) Journeys() ([]Journey, error) {
	rows, err := s.db.Query(`SELECT ` + journeyCols + ` FROM journeys ORDER BY persona, story`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Journey{}
	for rows.Next() {
		j, err := scanJourney(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// AddRevision stores r as the next revision of persona's journey for story,
// creating the journey on its first revision, and d as the decision that
// made it, in one transaction. A full revision goes live; a bug revision
// leaves the live one where it was. r.N, r.Parent and d's journey and
// revision fields are filled in here.
func (s *Store) AddRevision(persona, story string, r Revision, d Decision) (Journey, Revision, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Journey{}, Revision{}, err
	}
	defer tx.Rollback()
	j, err := ensureJourney(tx, persona, story, r.Created)
	if err != nil {
		return Journey{}, Revision{}, err
	}
	if err := tx.QueryRow(`SELECT COALESCE(MAX(n), 0) + 1 FROM journey_revisions WHERE journey_id = ?`, j.ID).Scan(&r.N); err != nil {
		return Journey{}, Revision{}, err
	}
	r.JourneyID, r.Parent = j.ID, j.LiveRev
	if r.Kind != RevisionBug {
		r.BugID, r.BugStep = "", 0
	}
	if _, err := tx.Exec(`INSERT INTO journey_revisions(journey_id, n, kind, code, reason, run_id, parent, bug_id, bug_step, created)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, r.JourneyID, r.N, string(r.Kind), r.Code, r.Reason, r.RunID, r.Parent, r.BugID, r.BugStep, ts(r.Created)); err != nil {
		return Journey{}, Revision{}, err
	}
	if r.Kind == RevisionBug && r.BugID != "" {
		if err := linkBug(tx, r.BugID, j.ID, r.N, r.BugStep); err != nil {
			return Journey{}, Revision{}, err
		}
	}
	d.JourneyID, d.RevBefore = j.ID, j.LiveRev
	if r.Kind == RevisionFull {
		j.LiveRev = r.N
	}
	d.RevAfter = j.LiveRev
	if d.Explored {
		j.LastExplored = r.Created
	}
	if _, err := tx.Exec(`UPDATE journeys SET live_rev = ?, last_explored = ? WHERE id = ?`, j.LiveRev, ts(j.LastExplored), j.ID); err != nil {
		return Journey{}, Revision{}, err
	}
	if err := resolve(tx, j.ID, d); err != nil {
		return Journey{}, Revision{}, err
	}
	if err := insertDecision(tx, &d); err != nil {
		return Journey{}, Revision{}, err
	}
	j, err = scanJourney(tx.QueryRow(`SELECT `+journeyCols+` FROM journeys WHERE id = ?`, j.ID))
	if err != nil {
		return Journey{}, Revision{}, err
	}
	return j, r, tx.Commit()
}

// EnsureJourney returns persona's journey for story, creating it with no
// revisions when there is none: a decision needs a journey to belong to.
func (s *Store) EnsureJourney(persona, story string, at time.Time) (Journey, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Journey{}, err
	}
	defer tx.Rollback()
	j, err := ensureJourney(tx, persona, story, at)
	if err != nil {
		return Journey{}, err
	}
	return j, tx.Commit()
}

func ensureJourney(tx *sql.Tx, persona, story string, at time.Time) (Journey, error) {
	j, err := scanJourney(tx.QueryRow(`SELECT `+journeyCols+` FROM journeys WHERE persona = ? AND story = ?`, persona, story))
	if !errors.Is(err, ErrNotFound) {
		return j, err
	}
	id, err := newIssueID()
	if err != nil {
		return Journey{}, err
	}
	j = Journey{ID: id, Persona: persona, Story: story, State: JourneyLive, Created: at}
	_, err = tx.Exec(`INSERT INTO journeys(id, persona, story, state, created) VALUES(?,?,?,?,?)`,
		j.ID, persona, story, string(j.State), ts(at))
	return j, err
}

// RunDecided reports whether a run has left any decision about a journey.
func (s *Store) RunDecided(runID string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT count(*) FROM journey_decisions WHERE run_id = ?`, runID).Scan(&n)
	return n > 0, err
}

const revisionCols = `journey_id, n, kind, code, reason, run_id, parent, bug_id, bug_step, created`

func scanRevision(sc interface{ Scan(...any) error }) (Revision, error) {
	var r Revision
	var created int64
	err := sc.Scan(&r.JourneyID, &r.N, &r.Kind, &r.Code, &r.Reason, &r.RunID, &r.Parent, &r.BugID, &r.BugStep, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Revision{}, ErrNotFound
	}
	r.Created = fromTS(created)
	return r, err
}

// Revisions returns a journey's revisions, newest first.
func (s *Store) Revisions(journeyID string) ([]Revision, error) {
	rows, err := s.db.Query(`SELECT `+revisionCols+` FROM journey_revisions WHERE journey_id = ? ORDER BY n DESC`, journeyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Revision{}
	for rows.Next() {
		r, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Revision returns revision n of a journey; n 0 is the live one. ErrNotFound
// when there is no such revision (or, for 0, none is live).
func (s *Store) Revision(journeyID string, n int) (Revision, error) {
	if n == 0 {
		return scanRevision(s.db.QueryRow(`SELECT `+revisionCols+` FROM journey_revisions
			WHERE journey_id = ? AND n = (SELECT live_rev FROM journeys WHERE id = ?)`, journeyID, journeyID))
	}
	return scanRevision(s.db.QueryRow(`SELECT `+revisionCols+` FROM journey_revisions WHERE journey_id = ? AND n = ?`, journeyID, n))
}

// AddDecision stores a decision about an existing journey, applying its
// Explored and Resolves.
func (s *Store) AddDecision(d Decision) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	explored := int64(0) // MAX leaves last_explored alone
	if d.Explored {
		explored = ts(d.Created)
	}
	res, err := tx.Exec(`UPDATE journeys SET last_explored = MAX(last_explored, ?) WHERE id = ?`, explored, d.JourneyID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := resolve(tx, d.JourneyID, d); err != nil {
		return err
	}
	if err := insertDecision(tx, &d); err != nil {
		return err
	}
	return tx.Commit()
}

// ErrJourneyChanged means a journey was not in the state a change expected:
// someone else changed it first.
var ErrJourneyChanged = errors.New("the journey changed meanwhile")

// SetJourneyStateFrom is SetJourneyState only while the journey is in state
// from; otherwise ErrJourneyChanged and nothing is written.
func (s *Store) SetJourneyStateFrom(id string, from, to JourneyState, d Decision) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	j, err := scanJourney(tx.QueryRow(`SELECT `+journeyCols+` FROM journeys WHERE id = ?`, id))
	if err != nil {
		return err
	}
	if j.State != from {
		return ErrJourneyChanged
	}
	if _, err := tx.Exec(`UPDATE journeys SET state = ? WHERE id = ?`, string(to), id); err != nil {
		return err
	}
	d.JourneyID, d.RevBefore, d.RevAfter = id, j.LiveRev, j.LiveRev
	if err := insertDecision(tx, &d); err != nil {
		return err
	}
	return tx.Commit()
}

// SetJourneyState moves a journey to state to and records d, in one
// transaction. ErrNotFound if there is no such journey.
func (s *Store) SetJourneyState(id string, to JourneyState, d Decision) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	j, err := scanJourney(tx.QueryRow(`SELECT `+journeyCols+` FROM journeys WHERE id = ?`, id))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE journeys SET state = ? WHERE id = ?`, string(to), id); err != nil {
		return err
	}
	d.JourneyID, d.RevBefore, d.RevAfter = id, j.LiveRev, j.LiveRev
	if err := insertDecision(tx, &d); err != nil {
		return err
	}
	return tx.Commit()
}

// ErrBugRevision means a bug revision was asked to go live.
var ErrBugRevision = errors.New("a bug revision reproduces one bug and never goes live")

// Rollback copies full revision n as the journey's next revision, which goes
// live, and records d, in one transaction. The new revision's reason is d's.
// ErrNotFound for no such journey or revision; ErrBugRevision for a bug one.
func (s *Store) Rollback(id string, n int, d Decision) (Journey, Revision, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Journey{}, Revision{}, err
	}
	defer tx.Rollback()
	j, err := scanJourney(tx.QueryRow(`SELECT `+journeyCols+` FROM journeys WHERE id = ?`, id))
	if err != nil {
		return Journey{}, Revision{}, err
	}
	old, err := scanRevision(tx.QueryRow(`SELECT `+revisionCols+` FROM journey_revisions WHERE journey_id = ? AND n = ?`, id, n))
	if err != nil {
		return Journey{}, Revision{}, err
	}
	if old.Kind == RevisionBug {
		return Journey{}, Revision{}, ErrBugRevision
	}
	r := Revision{JourneyID: id, Kind: RevisionFull, Code: old.Code, Reason: d.Reason, RunID: d.RunID, Parent: j.LiveRev, Created: d.Created}
	if err := tx.QueryRow(`SELECT COALESCE(MAX(n), 0) + 1 FROM journey_revisions WHERE journey_id = ?`, id).Scan(&r.N); err != nil {
		return Journey{}, Revision{}, err
	}
	if _, err := tx.Exec(`INSERT INTO journey_revisions(journey_id, n, kind, code, reason, run_id, parent, created)
		VALUES(?,?,?,?,?,?,?,?)`, r.JourneyID, r.N, string(r.Kind), r.Code, r.Reason, r.RunID, r.Parent, ts(r.Created)); err != nil {
		return Journey{}, Revision{}, err
	}
	d.JourneyID, d.RevBefore, d.RevAfter = id, j.LiveRev, r.N
	j.LiveRev = r.N
	if _, err := tx.Exec(`UPDATE journeys SET live_rev = ? WHERE id = ?`, j.LiveRev, id); err != nil {
		return Journey{}, Revision{}, err
	}
	if err := insertDecision(tx, &d); err != nil {
		return Journey{}, Revision{}, err
	}
	return j, r, tx.Commit()
}

// DeleteJourney deletes a journey, its revisions and its queued replays, and
// records d; every decision about it stays. ErrNotFound if it is not there.
func (s *Store) DeleteJourney(id string, d Decision) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	j, err := scanJourney(tx.QueryRow(`SELECT `+journeyCols+` FROM journeys WHERE id = ?`, id))
	if err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM journeys WHERE id = ?`,
		`DELETE FROM journey_revisions WHERE journey_id = ?`,
		`DELETE FROM queue WHERE journey_id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	d.JourneyID, d.RevBefore, d.RevAfter = id, j.LiveRev, 0
	if err := insertDecision(tx, &d); err != nil {
		return err
	}
	return tx.Commit()
}

// resolve applies d.Resolves to journey id. A block by a bug links that
// report to the live revision at the blocked step.
func resolve(tx *sql.Tx, id string, d Decision) error {
	if !d.Resolves {
		return nil
	}
	if _, err := tx.Exec(`UPDATE journeys SET state = CASE state WHEN 'paused' THEN 'paused' ELSE 'live' END,
		flaky_streak = 0, blocked_step = ?, blocked_bug = ? WHERE id = ?`, d.BlockStep, d.BlockBug, id); err != nil {
		return err
	}
	if d.BlockBug == "" || d.BlockStep < 1 {
		return nil
	}
	var live int
	if err := tx.QueryRow(`SELECT live_rev FROM journeys WHERE id = ?`, id).Scan(&live); err != nil {
		return err
	}
	return linkBug(tx, d.BlockBug, id, live, d.BlockStep)
}

// linkBug links a bug report to a journey revision and step, unless it is
// linked already: the first link wins.
func linkBug(tx *sql.Tx, bugID, journeyID string, rev, step int) error {
	if rev < 1 || step < 1 {
		return nil
	}
	_, err := tx.Exec(`UPDATE bugs SET journey_id = ?, journey_rev = ?, journey_step = ? WHERE id = ? AND journey_id = ''`,
		journeyID, rev, step, bugID)
	return err
}

// ActorModel is the actor of decisions an AI run made.
const ActorModel = "model"

// ActorDirector is the actor of decisions the scheduler made on its own.
const ActorDirector = "director"

// ReplayFollow is what the scheduler does after a replay ended, applied in
// one transaction with marking the replay followed.
type ReplayFollow struct {
	Next         []Queued
	Decisions    []Decision   // each JourneyID is the replay's journey
	State        JourneyState // "" leaves the state
	From         JourneyState // with State: change it only from this state, else ErrJourneyChanged
	FlakyStreak  *int         // nil leaves it
	ClearBlocked bool
	CopilotAt    time.Time // zero leaves it
}

// FollowReplay marks a replay run followed and applies f to its journey.
// ErrNotFound if the run is not there or was already followed.
func (s *Store) FollowReplay(runID string, f ReplayFollow) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var journeyID string
	err = tx.QueryRow(`SELECT journey_id FROM runs WHERE id = ? AND followed = 0`, runID).Scan(&journeyID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE runs SET followed = 1 WHERE id = ?`, runID); err != nil {
		return err
	}
	for _, q := range f.Next {
		if err := insertQueued(tx, q); err != nil {
			return err
		}
	}
	if f.State != "" && f.From != "" {
		res, err := tx.Exec(`UPDATE journeys SET state = ? WHERE id = ? AND state = ?`, string(f.State), journeyID, string(f.From))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrJourneyChanged
		}
		f.State = ""
	}
	sets, args := []string{}, []any{}
	if f.State != "" {
		sets, args = append(sets, "state = ?"), append(args, string(f.State))
	}
	if f.FlakyStreak != nil {
		sets, args = append(sets, "flaky_streak = ?"), append(args, *f.FlakyStreak)
	}
	if f.ClearBlocked {
		sets = append(sets, "blocked_step = 0", "blocked_bug = ''")
	}
	if !f.CopilotAt.IsZero() {
		sets, args = append(sets, "copilot_at = ?"), append(args, ts(f.CopilotAt))
	}
	if len(sets) > 0 {
		if _, err := tx.Exec(`UPDATE journeys SET `+strings.Join(sets, ", ")+` WHERE id = ?`, append(args, journeyID)...); err != nil {
			return err
		}
	}
	for _, d := range f.Decisions {
		j, err := scanJourney(tx.QueryRow(`SELECT `+journeyCols+` FROM journeys WHERE id = ?`, d.JourneyID))
		if err != nil {
			return err
		}
		d.RevBefore, d.RevAfter = j.LiveRev, j.LiveRev
		if err := insertDecision(tx, &d); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetBlocked records that the journey's replays fail at step because of bug.
func (s *Store) SetBlocked(id string, step int, bug string) error {
	res, err := s.db.Exec(`UPDATE journeys SET blocked_step = ?, blocked_bug = ? WHERE id = ?`, step, bug, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func insertDecision(tx *sql.Tx, d *Decision) error {
	if d.Evidence == nil {
		d.Evidence = []string{}
	}
	ev, err := json.Marshal(d.Evidence)
	if err != nil {
		return err
	}
	res, err := tx.Exec(`INSERT INTO journey_decisions(journey_id, run_id, kind, reason, actor, evidence, rev_before, rev_after, created)
		VALUES(?,?,?,?,?,?,?,?,?)`, d.JourneyID, d.RunID, d.Kind, d.Reason, d.Actor, string(ev), d.RevBefore, d.RevAfter, ts(d.Created))
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	d.ID = strconv.FormatInt(id, 10)
	return err
}

// Decisions returns a journey's decisions, newest first.
func (s *Store) Decisions(journeyID string) ([]Decision, error) {
	rows, err := s.db.Query(`SELECT id, journey_id, run_id, kind, reason, actor, evidence, rev_before, rev_after, created
		FROM journey_decisions WHERE journey_id = ? ORDER BY created DESC, id DESC`, journeyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Decision{}
	for rows.Next() {
		var d Decision
		var id, created int64
		var ev string
		if err := rows.Scan(&id, &d.JourneyID, &d.RunID, &d.Kind, &d.Reason, &d.Actor, &ev, &d.RevBefore, &d.RevAfter, &created); err != nil {
			return nil, err
		}
		d.ID, d.Created = strconv.FormatInt(id, 10), fromTS(created)
		if err := json.Unmarshal([]byte(ev), &d.Evidence); err != nil || d.Evidence == nil {
			d.Evidence = []string{}
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CountReplay adds one replay's result to a journey.
func (s *Store) CountReplay(journeyID string, passed bool, at time.Time) error {
	col := "failed"
	if passed {
		col = "passed"
	}
	res, err := s.db.Exec(`UPDATE journeys SET `+col+` = `+col+` + 1, last_replay = ? WHERE id = ?`, ts(at), journeyID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
