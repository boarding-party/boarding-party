package runs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

// loginSvc has lead Kate (email megan@x.io), hand-made Tom and Kate's child
// Ana, and a waiting run w-<persona> for each of Tom and Ana.
func loginSvc(t *testing.T, kind string) *Service {
	t.Helper()
	svc := emptySvc(t)
	p := project.Project{Name: "p", Sites: []string{"https://x.test"}, Roles: []project.Role{{Name: "owner",
		Personas: []project.Persona{
			{Name: "Kate", Identity: map[string]string{"email": "megan@x.io"}, Lead: &project.Lead{CreatePersonas: true, StartRuns: true}},
			{Name: "Tom"},
		},
		Stories: []project.Story{{Name: "settings", Goal: "change the VAT rate"}}}}}
	if err := svc.Store.PutProject(p); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.AddChild(store.Child{Name: "Ana", Lead: "Kate", Persona: project.Persona{Name: "Ana", Identity: map[string]string{"password": "gen"}},
		Stories: []project.Story{{Name: "pay", Goal: "pay"}}, Created: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct{ persona, role string }{{"Tom", "owner"}, {"Ana", "Ana"}} {
		id := "w-" + x.persona
		if err := svc.Store.CreateRun(store.Run{ID: id, Token: "tok-" + id, Persona: x.persona, Role: x.role, Story: "settings",
			State: store.RunRunning, Started: time.Now()}); err != nil {
			t.Fatal(err)
		}
		w := store.Wait{Kind: kind, Login: "admin", From: "a", Deadline: time.Now().Add(time.Hour)}
		if ok, err := svc.Store.FinishRunWaiting(id, "needs admin", time.Now(), w); err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	return svc
}

func TestGiveLoginResumes(t *testing.T) {
	svc := loginSvc(t, "login")
	if err := svc.GiveLogin(context.Background(), "w-Ana", " ann@x.test ", "pw1"); err != nil {
		t.Fatal(err)
	}
	p, _ := svc.Store.RawProject()
	if l, ok := p.LoginFor("admin"); !ok || l.Username != "ann@x.test" || l.Password != "pw1" {
		t.Fatalf("logins %+v", p.Logins)
	}
	if c, _ := svc.Store.Child("Ana"); c.Persona.Login != "admin" {
		t.Fatalf("child login %q", c.Persona.Login)
	}
	if w, _ := svc.Store.WaitFor("w-Ana"); w.At.IsZero() {
		t.Fatal("wait not answered")
	}
	merged, _ := svc.Store.Project()
	if a, _, _ := merged.Persona("Ana"); a.Identity["username"] != "ann@x.test" || a.Identity["password"] != "pw1" {
		t.Fatalf("Ana identity %v", a.Identity)
	}
	// A second login for the same kind replaces the first.
	if err := svc.GiveLogin(context.Background(), "w-Tom", "bob@x.test", "pw2"); err != nil {
		t.Fatal(err)
	}
	p, _ = svc.Store.RawProject()
	if len(p.Logins) != 1 || p.Logins[0].Username != "bob@x.test" {
		t.Fatalf("logins %+v", p.Logins)
	}
}

func TestGiveLoginHandmade(t *testing.T) {
	svc := loginSvc(t, "login")
	if err := svc.GiveLogin(context.Background(), "w-Tom", "ann@x.test", "pw"); err != nil {
		t.Fatal(err)
	}
	p, _ := svc.Store.RawProject()
	if tom, _, _ := p.Persona("Tom"); tom.Login != "admin" {
		t.Fatalf("Tom login %q", tom.Login)
	}
}

func TestGiveLoginRefused(t *testing.T) {
	svc := loginSvc(t, "email")
	if err := svc.GiveLogin(context.Background(), "w-Tom", "ann@x.test", "pw"); !errors.Is(err, ErrNoLoginWait) {
		t.Fatalf("email wait: %v", err)
	}
	if err := svc.GiveLogin(context.Background(), "nope", "ann@x.test", "pw"); !errors.Is(err, ErrNoLoginWait) {
		t.Fatalf("no run: %v", err)
	}
	p, _ := svc.Store.RawProject()
	if len(p.Logins) != 0 {
		t.Fatalf("saved anyway: %+v", p.Logins)
	}
	svc = loginSvc(t, "login")
	for _, bad := range [][2]string{{"", "pw"}, {"u", ""}, {string(make([]byte, 201)), "pw"}} {
		if err := svc.GiveLogin(context.Background(), "w-Tom", bad[0], bad[1]); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%q: %v", bad[0], err)
		}
	}
}

func TestFillEmails(t *testing.T) {
	svc := emptySvc(t)
	p := project.Project{Name: "p", Sites: []string{"https://x.test"}, Roles: []project.Role{{Name: "owner",
		Personas: []project.Persona{
			{Name: "Kate", Lead: &project.Lead{CreatePersonas: true, StartRuns: true}},
			{Name: "Lee", Identity: map[string]string{"email": "lee@x.io"}, Lead: &project.Lead{CreatePersonas: true, StartRuns: true}},
			{Name: "Tom"},
		}}}}
	if err := svc.Store.PutProject(p); err != nil {
		t.Fatal(err)
	}
	for _, c := range []store.Child{
		{Name: "Ana", Lead: "Kate", Persona: project.Persona{Name: "Ana", Identity: map[string]string{"password": "gen"}}},
		{Name: "Bo", Lead: "Kate", Persona: project.Persona{Name: "Bo", Identity: map[string]string{"email": "bo@own.io"}}},
	} {
		c.Stories, c.Created = []project.Story{{Name: "pay", Goal: "pay"}}, time.Now()
		if err := svc.Store.AddChild(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.FillEmails(context.Background(), "bpuser"); err != nil {
		t.Fatal(err)
	}
	if c, _ := svc.Store.Child("Ana"); c.Persona.Identity["email"] != "" {
		t.Fatal("filled from a plain username")
	}
	if err := svc.FillEmails(context.Background(), "bp@x.io"); err != nil {
		t.Fatal(err)
	}
	raw, _ := svc.Store.RawProject()
	kate, _, _ := raw.Persona("Kate")
	lee, _, _ := raw.Persona("Lee")
	tom, _, _ := raw.Persona("Tom")
	if kate.Identity["email"] != "bp@x.io" || lee.Identity["email"] != "lee@x.io" || tom.Identity["email"] != "" {
		t.Fatalf("leads %v %v, Tom %v", kate.Identity, lee.Identity, tom.Identity)
	}
	ana, _ := svc.Store.Child("Ana")
	bo, _ := svc.Store.Child("Bo")
	if ana.Persona.Identity["email"] != "bp+ana@x.io" || ana.Persona.Identity["password"] != "gen" || bo.Persona.Identity["email"] != "bo@own.io" {
		t.Fatalf("Ana %v Bo %v", ana.Persona.Identity, bo.Persona.Identity)
	}
}
