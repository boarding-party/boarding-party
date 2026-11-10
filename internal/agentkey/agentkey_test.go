package agentkey

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newFake(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	sc := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(sc); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(sc).WithObjects(objs...).Build()
}

func TestSetAPIKeyKeepsOtherKeys(t *testing.T) {
	c := newFake(t, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: SecretName, Namespace: "ns"},
		Data: map[string][]byte{"CLAUDE_CODE_OAUTH_TOKEN": []byte("oauth")}})
	s := NewSecretStore(c, "ns")
	ctx := context.Background()
	if st, err := s.Status(ctx); err != nil || st != (Status{Set: true, Kind: "subscription"}) {
		t.Fatalf("before: %+v %v", st, err)
	}
	if err := s.SetAPIKey(ctx, "sk-ant-abc"); err != nil {
		t.Fatal(err)
	}
	var sec corev1.Secret
	if err := c.Get(ctx, client.ObjectKey{Namespace: "ns", Name: SecretName}, &sec); err != nil {
		t.Fatal(err)
	}
	if string(sec.Data["CLAUDE_CODE_OAUTH_TOKEN"]) != "oauth" || string(sec.Data["ANTHROPIC_API_KEY"]) != "sk-ant-abc" {
		t.Fatalf("data %v", sec.Data)
	}
	if st, _ := s.Status(ctx); st != (Status{Set: true, Kind: "api-key"}) {
		t.Fatalf("after: %+v", st)
	}
}

func TestSetAPIKeyCreatesSecret(t *testing.T) {
	c := newFake(t)
	s := NewSecretStore(c, "ns")
	ctx := context.Background()
	if st, err := s.Status(ctx); err != nil || st.Set {
		t.Fatalf("missing secret: %+v %v", st, err)
	}
	if err := s.SetAPIKey(ctx, "sk-ant-abc"); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.Status(ctx); st != (Status{Set: true, Kind: "api-key"}) {
		t.Fatalf("after: %+v", st)
	}
}
