package sched

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/runs"
	"github.com/boarding-party/boardingparty/internal/store"
)

const exploreYAML = `name: p
sites: ["https://x.test"]
roles:
  - name: patient
    activity: every 1 h
    explore: daily
    personas: [{name: Ada}]
    stories:
      - {name: book, goal: book a slot}
      - {name: lone, goal: g}
      - {name: own, goal: g, replay: false}
`

func scheduleFire(stories ...string) Fire {
	return Fire{Source: "schedule", Persona: "Ada", Role: "patient", Stories: stories, Reason: "Scheduled: every 1 h"}
}

// journey gives Ada's story a live journey last explored at explored.
func (f *fix) journey(story string, explored time.Time) store.Journey {
	f.t.Helper()
	j, _, err := f.st.AddRevision("Ada", story, store.Revision{Kind: store.RevisionFull, Code: "c", Reason: "r", Created: explored},
		store.Decision{Kind: "recorded", Reason: "r", Actor: store.ActorModel, Created: explored})
	if err != nil {
		f.t.Fatal(err)
	}
	return j
}

func (f *fix) fireOnce(fires ...Fire) *fakeSource {
	f.t.Helper()
	src := &fakeSource{name: "schedule", fires: fires}
	f.s.Sources = []Source{src}
	if err := f.s.fire(context.Background(), f.clk.Now()); err != nil {
		f.t.Fatal(err)
	}
	return src
}

func TestFireQueuesReplayWithinExploreInterval(t *testing.T) {
	f := newFix(t, exploreYAML)
	j := f.journey("book", now.Add(-23*time.Hour))
	src := f.fireOnce(scheduleFire("book"))
	q := f.queue()
	if len(q) != 1 || q[0].Kind != store.RunReplay || q[0].JourneyID != j.ID || q[0].Revision != 0 || q[0].Seed < 1 || q[0].Seed > runs.MaxSeed ||
		q[0].Story != "book" || q[0].Reason != "Scheduled replay: every 1 h" {
		t.Fatalf("queue = %+v", q)
	}
	if fmt.Sprint(src.settled) != "[book]" {
		t.Fatalf("settled %q", src.settled)
	}
}

func TestFireQueuesAIRunWhenExploreIntervalPassed(t *testing.T) {
	f := newFix(t, exploreYAML)
	f.journey("book", now.Add(-25*time.Hour))
	f.fireOnce(scheduleFire("book"))
	if q := f.queue(); len(q) != 1 || q[0].Kind != "" || q[0].Reason != "Scheduled: every 1 h" {
		t.Fatalf("queue = %+v", q)
	}
}

func TestFireAIRunWithoutJourney(t *testing.T) {
	f := newFix(t, exploreYAML)
	if _, err := f.st.EnsureJourney("Ada", "book", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	f.fireOnce(scheduleFire("book"))
	if q := f.queue(); len(q) != 1 || q[0].Kind != "" {
		t.Fatalf("a journey with no live revision replays: %+v", q)
	}
}

func TestFireSkipsPausedAndRepairingJourney(t *testing.T) {
	for _, state := range []store.JourneyState{store.JourneyPaused, store.JourneyRepairing} {
		f := newFix(t, exploreYAML)
		j := f.journey("book", now.Add(-48*time.Hour))
		if err := f.st.SetJourneyState(j.ID, state, store.Decision{Kind: string(state), Actor: "kate", Created: now}); err != nil {
			t.Fatal(err)
		}
		src := f.fireOnce(scheduleFire("book", "lone"))
		if q := f.queue(); len(q) != 1 || q[0].Story != "lone" || q[0].Kind != "" {
			t.Fatalf("%s: queue = %+v", state, q)
		}
		f.st.DeleteQueued(f.queue()[0].ID)
		src = f.fireOnce(scheduleFire("book"))
		if q := f.queue(); len(q) != 0 || fmt.Sprintf("%q", src.settled) != `[""]` {
			t.Fatalf("%s alone: queue = %+v settled %q", state, q, src.settled)
		}
	}
}

func TestFireReplaysWhenOverBudget(t *testing.T) {
	f := newFix(t, exploreYAML)
	f.starter.SetGate(runs.ErrOverBudget)
	f.journey("book", now.Add(-time.Hour))
	f.fireOnce(scheduleFire("lone", "book"))
	if q := f.queue(); len(q) != 1 || q[0].Story != "book" || q[0].Kind != store.RunReplay {
		t.Fatalf("queue = %+v", q)
	}
	g := newFix(t, exploreYAML)
	g.starter.SetGate(runs.ErrOverBudget)
	src := g.fireOnce(scheduleFire("lone"))
	if q := g.queue(); len(q) != 0 || fmt.Sprintf("%q", src.settled) != `[""]` {
		t.Fatalf("over budget with nothing to replay: queue %+v settled %q", q, src.settled)
	}
	h := newFix(t, exploreYAML)
	h.starter.SetGate(runs.ErrPaused)
	h.journey("book", now.Add(-time.Hour))
	h.fireOnce(scheduleFire("book"))
	if q := h.queue(); len(q) != 0 {
		t.Fatalf("paused board queued %+v", q)
	}
}

func TestFireAIRunWhenStoryOptsOut(t *testing.T) {
	f := newFix(t, exploreYAML)
	f.journey("own", now.Add(-time.Hour))
	f.fireOnce(scheduleFire("own"))
	if q := f.queue(); len(q) != 1 || q[0].Kind != "" {
		t.Fatalf("queue = %+v", q)
	}
}

func TestMessageFireIsAIRun(t *testing.T) {
	f := newFix(t, exploreYAML)
	f.journey("book", now.Add(-time.Hour))
	fire := scheduleFire("book")
	fire.Context = []byte(`{"kind":"message"}`)
	f.fireOnce(fire)
	if q := f.queue(); len(q) != 1 || q[0].Kind != "" || string(q[0].Context) != `{"kind":"message"}` {
		t.Fatalf("queue = %+v", q)
	}
}
