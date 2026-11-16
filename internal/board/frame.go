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

// TracePrefix is where the board serves Playwright's trace viewer.
const TracePrefix = "/trace/"

// traceCSP lets the trace viewer run: its service worker, and the blob: and
// data: images it builds. Its snapshots are drawn by that worker, which gives
// each one its own script-nonce policy.
const traceCSP = "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; " +
	"style-src 'self' 'unsafe-inline'; font-src 'self' data:; worker-src 'self'; connect-src 'self'; " +
	"frame-ancestors 'self'"

// TraceHeaders sets the headers of the trace viewer's files: the security
// headers, the viewer's CSP, which only the board itself may frame (even
// when the cloud frames the board: the viewer's snapshots are drawn by its
// service worker, not served from here), and no X-Frame-Options. COOP cuts
// a cross-origin opener's handle: the viewer loads any trace posted to it.
// frameAncestors is unused and kept for symmetry with UIHeaders.
func TraceHeaders(h http.Header, frameAncestors string) {
	SecurityHeaders(h)
	h.Set("Content-Security-Policy", traceCSP)
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Del("X-Frame-Options")
	h.Set("Cache-Control", "no-cache")
}

// PageHeaders sets the headers of a board page or file at urlPath: the trace
// viewer's under TracePrefix, the board's everywhere else.
func PageHeaders(h http.Header, urlPath, frameAncestors string) {
	if strings.HasPrefix(urlPath, TracePrefix) {
		TraceHeaders(h, frameAncestors)
		return
	}
	UIHeaders(h, frameAncestors)
}
