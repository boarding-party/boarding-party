package board

import (
	"net/http"
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
