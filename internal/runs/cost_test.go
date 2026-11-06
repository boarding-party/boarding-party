package runs

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

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
