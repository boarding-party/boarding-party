package runs

import (
	"errors"
	"fmt"
	"sort"
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
	if d.FixChecks, err = s.fixChecks(since); err != nil {
		return d, fmt.Errorf("fix checks: %w", err)
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

	p, err := s.Store.Project()
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return d, fmt.Errorf("load project: %w", err)
	} // no project yet: the default timezone and budget
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

// fixChecks are the checks that settled since: passed when the check
// closed the issue ("Gone: …"), failed when it reopened it ("Still there:
// …"). The store keeps no status history, so these are read off the issue.
func (s *Service) fixChecks(since time.Time) (digest.List[digest.FixCheck], error) {
	is, err := s.Store.Issues()
	if err != nil {
		return digest.List[digest.FixCheck]{}, err
	}
	type at struct {
		digest.FixCheck
		when time.Time
	}
	var all []at
	for _, i := range is {
		switch {
		case i.Status == store.IssueClosed && strings.HasPrefix(i.CheckNote, "Gone:") && !i.Updated.Before(since):
			all = append(all, at{digest.FixCheck{IssueID: i.ID, Title: i.Summary, Passed: true}, i.Updated})
		case !i.Reopened.Before(since) && strings.HasPrefix(i.CheckNote, "Still there:"):
			all = append(all, at{digest.FixCheck{IssueID: i.ID, Title: i.Summary}, i.Reopened})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].when.Before(all[j].when) })
	out := make([]digest.FixCheck, len(all))
	for i, a := range all {
		out[i] = a.FixCheck
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
