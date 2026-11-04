package egress

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const operatorList = "# One host per line.\napi.anthropic.com\n.anthropic.com\n"

func TestWithBlock(t *testing.T) {
	first := withBlock(operatorList, []string{"fonts.gstatic.com", "staging.example.com"})
	want := operatorList + "\n" + blockBegin + "\nfonts.gstatic.com\nstaging.example.com\n" + blockEnd + "\n"
	if first != want {
		t.Fatalf("first sync:\n%s\nwant:\n%s", first, want)
	}
	// A later sync replaces the block and keeps the operator's lines.
	second := withBlock(first+"github.com\n", []string{"staging.example.com"})
	if !strings.HasPrefix(second, operatorList) || strings.Contains(second, "fonts.gstatic.com") ||
		!strings.Contains(second, "github.com") || strings.Count(second, blockBegin) != 1 {
		t.Fatalf("second sync:\n%s", second)
	}
	if got := withBlock(first, nil); got != operatorList {
		t.Fatalf("empty sync = %q, want the operator list back", got)
	}
}

func TestSyncUpdatesConfigMap(t *testing.T) {
	f := newFence(cm(egressAllowCM, map[string]string{"hosts": operatorList}))
	ctx := context.Background()
	if err := f.Sync(ctx, []string{"staging.example.com"}); err != nil {
		t.Fatal(err)
	}
	got := &corev1.ConfigMap{}
	if err := f.Client.Get(ctx, client.ObjectKey{Namespace: "bp", Name: egressAllowCM}, got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Data["hosts"], "\nstaging.example.com\n") || !strings.HasPrefix(got.Data["hosts"], operatorList) {
		t.Fatalf("allow-list after sync:\n%s", got.Data["hosts"])
	}
	if err := newFence().Sync(ctx, []string{"x.example.com"}); err == nil {
		t.Fatal("Sync without an allow-list succeeded")
	}
}

func TestParseDenied(t *testing.T) {
	since := time.Date(2026, 11, 4, 15, 0, 0, 0, time.UTC)
	logs := strings.Join([]string{
		"2026-11-04T14:59:59.000000000Z 2026/11/04 14:59:59 egress DENIED CONNECT before.example.com:443",
		"2026-11-04T15:00:01.500000000Z 2026/11/04 15:00:01 egress DENIED CONNECT Fonts.GStatic.com:443",
		"2026-11-04T15:00:02.000000000Z 2026/11/04 15:00:02 egress allow-list reloaded from /etc/x",
		"2026-11-04T15:00:03.000000000Z 2026/11/04 15:00:03 egress DENIED GET cdn.example.net",
		"2026-11-04T15:00:04.000000000Z 2026/11/04 15:00:04 egress DENIED CONNECT [2001:db8::1]:443",
		"2026-11-04T15:10:00.000000000Z 2026/11/04 15:10:00 egress DENIED CONNECT after.example.com:443",
		"not a log line",
	}, "\n")
	got, err := parseDenied(strings.NewReader(logs), since, since.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"fonts.gstatic.com", "cdn.example.net", "2001:db8::1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDenied = %v, want %v", got, want)
	}
}

func TestDeniedIgnoresOtherLines(t *testing.T) {
	// The fake clientset answers every log read with "fake logs".
	proxy := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "tiny-egress-1", Namespace: "bp", Labels: map[string]string{"app": "tiny-egress"}}}
	f := &Fence{Pods: fake.NewSimpleClientset(proxy).CoreV1(), Namespace: "bp"}
	got, err := f.Denied(context.Background(), time.Now().Add(-time.Minute), time.Time{})
	if err != nil || len(got) != 0 {
		t.Fatalf("Denied = %v, %v; want nothing", got, err)
	}
}
