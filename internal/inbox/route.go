package inbox

import (
	"net/mail"
	"slices"
	"strings"

	"github.com/boarding-party/boardingparty/internal/project"
)

// Filtered reasons set by Route.
const (
	FilteredNoPersona    = "no persona has this address"
	FilteredSenderDenied = "sender not allowed"
	FilteredUnverified   = "sender not verified"
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
	for _, to := range append(slices.Clone(m.Delivered), m.To...) {
		if name, ok := owner[strings.ToLower(to)]; ok {
			persona = name
			break
		}
	}
	if persona == "" {
		return "", FilteredNoPersona
	}
	if p.Inbox == nil || !project.SenderMatches(m.From, p.Inbox.AllowFrom) {
		return persona, FilteredSenderDenied
	}
	if !m.Verified || m.AuthServ != trustedAuthServ(p) {
		return persona, FilteredUnverified
	}
	return persona, ""
}

// DefaultAuthServID is the authserv-id trusted when the project sets none: Gmail, where persona mailboxes live.
const DefaultAuthServID = "mx.google.com"

func trustedAuthServ(p project.Project) string {
	if p.Inbox != nil && p.Inbox.AuthServID != "" {
		return p.Inbox.AuthServID
	}
	return DefaultAuthServID
}
