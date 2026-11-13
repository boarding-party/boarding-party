package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	ID         string      `json:"id"`
	Summary    string      `json:"summary"`
	Status     IssueStatus `json:"status"`
	Reason     string      `json:"reason,omitempty"`
	Created    time.Time   `json:"created"`
	Updated    time.Time   `json:"updated"`
	Reopened   time.Time   `json:"reopened,omitzero"`
	CheckChain string      `json:"-"`
	CheckNote  string      `json:"checkNote,omitempty"`
	Reports    int         `json:"reports"`
	LastSeen   time.Time   `json:"lastSeen,omitzero"`
	Personas   []string    `json:"personas"`
}

// Report is a bug with the run that reported it.
type Report struct {
	Bug
	Persona string `json:"persona"`
	Story   string `json:"story"`
}

const bugCols = `id, run_id, summary, step, evidence, severity, note, created, issue_id, journey_id, journey_rev, journey_step`

func scanBug(sc interface{ Scan(...any) error }) (Bug, error) {
	var b Bug
	var c int64
	if err := sc.Scan(&b.ID, &b.RunID, &b.Summary, &b.Step, &b.Evidence, &b.Severity, &b.Note, &c, &b.IssueID, &b.JourneyID, &b.JourneyRev, &b.JourneyStep); err != nil {
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
	_, err := tx.Exec(`INSERT INTO bugs(`+bugCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.ID, b.RunID, b.Summary, b.Step, b.Evidence, b.Severity, b.Note, at, b.IssueID, b.JourneyID, b.JourneyRev, b.JourneyStep)
	return err
}

// issueSelect reads issues with their report aggregates; the caller adds
// WHERE, ORDER BY and LIMIT.
const issueSelect = `SELECT i.id, i.summary, i.status, i.reason, i.created, i.updated, i.reopened, i.check_chain, i.check_note,
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
		if err := rows.Scan(&i.ID, &i.Summary, &status, &i.Reason, &created, &updated, &reopened, &i.CheckChain, &i.CheckNote,
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
		b.journey_id, b.journey_rev, b.journey_step, COALESCE(r.persona, ''), COALESCE(r.story, '') FROM bugs b LEFT JOIN runs r ON r.id = b.run_id
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
			&rp.JourneyID, &rp.JourneyRev, &rp.JourneyStep, &rp.Persona, &rp.Story); err != nil {
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

// Find is an issue and the run whose report first found it.
type Find struct {
	IssueID string
	RunID   string
}

// FirstFinds lists the issues first reported at or after since, each with
// the run of its earliest report. Issues marked not a bug are left out:
// they found nothing.
func (s *Store) FirstFinds(since time.Time) ([]Find, error) {
	rows, err := s.db.Query(`SELECT i.id, (SELECT b.run_id FROM bugs b WHERE b.issue_id = i.id ORDER BY b.created, b.rowid LIMIT 1)
		FROM issues i
		WHERE i.status != 'not_a_bug' AND (SELECT MIN(b.created) FROM bugs b WHERE b.issue_id = i.id) >= ?
		ORDER BY i.rowid`, ts(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Find
	for rows.Next() {
		var f Find
		if err := rows.Scan(&f.IssueID, &f.RunID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SetIssueStatus records the user's triage. The reason is kept only for not
// a bug. Any check under way no longer counts, and one still queued is
// dropped so it does not spend a run.
func (s *Store) SetIssueStatus(id string, st IssueStatus, reason string, now time.Time) error {
	if st != IssueNotABug {
		reason = ""
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var chain string
	err = tx.QueryRow(`SELECT check_chain FROM issues WHERE id=?`, id).Scan(&chain)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if chain != "" {
		if _, err := tx.Exec(`DELETE FROM queue WHERE chain=?`, chain); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE issues SET status=?, reason=?, updated=?, check_chain='' WHERE id=?`,
		string(st), reason, ts(now), id); err != nil {
		return err
	}
	return tx.Commit()
}

// Verdict is what a fix check run found.
type Verdict struct {
	Reached    bool   // got to the step where the problem was
	StillThere bool   // the problem is still there (only when Reached)
	Note       string // what the persona saw
	// ByReplay marks a fix-check replay's verdict, which holds whatever
	// state the run ended in (a replay that shows the bug ends failed).
	ByReplay bool
	// Next hands the check over: it is queued with the check's chain and
	// the issue stays verifying.
	Next *Queued
	// Settled is set by the store when the verdict moved the issue or
	// handed its check over; false when the issue was no longer waiting
	// for this check.
	Settled bool
}

// MaxCheckNote caps the persona's note on a fix check.
const MaxCheckNote = 500

// NoCheckNobody is the note for an issue marked fixed that nobody can check.
const NoCheckNobody = "Could not check: the story or persona is gone."

const checkOffQueue = "Could not check: the check was taken off the queue."

// StartCheck queues q, the run that checks issue's fix, and marks the issue
// verifying. It returns false, doing nothing, if a check is already under way.
func (s *Store) StartCheck(issueID string, q Queued, now time.Time) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE issues SET status='verifying', check_chain=?, check_note='', reason='', updated=?
		WHERE id=? AND status != 'verifying'`, q.Chain, ts(now), issueID)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return false, err
	}
	if err := insertQueued(tx, q); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// NoCheck marks an issue fixed without a check, saying why in note.
func (s *Store) NoCheck(issueID, note string, now time.Time) error {
	res, err := s.db.Exec(`UPDATE issues SET status='fixed', check_note=?, check_chain='', reason='', updated=? WHERE id=?`,
		note, ts(now), issueID)
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

// settleCheck ends the fix check of the run that just ended in state, if it
// is one and its issue is still waiting for this check. A waiting run settles
// nothing: its continuation carries the check.
func settleCheck(tx *sql.Tx, runID string, state RunState, v *Verdict, now time.Time) error {
	var ctx, chain string
	if err := tx.QueryRow(`SELECT context, chain FROM runs WHERE id = ?`, runID).Scan(&ctx, &chain); err != nil {
		return err
	}
	st, note, reopen := IssueFixed, fmt.Sprintf("Could not check: the run ended %s.", state), false
	switch {
	case state == RunWaiting:
		return nil
	case v != nil && v.Next != nil:
		ok, err := handOver(tx, ctx, chain, *v.Next)
		v.Settled = ok
		return err
	case v == nil || (state != RunDone && !v.ByReplay):
	case !v.Reached:
		note = "Could not check: " + Clip(v.Note, MaxCheckNote)
	case v.StillThere:
		st, note, reopen = IssueOpen, "Still there: "+Clip(v.Note, MaxCheckNote), true
	default:
		st, note = IssueClosed, "Gone: "+Clip(v.Note, MaxCheckNote)
	}
	ok, err := settle(tx, ctx, chain, st, note, reopen, now)
	if v != nil {
		v.Settled = ok
	}
	return err
}

// handOver queues next, the run that carries on the fix check of a verify
// context, if its issue is still verifying under chain; true when it did.
func handOver(tx *sql.Tx, ctxJSON, chain string, next Queued) (bool, error) {
	var c RunContext
	if json.Unmarshal([]byte(ctxJSON), &c) != nil || c.Verify == nil || chain == "" {
		return false, nil
	}
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM issues WHERE id = ? AND status = 'verifying' AND check_chain = ?`,
		c.Verify.IssueID, chain).Scan(&n); err != nil || n == 0 {
		return false, err
	}
	next.Chain = chain
	return true, insertQueued(tx, next)
}

// settle moves the issue named by a verify context to st, if that issue is
// still verifying under chain; true when it did.
func settle(tx *sql.Tx, ctxJSON, chain string, st IssueStatus, note string, reopen bool, now time.Time) (bool, error) {
	if ctxJSON == "" {
		return false, nil
	}
	var c RunContext
	if json.Unmarshal([]byte(ctxJSON), &c) != nil || c.Verify == nil || c.Verify.IssueID == "" || chain == "" {
		return false, nil
	}
	res, err := tx.Exec(`UPDATE issues SET status=?, check_note=?, check_chain='', updated=?,
		reopened = CASE WHEN ? THEN ? ELSE reopened END
		WHERE id=? AND status='verifying' AND check_chain=?`,
		string(st), note, ts(now), reopen, ts(now), c.Verify.IssueID, chain)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
