package cli

import (
	"errors"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/boarding-party/boardingparty/pkg/install"
	"github.com/spf13/cobra"
	apiextclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/kubernetes"
)

func newSetup(g *globals) *cobra.Command {
	o := install.Options{Built: buildTime()}
	defTag, defNote := resolveTag()
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Check the cluster and install the director",
		Long: "Checks that tiny is ready in the namespace, then installs (or upgrades in place)\n" +
			"the boardingparty director with server-side apply and waits until it answers.\n" +
			"Prerequisite: `tiny init -n <namespace>` and tiny's agent secret.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := g.chooseTarget(cmd.Context()); err != nil {
				return err
			}
			o.Ns = g.namespace
			if !cmd.Flags().Changed("image-tag") {
				o.Note = defNote
			}
			cfg, _, err := g.kubeConfig()
			if err != nil {
				return err
			}
			d := install.Deps{}
			if d.Kube, err = kubernetes.NewForConfig(cfg); err != nil {
				return err
			}
			if d.Ext, err = apiextclient.NewForConfig(cfg); err != nil {
				return err
			}
			if d.Apply, err = install.ServerSideApplier(cfg, o.Force); err != nil {
				return err
			}
			d.Verify = install.VerifyDirector(cfg, o.Ns)
			d.ImageExists = install.GHCRImageExists
			if err := install.Setup(cmd.Context(), d, o, cmd.OutOrStdout()); errors.Is(err, install.ErrPreflight) {
				return ExitError{Code: 1}
			} else if err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&o.Tag, "image-tag", defTag, "director and worker image tag")
	cmd.Flags().BoolVar(&o.DryRun, "dry-run", false, "run the checks and print the rendered manifest; apply nothing")
	cmd.Flags().BoolVar(&o.Force, "force-conflicts", false, "take over fields owned by another tool (use once after a kubectl apply)")
	cmd.Flags().DurationVar(&o.Timeout, "timeout", 3*time.Minute, "how long to wait for the director")
	return cmd
}

// buildTime is when this binary's commit was made (vcs.time), or zero.
func buildTime() time.Time {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return time.Time{}
	}
	for _, s := range bi.Settings {
		if s.Key == "vcs.time" {
			t, _ := time.Parse(time.RFC3339, s.Value)
			return t
		}
	}
	return time.Time{}
}

// wantedTag is the image tag this CLI wants; see resolveTag.
func wantedTag() string {
	tag, _ := resolveTag()
	return tag
}

// resolveTag picks the image tag for this binary. A release build wants its
// own version. A clean dev build wants the image CI built from the same commit
// (sha-<7 chars>); a dirty or unstamped one falls back to main and returns a
// note saying so.
func resolveTag() (tag, note string) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return tagFromBuild("", "", false)
	}
	var rev string
	var modified bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	return tagFromBuild(bi.Main.Version, rev, modified)
}

const devBuildNote = "dev build: using image tag main, which may not match this CLI"

func tagFromBuild(version, rev string, modified bool) (tag, note string) {
	if releaseSemver.MatchString(version) {
		return strings.TrimPrefix(version, "v"), ""
	}
	if rev != "" && !modified {
		if len(rev) > 7 {
			rev = rev[:7]
		}
		return "sha-" + rev, ""
	}
	return defaultImageTag, devBuildNote
}

var releaseSemver = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
