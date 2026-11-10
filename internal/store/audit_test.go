package store

import (
	"testing"
	"time"
)

func TestAuditNewestFirst(t *testing.T) {
	s := open(t)
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	for i, a := range []string{"project.save", "run.start", "lead.wake"} {
		if err := s.Audit("ann@x.io", a, "s"+a, at.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.AuditLog(2)
	if err != nil || len(got) != 2 || got[0].Action != "lead.wake" || got[1].Action != "run.start" ||
		got[0].Actor != "ann@x.io" || got[0].Subject != "slead.wake" || !got[0].At.Equal(at.Add(2*time.Minute)) {
		t.Fatalf("%+v %v", got, err)
	}
}
