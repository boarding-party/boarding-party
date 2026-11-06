package activity

import (
	"math/rand/v2"
	"strings"
	"testing"
	"time"
)

var london, _ = time.LoadLocation("Europe/London")

func half() float64 { return 0.5 }
func at(s string) time.Time {
	t, _ := time.ParseInLocation("2006-01-02 15:04", s, london)
	return t
}

func TestParseRejects(t *testing.T) {
	for _, s := range []string{"sometimes", "every 5 min", "every 25 h", "daily at 25:00",
		"0 times a day", "5–2 times a day", "every 30 min, 18:00–09:00", "daily at 24:00", "49 times a day"} {
		if _, err := Parse(s); err == nil || !strings.Contains(err.Error(), `use "daily at 09:00"`) {
			t.Errorf("Parse(%q) err = %v", s, err)
		}
	}
}

func TestParseCanonical(t *testing.T) {
	for in, want := range map[string]string{
		"":                               "",
		"Daily at 9:00":                  "daily at 09:00",
		"daily at 09:00, 17:30":          "daily at 09:00, 17:30",
		"every 30 mins, 09:00-17:30":     "every 30 min, 09:00–17:30",
		"every 2 hours":                  "every 2 h",
		"2-4 times a day, 07:00 - 23:00": "2–4 times a day, 07:00–23:00",
		"3 times a day":                  "3 times a day, 08:00–20:00",
	} {
		p, err := Parse(in)
		if err != nil || p.String() != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, p.String(), err, want)
		}
	}
}

func TestZero(t *testing.T) {
	p, err := Parse("")
	if err != nil || !p.IsZero() {
		t.Fatal(p, err)
	}
	p, _ = Parse("daily at 09:00")
	if p.IsZero() {
		t.Fatal("not zero")
	}
}

func TestNextDaily(t *testing.T) {
	p, _ := Parse("daily at 09:00, 17:30")
	if got := p.Next(at("2026-11-05 08:00"), london, half); !got.Equal(at("2026-11-05 09:00")) {
		t.Fatal(got)
	}
	if got := p.Next(at("2026-11-05 09:00"), london, half); !got.Equal(at("2026-11-05 17:30")) {
		t.Fatal(got)
	}
	if got := p.Next(at("2026-11-05 18:00"), london, half); !got.Equal(at("2026-11-06 09:00")) {
		t.Fatal(got)
	}
	early := p.Next(at("2026-11-05 08:00"), london, func() float64 { return 0 })
	if !early.Equal(at("2026-11-05 08:50")) {
		t.Fatal(early)
	}
}

func TestNextDailyAcrossDST(t *testing.T) {
	p, _ := Parse("daily at 09:00")
	got := p.Next(at("2026-10-24 10:00"), london, half) // clocks go back 25 Oct
	if got.In(london).Format("2006-01-02 15:04") != "2026-10-25 09:00" {
		t.Fatal(got)
	}
}

func TestNextEveryStaysInWindow(t *testing.T) {
	p, _ := Parse("every 30 min, 09:00–17:30")
	if got := p.Next(at("2026-11-05 10:00"), london, half); !got.Equal(at("2026-11-05 10:30")) {
		t.Fatal(got)
	}
	if got := p.Next(at("2026-11-05 17:20"), london, half); !got.Equal(at("2026-11-06 09:03")) {
		t.Fatal(got) // 09:00 + 0.5×6 min
	}
	if got := p.Next(at("2026-11-05 06:00"), london, half); !got.Equal(at("2026-11-05 09:03")) {
		t.Fatal(got)
	}
}

func TestNextTimesADay(t *testing.T) {
	p, _ := Parse("2–4 times a day, 08:00–20:00") // gap 4 h
	if got := p.Next(at("2026-11-05 09:00"), london, half); !got.Equal(at("2026-11-05 13:00")) {
		t.Fatal(got)
	}
	if got := p.Next(at("2026-11-05 19:00"), london, half); !got.Equal(at("2026-11-06 10:00")) {
		t.Fatal(got)
	}
	if got := p.Next(at("2026-11-05 05:00"), london, half); !got.Equal(at("2026-11-05 10:00")) {
		t.Fatal(got)
	}
}

func TestNextAlwaysAfter(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for _, s := range []string{"daily at 09:00", "every 15 min", "every 1 h, 09:00–10:00", "48 times a day, 00:00–24:00"} {
		p, _ := Parse(s)
		after := at("2026-11-05 09:55")
		for i := 0; i < 1000; i++ {
			if got := p.Next(after, london, r.Float64); !got.After(after) {
				t.Fatalf("%s: %v not after %v", s, got, after)
			}
		}
	}
}
