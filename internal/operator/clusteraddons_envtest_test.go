package operator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	v1alpha1 "github.com/MoranWeissman/sharko/api/v1alpha1"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
)

// TestClusterAddonsReconciler_Envtest is an integration test that spins up a
// real API server via envtest, installs the ClusterAddons CRD, creates a CR,
// and verifies the controller correctly updates .status by polling until convergence.
//
// This test SKIPS cleanly if KUBEBUILDER_ASSETS is not set (CI-honest behavior).
func TestClusterAddonsReconciler_Envtest(t *testing.T) {
	// Skip if KUBEBUILDER_ASSETS is not set (envtest binaries not provisioned).
	if _, ok := os.LookupEnv("KUBEBUILDER_ASSETS"); !ok {
		t.Skip("KUBEBUILDER_ASSETS not set, skipping envtest integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Build the scheme: client-go + v1alpha1 CRDs.
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme clientgoscheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme v1alpha1: %v", err)
	}

	// Set up envtest.Environment with CRD directory.
	// CRD lives at config/crd/sharko.dev_clusteraddons.yaml relative to repo root.
	// This test file is at internal/operator/, so ../../config/crd.
	crdPath := filepath.Join("..", "..", "config", "crd")
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: true,
		Scheme:                scheme,
	}

	// Start envtest control plane.
	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("envtest.Start: %v", err)
	}
	defer func() {
		if stopErr := testEnv.Stop(); stopErr != nil {
			t.Logf("envtest.Stop: %v", stopErr)
		}
	}()

	// Build the fake status reader that returns a success outcome.
	statusReader := &fakeStatusReader{
		records: map[string]clusterreconciler.ClusterReconcileRecord{
			"prod-eu": {
				Time:    time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
				Outcome: clusterreconciler.OutcomeSucceeded,
				Message: "",
			},
		},
	}

	// Create a manager.
	mgr, err := manager.New(cfg, manager.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0", // disable metrics server
		},
	})
	if err != nil {
		t.Fatalf("manager.New: %v", err)
	}

	// Set up the ClusterAddonsReconciler.
	reconciler := &ClusterAddonsReconciler{
		Client:       mgr.GetClient(),
		statusReader: statusReader,
	}
	if err := reconciler.SetupWithManager(mgr, statusReader); err != nil {
		t.Fatalf("SetupWithManager: %v", err)
	}

	// Start the manager in a goroutine.
	mgrCtx, mgrCancel := context.WithCancel(ctx)
	defer mgrCancel()
	go func() {
		if startErr := mgr.Start(mgrCtx); startErr != nil {
			t.Logf("mgr.Start: %v", startErr)
		}
	}()

	// Wait for manager cache to sync.
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("failed to wait for cache sync")
	}

	// Create a ClusterAddons CR.
	cr := &v1alpha1.ClusterAddons{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: v1alpha1.ClusterAddonsSpec{
			Cluster: "prod-eu",
			Addons: []v1alpha1.AddonAssignment{
				{Name: "datadog"},
				{Name: "prometheus"},
			},
		},
	}

	if err := mgr.GetClient().Create(ctx, cr); err != nil {
		t.Fatalf("Create CR: %v", err)
	}

	// Poll until .status converges (observedGeneration == generation and Ready=True).
	// The controller requeues every 60s, but the first reconcile should happen quickly.
	// Use a 30s polling timeout (plenty of time for a local envtest).
	pollErr := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		var updated v1alpha1.ClusterAddons
		getErr := mgr.GetClient().Get(ctx, types.NamespacedName{Name: "test-cluster", Namespace: "default"}, &updated)
		if getErr != nil {
			return false, getErr
		}

		// Check convergence: observedGeneration == generation.
		if updated.Status.ObservedGeneration != updated.Generation {
			return false, nil // not converged yet
		}

		// Check SyncedAddons = 2 (both addons enabled).
		if updated.Status.SyncedAddons != 2 {
			return false, nil
		}

		// Check Ready condition = True.
		if len(updated.Status.Conditions) == 0 {
			return false, nil
		}
		readyCond := updated.Status.Conditions[0]
		if readyCond.Type != "Ready" || readyCond.Status != metav1.ConditionTrue {
			return false, nil
		}

		return true, nil
	})

	if pollErr != nil {
		t.Fatalf("status did not converge: %v", pollErr)
	}

	// Verify final state.
	var final v1alpha1.ClusterAddons
	if err := mgr.GetClient().Get(ctx, types.NamespacedName{Name: "test-cluster", Namespace: "default"}, &final); err != nil {
		t.Fatalf("Get final CR: %v", err)
	}

	if final.Status.ObservedGeneration != final.Generation {
		t.Errorf("ObservedGeneration: got %d, want %d", final.Status.ObservedGeneration, final.Generation)
	}

	if final.Status.SyncedAddons != 2 {
		t.Errorf("SyncedAddons: got %d, want 2", final.Status.SyncedAddons)
	}

	if len(final.Status.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(final.Status.Conditions))
	}

	readyCond := final.Status.Conditions[0]
	if readyCond.Type != "Ready" {
		t.Errorf("condition type: got %s, want Ready", readyCond.Type)
	}
	if readyCond.Status != metav1.ConditionTrue {
		t.Errorf("condition status: got %s, want True", readyCond.Status)
	}
	if readyCond.Reason != "ReconcileSucceeded" {
		t.Errorf("condition reason: got %s, want ReconcileSucceeded", readyCond.Reason)
	}
}

// TestClusterAddonsReconciler_Envtest_CRDValidation tests that the API server
// rejects a malformed ClusterAddons CR (missing required spec.cluster field).
func TestClusterAddonsReconciler_Envtest_CRDValidation(t *testing.T) {
	// Skip if KUBEBUILDER_ASSETS is not set.
	if _, ok := os.LookupEnv("KUBEBUILDER_ASSETS"); !ok {
		t.Skip("KUBEBUILDER_ASSETS not set, skipping envtest integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build the scheme.
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme clientgoscheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme v1alpha1: %v", err)
	}

	// Set up envtest.
	crdPath := filepath.Join("..", "..", "config", "crd")
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: true,
		Scheme:                scheme,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("envtest.Start: %v", err)
	}
	defer func() {
		if stopErr := testEnv.Stop(); stopErr != nil {
			t.Logf("envtest.Stop: %v", stopErr)
		}
	}()

	// Create a client (no manager needed for this validation-only test).
	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}

	// Try to create a CR with an empty spec.cluster (violates MinLength=1).
	malformedCR := &v1alpha1.ClusterAddons{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "malformed",
			Namespace: "default",
		},
		Spec: v1alpha1.ClusterAddonsSpec{
			Cluster: "", // INVALID: spec.cluster is required + minLength=1
			Addons:  []v1alpha1.AddonAssignment{{Name: "foo"}},
		},
	}

	err = k8sClient.Create(ctx, malformedCR)
	if err == nil {
		t.Fatal("expected Create to fail for malformed CR, but it succeeded")
	}

	// Verify the error is a validation error (not a network error, etc.).
	// The API server should reject it with a 422 Invalid or similar.
	// We check that the error message contains "cluster" or "required" or "invalid".
	errMsg := err.Error()
	if !contains(errMsg, "cluster") && !contains(errMsg, "required") && !contains(errMsg, "invalid") {
		t.Errorf("expected validation error mentioning 'cluster' or 'required', got: %v", err)
	}
}

// TestClusterAddonsReconciler_Envtest_FailedOutcome tests that a failed
// reconcile outcome produces Ready=False.
func TestClusterAddonsReconciler_Envtest_FailedOutcome(t *testing.T) {
	// Skip if KUBEBUILDER_ASSETS is not set.
	if _, ok := os.LookupEnv("KUBEBUILDER_ASSETS"); !ok {
		t.Skip("KUBEBUILDER_ASSETS not set, skipping envtest integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme clientgoscheme: %v", err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme v1alpha1: %v", err)
	}

	crdPath := filepath.Join("..", "..", "config", "crd")
	testEnv := &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
		ErrorIfCRDPathMissing: true,
		Scheme:                scheme,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("envtest.Start: %v", err)
	}
	defer func() {
		if stopErr := testEnv.Stop(); stopErr != nil {
			t.Logf("envtest.Stop: %v", stopErr)
		}
	}()

	// Build a status reader that returns OutcomeFailed.
	statusReader := &fakeStatusReader{
		records: map[string]clusterreconciler.ClusterReconcileRecord{
			"staging-us": {
				Time:    time.Now(),
				Outcome: clusterreconciler.OutcomeFailed,
				Message: "vault credential fetch failed",
			},
		},
	}

	mgr, err := manager.New(cfg, manager.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
	})
	if err != nil {
		t.Fatalf("manager.New: %v", err)
	}

	reconciler := &ClusterAddonsReconciler{
		Client:       mgr.GetClient(),
		statusReader: statusReader,
	}
	if err := reconciler.SetupWithManager(mgr, statusReader); err != nil {
		t.Fatalf("SetupWithManager: %v", err)
	}

	mgrCtx, mgrCancel := context.WithCancel(ctx)
	defer mgrCancel()
	go func() {
		if startErr := mgr.Start(mgrCtx); startErr != nil {
			t.Logf("mgr.Start: %v", startErr)
		}
	}()

	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("failed to wait for cache sync")
	}

	cr := &v1alpha1.ClusterAddons{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-failed",
			Namespace: "default",
		},
		Spec: v1alpha1.ClusterAddonsSpec{
			Cluster: "staging-us",
			Addons:  []v1alpha1.AddonAssignment{{Name: "foo"}},
		},
	}

	if err := mgr.GetClient().Create(ctx, cr); err != nil {
		t.Fatalf("Create CR: %v", err)
	}

	// Poll until status converges with Ready=False.
	pollErr := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		var updated v1alpha1.ClusterAddons
		getErr := mgr.GetClient().Get(ctx, types.NamespacedName{Name: "test-failed", Namespace: "default"}, &updated)
		if getErr != nil {
			return false, getErr
		}

		if updated.Status.ObservedGeneration != updated.Generation {
			return false, nil
		}

		if len(updated.Status.Conditions) == 0 {
			return false, nil
		}

		readyCond := updated.Status.Conditions[0]
		if readyCond.Type == "Ready" && readyCond.Status == metav1.ConditionFalse {
			return true, nil
		}

		return false, nil
	})

	if pollErr != nil {
		t.Fatalf("status did not converge to Ready=False: %v", pollErr)
	}

	// Verify final state.
	var final v1alpha1.ClusterAddons
	if err := mgr.GetClient().Get(ctx, types.NamespacedName{Name: "test-failed", Namespace: "default"}, &final); err != nil {
		t.Fatalf("Get final CR: %v", err)
	}

	if final.Status.SyncedAddons != 0 {
		t.Errorf("SyncedAddons: got %d, want 0 (failed outcome)", final.Status.SyncedAddons)
	}

	if len(final.Status.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(final.Status.Conditions))
	}

	readyCond := final.Status.Conditions[0]
	if readyCond.Status != metav1.ConditionFalse {
		t.Errorf("condition status: got %s, want False", readyCond.Status)
	}
	if readyCond.Reason != "ReconcileFailed" {
		t.Errorf("condition reason: got %s, want ReconcileFailed", readyCond.Reason)
	}
	if readyCond.Message != "vault credential fetch failed" {
		t.Errorf("condition message: got %q, want %q", readyCond.Message, "vault credential fetch failed")
	}
}

// contains is a helper for substring matching in error messages.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
