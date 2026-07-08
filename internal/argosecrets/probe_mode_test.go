package argosecrets

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// zeroAddonClusterYAML declares one cluster with no addon-* labels — the
// shape that would normally get the connectivity-check label applied.
const zeroAddonClusterYAML = `
clusters:
  - name: fresh-cluster
    region: us-east-1
`

// TestReconcileOnce_ProbeModeFn_APITest_SkipsConnectivityCheckLabel is the
// legacy-reconciler counterpart of the clusterreconciler test with the same
// name: the V2-cleanup-85.4 probe_mode server setting must suppress the
// connectivity-check label on this writer too — otherwise the legacy
// reconciler would silently keep deploying the check app even after an
// operator switches probe_mode to api-test.
func TestReconcileOnce_ProbeModeFn_APITest_SkipsConnectivityCheckLabel(t *testing.T) {
	reader := &mockGitReader{
		files: map[string][]byte{
			clusterAddonsPath: []byte(zeroAddonClusterYAML),
		},
	}
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"fresh-cluster": kubeconfig("https://fresh-cluster.example.com"),
		},
	}

	rec, _, client := newTestReconciler(func() GitReader { return reader }, creds)

	// connectivityCheckEnabled defaults to true (feature "on" by the static
	// knob) — ProbeModeFn is the only thing suppressing the label here.
	rec.SetProbeModeFn(func(context.Context) bool { return true })

	rec.ReconcileOnce(context.Background())

	secret, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "fresh-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected secret 'fresh-cluster' to exist after reconcile: %v", err)
	}
	if models.HasConnectivityCheckLabel(secret.Labels) {
		t.Errorf("expected NO connectivity-check label in api-test mode, got labels=%v", secret.Labels)
	}
}

// TestReconcileOnce_ProbeModeFn_CheckApp_AppliesConnectivityCheckLabel is the
// control case: ProbeModeFn returning false (probe_mode = check-app,
// default) leaves the pre-85.4 behavior intact — the zero-addon cluster
// still gets the connectivity-check label.
func TestReconcileOnce_ProbeModeFn_CheckApp_AppliesConnectivityCheckLabel(t *testing.T) {
	reader := &mockGitReader{
		files: map[string][]byte{
			clusterAddonsPath: []byte(zeroAddonClusterYAML),
		},
	}
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"fresh-cluster": kubeconfig("https://fresh-cluster.example.com"),
		},
	}

	rec, _, client := newTestReconciler(func() GitReader { return reader }, creds)
	rec.SetProbeModeFn(func(context.Context) bool { return false })

	rec.ReconcileOnce(context.Background())

	secret, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "fresh-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected secret 'fresh-cluster' to exist after reconcile: %v", err)
	}
	if !models.HasConnectivityCheckLabel(secret.Labels) {
		t.Errorf("expected the connectivity-check label in check-app (default) mode, got labels=%v", secret.Labels)
	}
}

// TestReconcileOnce_ProbeModeFn_NilFallsBackToStaticFlag verifies backward
// compatibility: never calling SetProbeModeFn leaves the pre-85.4 behavior
// fully intact, driven only by SetConnectivityCheck / the default.
func TestReconcileOnce_ProbeModeFn_NilFallsBackToStaticFlag(t *testing.T) {
	reader := &mockGitReader{
		files: map[string][]byte{
			clusterAddonsPath: []byte(zeroAddonClusterYAML),
		},
	}
	creds := &mockCredProvider{
		creds: map[string]*providers.Kubeconfig{
			"fresh-cluster": kubeconfig("https://fresh-cluster.example.com"),
		},
	}

	rec, _, client := newTestReconciler(func() GitReader { return reader }, creds)
	rec.SetConnectivityCheck(false) // static knob off, ProbeModeFn never set

	rec.ReconcileOnce(context.Background())

	secret, err := client.CoreV1().Secrets(testNamespace).Get(context.Background(), "fresh-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected secret 'fresh-cluster' to exist after reconcile: %v", err)
	}
	if models.HasConnectivityCheckLabel(secret.Labels) {
		t.Errorf("expected NO connectivity-check label when SetConnectivityCheck(false) and ProbeModeFn unset, got labels=%v", secret.Labels)
	}
}
