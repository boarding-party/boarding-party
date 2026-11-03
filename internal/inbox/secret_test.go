package inbox

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newFake(t *testing.T) client.Client {
	t.Helper()
	sc := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(sc); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(sc).Build()
}

func TestSecretStoreRoundTrip(t *testing.T) {
	c := newFake(t)
	s := NewSecretStore(c, "ns")
	ctx := context.Background()
	if _, ok, err := s.Load(ctx); ok || err != nil {
		t.Fatalf("empty load = %v, %v", ok, err)
	}
	want := Config{Host: "imap.example.test", Port: 993, Username: "u@example.test", Password: "pw"}
	if err := s.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Load(ctx)
	if err != nil || !ok || got != want {
		t.Fatalf("load = %+v, %v, %v", got, ok, err)
	}
	want.Password = "pw2"
	if err := s.Save(ctx, want); err != nil { // update path
		t.Fatal(err)
	}
	got, _, _ = s.Load(ctx)
	if got.Password != "pw2" {
		t.Fatalf("not updated: %+v", got)
	}
	var sec corev1.Secret
	if err := c.Get(ctx, client.ObjectKey{Namespace: "ns", Name: "bp-mailbox"}, &sec); err != nil {
		t.Fatal(err)
	}
	if sec.Labels["app.kubernetes.io/managed-by"] != "boardingparty" {
		t.Fatalf("labels = %v", sec.Labels)
	}
	if err := s.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Load(ctx); ok {
		t.Fatal("still there after delete")
	}
}

func TestSecretStoreDeleteMissing(t *testing.T) {
	if err := NewSecretStore(newFake(t), "ns").Delete(context.Background()); err != nil {
		t.Fatal(err)
	}
}
