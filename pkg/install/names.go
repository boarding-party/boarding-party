// Package install puts the boardingparty director into a namespace and
// reaches it again later: preflight checks, server-side apply, the admin
// Secret and a port-forward to the admin listener. The CLI and the cloud
// both use it; everything is found by fixed names in the namespace.
package install

import (
	"errors"
	"strings"
	"unicode"
)

// Known names a director's namespace holds.
const (
	DirectorDeployment = directorDeploy
	AdminSecret        = adminSecret
	AgentSecret        = agentSecret
	// ProjectConfigMap holds settings the cloud owns (frameAncestors); install never writes it.
	ProjectConfigMap       = "bp-project"
	ManifestHashAnnotation = manifestHashAnnotation
	CLIBuiltAnnotation     = cliBuiltAnnotation
	ManagedByLabel         = "app.kubernetes.io/managed-by"
	ManagedByValue         = "boardingparty"
	DirectorSelector       = "app=bp-director"
	AdminPort              = 8081
	// DefaultImageTag is what a build that can't name its own commit installs.
	DefaultImageTag = defaultImageTag
)

const (
	managedByLabel   = ManagedByLabel
	managedByValue   = ManagedByValue
	directorSelector = DirectorSelector
	adminPort        = AdminPort
)

// ErrPreflight means the namespace isn't ready; the checks printed why.
var ErrPreflight = errors.New("preflight checks failed")

// Clean replaces control runes (C0, DEL, C1) except tab, and Unicode format
// characters (e.g. bidi overrides), with '?', so server-supplied text cannot
// inject terminal escape sequences or reorder what the operator reads.
func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		if (r != '\t' && unicode.IsControl(r)) || unicode.Is(unicode.Cf, r) {
			return '?'
		}
		return r
	}, s)
}

func clean(s string) string { return Clean(s) }
