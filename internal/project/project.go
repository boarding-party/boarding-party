// Package project defines the boardingparty project config.
package project

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"

	"sigs.k8s.io/yaml"

	"github.com/boarding-party/boardingparty/internal/activity"
)

type Project struct {
	Name       string   `json:"name"`
	Sites      []string `json:"sites"` // staging URLs; hosts become the allow-list
	ExtraHosts []string `json:"extraHosts"`
	Model      string   `json:"model"` // optional override
	Roles      []Role   `json:"roles"`
	// Resources sizes each run pod's agent container. Optional; see
	// Project.RunResources for the defaults and bounds.
	Resources *Resources `json:"resources,omitempty"`
	// NodePool pins run pods to a dedicated node pool, named by one node
	// label as "key=value". The pool carries the same key=value as a
	// NoSchedule taint, which runs tolerate. Optional; empty schedules runs
	// anywhere.
	NodePool string `json:"nodePool,omitempty"`
	// Inbox configures inbound persona email. Optional; without it every
	// received message is filtered.
	Inbox *Inbox `json:"inbox,omitempty"`
	// Timezone is the IANA zone that role schedules are read in. Optional;
	// empty means DefaultTimezone.
	Timezone string `json:"timezone,omitempty"`
	// DailyBudget caps a day's spend, in US dollars. Optional; nil means
	// DefaultDailyBudget.
	DailyBudget *float64 `json:"dailyBudget,omitempty"`
}

// Time zone and budget defaults and bounds.
const (
	DefaultTimezone    = "Europe/London"
	DefaultDailyBudget = 5.0
	MaxDailyBudget     = 1000.0
)

// Location returns the project's time zone, DefaultTimezone when unset.
func (p Project) Location() *time.Location {
	name := p.Timezone
	if name == "" {
		name = DefaultTimezone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC // unreachable for a validated project
	}
	return loc
}

// Budget returns the daily budget in US dollars, DefaultDailyBudget when unset.
func (p Project) Budget() float64 {
	if p.DailyBudget == nil {
		return DefaultDailyBudget
	}
	return *p.DailyBudget
}

// Inbox is the project's inbound email policy.
type Inbox struct {
	// AllowFrom lists senders whose mail reaches personas: a bare domain
	// (matches it and its subdomains) or a full address. Lower-cased on load.
	AllowFrom []string `json:"allowFrom"`
	// AuthServID is the authserv-id of the Authentication-Results header whose verdict is
	// trusted: the one your mail provider adds. Empty means mx.google.com.
	AuthServID string `json:"authServId,omitempty"`
}

var hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// normalise lower-cases and validates inbox.allowFrom.
func (in *Inbox) normalise() error {
	in.AuthServID = strings.ToLower(strings.TrimSpace(in.AuthServID))
	if in.AuthServID != "" && !hostRe.MatchString(in.AuthServID) {
		return fmt.Errorf("inbox.authServId %q is not a valid hostname", in.AuthServID)
	}
	for i, e := range in.AllowFrom {
		e = strings.ToLower(strings.TrimSpace(e))
		in.AllowFrom[i] = e
		if strings.Contains(e, "@") {
			a, err := mail.ParseAddress(e)
			local, domain, _ := strings.Cut(e, "@")
			if err != nil || a.Address != e || local == "" || !hostRe.MatchString(domain) {
				return fmt.Errorf("inbox.allowFrom %q is not a valid email address", e)
			}
		} else if !hostRe.MatchString(e) {
			return fmt.Errorf("inbox.allowFrom %q is not a valid domain", e)
		}
	}
	return nil
}

// Pool splits NodePool into its label key and value; ok is false when unset.
func (p Project) Pool() (key, value string, ok bool) {
	key, value, ok = strings.Cut(p.NodePool, "=")
	return key, value, ok && key != ""
}

// Resources is the run pod's CPU and memory, as Kubernetes quantities.
type Resources struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// Run pod resource defaults and bounds. A run pod is never unbounded.
const (
	DefaultCPU    = "1"
	DefaultMemory = "3Gi"
	minCPU        = "250m"
	maxCPU        = "1"
	minMemory     = "1Gi"
	maxMemory     = "8Gi"
)

// RunResources returns the CPU and memory for a run pod: the project's values,
// with the defaults filling anything unset.
func (p Project) RunResources() Resources {
	r := Resources{CPU: DefaultCPU, Memory: DefaultMemory}
	if p.Resources != nil {
		if p.Resources.CPU != "" {
			r.CPU = p.Resources.CPU
		}
		if p.Resources.Memory != "" {
			r.Memory = p.Resources.Memory
		}
	}
	return r
}

func checkQuantity(field, v, lo, hi string) error {
	q, err := resource.ParseQuantity(v)
	if err != nil {
		return fmt.Errorf("resources.%s %q is not a valid quantity: %w", field, v, err)
	}
	if q.Cmp(resource.MustParse(lo)) < 0 || q.Cmp(resource.MustParse(hi)) > 0 {
		return fmt.Errorf("resources.%s %q must be between %s and %s", field, v, lo, hi)
	}
	return nil
}

type Role struct {
	Name               string    `json:"name"`
	Description        string    `json:"description"`
	CanWatchRecordings bool      `json:"canWatchRecordings"`
	Personas           []Persona `json:"personas"` // explicit in Plan 1; generated in Plan 2
	Stories            []Story   `json:"stories"`
	// Activity is when the role's personas are active, such as
	// "every 30 min, 09:00–17:30". Stored in canonical form. Optional.
	Activity string `json:"activity,omitempty"`
}

// Pattern returns the parsed Activity; validation guarantees it parses.
func (r Role) Pattern() activity.Pattern {
	p, _ := activity.Parse(r.Activity)
	return p
}

type Persona struct {
	Name     string            `json:"name"`
	Traits   []string          `json:"traits"`   // e.g. "impatient", "phone"
	Identity map[string]string `json:"identity"` // synthetic: email, password, address...
	// Profile is the persona's structured background (device, age, ...).
	Profile Profile `json:"profile,omitzero"`
}

type Story struct {
	Name string `json:"name"`
	Goal string `json:"goal"`
	// Model runs this story on a different model than the project's, such
	// as a cheap one for a routine story. Empty uses the project's.
	Model string `json:"model,omitempty"`
	// Handoffs name the stories that follow this one, each in another role.
	Handoffs []Handoff `json:"handoffs,omitempty"`
}

// Handoff points at a story of another role that this story passes work to.
type Handoff struct {
	Role  string `json:"role"`
	Story string `json:"story"`
}

// modelID is the shape of a Claude model id, such as claude-haiku-5-5.
var modelID = regexp.MustCompile(`^[a-z0-9][a-z0-9.@_-]{0,99}$`)

// Load parses and validates a project from YAML.
func Load(b []byte) (Project, error) {
	var p Project
	if err := yaml.UnmarshalStrict(b, &p); err != nil {
		return Project{}, fmt.Errorf("parse project: %w", err)
	}
	if err := p.validate(); err != nil {
		return Project{}, err
	}
	return p, nil
}

// LoadJSON parses and validates a project from JSON, with the same rules as Load.
func LoadJSON(b []byte) (Project, error) {
	var p Project
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Project{}, fmt.Errorf("parse project: %w", err)
	}
	if dec.More() {
		return Project{}, fmt.Errorf("parse project: trailing data")
	}
	if err := p.validate(); err != nil {
		return Project{}, err
	}
	return p, nil
}

func (p Project) validate() error {
	if len(p.Sites) == 0 {
		return fmt.Errorf("project needs at least one site")
	}
	for _, s := range p.Sites {
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("site %q must be an https:// URL", s)
		}
		h := strings.ToLower(u.Hostname())
		if len(validation.IsDNS1123Subdomain(h)) > 0 {
			return fmt.Errorf("site %q must use a host name such as staging.example.com", s)
		}
		if why := publicHostProblem(h); why != "" {
			return fmt.Errorf("site %q %s", s, why)
		}
	}
	for i, h := range p.ExtraHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if len(validation.IsDNS1123Subdomain(h)) > 0 {
			return fmt.Errorf("extra host %q must be a host name such as fonts.gstatic.com", p.ExtraHosts[i])
		}
		if why := publicHostProblem(h); why != "" {
			return fmt.Errorf("extra host %q %s", p.ExtraHosts[i], why)
		}
		p.ExtraHosts[i] = h
	}
	if r := p.Resources; r != nil {
		if r.CPU != "" {
			if err := checkQuantity("cpu", r.CPU, minCPU, maxCPU); err != nil {
				return err
			}
		}
		if r.Memory != "" {
			if err := checkQuantity("memory", r.Memory, minMemory, maxMemory); err != nil {
				return err
			}
		}
	}
	if p.Inbox != nil {
		if err := p.Inbox.normalise(); err != nil {
			return err
		}
	}
	if p.Timezone != "" {
		if _, err := time.LoadLocation(p.Timezone); err != nil {
			return fmt.Errorf("timezone %q is not a time zone, such as Europe/London", p.Timezone)
		}
	}
	if b := p.DailyBudget; b != nil && (!(*b > 0) || *b > MaxDailyBudget) {
		return fmt.Errorf("dailyBudget must be more than 0 and at most 1000 (US dollars)")
	}
	if p.NodePool != "" {
		k, v, ok := p.Pool()
		if !ok || len(validation.IsQualifiedName(k)) > 0 || v == "" || len(validation.IsValidLabelValue(v)) > 0 {
			return fmt.Errorf("nodePool %q must be a node label as key=value, such as boardingparty.io/run=true", p.NodePool)
		}
	}
	seen := map[string]bool{}
	personas := map[string]bool{} // runs and persona state are keyed by persona name
	for i := range p.Roles {
		r := &p.Roles[i]
		if r.Name == "" {
			return fmt.Errorf("role needs a name")
		}
		if seen[r.Name] {
			return fmt.Errorf("duplicate role %q", r.Name)
		}
		seen[r.Name] = true
		pat, err := activity.Parse(r.Activity)
		if err != nil {
			return fmt.Errorf("role %q: %w", r.Name, err)
		}
		r.Activity = pat.String()
		for _, pe := range r.Personas {
			if pe.Name == "" {
				return fmt.Errorf("role %q: persona needs a name", r.Name)
			}
			if personas[pe.Name] {
				return fmt.Errorf("duplicate persona %q", pe.Name)
			}
			personas[pe.Name] = true
			if err := pe.Profile.validate(); err != nil {
				return fmt.Errorf("persona %q: %w", pe.Name, err)
			}
		}
		for _, st := range r.Stories {
			if st.Goal == "" {
				return fmt.Errorf("role %q: story %q needs a goal", r.Name, st.Name)
			}
			if st.Model != "" && !modelID.MatchString(st.Model) {
				return fmt.Errorf("role %q: story %q: %q is not a model id, such as claude-haiku-5-5", r.Name, st.Name, st.Model)
			}
		}
	}
	// Handoffs need every role, so they are checked after the role loop.
	for _, r := range p.Roles {
		for _, st := range r.Stories {
			if err := p.checkHandoffs(r.Name, st); err != nil {
				return fmt.Errorf("role %q: story %q: %w", r.Name, st.Name, err)
			}
		}
	}
	return nil
}

func (p Project) checkHandoffs(role string, st Story) error {
	seen := map[Handoff]bool{}
	for _, h := range st.Handoffs {
		if h.Role == "" || h.Story == "" {
			return fmt.Errorf("handoff needs a role and a story")
		}
		target, ok := p.Role(h.Role)
		if !ok {
			return fmt.Errorf("hands off to unknown role %q", h.Role)
		}
		if h.Role == role {
			return fmt.Errorf("hands off to its own role")
		}
		if _, ok := p.FindStory(h.Role, h.Story); !ok {
			return fmt.Errorf("hands off to unknown story %q of role %q", h.Story, target.Name)
		}
		if seen[h] {
			return fmt.Errorf("hands off to %s/%s twice", h.Role, h.Story)
		}
		seen[h] = true
	}
	return nil
}

// Role returns the role with the given name.
func (p Project) Role(name string) (Role, bool) {
	for _, r := range p.Roles {
		if r.Name == name {
			return r, true
		}
	}
	return Role{}, false
}

// FindStory returns the named story of the named role.
func (p Project) FindStory(role, story string) (Story, bool) {
	if r, ok := p.Role(role); ok {
		for _, st := range r.Stories {
			if st.Name == story {
				return st, true
			}
		}
	}
	return Story{}, false
}

// StoryModel returns the model the story asks for, or "" for the project's.
func (p Project) StoryModel(role, story string) string {
	st, _ := p.FindStory(role, story)
	return st.Model
}

// Hosts returns the hosts runs may reach: each site's host and the extra
// hosts, lower-case, sorted and without duplicates.
func (p Project) Hosts() []string {
	var hosts []string
	for _, s := range p.Sites {
		if u, err := url.Parse(s); err == nil && u.Hostname() != "" {
			hosts = append(hosts, strings.ToLower(u.Hostname()))
		}
	}
	for _, h := range p.ExtraHosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			hosts = append(hosts, h)
		}
	}
	slices.Sort(hosts)
	return slices.Compact(hosts)
}

// internalSuffixes are name endings that resolve inside a cluster, a cloud
// project or a local network, never to a public staging site.
var internalSuffixes = []string{".internal", ".local", ".localhost", ".localdomain", ".svc", ".cluster.local", ".home.arpa", ".arpa"}

// publicHostProblem says why a lower-case host can't go on the egress
// allow-list, or "" when it can. The proxy matches host names as written, so
// an IP address or an internal name there would open the cluster's own
// network (or the cloud metadata server) to the agents.
func publicHostProblem(h string) string {
	// A leading dot means "every subdomain" to the proxy; empty labels and a
	// trailing dot are spellings the checks below would misread.
	if strings.HasPrefix(h, ".") || strings.HasSuffix(h, ".") || strings.Contains(h, "..") {
		return "must be a plain host name such as fonts.gstatic.com"
	}
	if net.ParseIP(strings.Trim(h, "[]")) != nil || allDigitsLastLabel(h) {
		return "is an IP address; use the site's host name"
	}
	if !strings.Contains(h, ".") {
		return "must be a full host name such as fonts.gstatic.com"
	}
	if embedsIP(h) {
		return "points at an IP address through a wildcard DNS name; use the site's host name"
	}
	for _, sfx := range internalSuffixes {
		if strings.HasSuffix(h, sfx) || h == sfx[1:] {
			return "is an internal name; runs may reach only public hosts"
		}
	}
	if suffix, _ := publicsuffix.PublicSuffix(h); suffix == h {
		return "is a whole domain ending; name the host, such as fonts.gstatic.com"
	}
	return ""
}

// wildcardDNS are services whose names resolve to whatever address the name
// spells, such as 10.0.0.1.nip.io.
var wildcardDNS = []string{"nip.io", "sslip.io", "xip.io", "traefik.me", "localtest.me", "lvh.me", "local.gd", "vcap.me", "lacolhost.com"}

// ipInName matches an IPv4 address written with dots or dashes inside a name.
var ipInName = regexp.MustCompile(`(^|[.-])\d{1,3}[.-]\d{1,3}[.-]\d{1,3}[.-]\d{1,3}([.-]|$)`)

// embedsIP reports names that carry their own address: wildcard DNS services
// and names spelling an IPv4 address. It can't catch every name an attacker
// points at a private address; tiny's proxy is the place for that.
func embedsIP(h string) bool {
	for _, d := range wildcardDNS {
		if h == d || strings.HasSuffix(h, "."+d) {
			return true
		}
	}
	return ipInName.MatchString(h)
}

// allDigitsLastLabel catches IPv4 forms net.ParseIP rejects (such as
// "10.1" or "0x7f.1"): no public top-level domain is numeric or hex.
func allDigitsLastLabel(h string) bool {
	last := h[strings.LastIndexByte(h, '.')+1:]
	if last == "" {
		return false
	}
	if strings.HasPrefix(last, "0x") {
		return true
	}
	for _, c := range last {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
