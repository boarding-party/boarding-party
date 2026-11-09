package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
)

// ErrExists means the name is already taken.
var ErrExists = errors.New("already exists")

// Child is a persona a lead made. Children live here, not in the user's
// project file; Project merges them in.
type Child struct {
	Name    string          `json:"name"`
	Lead    string          `json:"lead"`
	Reason  string          `json:"reason"`
	Persona project.Persona `json:"persona"`
	Stories []project.Story `json:"stories"`
	Created time.Time       `json:"created"`
	Stopped time.Time       `json:"stopped,omitzero"`
}

// childBody is what the body column holds.
type childBody struct {
	Persona project.Persona `json:"persona"`
	Stories []project.Story `json:"stories"`
}

// RawProject returns the user's project file as stored, without children.
func (s *Store) RawProject() (project.Project, error) {
	var body string
	err := s.db.QueryRow(`SELECT body FROM project WHERE id = 1`).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Project{}, ErrNotFound
	}
	if err != nil {
		return project.Project{}, err
	}
	var p project.Project
	err = json.Unmarshal([]byte(body), &p)
	return p, err
}

// Project returns the project the director runs: the user's file plus one
// role per child that is not stopped and whose lead is still a lead.
func (s *Store) Project() (project.Project, error) {
	p, err := s.RawProject()
	if err != nil {
		return p, err
	}
	cs, err := s.AllChildren()
	if err != nil {
		return project.Project{}, err
	}
	for _, c := range cs {
		if !c.Stopped.IsZero() {
			continue
		}
		if lead, _, ok := p.Persona(c.Lead); !ok || !lead.IsLead() {
			continue
		}
		pe := c.Persona
		pe.Name, pe.Lead = c.Name, nil // a child never has powers
		p.Roles = append(p.Roles, project.Role{Name: c.Name, Description: "Made by " + c.Lead, MadeBy: c.Lead,
			Personas: []project.Persona{pe}, Stories: c.Stories})
	}
	return p, nil
}

const childCols = `name, lead, body, reason, created, stopped`

func scanChild(sc interface{ Scan(...any) error }) (Child, error) {
	var c Child
	var body string
	var created, stopped int64
	if err := sc.Scan(&c.Name, &c.Lead, &body, &c.Reason, &created, &stopped); err != nil {
		return Child{}, err
	}
	var b childBody
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		return Child{}, err
	}
	c.Persona, c.Stories, c.Created, c.Stopped = b.Persona, b.Stories, fromTS(created), fromTS(stopped)
	return c, nil
}

func childJSON(c Child) (string, error) {
	b, err := json.Marshal(childBody{Persona: c.Persona, Stories: c.Stories})
	return string(b), err
}

// AddChild stores a new child; ErrExists if a child already has the name.
func (s *Store) AddChild(c Child) error {
	body, err := childJSON(c)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`INSERT INTO children(`+childCols+`) VALUES(?, ?, ?, ?, ?, ?) ON CONFLICT(name) DO NOTHING`,
		c.Name, c.Lead, body, c.Reason, ts(c.Created), ts(c.Stopped))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrExists
	}
	return nil
}

// UpdateChild replaces a child's persona and stories.
func (s *Store) UpdateChild(c Child) error {
	body, err := childJSON(c)
	if err != nil {
		return err
	}
	return affected(s.db.Exec(`UPDATE children SET body = ? WHERE name = ?`, body, c.Name))
}

// StopChild marks a child stopped: it stays listed but no longer runs.
func (s *Store) StopChild(name string, at time.Time) error {
	return affected(s.db.Exec(`UPDATE children SET stopped = ? WHERE name = ?`, ts(at), name))
}

// DeleteChild removes a child.
func (s *Store) DeleteChild(name string) error {
	return affected(s.db.Exec(`DELETE FROM children WHERE name = ?`, name))
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Child returns one child, or ErrNotFound.
func (s *Store) Child(name string) (Child, error) {
	c, err := scanChild(s.db.QueryRow(`SELECT `+childCols+` FROM children WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return Child{}, ErrNotFound
	}
	return c, err
}

// Children returns a lead's children, oldest first.
func (s *Store) Children(lead string) ([]Child, error) {
	return s.queryChildren(`WHERE lead = ? ORDER BY created, name`, lead)
}

// AllChildren returns every child, oldest first.
func (s *Store) AllChildren() ([]Child, error) {
	return s.queryChildren(`ORDER BY created, name`)
}

func (s *Store) queryChildren(where string, args ...any) ([]Child, error) {
	rows, err := s.db.Query(`SELECT `+childCols+` FROM children `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Child
	for rows.Next() {
		c, err := scanChild(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AdoptChild makes a child hand-made: p (the user's project with the child's
// role added) becomes the project file and the child is removed, in one
// transaction.
func (s *Store) AdoptChild(name string, p project.Project) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := affected(tx.Exec(`DELETE FROM children WHERE name = ?`, name)); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO project(id, body) VALUES(1, ?) ON CONFLICT(id) DO UPDATE SET body = excluded.body`, string(b)); err != nil {
		return err
	}
	return tx.Commit()
}

// LeadAction is one line of a lead's decision log.
type LeadAction struct {
	ID      int64     `json:"id"`
	Lead    string    `json:"lead"`
	RunID   string    `json:"runId"`
	Action  string    `json:"action"`  // create, change, stop, run, join
	Subject string    `json:"subject"` // the child, or the report joined
	Reason  string    `json:"reason"`
	Created time.Time `json:"created"`
}

// LogLead appends to a lead's decision log.
func (s *Store) LogLead(a LeadAction) error {
	_, err := s.db.Exec(`INSERT INTO lead_log(lead, run_id, action, subject, reason, created) VALUES(?, ?, ?, ?, ?, ?)`,
		a.Lead, a.RunID, a.Action, a.Subject, a.Reason, ts(a.Created))
	return err
}

// LeadLog returns a lead's newest decisions first.
func (s *Store) LeadLog(lead string, limit int) ([]LeadAction, error) {
	rows, err := s.db.Query(`SELECT id, lead, run_id, action, subject, reason, created FROM lead_log
		WHERE lead = ? ORDER BY created DESC, id DESC LIMIT ?`, lead, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LeadAction
	for rows.Next() {
		var a LeadAction
		var c int64
		if err := rows.Scan(&a.ID, &a.Lead, &a.RunID, &a.Action, &a.Subject, &a.Reason, &c); err != nil {
			return nil, err
		}
		a.Created = fromTS(c)
		out = append(out, a)
	}
	return out, rows.Err()
}

// JoinReport moves a report to another issue, as a lead tidying the bug
// list does. An issue left without reports is removed. No status changes.
func (s *Store) JoinReport(bugID, issueID string, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old string
	err = tx.QueryRow(`SELECT issue_id FROM bugs WHERE id = ?`, bugID).Scan(&old)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := affected(tx.Exec(`UPDATE issues SET updated = ? WHERE id = ?`, ts(now), issueID)); err != nil {
		return err
	}
	if old == issueID {
		return nil // nothing moves; the rollback keeps updated as it was
	}
	if _, err := tx.Exec(`UPDATE bugs SET issue_id = ? WHERE id = ?`, issueID, bugID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM issues WHERE id = ? AND NOT EXISTS (SELECT 1 FROM bugs WHERE issue_id = ?)`, old, old); err != nil {
		return err
	}
	return tx.Commit()
}
