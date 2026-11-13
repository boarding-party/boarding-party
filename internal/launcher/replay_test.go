package launcher

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	agentsv1 "github.com/tiny-systems/tiny/api/v1alpha1"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

func replayRun() store.Run {
	return store.Run{ID: "ffffffff11", Token: "tok9", Persona: "ann", Role: "buyer", Story: "checkout",
		Kind: store.RunReplay, JourneyID: "j1", Revision: 2, Seed: 77, Claim: "claim5"}
}

func replayProject() project.Project {
	return project.Project{NodePool: "pool=runs", Roles: []project.Role{{Name: "buyer",
		Personas: []project.Persona{{Name: "ann", Profile: project.Profile{Device: "iphone-15", Assistive: "zoom", Vision: "protanopia", Display: "reduced-motion"}}},
		Stories:  []project.Story{{Name: "checkout", Goal: "g"}}}}}
}

func TestLauncherStartsReplayAsCommandSession(t *testing.T) {
	tn := newTiny()
	name, err := tn.Start(context.Background(), replayRun(), replayProject())
	if err != nil {
		t.Fatal(err)
	}
	s := getSession(t, tn, name).Spec
	if s.Agent != "command" || s.Task != "" || len(s.MCPServers) != 0 || s.Image != tn.WorkerImage {
		t.Fatalf("spec %+v", s)
	}
	if len(s.Command) == 0 || s.Command[0] != "bp-replay" {
		t.Fatalf("command %v", s.Command)
	}
	d := tn.DirectorURL
	for _, want := range []string{"--claim-url=" + d + "/claim/claim5", "--seed=77", "--device=iphone-15", "--assistive=zoom",
		"--vision=protanopia", "--display=reduced-motion"} {
		if !slices.Contains(s.Command, want) {
			t.Errorf("command %v lacks %s", s.Command, want)
		}
	}
	// Everything in the pod can read its command: the token is never in it.
	if strings.Contains(strings.Join(s.Command, " "), "tok9") {
		t.Errorf("command %v carries the run token", s.Command)
	}
	if s.NodeSelector["pool"] != "runs" || len(s.Tolerations) != 1 || s.Resources == nil {
		t.Fatalf("placement or resources lost: %+v", s)
	}
}

func TestReplayOnOldTinyAsksForUpdate(t *testing.T) {
	for name, create := range map[string]func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error{
		// a CRD from before v0.15 refuses the agent value outright
		"refused": func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if s, ok := obj.(*agentsv1.Session); ok && s.Spec.Agent == "command" {
				return errors.New(`Session.agents.tinysystems.io "x" is invalid: spec.agent: Unsupported value: "command": supported values: "claude", "codex"`)
			}
			return c.Create(ctx, obj, opts...)
		},
		// or keeps it but prunes the command (tiny's own check names tiny init)
		"pruned": func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if s, ok := obj.(*agentsv1.Session); ok {
				s.Spec.Command = nil
			}
			return c.Create(ctx, obj, opts...)
		},
	} {
		tn := newTiny()
		tn.Client = interceptor.NewClient(tn.Client.(client.WithWatch), interceptor.Funcs{Create: create})
		_, err := tn.Start(context.Background(), replayRun(), replayProject())
		if err == nil || !strings.Contains(err.Error(), "tiny init") || (name == "refused" && !strings.Contains(err.Error(), "older than v0.15")) {
			t.Errorf("%s: err = %v", name, err)
		}
		l := &agentsv1.SessionList{}
		if err := tn.Client.List(context.Background(), l); err != nil || len(l.Items) != 0 {
			t.Errorf("%s: sessions left %d %v", name, len(l.Items), err)
		}
	}
}
