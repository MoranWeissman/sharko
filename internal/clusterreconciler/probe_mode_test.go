package clusterreconciler

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// TestPollOnce_ProbeModeFn_APITest_SkipsConnectivityCheckLabel verifies the
// V2-cleanup-85.4 wiring: when ProbeModeFn reports api-test mode, a newly
// created cluster Secret for a zero-addon cluster does NOT carry the
// connectivity-check label — no matter what the static
// Deps.DisableConnectivityCheck escape hatch says (it defaults to false /
// "feature on" here, matching the SHARKO_CONNECTIVITY_CHECK default).
func TestPollOnce_ProbeModeFn_APITest_SkipsConnectivityCheckLabel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("prod-eu")
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {Server: "https://prod-eu.example.com", Token: "fake-token"},
		},
	}
	k8sClient := fake.NewSimpleClientset()
	audits := &auditCollector{}
	fg := &fakeGit{files: map[string][]byte{DefaultManagedClustersPath: body}}

	r := New(Deps{
		GitProvider:              func() gitprovider.GitProvider { return fg },
		ArgoClient:               k8sClient,
		Vault:                    vault,
		AuditFn:                  audits.Add,
		TickInterval:             0,
		DisableConnectivityCheck: false, // static escape hatch OFF — feature "on" by that knob alone
		ProbeModeFn:              func(context.Context) bool { return true }, // probe_mode = api-test
	})
	r.pollOnce(ctx)

	secret, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected secret 'prod-eu' to exist after reconcile: %v", err)
	}
	if models.HasConnectivityCheckLabel(secret.Labels) {
		t.Errorf("expected NO connectivity-check label in api-test mode, got labels=%v", secret.Labels)
	}
}

// TestPollOnce_ProbeModeFn_CheckApp_AppliesConnectivityCheckLabel is the
// control case: probe_mode defaulting to check-app (ProbeModeFn returns
// false) still applies the connectivity-check label for a zero-addon
// cluster — the pre-85.4 behavior is unchanged.
func TestPollOnce_ProbeModeFn_CheckApp_AppliesConnectivityCheckLabel(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("prod-eu")
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {Server: "https://prod-eu.example.com", Token: "fake-token"},
		},
	}
	k8sClient := fake.NewSimpleClientset()
	audits := &auditCollector{}
	fg := &fakeGit{files: map[string][]byte{DefaultManagedClustersPath: body}}

	r := New(Deps{
		GitProvider:  func() gitprovider.GitProvider { return fg },
		ArgoClient:   k8sClient,
		Vault:        vault,
		AuditFn:      audits.Add,
		TickInterval: 0,
		ProbeModeFn:  func(context.Context) bool { return false }, // probe_mode = check-app (default)
	})
	r.pollOnce(ctx)

	secret, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected secret 'prod-eu' to exist after reconcile: %v", err)
	}
	if !models.HasConnectivityCheckLabel(secret.Labels) {
		t.Errorf("expected the connectivity-check label in check-app (default) mode, got labels=%v", secret.Labels)
	}
}

// TestPollOnce_ProbeModeFn_Nil_FallsBackToStaticFlag verifies backward
// compatibility: a nil ProbeModeFn (no settings store wired — local/dev
// mode) leaves the pre-85.4 behavior fully intact, driven only by
// DisableConnectivityCheck.
func TestPollOnce_ProbeModeFn_Nil_FallsBackToStaticFlag(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("prod-eu")
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"prod-eu": {Server: "https://prod-eu.example.com", Token: "fake-token"},
		},
	}
	k8sClient := fake.NewSimpleClientset()
	audits := &auditCollector{}
	fg := &fakeGit{files: map[string][]byte{DefaultManagedClustersPath: body}}

	r := New(Deps{
		GitProvider:              func() gitprovider.GitProvider { return fg },
		ArgoClient:               k8sClient,
		Vault:                    vault,
		AuditFn:                  audits.Add,
		TickInterval:             0,
		DisableConnectivityCheck: true, // no ProbeModeFn — this alone decides
	})
	r.pollOnce(ctx)

	secret, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected secret 'prod-eu' to exist after reconcile: %v", err)
	}
	if models.HasConnectivityCheckLabel(secret.Labels) {
		t.Errorf("expected NO connectivity-check label when DisableConnectivityCheck=true and ProbeModeFn=nil, got labels=%v", secret.Labels)
	}
}
