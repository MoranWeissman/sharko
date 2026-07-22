package operator

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	v1alpha1 "github.com/MoranWeissman/sharko/api/v1alpha1"
)

// NewManager creates a new controller-runtime manager for the Sharko operator.
// This is Story 1.2 — the manager boots and shuts down cleanly, but has NO
// reconcilers registered yet (that's Story 1.3). The manager is configured to:
//
//   - Disable the metrics server (BindAddress: "0") to avoid port conflicts with
//     Sharko's existing /metrics endpoint
//   - Disable the health probe listener (HealthProbeBindAddress: "0")
//   - Enable leader election with ID "sharko-operator" in the provided namespace
//
// The caller is responsible for calling mgr.Start(ctx) to run the manager.
// The manager stops when the provided context is canceled.
//
// Parameters:
//   - cfg: the in-cluster REST config (from rest.InClusterConfig())
//   - namespace: the namespace for leader election lease (typically "sharko")
//
// Returns the manager or an error if construction fails (scheme registration
// or manager creation error).
func NewManager(cfg *rest.Config, namespace string) (manager.Manager, error) {
	scheme := runtime.NewScheme()

	// Register client-go's built-in types (core K8s resources like Pod, Service, etc.)
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add client-go scheme: %w", err)
	}

	// Register the sharko.dev/v1alpha1 types (ClusterAddons CRD)
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add sharko v1alpha1 scheme: %w", err)
	}

	// Create the manager with:
	// - Metrics disabled (BindAddress "0") — avoids clash with Sharko's own /metrics
	// - Health probe disabled (HealthProbeBindAddress "0")
	// - Leader election enabled with lease in the specified namespace
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0", // Disable metrics server (docs: set to "0")
		},
		HealthProbeBindAddress: "0", // Disable health probe listener
		LeaderElection:         true,
		LeaderElectionID:       "sharko-operator",
		LeaderElectionNamespace: namespace,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create manager: %w", err)
	}

	return mgr, nil
}
