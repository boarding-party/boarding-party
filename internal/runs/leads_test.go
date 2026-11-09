package runs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

// leadSvc has lead Kate (runs cap runs) and hand-made ann in role buyer, a
// $100 project budget, and Kate's child Ana.
func leadSvc(t *testing.T, runs int) *Service {
	t.Helper()
	svc, _ := newSvc(t)
	big := 100.0
	p := project.Project{Name: "p", Sites: []string{"https://x.test"}, DailyBudget: &big, Roles: []project.Role{{
		Name:     "buyer",
		Personas: []project.Persona{{Name: "ann"}, {Name: "Kate", Lead: &project.Lead{CreatePersonas: true, StartRuns: true, DailyRuns: runs}}},
		Stories:  []project.Story{{Name: "checkout", Goal: "buy a thing"}},
	}}}
	if err := svc.Store.PutProject(p); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.AddChild(store.Child{Name: "Ana", Lead: "Kate", Persona: project.Persona{Name: "Ana"},
		Stories: []project.Story{{Name: "pay", Goal: "pay an invoice"}}, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return svc
}

// pastRun records a finished run of persona today, with usage costing $6.
func pastRun(t *testing.T, svc *Service, id, persona, role, story string) {
	t.Helper()
	at := time.Now().Add(-time.Minute)
	if err := svc.Store.CreateRun(store.Run{ID: id, Token: id, Persona: persona, Role: role, Story: story,
		State: store.RunRunning, Started: at, Journey: id}); err != nil {
		t.Fatal(err)
	}
	u := Usage{ByModel: map[string]Counts{"claude-opus-5-5": {Output: 300_000}}}
	if err := svc.SaveUsage(id, u, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store.FinishRun(id, store.RunDone, "ok", at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestLeadRunsOnlyLeadStory(t *testing.T) {
	svc := leadSvc(t, 0)
	ctx := context.Background()
	if _, err := svc.Start(ctx, "Kate", "checkout"); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "is a lead") {
		t.Fatalf("lead browser story: %v", err)
	}
	if _, err := svc.Start(ctx, "ann", project.LeadStory); !errors.Is(err, ErrInvalid) {
		t.Fatalf("lead story for a hand-made persona: %v", err)
	}
	r, err := svc.Start(ctx, "Kate", project.LeadStory)
	if err != nil || r.Role != "buyer" || r.Story != project.LeadStory {
		t.Fatalf("lead run %+v %v", r, err)
	}
}

func TestLeadBudgetCountsChildren(t *testing.T) {
	svc := leadSvc(t, 2)
	pastRun(t, svc, "r1", "Kate", "buyer", project.LeadStory)
	pastRun(t, svc, "r2", "Ana", "Ana", "pay")
	p, _ := svc.Store.Project()
	u, err := svc.LeadUsage(p, "Kate", time.Now())
	if err != nil || u.Runs != 2 || u.Spent < 11.9 {
		t.Fatalf("usage %+v %v", u, err)
	}
	if _, err := svc.Start(context.Background(), "Ana", "pay"); !errors.Is(err, ErrLeadOverBudget) {
		t.Fatalf("child over runs cap: %v", err)
	}
	if _, err := svc.Start(context.Background(), "ann", "checkout"); err != nil {
		t.Fatalf("hand-made persona: %v", err)
	}
}

func TestLeadMoneyCap(t *testing.T) {
	svc := leadSvc(t, 0)
	pastRun(t, svc, "r1", "Ana", "Ana", "pay") // $6 against the default $5
	_, err := svc.Start(context.Background(), "Kate", project.LeadStory)
	if !errors.Is(err, ErrLeadOverBudget) || !strings.Contains(err.Error(), "Kate") {
		t.Fatalf("lead over money cap: %v", err)
	}
}

func TestSaveProjectChildNameTaken(t *testing.T) {
	svc := leadSvc(t, 0)
	p, _ := svc.Store.RawProject()
	p.Roles[0].Personas = append(p.Roles[0].Personas, project.Persona{Name: "Ana"})
	if err := svc.SaveProject(context.Background(), p); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "Ana") {
		t.Fatalf("persona named like a child: %v", err)
	}
	p, _ = svc.Store.RawProject()
	p.Roles = append(p.Roles, project.Role{Name: "Ana"})
	if err := svc.SaveProject(context.Background(), p); !errors.Is(err, ErrInvalid) {
		t.Fatalf("role named like a child: %v", err)
	}
}

func TestAdoptMovesChild(t *testing.T) {
	svc := leadSvc(t, 0)
	ctx := context.Background()
	bad := project.Persona{Profile: project.Profile{Device: "toaster"}}
	if err := svc.Adopt(ctx, "Ana", bad, []project.Story{{Name: "pay", Goal: "g"}}); err == nil {
		t.Fatal("invalid profile adopted")
	}
	if _, err := svc.Store.Child("Ana"); err != nil {
		t.Fatalf("child lost on a failed adopt: %v", err)
	}
	pe := project.Persona{Traits: []string{"phone"}, Lead: &project.Lead{SeeRuns: true}}
	if err := svc.Adopt(ctx, "Ana", pe, []project.Story{{Name: "pay", Goal: "pay twice"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := svc.Store.RawProject()
	r, ok := raw.Role("Ana")
	if !ok || r.MadeBy != "" || r.Description != "Adopted from Kate" || r.Personas[0].Name != "Ana" || r.Personas[0].Lead != nil ||
		r.Stories[0].Goal != "pay twice" {
		t.Fatalf("adopted role %+v %v", r, ok)
	}
	if _, err := svc.Store.Child("Ana"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("child still stored")
	}
	if err := svc.Adopt(ctx, "Ana", pe, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("adopt twice: %v", err)
	}
}

func TestWakeLead(t *testing.T) {
	svc := leadSvc(t, 0)
	now := time.Now()
	q, err := svc.WakeLead("Kate", "Run from the board", now)
	if err != nil || q.Story != project.LeadStory || q.Role != "buyer" || !strings.Contains(string(q.Context), `"reason":"Run from the board"`) {
		t.Fatalf("wake %+v %v", q, err)
	}
	if _, err := svc.WakeLead("Kate", "again", now); !errors.Is(err, ErrBusy) {
		t.Fatalf("second wake: %v", err)
	}
	if _, err := svc.WakeLead("ann", "x", now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wake a non-lead: %v", err)
	}
}
