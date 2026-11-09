package store

import (
	"errors"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
)

// leadRaw is a raw project with lead Kate (powers given) and hand-made Tom.
func leadRaw(powers bool) project.Project {
	return project.Project{Name: "p", Sites: []string{"https://x.test"}, Roles: []project.Role{{Name: "owner",
		Personas: []project.Persona{{Name: "Kate", Lead: &project.Lead{CreatePersonas: powers, StartRuns: powers}}, {Name: "Tom"}},
		Stories:  []project.Story{{Name: "invoice", Goal: "make an invoice"}}}}}
}

func child(name, lead string) Child {
	return Child{Name: name, Lead: lead, Reason: "nobody tried a phone",
		Persona: project.Persona{Name: name, Traits: []string{"phone"}},
		Stories: []project.Story{{Name: "pay", Goal: "pay an invoice"}}, Created: t0}
}

func putRaw(t *testing.T, s *Store, p project.Project) {
	t.Helper()
	if err := s.PutProject(p); err != nil {
		t.Fatal(err)
	}
}

func TestProjectMergesChildren(t *testing.T) {
	s := open(t)
	putRaw(t, s, leadRaw(true))
	if err := s.AddChild(child("Ana", "Kate")); err != nil {
		t.Fatal(err)
	}
	bo := child("Bo", "Kate")
	bo.Stopped = t0
	if err := s.AddChild(bo); err != nil {
		t.Fatal(err)
	}
	p, err := s.Project()
	if err != nil {
		t.Fatal(err)
	}
	r, ok := p.Role("Ana")
	if !ok || r.MadeBy != "Kate" || len(r.Personas) != 1 || r.Personas[0].Name != "Ana" || r.Stories[0].Name != "pay" {
		t.Fatalf("Ana role %+v %v", r, ok)
	}
	if _, ok := p.Role("Bo"); ok {
		t.Fatal("stopped child merged")
	}
	raw, err := s.RawProject()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := raw.Role("Ana"); ok {
		t.Fatal("raw project has a child")
	}
}

func TestOrphanChildLeftOut(t *testing.T) {
	s := open(t)
	putRaw(t, s, leadRaw(true))
	if err := s.AddChild(child("Ana", "Kate")); err != nil {
		t.Fatal(err)
	}
	putRaw(t, s, leadRaw(false))
	p, _ := s.Project()
	if _, ok := p.Role("Ana"); ok {
		t.Fatal("child of a lead without powers merged")
	}
	gone := leadRaw(true)
	gone.Roles[0].Personas = gone.Roles[0].Personas[1:]
	putRaw(t, s, gone)
	p, _ = s.Project()
	if _, ok := p.Role("Ana"); ok {
		t.Fatal("child of a removed lead merged")
	}
}

func TestAddChildTaken(t *testing.T) {
	s := open(t)
	if err := s.AddChild(child("Ana", "Kate")); err != nil {
		t.Fatal(err)
	}
	if err := s.AddChild(child("Ana", "Kate")); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
}

func TestChildUpdateStopDelete(t *testing.T) {
	s := open(t)
	_ = s.AddChild(child("Ana", "Kate"))
	_ = s.AddChild(child("Cy", "Lee"))
	c := child("Ana", "Kate")
	c.Stories[0].Goal = "pay twice"
	if err := s.UpdateChild(c); err != nil {
		t.Fatal(err)
	}
	if err := s.StopChild("Ana", t0); err != nil {
		t.Fatal(err)
	}
	got, err := s.Child("Ana")
	if err != nil || got.Stories[0].Goal != "pay twice" || !got.Stopped.Equal(t0) || got.Reason != "nobody tried a phone" {
		t.Fatalf("child %+v %v", got, err)
	}
	ks, _ := s.Children("Kate")
	all, _ := s.AllChildren()
	if len(ks) != 1 || len(all) != 2 {
		t.Fatalf("children %d all %d", len(ks), len(all))
	}
	if err := s.DeleteChild("Ana"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteChild("Ana"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete %v", err)
	}
	if err := s.UpdateChild(c); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update gone %v", err)
	}
}

func TestAdoptChild(t *testing.T) {
	s := open(t)
	putRaw(t, s, leadRaw(true))
	_ = s.AddChild(child("Ana", "Kate"))
	raw := leadRaw(true)
	raw.Roles = append(raw.Roles, project.Role{Name: "Ana", Personas: []project.Persona{{Name: "Ana"}},
		Stories: []project.Story{{Name: "pay", Goal: "pay it"}}})
	if err := s.AdoptChild("Ana", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Child("Ana"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("child still stored: %v", err)
	}
	got, _ := s.RawProject()
	if st, ok := got.FindStory("Ana", "pay"); !ok || st.Goal != "pay it" {
		t.Fatal("adopted role not in raw project")
	}
	if err := s.AdoptChild("Ana", raw); !errors.Is(err, ErrNotFound) {
		t.Fatalf("adopt unknown: %v", err)
	}
}

func TestLeadLog(t *testing.T) {
	s := open(t)
	for i, a := range []string{"create", "run", "stop"} {
		if err := s.LogLead(LeadAction{Lead: "Kate", RunID: "r1", Action: a, Subject: "Ana", Reason: "why " + a,
			Created: t0.Add(time.Duration(i) * time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.LogLead(LeadAction{Lead: "Lee", Action: "create", Created: t0})
	got, err := s.LeadLog("Kate", 2)
	if err != nil || len(got) != 2 || got[0].Action != "stop" || got[1].Action != "run" || got[0].Reason != "why stop" {
		t.Fatalf("log %+v %v", got, err)
	}
}

func TestJoinReport(t *testing.T) {
	s := open(t)
	withRun(t, s, "r1", "Ada", "todos")
	addBug(t, s, Bug{ID: "b1", RunID: "r1", Summary: "footer tiny", Created: t0})
	addBug(t, s, Bug{ID: "b2", RunID: "r1", Summary: "footer unreadable", Created: t0})
	b1, _ := s.Bugs("r1")
	a, b := b1[0].IssueID, b1[1].IssueID
	if b1[0].ID != "b1" {
		a, b = b, a
	}
	if err := s.SetIssueStatus(b, IssueConfirmed, "", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.JoinReport("b1", b, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Issue(a); !errors.Is(err, ErrNotFound) {
		t.Fatalf("emptied issue kept: %v", err)
	}
	is, _ := s.Issue(b)
	if is.Status != IssueConfirmed || is.Reports != 2 {
		t.Fatalf("target %+v", is)
	}
	if err := s.JoinReport("b1", "nope", t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown issue: %v", err)
	}
	if err := s.JoinReport("nope", b, t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown bug: %v", err)
	}
	if err := s.JoinReport("b1", b, t0); err != nil {
		t.Fatalf("already there: %v", err)
	}
}
