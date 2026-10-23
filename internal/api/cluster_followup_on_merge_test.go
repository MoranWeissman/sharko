package api

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// EP3-DRYRUN-FIXES call 1: when a remove PR that kept the connection is
// merged later, the merge hook takes Sharko's label off the connection and
// deletes nothing. It does nothing when the cluster is back in Git, and it
// ignores every other kind of PR.

func keptRemovePR(cluster string) prtracker.PRInfo {
	return prtracker.PRInfo{
		PRID:      12,
		Cluster:   cluster,
		Operation: prtracker.OpRemoveCluster,
		FollowUp:  &prtracker.ClusterFollowUp{Cleanup: "git", KeepConnection: true, CredentialKey: cluster},
	}
}

func ownedSecret(name string) *corev1.Secret {
	return brownfieldSecret(name, map[string]string{argosecrets.LabelManagedBy: argosecrets.ManagedByValue}, nil)
}

func TestFinishClusterFollowUpOnMerge_KeptConnectionLosesTheLabel(t *testing.T) {
	gp := newTakeoverFakeGP() // fleet record holds only other-cluster
	srv, _, k8s := takeoverTestServer(t, gp, []*corev1.Secret{ownedSecret("prod-eu")}, fakeAppSetReader{})

	srv.FinishClusterFollowUpOnMerge(context.Background(), keptRemovePR("prod-eu"))

	sec, err := k8s.CoreV1().Secrets("argocd").Get(context.Background(), "prod-eu", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("the kept connection was deleted: %v", err)
	}
	if _, still := sec.Labels[argosecrets.LabelManagedBy]; still {
		t.Fatalf("the kept connection still carries Sharko's label: %v", sec.Labels)
	}
}

func TestFinishClusterFollowUpOnMerge_DoesNothingWhenItShouldNot(t *testing.T) {
	cases := []struct {
		name  string
		pr    prtracker.PRInfo
		inGit bool
	}{
		{"the cluster is back in Git", keptRemovePR("prod-eu"), true},
		{"a register PR", prtracker.PRInfo{PRID: 3, Cluster: "prod-eu", Operation: "register", FollowUp: &prtracker.ClusterFollowUp{KeepConnection: true}}, false},
		{"a remove PR tracked before the hook existed", prtracker.PRInfo{PRID: 4, Cluster: "prod-eu", Operation: prtracker.OpRemoveCluster}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gp := newTakeoverFakeGP()
			if tc.inGit {
				gp.files[orchestrator.V4ManagedClustersPath] = []byte("clusters:\n  - name: other-cluster\n  - name: prod-eu\n")
			}
			srv, _, k8s := takeoverTestServer(t, gp, []*corev1.Secret{ownedSecret("prod-eu")}, fakeAppSetReader{})

			srv.FinishClusterFollowUpOnMerge(context.Background(), tc.pr)

			sec, err := k8s.CoreV1().Secrets("argocd").Get(context.Background(), "prod-eu", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("the connection was deleted: %v", err)
			}
			if sec.Labels[argosecrets.LabelManagedBy] != argosecrets.ManagedByValue {
				t.Fatalf("the connection was changed: %v", sec.Labels)
			}
		})
	}
}
