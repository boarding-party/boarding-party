package store

import (
	"database/sql"
	"errors"
	"time"
)

const keyPaused = "paused"

// Paused reports whether the cast is paused. Unset means running.
func (s *Store) Paused() (bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, keyPaused).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return v == "1", err
}

// SetPaused pauses or resumes the cast.
func (s *Store) SetPaused(paused bool) error {
	v := "0"
	if paused {
		v = "1"
	}
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, keyPaused, v)
	return err
}

// RunsSince returns runs started at or after t, oldest first.
func (s *Store) RunsSince(t time.Time) ([]Run, error) {
	return s.queryRuns(`SELECT `+runCols+` FROM runs WHERE started >= ? ORDER BY started, rowid`, ts(t))
}
