package api

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

const adoptFinishNS = "argocd"

// adoptWaitingSecret is a connection someone else made, with the waiting
// mark an open adopt pull request leaves on it.
func adoptWaitingSecret(mark string) *corev1.Secret {
	ann := map[string]string{}
	if mark != "" {
		ann[argosecrets.AnnotationAdoptPendingPR] = mark
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster-10.0.0.5-1", Namespace: adoptFinishNS,
			Labels:      map[string]string{argosecrets.LabelSecretType: "cluster", "env": "prod"},
			Annotations: ann,
		},
		Data: map[string][]byte{"name": []byte("prod-eu"), "server": []byte("https://prod-eu.example.com"), "config": []byte(`{}`)},
	}
}

func adoptFinishServer(secret *corev1.Secret) (*Server, *fake.Clientset) {
	client := fake.NewSimpleClientset(secret)
	return &Server{
		argoSecretManager: argosecrets.NewManager(client, adoptFinishNS),
		auditLog:          audit.NewLog(10),
	}, client
}

func liveAdoptSecret(t *testing.T, client *fake.Clientset) *corev1.Secret {
	t.Helper()
	s, err := client.CoreV1().Secrets(adoptFinishNS).Get(context.Background(), "cluster-10.0.0.5-1", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func adoptPR(id int) prtracker.PRInfo {
	return prtracker.PRInfo{PRID: id, Cluster: "prod-eu", Operation: prtracker.OpAdoptCluster}
}

func TestFinishAdopt_MergedTakesOwnershipAndMarksAdopted(t *testing.T) {
	srv, client := adoptFinishServer(adoptWaitingSecret("12"))
	if got := srv.FinishAdoptOnPRComplete(context.Background(), adoptPR(12), "merged"); got != adoptFinishAdopted {
		t.Fatalf("outcome = %q, want %q", got, adoptFinishAdopted)
	}
	live := liveAdoptSecret(t, client)
	if live.Labels[argosecrets.LabelManagedBy] != argosecrets.ManagedByValue {
		t.Fatalf("ownership not taken: %v", live.Labels)
	}
	if live.Labels["env"] != "prod" {
		t.Fatalf("the old labels must be kept: %v", live.Labels)
	}
	if live.Annotations[orchestrator.AnnotationAdopted] != "true" {
		t.Fatalf("not marked adopted: %v", live.Annotations)
	}
	if _, ok := live.Annotations[argosecrets.AnnotationAdoptPendingPR]; ok {
		t.Fatalf("waiting mark still there: %v", live.Annotations)
	}
}

func TestFinishAdopt_ClosedTakesOnlyTheMarkOff(t *testing.T) {
	before := adoptWaitingSecret("12")
	srv, client := adoptFinishServer(before.DeepCopy())
	if got := srv.FinishAdoptOnPRComplete(context.Background(), adoptPR(12), "closed"); got != adoptFinishReleased {
		t.Fatalf("outcome = %q, want %q", got, adoptFinishReleased)
	}
	live := liveAdoptSecret(t, client)
	if len(live.Annotations) != 0 {
		t.Fatalf("annotations left: %v", live.Annotations)
	}
	if len(live.Labels) != len(before.Labels) || live.Labels["env"] != "prod" {
		t.Fatalf("labels changed on a closed pull request: %v", live.Labels)
	}
	if _, owned := live.Labels[argosecrets.LabelManagedBy]; owned {
		t.Fatal("a closed pull request must not hand ownership to Sharko")
	}
}

func TestFinishAdopt_IgnoresPullRequestsThatAreNotTheWaitingOne(t *testing.T) {
	cases := map[string]struct {
		mark string
		pr   prtracker.PRInfo
	}{
		"other pull request number": {"12", adoptPR(13)},
		"no mark (a take over pull request shares the operation code)": {"", adoptPR(12)},
		"other operation": {"12", prtracker.PRInfo{PRID: 12, Cluster: "prod-eu", Operation: prtracker.OpRegisterCluster}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, client := adoptFinishServer(adoptWaitingSecret(tc.mark))
			if got := srv.FinishAdoptOnPRComplete(context.Background(), tc.pr, "merged"); got != adoptFinishIgnored {
				t.Fatalf("outcome = %q, want ignored", got)
			}
			live := liveAdoptSecret(t, client)
			if _, owned := live.Labels[argosecrets.LabelManagedBy]; owned {
				t.Fatal("ownership changed for a pull request that is not the waiting one")
			}
		})
	}
}
