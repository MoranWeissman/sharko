package secrets

// sync_cluster_test.go — tests for SyncCluster, the git-backed engine
// behind POST /clusters/{name}/secrets/refresh (task #152, story 152.A).
// The security property under test: what a refresh delivers comes from
// the Git catalog and NOWHERE else — a git change changes what the next
// refresh delivers, a cluster or addon Git does not define is refused,
// and only the named cluster is ever touched.

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// twoClusterAddonsYAML lists two managed clusters that both run datadog —
// used to prove SyncCluster touches ONLY the cluster it was asked about.
const twoClusterAddonsYAML = `
clusters:
  - name: prod-cluster
    labels:
      datadog: enabled
  - name: other-cluster
    labels:
      datadog: enabled
`

// catalogWithSecretsV2 is catalogWithSecrets after a Git change: the
// api-key now points at a DIFFERENT provider path. A refresh after this
// change must deliver the value at the new path — proof the refresh reads
// Git, not anything remembered in memory.
const catalogWithSecretsV2 = `
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
          api-key: "secrets/datadog/rotated-api-key"
          app-key: "secrets/datadog/app-key"
`

// TestSyncCluster_DeliversGitContent_AndFollowsGitChanges is the story's
// core acceptance test: a refresh delivers exactly what Git defines, and a
// refresh AFTER a Git change delivers the new Git content.
func TestSyncCluster_DeliversGitContent_AndFollowsGitChanges(t *testing.T) {
	client := fake.NewSimpleClientset()
	gitReader := standardGitReader(catalogWithSecrets)

	r := newReconciler(
		gitReader,
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key":         []byte("the-api-key"),
			"secrets/datadog/rotated-api-key": []byte("the-rotated-api-key"),
			"secrets/datadog/app-key":         []byte("the-app-key"),
		}},
		fakeRemoteClientFn(client),
	)

	refreshed, failed, err := r.SyncCluster(context.Background(), "prod-cluster", "")
	if err != nil {
		t.Fatalf("SyncCluster: %v", err)
	}
	if len(refreshed) != 1 || refreshed[0] != "datadog-secret" {
		t.Fatalf("refreshed = %v, want [datadog-secret]", refreshed)
	}
	if len(failed) != 0 {
		t.Fatalf("failed = %v, want empty", failed)
	}

	secret, getErr := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("expected secret to be created: %v", getErr)
	}
	if string(secret.Data["api-key"]) != "the-api-key" {
		t.Errorf("api-key = %q, want the value Git's definition points at", secret.Data["api-key"])
	}

	// The refresh keeps the same per-item record the periodic pass keeps.
	if outcome, ok := r.LastItemOutcome("prod-cluster", "datadog"); !ok || outcome != string(ItemOutcomeCreated) {
		t.Errorf("LastItemOutcome = %q ok=%v, want created/true", outcome, ok)
	}

	// --- The Git change: api-key now points at a different provider path.
	gitReader.files["configuration/addons-catalog.yaml"] = []byte(catalogWithSecretsV2)

	refreshed, failed, err = r.SyncCluster(context.Background(), "prod-cluster", "")
	if err != nil {
		t.Fatalf("SyncCluster after git change: %v", err)
	}
	if len(refreshed) != 1 || len(failed) != 0 {
		t.Fatalf("after git change: refreshed=%v failed=%v, want one refreshed, none failed", refreshed, failed)
	}

	secret, getErr = client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("secret vanished after second refresh: %v", getErr)
	}
	if string(secret.Data["api-key"]) != "the-rotated-api-key" {
		t.Errorf("api-key after git change = %q, want %q (the refresh must follow Git)",
			secret.Data["api-key"], "the-rotated-api-key")
	}
}

// TestSyncCluster_OnlyTouchesTheNamedCluster: two clusters both run the
// addon; a refresh for one must write to that one and leave the other
// completely untouched.
func TestSyncCluster_OnlyTouchesTheNamedCluster(t *testing.T) {
	prodClient := fake.NewSimpleClientset()
	otherClient := fake.NewSimpleClientset()

	gitReader := &mockGitReader{files: map[string][]byte{
		"configuration/addons-catalog.yaml":   []byte(catalogWithSecrets),
		"configuration/managed-clusters.yaml": []byte(twoClusterAddonsYAML),
	}}

	// perClusterCredProvider + perClusterClientFn are the shared per-cluster
	// routing fakes from orphans_test.go: the kubeconfig IS the cluster's
	// credential-lookup key, so the factory can route each write to its own
	// fake clientset and the test can prove where a write landed.
	r := newReconciler(
		gitReader,
		&perClusterCredProvider{},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("k1"),
			"secrets/datadog/app-key": []byte("k2"),
		}},
		perClusterClientFn(map[string]kubernetes.Interface{
			"prod-cluster":  prodClient,
			"other-cluster": otherClient,
		}),
	)

	refreshed, failed, err := r.SyncCluster(context.Background(), "prod-cluster", "")
	if err != nil {
		t.Fatalf("SyncCluster: %v", err)
	}
	if len(refreshed) != 1 || len(failed) != 0 {
		t.Fatalf("refreshed=%v failed=%v, want exactly one refreshed", refreshed, failed)
	}

	if _, getErr := prodClient.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{}); getErr != nil {
		t.Errorf("named cluster should have the secret: %v", getErr)
	}
	if _, getErr := otherClient.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{}); getErr == nil {
		t.Errorf("the OTHER cluster must be left untouched, but the secret exists there")
	}
}

// TestSyncCluster_UnknownCluster_Refused: a cluster the managed-clusters
// file does not list is refused with the fixed sentinel.
func TestSyncCluster_UnknownCluster_Refused(t *testing.T) {
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{},
		fakeRemoteClientFn(fake.NewSimpleClientset()),
	)

	_, _, err := r.SyncCluster(context.Background(), "not-a-cluster", "")
	if !errors.Is(err, ErrClusterNotInGit) {
		t.Fatalf("err = %v, want ErrClusterNotInGit", err)
	}
}

// TestSyncCluster_AddonNotInGit_Refused: naming an addon Git does not
// define for this cluster refuses the whole call — and writes nothing.
func TestSyncCluster_AddonNotInGit_Refused(t *testing.T) {
	client := fake.NewSimpleClientset()
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("k1"),
			"secrets/datadog/app-key": []byte("k2"),
		}},
		fakeRemoteClientFn(client),
	)

	_, _, err := r.SyncCluster(context.Background(), "prod-cluster", "vault")
	if !errors.Is(err, ErrAddonNotInGit) {
		t.Fatalf("err = %v, want ErrAddonNotInGit", err)
	}

	secrets, listErr := client.CoreV1().Secrets("").List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatalf("listing secrets: %v", listErr)
	}
	if len(secrets.Items) != 0 {
		t.Errorf("a refused refresh must write nothing, found %d secrets", len(secrets.Items))
	}
}

// TestSyncCluster_NoGitConnection: no Git connection means nothing can be
// resolved, so the whole call is refused with the shared sentinel.
func TestSyncCluster_NoGitConnection(t *testing.T) {
	r := newReconciler(
		nil, // gitReader closure returns nil below
		&mockCredProvider{},
		&mockSecretProvider{},
		fakeRemoteClientFn(fake.NewSimpleClientset()),
	)
	r.gitReader = func() GitReader { return nil }

	_, _, err := r.SyncCluster(context.Background(), "prod-cluster", "")
	if !errors.Is(err, ErrNoGitConnection) {
		t.Fatalf("err = %v, want ErrNoGitConnection", err)
	}
}

// TestSyncCluster_ForeignSecretLeftAlone: a secret with the right name
// that Sharko did not create is never written — the same ownership gate
// the periodic pass and SyncOne go through. It lands in NEITHER list.
func TestSyncCluster_ForeignSecretLeftAlone(t *testing.T) {
	foreign := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "datadog-secret",
			Namespace: "monitoring",
			// no app.kubernetes.io/managed-by=sharko label — somebody
			// else owns this.
		},
		Data: map[string][]byte{"api-key": []byte("theirs")},
	}
	client := fake.NewSimpleClientset(foreign)

	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{values: map[string][]byte{
			"secrets/datadog/api-key": []byte("ours"),
			"secrets/datadog/app-key": []byte("ours-too"),
		}},
		fakeRemoteClientFn(client),
	)

	refreshed, failed, err := r.SyncCluster(context.Background(), "prod-cluster", "")
	if err != nil {
		t.Fatalf("SyncCluster: %v", err)
	}
	if len(refreshed) != 0 || len(failed) != 0 {
		t.Fatalf("refreshed=%v failed=%v, want both empty — a foreign secret is a boundary, not work", refreshed, failed)
	}

	got, getErr := client.CoreV1().Secrets("monitoring").Get(context.Background(), "datadog-secret", metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("foreign secret should still exist: %v", getErr)
	}
	if string(got.Data["api-key"]) != "theirs" {
		t.Errorf("foreign secret was overwritten: api-key = %q, want %q", got.Data["api-key"], "theirs")
	}
}

// TestSyncCluster_KnownClusterWithNoPushes_EmptySuccess: a cluster Git
// lists but with no addon-values definitions is a clean empty refresh —
// not a refusal (the cluster is real; there is simply nothing to push).
func TestSyncCluster_KnownClusterWithNoPushes_EmptySuccess(t *testing.T) {
	r := newReconciler(
		standardGitReader(catalogWithoutSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{},
		fakeRemoteClientFn(fake.NewSimpleClientset()),
	)

	refreshed, failed, err := r.SyncCluster(context.Background(), "prod-cluster", "")
	if err != nil {
		t.Fatalf("SyncCluster: %v", err)
	}
	if len(refreshed) != 0 || len(failed) != 0 {
		t.Errorf("refreshed=%v failed=%v, want both empty", refreshed, failed)
	}
}

// TestSyncCluster_ProviderFailureLandsInFailed: a value the provider
// cannot serve fails that one secret, and the failure is reported by
// destination secret NAME only.
func TestSyncCluster_ProviderFailureLandsInFailed(t *testing.T) {
	r := newReconciler(
		standardGitReader(catalogWithSecrets),
		&mockCredProvider{kubeconfig: []byte("fake-kubeconfig")},
		&mockSecretProvider{err: errors.New("vault is down")},
		fakeRemoteClientFn(fake.NewSimpleClientset()),
	)

	refreshed, failed, err := r.SyncCluster(context.Background(), "prod-cluster", "")
	if err != nil {
		t.Fatalf("SyncCluster: %v", err)
	}
	if len(refreshed) != 0 {
		t.Errorf("refreshed = %v, want empty", refreshed)
	}
	if len(failed) != 1 || failed[0] != "datadog-secret" {
		t.Errorf("failed = %v, want [datadog-secret]", failed)
	}
}
