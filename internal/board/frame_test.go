package board

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestFrameAncestors(t *testing.T) {
	nine := strings.TrimSpace(strings.Repeat("https://a.io ", 9))
	for raw, want := range map[string]string{
		"":                               "'none'",
		"'none'":                         "'none'",
		"https://cloud.boardingparty.io": "https://cloud.boardingparty.io",
		"https://a.io https://b.io:8443": "https://a.io https://b.io:8443",
		"'none'; script-src *":           "'none'",
		"http://a.io":                    "'none'",
		"https://a.io\nX: y":             "'none'",
		"https://a.io; script-src *":     "'none'",
		"https://user@a.io":              "'none'",
		nine:                             "'none'",
	} {
		if got := FrameAncestors(raw); got != want {
			t.Errorf("FrameAncestors(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestUIHeadersFrame(t *testing.T) {
	h := http.Header{}
	UIHeaders(h, "'none'")
	if !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") || h.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("none: %v", h)
	}
	h = http.Header{}
	UIHeaders(h, "https://c.io")
	if !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors https://c.io") || h.Get("X-Frame-Options") != "" ||
		strings.Contains(h.Get("Content-Security-Policy"), "'none'") {
		t.Fatalf("framed: %v", h)
	}
}

func TestTraceHeaders(t *testing.T) {
	h := http.Header{}
	PageHeaders(h, "/trace/sw.bundle.js", "'none'")
	csp := h.Get("Content-Security-Policy")
	for _, want := range []string{"worker-src 'self'", "img-src 'self' data: blob:", "frame-ancestors 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("trace CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "'none'") || h.Get("X-Frame-Options") != "" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("trace headers: %v", h)
	}
	h = http.Header{}
	PageHeaders(h, "/trace/index.html", "https://c.io")
	if !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'self' https://c.io") {
		t.Fatalf("framed trace CSP = %q", h.Get("Content-Security-Policy"))
	}
	// Everything else keeps the board's own policy.
	for _, p := range []string{"/", "/app.js", "/tracer", "/x/trace/a"} {
		h = http.Header{}
		PageHeaders(h, p, "'none'")
		if h.Get("Content-Security-Policy") != strings.Replace(CSP, "frame-ancestors 'none'", "frame-ancestors 'none'", 1) || h.Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: %v", p, h)
		}
	}
}

func TestTraceViewerMatchesWorker(t *testing.T) {
	v, err := os.ReadFile("ui/trace/VERSION")
	if err != nil {
		t.Fatal(err)
	}
	df, err := os.ReadFile("../../worker/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^ARG PLAYWRIGHT_MCP_VERSION=(\S+)$`).FindSubmatch(df)
	if m == nil {
		t.Fatal("worker/Dockerfile has no PLAYWRIGHT_MCP_VERSION")
	}
	first, _, _ := strings.Cut(string(v), "\n")
	if first != "@playwright/mcp "+string(m[1]) {
		t.Fatalf("vendored trace viewer is for %q, the worker runs @playwright/mcp %s: run scripts/vendor-traceviewer.sh", first, m[1])
	}
}
