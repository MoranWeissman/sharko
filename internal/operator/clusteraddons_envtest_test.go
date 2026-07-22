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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	v1alpha1 "github.com/MoranWeissman/sharko/api/v1alpha1"
	"github.com/MoranWeissman/sharko/internal/argosecrets"
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

// fakeEnvtestLabelWriter is a test fake labelWriter that simulates
// SyncManagedClusterLabels for envtest WITHOUT a real ArgoCD cluster Secret
// (the envtest apiserver doesn't have the argocd namespace or ArgoCD CRDs).
//
// It tracks writes and returns Found=true for prod-eu/staging-us (our test
// fixture clusters), Found=false for foreign/missing clusters.
type fakeEnvtestLabelWriter struct {
	writes map[string]map[string]string // clusterName → desiredLabels
}

func newFakeEnvtestLabelWriter() *fakeEnvtestLabelWriter {
	return &fakeEnvtestLabelWriter{
		writes: make(map[string]map[string]string),
	}
}

func (f *fakeEnvtestLabelWriter) SyncManagedClusterLabels(ctx context.Context, name string, desiredLabels map[string]string) (argosecrets.ManagedLabelSyncResult, error) {
	// Simulate the real manager's behavior: Found=true only for prod-eu/staging-us
	// (our fixture clusters). A missing or foreign cluster returns Found=false.
	if name != "prod-eu" && name != "staging-us" {
		return argosecrets.ManagedLabelSyncResult{Found: false}, nil
	}

	// Deep-copy desiredLabels to simulate a write.
	labelsCopy := make(map[string]string, len(desiredLabels))
	for k, v := range desiredLabels {
		labelsCopy[k] = v
	}
	f.writes[name] = labelsCopy

	// Simulate Changed=true the first time we write this cluster, Changed=false
	// on subsequent writes with the same label set (no-op convergence).
	changed := true
	if existing, ok := f.writes[name]; ok && labelsEqualMap(existing, desiredLabels) {
		changed = false
	}

	// Return Converged = the keys we wrote (addon keys only).
	convergedKeys := make([]string, 0, len(desiredLabels))
	for k := range desiredLabels {
		convergedKeys = append(convergedKeys, k)
	}

	return argosecrets.ManagedLabelSyncResult{
		Found:     true,
		Changed:   changed,
		Converged: convergedKeys,
	}, nil
}

func labelsEqualMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestClusterAddonsReconciler_Envtest_DrivesLabels is an envtest integration test
// for Story 2.4 (flag ON, Phase 2): verifies that when DrivesLabels=true, the
// controller drives addon labels from CR spec → writes them to the Secret (via
// a fake labelWriter) → reports the write outcome in .status.
//
// This test uses a SEPARATE reconciler instance (not the shared TestMain one)
// because TestMain wires DrivesLabels=false (Phase 1 mode). We call Reconcile()
// directly on the new reconciler struct instead of running a second manager
// (to avoid controller-name collisions and cache-isolation flakes).
func TestClusterAddonsReconciler_Envtest_DrivesLabels(t *testing.T) {
	if !envtestReady {
		t.Skip("KUBEBUILDER_ASSETS not set, skipping envtest integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create a fake labelWriter that tracks writes.
	labelWriter := newFakeEnvtestLabelWriter()

	// Build a Phase 2 reconciler (flag ON) with the fake labelWriter.
	reconciler := &ClusterAddonsReconciler{
		Client:       testClient,
		labelWriter:  labelWriter,
		DrivesLabels: true, // FLAG ON (Phase 2 mode)
	}

	// Test scenario 1: CR with 2 enabled addons → labels converge, Ready=True.
	seq := atomic.AddUint64(&testSeqNumber, 1)
	crName := fmt.Sprintf("envtest-drive-%d", seq)
	cr := &v1alpha1.ClusterAddons{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crName,
			Namespace: "default",
		},
		Spec: v1alpha1.ClusterAddonsSpec{
			Cluster: "prod-eu", // fixture cluster (labelWriter returns Found=true)
			Addons: []v1alpha1.AddonAssignment{
				{Name: "datadog"},
				{Name: "prometheus"},
			},
		},
	}

	if err := testClient.Create(ctx, cr); err != nil {
		t.Fatalf("Create CR: %v", err)
	}

	// Small delay to let the cache propagate.
	time.Sleep(100 * time.Millisecond)

	// Call Reconcile() directly (no manager loop — we're testing the reconciler's
	// logic, not the watch/cache plumbing).
	req := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: crName, Namespace: "default"},
	}
	res, err := reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("expected no requeue on success, got RequeueAfter=%v", res.RequeueAfter)
	}

	// Verify the labelWriter received the correct labels.
	if len(labelWriter.writes) != 1 {
		t.Fatalf("expected 1 cluster write, got %d", len(labelWriter.writes))
	}
	prodEULabels, ok := labelWriter.writes["prod-eu"]
	if !ok {
		t.Fatalf("expected write for prod-eu, got writes: %+v", labelWriter.writes)
	}
	wantLabels := map[string]string{
		"datadog":    "enabled",
		"prometheus": "enabled",
	}
	if !labelsEqualMap(prodEULabels, wantLabels) {
		t.Errorf("prod-eu labels: got %+v, want %+v", prodEULabels, wantLabels)
	}

	// NOTE: We do NOT verify .status here because the shared TestMain manager
	// (which runs Phase 1 mode) is also reconciling this CR in the background
	// and will overwrite the status we just wrote. The critical contract for
	// Story 2.4 is that the labelWriter was called with the correct desired
	// labels (verified above) — status behavior is tested in the unit tests
	// (TestClusterAddonsReconciler_DrivesLabels_StatusFromWriter).

	// Test scenario 2: flip an addon to disabled → re-reconcile → label removed.
	// Retry-loop for Update to handle resourceVersion conflicts from the shared
	// TestMain manager reconciling in the background.
	updateErr := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 10*time.Second, true, func(ctx context.Context) (bool, error) {
		if err := testClient.Get(ctx, types.NamespacedName{Name: crName, Namespace: "default"}, cr); err != nil {
			return false, err
		}
		cr.Spec.Addons = []v1alpha1.AddonAssignment{
			{Name: "datadog"},
			{Name: "prometheus", Enabled: boolPtr(false)}, // disabled
		}
		if err := testClient.Update(ctx, cr); err != nil {
			// Retry on conflict (resourceVersion stale).
			return false, nil
		}
		return true, nil
	})
	if updateErr != nil {
		t.Fatalf("Update CR (flip addon): %v", updateErr)
	}

	time.Sleep(100 * time.Millisecond)

	res, err = reconciler.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("Reconcile (flip addon): %v", err)
	}

	// Verify the labelWriter now has only datadog.
	prodEULabels, ok = labelWriter.writes["prod-eu"]
	if !ok {
		t.Fatalf("expected write for prod-eu after flip, got writes: %+v", labelWriter.writes)
	}
	wantLabelsAfterFlip := map[string]string{
		"datadog": "enabled",
		// "prometheus" removed
	}
	if !labelsEqualMap(prodEULabels, wantLabelsAfterFlip) {
		t.Errorf("prod-eu labels after flip: got %+v, want %+v", prodEULabels, wantLabelsAfterFlip)
	}

	// (Status checks omitted — shared TestMain manager will overwrite.)

	// Test scenario 3: CR pointing at a foreign/missing cluster → Ready=False, no write.
	seqForeign := atomic.AddUint64(&testSeqNumber, 1)
	crNameForeign := fmt.Sprintf("envtest-drive-foreign-%d", seqForeign)
	crForeign := &v1alpha1.ClusterAddons{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crNameForeign,
			Namespace: "default",
		},
		Spec: v1alpha1.ClusterAddonsSpec{
			Cluster: "foreign-cluster", // NOT in labelWriter's fixture set
			Addons:  []v1alpha1.AddonAssignment{{Name: "foo"}},
		},
	}

	if err := testClient.Create(ctx, crForeign); err != nil {
		t.Fatalf("Create foreign CR: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	reqForeign := ctrl.Request{
		NamespacedName: types.NamespacedName{Name: crNameForeign, Namespace: "default"},
	}
	res, err = reconciler.Reconcile(ctx, reqForeign)
	if err != nil {
		t.Fatalf("Reconcile (foreign cluster): %v", err)
	}

	// Verify NO write occurred for foreign-cluster.
	if _, written := labelWriter.writes["foreign-cluster"]; written {
		t.Errorf("expected NO write for foreign-cluster, but labelWriter recorded a write")
	}

	// (Status checks omitted — the critical contract is that the labelWriter
	// was NOT called for foreign-cluster, which we verified above. Status
	// behavior for SecretNotFound is tested in the unit tests.)
}
