package runs

import (
	"context"
	"encoding/json"
	"errors"
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

// markFixed checks the fix: by replaying the journey step of the newest
// report linked to one that can still be replayed, or else with a run of
// the persona and story of the issue's latest report whose persona and story
// still exist, to look for the problem again.
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
	for _, rp := range reports { // newest first
		if rp.JourneyID == "" || rp.JourneyStep < 1 {
			continue
		}
		q, ok, err := s.replayCheck(is, rp, now)
		if err != nil {
			return err
		}
		if ok {
			_, err = s.Store.StartCheck(id, q, now)
			return err
		}
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
		q := store.Queued{ID: qid, Chain: qid, Persona: rp.Persona, Role: role, Story: story, Context: ctx,
			Reason: "Checks the fix: " + store.Clip(is.Summary, maxCheckReason), Created: now}
		_, err = s.Store.StartCheck(id, q, now)
		return err
	}
	return s.Store.NoCheck(id, store.NoCheckNobody, now)
}

// replayCheck is the queued replay that checks issue's fix at report rp's
// journey step, or false when that journey revision cannot be replayed.
func (s *Service) replayCheck(is store.Issue, rp store.Report, now time.Time) (store.Queued, bool, error) {
	j, role, rev, err := s.replayable(rp.JourneyID, rp.JourneyRev)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, ErrStaleJourney) {
		return store.Queued{}, false, nil
	}
	if err != nil {
		return store.Queued{}, false, err
	}
	ctx, err := json.Marshal(store.RunContext{Kind: "verify", Verify: &store.ContextVerify{
		IssueID: is.ID, Summary: store.Clip(rp.Summary, maxVerifyText), Step: store.Clip(rp.Step, maxVerifyText),
		Evidence: store.Clip(rp.Evidence, maxVerifyText), JourneyID: j.ID, Revision: rev.N, JourneyStep: rp.JourneyStep}})
	if err != nil {
		return store.Queued{}, false, err
	}
	seed, err := NewSeed()
	if err != nil {
		return store.Queued{}, false, err
	}
	qid, err := randHex(16)
	if err != nil {
		return store.Queued{}, false, err
	}
	return store.Queued{ID: qid, Chain: qid, Persona: j.Persona, Role: role, Story: j.Story, Context: ctx,
		Reason: "Checks the fix by replay: " + store.Clip(is.Summary, maxCheckReason), Created: now,
		Kind: store.RunReplay, JourneyID: j.ID, Revision: rev.N, Seed: seed}, true, nil
}

// replayVerdict is a fix-check replay's verdict on step v.JourneyStep, or
// handOver when the replay never showed whether the problem is there.
func replayVerdict(v store.ContextVerify, res ReplayResult) (verdict store.Verdict, handOver bool) {
	n := v.JourneyStep
	switch {
	case res.Status == "failed" && res.Step == n:
		return store.Verdict{Reached: true, StillThere: true, ByReplay: true,
			Note: fmt.Sprintf("The replay failed at step %d again: %s", n, store.Clip(res.StepName, 200))}, false
	case res.Status == "passed" || (res.Status == "failed" && res.Step > n):
		return store.Verdict{Reached: true, ByReplay: true, Note: fmt.Sprintf("The replay got past step %d.", n)}, false
	}
	return store.Verdict{ByReplay: true}, true
}

// replayNote says what a fix-check replay of revision rev saw before it
// handed the check over.
func replayNote(rev int, v store.ContextVerify, res ReplayResult) string {
	switch {
	case res.Status == "failed" && res.Step > 0:
		return fmt.Sprintf("A replay of revision %d stopped at step %d (%s) before step %d: %s",
			rev, res.Step, store.Clip(res.StepName, 200), v.JourneyStep, store.Clip(res.Error, 300))
	case res.Status == "failed":
		return fmt.Sprintf("A replay of revision %d failed before its first step: %s", rev, store.Clip(res.Error, 300))
	}
	return fmt.Sprintf("A replay of revision %d stopped without a result.", rev)
}

// fixCheckVerdict is the verdict of replay run r on the fix it checks, with
// the AI run that takes over when the replay cannot tell; nil when r checks
// no fix.
func (s *Service) fixCheckVerdict(r store.Run, res ReplayResult, now time.Time) (*store.Verdict, error) {
	var c store.RunContext
	if json.Unmarshal(r.Context, &c) != nil || c.Verify == nil || c.Verify.JourneyStep < 1 {
		return nil, nil
	}
	v, hand := replayVerdict(*c.Verify, res)
	if !hand {
		return &v, nil
	}
	cv := *c.Verify
	cv.JourneyID, cv.Revision, cv.JourneyStep = "", 0, 0
	cv.ReplayNote = replayNote(r.Revision, *c.Verify, res)
	ctx, err := json.Marshal(store.RunContext{Kind: "verify", Verify: &cv})
	if err != nil {
		return nil, err
	}
	qid, err := randHex(16)
	if err != nil {
		return nil, err
	}
	v.Next = &store.Queued{ID: qid, Chain: r.Chain, Follows: r.ID, Persona: r.Persona, Role: r.Role, Story: r.Story, Context: ctx,
		Reason: "Checks the fix after a replay: " + store.Clip(cv.Summary, maxCheckReason), Created: now}
	return &v, nil
}

// fixCheckDecided records on the journey what a fix-check replay decided.
func (s *Service) fixCheckDecided(r store.Run, v *store.Verdict, now time.Time) error {
	var c store.RunContext
	_ = json.Unmarshal(r.Context, &c)
	what := "Gone"
	switch {
	case v.Next != nil:
		what = "handed to an AI run"
	case v.StillThere:
		what = "Still there"
	}
	j, err := s.Store.Journey(r.JourneyID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.Store.AddDecision(store.Decision{JourneyID: j.ID, RunID: r.ID, Kind: "fix check", Actor: store.ActorDirector,
		Reason: "Issue " + c.Verify.IssueID + ": " + what, RevBefore: j.LiveRev, RevAfter: j.LiveRev, Created: now})
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
