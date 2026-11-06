package sched

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

// Calendar is the trigger source for persona calendar events: a story starts
// its lead time before an event whose title matches the story's trigger rule.
type Calendar struct {
	Store *store.Store
}

func (*Calendar) Name() string { return "calendar" }

func (c *Calendar) Due(_ context.Context, p project.Project, now time.Time) ([]Fire, error) {
	maxLead, found := time.Duration(0), false
	for _, r := range p.Roles {
		for _, st := range r.Stories {
			if st.Trigger != nil && st.Trigger.Calendar != nil {
				maxLead, found = max(maxLead, st.Trigger.Calendar.LeadTime()), true
			}
		}
	}
	if !found {
		return nil, nil
	}
	events, err := c.Store.DueEvents(now, maxLead)
	if err != nil {
		return nil, err
	}
	var fires []Fire
	for _, e := range events {
		role, ok := roleOf(p, e.Persona)
		var ready []string
		matched := false
		if ok {
			for _, st := range role.Stories {
				if st.Trigger == nil || st.Trigger.Calendar == nil || !st.Trigger.Calendar.Matches(e.Title) {
					continue
				}
				matched = true
				if !e.Start.Add(-st.Trigger.Calendar.LeadTime()).After(now) {
					ready = append(ready, st.Name)
				}
			}
		}
		if !matched {
			// Nothing could ever start (or the persona is gone): not offered again
			// until the event moves.
			if err := c.Store.MarkEventFired(e.Persona, e.UID, e.Start, ""); err != nil {
				return nil, err
			}
			continue
		}
		if len(ready) == 0 {
			continue // its lead is not reached yet
		}
		ctx, err := json.Marshal(store.RunContext{Kind: "calendar", Event: &store.ContextEvent{UID: e.UID,
			Title: store.Clip(e.Title, MaxContextText), Start: e.Start, Location: store.Clip(e.Location, MaxContextText), URL: e.URL}})
		if err != nil {
			return nil, err
		}
		fires = append(fires, Fire{Source: c.Name(), Persona: e.Persona, Role: role.Name, Stories: ready,
			Reason: store.CalendarReason(e.Title), Context: ctx, RetryWhenBusy: true})
	}
	return fires, nil
}

// Settle marks the event fired for its current start with the story queued,
// or "" when it was skipped: it fires again only if the event is rescheduled.
// An event that met a busy persona is not settled, so it fires once the
// persona is free, until an hour after it starts.
func (c *Calendar) Settle(f Fire, story string, _ time.Time) error {
	var rc store.RunContext
	if err := json.Unmarshal(f.Context, &rc); err != nil {
		return err
	}
	if rc.Event == nil {
		return fmt.Errorf("calendar fire for %s has no event", f.Persona)
	}
	return c.Store.MarkEventFired(f.Persona, rc.Event.UID, rc.Event.Start, story)
}
