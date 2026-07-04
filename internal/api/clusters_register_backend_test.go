package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

// V2-cleanup-53.1 — registration with a secret-backend creds source must
// reach the configured ClusterCredentialsProvider (the live bug: with
// provider.type=aws-sm the registration path silently used the ArgoCD
// provider, 502ing on "argocd cluster secret ... not found").
//
// These tests drive POST /api/v1/clusters through the real handler + real
// orchestrator with a FAKE (SM-style) provider injected on the Server,
// pinning:
//   1. creds_source=secret-kubeconfig + secret_path=X calls GetCredentials(X)
//      (secret_path wins over cluster name);
//   2. creds_source=secret-kubeconfig without secret_path looks up the
//      cluster name;
//   3. the inline-kubeconfig path NEVER touches the backend provider.
//
// The hot-reload contract (PUT /connections swaps the registration provider
// without a restart) is pinned in TestHotReload_* below via the REAL save
// path (PUT /api/v1/connections/{name} → ReinitializeFromConnection).

// recordingCredProvider is an SM-style ClusterCredentialsProvider fake that
// records every GetCredentials lookup.
type recordingCredProvider struct {
	calls []string
	kc    *providers.Kubeconfig
	err   error
}

func (p *recordingCredProvider) GetCredentials(name string) (*providers.Kubeconfig, error) {
	p.calls = append(p.calls, name)
	if p.err != nil {
		return nil, p.err
	}
	return p.kc, nil
}

func (p *recordingCredProvider) ListClusters() ([]providers.ClusterInfo, error) { return nil, nil }
func (p *recordingCredProvider) SearchSecrets(string) ([]string, error)         { return nil, nil }
func (p *recordingCredProvider) HealthCheck(context.Context) error              { return nil }

var _ providers.ClusterCredentialsProvider = (*recordingCredProvider)(nil)

// newRegisterBackendTestServer wires a Server with an ArgoCD httptest stub
// (empty cluster list), a fake GitProvider, and the supplied credProvider —
// enough for POST /api/v1/clusters dry_run to run the full pre-Git flow.
func newRegisterBackendTestServer(t *testing.T, cp providers.ClusterCredentialsProvider) *Server {
	t.Helper()
	srv := newIsolatedTestServer(t)
	argoStub := startArgocdStub(t, nil) // no clusters registered in ArgoCD
	seedActiveConnectionWithArgo(t, srv, argoStub.URL)
	srv.credProvider = cp
	return srv
}

func TestRegisterCluster_SecretKubeconfig_ReachesBackendProvider_SecretPathWins(t *testing.T) {
	fake := &recordingCredProvider{
		// Raw nil on purpose: skips the Stage1 remote verification (which
		// would need a live cluster) — the assertion is the provider lookup.
		kc: &providers.Kubeconfig{Server: "https://fetched.example.com"},
	}
	srv := newRegisterBackendTestServer(t, fake)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"name":         "smoke-target-1",
		"creds_source": "secret-kubeconfig",
		"secret_path":  "sharko-smoke-target-1-kubeconfig",
		"dry_run":      true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if len(fake.calls) != 1 || fake.calls[0] != "sharko-smoke-target-1-kubeconfig" {
		t.Fatalf("GetCredentials calls = %v, want exactly [sharko-smoke-target-1-kubeconfig] (secret_path must win over cluster name)", fake.calls)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	cluster, _ := resp["cluster"].(map[string]interface{})
	if cluster == nil || cluster["server"] != "https://fetched.example.com" {
		t.Errorf("response cluster.server = %v, want the provider-fetched server URL", resp["cluster"])
	}
}

func TestRegisterCluster_SecretKubeconfig_NoSecretPath_LooksUpClusterName(t *testing.T) {
	fake := &recordingCredProvider{kc: &providers.Kubeconfig{Server: "https://fetched.example.com"}}
	srv := newRegisterBackendTestServer(t, fake)
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"name":         "prod-eu",
		"creds_source": "secret-kubeconfig",
		"dry_run":      true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if len(fake.calls) != 1 || fake.calls[0] != "prod-eu" {
		t.Fatalf("GetCredentials calls = %v, want exactly [prod-eu]", fake.calls)
	}
}

func TestRegisterCluster_InlineKubeconfig_NeverTouchesBackendProvider(t *testing.T) {
	fake := &recordingCredProvider{kc: &providers.Kubeconfig{Server: "https://should-not-be-used"}}
	srv := newRegisterBackendTestServer(t, fake)
	router := NewRouter(srv, nil)

	// Valid kubeconfig YAML pointing at a dead local port: the inline path
	// parses it, then Stage1 verification fails fast (connection refused).
	// The assertion is upstream of that: the backend provider must never be
	// consulted, whatever the final status is.
	inlineKubeconfig := `apiVersion: v1
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
	body, _ := json.Marshal(map[string]interface{}{
		"name":         "kind-local",
		"creds_source": "inline-kubeconfig",
		"kubeconfig":   inlineKubeconfig,
		"dry_run":      true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if len(fake.calls) != 0 {
		t.Fatalf("GetCredentials calls = %v, want none — the inline-kubeconfig path must not consult the backend provider", fake.calls)
	}
	if w.Code == http.StatusServiceUnavailable {
		t.Fatalf("status = 503: the inline path must not be gated on the backend provider")
	}
}

// --- hot-reload through the real save path ----------------------------------

// Saving a connection whose provider is aws-sm via PUT /api/v1/connections/
// {name} must swap the registration/cluster-test credProvider to the
// SM-backed provider WITHOUT a restart. Deterministic in CI: AWS SDK config
// loading succeeds without real credentials (resolution is deferred to the
// first API call).
func TestHotReload_ConnectionSaveSwapsRegistrationProviderToAWSSM(t *testing.T) {
	// Keep any accidental AWS credential resolution fast and offline-safe.
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	srv := newIsolatedTestServer(t)
	argoStub := startArgocdStub(t, nil)
	seedActiveConnectionWithArgo(t, srv, argoStub.URL)

	// Boot state: whatever the environment gave us (typically nil
	// out-of-cluster). The point is the transition below.
	router := NewRouter(srv, nil)

	body, _ := json.Marshal(map[string]interface{}{
		"git": map[string]interface{}{
			"provider": "github",
			"owner":    "sharko-test",
			"repo":     "sharko-addons",
		},
		"argocd": map[string]interface{}{
			"server_url": argoStub.URL,
			"token":      "stub-token-not-validated-by-httptest",
			"insecure":   true,
		},
		"provider": map[string]interface{}{
			"type":   "aws-sm",
			"region": "eu-west-1",
			"prefix": "clusters/",
		},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/connections/patch-handler-test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("PUT /connections status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}

	// The save path must have hot-reloaded the registration provider.
	if _, ok := srv.credProvider.(*providers.AWSSecretsManagerProvider); !ok {
		t.Fatalf("credProvider after connection save = %T, want *providers.AWSSecretsManagerProvider (hot-reload must not require a pod restart)", srv.credProvider)
	}
	if srv.clusterTestCfg == nil || srv.clusterTestCfg.Type != "aws-sm" {
		t.Fatalf("clusterTestCfg after connection save = %+v, want Type=aws-sm", srv.clusterTestCfg)
	}
	if srv.clusterTestCfg.ArgoCDNamespace != "" {
		t.Errorf("ArgoCDNamespace = %q, want empty (V125-1-10.8 cross-contamination guard)", srv.clusterTestCfg.ArgoCDNamespace)
	}
	if srv.clusterTestCfg.Region != "eu-west-1" || srv.clusterTestCfg.Prefix != "clusters/" {
		t.Errorf("clusterTestCfg carried (region=%q, prefix=%q), want (eu-west-1, clusters/)", srv.clusterTestCfg.Region, srv.clusterTestCfg.Prefix)
	}
}

// After the hot-reload above, a subsequent registration must use the NEW
// provider. We pin the full loop: save aws-sm connection → swap in a
// recording fake shaped like the reloaded provider is NOT needed — instead we
// assert the reloaded provider is what the register handler will read
// (per-request orchestrator reads s.credProvider at request time, so the
// swap IS the contract). This test drives one step further: change the
// connection provider TYPE and confirm the registration path consults the
// provider that matches the latest save.
func TestHotReload_SubsequentRegistrationUsesNewProvider(t *testing.T) {
	// The post-reload registration hits the real (credential-less) AWS SM
	// provider; disabling IMDS keeps that failure fast and offline-safe.
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	fakeOld := &recordingCredProvider{kc: &providers.Kubeconfig{Server: "https://old.example.com"}}
	srv := newRegisterBackendTestServer(t, fakeOld)
	router := NewRouter(srv, nil)

	// Sanity: registration consults the current (old) provider.
	body, _ := json.Marshal(map[string]interface{}{
		"name":         "prod-eu",
		"creds_source": "secret-kubeconfig",
		"dry_run":      true,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if len(fakeOld.calls) != 1 {
		t.Fatalf("pre-reload registration should consult the current provider, calls=%v", fakeOld.calls)
	}

	// Save the connection with provider type aws-sm (the real save path).
	saveBody, _ := json.Marshal(map[string]interface{}{
		"git": map[string]interface{}{
			"provider": "github",
			"owner":    "sharko-test",
			"repo":     "sharko-addons",
		},
		"argocd": map[string]interface{}{
			"server_url": activeArgoURL(t, srv),
			"token":      "stub-token-not-validated-by-httptest",
			"insecure":   true,
		},
		"provider": map[string]interface{}{"type": "aws-sm", "region": "eu-west-1"},
	})
	saveReq := httptest.NewRequest(http.MethodPut, "/api/v1/connections/patch-handler-test", bytes.NewReader(saveBody))
	saveReq.Header.Set("Content-Type", "application/json")
	saveW := httptest.NewRecorder()
	router.ServeHTTP(saveW, saveReq)
	if saveW.Code != http.StatusOK {
		t.Fatalf("PUT /connections status = %d (body=%s)", saveW.Code, saveW.Body.String())
	}

	// A subsequent registration must NOT consult the old provider anymore.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if len(fakeOld.calls) != 1 {
		t.Fatalf("old provider was consulted after the connection save (calls=%v) — hot-reload failed", fakeOld.calls)
	}
	if _, ok := srv.credProvider.(*providers.AWSSecretsManagerProvider); !ok {
		t.Fatalf("credProvider = %T after save, want *providers.AWSSecretsManagerProvider", srv.credProvider)
	}
}

// activeArgoURL returns the active connection's ArgoCD server URL so the
// hot-reload PUT can round-trip the same stub URL.
func activeArgoURL(t *testing.T, s *Server) string {
	t.Helper()
	conn, err := s.connSvc.GetActiveConnection()
	if err != nil || conn == nil {
		t.Fatalf("no active connection: %v", err)
	}
	return conn.Argocd.ServerURL
}
