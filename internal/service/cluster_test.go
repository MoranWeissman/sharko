package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
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

func (f *fakeGP) ListDirectory(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}
func (f *fakeGP) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}
func (f *fakeGP) TestConnection(_ context.Context) error                            { return nil }
func (f *fakeGP) CreateBranch(_ context.Context, _, _ string) error                 { return nil }
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
func (f *fakeGP) MergePullRequest(_ context.Context, _ int) error            { return nil }
func (f *fakeGP) GetPullRequestStatus(_ context.Context, _ int) (string, error) { return "", nil }
func (f *fakeGP) DeleteBranch(_ context.Context, _ string) error             { return nil }

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
