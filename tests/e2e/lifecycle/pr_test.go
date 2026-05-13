//go:build e2e

// Package lifecycle — V2 Epic 7-1.13.
//
// This file exercises the PR-tracking + notification surface end to
// end against an in-process sharko stack:
//
//	GET    /api/v1/prs                   — list tracked PRs
//	GET    /api/v1/prs/merged            — list merged PRs (via GitProvider)
//	GET    /api/v1/prs/{id}              — single PR details
//	POST   /api/v1/prs/{id}/refresh      — force re-poll from upstream
//	DELETE /api/v1/prs/{id}              — drop tracked PR
//	GET    /api/v1/notifications         — list notifications
//	POST   /api/v1/notifications/read-all — mark all as read
//
// Wiring rationale:
//
//   - sharko's in-process boot path (StartSharko, harness/sharko.go) does
//     NOT install a prtracker — the production path constructs one from a
//     real Kubernetes ConfigMap store, which the harness does not own. So
//     the test instantiates a real prtracker.Tracker backed by a fake
//     k8s.io clientset (kubernetes/fake) + cmstore.Store, then attaches
//     it via *api.Server.SetPRTracker. This keeps the test exercising
//     real handler code (handleListPRs / handleGetPR / handleRefreshPR /
//     handleDeletePR) without inventing a parallel mock.
//
//   - PR creation is simulated by calling tracker.TrackPR + the
//     MockGitProvider's CreatePullRequest — the orchestrator's normal
//     "open a PR + track it" funnel is exercised in the per-flow tests
//     (cluster lifecycle, addon lifecycle). Here we focus on the tracking
//     surface itself, so seeding directly is the cleanest separation.
//
//   - State changes (merge / close) are driven through MockGitProvider's
//     MergePR / ClosePR helpers — those mutate the mock's internal state,
//     and the tracker's PollSinglePR (invoked by /prs/{id}/refresh)
//     then sees the new status via GetPullRequestStatus.
//
//   - Notifications are seeded via *api.Server.NotificationStore().Add —
//     the production path uses the same Store from the background Checker.
//
// All tests run in <500ms wall-clock; no kind / docker / argocd needed.
package lifecycle

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/notifications"
	"github.com/MoranWeissman/sharko/internal/prtracker"
	"github.com/MoranWeissman/sharko/tests/e2e/harness"

	"k8s.io/client-go/kubernetes/fake"
)

// prTestStack bundles the harness primitives a single test needs. Each
// test allocates its own stack so subtests run sequentially with
// isolated state — matches the rest of the lifecycle suite's pattern.
type prTestStack struct {
	sharko  *harness.Sharko
	mock    *harness.MockGitProvider
	tracker *prtracker.Tracker
	admin   *harness.Client
}

// newPRTestStack boots an in-process sharko + GH mock + git fake, wires
// a prtracker (backed by a fake k8s ConfigMap store + the mock as the
// upstream Git provider), seeds the admin client, and returns the
// wired stack.
//
// The prtracker's GitProvider accessor is a closure over the mock so
// every PollSinglePR call (triggered by /prs/{id}/refresh) routes
// through MockGitProvider.GetPullRequestStatus — the only method the
// tracker's interface needs.
func newPRTestStack(t *testing.T) *prTestStack {
	t.Helper()

	git := harness.StartGitFake(t)
	mock := harness.StartGitMock(t)
	sharko := harness.StartSharko(t, harness.SharkoConfig{
		Mode:        harness.SharkoModeInProcess,
		GitFake:     git,
		GitProvider: mock,
	})
	sharko.WaitHealthy(t, 10*time.Second)
	harness.SeedUsers(t, sharko, harness.DefaultTestUsers())

	srv := sharko.APIServer()
	if srv == nil {
		t.Fatalf("newPRTestStack: APIServer is nil — harness wiring is broken")
	}

	// In-memory ConfigMap store backing the tracker. The fake clientset
	// satisfies kubernetes.Interface; cmstore writes to / reads from a
	// single ConfigMap (created on first write). namespace + name are
	// arbitrary in tests since there's no real apiserver behind them.
	k8sClient := fake.NewSimpleClientset()
	cmStore := cmstore.NewStore(k8sClient, "sharko", "sharko-pending-prs")

	// Lazy GitProvider accessor — the tracker calls this for every poll
	// so it always sees the live mock. Wrapping the mock in a typed
	// closure keeps the prtracker import surface narrow (it only needs
	// GetPullRequestStatus) and makes the dependency obvious from the
	// call site.
	gitProviderFn := func() prtracker.GitProvider { return mock }

	// Audit sink — discard. The tests don't assert on audit entries
	// here; the prtracker_test unit suite covers the audit shape.
	auditFn := func(_ audit.Entry) {}

	tracker := prtracker.NewTracker(cmStore, gitProviderFn, auditFn)
	srv.SetPRTracker(tracker)

	admin := harness.NewClient(t, sharko)
	return &prTestStack{
		sharko:  sharko,
		mock:    mock,
		tracker: tracker,
		admin:   admin,
	}
}

// seedTrackedPR opens a PR on the mock (so /refresh has a real upstream
// to poll) and tracks it via the prtracker. Returns the tracker's view
// of the PR so the caller can assert against PRID etc.
//
// The mock auto-creates the head branch when CreateOrUpdateFile sees
// it for the first time, so we don't have to call CreateBranch
// explicitly here.
func seedTrackedPR(t *testing.T, st *prTestStack, branch, title, operation string) prtracker.PRInfo {
	t.Helper()
	ctx := context.Background()

	// Make sure the head branch exists with at least one file so
	// CreatePullRequest's "head exists" guard passes.
	if err := st.mock.CreateOrUpdateFile(ctx,
		"configuration/managed-clusters.yaml",
		[]byte("clusters: []\n"),
		branch, "seed"); err != nil {
		t.Fatalf("seedTrackedPR: CreateOrUpdateFile: %v", err)
	}

	pr, err := st.mock.CreatePullRequest(ctx, title, "seeded by e2e", branch, "main")
	if err != nil {
		t.Fatalf("seedTrackedPR: CreatePullRequest: %v", err)
	}

	info := prtracker.PRInfo{
		PRID:       pr.ID,
		PRUrl:      pr.URL,
		PRBranch:   branch,
		PRTitle:    title,
		PRBase:     "main",
		Cluster:    "e2e-cluster",
		Operation:  operation,
		User:       "admin",
		Source:     "api",
		CreatedAt:  time.Now(),
		LastStatus: "open",
	}
	if err := st.tracker.TrackPR(ctx, info); err != nil {
		t.Fatalf("seedTrackedPR: TrackPR: %v", err)
	}
	return info
}

// TestPRTracking covers the /prs and /prs/merged endpoint group.
//
// Subtests share the stack so each one builds on the prior state — the
// natural lifecycle ordering (empty → seeded → refreshed → merged →
// dropped) maps cleanly onto t.Run sub-tests and saves a few hundred
// ms per subtest of boot time.
func TestPRTracking(t *testing.T) {
	st := newPRTestStack(t)

	t.Run("ListEmpty", func(t *testing.T) {
		got := st.admin.ListPRs(t)
		if len(got.PRs) != 0 {
			t.Fatalf("ListEmpty: expected 0 PRs, got %d (%+v)", len(got.PRs), got.PRs)
		}
		// Limit must be non-zero so the FE knows the cap; the handler
		// always echoes the effective limit even on empty responses.
		if got.Limit == 0 {
			t.Fatalf("ListEmpty: expected non-zero Limit, got 0")
		}
	})

	pr := seedTrackedPR(t, st, "sharko/register-e2e", "Register cluster e2e", prtracker.OpRegisterCluster)

	t.Run("ListAfterSeed", func(t *testing.T) {
		got := st.admin.ListPRs(t)
		if len(got.PRs) != 1 {
			t.Fatalf("ListAfterSeed: expected 1 PR, got %d (%+v)", len(got.PRs), got.PRs)
		}
		row := got.PRs[0]
		if row.PRID != pr.PRID {
			t.Errorf("PRID mismatch: got %d want %d", row.PRID, pr.PRID)
		}
		if row.Operation != prtracker.OpRegisterCluster {
			t.Errorf("Operation mismatch: got %q want %q", row.Operation, prtracker.OpRegisterCluster)
		}
		if row.LastStatus != "open" {
			t.Errorf("LastStatus mismatch: got %q want %q", row.LastStatus, "open")
		}
		if row.Cluster != "e2e-cluster" {
			t.Errorf("Cluster mismatch: got %q want %q", row.Cluster, "e2e-cluster")
		}
	})

	t.Run("GetPRDetail", func(t *testing.T) {
		got := st.admin.GetPR(t, pr.PRID)
		if got.PRID != pr.PRID {
			t.Fatalf("GetPR: PRID got %d want %d", got.PRID, pr.PRID)
		}
		if got.PRTitle != "Register cluster e2e" {
			t.Errorf("GetPR: PRTitle got %q want %q", got.PRTitle, "Register cluster e2e")
		}
	})

	t.Run("GetPRNotFound", func(t *testing.T) {
		// Unknown PR ID must return 404 — exercises the nil-PR branch
		// in handleGetPR. WithExpectStatus narrows the helper's check.
		var ignored map[string]interface{}
		st.admin.GetJSON(t, "/api/v1/prs/9999", &ignored, harness.WithExpectStatus(http.StatusNotFound))
	})

	t.Run("RefreshPR_NoChange", func(t *testing.T) {
		// PR is still open on the mock — refresh must succeed and keep
		// LastStatus="open". This exercises the happy refresh path
		// without a state transition.
		got := st.admin.RefreshPR(t, pr.PRID)
		if got.LastStatus != "open" {
			t.Errorf("RefreshPR_NoChange: LastStatus got %q want %q", got.LastStatus, "open")
		}
		if got.LastPolled == "" {
			t.Errorf("RefreshPR_NoChange: empty LastPolled — handler should stamp poll time")
		}
	})

	t.Run("RefreshPR_AfterExternalMerge", func(t *testing.T) {
		// Simulate a maintainer merging the PR via the GitHub UI by
		// flipping the mock's PR state directly. The next /refresh call
		// must (a) see status="merged", (b) emit a pr_merged audit
		// event, and (c) DROP the PR from tracking (matches
		// PollOnce/PollSinglePR semantics — merged PRs are removed).
		st.mock.MergePR(t, pr.PRID)

		got := st.admin.RefreshPR(t, pr.PRID)
		if got.LastStatus != "merged" {
			t.Fatalf("RefreshPR_AfterExternalMerge: LastStatus got %q want %q", got.LastStatus, "merged")
		}

		// After the merge transition the tracker drops the PR; a
		// subsequent List should be empty.
		listed := st.admin.ListPRs(t)
		if len(listed.PRs) != 0 {
			t.Errorf("RefreshPR_AfterExternalMerge: expected 0 tracked PRs after merge, got %d", len(listed.PRs))
		}

		// And the PR-detail endpoint should now 404 (PR is gone from
		// the tracker store, even though the mock still knows about it
		// in "merged" state).
		var ignored map[string]interface{}
		st.admin.GetJSON(t, "/api/v1/prs/"+itoa(pr.PRID), &ignored, harness.WithExpectStatus(http.StatusNotFound))
	})

	t.Run("MergedListIncludesMergedPR", func(t *testing.T) {
		// /prs/merged queries the active GitProvider (the mock) for
		// closed PRs, filters to status="merged". Our merged PR must
		// appear with the inferred fields populated. Note: the cache
		// is keyed by "all" with a 60s TTL — first call populates it.
		got := st.admin.ListMergedPRs(t)
		if len(got.PRs) == 0 {
			t.Fatalf("MergedListIncludesMergedPR: expected at least 1 merged PR, got 0")
		}
		var found bool
		for _, m := range got.PRs {
			if m.PRID == pr.PRID {
				found = true
				if m.PRTitle != "Register cluster e2e" {
					t.Errorf("merged PRTitle got %q want %q", m.PRTitle, "Register cluster e2e")
				}
				if m.PRBranch != "sharko/register-e2e" {
					t.Errorf("merged PRBranch got %q want %q", m.PRBranch, "sharko/register-e2e")
				}
				break
			}
		}
		if !found {
			t.Fatalf("MergedListIncludesMergedPR: PR #%d not in merged list (got %+v)", pr.PRID, got.PRs)
		}
	})

	// Seed a second PR specifically for the DELETE assertion so
	// MergedListIncludesMergedPR's state isn't disturbed.
	closedPR := seedTrackedPR(t, st, "sharko/values-e2e", "Update foo overrides on cluster e2e-cluster", prtracker.OpValuesEdit)

	t.Run("DropTrackedPR", func(t *testing.T) {
		// DELETE /api/v1/prs/{id} — admin scope. Asserts 200 + that
		// subsequent List does not contain the dropped PR + that GET
		// detail returns 404.
		st.admin.DeletePR(t, closedPR.PRID)

		listed := st.admin.ListPRs(t)
		for _, row := range listed.PRs {
			if row.PRID == closedPR.PRID {
				t.Fatalf("DropTrackedPR: PR #%d still listed after DELETE", closedPR.PRID)
			}
		}
		var ignored map[string]interface{}
		st.admin.GetJSON(t, "/api/v1/prs/"+itoa(closedPR.PRID), &ignored, harness.WithExpectStatus(http.StatusNotFound))
	})
}

// TestNotificationsLifecycle covers the /notifications endpoint pair.
//
// Notifications are an independent surface (no prtracker dep), so this
// test stays minimal: seed via the in-process Store, list, mark all
// read, verify the read flag flipped on each entry.
func TestNotificationsLifecycle(t *testing.T) {
	git := harness.StartGitFake(t)
	mock := harness.StartGitMock(t)
	sharko := harness.StartSharko(t, harness.SharkoConfig{
		Mode:        harness.SharkoModeInProcess,
		GitFake:     git,
		GitProvider: mock,
	})
	sharko.WaitHealthy(t, 10*time.Second)
	harness.SeedUsers(t, sharko, harness.DefaultTestUsers())

	srv := sharko.APIServer()
	store := srv.NotificationStore()
	if store == nil {
		t.Fatalf("NotificationStore is nil — handler-level wiring regressed")
	}

	admin := harness.NewClient(t, sharko)

	t.Run("ListInitial", func(t *testing.T) {
		// Fresh boot: persistence dir (/data) does not exist on the
		// test host so the store falls back to in-memory mode and
		// loads zero notifications. Anything non-empty here means a
		// background producer leaked into the test boot path.
		got := admin.ListNotifications(t)
		if len(got.Notifications) != 0 {
			t.Fatalf("ListInitial: expected 0 notifications, got %d (%+v)", len(got.Notifications), got.Notifications)
		}
		if got.UnreadCount != 0 {
			t.Errorf("ListInitial: UnreadCount got %d want 0", got.UnreadCount)
		}
	})

	// Seed two distinct notifications. Same Title would dedupe, so we
	// give them unique titles.
	store.Add(notifications.Notification{
		ID:          "n-upgrade-1",
		Type:        notifications.TypeUpgrade,
		Title:       "Addon argo-cd upgrade available",
		Description: "argo-cd v2.10.0 → v2.11.0",
		Timestamp:   time.Now(),
	})
	store.Add(notifications.Notification{
		ID:          "n-security-1",
		Type:        notifications.TypeSecurity,
		Title:       "Security advisory for cert-manager",
		Description: "CVE-2025-XXXX",
		Timestamp:   time.Now(),
	})

	t.Run("ListAfterSeed", func(t *testing.T) {
		got := admin.ListNotifications(t)
		if len(got.Notifications) != 2 {
			t.Fatalf("ListAfterSeed: expected 2 notifications, got %d (%+v)", len(got.Notifications), got.Notifications)
		}
		if got.UnreadCount != 2 {
			t.Errorf("ListAfterSeed: UnreadCount got %d want 2", got.UnreadCount)
		}
		for _, n := range got.Notifications {
			if n.Read {
				t.Errorf("ListAfterSeed: notification %q should be unread but Read=true", n.Title)
			}
		}
	})

	t.Run("MarkAllRead", func(t *testing.T) {
		admin.MarkAllNotificationsRead(t)

		got := admin.ListNotifications(t)
		if got.UnreadCount != 0 {
			t.Errorf("MarkAllRead: UnreadCount got %d want 0", got.UnreadCount)
		}
		if len(got.Notifications) != 2 {
			t.Fatalf("MarkAllRead: notifications count changed (got %d, want 2)", len(got.Notifications))
		}
		for _, n := range got.Notifications {
			if !n.Read {
				t.Errorf("MarkAllRead: notification %q still has Read=false", n.Title)
			}
		}
	})
}

// itoa keeps this file free of a strconv import for one path-fragment
// formatter. Mirrors the harness-side helper in apiclient_pr.go so the
// test reads naturally without importing the harness's unexported
// utility.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
