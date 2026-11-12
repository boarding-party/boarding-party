package runs

import (
	"sort"
	"time"
)

// Value is what the cast spent since a time and what it found for it. A bug
// is an issue first reported in the window and not marked not a bug; it is
// credited to the run whose report came first.
type Value struct {
	Since    time.Time  `json:"since"`
	Runs     int        `json:"runs"`
	Spent    float64    `json:"spent"`
	Bugs     int        `json:"bugs"`
	PerBug   float64    `json:"perBug"` // 0 when nothing was found
	Personas []ValueRow `json:"personas"`
	Stories  []ValueRow `json:"stories"`
}

// ValueRow is one persona's or one story's share.
type ValueRow struct {
	Name  string  `json:"name"`
	Runs  int     `json:"runs"`
	Spent float64 `json:"spent"`
	Bugs  int     `json:"bugs"`
}

// Value adds up spend and new bugs since t, by persona and by story, the
// dearest first.
func (s *Service) Value(since time.Time) (Value, error) {
	rs, err := s.Store.RunsSince(since)
	if err != nil {
		return Value{}, err
	}
	finds, err := s.Store.FirstFinds(since)
	if err != nil {
		return Value{}, err
	}
	found := map[string]int{}
	for _, f := range finds {
		found[f.RunID]++
	}
	v := Value{Since: since, Runs: len(rs)}
	personas, stories := map[string]*ValueRow{}, map[string]*ValueRow{}
	add := func(m map[string]*ValueRow, name string, spent float64, bugs int) {
		row := m[name]
		if row == nil {
			row = &ValueRow{Name: name}
			m[name] = row
		}
		row.Runs++
		row.Spent += spent
		row.Bugs += bugs
	}
	for _, r := range rs {
		u, err := s.Usage(r.ID)
		if err != nil {
			return Value{}, err
		}
		var spent float64
		if u != nil {
			spent = Cost(*u)
		}
		bugs := found[r.ID]
		v.Spent += spent
		add(personas, r.Persona, spent, bugs)
		add(stories, r.Story, spent, bugs)
	}
	// A run that started before the window can still report a bug in it:
	// the bug counts, its run's spend and row do not.
	v.Bugs = len(finds)
	if v.Bugs > 0 {
		v.PerBug = v.Spent / float64(v.Bugs)
	}
	v.Personas, v.Stories = dearestFirst(personas), dearestFirst(stories)
	return v, nil
}

func dearestFirst(m map[string]*ValueRow) []ValueRow {
	out := make([]ValueRow, 0, len(m))
	for _, row := range m {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Spent != out[j].Spent {
			return out[i].Spent > out[j].Spent
		}
		return out[i].Name < out[j].Name
	})
	return out
}
