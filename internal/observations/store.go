package observations

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/verify"
)

const configMapName = "sharko-cluster-observations"

// Store persists per-cluster observations in a ConfigMap via cmstore.
type Store struct {
	cm *cmstore.Store
}

// NewStore creates a new observations store backed by a ConfigMap.
func NewStore(client kubernetes.Interface, namespace string) *Store {
	return &Store{
		cm: cmstore.NewStore(client, namespace, configMapName),
	}
}

// RecordTestResult writes the outcome of a connectivity test for a cluster.
func (s *Store) RecordTestResult(ctx context.Context, clusterName string, result verify.Result) error {
	return s.cm.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		clustersRaw, ok := data["clusters"]
		if !ok {
			clustersRaw = map[string]interface{}{}
		}
		clusters, ok := clustersRaw.(map[string]interface{})
		if !ok {
			clusters = map[string]interface{}{}
		}

		now := time.Now().UTC()
		outcome := "success"
		if !result.Success {
			outcome = "failure"
		}

		obs := Observation{
			LastTestAt:           now,
			LastTestStage:        result.Stage,
			LastTestOutcome:      outcome,
			LastTestErrorCode:    string(result.ErrorCode),
			LastTestErrorMessage: result.ErrorMessage,
			LastTestDurationMs:   result.DurationMs,
			LastSeenAt:           now,
		}

		// Marshal to map[string]interface{} for cmstore compatibility.
		obsBytes, err := json.Marshal(obs)
		if err != nil {
			return fmt.Errorf("marshal observation: %w", err)
		}
		var obsMap map[string]interface{}
		if err := json.Unmarshal(obsBytes, &obsMap); err != nil {
			return fmt.Errorf("unmarshal observation to map: %w", err)
		}

		clusters[clusterName] = obsMap
		data["clusters"] = clusters

		slog.Debug("recorded cluster observation", "cluster", clusterName, "outcome", outcome, "stage", result.Stage)
		return nil
	})
}

// GetObservation reads the observation for a single cluster.
// Returns nil if the cluster has no recorded observation.
func (s *Store) GetObservation(ctx context.Context, clusterName string) (*Observation, error) {
	data, err := s.cm.Read(ctx)
	if err != nil {
		return nil, err
	}

	return extractObservation(data, clusterName)
}

// ListObservations returns all recorded cluster observations.
func (s *Store) ListObservations(ctx context.Context) (map[string]*Observation, error) {
	data, err := s.cm.Read(ctx)
	if err != nil {
		return nil, err
	}

	clustersRaw, ok := data["clusters"]
	if !ok {
		return map[string]*Observation{}, nil
	}
	clusters, ok := clustersRaw.(map[string]interface{})
	if !ok {
		return map[string]*Observation{}, nil
	}

	result := make(map[string]*Observation, len(clusters))
	for name := range clusters {
		obs, err := extractObservation(data, name)
		if err != nil {
			slog.Warn("skipping malformed observation", "cluster", name, "error", err)
			continue
		}
		if obs != nil {
			result[name] = obs
		}
	}
	return result, nil
}

// extractObservation reads a single cluster observation from the top-level data map.
func extractObservation(data map[string]interface{}, clusterName string) (*Observation, error) {
	clustersRaw, ok := data["clusters"]
	if !ok {
		return nil, nil
	}
	clusters, ok := clustersRaw.(map[string]interface{})
	if !ok {
		return nil, nil
	}
	obsRaw, ok := clusters[clusterName]
	if !ok {
		return nil, nil
	}

	obsBytes, err := json.Marshal(obsRaw)
	if err != nil {
		return nil, fmt.Errorf("marshal observation for %s: %w", clusterName, err)
	}
	var obs Observation
	if err := json.Unmarshal(obsBytes, &obs); err != nil {
		return nil, fmt.Errorf("unmarshal observation for %s: %w", clusterName, err)
	}
	return &obs, nil
}
