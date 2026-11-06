package store

import (
	"strings"
	"time"
)

// Due is when a persona is next scheduled to act, and which of its role's
// stories comes first then.
type Due struct {
	Persona   string
	Activity  string // the role's activity text this due was computed from
	At        time.Time
	NextStory int // index into the role's stories
}

// Dues returns every persona's due, keyed by persona.
func (s *Store) Dues() (map[string]Due, error) {
	rows, err := s.db.Query(`SELECT persona, activity, due, next_story FROM persona_due`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Due{}
	for rows.Next() {
		var d Due
		var at int64
		if err := rows.Scan(&d.Persona, &d.Activity, &at, &d.NextStory); err != nil {
			return nil, err
		}
		d.At = fromTS(at)
		out[d.Persona] = d
	}
	return out, rows.Err()
}

// SetDue inserts or replaces a persona's due.
func (s *Store) SetDue(d Due) error {
	_, err := s.db.Exec(`INSERT INTO persona_due(persona, activity, due, next_story) VALUES(?,?,?,?)
		ON CONFLICT(persona) DO UPDATE SET activity = excluded.activity, due = excluded.due, next_story = excluded.next_story`,
		d.Persona, d.Activity, ts(d.At), d.NextStory)
	return err
}

// DeleteDuesExcept removes the dues of every persona not in personas.
func (s *Store) DeleteDuesExcept(personas []string) error {
	q := `DELETE FROM persona_due`
	args := make([]any, len(personas))
	for i, p := range personas {
		args[i] = p
	}
	if len(personas) > 0 {
		q += ` WHERE persona NOT IN (?` + strings.Repeat(",?", len(personas)-1) + `)`
	}
	_, err := s.db.Exec(q, args...)
	return err
}

// LastRun returns the persona's newest run of a story; ErrNotFound if none.
func (s *Store) LastRun(persona, story string) (Run, error) {
	rs, err := s.queryRuns(`SELECT `+runCols+` FROM runs WHERE persona = ? AND story = ? ORDER BY started DESC, rowid DESC LIMIT 1`, persona, story)
	if err != nil {
		return Run{}, err
	}
	if len(rs) == 0 {
		return Run{}, ErrNotFound
	}
	return rs[0], nil
}
