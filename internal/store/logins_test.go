package store

import (
	"testing"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
)

func TestProjectOverlaysLogin(t *testing.T) {
	s := open(t)
	raw := leadRaw(true)
	raw.Logins = []project.Login{{Kind: "Admin", Username: "ann@x.test", Password: "pw"}}
	raw.Roles[0].Personas[1].Login = "Admin"
	raw.Roles[0].Personas[1].Identity = map[string]string{"email": "tom@x.test", "password": "own"}
	putRaw(t, s, raw)
	ana := child("Ana", "Kate")
	ana.Persona.Login = "admin"
	ana.Persona.Identity = map[string]string{"email": "bp+ana@x.test", "password": "gen"}
	if err := s.AddChild(ana); err != nil {
		t.Fatal(err)
	}
	p, err := s.Project()
	if err != nil {
		t.Fatal(err)
	}
	tom, _, _ := p.Persona("Tom")
	if tom.Identity["username"] != "ann@x.test" || tom.Identity["password"] != "pw" || tom.Identity["email"] != "tom@x.test" {
		t.Fatalf("Tom identity %v", tom.Identity)
	}
	a, _, _ := p.Persona("Ana")
	if a.Identity["username"] != "ann@x.test" || a.Identity["password"] != "pw" || a.Identity["email"] != "bp+ana@x.test" {
		t.Fatalf("Ana identity %v", a.Identity)
	}
	r, err := s.RawProject()
	if err != nil {
		t.Fatal(err)
	}
	if rt, _, _ := r.Persona("Tom"); rt.Identity["password"] != "own" || rt.Identity["username"] != "" {
		t.Fatalf("raw Tom changed: %v", rt.Identity)
	}
	cs, err := s.Children("Kate")
	if err != nil || cs[0].Persona.Identity["password"] != "gen" {
		t.Fatalf("stored child changed: %v %v", cs, err)
	}
}

func TestProjectLoginMissingKindNoOverlay(t *testing.T) {
	s := open(t)
	putRaw(t, s, leadRaw(true))
	ana := child("Ana", "Kate")
	ana.Persona.Login = "admin"
	ana.Persona.Identity = map[string]string{"password": "gen"}
	if err := s.AddChild(ana); err != nil {
		t.Fatal(err)
	}
	p, err := s.Project()
	if err != nil {
		t.Fatal(err)
	}
	if a, _, _ := p.Persona("Ana"); a.Identity["password"] != "gen" || a.Identity["username"] != "" {
		t.Fatalf("Ana identity %v", a.Identity)
	}
}

func TestGiveLogin(t *testing.T) {
	s := open(t)
	waitRun(t, s, "r1", RunRunning)
	waitRun(t, s, "r2", RunRunning)
	fin := time.Unix(100, 0).UTC()
	if ok, err := s.FinishRunWaiting("r1", "needs admin", fin, Wait{Kind: "login", Login: "admin", Note: "open Settings", Deadline: fin.Add(time.Hour)}); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if ok, err := s.FinishRunWaiting("r2", "needs mail", fin, Wait{Kind: "email", From: "a", Deadline: fin.Add(time.Hour)}); err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	ws, err := s.OpenWaits()
	if err != nil || len(ws) != 2 || ws[0].Login != "admin" || ws[0].Note != "open Settings" || !ws[0].At.IsZero() {
		t.Fatalf("waits %+v %v", ws, err)
	}
	at := fin.Add(time.Minute)
	if ok, err := s.GiveLogin("r1", at); err != nil || !ok {
		t.Fatalf("give: ok=%v err=%v", ok, err)
	}
	w, _ := s.WaitFor("r1")
	if !w.At.Equal(at) {
		t.Fatalf("at = %v", w.At)
	}
	if ok, err := s.GiveLogin("r1", at); err != nil || ok {
		t.Fatalf("second give: ok=%v err=%v", ok, err)
	}
	if ok, err := s.GiveLogin("r2", at); err != nil || ok {
		t.Fatalf("email wait: ok=%v err=%v", ok, err)
	}
}
