package api

import (
	"context"
	"errors"
	"testing"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// V125-1.5 / BUG-053 — pending-registration resolver behaviour.
//
// The three contracts pinned here mirror the bug list from the maintainer's
// V125-1.4 Track-B reproducer: pending registration PRs are visible in the
// API response, are NEVER nil, and a Git-provider blip never takes down the
// whole /clusters endpoint.

// fakeGP is a tiny gitprovider.GitProvider used by these tests. It only
// implements ListPullRequests; the other methods are no-ops because the
// resolver does not call them. We keep it local to the file so it never
// drifts from what the resolver actually exercises.
type fakeGP struct {
	prs    []gitprovider.PullRequest
	prsErr error
}

func (f *fakeGP) GetFileContent(_ context.Context, _, _ string) ([]byte, error) {
	return nil, nil
}
func (f *fakeGP) ListDirectory(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}
func (f *fakeGP) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return f.prs, f.prsErr
}
func (f *fakeGP) TestConnection(_ context.Context) error                          { return nil }
func (f *fakeGP) CreateBranch(_ context.Context, _, _ string) error               { return nil }
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
func (f *fakeGP) MergePullRequest(_ context.Context, _ int) error                  { return nil }
func (f *fakeGP) GetPullRequestStatus(_ context.Context, _ int) (string, error)    { return "", nil }
func (f *fakeGP) DeleteBranch(_ context.Context, _ string) error                   { return nil }

// Compile-time assertion the fake satisfies the full interface — if a future
// gitprovider method is added, this test file fails to compile rather than
// silently leaving fakeGP behind. (Same discipline as fakeGP in
// internal/service/cluster_test.go, review finding L3.)
var _ gitprovider.GitProvider = (*fakeGP)(nil)

func TestResolvePendingRegistrations_MatchesRegisterPRs(t *testing.T) {
	gp := &fakeGP{
		prs: []gitprovider.PullRequest{
			{
				ID:           42,
				Title:        "sharko: register cluster prod-eu",
				SourceBranch: "sharko/register-cluster-prod-eu-abcd1234",
				URL:          "https://github.com/org/repo/pull/42",
				CreatedAt:    "2026-05-01T12:00:00Z",
			},
			{
				ID:           43,
				Title:        "sharko: register cluster kind-local (kubeconfig provider)",
				SourceBranch: "sharko/register-cluster-kind-local-efef5678",
				URL:          "https://github.com/org/repo/pull/43",
				CreatedAt:    "2026-05-02T08:30:00Z",
			},
			{
				// Unrelated PR — must be excluded.
				ID:    99,
				Title: "sharko: remove cluster staging",
				URL:   "https://github.com/org/repo/pull/99",
			},
		},
	}

	got := resolvePendingRegistrations(context.Background(), gp, "sharko:")

	if got == nil {
		t.Fatal("expected non-nil slice (V125-1.4 nil-array regression guard)")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 pending registrations, got %d: %+v", len(got), got)
	}
	if got[0].ClusterName != "prod-eu" {
		t.Errorf("first cluster_name = %q, want %q", got[0].ClusterName, "prod-eu")
	}
	if got[0].PRURL != "https://github.com/org/repo/pull/42" {
		t.Errorf("first pr_url = %q", got[0].PRURL)
	}
	if got[0].Branch != "sharko/register-cluster-prod-eu-abcd1234" {
		t.Errorf("first branch = %q", got[0].Branch)
	}
	if got[0].OpenedAt != "2026-05-01T12:00:00Z" {
		t.Errorf("first opened_at = %q", got[0].OpenedAt)
	}
	if got[1].ClusterName != "kind-local" {
		t.Errorf("second cluster_name = %q, want %q (kubeconfig-suffix stripped)",
			got[1].ClusterName, "kind-local")
	}
}

func TestResolvePendingRegistrations_NoOpenPRsReturnsEmptyNotNil(t *testing.T) {
	gp := &fakeGP{prs: nil}

	got := resolvePendingRegistrations(context.Background(), gp, "sharko:")
	if got == nil {
		t.Fatal("expected non-nil empty slice (V125-1.4 nil-array regression guard)")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 pending registrations, got %d: %+v", len(got), got)
	}
}

func TestResolvePendingRegistrations_ProviderErrorDegradesToEmpty(t *testing.T) {
	// V124-22 dignified-degrade pattern: a transient rate-limit or auth
	// blip in ListPullRequests must NOT 500 the entire /clusters
	// endpoint — the resolver swallows the error, logs a warning, and
	// returns the same non-nil empty slice it would on the happy
	// no-open-PRs path. The /clusters handler treats both as "nothing
	// pending right now".
	gp := &fakeGP{prsErr: errors.New("rate limited by github (transient)")}

	got := resolvePendingRegistrations(context.Background(), gp, "sharko:")
	if got == nil {
		t.Fatal("expected non-nil empty slice on provider error")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 pending registrations on provider error, got %d", len(got))
	}
}

func TestResolvePendingRegistrations_NilProviderReturnsEmpty(t *testing.T) {
	// Defensive: if SetOrchestrator hasn't run yet (no active connection),
	// gp can be nil. The resolver must not crash.
	got := resolvePendingRegistrations(context.Background(), nil, "sharko:")
	if got == nil {
		t.Fatal("expected non-nil empty slice on nil provider")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 pending registrations on nil provider, got %d", len(got))
	}
}

func TestResolvePendingRegistrations_RespectsCommitPrefix(t *testing.T) {
	// The commit prefix is operator-configurable. A PR opened with a
	// different prefix must not be mis-matched.
	gp := &fakeGP{
		prs: []gitprovider.PullRequest{
			{Title: "sharko: register cluster a"},      // matches "sharko:"
			{Title: "[acme] register cluster b"},       // does not match "sharko:"
			{Title: "[acme] register cluster c (kubeconfig provider)"},
		},
	}
	got := resolvePendingRegistrations(context.Background(), gp, "sharko:")
	if len(got) != 1 || got[0].ClusterName != "a" {
		t.Errorf("expected exactly cluster 'a' for prefix 'sharko:', got %+v", got)
	}

	gotAcme := resolvePendingRegistrations(context.Background(), gp, "[acme]")
	if len(gotAcme) != 2 {
		t.Fatalf("expected 2 matches for prefix '[acme]', got %d: %+v", len(gotAcme), gotAcme)
	}
	if gotAcme[0].ClusterName != "b" || gotAcme[1].ClusterName != "c" {
		t.Errorf("expected clusters [b, c] for prefix '[acme]', got %+v", gotAcme)
	}
}
