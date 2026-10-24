package api

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/models"
)

// `argocd cluster add` names Secrets cluster-<host>-<hash>. The orphan list
// must name such a Secret by the cluster name inside it (episode 3 dry run,
// finding 5), or it never lines up with ArgoCD's cluster list.
func TestListSharkoOwnedSecretNames_UsesArgoCDClusterName(t *testing.T) {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-10.0.0.5-3621829410",
			Namespace: "argocd",
			// A real leftover (story A's rule): only those reach the set.
			Annotations: map[string]string{
				models.AnnotationRegistrationPending: models.RegistrationPendingTimestamp(time.Now().Add(-time.Hour)),
			},
			Labels: map[string]string{
				clusterreconciler.LabelManagedBy: clusterreconciler.LabelValueSharko,
				"argocd.argoproj.io/secret-type": "cluster",
			},
		},
		Data: map[string][]byte{"name": []byte("legacy-1")},
	}
	got := listSharkoOwnedSecretNames(context.Background(), fake.NewSimpleClientset(s), "argocd")
	if _, ok := got["legacy-1"]; !ok || len(got) != 1 {
		t.Fatalf("names = %v, want only legacy-1", got)
	}
}
