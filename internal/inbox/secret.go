package inbox

import (
	"context"
	"errors"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SecretName is the Secret holding the mailbox login.
const SecretName = "bp-mailbox"

type secretStore struct {
	c  client.Client
	ns string
}

// NewSecretStore keeps the mailbox Config in Secret bp-mailbox in namespace.
func NewSecretStore(c client.Client, namespace string) Secrets {
	return &secretStore{c: c, ns: namespace}
}

func (s *secretStore) key() client.ObjectKey {
	return client.ObjectKey{Namespace: s.ns, Name: SecretName}
}

func (s *secretStore) Load(ctx context.Context) (Config, bool, error) {
	var sec corev1.Secret
	if err := s.c.Get(ctx, s.key(), &sec); err != nil {
		if apierrors.IsNotFound(err) {
			return Config{}, false, nil
		}
		return Config{}, false, err
	}
	port := 0
	if p := string(sec.Data["port"]); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return Config{}, false, errors.New("mailbox secret has a bad port")
		}
		port = n
	}
	return Config{
		Host: string(sec.Data["host"]), Port: port,
		Username: string(sec.Data["username"]), Password: string(sec.Data["password"]),
	}, true, nil
}

func (s *secretStore) Save(ctx context.Context, c Config) error {
	data := map[string][]byte{
		"host": []byte(c.Host), "port": []byte(strconv.Itoa(c.Port)),
		"username": []byte(c.Username), "password": []byte(c.Password),
	}
	var sec corev1.Secret
	err := s.c.Get(ctx, s.key(), &sec)
	if apierrors.IsNotFound(err) {
		return s.c.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: SecretName, Namespace: s.ns,
				Labels: map[string]string{"app.kubernetes.io/managed-by": "boardingparty"}},
			Data: data,
		})
	}
	if err != nil {
		return err
	}
	sec.Data = data
	return s.c.Update(ctx, &sec)
}

func (s *secretStore) Delete(ctx context.Context) error {
	err := s.c.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: SecretName, Namespace: s.ns}})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
