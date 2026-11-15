package digest

import "testing"

func TestCapCountsTheRest(t *testing.T) {
	all := make([]int, 13)
	l := Cap(all)
	if len(l.Items) != MaxItems || l.More != 3 {
		t.Fatalf("items %d more %d, want 10 and 3", len(l.Items), l.More)
	}
	e := Cap[int](nil)
	if e.Items == nil || len(e.Items) != 0 || e.More != 0 {
		t.Fatalf("empty %+v, want non-nil empty items", e)
	}
}

func TestNews(t *testing.T) {
	if (Digest{}).News() || (Digest{Paused: true}).News() || (Digest{Runs: Runs{Finished: 6}}).News() {
		t.Fatal("an empty, only paused or only healthy digest has news")
	}
	for name, d := range map[string]Digest{
		"bug":   {NewBugs: List[Bug]{Items: []Bug{{ID: "i1"}}}},
		"more":  {FixChecks: List[FixCheck]{More: 1}},
		"over":  {OverBudget: true},
		"broke": {Journeys: Journeys{Broke: List[Journey]{Items: []Journey{{ID: "j"}}}}},
	} {
		if !d.News() {
			t.Errorf("%s: no news", name)
		}
	}
}
