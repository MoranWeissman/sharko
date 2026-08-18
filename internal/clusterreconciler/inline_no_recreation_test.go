package clusterreconciler

// inline_no_recreation_test.go — the no-reconstruction guarantee for legacy
// inline-kubeconfig clusters (product ruling 1, Story 1 of the connection
// reconciliation view).
//
// An inline-registered cluster's credential existed ONLY inside its live
// ArgoCD connection Secret — no secrets backend holds a copy. When that
// Secret is deleted, the reconciler must NOT invent a reconstruction: the
// only route a write takes (ConnectionCredentialSpecForWrite) asks the
// credentials backend, the backend has nothing for this cluster, and the
// create is refused. This test pins that a deleted inline Secret stays
// deleted across reconcile passes — the API's row-11 answer ("cannot be
// restored from Git", migrate_credentials) depends on this being true.

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// inlineManagedClustersBody is a schema-valid managed-clusters envelope with
// one inline-registered cluster. Its credsSource is the recorded fact that
// the credential was pasted at registration and lives nowhere else.
var inlineManagedClustersBody = []byte(`apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: kind-inline
      credsSource: inline-kubeconfig
      labels:
        datadog: enabled
`)

func TestPollOnce_DeletedInlineSecret_IsNeverRecreated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// The defining state of a deleted inline cluster: the Secret is gone,
	// and the credentials backend has NO entry for it (fakeVault with no
	// canned creds errors on every name — exactly what a real backend does
	// for a cluster whose credential was never stored in it).
	k8sClient := fake.NewSimpleClientset()
	vault := &fakeVault{}
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, vault, audits, inlineManagedClustersBody)

	// Two passes, because "not on this tick" is not "never".
	r.pollOnce(ctx)
	r.pollOnce(ctx)

	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "kind-inline", metav1.GetOptions{}); err == nil {
		t.Fatal("the reconciler re-created a deleted inline-kubeconfig Secret — the credential cannot exist anywhere it could rebuild from, so this Secret is an invention")
	}
	for _, s := range secretsListUnfiltered(t, k8sClient, DefaultArgoCDNamespace) {
		t.Fatalf("unexpected Secret %q created for an inline cluster with no backend credential", s.Name)
	}

	// And the refusal is visible, not silent: the create attempt failed at
	// the credential fetch and was audited as such.
	// Ruling (f): the failure-shaped event, because no Secret was created.
	// "Connection Secret created · failure" was a past-tense claim nothing
	// backed up.
	if !hasEventForResource(audits.Snapshot(), EventClusterSecretCreateFailed, "cluster:kind-inline") {
		t.Fatal("expected a cluster_secret_create_failed audit entry for kind-inline — the refusal must be recorded, not silent")
	}
	if hasEventForResource(audits.Snapshot(), EventClusterSecretCreate, "cluster:kind-inline") {
		t.Fatal("a past-tense \"Secret created\" entry was written for a Secret that does not exist")
	}
}
