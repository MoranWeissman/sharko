package cmstore

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	stateKey         = "state"
	sizeWarningBytes = 800 * 1024 // 800KB
)

// Store is a reusable ConfigMap-based JSON state store.
// It serializes read-modify-write cycles with an in-process mutex.
type Store struct {
	client    kubernetes.Interface
	namespace string
	name      string
	mu        sync.Mutex
}

// NewStore creates a new ConfigMap state store.
func NewStore(client kubernetes.Interface, namespace, name string) *Store {
	return &Store{
		client:    client,
		namespace: namespace,
		name:      name,
	}
}

// ReadModifyWrite reads the ConfigMap state, applies modifyFn, and writes back.
// If the ConfigMap does not exist, it is created with version=1.
// The operation is serialized via an in-process mutex.
func (s *Store) ReadModifyWrite(ctx context.Context, modifyFn func(data map[string]interface{}) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Read existing ConfigMap
	cm, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})

	var data map[string]interface{}
	notFound := apierrors.IsNotFound(err)

	if notFound {
		data = map[string]interface{}{"version": float64(1)}
	} else if err != nil {
		return fmt.Errorf("read configmap %s: %w", s.name, err)
	} else {
		raw, ok := cm.Data[stateKey]
		if !ok || raw == "" {
			data = map[string]interface{}{"version": float64(1)}
		} else if err := json.Unmarshal([]byte(raw), &data); err != nil {
			data = map[string]interface{}{"version": float64(1)}
		}
	}

	// Apply modification
	if err := modifyFn(data); err != nil {
		return err
	}

	// Marshal state
	stateJSON, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	// Size warning
	if len(stateJSON) > sizeWarningBytes {
		slog.Warn("configmap approaching size limit",
			"name", s.name,
			"namespace", s.namespace,
			"size_bytes", len(stateJSON),
		)
	}

	// Write back
	if notFound || cm == nil {
		_, err = s.client.CoreV1().ConfigMaps(s.namespace).Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      s.name,
				Namespace: s.namespace,
			},
			Data: map[string]string{stateKey: string(stateJSON)},
		}, metav1.CreateOptions{})
	} else {
		cm.Data = map[string]string{stateKey: string(stateJSON)}
		_, err = s.client.CoreV1().ConfigMaps(s.namespace).Update(ctx, cm, metav1.UpdateOptions{})
	}
	if err != nil {
		return fmt.Errorf("write configmap %s: %w", s.name, err)
	}

	return nil
}

// Read returns the current state from the ConfigMap.
// If the ConfigMap does not exist or has no state, an empty map is returned.
func (s *Store) Read(ctx context.Context) (map[string]interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cm, err := s.client.CoreV1().ConfigMaps(s.namespace).Get(ctx, s.name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return map[string]interface{}{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read configmap %s: %w", s.name, err)
	}

	raw, ok := cm.Data[stateKey]
	if !ok || raw == "" {
		return map[string]interface{}{}, nil
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, fmt.Errorf("unmarshal state from configmap %s: %w", s.name, err)
	}

	return data, nil
}
