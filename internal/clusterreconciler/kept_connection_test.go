package clusterreconciler

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/models"
)

// EP3-DRYRUN-FIXES call 1 + 4: a removal that keeps the ArgoCD connection
// must never end with the orphan sweep deleting that connection. While the
// tracked remove PR says "keep", the sweep takes Sharko's label off instead
// of deleting; when the tracked PR list cannot be read, it keeps the Secret.

func keptOrphanSecret(k8sName, shownName string) *corev1.Secret {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k8sName,
			Namespace: DefaultArgoCDNamespace,
			Labels: map[string]string{
				LabelManagedBy:                   LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
			},
		},
		Type: corev1.SecretTypeOpaque,
	}
	if shownName != "" {
		s.Data = map[string][]byte{"name": []byte(shownName)}
	}
	return s
}

func TestPollOnce_KeptConnection_LabelStrippedNotDeleted(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		k8sName   string
		shownName string
		kept      string
	}{
		{"matched by the Secret name", "kept-one", "", "kept-one"},
		{"matched by the cluster name ArgoCD shows", "cluster-kept-host-1a2b3c", "kept-two", "kept-two"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			k8sClient := fake.NewSimpleClientset(keptOrphanSecret(tc.k8sName, tc.shownName), keeperSecret())
			audits := &auditCollector{}
			r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, audits, envelopedManagedClusters("keeper"))
			r.deps.KeptConnectionsFn = func(context.Context) (map[string]models.ConnectionHold, error) {
				return map[string]models.ConnectionHold{tc.kept: {ReleasePR: 1, Hold: true}}, nil
			}
			r.pollOnce(ctx)

			got, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, tc.k8sName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("a connection a removal keeps was deleted: %v", err)
			}
			if _, still := got.Labels[LabelManagedBy]; still {
				t.Fatalf("a kept connection still carries Sharko's label, so it would show as a leftover: %v", got.Labels)
			}
			if hasEventForResource(audits.Snapshot(), "cluster_secret_delete", "cluster:"+tc.k8sName) {
				t.Fatal("a delete was audited for a kept connection")
			}
		})
	}
}

func TestPollOnce_KeptConnectionsUnreadable_KeepsTheSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	k8sClient := fake.NewSimpleClientset(keptOrphanSecret("maybe-kept", ""), keeperSecret())
	r := newReconcilerForTest(t, nil, k8sClient, &fakeVault{}, &auditCollector{}, envelopedManagedClusters("keeper"))
	r.deps.KeptConnectionsFn = func(context.Context) (map[string]models.ConnectionHold, error) {
		return nil, errors.New("the PR tracker ConfigMap could not be read")
	}
	r.pollOnce(ctx)

	got, err := k8sClient.CoreV1().Secrets(DefaultArgoCDNamespace).Get(ctx, "maybe-kept", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Sharko deleted a connection while it could not tell whether a removal keeps it: %v", err)
	}
	if got.Labels[LabelManagedBy] != LabelValueSharko {
		t.Fatalf("Sharko changed a connection while it could not tell whether a removal keeps it: %v", got.Labels)
	}
}
