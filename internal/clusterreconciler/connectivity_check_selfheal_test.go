package clusterreconciler

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// TestConnectivityCheckTick_ZeroAddonsMissingLabel_GetsStamped is the walk
// finding "bare spoke" itself: a managed cluster Secret that exists WITHOUT
// the connectivity-check label (e.g. one created before this fix, or by any
// path other than createOne) and has zero enabled addons gets the label
// stamped on the very next tick — with self-heal OFF, proving this
// convergence does not depend on the opt-in managed_cluster_self_heal
// setting the way OTHER addon-label drift correction does.
func TestConnectivityCheckTick_ZeroAddonsMissingLabel_GetsStamped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := fake.NewSimpleClientset()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spoke-us",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
				// no connectivity-check label — the bug
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"name":   "spoke-us",
			"server": "https://spoke-us.example.com",
			"config": `{"execProviderConfig":{}}`,
		},
	}
	if _, err := client.CoreV1().Secrets("argocd").Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	fg := &fakeGit{files: map[string][]byte{DefaultManagedClustersPath: envelopedManagedClusters("spoke-us")}}
	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"spoke-us": {Server: "https://spoke-us.example.com", Token: "fake-token"},
	}}
	audits := &auditCollector{}

	r := New(Deps{
		GitProvider:  func() gitprovider.GitProvider { return fg },
		ArgoClient:   client,
		Vault:        vault,
		AuditFn:      audits.Add,
		Namespace:    "argocd",
		TickInterval: 0,
		SelfHealFn:   nil, // OFF — proves this is independent of opt-in self-heal
	})

	r.pollOnce(ctx)

	updated, err := client.CoreV1().Secrets("argocd").Get(ctx, "spoke-us", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get updated secret: %v", err)
	}
	if updated.Labels[models.LabelConnectivityCheck] != models.LabelEnabled {
		t.Errorf("expected canonical connectivity-check label stamped, got labels=%v", updated.Labels)
	}
	if updated.Labels[models.LabelConnectivityCheckLegacy] != models.LabelEnabled {
		t.Errorf("expected legacy connectivity-check label stamped, got labels=%v", updated.Labels)
	}
	if updated.Labels[LabelManagedBy] != LabelValueSharko {
		t.Error("managed-by must be untouched by connectivity-check convergence")
	}

	if !hasEventForResource(audits.Snapshot(), "cluster_secret_connectivity_check_sync", "spoke-us") {
		t.Error("expected a cluster_secret_connectivity_check_sync audit entry")
	}
}

// TestConnectivityCheckTick_FirstAddonEnabled_LabelRemoved verifies the
// other direction: once git shows an enabled addon for a cluster whose
// addon labels already match git (no OTHER drift this tick), a previously
// stamped connectivity-check label is removed on this tick alone.
func TestConnectivityCheckTick_FirstAddonEnabled_LabelRemoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := fake.NewSimpleClientset()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spoke-us",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy:                      LabelValueSharko,
				"argocd.argoproj.io/secret-type":    "cluster",
				"datadog":                           "enabled", // already matches git below
				models.LabelConnectivityCheck:       models.LabelEnabled,
				models.LabelConnectivityCheckLegacy: models.LabelEnabled,
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"name":   "spoke-us",
			"server": "https://spoke-us.example.com",
			"config": `{"execProviderConfig":{}}`,
		},
	}
	if _, err := client.CoreV1().Secrets("argocd").Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	managedClustersBody := []byte(`
apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: spoke-us
      labels:
        datadog: enabled
`)
	fg := &fakeGit{files: map[string][]byte{DefaultManagedClustersPath: managedClustersBody}}
	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"spoke-us": {Server: "https://spoke-us.example.com", Token: "fake-token"},
	}}
	audits := &auditCollector{}

	r := New(Deps{
		GitProvider:  func() gitprovider.GitProvider { return fg },
		ArgoClient:   client,
		Vault:        vault,
		AuditFn:      audits.Add,
		Namespace:    "argocd",
		TickInterval: 0,
	})

	r.pollOnce(ctx)

	updated, err := client.CoreV1().Secrets("argocd").Get(ctx, "spoke-us", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get updated secret: %v", err)
	}
	if models.HasConnectivityCheckLabel(updated.Labels) {
		t.Errorf("expected connectivity-check label removed once an addon is enabled, got labels=%v", updated.Labels)
	}
	if updated.Labels["datadog"] != "enabled" {
		t.Error("datadog addon label must be untouched")
	}
}

// TestConnectivityCheckTick_AllAddonsDisabled_LabelReturns verifies the
// self-healing round trip described in internal/models/connectivity_check.go:
// once every addon is disabled again (matching git, no OTHER drift), the
// label comes back even though nothing else about this cluster changed.
func TestConnectivityCheckTick_AllAddonsDisabled_LabelReturns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := fake.NewSimpleClientset()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spoke-us",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
				"datadog":                        "disabled", // already matches git below
				// connectivity-check label absent — stale from when datadog
				// was last enabled, never independently re-derived
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"name":   "spoke-us",
			"server": "https://spoke-us.example.com",
			"config": `{"execProviderConfig":{}}`,
		},
	}
	if _, err := client.CoreV1().Secrets("argocd").Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	managedClustersBody := []byte(`
apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: spoke-us
      labels:
        datadog: disabled
`)
	fg := &fakeGit{files: map[string][]byte{DefaultManagedClustersPath: managedClustersBody}}
	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"spoke-us": {Server: "https://spoke-us.example.com", Token: "fake-token"},
	}}

	r := New(Deps{
		GitProvider:  func() gitprovider.GitProvider { return fg },
		ArgoClient:   client,
		Vault:        vault,
		AuditFn:      func(audit.Entry) {},
		Namespace:    "argocd",
		TickInterval: 0,
	})

	r.pollOnce(ctx)

	updated, err := client.CoreV1().Secrets("argocd").Get(ctx, "spoke-us", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get updated secret: %v", err)
	}
	if !models.HasConnectivityCheckLabel(updated.Labels) {
		t.Errorf("expected connectivity-check label to return once all addons are disabled, got labels=%v", updated.Labels)
	}
}

// TestConnectivityCheckTick_FeatureOff_LabelStripped verifies the static
// DisableConnectivityCheck escape hatch strips a stale label even for a
// zero-addon cluster, on the normal tick, not just at create time.
func TestConnectivityCheckTick_FeatureOff_LabelStripped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := fake.NewSimpleClientset()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spoke-us",
			Namespace: "argocd",
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
				models.LabelConnectivityCheck:    models.LabelEnabled,
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"name":   "spoke-us",
			"server": "https://spoke-us.example.com",
			"config": `{"execProviderConfig":{}}`,
		},
	}
	if _, err := client.CoreV1().Secrets("argocd").Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	fg := &fakeGit{files: map[string][]byte{DefaultManagedClustersPath: envelopedManagedClusters("spoke-us")}}
	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"spoke-us": {Server: "https://spoke-us.example.com", Token: "fake-token"},
	}}

	r := New(Deps{
		GitProvider:              func() gitprovider.GitProvider { return fg },
		ArgoClient:               client,
		Vault:                    vault,
		AuditFn:                  func(audit.Entry) {},
		Namespace:                "argocd",
		TickInterval:             0,
		DisableConnectivityCheck: true,
	})

	r.pollOnce(ctx)

	updated, err := client.CoreV1().Secrets("argocd").Get(ctx, "spoke-us", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get updated secret: %v", err)
	}
	if models.HasConnectivityCheckLabel(updated.Labels) {
		t.Errorf("expected connectivity-check label stripped when the feature is off, got labels=%v", updated.Labels)
	}
}

// TestConnectivityCheckTick_SelfManaged_Untouched verifies the guest
// stance: a self-managed (user-owned) connection with zero enabled addons
// never gets the connectivity-check label, on the normal tick, exactly like
// createOne never touches it for self-managed connections.
func TestConnectivityCheckTick_SelfManaged_Untouched(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	client := fake.NewSimpleClientset()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "self-managed-cluster",
			Namespace: "argocd",
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "cluster",
				// no managed-by — user-owned connection
			},
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"name":   "self-managed-cluster",
			"server": "https://self.example.com",
			"config": `{"execProviderConfig":{}}`,
		},
	}
	if _, err := client.CoreV1().Secrets("argocd").Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	managedClustersBody := []byte(`
apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: self-managed-cluster
      connectionManagedBy: user
`)
	fg := &fakeGit{files: map[string][]byte{DefaultManagedClustersPath: managedClustersBody}}
	vault := &fakeVault{creds: map[string]*providers.Kubeconfig{
		"self-managed-cluster": {Server: "https://self.example.com", Token: "fake-token"},
	}}

	r := New(Deps{
		GitProvider:  func() gitprovider.GitProvider { return fg },
		ArgoClient:   client,
		Vault:        vault,
		AuditFn:      func(audit.Entry) {},
		Namespace:    "argocd",
		TickInterval: 0,
	})

	r.pollOnce(ctx)

	updated, err := client.CoreV1().Secrets("argocd").Get(ctx, "self-managed-cluster", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get updated secret: %v", err)
	}
	if models.HasConnectivityCheckLabel(updated.Labels) {
		t.Errorf("self-managed connection must never get the connectivity-check label, got labels=%v", updated.Labels)
	}
}
