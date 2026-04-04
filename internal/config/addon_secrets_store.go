package config

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// AddonSecretStore persists addon secret definitions across restarts.
type AddonSecretStore interface {
	Save(defs map[string]orchestrator.AddonSecretDefinition) error
	Load() (map[string]orchestrator.AddonSecretDefinition, error)
}

// NewAddonSecretStore auto-detects the runtime environment and returns the
// appropriate store: K8s ConfigMap when running in-cluster, file-based otherwise.
func NewAddonSecretStore() AddonSecretStore {
	cfg, err := rest.InClusterConfig()
	if err == nil {
		client, err := kubernetes.NewForConfig(cfg)
		if err == nil {
			namespace := detectAddonSecretsNamespace()
			slog.Info("addon secret store initialised in K8s mode", "namespace", namespace)
			return &K8sAddonSecretStore{
				client:    client,
				namespace: namespace,
				cmName:    "sharko-addon-secrets",
			}
		}
	}

	path := "addon-secrets.json"
	slog.Info("addon secret store initialised in file mode", "path", path)
	return &FileAddonSecretStore{path: path}
}

// detectAddonSecretsNamespace returns the Kubernetes namespace from the service
// account file, SHARKO_NAMESPACE env var, or the default "sharko".
func detectAddonSecretsNamespace() string {
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err == nil && len(data) > 0 {
		ns := string(data)
		for len(ns) > 0 && (ns[len(ns)-1] == '\n' || ns[len(ns)-1] == '\r' || ns[len(ns)-1] == ' ') {
			ns = ns[:len(ns)-1]
		}
		return ns
	}
	if ns := os.Getenv("SHARKO_NAMESPACE"); ns != "" {
		return ns
	}
	return "sharko"
}

// ---- K8s implementation ----

// K8sAddonSecretStore persists addon secret definitions in a K8s ConfigMap.
type K8sAddonSecretStore struct {
	client    kubernetes.Interface
	namespace string
	cmName    string
}

func (s *K8sAddonSecretStore) Save(defs map[string]orchestrator.AddonSecretDefinition) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	data, err := json.Marshal(defs)
	if err != nil {
		return fmt.Errorf("marshaling addon secret definitions: %w", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.cmName,
			Namespace: s.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "sharko",
				"app.kubernetes.io/component":  "addon-secrets",
			},
		},
		Data: map[string]string{
			"definitions": string(data),
		},
	}

	_, err = s.client.CoreV1().ConfigMaps(s.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			_, err = s.client.CoreV1().ConfigMaps(s.namespace).Create(ctx, cm, metav1.CreateOptions{})
		}
		if err != nil {
			return fmt.Errorf("saving addon secrets ConfigMap: %w", err)
		}
	}

	slog.Info("addon secret definitions saved to K8s ConfigMap", "count", len(defs))
	return nil
}

func (s *K8sAddonSecretStore) Load() (map[string]orchestrator.AddonSecretDefinition, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cm, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.cmName, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return make(map[string]orchestrator.AddonSecretDefinition), nil
		}
		return nil, fmt.Errorf("reading addon secrets ConfigMap: %w", err)
	}

	raw, ok := cm.Data["definitions"]
	if !ok || raw == "" {
		return make(map[string]orchestrator.AddonSecretDefinition), nil
	}

	var defs map[string]orchestrator.AddonSecretDefinition
	if err := json.Unmarshal([]byte(raw), &defs); err != nil {
		return nil, fmt.Errorf("parsing addon secret definitions: %w", err)
	}

	return defs, nil
}

// ---- File implementation ----

// FileAddonSecretStore persists addon secret definitions in a local JSON file.
type FileAddonSecretStore struct {
	path string
	mu   sync.RWMutex
}

func (s *FileAddonSecretStore) Save(defs map[string]orchestrator.AddonSecretDefinition) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(defs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling addon secret definitions: %w", err)
	}

	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return fmt.Errorf("writing addon secrets file: %w", err)
	}

	return nil
}

func (s *FileAddonSecretStore) Load() (map[string]orchestrator.AddonSecretDefinition, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]orchestrator.AddonSecretDefinition), nil
		}
		return nil, fmt.Errorf("reading addon secrets file: %w", err)
	}

	var defs map[string]orchestrator.AddonSecretDefinition
	if err := json.Unmarshal(data, &defs); err != nil {
		return nil, fmt.Errorf("parsing addon secret definitions: %w", err)
	}

	return defs, nil
}
