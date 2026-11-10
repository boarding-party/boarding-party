package store

import (
	"strings"
	"time"
)

const (
	// MaxConsoleEvents caps what one run keeps; later events are dropped.
	MaxConsoleEvents = 2000
	// MaxConsoleText caps one event's text, in bytes.
	MaxConsoleText = 2000
	// MaxConsoleURL caps one event's URL, in bytes.
	MaxConsoleURL = 2048
)

// cut shortens s to at most n bytes without splitting a character.
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

// ConsoleEvent is one thing a run's browser logged. Level is error, warning
// or info; Kind is console, exception, log or network.
type ConsoleEvent struct {
	At    time.Time `json:"at"`
	Level string    `json:"level"`
	Kind  string    `json:"kind"`
	Text  string    `json:"text"`
	URL   string    `json:"url,omitempty"`
}

// AddConsole appends events to a run's console, up to MaxConsoleEvents.
func (s *Store) AddConsole(runID string, evs []ConsoleEvent) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM console WHERE run_id = ?`, runID).Scan(&n); err != nil {
		return err
	}
	for _, e := range evs {
		if n >= MaxConsoleEvents {
			break
		}
		e.Text, e.URL = cut(e.Text, MaxConsoleText), cut(e.URL, MaxConsoleURL)
		if _, err := tx.Exec(`INSERT INTO console(run_id, at, level, kind, text, url) VALUES(?, ?, ?, ?, ?, ?)`,
			runID, ts(e.At), e.Level, e.Kind, e.Text, e.URL); err != nil {
			return err
		}
		n++
	}
	return tx.Commit()
}

// Console returns a run's console events in the order they arrived.
func (s *Store) Console(runID string) ([]ConsoleEvent, error) {
	rows, err := s.db.Query(`SELECT at, level, kind, text, url FROM console WHERE run_id = ? ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConsoleEvent{}
	for rows.Next() {
		var e ConsoleEvent
		var at int64
		if err := rows.Scan(&at, &e.Level, &e.Kind, &e.Text, &e.URL); err != nil {
			return nil, err
		}
		e.At = fromTS(at)
		out = append(out, e)
	}
	return out, rows.Err()
}
