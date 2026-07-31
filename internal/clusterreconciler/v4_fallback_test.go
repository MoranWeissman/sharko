package clusterreconciler

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// TestPollOnce_V4Repo_ReadsFleetConnectionsFallback is the v4 Wave 1 Story
// 4.4 regression guard: on a v4 repo, configuration/managed-clusters.yaml
// (DefaultManagedClustersPath) genuinely does not exist — the cluster
// registry lives at managed-clusters.yaml instead (design doc §2.4, same
// kind/shape, different location). Before this fix the reconciler would
// treat that as "empty desired state" and never create the ArgoCD cluster
// Secret for a cluster registered on a v4 repo — this test proves the
// fallback read picks up managed-clusters.yaml and reconciles normally.
func TestPollOnce_V4Repo_ReadsFleetConnectionsFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	body := envelopedManagedClusters("prod-eu")
	gp := &fakeGit{files: map[string][]byte{
		v4ManagedClustersPath: body, // v3 DefaultManagedClustersPath is absent
	}}
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

	r := newReconcilerForTest(t, gp, k8sClient, vault, audits, nil)
	r.pollOnce(ctx)

	secret, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected the v4-registered cluster's Secret to be created via the managed-clusters.yaml fallback: %v", err)
	}
	if !IsManagedBySharko(secret) {
		t.Fatalf("created secret is missing the sharko ownership label: labels=%v", secret.Labels)
	}
}

// TestPollOnce_V3PathPresent_NeverTriesV4Fallback proves the fallback is
// truly a fallback: when the configured v3 path resolves, the reconciler
// must not also read managed-clusters.yaml (a real repo could have a
// stray file there, e.g. mid-migration, and it must never leak into the
// v3 desired state).
func TestPollOnce_V3PathPresent_NeverTriesV4Fallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	v3Body := envelopedManagedClusters("v3-cluster")
	v4Body := envelopedManagedClusters("v4-cluster-should-be-ignored")
	gp := &fakeGit{files: map[string][]byte{
		DefaultManagedClustersPath: v3Body,
		v4ManagedClustersPath:          v4Body,
	}}
	vault := &fakeVault{
		creds: map[string]*providers.Kubeconfig{
			"v3-cluster":                   {Server: "https://v3.example.com", CAData: []byte("ca"), Token: "tk"},
			"v4-cluster-should-be-ignored": {Server: "https://v4.example.com", CAData: []byte("ca"), Token: "tk"},
		},
	}
	k8sClient := fake.NewSimpleClientset()
	audits := &auditCollector{}

	r := newReconcilerForTest(t, gp, k8sClient, vault, audits, nil)
	r.pollOnce(ctx)

	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "v3-cluster", metav1.GetOptions{}); err != nil {
		t.Fatalf("expected v3-cluster's Secret to be created: %v", err)
	}
	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "v4-cluster-should-be-ignored", metav1.GetOptions{}); err == nil {
		t.Fatal("managed-clusters.yaml must be ignored when the v3 path resolves — it was read anyway")
	}
}
