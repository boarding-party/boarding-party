package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

// How a fix gets checked when the issue is marked fixed (CheckBy).
const (
	CheckByReplay = "replay" // a replay of the journey step that shows it
	CheckByRun    = "run"    // an AI run of the story that found it
	CheckByNobody = "none"   // nobody: the persona or story is gone
)

// markFixed checks the fix with the run checkFor picks, or marks the issue
// fixed unchecked when there is none.
func (s *Service) markFixed(id string, now time.Time) error {
	is, err := s.Store.Issue(id)
	if err != nil {
		return err
	}
	if is.Status == store.IssueVerifying {
		return nil
	}
	q, ok, err := s.checkFor(is, now)
	if err != nil {
		return err
	}
	if !ok {
		return s.Store.NoCheck(id, store.NoCheckNobody, now)
	}
	_, err = s.Store.StartCheck(id, q, now)
	return err
}

// CheckBy says how marking issue id fixed would check the fix: CheckByReplay,
// CheckByRun or CheckByNobody. It queues nothing.
func (s *Service) CheckBy(id string) (string, error) {
	is, err := s.Store.Issue(id)
	if err != nil {
		return "", err
	}
	q, ok, err := s.checkFor(is, time.Now().UTC())
	switch {
	case err != nil:
		return "", err
	case !ok:
		return CheckByNobody, nil
	case q.Kind == store.RunReplay:
		return CheckByReplay, nil
	}
	return CheckByRun, nil
}

// checkFor is the run that checks issue is's fix: a replay of the journey
// step of the newest report linked to one that can still be replayed, or
// else a run of the persona and story of the latest report whose persona and
// story still exist, to look for the problem again. False when there is none.
func (s *Service) checkFor(is store.Issue, now time.Time) (store.Queued, bool, error) {
	reports, err := s.Store.IssueReports(is.ID)
	if err != nil {
		return store.Queued{}, false, err
	}
	for _, rp := range reports { // newest first
		if rp.JourneyID == "" || rp.JourneyStep < 1 {
			continue
		}
		q, ok, err := s.replayCheck(is, rp, now)
		if err != nil || ok {
			return q, ok, err
		}
	}
	p, err := s.Store.Project()
	if errors.Is(err, store.ErrNotFound) {
		return store.Queued{}, false, nil // no project yet: no persona to check it
	}
	if err != nil {
		return store.Queued{}, false, fmt.Errorf("load project: %w", err)
	}
	for _, rp := range reports { // newest first
		role, story, err := find(p, rp.Persona, rp.Story)
		if err != nil {
			continue
		}
		ctx, err := json.Marshal(store.RunContext{Kind: "verify", Verify: &store.ContextVerify{
			IssueID: is.ID, Summary: store.Clip(rp.Summary, maxVerifyText), Step: store.Clip(rp.Step, maxVerifyText),
			Evidence: store.Clip(rp.Evidence, maxVerifyText)}})
		if err != nil {
			return store.Queued{}, false, err
		}
		qid, err := randHex(16)
		if err != nil {
			return store.Queued{}, false, err
		}
		return store.Queued{ID: qid, Chain: qid, Persona: rp.Persona, Role: role, Story: story, Context: ctx,
			Reason: "Checks the fix: " + store.Clip(is.Summary, maxCheckReason), Created: now}, true, nil
	}
	return store.Queued{}, false, nil
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

// After a fix-check replay: nothing more, the same replay again, or an AI run.
type checkNext int

const (
	nextNone checkNext = iota
	nextRepeat
	nextAI
)

// replayVerdict is a fix-check replay's verdict on step v.JourneyStep, and
// what runs next when the replay has not settled it. A first failure at the
// step is run again with the same inputs, as a journey's own failure is,
// unless the replay resumed from a wait: what arrived (such as a one-time
// link) may be used up.
func replayVerdict(v store.ContextVerify, res ReplayResult, resumed bool) (store.Verdict, checkNext) {
	n := v.JourneyStep
	switch {
	case res.Status == "failed" && res.Step == n && !v.Repeat && !resumed:
		return store.Verdict{ByReplay: true}, nextRepeat
	case res.Status == "failed" && res.Step == n:
		twice := ""
		if v.Repeat {
			twice = " twice with the same inputs"
		}
		return store.Verdict{Reached: true, StillThere: true, ByReplay: true,
			Note: fmt.Sprintf("The replay failed at step %d (%s)%s: %s", n, store.Clip(res.StepName, 200), twice, store.Clip(res.Error, 300))}, nextNone
	case res.Status == "passed" || ((res.Status == "failed" || res.Status == "waiting") && res.Step > n):
		note := fmt.Sprintf("The replay got past step %d.", n)
		if v.Repeat {
			note = fmt.Sprintf("The replay got past step %d when run again with the same inputs, after failing there once.", n)
		}
		return store.Verdict{Reached: true, ByReplay: true, Note: note}, nextNone
	}
	return store.Verdict{ByReplay: true}, nextAI
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
	case res.Status == "waiting":
		return fmt.Sprintf("A replay of revision %d stopped at a wait after step %d, before step %d, and the wait was never met.", rev, res.Step, v.JourneyStep)
	}
	return fmt.Sprintf("A replay of revision %d stopped without a result.", rev)
}

// FixCheckVerdict is the verdict of replay run r on the fix it checks, with
// the run that takes over when the replay has not settled it: the same
// replay again, or an AI run when the replay cannot tell. Nil when r checks
// no fix.
func FixCheckVerdict(r store.Run, res ReplayResult, now time.Time) (*store.Verdict, error) {
	var c store.RunContext
	if json.Unmarshal(r.Context, &c) != nil || c.Verify == nil || c.Verify.JourneyStep < 1 {
		return nil, nil
	}
	v, next := replayVerdict(*c.Verify, res, c.Resume != nil)
	if next == nextNone {
		return &v, nil
	}
	qid, err := randHex(16)
	if err != nil {
		return nil, err
	}
	cv := *c.Verify
	q := store.Queued{ID: qid, Chain: r.Chain, Follows: r.ID, Persona: r.Persona, Role: r.Role, Story: r.Story, Created: now}
	if next == nextRepeat {
		cv.Repeat = true
		q.Reason = "Checks the fix again with the same inputs: " + store.Clip(cv.Summary, maxCheckReason)
		q.Kind, q.JourneyID, q.Revision, q.Seed = store.RunReplay, r.JourneyID, r.Revision, r.Seed
	} else {
		cv.JourneyID, cv.Revision, cv.JourneyStep, cv.Repeat = "", 0, 0, false
		cv.ReplayNote = replayNote(r.Revision, *c.Verify, res)
		q.Reason = "Checks the fix after a replay: " + store.Clip(cv.Summary, maxCheckReason)
	}
	if q.Context, err = json.Marshal(store.RunContext{Kind: "verify", Verify: &cv}); err != nil {
		return nil, err
	}
	v.Next = &q
	return &v, nil
}

// FixCheckDecided records on the journey what a fix-check replay decided,
// if its verdict settled anything.
func FixCheckDecided(st *store.Store, r store.Run, v *store.Verdict, now time.Time) error {
	if !v.Settled {
		return nil
	}
	var c store.RunContext
	_ = json.Unmarshal(r.Context, &c)
	what := "Gone"
	switch {
	case v.Next != nil && v.Next.Kind == store.RunReplay:
		what = fmt.Sprintf("failed at step %d; checking again with the same inputs", c.Verify.JourneyStep)
	case v.Next != nil:
		what = "handed to an AI run"
	case v.StillThere:
		what = "Still there"
	}
	j, err := st.Journey(r.JourneyID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return st.AddDecision(store.Decision{JourneyID: j.ID, RunID: r.ID, Kind: "fix check", Actor: store.ActorDirector,
		Reason: "Issue " + c.Verify.IssueID + ": " + what, RevBefore: j.LiveRev, RevAfter: j.LiveRev, Created: now})
}

// FixCheckFallback hands queued item q, a fix-check replay that can no
// longer be replayed (its journey paused or deleted, its revision or story
// gone), to an AI run of the same story in its place. False, changing
// nothing, when q is no such replay.
func (s *Service) FixCheckFallback(q store.Queued, now time.Time) (bool, error) {
	var c store.RunContext
	if q.Kind != store.RunReplay || json.Unmarshal(q.Context, &c) != nil || c.Verify == nil || c.Verify.JourneyStep < 1 {
		return false, nil
	}
	var why string
	switch _, _, _, err := s.replayable(q.JourneyID, q.Revision); {
	case errors.Is(err, store.ErrNotFound):
		why = "the journey was deleted"
	case errors.Is(err, ErrStaleJourney):
		why = strings.TrimPrefix(err.Error(), ErrStaleJourney.Error()+": ")
	case err != nil:
		return false, err
	default:
		return false, nil // it can be replayed: whatever stopped it, it is not the journey
	}
	cv := *c.Verify
	cv.JourneyID, cv.Revision, cv.JourneyStep, cv.Repeat = "", 0, 0, false
	cv.ReplayNote = fmt.Sprintf("Revision %d of the journey could not be replayed: %s.", q.Revision, why)
	ctx, err := json.Marshal(store.RunContext{Kind: "verify", Verify: &cv})
	if err != nil {
		return false, err
	}
	qid, err := randHex(16)
	if err != nil {
		return false, err
	}
	next := store.Queued{ID: qid, Chain: q.Chain, Persona: q.Persona, Role: q.Role, Story: q.Story, Context: ctx,
		Reason: "Checks the fix: " + store.Clip(cv.Summary, maxCheckReason), Created: now}
	if _, err := s.Store.ReplaceQueued(q.ID, next); err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	return true, nil
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
