// Package agentkey keeps the Anthropic key agents run with in tiny's agent
// Secret, so the board can set it and nobody outside the namespace sees it.
package agentkey

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// SecretName is tiny's agent Secret; its keys become the agent's environment.
	SecretName = "tiny-agent-env"
	apiKeyKey  = "ANTHROPIC_API_KEY"
	oauthKey   = "CLAUDE_CODE_OAUTH_TOKEN"
)

// Status says whether agents have a key, never what it is. Kind is
// "api-key", "subscription" (only a Claude subscription token) or "".
type Status struct {
	Set  bool   `json:"set"`
	Kind string `json:"kind"`
}

// Store reads and sets the agent key.
type Store interface {
	Status(ctx context.Context) (Status, error)
	SetAPIKey(ctx context.Context, key string) error
}

type secretStore struct {
	c  client.Client
	ns string
}

// NewSecretStore keeps the key in Secret tiny-agent-env in namespace.
func NewSecretStore(c client.Client, namespace string) Store {
	return &secretStore{c: c, ns: namespace}
}

func (s *secretStore) key() client.ObjectKey { return client.ObjectKey{Namespace: s.ns, Name: SecretName} }

func (s *secretStore) Status(ctx context.Context) (Status, error) {
	var sec corev1.Secret
	if err := s.c.Get(ctx, s.key(), &sec); err != nil {
		if apierrors.IsNotFound(err) {
			return Status{}, nil
		}
		return Status{}, err
	}
	switch {
	case len(sec.Data[apiKeyKey]) > 0:
		return Status{Set: true, Kind: "api-key"}, nil
	case len(sec.Data[oauthKey]) > 0:
		return Status{Set: true, Kind: "subscription"}, nil
	}
	return Status{}, nil
}

// SetAPIKey writes ANTHROPIC_API_KEY, creating the Secret if needed and
// keeping every other key in it.
func (s *secretStore) SetAPIKey(ctx context.Context, key string) error {
	var sec corev1.Secret
	err := s.c.Get(ctx, s.key(), &sec)
	if apierrors.IsNotFound(err) {
		return s.c.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: SecretName, Namespace: s.ns},
			Data: map[string][]byte{apiKeyKey: []byte(key)}})
	}
	if err != nil {
		return err
	}
	if sec.Data == nil {
		sec.Data = map[string][]byte{}
	}
	sec.Data[apiKeyKey] = []byte(key)
	return s.c.Update(ctx, &sec)
}
