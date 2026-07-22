package operator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
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

// Package-level shared envtest environment, manager, and client.
// TestMain sets these up once; all tests share them.
var (
	testEnv       *envtest.Environment
	testMgr       manager.Manager
	testClient    client.Client
	testCtx       context.Context
	testCancel    context.CancelFunc
	envtestReady  bool   // true if TestMain succeeded; tests skip if false
	testSeqNumber uint64 // atomic counter for unique CR names across parallel/repeat runs
)

// TestMain sets up a single shared envtest apiserver + manager for all tests.
// This avoids controller-name and cache-isolation flakes from spinning up
// multiple managers per process.
func TestMain(m *testing.M) {
	// Check if KUBEBUILDER_ASSETS is set; if not, skip envtest suite cleanly.
	if _, ok := os.LookupEnv("KUBEBUILDER_ASSETS"); !ok {
		// Assets absent — tests will skip individually. Don't try to start envtest.
		os.Exit(m.Run())
	}

	// Build the scheme: client-go + v1alpha1 CRDs.
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}

	// Set up envtest.Environment with CRD directory.
	crdPath := filepath.Join("..", "..", "config", "crd")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:      []string{crdPath},
		ErrorIfCRDPathMissing:  true,
		Scheme:                 scheme,
		BinaryAssetsDirectory:  os.Getenv("KUBEBUILDER_ASSETS"), // explicitly set from env
	}

	// Start envtest control plane.
	cfg, err := testEnv.Start()
	if err != nil {
		panic(err)
	}

	// Build the shared fake status reader that serves BOTH test scenarios:
	// "prod-eu" → success, "staging-us" → failed.
	sharedStatusReader := &fakeStatusReader{
		records: map[string]clusterreconciler.ClusterReconcileRecord{
			"prod-eu": {
				Time:    time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
				Outcome: clusterreconciler.OutcomeSucceeded,
				Message: "",
			},
			"staging-us": {
				Time:    time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
				Outcome: clusterreconciler.OutcomeFailed,
				Message: "vault credential fetch failed",
			},
		},
	}

	// Create a single manager.
	testMgr, err = manager.New(cfg, manager.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0", // disable metrics server
		},
	})
	if err != nil {
		_ = testEnv.Stop()
		panic(err)
	}

	// Set up the ClusterAddonsReconciler ONCE. Pass nil labelWriter and
	// DrivesLabels=false (default Phase 1 mode) — Phase 1 tests use this setup.
	reconciler := &ClusterAddonsReconciler{
		Client:       testMgr.GetClient(),
		statusReader: sharedStatusReader,
		DrivesLabels: false, // Phase 1 (flag OFF, default)
	}
	if err := reconciler.SetupWithManager(testMgr, sharedStatusReader, nil); err != nil {
		_ = testEnv.Stop()
		panic(err)
	}

	// Start the manager in a goroutine.
	testCtx, testCancel = context.WithCancel(context.Background())
	go func() {
		if startErr := testMgr.Start(testCtx); startErr != nil {
			// Manager stopped — expected on testCancel().
		}
	}()

	// Wait for manager cache to sync.
	syncCtx, syncCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer syncCancel()
	if !testMgr.GetCache().WaitForCacheSync(syncCtx) {
		testCancel()
		_ = testEnv.Stop()
		panic("cache sync failed")
	}

	// Give the controller a moment to fully initialize after cache sync.
	// Without this, the first test may hit the controller before it's ready.
	time.Sleep(500 * time.Millisecond)

	testClient = testMgr.GetClient()
	envtestReady = true

	// Run tests.
	exitCode := m.Run()

	// Teardown.
	testCancel()
	if stopErr := testEnv.Stop(); stopErr != nil {
		panic(stopErr)
	}

	os.Exit(exitCode)
}

// TestClusterAddonsReconciler_Envtest is an integration test that verifies
// the controller correctly updates .status by polling until convergence.
// Uses the shared envtest apiserver + manager from TestMain.
func TestClusterAddonsReconciler_Envtest(t *testing.T) {
	if !envtestReady {
		t.Skip("KUBEBUILDER_ASSETS not set, skipping envtest integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create a ClusterAddons CR with a UNIQUE name (envtest-success-<seq>).
	seq := atomic.AddUint64(&testSeqNumber, 1)
	crName := fmt.Sprintf("envtest-success-%d", seq)
	cr := &v1alpha1.ClusterAddons{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crName,
			Namespace: "default",
		},
		Spec: v1alpha1.ClusterAddonsSpec{
			Cluster: "prod-eu", // sharedStatusReader serves this with OutcomeSucceeded
			Addons: []v1alpha1.AddonAssignment{
				{Name: "datadog"},
				{Name: "prometheus"},
			},
		},
	}

	if err := testClient.Create(ctx, cr); err != nil {
		t.Fatalf("Create CR: %v", err)
	}
	// Note: cleanup removed — with shared envtest, leftover CRs are acceptable.
	// Each test uses a unique sequence number, so no collision.

	// Small delay to let the cache propagate the newly created CR.
	time.Sleep(100 * time.Millisecond)

	// Poll until .status converges (observedGeneration == generation and Ready=True).
	pollErr := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		var updated v1alpha1.ClusterAddons
		getErr := testClient.Get(ctx, types.NamespacedName{Name: crName, Namespace: "default"}, &updated)
		if getErr != nil {
			return false, getErr
		}

		if updated.Status.ObservedGeneration != updated.Generation {
			return false, nil
		}

		if updated.Status.SyncedAddons != 2 {
			return false, nil
		}

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
	if err := testClient.Get(ctx, types.NamespacedName{Name: crName, Namespace: "default"}, &final); err != nil {
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
// Uses the shared envtest apiserver from TestMain.
func TestClusterAddonsReconciler_Envtest_CRDValidation(t *testing.T) {
	if !envtestReady {
		t.Skip("KUBEBUILDER_ASSETS not set, skipping envtest integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

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

	err := testClient.Create(ctx, malformedCR)
	if err == nil {
		t.Fatal("expected Create to fail for malformed CR, but it succeeded")
	}

	// Verify the error is a validation error.
	errMsg := err.Error()
	if !contains(errMsg, "cluster") && !contains(errMsg, "required") && !contains(errMsg, "invalid") {
		t.Errorf("expected validation error mentioning 'cluster' or 'required', got: %v", err)
	}
}

// TestClusterAddonsReconciler_Envtest_FailedOutcome tests that a failed
// reconcile outcome produces Ready=False.
// Uses the shared envtest apiserver + manager from TestMain.
func TestClusterAddonsReconciler_Envtest_FailedOutcome(t *testing.T) {
	if !envtestReady {
		t.Skip("KUBEBUILDER_ASSETS not set, skipping envtest integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create a CR with a UNIQUE name (envtest-failed-<seq>) that uses "staging-us".
	seq := atomic.AddUint64(&testSeqNumber, 1)
	crName := fmt.Sprintf("envtest-failed-%d", seq)
	cr := &v1alpha1.ClusterAddons{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crName,
			Namespace: "default",
		},
		Spec: v1alpha1.ClusterAddonsSpec{
			Cluster: "staging-us", // sharedStatusReader serves this with OutcomeFailed
			Addons:  []v1alpha1.AddonAssignment{{Name: "foo"}},
		},
	}

	if err := testClient.Create(ctx, cr); err != nil {
		t.Fatalf("Create CR: %v", err)
	}
	// Note: cleanup removed — with shared envtest, leftover CRs are acceptable.
	// Each test uses a unique sequence number, so no collision.

	// Small delay to let the cache propagate the newly created CR.
	time.Sleep(100 * time.Millisecond)

	// Poll until status converges with Ready=False.
	pollErr := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		var updated v1alpha1.ClusterAddons
		getErr := testClient.Get(ctx, types.NamespacedName{Name: crName, Namespace: "default"}, &updated)
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
	if err := testClient.Get(ctx, types.NamespacedName{Name: crName, Namespace: "default"}, &final); err != nil {
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
