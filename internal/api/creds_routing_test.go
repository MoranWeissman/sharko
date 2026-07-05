package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// V2-cleanup-60.4 — per-cluster credential routing (review H4).
//
// Since the aws-sm / k8s-secrets cluster-credentials arms were restored,
// configuring a backend connection routed ALL cluster credential fetches to
// that backend. An inline-kubeconfig-registered cluster has no backend
// secret (its credentials live only in the ArgoCD cluster Secret), so
// Test / Diagnose answered "secret not found" for it. These tests pin the
// fix end-to-end at the handlers: the cluster's stored creds_source routes
// the fetch.

// routingManagedClusters: one inline-registered cluster (credsSource
// stamped), one backend-registered cluster, one legacy record (no
// credsSource — written before the field existed).
const routingManagedClusters = `clusters:
  - name: kind-inline
    credsSource: inline-kubeconfig
    labels: {}
  - name: prod-backend
    credsSource: eks-token
    labels: {}
  - name: legacy-inline
    labels: {}
`

// routingTestKubeconfig is a syntactically valid kubeconfig pointing at a
// dead local port: remoteclient builds a client from it, and the first live
// call fails fast (connection refused) — far past the credential fetch
// stage these tests assert on.
const routingTestKubeconfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:1
    insecure-skip-tls-verify: true
  name: inline
contexts:
- context:
    cluster: inline
    user: inline
  name: inline
current-context: inline
users:
- name: inline
  user:
    token: inline-token
`

// installCredProviderWithReader publishes backend + a WORKING fake ArgoCD
// reader through the production providerSet shape (contrast with
// installCredProvider, which disables the reader for hermeticity).
func installCredProviderWithReader(srv *Server, backend, reader providers.ClusterCredentialsProvider) {
	srv.providerState.Store(&providerSet{
		credProvider: backend,
		credsRouter: &providers.ClusterCredsRouter{
			Backend: backend,
			ArgoCDReaderFn: func() (providers.ClusterCredentialsProvider, error) {
				return reader, nil
			},
		},
	})
}

func newCredsRoutingServer(t *testing.T) (*Server, *recordingCredProvider, *recordingCredProvider) {
	t.Helper()
	srv := newIsolatedTestServer(t)
	srv.connSvc.SetGitProviderOverride(&handlerFakeGitProvider{files: map[string][]byte{
		"configuration/managed-clusters.yaml": []byte(routingManagedClusters),
	}})
	// The backend behaves like an aws-sm connection that has NO secret for
	// inline-registered clusters ("secret not found") — the exact live-bug
	// shape.
	backend := &recordingCredProvider{err: errors.New("secret kind-inline not found")}
	reader := &recordingCredProvider{kc: &providers.Kubeconfig{
		Raw:    []byte(routingTestKubeconfig),
		Server: "https://127.0.0.1:1",
		Token:  "inline-token",
	}}
	installCredProviderWithReader(srv, backend, reader)
	return srv, backend, reader
}

// H4 acceptance (Test): backend connection configured + cluster registered
// inline → POST /clusters/{name}/test gets PAST the credential fetch via
// the ArgoCD read path; the backend is never consulted.
func TestClusterTest_InlineCluster_UnderBackendConnection_UsesArgoCDReadPath(t *testing.T) {
	srv, backend, reader := newCredsRoutingServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/kind-inline/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp struct {
		Result struct {
			Stage string `json:"stage"`
			Steps []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"steps"`
		} `json:"result"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The pre-fix failure was stage "credentials" ("secret not found"). Now
	// the fetch succeeds via the ArgoCD reader and the test proceeds to the
	// live-connection stages (which fail fast on the dead port — fine).
	if resp.Result.Stage == "credentials" {
		t.Fatalf("test still fails at the credentials stage: %+v", resp.Result)
	}
	if len(resp.Result.Steps) > 0 && resp.Result.Steps[0].Status != "pass" {
		t.Errorf("fetch-credentials step = %+v, want pass", resp.Result.Steps[0])
	}
	if len(backend.calls) != 0 {
		t.Errorf("backend consulted for the inline cluster: %v", backend.calls)
	}
	if len(reader.calls) != 1 || reader.calls[0] != "kind-inline" {
		t.Errorf("ArgoCD reader calls = %v, want [kind-inline]", reader.calls)
	}
}

// H4 acceptance (Diagnose): same routing at POST /clusters/{name}/diagnose.
func TestDiagnose_InlineCluster_UnderBackendConnection_UsesArgoCDReadPath(t *testing.T) {
	srv, backend, reader := newCredsRoutingServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/kind-inline/diagnose", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Pre-fix: 502 "failed to fetch credentials: secret not found". Now the
	// fetch succeeds via the ArgoCD reader; the diagnostic report itself is
	// computed against the dead port (permission checks fail — that's the
	// report's content, not an HTTP error).
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if len(backend.calls) != 0 {
		t.Errorf("backend consulted for the inline cluster: %v", backend.calls)
	}
	if len(reader.calls) != 1 || reader.calls[0] != "kind-inline" {
		t.Errorf("ArgoCD reader calls = %v, want [kind-inline]", reader.calls)
	}
}

// Backend-registered cluster: UNCHANGED behavior — the backend is consulted
// with the lookup key, the ArgoCD reader is not touched, and the backend's
// failure surfaces exactly as before (no masking fallback).
func TestClusterTest_BackendCluster_UnchangedRoute(t *testing.T) {
	srv, backend, reader := newCredsRoutingServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/prod-backend/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (failed-fetch verify.Result envelope)", w.Code)
	}
	if len(backend.calls) != 1 || backend.calls[0] != "prod-backend" {
		t.Errorf("backend calls = %v, want [prod-backend]", backend.calls)
	}
	if len(reader.calls) != 0 {
		t.Errorf("ArgoCD reader consulted for a backend cluster: %v", reader.calls)
	}
}

// Legacy record (registered before credsSource existed) with nothing in the
// backend: the fetch heals via the ArgoCD reader — the maintainer's live
// inline cluster keeps working without a migration.
func TestClusterTest_LegacyInlineCluster_HealsViaArgoCDReader(t *testing.T) {
	srv, backend, reader := newCredsRoutingServer(t)
	router := NewRouter(srv, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/legacy-inline/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Result struct {
			Stage string `json:"stage"`
		} `json:"result"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Result.Stage == "credentials" {
		t.Fatalf("legacy inline cluster still fails at the credentials stage")
	}
	// Backend first (legacy behavior), then the healing reader.
	if len(backend.calls) != 1 || backend.calls[0] != "legacy-inline" {
		t.Errorf("backend calls = %v, want [legacy-inline]", backend.calls)
	}
	if len(reader.calls) != 1 || reader.calls[0] != "legacy-inline" {
		t.Errorf("ArgoCD reader calls = %v, want [legacy-inline]", reader.calls)
	}
}

// M1 — the provider hot-reload publish is race-safe: hammer the publish
// seam (the same one ReinitializeFromConnection uses) while handlers read
// the provider concurrently. Run with -race: the pre-fix plain field
// assignments flag here; the atomic.Pointer snapshot must not.
func TestProviderHotReload_ConcurrentPublishAndReads_RaceSafe(t *testing.T) {
	srv := newIsolatedTestServer(t)
	router := NewRouter(srv, nil)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			cfgT := &providers.ClusterTestProviderConfig{Type: "aws-sm", Region: fmt.Sprintf("region-%d", i)}
			cfgA := &providers.AddonSecretProviderConfig{Type: "aws-sm", Region: cfgT.Region}
			srv.publishProviders(&recordingCredProvider{kc: &providers.Kubeconfig{}}, cfgA, cfgT)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			// /health reads credProvider(); /providers reads both configs.
			for _, path := range []string{"/api/v1/health", "/api/v1/providers"} {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
			}
		}
	}()
	wg.Wait()
}
