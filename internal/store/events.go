package store

import "time"

// Event is a calendar entry synced from a persona's invitations. Start moving
// is what makes it fire again: FiredStart is the start it last fired for.
type Event struct {
	Persona, UID, Title string
	Start, End          time.Time
	Location, URL       string
	MessageID           string // the invitation email it came from
	FiredStart          time.Time
	FiredStory          string
}

const eventCols = `persona, uid, title, start, "end", location, url, message_id, fired_start, fired_story`

// UpsertEvent inserts the event or updates its fields, keeping the fired state.
func (s *Store) UpsertEvent(e Event) error {
	_, err := s.db.Exec(`INSERT INTO events(persona, uid, title, start, "end", location, url, message_id) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(persona, uid) DO UPDATE SET title = excluded.title, start = excluded.start, "end" = excluded."end",
		location = excluded.location, url = excluded.url, message_id = excluded.message_id`,
		e.Persona, e.UID, e.Title, ts(e.Start), ts(e.End), e.Location, e.URL, e.MessageID)
	return err
}

func (s *Store) DeleteEvent(persona, uid string) error {
	_, err := s.db.Exec(`DELETE FROM events WHERE persona = ? AND uid = ?`, persona, uid)
	return err
}

// Events returns events starting in [from, to], ordered by start; persona "" = all.
func (s *Store) Events(persona string, from, to time.Time) ([]Event, error) {
	q := `SELECT ` + eventCols + ` FROM events WHERE start >= ? AND start <= ?`
	args := []any{ts(from), ts(to)}
	if persona != "" {
		q += ` AND persona = ?`
		args = append(args, persona)
	}
	return s.queryEvents(q+` ORDER BY start, persona, uid`, args...)
}

// DueEvents returns events that start within maxLead of now (or began up to an
// hour ago) and have not fired for their current start.
func (s *Store) DueEvents(now time.Time, maxLead time.Duration) ([]Event, error) {
	return s.queryEvents(`SELECT `+eventCols+` FROM events
		WHERE start <= ? AND start >= ? AND fired_start != start ORDER BY start, persona, uid`,
		ts(now.Add(maxLead)), ts(now.Add(-time.Hour)))
}

// MarkEventFired records that story was started for the event at start.
func (s *Store) MarkEventFired(persona, uid string, start time.Time, story string) error {
	_, err := s.db.Exec(`UPDATE events SET fired_start = ?, fired_story = ? WHERE persona = ? AND uid = ?`,
		ts(start), story, persona, uid)
	return err
}

func (s *Store) queryEvents(q string, args ...any) ([]Event, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var start, end, fired int64
		if err := rows.Scan(&e.Persona, &e.UID, &e.Title, &start, &end, &e.Location, &e.URL, &e.MessageID, &fired, &e.FiredStory); err != nil {
			return nil, err
		}
		e.Start, e.End, e.FiredStart = fromTS(start), fromTS(end), fromTS(fired)
		out = append(out, e)
	}
	return out, rows.Err()
}
