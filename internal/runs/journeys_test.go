package runs

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/boarding-party/boardingparty/internal/store"
)

func startedRun(t *testing.T, svc *Service) store.Run {
	t.Helper()
	r, err := svc.Start(context.Background(), "ann", "checkout")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const journeyCode = "export default async function journey({ page, bp }) {}"

func TestSaveJourneyStoresProvenFullRevision(t *testing.T) {
	svc, _ := newSvc(t)
	r := startedRun(t, svc)
	rev, err := svc.SaveJourney(r.Token, SaveJourney{Code: journeyCode, Reason: "first recording", Kind: store.RevisionFull,
		SelfReplay: ReplayResult{Status: "passed", Seed: 7}})
	if err != nil {
		t.Fatal(err)
	}
	j, err := svc.Store.JourneyFor("ann", "checkout")
	if err != nil || j.LiveRev != rev.N || rev.N != 1 || rev.RunID != r.ID || rev.Kind != store.RevisionFull {
		t.Fatalf("journey %+v rev %+v %v", j, rev, err)
	}
	ds, _ := svc.Store.Decisions(j.ID)
	if len(ds) != 1 || ds[0].Kind != "recorded" || ds[0].Actor != store.ActorModel || ds[0].RunID != r.ID || ds[0].Reason != "first recording" {
		t.Fatalf("decisions %+v", ds)
	}
}

func TestSaveJourneyRefusesUnprovenRevision(t *testing.T) {
	svc, _ := newSvc(t)
	r := startedRun(t, svc)
	for name, in := range map[string]SaveJourney{
		"full that failed":     {Kind: store.RevisionFull, SelfReplay: ReplayResult{Status: "failed", Step: 2}},
		"bug that passed":      {Kind: store.RevisionBug, BugID: "b1", BugStep: 2, SelfReplay: ReplayResult{Status: "passed"}},
		"bug failed elsewhere": {Kind: store.RevisionBug, BugID: "b1", BugStep: 5, SelfReplay: ReplayResult{Status: "failed", Step: 3}},
		"no self replay":       {Kind: store.RevisionFull},
		"unknown kind":         {Kind: "other", SelfReplay: ReplayResult{Status: "passed"}},
		"bug without bug id":   {Kind: store.RevisionBug, BugStep: 3, SelfReplay: ReplayResult{Status: "failed", Step: 3}},
		"bug of another run":   {Kind: store.RevisionBug, BugID: "not-this-runs", BugStep: 3, SelfReplay: ReplayResult{Status: "failed", Step: 3}},
		"code too long":        {Kind: store.RevisionFull, Code: strings.Repeat("x", MaxJourneyCode+1), SelfReplay: ReplayResult{Status: "passed"}},
		"reason too long":      {Kind: store.RevisionFull, Reason: strings.Repeat("x", MaxJourneyReason+1), SelfReplay: ReplayResult{Status: "passed"}},
	} {
		if in.Code == "" {
			in.Code = journeyCode
		}
		if in.Reason == "" {
			in.Reason = "because"
		}
		if _, err := svc.SaveJourney(r.Token, in); err == nil {
			t.Errorf("%s: stored", name)
		}
	}
	if _, err := svc.SaveJourney(r.Token, SaveJourney{Kind: store.RevisionFull, Code: "  ", Reason: "x",
		SelfReplay: ReplayResult{Status: "passed"}}); err == nil {
		t.Error("empty code: stored")
	}
	if _, err := svc.SaveJourney(r.Token, SaveJourney{Kind: store.RevisionFull, Code: journeyCode, Reason: "x",
		SelfReplay: ReplayResult{Status: "failed", Step: 1}}); !errors.Is(err, ErrUnproven) {
		t.Fatalf("want ErrUnproven, got %v", err)
	}
	if _, err := svc.Store.JourneyFor("ann", "checkout"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a refused revision left a journey: %v", err)
	}
}

func TestSaveJourneyBugRevisionLinksTheBug(t *testing.T) {
	svc, _ := newSvc(t)
	r := startedRun(t, svc)
	if _, err := svc.SaveJourney(r.Token, SaveJourney{Kind: store.RevisionFull, Code: journeyCode, Reason: "full",
		SelfReplay: ReplayResult{Status: "passed"}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.AddBug(store.Bug{ID: "b1", RunID: r.ID, Summary: "total wrong", Severity: "high"}); err != nil {
		t.Fatal(err)
	}
	rev, err := svc.SaveJourney(r.Token, SaveJourney{Kind: store.RevisionBug, BugID: "b1", BugStep: 3, Code: journeyCode,
		Reason: "total is wrong at step 3", SelfReplay: ReplayResult{Status: "failed", Step: 3, StepName: "Check the total"}})
	if err != nil {
		t.Fatal(err)
	}
	j, _ := svc.Store.JourneyFor("ann", "checkout")
	if rev.BugID != "b1" || rev.BugStep != 3 || rev.N != 2 || j.LiveRev != 1 {
		t.Fatalf("rev %+v journey %+v", rev, j)
	}
	ds, _ := svc.Store.Decisions(j.ID)
	if ds[0].Kind != "bug revision recorded" {
		t.Fatalf("decision %+v", ds[0])
	}
}

func TestJourneyDecision(t *testing.T) {
	svc, _ := newSvc(t)
	r := startedRun(t, svc)
	if err := svc.CheckJourneyDecided(r); !errors.Is(err, ErrNoJourneyDecision) {
		t.Fatalf("undecided run: %v", err)
	}
	if err := svc.JourneyDecision(r.Token, "confirmed", "same as before"); err == nil {
		t.Fatal("confirmed with no live journey")
	}
	if err := svc.JourneyDecision(r.Token, "maybe", "x"); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if err := svc.JourneyDecision(r.Token, "not recorded", ""); err == nil {
		t.Fatal("a decision needs a reason")
	}
	if err := svc.JourneyDecision(r.Token, "not recorded", "the site logged me out mid-way"); err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckJourneyDecided(r); err != nil {
		t.Fatalf("decided run: %v", err)
	}
	j, err := svc.Store.JourneyFor("ann", "checkout")
	if err != nil || j.LiveRev != 0 {
		t.Fatalf("journey %+v %v", j, err)
	}
	ds, _ := svc.Store.Decisions(j.ID)
	if len(ds) != 1 || ds[0].Kind != "not recorded" || ds[0].RunID != r.ID || ds[0].Actor != store.ActorModel {
		t.Fatalf("decisions %+v", ds)
	}
}

func TestSavedRevisionDecidesTheRun(t *testing.T) {
	svc, _ := newSvc(t)
	r := startedRun(t, svc)
	if _, err := svc.SaveJourney(r.Token, SaveJourney{Kind: store.RevisionFull, Code: journeyCode, Reason: "x",
		SelfReplay: ReplayResult{Status: "passed"}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.CheckJourneyDecided(r); err != nil {
		t.Fatal(err)
	}
	if err := svc.JourneyDecision(r.Token, "confirmed", "same flow as revision 1"); err != nil {
		t.Fatalf("confirm with a live journey: %v", err)
	}
}

// A journey replays unattended with its run's credentials in reach, so code
// that carries a run token (every director URL does) is refused.
func TestSaveJourneyRefusesTokens(t *testing.T) {
	svc, _ := newSvc(t)
	r := startedRun(t, svc)
	for name, code := range map[string]string{
		"own token":   journeyCode + "\n// " + r.Token,
		"upload url":  "await fetch('http://director:8080/artifacts/" + r.Token + "/x')",
		"other token": "const t = '" + strings.Repeat("ab", 32) + "';",
	} {
		_, err := svc.SaveJourney(r.Token, SaveJourney{Kind: store.RevisionFull, Code: code, Reason: "x", SelfReplay: ReplayResult{Status: "passed"}})
		if err == nil || !strings.Contains(err.Error(), "token") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPauseResumeRows(t *testing.T) {
	svc, _ := newSvc(t)
	j := recorded(t, svc, 1)
	if err := svc.PauseJourney(j.ID, "kate@shop.test"); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Store.Journey(j.ID); got.State != store.JourneyPaused {
		t.Fatalf("state %s", got.State)
	}
	if err := svc.ResumeJourney(j.ID, "kate@shop.test"); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.Store.Journey(j.ID); got.State != store.JourneyLive {
		t.Fatalf("state %s", got.State)
	}
	ds, _ := svc.Store.Decisions(j.ID)
	if ds[0].Kind != "resumed" || ds[0].Reason != "Resumed by kate@shop.test" || ds[0].Actor != "kate@shop.test" ||
		ds[1].Kind != "paused" || ds[1].Reason != "Paused by kate@shop.test" {
		t.Fatalf("decisions %+v", ds)
	}
	if err := svc.PauseJourney("nope", "kate"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing journey: %v", err)
	}
}

func TestRollbackJourneyRow(t *testing.T) {
	svc, _ := newSvc(t)
	j := recorded(t, svc, 2)
	rev, err := svc.RollbackJourney(j.ID, 1, "kate")
	if err != nil || rev.N != 3 {
		t.Fatalf("%+v %v", rev, err)
	}
	ds, _ := svc.Store.Decisions(j.ID)
	if ds[0].Kind != "rolled back" || ds[0].Reason != "Rolled back to revision 1 by kate" || ds[0].Actor != "kate" {
		t.Fatalf("decision %+v", ds[0])
	}
	if _, err := svc.RollbackJourney(j.ID, 0, "kate"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("revision 0: %v", err)
	}
	if _, err := svc.RollbackJourney(j.ID, 9, "kate"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("revision 9: %v", err)
	}
}

func TestDeleteJourneyRow(t *testing.T) {
	svc, _ := newSvc(t)
	j := recorded(t, svc, 1)
	if err := svc.DeleteJourney(j.ID, "kate"); err != nil {
		t.Fatal(err)
	}
	ds, _ := svc.Store.Decisions(j.ID)
	if ds[0].Kind != "deleted" || ds[0].Reason != "Deleted by kate" || ds[0].Actor != "kate" {
		t.Fatalf("decision %+v", ds[0])
	}
}

// A journey that waits is proven up to its first wait: self mode stops there.
func TestSaveJourneyAcceptsWaitingSelfReplay(t *testing.T) {
	svc, _ := newSvc(t)
	r := startedRun(t, svc)
	rev, err := svc.SaveJourney(r.Token, SaveJourney{Code: journeyCode, Reason: "signs up and confirms by email", Kind: store.RevisionFull,
		SelfReplay: ReplayResult{Status: "waiting", Step: 2, Wait: &ReplayWait{N: 1, Until: []byte(`{"email":{"subject":"Confirm"}}`)}}})
	if err != nil || rev.N != 1 {
		t.Fatalf("%+v %v", rev, err)
	}
}
