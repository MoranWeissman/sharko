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
