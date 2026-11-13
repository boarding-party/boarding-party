package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRedeemClaimOnceWhileActive(t *testing.T) {
	s := open(t)
	r := Run{ID: "r1", Token: "t1", State: RunRunning, Started: time.Now(), Chain: "r1", Kind: RunReplay, Claim: "c1"}
	if err := s.CreateRun(r); err != nil {
		t.Fatal(err)
	}
	got, err := s.RedeemClaim("c1")
	if err != nil || got.ID != "r1" || got.Token != "t1" {
		t.Fatalf("first redeem = %+v, %v", got, err)
	}
	if _, err := s.RedeemClaim("c1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second redeem err = %v, want ErrNotFound", err)
	}
	if _, err := s.RedeemClaim(""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty claim err = %v, want ErrNotFound", err)
	}
}

func TestRedeemClaimRefusesFinishedRun(t *testing.T) {
	s := open(t)
	if err := s.CreateRun(Run{ID: "r2", Token: "t2", State: RunFailed, Started: time.Now(), Chain: "r2", Kind: RunReplay, Claim: "c2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemClaim("c2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("finished run claim err = %v, want ErrNotFound", err)
	}
}

func TestClaimNeverInRunJSON(t *testing.T) {
	b, _ := json.Marshal(Run{ID: "r3", Claim: "secret-claim"})
	if strings.Contains(string(b), "secret-claim") {
		t.Fatalf("run JSON carries the claim: %s", b)
	}
}
