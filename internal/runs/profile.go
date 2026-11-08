package runs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// MaxProfileBytes caps a saved browser profile (Playwright storage state).
const MaxProfileBytes = 1 << 20

// ErrBadProfile means a browser profile is not a Playwright storage state.
var ErrBadProfile = errors.New("not a browser storage state: want a JSON object with cookies and origins arrays, at most 1 MiB")

// How a run's browser starts.
const (
	ProfileNone     = "none"     // the persona has no saved profile yet
	ProfileRestored = "restored" // the persona's saved profile is loaded
	ProfileFresh    = "fresh"    // the story asks for an empty browser
)

// Profile returns the browser profile the run's browser starts with, nil
// when it starts empty, and which of the three cases applies.
func (s *Service) Profile(token string) (json.RawMessage, string, error) {
	r, err := s.Active(token)
	if err != nil {
		return nil, "", err
	}
	p, err := s.Store.Project()
	if err != nil {
		return nil, "", fmt.Errorf("load project: %w", err)
	}
	if st, ok := p.FindStory(r.Role, r.Story); ok && st.FreshBrowser {
		return nil, ProfileFresh, nil
	}
	state, _, err := s.Store.PersonaProfile(r.Persona)
	if err != nil {
		return nil, "", err
	}
	if state == nil {
		return nil, ProfileNone, nil
	}
	return state, ProfileRestored, nil
}

// SaveProfile keeps the run's browser storage state for the persona's next run.
func (s *Service) SaveProfile(token string, state []byte, now time.Time) error {
	r, err := s.Active(token)
	if err != nil {
		return err
	}
	if len(state) > MaxProfileBytes {
		return ErrBadProfile
	}
	var v struct {
		Cookies []json.RawMessage `json:"cookies"`
		Origins []json.RawMessage `json:"origins"`
	}
	dec := json.NewDecoder(bytes.NewReader(state))
	if err := dec.Decode(&v); err != nil || v.Cookies == nil || v.Origins == nil || dec.More() {
		return ErrBadProfile
	}
	return s.Store.SavePersonaProfile(r.Persona, json.RawMessage(state), now)
}
