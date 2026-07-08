package prtracker

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"k8s.io/client-go/kubernetes/fake"
)

// mockGitProvider implements GitProvider for testing.
type mockGitProvider struct {
	statuses        map[int]string
	statusErrs      map[int]error // per-PR error returned by GetPullRequestStatus
	deletedBranches []string
	deleteBranchErr error
}

func (m *mockGitProvider) GetPullRequestStatus(_ context.Context, prNumber int) (string, error) {
	if m.statusErrs != nil {
		if err, ok := m.statusErrs[prNumber]; ok {
			return "", err
		}
	}
	s, ok := m.statuses[prNumber]
	if !ok {
		return "open", nil
	}
	return s, nil
}

// DeleteBranch records the branch name and returns the configured error
// (if any). BUG-032: prtracker now invokes DeleteBranch on observed-merge
// for hygiene; tests use this to assert the call happened (or didn't, when
// PRBranch is empty) and to exercise the best-effort error path.
func (m *mockGitProvider) DeleteBranch(_ context.Context, branchName string) error {
	m.deletedBranches = append(m.deletedBranches, branchName)
	return m.deleteBranchErr
}

func newTestTracker(gp GitProvider) (*Tracker, *[]audit.Entry) {
	client := fake.NewSimpleClientset()
	store := cmstore.NewStore(client, "default", "sharko-pending-prs")

	var events []audit.Entry
	auditFn := func(e audit.Entry) {
		events = append(events, e)
	}

	tracker := NewTracker(store, func() GitProvider { return gp }, auditFn)
	return tracker, &events
}

func TestTrackAndListPR(t *testing.T) {
	gp := &mockGitProvider{statuses: map[int]string{}}
	tracker, _ := newTestTracker(gp)
	ctx := context.Background()

	pr := PRInfo{
		PRID:       42,
		PRUrl:      "https://github.com/test/repo/pull/42",
		PRBranch:   "sharko/register-prod",
		PRTitle:    "Register cluster prod",
		PRBase:     "main",
		Cluster:    "prod",
		Operation:  "register",
		User:       "admin",
		Source:     "api",
		CreatedAt:  time.Now(),
		LastStatus: "open",
	}

	// Track
	if err := tracker.TrackPR(ctx, pr); err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	// List all
	prs, err := tracker.ListPRs(ctx, "", "", "", "")
	if err != nil {
		t.Fatalf("ListPRs: %v", err)
	}
	if len(prs) != 1 {
		t.Fatalf("expected 1 PR, got %d", len(prs))
	}
	if prs[0].PRID != 42 {
		t.Errorf("expected PR ID 42, got %d", prs[0].PRID)
	}

	// List with status filter
	prs, _ = tracker.ListPRs(ctx, "merged", "", "", "")
	if len(prs) != 0 {
		t.Errorf("expected 0 merged PRs, got %d", len(prs))
	}

	// List with cluster filter
	prs, _ = tracker.ListPRs(ctx, "", "prod", "", "")
	if len(prs) != 1 {
		t.Errorf("expected 1 PR for cluster prod, got %d", len(prs))
	}

	// Get single
	got, err := tracker.GetPR(ctx, 42)
	if err != nil {
		t.Fatalf("GetPR: %v", err)
	}
	if got == nil {
		t.Fatal("expected PR, got nil")
	}
	if got.Cluster != "prod" {
		t.Errorf("expected cluster prod, got %s", got.Cluster)
	}

	// Get non-existent
	got, _ = tracker.GetPR(ctx, 999)
	if got != nil {
		t.Error("expected nil for non-existent PR")
	}
}

func TestPollOnce_MergedPR(t *testing.T) {
	gp := &mockGitProvider{statuses: map[int]string{10: "merged"}}
	tracker, events := newTestTracker(gp)
	ctx := context.Background()

	var mergedPR *PRInfo
	tracker.SetOnMergeFn(func(pr PRInfo) {
		mergedPR = &pr
	})

	// Track a PR
	err := tracker.TrackPR(ctx, PRInfo{
		PRID:       10,
		PRBranch:   "sharko/test",
		Cluster:    "staging",
		Operation:  "register",
		User:       "admin",
		LastStatus: "open",
	})
	if err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	// Poll — should detect merge
	tracker.PollOnce(ctx)

	// Check audit event
	if len(*events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(*events))
	}
	if (*events)[0].Event != "pr_merged" {
		t.Errorf("expected event pr_merged, got %s", (*events)[0].Event)
	}

	// Check merge callback
	if mergedPR == nil {
		t.Fatal("expected merge callback to fire")
	}
	if mergedPR.PRID != 10 {
		t.Errorf("expected PR 10 in callback, got %d", mergedPR.PRID)
	}

	// PR should be removed from tracking
	prs, _ := tracker.ListPRs(ctx, "", "", "", "")
	if len(prs) != 0 {
		t.Errorf("expected 0 tracked PRs after merge, got %d", len(prs))
	}
}

func TestPollOnce_ClosedPR(t *testing.T) {
	gp := &mockGitProvider{statuses: map[int]string{20: "closed"}}
	tracker, events := newTestTracker(gp)
	ctx := context.Background()

	err := tracker.TrackPR(ctx, PRInfo{
		PRID:       20,
		PRBranch:   "sharko/remove",
		Cluster:    "dev",
		Operation:  "remove",
		User:       "admin",
		LastStatus: "open",
	})
	if err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	tracker.PollOnce(ctx)

	if len(*events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(*events))
	}
	if (*events)[0].Event != "pr_closed_without_merge" {
		t.Errorf("expected event pr_closed_without_merge, got %s", (*events)[0].Event)
	}

	prs, _ := tracker.ListPRs(ctx, "", "", "", "")
	if len(prs) != 0 {
		t.Errorf("expected 0 tracked PRs after close, got %d", len(prs))
	}
}

// TestPollOnce_MergedPR_FiresOnCompleteFn covers the V2-cleanup-84.1 seam:
// the change-log store hooks SetOnCompleteFn so it can record a durable
// entry BEFORE prtracker drops the PR from tracking. Assert the callback
// fires with status "merged" and the PR is still gone from tracking
// afterward (the record hook must not change prtracker's own removal
// behavior).
func TestPollOnce_MergedPR_FiresOnCompleteFn(t *testing.T) {
	gp := &mockGitProvider{statuses: map[int]string{11: "merged"}}
	tracker, _ := newTestTracker(gp)
	ctx := context.Background()

	var completedPR *PRInfo
	var completedStatus string
	tracker.SetOnCompleteFn(func(pr PRInfo, status string) {
		completedPR = &pr
		completedStatus = status
	})

	err := tracker.TrackPR(ctx, PRInfo{
		PRID:       11,
		PRBranch:   "sharko/test-complete",
		Cluster:    "staging",
		Addon:      "cert-manager",
		Operation:  "addon-enable",
		User:       "admin",
		LastStatus: "open",
	})
	if err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	tracker.PollOnce(ctx)

	if completedPR == nil {
		t.Fatal("expected onCompleteFn to fire on merge")
	}
	if completedPR.PRID != 11 {
		t.Errorf("expected PR 11 in onCompleteFn, got %d", completedPR.PRID)
	}
	if completedStatus != "merged" {
		t.Errorf("expected status merged, got %s", completedStatus)
	}

	// prtracker's own removal behavior must be unchanged.
	prs, _ := tracker.ListPRs(ctx, "", "", "", "")
	if len(prs) != 0 {
		t.Errorf("expected 0 tracked PRs after merge, got %d", len(prs))
	}
}

// TestPollOnce_ClosedPR_FiresOnCompleteFn mirrors the merge case above but
// for close-without-merge — onCompleteFn must fire for BOTH terminal
// states, unlike onMergeFn which only fires on merge.
func TestPollOnce_ClosedPR_FiresOnCompleteFn(t *testing.T) {
	gp := &mockGitProvider{statuses: map[int]string{21: "closed"}}
	tracker, _ := newTestTracker(gp)
	ctx := context.Background()

	var completedPR *PRInfo
	var completedStatus string
	tracker.SetOnCompleteFn(func(pr PRInfo, status string) {
		completedPR = &pr
		completedStatus = status
	})

	err := tracker.TrackPR(ctx, PRInfo{
		PRID:       21,
		PRBranch:   "sharko/remove-complete",
		Cluster:    "dev",
		Operation:  "remove-cluster",
		User:       "admin",
		LastStatus: "open",
	})
	if err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	tracker.PollOnce(ctx)

	if completedPR == nil {
		t.Fatal("expected onCompleteFn to fire on close")
	}
	if completedPR.PRID != 21 {
		t.Errorf("expected PR 21 in onCompleteFn, got %d", completedPR.PRID)
	}
	if completedStatus != "closed" {
		t.Errorf("expected status closed, got %s", completedStatus)
	}

	prs, _ := tracker.ListPRs(ctx, "", "", "", "")
	if len(prs) != 0 {
		t.Errorf("expected 0 tracked PRs after close, got %d", len(prs))
	}
}

// TestPollSinglePR_FiresOnCompleteFn covers the other polling path
// (single-PR poll, used by the manual poll endpoint) to make sure the
// completion hook is wired there too, not just in the batch PollOnce loop.
func TestPollSinglePR_FiresOnCompleteFn(t *testing.T) {
	gp := &mockGitProvider{statuses: map[int]string{31: "merged"}}
	tracker, _ := newTestTracker(gp)
	ctx := context.Background()

	var completedStatus string
	tracker.SetOnCompleteFn(func(pr PRInfo, status string) {
		completedStatus = status
	})

	err := tracker.TrackPR(ctx, PRInfo{
		PRID:       31,
		Cluster:    "prod",
		Operation:  "register-cluster",
		User:       "admin",
		LastStatus: "open",
	})
	if err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	if _, err := tracker.PollSinglePR(ctx, 31); err != nil {
		t.Fatalf("PollSinglePR: %v", err)
	}

	if completedStatus != "merged" {
		t.Errorf("expected onCompleteFn to fire with status merged, got %q", completedStatus)
	}
}

func TestPollOnce_NoProviderSkips(t *testing.T) {
	client := fake.NewSimpleClientset()
	store := cmstore.NewStore(client, "default", "sharko-pending-prs")

	tracker := NewTracker(store, func() GitProvider { return nil }, func(e audit.Entry) {})
	ctx := context.Background()

	// Should not panic when no provider
	tracker.PollOnce(ctx)
}

func TestStopTracking(t *testing.T) {
	gp := &mockGitProvider{statuses: map[int]string{}}
	tracker, _ := newTestTracker(gp)
	ctx := context.Background()

	err := tracker.TrackPR(ctx, PRInfo{PRID: 50, LastStatus: "open"})
	if err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	if err := tracker.StopTracking(ctx, 50); err != nil {
		t.Fatalf("StopTracking: %v", err)
	}

	prs, _ := tracker.ListPRs(ctx, "", "", "", "")
	if len(prs) != 0 {
		t.Errorf("expected 0 PRs after stop tracking, got %d", len(prs))
	}
}

func TestPollSinglePR(t *testing.T) {
	gp := &mockGitProvider{statuses: map[int]string{30: "open"}}
	tracker, _ := newTestTracker(gp)
	ctx := context.Background()

	err := tracker.TrackPR(ctx, PRInfo{
		PRID:       30,
		Cluster:    "test",
		Operation:  "adopt",
		User:       "qa",
		LastStatus: "open",
	})
	if err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	pr, err := tracker.PollSinglePR(ctx, 30)
	if err != nil {
		t.Fatalf("PollSinglePR: %v", err)
	}
	if pr.LastStatus != "open" {
		t.Errorf("expected status open, got %s", pr.LastStatus)
	}

	// Non-existent PR
	_, err = tracker.PollSinglePR(ctx, 999)
	if err == nil {
		t.Error("expected error for non-existent PR")
	}
}

// V125-1-6: ensure the new Operation enum round-trips through the
// ConfigMap encoding without loss. Older client code that reads the
// stored JSON must still see the canonical string verbatim.
func TestPRInfo_OperationRoundtrip(t *testing.T) {
	gp := &mockGitProvider{statuses: map[int]string{}}
	tracker, _ := newTestTracker(gp)
	ctx := context.Background()

	want := PRInfo{
		PRID:       301,
		Cluster:    "prod-eu",
		Operation:  OpRegisterCluster, // canonical enum string
		User:       "admin",
		LastStatus: "open",
	}
	if err := tracker.TrackPR(ctx, want); err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	got, err := tracker.GetPR(ctx, want.PRID)
	if err != nil {
		t.Fatalf("GetPR: %v", err)
	}
	if got == nil {
		t.Fatal("GetPR returned nil")
	}
	if got.Operation != "register-cluster" {
		t.Errorf("Operation roundtrip mismatch: got %q want %q", got.Operation, "register-cluster")
	}
}

// V125-1-6: ListPRsFiltered with ?operation=<csv>. Empty operations
// slice means "no filter" and behaves identically to ListPRs.
func TestListPRsFiltered_OperationCSV(t *testing.T) {
	gp := &mockGitProvider{statuses: map[int]string{}}
	tracker, _ := newTestTracker(gp)
	ctx := context.Background()

	mustTrack := func(id int, op string) {
		t.Helper()
		if err := tracker.TrackPR(ctx, PRInfo{PRID: id, Operation: op, LastStatus: "open"}); err != nil {
			t.Fatalf("TrackPR(%d,%s): %v", id, op, err)
		}
	}
	mustTrack(1, "register-cluster")
	mustTrack(2, "addon-add")
	mustTrack(3, "addon-upgrade")
	mustTrack(4, "init-repo")

	// No filter → all four
	all, err := tracker.ListPRsFiltered(ctx, "", "", "", "", nil)
	if err != nil {
		t.Fatalf("ListPRsFiltered nil: %v", err)
	}
	if len(all) != 4 {
		t.Errorf("expected 4 PRs, got %d", len(all))
	}

	// Two-element CSV → 2 matches
	got, err := tracker.ListPRsFiltered(ctx, "", "", "", "", []string{"addon-add", "addon-upgrade"})
	if err != nil {
		t.Fatalf("ListPRsFiltered: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 PRs, got %d", len(got))
	}

	// Empty-strings in CSV → silently dropped, behaves like nil filter
	got, _ = tracker.ListPRsFiltered(ctx, "", "", "", "", []string{"", "   "})
	if len(got) != 4 {
		t.Errorf("expected 4 PRs (empty strings dropped), got %d", len(got))
	}

	// No matches → empty
	got, _ = tracker.ListPRsFiltered(ctx, "", "", "", "", []string{"adopt-cluster"})
	if len(got) != 0 {
		t.Errorf("expected 0 PRs, got %d", len(got))
	}
}

func TestReconcileOnStartup(t *testing.T) {
	gp := &mockGitProvider{statuses: map[int]string{60: "merged"}}
	tracker, events := newTestTracker(gp)
	ctx := context.Background()

	// Simulate a PR that was tracked before restart
	err := tracker.TrackPR(ctx, PRInfo{
		PRID:       60,
		Cluster:    "prod",
		Operation:  "register",
		User:       "admin",
		LastStatus: "open",
	})
	if err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	// Reconcile on startup should detect the merge
	tracker.ReconcileOnStartup(ctx)

	if len(*events) != 1 {
		t.Fatalf("expected 1 audit event from startup reconcile, got %d", len(*events))
	}
	if (*events)[0].Event != "pr_merged" {
		t.Errorf("expected pr_merged event, got %s", (*events)[0].Event)
	}
}

// TestPollOnce_MergedPR_DeletesBranch — BUG-032: when prtracker observes
// a tracked PR flip to merged (e.g. external user merged via the GitHub
// UI) it must call DeleteBranch on the source branch as a hygiene step.
func TestPollOnce_MergedPR_DeletesBranch(t *testing.T) {
	gp := &mockGitProvider{statuses: map[int]string{42: "merged"}}
	tracker, _ := newTestTracker(gp)
	ctx := context.Background()

	if err := tracker.TrackPR(ctx, PRInfo{
		PRID:       42,
		PRBranch:   "sharko/register-prod-abcd1234",
		Cluster:    "prod",
		Operation:  "register",
		User:       "admin",
		LastStatus: "open",
	}); err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	tracker.PollOnce(ctx)

	if len(gp.deletedBranches) != 1 {
		t.Fatalf("expected 1 DeleteBranch call, got %d (%v)",
			len(gp.deletedBranches), gp.deletedBranches)
	}
	if gp.deletedBranches[0] != "sharko/register-prod-abcd1234" {
		t.Errorf("DeleteBranch(%q), want %q",
			gp.deletedBranches[0], "sharko/register-prod-abcd1234")
	}
}

// TestPollOnce_MergedPR_DeleteBranchBestEffort — BUG-032 best-effort
// guarantee: a DeleteBranch failure (AzureDevOps not-yet-implemented,
// branch already deleted, transient API hiccup) is logged but never
// blocks the tracker loop. The PR must still be removed from tracking.
func TestPollOnce_MergedPR_DeleteBranchBestEffort(t *testing.T) {
	gp := &mockGitProvider{
		statuses:        map[int]string{42: "merged"},
		deleteBranchErr: errBranchGone,
	}
	tracker, events := newTestTracker(gp)
	ctx := context.Background()

	if err := tracker.TrackPR(ctx, PRInfo{
		PRID:       42,
		PRBranch:   "sharko/already-deleted",
		Cluster:    "prod",
		Operation:  "register",
		User:       "admin",
		LastStatus: "open",
	}); err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	tracker.PollOnce(ctx)

	// Audit event must still fire and the PR must be removed from tracking.
	if len(*events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(*events))
	}
	if (*events)[0].Event != "pr_merged" {
		t.Errorf("expected pr_merged, got %s", (*events)[0].Event)
	}
	prs, _ := tracker.ListPRs(ctx, "", "", "", "")
	if len(prs) != 0 {
		t.Errorf("expected PR removed after merge despite DeleteBranch failure, got %d", len(prs))
	}
}

// TestPollOnce_MergedPR_NoBranch — old state-store entries from before
// V125-1-6 may have empty PRBranch; the tracker must skip DeleteBranch
// silently rather than calling DeleteBranch("") and producing a confusing
// 404 in the provider logs.
func TestPollOnce_MergedPR_NoBranch(t *testing.T) {
	gp := &mockGitProvider{statuses: map[int]string{42: "merged"}}
	tracker, _ := newTestTracker(gp)
	ctx := context.Background()

	if err := tracker.TrackPR(ctx, PRInfo{
		PRID:       42,
		PRBranch:   "", // legacy entry — no branch on file
		Cluster:    "prod",
		Operation:  "register",
		User:       "admin",
		LastStatus: "open",
	}); err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	tracker.PollOnce(ctx)

	if len(gp.deletedBranches) != 0 {
		t.Errorf("expected no DeleteBranch when PRBranch is empty, got %v",
			gp.deletedBranches)
	}
}

// errBranchGone simulates the family of "branch is already gone or the
// provider can't delete it" errors (AzureDevOps "not yet implemented",
// GitHub 422 when the branch was already deleted, etc.).
var errBranchGone = stringErr("branch already deleted")

type stringErr string

func (e stringErr) Error() string { return string(e) }

// TestPollOnce_GonePR_Dropped — V2-cleanup-18. When the provider reports the
// PR no longer exists (HTTP 404 → gitprovider.ErrPullRequestNotFound), the
// tracker must drop the stale entry (self-heal a phantom "open" PR left over
// from a deleted PR / recreated repo) and emit a pr_gone audit event.
func TestPollOnce_GonePR_Dropped(t *testing.T) {
	gp := &mockGitProvider{
		statusErrs: map[int]error{
			8: fmt.Errorf("get pull request #8: %w", gitprovider.ErrPullRequestNotFound),
		},
	}
	tracker, events := newTestTracker(gp)
	ctx := context.Background()

	if err := tracker.TrackPR(ctx, PRInfo{
		PRID:       8,
		PRBranch:   "sharko/addon-cert-manager",
		Cluster:    "prod",
		Operation:  "addon-add",
		User:       "admin",
		LastStatus: "open",
	}); err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	tracker.PollOnce(ctx)

	// The phantom entry must be gone from ConfigMap state.
	prs, _ := tracker.ListPRs(ctx, "", "", "", "")
	if len(prs) != 0 {
		t.Errorf("expected 0 tracked PRs after gone-PR drop, got %d", len(prs))
	}

	// A pr_gone audit event must have been emitted.
	if len(*events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(*events))
	}
	if (*events)[0].Event != "pr_gone" {
		t.Errorf("expected event pr_gone, got %s", (*events)[0].Event)
	}
	if (*events)[0].Result != "warn" {
		t.Errorf("expected result warn, got %s", (*events)[0].Result)
	}
}

// TestPollOnce_TransientError_Kept — V2-cleanup-18. A transient/generic error
// (network, 401, 403, 429, 5xx — anything that is NOT
// gitprovider.ErrPullRequestNotFound) must NOT drop the PR. The entry is kept
// so the next poll retries it. No audit event is emitted.
func TestPollOnce_TransientError_Kept(t *testing.T) {
	gp := &mockGitProvider{
		statusErrs: map[int]error{
			9: errors.New("get pull request #9: 503 service unavailable"),
		},
	}
	tracker, events := newTestTracker(gp)
	ctx := context.Background()

	if err := tracker.TrackPR(ctx, PRInfo{
		PRID:       9,
		PRBranch:   "sharko/register-prod",
		Cluster:    "prod",
		Operation:  "register",
		User:       "admin",
		LastStatus: "open",
	}); err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	tracker.PollOnce(ctx)

	// The PR must still be tracked (will retry next poll).
	prs, _ := tracker.ListPRs(ctx, "", "", "", "")
	if len(prs) != 1 {
		t.Errorf("expected 1 tracked PR kept on transient error, got %d", len(prs))
	}

	// No audit event on a transient failure.
	if len(*events) != 0 {
		t.Errorf("expected 0 audit events on transient error, got %d", len(*events))
	}
}

// TestPollSinglePR_Gone_Dropped — V2-cleanup-18. PollSinglePR must apply the
// same gone-PR handling: drop the entry, emit pr_gone, and return (nil, nil)
// because the PR being gone is an expected terminal state, not a failure.
func TestPollSinglePR_Gone_Dropped(t *testing.T) {
	gp := &mockGitProvider{
		statusErrs: map[int]error{
			8: fmt.Errorf("get pull request #8: %w", gitprovider.ErrPullRequestNotFound),
		},
	}
	tracker, events := newTestTracker(gp)
	ctx := context.Background()

	if err := tracker.TrackPR(ctx, PRInfo{
		PRID:       8,
		Cluster:    "prod",
		Operation:  "addon-add",
		User:       "admin",
		LastStatus: "open",
	}); err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	pr, err := tracker.PollSinglePR(ctx, 8)
	if err != nil {
		t.Fatalf("PollSinglePR returned error for gone PR, want nil: %v", err)
	}
	if pr != nil {
		t.Errorf("expected nil PRInfo for gone PR, got %+v", pr)
	}

	// Entry dropped from state.
	prs, _ := tracker.ListPRs(ctx, "", "", "", "")
	if len(prs) != 0 {
		t.Errorf("expected 0 tracked PRs after gone-PR drop, got %d", len(prs))
	}

	// pr_gone audit emitted.
	if len(*events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(*events))
	}
	if (*events)[0].Event != "pr_gone" {
		t.Errorf("expected event pr_gone, got %s", (*events)[0].Event)
	}
}

// TestPollSinglePR_TransientError_Kept — V2-cleanup-18. A transient/generic
// error from PollSinglePR must surface as an error (so the caller knows the
// poll failed) and must NOT drop the tracked PR.
func TestPollSinglePR_TransientError_Kept(t *testing.T) {
	gp := &mockGitProvider{
		statusErrs: map[int]error{
			9: errors.New("get pull request #9: 500 internal server error"),
		},
	}
	tracker, events := newTestTracker(gp)
	ctx := context.Background()

	if err := tracker.TrackPR(ctx, PRInfo{
		PRID:       9,
		Cluster:    "prod",
		Operation:  "register",
		User:       "admin",
		LastStatus: "open",
	}); err != nil {
		t.Fatalf("TrackPR: %v", err)
	}

	_, err := tracker.PollSinglePR(ctx, 9)
	if err == nil {
		t.Fatal("expected error on transient failure, got nil")
	}

	// PR must still be tracked.
	prs, _ := tracker.ListPRs(ctx, "", "", "", "")
	if len(prs) != 1 {
		t.Errorf("expected 1 tracked PR kept on transient error, got %d", len(prs))
	}

	// No audit event on a transient failure.
	if len(*events) != 0 {
		t.Errorf("expected 0 audit events on transient error, got %d", len(*events))
	}
}
