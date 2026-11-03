package inbox

import (
	"testing"

	"github.com/boarding-party/boardingparty/internal/project"
)

func TestRoute(t *testing.T) {
	p := project.Project{
		Roles: []project.Role{{Name: "r", Personas: []project.Persona{
			{Name: "John", Identity: map[string]string{"email": "Cast+John@ex.test"}},
		}}},
		Inbox: &project.Inbox{AllowFrom: []string{"smesolutions.uk", "hello@tinysystems.io"}},
	}
	noAllow := p
	noAllow.Inbox = nil
	tests := []struct {
		name       string
		p          project.Project
		from, to   string
		persona    string
		filtered   string
		unverified bool
	}{
		{"subdomain", p, "no-reply@mail.smesolutions.uk", "cast+john@ex.test", "John", "", false},
		{"exact address", p, "hello@tinysystems.io", "cast+john@ex.test", "John", "", false},
		{"other sender", p, "x@evil.test", "cast+john@ex.test", "John", "sender not allowed", false},
		{"unknown address", p, "hello@tinysystems.io", "cast+ada@ex.test", "", "no persona has this address", false},
		{"empty allowFrom", noAllow, "hello@tinysystems.io", "cast+john@ex.test", "John", "sender not allowed", false},
		{"suffix attack", p, "a@smesolutions.uk.evil.test", "cast+john@ex.test", "John", "sender not allowed", false},
		{"unverified", p, "hello@tinysystems.io", "cast+john@ex.test", "John", "sender not verified", true},
		{"domain not address", p, "other@tinysystems.io", "cast+john@ex.test", "John", "sender not allowed", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			persona, filtered := Route(Message{From: tc.from, Verified: !tc.unverified, AuthServ: "mx.google.com", To: []string{"x@y.test", tc.to}}, tc.p)
			if persona != tc.persona || filtered != tc.filtered {
				t.Errorf("got (%q, %q), want (%q, %q)", persona, filtered, tc.persona, tc.filtered)
			}
		})
	}
}

func TestRoutePrefersDelivered(t *testing.T) {
	p := project.Project{
		Roles: []project.Role{{Name: "r", Personas: []project.Persona{
			{Name: "A", Identity: map[string]string{"email": "a@ex.test"}},
			{Name: "B", Identity: map[string]string{"email": "b@ex.test"}},
		}}},
		Inbox: &project.Inbox{AllowFrom: []string{"example.test"}},
	}
	m := Message{From: "s@example.test", Verified: true, AuthServ: "mx.google.com", To: []string{"a@ex.test", "b@ex.test"}, Delivered: []string{"b@ex.test"}}
	if got, _ := Route(m, p); got != "B" {
		t.Errorf("persona = %q, want B", got)
	}
}

func TestRouteTrustedAuthServ(t *testing.T) {
	mk := func(id string) project.Project {
		return project.Project{
			Roles: []project.Role{{Name: "r", Personas: []project.Persona{{Name: "A", Identity: map[string]string{"email": "a@ex.test"}}}}},
			Inbox: &project.Inbox{AllowFrom: []string{"example.test"}, AuthServID: id},
		}
	}
	m := func(serv string) Message {
		return Message{From: "s@example.test", To: []string{"a@ex.test"}, Verified: true, AuthServ: serv}
	}
	tests := []struct {
		name, trusted, serv, filtered string
	}{
		{"default accepts google", "", "mx.google.com", ""},
		{"default rejects other", "", "evil.test", "sender not verified"},
		{"custom accepts own", "mx.example.test", "mx.example.test", ""},
		{"custom rejects google", "mx.example.test", "mx.google.com", "sender not verified"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, f := Route(m(tc.serv), mk(tc.trusted)); f != tc.filtered {
				t.Errorf("filtered = %q, want %q", f, tc.filtered)
			}
		})
	}
}
