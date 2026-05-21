package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// fakeGitProvider implements gitprovider.GitProvider for testing.
type fakeGitProvider struct {
	files map[string][]byte
}

func (f *fakeGitProvider) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	return f.files[path], nil
}

func (f *fakeGitProvider) ListDirectory(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

func (f *fakeGitProvider) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}

func (f *fakeGitProvider) TestConnection(_ context.Context) error {
	return nil
}

func (f *fakeGitProvider) CreateBranch(_ context.Context, _, _ string) error {
	return nil
}

func (f *fakeGitProvider) CreateOrUpdateFile(_ context.Context, _ string, _ []byte, _, _ string) error {
	return nil
}

func (f *fakeGitProvider) BatchCreateFiles(_ context.Context, _ map[string][]byte, _, _ string) error {
	return nil
}

func (f *fakeGitProvider) DeleteFile(_ context.Context, _, _, _ string) error {
	return nil
}

func (f *fakeGitProvider) CreatePullRequest(_ context.Context, _, _, _, _ string) (*gitprovider.PullRequest, error) {
	return nil, nil
}

func (f *fakeGitProvider) MergePullRequest(_ context.Context, _ int) error {
	return nil
}

func (f *fakeGitProvider) GetPullRequestStatus(_ context.Context, _ int) (string, error) {
	return "open", nil
}

func (f *fakeGitProvider) DeleteBranch(_ context.Context, _ string) error {
	return nil
}

func TestGetVersionMatrix(t *testing.T) {
	clusterAddonsYAML := []byte(`
clusters:
  - name: cluster-a
    labels:
      ingress-nginx: enabled
      cert-manager: enabled
      cert-manager-version: "1.15.0"
      external-dns: disabled
  - name: cluster-b
    labels:
      ingress-nginx: enabled
      cert-manager: enabled
`)

	addonsCatalogYAML := []byte(`
applicationsets:
  - name: cert-manager
    repoURL: https://charts.jetstack.io
    chart: cert-manager
    version: "1.14.0"
    namespace: cert-manager
  - name: ingress-nginx
    repoURL: https://kubernetes.github.io/ingress-nginx
    chart: ingress-nginx
    version: "4.10.0"
    namespace: ingress-nginx
  - name: external-dns
    repoURL: https://kubernetes-sigs.github.io/external-dns
    chart: external-dns
    version: "1.14.0"
    namespace: external-dns
`)

	// Fake ArgoCD server returning applications
	argoApps := map[string]interface{}{
		"items": []map[string]interface{}{
			{
				"metadata": map[string]interface{}{"name": "cert-manager-cluster-a", "namespace": "argocd"},
				"spec": map[string]interface{}{
					"project": "default",
					"source":  map[string]interface{}{"repoURL": "https://charts.jetstack.io", "targetRevision": "1.15.0", "chart": "cert-manager"},
					"destination": map[string]interface{}{"server": "https://cluster-a", "namespace": "cert-manager"},
				},
				"status": map[string]interface{}{
					"sync":   map[string]interface{}{"status": "Synced"},
					"health": map[string]interface{}{"status": "Healthy"},
				},
			},
			{
				"metadata": map[string]interface{}{"name": "ingress-nginx-cluster-a", "namespace": "argocd"},
				"spec": map[string]interface{}{
					"project": "default",
					"source":  map[string]interface{}{"repoURL": "https://kubernetes.github.io/ingress-nginx", "targetRevision": "4.10.0", "chart": "ingress-nginx"},
					"destination": map[string]interface{}{"server": "https://cluster-a", "namespace": "ingress-nginx"},
				},
				"status": map[string]interface{}{
					"sync":   map[string]interface{}{"status": "Synced"},
					"health": map[string]interface{}{"status": "Degraded"},
				},
			},
			{
				"metadata": map[string]interface{}{"name": "cert-manager-cluster-b", "namespace": "argocd"},
				"spec": map[string]interface{}{
					"project": "default",
					"source":  map[string]interface{}{"repoURL": "https://charts.jetstack.io", "targetRevision": "1.14.0", "chart": "cert-manager"},
					"destination": map[string]interface{}{"server": "https://cluster-b", "namespace": "cert-manager"},
				},
				"status": map[string]interface{}{
					"sync":   map[string]interface{}{"status": "Synced"},
					"health": map[string]interface{}{"status": "Healthy"},
				},
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(argoApps)
	}))
	defer ts.Close()

	gp := &fakeGitProvider{
		files: map[string][]byte{
			"configuration/managed-clusters.yaml": clusterAddonsYAML,
			"configuration/addons-catalog.yaml":   addonsCatalogYAML,
		},
	}

	ac := argocd.NewClient(ts.URL, "fake-token", false)
	svc := NewAddonService("")

	resp, err := svc.GetVersionMatrix(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("GetVersionMatrix returned error: %v", err)
	}

	// Verify clusters are sorted
	if len(resp.Clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d", len(resp.Clusters))
	}
	if resp.Clusters[0] != "cluster-a" || resp.Clusters[1] != "cluster-b" {
		t.Errorf("expected clusters [cluster-a, cluster-b], got %v", resp.Clusters)
	}

	// Verify addons are sorted by name
	if len(resp.Addons) != 3 {
		t.Fatalf("expected 3 addons, got %d", len(resp.Addons))
	}
	if resp.Addons[0].AddonName != "cert-manager" {
		t.Errorf("expected first addon to be cert-manager, got %s", resp.Addons[0].AddonName)
	}
	if resp.Addons[1].AddonName != "external-dns" {
		t.Errorf("expected second addon to be external-dns, got %s", resp.Addons[1].AddonName)
	}
	if resp.Addons[2].AddonName != "ingress-nginx" {
		t.Errorf("expected third addon to be ingress-nginx, got %s", resp.Addons[2].AddonName)
	}

	// Check cert-manager on cluster-a: version override 1.15.0, drift = true, health = Healthy
	cmA := resp.Addons[0].Cells["cluster-a"]
	if cmA.Version != "1.15.0" {
		t.Errorf("cert-manager cluster-a version: expected 1.15.0, got %s", cmA.Version)
	}
	if !cmA.DriftFromCatalog {
		t.Error("cert-manager cluster-a should have drift_from_catalog=true")
	}
	if cmA.Health != "Healthy" {
		t.Errorf("cert-manager cluster-a health: expected Healthy, got %s", cmA.Health)
	}

	// Check cert-manager on cluster-b: no override, no drift, health = Healthy
	cmB := resp.Addons[0].Cells["cluster-b"]
	if cmB.Version != "1.14.0" {
		t.Errorf("cert-manager cluster-b version: expected 1.14.0, got %s", cmB.Version)
	}
	if cmB.DriftFromCatalog {
		t.Error("cert-manager cluster-b should have drift_from_catalog=false")
	}
	if cmB.Health != "Healthy" {
		t.Errorf("cert-manager cluster-b health: expected Healthy, got %s", cmB.Health)
	}

	// Check external-dns on cluster-a: disabled
	edA := resp.Addons[1].Cells["cluster-a"]
	if edA.Health != "not_enabled" {
		t.Errorf("external-dns cluster-a health: expected not_enabled, got %s", edA.Health)
	}

	// Check external-dns on cluster-b: no label, should not exist
	if _, exists := resp.Addons[1].Cells["cluster-b"]; exists {
		t.Error("external-dns should not have an entry for cluster-b (no label)")
	}

	// Check ingress-nginx on cluster-a: health = Degraded
	inA := resp.Addons[2].Cells["cluster-a"]
	if inA.Health != "Degraded" {
		t.Errorf("ingress-nginx cluster-a health: expected Degraded, got %s", inA.Health)
	}

	// Check ingress-nginx on cluster-b: enabled but no ArgoCD app -> missing
	inB := resp.Addons[2].Cells["cluster-b"]
	if inB.Health != "missing" {
		t.Errorf("ingress-nginx cluster-b health: expected missing, got %s", inB.Health)
	}
}

// TestGetVersionMatrix_MissingFileReturnsEmpty is the V124-23 / BUG-048
// regression test. When managed-clusters.yaml (or addons-catalog.yaml) is
// missing — the natural state of a freshly-installed Sharko whose gitops
// repo has not been bootstrapped yet — GetVersionMatrix MUST degrade to an
// empty matrix rather than propagate a 500-class error. This locks down
// the parity fix that brings the addons handler onto the same isGitFileNotFound
// contract as ClusterService.ListClusters (V124-2.2).
//
// Backs the test with the shared fakeGP (cluster_test.go) because it returns
// a wrapped gitprovider.ErrFileNotFound on missing keys, exactly the shape
// the production providers honour after V124-2.12.
func TestGetVersionMatrix_MissingFileReturnsEmpty(t *testing.T) {
	// Stub ArgoCD with an empty applications list — there are no apps to
	// enrich since there are no clusters.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)

	ac := argocd.NewClient(srv.URL, "test-token", true)
	svc := NewAddonService("")
	gp := &fakeGP{} // empty maps — every lookup returns ErrFileNotFound

	resp, err := svc.GetVersionMatrix(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("GetVersionMatrix returned err on missing-file path: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response on missing-file path")
	}
	if len(resp.Clusters) != 0 {
		t.Errorf("expected 0 clusters from missing-file path, got %d: %+v", len(resp.Clusters), resp.Clusters)
	}
	if len(resp.Addons) != 0 {
		t.Errorf("expected 0 addons from missing-file path, got %d: %+v", len(resp.Addons), resp.Addons)
	}
}

// TestGetVersionMatrix_RealErrorPropagates locks down the other half of
// the V124-23 contract: a non-file-not-found error from the git provider
// MUST propagate (5xx) rather than silently degrade to an empty matrix.
// The pre-fix strings.Contains(err.Error(), "404") matcher would have
// silently masked any of these error shapes — same H2 anti-pattern that
// V124-2.12 fixed for /clusters.
func TestGetVersionMatrix_RealErrorPropagates(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"github auth-or-perm error", errors.New("GitHub repository not found — check the URL and credentials")},
		{"wrong branch", errors.New("branch 'main' not found")},
		{"rate limit with 404 in body", errors.New("rate limited; body: {\"status\":404,\"reason\":\"abuse\"}")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewAddonService("")
			gp := &fakeGP{
				err: map[string]error{
					"configuration/managed-clusters.yaml": tc.err,
				},
			}
			// nil ac is fine because the call MUST fail before reaching
			// the ArgoCD step. If a regression re-introduces the substring
			// matcher, GetVersionMatrix would proceed past the err check
			// and eventually nil-deref on ac.ListApplications.
			if _, err := svc.GetVersionMatrix(context.Background(), gp, nil); err == nil {
				t.Fatalf("expected error to propagate from %q, got nil", tc.err)
			} else if !strings.Contains(err.Error(), "managed-clusters.yaml") {
				t.Errorf("expected error to mention managed-clusters.yaml, got %q", err.Error())
			}
		})
	}
}

// TestGetVersionMatrix_EmptyResponseHasNoLeakedError is the over-the-wire
// shape contract for BUG-048: the missing-file path must not surface raw
// filesystem error strings to the caller. Combined with the handler's
// writeJSON wrapper this guarantees a clean 200 + `{clusters:[],addons:[]}`
// payload — no `"reading managed-clusters.yaml: ... file not found"` leak.
//
// We assert this at the service-shape level rather than serializing through
// the handler, because the handler test suite already covers writeJSON's
// behaviour and the service contract is what's load-bearing here.
func TestGetVersionMatrix_EmptyResponseHasNoLeakedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)

	ac := argocd.NewClient(srv.URL, "test-token", true)
	svc := NewAddonService("")
	gp := &fakeGP{
		err: map[string]error{
			"configuration/managed-clusters.yaml": fmt.Errorf(
				"fakeGP: configuration/managed-clusters.yaml: %w",
				gitprovider.ErrFileNotFound,
			),
			"configuration/addons-catalog.yaml": fmt.Errorf(
				"fakeGP: configuration/addons-catalog.yaml: %w",
				gitprovider.ErrFileNotFound,
			),
		},
	}

	resp, err := svc.GetVersionMatrix(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("expected nil err on missing-file path, got %v", err)
	}
	// Confirm the response body would serialise cleanly — no nil maps that
	// would render as JSON nulls and confuse the UI.
	if resp.Clusters == nil {
		t.Error("expected resp.Clusters to be non-nil empty slice (got nil)")
	}
	if resp.Addons == nil {
		t.Error("expected resp.Addons to be non-nil empty slice (got nil)")
	}
	body, mErr := json.Marshal(resp)
	if mErr != nil {
		t.Fatalf("response did not serialise: %v", mErr)
	}
	if strings.Contains(string(body), "managed-clusters.yaml") {
		t.Errorf("response body leaked filesystem path: %s", string(body))
	}
	if strings.Contains(string(body), "file not found") {
		t.Errorf("response body leaked error string: %s", string(body))
	}
}

// TestGetCatalog_DeployedAndTargetClusterCounts is the V126-3.1 (DESIGN-02)
// contract test: every AddonCatalogItem in the catalog response must carry
// the deployed_cluster_count (N = clusters where the ArgoCD Application is
// BOTH Synced AND Healthy) and total_target_cluster_count (M = clusters
// where the addon is labelled enabled). The UI's tile-level badge picks one
// of four copies based on the (N, M) pair, so we fixture four addons that
// exercise all four states:
//
//	M=0, N=0  → "Not deployed anywhere"
//	M>0, N=0  → "Not deployed yet"
//	0<N<M     → "Running on N/M clusters"
//	N==M, M>0 → "Running on N clusters"
//
// Synced + Healthy is the gating predicate, not Healthy alone — an addon
// that is Healthy but OutOfSync is counted toward HealthyApplications (for
// the legacy stat cards) but NOT toward DeployedClusterCount. This lock
// down test guards both that gating logic and the field plumbing.
func TestGetCatalog_DeployedAndTargetClusterCounts(t *testing.T) {
	clustersYAML := []byte(`
clusters:
  - name: cluster-a
    labels:
      addon-running-everywhere: enabled
      addon-partially-running: enabled
      addon-target-only: enabled
  - name: cluster-b
    labels:
      addon-running-everywhere: enabled
      addon-partially-running: enabled
      addon-target-only: enabled
  - name: cluster-c
    labels:
      addon-running-everywhere: enabled
      addon-not-anywhere: disabled
`)

	catalogYAML := []byte(`
applicationsets:
  - name: addon-running-everywhere
    repoURL: https://example.com/charts
    chart: chart-everywhere
    version: "1.0.0"
    namespace: ns-everywhere
  - name: addon-partially-running
    repoURL: https://example.com/charts
    chart: chart-partial
    version: "1.0.0"
    namespace: ns-partial
  - name: addon-target-only
    repoURL: https://example.com/charts
    chart: chart-target
    version: "1.0.0"
    namespace: ns-target
  - name: addon-not-anywhere
    repoURL: https://example.com/charts
    chart: chart-nowhere
    version: "1.0.0"
    namespace: ns-nowhere
`)

	// ArgoCD fixture: each application carries explicit sync + health.
	// - addon-running-everywhere/{a,b,c} → all Synced + Healthy → N=3, M=3
	// - addon-partially-running/a → Synced + Healthy (counts toward N)
	//   addon-partially-running/b → Synced + Healthy BUT we mark it
	//                              OutOfSync to prove the predicate is the
	//                              AND of sync + health, not health alone
	//   → N=1, M=2
	// - addon-target-only/{a,b} → NO ArgoCD apps → N=0, M=2
	// - addon-not-anywhere → no cluster enables it (cluster-c sets disabled)
	//                       → N=0, M=0
	argoApps := map[string]interface{}{
		"items": []map[string]interface{}{
			{
				"metadata": map[string]interface{}{"name": "addon-running-everywhere-cluster-a", "namespace": "argocd"},
				"spec":     map[string]interface{}{"source": map[string]interface{}{}, "destination": map[string]interface{}{}},
				"status": map[string]interface{}{
					"sync":   map[string]interface{}{"status": "Synced"},
					"health": map[string]interface{}{"status": "Healthy"},
				},
			},
			{
				"metadata": map[string]interface{}{"name": "addon-running-everywhere-cluster-b", "namespace": "argocd"},
				"spec":     map[string]interface{}{"source": map[string]interface{}{}, "destination": map[string]interface{}{}},
				"status": map[string]interface{}{
					"sync":   map[string]interface{}{"status": "Synced"},
					"health": map[string]interface{}{"status": "Healthy"},
				},
			},
			{
				"metadata": map[string]interface{}{"name": "addon-running-everywhere-cluster-c", "namespace": "argocd"},
				"spec":     map[string]interface{}{"source": map[string]interface{}{}, "destination": map[string]interface{}{}},
				"status": map[string]interface{}{
					"sync":   map[string]interface{}{"status": "Synced"},
					"health": map[string]interface{}{"status": "Healthy"},
				},
			},
			{
				"metadata": map[string]interface{}{"name": "addon-partially-running-cluster-a", "namespace": "argocd"},
				"spec":     map[string]interface{}{"source": map[string]interface{}{}, "destination": map[string]interface{}{}},
				"status": map[string]interface{}{
					"sync":   map[string]interface{}{"status": "Synced"},
					"health": map[string]interface{}{"status": "Healthy"},
				},
			},
			{
				// Healthy but OutOfSync — proves the AND predicate is enforced.
				"metadata": map[string]interface{}{"name": "addon-partially-running-cluster-b", "namespace": "argocd"},
				"spec":     map[string]interface{}{"source": map[string]interface{}{}, "destination": map[string]interface{}{}},
				"status": map[string]interface{}{
					"sync":   map[string]interface{}{"status": "OutOfSync"},
					"health": map[string]interface{}{"status": "Healthy"},
				},
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(argoApps)
	}))
	defer ts.Close()

	gp := &fakeGitProvider{
		files: map[string][]byte{
			"configuration/managed-clusters.yaml": clustersYAML,
			"configuration/addons-catalog.yaml":   catalogYAML,
		},
	}
	ac := argocd.NewClient(ts.URL, "fake-token", false)
	svc := NewAddonService("")

	resp, err := svc.GetCatalog(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("GetCatalog returned err: %v", err)
	}

	byName := make(map[string]int)
	for i, a := range resp.Addons {
		byName[a.AddonName] = i
	}

	cases := []struct {
		name           string
		wantDeployed   int // N
		wantTarget     int // M
		stateNarrative string
	}{
		{"addon-running-everywhere", 3, 3, "N==M (Running on N clusters)"},
		{"addon-partially-running", 1, 2, "0<N<M (Running on N/M clusters)"},
		{"addon-target-only", 0, 2, "N=0, M>0 (Not deployed yet)"},
		{"addon-not-anywhere", 0, 0, "M=0 (Not deployed anywhere)"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx, ok := byName[tc.name]
			if !ok {
				t.Fatalf("expected addon %q in response, got %+v", tc.name, byName)
			}
			got := resp.Addons[idx]
			if got.DeployedClusterCount != tc.wantDeployed {
				t.Errorf("[%s] DeployedClusterCount: want %d, got %d (state: %s)",
					tc.name, tc.wantDeployed, got.DeployedClusterCount, tc.stateNarrative)
			}
			if got.TotalTargetClusterCount != tc.wantTarget {
				t.Errorf("[%s] TotalTargetClusterCount: want %d, got %d (state: %s)",
					tc.name, tc.wantTarget, got.TotalTargetClusterCount, tc.stateNarrative)
			}
		})
	}
}
