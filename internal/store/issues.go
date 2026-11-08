package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"
)

// IssueStatus is where an issue stands in triage.
type IssueStatus string

const (
	IssueOpen      IssueStatus = "open"
	IssueConfirmed IssueStatus = "confirmed"
	IssueNotABug   IssueStatus = "not_a_bug"
	IssueFixed     IssueStatus = "fixed"
	IssueVerifying IssueStatus = "verifying"
	IssueClosed    IssueStatus = "closed"
)

// Issue is one problem on the site. Reports, LastSeen and Personas are worked
// out from its reports.
type Issue struct {
	ID           string      `json:"id"`
	Summary      string      `json:"summary"`
	Status       IssueStatus `json:"status"`
	Reason       string      `json:"reason,omitempty"`
	Created      time.Time   `json:"created"`
	Updated      time.Time   `json:"updated"`
	Reopened     time.Time   `json:"reopened,omitzero"`
	CheckJourney string      `json:"-"`
	CheckNote    string      `json:"checkNote,omitempty"`
	Reports      int         `json:"reports"`
	LastSeen     time.Time   `json:"lastSeen,omitzero"`
	Personas     []string    `json:"personas"`
}

// Report is a bug with the run that reported it.
type Report struct {
	Bug
	Persona string `json:"persona"`
	Story   string `json:"story"`
}

const bugCols = `id, run_id, summary, step, evidence, severity, note, created, issue_id`

func scanBug(sc interface{ Scan(...any) error }) (Bug, error) {
	var b Bug
	var c int64
	if err := sc.Scan(&b.ID, &b.RunID, &b.Summary, &b.Step, &b.Evidence, &b.Severity, &b.Note, &c, &b.IssueID); err != nil {
		return Bug{}, err
	}
	b.Created = fromTS(c)
	return b, nil
}

func newIssueID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// insertBug stores a report, attaching it to b.IssueID when that issue
// exists (a closed issue reopens) and opening a new issue otherwise.
func insertBug(tx *sql.Tx, b Bug) error {
	at := ts(b.Created)
	attached := false
	if b.IssueID != "" {
		res, err := tx.Exec(`UPDATE issues SET updated = MAX(updated, ?),
			reopened = CASE status WHEN 'closed' THEN ? ELSE reopened END,
			status = CASE status WHEN 'closed' THEN 'open' ELSE status END
			WHERE id = ?`, at, at, b.IssueID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		attached = n > 0
	}
	if !attached {
		id, err := newIssueID()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO issues(id, summary, status, created, updated) VALUES(?,?,?,?,?)`,
			id, b.Summary, string(IssueOpen), at, at); err != nil {
			return err
		}
		b.IssueID = id
	}
	_, err := tx.Exec(`INSERT INTO bugs(`+bugCols+`) VALUES(?,?,?,?,?,?,?,?,?)`,
		b.ID, b.RunID, b.Summary, b.Step, b.Evidence, b.Severity, b.Note, at, b.IssueID)
	return err
}

// issueSelect reads issues with their report aggregates; the caller adds
// WHERE, ORDER BY and LIMIT.
const issueSelect = `SELECT i.id, i.summary, i.status, i.reason, i.created, i.updated, i.reopened, i.check_journey, i.check_note,
	(SELECT COUNT(*) FROM bugs b WHERE b.issue_id = i.id),
	(SELECT COALESCE(MAX(b.created), 0) FROM bugs b WHERE b.issue_id = i.id),
	(SELECT COALESCE(GROUP_CONCAT(p, char(31)), '') FROM (SELECT DISTINCT r.persona AS p FROM bugs b JOIN runs r ON r.id = b.run_id
		WHERE b.issue_id = i.id AND r.persona != '' ORDER BY p))
	FROM issues i `

func (s *Store) queryIssues(tail string, args ...any) ([]Issue, error) {
	rows, err := s.db.Query(issueSelect+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Issue
	for rows.Next() {
		var i Issue
		var status, personas string
		var created, updated, reopened, last int64
		if err := rows.Scan(&i.ID, &i.Summary, &status, &i.Reason, &created, &updated, &reopened, &i.CheckJourney, &i.CheckNote,
			&i.Reports, &last, &personas); err != nil {
			return nil, err
		}
		i.Status = IssueStatus(status)
		i.Created, i.Updated, i.Reopened, i.LastSeen = fromTS(created), fromTS(updated), fromTS(reopened), fromTS(last)
		i.Personas = []string{}
		if personas != "" {
			i.Personas = strings.Split(personas, "\x1f")
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Issues returns every issue, latest activity first.
func (s *Store) Issues() ([]Issue, error) {
	return s.queryIssues(`ORDER BY i.updated DESC, i.rowid DESC`)
}

// Issue returns one issue, or ErrNotFound.
func (s *Store) Issue(id string) (Issue, error) {
	is, err := s.queryIssues(`WHERE i.id = ?`, id)
	if err != nil {
		return Issue{}, err
	}
	if len(is) == 0 {
		return Issue{}, ErrNotFound
	}
	return is[0], nil
}

// IssueReports returns an issue's reports with the run that made each, newest first.
func (s *Store) IssueReports(id string) ([]Report, error) {
	rows, err := s.db.Query(`SELECT b.id, b.run_id, b.summary, b.step, b.evidence, b.severity, b.note, b.created, b.issue_id,
		COALESCE(r.persona, ''), COALESCE(r.story, '') FROM bugs b LEFT JOIN runs r ON r.id = b.run_id
		WHERE b.issue_id = ? ORDER BY b.created DESC, b.rowid DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Report
	for rows.Next() {
		var rp Report
		var c int64
		if err := rows.Scan(&rp.ID, &rp.RunID, &rp.Summary, &rp.Step, &rp.Evidence, &rp.Severity, &rp.Note, &c, &rp.IssueID,
			&rp.Persona, &rp.Story); err != nil {
			return nil, err
		}
		rp.Created = fromTS(c)
		out = append(out, rp)
	}
	return out, rows.Err()
}

// KnownIssuesClosedFor is how long a closed issue stays among the known
// issues, so a returning problem can still be matched and reopened.
const KnownIssuesClosedFor = 30 * 24 * time.Hour

// KnownIssues returns what personas are told about: every not-a-bug issue
// first, then the others by latest activity, closed ones only while recent.
func (s *Store) KnownIssues(now time.Time, limit int) ([]Issue, error) {
	return s.queryIssues(`WHERE i.status != 'closed' OR i.updated >= ?
		ORDER BY (i.status = 'not_a_bug') DESC, i.updated DESC, i.rowid DESC LIMIT ?`, ts(now.Add(-KnownIssuesClosedFor)), limit)
}

// SetIssueStatus records the user's triage. The reason is kept only for not
// a bug. Any check under way no longer counts.
func (s *Store) SetIssueStatus(id string, st IssueStatus, reason string, now time.Time) error {
	if st != IssueNotABug {
		reason = ""
	}
	res, err := s.db.Exec(`UPDATE issues SET status=?, reason=?, updated=?, check_journey='' WHERE id=?`,
		string(st), reason, ts(now), id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}
