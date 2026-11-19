package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

// ErrSetUp says the project already has roles, so the first-run setup no
// longer applies.
var ErrSetUp = errors.New("the project is already set up")

// SetupRequest is what the owner gives a new project on the setup page.
type SetupRequest struct {
	Site        string          `json:"site"`
	Stories     []string        `json:"stories"`
	Personas    int             `json:"personas"`
	DailyBudget float64         `json:"dailyBudget"`
	Logins      []project.Login `json:"logins"`
}

// SetupNeeded reports whether the project has no roles yet (or no file).
func (s *Service) SetupNeeded() (bool, error) {
	p, err := s.Store.RawProject()
	if errors.Is(err, store.ErrNotFound) {
		return true, nil
	}
	return err == nil && len(p.Roles) == 0, err
}

// Setup starts a new project: it writes the site, the logins and a
// first-run lead whose brief is the owner's stories, into whatever project
// file exists. The scheduler's first wake then starts the lead. mailbox is
// the saved mailbox's username ("" for none); when it is an address, the
// lead gets it, so its personas get email.
func (s *Service) Setup(ctx context.Context, req SetupRequest, mailbox string) error {
	stories, err := checkSetup(req)
	if err != nil {
		return err
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	p, err := s.Store.RawProject()
	if errors.Is(err, store.ErrNotFound) {
		p, err = project.Project{}, nil
	}
	if err != nil {
		return err
	}
	if len(p.Roles) > 0 {
		return ErrSetUp
	}
	if p.Name == "" {
		if u, err := url.Parse(req.Site); err == nil {
			p.Name = u.Hostname()
		}
	}
	p.Sites = []string{req.Site}
	p.Logins = req.Logins
	if p.Budget() < req.DailyBudget {
		b := req.DailyBudget
		p.DailyBudget = &b
	}
	if d := project.SiteDomain(req.Site); p.Inbox == nil && d != "" {
		p.Inbox = &project.Inbox{AllowFrom: []string{d}}
	}
	budget := req.DailyBudget
	lead := project.Persona{Name: project.FirstRunLead, Identity: map[string]string{}, Lead: &project.Lead{
		Brief: project.FirstRunBrief(stories, req.Personas), SeeRuns: true, CreatePersonas: true, StartRuns: true,
		DailyBudget: &budget, DailyRuns: min(req.Personas*len(stories), project.MaxFirstRunRuns), MaxChildren: req.Personas}}
	if a, err := mail.ParseAddress(mailbox); err == nil && a.Address == mailbox {
		lead.Identity["email"] = mailbox
	}
	p.Roles = []project.Role{{Name: project.FirstRunRole, Description: "Writes the first personas from your stories", Personas: []project.Persona{lead}}}
	// One validation path: the same rules as a saved project file.
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	p, err = project.LoadJSON(b)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return s.SaveProject(ctx, p)
}

// checkSetup checks the request's limits and returns the trimmed stories.
func checkSetup(req SetupRequest) ([]string, error) {
	var stories []string
	for _, st := range req.Stories {
		st = strings.TrimSpace(st)
		if st == "" {
			return nil, fmt.Errorf("%w: a story is empty", ErrInvalid)
		}
		if len([]rune(st)) > project.MaxFirstRunStory {
			return nil, fmt.Errorf("%w: a story is longer than %d characters", ErrInvalid, project.MaxFirstRunStory)
		}
		stories = append(stories, st)
	}
	switch {
	case len(stories) == 0 || len(stories) > project.MaxFirstRunStories:
		return nil, fmt.Errorf("%w: give 1 to %d stories", ErrInvalid, project.MaxFirstRunStories)
	case req.Personas < 1 || req.Personas > project.MaxFirstRunPersonas:
		return nil, fmt.Errorf("%w: personas must be 1 to %d", ErrInvalid, project.MaxFirstRunPersonas)
	case !(req.DailyBudget > 0) || req.DailyBudget > project.MaxDailyBudget:
		return nil, fmt.Errorf("%w: money per day must be more than 0 and at most 1000 (US dollars)", ErrInvalid)
	}
	return stories, nil
}
