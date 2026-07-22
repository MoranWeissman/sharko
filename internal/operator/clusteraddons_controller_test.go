package operator

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/MoranWeissman/sharko/api/v1alpha1"
	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
)

// fakeStatusReader is a test fake that returns a fixed ClusterReconcileRecord
// for a given cluster name.
type fakeStatusReader struct {
	records map[string]clusterreconciler.ClusterReconcileRecord
}

func (f *fakeStatusReader) LastReconcile(name string) (clusterreconciler.ClusterReconcileRecord, bool) {
	rec, ok := f.records[name]
	return rec, ok
}

// boolPtr is a test helper to create a pointer to a bool.
func boolPtr(b bool) *bool {
	return &b
}

// TestClusterAddonsReconciler_StatusMapping is a table-driven test that verifies
// the controller correctly maps each reconcile outcome to the appropriate .status fields.
func TestClusterAddonsReconciler_StatusMapping(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	baseTime := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name              string
		outcome           clusterreconciler.ReconcileOutcome
		message           string
		hasRecord         bool
		enabledAddons     []v1alpha1.AddonAssignment
		wantSyncedAddons  int
		wantReadyStatus   metav1.ConditionStatus
		wantReadyReason   string
		wantReadyMessage  string
	}{
		{
			name:             "succeeded outcome with 2 enabled addons",
			outcome:          clusterreconciler.OutcomeSucceeded,
			message:          "",
			hasRecord:        true,
			enabledAddons:    []v1alpha1.AddonAssignment{{Name: "foo"}, {Name: "bar"}},
			wantSyncedAddons: 2,
			wantReadyStatus:  metav1.ConditionTrue,
			wantReadyReason:  "ReconcileSucceeded",
			wantReadyMessage: "cluster addons are in sync with managed-clusters.yaml",
		},
		{
			name:             "succeeded with 1 enabled, 1 disabled",
			outcome:          clusterreconciler.OutcomeSucceeded,
			message:          "",
			hasRecord:        true,
			enabledAddons:    []v1alpha1.AddonAssignment{{Name: "foo"}, {Name: "bar", Enabled: boolPtr(false)}},
			wantSyncedAddons: 1,
			wantReadyStatus:  metav1.ConditionTrue,
			wantReadyReason:  "ReconcileSucceeded",
			wantReadyMessage: "cluster addons are in sync with managed-clusters.yaml",
		},
		{
			name:             "failed outcome",
			outcome:          clusterreconciler.OutcomeFailed,
			message:          "vault credential fetch failed",
			hasRecord:        true,
			enabledAddons:    []v1alpha1.AddonAssignment{{Name: "foo"}},
			wantSyncedAddons: 0,
			wantReadyStatus:  metav1.ConditionFalse,
			wantReadyReason:  "ReconcileFailed",
			wantReadyMessage: "vault credential fetch failed",
		},
		{
			name:             "skipped outcome",
			outcome:          clusterreconciler.OutcomeSkipped,
			message:          "self-managed connection Secret not created yet",
			hasRecord:        true,
			enabledAddons:    []v1alpha1.AddonAssignment{{Name: "foo"}},
			wantSyncedAddons: 0,
			wantReadyStatus:  metav1.ConditionUnknown,
			wantReadyReason:  "ReconcileSkipped",
			wantReadyMessage: "self-managed connection Secret not created yet",
		},
		{
			name:             "no record yet (first reconcile)",
			outcome:          "",
			message:          "",
			hasRecord:        false,
			enabledAddons:    []v1alpha1.AddonAssignment{{Name: "foo"}},
			wantSyncedAddons: 0,
			wantReadyStatus:  metav1.ConditionUnknown,
			wantReadyReason:  "NoReconcileRecord",
			wantReadyMessage: "cluster has not been reconciled yet (waiting for canonical reconciler)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build the ClusterAddons CR.
			cr := &v1alpha1.ClusterAddons{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-cluster",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: v1alpha1.ClusterAddonsSpec{
					Cluster: "prod-eu",
					Addons:  tt.enabledAddons,
				},
			}

			// Build the fake status reader.
			statusReader := &fakeStatusReader{
				records: make(map[string]clusterreconciler.ClusterReconcileRecord),
			}
			if tt.hasRecord {
				statusReader.records["prod-eu"] = clusterreconciler.ClusterReconcileRecord{
					Time:    baseTime,
					Outcome: tt.outcome,
					Message: tt.message,
				}
			}

			// Build the fake client with the CR.
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(cr).
				WithStatusSubresource(cr).
				Build()

			// Create the reconciler.
			reconciler := &ClusterAddonsReconciler{
				Client:       fakeClient,
				statusReader: statusReader,
			}

			// Reconcile.
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-cluster",
					Namespace: "default",
				},
			}
			res, err := reconciler.Reconcile(context.Background(), req)
			if err != nil {
				t.Fatalf("reconcile failed: %v", err)
			}
			if res.RequeueAfter != 60*time.Second {
				t.Errorf("expected RequeueAfter=60s, got %v", res.RequeueAfter)
			}

			// Fetch the updated CR to check .status.
			var updated v1alpha1.ClusterAddons
			if err := fakeClient.Get(context.Background(), req.NamespacedName, &updated); err != nil {
				t.Fatalf("failed to get updated CR: %v", err)
			}

			// Assert status fields.
			if updated.Status.ObservedGeneration != 1 {
				t.Errorf("ObservedGeneration: got %d, want 1", updated.Status.ObservedGeneration)
			}

			if tt.hasRecord {
				if updated.Status.LastReconcileTime == nil {
					t.Errorf("LastReconcileTime: got nil, want %v", baseTime)
				} else if !updated.Status.LastReconcileTime.Time.Equal(baseTime) {
					t.Errorf("LastReconcileTime: got %v, want %v", updated.Status.LastReconcileTime.Time, baseTime)
				}
			} else {
				if updated.Status.LastReconcileTime != nil {
					t.Errorf("LastReconcileTime: got %v, want nil", updated.Status.LastReconcileTime)
				}
			}

			if updated.Status.SyncedAddons != tt.wantSyncedAddons {
				t.Errorf("SyncedAddons: got %d, want %d", updated.Status.SyncedAddons, tt.wantSyncedAddons)
			}

			// Assert the Ready condition.
			if len(updated.Status.Conditions) != 1 {
				t.Fatalf("expected 1 condition, got %d", len(updated.Status.Conditions))
			}
			cond := updated.Status.Conditions[0]
			if cond.Type != "Ready" {
				t.Errorf("condition type: got %s, want Ready", cond.Type)
			}
			if cond.Status != tt.wantReadyStatus {
				t.Errorf("condition status: got %s, want %s", cond.Status, tt.wantReadyStatus)
			}
			if cond.Reason != tt.wantReadyReason {
				t.Errorf("condition reason: got %s, want %s", cond.Reason, tt.wantReadyReason)
			}
			if cond.Message != tt.wantReadyMessage {
				t.Errorf("condition message: got %q, want %q", cond.Message, tt.wantReadyMessage)
			}
			if cond.ObservedGeneration != 1 {
				t.Errorf("condition observedGeneration: got %d, want 1", cond.ObservedGeneration)
			}
		})
	}
}

// TestClusterAddonsReconciler_NotFound tests the clean no-op when the CR is deleted.
func TestClusterAddonsReconciler_NotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	statusReader := &fakeStatusReader{records: make(map[string]clusterreconciler.ClusterReconcileRecord)}

	reconciler := &ClusterAddonsReconciler{
		Client:       fakeClient,
		statusReader: statusReader,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "nonexistent",
			Namespace: "default",
		},
	}

	res, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("expected no requeue on NotFound, got RequeueAfter=%v", res.RequeueAfter)
	}
}

// recordingClient is a fake client that records all write operations to verify
// the read-only invariant (the controller must ONLY touch the status subresource).
type recordingClient struct {
	client.Client
	writes []writeRecord
}

type writeRecord struct {
	method string // "Create", "Update", "Delete", "Patch", "StatusUpdate"
	obj    client.Object
}

func (r *recordingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	r.writes = append(r.writes, writeRecord{method: "Create", obj: obj})
	return r.Client.Create(ctx, obj, opts...)
}

func (r *recordingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	r.writes = append(r.writes, writeRecord{method: "Update", obj: obj})
	return r.Client.Update(ctx, obj, opts...)
}

func (r *recordingClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	r.writes = append(r.writes, writeRecord{method: "Delete", obj: obj})
	return r.Client.Delete(ctx, obj, opts...)
}

func (r *recordingClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	r.writes = append(r.writes, writeRecord{method: "Patch", obj: obj})
	return r.Client.Patch(ctx, obj, patch, opts...)
}

func (r *recordingClient) Status() client.StatusWriter {
	return &recordingStatusWriter{
		StatusWriter: r.Client.Status(),
		parent:       r,
	}
}

type recordingStatusWriter struct {
	client.StatusWriter
	parent *recordingClient
}

func (r *recordingStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	r.parent.writes = append(r.parent.writes, writeRecord{method: "StatusUpdate", obj: obj})
	return r.StatusWriter.Update(ctx, obj, opts...)
}

func (r *recordingStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	r.parent.writes = append(r.parent.writes, writeRecord{method: "StatusPatch", obj: obj})
	return r.StatusWriter.Patch(ctx, obj, patch, opts...)
}

// TestClusterAddonsReconciler_ReadOnlyGuard verifies that the controller ONLY
// writes to the status subresource and NEVER touches spec/labels/other resources.
// This is the critical read-only invariant for Operator Phase 1.
func TestClusterAddonsReconciler_ReadOnlyGuard(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	cr := &v1alpha1.ClusterAddons{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-cluster",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: v1alpha1.ClusterAddonsSpec{
			Cluster: "prod-eu",
			Addons:  []v1alpha1.AddonAssignment{{Name: "foo"}},
		},
	}

	statusReader := &fakeStatusReader{
		records: map[string]clusterreconciler.ClusterReconcileRecord{
			"prod-eu": {
				Time:    time.Now(),
				Outcome: clusterreconciler.OutcomeSucceeded,
				Message: "",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()

	recordingClient := &recordingClient{Client: fakeClient}

	reconciler := &ClusterAddonsReconciler{
		Client:       recordingClient,
		statusReader: statusReader,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-cluster",
			Namespace: "default",
		},
	}

	_, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	// Assert that the ONLY write was a StatusUpdate on the ClusterAddons resource.
	if len(recordingClient.writes) != 1 {
		t.Fatalf("expected exactly 1 write, got %d: %+v", len(recordingClient.writes), recordingClient.writes)
	}

	write := recordingClient.writes[0]
	if write.method != "StatusUpdate" {
		t.Errorf("expected write method=StatusUpdate, got %s", write.method)
	}

	if _, ok := write.obj.(*v1alpha1.ClusterAddons); !ok {
		t.Errorf("expected write object to be *v1alpha1.ClusterAddons, got %T", write.obj)
	}

	// Additional guard: no Create/Update/Delete/Patch calls on ANY resource.
	for _, w := range recordingClient.writes {
		if w.method != "StatusUpdate" && w.method != "StatusPatch" {
			t.Errorf("unexpected non-status write: method=%s, obj=%T", w.method, w.obj)
		}
	}
}

// TestClusterAddonsReconciler_ReadOnlyGuard_AllOutcomes extends the read-only
// guard test to verify that ALL reconcile outcomes (succeeded, failed, skipped,
// no record) result in exactly one StatusUpdate and no other writes.
func TestClusterAddonsReconciler_ReadOnlyGuard_AllOutcomes(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	tests := []struct {
		name      string
		outcome   clusterreconciler.ReconcileOutcome
		hasRecord bool
	}{
		{name: "succeeded", outcome: clusterreconciler.OutcomeSucceeded, hasRecord: true},
		{name: "failed", outcome: clusterreconciler.OutcomeFailed, hasRecord: true},
		{name: "skipped", outcome: clusterreconciler.OutcomeSkipped, hasRecord: true},
		{name: "no record", outcome: "", hasRecord: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := &v1alpha1.ClusterAddons{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-cluster",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: v1alpha1.ClusterAddonsSpec{
					Cluster: "prod-eu",
					Addons:  []v1alpha1.AddonAssignment{{Name: "foo"}},
				},
			}

			statusReader := &fakeStatusReader{
				records: make(map[string]clusterreconciler.ClusterReconcileRecord),
			}
			if tt.hasRecord {
				statusReader.records["prod-eu"] = clusterreconciler.ClusterReconcileRecord{
					Time:    time.Now(),
					Outcome: tt.outcome,
					Message: "test message",
				}
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(cr).
				WithStatusSubresource(cr).
				Build()

			recordingClient := &recordingClient{Client: fakeClient}

			reconciler := &ClusterAddonsReconciler{
				Client:       recordingClient,
				statusReader: statusReader,
			}

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-cluster",
					Namespace: "default",
				},
			}

			_, err := reconciler.Reconcile(context.Background(), req)
			if err != nil {
				t.Fatalf("reconcile failed: %v", err)
			}

			// Verify exactly 1 StatusUpdate and nothing else.
			if len(recordingClient.writes) != 1 {
				t.Errorf("expected exactly 1 write, got %d: %+v", len(recordingClient.writes), recordingClient.writes)
			}

			if len(recordingClient.writes) > 0 {
				write := recordingClient.writes[0]
				if write.method != "StatusUpdate" {
					t.Errorf("expected StatusUpdate, got %s", write.method)
				}
			}
		})
	}
}

// TestClusterAddonsReconciler_StatusMapping_LabelDrift tests that label drift
// information (if present in the ClusterReconcileRecord) is safely ignored by
// the controller (it's a V3 G1 feature for the API layer, not for the CR status).
func TestClusterAddonsReconciler_StatusMapping_LabelDrift(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	cr := &v1alpha1.ClusterAddons{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-cluster",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: v1alpha1.ClusterAddonsSpec{
			Cluster: "prod-eu",
			Addons:  []v1alpha1.AddonAssignment{{Name: "foo"}},
		},
	}

	// Build a record with LabelDrift populated (V3 G1 feature).
	statusReader := &fakeStatusReader{
		records: map[string]clusterreconciler.ClusterReconcileRecord{
			"prod-eu": {
				Time:    time.Now(),
				Outcome: clusterreconciler.OutcomeSucceeded,
				Message: "",
				LabelDrift: &clusterreconciler.LabelDrift{
					Added:   []string{"bar"},
					Removed: []string{"baz"},
					Changed: []string{"qux"},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()

	reconciler := &ClusterAddonsReconciler{
		Client:       fakeClient,
		statusReader: statusReader,
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-cluster",
			Namespace: "default",
		},
	}

	_, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	// Verify the controller still sets Ready=True (drift is informational only,
	// not a failure condition for the CR).
	var updated v1alpha1.ClusterAddons
	if err := fakeClient.Get(context.Background(), req.NamespacedName, &updated); err != nil {
		t.Fatalf("Get updated CR: %v", err)
	}

	if len(updated.Status.Conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(updated.Status.Conditions))
	}

	cond := updated.Status.Conditions[0]
	if cond.Status != metav1.ConditionTrue {
		t.Errorf("condition status: got %s, want True (drift should not affect Ready status)", cond.Status)
	}
}

// fakeLabelWriter is a test fake that implements ManagedClusterLabelWriter
// and returns a controlled ManagedLabelSyncResult.
type fakeLabelWriter struct {
	result argosecrets.ManagedLabelSyncResult
	err    error
}

func (f *fakeLabelWriter) SyncManagedClusterLabels(ctx context.Context, name string, desiredLabels map[string]string) (argosecrets.ManagedLabelSyncResult, error) {
	return f.result, f.err
}

// TestClusterAddonsReconciler_DrivesLabels_StatusFromWriter verifies that when
// DrivesLabels=true (flag ON, Phase 2), the controller sets .status fields from
// the labelWriter's result, NOT from the statusReader's LastReconcile.
func TestClusterAddonsReconciler_DrivesLabels_StatusFromWriter(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	tests := []struct {
		name             string
		enabledAddons    []v1alpha1.AddonAssignment
		writerResult     argosecrets.ManagedLabelSyncResult
		writerErr        error
		wantSyncedAddons int
		wantReadyStatus  metav1.ConditionStatus
		wantReadyReason  string
		wantMessageHas   string // substring match for message
	}{
		{
			name:          "Secret found, labels changed, 2 addons",
			enabledAddons: []v1alpha1.AddonAssignment{{Name: "foo"}, {Name: "bar"}},
			writerResult: argosecrets.ManagedLabelSyncResult{
				Found:     true,
				Changed:   true,
				Converged: []string{"foo", "bar"},
			},
			wantSyncedAddons: 2,
			wantReadyStatus:  metav1.ConditionTrue,
			wantReadyReason:  "LabelsApplied",
			wantMessageHas:   "2 addon labels applied",
		},
		{
			name:          "Secret found, labels already converged, 1 addon",
			enabledAddons: []v1alpha1.AddonAssignment{{Name: "foo"}},
			writerResult: argosecrets.ManagedLabelSyncResult{
				Found:     true,
				Changed:   false,
				Converged: []string{"foo"},
			},
			wantSyncedAddons: 1,
			wantReadyStatus:  metav1.ConditionTrue,
			wantReadyReason:  "LabelsApplied",
			wantMessageHas:   "already in sync",
		},
		{
			name:          "Secret not found (cluster not registered)",
			enabledAddons: []v1alpha1.AddonAssignment{{Name: "foo"}},
			writerResult: argosecrets.ManagedLabelSyncResult{
				Found: false,
			},
			wantSyncedAddons: 0,
			wantReadyStatus:  metav1.ConditionFalse,
			wantReadyReason:  "SecretNotFound",
			wantMessageHas:   "not found or not managed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := &v1alpha1.ClusterAddons{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-cluster",
					Namespace:  "default",
					Generation: 5,
				},
				Spec: v1alpha1.ClusterAddonsSpec{
					Cluster: "prod-eu",
					Addons:  tt.enabledAddons,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(cr).
				WithStatusSubresource(cr).
				Build()

			reconciler := &ClusterAddonsReconciler{
				Client: fakeClient,
				labelWriter: &fakeLabelWriter{
					result: tt.writerResult,
					err:    tt.writerErr,
				},
				DrivesLabels: true, // FLAG ON
			}

			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-cluster",
					Namespace: "default",
				},
			}

			_, err := reconciler.Reconcile(context.Background(), req)
			if err != nil && tt.writerErr == nil {
				t.Fatalf("reconcile failed: %v", err)
			}

			var updated v1alpha1.ClusterAddons
			if err := fakeClient.Get(context.Background(), req.NamespacedName, &updated); err != nil {
				t.Fatalf("Get updated CR: %v", err)
			}

			// Verify status fields.
			if updated.Status.ObservedGeneration != cr.Generation {
				t.Errorf("observedGeneration: got %d, want %d", updated.Status.ObservedGeneration, cr.Generation)
			}

			if updated.Status.SyncedAddons != tt.wantSyncedAddons {
				t.Errorf("syncedAddons: got %d, want %d", updated.Status.SyncedAddons, tt.wantSyncedAddons)
			}

			if updated.Status.LastReconcileTime == nil {
				t.Error("lastReconcileTime should be set")
			}

			if len(updated.Status.Conditions) != 1 {
				t.Fatalf("expected 1 condition, got %d", len(updated.Status.Conditions))
			}

			cond := updated.Status.Conditions[0]
			if cond.Status != tt.wantReadyStatus {
				t.Errorf("Ready status: got %s, want %s", cond.Status, tt.wantReadyStatus)
			}

			if cond.Reason != tt.wantReadyReason {
				t.Errorf("Ready reason: got %s, want %s", cond.Reason, tt.wantReadyReason)
			}

			if tt.wantMessageHas != "" {
				if !contains(cond.Message, tt.wantMessageHas) {
					t.Errorf("Ready message: got %q, want substring %q", cond.Message, tt.wantMessageHas)
				}
			}
		})
	}
}

// recordingLabelWriter is a test fake that records every SyncManagedClusterLabels
// call and returns a controlled result. Used to verify the single-writer invariant
// (Story 2.4 — the controller writes zero labels when flag OFF, writes exactly the
// desired labels when flag ON).
type recordingLabelWriter struct {
	calls  []labelWriteCall
	result argosecrets.ManagedLabelSyncResult
	err    error
}

type labelWriteCall struct {
	clusterName   string
	desiredLabels map[string]string
}

func (r *recordingLabelWriter) SyncManagedClusterLabels(ctx context.Context, name string, desiredLabels map[string]string) (argosecrets.ManagedLabelSyncResult, error) {
	// Deep-copy desiredLabels so the test can safely inspect the captured state.
	labelsCopy := make(map[string]string, len(desiredLabels))
	for k, v := range desiredLabels {
		labelsCopy[k] = v
	}
	r.calls = append(r.calls, labelWriteCall{
		clusterName:   name,
		desiredLabels: labelsCopy,
	})
	return r.result, r.err
}

// TestClusterAddonsReconciler_DrivesLabelsOFF_WritesZeroLabels verifies the
// flag-OFF half of the single-writer invariant (Story 2.4): when DrivesLabels=false,
// the controller writes ZERO addon labels (read-only mode, Phase 1 behavior).
func TestClusterAddonsReconciler_DrivesLabelsOFF_WritesZeroLabels(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	cr := &v1alpha1.ClusterAddons{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-cluster",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: v1alpha1.ClusterAddonsSpec{
			Cluster: "prod-eu",
			Addons:  []v1alpha1.AddonAssignment{{Name: "foo"}, {Name: "bar"}},
		},
	}

	statusReader := &fakeStatusReader{
		records: map[string]clusterreconciler.ClusterReconcileRecord{
			"prod-eu": {
				Time:    time.Now(),
				Outcome: clusterreconciler.OutcomeSucceeded,
				Message: "",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()

	recorder := &recordingLabelWriter{
		result: argosecrets.ManagedLabelSyncResult{Found: true, Changed: false},
	}

	reconciler := &ClusterAddonsReconciler{
		Client:       fakeClient,
		statusReader: statusReader,
		labelWriter:  recorder, // Wired, but should never be called when flag OFF
		DrivesLabels: false,    // FLAG OFF (Phase 1 mode)
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-cluster",
			Namespace: "default",
		},
	}

	_, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	// Assert ZERO label writes occurred (the single-writer invariant).
	if len(recorder.calls) != 0 {
		t.Errorf("flag OFF: expected ZERO label writes, got %d calls: %+v", len(recorder.calls), recorder.calls)
	}
}

// TestClusterAddonsReconciler_DrivesLabelsON_DrivesLabelsSafely verifies the
// flag-ON half of the drives-labels-safely guard (Story 2.4): when DrivesLabels=true,
// the controller writes ONLY addon-label keys (no Secret Data, no annotations,
// no foreign labels, no strip of managed-by).
//
// This is the INVERSION of the Phase-1 read-only guard — same principle, opposite
// assertion. With flag ON, the controller MUST write labels (verified by checking
// the recording writer was called with the correct desired labels), but ONLY addon
// keys (verified by the fact that SyncManagedClusterLabels is the primitive — it
// PRESERVES managed-by + foreign labels + Secret Data by design).
func TestClusterAddonsReconciler_DrivesLabelsON_DrivesLabelsSafely(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = v1alpha1.AddToScheme(scheme)

	cr := &v1alpha1.ClusterAddons{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-cluster",
			Namespace:  "default",
			Generation: 1,
		},
		Spec: v1alpha1.ClusterAddonsSpec{
			Cluster: "prod-eu",
			Addons: []v1alpha1.AddonAssignment{
				{Name: "datadog"},
				{Name: "prometheus", Enabled: boolPtr(true)},
				{Name: "istio", Enabled: boolPtr(false)}, // disabled → no label
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(cr).
		Build()

	recorder := &recordingLabelWriter{
		result: argosecrets.ManagedLabelSyncResult{
			Found:     true,
			Changed:   true,
			Converged: []string{"datadog", "prometheus"},
		},
	}

	reconciler := &ClusterAddonsReconciler{
		Client:       fakeClient,
		labelWriter:  recorder,
		DrivesLabels: true, // FLAG ON (Phase 2 mode)
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-cluster",
			Namespace: "default",
		},
	}

	_, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	// Assert exactly ONE call to SyncManagedClusterLabels.
	if len(recorder.calls) != 1 {
		t.Fatalf("expected exactly 1 label write call, got %d", len(recorder.calls))
	}

	call := recorder.calls[0]
	if call.clusterName != "prod-eu" {
		t.Errorf("cluster name: got %q, want %q", call.clusterName, "prod-eu")
	}

	// Verify the desired labels: ONLY addon keys, NO foreign labels, NO system labels.
	// The spec has datadog + prometheus enabled, istio disabled (should be skipped).
	wantLabels := map[string]string{
		"datadog":    "enabled",
		"prometheus": "enabled",
		// "istio" MUST NOT appear (disabled).
	}

	if len(call.desiredLabels) != len(wantLabels) {
		t.Errorf("desired labels count: got %d, want %d", len(call.desiredLabels), len(wantLabels))
	}

	for k, wantV := range wantLabels {
		gotV, ok := call.desiredLabels[k]
		if !ok {
			t.Errorf("desired labels: missing key %q", k)
		} else if gotV != wantV {
			t.Errorf("desired labels[%q]: got %q, want %q", k, gotV, wantV)
		}
	}

	// Assert NO foreign labels (e.g., "app.kubernetes.io/managed-by") in the desired set.
	// The controller computes desired labels from CR spec via AddonAssignmentsToLabels,
	// which emits ONLY addon keys. SyncManagedClusterLabels PRESERVES foreign labels
	// (it doesn't delete them), but the desired set passed to it must be addon-only.
	for k := range call.desiredLabels {
		if contains(k, "/") {
			t.Errorf("desired labels: unexpected foreign label %q (controller should only pass addon keys)", k)
		}
	}

	// The "drives-labels-safely" contract: the controller ONLY writes labels
	// (via SyncManagedClusterLabels), NEVER Secret Data, NEVER annotations, NEVER
	// strips managed-by. Those guarantees are tested here by proxy: we verify the
	// controller calls SyncManagedClusterLabels (which we KNOW preserves those
	// fields — that's the G3-safe primitive's contract) with addon-only keys.
}

// TestClusterAddonsReconciler_RoundTrip_SpecToLabelsToSpec verifies that the
// spec→labels (controller) and labels→spec (generator) mappers round-trip exactly
// (Story 2.4): AddonAssignmentsToLabels ∘ mapLabelsToAddonAssignments = identity.
//
// This guards Git↔CR↔Secret consistency — if the two mappers disagree, the
// generator and controller will drift the CR on every reconcile.
func TestClusterAddonsReconciler_RoundTrip_SpecToLabelsToSpec(t *testing.T) {
	tests := []struct {
		name                string
		startingAssignments []v1alpha1.AddonAssignment
	}{
		{
			name: "two enabled addons",
			startingAssignments: []v1alpha1.AddonAssignment{
				{Name: "datadog"},
				{Name: "prometheus"},
			},
		},
		{
			name: "one enabled, one explicitly enabled",
			startingAssignments: []v1alpha1.AddonAssignment{
				{Name: "datadog"},
				{Name: "prometheus", Enabled: boolPtr(true)},
			},
		},
		{
			name: "one enabled, one disabled (absent from labels)",
			startingAssignments: []v1alpha1.AddonAssignment{
				{Name: "datadog"},
				{Name: "istio", Enabled: boolPtr(false)},
			},
		},
		{
			name:                "empty assignments (no labels)",
			startingAssignments: []v1alpha1.AddonAssignment{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Round-trip: spec → labels → spec.
			labels := AddonAssignmentsToLabels(tt.startingAssignments)
			roundTripped := mapLabelsToAddonAssignments(labels)

			// Build the expected set: only ENABLED assignments (disabled are absent from labels).
			var wantAssignments []v1alpha1.AddonAssignment
			for _, a := range tt.startingAssignments {
				if a.Enabled == nil || *a.Enabled {
					// Round-trip produces explicit Enabled pointers (generator always sets them).
					wantAssignments = append(wantAssignments, v1alpha1.AddonAssignment{
						Name:    a.Name,
						Enabled: boolPtr(true),
					})
				}
			}

			// Compare counts.
			if len(roundTripped) != len(wantAssignments) {
				t.Fatalf("round-trip count: got %d, want %d", len(roundTripped), len(wantAssignments))
			}

			// Compare contents (order-agnostic).
			wantByName := make(map[string]bool, len(wantAssignments))
			for _, a := range wantAssignments {
				wantByName[a.Name] = *a.Enabled
			}

			for _, a := range roundTripped {
				wantEnabled, ok := wantByName[a.Name]
				if !ok {
					t.Errorf("round-trip: unexpected addon %q", a.Name)
					continue
				}
				if a.Enabled == nil || *a.Enabled != wantEnabled {
					t.Errorf("round-trip addon %q: got enabled=%v, want %v", a.Name, a.Enabled, wantEnabled)
				}
			}
		})
	}
}

// TestClusterAddonsReconciler_RoundTrip_LabelsToSpecToLabels verifies the reverse
// round-trip (labels→spec→labels = identity). This is the stronger guarantee:
// the generator READS labels and emits spec; the controller READS spec and emits
// labels. Both directions must compose back to the same state.
func TestClusterAddonsReconciler_RoundTrip_LabelsToSpecToLabels(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
	}{
		{
			name: "two enabled addons",
			labels: map[string]string{
				"datadog":    "enabled",
				"prometheus": "enabled",
			},
		},
		{
			name: "one enabled addon",
			labels: map[string]string{
				"datadog": "enabled",
			},
		},
		{
			name:   "empty labels",
			labels: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Round-trip: labels → spec → labels.
			assignments := mapLabelsToAddonAssignments(tt.labels)
			roundTripped := AddonAssignmentsToLabels(assignments)

			// Compare label sets (order-agnostic).
			if len(roundTripped) != len(tt.labels) {
				t.Fatalf("round-trip count: got %d, want %d", len(roundTripped), len(tt.labels))
			}

			for k, wantV := range tt.labels {
				gotV, ok := roundTripped[k]
				if !ok {
					t.Errorf("round-trip: missing label %q", k)
				} else if gotV != wantV {
					t.Errorf("round-trip label[%q]: got %q, want %q", k, gotV, wantV)
				}
			}

			for k := range roundTripped {
				if _, ok := tt.labels[k]; !ok {
					t.Errorf("round-trip: unexpected label %q", k)
				}
			}
		})
	}
}
