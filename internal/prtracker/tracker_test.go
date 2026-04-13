package prtracker

import (
	"context"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/cmstore"
	"k8s.io/client-go/kubernetes/fake"
)

// mockGitProvider implements GitProvider for testing.
type mockGitProvider struct {
	statuses map[int]string
}

func (m *mockGitProvider) GetPullRequestStatus(_ context.Context, prNumber int) (string, error) {
	s, ok := m.statuses[prNumber]
	if !ok {
		return "open", nil
	}
	return s, nil
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
