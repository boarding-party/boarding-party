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
		name     string
		p        project.Project
		from, to string
		persona  string
		filtered string
	}{
		{"subdomain", p, "no-reply@mail.smesolutions.uk", "cast+john@ex.test", "John", ""},
		{"exact address", p, "hello@tinysystems.io", "cast+john@ex.test", "John", ""},
		{"other sender", p, "x@evil.test", "cast+john@ex.test", "John", "sender not allowed"},
		{"unknown address", p, "hello@tinysystems.io", "cast+ada@ex.test", "", "no persona has this address"},
		{"empty allowFrom", noAllow, "hello@tinysystems.io", "cast+john@ex.test", "John", "sender not allowed"},
		{"suffix attack", p, "a@smesolutions.uk.evil.test", "cast+john@ex.test", "John", "sender not allowed"},
		{"domain not address", p, "other@tinysystems.io", "cast+john@ex.test", "John", "sender not allowed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			persona, filtered := Route(Message{From: tc.from, To: []string{"x@y.test", tc.to}}, tc.p)
			if persona != tc.persona || filtered != tc.filtered {
				t.Errorf("got (%q, %q), want (%q, %q)", persona, filtered, tc.persona, tc.filtered)
			}
		})
	}
}
