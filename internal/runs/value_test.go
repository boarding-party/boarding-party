package runs

import (
	"math"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
)

// paid stores a finished run that spent $2 (1M sonnet input tokens).
func paid(t *testing.T, svc *Service, id, persona, story string, when time.Time) {
	t.Helper()
	if err := svc.Store.CreateRun(store.Run{ID: id, Token: "tok-" + id, Persona: persona, Role: "buyer", Story: story,
		State: store.RunRunning, Started: when, Chain: id}); err != nil {
		t.Fatal(err)
	}
	u := Usage{Counts: Counts{Input: 1_000_000}, ByModel: map[string]Counts{"claude-sonnet-5-5": {Input: 1_000_000}}}
	if err := svc.SaveUsage("tok-"+id, u, when); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store.FinishRun(id, store.RunDone, "ok", when.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestValueSplitsSpendAndFindsByPersonaAndStory(t *testing.T) {
	svc, _ := newSvc(t)
	since := at.Add(-time.Hour)
	paid(t, svc, "old", "ann", "checkout", since.Add(-time.Hour)) // before the window: not counted
	paid(t, svc, "r1", "ann", "checkout", at)
	paid(t, svc, "r2", "ann", "checkout", at.Add(time.Minute))
	paid(t, svc, "r3", "bob", "signup", at.Add(2*time.Minute))
	if err := svc.Store.AddBug(store.Bug{ID: "b1", RunID: "r1", Summary: "total wrong", Created: at.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	v, err := svc.Value(since)
	if err != nil {
		t.Fatal(err)
	}
	if v.Runs != 3 || math.Abs(v.Spent-6) > 1e-9 || v.Bugs != 1 || math.Abs(v.PerBug-6) > 1e-9 {
		t.Fatalf("value %+v, want 3 runs, $6, 1 bug, $6 a bug", v)
	}
	if len(v.Personas) != 2 || v.Personas[0].Name != "ann" || v.Personas[0].Runs != 2 || v.Personas[0].Bugs != 1 ||
		math.Abs(v.Personas[0].Spent-4) > 1e-9 || v.Personas[1].Name != "bob" || v.Personas[1].Bugs != 0 {
		t.Fatalf("personas %+v, want ann (2 runs, $4, 1 bug) before bob", v.Personas)
	}
	if len(v.Stories) != 2 || v.Stories[0].Name != "checkout" || v.Stories[1].Name != "signup" {
		t.Fatalf("stories %+v", v.Stories)
	}
}

func TestValueWithNoBugsHasNoPerBug(t *testing.T) {
	svc, _ := newSvc(t)
	paid(t, svc, "r1", "ann", "checkout", at)
	v, err := svc.Value(at.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if v.Bugs != 0 || v.PerBug != 0 || math.Abs(v.Spent-2) > 1e-9 {
		t.Fatalf("value %+v", v)
	}
}
