package install

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/boarding-party/boardingparty/deploy"
	appsv1 "k8s.io/api/apps/v1"
	apiextclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/yaml"
)

const (
	fieldManager    = "boardingparty"
	sessionCRD      = "sessions.agents.tinysystems.io"
	sessionSA       = "tiny-session"
	agentSecret     = "tiny-agent-env"
	tinySettings    = "tiny-settings"
	egressAllowCM   = "tiny-egress-allow"
	egressAllowKey  = "hosts"
	directorDeploy  = "bp-director"
	directorImage   = "ghcr.io/boarding-party/director"
	workerImage     = "ghcr.io/boarding-party/worker"
	defaultImageTag = "main"
	// manifestHashAnnotation is stamped on the director Deployment's own
	// metadata (not its pod template) with the fingerprint of what was applied.
	manifestHashAnnotation = "boardingparty.io/manifest-hash"
	// cliBuiltAnnotation records when the CLI that last deployed the director
	// was built, so an older CLI can tell it would be going backwards.
	cliBuiltAnnotation = "boardingparty.io/cli-built"
)

// checker prints one line per preflight check and remembers failures.
type checker struct {
	out    io.Writer
	failed bool
	// notSetUp is set when a check proved tiny's setup is absent or too old,
	// as opposed to a check we weren't allowed to make.
	notSetUp bool
}

func (c *checker) ok(format string, a ...any) {
	fmt.Fprintf(c.out, "✓ %s\n", clean(fmt.Sprintf(format, a...)))
}
func (c *checker) warn(format string, a ...any) {
	fmt.Fprintf(c.out, "! %s\n", clean(fmt.Sprintf(format, a...)))
}
func (c *checker) missing(format string, a ...any) {
	c.notSetUp = true
	c.fail(format, a...)
}
func (c *checker) fail(format string, a ...any) {
	c.failed = true
	fmt.Fprintf(c.out, "✗ %s\n", clean(fmt.Sprintf(format, a...)))
}

// Preflight runs the read-only checks and reports whether any failed.
// It never prints Secret values, only key names.
func Preflight(ctx context.Context, kube kubernetes.Interface, ext apiextclient.Interface, ns string, out io.Writer) (failed bool) {
	return preflightCheck(ctx, kube, ext, ns, out).failed
}

// denied reports a read the user's credentials aren't allowed to make.
func denied(verb, resource string) string {
	return fmt.Sprintf("couldn't check: no permission (%s %s)", verb, resource)
}

func preflightCheck(ctx context.Context, kube kubernetes.Interface, ext apiextclient.Interface, ns string, out io.Writer) *checker {
	c := &checker{out: out}
	tinyInit := fmt.Sprintf("run `tiny init -n %s`", ns)

	crd, err := ext.ApiextensionsV1().CustomResourceDefinitions().Get(ctx, sessionCRD, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		c.missing("CRD %s not found: tiny isn't installed, %s", sessionCRD, tinyInit)
	case apierrors.IsForbidden(err):
		c.warn("CRD %s: %s; skipped, the check needs cluster-scoped read access", sessionCRD, denied("get", "customresourcedefinitions"))
	case err != nil:
		c.fail("CRD %s: %v", sessionCRD, err)
	default:
		has := false
		for _, v := range crd.Spec.Versions {
			if v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
				continue
			}
			if spec, ok := v.Schema.OpenAPIV3Schema.Properties["spec"]; ok {
				if _, ok := spec.Properties["mcpServers"]; ok {
					has = true
				}
			}
		}
		if has {
			c.ok("CRD %s has spec.mcpServers", sessionCRD)
		} else {
			c.missing("CRD %s lacks spec.mcpServers: tiny is older than v0.11.0, upgrade tiny", sessionCRD)
		}
	}

	_, err = kube.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	nsOK := err == nil || apierrors.IsForbidden(err)
	switch {
	case apierrors.IsNotFound(err):
		c.missing("namespace %s not found: %s", ns, tinyInit)
	case apierrors.IsForbidden(err):
		c.warn("namespace %s: %s; skipped, the check needs cluster-scoped read access", ns, denied("get", "namespaces"))
	case err != nil:
		c.fail("namespace %s: %v", ns, err)
	default:
		c.ok("namespace %s", ns)
	}
	if nsOK {
		_, err = kube.CoreV1().ServiceAccounts(ns).Get(ctx, sessionSA, metav1.GetOptions{})
		switch {
		case apierrors.IsNotFound(err):
			c.missing("service account %s not found in %s: %s", sessionSA, ns, tinyInit)
		case apierrors.IsForbidden(err):
			c.fail("service account %s: %s", sessionSA, denied("get", "serviceaccounts"))
		case err != nil:
			c.fail("service account %s: %v", sessionSA, err)
		default:
			c.ok("service account %s", sessionSA)
		}
	}

	checkAgentSecret(ctx, kube, ns, c)
	checkEgress(ctx, kube, ns, c)
	c.warn("network policies are only enforced if your CNI enforces them")
	return c
}

func checkAgentSecret(ctx context.Context, kube kubernetes.Interface, ns string, c *checker) {
	sec, err := kube.CoreV1().Secrets(ns).Get(ctx, agentSecret, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		c.warn("secret %s not found in %s: set the Anthropic key in the board (Project → Agent key); runs wait until it is set", agentSecret, ns)
		return
	case apierrors.IsForbidden(err):
		c.fail("secret %s: %s", agentSecret, denied("get", "secrets"))
		return
	case err != nil:
		c.fail("secret %s: %v", agentSecret, err)
		return
	}
	_, key := sec.Data["ANTHROPIC_API_KEY"]
	_, oauth := sec.Data["CLAUDE_CODE_OAUTH_TOKEN"]
	switch {
	case key:
		c.ok("secret %s has ANTHROPIC_API_KEY", agentSecret)
	case oauth:
		c.warn("secret %s has only CLAUDE_CODE_OAUTH_TOKEN: a subscription token; automated runs should use a metered ANTHROPIC_API_KEY under Anthropic's terms", agentSecret)
	default:
		c.warn("secret %s has neither ANTHROPIC_API_KEY nor CLAUDE_CODE_OAUTH_TOKEN: set the Anthropic key in the board (Project → Agent key); runs wait until it is set", agentSecret)
	}
}

func checkEgress(ctx context.Context, kube kubernetes.Interface, ns string, c *checker) {
	cm, err := kube.CoreV1().ConfigMaps(ns).Get(ctx, tinySettings, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		c.warn("configmap %s: %v", tinySettings, err)
		return
	}
	if err != nil || cm.Data["egressProxy"] != "true" {
		c.warn("egress proxy is off: no hostname allow-list, so the director refuses to start runs (or run it with --no-egress-fence)")
		return
	}
	al, err := kube.CoreV1().ConfigMaps(ns).Get(ctx, egressAllowCM, metav1.GetOptions{})
	if err != nil {
		c.warn("egress proxy is on but allow-list %s can't be read: %v", egressAllowCM, err)
		return
	}
	var hosts []string
	sc := bufio.NewScanner(strings.NewReader(al.Data[egressAllowKey]))
	for sc.Scan() {
		if h := strings.TrimSpace(sc.Text()); h != "" && !strings.HasPrefix(h, "#") {
			hosts = append(hosts, h)
		}
	}
	c.ok("egress proxy on; allowed hosts: %s", strings.Join(hosts, ", "))
}

// renderManifest returns the director objects for ns with the image tag applied.
func renderManifest(raw []byte, ns, tag string) ([]*unstructured.Unstructured, error) {
	text := strings.ReplaceAll(string(raw), directorImage+":main", directorImage+":"+tag)
	text = strings.ReplaceAll(text, workerImage+":main", workerImage+":"+tag)
	var objs []*unstructured.Unstructured
	for _, doc := range strings.Split(text, "\n---\n") {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		u := &unstructured.Unstructured{}
		if err := yaml.Unmarshal([]byte(doc), &u.Object); err != nil {
			return nil, fmt.Errorf("parse manifest: %w", err)
		}
		if len(u.Object) == 0 {
			continue
		}
		u.SetNamespace(ns)
		if u.GetKind() == "RoleBinding" {
			subs, _, _ := unstructured.NestedSlice(u.Object, "subjects")
			for _, s := range subs {
				if m, ok := s.(map[string]any); ok && m["kind"] == "ServiceAccount" {
					m["namespace"] = ns
				}
			}
			_ = unstructured.SetNestedSlice(u.Object, subs, "subjects")
		}
		objs = append(objs, u)
	}
	return objs, nil
}

// manifestHash fingerprints the fully rendered manifest (every object, for
// this namespace and image tag): the first 16 hex chars of a sha256 over a
// deterministic serialisation (encoding/json sorts map keys).
func manifestHash(objs []*unstructured.Unstructured) (string, error) {
	h := sha256.New()
	for _, o := range objs {
		b, err := json.Marshal(o.Object)
		if err != nil {
			return "", err
		}
		h.Write(b)
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

func printManifest(out io.Writer, objs []*unstructured.Unstructured) error {
	for i, o := range objs {
		b, err := yaml.Marshal(o.Object)
		if err != nil {
			return err
		}
		if i > 0 {
			fmt.Fprintln(out, "---")
		}
		out.Write(b)
	}
	return nil
}

type Options struct {
	Ns, Tag string
	Note    string // printed before an install or upgrade
	DryRun  bool
	Force   bool
	Timeout time.Duration
	Built   time.Time // this CLI's build time; zero when unknown
	// NoDowngrade refuses to replace a director that a newer CLI deployed:
	// the board's UI comes from this CLI, and an older one drops fields the
	// newer director keeps.
	NoDowngrade bool
}

// Deps are the cluster-facing pieces, replaceable in tests.
type Deps struct {
	Kube  kubernetes.Interface
	Ext   apiextclient.Interface
	Apply func(ctx context.Context, obj *unstructured.Unstructured) error
	// Verify checks the director answers GET /api/runs.
	Verify func(ctx context.Context) error
	// ImageExists reports whether repo:tag is published; nil skips the check.
	ImageExists func(ctx context.Context, repo, tag string) (bool, error)
}

var waitPoll = 2 * time.Second

func Setup(ctx context.Context, d Deps, o Options, out io.Writer) error {
	if Preflight(ctx, d.Kube, d.Ext, o.Ns, out) {
		return ErrPreflight
	}
	objs, err := renderManifest(deploy.Director, o.Ns, o.Tag)
	if err != nil {
		return err
	}
	if o.DryRun {
		fmt.Fprintln(out, "---")
		return printManifest(out, objs)
	}
	if err := checkImages(ctx, d, o.Tag); err != nil {
		return err
	}
	if o.Note != "" {
		fmt.Fprintln(out, o.Note)
	}
	wctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	if _, err := installDirector(wctx, d, o, objs, out); err != nil {
		return err
	}
	if err := waitVerify(wctx, d); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ director ready\n\nNext:\n  boardingparty board -n %s\n", o.Ns)
	return nil
}

// installDirector applies the objects one by one and waits for the rollout.
// It returns the admin API token.
func installDirector(ctx context.Context, d Deps, o Options, objs []*unstructured.Unstructured, out io.Writer) (string, error) {
	// The Deployment reads the admin secret, so it has to exist first.
	sec, created, err := ensureAdminSecret(ctx, d.Kube, o.Ns)
	if err != nil {
		return "", err
	}
	token, err := adminTokenOf(sec, o.Ns)
	if err != nil {
		return "", err
	}
	if created {
		fmt.Fprintf(out, "✓ Secret/%s (admin key, created)\n", adminSecret)
	}
	hash, err := manifestHash(objs)
	if err != nil {
		return "", err
	}
	for _, obj := range objs {
		if obj.GetKind() == "Deployment" {
			ann := obj.GetAnnotations()
			if ann == nil {
				ann = map[string]string{}
			}
			ann[manifestHashAnnotation] = hash
			if !o.Built.IsZero() {
				ann[cliBuiltAnnotation] = o.Built.UTC().Format(time.RFC3339)
			}
			obj.SetAnnotations(ann)
		}
		if obj.GetKind() == "Deployment" && sec.UID != "" {
			if err := unstructured.SetNestedField(obj.Object, string(sec.UID), "spec", "template", "metadata", "annotations", adminSecretAnnotation); err != nil {
				return "", err
			}
		}
	}
	for _, obj := range objs {
		if err := d.Apply(ctx, obj); err != nil {
			if apierrors.IsConflict(err) && !o.Force {
				return "", fmt.Errorf("apply %s/%s: %w\nthe director was previously applied by another tool; run `boardingparty setup --force-conflicts` once", obj.GetKind(), obj.GetName(), err)
			}
			return "", fmt.Errorf("apply %s/%s: %w", obj.GetKind(), obj.GetName(), err)
		}
		fmt.Fprintf(out, "✓ %s/%s\n", clean(obj.GetKind()), clean(obj.GetName()))
	}
	var lastErr error
	err = wait.PollUntilContextCancel(ctx, waitPoll, true, func(ctx context.Context) (bool, error) {
		dep, err := d.Kube.AppsV1().Deployments(o.Ns).Get(ctx, directorDeploy, metav1.GetOptions{})
		if err != nil {
			lastErr = err
			return false, nil
		}
		lastErr = nil
		return rolloutComplete(dep), nil
	})
	if err != nil {
		return "", fmt.Errorf("deployment %s not rolled out within %s: %v (last error: %v)", directorDeploy, o.Timeout, err, lastErr)
	}
	fmt.Fprintf(out, "✓ deployment/%s rolled out\n", directorDeploy)
	return token, nil
}

// waitVerify retries the admin-API check until it passes or ctx ends.
func waitVerify(ctx context.Context, d Deps) error {
	var verr error
	err := wait.PollUntilContextCancel(ctx, waitPoll, true, func(ctx context.Context) (bool, error) {
		verr = d.Verify(ctx)
		return verr == nil, nil
	})
	if err != nil {
		return fmt.Errorf("director not answering: %v (last error: %v)", err, verr)
	}
	return nil
}

// EnsureDirector is what every command that needs the director runs first.
// Fast path: a Deployment GET and a Secret GET; if the director is there with
// the wanted manifest fingerprint (which covers the image tag) and its admin Secret exists, nothing else happens and the
// admin token is returned. Otherwise (including a missing Secret, which the
// running director can't work without) tiny's prerequisites are checked (read-only,
// failing with a pointer to `tiny init`), the warnings are printed once, and
// the director is installed or upgraded in place.
func EnsureDirector(ctx context.Context, d Deps, o Options, out io.Writer) (string, error) {
	dep, err := d.Kube.AppsV1().Deployments(o.Ns).Get(ctx, directorDeploy, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return "", fmt.Errorf("get deployment %s: %w", directorDeploy, err)
	}
	upgrade := err == nil
	keyMissing := false
	objs, err := renderManifest(deploy.Director, o.Ns, o.Tag)
	if err != nil {
		return "", err
	}
	want, err := manifestHash(objs)
	if err != nil {
		return "", err
	}
	if upgrade && dep.Annotations[manifestHashAnnotation] == want {
		sec, serr := d.Kube.CoreV1().Secrets(o.Ns).Get(ctx, adminSecret, metav1.GetOptions{})
		switch {
		case serr == nil:
			return adminTokenOf(sec, o.Ns)
		case !apierrors.IsNotFound(serr):
			return "", fmt.Errorf("read secret %s (the director's admin key): %w", adminSecret, serr)
		}
		keyMissing = true
	}
	if upgrade && o.NoDowngrade && !o.Built.IsZero() {
		if theirs, err := time.Parse(time.RFC3339, dep.Annotations[cliBuiltAnnotation]); err == nil && theirs.After(o.Built) {
			return "", fmt.Errorf("the director in %s (%s) was deployed by a newer boardingparty CLI (built %s) than this one (built %s); update this CLI and run again",
				o.Ns, ImageTag(dep), theirs.Format(time.DateTime), o.Built.UTC().Format(time.DateTime))
		}
	}
	var buf strings.Builder
	if pc := preflightCheck(ctx, d.Kube, d.Ext, o.Ns, &buf); pc.failed {
		var failed []string
		for _, l := range strings.Split(buf.String(), "\n") {
			if strings.HasPrefix(l, "✗") {
				failed = append(failed, l)
			}
		}
		if !pc.notSetUp {
			return "", fmt.Errorf("couldn't check tiny's setup in namespace %s: your credentials lack a permission\n%s",
				o.Ns, strings.Join(failed, "\n"))
		}
		return "", fmt.Errorf("tiny isn't set up in namespace %s: run `tiny init -n %s` (and `tiny setup` for the agent key)\n%s",
			o.Ns, o.Ns, strings.Join(failed, "\n"))
	}
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(l, "!") {
			fmt.Fprintln(out, l)
		}
	}
	if err := checkImages(ctx, d, o.Tag); err != nil {
		return "", err
	}
	if o.Note != "" {
		fmt.Fprintln(out, o.Note)
	}
	switch {
	case keyMissing:
		fmt.Fprintf(out, "reinstalling the director in %s (its admin key is missing)\n", o.Ns)
	case !upgrade:
		fmt.Fprintf(out, "installing the director in %s (image tag %s)\n", o.Ns, o.Tag)
	default:
		fmt.Fprintf(out, "upgrading the director in %s (%s -> %s)\n", o.Ns, ImageTag(dep), o.Tag)
	}
	wctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	token, err := installDirector(wctx, d, o, objs, out)
	if err != nil {
		return "", err
	}
	if err := waitVerify(wctx, d); err != nil {
		return "", err
	}
	return token, nil
}

// ImageTag returns the tag of the director container image, or "" when the
// image has none (untagged or digest-only).
func ImageTag(dep *appsv1.Deployment) string {
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Name == "director" {
			return tagOf(c.Image)
		}
	}
	return ""
}

func tagOf(image string) string {
	if i := strings.IndexByte(image, '@'); i >= 0 {
		image = image[:i]
	}
	last := image[strings.LastIndex(image, "/")+1:] // a registry port sits before the last slash
	if j := strings.LastIndex(last, ":"); j >= 0 {
		return last[j+1:]
	}
	return ""
}

// checkImages fails when a sha-… tag has no published images yet. Other tags
// are not checked, and a nil check (tests) skips it.
func checkImages(ctx context.Context, d Deps, tag string) error {
	if d.ImageExists == nil || !strings.HasPrefix(tag, "sha-") {
		return nil
	}
	for _, repo := range []string{"boarding-party/director", "boarding-party/worker"} {
		ok, err := d.ImageExists(ctx, repo, tag)
		if err != nil {
			return fmt.Errorf("check image %s:%s on ghcr.io: %w", repo, tag, err)
		}
		if !ok {
			return fmt.Errorf("images for %s aren't published yet — wait for CI (github.com/boarding-party/boardingparty/actions) and try again", strings.TrimPrefix(tag, "sha-"))
		}
	}
	return nil
}

// GHCRImageExists asks ghcr.io anonymously whether repo:tag has a manifest.
func GHCRImageExists(ctx context.Context, repo, tag string) (bool, error) {
	c := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ghcr.io/token?scope=repository:"+repo+":pull", nil)
	if err != nil {
		return false, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("token request: %s", resp.Status)
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&tok); err != nil {
		return false, err
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodHead, "https://ghcr.io/v2/"+repo+"/manifests/"+tag, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	r2, err := c.Do(req)
	if err != nil {
		return false, err
	}
	defer r2.Body.Close()
	switch r2.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("manifest request: %s", r2.Status)
}

// rolloutComplete is true once the controller has seen the latest spec and
// every replica is updated and available. The Available condition alone is not
// enough: it stays true on the old pod right after an apply.
func rolloutComplete(dep *appsv1.Deployment) bool {
	want := int32(1)
	if dep.Spec.Replicas != nil {
		want = *dep.Spec.Replicas
	}
	st := dep.Status
	return st.ObservedGeneration >= dep.Generation && st.UpdatedReplicas == want &&
		st.AvailableReplicas == want && st.UnavailableReplicas == 0
}

// ServerSideApplier applies objects with server-side apply, conflicts forced only when asked.
func ServerSideApplier(cfg *rest.Config, force bool) (func(context.Context, *unstructured.Unstructured) error, error) {
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, err
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disc))
	return func(ctx context.Context, obj *unstructured.Unstructured) error {
		gvk := obj.GroupVersionKind()
		m, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return err
		}
		body, err := obj.MarshalJSON()
		if err != nil {
			return err
		}
		_, err = dc.Resource(m.Resource).Namespace(obj.GetNamespace()).Patch(ctx, obj.GetName(), types.ApplyPatchType, body,
			metav1.PatchOptions{FieldManager: fieldManager, Force: &force})
		return err
	}, nil
}

// VerifyDirector port-forwards to the director and expects GET /api/runs = 200.
func VerifyDirector(cfg *rest.Config, ns string) func(context.Context) error {
	return func(ctx context.Context) error {
		kube, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return err
		}
		token, err := ReadAdminToken(ctx, kube, ns)
		if err != nil {
			return err
		}
		base, stop, err := Forward(ctx, cfg, ns, directorSelector, adminPort)
		if err != nil {
			return err
		}
		defer stop()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/runs", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET /api/runs: %s", clean(resp.Status))
		}
		return nil
	}
}
