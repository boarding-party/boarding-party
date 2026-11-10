package store

import "time"

// AuditEntry is one board action: who, what, on which thing.
type AuditEntry struct {
	At      time.Time `json:"at"`
	Actor   string    `json:"actor"`
	Action  string    `json:"action"`
	Subject string    `json:"subject"`
}

// Audit records a board action.
func (s *Store) Audit(actor, action, subject string, at time.Time) error {
	_, err := s.db.Exec(`INSERT INTO audit(at, actor, action, subject) VALUES(?, ?, ?, ?)`, ts(at), actor, action, subject)
	return err
}

// AuditLog returns the newest limit entries, newest first.
func (s *Store) AuditLog(limit int) ([]AuditEntry, error) {
	rows, err := s.db.Query(`SELECT at, actor, action, subject FROM audit ORDER BY at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var at int64
		if err := rows.Scan(&at, &e.Actor, &e.Action, &e.Subject); err != nil {
			return nil, err
		}
		e.At = fromTS(at)
		out = append(out, e)
	}
	return out, rows.Err()
}
