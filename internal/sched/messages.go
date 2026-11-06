package sched

import (
	"context"
	"encoding/json"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

// LookBack is how far back an unfired message is still considered: a message
// older than this never starts a story, so a long outage or a new trigger rule
// does not replay old mail.
const LookBack = 24 * time.Hour

// maxMessages bounds the messages one tick looks at.
const maxMessages = 200

// Messages is the trigger source for delivered email: a story starts when a
// message for its persona matches the story's trigger rule.
type Messages struct {
	Store *store.Store
}

func (*Messages) Name() string { return "message" }

func (m *Messages) Due(_ context.Context, p project.Project, now time.Time) ([]Fire, error) {
	msgs, err := m.Store.UnfiredMessages(now.Add(-LookBack), maxMessages)
	if err != nil {
		return nil, err
	}
	var fires []Fire
	for _, msg := range msgs {
		role, ok := roleOf(p, msg.Persona)
		var matches []string
		if ok {
			for _, st := range role.Stories {
				if st.Trigger != nil && st.Trigger.Message != nil &&
					st.Trigger.Message.Matches(msg.From, msg.Subject, msg.Text, msg.Links) {
					matches = append(matches, st.Name)
				}
			}
		}
		if len(matches) == 0 {
			// Nothing to start (or the persona is gone): never offered again.
			if err := m.Store.MarkMessageFired(msg.ID, "", now); err != nil {
				return nil, err
			}
			continue
		}
		subject := clip(msg.Subject, 80)
		ctx, err := json.Marshal(store.RunContext{Kind: "message", MessageID: msg.ID,
			Arrived: "Email from " + clip(msg.From, MaxContextText) + ": " + clip(msg.Subject, MaxContextText)})
		if err != nil {
			return nil, err
		}
		fires = append(fires, Fire{Source: m.Name(), Persona: msg.Persona, Role: role.Name, Stories: matches,
			Reason: "Email: " + subject, Context: ctx})
	}
	return fires, nil
}

// Settle marks the message fired with the story queued, or "" when it was
// skipped: a message is never replayed.
func (m *Messages) Settle(f Fire, story string, now time.Time) error {
	var c store.RunContext
	if err := json.Unmarshal(f.Context, &c); err != nil {
		return err
	}
	return m.Store.MarkMessageFired(c.MessageID, story, now)
}

func roleOf(p project.Project, persona string) (project.Role, bool) {
	for _, r := range p.Roles {
		for _, ps := range r.Personas {
			if ps.Name == persona {
				return r, true
			}
		}
	}
	return project.Role{}, false
}
