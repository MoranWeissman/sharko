package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/providers"
)

// lazyCredsFakeGit wraps handlerFakeGitProvider with a working
// CreatePullRequest. handlerFakeGitProvider itself stubs CreatePullRequest
// to (nil, nil), which is fine for the read-path / early-rejection tests it
// was built for, but panics downstream (nil PR dereference in
// commitChangesWithMeta) for any test that drives a full write all the way
// to a Git commit — exactly what the "secret-less addon succeeds" and
// "cred-having cluster succeeds" cases below need to do.
type lazyCredsFakeGit struct {
	*handlerFakeGitProvider
	nextPRID int
}

func (f *lazyCredsFakeGit) CreatePullRequest(_ context.Context, title, _, branch, _ string) (*gitprovider.PullRequest, error) {
	f.nextPRID++
	pr := gitprovider.PullRequest{
		ID:           f.nextPRID,
		Title:        title,
		SourceBranch: branch,
		Status:       "open",
		URL:          fmt.Sprintf("https://example.com/pull/%d", f.nextPRID),
	}
	f.prs = append(f.prs, pr)
	return &pr, nil
}

// V2-cleanup-88.3 — "lazy credentials": API-layer coverage for the
// EnableAddon 4xx gate and the addon_secrets_ready read-model field.

// lazyCredsCatalogYAML mirrors internal/orchestrator/lazy_credentials_test.go's
// fixture: "datadog" declares two Secrets, "cert-manager" declares none.
const lazyCredsCatalogYAML = `applicationsets:
  - name: datadog
    chart: datadog
    repoURL: https://example.com
    version: 1.0.0
    secrets:
      - secretName: datadog-keys
        namespace: datadog
        keys:
          api-key: secrets/datadog/api-key
      - secretName: datadog-app-keys
        namespace: datadog
        keys:
          app-key: secrets/datadog/app-key
  - name: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: 1.0.0
`

// newLazyCredsTestServer wires an isolated Server with an ArgoCD stub (no
// pre-existing clusters) and a fake GitProvider pre-seeded with the catalog
// above, a managed-clusters.yaml entry for "prod-eu", and its values file —
// enough for POST /clusters/{name}/addons/{addon} to run the full flow.
// credProvider is left unconfigured (nil) unless the caller installs one.
func newLazyCredsTestServer(t *testing.T) (*Server, *lazyCredsFakeGit) {
	t.Helper()
	srv := newIsolatedTestServer(t)
	argoStub := startArgocdStub(t, nil)
	seedActiveConnectionWithArgo(t, srv, argoStub.URL)

	fakeGit := &lazyCredsFakeGit{handlerFakeGitProvider: &handlerFakeGitProvider{files: map[string][]byte{
		"configuration/addons-catalog.yaml":   []byte(lazyCredsCatalogYAML),
		"configuration/managed-clusters.yaml": []byte("clusters:\n  - name: prod-eu\n    labels: {}\n"),
		// s.repoPaths is zero-valued in this isolated test server (no
		// SetWriteAPIDeps call), so ClusterValues resolves to "" and the
		// values-file path is just the bare filename.
		"prod-eu.yaml": []byte("# Cluster values for prod-eu\nclusterGlobalValues:\n"),
	}}}
	srv.connSvc.SetGitProviderOverride(fakeGit)
	return srv, fakeGit
}

func postEnableAddon(t *testing.T, router http.Handler, cluster, addon string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{"yes": true})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+cluster+"/addons/"+addon, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// TestHandleEnableAddon_SecretBearingAddon_NoCredentials_Returns422 pins the
// HTTP contract for the orchestrator gate: a 422 (not 500/502) whose body
// names exactly what's missing.
func TestHandleEnableAddon_SecretBearingAddon_NoCredentials_Returns422(t *testing.T) {
	srv, _ := newLazyCredsTestServer(t)
	router := NewRouter(srv, nil)

	w := postEnableAddon(t, router, "prod-eu", "datadog")

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body=%s)", w.Code, w.Body.String())
	}
	msg := decodeError(t, w)
	want := `addon "datadog" needs 2 secrets pushed to the cluster, but Sharko has no credentials for cluster "prod-eu" — add connection credentials (secret path or EKS role) to the cluster, or choose an addon without secrets`
	if msg != want {
		t.Errorf("error message mismatch:\n got:  %s\n want: %s", msg, want)
	}
}

// TestHandleEnableAddon_SecretLessAddon_NoCredentials_Succeeds pins the
// zero-friction side: an addon with no secrets enables on a cred-less
// cluster with no rejection at all.
func TestHandleEnableAddon_SecretLessAddon_NoCredentials_Succeeds(t *testing.T) {
	srv, _ := newLazyCredsTestServer(t)
	router := NewRouter(srv, nil)

	w := postEnableAddon(t, router, "prod-eu", "cert-manager")

	if w.Code != http.StatusOK && w.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 200 or 207 (body=%s)", w.Code, w.Body.String())
	}
}

// TestHandleEnableAddon_SecretBearingAddon_UnknownAddon_StillReturns422ForCatalog
// makes sure the referential-integrity 422 (V2-cleanup-22) and the new
// credentials 422 (V2-cleanup-88.3) don't get confused — an addon that
// isn't in the catalog at all is still reported as a catalog problem, not a
// credentials problem, even on a cred-less cluster.
func TestHandleEnableAddon_UnknownAddon_StillReturns422ForCatalog(t *testing.T) {
	srv, _ := newLazyCredsTestServer(t)
	router := NewRouter(srv, nil)

	w := postEnableAddon(t, router, "prod-eu", "does-not-exist")

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body=%s)", w.Code, w.Body.String())
	}
	msg := decodeError(t, w)
	if !strings.Contains(msg, "not in the catalog") {
		t.Errorf("error %q should mention the catalog rejection, not a credentials rejection", msg)
	}
}

// TestHandleEnableAddon_SecretBearingAddon_WithCredentials_Succeeds proves
// the WITH-creds path through the real handler is untouched: installing a
// credentials provider that resolves for the cluster lets the same
// secret-bearing addon enable successfully.
func TestHandleEnableAddon_SecretBearingAddon_WithCredentials_Succeeds(t *testing.T) {
	srv, fakeGit := newLazyCredsTestServer(t)
	fakeGit.files["configuration/managed-clusters.yaml"] = []byte(
		"clusters:\n  - name: prod-eu\n    credsSource: secret-kubeconfig\n    labels: {}\n")
	installCredProvider(srv, &recordingCredProvider{
		kc: &providers.Kubeconfig{Server: "https://eu.example.com:6443", Raw: []byte("fake-kubeconfig")},
	}, nil, nil)
	router := NewRouter(srv, nil)

	w := postEnableAddon(t, router, "prod-eu", "datadog")

	if w.Code != http.StatusOK && w.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, want 200 or 207 (body=%s)", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// addon_secrets_ready read-model field (Goal 3)
// ---------------------------------------------------------------------------

// TestHandleListClusters_AddonSecretsReady_TruthTable drives GET /clusters
// against a set of managed-clusters.yaml records covering the readiness
// predicate's cases, with no credentials provider configured server-wide.
func TestHandleListClusters_AddonSecretsReady_TruthTable(t *testing.T) {
	srv, _ := newLazyCredsTestServer(t)
	fakeGit := &handlerFakeGitProvider{files: map[string][]byte{
		"configuration/addons-catalog.yaml": []byte(lazyCredsCatalogYAML),
		"configuration/managed-clusters.yaml": []byte(`clusters:
  - name: inline-sharko-managed
    credsSource: inline-kubeconfig
    labels: {}
  - name: inline-self-managed
    credsSource: inline-kubeconfig
    connectionManagedBy: user
    labels: {}
  - name: backend-no-provider
    credsSource: secret-kubeconfig
    labels: {}
  - name: connection-only
    labels: {}
`),
	}}
	srv.connSvc.SetGitProviderOverride(fakeGit)
	// No credProvider installed — backendConfigured=false for every cluster.

	router := NewRouter(srv, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	var resp models.ClustersResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	want := map[string]bool{
		"inline-sharko-managed": true,  // Sharko wrote the ArgoCD Secret at registration
		"inline-self-managed":   false, // Sharko never writes it for a self-managed connection
		"backend-no-provider":   false, // backend source, but no secrets provider configured
		"connection-only":       false, // unknown source, no backend configured
	}
	got := map[string]bool{}
	for _, c := range resp.Clusters {
		got[c.Name] = c.AddonSecretsReady
	}
	for name, wantReady := range want {
		gotReady, ok := got[name]
		if !ok {
			t.Errorf("cluster %q missing from response", name)
			continue
		}
		if gotReady != wantReady {
			t.Errorf("cluster %q: addon_secrets_ready = %v, want %v", name, gotReady, wantReady)
		}
	}
}
