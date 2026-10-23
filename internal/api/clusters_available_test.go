package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

func availableClusterSecret(k8sName, clusterName string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: k8sName, Namespace: "argocd",
			Labels: map[string]string{argosecrets.LabelSecretType: "cluster"},
		},
		Data: map[string][]byte{"name": []byte(clusterName), "server": []byte("https://10.0.0.5:6443")},
	}
}

// GET /clusters/available lists the clusters ArgoCD already connects to that
// Sharko does not manage, read from ArgoCD (episode 3 dry run, finding 29).
// It used to read the credentials backend and came back empty.
func TestDiscoverClusters_ListsWhatArgoCDKnowsAndSharkoDoesNotManage(t *testing.T) {
	srv := newDoctorTestServer(t)
	// prod-eu is in the fleet record in Git, so it is managed.
	withDoctorGitFiles(srv)
	srv.connSvc.SetArgocdClientOverride(&discoverFakeArgocd{clusters: []models.ArgocdCluster{
		{Name: "in-cluster", Server: "https://kubernetes.default.svc", ConnectionState: "Successful"},
		{Name: "prod-eu", Server: "https://prod-eu.example.invalid", ConnectionState: "Successful"},
		{Name: "legacy-1", Server: "https://10.0.0.5:6443", ConnectionState: "Successful"},
		{Name: "legacy-2", Server: "https://10.0.0.6:6443", ConnectionState: "Failed"},
		{Name: "legacy-3", Server: "https://10.0.0.7:6443"},
	}})
	srv.argoSecretManager = argosecrets.NewManager(fake.NewSimpleClientset(
		availableClusterSecret("cluster-10.0.0.5-3621829410", "legacy-1"),
		availableClusterSecret("legacy-2", "legacy-2"),
	), "argocd")

	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/clusters/available", nil), "operator")
	w := httptest.NewRecorder()
	srv.handleDiscoverClusters(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body.String())
	}
	const want = `{"clusters":[` +
		`{"name":"legacy-1","server_url":"https://10.0.0.5:6443","secret_name":"cluster-10.0.0.5-3621829410","reachable":true},` +
		`{"name":"legacy-2","server_url":"https://10.0.0.6:6443","secret_name":"legacy-2","reachable":false},` +
		`{"name":"legacy-3","server_url":"https://10.0.0.7:6443"}]}`
	var got, wantV any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte(want), &wantV)
	gotB, _ := json.Marshal(got)
	wantB, _ := json.Marshal(wantV)
	if string(gotB) != string(wantB) {
		t.Fatalf("body:\n got: %s\nwant: %s", gotB, wantB)
	}
}

// With no clusters left, the list is [] and never null.
func TestDiscoverClusters_EmptyIsAnEmptyList(t *testing.T) {
	srv := newDoctorTestServer(t)
	withDoctorGitFiles(srv)
	srv.connSvc.SetArgocdClientOverride(&discoverFakeArgocd{clusters: []models.ArgocdCluster{
		{Name: "prod-eu", Server: "https://prod-eu.example.invalid"},
	}})

	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/clusters/available", nil), "operator")
	w := httptest.NewRecorder()
	srv.handleDiscoverClusters(w, req)

	if w.Code != http.StatusOK || w.Body.String() != "{\"clusters\":[]}\n" {
		t.Fatalf("got %d %q, want 200 {\"clusters\":[]}", w.Code, w.Body.String())
	}
}

// R7: a cluster that is being registered is not available. It shows in the
// pending list, and adopt or take over would open a second pull request.
func TestDiscoverClusters_LeavesOutClustersBeingRegistered(t *testing.T) {
	srv := newDoctorTestServer(t)
	prefix := strings.TrimSpace(srv.gitopsConfig().CommitPrefix)
	srv.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{
		files: map[string][]byte{
			srv.repoPaths.Catalog:         []byte(doctorCatalogYAML),
			srv.repoPaths.ManagedClusters: []byte(doctorManagedClustersYAML),
		},
		prs: []gitprovider.PullRequest{{ID: 12, Title: prefix + " register cluster dev", URL: "https://example.invalid/pull/12"}},
	})
	srv.connSvc.SetArgocdClientOverride(&discoverFakeArgocd{clusters: []models.ArgocdCluster{
		{Name: "dev", Server: "https://10.0.0.8:6443"},
		{Name: "staging", Server: "https://10.0.0.9:6443"},
		{Name: "legacy-1", Server: "https://10.0.0.5:6443"},
	}})
	marked := availableClusterSecret("staging", "staging")
	marked.Annotations = map[string]string{models.AnnotationRegistrationPending: "2026-10-23T10:00:00Z"}
	srv.argoSecretManager = argosecrets.NewManager(fake.NewSimpleClientset(marked), "argocd")

	req := withRole(httptest.NewRequest(http.MethodGet, "/api/v1/clusters/available", nil), "operator")
	w := httptest.NewRecorder()
	srv.handleDiscoverClusters(w, req)

	const want = "{\"clusters\":[{\"name\":\"legacy-1\",\"server_url\":\"https://10.0.0.5:6443\"}]}\n"
	if w.Code != http.StatusOK || w.Body.String() != want {
		t.Fatalf("got %d %s, want only legacy-1", w.Code, w.Body.String())
	}
}
