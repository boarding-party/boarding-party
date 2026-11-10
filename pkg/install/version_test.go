package install

import (
	"testing"
	"time"
)

func TestInstalledVersion(t *testing.T) {
	d := directorAt0("sha-abc1234")
	d.Annotations = map[string]string{CLIBuiltAnnotation: "2026-11-10T09:00:00Z"}
	tag, built := InstalledVersion(d)
	if tag != "sha-abc1234" || !built.Equal(time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("%q %v", tag, built)
	}
	d.Annotations = nil
	if _, built := InstalledVersion(d); !built.IsZero() {
		t.Fatalf("no annotation: %v", built)
	}
}
