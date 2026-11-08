package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
)

// maxCheckReason caps the issue summary in a fix check's queue reason.
const maxCheckReason = 80

// maxVerifyText caps each report field copied into a fix check's context.
const maxVerifyText = 500

// MaxIssueReason caps the user's "not a bug" reason.
const MaxIssueReason = 500

// SetIssueStatus records the user's triage of an issue. Marking it fixed
// queues a run that checks the fix (a no-op while one is under way).
func (s *Service) SetIssueStatus(id string, st store.IssueStatus, reason string, now time.Time) error {
	switch st {
	case store.IssueOpen, store.IssueConfirmed, store.IssueNotABug:
		return s.Store.SetIssueStatus(id, st, store.Clip(reason, MaxIssueReason), now)
	case store.IssueFixed:
		return s.markFixed(id, now)
	}
	return fmt.Errorf("%w: status must be open, confirmed, not_a_bug or fixed, got %q", ErrInvalid, st)
}

// markFixed queues the persona and story of the issue's latest report whose
// persona and story still exist, to look for the problem again.
func (s *Service) markFixed(id string, now time.Time) error {
	is, err := s.Store.Issue(id)
	if err != nil {
		return err
	}
	if is.Status == store.IssueVerifying {
		return nil
	}
	reports, err := s.Store.IssueReports(id)
	if err != nil {
		return err
	}
	p, err := s.Store.Project()
	if err != nil {
		return fmt.Errorf("load project: %w", err)
	}
	for _, rp := range reports { // newest first
		role, story, err := find(p, rp.Persona, rp.Story)
		if err != nil {
			continue
		}
		ctx, err := json.Marshal(store.RunContext{Kind: "verify", Verify: &store.ContextVerify{
			IssueID: id, Summary: store.Clip(rp.Summary, maxVerifyText), Step: store.Clip(rp.Step, maxVerifyText),
			Evidence: store.Clip(rp.Evidence, maxVerifyText)}})
		if err != nil {
			return err
		}
		qid, err := randHex(16)
		if err != nil {
			return err
		}
		q := store.Queued{ID: qid, Journey: qid, Persona: rp.Persona, Role: role, Story: story, Context: ctx,
			Reason: "Checks the fix: " + store.Clip(is.Summary, maxCheckReason), Created: now}
		_, err = s.Store.StartCheck(id, q, now)
		return err
	}
	return s.Store.NoCheck(id, store.NoCheckNobody, now)
}

// FinishChecked is Finish for a run that checks a fix: v is its verdict.
func (s *Service) FinishChecked(ctx context.Context, token string, state store.RunState, outcome string, v *store.Verdict) error {
	switch state {
	case store.RunDone, store.RunStuck, store.RunFailed:
	default:
		return fmt.Errorf("%w: got %q", ErrBadState, state)
	}
	r, err := s.Active(token)
	if err != nil {
		return err
	}
	return s.finish(ctx, r, state, outcome, v)
}
