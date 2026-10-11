package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// GET /clusters/{name}/secrets lists Secrets only in the namespaces Sharko
// could have written to, never across all namespaces.

// fakeSpokeSecretsAPI is a tiny Kubernetes API that answers secret LISTs and
// records the path of each one. /api/v1/secrets (no namespace) is a list
// across all namespaces.
type fakeSpokeSecretsAPI struct {
	mu     sync.Mutex
	paths  []string
	forbid bool
}

func (f *fakeSpokeSecretsAPI) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]string(nil), f.paths...)
	sort.Strings(out)
	return out
}

func startFakeSpokeSecretsAPI(t *testing.T, api *fakeSpokeSecretsAPI) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/secrets") {
			http.NotFound(w, r)
			return
		}
		api.mu.Lock()
		api.paths = append(api.paths, r.URL.Path)
		forbid := api.forbid
		api.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if forbid {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(metav1.Status{
				TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
				Status:   metav1.StatusFailure, Reason: metav1.StatusReasonForbidden, Code: http.StatusForbidden,
				Message: "secrets is forbidden: User \"system:serviceaccount:x:y\" cannot list resource",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(corev1.SecretList{TypeMeta: metav1.TypeMeta{Kind: "SecretList", APIVersion: "v1"}})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func spokeKubeconfig(server string) []byte {
	return []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: ` + server + `
  name: spoke
contexts:
- context:
    cluster: spoke
    user: spoke
  name: spoke
current-context: spoke
users:
- name: spoke
  user:
    token: spoke-token
`)
}

func newClusterSecretsTestServer(t *testing.T, api *fakeSpokeSecretsAPI) *Server {
	t.Helper()
	srv := newIsolatedTestServer(t)
	srv.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{files: map[string][]byte{
		"configuration/managed-clusters.yaml": []byte(routingManagedClusters),
	}})
	reader := &recordingCredProvider{kc: &providers.Kubeconfig{Raw: spokeKubeconfig(startFakeSpokeSecretsAPI(t, api))}}
	installCredProviderWithReader(srv, reader, reader)
	return srv
}

func getClusterSecrets(t *testing.T, srv *Server) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/kind-inline/secrets", nil)
	w := httptest.NewRecorder()
	NewRouter(srv, nil).ServeHTTP(w, req)
	return w
}

func TestListClusterSecrets_ListsOnlyKnownNamespaces(t *testing.T) {
	api := &fakeSpokeSecretsAPI{}
	srv := newClusterSecretsTestServer(t, api)
	srv.SetSecretReconciler(&fakeReconciler{knownNamespaces: map[string][]string{
		"kind-inline": {"data", "monitoring"},
	}})

	w := getClusterSecrets(t, srv)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	want := []string{"/api/v1/namespaces/data/secrets", "/api/v1/namespaces/monitoring/secrets"}
	if got := api.seen(); !reflect.DeepEqual(got, want) {
		t.Fatalf("secret lists = %q, want exactly %q — never /api/v1/secrets (all namespaces)", got, want)
	}
}

func TestListClusterSecrets_NoKnownNamespace_NeverListsAllNamespaces(t *testing.T) {
	api := &fakeSpokeSecretsAPI{}
	srv := newClusterSecretsTestServer(t, api) // no reconciler, no addon secret definitions

	w := getClusterSecrets(t, srv)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if got := api.seen(); len(got) != 0 {
		t.Fatalf("secret lists = %q, want none — with no known namespace there is nothing to list", got)
	}
	var body struct {
		Secrets []json.RawMessage `json:"secrets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Secrets == nil || len(body.Secrets) != 0 {
		t.Fatalf("secrets = %s, want an empty list (not null)", w.Body.String())
	}
}

func TestListClusterSecrets_EveryNamespaceFails_502WithSafeSentence(t *testing.T) {
	api := &fakeSpokeSecretsAPI{forbid: true}
	srv := newClusterSecretsTestServer(t, api)
	srv.SetSecretReconciler(&fakeReconciler{knownNamespaces: map[string][]string{
		"kind-inline": {"data"},
	}})

	w := getClusterSecrets(t, srv)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 when no namespace could be read (body=%s)", w.Code, w.Body.String())
	}
	want := `Sharko is not allowed to list secrets on cluster "kind-inline". Grant its service account permission to list secrets there and try again.`
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error != want {
		t.Fatalf("error = %q, want exactly %q", body.Error, want)
	}
	if strings.Contains(w.Body.String(), "system:serviceaccount") {
		t.Fatalf("body leaks the Kubernetes error text: %s", w.Body.String())
	}
}
