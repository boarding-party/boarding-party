package runs

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

func TestCostHaiku(t *testing.T) {
	u := Usage{ByModel: map[string]Counts{"claude-haiku-5-5": {Input: 42, Output: 7691, CacheRead: 1067295, CacheWrite: 55534}}}
	if got := Cost(u); math.Abs(got-0.02146) > 0.0001 {
		t.Fatal(got)
	}
}

func TestCostUnknownModelIsDearest(t *testing.T) {
	c := Counts{Output: 1_000_000}
	if got := Cost(Usage{ByModel: map[string]Counts{"claude-next-9": c}}); got != 20 {
		t.Fatal(got)
	}
	if got := Cost(Usage{Counts: c}); got != 20 {
		t.Fatal(got)
	}
	if got := Cost(Usage{ByModel: map[string]Counts{"claude-haiku-5-5-20261101": c}}); got != 0.5 {
		t.Fatal(got)
	}
}

func TestDayStartLondon(t *testing.T) { // 00:30 BST on 1 Jul is 23:30 UTC on 30 Jun
	loc, _ := time.LoadLocation("Europe/London")
	now := time.Date(2026, 6, 30, 23, 30, 0, 0, time.UTC)
	if got := DayStart(now, loc); !got.Equal(time.Date(2026, 6, 30, 23, 0, 0, 0, time.UTC)) {
		t.Fatal(got)
	}
}

// spendRunning is like spend but via a running run so SaveUsage accepts it.
func spendRunning(t *testing.T, svc *Service, id string, at time.Time) {
	t.Helper()
	r := store.Run{ID: id, Token: id, Persona: "ann", Role: "buyer", Story: "checkout", State: store.RunRunning, Started: at}
	if err := svc.Store.CreateRun(r); err != nil {
		t.Fatal(err)
	}
	u := Usage{ByModel: map[string]Counts{"claude-opus-5-5": {Output: 300_000}}}
	if err := svc.SaveUsage(id, u, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store.FinishRun(id, store.RunDone, "ok", at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestGate(t *testing.T) {
	svc, _ := newSvc(t)
	if err := svc.Gate(time.Now()); err != nil {
		t.Fatal(err)
	}
	spendRunning(t, svc, "r1", time.Now().Add(-time.Second))
	err := svc.Gate(time.Now())
	if !errors.Is(err, ErrOverBudget) || !strings.Contains(err.Error(), "$6.00 of $5.00") {
		t.Fatal(err)
	}
	if _, err := svc.Start(context.Background(), "ann", "checkout"); !errors.Is(err, ErrOverBudget) {
		t.Fatal(err)
	}
	svc.Store.SetPaused(true)
	if err := svc.Gate(time.Now()); !errors.Is(err, ErrPaused) {
		t.Fatal(err)
	}
}

func TestGateIgnoresYesterday(t *testing.T) {
	svc, _ := newSvc(t)
	spendRunning(t, svc, "r1", time.Now().Add(-25*time.Hour))
	if err := svc.Gate(time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestCostPricesUnsplitExcess(t *testing.T) {
	u := Usage{Counts: Counts{Output: 1_000_000},
		ByModel: map[string]Counts{"claude-haiku-5-5": {Output: 400_000}}}
	// 0.4M haiku output = 0.2, plus 0.6M unsplit at opus = 12.
	if got := Cost(u); math.Abs(got-12.2) > 1e-9 {
		t.Fatal(got)
	}
}

func TestStatusIncludesCalendarDue(t *testing.T) {
	svc, _ := newSvc(t)
	p := project.Project{Name: "p", Sites: []string{"https://x.test"}, Roles: []project.Role{{
		Name: "buyer", Activity: "every 1 h",
		Personas: []project.Persona{{Name: "ann"}, {Name: "bo"}},
		Stories: []project.Story{{Name: "checkout", Goal: "buy a thing"},
			{Name: "join", Goal: "join the call", Trigger: &project.Trigger{Calendar: &project.CalendarRule{Title: "appointment", Lead: "10m"}}},
			{Name: "early", Goal: "prepare", Trigger: &project.Trigger{Calendar: &project.CalendarRule{Title: "video", Lead: "30m"}}}},
	}}}
	if err := svc.Store.PutProject(p); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := svc.Store.SetDue(store.Due{Persona: "ann", Activity: "every 1 h", At: now.Add(20 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []store.Event{
		{Persona: "bo", UID: "e1", Title: "Video appointment", Start: now.Add(time.Hour)},  // due at start-30m
		{Persona: "bo", UID: "lunch", Title: "Team lunch", Start: now.Add(time.Hour)},      // no story matches
		{Persona: "bo", UID: "tmrw", Title: "Appointment", Start: now.Add(30 * time.Hour)}, // beyond 24 h
		{Persona: "gone", UID: "x", Title: "Appointment", Start: now.Add(time.Hour)},       // not in the project
		{Persona: "bo", UID: "fired", Title: "Appointment", Start: now.Add(2 * time.Hour)}, // already fired
	} {
		if err := svc.Store.UpsertEvent(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Store.MarkEventFired("bo", "fired", now.Add(2*time.Hour), "join"); err != nil {
		t.Fatal(err)
	}
	s, err := svc.Status(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Due) != 2 {
		t.Fatalf("due = %+v", s.Due)
	}
	if d := s.Due[0]; d.Persona != "ann" || !d.At.Equal(now.Add(20*time.Minute)) || d.Reason != "" {
		t.Fatalf("due[0] = %+v", d)
	}
	if d := s.Due[1]; d.Persona != "bo" || d.Role != "buyer" || !d.At.Equal(now.Add(30*time.Minute)) || d.Reason != "Calendar: Video appointment" {
		t.Fatalf("due[1] = %+v", d)
	}
}
