package runs

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/boarding-party/boardingparty/internal/store"
	"github.com/boarding-party/boardingparty/pkg/digest"
)

// Digest is the board's news since since, for the cloud's morning email:
// what a person would want to know without opening the board. Titles and
// names only.
func (s *Service) Digest(since, now time.Time) (digest.Digest, error) {
	d := digest.Digest{Since: since, Until: now}
	var err error
	if d.NewBugs, err = s.newBugs(since); err != nil {
		return d, fmt.Errorf("new bugs: %w", err)
	}

	if d.Journeys.Broke, err = s.journeysDecided(since, "copilot called"); err != nil {
		return d, fmt.Errorf("journeys: %w", err)
	}
	if d.Journeys.Repaired, err = s.journeysDecided(since, DecideSiteChanged, DecideWeakness, "rolled back"); err != nil {
		return d, fmt.Errorf("journeys: %w", err)
	}
	finished, err := s.Store.RunsFinishedSince(since)
	if err != nil {
		return d, fmt.Errorf("runs: %w", err)
	}
	var stuck []digest.Run
	for _, r := range finished {
		if r.State == store.RunStuck || r.State == store.RunFailed {
			stuck = append(stuck, digest.Run{ID: r.ID, Persona: r.Persona, Story: r.Story, State: string(r.State)})
		}
	}
	d.Runs = digest.Runs{Finished: len(finished), Stuck: digest.Cap(stuck)}
	if d.FixChecks, err = s.fixChecks(finished); err != nil {
		return d, fmt.Errorf("fix checks: %w", err)
	}

	p, err := s.Store.Project()
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return d, fmt.Errorf("load project: %w", err)
	} // no project yet: the default timezone and budget
	if d.Asks, err = s.asksSince(p, since); err != nil {
		return d, fmt.Errorf("asks: %w", err)
	}
	loc := p.Location()
	today := DayStart(now, loc)
	if d.Spent, err = s.SpentBetween(DayStart(today.Add(-time.Hour), loc), today); err != nil {
		return d, fmt.Errorf("spend: %w", err)
	}
	d.Budget = p.Budget()
	d.OverBudget = d.Spent >= d.Budget
	if d.Paused, err = s.Store.Paused(); err != nil {
		return d, err
	}
	return d, nil
}

func (s *Service) newBugs(since time.Time) (digest.List[digest.Bug], error) {
	finds, err := s.Store.FirstFinds(since)
	if err != nil {
		return digest.List[digest.Bug]{}, err
	}
	var out []digest.Bug
	for _, f := range finds {
		is, err := s.Store.Issue(f.IssueID)
		if err != nil {
			return digest.List[digest.Bug]{}, err
		}
		b := digest.Bug{ID: is.ID, Title: is.Summary}
		reports, err := s.Store.IssueReports(is.ID)
		if err != nil {
			return digest.List[digest.Bug]{}, err
		}
		var first time.Time
		for _, r := range reports {
			if first.IsZero() || r.Created.Before(first) {
				first, b.Severity = r.Created, r.Severity
			}
		}
		out = append(out, b)
	}
	return digest.Cap(out), nil
}

// fixChecks are the fix checks among finished runs whose verdict settled
// the issue: passed when it closed it ("Gone: …"), failed when it reopened
// it ("Still there: …"). The verdict is read off the issue, which the check
// updated when it finished; a later check of the same issue wins.
func (s *Service) fixChecks(finished []store.Run) (digest.List[digest.FixCheck], error) {
	last := map[string]store.Run{}
	var order []string
	for _, r := range finished { // oldest first
		var c store.RunContext
		if json.Unmarshal(r.Context, &c) != nil || c.Verify == nil || c.Verify.IssueID == "" {
			continue
		}
		if _, ok := last[c.Verify.IssueID]; !ok {
			order = append(order, c.Verify.IssueID)
		}
		last[c.Verify.IssueID] = r
	}
	var out []digest.FixCheck
	for _, id := range order {
		i, err := s.Store.Issue(id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return digest.List[digest.FixCheck]{}, err
		}
		if i.Updated.Before(last[id].Finished) {
			continue // the check settled nothing: the issue had moved on
		}
		switch {
		case strings.HasPrefix(i.CheckNote, "Gone:"):
			out = append(out, digest.FixCheck{IssueID: i.ID, Title: i.Summary, Passed: true})
		case strings.HasPrefix(i.CheckNote, "Still there:"):
			out = append(out, digest.FixCheck{IssueID: i.ID, Title: i.Summary})
		}
	}
	return digest.Cap(out), nil
}

// journeysDecided lists, once each and oldest first, the journeys that got
// a decision of one of kinds since. Deleted journeys are left out.
func (s *Service) journeysDecided(since time.Time, kinds ...string) (digest.List[digest.Journey], error) {
	ds, err := s.Store.DecisionsSince(since, kinds...)
	if err != nil {
		return digest.List[digest.Journey]{}, err
	}
	seen := map[string]bool{}
	var out []digest.Journey
	for _, d := range ds {
		if seen[d.JourneyID] {
			continue
		}
		seen[d.JourneyID] = true
		j, err := s.Store.Journey(d.JourneyID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return digest.List[digest.Journey]{}, err
		}
		out = append(out, digest.Journey{ID: j.ID, Persona: j.Persona, Story: j.Story})
	}
	return digest.Cap(out), nil
}
