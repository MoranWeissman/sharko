package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// Compile-time assertion that fakeGP satisfies gitprovider.GitProvider in
// full. Locks down review finding L3 — without this assertion, an
// interface-method-rename in gitprovider would silently leave fakeGP behind
// without the test package noticing until a real call site broke.
var _ gitprovider.GitProvider = (*fakeGP)(nil)

// fakeGP is a minimal gitprovider.GitProvider that returns canned errors or
// content per path. It only implements the read methods used by
// ClusterService — write methods are no-ops.
type fakeGP struct {
	files map[string][]byte
	err   map[string]error
}

// GetFileContent returns canned content per path or canned error per path.
// Paths absent from both maps return a wrapped gitprovider.ErrFileNotFound
// so isGitFileNotFound can detect the missing-file condition via errors.Is
// (the same contract real providers honour after the V124-2.12 fix).
func (f *fakeGP) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	if e, ok := f.err[path]; ok {
		return nil, e
	}
	if data, ok := f.files[path]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("fakeGP: %s: %w", path, gitprovider.ErrFileNotFound)
}

// ListDirectory returns the basenames of entries directly under dir,
// derived from the keys of f.files (immediate children only — no nested
// path segments), the same convention fakeGitProvider uses in
// addon_test.go. A canned error keyed by dir in f.err takes priority, so
// tests can exercise a real listing failure (not just a not-found) the
// same way GetFileContent already can.
func (f *fakeGP) ListDirectory(_ context.Context, dir, _ string) ([]string, error) {
	if e, ok := f.err[dir]; ok {
		return nil, e
	}
	prefix := strings.TrimSuffix(dir, "/") + "/"
	var names []string
	for p := range f.files {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		names = append(names, rest)
	}
	return names, nil
}
func (f *fakeGP) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}
func (f *fakeGP) TestConnection(_ context.Context) error            { return nil }
func (f *fakeGP) CreateBranch(_ context.Context, _, _ string) error { return nil }
func (f *fakeGP) CreateOrUpdateFile(_ context.Context, _ string, _ []byte, _, _ string) error {
	return nil
}
func (f *fakeGP) BatchCreateFiles(_ context.Context, _ map[string][]byte, _, _ string) error {
	return nil
}
func (f *fakeGP) DeleteFile(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeGP) CreatePullRequest(_ context.Context, _, _, _, _ string) (*gitprovider.PullRequest, error) {
	return nil, nil
}
func (f *fakeGP) MergePullRequest(_ context.Context, _ int) error               { return nil }
func (f *fakeGP) GetPullRequestStatus(_ context.Context, _ int) (string, error) { return "", nil }
func (f *fakeGP) DeleteBranch(_ context.Context, _ string) error                { return nil }

// TestIsGitFileNotFound checks every error shape the helper must accept and,
// just as importantly, the false-positive shapes it MUST reject. The
// pre-fix substring matcher silently masked legitimate auth/branch/perm
// errors as "missing file → empty list" (review finding H2). After the
// V124-2.12 fix detection is type-based via gitprovider.ErrFileNotFound or
// fs.ErrNotExist — every other error returns false.
func TestIsGitFileNotFound(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// Positive cases — all wrap the canonical sentinel.
		{"nil", nil, false},
		{"sentinel directly", gitprovider.ErrFileNotFound, true},
		{"sentinel wrapped (mock provider shape)", fmt.Errorf("mock git: configuration/managed-clusters.yaml: %w", gitprovider.ErrFileNotFound), true},
		{"sentinel wrapped (github shape)", fmt.Errorf("get file content: path %q at ref %q: %w", "configuration/managed-clusters.yaml", "main", gitprovider.ErrFileNotFound), true},
		{"sentinel wrapped (azure devops shape)", fmt.Errorf("get file content: path %q at ref %q: %w", "configuration/managed-clusters.yaml", "main", gitprovider.ErrFileNotFound), true},
		{"fs.ErrNotExist via errors.Is", fs.ErrNotExist, true},

		// False-positive cases — these all CONTAIN substrings that the old
		// helper would have matched (review finding H2). They must not
		// silently degrade to "empty list" anymore.
		{"github auth-or-perm error mentioning 'not found'", errors.New("GitHub repository not found — check the URL and credentials"), false},
		{"wrong branch error", errors.New("branch 'main' not found"), false},
		{"unrelated downstream not-found", errors.New("deployment 'foo' not found"), false},
		{`bytes-count error containing "404"`, errors.New("got 4040 bytes"), false},
		{"rate-limit body containing 404", errors.New("rate limited; body: {\"status\":404,\"reason\":\"abuse\"}"), false},
		{"unrelated error", errors.New("rate limited"), false},
		{"empty string", errors.New(""), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isGitFileNotFound(tc.err); got != tc.want {
				t.Errorf("isGitFileNotFound(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestClusterService_ListClusters_MissingFileReturnsEmptyList is the V124-2.2
// regression test: when managed-clusters.yaml does not exist, ListClusters
// must NOT propagate a 500-style error. It treats the missing file as an
// empty cluster list (the natural state of a freshly-installed Sharko where
// no clusters have been registered yet) and continues into the ArgoCD
// enrichment step.
//
// We back the argocd.Client with an httptest server so the test exercises
// the real ListClusters code path end-to-end (review finding L2 caught
// that the previous version of this test called GetClusterDetail despite
// its name — the test name lied about what it covered).
func TestClusterService_ListClusters_MissingFileReturnsEmptyList(t *testing.T) {
	// Stand up a stub ArgoCD server that returns an empty cluster list. The
	// real argocd.Client will hit /api/v1/clusters and decode the items
	// array; an empty array means "no upstream clusters either".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/clusters") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)

	ac := argocd.NewClient(srv.URL, "test-token", true)
	svc := NewClusterService("")
	gp := &fakeGP{
		err: map[string]error{
			"configuration/managed-clusters.yaml": fmt.Errorf(
				"fakeGP: configuration/managed-clusters.yaml: %w",
				gitprovider.ErrFileNotFound,
			),
		},
	}

	resp, err := svc.ListClusters(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("ListClusters returned err on missing-file path: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response on missing-file path")
	}
	if len(resp.Clusters) != 0 {
		t.Errorf("expected 0 clusters from missing-file path, got %d: %+v", len(resp.Clusters), resp.Clusters)
	}
}

// TestClusterService_ListClusters_RealErrorPropagates locks down the other
// half of the V124-2.12 contract: a non-file-not-found error from the git
// provider MUST propagate as an error rather than silently degrade to an
// empty list. The pre-fix substring matcher would have masked any of the
// fake errors below as "empty list" — review finding H2.
func TestClusterService_ListClusters_RealErrorPropagates(t *testing.T) {
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
			svc := NewClusterService("")
			gp := &fakeGP{
				err: map[string]error{
					"configuration/managed-clusters.yaml": tc.err,
				},
			}
			// Pass nil ac because we expect the call to fail before ever
			// reaching the ArgoCD step. If a regression re-introduces the
			// substring matcher, ListClusters would proceed past the err
			// check and eventually nil-deref on ac.ListClusters — that is
			// the loud failure we want.
			if _, err := svc.ListClusters(context.Background(), gp, nil); err == nil {
				t.Fatalf("expected error to propagate from %q, got nil", tc.err)
			}
		})
	}
}

// TestClusterService_ListClusters_ParsesEmptyFile confirms that when the
// managed-clusters.yaml is parsed as the empty-bootstrap document
// "clusters: []", ParseClusterAddons returns an empty slice (no panic, no
// nil deref). This locks down the handoff between the file-not-found
// fallback and the YAML parser — they have to agree on shape.
func TestClusterService_ListClusters_ParsesEmptyFile(t *testing.T) {
	svc := NewClusterService("")
	clusters, err := svc.parser.ParseClusterAddons([]byte("clusters: []"))
	if err != nil {
		t.Fatalf("ParseClusterAddons rejected the empty bootstrap doc: %v", err)
	}
	if len(clusters) != 0 {
		t.Errorf("expected 0 clusters from empty bootstrap, got %d", len(clusters))
	}
}

// TestClusterService_ListClusters_OrphanAndPendingDefaultNonNil locks
// down the V125-1-7 / BUG-058 service-layer contract: every code path
// that returns a ClustersResponse MUST set OrphanRegistrations and
// PendingRegistrations to non-nil empty slices (not nil). The handler
// overwrites these fields with resolver output; the service layer's job
// is to never let a nil array reach the marshaller. V125-1.4 lesson:
// nil arrays surface as `null` JSON which the FE then crashes on when
// it calls `.length`.
func TestClusterService_ListClusters_OrphanAndPendingDefaultNonNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)
	ac := argocd.NewClient(srv.URL, "test-token", true)

	svc := NewClusterService("")

	t.Run("file-not-found path", func(t *testing.T) {
		gp := &fakeGP{
			err: map[string]error{
				"configuration/managed-clusters.yaml": fmt.Errorf(
					"fakeGP: %w", gitprovider.ErrFileNotFound,
				),
			},
		}
		resp, err := svc.ListClusters(context.Background(), gp, ac)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.OrphanRegistrations == nil {
			t.Error("OrphanRegistrations is nil on file-not-found path — must be []")
		}
		if resp.PendingRegistrations == nil {
			t.Error("PendingRegistrations is nil on file-not-found path — must be []")
		}
	})

	t.Run("argocd-error degrade path", func(t *testing.T) {
		// argocd.Client with a bogus URL → ListClusters errors → the
		// service degrades to the early-return branch which must STILL
		// default both arrays to non-nil empty.
		badAC := argocd.NewClient("http://127.0.0.1:1", "token", true)
		gp := &fakeGP{
			files: map[string][]byte{
				"configuration/managed-clusters.yaml": []byte("clusters: []"),
			},
		}
		resp, err := svc.ListClusters(context.Background(), gp, badAC)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.OrphanRegistrations == nil {
			t.Error("OrphanRegistrations is nil on argocd-degrade path — must be []")
		}
		if resp.PendingRegistrations == nil {
			t.Error("PendingRegistrations is nil on argocd-degrade path — must be []")
		}
	})

	t.Run("happy path", func(t *testing.T) {
		gp := &fakeGP{
			files: map[string][]byte{
				"configuration/managed-clusters.yaml": []byte("clusters: []"),
			},
		}
		resp, err := svc.ListClusters(context.Background(), gp, ac)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.OrphanRegistrations == nil {
			t.Error("OrphanRegistrations is nil on happy path — must be []")
		}
		if resp.PendingRegistrations == nil {
			t.Error("PendingRegistrations is nil on happy path — must be []")
		}
	})
}

// TestClusterService_ListClusters_PopulatesServerURL is the V2-cleanup-74.1
// regression test: models.Cluster.ServerURL must be copied from the
// matching ArgoCD cluster's Server field for BOTH a git-managed cluster
// (the argocdMap-match branch) and an ArgoCD-only "not in git" cluster
// (the notInGitClusters branch). Without this, the UI's ClusterTypeBadge
// always fell back to "Self-hosted" because server_url was always null.
//
// It also locks down that the hub-local "in-cluster" entry (matched here
// by both name and the https://kubernetes.default server prefix) never
// leaks a server URL into the response — ListClusters skips it entirely.
func TestClusterService_ListClusters_PopulatesServerURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
			{"name":"prod-eks","server":"https://EXAMPLE.gr7.eu-west-1.eks.amazonaws.com","serverVersion":"v1.29.3"},
			{"name":"orphan-eks","server":"https://ORPHAN.gr7.eu-west-1.eks.amazonaws.com","serverVersion":"v1.28.1"},
			{"name":"in-cluster","server":"https://kubernetes.default.svc"}
		]}`))
	}))
	t.Cleanup(srv.Close)
	ac := argocd.NewClient(srv.URL, "test-token", true)

	svc := NewClusterService("")
	gp := &fakeGP{
		files: map[string][]byte{
			"configuration/managed-clusters.yaml": []byte(
				"clusters:\n  - name: prod-eks\n    labels: {}\n",
			),
		},
	}

	resp, err := svc.ListClusters(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	byName := map[string]string{}
	for _, c := range resp.Clusters {
		byName[c.Name] = c.ServerURL
		if c.Name == "in-cluster" {
			t.Errorf("hub-local in-cluster entry must not appear in ListClusters output, got %+v", c)
		}
	}

	if got := byName["prod-eks"]; got != "https://EXAMPLE.gr7.eu-west-1.eks.amazonaws.com" {
		t.Errorf("git-managed cluster prod-eks: ServerURL = %q, want the EKS API-server URL", got)
	}
	if got := byName["orphan-eks"]; got != "https://ORPHAN.gr7.eu-west-1.eks.amazonaws.com" {
		t.Errorf("ArgoCD-only cluster orphan-eks: ServerURL = %q, want the EKS API-server URL", got)
	}
}

// TestClusterService_GetClusterDetail_PopulatesServerURL locks down the
// GetClusterDetail half of V2-cleanup-74.1: the single-cluster detail
// response must also carry ServerURL, sourced by looking up the requested
// cluster name in ac.ListClusters (GetClusterDetail does not already fetch
// per-cluster ArgoCD data the way ListClusters does).
func TestClusterService_GetClusterDetail_PopulatesServerURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/clusters") {
			_, _ = w.Write([]byte(`{"items":[
				{"name":"prod-eks","server":"https://EXAMPLE.gr7.eu-west-1.eks.amazonaws.com","serverVersion":"v1.29.3","info":{"connectionState":{"status":"Successful"}}}
			]}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/applications") {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	ac := argocd.NewClient(srv.URL, "test-token", true)

	svc := NewClusterService("")
	gp := &fakeGP{
		files: map[string][]byte{
			"configuration/managed-clusters.yaml": []byte(
				"clusters:\n  - name: prod-eks\n    labels: {}\n",
			),
			"configuration/addons-catalog.yaml": []byte("applicationsets: []"),
		},
	}

	resp, err := svc.GetClusterDetail(context.Background(), "prod-eks", gp, ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response for known cluster")
	}
	if got := resp.Cluster.ServerURL; got != "https://EXAMPLE.gr7.eu-west-1.eks.amazonaws.com" {
		t.Errorf("GetClusterDetail: ServerURL = %q, want the EKS API-server URL", got)
	}
}

// TestClusterService_GetClusterComparison_PopulatesServerURL is the
// V2-cleanup-80.1 regression test: the comparison endpoint's cluster object
// must also carry ServerURL. #487 (V2-cleanup-74.1) populated ServerURL in
// ListClusters and GetClusterDetail but missed GetClusterComparison, so the
// detail page's ClusterTypeBadge (which reads data.cluster.server_url off
// the comparison response) rendered "Unknown" for a connected EKS cluster.
func TestClusterService_GetClusterComparison_PopulatesServerURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/clusters") {
			_, _ = w.Write([]byte(`{"items":[
				{"name":"prod-eks","server":"https://EXAMPLE.gr7.eu-west-1.eks.amazonaws.com","serverVersion":"v1.29.3","info":{"connectionState":{"status":"Successful"}}}
			]}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/applications") {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	ac := argocd.NewClient(srv.URL, "test-token", true)

	svc := NewClusterService("")
	gp := &fakeGP{
		files: map[string][]byte{
			"configuration/managed-clusters.yaml": []byte(
				"clusters:\n  - name: prod-eks\n    labels: {}\n",
			),
			"configuration/addons-catalog.yaml": []byte("applicationsets: []"),
		},
	}

	resp, err := svc.GetClusterComparison(context.Background(), "prod-eks", gp, ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response for known cluster")
	}
	if got := resp.Cluster.ServerURL; got != "https://EXAMPLE.gr7.eu-west-1.eks.amazonaws.com" {
		t.Errorf("GetClusterComparison: ServerURL = %q, want the EKS API-server URL", got)
	}
}

// TestClusterService_GetClusterComparison_InClusterServerURLStaysUnset locks
// down that the hub-local "in-cluster" entry never leaks a server URL into
// the comparison response, mirroring the ListClusters/GetClusterDetail
// behavior for the same special case.
func TestClusterService_GetClusterComparison_InClusterServerURLStaysUnset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/clusters") {
			_, _ = w.Write([]byte(`{"items":[
				{"name":"in-cluster","server":"https://kubernetes.default.svc","info":{"connectionState":{"status":"Successful"}}}
			]}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/applications") {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	ac := argocd.NewClient(srv.URL, "test-token", true)

	svc := NewClusterService("")
	gp := &fakeGP{
		files: map[string][]byte{
			"configuration/managed-clusters.yaml": []byte(
				"clusters:\n  - name: in-cluster\n    labels: {}\n",
			),
			"configuration/addons-catalog.yaml": []byte("applicationsets: []"),
		},
	}

	resp, err := svc.GetClusterComparison(context.Background(), "in-cluster", gp, ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response for known cluster")
	}
	if got := resp.Cluster.ServerURL; got != "" {
		t.Errorf("GetClusterComparison: in-cluster ServerURL = %q, want empty", got)
	}
}

// TestClusterService_GetClusterDetail_UnknownClusterReturnsNil ensures the
// happy path still works — an empty managed-clusters.yaml plus a known
// catalog yields a clean nil response (cluster not found) rather than a
// surprise error.
func TestClusterService_GetClusterDetail_UnknownClusterReturnsNil(t *testing.T) {
	svc := NewClusterService("")
	gp := &fakeGP{
		files: map[string][]byte{
			"configuration/managed-clusters.yaml": []byte("clusters: []"),
			"configuration/addons-catalog.yaml":   []byte("applicationsets: []"),
		},
	}
	resp, err := svc.GetClusterDetail(context.Background(), "ghost", gp, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		t.Errorf("expected nil for unknown cluster, got %+v", resp)
	}
}

// TestClusterService_GetClusterComparison_ForeignAppsNotShownAsAddons is the
// V3-TX-B regression test: a foreign ArgoCD app deployed on an adopted cluster
// (e.g. a user's guestbook/hello-world that is NOT a Sharko-managed addon)
// must NOT appear in the addon_comparisons list at all. The cluster page's
// addon list shows ONLY Sharko-managed addons plus Sharko's own system apps
// (connectivity-check). Foreign apps are neither.
func TestClusterService_GetClusterComparison_ForeignAppsNotShownAsAddons(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/clusters") {
			_, _ = w.Write([]byte(`{"items":[
				{"name":"prod-eks","server":"https://EXAMPLE.gr7.eu-west-1.eks.amazonaws.com","info":{"connectionState":{"status":"Successful"}}}
			]}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/applications") {
			// ArgoCD has 3 apps on this cluster:
			// 1. A Sharko-managed addon (cert-manager) — enabled in Git.
			// 2. Sharko's own connectivity-check app — a system app, not a catalog addon.
			// 3. A FOREIGN app (guestbook) — deployed by the user, NOT managed by Sharko.
			_, _ = w.Write([]byte(`{"items":[
				{"metadata":{"name":"cert-manager-prod-eks"},"spec":{"destination":{"namespace":"cert-manager","server":"https://EXAMPLE.gr7.eu-west-1.eks.amazonaws.com"},"source":{"repoURL":"https://charts.jetstack.io","targetRevision":"v1.11.0"}},"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"}}},
				{"metadata":{"name":"connectivity-check-prod-eks"},"spec":{"destination":{"namespace":"sharko-system","server":"https://EXAMPLE.gr7.eu-west-1.eks.amazonaws.com"},"source":{"repoURL":"https://github.com/example/gitops","targetRevision":"main"}},"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"}}},
				{"metadata":{"name":"guestbook"},"spec":{"destination":{"namespace":"default","server":"https://EXAMPLE.gr7.eu-west-1.eks.amazonaws.com"},"source":{"repoURL":"https://github.com/example/guestbook","targetRevision":"main"}},"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"}}}
			]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	ac := argocd.NewClient(srv.URL, "test-token", true)

	svc := NewClusterService("")
	gp := &fakeGP{
		files: map[string][]byte{
			"configuration/managed-clusters.yaml": []byte(`
clusters:
  - name: prod-eks
    labels:
      cert-manager: enabled
`),
			"configuration/addons-catalog.yaml": []byte(`
applicationsets:
  - name: cert-manager
    chart: cert-manager
    repoURL: https://charts.jetstack.io
    version: v1.11.0
    namespace: cert-manager
`),
		},
	}

	resp, err := svc.GetClusterComparison(context.Background(), "prod-eks", gp, ac)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response for known cluster")
	}

	// Build a map of addon names in the comparison list for easy lookup.
	addonNames := make(map[string]bool)
	for _, comp := range resp.AddonComparisons {
		addonNames[comp.AddonName] = true
	}

	// Verify the managed addon is present.
	if !addonNames["cert-manager"] {
		t.Errorf("expected managed addon cert-manager in addon_comparisons, not found. Got: %+v", addonNames)
	}

	// Verify Sharko's connectivity-check app is present with sharko_system status.
	foundConnCheck := false
	for _, comp := range resp.AddonComparisons {
		if comp.AddonName == "connectivity-check-prod-eks" {
			foundConnCheck = true
			if comp.Status != "sharko_system" {
				t.Errorf("connectivity-check status = %q, want sharko_system", comp.Status)
			}
		}
	}
	if !foundConnCheck {
		t.Errorf("expected Sharko's connectivity-check-prod-eks in addon_comparisons, not found. Got: %+v", addonNames)
	}

	// Verify the foreign app is NOT present.
	if addonNames["guestbook"] {
		t.Errorf("foreign app guestbook must NOT appear in addon_comparisons, but found it. Full list: %+v", resp.AddonComparisons)
	}

	// Verify totalUntracked is 0 (no foreign apps counted).
	if resp.TotalUntrackedInArgocd != 0 {
		t.Errorf("TotalUntrackedInArgocd = %d, want 0 (foreign apps not counted)", resp.TotalUntrackedInArgocd)
	}
}

// TestClusterService_ListClusters_V4Repo_ReadsFleetConnectionsFallback is
// the v4 Wave 1 Story 4.4 regression guard: when the v3 path
// (configuration/managed-clusters.yaml) is genuinely absent, ListClusters
// must fall back to the v4 path (orchestrator.V4ManagedClustersPath,
// "managed-clusters.yaml" — design doc §2.4, same ManagedClusters shape)
// so a cluster registered on a v4 repo appears on the dashboard.
func TestClusterService_ListClusters_V4Repo_ReadsFleetConnectionsFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/clusters") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)

	ac := argocd.NewClient(srv.URL, "test-token", true)
	svc := NewClusterService("")
	gp := &fakeGP{
		files: map[string][]byte{
			// v3 configuration/managed-clusters.yaml is intentionally
			// absent — falls through to fakeGP's default ErrFileNotFound.
			orchestrator.V4ManagedClustersPath: []byte(`apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: connections
spec:
  clusters:
    - name: prod-eu
`),
		},
	}

	resp, err := svc.ListClusters(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("ListClusters returned error: %v", err)
	}
	if len(resp.Clusters) != 1 || resp.Clusters[0].Name != "prod-eu" {
		t.Fatalf("expected the v4-registered cluster prod-eu to appear, got %+v", resp.Clusters)
	}
	if !resp.Clusters[0].Managed {
		t.Error("expected the v4-registered cluster to be marked Managed=true")
	}
}

// TestClusterService_ListClusters_V3PathPresent_NeverTriesV4Fallback
// proves the fallback is truly a fallback: when the v3 path resolves, a
// stray managed-clusters.yaml (e.g. mid-migration) must never leak into
// the v3 desired state.
func TestClusterService_ListClusters_V3PathPresent_NeverTriesV4Fallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/clusters") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)

	ac := argocd.NewClient(srv.URL, "test-token", true)
	svc := NewClusterService("")
	gp := &fakeGP{
		files: map[string][]byte{
			"configuration/managed-clusters.yaml": []byte("clusters:\n  - name: v3-cluster\n"),
			orchestrator.V4ManagedClustersPath:    []byte("clusters:\n  - name: v4-cluster-should-be-ignored\n"),
		},
	}

	resp, err := svc.ListClusters(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("ListClusters returned error: %v", err)
	}
	if len(resp.Clusters) != 1 || resp.Clusters[0].Name != "v3-cluster" {
		t.Fatalf("expected only v3-cluster, got %+v", resp.Clusters)
	}
}

// TestClusterService_ListClusters_HonorsBaseBranch is the Wave 2 review
// "BaseBranch hardcode sweep" regression test: ClusterService.ListClusters
// (via readManagedClustersData) must read from the connection's configured
// GitOps base branch — wired via SetBaseBranchFn — instead of a hardcoded
// "main". Uses refRecordingGitProvider (defined in addon_test.go, same
// package) which records every ref (branch) GetFileContent/ListDirectory
// was asked to read from, so a regression to a literal "main" fails this
// test even though a same-content-on-every-branch fake would otherwise mask
// the bug.
func TestClusterService_ListClusters_HonorsBaseBranch(t *testing.T) {
	const configuredBranch = "release"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/clusters") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)

	ac := argocd.NewClient(srv.URL, "test-token", true)
	svc := NewClusterService("")
	svc.SetBaseBranchFn(func() string { return configuredBranch })
	gp := newRefRecordingGitProvider(map[string][]byte{
		"configuration/managed-clusters.yaml": []byte("clusters:\n  - name: prod-eu\n"),
	})

	resp, err := svc.ListClusters(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("ListClusters returned error: %v", err)
	}
	if len(resp.Clusters) != 1 || resp.Clusters[0].Name != "prod-eu" {
		t.Fatalf("expected [prod-eu], got %+v — base-branch wiring broke the read", resp.Clusters)
	}

	if gp.refs["main"] {
		t.Errorf("ListClusters read from hardcoded %q even though the connection's base branch is configured as %q — refs seen: %v", "main", configuredBranch, gp.refs)
	}
	if !gp.refs[configuredBranch] {
		t.Errorf("expected ListClusters to read from the configured base branch %q, refs seen: %v", configuredBranch, gp.refs)
	}
}

// ---------------------------------------------------------------------------
// S1 (walk day 5 root cause) — ClusterService reads the v4 repo layout
// (catalog.yaml + cluster-addons/<name>.yaml) instead of silently falling
// back to an empty v3 catalog. Live evidence before this fix: a v4 repo's
// GET /clusters/{name}/comparison returned git_total_addons:0 and empty
// addon_comparisons FOREVER, and GET /clusters/{name} returned addons: [],
// even though the cluster had a real, merged addon assignment. The
// version-matrix endpoint (AddonService.GetVersionMatrix) already had this
// v4 dispatch; ClusterService never did.
// ---------------------------------------------------------------------------

// buildV4Fixture returns the three files a v4 repo carries that
// ClusterService must now read: the engine pin (routes every read here to
// the v4 branch), the org's approved catalog.yaml (one addon,
// cert-manager), and one cluster's cluster-addons/<name>.yaml assignment
// (cert-manager enabled with a per-cluster version pin). Shared by the
// GetClusterDetail, GetClusterComparison and ListClusters v4 tests below so
// the fixture can't quietly drift between them.
func buildV4Fixture(t *testing.T, clusterName string) map[string][]byte {
	t.Helper()

	catalogYAML, err := config.SaveAddonCatalog(config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"cert-manager": {
				RepoURL:   "https://charts.jetstack.io",
				Chart:     "cert-manager",
				Version:   "1.14.5",
				Namespace: "cert-manager",
			},
		},
	})
	if err != nil {
		t.Fatalf("building catalog.yaml fixture: %v", err)
	}

	clusterAddonsYAML, err := models.SaveClusterAddons(models.ClusterAddonsSpec{
		Cluster: clusterName,
		Addons: map[string]models.ClusterAddonsAddon{
			"cert-manager": {Enabled: true, Version: "1.12.0"}, // per-cluster pin
		},
	})
	if err != nil {
		t.Fatalf("building cluster-addons fixture: %v", err)
	}

	return map[string][]byte{
		orchestrator.EnginePinPath: []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
		"configuration/managed-clusters.yaml": []byte(
			"clusters:\n  - name: " + clusterName + "\n    labels: {}\n",
		),
		config.AddonCatalogPath:                   catalogYAML,
		"cluster-addons/" + clusterName + ".yaml": clusterAddonsYAML,
	}
}

// TestClusterService_GetClusterDetail_V4Repo is the S1 regression guard for
// GetClusterDetail: on a v4 repo the addon list must be built from
// catalog.yaml + cluster-addons/<name>.yaml, not the v3
// configuration/addons-catalog.yaml (which a v4 repo never has — the old
// code's silent isGitFileNotFound fallback to an empty catalog is exactly
// what produced "addons: []" forever).
func TestClusterService_GetClusterDetail_V4Repo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/clusters") {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/applications") {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	ac := argocd.NewClient(srv.URL, "test-token", true)

	svc := NewClusterService("")
	gp := &fakeGP{files: buildV4Fixture(t, "prod-eu")}

	resp, err := svc.GetClusterDetail(context.Background(), "prod-eu", gp, ac)
	if err != nil {
		t.Fatalf("GetClusterDetail returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response for a v4-registered cluster")
	}
	if len(resp.Addons) != 1 {
		t.Fatalf("expected 1 addon (cert-manager) from the v4 layout, got %d: %+v — the v3 fallback is still being read", len(resp.Addons), resp.Addons)
	}
	addon := resp.Addons[0]
	if addon.AddonName != "cert-manager" {
		t.Errorf("AddonName = %q, want cert-manager", addon.AddonName)
	}
	if addon.Chart != "cert-manager" || addon.RepoURL != "https://charts.jetstack.io" || addon.Namespace != "cert-manager" {
		t.Errorf("addon deployment fields not carried from catalog.yaml: %+v", addon)
	}
	if addon.CurrentVersion != "1.12.0" {
		t.Errorf("CurrentVersion = %q, want the per-cluster pin 1.12.0", addon.CurrentVersion)
	}
	if addon.EnvironmentVersion != "1.14.5" {
		t.Errorf("EnvironmentVersion = %q, want the catalog default 1.14.5", addon.EnvironmentVersion)
	}
	if !addon.HasVersionOverride {
		t.Error("expected HasVersionOverride=true — cert-manager has a per-cluster pin")
	}
	if resp.Cluster.Labels["cert-manager"] != "enabled" {
		t.Errorf(`Cluster.Labels["cert-manager"] = %q, want "enabled" (S1: labels must be synthesized on a v4 cluster)`, resp.Cluster.Labels["cert-manager"])
	}
}

// TestClusterService_GetClusterComparison_V4Repo is the S1 regression
// guard for GetClusterComparison: this is the exact endpoint the walk-day
// investigation found returning git_total_addons:0 permanently on a v4
// repo.
func TestClusterService_GetClusterComparison_V4Repo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/clusters") {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/applications") {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	ac := argocd.NewClient(srv.URL, "test-token", true)

	svc := NewClusterService("")
	gp := &fakeGP{files: buildV4Fixture(t, "prod-eu")}

	resp, err := svc.GetClusterComparison(context.Background(), "prod-eu", gp, ac)
	if err != nil {
		t.Fatalf("GetClusterComparison returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response for a v4-registered cluster")
	}
	if resp.GitTotalAddons != 1 {
		t.Fatalf("GitTotalAddons = %d, want 1 — this is the walk-day-5 bug: git_total_addons:0 forever on a v4 repo", resp.GitTotalAddons)
	}
	if len(resp.AddonComparisons) != 1 {
		t.Fatalf("expected 1 addon comparison, got %d: %+v", len(resp.AddonComparisons), resp.AddonComparisons)
	}
	comp := resp.AddonComparisons[0]
	if comp.AddonName != "cert-manager" {
		t.Errorf("AddonName = %q, want cert-manager", comp.AddonName)
	}
	if comp.GitVersion != "1.12.0" {
		t.Errorf("GitVersion = %q, want the per-cluster pin 1.12.0", comp.GitVersion)
	}
	if comp.GitChart != "cert-manager" || comp.GitRepoURL != "https://charts.jetstack.io" {
		t.Errorf("Git deployment fields not carried from catalog.yaml: %+v", comp)
	}
	if comp.Status != "missing_in_argocd" {
		t.Errorf("Status = %q, want missing_in_argocd (no matching ArgoCD app in this fixture)", comp.Status)
	}
}

// TestClusterService_ListClusters_V4Repo_SynthesizesLabelsForAddonCount is
// the S1 regression guard for the clusters list surface: the UI derives
// each cluster's addon count client-side from
// `Object.values(cluster.labels).filter(v => v === 'enabled').length`
// (ui/src/views/ClustersOverview.tsx). A v4 cluster's managed-clusters.yaml
// entry never carries addon labels (enablement lives in
// cluster-addons/<name>.yaml instead), so before this fix every v4 cluster
// showed labels: {} and a permanent 0-addon count.
func TestClusterService_ListClusters_V4Repo_SynthesizesLabelsForAddonCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v1/clusters") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)
	ac := argocd.NewClient(srv.URL, "test-token", true)

	svc := NewClusterService("")
	gp := newRefRecordingGitProvider(buildV4Fixture(t, "prod-eu"))

	resp, err := svc.ListClusters(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("ListClusters returned error: %v", err)
	}
	if len(resp.Clusters) != 1 || resp.Clusters[0].Name != "prod-eu" {
		t.Fatalf("expected [prod-eu], got %+v", resp.Clusters)
	}
	if resp.Clusters[0].Labels["cert-manager"] != "enabled" {
		t.Errorf(`Labels["cert-manager"] = %q, want "enabled" — labels: {} is the walk-day-5 bug`, resp.Clusters[0].Labels["cert-manager"])
	}
}

// TestClusterService_V3Repo_UnaffectedByV4Files proves the v3 code path is
// untouched: even with a cluster-addons/<name>.yaml file sitting in the
// repo (e.g. mid-migration leftovers), GetClusterDetail and
// GetClusterComparison must ignore it entirely when the engine pin is
// absent — the probe, not file presence, decides.
func TestClusterService_V3Repo_UnaffectedByV4Files(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/clusters") {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/applications") {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	ac := argocd.NewClient(srv.URL, "test-token", true)

	svc := NewClusterService("")
	v4Files := buildV4Fixture(t, "prod-eks")
	delete(v4Files, orchestrator.EnginePinPath) // no engine pin: this is a v3 repo
	v4Files["configuration/managed-clusters.yaml"] = []byte("clusters:\n  - name: prod-eks\n    labels:\n      cert-manager: enabled\n")
	v4Files["configuration/addons-catalog.yaml"] = []byte(
		"applicationsets:\n  - name: cert-manager\n    repoURL: https://charts.jetstack.io\n    chart: cert-manager\n    version: 1.9.0\n",
	)

	resp, err := svc.GetClusterDetail(context.Background(), "prod-eks", &fakeGP{files: v4Files}, ac)
	if err != nil {
		t.Fatalf("GetClusterDetail returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if len(resp.Addons) != 1 || resp.Addons[0].CurrentVersion != "1.9.0" {
		t.Fatalf("expected the v3 addons-catalog.yaml version (1.9.0), got %+v — the v4 cluster-addons file must be ignored without the engine pin", resp.Addons)
	}
}

// TestClusterService_GetClusterDetail_WarnsOnMissingCatalog and
// TestClusterService_GetClusterComparison_WarnsOnMissingCatalog lock down
// the walk-day-5 "zero log lines in a 40-minute window" finding: the v3
// silent isGitFileNotFound fallback to an empty catalog must now emit a
// WARN log naming the missing file and the branch read, so the next
// investigation isn't blind.
func TestClusterService_GetClusterDetail_WarnsOnMissingCatalog(t *testing.T) {
	var buf bytes.Buffer
	originalLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(originalLogger) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/clusters") {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/applications") {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	ac := argocd.NewClient(srv.URL, "test-token", true)

	svc := NewClusterService("")
	gp := &fakeGP{
		files: map[string][]byte{
			"configuration/managed-clusters.yaml": []byte("clusters:\n  - name: prod-eks\n    labels: {}\n"),
			// configuration/addons-catalog.yaml intentionally absent.
		},
	}

	if _, err := svc.GetClusterDetail(context.Background(), "prod-eks", gp, ac); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logOut := buf.String()
	if !strings.Contains(logOut, "addons-catalog.yaml not found") {
		t.Errorf("expected a WARN log naming the missing file, got:\n%s", logOut)
	}
	if !strings.Contains(logOut, `"path":"configuration/addons-catalog.yaml"`) {
		t.Errorf("expected the WARN log to name the missing path, got:\n%s", logOut)
	}
	if !strings.Contains(logOut, `"branch":"main"`) {
		t.Errorf("expected the WARN log to name the branch read, got:\n%s", logOut)
	}
}

func TestClusterService_GetClusterComparison_WarnsOnMissingCatalog(t *testing.T) {
	var buf bytes.Buffer
	originalLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(originalLogger) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/clusters") {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/applications") {
			_, _ = w.Write([]byte(`{"items":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	ac := argocd.NewClient(srv.URL, "test-token", true)

	svc := NewClusterService("")
	gp := &fakeGP{
		files: map[string][]byte{
			"configuration/managed-clusters.yaml": []byte("clusters:\n  - name: prod-eks\n    labels: {}\n"),
			// configuration/addons-catalog.yaml intentionally absent.
		},
	}

	if _, err := svc.GetClusterComparison(context.Background(), "prod-eks", gp, ac); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	logOut := buf.String()
	if !strings.Contains(logOut, "addons-catalog.yaml not found") {
		t.Errorf("expected a WARN log naming the missing file, got:\n%s", logOut)
	}
	if !strings.Contains(logOut, `"path":"configuration/addons-catalog.yaml"`) {
		t.Errorf("expected the WARN log to name the missing path, got:\n%s", logOut)
	}
	if !strings.Contains(logOut, `"branch":"main"`) {
		t.Errorf("expected the WARN log to name the branch read, got:\n%s", logOut)
	}
}

// ---------------------------------------------------------------------------
// v4 closing wave, final fix row: GetConfigDiff and GetClusterValues never
// got a v4 branch. On a v4 repo both read
// configuration/addons-clusters-values/<cluster>.yaml unconditionally,
// which never exists on a v4 repo, so both 500'd for every cluster. This
// mirrors the v4-branch tests above for GetClusterDetail/GetClusterComparison.
// ---------------------------------------------------------------------------

// TestClusterService_GetConfigDiff_V3AndV4 is table-driven across both repo
// layouts: v3's single combined
// configuration/addons-clusters-values/<cluster>.yaml (one key per addon,
// plus the clusterGlobalValues scratch block) versus v4's one file per
// addon at values/clusters/<cluster>/<addon>.yaml, diffed against
// values/global/<addon>.yaml (design doc §2.2). Both cases exercise the
// same two addons — one with a real override, one whose cluster file has
// no matching global defaults file at all — so the HasOverrides logic is
// proven identically on both layouts.
func TestClusterService_GetConfigDiff_V3AndV4(t *testing.T) {
	t.Run("v3", func(t *testing.T) {
		svc := NewClusterService("")
		gp := &fakeGP{files: map[string][]byte{
			"configuration/addons-clusters-values/prod-eu.yaml": []byte(
				"clusterGlobalValues:\n  foo: bar\ncert-manager:\n  replicaCount: 3\nmetrics-server:\n  args:\n    - --foo\n",
			),
			"configuration/addons-global-values/cert-manager.yaml": []byte("replicaCount: 2\n"),
			// metrics-server has no global defaults file — GlobalValues
			// stays "" and HasOverrides is still true (non-empty cluster
			// values vs empty global).
		}}

		resp, err := svc.GetConfigDiff(context.Background(), "prod-eu", gp)
		if err != nil {
			t.Fatalf("GetConfigDiff returned error: %v", err)
		}
		if resp.ClusterName != "prod-eu" {
			t.Errorf("ClusterName = %q, want prod-eu", resp.ClusterName)
		}
		if got, want := resp.GlobalValues["foo"], "bar"; got != want {
			t.Errorf("GlobalValues[foo] = %v, want %v (from clusterGlobalValues)", got, want)
		}
		if len(resp.AddonDiffs) != 2 {
			t.Fatalf("expected 2 addon diffs, got %d: %+v", len(resp.AddonDiffs), resp.AddonDiffs)
		}
		// Sorted by addon name.
		cm, ms := resp.AddonDiffs[0], resp.AddonDiffs[1]
		if cm.AddonName != "cert-manager" || ms.AddonName != "metrics-server" {
			t.Fatalf("expected sorted [cert-manager, metrics-server], got [%s, %s]", cm.AddonName, ms.AddonName)
		}
		if !cm.HasOverrides {
			t.Error("cert-manager: expected HasOverrides=true (replicaCount 3 vs global 2)")
		}
		if strings.TrimSpace(cm.GlobalValues) != "replicaCount: 2" {
			t.Errorf("cert-manager GlobalValues = %q", cm.GlobalValues)
		}
		if !ms.HasOverrides {
			t.Error("metrics-server: expected HasOverrides=true (has cluster values, no global file)")
		}
		if ms.GlobalValues != "" {
			t.Errorf("metrics-server GlobalValues = %q, want empty (no global defaults file)", ms.GlobalValues)
		}
	})

	t.Run("v4", func(t *testing.T) {
		svc := NewClusterService("")
		gp := &fakeGP{files: map[string][]byte{
			orchestrator.EnginePinPath:                    []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
			"values/clusters/prod-eu/cert-manager.yaml":   []byte("replicaCount: 3\n"),
			"values/global/cert-manager.yaml":             []byte("replicaCount: 2\n"),
			"values/clusters/prod-eu/metrics-server.yaml": []byte("args:\n  - --foo\n"),
			// values/global/metrics-server.yaml intentionally absent.
		}}

		resp, err := svc.GetConfigDiff(context.Background(), "prod-eu", gp)
		if err != nil {
			t.Fatalf("GetConfigDiff returned error: %v", err)
		}
		if resp.ClusterName != "prod-eu" {
			t.Errorf("ClusterName = %q, want prod-eu", resp.ClusterName)
		}
		if resp.GlobalValues != nil {
			t.Errorf("GlobalValues = %+v, want nil — v4 never carries the v3 clusterGlobalValues scratch block", resp.GlobalValues)
		}
		if len(resp.AddonDiffs) != 2 {
			t.Fatalf("expected 2 addon diffs, got %d: %+v", len(resp.AddonDiffs), resp.AddonDiffs)
		}
		cm, ms := resp.AddonDiffs[0], resp.AddonDiffs[1]
		if cm.AddonName != "cert-manager" || ms.AddonName != "metrics-server" {
			t.Fatalf("expected sorted [cert-manager, metrics-server], got [%s, %s]", cm.AddonName, ms.AddonName)
		}
		if !cm.HasOverrides {
			t.Error("cert-manager: expected HasOverrides=true (replicaCount 3 vs global 2)")
		}
		if strings.TrimSpace(cm.GlobalValues) != "replicaCount: 2" {
			t.Errorf("cert-manager GlobalValues = %q", cm.GlobalValues)
		}
		if strings.TrimSpace(cm.ClusterValues) != "replicaCount: 3" {
			t.Errorf("cert-manager ClusterValues = %q", cm.ClusterValues)
		}
		if !ms.HasOverrides {
			t.Error("metrics-server: expected HasOverrides=true (has cluster values, no global file)")
		}
		if ms.GlobalValues != "" {
			t.Errorf("metrics-server GlobalValues = %q, want empty (no values/global/metrics-server.yaml)", ms.GlobalValues)
		}
	})

	t.Run("v4_no_overrides_yet_is_empty_not_error", func(t *testing.T) {
		// A cluster registered but with no per-addon override files yet —
		// values/clusters/<cluster>/ does not exist. Design doc D16:
		// "missing means empty", never an error.
		svc := NewClusterService("")
		gp := &fakeGP{files: map[string][]byte{
			orchestrator.EnginePinPath: []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
		}}

		resp, err := svc.GetConfigDiff(context.Background(), "prod-eu", gp)
		if err != nil {
			t.Fatalf("GetConfigDiff returned error: %v", err)
		}
		if len(resp.AddonDiffs) != 0 {
			t.Errorf("expected 0 addon diffs for a cluster with no override files, got %+v", resp.AddonDiffs)
		}
	})
}

// TestClusterService_GetClusterValues_V3AndV4 is table-driven across both
// repo layouts. v3 returns the combined per-cluster file's raw bytes
// unchanged; v4 combines every values/clusters/<cluster>/<addon>.yaml file
// (design doc §2.2) back into one document keyed by addon name, so the
// response shape (one YAML string, one top-level key per addon) is the
// same for either layout.
func TestClusterService_GetClusterValues_V3AndV4(t *testing.T) {
	t.Run("v3", func(t *testing.T) {
		svc := NewClusterService("")
		raw := "cert-manager:\n  replicaCount: 3\nmetrics-server:\n  args:\n    - --foo\n"
		gp := &fakeGP{files: map[string][]byte{
			"configuration/addons-clusters-values/prod-eu.yaml": []byte(raw),
		}}

		resp, err := svc.GetClusterValues(context.Background(), "prod-eu", gp)
		if err != nil {
			t.Fatalf("GetClusterValues returned error: %v", err)
		}
		if resp.ClusterName != "prod-eu" {
			t.Errorf("ClusterName = %q, want prod-eu", resp.ClusterName)
		}
		if resp.ValuesYAML != raw {
			t.Errorf("ValuesYAML = %q, want the raw file content %q unchanged", resp.ValuesYAML, raw)
		}
	})

	t.Run("v3_missing_file_is_an_error", func(t *testing.T) {
		// Unchanged v3 behavior: GetClusterValues has always hard-errored
		// on a missing combined file, unlike GetConfigDiff/ListClusters'
		// "missing means empty" treatment elsewhere in this file. This
		// case pins that existing behavior so the v4 branch added
		// alongside it cannot accidentally change it.
		svc := NewClusterService("")
		gp := &fakeGP{files: map[string][]byte{}}

		if _, err := svc.GetClusterValues(context.Background(), "prod-eu", gp); err == nil {
			t.Fatal("expected an error for a missing v3 cluster values file")
		}
	})

	t.Run("v4", func(t *testing.T) {
		svc := NewClusterService("")
		gp := &fakeGP{files: map[string][]byte{
			orchestrator.EnginePinPath:                    []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
			"values/clusters/prod-eu/cert-manager.yaml":   []byte("replicaCount: 3\n"),
			"values/clusters/prod-eu/metrics-server.yaml": []byte("args:\n  - --foo\n"),
		}}

		resp, err := svc.GetClusterValues(context.Background(), "prod-eu", gp)
		if err != nil {
			t.Fatalf("GetClusterValues returned error: %v", err)
		}
		if resp.ClusterName != "prod-eu" {
			t.Errorf("ClusterName = %q, want prod-eu", resp.ClusterName)
		}

		// Parse the combined YAML back rather than comparing strings —
		// yaml.Marshal's key order is an implementation detail, the
		// combined *shape* (one top-level key per addon) is the contract.
		var combined map[string]interface{}
		if err := yaml.Unmarshal([]byte(resp.ValuesYAML), &combined); err != nil {
			t.Fatalf("ValuesYAML did not parse as YAML: %v\n%s", err, resp.ValuesYAML)
		}
		cm, ok := combined["cert-manager"].(map[string]interface{})
		if !ok {
			t.Fatalf("combined[cert-manager] = %#v, want a map", combined["cert-manager"])
		}
		if cm["replicaCount"] != 3 {
			t.Errorf("combined[cert-manager][replicaCount] = %v, want 3", cm["replicaCount"])
		}
		if _, ok := combined["metrics-server"]; !ok {
			t.Errorf("combined has no metrics-server key: %#v", combined)
		}
	})

	t.Run("v4_no_overrides_yet_is_empty_not_error", func(t *testing.T) {
		// No values/clusters/<cluster>/ directory at all — a cluster with
		// no per-addon overrides yet. Design doc D16: "missing means
		// empty", never an error, and (unlike v3's hard error above) v4
		// has a real "nothing here" state to fall back to instead of one
		// combined file that either exists or doesn't.
		svc := NewClusterService("")
		gp := &fakeGP{files: map[string][]byte{
			orchestrator.EnginePinPath: []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
		}}

		resp, err := svc.GetClusterValues(context.Background(), "prod-eu", gp)
		if err != nil {
			t.Fatalf("GetClusterValues returned error: %v", err)
		}
		if resp.ValuesYAML != "" {
			t.Errorf("ValuesYAML = %q, want empty for a cluster with no override files", resp.ValuesYAML)
		}
	})
}
