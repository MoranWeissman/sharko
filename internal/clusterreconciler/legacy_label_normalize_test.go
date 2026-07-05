package clusterreconciler

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// TestPollOnce_LegacyTrueLabel_NormalizedToEnabled covers the self-heal path
// (V2-cleanup-20, decision #4). A cluster registered before the fix carries a
// legacy addon label `monitoring: "true"` in managed-clusters.yaml. The
// ArgoCD ApplicationSet selector only treats "enabled" as on, so "true" would
// leave the addon undeployed. On its next write the reconciler must upgrade
// the legacy value to the canonical "enabled" on the ArgoCD cluster Secret so
// the addon converges with no manual re-register. "false" likewise becomes
// "disabled".
func TestPollOnce_LegacyTrueLabel_NormalizedToEnabled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := []byte(`apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: prod-eu
      labels:
        monitoring: "true"
        logging: "false"
        already-canonical: enabled
`)

	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {
				Server: "https://prod-eu.example.com",
				CAData: []byte("fake-ca-bytes"),
				Token:  "fake-token",
			},
		},
	}
	k8sClient := fake.NewSimpleClientset()
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, body)
	r.pollOnce(ctx)

	secret, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected secret 'prod-eu' to exist after reconcile: %v", err)
	}

	if got := secret.Labels["monitoring"]; got != "enabled" {
		t.Errorf("legacy 'monitoring: true' was NOT normalized: got %q, want %q", got, "enabled")
	}
	if got := secret.Labels["logging"]; got != "disabled" {
		t.Errorf("legacy 'logging: false' was NOT normalized: got %q, want %q", got, "disabled")
	}
	// An already-canonical value must be left untouched.
	if got := secret.Labels["already-canonical"]; got != "enabled" {
		t.Errorf("already-canonical label changed unexpectedly: got %q, want %q", got, "enabled")
	}
}
