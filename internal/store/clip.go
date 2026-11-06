package store

import (
	"strings"
	"unicode"
)

// Clip makes outcome text fit a run context: control characters other than
// newline and tab become spaces and it is cut to max runes.
func Clip(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return s
}
