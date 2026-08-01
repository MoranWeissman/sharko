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
	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// inMemoryConnStore is a minimal config.Store implementation for dashboard
// tests — enough to satisfy DashboardService.GetStats's connSvc.List() call
// without standing up a full FileStore. Returns an empty-but-well-formed
// connections list, which is what GetStats's connection-stats branch wants
// when probing aggregate state on a fresh install.
type inMemoryConnStore struct {
	connections []models.Connection
	active      string
}

func (s *inMemoryConnStore) ListConnections() ([]models.Connection, error) {
	out := make([]models.Connection, len(s.connections))
	copy(out, s.connections)
	return out, nil
}
func (s *inMemoryConnStore) GetConnection(name string) (*models.Connection, error) {
	for i := range s.connections {
		if s.connections[i].Name == name {
			c := s.connections[i]
			return &c, nil
		}
	}
	return nil, nil
}
func (s *inMemoryConnStore) SaveConnection(conn models.Connection) error {
	s.connections = append(s.connections, conn)
	return nil
}
func (s *inMemoryConnStore) DeleteConnection(name string) error {
	out := s.connections[:0]
	for _, c := range s.connections {
		if c.Name != name {
			out = append(out, c)
		}
	}
	s.connections = out
	return nil
}
func (s *inMemoryConnStore) GetActiveConnection() (string, error) { return s.active, nil }
func (s *inMemoryConnStore) SetActiveConnection(name string) error {
	s.active = name
	return nil
}
func (s *inMemoryConnStore) MergeConnectionFromEnvAtomic(name string) (bool, error) {
	return false, nil // no-op for dashboard test
}

// argocdEmptyStub returns an httptest server that answers every ArgoCD list
// endpoint with an empty `{"items":[]}` payload. Lets DashboardService.GetStats
// exercise its real ArgoCD client without dragging in a fake-client harness —
// the dashboard test is exercising the file-not-found degrade path, not the
// ArgoCD wiring itself.
func argocdEmptyStub(t *testing.T) *argocd.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)
	return argocd.NewClient(srv.URL, "test-token", true)
}

// argocdClustersStub returns an httptest server that answers
// GET /api/v1/clusters with the given name -> connectionState pairs (in
// the same nested shape ArgoCD's real API uses, per argocdClusterItem in
// internal/argocd/client.go) and every other endpoint (applications, etc.)
// with an empty items list. Lets the cluster-classification tests below
// drive DashboardService.GetStats's five-state breakdown through the real
// ArgoCD client/JSON-decoding path rather than only unit-testing the
// switch statement in isolation.
func argocdClustersStub(t *testing.T, clusters map[string]string) *argocd.Client {
	t.Helper()
	type item struct {
		Name string `json:"name"`
		Info struct {
			ConnectionState struct {
				Status string `json:"status"`
			} `json:"connectionState"`
		} `json:"info"`
	}
	items := make([]item, 0, len(clusters))
	for name, state := range clusters {
		it := item{Name: name}
		it.Info.ConnectionState.Status = state
		items = append(items, it)
	}
	body, err := json.Marshal(struct {
		Items []item `json:"items"`
	}{Items: items})
	if err != nil {
		t.Fatalf("marshalling stub cluster items: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/clusters" {
			_, _ = w.Write(body)
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)
	return argocd.NewClient(srv.URL, "test-token", true)
}

// dashboardClassificationFixture builds a v4 fakeGitProvider with two
// Git-registered clusters: "with-addon" (one enabled addon) and
// "no-addons" (zero enabled addons) — the exact split the
// untested-vs-pending distinction hinges on.
func dashboardClassificationFixture(t *testing.T) *fakeGitProvider {
	t.Helper()
	withAddon, err := models.SaveClusterAddons(models.ClusterAddonsSpec{
		Cluster: "with-addon",
		Addons:  map[string]models.ClusterAddonsAddon{"cert-manager": {Enabled: true}},
	})
	if err != nil {
		t.Fatalf("building with-addon assignment: %v", err)
	}
	noAddons, err := models.SaveClusterAddons(models.ClusterAddonsSpec{
		Cluster: "no-addons",
		Addons:  map[string]models.ClusterAddonsAddon{},
	})
	if err != nil {
		t.Fatalf("building no-addons assignment: %v", err)
	}
	approved, err := config.SaveAddonCatalog(config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
		},
	})
	if err != nil {
		t.Fatalf("building catalog: %v", err)
	}
	return &fakeGitProvider{
		files: map[string][]byte{
			orchestrator.EnginePinPath:         []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
			orchestrator.V4ManagedClustersPath: []byte("clusters:\n  - name: with-addon\n  - name: no-addons\n"),
			"clusters/with-addon.yaml":         withAddon,
			"clusters/no-addons.yaml":          noAddons,
			config.AddonCatalogPath:            approved,
		},
	}
}

// TestGetStats_ClusterClassification_FiveStates locks down the five-state
// breakdown (dashboard UX review 2026-08-01, blocker B1): the server is
// now the single place this classification is computed, using the SAME
// vocabulary as ui/src/lib/clusterStatus.ts.
func TestGetStats_ClusterClassification_FiveStates(t *testing.T) {
	cases := []struct {
		name            string
		argocdState     string // ConnectionState for "with-addon"; "" means absent from ArgoCD entirely (missing)
		absentFromArgo  bool
		wantConnected   int
		wantPending     int
		wantFailed      int
		wantMissing     int
		wantUntestedMin int // "no-addons" is always untested/unknown in ArgoCD in these cases
	}{
		{name: "Successful is connected", argocdState: "Successful", wantConnected: 1},
		{name: "Connected (alt spelling) is also connected", argocdState: "Connected", wantConnected: 1},
		{name: "empty status with addons enabled is pending", argocdState: "", wantPending: 1},
		{name: "Unknown status with addons enabled is pending", argocdState: "Unknown", wantPending: 1},
		{name: "Failed is failed", argocdState: "Failed", wantFailed: 1},
		{name: "an unrecognised status falls through to failed", argocdState: "SomeNewArgoStatus", wantFailed: 1},
		{name: "absent from ArgoCD entirely is missing", absentFromArgo: true, wantMissing: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			connSvc := NewConnectionService(&inMemoryConnStore{})
			svc := NewDashboardService(connSvc, "")
			gp := dashboardClassificationFixture(t)

			clusters := map[string]string{"no-addons": "Unknown"}
			if !tc.absentFromArgo {
				clusters["with-addon"] = tc.argocdState
			}
			ac := argocdClustersStub(t, clusters)

			resp, err := svc.GetStats(context.Background(), gp, ac)
			if err != nil {
				t.Fatalf("GetStats returned error: %v", err)
			}
			if resp.Clusters.Total != 2 {
				t.Fatalf("Clusters.Total = %d, want 2", resp.Clusters.Total)
			}
			if resp.Clusters.Connected != tc.wantConnected {
				t.Errorf("Connected = %d, want %d", resp.Clusters.Connected, tc.wantConnected)
			}
			if resp.Clusters.Pending != tc.wantPending {
				t.Errorf("Pending = %d, want %d", resp.Clusters.Pending, tc.wantPending)
			}
			if resp.Clusters.Failed != tc.wantFailed {
				t.Errorf("Failed = %d, want %d", resp.Clusters.Failed, tc.wantFailed)
			}
			if resp.Clusters.Missing != tc.wantMissing {
				t.Errorf("Missing = %d, want %d", resp.Clusters.Missing, tc.wantMissing)
			}
			// "no-addons" (zero enabled addons, Unknown in ArgoCD) is
			// ALWAYS untested in every case here — it never has anything
			// for ArgoCD to probe regardless of what "with-addon" is doing.
			if resp.Clusters.Untested != 1 {
				t.Errorf("Untested = %d, want 1 (the zero-addon cluster)", resp.Clusters.Untested)
			}
		})
	}
}

// TestGetStats_ClusterClassification_UntestedVsPending is the direct
// regression test for blocker B1: a cluster with ZERO enabled addons and
// an unresolved ArgoCD status is "untested" (ArgoCD will never probe it
// until something is enabled), never "pending"/"disconnected" — the old
// binary count called this cluster "disconnected" forever.
func TestGetStats_ClusterClassification_UntestedVsPending(t *testing.T) {
	connSvc := NewConnectionService(&inMemoryConnStore{})
	svc := NewDashboardService(connSvc, "")
	gp := dashboardClassificationFixture(t)
	ac := argocdClustersStub(t, map[string]string{
		"with-addon": "Unknown", // has an addon enabled -> pending, a probe is coming
		"no-addons":  "Unknown", // nothing enabled -> untested, no probe ever coming
	})

	resp, err := svc.GetStats(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("GetStats returned error: %v", err)
	}
	if resp.Clusters.Pending != 1 {
		t.Errorf("Pending = %d, want 1 (with-addon)", resp.Clusters.Pending)
	}
	if resp.Clusters.Untested != 1 {
		t.Errorf("Untested = %d, want 1 (no-addons)", resp.Clusters.Untested)
	}
	// Neither untested nor pending count toward failed/missing — the old
	// "disconnected" bucket that a fresh, zero-addon cluster fell into
	// forever must be zero here.
	if resp.Clusters.Failed != 0 || resp.Clusters.Missing != 0 {
		t.Errorf("expected 0 failed/missing, got failed=%d missing=%d", resp.Clusters.Failed, resp.Clusters.Missing)
	}
}

// TestGetStats_Addons_EnabledDeploymentsIsPlainCount is the regression
// test for finding H5: EnabledDeployments must be a plain count, not one
// half of a fake "N/N" ratio. (The old TotalDeployments field is gone
// from the model entirely — this test would fail to compile if it ever
// came back as a sibling field defined equal to EnabledDeployments.)
func TestGetStats_Addons_EnabledDeploymentsIsPlainCount(t *testing.T) {
	connSvc := NewConnectionService(&inMemoryConnStore{})
	svc := NewDashboardService(connSvc, "")
	gp := dashboardClassificationFixture(t)
	ac := argocdClustersStub(t, map[string]string{"with-addon": "Successful", "no-addons": "Unknown"})

	resp, err := svc.GetStats(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("GetStats returned error: %v", err)
	}
	if resp.Addons.EnabledDeployments != 1 {
		t.Errorf("EnabledDeployments = %d, want 1", resp.Addons.EnabledDeployments)
	}
}

// TestGetStats_MissingFileReturnsZeroState is the V124-23 / BUG-048
// regression test. When managed-clusters.yaml (or addons-catalog.yaml) is
// missing — fresh-install gitops repo, no clusters yet — GetStats MUST
// degrade to zero-state stats rather than propagate a 500 with the raw
// filesystem error string. Same isGitFileNotFound contract as
// ClusterService.ListClusters (V124-2.2).
func TestGetStats_MissingFileReturnsZeroState(t *testing.T) {
	connSvc := NewConnectionService(&inMemoryConnStore{})
	svc := NewDashboardService(connSvc, "")
	gp := &fakeGP{} // every lookup returns wrapped ErrFileNotFound

	resp, err := svc.GetStats(context.Background(), gp, argocdEmptyStub(t))
	if err != nil {
		t.Fatalf("GetStats returned err on missing-file path: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response on missing-file path")
	}
	// Zero state across every category — there are no clusters, addons,
	// or applications when the gitops repo is empty.
	if resp.Clusters.Total != 0 {
		t.Errorf("expected 0 clusters, got %d", resp.Clusters.Total)
	}
	if resp.Addons.TotalAvailable != 0 {
		t.Errorf("expected 0 addons available, got %d", resp.Addons.TotalAvailable)
	}
	if resp.Addons.EnabledDeployments != 0 {
		t.Errorf("expected 0 enabled deployments, got %d", resp.Addons.EnabledDeployments)
	}
	if resp.Applications.Total != 0 {
		t.Errorf("expected 0 applications, got %d", resp.Applications.Total)
	}
}

// TestGetStats_RealErrorPropagates locks down the other half of the
// V124-23 contract: a non-file-not-found error from the git provider MUST
// propagate (5xx) rather than silently degrade to zero state. Same H2
// anti-pattern that V124-2.12 already fixed for /clusters.
func TestGetStats_RealErrorPropagates(t *testing.T) {
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
			connSvc := NewConnectionService(&inMemoryConnStore{})
			svc := NewDashboardService(connSvc, "")
			gp := &fakeGP{
				err: map[string]error{
					"configuration/managed-clusters.yaml": tc.err,
				},
			}
			if _, err := svc.GetStats(context.Background(), gp, argocdEmptyStub(t)); err == nil {
				t.Fatalf("expected error to propagate from %q, got nil", tc.err)
			} else if !strings.Contains(err.Error(), "managed-clusters.yaml") {
				t.Errorf("expected error to mention managed-clusters.yaml, got %q", err.Error())
			}
		})
	}
}

// TestGetStats_EmptyResponseHasNoLeakedError is the over-the-wire shape
// contract for BUG-048: the missing-file path must not surface raw
// filesystem error strings to the caller. Pairs with the
// GetVersionMatrix variant in addon_test.go.
func TestGetStats_EmptyResponseHasNoLeakedError(t *testing.T) {
	connSvc := NewConnectionService(&inMemoryConnStore{})
	svc := NewDashboardService(connSvc, "")
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

	resp, err := svc.GetStats(context.Background(), gp, argocdEmptyStub(t))
	if err != nil {
		t.Fatalf("expected nil err on missing-file path, got %v", err)
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

// ---------------------------------------------------------------------------
// Wave 2 ride-along w2-q6 item 4: dashboard git-side stats must be v4-aware
// (managed-clusters.yaml + clusters/*.yaml + catalog/addons.yaml delta)
// instead of only ever reading the v3 files. fakeGitProvider (defined in
// addon_test.go, same package) is reused here because — unlike fakeGP — its
// ListDirectory derives real entries from the files map, which the v4
// clusters/*.yaml listing needs.
// ---------------------------------------------------------------------------

// TestGetStats_V4Repo_UsesV4Sources proves GetStats detects a v4 repo (the
// same engine-pin probe AddonService.GetVersionMatrix uses) and counts
// clusters/addons from managed-clusters.yaml + clusters/*.yaml +
// catalog/addons.yaml instead of the v3 files — even when v3 files are
// ALSO present (a repo mid-migration), the v4 branch must win once the
// engine pin exists.
func TestGetStats_V4Repo_UsesV4Sources(t *testing.T) {
	connSvc := NewConnectionService(&inMemoryConnStore{})
	svc := NewDashboardService(connSvc, "")

	prodEU, err := models.SaveClusterAddons(models.ClusterAddonsSpec{
		Cluster: "prod-eu",
		Addons: map[string]models.ClusterAddonsAddon{
			"cert-manager": {Enabled: true},
			"external-dns": {Enabled: false},
		},
	})
	if err != nil {
		t.Fatalf("building prod-eu assignment: %v", err)
	}
	stagingUS, err := models.SaveClusterAddons(models.ClusterAddonsSpec{
		Cluster: "staging-us",
		Addons: map[string]models.ClusterAddonsAddon{
			"cert-manager": {Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("building staging-us assignment: %v", err)
	}
	delta, err := config.SaveAddonCatalog(config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
			"external-dns": {RepoURL: "https://kubernetes-sigs.github.io/external-dns", Chart: "external-dns", Version: "1.14.0"},
		},
	})
	if err != nil {
		t.Fatalf("building catalog delta: %v", err)
	}

	gp := &fakeGitProvider{
		files: map[string][]byte{
			orchestrator.EnginePinPath:     []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
			orchestrator.V4ManagedClustersPath: []byte("clusters:\n  - name: prod-eu\n  - name: staging-us\n"),
			"clusters/prod-eu.yaml":        prodEU,
			"clusters/staging-us.yaml":     stagingUS,
			config.AddonCatalogPath:   delta,
			// v3 files ALSO present, to prove they are ignored once the
			// engine pin routes this to the v4 branch.
			"configuration/managed-clusters.yaml": []byte("clusters:\n  - name: v3-only-cluster\n    labels: {}\n"),
			"configuration/addons-catalog.yaml":   []byte("applicationsets:\n  - name: v3-only-addon\n"),
		},
	}

	resp, err := svc.GetStats(context.Background(), gp, argocdEmptyStub(t))
	if err != nil {
		t.Fatalf("GetStats returned error: %v", err)
	}
	if resp.Clusters.Total != 2 {
		t.Errorf("Clusters.Total = %d, want 2 (v4 managed-clusters.yaml) — v3 managed-clusters.yaml must be ignored", resp.Clusters.Total)
	}
	if resp.Addons.TotalAvailable != 2 {
		t.Errorf("Addons.TotalAvailable = %d, want 2 (v4 catalog/addons.yaml delta entries)", resp.Addons.TotalAvailable)
	}
	// prod-eu.cert-manager (enabled) + staging-us.cert-manager (enabled);
	// prod-eu.external-dns is enabled=false and must not count.
	if resp.Addons.EnabledDeployments != 2 {
		t.Errorf("Addons.EnabledDeployments = %d, want 2", resp.Addons.EnabledDeployments)
	}
}

// TestGetStats_V4Repo_MissingFilesReturnsZeroState mirrors
// TestGetStats_MissingFileReturnsZeroState for the v4 branch: a v4 repo
// (engine pin present) with no clusters or catalog delta yet degrades to
// zero-state stats, never a 500.
func TestGetStats_V4Repo_MissingFilesReturnsZeroState(t *testing.T) {
	connSvc := NewConnectionService(&inMemoryConnStore{})
	svc := NewDashboardService(connSvc, "")
	gp := &fakeGitProvider{
		files: map[string][]byte{
			orchestrator.EnginePinPath: []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
		},
	}

	resp, err := svc.GetStats(context.Background(), gp, argocdEmptyStub(t))
	if err != nil {
		t.Fatalf("GetStats returned err on v4 missing-file path: %v", err)
	}
	if resp.Clusters.Total != 0 {
		t.Errorf("expected 0 clusters, got %d", resp.Clusters.Total)
	}
	if resp.Addons.TotalAvailable != 0 {
		t.Errorf("expected 0 addons available, got %d", resp.Addons.TotalAvailable)
	}
	if resp.Addons.EnabledDeployments != 0 {
		t.Errorf("expected 0 enabled deployments, got %d", resp.Addons.EnabledDeployments)
	}
}

// dashboardCuratedFixture returns a small, hand-built curated catalog (3
// addons) for TestGetStats_V4Repo_TotalAvailableUsesCuratedCatalog.
func dashboardCuratedFixture(t *testing.T) *catalog.Catalog {
	t.Helper()
	y := `
addons:
  - name: cert-manager
    description: Automated TLS certificate lifecycle management.
    chart: cert-manager
    repo: https://charts.jetstack.io
    default_namespace: cert-manager
    license: Apache-2.0
    maintainers: [jetstack]
    category: security
    curated_by: [cncf-graduated]
  - name: external-dns
    description: Syncs Kubernetes Services/Ingresses to DNS providers.
    chart: external-dns
    repo: https://kubernetes-sigs.github.io/external-dns
    default_namespace: external-dns
    license: Apache-2.0
    maintainers: [kubernetes-sigs]
    category: networking
    curated_by: [cncf-graduated]
  - name: metrics-server
    description: Resource metrics for kubectl top and HPA.
    chart: metrics-server
    repo: https://kubernetes-sigs.github.io/metrics-server/
    default_namespace: kube-system
    license: Apache-2.0
    maintainers: [kubernetes-sigs]
    category: observability
    curated_by: [cncf-graduated]
`
	cat, err := catalog.LoadBytes([]byte(y))
	if err != nil {
		t.Fatalf("LoadBytes fixture: %v", err)
	}
	return cat
}

// TestGetStats_V4Repo_TotalAvailableCountsApprovedOnly: the dashboard tile
// says how many addons the ORG approved. A fresh v4 repo with no
// catalog.yaml has approved nothing, so the honest number is 0 — a tile
// claiming 45 available addons on day zero is exactly the lie the
// approved-list rebuild removes.
func TestGetStats_V4Repo_TotalAvailableCountsApprovedOnly(t *testing.T) {
	connSvc := NewConnectionService(&inMemoryConnStore{})
	svc := NewDashboardService(connSvc, "")
	svc.SetCuratedCatalog(dashboardCuratedFixture(t))

	gp := &fakeGitProvider{
		files: map[string][]byte{
			orchestrator.EnginePinPath:         []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
			orchestrator.V4ManagedClustersPath: []byte("clusters:\n  - name: prod-eu\n"),
			// No catalog.yaml at all — nothing approved yet.
		},
	}

	resp, err := svc.GetStats(context.Background(), gp, argocdEmptyStub(t))
	if err != nil {
		t.Fatalf("GetStats returned error: %v", err)
	}
	if resp.Addons.TotalAvailable != 0 {
		t.Errorf("Addons.TotalAvailable = %d, want 0 — a fresh repo approves nothing", resp.Addons.TotalAvailable)
	}
}

// TestGetStats_V4Repo_TotalAvailableCountsTheCatalogFile: once the org
// approves two addons, the tile says two — even though the list Sharko
// ships carries three.
func TestGetStats_V4Repo_TotalAvailableCountsTheCatalogFile(t *testing.T) {
	connSvc := NewConnectionService(&inMemoryConnStore{})
	svc := NewDashboardService(connSvc, "")
	svc.SetCuratedCatalog(dashboardCuratedFixture(t))

	approved, err := config.SaveAddonCatalog(config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", Version: "1.14.5"},
			"external-dns": {RepoURL: "https://example.test/charts", Chart: "external-dns", Version: "1.14.4"},
		},
	})
	if err != nil {
		t.Fatalf("SaveAddonCatalog: %v", err)
	}

	gp := &fakeGitProvider{
		files: map[string][]byte{
			orchestrator.EnginePinPath:         []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
			orchestrator.V4ManagedClustersPath: []byte("clusters:\n  - name: prod-eu\n"),
			config.AddonCatalogPath:            approved,
		},
	}

	resp, err := svc.GetStats(context.Background(), gp, argocdEmptyStub(t))
	if err != nil {
		t.Fatalf("GetStats returned error: %v", err)
	}
	if resp.Addons.TotalAvailable != 2 {
		t.Errorf("Addons.TotalAvailable = %d, want 2 — the size of catalog.yaml, not the shipped list", resp.Addons.TotalAvailable)
	}
}
