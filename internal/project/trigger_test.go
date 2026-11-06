package project

import (
	"strings"
	"testing"
	"time"
)

func TestMessageRuleMatches(t *testing.T) {
	tests := []struct {
		name    string
		rule    MessageRule
		from    string
		subject string
		text    string
		links   []string
		want    bool
	}{
		{"from domain", MessageRule{From: "smesolutions.uk"}, "hello@smesolutions.uk", "", "", nil, true},
		{"from subdomain", MessageRule{From: "smesolutions.uk"}, "a@mail.smesolutions.uk", "", "", nil, true},
		{"from lookalike", MessageRule{From: "smesolutions.uk"}, "x@evil-smesolutions.uk", "", "", nil, false},
		{"from address", MessageRule{From: "hello@smesolutions.uk"}, "other@smesolutions.uk", "", "", nil, false},
		{"subject", MessageRule{Subject: "confirm"}, "", "Confirm Martyn's Law reminders", "", nil, true},
		{"subject miss", MessageRule{Subject: "confirm"}, "", "Welcome", "", nil, false},
		{"keywords in text", MessageRule{Keywords: []string{"reminders", "hall"}}, "", "Reminders", "Book the Hall", nil, true},
		{"keywords need all", MessageRule{Keywords: []string{"reminders", "hall"}}, "", "Reminders", "nothing", nil, false},
		{"link subdomain", MessageRule{LinkHost: "smesolutions.uk"}, "", "", "", []string{"https://www.smesolutions.uk/r?x"}, true},
		{"link host case", MessageRule{LinkHost: "SMESolutions.uk"}, "", "", "", []string{"https://smesolutions.uk/r"}, true},
		{"link lookalike", MessageRule{LinkHost: "smesolutions.uk"}, "", "", "", []string{"https://smesolutions.uk.evil.test/"}, false},
		{"no links", MessageRule{LinkHost: "smesolutions.uk"}, "", "", "", nil, false},
		{"combined ok", MessageRule{From: "smesolutions.uk", Subject: "confirm", LinkHost: "smesolutions.uk"},
			"a@smesolutions.uk", "Please confirm", "", []string{"https://smesolutions.uk/c"}, true},
		{"combined one fails", MessageRule{From: "smesolutions.uk", Subject: "confirm", LinkHost: "smesolutions.uk"},
			"a@smesolutions.uk", "Hello", "", []string{"https://smesolutions.uk/c"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rule.Matches(tc.from, tc.subject, tc.text, tc.links); got != tc.want {
				t.Fatalf("Matches = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCalendarRule(t *testing.T) {
	if !(CalendarRule{}).Matches("anything") {
		t.Fatal("empty title should match everything")
	}
	r := CalendarRule{Title: "Appointment"}
	if !r.Matches("Video appointment with Dr Lee") {
		t.Fatal("title should match case-insensitively")
	}
	if r.Matches("Lunch") {
		t.Fatal("title should not match")
	}
	if got := (CalendarRule{}).LeadTime(); got != 2*time.Minute {
		t.Fatalf("default lead = %v", got)
	}
	if got := (CalendarRule{Lead: "5m"}).LeadTime(); got != 5*time.Minute {
		t.Fatalf("lead = %v", got)
	}
}

func TestSenderMatches(t *testing.T) {
	if !SenderMatches("A@B.test", []string{"a@b.test"}) || SenderMatches("nobody", []string{"b.test"}) {
		t.Fatal("SenderMatches")
	}
}

func TestTriggerValidation(t *testing.T) {
	doc := func(trigger string) string {
		return `name: p
sites: ["https://x.test"]
roles:
  - name: a
    stories:
      - name: s
        goal: g
        trigger: ` + trigger + `
`
	}
	tests := []struct{ name, trigger, want string }{
		{"both kinds", `{message: {subject: x}, calendar: {title: y}}`, "a story trigger has either message or calendar, not both"},
		{"empty trigger", `{}`, "a story trigger needs a message or a calendar rule"},
		{"empty message", `{message: {}}`, "a message trigger needs at least one of from, subject, keywords, linkHost"},
		{"negative lead", `{calendar: {lead: -1m}}`, "lead"},
		{"long lead", `{calendar: {lead: 25h}}`, "lead"},
		{"bad lead", `{calendar: {lead: soon}}`, "lead"},
		{"link host slash", `{message: {linkHost: "a.test/x"}}`, "linkHost"},
		{"link host port", `{message: {linkHost: "a.test:80"}}`, "linkHost"},
		{"blank keyword", `{message: {keywords: [" "]}}`, "keywords"},
		{"bad from", `{message: {from: "a b"}}`, "from"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load([]byte(doc(tc.trigger)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), `role "a": story "s": `) {
				t.Fatalf("err = %v, want role/story prefix", err)
			}
		})
	}
	for _, ok := range []string{
		`{message: {from: x.test, subject: s, keywords: [a], linkHost: l.test}}`,
		`{calendar: {}}`,
		`{calendar: {title: t, lead: 0s}}`,
		`{calendar: {lead: 24h}}`,
	} {
		if _, err := Load([]byte(doc(ok))); err != nil {
			t.Fatalf("%s: %v", ok, err)
		}
	}
}
