package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Message is a persona's inbound email. It mirrors inbox.Message (minus the
// routing verdict, which is captured in Filtered) plus the IMAP identity.
type Message struct {
	ID          string    `json:"id"`
	UIDValidity uint32    `json:"uidvalidity"`
	UID         uint32    `json:"uid"`
	Persona     string    `json:"persona"`
	From        string    `json:"from"`
	To          []string  `json:"to"`
	Subject     string    `json:"subject"`
	Text        string    `json:"text"`
	Links       []string  `json:"links"`
	Received    time.Time `json:"received"`
	Stored      time.Time `json:"stored"` // when the director saved it
	Filtered    string    `json:"filtered"`
	// Mailbox identifies the account the message came from (lower host|lower username), so
	// the same UIDVALIDITY and UID in another mailbox is not mistaken for a duplicate.
	Mailbox string `json:"-"`
}

type MessageQuery struct {
	Persona  string // "" = all personas
	Filtered bool   // false: only delivered mail; true: only filtered mail
	Since    time.Time
	Limit    int // default 50, max 200
}

const msgCols = `id, uidvalidity, uid, persona, sender, recipients, subject, body, links, received, stored, filtered`

func jsonList(v []string) string {
	if v == nil {
		return "[]"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// AddMessage stores m; added is false when (Mailbox, UIDValidity, UID) was already stored.
func (s *Store) AddMessage(m Message) (bool, error) {
	if m.Stored.IsZero() {
		m.Stored = time.Now()
	}
	res, err := s.db.Exec(`INSERT OR IGNORE INTO messages(`+msgCols+`, mailbox) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.UIDValidity, m.UID, m.Persona, m.From, jsonList(m.To), m.Subject, m.Text,
		jsonList(m.Links), ts(m.Received), ts(m.Stored), m.Filtered, m.Mailbox)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func scanMessage(sc interface{ Scan(...any) error }) (Message, error) {
	var m Message
	var to, links string
	var received, stored int64
	if err := sc.Scan(&m.ID, &m.UIDValidity, &m.UID, &m.Persona, &m.From, &to, &m.Subject, &m.Text,
		&links, &received, &stored, &m.Filtered, &m.Mailbox); err != nil {
		return Message{}, err
	}
	if err := json.Unmarshal([]byte(to), &m.To); err != nil {
		return Message{}, err
	}
	if err := json.Unmarshal([]byte(links), &m.Links); err != nil {
		return Message{}, err
	}
	m.Received, m.Stored = fromTS(received), fromTS(stored)
	return m, nil
}

// Messages returns matching messages, newest stored first.
func (s *Store) Messages(q MessageQuery) ([]Message, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	query := `SELECT ` + msgCols + `, mailbox FROM messages WHERE 1=1`
	var args []any
	if q.Persona != "" {
		query += ` AND persona = ?`
		args = append(args, q.Persona)
	}
	if q.Filtered {
		query += ` AND filtered != ''`
	} else {
		query += ` AND filtered = ''`
	}
	if !q.Since.IsZero() {
		query += ` AND stored >= ?`
		args = append(args, ts(q.Since))
	}
	query += ` ORDER BY stored DESC, rowid DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) Message(id string) (Message, error) {
	m, err := scanMessage(s.db.QueryRow(`SELECT `+msgCols+`, mailbox FROM messages WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrNotFound
	}
	return m, err
}

// MailCursor returns the last seen IMAP position; ok is false before the first set.
func (s *Store) MailCursor() (validity, last uint32, ok bool, err error) {
	err = s.db.QueryRow(`SELECT validity, last FROM mail_cursor WHERE id = 1`).Scan(&validity, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	return validity, last, true, nil
}

func (s *Store) SetMailCursor(validity, last uint32) error {
	_, err := s.db.Exec(`INSERT INTO mail_cursor(id, validity, last) VALUES(1,?,?)
		ON CONFLICT(id) DO UPDATE SET validity=excluded.validity, last=excluded.last`, validity, last)
	return err
}

// ClearMailCursor forgets the IMAP position, so the next poll starts at the mailbox's UIDNEXT.
func (s *Store) ClearMailCursor() error {
	_, err := s.db.Exec(`DELETE FROM mail_cursor WHERE id = 1`)
	return err
}

// PruneMessages deletes messages stored before the cutoff and returns the count.
func (s *Store) PruneMessages(before time.Time) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM messages WHERE stored < ?`, ts(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
