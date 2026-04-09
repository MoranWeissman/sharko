package argosecrets

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// --------------------------------------------------------------------------
// Mock implementations
// --------------------------------------------------------------------------

// mockGitReader implements GitReader for tests.
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

// mockCredProvider implements ClusterCredentialsProvider for tests.
type mockCredProvider struct {
	creds map[string]*providers.Kubeconfig
	// errFor maps cluster name → error to return for that specific cluster.
	errFor map[string]error
	// err is a global error returned for all lookups when set.
	err error
}

func (m *mockCredProvider) GetCredentials(name string) (*providers.Kubeconfig, error) {
	if m.err != nil {
		return nil, m.err
	}
	if e, ok := m.errFor[name]; ok {
		return nil, e
	}
	kc, ok := m.creds[name]
	if !ok {
		return nil, fmt.Errorf("no credentials for %s", name)
	}
	return kc, nil
}

// --------------------------------------------------------------------------
// Test helpers
// --------------------------------------------------------------------------

const clusterAddonsPath = "configuration/cluster-addons.yaml"

// twoClusterYAML is a valid cluster-addons.yaml with two clusters.
const twoClusterYAML = `
clusters:
  - name: cluster-1
    region: us-east-1
    labels:
      addon-datadog: "true"
  - name: cluster-2
    region: eu-west-1
    labels:
      addon-karpenter: "true"
`

// twoClusterYAMLAlt has slightly different content (different label value) so
// the SHA-256 differs and a second ReconcileOnce will not short-circuit.
const twoClusterYAMLAlt = `
clusters:
  - name: cluster-1
    region: us-east-1
    labels:
      addon-datadog: "false"
  - name: cluster-2
    region: eu-west-1
    labels:
      addon-karpenter: "false"
`

func kubeconfig(server string) *providers.Kubeconfig {
	return &providers.Kubeconfig{
		Server: server,
		Raw:    []byte("fake-kubeconfig"),
	}
}

// newTestReconciler wires up a Reconciler with a fake K8s clientset.
// The returned *Manager and fake.Clientset are exposed for assertions.
func newTestReconciler(
	gitReaderFn func() GitReader,
	credProvider ClusterCredentialsProvider,
) (*Reconciler, *Manager, *fake.Clientset) {
	client := fake.NewSimpleClientset()
	mgr := NewManager(client, testNamespace)
	parser := config.NewParser()

	rec := NewReconciler(
		mgr,
		credProvider,
		gitReaderFn,
		parser,
		"main",
		"arn:aws:iam::123456789012:role/default",
		0, // use default interval
	)
	return rec, mgr, client
}

// --------------------------------------------------------------------------
// Test cases
// --------------------------------------------------------------------------

// TestReconcileOnce_HappyPath verifies that two clusters in cluster-addons.yaml
// each get an Ensure call, resulting in two created secrets.
func TestReconcileOnce_HappyPath(t *testing.T) {
	reader := &mockGitReader{
		files: map[string][]byte{
			clusterAddonsPath: []byte(twoClusterYAML),
		},
	}
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"cluster-1": kubeconfig("https://api.cluster-1.example.com"),
			"cluster-2": kubeconfig("https://api.cluster-2.example.com"),
		},
	}

	rec, _, client := newTestReconciler(func() GitReader { return reader }, creds)

	rec.ReconcileOnce(context.Background())

	// Verify two secrets were created.
	secrets, err := client.CoreV1().Secrets(testNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing secrets: %v", err)
	}
	if len(secrets.Items) != 2 {
		t.Errorf("expected 2 secrets, got %d", len(secrets.Items))
	}

	nameSet := make(map[string]bool, len(secrets.Items))
	for _, s := range secrets.Items {
		nameSet[s.Name] = true
	}
	if !nameSet["cluster-1"] {
		t.Error("secret cluster-1 not found")
	}
	if !nameSet["cluster-2"] {
		t.Error("secret cluster-2 not found")
	}

	stats := rec.GetStats()
	if stats.Checked != 2 {
		t.Errorf("stats.Checked = %d, want 2", stats.Checked)
	}
	if stats.Created != 2 {
		t.Errorf("stats.Created = %d, want 2", stats.Created)
	}
	if stats.Errors != 0 {
		t.Errorf("stats.Errors = %d, want 0", stats.Errors)
	}
}

// TestReconcileOnce_OrphanCleanup verifies that a managed secret not present
// in cluster-addons.yaml is deleted.
func TestReconcileOnce_OrphanCleanup(t *testing.T) {
	// Pre-create an orphan managed secret that is NOT in the YAML.
	orphan := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orphan-cluster",
			Namespace: testNamespace,
			Labels: map[string]string{
				LabelSecretType: "cluster",
				LabelManagedBy:  ManagedByValue,
			},
		},
		Type: corev1.SecretTypeOpaque,
	}

	client := fake.NewSimpleClientset(orphan)
	mgr := NewManager(client, testNamespace)
	parser := config.NewParser()

	reader := &mockGitReader{
		files: map[string][]byte{
			clusterAddonsPath: []byte(twoClusterYAML),
		},
	}
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"cluster-1": kubeconfig("https://api.cluster-1.example.com"),
			"cluster-2": kubeconfig("https://api.cluster-2.example.com"),
		},
	}

	rec := NewReconciler(mgr, creds, func() GitReader { return reader }, parser, "main", "", 0)
	rec.ReconcileOnce(context.Background())

	// Orphan must be gone.
	_, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "orphan-cluster", metav1.GetOptions{})
	if err == nil {
		t.Error("orphan-cluster should have been deleted, but still exists")
	}

	stats := rec.GetStats()
	if stats.Deleted != 1 {
		t.Errorf("stats.Deleted = %d, want 1", stats.Deleted)
	}
}

// TestReconcileOnce_ContentHashSkip verifies that a second call with the same
// cluster-addons.yaml content short-circuits and makes no K8s API writes.
func TestReconcileOnce_ContentHashSkip(t *testing.T) {
	reader := &mockGitReader{
		files: map[string][]byte{
			clusterAddonsPath: []byte(twoClusterYAML),
		},
	}
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"cluster-1": kubeconfig("https://api.cluster-1.example.com"),
			"cluster-2": kubeconfig("https://api.cluster-2.example.com"),
		},
	}

	rec, _, client := newTestReconciler(func() GitReader { return reader }, creds)

	// First reconcile — should create 2 secrets.
	rec.ReconcileOnce(context.Background())

	// Snapshot the action count after the first run.
	actionsAfterFirst := len(client.Actions())

	// Second reconcile with the same content — must short-circuit.
	rec.ReconcileOnce(context.Background())

	actionsAfterSecond := len(client.Actions())
	if actionsAfterSecond != actionsAfterFirst {
		t.Errorf("second ReconcileOnce with identical content issued %d additional K8s actions, expected 0",
			actionsAfterSecond-actionsAfterFirst)
	}
}

// TestReconcileOnce_PerClusterErrorContinuation verifies that an error for
// cluster-2 does not prevent cluster-1 from being reconciled.
func TestReconcileOnce_PerClusterErrorContinuation(t *testing.T) {
	reader := &mockGitReader{
		files: map[string][]byte{
			clusterAddonsPath: []byte(twoClusterYAML),
		},
	}
	// cluster-2 has no credentials — will error.
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"cluster-1": kubeconfig("https://api.cluster-1.example.com"),
		},
	}

	rec, _, client := newTestReconciler(func() GitReader { return reader }, creds)
	rec.ReconcileOnce(context.Background())

	// cluster-1 should be created despite cluster-2 failing.
	_, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "cluster-1", metav1.GetOptions{})
	if err != nil {
		t.Errorf("cluster-1 secret not found after per-cluster error: %v", err)
	}

	stats := rec.GetStats()
	if stats.Errors != 1 {
		t.Errorf("stats.Errors = %d, want 1", stats.Errors)
	}
	if stats.Created != 1 {
		t.Errorf("stats.Created = %d, want 1", stats.Created)
	}
	if stats.Checked != 2 {
		t.Errorf("stats.Checked = %d, want 2", stats.Checked)
	}

	errs := rec.GetErrors()
	if len(errs) != 1 {
		t.Errorf("GetErrors() returned %d errors, want 1", len(errs))
	}
}

// TestReconcileOnce_NilGitReader verifies that ReconcileOnce exits gracefully
// when the gitReader accessor returns nil (no active connection).
func TestReconcileOnce_NilGitReader(t *testing.T) {
	creds := &mockCredProvider{}
	rec, _, client := newTestReconciler(func() GitReader { return nil }, creds)

	// Must not panic.
	rec.ReconcileOnce(context.Background())

	// No K8s actions should have been taken.
	if len(client.Actions()) != 0 {
		t.Errorf("expected 0 K8s actions when gitReader is nil, got %d", len(client.Actions()))
	}
}

// TestReconcileOnce_AuditCallbackFired verifies that the audit function is
// called with the correct counts when changes occurred.
func TestReconcileOnce_AuditCallbackFired(t *testing.T) {
	reader := &mockGitReader{
		files: map[string][]byte{
			clusterAddonsPath: []byte(twoClusterYAML),
		},
	}
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"cluster-1": kubeconfig("https://api.cluster-1.example.com"),
			"cluster-2": kubeconfig("https://api.cluster-2.example.com"),
		},
	}

	rec, _, _ := newTestReconciler(func() GitReader { return reader }, creds)

	var auditCreated, auditUpdated, auditDeleted int
	auditCalled := false
	rec.SetAuditFunc(func(created, updated, deleted int) {
		auditCalled = true
		auditCreated = created
		auditUpdated = updated
		auditDeleted = deleted
	})

	rec.ReconcileOnce(context.Background())

	if !auditCalled {
		t.Error("audit function was not called when secrets were created")
	}
	if auditCreated != 2 {
		t.Errorf("audit created = %d, want 2", auditCreated)
	}
	if auditUpdated != 0 {
		t.Errorf("audit updated = %d, want 0", auditUpdated)
	}
	if auditDeleted != 0 {
		t.Errorf("audit deleted = %d, want 0", auditDeleted)
	}
}

// TestReconcileOnce_AuditCallbackNotFired verifies that the audit function is
// NOT called when no changes occurred (all clusters skipped on second run).
func TestReconcileOnce_AuditCallbackNotFired(t *testing.T) {
	reader := &mockGitReader{
		files: map[string][]byte{
			clusterAddonsPath: []byte(twoClusterYAML),
		},
	}
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"cluster-1": kubeconfig("https://api.cluster-1.example.com"),
			"cluster-2": kubeconfig("https://api.cluster-2.example.com"),
		},
	}

	rec, _, _ := newTestReconciler(func() GitReader { return reader }, creds)

	// First run — creates secrets.
	rec.ReconcileOnce(context.Background())

	// Change the content so the second run does NOT short-circuit at the hash check,
	// but instead runs through and finds all secrets up-to-date (updated conservatively).
	// For this test, verify the audit is NOT called when content hash is unchanged.
	auditCalled := false
	rec.SetAuditFunc(func(created, updated, deleted int) {
		auditCalled = true
	})

	// Second run with identical content — hash check short-circuits, audit never called.
	rec.ReconcileOnce(context.Background())

	if auditCalled {
		t.Error("audit function was called on second run with unchanged content, expected no-op")
	}
}

// TestReconcileOnce_Trigger verifies that calling Trigger() causes a reconcile
// to run when the reconciler is running.
func TestReconcileOnce_Trigger(t *testing.T) {
	// Use a large interval so the ticker does not fire during the test.
	reader := &mockGitReader{
		files: map[string][]byte{
			clusterAddonsPath: []byte(twoClusterYAML),
		},
	}
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"cluster-1": kubeconfig("https://api.cluster-1.example.com"),
			"cluster-2": kubeconfig("https://api.cluster-2.example.com"),
		},
	}

	client := fake.NewSimpleClientset()
	mgr := NewManager(client, testNamespace)
	parser := config.NewParser()

	rec := NewReconciler(mgr, creds, func() GitReader { return reader }, parser, "main", "", 10*time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rec.Start(ctx)

	// Wait for the initial reconcile to complete (it runs immediately on Start).
	// Then update the content so a trigger causes a new pass.
	time.Sleep(100 * time.Millisecond)

	// Change the file content so the triggered run does not short-circuit.
	reader.files[clusterAddonsPath] = []byte(twoClusterYAMLAlt)

	// Trigger an immediate reconcile.
	rec.Trigger()

	// Wait long enough for the triggered reconcile to run.
	time.Sleep(300 * time.Millisecond)

	rec.Stop()

	// Both clusters should exist — from both the initial run and the triggered run.
	secrets, err := client.CoreV1().Secrets(testNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing secrets: %v", err)
	}
	if len(secrets.Items) != 2 {
		t.Errorf("expected 2 secrets, got %d", len(secrets.Items))
	}
}

// TestReconcileOnce_SecretPathOverride verifies that when a cluster has
// SecretPath set, GetCredentials is called with that path, not the cluster name.
func TestReconcileOnce_SecretPathOverride(t *testing.T) {
	const yaml = `
clusters:
  - name: prod-cluster
    secretPath: clusters/prod/my-cluster
    region: us-east-1
    labels: {}
`
	reader := &mockGitReader{
		files: map[string][]byte{
			clusterAddonsPath: []byte(yaml),
		},
	}

	var calledWith string
	credProvider := &trackingCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"clusters/prod/my-cluster": kubeconfig("https://api.prod-cluster.example.com"),
		},
		onGet: func(name string) {
			calledWith = name
		},
	}

	client := fake.NewSimpleClientset()
	mgr := NewManager(client, testNamespace)
	parser := config.NewParser()

	rec := NewReconciler(mgr, credProvider, func() GitReader { return reader }, parser, "main", "", 0)
	rec.ReconcileOnce(context.Background())

	if calledWith != "clusters/prod/my-cluster" {
		t.Errorf("GetCredentials called with %q, want %q", calledWith, "clusters/prod/my-cluster")
	}

	// Verify the created secret uses the cluster Name, not the SecretPath.
	_, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "prod-cluster", metav1.GetOptions{})
	if err != nil {
		t.Errorf("secret prod-cluster not found: %v", err)
	}
}

// --------------------------------------------------------------------------
// trackingCredProvider — records which name was passed to GetCredentials.
// --------------------------------------------------------------------------

type trackingCredProvider struct {
	creds map[string]*providers.Kubeconfig
	onGet func(name string)
}

func (t *trackingCredProvider) GetCredentials(name string) (*providers.Kubeconfig, error) {
	if t.onGet != nil {
		t.onGet(name)
	}
	kc, ok := t.creds[name]
	if !ok {
		return nil, fmt.Errorf("no credentials for %s", name)
	}
	return kc, nil
}
