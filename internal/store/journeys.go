package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
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
}

const journeyCols = `id, persona, story, state, live_rev, created, passed, failed, last_replay, last_explored`

func scanJourney(sc interface{ Scan(...any) error }) (Journey, error) {
	var j Journey
	var created, replay, explored int64
	err := sc.Scan(&j.ID, &j.Persona, &j.Story, &j.State, &j.LiveRev, &created, &j.Passed, &j.Failed, &replay, &explored)
	if errors.Is(err, sql.ErrNoRows) {
		return Journey{}, ErrNotFound
	}
	if err != nil {
		return Journey{}, err
	}
	j.Created, j.LastReplay, j.LastExplored = fromTS(created), fromTS(replay), fromTS(explored)
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
	d.JourneyID, d.RevBefore = j.ID, j.LiveRev
	if r.Kind == RevisionFull {
		j.LiveRev = r.N
	}
	d.RevAfter = j.LiveRev
	j.LastExplored = r.Created
	if _, err := tx.Exec(`UPDATE journeys SET live_rev = ?, last_explored = ? WHERE id = ?`, j.LiveRev, ts(j.LastExplored), j.ID); err != nil {
		return Journey{}, Revision{}, err
	}
	if err := insertDecision(tx, &d); err != nil {
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

// AddDecision stores a decision about an existing journey. A model's
// decision also marks the journey as explored at that time.
func (s *Store) AddDecision(d Decision) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	explored := int64(0) // MAX leaves last_explored alone
	if d.Actor == ActorModel {
		explored = ts(d.Created)
	}
	res, err := tx.Exec(`UPDATE journeys SET last_explored = MAX(last_explored, ?) WHERE id = ?`, explored, d.JourneyID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
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

// ActorModel is the actor of decisions an AI run made.
const ActorModel = "model"

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
