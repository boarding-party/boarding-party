package project

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultExplore is how often a story gets a fresh AI run when its role
// sets no explore interval.
const DefaultExplore = 7 * 24 * time.Hour

var exploreEvery = regexp.MustCompile(`^every (\d+) (days?|h|hours?)$`)

// ParseExplore parses a role's explore interval: "daily", "weekly",
// "every N days" (1–90) or "every N h" (1–2160). Empty is weekly.
func ParseExplore(s string) (time.Duration, error) {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	switch s {
	case "":
		return DefaultExplore, nil
	case "daily":
		return 24 * time.Hour, nil
	case "weekly":
		return DefaultExplore, nil
	}
	m := exploreEvery.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("explore %q: use daily, weekly, every N days or every N h", s)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("explore %q: %w", s, err)
	}
	if strings.HasPrefix(m[2], "day") {
		if n < 1 || n > 90 {
			return 0, fmt.Errorf("explore %q: days must be 1 to 90", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	if n < 1 || n > 2160 {
		return 0, fmt.Errorf("explore %q: hours must be 1 to 2160", s)
	}
	return time.Duration(n) * time.Hour, nil
}

// exploreText is the canonical text of an explore interval.
func exploreText(d time.Duration) string {
	if d%(24*time.Hour) != 0 {
		return fmt.Sprintf("every %d h", d/time.Hour)
	}
	switch days := d / (24 * time.Hour); days {
	case 1:
		return "daily"
	case 7:
		return "weekly"
	default:
		return fmt.Sprintf("every %d days", days)
	}
}

// ExploreEvery returns the role's explore interval; validation guarantees
// it parses.
func (r Role) ExploreEvery() time.Duration {
	d, err := ParseExplore(r.Explore)
	if err != nil {
		return DefaultExplore
	}
	return d
}

// Replays reports whether scheduled runs of the story replay its journey
// once it has one. Unset is yes.
func (s Story) Replays() bool { return s.Replay == nil || *s.Replay }
