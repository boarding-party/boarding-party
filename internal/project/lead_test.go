package project

import (
	"reflect"
	"strings"
	"testing"
)

// leadProject is a project JSON with one persona whose lead block is lead.
func leadProject(lead string) string {
	return `{"name":"p","sites":["https://x.test"],"roles":[{"name":"owner","personas":[{"name":"Kate","lead":` + lead + `}]}]}`
}

func TestLeadValidation(t *testing.T) {
	bad := []struct{ name, json, want string }{
		{"zero budget", leadProject(`{"seeRuns":true,"dailyBudget":0}`), "dailyBudget"},
		{"big budget", leadProject(`{"seeRuns":true,"dailyBudget":1001}`), "dailyBudget"},
		{"negative runs", leadProject(`{"seeRuns":true,"dailyRuns":-1}`), "dailyRuns"},
		{"many runs", leadProject(`{"seeRuns":true,"dailyRuns":101}`), "dailyRuns"},
		{"negative children", leadProject(`{"seeRuns":true,"maxChildren":-1}`), "maxChildren"},
		{"many children", leadProject(`{"seeRuns":true,"maxChildren":21}`), "maxChildren"},
		{"wake words", leadProject(`{"seeRuns":true,"wakeAt":"9am"}`), "wakeAt"},
		{"wake 24", leadProject(`{"seeRuns":true,"wakeAt":"24:00"}`), "wakeAt"},
		{"long brief", leadProject(`{"seeRuns":true,"brief":"` + strings.Repeat("a", 2001) + `"}`), "brief"},
		{"madeBy", `{"name":"p","sites":["https://x.test"],"roles":[{"name":"a","madeBy":"Kate"}]}`, "madeBy is set by the director"},
		{"reserved persona", `{"name":"p","sites":["https://x.test"],"roles":[{"name":"a","personas":[{"name":"(lead)"}]}]}`, "(lead) is reserved"},
		{"reserved story", `{"name":"p","sites":["https://x.test"],"roles":[{"name":"a","stories":[{"name":"(lead)","goal":"g"}]}]}`, "(lead) is reserved"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadJSON([]byte(tc.json))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	if _, err := LoadJSON([]byte(leadProject(`{"brief":"find bugs","seeRuns":true,"wakeAt":"07:30"}`))); err != nil {
		t.Fatal(err)
	}
}

func TestCreateImpliesStart(t *testing.T) {
	p, err := LoadJSON([]byte(leadProject(`{"createPersonas":true,"startRuns":false}`)))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Roles[0].Personas[0].Lead.StartRuns {
		t.Fatal("createPersonas should tick startRuns")
	}
}

func TestLeadDefaults(t *testing.T) {
	var l Lead
	if l.Budget() != 5 || l.Runs() != 10 || l.Children() != 6 {
		t.Fatalf("defaults %v %v %v", l.Budget(), l.Runs(), l.Children())
	}
	if h, m := l.Wake(); h != 9 || m != 0 {
		t.Fatalf("wake %d:%d", h, m)
	}
	b := 2.5
	l = Lead{DailyBudget: &b, DailyRuns: 3, MaxChildren: 2, WakeAt: "07:45"}
	if h, m := l.Wake(); l.Budget() != 2.5 || l.Runs() != 3 || l.Children() != 2 || h != 7 || m != 45 {
		t.Fatal("set values not used")
	}
}

func TestIsLead(t *testing.T) {
	if (Persona{}).IsLead() {
		t.Fatal("nil lead")
	}
	if (Persona{Lead: &Lead{Brief: "b"}}).IsLead() {
		t.Fatal("brief only")
	}
	for _, l := range []Lead{{SeeRuns: true}, {CreatePersonas: true}, {StartRuns: true}, {TidyBugs: true}} {
		if !(Persona{Lead: &l}).IsLead() {
			t.Fatalf("%+v should be a lead", l)
		}
	}
}

func TestLeadOfAndChildren(t *testing.T) {
	p := Project{Roles: []Role{
		{Name: "owner", Personas: []Persona{{Name: "Kate", Lead: &Lead{CreatePersonas: true}}}},
		{Name: "Ana", MadeBy: "Kate", Personas: []Persona{{Name: "Ana"}}},
	}}
	if l, ok := p.LeadOf("Ana"); !ok || l != "Kate" {
		t.Fatalf("LeadOf(Ana) = %q %v", l, ok)
	}
	if _, ok := p.LeadOf("Kate"); ok {
		t.Fatal("Kate has no lead")
	}
	if got := p.ChildrenOf("Kate"); !reflect.DeepEqual(got, []string{"Ana"}) {
		t.Fatalf("ChildrenOf = %v", got)
	}
	if ls := p.Leads(); len(ls) != 1 || ls[0].Name != "Kate" {
		t.Fatalf("Leads = %v", ls)
	}
	if pe, r, ok := p.Persona("Ana"); !ok || pe.Name != "Ana" || r.Name != "Ana" {
		t.Fatal("Persona(Ana)")
	}
}
