package project

import (
	"fmt"
	"strings"
	"testing"
)

// loginProject is a project JSON with logins and one persona whose login is login.
func loginProject(logins, login string) string {
	return `{"name":"p","sites":["https://x.test"],"logins":` + logins +
		`,"roles":[{"name":"owner","personas":[{"name":"Kate","login":"` + login + `"}]}]}`
}

func TestLoginsValidate(t *testing.T) {
	long := strings.Repeat("a", MaxLoginField+1)
	bad := []struct{ name, json, want string }{
		{"empty kind", loginProject(`[{"kind":" ","username":"u","password":"p"}]`, ""), "kind"},
		{"long kind", loginProject(`[{"kind":"`+strings.Repeat("k", MaxLoginKind+1)+`","username":"u","password":"p"}]`, ""), "kind"},
		{"duplicate kind", loginProject(`[{"kind":"Admin","username":"u","password":"p"},{"kind":"admin","username":"v","password":"q"}]`, ""), "duplicate"},
		{"empty username", loginProject(`[{"kind":"admin","username":"","password":"p"}]`, ""), "username"},
		{"long username", loginProject(`[{"kind":"admin","username":"`+long+`","password":"p"}]`, ""), "username"},
		{"long password", loginProject(`[{"kind":"admin","username":"u","password":"`+long+`"}]`, ""), "password"},
		{"empty password", loginProject(`[{"kind":"admin","username":"u","password":""}]`, ""), "password"},
		{"unknown login", loginProject(`[{"kind":"admin","username":"u","password":"p"}]`, "owner"), `persona "Kate"`},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadJSON([]byte(tc.json))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	p, err := LoadJSON([]byte(loginProject(`[{"kind":" Admin ","username":" ann@x.test ","password":"pw"}]`, "admin")))
	if err != nil {
		t.Fatal(err)
	}
	l, ok := p.LoginFor("ADMIN")
	if !ok || l.Kind != "Admin" || l.Username != "ann@x.test" || l.Password != "pw" {
		t.Fatalf("LoginFor = %+v, %v", l, ok)
	}
	if got := p.LoginKinds(); len(got) != 1 || got[0] != "Admin" {
		t.Fatalf("LoginKinds = %v", got)
	}
	if _, ok := p.LoginFor("owner"); ok {
		t.Fatal("found a login that is not there")
	}
}

func TestChildEmail(t *testing.T) {
	if got := ChildEmail("bp+old@x.com", "Priya Shah"); got != "bp+priya.shah@x.com" {
		t.Fatalf("got %q", got)
	}
	if got := ChildEmail("bp@x.com", "Sean O'Neil"); got != "bp+sean.oneil@x.com" {
		t.Fatalf("got %q", got)
	}
	if got := ChildEmail("not an address", "A"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestSiteDomain(t *testing.T) {
	for in, want := range map[string]string{
		"https://staging.shop.example.co.uk/app": "example.co.uk",
		"https://app.example.com":                "example.com",
		"https://example.com":                    "example.com",
		"::":                                     "",
	} {
		if got := SiteDomain(in); got != want {
			t.Errorf("SiteDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFirstRunBriefFits(t *testing.T) {
	var stories []string
	for i := range MaxFirstRunStories {
		stories = append(stories, fmt.Sprintf("%d%s", i, strings.Repeat("s", MaxFirstRunStory-1)))
	}
	b := FirstRunBrief(stories, MaxFirstRunPersonas)
	if len(b) > MaxLeadBrief {
		t.Fatalf("brief is %d characters, more than %d", len(b), MaxLeadBrief)
	}
	for _, s := range stories {
		if !strings.Contains(b, s) {
			t.Fatalf("brief misses story %q", s[:5])
		}
	}
	for _, w := range []string{"6 personas", "age", "device", "tech confidence", "login"} {
		if !strings.Contains(b, w) {
			t.Fatalf("brief misses %q:\n%s", w, b)
		}
	}
	if b1 := FirstRunBrief([]string{"sign up"}, 1); !strings.Contains(b1, "1 persona") || strings.Contains(b1, "1 personas") {
		t.Fatalf("brief for one persona:\n%s", b1)
	}
}

func TestFirstRunBriefFitsInOtherScripts(t *testing.T) {
	var stories []string
	for range MaxFirstRunStories {
		stories = append(stories, strings.Repeat("ї", MaxFirstRunStory))
	}
	b := FirstRunBrief(stories, MaxFirstRunPersonas)
	j := `{"name":"p","sites":["https://x.test"],"roles":[{"name":"r","personas":[{"name":"L","lead":{"seeRuns":true,"brief":` + fmt.Sprintf("%q", b) + `}}]}]}`
	if _, err := LoadJSON([]byte(j)); err != nil {
		t.Fatalf("5 stories of %d Cyrillic letters: %v", MaxFirstRunStory, err)
	}
}
