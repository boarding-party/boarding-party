// Package digest is what a board's GET /api/digest answers: the news of a
// window, decided by the director, for the cloud's morning email. It holds
// titles and names only, never report bodies, page text or screenshots.
package digest

import "time"

// MaxItems caps every list; More counts the rest.
const MaxItems = 10

// List is up to MaxItems items and how many more there were.
type List[T any] struct {
	Items []T `json:"items"`
	More  int `json:"more"`
}

// Cap keeps the first MaxItems of all. Items is never nil.
func Cap[T any](all []T) List[T] {
	if len(all) > MaxItems {
		return List[T]{Items: all[:MaxItems], More: len(all) - MaxItems}
	}
	if all == nil {
		all = []T{}
	}
	return List[T]{Items: all}
}

func (l List[T]) empty() bool { return len(l.Items) == 0 && l.More == 0 }

// Bug is an issue first reported in the window.
type Bug struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Severity string `json:"severity"`
}

// FixCheck is a fix check that settled in the window: passed (the bug is
// gone) or failed (the bug is open again).
type FixCheck struct {
	IssueID string `json:"issueId"`
	Title   string `json:"title"`
	Passed  bool   `json:"passed"`
}

// Journey names a journey by its persona and story.
type Journey struct {
	ID      string `json:"id"`
	Persona string `json:"persona"`
	Story   string `json:"story"`
}

// Run is a run that got stuck or failed.
type Run struct {
	ID      string `json:"id"`
	Persona string `json:"persona"`
	Story   string `json:"story"`
	State   string `json:"state"`
}

// Ask is a lead asking its owner to raise one of its limits (maxChildren,
// dailyRuns or dailyBudget) from Now to To.
type Ask struct {
	Lead  string  `json:"lead"`
	Limit string  `json:"limit"`
	Now   float64 `json:"now"`
	To    float64 `json:"to"`
}

// Journeys broke (a replay failed) or got repaired or rolled back.
type Journeys struct {
	Broke    List[Journey] `json:"broke"`
	Repaired List[Journey] `json:"repaired"`
}

// Runs is how many runs finished and which got stuck or failed.
type Runs struct {
	Finished int       `json:"finished"`
	Stuck    List[Run] `json:"stuck"`
}

// Digest is a board's news between Since and Until. Spent is the previous
// local day's spend against the daily budget.
type Digest struct {
	Since      time.Time      `json:"since"`
	Until      time.Time      `json:"until"`
	NewBugs    List[Bug]      `json:"newBugs"`
	FixChecks  List[FixCheck] `json:"fixChecks"`
	Journeys   Journeys       `json:"journeys"`
	Runs       Runs           `json:"runs"`
	Asks       List[Ask]      `json:"asks"`
	Spent      float64        `json:"spent"`
	Budget     float64        `json:"budget"`
	OverBudget bool           `json:"overBudget"`
	Paused     bool           `json:"paused"`
}

// News reports whether the digest has anything to tell. Runs that went
// fine and a board that is only paused are not news: a board on a schedule
// would otherwise say the same thing every morning.
func (d Digest) News() bool {
	return !d.NewBugs.empty() || !d.FixChecks.empty() || !d.Journeys.Broke.empty() ||
		!d.Journeys.Repaired.empty() || !d.Runs.Stuck.empty() || !d.Asks.empty() || d.OverBudget
}
