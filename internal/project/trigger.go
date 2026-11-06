package project

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
)

// Trigger starts a story from an event instead of the role's schedule.
// Exactly one kind is set.
type Trigger struct {
	Message  *MessageRule  `json:"message,omitempty"`
	Calendar *CalendarRule `json:"calendar,omitempty"`
}

// MessageRule matches an incoming message. Every field that is set must
// match; at least one must be set.
type MessageRule struct {
	// From is a full address or a domain, which also matches its subdomains.
	From string `json:"from,omitempty"`
	// Subject is a case-insensitive substring of the subject.
	Subject string `json:"subject,omitempty"`
	// Keywords must all appear, case-insensitively, in subject and text.
	Keywords []string `json:"keywords,omitempty"`
	// LinkHost is a bare host name that some link must point at, or at a subdomain of.
	LinkHost string `json:"linkHost,omitempty"`
}

// CalendarRule matches a calendar event by title.
type CalendarRule struct {
	// Title is a case-insensitive substring; empty matches every event.
	Title string `json:"title,omitempty"`
	// Lead is how long before the event starts the story runs, a Go duration
	// from 0 to 24h. Empty means DefaultLead.
	Lead string `json:"lead,omitempty"`
}

// DefaultLead is the lead time of a calendar rule that sets none.
const DefaultLead = 2 * time.Minute

// MaxLead is the longest lead time a calendar rule may set.
const MaxLead = 24 * time.Hour

// Matches reports whether a message satisfies every field the rule sets.
func (r MessageRule) Matches(from, subject, text string, links []string) bool {
	if r.From != "" && !SenderMatches(from, []string{r.From}) {
		return false
	}
	if r.Subject != "" && !strings.Contains(strings.ToLower(subject), strings.ToLower(r.Subject)) {
		return false
	}
	body := strings.ToLower(subject + " " + text)
	for _, k := range r.Keywords {
		if !strings.Contains(body, strings.ToLower(k)) {
			return false
		}
	}
	if r.LinkHost != "" {
		want := strings.ToLower(r.LinkHost)
		found := false
		for _, l := range links {
			u, err := url.Parse(l)
			if err != nil {
				continue
			}
			if h := strings.ToLower(u.Hostname()); h == want || strings.HasSuffix(h, "."+want) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Matches reports whether an event title satisfies the rule.
func (r CalendarRule) Matches(title string) bool {
	return strings.Contains(strings.ToLower(title), strings.ToLower(r.Title))
}

// LeadTime is the parsed Lead; validation guarantees it parses.
func (r CalendarRule) LeadTime() time.Duration {
	if r.Lead == "" {
		return DefaultLead
	}
	d, _ := time.ParseDuration(r.Lead)
	return d
}

func (t Trigger) validate() error {
	switch {
	case t.Message != nil && t.Calendar != nil:
		return fmt.Errorf("a story trigger has either message or calendar, not both")
	case t.Message != nil:
		return t.Message.validate()
	case t.Calendar != nil:
		return t.Calendar.validate()
	}
	return fmt.Errorf("a story trigger needs a message or a calendar rule")
}

func (r MessageRule) validate() error {
	if r.From == "" && r.Subject == "" && len(r.Keywords) == 0 && r.LinkHost == "" {
		return fmt.Errorf("a message trigger needs at least one of from, subject, keywords, linkHost")
	}
	if strings.ContainsAny(r.From, " \t\r\n") {
		return fmt.Errorf("trigger from %q must be an address or a domain", r.From)
	}
	for _, k := range r.Keywords {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("trigger keywords must not be blank")
		}
	}
	if r.LinkHost != "" && (strings.ContainsAny(r.LinkHost, "/:") || len(validation.IsDNS1123Subdomain(strings.ToLower(r.LinkHost))) > 0) {
		return fmt.Errorf("trigger linkHost %q must be a bare host name such as smesolutions.uk", r.LinkHost)
	}
	return nil
}

func (r CalendarRule) validate() error {
	if r.Lead == "" {
		return nil
	}
	d, err := time.ParseDuration(r.Lead)
	if err != nil || d < 0 || d > MaxLead {
		return fmt.Errorf("trigger lead %q must be a duration from 0s to 24h, such as 2m", r.Lead)
	}
	return nil
}

// SenderMatches reports whether a sender address is on an allow-list whose
// entries are full addresses or domains; a domain also covers its subdomains.
func SenderMatches(from string, allow []string) bool {
	from = strings.ToLower(from)
	at := strings.LastIndexByte(from, '@')
	if at < 0 {
		return false
	}
	domain := from[at+1:]
	for _, e := range allow {
		e = strings.ToLower(e)
		if strings.Contains(e, "@") {
			if from == e {
				return true
			}
		} else if e != "" && (domain == e || strings.HasSuffix(domain, "."+e)) {
			return true
		}
	}
	return false
}
