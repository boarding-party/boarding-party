package project

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/yaml"
)

func TestLoadRejects(t *testing.T) {
	tests := []struct{ name, yaml string }{
		{"empty sites", "name: p\nsites: []\nroles: []\n"},
		{"http site", "name: p\nsites: [\"http://x.test\"]\n"},
		{"duplicate role", `name: p
sites: ["https://x.test"]
roles:
  - {name: a}
  - {name: a}
`},
		{"persona without name", `name: p
sites: ["https://x.test"]
roles:
  - name: a
    personas: [{traits: [x]}]
`},
		{"duplicate persona across roles", `name: p
sites: ["https://x.test"]
roles:
  - {name: a, personas: [{name: ann}]}
  - {name: b, personas: [{name: ann}]}
`},
		{"story without goal", `name: p
sites: ["https://x.test"]
roles:
  - name: a
    stories: [{name: s, goal: ""}]
`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load([]byte(tc.yaml)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestLoadExample(t *testing.T) {
	b, err := os.ReadFile("../../examples/todomvc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	p, err := Load(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Roles) != 1 || p.Roles[0].Name != "visitor" {
		t.Fatalf("roles: %+v", p.Roles)
	}
	r := p.Roles[0]
	if len(r.Personas) != 1 || len(r.Stories) != 1 {
		t.Fatalf("role: %+v", r)
	}
	if !strings.Contains(r.Stories[0].Goal, "2 items left") {
		t.Fatalf("goal: %q", r.Stories[0].Goal)
	}
}

func TestRunResourcesDefaultsAndOverride(t *testing.T) {
	base := "name: p\nsites: [\"https://x.test\"]\n"
	p, err := Load([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	if r := p.RunResources(); r.CPU != "1" || r.Memory != "3Gi" {
		t.Errorf("default = %+v", r)
	}
	p, err = Load([]byte(base + "resources: {cpu: 500m, memory: 4Gi}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r := p.RunResources(); r.CPU != "500m" || r.Memory != "4Gi" {
		t.Errorf("override = %+v", r)
	}
	p, err = LoadJSON([]byte(`{"name":"p","sites":["https://x.test"],"resources":{"memory":"2Gi"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if r := p.RunResources(); r.CPU != "1" || r.Memory != "2Gi" {
		t.Errorf("partial override = %+v", r)
	}
}

func TestResourcesRejected(t *testing.T) {
	for _, res := range []string{
		`{"cpu":"100m"}`, `{"cpu":"2"}`, `{"cpu":"5"}`, `{"cpu":"lots"}`,
		`{"memory":"512Mi"}`, `{"memory":"16Gi"}`, `{"memory":"big"}`,
	} {
		j := `{"name":"p","sites":["https://x.test"],"resources":` + res + `}`
		if _, err := LoadJSON([]byte(j)); err == nil {
			t.Errorf("LoadJSON accepted resources %s", res)
		}
		y := "name: p\nsites: [\"https://x.test\"]\nresources: " + res + "\n"
		if _, err := Load([]byte(y)); err == nil {
			t.Errorf("Load accepted resources %s", res)
		}
	}
	for _, res := range []string{`{"cpu":"250m","memory":"1Gi"}`, `{"cpu":"1","memory":"8Gi"}`} {
		if _, err := LoadJSON([]byte(`{"name":"p","sites":["https://x.test"],"resources":` + res + `}`)); err != nil {
			t.Errorf("rejected boundary %s: %v", res, err)
		}
	}
}

func TestNodePool(t *testing.T) {
	p, err := LoadJSON([]byte(`{"name":"p","sites":["https://x.test"],"nodePool":"boardingparty.io/run=true"}`))
	if err != nil {
		t.Fatal(err)
	}
	if k, v, ok := p.Pool(); !ok || k != "boardingparty.io/run" || v != "true" {
		t.Errorf("Pool() = %q %q %v", k, v, ok)
	}
	if _, _, ok := (Project{}).Pool(); ok {
		t.Error("unset nodePool reports a pool")
	}
	for _, bad := range []string{"runs", "=true", "pool=", "bad key=x", "pool=a b", "pool=x=y"} {
		if _, err := LoadJSON([]byte(`{"name":"p","sites":["https://x.test"],"nodePool":"` + bad + `"}`)); err == nil {
			t.Errorf("accepted nodePool %q", bad)
		}
	}
}

func TestRuntimeClass(t *testing.T) {
	p, err := LoadJSON([]byte(`{"name":"p","sites":["https://x.test"],"runtimeClass":"gvisor"}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.RuntimeClass != "gvisor" {
		t.Errorf("RuntimeClass = %q", p.RuntimeClass)
	}
	for _, bad := range []string{"gVisor", "run sc", "a_b", "-gvisor"} {
		if _, err := LoadJSON([]byte(`{"name":"p","sites":["https://x.test"],"runtimeClass":"` + bad + `"}`)); err == nil {
			t.Errorf("accepted runtimeClass %q", bad)
		}
	}
}

func TestInboxAllowFrom(t *testing.T) {
	base := "name: p\nsites: [\"https://x.test\"]\n"
	p, err := Load([]byte(base + "inbox:\n  allowFrom: [Example.com, A@B.test]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Inbox.AllowFrom; len(got) != 2 || got[0] != "example.com" || got[1] != "a@b.test" {
		t.Errorf("allowFrom = %v", got)
	}
	for _, bad := range []string{`["not a domain"]`, `["@x"]`} {
		_, err := Load([]byte(base + "inbox:\n  allowFrom: " + bad + "\n"))
		if err == nil || !strings.Contains(err.Error(), "inbox.allowFrom") {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
}

func TestInboxAuthServID(t *testing.T) {
	base := "name: p\nsites: [\"https://x.test\"]\ninbox:\n  allowFrom: [a.test]\n"
	p, err := Load([]byte(base + "  authServId: MX.Example.test\n"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Inbox.AuthServID != "mx.example.test" {
		t.Errorf("authServId = %q", p.Inbox.AuthServID)
	}
	for _, bad := range []string{"not a host", "nodot"} {
		_, err := Load([]byte(base + "  authServId: \"" + bad + "\"\n"))
		if err == nil || !strings.Contains(err.Error(), "inbox.authServId") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func TestExtraHosts(t *testing.T) {
	p, err := LoadJSON([]byte(`{"name":"p","sites":["https://x.test"],"extraHosts":[" Fonts.GStatic.com "]}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.ExtraHosts[0] != "fonts.gstatic.com" {
		t.Fatalf("extra host not normalised: %q", p.ExtraHosts[0])
	}
	for _, h := range []string{"com", ".com", "co.uk", "*", "fonts.gstatic.com\nevil.com", "a b.com", "https://x.com", "localhost",
		"10.0.0.1", "169.254.169.254", "metadata.google.internal", "bp-director.boardingparty.svc", "kubernetes.default.svc.cluster.local", "printer.local", "10.1", "0x7f.1",
		"169.254.169.254.nip.io", "10-0-0-1.sslip.io", "app.lvh.me", "x.10.0.0.1.example.com"} {
		if _, err := LoadJSON([]byte(`{"name":"p","sites":["https://x.test"],"extraHosts":["` + h + `"]}`)); err == nil {
			t.Errorf("extra host %q accepted", h)
		}
	}
}

func TestSitesMustBePublic(t *testing.T) {
	for _, site := range []string{"https://10.0.0.5/", "https://[::1]/", "https://metadata.google.internal/", "https://app.svc/", "https://staging/",
		"https://.com/", "https://.example.com/", "https://example.com./", "https://a..example.com/"} {
		if _, err := LoadJSON([]byte(`{"name":"p","sites":["` + site + `"]}`)); err == nil {
			t.Errorf("site %q accepted", site)
		}
	}
}

func TestStoryModel(t *testing.T) {
	doc := func(model string) []byte {
		return []byte(`{"name":"p","sites":["https://x.test"],"roles":[{"name":"customer","personas":[{"name":"ann"}],"stories":[{"name":"pay","goal":"g","model":"` + model + `"},{"name":"browse","goal":"g"}]}]}`)
	}
	p, err := LoadJSON(doc("claude-haiku-5-5"))
	if err != nil {
		t.Fatal(err)
	}
	if m := p.StoryModel("customer", "pay"); m != "claude-haiku-5-5" {
		t.Errorf("pay model = %q", m)
	}
	if m := p.StoryModel("customer", "browse"); m != "" {
		t.Errorf("browse model = %q, want the project's", m)
	}
	for _, bad := range []string{"Claude Haiku", "haiku 5", "-x"} {
		if _, err := LoadJSON(doc(bad)); err == nil {
			t.Errorf("story model %q accepted", bad)
		}
	}
}

const handoffYAML = `name: p
sites: ["https://x.test"]
roles:
  - name: patient
    stories:
      - name: book
        goal: book a slot
        handoffs: [{role: clinician, story: approve}]
  - name: clinician
    stories:
      - {name: approve, goal: approve it}
`

func TestHandoffsLoad(t *testing.T) {
	p, err := Load([]byte(handoffYAML))
	if err != nil {
		t.Fatal(err)
	}
	want := []Handoff{{"clinician", "approve"}}
	if got := p.Roles[0].Stories[0].Handoffs; !reflect.DeepEqual(got, want) {
		t.Fatalf("handoffs = %v, want %v", got, want)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	q, err := LoadJSON(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p, q) {
		t.Fatalf("round trip differs:\n%v\n%v", p, q)
	}
}

func TestHandoffsValidate(t *testing.T) {
	mk := func(h string) string {
		return `name: p
sites: ["https://x.test"]
roles:
  - name: patient
    stories:
      - {name: book, goal: g, handoffs: ` + h + `}
  - name: clinician
    stories:
      - {name: approve, goal: g}
`
	}
	tests := []struct{ name, handoffs, want string }{
		{"unknown role", `[{role: nurse, story: x}]`, `hands off to unknown role "nurse"`},
		{"unknown story", `[{role: clinician, story: triage}]`, `hands off to unknown story "triage" of role "clinician"`},
		{"same role", `[{role: patient, story: book}]`, `hands off to its own role`},
		{"duplicate", `[{role: clinician, story: approve}, {role: clinician, story: approve}]`, `hands off to clinician/approve twice`},
		{"empty role", `[{story: approve}]`, `handoff needs a role and a story`},
		{"empty story", `[{role: clinician}]`, `handoff needs a role and a story`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load([]byte(mk(tc.handoffs)))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestFindStory(t *testing.T) {
	p, err := Load([]byte(handoffYAML))
	if err != nil {
		t.Fatal(err)
	}
	st, ok := p.FindStory("clinician", "approve")
	if !ok || st.Goal != "approve it" {
		t.Fatalf("got %v %v", st, ok)
	}
	if _, ok := p.FindStory("clinician", "book"); ok {
		t.Fatal("found missing story")
	}
	if _, ok := p.FindStory("nobody", "book"); ok {
		t.Fatal("found missing role")
	}
	if r, ok := p.Role("patient"); !ok || r.Name != "patient" {
		t.Fatalf("Role: %v %v", r, ok)
	}
	if _, ok := p.Role("nobody"); ok {
		t.Fatal("found missing role")
	}
}

func TestProjectTimezoneAndBudget(t *testing.T) {
	base := "name: p\nsites: [\"https://x.test\"]\n"
	p, err := Load([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	if p.Location().String() != "Europe/London" || p.Budget() != 5 {
		t.Errorf("defaults: %v %v", p.Location(), p.Budget())
	}
	p, err = Load([]byte(base + "timezone: America/New_York\ndailyBudget: 12.5\n"))
	if err != nil || p.Location().String() != "America/New_York" || p.Budget() != 12.5 {
		t.Errorf("set: %v %v %v", err, p.Location(), p.Budget())
	}
	for _, tc := range []struct{ yaml, want string }{
		{"timezone: Mars/Base\n", `timezone "Mars/Base" is not a time zone, such as Europe/London`},
		{"dailyBudget: 0\n", "dailyBudget must be more than 0 and at most 1000 (US dollars)"},
		{"dailyBudget: 1001\n", "dailyBudget must be more than 0 and at most 1000 (US dollars)"},
	} {
		if _, err := Load([]byte(base + tc.yaml)); err == nil || err.Error() != tc.want {
			t.Errorf("%q: got %v, want %q", tc.yaml, err, tc.want)
		}
		js, _ := yamlToJSON(base + tc.yaml)
		if _, err := LoadJSON(js); err == nil || err.Error() != tc.want {
			t.Errorf("json %q: got %v", tc.yaml, err)
		}
	}
}

// The browser speaks the project's language: en-GB unless set, canonical
// when set, refused when it is no language tag.
func TestProjectLocale(t *testing.T) {
	base := "name: p\nsites: [\"https://x.test\"]\n"
	p, err := Load([]byte(base))
	if err != nil || p.BrowserLocale() != "en-GB" {
		t.Fatalf("default: %v %q", err, p.BrowserLocale())
	}
	for in, want := range map[string]string{"en-US": "en-US", "fr_fr": "fr-FR", "cy": "cy"} {
		p, err := Load([]byte(base + "locale: " + in + "\n"))
		if err != nil || p.BrowserLocale() != want {
			t.Errorf("%s: %v %q, want %q", in, err, p.BrowserLocale(), want)
		}
	}
	for _, in := range []string{"English", "'en GB'", "en-GB-x-'quote'"} {
		_, err := Load([]byte(base + "locale: " + in + "\n"))
		if err == nil || !strings.Contains(err.Error(), "is not a language tag, such as en-GB") {
			t.Errorf("%s: got %v", in, err)
		}
	}
}

func yamlToJSON(s string) ([]byte, error) { return yaml.YAMLToJSON([]byte(s)) }

func TestRoleActivity(t *testing.T) {
	base := "name: p\nsites: [\"https://x.test\"]\nroles:\n  - name: buyer\n    activity: "
	p, err := Load([]byte(base + "every 30 mins, 09:00-17:30\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Roles[0].Activity; got != "every 30 min, 09:00–17:30" {
		t.Errorf("canonical = %q", got)
	}
	if p.Roles[0].Pattern().IsZero() {
		t.Error("Pattern is zero")
	}
	_, err = Load([]byte(base + "sometimes\n"))
	if err == nil || !strings.Contains(err.Error(), `role "buyer": activity "sometimes"`) {
		t.Errorf("err = %v", err)
	}
}

func TestParseExplore(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"": 168 * time.Hour, "daily": 24 * time.Hour, "Weekly": 168 * time.Hour, "every 3 days": 72 * time.Hour,
		"every 1 day": 24 * time.Hour, "every 90 days": 90 * 24 * time.Hour, "every 12 h": 12 * time.Hour,
		"every 1 hour": time.Hour, "every 2160 hours": 2160 * time.Hour,
	} {
		got, err := ParseExplore(in)
		if err != nil || got != want {
			t.Errorf("ParseExplore(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"every 0 days", "monthly", "every 91 days", "every 2161 h", "every -1 h", "every x days", "every 3"} {
		if _, err := ParseExplore(in); err == nil {
			t.Errorf("ParseExplore(%q) accepted", in)
		}
	}
	base := "name: p\nsites: [\"https://x.test\"]\nroles:\n  - name: buyer\n    explore: "
	for in, canon := range map[string]string{"daily": "daily", "every 7 days": "weekly", "every 3 days": "every 3 days",
		"every 12 hours": "every 12 h", "every 48 h": "every 2 days", "weekly": "weekly"} {
		p, err := Load([]byte(base + in + "\n"))
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got := p.Roles[0].Explore; got != canon {
			t.Errorf("explore %q stored as %q, want %q", in, got, canon)
		}
	}
	if _, err := Load([]byte(base + "monthly\n")); err == nil || !strings.Contains(err.Error(), `role "buyer": explore "monthly"`) {
		t.Errorf("monthly: %v", err)
	}
	p, _ := Load([]byte("name: p\nsites: [\"https://x.test\"]\nroles:\n  - name: buyer\n"))
	if p.Roles[0].Explore != "" || p.Roles[0].ExploreEvery() != 168*time.Hour {
		t.Errorf("default explore %q %v", p.Roles[0].Explore, p.Roles[0].ExploreEvery())
	}
}

func TestStoryReplays(t *testing.T) {
	no := false
	if !(Story{}).Replays() || (Story{Replay: &no}).Replays() {
		t.Fatal("Replays: nil means yes, false means no")
	}
	p, err := Load([]byte("name: p\nsites: [\"https://x.test\"]\nroles:\n  - name: buyer\n    stories:\n      - name: s\n        goal: g\n        replay: false\n"))
	if err != nil || p.Roles[0].Stories[0].Replays() {
		t.Fatalf("replay: false %v", err)
	}
}
