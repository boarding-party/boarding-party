package runs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestProfileRoundTrip(t *testing.T) {
	svc, _ := newSvc(t)
	r, err := svc.Start(context.Background(), "ann", "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if p, how, err := svc.Profile(r.Token); err != nil || p != nil || how != ProfileNone {
		t.Fatalf("empty: %s %q %v", p, how, err)
	}
	state := `{"cookies":[{"name":"sid","value":"x"}],"origins":[]}`
	if err := svc.SaveProfile(r.Token, []byte(state), time.Now()); err != nil {
		t.Fatal(err)
	}
	if p, how, err := svc.Profile(r.Token); err != nil || string(p) != state || how != ProfileRestored {
		t.Fatalf("saved: %s %q %v", p, how, err)
	}
}

func TestProfileFreshBrowserStory(t *testing.T) {
	svc, _ := newSvc(t)
	p, _ := svc.Store.Project()
	p.Roles[0].Stories[0].FreshBrowser = true
	if err := svc.Store.PutProject(p); err != nil {
		t.Fatal(err)
	}
	r, err := svc.Start(context.Background(), "ann", "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SaveProfile(r.Token, []byte(`{"cookies":[],"origins":[]}`), time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, how, err := svc.Profile(r.Token); err != nil || got != nil || how != ProfileFresh {
		t.Fatalf("fresh story: %s %q %v", got, how, err)
	}
}

func TestSaveProfileRejectsBadState(t *testing.T) {
	svc, _ := newSvc(t)
	r, err := svc.Start(context.Background(), "ann", "checkout")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{``, `not json`, `[]`, `{"cookies":[]}`, `{"origins":[]}`, `{"cookies":{},"origins":[]}`,
		`{"cookies":[],"origins":[]} trailing`} {
		if err := svc.SaveProfile(r.Token, []byte(bad), time.Now()); !errors.Is(err, ErrBadProfile) {
			t.Errorf("%q: got %v, want ErrBadProfile", bad, err)
		}
	}
	big := `{"cookies":[],"origins":[{"origin":"https://x.test","localStorage":[{"name":"k","value":"` +
		strings.Repeat("a", MaxProfileBytes) + `"}]}]}`
	if err := svc.SaveProfile(r.Token, []byte(big), time.Now()); !errors.Is(err, ErrBadProfile) {
		t.Errorf("too big: got %v", err)
	}
	if p, _, _ := svc.Profile(r.Token); p != nil {
		t.Fatalf("bad state stored: %s", p)
	}
}

func TestProfileNeedsActiveRun(t *testing.T) {
	svc, _ := newSvc(t)
	if _, _, err := svc.Profile("deadbeef"); !errors.Is(err, ErrNotActive) {
		t.Errorf("profile: %v", err)
	}
	if err := svc.SaveProfile("deadbeef", []byte(`{"cookies":[],"origins":[]}`), time.Now()); !errors.Is(err, ErrNotActive) {
		t.Errorf("save: %v", err)
	}
}
