package secrets

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/providers"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// ---- mock helpers ----

// mockGitReader implements GitReader with canned file contents.
type mockGitReader struct {
	files map[string][]byte
	err   error
}

func (m *mockGitReader) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	data, ok := m.files[path]
	if !ok {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	return data, nil
}

// mockCredProvider implements providers.ClusterCredentialsProvider.
type mockCredProvider struct {
	kubeconfig []byte
	err        error
}

func (m *mockCredProvider) GetCredentials(_ string) (*providers.Kubeconfig, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &providers.Kubeconfig{Raw: m.kubeconfig}, nil
}

func (m *mockCredProvider) ListClusters() ([]providers.ClusterInfo, error) {
	return nil, nil
}

func (m *mockCredProvider) SearchSecrets(query string) ([]string, error) {
	return nil, nil
}

func (m *mockCredProvider) HealthCheck(ctx context.Context) error {
	return nil
}

// mockSecretProvider implements providers.SecretProvider.
type mockSecretProvider struct {
	values map[string][]byte
	err    error
}

func (m *mockSecretProvider) GetSecretValue(_ context.Context, path string) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	val, ok := m.values[path]
	if !ok {
		return nil, fmt.Errorf("secret not found: %s", path)
	}
	return val, nil
}

// fakeRemoteClientFn returns a RemoteClientFactory that always returns the given client.
func fakeRemoteClientFn(client kubernetes.Interface) RemoteClientFactory {
	return func(_ []byte) (kubernetes.Interface, error) {
		return client, nil
	}
}

// errRemoteClientFn is a RemoteClientFactory that always fails.
func errRemoteClientFn(msg string) RemoteClientFactory {
	return func(_ []byte) (kubernetes.Interface, error) {
		return nil, errors.New(msg)
	}
}

// ---- catalog / cluster YAML helpers ----

const catalogWithSecrets = `
applicationsets:
  - name: datadog
    repoURL: https://helm.datadoghq.com
    chart: datadog
    version: "3.50.0"
    namespace: monitoring
    secrets:
      - secretName: datadog-secret
        namespace: monitoring
        keys:
          api-key: "secrets/datadog/api-key"
          app-key: "secrets/datadog/app-key"
`

const catalogWithoutSecrets = `
applicationsets:
  - name: nginx
    repoURL: https://charts.helm.sh/stable
    chart: nginx
    version: "1.2.3"
    namespace: default
`

const clusterAddonsYAML = `
clusters:
  - name: prod-cluster
    labels:
      datadog: enabled
`

const clusterAddonsNoMatch = `
clusters:
  - name: prod-cluster
    labels:
      nginx: enabled
`

// ---- test helpers ----

func newReconciler(
	gitReader GitReader,
	creds providers.ClusterCredentialsProvider,
	secretProv providers.SecretProvider,
	clientFn RemoteClientFactory,
) *Reconciler {
	parser := config.NewParser()
	gr := gitReader // captured for closure
	return NewReconciler(
		creds,
		secretProv,
		func() GitReader { return gr },
		clientFn,
		parser,
		"main",
		"configuration/managed-clusters.yaml",
		0, // default interval, not used in tests
	)
}

func standardGitReader(catalogYAML string) *mockGitReader {
	return &mockGitReader{
		files: map[string][]byte{
			"configuration/addons-catalog.yaml":   []byte(catalogYAML),
			"configuration/managed-clusters.yaml": []byte(clusterAddonsYAML),
		},
	}
}

// ---- tests ----

// TestReconcile_CreateMissing verifies that a secret is created on the cluster
// when no secret with that name exists yet.
func TestReconcile_CreateMissing(t *testing.T) {
	client := fake.NewSimpleClientset()

	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("the-api-key"),
			"secrets/datadog/app-key": []byte("the-app-key"),
		}},
		fakeRemoteClientFn(client),
	)
	r.reconcile()

	// Secret should exist on the fake cluster.
	secret, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected secret to be created, got error: %v", err)
	}
	if string(secret.Data["api-key"]) != "the-api-key" {
		t.Errorf("expected api-key=the-api-key, got %q", secret.Data["api-key"])
	}
	if string(secret.Data["app-key"]) != "the-app-key" {
		t.Errorf("expected app-key=the-app-key, got %q", secret.Data["app-key"])
	}

	stats := r.GetStats().(ReconcileStats)
	if stats.Created != 1 {
		t.Errorf("expected Created=1, got %d", stats.Created)
	}
	if stats.Skipped != 0 {
		t.Errorf("expected Skipped=0, got %d", stats.Skipped)
	}
	if stats.Errors != 0 {
		t.Errorf("expected Errors=0, got %d", stats.Errors)
	}
}

// TestReconcile_SkipUpToDate verifies that a secret with matching content is not updated.
func TestReconcile_SkipUpToDate(t *testing.T) {
	client := fake.NewSimpleClientset()
	secretValues := map[string][]byte{
		"secrets/datadog/api-key": []byte("key1"),
		"secrets/datadog/app-key": []byte("key2"),
	}

	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: secretValues},
		fakeRemoteClientFn(client),
	)

	// First reconcile — creates the secret.
	r.reconcile()
	stats := r.GetStats().(ReconcileStats)
	if stats.Created != 1 {
		t.Fatalf("expected Created=1 after first reconcile, got %d", stats.Created)
	}

	// Second reconcile — hashes match, should skip.
	r.reconcile()
	stats = r.GetStats().(ReconcileStats)
	if stats.Skipped != 1 {
		t.Errorf("expected Skipped=1 after second reconcile, got %d", stats.Skipped)
	}
	if stats.Updated != 0 {
		t.Errorf("expected Updated=0, got %d", stats.Updated)
	}
}

// TestReconcile_UpdateRotated verifies that a secret with different content is updated.
func TestReconcile_UpdateRotated(t *testing.T) {
	client := fake.NewSimpleClientset()

	firstValues := map[string][]byte{
		"secrets/datadog/api-key": []byte("old-key"),
		"secrets/datadog/app-key": []byte("old-app"),
	}

	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: firstValues},
		fakeRemoteClientFn(client),
	)
	r.reconcile()

	stats := r.GetStats().(ReconcileStats)
	if stats.Created != 1 {
		t.Fatalf("expected Created=1, got %d", stats.Created)
	}

	// Update the secret provider to return different values.
	r.secretProvider = &mockSecretProvider{values: map[string][]byte{
		"secrets/datadog/api-key": []byte("rotated-key"),
		"secrets/datadog/app-key": []byte("rotated-app"),
	}}

	r.reconcile()
	stats = r.GetStats().(ReconcileStats)
	if stats.Updated != 1 {
		t.Errorf("expected Updated=1, got %d", stats.Updated)
	}
	if stats.Skipped != 0 {
		t.Errorf("expected Skipped=0, got %d", stats.Skipped)
	}

	// Verify new values in the cluster.
	secret, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("secret not found: %v", err)
	}
	if string(secret.Data["api-key"]) != "rotated-key" {
		t.Errorf("expected api-key=rotated-key, got %q", secret.Data["api-key"])
	}
}

// TestReconcile_NoSecretDefinitions verifies that no K8s calls are made when
// no addons in the catalog declare secrets.
func TestReconcile_NoSecretDefinitions(t *testing.T) {
	client := fake.NewSimpleClientset()

	r := newReconciler(
		standardGitReader(catalogWithoutSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{}},
		fakeRemoteClientFn(client),
	)
	r.reconcile()

	stats := r.GetStats().(ReconcileStats)
	if stats.Checked != 0 {
		t.Errorf("expected Checked=0, got %d", stats.Checked)
	}
	if stats.Created != 0 {
		t.Errorf("expected Created=0, got %d", stats.Created)
	}

	// LastRun should be zero because we returned early before writing stats.
	if !stats.LastRun.IsZero() {
		t.Error("expected zero LastRun when nothing to reconcile")
	}
}

// TestReconcile_ProviderError verifies that a provider fetch failure is
// captured as an error but reconciliation continues for other secrets.
func TestReconcile_ProviderError(t *testing.T) {
	client := fake.NewSimpleClientset()

	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{err: errors.New("vault unavailable")},
		fakeRemoteClientFn(client),
	)
	r.reconcile()

	stats := r.GetStats().(ReconcileStats)
	if stats.Errors == 0 {
		t.Error("expected at least one error due to provider failure")
	}
	errs := r.GetErrors()
	if len(errs) == 0 {
		t.Error("expected error messages to be recorded")
	}
	// No secret should have been created.
	if stats.Created != 0 {
		t.Errorf("expected Created=0, got %d", stats.Created)
	}
}

// TestReconcile_ClusterError verifies that a cluster connection failure is
// captured as an error and reconciliation continues.
func TestReconcile_ClusterError(t *testing.T) {
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("key"),
			"secrets/datadog/app-key": []byte("app"),
		}},
		errRemoteClientFn("connection refused"),
	)
	r.reconcile()

	stats := r.GetStats().(ReconcileStats)
	if stats.Errors == 0 {
		t.Error("expected error due to cluster connection failure")
	}
	if stats.Created != 0 {
		t.Errorf("expected Created=0, got %d", stats.Created)
	}
}

// TestReconcile_NoGitConnection verifies that the reconciler is a no-op when
// no Git connection is available.
func TestReconcile_NoGitConnection(t *testing.T) {
	parser := config.NewParser()
	r := NewReconciler(
		&mockCredProvider{},
		&mockSecretProvider{},
		func() GitReader { return nil }, // no connection
		fakeRemoteClientFn(fake.NewSimpleClientset()),
		parser,
		"main",
		"configuration/managed-clusters.yaml",
		0,
	)
	r.reconcile()

	// Stats should be zero / unset.
	stats := r.GetStats().(ReconcileStats)
	if stats.Checked != 0 || stats.Created != 0 || stats.Errors != 0 {
		t.Errorf("expected all-zero stats when no git connection, got %+v", stats)
	}
}

// TestHashSecretData verifies that the hash function is deterministic and
// order-independent.
func TestHashSecretData(t *testing.T) {
	data1 := map[string][]byte{
		"alpha": []byte("value-a"),
		"beta":  []byte("value-b"),
	}
	data2 := map[string][]byte{
		"beta":  []byte("value-b"),
		"alpha": []byte("value-a"),
	}
	h1 := hashSecretData(data1)
	h2 := hashSecretData(data2)
	if h1 != h2 {
		t.Errorf("expected same hash for maps with same content but different insertion order: %s != %s", h1, h2)
	}

	// Different data should produce a different hash.
	data3 := map[string][]byte{
		"alpha": []byte("different"),
		"beta":  []byte("value-b"),
	}
	h3 := hashSecretData(data3)
	if h1 == h3 {
		t.Errorf("expected different hash for different data, but got same: %s", h1)
	}

	// Empty map should not panic.
	h4 := hashSecretData(map[string][]byte{})
	if h4 == "" {
		t.Error("expected non-empty hash for empty map")
	}
}

// TestReconcile_AddonNotEnabled verifies that secrets are not created for a
// cluster that does not have the addon label set to "enabled".
func TestReconcile_AddonNotEnabled(t *testing.T) {
	client := fake.NewSimpleClientset()
	gitReader := &mockGitReader{
		files: map[string][]byte{
			"configuration/addons-catalog.yaml":   []byte(catalogWithSecrets),
			"configuration/managed-clusters.yaml": []byte(clusterAddonsNoMatch),
		},
	}

	r := newReconciler(
		gitReader,
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("key"),
			"secrets/datadog/app-key": []byte("app"),
		}},
		fakeRemoteClientFn(client),
	)
	r.reconcile()

	stats := r.GetStats().(ReconcileStats)
	if stats.Checked != 0 {
		t.Errorf("expected Checked=0 when addon not enabled, got %d", stats.Checked)
	}
	if stats.Created != 0 {
		t.Errorf("expected Created=0 when addon not enabled, got %d", stats.Created)
	}
}

// TestReconcile_CredentialsError verifies that a credentials lookup failure
// is captured as an error.
func TestReconcile_CredentialsError(t *testing.T) {
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{err: errors.New("secret not found")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("key"),
			"secrets/datadog/app-key": []byte("app"),
		}},
		errRemoteClientFn("should not be reached"),
	)
	r.reconcile()

	stats := r.GetStats().(ReconcileStats)
	if stats.Errors == 0 {
		t.Error("expected error due to credentials failure")
	}
}

// ---- per-item state (S1: addon values secrets remember what happened to
// each one) ----

// TestReconcile_ItemRecord_FreshStartup verifies that a cluster+addon pair
// that has never been reconciled on this server instance reports
// ok=false — "not checked since restart", never a fabricated timestamp.
func TestReconcile_ItemRecord_FreshStartup(t *testing.T) {
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{}},
		fakeRemoteClientFn(fake.NewSimpleClientset()),
	)
	if _, ok := r.LastItemState("prod-cluster", "datadog"); ok {
		t.Fatal("expected no item state before the first reconcile ever runs")
	}
	if _, ok := r.LastItemChecked("prod-cluster", "datadog"); ok {
		t.Fatal("expected LastItemChecked ok=false before the first reconcile ever runs")
	}
}

// TestReconcile_ItemRecord_CreateThenUnchanged verifies outcome
// classification per item, and that an unchanged pass moves LastChecked
// forward while leaving ChangedAt exactly where the create left it.
func TestReconcile_ItemRecord_CreateThenUnchanged(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("key1"),
			"secrets/datadog/app-key": []byte("key2"),
		}},
		fakeRemoteClientFn(client),
	)

	r.reconcile()
	rec, ok := r.LastItemState("prod-cluster", "datadog")
	if !ok {
		t.Fatal("expected an item record after the first reconcile")
	}
	if rec.Outcome != ItemOutcomeCreated {
		t.Errorf("outcome = %q, want %q", rec.Outcome, ItemOutcomeCreated)
	}
	if rec.LastChecked.IsZero() {
		t.Error("expected LastChecked to be set")
	}
	if rec.ChangedAt.IsZero() {
		t.Error("expected ChangedAt to be set on a create")
	}
	firstChecked, firstChanged := rec.LastChecked, rec.ChangedAt

	// Second pass: same values, hash matches -> unchanged.
	time.Sleep(2 * time.Millisecond)
	r.reconcile()
	rec, ok = r.LastItemState("prod-cluster", "datadog")
	if !ok {
		t.Fatal("expected an item record after the second reconcile")
	}
	if rec.Outcome != ItemOutcomeUnchanged {
		t.Errorf("outcome = %q, want %q", rec.Outcome, ItemOutcomeUnchanged)
	}
	if !rec.LastChecked.After(firstChecked) {
		t.Errorf("LastChecked = %v, want later than %v — an unchanged pass still checked it", rec.LastChecked, firstChecked)
	}
	if !rec.ChangedAt.Equal(firstChanged) {
		t.Errorf("ChangedAt = %v, want unchanged from %v — an unchanged check must never move it", rec.ChangedAt, firstChanged)
	}

	// LastItemChecked (the primitive getter internal/api reads) agrees.
	checked, ok := r.LastItemChecked("prod-cluster", "datadog")
	if !ok || !checked.Equal(rec.LastChecked) {
		t.Errorf("LastItemChecked = (%v, %v), want (%v, true)", checked, ok, rec.LastChecked)
	}
}

// TestReconcile_ItemRecord_UpdateMovesChangedAt verifies that a rotation
// (existing secret, different hash) is classified Updated and moves
// ChangedAt forward.
func TestReconcile_ItemRecord_UpdateMovesChangedAt(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("old-key"),
			"secrets/datadog/app-key": []byte("old-app"),
		}},
		fakeRemoteClientFn(client),
	)
	r.reconcile()
	firstChanged := mustItemState(t, r, "prod-cluster", "datadog").ChangedAt

	time.Sleep(2 * time.Millisecond)
	r.secretProvider = &mockSecretProvider{values: map[string][]byte{
		"secrets/datadog/api-key": []byte("rotated-key"),
		"secrets/datadog/app-key": []byte("rotated-app"),
	}}
	r.reconcile()

	rec := mustItemState(t, r, "prod-cluster", "datadog")
	if rec.Outcome != ItemOutcomeUpdated {
		t.Errorf("outcome = %q, want %q", rec.Outcome, ItemOutcomeUpdated)
	}
	if !rec.ChangedAt.After(firstChanged) {
		t.Errorf("ChangedAt = %v, want later than %v — an update must move it", rec.ChangedAt, firstChanged)
	}
}

// TestReconcile_ItemRecord_ErrorOutcome verifies that a live-cluster
// failure is classified Error, carries the error message, and never moves
// ChangedAt (nothing was actually written).
func TestReconcile_ItemRecord_ErrorOutcome(t *testing.T) {
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("key"),
			"secrets/datadog/app-key": []byte("app"),
		}},
		errRemoteClientFn("connection refused"),
	)
	r.reconcile()

	rec := mustItemState(t, r, "prod-cluster", "datadog")
	if rec.Outcome != ItemOutcomeError {
		t.Errorf("outcome = %q, want %q", rec.Outcome, ItemOutcomeError)
	}
	if rec.Error == "" {
		t.Error("expected a non-empty error message on an Error outcome")
	}
	if !rec.ChangedAt.IsZero() {
		t.Errorf("ChangedAt = %v, want zero — nothing was ever written for this item", rec.ChangedAt)
	}
}

// TestReconcile_ItemRecord_VanishedWorkStopsBeingReported verifies that an
// item whose work disappears from the plan (the addon gets disabled on the
// cluster) is no longer reported the very next pass — the per-item map is
// rebuilt fresh each pass, not accumulated forever.
func TestReconcile_ItemRecord_VanishedWorkStopsBeingReported(t *testing.T) {
	client := fake.NewSimpleClientset()
	gr := standardGitReader(catalogWithSecrets)
	r := newReconciler(
		gr,
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("key"),
			"secrets/datadog/app-key": []byte("app"),
		}},
		fakeRemoteClientFn(client),
	)
	r.reconcile()
	if _, ok := r.LastItemState("prod-cluster", "datadog"); !ok {
		t.Fatal("expected an item record after the first reconcile")
	}

	// The addon is switched off on the cluster — the same catalog, a
	// managed-clusters.yaml where the cluster no longer runs datadog.
	gr.files["configuration/managed-clusters.yaml"] = []byte(clusterAddonsNoMatch)
	r.reconcile()

	if _, ok := r.LastItemState("prod-cluster", "datadog"); ok {
		t.Error("expected the item record to be gone once the addon is no longer in the plan")
	}
}

// mustItemState is a test helper that fails the test if no item record
// exists for the given cluster+addon pair.
func mustItemState(t *testing.T, r *Reconciler, cluster, addon string) ItemRecord {
	t.Helper()
	rec, ok := r.LastItemState(cluster, addon)
	if !ok {
		t.Fatalf("expected an item record for %s/%s", cluster, addon)
	}
	return rec
}

// ---- per-item audit (S2: an audit entry on a real change, never on an
// unchanged check) ----

// TestReconcile_ItemAuditFn_FiresOnRealChangeOnly verifies that the
// per-item audit callback fires exactly once per create/update and never
// for an unchanged check — the flood-prevention requirement at scale (50
// clusters x 10 addons every 5 minutes).
func TestReconcile_ItemAuditFn_FiresOnRealChangeOnly(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("key1"),
			"secrets/datadog/app-key": []byte("key2"),
		}},
		fakeRemoteClientFn(client),
	)

	var calls []string
	r.SetItemAuditFunc(func(cluster, addon string, outcome ItemOutcome) {
		calls = append(calls, fmt.Sprintf("%s/%s:%s", cluster, addon, outcome))
	})

	// First pass creates -> exactly one audit call.
	r.reconcile()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one item-audit call on create, got %d (%v)", len(calls), calls)
	}
	if calls[0] != "prod-cluster/datadog:created" {
		t.Errorf("call = %q, want %q", calls[0], "prod-cluster/datadog:created")
	}

	// Second pass: unchanged -> no additional audit call.
	r.reconcile()
	if len(calls) != 1 {
		t.Fatalf("expected no additional item-audit call on an unchanged check, got %d total (%v)", len(calls), calls)
	}

	// Rotate the value -> update -> exactly one more audit call.
	r.secretProvider = &mockSecretProvider{values: map[string][]byte{
		"secrets/datadog/api-key": []byte("rotated"),
		"secrets/datadog/app-key": []byte("rotated2"),
	}}
	r.reconcile()
	if len(calls) != 2 {
		t.Fatalf("expected exactly one more item-audit call on update, got %d total (%v)", len(calls), calls)
	}
	if calls[1] != "prod-cluster/datadog:updated" {
		t.Errorf("call = %q, want %q", calls[1], "prod-cluster/datadog:updated")
	}
}

// TestReconciler_StartRunsImmediatePass pins P2-D D5: this engine already
// ran one pass immediately at Start() before this lane (Start's body is
// `r.reconcile(); ticker := ...` — a plain read of the code, not something
// this lane changed) — this test locks that behaviour in so a future edit
// cannot silently regress it back to "wait for the first tick". Uses a
// git reader that always errors, so the immediate pass takes the fast
// plan-level-failure path (recordRun is called directly, no cluster/addon
// mocks needed) and LastRunTime() moves off the zero value the moment that
// pass completes — with a one-hour tick interval, the only way it can move
// within this test's short deadline is the immediate pass.
func TestReconciler_StartRunsImmediatePass(t *testing.T) {
	t.Parallel()
	reader := &mockGitReader{err: errors.New("git host unreachable (test)")}
	parser := config.NewParser()
	r := NewReconciler(
		&mockCredProvider{},
		&mockSecretProvider{},
		func() GitReader { return reader },
		fakeRemoteClientFn(fake.NewSimpleClientset()),
		parser,
		"main",
		"configuration/managed-clusters.yaml",
		time.Hour,
	)

	r.Start()
	defer r.Stop()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !r.LastRunTime().IsZero() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Start() did not run an immediate pass within 500ms — LastRunTime is still zero")
}
