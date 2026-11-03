package inbox

import (
	"net/mail"
	"strings"

	"github.com/boarding-party/boardingparty/internal/project"
)

// Filtered reasons set by Route.
const (
	FilteredNoPersona    = "no persona has this address"
	FilteredSenderDenied = "sender not allowed"
)

// Route finds the persona a message is addressed to and checks the sender against
// the project's allow-list. The address is checked first; persona is empty when no
// persona owns any recipient address.
func Route(m Message, p project.Project) (persona, filtered string) {
	owner := map[string]string{}
	for _, r := range p.Roles {
		for _, pe := range r.Personas {
			if a, err := mail.ParseAddress(pe.Identity["email"]); err == nil {
				owner[strings.ToLower(a.Address)] = pe.Name
			}
		}
	}
	for _, to := range m.To {
		if name, ok := owner[strings.ToLower(to)]; ok {
			persona = name
			break
		}
	}
	if persona == "" {
		return "", FilteredNoPersona
	}
	if p.Inbox == nil || !senderAllowed(m.From, p.Inbox.AllowFrom) {
		return persona, FilteredSenderDenied
	}
	return persona, ""
}

func senderAllowed(from string, allow []string) bool {
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
