// Package launcher runs each persona run as one tiny Session.
package launcher

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	agentsv1 "github.com/tiny-systems/tiny/api/v1alpha1"
	"github.com/tiny-systems/tiny/pkg/session"

	"github.com/boarding-party/boardingparty/internal/project"
	"github.com/boarding-party/boardingparty/internal/store"
)

const runLabel = "boardingparty.io/run"

// Labels tiny puts on a session's pods (internal/workload): every session pod
// has app=tiny-session and tinysystems.io/session=<session name>.
const (
	podAppLabel   = "app"
	podAppValue   = "tiny-session"
	runNamePrefix = "bp-run-"
)

// unschedulableGrace is how long a run pod may wait for a node before the run
// fails. It covers a node pool scaling up from zero, which takes a minute or two.
const unschedulableGrace = 5 * time.Minute

// Tiny launches runs as tiny Sessions. tiny has no manager, so Start creates
// the Session and its workload (workspace PVC and Deployment) itself through
// tiny's pkg/session.
type Tiny struct {
	Client      client.Client // scheme must know corev1, appsv1 and agentsv1
	Namespace   string
	WorkerImage string
	DirectorURL string // in-namespace, with a .svc name, e.g. http://bp-director.<ns>.svc:8080
	Model       string
	// Owner, when set, is put on every Session so that deleting the owner (the
	// director's Deployment) garbage-collects the run Sessions and their pods.
	Owner *metav1.OwnerReference
}

// DirectorDeployment is the name of the director's own Deployment.
const DirectorDeployment = "bp-director"

// DirectorOwner reads the director's Deployment and returns an owner reference
// to it. blockOwnerDeletion is false and controller is unset: the reference
// only drives garbage collection.
func DirectorOwner(ctx context.Context, r client.Reader, namespace string) (*metav1.OwnerReference, error) {
	d := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: DirectorDeployment}, d); err != nil {
		return nil, fmt.Errorf("get deployment %s: %w", DirectorDeployment, err)
	}
	f := false
	return &metav1.OwnerReference{
		APIVersion: "apps/v1", Kind: "Deployment", Name: d.Name, UID: d.UID,
		BlockOwnerDeletion: &f, Controller: &f,
	}, nil
}

// sessionName is bp-run-<first 8 of run ID>; run IDs are lower-case hex.
func sessionName(runID string) string {
	if len(runID) > 8 {
		runID = runID[:8]
	}
	return runNamePrefix + strings.ToLower(runID)
}

// SessionName implements runs.Launcher.
func (t *Tiny) SessionName(runID string) string { return sessionName(runID) }

func (t *Tiny) Start(ctx context.Context, r store.Run, p project.Project) (string, error) {
	model := t.Model
	if p.Model != "" {
		model = p.Model
	}
	if m := p.StoryModel(r.Role, r.Story); m != "" {
		model = m
	}
	name := sessionName(r.ID)
	res := p.RunResources() // always set: a run pod is never unbounded
	s := &agentsv1.Session{
		ObjectMeta: metav1.ObjectMeta{Namespace: t.Namespace, Name: name, Labels: map[string]string{runLabel: r.ID}},
		Spec: agentsv1.SessionSpec{
			Task:      Prompt(r),
			Image:     t.WorkerImage,
			Model:     model,
			Resources: &agentsv1.SessionResources{CPU: res.CPU, Memory: res.Memory},
			MCPServers: []agentsv1.MCPServer{
				{Name: "bp", URL: t.DirectorURL + "/mcp/run/" + r.Token},
				{
					Name:    "pw",
					Command: "bp-browser",
					Args:    t.browserArgs(r, p),
				},
			},
		},
	}
	if k, v, ok := p.Pool(); ok {
		s.Spec.NodeSelector = map[string]string{k: v}
		s.Spec.Tolerations = []corev1.Toleration{{Key: k, Operator: corev1.TolerationOpEqual, Value: v, Effect: corev1.TaintEffectNoSchedule}}
	}
	s.Spec.RuntimeClassName = p.RuntimeClass
	if r.Kind == store.RunReplay {
		// A replay runs a recorded journey with no model: a command session
		// running bp-replay, with no agent credentials and no MCP servers.
		s.Spec.Task, s.Spec.Model, s.Spec.MCPServers = "", "", nil
		s.Spec.Agent, s.Spec.Command = "command", t.replayCommand(r, p)
	}
	if r.Story == project.LeadStory {
		// A lead run has no browser; bp-browser only reports usage.
		s.Spec.Task = LeadPrompt(r)
		s.Spec.MCPServers = []agentsv1.MCPServer{s.Spec.MCPServers[0],
			{Name: "usage", Command: "bp-browser", Args: []string{"--usage-url=" + t.DirectorURL + "/usage/" + r.Token, "--usage-only"}}}
	}
	if t.Owner != nil {
		s.OwnerReferences = []metav1.OwnerReference{*t.Owner}
	}
	if err := session.Create(ctx, t.Client, s); err != nil {
		if r.Kind == store.RunReplay && strings.Contains(err.Error(), `Unsupported value: "command"`) {
			return "", errOldTinyReplay
		}
		return "", fmt.Errorf("create session %s: %w", name, err)
	}
	// A Session CRD from before tiny v0.12 drops placement without an error,
	// and the run would land on any node. Create decodes the stored object
	// back into s, so a missing selector here means the cluster pruned it.
	if p.NodePool != "" && len(s.Spec.NodeSelector) == 0 {
		if err := t.Stop(ctx, name); err != nil {
			return "", fmt.Errorf("stop session %s: %w", name, err)
		}
		return "", fmt.Errorf("tiny on this cluster is older than v0.12 and ignores nodePool; update it with tiny init, then start the run again")
	}
	// Same for the sandbox before tiny v0.13: a dropped runtimeClassName
	// would run the agent on the node's kernel, the one thing it was set to
	// prevent.
	if p.RuntimeClass != "" && s.Spec.RuntimeClassName == "" {
		if err := t.Stop(ctx, name); err != nil {
			return "", fmt.Errorf("stop session %s: %w", name, err)
		}
		return "", fmt.Errorf("tiny on this cluster is older than v0.13 and ignores runtimeClass; update it with tiny init, then start the run again")
	}
	return name, nil
}

// Stop deletes the session (its workload goes with it); a missing session is
// success. The delete is foreground: the Session stays listed, with a
// deletionTimestamp, until its Deployment, ReplicaSet and pods are gone, so the
// busy gate keeps seeing it. Re-issuing the delete on a deleting Session is a no-op.
func (t *Tiny) Stop(ctx context.Context, name string) error {
	s := &agentsv1.Session{ObjectMeta: metav1.ObjectMeta{Namespace: t.Namespace, Name: name}}
	err := t.Client.Delete(ctx, s, client.PropagationPolicy(metav1.DeletePropagationForeground))
	if err = client.IgnoreNotFound(err); err != nil {
		return fmt.Errorf("delete session %s: %w", name, err)
	}
	return nil
}

// Phase returns the session's status phase and message, and whether it exists.
func (t *Tiny) Phase(ctx context.Context, name string) (string, bool, string, error) {
	s := &agentsv1.Session{}
	err := t.Client.Get(ctx, client.ObjectKey{Namespace: t.Namespace, Name: name}, s)
	if apierrors.IsNotFound(err) {
		return "", false, "", nil
	}
	if err != nil {
		return "", false, "", fmt.Errorf("get session %s: %w", name, err)
	}
	if s.Status.Phase == agentsv1.SessionFailed || s.Status.Phase == agentsv1.SessionDone {
		return string(s.Status.Phase), true, s.Status.Message, nil
	}
	// A pod no node can take would otherwise sit Pending until the run
	// timeout; past the grace it fails the run with the scheduler's reason.
	if msg, err := t.unschedulable(ctx, name, time.Now()); err != nil {
		return "", false, "", err
	} else if msg != "" {
		return string(agentsv1.SessionFailed), true, "no node can run it: " + msg, nil
	}
	return string(s.Status.Phase), true, s.Status.Message, nil
}

// unschedulable returns the scheduler's message for a session pod that has
// been unschedulable for longer than unschedulableGrace, or "".
func (t *Tiny) unschedulable(ctx context.Context, name string, now time.Time) (string, error) {
	l := &corev1.PodList{}
	if err := t.Client.List(ctx, l, client.InNamespace(t.Namespace),
		client.MatchingLabels{podAppLabel: podAppValue, session.SessionLabel: name}); err != nil {
		return "", fmt.Errorf("list pods: %w", err)
	}
	for _, p := range l.Items {
		if p.Spec.NodeName != "" || now.Sub(p.CreationTimestamp.Time) < unschedulableGrace {
			continue
		}
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason == corev1.PodReasonUnschedulable {
				return c.Message, nil
			}
		}
	}
	return "", nil
}

// Sessions lists run sessions (those labelled boardingparty.io/run) as
// session name -> run ID.
func (t *Tiny) Sessions(ctx context.Context) (map[string]string, error) {
	l := &agentsv1.SessionList{}
	if err := t.Client.List(ctx, l, client.InNamespace(t.Namespace), client.HasLabels{runLabel}); err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	out := make(map[string]string, len(l.Items))
	for _, s := range l.Items {
		out[s.Name] = s.Labels[runLabel]
	}
	return out, nil
}

// LivePods counts the pods of run sessions that still exist, terminating or
// not. Deleting a Session returns before its pod is gone (background
// garbage collection), and a lingering pod can read the next run's token.
func (t *Tiny) LivePods(ctx context.Context) (int, error) {
	l := &corev1.PodList{}
	if err := t.Client.List(ctx, l, client.InNamespace(t.Namespace),
		client.MatchingLabels{podAppLabel: podAppValue}, client.HasLabels{session.SessionLabel}); err != nil {
		return 0, fmt.Errorf("list pods: %w", err)
	}
	n := 0
	for _, p := range l.Items {
		if strings.HasPrefix(p.Labels[session.SessionLabel], runNamePrefix) {
			n++
		}
	}
	return n, nil
}

// Stage reports how far the session's pod has got: "waiting-for-node" while
// it has no node, "starting" with no pod yet or while its containers are not
// all ready, "running" once it is Ready, and "failing" (with the reason) when
// a container cannot start. Pods being deleted or already finished are
// ignored; of the rest the furthest along wins: running, starting,
// waiting-for-node, failing.
func (t *Tiny) Stage(ctx context.Context, name string) (stage, reason string, err error) {
	l := &corev1.PodList{}
	if err := t.Client.List(ctx, l, client.InNamespace(t.Namespace),
		client.MatchingLabels{podAppLabel: podAppValue, session.SessionLabel: name}); err != nil {
		return "", "", fmt.Errorf("list pods: %w", err)
	}
	rank := map[string]int{"": 0, "failing": 1, "waiting-for-node": 2, "starting": 3, "running": 4}
	best, bestReason := "", ""
	for _, p := range l.Items {
		if p.DeletionTimestamp != nil || p.Status.Phase == corev1.PodSucceeded {
			continue
		}
		st, why := podStage(p)
		if rank[st] > rank[best] {
			best, bestReason = st, why
		}
	}
	if best == "" {
		return "starting", "", nil
	}
	return best, bestReason, nil
}

var failingReasons = map[string]bool{
	"CrashLoopBackOff": true, "ImagePullBackOff": true, "ErrImagePull": true, "CreateContainerConfigError": true,
}

func podStage(p corev1.Pod) (string, string) {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return "running", ""
		}
	}
	statuses := append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...)
	for _, cs := range statuses {
		if w := cs.State.Waiting; w != nil && failingReasons[w.Reason] {
			return "failing", w.Reason
		}
	}
	if p.Status.Phase == corev1.PodFailed {
		return "failing", p.Status.Reason
	}
	if p.Spec.NodeName == "" {
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse {
				return "waiting-for-node", ""
			}
		}
		return "starting", ""
	}
	return "starting", ""
}

// browserArgs builds the bp-browser arguments. tiny's CRD rejects MCP args
// with whitespace, quotes or backslashes, so the persona's device and
// accessibility settings go as stable keys (--bp-device, --bp-assistive,
// --bp-vision, --bp-display) that the worker turns into browser settings.
func (t *Tiny) browserArgs(r store.Run, p project.Project) []string {
	args := []string{"--live-url=" + t.DirectorURL + "/live/" + r.Token,
		"--files-url=" + t.DirectorURL + "/files/" + r.Token + "/",
		"--usage-url=" + t.DirectorURL + "/usage/" + r.Token,
		"--profile-url=" + t.DirectorURL + "/profile/" + r.Token,
		"--console-url=" + t.DirectorURL + "/console/" + r.Token,
		"--headless", "--isolated", "--no-sandbox", "--browser=chromium", "--output-dir=/workspace/out"}
	return append(append(args, settingArgs("--bp-", personaProfile(r, p))...), "--caps=devtools,storage")
}

var errOldTinyReplay = errors.New("tiny on this cluster is older than v0.15 and cannot run replays; update it with tiny init, then start the replay again")

// replayCommand is the bp-replay invocation of a replay run. Everything in
// the pod, the journey too, can read the command, so it carries the run's
// one-time claim and never its token: bp-replay spends the claim for its
// URLs before the journey starts.
func (t *Tiny) replayCommand(r store.Run, p project.Project) []string {
	cmd := []string{"bp-replay",
		"--claim-url=" + t.DirectorURL + "/claim/" + r.Claim,
		"--seed=" + strconv.FormatInt(r.Seed, 10)}
	return append(cmd, settingArgs("--", personaProfile(r, p))...)
}

// settingArgs are the persona's browser settings as --<prefix><key>=<value>
// flags, in a stable order.
func settingArgs(prefix string, pr project.Profile) []string {
	set := pr.BrowserSettings()
	var out []string
	for _, k := range []string{"device", "assistive", "vision", "display"} {
		if v, ok := set[k]; ok {
			out = append(out, prefix+k+"="+v)
		}
	}
	return out
}

func personaProfile(r store.Run, p project.Project) project.Profile {
	for _, role := range p.Roles {
		if role.Name != r.Role {
			continue
		}
		for _, pe := range role.Personas {
			if pe.Name == r.Persona {
				return pe.Profile
			}
		}
	}
	return project.Profile{}
}
