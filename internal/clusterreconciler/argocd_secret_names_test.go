package clusterreconciler

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// `argocd cluster add` names a cluster Secret cluster-<host>-<hash> and puts
// the cluster name inside it. The reconciler must line those Secrets up with
// git by the name inside, never by the Secret's Kubernetes name (episode 3
// dry run, finding 5).

const argoAddedName = "cluster-10.0.0.5-3621829410"

func argoAddedClusterSecret(k8sName, clusterName string, labels map[string]string) *corev1.Secret {
	l := map[string]string{"argocd.argoproj.io/secret-type": "cluster"}
	for k, v := range labels {
		l[k] = v
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: k8sName, Namespace: DefaultArgoCDNamespace, Labels: l},
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte(clusterName),
			"server": []byte("https://10.0.0.5:6443"),
		},
	}
}

// A managed cluster whose Secret has an ArgoCD-style name is in git: the
// reconciler must neither delete it as an orphan nor create a second Secret.
// Run both with and without the adopted mark: without it, only the
// lookup by ArgoCD's name keeps the orphan sweep away.
func TestPollOnce_ArgoCDNamedSecret_InGit_NotDeletedNotDuplicated(t *testing.T) {
	for _, adopted := range []bool{true, false} {
		adopted := adopted
		t.Run(map[bool]string{true: "adopted", false: "registered"}[adopted], func(t *testing.T) {
			t.Parallel()
			testArgoCDNamedInGit(t, adopted)
		})
	}
}

func testArgoCDNamedInGit(t *testing.T, adopted bool) {
	ctx := context.Background()

	s := argoAddedClusterSecret(argoAddedName, "legacy-1", map[string]string{LabelManagedBy: LabelValueSharko})
	if adopted {
		s.Annotations = map[string]string{"sharko.dev/adopted": "true"}
	}
	k8sClient := fake.NewSimpleClientset(s, keeperSecret())
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, checkVault("legacy-1", "keeper"), audits, envelopedManagedClusters("legacy-1", "keeper"))
	r.pollOnce(ctx)

	for _, a := range k8sClient.Actions() {
		if a.GetVerb() == "delete" || a.GetVerb() == "create" {
			t.Fatalf("unexpected %s on %s — the cluster is in git and its Secret is there", a.GetVerb(), actionTarget(a))
		}
	}
	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, argoAddedName, metav1.GetOptions{}); err != nil {
		t.Fatalf("the user's Secret %s is gone: %v", argoAddedName, err)
	}
	if n := len(secretsListUnfiltered(t, k8sClient, DefaultArgoCDNamespace)); n != 2 {
		t.Fatalf("want 2 Secrets (legacy-1 and keeper), got %d", n)
	}
}

// An orphan Sharko Secret with an ArgoCD-style name is deleted by its real
// Kubernetes name.
func TestPollOnce_ArgoCDNamedSecret_Orphan_DeletedByRealName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	s := argoAddedClusterSecret(argoAddedName, "gone-1", map[string]string{LabelManagedBy: LabelValueSharko})
	k8sClient := fake.NewSimpleClientset(s, keeperSecret())
	audits := &auditCollector{}

	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, envelopedManagedClusters("keeper"))
	r.pollOnce(ctx)

	if _, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, argoAddedName, metav1.GetOptions{}); err == nil {
		t.Fatalf("orphan %s should have been deleted", argoAddedName)
	}
	if !hasEventForResource(audits.Snapshot(), "cluster_secret_delete", "cluster:gone-1") {
		t.Fatalf("expected a delete audit for cluster gone-1; got %v", audits.Snapshot())
	}
}

// A cluster in git whose ArgoCD connection exists WITHOUT Sharko's label,
// under an ArgoCD-style name, is Adopt territory: no second Secret.
func TestPollOnce_ArgoCDNamedUnlabeledSecret_NoDuplicateCreated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	k8sClient := fake.NewSimpleClientset(argoAddedClusterSecret(argoAddedName, "legacy-1", nil))
	audits := &auditCollector{}

	// Credentials are there, so nothing but the lookup stands between
	// the reconciler and a second Secret.
	r := newReconcilerForTest(t, nil, k8sClient, checkVault("legacy-1"), audits, envelopedManagedClusters("legacy-1"))
	r.pollOnce(ctx)

	for _, a := range k8sClient.Actions() {
		if a.GetVerb() == "create" {
			t.Fatalf("created %s next to the user's own %s", actionTarget(a), argoAddedName)
		}
	}
}
