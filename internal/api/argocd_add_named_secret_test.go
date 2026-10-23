package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/prtracker"
	"github.com/MoranWeissman/sharko/internal/service"
)

// EP3-DRYRUN-FIXES join, design call 3: `argocd cluster add` names the
// cluster Secret cluster-<host>-<hash>, and the cluster name ArgoCD shows is
// the `name` inside it. Story A's remove, un-adopt and Discard paths must
// find that Secret by the name inside it (story B's shared
// argosecrets.GetClusterSecret), not by its Kubernetes name.

const (
	addedSecretName  = "cluster-10.0.0.5-3621829410"
	addedClusterName = "legacy-1"
	addedServer      = "https://10.0.0.5:6443"
)

// argocdAddedSecret is a Sharko-owned connection whose Kubernetes name is
// not the cluster name, the way `argocd cluster add` makes it.
func argocdAddedSecret(annotations map[string]string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      addedSecretName,
			Namespace: "argocd",
			Labels: map[string]string{
				"argocd.argoproj.io/secret-type": "cluster",
				argosecrets.LabelManagedBy:       argosecrets.ManagedByValue,
			},
			Annotations: annotations,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"name":   []byte(addedClusterName),
			"server": []byte(addedServer),
			"config": []byte(`{"bearerToken":"x"}`),
		},
	}
}

// followUpTestServer is takeoverTestServer with a live ArgoCD stub, so the
// remove hook's delete step has an ArgoCD to call.
func followUpTestServer(t *testing.T, gp gitprovider.GitProvider, argoURL string, secrets ...*corev1.Secret) (*Server, *fake.Clientset) {
	t.Helper()
	f, err := os.CreateTemp("", "sharko-followup-test-*.yaml")
	if err != nil {
		t.Fatalf("create temp config file: %v", err)
	}
	f.Close()
	t.Cleanup(func() { os.Remove(f.Name()) })

	connSvc := service.NewConnectionService(config.NewFileStore(f.Name()))
	clusterSvc := service.NewClusterService("")
	srv := withLegacyOpenAuthForTests(NewServer(connSvc, clusterSvc, service.NewAddonService(""),
		service.NewDashboardService(connSvc, ""), service.NewObservabilityService(clusterSvc),
		service.NewUpgradeService(ai.NewClient(ai.Config{}), nil, ""), ai.NewClient(ai.Config{})))
	if err := connSvc.Create(models.CreateConnectionRequest{
		Name:   "followup-test",
		Git:    models.GitRepoConfig{Provider: models.GitProviderGitHub, Owner: "o", Repo: "r"},
		Argocd: models.ArgocdConfig{ServerURL: argoURL, Token: "t", Insecure: true},
	}); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	if err := connSvc.SetActive("followup-test"); err != nil {
		t.Fatalf("activate connection: %v", err)
	}
	connSvc.SetGitProviderOverride(gp)

	k8s := fake.NewSimpleClientset()
	for _, s := range secrets {
		if _, err := k8s.CoreV1().Secrets("argocd").Create(context.Background(), s, metav1.CreateOptions{}); err != nil {
			t.Fatalf("seed cluster secret %q: %v", s.Name, err)
		}
	}
	srv.SetArgoSecretManager(argosecrets.NewManager(k8s, "argocd"))
	return srv, k8s
}

func getAddedSecret(t *testing.T, k8s *fake.Clientset) *corev1.Secret {
	t.Helper()
	sec, err := k8s.CoreV1().Secrets("argocd").Get(context.Background(), addedSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("the connection %s is gone: %v", addedSecretName, err)
	}
	return sec
}

// Remove, keep the connection, merged later: Sharko's label comes off the
// cluster-<host>-<hash> Secret and the Secret stays.
func TestRemoveAfterMerge_KeepConnection_ArgocdAddNamedSecret(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{{"name": addedClusterName, "server": addedServer}}, http.StatusOK)
	srv, k8s := followUpTestServer(t, newTakeoverFakeGP(), argo.URL, argocdAddedSecret(nil))

	if err := srv.FinishClusterFollowUpOnMerge(context.Background(), prtracker.PRInfo{
		PRID: 12, Cluster: addedClusterName, Operation: prtracker.OpRemoveCluster,
		FollowUp: &prtracker.ClusterFollowUp{Cleanup: "git", KeepConnection: true},
	}); err != nil {
		t.Fatal(err)
	}

	sec := getAddedSecret(t, k8s)
	if _, still := sec.Labels[argosecrets.LabelManagedBy]; still {
		t.Fatalf("the kept connection still carries Sharko's label: %v", sec.Labels)
	}
	if got := atomic.LoadInt32(argo.deleteCalls); got != 0 {
		t.Fatalf("a kept connection was deleted from ArgoCD (%d DELETE calls)", got)
	}
}

// Remove, delete the connection, merged later: Sharko confirms its label
// on the cluster-<host>-<hash> Secret and deletes that Secret by its own
// name. It does not delete through ArgoCD by server address, because two
// connections can share an address (EP3 dry run round R, R8).
func TestRemoveAfterMerge_DeleteConnection_ArgocdAddNamedSecret(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{{"name": addedClusterName, "server": addedServer}}, http.StatusOK)
	srv, k8s := followUpTestServer(t, newTakeoverFakeGP(), argo.URL, argocdAddedSecret(nil))

	if err := srv.FinishClusterFollowUpOnMerge(context.Background(), prtracker.PRInfo{
		PRID: 13, Cluster: addedClusterName, Operation: prtracker.OpRemoveCluster,
		FollowUp: &prtracker.ClusterFollowUp{Cleanup: "all"},
	}); err != nil {
		t.Fatal(err)
	}

	if got := atomic.LoadInt32(argo.deleteCalls); got != 0 {
		t.Fatalf("deleted through ArgoCD by server address (%d DELETE calls)", got)
	}
	if _, err := k8s.CoreV1().Secrets("argocd").Get(context.Background(), addedSecretName, metav1.GetOptions{}); err == nil {
		t.Fatalf("the Secret %s should be deleted", addedSecretName)
	}
}

// Un-adopt, merged later: the adopted and take-over marks and Sharko's
// label come off the cluster-<host>-<hash> Secret, and the Secret stays.
func TestUnadoptAfterMerge_ArgocdAddNamedSecret(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{{"name": addedClusterName, "server": addedServer}}, http.StatusOK)
	srv, k8s := followUpTestServer(t, newTakeoverFakeGP(), argo.URL, argocdAddedSecret(map[string]string{
		argosecrets.AnnotationAdopted:                 "true",
		argosecrets.AnnotationTakenOverAt:             "2026-10-23T10:00:00Z",
		argosecrets.AnnotationTakeoverPreservedLabels: `{"team":"a"}`,
	}))

	if err := srv.FinishClusterFollowUpOnMerge(context.Background(), prtracker.PRInfo{
		PRID: 14, Cluster: addedClusterName, Operation: prtracker.OpUnadoptCluster,
		FollowUp: &prtracker.ClusterFollowUp{},
	}); err != nil {
		t.Fatal(err)
	}

	sec := getAddedSecret(t, k8s)
	if _, still := sec.Labels[argosecrets.LabelManagedBy]; still {
		t.Fatalf("un-adopt left Sharko's label: %v", sec.Labels)
	}
	for _, key := range []string{argosecrets.AnnotationAdopted, argosecrets.AnnotationTakenOverAt, argosecrets.AnnotationTakeoverPreservedLabels} {
		if _, still := sec.Annotations[key]; still {
			t.Fatalf("un-adopt left %s: %v", key, sec.Annotations)
		}
	}
	if got := atomic.LoadInt32(argo.deleteCalls); got != 0 {
		t.Fatalf("un-adopt deleted the cluster from ArgoCD (%d DELETE calls)", got)
	}
}

// Discard: a real leftover whose Secret is named cluster-<host>-<hash> is
// found by the cluster name ArgoCD shows, re-checked, and discarded.
func TestDiscardOrphan_ArgocdAddNamedSecret(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{{"name": addedClusterName, "server": addedServer}}, http.StatusOK)
	gp := &orphanFakeGP{managedYAML: []byte("clusters: []")}
	k8s := orphanK8sClient(argocdAddedSecret(map[string]string{
		models.AnnotationRegistrationPending: models.RegistrationPendingTimestamp(time.Now().Add(-time.Hour)),
	}))
	_, router := orphanTestServer(t, gp, argo.URL, k8s)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, orphanAdminReq(addedClusterName))
	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d (body=%s)", w.Code, w.Body.String())
	}
	// R8: the delete goes by the Secret's own name, never through ArgoCD
	// by server address.
	if got := atomic.LoadInt32(argo.deleteCalls); got != 0 {
		t.Fatalf("deleted through ArgoCD by server address (%d calls)", got)
	}
	if _, err := k8s.CoreV1().Secrets("argocd").Get(context.Background(), addedSecretName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("the Secret %s is still there: %v", addedSecretName, err)
	}
}

// Discard refuses a cluster-<host>-<hash> Secret that was adopted: the
// re-check reads the right Secret and says no (409).
func TestDiscardOrphan_ArgocdAddNamedSecret_AdoptedRefused(t *testing.T) {
	argo := newStubArgoSrv(t, []map[string]interface{}{{"name": addedClusterName, "server": addedServer}}, http.StatusOK)
	gp := &orphanFakeGP{managedYAML: []byte("clusters: []")}
	k8s := orphanK8sClient(argocdAddedSecret(map[string]string{
		models.AnnotationRegistrationPending: models.RegistrationPendingTimestamp(time.Now().Add(-time.Hour)),
		argosecrets.AnnotationAdopted:        "true",
	}))
	_, router := orphanTestServer(t, gp, argo.URL, k8s)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, orphanAdminReq(addedClusterName))
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d (body=%s)", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(argo.deleteCalls); got != 0 {
		t.Fatalf("an adopted connection was discarded (%d DELETE calls)", got)
	}
}
