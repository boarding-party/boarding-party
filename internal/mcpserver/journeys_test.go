package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/boarding-party/boardingparty/internal/store"
)

// decided makes the run's journey decision so finish_run accepts it.
func decided(t *testing.T, cs *mcp.ClientSession) {
	t.Helper()
	if text, isErr := call(t, cs, "journey_decision", map[string]any{"kind": "not recorded", "reason": "test run"}); isErr {
		t.Fatalf("journey_decision: %s", text)
	}
}

const testJourney = "export default async function journey({ page, bp }) { await bp.step('Open', async () => {}); }"

func TestFinishRunNeedsJourneyDecision(t *testing.T) {
	_, st, srv, run := setup(t)
	cs := connect(t, srv, run.Token)
	text, isErr := call(t, cs, "finish_run", map[string]any{"outcome": "done", "summary": "s"})
	if !isErr || !strings.Contains(text, "save_journey") || !strings.Contains(text, "journey_decision") {
		t.Fatalf("undecided finish: %q %v", text, isErr)
	}
	if r, _ := st.RunByID(run.ID); r.State != store.RunRunning {
		t.Fatalf("run finished anyway: %s", r.State)
	}
	if text, isErr := call(t, cs, "journey_decision", map[string]any{"kind": "confirmed", "reason": "same"}); !isErr {
		t.Fatalf("confirmed with no journey: %s", text)
	}
	if text, isErr := call(t, cs, "journey_decision", map[string]any{"kind": "not recorded", "reason": "logged out mid-way"}); isErr {
		t.Fatal(text)
	}
	if text, isErr := call(t, cs, "finish_run", map[string]any{"outcome": "done", "summary": "s"}); isErr || text != "finished" {
		t.Fatalf("decided finish: %q", text)
	}
}

func TestSaveJourneyTool(t *testing.T) {
	_, st, srv, run := setup(t)
	cs := connect(t, srv, run.Token)
	unproven := map[string]any{"code": testJourney, "reason": "r", "kind": "full",
		"self_replay": map[string]any{"status": "failed", "step": 1, "stepName": "Open"}}
	if text, isErr := call(t, cs, "save_journey", unproven); !isErr || !strings.Contains(text, "does not prove") {
		t.Fatalf("unproven: %q", text)
	}
	ok := map[string]any{"code": testJourney, "reason": "first recording", "kind": "full",
		"self_replay": map[string]any{"status": "passed", "seed": 42, "durationMs": 900}}
	text, isErr := call(t, cs, "save_journey", ok)
	if isErr || !strings.Contains(text, "revision 1") {
		t.Fatalf("save: %q", text)
	}
	j, err := st.JourneyFor("ann", "checkout")
	if err != nil || j.LiveRev != 1 {
		t.Fatalf("journey %+v %v", j, err)
	}
	if text, isErr := call(t, cs, "finish_run", map[string]any{"outcome": "done", "summary": "s"}); isErr {
		t.Fatalf("finish after save: %s", text)
	}
}

func TestNextAssignmentCarriesLiveJourney(t *testing.T) {
	_, st, srv, run := setup(t)
	cs := connect(t, srv, run.Token)
	text, _ := call(t, cs, "next_assignment", nil)
	var a struct {
		Journey *struct {
			Revision int    `json:"revision"`
			Code     string `json:"code"`
		} `json:"journey"`
	}
	if err := json.Unmarshal([]byte(text), &a); err != nil || a.Journey != nil || !strings.Contains(text, `"journey":null`) {
		t.Fatalf("no journey yet: %s %v", text, err)
	}
	if _, _, err := st.AddRevision("ann", "checkout", store.Revision{Kind: store.RevisionFull, Code: testJourney, Reason: "r", RunID: "old"},
		store.Decision{Kind: "recorded", Reason: "r", Actor: store.ActorModel}); err != nil {
		t.Fatal(err)
	}
	text, _ = call(t, cs, "next_assignment", nil)
	if err := json.Unmarshal([]byte(text), &a); err != nil || a.Journey == nil || a.Journey.Revision != 1 || a.Journey.Code != testJourney {
		t.Fatalf("live journey: %s %v", text, err)
	}
}

// A replay runs untrusted journey code with its token in reach: the token
// opens none of the agent tools.
func TestReplayTokenOpensNoTools(t *testing.T) {
	_, st, srv, run := setup(t)
	if _, err := st.FinishRun(run.ID, store.RunDone, "x", time.Now()); err != nil {
		t.Fatal(err)
	}
	rp := store.Run{ID: "rp", Token: "tok-replay", Persona: "ann", Role: "buyer", Story: "checkout", State: store.RunRunning,
		Started: time.Now(), Chain: "rp", Kind: store.RunReplay}
	if err := st.CreateRun(rp); err != nil {
		t.Fatal(err)
	}
	cs := connect(t, srv, rp.Token)
	for name, args := range map[string]map[string]any{
		"next_assignment": nil, "persona_save": {"state": map[string]any{}},
		"save_journey":     {"code": testJourney, "reason": "r", "kind": "full", "self_replay": map[string]any{"status": "passed"}},
		"journey_decision": {"kind": "not recorded", "reason": "r"},
		"finish_run":       {"outcome": "done", "summary": "x"},
	} {
		if text, isErr := call(t, cs, name, args); !isErr || text != errNotActive {
			t.Errorf("%s with a replay token: %q", name, text)
		}
	}
}

// copilotRun ends the setup run and starts a copilot run for ann's
// checkout journey, which has one live revision.
func copilotRun(t *testing.T, st *store.Store, run store.Run) (store.Run, store.Journey) {
	t.Helper()
	if _, err := st.FinishRun(run.ID, store.RunDone, "x", time.Now()); err != nil {
		t.Fatal(err)
	}
	j, _, err := st.AddRevision("ann", "checkout", store.Revision{Kind: store.RevisionFull, Code: testJourney, Reason: "r", RunID: "old", Created: time.Now()},
		store.Decision{Kind: "recorded", Reason: "r", Actor: store.ActorModel, Created: time.Now(), Explored: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, _ := json.Marshal(store.RunContext{Kind: "replay_failed", PreviousRun: "rp", Previous: "Failed at step 1: Open",
		Replay: &store.ContextReplay{JourneyID: j.ID, Revision: 1, Step: 1, StepName: "Open", Error: "timeout", Seed: 9, Why: "failed again with the same inputs"}})
	cp := store.Run{ID: "cp", Token: "tok-copilot", Persona: "ann", Role: "buyer", Story: "checkout", State: store.RunRunning,
		Started: time.Now(), Chain: "rp", Via: "copilot", Follows: "rp", Context: ctx}
	if err := st.CreateRun(cp); err != nil {
		t.Fatal(err)
	}
	return cp, j
}

func TestCopilotRefusesSaveJourneyAndWaiting(t *testing.T) {
	_, st, srv, run := setup(t)
	cp, _ := copilotRun(t, st, run)
	cs := connect(t, srv, cp.Token)
	for name, args := range map[string]map[string]any{
		"save_journey":     {"code": testJourney, "reason": "r", "kind": "full", "self_replay": map[string]any{"status": "passed"}},
		"journey_decision": {"kind": "confirmed", "reason": "r"},
	} {
		if text, isErr := call(t, cs, name, args); !isErr || !strings.Contains(text, "copilot run: decide with decide_journey") {
			t.Errorf("%s on a copilot run: %q", name, text)
		}
	}
	text, isErr := call(t, cs, "finish_run", map[string]any{"outcome": "waiting", "summary": "s", "until": map[string]any{"time": map[string]any{"after": "1h"}}})
	if !isErr || !strings.Contains(text, "copilot") {
		t.Fatalf("waiting copilot: %q", text)
	}
	text, isErr = call(t, cs, "finish_run", map[string]any{"outcome": "done", "summary": "s"})
	if !isErr || !strings.Contains(text, "decide_journey") {
		t.Fatalf("undecided copilot finish: %q", text)
	}
}

func TestDecideJourneyTool(t *testing.T) {
	_, st, srv, run := setup(t)
	cp, j := copilotRun(t, st, run)
	cs := connect(t, srv, cp.Token)
	if text, isErr := call(t, cs, "decide_journey", map[string]any{"kind": "site changed", "reason": "Open moved",
		"code": testJourney, "self_replay": map[string]any{"status": "passed"}}); !isErr || !strings.Contains(text, "why_not_bug") {
		t.Fatalf("site changed without why: %q", text)
	}
	text, isErr := call(t, cs, "decide_journey", map[string]any{"kind": "flaky", "reason": "the page loads slowly",
		"evidence": []string{"trace.zip: 14 s to load"}, "code": testJourney, "self_replay": map[string]any{"status": "passed", "seed": 9}})
	if isErr || !strings.Contains(text, "revision 2") {
		t.Fatalf("flaky: %q", text)
	}
	got, _ := st.Journey(j.ID)
	if got.LiveRev != 2 {
		t.Fatalf("journey %+v", got)
	}
	if text, isErr := call(t, cs, "finish_run", map[string]any{"outcome": "done", "summary": "s"}); isErr {
		t.Fatalf("decided copilot finish: %q", text)
	}
}
