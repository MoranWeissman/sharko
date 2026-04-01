package config

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/moran/argocd-addons-platform/internal/crypto"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const aiSecretName = "aap-ai-config"

// AIConfigStore persists AI provider settings in an encrypted K8s Secret.
// Uses raw JSON bytes to avoid import cycles with the ai package.
type AIConfigStore struct {
	client        kubernetes.Interface
	namespace     string
	encryptionKey string
}

// NewAIConfigStore creates a K8s Secret-backed AI config store.
func NewAIConfigStore(namespace, encryptionKey string) (*AIConfigStore, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("creating in-cluster config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating k8s client: %w", err)
	}
	return &AIConfigStore{client: clientset, namespace: namespace, encryptionKey: encryptionKey}, nil
}

// SaveJSON encrypts and persists JSON-encoded AI config to a K8s Secret.
func (s *AIConfigStore) SaveJSON(data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	encrypted, err := crypto.Encrypt(data, s.encryptionKey)
	if err != nil {
		return fmt.Errorf("encrypting ai config: %w", err)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      aiSecretName,
			Namespace: s.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "aap",
				"app.kubernetes.io/component":  "ai-config",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"config": []byte(encrypted),
		},
	}

	_, err = s.client.CoreV1().Secrets(s.namespace).Update(ctx, secret, metav1.UpdateOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			_, err = s.client.CoreV1().Secrets(s.namespace).Create(ctx, secret, metav1.CreateOptions{})
		}
		if err != nil {
			return fmt.Errorf("saving ai config secret: %w", err)
		}
	}

	slog.Info("AI config saved to K8s Secret", "namespace", s.namespace)
	return nil
}

// LoadJSON reads and decrypts AI config JSON from the K8s Secret.
// Returns nil if the Secret doesn't exist (fall back to env vars).
func (s *AIConfigStore) LoadJSON() (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	secret, err := s.client.CoreV1().Secrets(s.namespace).Get(ctx, aiSecretName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading ai config secret: %w", err)
	}

	encData, ok := secret.Data["config"]
	if !ok || len(encData) == 0 {
		return nil, nil
	}

	plaintext, err := crypto.Decrypt(string(encData), s.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("decrypting ai config: %w", err)
	}

	return json.RawMessage(plaintext), nil
}
