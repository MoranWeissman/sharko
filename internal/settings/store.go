// Package settings persists small, server-wide (not per-cluster) Sharko
// configuration toggles in a ConfigMap via internal/cmstore — the same
// K8s-object-state pattern internal/observations and internal/notifications
// use. It intentionally stays narrow: one key so far (probe_mode,
// V2-cleanup-85.4). Do not grow this into a generic multi-tenant settings
// framework — add new typed getters/setters here the same way GetProbeMode/
// SetProbeMode are added, one setting at a time.
package settings

import (
	"context"
	"fmt"

	"k8s.io/client-go/kubernetes"

	"github.com/MoranWeissman/sharko/internal/cmstore"
)

// Probe mode values (V2-cleanup-85.4). probe_mode controls whether Sharko
// deploys a transient "connectivity-check" ArgoCD Application to newly
// registered clusters with zero enabled addons.
const (
	// ProbeModeCheckApp is the default: Sharko deploys the connectivity-check
	// app so a brand-new, zero-addon cluster proves end-to-end deployability
	// even before the operator enables a real addon.
	ProbeModeCheckApp = "check-app"

	// ProbeModeAPITest disables the connectivity-check app entirely — no
	// app is ever auto-deployed to a new cluster, even transiently.
	// Reachability is derived purely from ArgoCD's own connection state.
	ProbeModeAPITest = "api-test"
)

const (
	configMapName = "sharko-server-settings"
	keyProbeMode  = "probe_mode"
)

// Store persists server-wide settings in a ConfigMap via cmstore.
type Store struct {
	cm *cmstore.Store
}

// NewStore creates a new settings store backed by a ConfigMap named
// "sharko-server-settings" in namespace.
func NewStore(client kubernetes.Interface, namespace string) *Store {
	return &Store{
		cm: cmstore.NewStore(client, namespace, configMapName),
	}
}

// isValidProbeMode reports whether mode is a recognized probe_mode value.
func isValidProbeMode(mode string) bool {
	return mode == ProbeModeCheckApp || mode == ProbeModeAPITest
}

// GetProbeMode returns the persisted probe_mode, defaulting to
// ProbeModeCheckApp when the ConfigMap does not exist yet, the key was
// never set, or the stored value is not recognized (e.g. written by a
// future Sharko version). Never returns an unrecognized value.
func (s *Store) GetProbeMode(ctx context.Context) (string, error) {
	data, err := s.cm.Read(ctx)
	if err != nil {
		return ProbeModeCheckApp, err
	}
	mode, _ := data[keyProbeMode].(string)
	if !isValidProbeMode(mode) {
		return ProbeModeCheckApp, nil
	}
	return mode, nil
}

// SetProbeMode validates and persists mode. Returns an error for any value
// other than ProbeModeCheckApp / ProbeModeAPITest — callers must not write
// unrecognized values into the ConfigMap.
func (s *Store) SetProbeMode(ctx context.Context, mode string) error {
	if !isValidProbeMode(mode) {
		return &InvalidProbeModeError{Value: mode}
	}
	return s.cm.ReadModifyWrite(ctx, func(data map[string]interface{}) error {
		data[keyProbeMode] = mode
		return nil
	})
}

// IsAPITest is a nil-safe, error-swallowing convenience wrapper for
// non-HTTP callers (the register/reconcile paths) that need a plain bool
// and must never let a transient settings-store read failure block cluster
// reconciliation — it falls back to the safe default (check-app / false)
// on any error or when s is nil (settings store not wired, e.g. out-of-
// cluster dev mode).
func (s *Store) IsAPITest(ctx context.Context) bool {
	if s == nil {
		return false
	}
	mode, err := s.GetProbeMode(ctx)
	if err != nil {
		return false
	}
	return mode == ProbeModeAPITest
}

// InvalidProbeModeError is returned by SetProbeMode for an unrecognized
// value. Exported so API handlers can distinguish "bad input" (400) from
// a ConfigMap write failure (500).
type InvalidProbeModeError struct {
	Value string
}

func (e *InvalidProbeModeError) Error() string {
	return fmt.Sprintf("invalid probe_mode %q: must be %q or %q", e.Value, ProbeModeCheckApp, ProbeModeAPITest)
}
