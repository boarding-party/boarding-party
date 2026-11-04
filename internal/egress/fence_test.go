package egress

import (
	"context"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newFence(objs ...client.Object) *Fence {
	sc := runtime.NewScheme()
	if err := corev1.AddToScheme(sc); err != nil {
		panic(err)
	}
	return &Fence{Client: fake.NewClientBuilder().WithScheme(sc).WithObjects(objs...).Build(), Namespace: "bp"}
}

func cm(name string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "bp"}, Data: data}
}

func TestCheckFence(t *testing.T) {
	on := cm(tinySettings, map[string]string{"egressProxy": "true"})
	cases := []struct {
		name string
		objs []client.Object
		want string // substring of the error; "" means fenced
	}{
		{"no settings", nil, "proxy is off"},
		{"proxy off", []client.Object{cm(tinySettings, map[string]string{"egressProxy": "false"}), cm(egressAllowCM, nil)}, "proxy is off"},
		{"no allow-list", []client.Object{on}, "can't read allow-list"},
		{"fenced", []client.Object{on, cm(egressAllowCM, map[string]string{"hosts": "staging.example.com\n.example.com # subdomains\napi.anthropic.com\n.githubusercontent.com\n"})}, ""},
		{"public suffix", []client.Object{on, cm(egressAllowCM, map[string]string{"hosts": "staging.example.com\n.com\n"})}, "admits whole domains: .com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := newFence(c.objs...).Check(context.Background())
			if c.want == "" {
				if err != nil {
					t.Fatalf("Check = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Check = %v, want error containing %q", err, c.want)
			}
		})
	}
}

func TestWideEntries(t *testing.T) {
	got := wideEntries("*\n.\n.co.uk\n.uk # all of it\n.example.co.uk\nexample.com\n# .com\n.githubusercontent.com\n.github.io\n.corp\n")
	want := []string{"*", ".", ".co.uk", ".uk", ".corp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wideEntries = %v, want %v", got, want)
	}
}
