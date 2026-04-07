package secrets

import (
	"context"
	"errors"
	"fmt"
	"testing"

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
		0, // default interval, not used in tests
	)
}

func standardGitReader(catalogYAML string) *mockGitReader {
	return &mockGitReader{
		files: map[string][]byte{
			"configuration/addons-catalog.yaml": []byte(catalogYAML),
			"configuration/cluster-addons.yaml": []byte(clusterAddonsYAML),
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
			"configuration/addons-catalog.yaml": []byte(catalogWithSecrets),
			"configuration/cluster-addons.yaml": []byte(clusterAddonsNoMatch),
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
