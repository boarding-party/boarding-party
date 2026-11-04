package store

import (
	"strings"
	"time"
)

type WaitStatus string

const (
	WaitOpen  WaitStatus = "open"
	WaitMet   WaitStatus = "met"
	WaitStuck WaitStatus = "stuck"
)

// Wait is the condition a waiting run is parked on.
type Wait struct {
	RunID    string     `json:"runId"`
	Kind     string     `json:"kind"` // "email" | "handoff" | "time"
	From     string     `json:"from,omitempty"`
	Subject  string     `json:"subject,omitempty"`
	Role     string     `json:"role,omitempty"`
	Story    string     `json:"story,omitempty"`
	At       time.Time  `json:"at,omitzero"`
	Began    time.Time  `json:"began"`
	Deadline time.Time  `json:"deadline"`
	Status   WaitStatus `json:"status"`
	Detail   string     `json:"detail,omitempty"`
	Ended    time.Time  `json:"ended,omitzero"`
}

const waitCols = `run_id, kind, sender, subject, role, story, at, began, deadline, status, detail, ended`

// FinishRunWaiting moves an active run to waiting and records w as open, in
// one transaction. It returns false, writing nothing, if the run was not active.
func (s *Store) FinishRunWaiting(id, outcome string, finished time.Time, w Wait) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE runs SET state=?, outcome=?, finished=? WHERE id=? AND state IN ('pending','running')`,
		string(RunWaiting), outcome, ts(finished), id)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	_, err = tx.Exec(`INSERT INTO waits(`+waitCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, w.Kind, w.From, w.Subject, w.Role, w.Story, ts(w.At), ts(finished), ts(w.Deadline), string(WaitOpen), "", 0)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func scanWait(sc interface{ Scan(...any) error }) (Wait, error) {
	var w Wait
	var status string
	var at, began, deadline, ended int64
	if err := sc.Scan(&w.RunID, &w.Kind, &w.From, &w.Subject, &w.Role, &w.Story, &at, &began, &deadline, &status, &w.Detail, &ended); err != nil {
		return Wait{}, err
	}
	w.Status = WaitStatus(status)
	w.At, w.Began, w.Deadline, w.Ended = fromTS(at), fromTS(began), fromTS(deadline), fromTS(ended)
	return w, nil
}

func (s *Store) queryWaits(q string, args ...any) ([]Wait, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Wait
	for rows.Next() {
		w, err := scanWait(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// OpenWaits returns the unresolved waits, oldest first.
func (s *Store) OpenWaits() ([]Wait, error) {
	return s.queryWaits(`SELECT ` + waitCols + ` FROM waits WHERE status = 'open' ORDER BY began, rowid`)
}

// WaitFor returns a run's wait; ErrNotFound if it has none.
func (s *Store) WaitFor(runID string) (Wait, error) {
	ws, err := s.queryWaits(`SELECT `+waitCols+` FROM waits WHERE run_id = ?`, runID)
	if err != nil {
		return Wait{}, err
	}
	if len(ws) == 0 {
		return Wait{}, ErrNotFound
	}
	return ws[0], nil
}

// WaitsFor returns the waits of the given runs, keyed by run id.
func (s *Store) WaitsFor(runIDs []string) (map[string]Wait, error) {
	out := map[string]Wait{}
	if len(runIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(runIDs))
	for i, id := range runIDs {
		args[i] = id
	}
	ws, err := s.queryWaits(`SELECT `+waitCols+` FROM waits WHERE run_id IN (?`+strings.Repeat(",?", len(runIDs)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	for _, w := range ws {
		out[w.RunID] = w
	}
	return out, nil
}

// Resolution is how an open wait ended, with what follows from it.
type Resolution struct {
	Status  WaitStatus
	Detail  string
	Ended   time.Time
	Next    *Queued
	Finding *Bug
}

// ResolveWait closes an open wait at most once. It returns false, doing
// nothing else, if the wait was not open.
func (s *Store) ResolveWait(runID string, res Resolution) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	r, err := tx.Exec(`UPDATE waits SET status=?, detail=?, ended=? WHERE run_id=? AND status='open'`,
		string(res.Status), res.Detail, ts(res.Ended), runID)
	if err != nil {
		return false, err
	}
	if n, err := r.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	if res.Next != nil {
		if err := insertQueued(tx, *res.Next); err != nil {
			return false, err
		}
	}
	if res.Finding != nil {
		if err := insertBug(tx, *res.Finding); err != nil {
			return false, err
		}
	}
	if res.Status == WaitStuck {
		if _, err := tx.Exec(`UPDATE runs SET state='stuck' WHERE id=? AND state='waiting'`, runID); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}
