package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// PersonaProfile returns the persona's saved browser storage state and when
// it was saved; nil if there is none.
func (s *Store) PersonaProfile(persona string) (json.RawMessage, time.Time, error) {
	var st string
	var saved int64
	err := s.db.QueryRow(`SELECT state, saved FROM persona_profile WHERE persona = ?`, persona).Scan(&st, &saved)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	return json.RawMessage(st), fromTS(saved), nil
}

// SavePersonaProfile replaces the persona's browser storage state.
func (s *Store) SavePersonaProfile(persona string, state json.RawMessage, now time.Time) error {
	_, err := s.db.Exec(`INSERT INTO persona_profile(persona, state, saved) VALUES(?, ?, ?)
		ON CONFLICT(persona) DO UPDATE SET state = excluded.state, saved = excluded.saved`,
		persona, string(state), ts(now))
	return err
}

// DeletePersonaProfile forgets the persona's browser storage state; a missing
// profile is not an error.
func (s *Store) DeletePersonaProfile(persona string) error {
	_, err := s.db.Exec(`DELETE FROM persona_profile WHERE persona = ?`, persona)
	return err
}
