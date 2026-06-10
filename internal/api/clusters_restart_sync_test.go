package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/service"
)

// argocdRestartSyncServer builds a minimal ArgoCD httptest server that handles
// GetApplication, TerminateOperation, and SyncApplication.
//
// terminated and synced are set to true when the respective calls arrive.
func argocdRestartSyncServer(t *testing.T, appName string, phase string) (ts *httptest.Server, terminated *bool, synced *bool) {
	t.Helper()
	term := false
	syn := false
	terminated = &term
	synced = &syn

	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == http.MethodGet && path == "/api/v1/applications/"+appName:
			// GET application — return the app with the specified phase.
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `{
				"metadata":{"name":%q,"namespace":"argocd"},
				"spec":{"project":"default","source":{"repoURL":"https://github.com/example/repo"}},
				"status":{
					"sync":{"status":"OutOfSync"},
					"health":{"status":"Healthy"},
					"operationState":{"phase":%q,"startedAt":"2026-06-10T11:50:00Z"}
				}
			}`, appName, phase)
		case r.Method == http.MethodDelete && path == "/api/v1/applications/"+appName+"/operation":
			term = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodPost && path == "/api/v1/applications/"+appName+"/sync":
			syn = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Logf("unexpected request: %s %s", r.Method, path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return ts, terminated, synced
}

// newTestServerWithArgocd creates a Server wired to an ArgoCD httptest server.
func newTestServerWithArgocd(t *testing.T, argoURL, argoToken string) *Server {
	t.Helper()
	// Write a temp connection config file so ConnectionService resolves.
	f, err := os.CreateTemp(t.TempDir(), "sharko-test-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	connYAML := fmt.Sprintf(`connections:
  - name: test
    argocd:
      server_url: %q
      token: %q
    git:
      type: github
      token: test
      org: test
      repo: test
active_connection: test
`, argoURL, argoToken)
	if _, err := f.WriteString(connYAML); err != nil {
		t.Fatal(err)
	}
	f.Close()

	store := config.NewFileStore(f.Name())
	connSvc := service.NewConnectionService(store)

	from := newTestServer()
	from.connSvc = connSvc
	return from
}

func TestHandleRestartAddonSync_200_WithOperation(t *testing.T) {
	appName := "keda-moran-test"
	ts, terminated, synced := argocdRestartSyncServer(t, appName, "Running")
	defer ts.Close()

	srv := newTestServerWithArgocd(t, ts.URL, "test-token")
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/moran-test/addons/keda/restart-sync", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var result RestartSyncResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result.Terminated {
		t.Errorf("expected terminated=true, got false")
	}
	if !result.Synced {
		t.Errorf("expected synced=true, got false")
	}
	if !*terminated {
		t.Errorf("expected ArgoCD TerminateOperation to have been called")
	}
	if !*synced {
		t.Errorf("expected ArgoCD SyncApplication to have been called")
	}
}

func TestHandleRestartAddonSync_200_NoOperation(t *testing.T) {
	// No operation in flight — only sync should fire, not terminate.
	appName := "keda-moran-test"
	ts, terminated, synced := argocdRestartSyncServer(t, appName, "") // empty phase
	defer ts.Close()

	srv := newTestServerWithArgocd(t, ts.URL, "test-token")
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/moran-test/addons/keda/restart-sync", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}
	var result RestartSyncResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Terminated {
		t.Errorf("expected terminated=false (no operation), got true")
	}
	if !result.Synced {
		t.Errorf("expected synced=true")
	}
	if *terminated {
		t.Errorf("expected no TerminateOperation call when no operation in flight")
	}
	if !*synced {
		t.Errorf("expected SyncApplication to have been called")
	}
}

func TestHandleRestartAddonSync_404_AppNotFound(t *testing.T) {
	// ArgoCD returns 404 for the application.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"application not found"}`))
	}))
	defer ts.Close()

	srv := newTestServerWithArgocd(t, ts.URL, "test-token")
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/moran-test/addons/keda/restart-sync", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestHandleRestartAddonSync_403_ViewerRole(t *testing.T) {
	// Viewer role should be denied.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	srv := newTestServerWithArgocd(t, ts.URL, "test-token")
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/moran-test/addons/keda/restart-sync", nil)
	// Set authenticated viewer — auth is enforced when X-Sharko-User is present.
	req.Header.Set("X-Sharko-User", "bob")
	req.Header.Set("X-Sharko-Role", "viewer")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for viewer role, got %d; body: %s", w.Code, w.Body.String())
	}
}

func TestHandleRestartAddonSync_NoArgoConnection_502(t *testing.T) {
	// No connection configured → GetActiveArgocdClient fails → 502.
	srv := newTestServer() // no ArgoCD connection
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/moran-test/addons/keda/restart-sync", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502 when no ArgoCD connection, got %d; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "no active ArgoCD connection") {
		t.Errorf("expected error message about no connection; got: %s", w.Body.String())
	}
}
