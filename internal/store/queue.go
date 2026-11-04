package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Queued is a run waiting for the scheduler to start it.
type Queued struct {
	ID      string          `json:"id"`
	Persona string          `json:"persona"`
	Role    string          `json:"role"`
	Story   string          `json:"story"`
	Journey string          `json:"journey"`
	Follows string          `json:"follows"`
	Via     string          `json:"via"`
	Context json.RawMessage `json:"context,omitempty"`
	Reason  string          `json:"reason"` // board text, e.g. "Handed off by Ada (book)"
	Created time.Time       `json:"created"`
}

const queueCols = `id, persona, role, story, journey, follows, via, context, reason, created`

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func insertQueued(x execer, q Queued) error {
	if q.ID == "" {
		return errors.New("queued run needs an id")
	}
	_, err := x.Exec(`INSERT INTO queue(`+queueCols+`) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		q.ID, q.Persona, q.Role, q.Story, q.Journey, q.Follows, q.Via, string(q.Context), q.Reason, ts(q.Created))
	return err
}

// Enqueue adds a run to the queue.
func (s *Store) Enqueue(q Queued) error { return insertQueued(s.db, q) }

// Queue returns the queued runs, first in first out.
func (s *Store) Queue() ([]Queued, error) {
	rows, err := s.db.Query(`SELECT ` + queueCols + ` FROM queue ORDER BY created, rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Queued
	for rows.Next() {
		var q Queued
		var ctx string
		var created int64
		if err := rows.Scan(&q.ID, &q.Persona, &q.Role, &q.Story, &q.Journey, &q.Follows, &q.Via, &ctx, &q.Reason, &created); err != nil {
			return nil, err
		}
		if ctx != "" {
			q.Context = json.RawMessage(ctx)
		}
		q.Created = fromTS(created)
		out = append(out, q)
	}
	return out, rows.Err()
}

// DeleteQueued removes a queued run; ErrNotFound if it is not there.
func (s *Store) DeleteQueued(id string) error {
	res, err := s.db.Exec(`DELETE FROM queue WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// FollowUp marks a run as acted on and queues its follow-ups, atomically.
func (s *Store) FollowUp(runID string, next []Queued) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE runs SET followed = 1 WHERE id = ?`, runID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	for _, q := range next {
		if err := insertQueued(tx, q); err != nil {
			return err
		}
	}
	return tx.Commit()
}
