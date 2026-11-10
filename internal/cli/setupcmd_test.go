package cli

import "testing"

func TestTagFromBuild(t *testing.T) {
	cases := []struct {
		name, version, rev string
		modified           bool
		tag, note          string
	}{
		{"release", "v1.2.3", "abcdef1234567", false, "1.2.3", ""},
		{"release ignores dirty", "v1.2.3", "abcdef1234567", true, "1.2.3", ""},
		{"clean dev", "(devel)", "abcdef1234567890", false, "sha-abcdef1", ""},
		{"modified dev", "(devel)", "abcdef1234567890", true, "main", devBuildNote},
		{"no revision", "(devel)", "", false, "main", devBuildNote},
	}
	for _, c := range cases {
		tag, note := tagFromBuild(c.version, c.rev, c.modified)
		if tag != c.tag || note != c.note {
			t.Errorf("%s: got %q %q, want %q %q", c.name, tag, note, c.tag, c.note)
		}
	}
}
