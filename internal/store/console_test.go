package store

import (
	"strings"
	"testing"
	"time"
)

func TestConsoleAddAndCap(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	evs := []ConsoleEvent{
		{At: at, Level: "error", Kind: "exception", Text: "TypeError: x is undefined", URL: "https://a.test/app.js"},
		{At: at.Add(time.Second), Level: "warning", Kind: "console", Text: strings.Repeat("w", 3000), URL: "https://a.test/" + strings.Repeat("u", 5000)},
	}
	if err := s.AddConsole("r1", evs); err != nil {
		t.Fatal(err)
	}
	got, err := s.Console("r1")
	if err != nil || len(got) != 2 || got[0].Kind != "exception" || !got[0].At.Equal(at) || len(got[1].Text) != MaxConsoleText || len(got[1].URL) != MaxConsoleURL {
		t.Fatalf("%+v %v", got, err)
	}
	many := make([]ConsoleEvent, MaxConsoleEvents)
	for i := range many {
		many[i] = ConsoleEvent{At: at, Level: "error", Kind: "console", Text: "e"}
	}
	if err := s.AddConsole("r1", many); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Console("r1"); len(got) != MaxConsoleEvents {
		t.Fatalf("cap: %d", len(got))
	}
	if got, _ := s.Console("r2"); len(got) != 0 {
		t.Fatalf("other run: %d", len(got))
	}
}
