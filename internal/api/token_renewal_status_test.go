package api

import (
	"context"
	"encoding/base64"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// refusingEKSBackend fails every mint, the way an EKS backend does when its
// AWS login stopped working.
type refusingEKSBackend struct{}

func (refusingEKSBackend) GetCredentials(string) (*providers.Kubeconfig, error) {
	return nil, errors.New("the backend refused")
}
func (refusingEKSBackend) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (refusingEKSBackend) SearchSecrets(string) ([]string, error)         { return nil, nil }
func (refusingEKSBackend) HealthCheck(context.Context) error              { return nil }

// TestTokenRenewalFailure_IsNotShownAsConnected pins ep4 serious 1 on the
// read side: when Sharko could not renew an EKS token, the cluster must not
// read as "Connected" just because ArgoCD has not noticed yet.
func TestTokenRenewalFailure_IsNotShownAsConnected(t *testing.T) {
	const name = "eks-old"
	recon := startFailingRenewal(t, name, refusingEKSBackend{})

	c := models.Cluster{Name: name, ConnectionStatus: "Successful"}
	applyTokenRenewalTrouble(&c, recon)
	if c.ConnectionStatus == "Successful" {
		t.Fatal("a cluster whose token renewal failed still reads as Connected")
	}
	v := computeConnectivityVerdict(c.Name, c.ConnectionStatus, nil)
	if v.Status == "verified_argocd" {
		t.Fatal("a cluster whose token renewal failed still gets the verified-by-ArgoCD verdict")
	}
	applyLastReconcile(&c, recon)
	if c.LastReconcile == nil || c.LastReconcile.Outcome != string(clusterreconciler.OutcomeFailed) {
		t.Fatalf("last reconcile = %+v, want failed", c.LastReconcile)
	}
	if c.LastReconcile.Message != clusterreconciler.TokenRenewalFailedSentence {
		t.Fatalf("last reconcile message = %q, want the fixed renewal sentence", c.LastReconcile.Message)
	}

	// A cluster with no renewal trouble is left exactly as ArgoCD said.
	other := models.Cluster{Name: "fine", ConnectionStatus: "Successful"}
	applyTokenRenewalTrouble(&other, recon)
	if other.ConnectionStatus != "Successful" {
		t.Fatalf("a healthy cluster was changed to %q", other.ConnectionStatus)
	}
}

// startFailingRenewal runs one reconciler pass over an eks-token cluster whose
// live Secret holds an old token, with the given backend, and waits until the
// renewal is marked as failing.
func startFailingRenewal(t *testing.T, name string, backend providers.ClusterCredentialsProvider) *clusterreconciler.Reconciler {
	t.Helper()
	old := "k8s-aws-v1." + base64.RawURLEncoding.EncodeToString([]byte(
		"https://sts.us-east-1.amazonaws.com/?Action=GetCallerIdentity&X-Amz-Date=20200101T000000Z"))
	cfg, err := argosecrets.BuildSecretConfigJSON(argosecrets.ClusterSecretSpec{Name: name, Server: "https://eks-old.example.invalid", Token: old})
	if err != nil {
		t.Fatal(err)
	}
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "argocd", Labels: map[string]string{
			argosecrets.LabelManagedBy:  argosecrets.ManagedByValue,
			argosecrets.LabelSecretType: "cluster",
		}},
		Data: map[string][]byte{"name": []byte(name), "server": []byte("https://eks-old.example.invalid"), "config": []byte(cfg)},
	}
	gp := &comparisonGP{managedYAML: []byte(`apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters:
    - name: ` + name + `
      credsSource: eks-token
      region: us-east-1
`)}
	recon := clusterreconciler.New(clusterreconciler.Deps{
		GitProvider:  func() gitprovider.GitProvider { return gp },
		ArgoClient:   fake.NewSimpleClientset(live),
		Vault:        staticVault(backend),
		AuditFn:      func(audit.Entry) {},
		Namespace:    "argocd",
		TickInterval: time.Hour,
	})
	ctx, cancel := context.WithCancel(context.Background())
	recon.Start(ctx) // runs one pass right away
	t.Cleanup(func() { recon.Stop(); cancel() })

	deadline := time.Now().Add(10 * time.Second)
	for !recon.TokenRenewalFailing(name) {
		if time.Now().After(deadline) {
			t.Fatal("the reconciler never marked the failed renewal")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Let the pass finish writing its record.
	deadline = time.Now().Add(10 * time.Second)
	for {
		if _, ok := recon.LastReconcile(name); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pass never recorded a result for the cluster")
		}
		time.Sleep(20 * time.Millisecond)
	}

	return recon
}

// movedEKSBackend is an EKS backend whose stored details now point at another
// cluster address. It can say so read-only, so the renewal must not mint.
type movedEKSBackend struct{ mints *atomic.Int32 }

func (b movedEKSBackend) GetCredentials(string) (*providers.Kubeconfig, error) {
	b.mints.Add(1)
	return &providers.Kubeconfig{Server: "https://eks-moved.example.invalid", Token: "k8s-aws-v1.fake-minted"}, nil
}
func (movedEKSBackend) StoredConnectionFacts(string) (*providers.StoredConnectionFacts, error) {
	return &providers.StoredConnectionFacts{Server: "https://eks-moved.example.invalid", CredentialMintedPerFetch: true}, nil
}
func (movedEKSBackend) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (movedEKSBackend) SearchSecrets(string) ([]string, error)         { return nil, nil }
func (movedEKSBackend) HealthCheck(context.Context) error              { return nil }

// ep4 round R, R3 on the read side: a renewal stopped because the connection
// changed says THAT, not "could not get a new sign-in token", and no token
// was made for it.
func TestTokenRenewalConnectionChanged_SaysSoAndMintsNothing(t *testing.T) {
	const name = "eks-old"
	var mints atomic.Int32
	recon := startFailingRenewal(t, name, movedEKSBackend{mints: &mints})
	if n := mints.Load(); n != 0 {
		t.Fatalf("a changed connection made %d sign-in tokens, want 0", n)
	}
	c := models.Cluster{Name: name, ConnectionStatus: "Successful"}
	applyLastReconcile(&c, recon)
	if c.LastReconcile == nil || c.LastReconcile.Message != clusterreconciler.TokenRenewalConnectionChangedSentence {
		t.Fatalf("last reconcile = %+v, want the connection-changed sentence", c.LastReconcile)
	}
}
