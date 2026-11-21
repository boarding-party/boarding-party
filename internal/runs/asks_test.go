package runs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
)

// askSvc has lead Megan with maxChildren 2, dailyRuns 4 and a $2 budget.
func askSvc(t *testing.T) *Service {
	t.Helper()
	svc := emptySvc(t)
	budget := 2.0
	p := project.Project{Name: "p", Sites: []string{"https://x.test"}, Roles: []project.Role{{Name: "Venue manager",
		Personas: []project.Persona{{Name: "Megan", Lead: &project.Lead{CreatePersonas: true, StartRuns: true,
			MaxChildren: 2, DailyRuns: 4, DailyBudget: &budget}}}}}}
	if err := svc.Store.PutProject(p); err != nil {
		t.Fatal(err)
	}
	return svc
}

func megan(t *testing.T, svc *Service) project.Lead {
	t.Helper()
	p, err := svc.Store.Project()
	if err != nil {
		t.Fatal(err)
	}
	pe, _, _ := p.Persona("Megan")
	return *pe.Lead
}

func TestCheckAsk(t *testing.T) {
	budget := 2.0
	l := project.Lead{MaxChildren: 2, DailyRuns: 4, DailyBudget: &budget}
	ok := []Ask{{Limit: AskChildren, To: 3}, {Limit: AskRuns, To: 100}, {Limit: AskBudget, To: 2.5}}
	for _, a := range ok {
		if err := CheckAsk(l, a); err != nil {
			t.Errorf("CheckAsk(%+v) = %v", a, err)
		}
	}
	bad := []Ask{
		{Limit: "wakeAt", To: 3},
		{Limit: AskChildren, To: 2},   // not more than now
		{Limit: AskChildren, To: 2.5}, // not whole
		{Limit: AskChildren, To: project.MaxLeadChildren + 1},
		{Limit: AskRuns, To: project.MaxLeadRuns + 1},
		{Limit: AskBudget, To: 1},
		{Limit: AskBudget, To: project.MaxDailyBudget + 1},
	}
	for _, a := range bad {
		if err := CheckAsk(l, a); !errors.Is(err, ErrInvalid) {
			t.Errorf("CheckAsk(%+v) = %v, want ErrInvalid", a, err)
		}
	}
}

func TestOpenAskAndRaise(t *testing.T) {
	svc := askSvc(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if a, err := svc.OpenAsk("Megan"); err != nil || a != nil {
		t.Fatalf("no ask yet: %+v, %v", a, err)
	}
	if err := svc.LogAsk("Megan", "run1", Ask{Limit: AskChildren, To: 3}, "a restaurant venue persona", now); err != nil {
		t.Fatal(err)
	}
	a, err := svc.OpenAsk("Megan")
	if err != nil || a == nil {
		t.Fatalf("OpenAsk = %+v, %v", a, err)
	}
	if a.Limit != AskChildren || a.To != 3 || a.Now != 2 || a.RunID != "run1" || a.Reason != "a restaurant venue persona" {
		t.Fatalf("ask %+v", a)
	}

	if err := svc.RaiseLeadLimit(ctx, "Megan", Ask{Limit: AskChildren, To: 3}); err != nil {
		t.Fatal(err)
	}
	if got := megan(t, svc).Children(); got != 3 {
		t.Fatalf("maxChildren = %d", got)
	}
	if a, err := svc.OpenAsk("Megan"); err != nil || a != nil {
		t.Fatalf("met ask still open: %+v, %v", a, err)
	}
	if err := svc.RaiseLeadLimit(ctx, "Megan", Ask{Limit: AskBudget, To: 3.5}); err != nil {
		t.Fatal(err)
	}
	if got := megan(t, svc).Budget(); got != 3.5 {
		t.Fatalf("budget = %v", got)
	}
}

func TestRaiseLeadLimitRefused(t *testing.T) {
	svc := askSvc(t)
	ctx := context.Background()
	if err := svc.RaiseLeadLimit(ctx, "Nobody", Ask{Limit: AskChildren, To: 3}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown lead: %v", err)
	}
	if err := svc.RaiseLeadLimit(ctx, "Megan", Ask{Limit: AskRuns, To: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("lower: %v", err)
	}
	if got := megan(t, svc).Runs(); got != 4 {
		t.Fatalf("dailyRuns changed to %d", got)
	}
}

func TestDigestAsks(t *testing.T) {
	svc := askSvc(t)
	since := time.Now().UTC().Add(-time.Hour)
	old := since.Add(-time.Hour)
	if err := svc.LogAsk("Megan", "r0", Ask{Limit: AskRuns, To: 6}, "older", old); err != nil {
		t.Fatal(err)
	}
	if err := svc.LogAsk("Megan", "r1", Ask{Limit: AskChildren, To: 3}, "venue", since.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	d, err := svc.Digest(since, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Asks.Items) != 1 {
		t.Fatalf("asks %+v", d.Asks)
	}
	if a := d.Asks.Items[0]; a.Lead != "Megan" || a.Limit != AskChildren || a.Now != 2 || a.To != 3 {
		t.Fatalf("ask %+v", a)
	}
	if !d.News() {
		t.Fatal("an ask is news")
	}
	if err := svc.RaiseLeadLimit(context.Background(), "Megan", Ask{Limit: AskChildren, To: 3}); err != nil {
		t.Fatal(err)
	}
	if d, _ := svc.Digest(since, time.Now().UTC()); len(d.Asks.Items) != 0 {
		t.Fatalf("met ask in digest: %+v", d.Asks)
	}
}
