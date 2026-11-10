package board

import (
	"net/http"
	"regexp"
	"strings"
)

// maxFrameOrigins caps how many origins may frame the board.
const maxFrameOrigins = 8

var frameOrigin = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?$`)

// FrameAncestors turns the bp-project setting into a CSP frame-ancestors
// source list: one to eight space-separated https origins, or 'none' for
// anything else, so a bad value can never change the rest of the policy.
func FrameAncestors(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "'none'" {
		return "'none'"
	}
	origins := strings.Split(raw, " ")
	if len(origins) > maxFrameOrigins {
		return "'none'"
	}
	for _, o := range origins {
		if !frameOrigin.MatchString(o) {
			return "'none'"
		}
	}
	return raw
}

// UIHeaders sets the headers the director's board pages carry: the board's
// CSP with the given frame-ancestors, and X-Frame-Options only when nobody
// may frame it.
func UIHeaders(h http.Header, frameAncestors string) {
	SecurityHeaders(h)
	h.Set("Content-Security-Policy", strings.Replace(CSP, "frame-ancestors 'none'", "frame-ancestors "+frameAncestors, 1))
	if frameAncestors != "'none'" {
		h.Del("X-Frame-Options")
	}
	h.Set("Cache-Control", "no-cache")
}
