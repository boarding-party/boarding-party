package runs

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/boarding-party/boardingparty/internal/artifacts"
	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

// emptySvc is a service whose store has no project yet.
func emptySvc(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "bp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Service{Store: st, Artifacts: artifacts.NewFS(t.TempDir(), 1024), Launcher: &fakeLauncher{}}
}

func setupReq() SetupRequest {
	return SetupRequest{Site: "https://staging.example.com", Stories: []string{"a small business owner signs up", "an admin changes the VAT rate"},
		Personas: 4, DailyBudget: 7}
}

func firstLead(t *testing.T, p project.Project) project.Persona {
	t.Helper()
	r, ok := p.Role(project.FirstRunRole)
	if !ok || len(r.Personas) != 1 || r.Personas[0].Name != project.FirstRunLead {
		t.Fatalf("first run role %+v %v", r, ok)
	}
	return r.Personas[0]
}

func TestSetupWritesFirstRunLead(t *testing.T) {
	svc := emptySvc(t)
	req := setupReq()
	req.Logins = []project.Login{{Kind: "admin", Username: "ann@example.com", Password: "pw"}}
	if err := svc.Setup(context.Background(), req, ""); err != nil {
		t.Fatal(err)
	}
	p, err := svc.Store.RawProject()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Sites, []string{"https://staging.example.com"}) || p.Name != "staging.example.com" {
		t.Fatalf("project %q sites %v", p.Name, p.Sites)
	}
	l := firstLead(t, p).Lead
	if l == nil || !l.SeeRuns || !l.CreatePersonas || !l.StartRuns || l.TidyBugs || l.MaxChildren != 4 || l.DailyRuns != 8 ||
		l.Budget() != 7 || l.Brief != project.FirstRunBrief(req.Stories, 4) {
		t.Fatalf("lead %+v", l)
	}
	if p.Budget() != 7 {
		t.Fatalf("project budget %v, want it raised to 7", p.Budget())
	}
	if p.Inbox == nil || !slices.Equal(p.Inbox.AllowFrom, []string{"example.com"}) {
		t.Fatalf("inbox %+v", p.Inbox)
	}
	if lg, ok := p.LoginFor("admin"); !ok || lg.Username != "ann@example.com" {
		t.Fatalf("logins %+v", p.Logins)
	}
}

func TestSetupRunsCapped(t *testing.T) {
	svc := emptySvc(t)
	req := setupReq()
	req.Stories = []string{"a", "b", "c", "d", "e"}
	req.Personas = 6
	if err := svc.Setup(context.Background(), req, ""); err != nil {
		t.Fatal(err)
	}
	p, _ := svc.Store.RawProject()
	if l := firstLead(t, p).Lead; l.DailyRuns != 30 || l.MaxChildren != 6 {
		t.Fatalf("lead %+v", l)
	}
}

func TestSetupKeepsExistingFields(t *testing.T) {
	svc := emptySvc(t)
	big := 50.0
	old := project.Project{Name: "shop", Sites: []string{"https://old.example.com"}, ExtraHosts: []string{"fonts.gstatic.com"},
		Model: "claude-haiku-5-5", Timezone: "Europe/Kyiv", DailyBudget: &big, Inbox: &project.Inbox{AllowFrom: []string{"mail.example.org"}}}
	if err := svc.Store.PutProject(old); err != nil {
		t.Fatal(err)
	}
	if err := svc.Setup(context.Background(), setupReq(), ""); err != nil {
		t.Fatal(err)
	}
	p, _ := svc.Store.RawProject()
	if p.Name != "shop" || p.Model != "claude-haiku-5-5" || p.Timezone != "Europe/Kyiv" || !slices.Equal(p.ExtraHosts, []string{"fonts.gstatic.com"}) ||
		p.Budget() != 50 || !slices.Equal(p.Inbox.AllowFrom, []string{"mail.example.org"}) || !slices.Equal(p.Sites, []string{"https://staging.example.com"}) {
		t.Fatalf("project %+v", p)
	}
	firstLead(t, p)
}

func TestSetupRefusesSetUpProject(t *testing.T) {
	svc := emptySvc(t)
	if err := svc.Setup(context.Background(), setupReq(), ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Setup(context.Background(), setupReq(), ""); !errors.Is(err, ErrSetUp) {
		t.Fatalf("second setup err = %v", err)
	}
	p, _ := svc.Store.RawProject()
	if len(p.Roles) != 1 {
		t.Fatalf("roles %d", len(p.Roles))
	}
	done, _ := newSvc(t)
	if err := done.Setup(context.Background(), setupReq(), ""); !errors.Is(err, ErrSetUp) {
		t.Fatalf("set-up project err = %v", err)
	}
}

func TestSetupLeadEmailFromMailbox(t *testing.T) {
	svc := emptySvc(t)
	if err := svc.Setup(context.Background(), setupReq(), "bp@example.com"); err != nil {
		t.Fatal(err)
	}
	p, _ := svc.Store.RawProject()
	if firstLead(t, p).Identity["email"] != "bp@example.com" {
		t.Fatalf("identity %v", firstLead(t, p).Identity)
	}
	svc = emptySvc(t)
	if err := svc.Setup(context.Background(), setupReq(), "bpuser"); err != nil {
		t.Fatal(err)
	}
	p, _ = svc.Store.RawProject()
	if _, ok := firstLead(t, p).Identity["email"]; ok {
		t.Fatalf("identity %v", firstLead(t, p).Identity)
	}
}

func TestSetupValidates(t *testing.T) {
	bad := map[string]func(*SetupRequest){
		"no stories":    func(r *SetupRequest) { r.Stories = nil },
		"blank story":   func(r *SetupRequest) { r.Stories = []string{"  "} },
		"six stories":   func(r *SetupRequest) { r.Stories = []string{"a", "b", "c", "d", "e", "f"} },
		"long story":    func(r *SetupRequest) { r.Stories = []string{strings.Repeat("s", 201)} },
		"no personas":   func(r *SetupRequest) { r.Personas = 0 },
		"many personas": func(r *SetupRequest) { r.Personas = 7 },
		"no budget":     func(r *SetupRequest) { r.DailyBudget = 0 },
		"big budget":    func(r *SetupRequest) { r.DailyBudget = 1001 },
		"http site":     func(r *SetupRequest) { r.Site = "http://staging.example.com" },
		"bad login":     func(r *SetupRequest) { r.Logins = []project.Login{{Kind: "admin"}} },
	}
	for name, f := range bad {
		t.Run(name, func(t *testing.T) {
			svc := emptySvc(t)
			req := setupReq()
			f(&req)
			if err := svc.Setup(context.Background(), req, ""); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			if _, err := svc.Store.RawProject(); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("project written: %v", err)
			}
		})
	}
}
