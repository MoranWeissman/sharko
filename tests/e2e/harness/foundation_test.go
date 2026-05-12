//go:build e2e

package harness

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
)

// TestFoundationStack proves the V2 Epic 7-1.3 contracts work end-to-end
// without kind / docker / argocd. Exercises:
//
//   - StartGitFake (7-1.2) — for completeness, even though sharko in
//     this in-process configuration writes through the GH mock, not git.
//   - StartGitMock (7-1.3) — installed as the active git provider via
//     SharkoConfig.GitProvider.
//   - StartSharko (7-1.2) — boots in-process with the mock injected.
//   - SeedUsers (7-1.3) — creates an editor + viewer for RBAC fixtures.
//   - NewClient / NewClientAs (7-1.3) — typed API client + auth helpers.
//   - 401-retry-once (7-1.3) — exercised by nuking the token and calling
//     a typed wrapper.
//   - Eventually (7-1.3) — sanity check the polling helper.
//
// Should PASS in seconds when run as `go test -tags=e2e -run
// TestFoundationStack ./tests/e2e/harness/...`.
func TestFoundationStack(t *testing.T) {
	git := StartGitFake(t)
	mock := StartGitMock(t)
	sharko := StartSharko(t, SharkoConfig{
		Mode:        SharkoModeInProcess,
		GitFake:     git,
		GitProvider: mock,
	})
	sharko.WaitHealthy(t, 10*time.Second)

	// ---- Typed API client (admin) ----------------------------------------
	admin := NewClient(t, sharko)

	health := admin.Health(t)
	if health.Status != "healthy" {
		t.Fatalf("admin.Health: status=%q want %q", health.Status, "healthy")
	}
	if health.Version == "" {
		t.Fatalf("admin.Health: empty version")
	}
	t.Logf("foundation: health.Version=%s health.Mode=%s", health.Version, health.Mode)

	// ---- Seed RBAC fixtures + non-admin client ---------------------------
	users := DefaultTestUsers()
	SeedUsers(t, sharko, users)

	// Locate the viewer credentials so we can exercise NewClientAs.
	var viewer TestUser
	for _, u := range users {
		if u.Role == "viewer" {
			viewer = u
			break
		}
	}
	if viewer.Username == "" {
		t.Fatal("DefaultTestUsers: missing viewer entry")
	}
	viewerClient := NewClientAs(t, sharko, viewer.Username, viewer.Password)
	// Viewer can hit /health (no auth required) but more importantly the
	// login flow worked without t.Fatalf — proving SeedUsers + NewClientAs
	// compose correctly.
	if got := viewerClient.Health(t); got.Status != "healthy" {
		t.Fatalf("viewer.Health: status=%q want %q", got.Status, "healthy")
	}

	// ---- 401-retry-once smoke test ---------------------------------------
	// Nuke the token, then call any authenticated endpoint. The first
	// request gets a 401, the helper re-logs in transparently, the second
	// request succeeds.
	admin.SetToken("definitely-not-a-real-token")
	// Use the lower-level Do to confirm we get a 401 if WithNoRetry is
	// in effect, then a typed helper to confirm the retry path heals.
	resp := admin.Do(t, http.MethodGet, "/api/v1/users", nil, WithNoRetry())
	if resp.StatusCode != http.StatusUnauthorized {
		resp.Body.Close()
		t.Fatalf("expected 401 on bad-token + WithNoRetry, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Reset token to the bad value again so the typed helper exercises retry.
	admin.SetToken("definitely-not-a-real-token-still")
	_ = admin.ListUsers(t) // would fail if 401-retry didn't heal

	// ---- GitProvider injection — verified at the mock surface -----------
	// We do NOT call admin.ListClusters here because GET /api/v1/clusters
	// also resolves an ArgoCD client (no connection → 503), which is out
	// of scope for the GH-mock contract. Downstream story 7-1.4 layers an
	// argocd fake on top and exercises the typed wrapper end-to-end.
	//
	// The mock-only smoke test below proves CreateBranch +
	// BatchCreateFiles + CreatePullRequest + MergePullRequest all work
	// against the in-memory state — that's the contract sharko handlers
	// rely on once the test wires both fakes.
	if err := mock.CreateOrUpdateFile(context.Background(),
		"configuration/managed-clusters.yaml",
		[]byte("clusters: []\n"),
		"main", "init"); err != nil {
		t.Fatalf("mock.CreateOrUpdateFile: %v", err)
	}
	if got := mock.FileAt("main", "configuration/managed-clusters.yaml"); got != "clusters: []\n" {
		t.Fatalf("mock.FileAt managed-clusters.yaml: got %q", got)
	}

	// ---- Mock-only smoke test --------------------------------------------
	// Drive a CreateBranch + BatchCreateFiles + CreatePullRequest +
	// MergePullRequest cycle directly against the mock so downstream
	// stories know the surface works without sharko in the picture.
	ctx := context.Background()
	if err := mock.CreateBranch(ctx, "sharko/foundation-test-7-1-3", "main"); err != nil {
		t.Fatalf("mock.CreateBranch: %v", err)
	}
	if err := mock.BatchCreateFiles(ctx, map[string][]byte{
		"configuration/addons-clusters-values/foo.yaml": []byte("name: foo\n"),
	}, "sharko/foundation-test-7-1-3", "feat: add foo"); err != nil {
		t.Fatalf("mock.BatchCreateFiles: %v", err)
	}
	pr, err := mock.CreatePullRequest(ctx,
		"feat: add foo cluster", "body",
		"sharko/foundation-test-7-1-3", "main")
	if err != nil {
		t.Fatalf("mock.CreatePullRequest: %v", err)
	}
	if pr.ID != 1 {
		t.Fatalf("first mock PR should be #1, got #%d", pr.ID)
	}
	if pr.Status != "open" {
		t.Fatalf("new PR status=%q want open", pr.Status)
	}

	// Verify ListPullRequests filters correctly.
	open, _ := mock.ListPullRequests(ctx, "open")
	if len(open) != 1 {
		t.Fatalf("ListPullRequests(open): got %d want 1", len(open))
	}

	// Merge + verify file moved to base.
	if err := mock.MergePullRequest(ctx, pr.ID); err != nil {
		t.Fatalf("mock.MergePullRequest: %v", err)
	}
	if got := mock.FileAt("main", "configuration/addons-clusters-values/foo.yaml"); got != "name: foo\n" {
		t.Fatalf("post-merge FileAt: got %q want %q", got, "name: foo\n")
	}
	merged := mock.ListMockPRs("merged")
	if len(merged) != 1 {
		t.Fatalf("ListMockPRs(merged): got %d want 1", len(merged))
	}
	if merged[0].MergeCommit == "" {
		t.Fatal("merged PR has empty MergeCommit")
	}

	// Verify GetFileContent on a missing path uses the canonical sentinel.
	if _, err := mock.GetFileContent(ctx, "does/not/exist", "main"); !errors.Is(err, gitprovider.ErrFileNotFound) {
		t.Fatalf("expected ErrFileNotFound, got %v", err)
	}

	// ---- Eventually + EventuallyNoError smoke ----------------------------
	Eventually(t, 2*time.Second, func() bool { return true })
	EventuallyNoError(t, 2*time.Second, func() error { return nil })

	// ---- MustJSON + RandSuffix smoke -------------------------------------
	if got := string(MustJSON(t, map[string]string{"k": "v"})); got != `{"k":"v"}` {
		t.Fatalf("MustJSON: got %q", got)
	}
	if got := RandSuffix(); len(got) < 6 {
		t.Fatalf("RandSuffix: got %q (too short)", got)
	}
}
