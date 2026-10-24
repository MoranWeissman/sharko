package prtracker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// hookGitProvider answers a fixed status per PR and runs onStatus (if set)
// while the status call is in flight — the moment PollOnce used to hold an
// old copy of the whole PR list.
type hookGitProvider struct {
	mu       sync.Mutex
	statuses map[int]string
	onStatus func(prNumber int)
}

func (h *hookGitProvider) GetPullRequestStatus(_ context.Context, n int) (string, error) {
	if h.onStatus != nil {
		h.onStatus(n)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.statuses[n]; ok {
		return s, nil
	}
	return "open", nil
}

func (h *hookGitProvider) DeleteBranch(context.Context, string) error { return nil }

func trackOpen(t *testing.T, tr *Tracker, id int, cluster, op string) {
	t.Helper()
	if err := tr.TrackPR(context.Background(), PRInfo{
		PRID: id, Cluster: cluster, Operation: op, LastStatus: "open", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("TrackPR %d: %v", id, err)
	}
}

// R2: a pull request tracked while a poll is running must still be tracked
// after the poll writes its result. The old poll wrote back the copy it
// read at the start, which dropped it.
func TestPollOnce_KeepsAPRTrackedDuringThePoll(t *testing.T) {
	gp := &hookGitProvider{statuses: map[int]string{1: "merged"}}
	tr, _ := newTestTracker(gp)
	trackOpen(t, tr, 1, "a", OpRegisterCluster)

	gp.onStatus = func(n int) {
		if n == 1 {
			trackOpen(t, tr, 2, "b", OpRegisterCluster)
		}
	}
	tr.PollOnce(context.Background())

	got, err := tr.GetPR(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("PR #2 was tracked during the poll and is gone afterwards: the poll wrote back an old copy")
	}
	if one, _ := tr.GetPR(context.Background(), 1); one != nil {
		t.Fatalf("merged PR #1 should be dropped, still tracked: %+v", one)
	}
}

// R2: the hooks run outside the store's lock. A hook that tracks a pull
// request (as the merge hook's follow-up work can) used to deadlock inside
// PollSinglePR, because it held the store lock while the hook ran.
func TestPollSinglePR_HookRunsOutsideTheStoreLock(t *testing.T) {
	gp := &hookGitProvider{statuses: map[int]string{7: "merged"}}
	tr, _ := newTestTracker(gp)
	trackOpen(t, tr, 7, "a", OpRegisterCluster)
	tr.SetOnMergeFn(func(PRInfo) {
		_ = tr.TrackPR(context.Background(), PRInfo{PRID: 8, LastStatus: "open"})
	})

	done := make(chan struct{})
	go func() {
		_, _ = tr.PollSinglePR(context.Background(), 7)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("PollSinglePR is stuck: it runs the merge hook while holding the store lock")
	}
	if got, _ := tr.GetPR(context.Background(), 8); got == nil {
		t.Fatal("PR #8 tracked by the merge hook was lost")
	}
}

// R1/R4: a finish step that fails keeps the pull request tracked, with its
// merged status, and the next poll runs only the finish step again — the
// merge and complete hooks run once.
func TestPollOnce_FailedFinishIsTriedAgain(t *testing.T) {
	gp := &hookGitProvider{statuses: map[int]string{3: "merged"}}
	tr, _ := newTestTracker(gp)
	if err := tr.TrackPR(context.Background(), PRInfo{
		PRID: 3, Cluster: "keep-me", Operation: OpRemoveCluster, LastStatus: "open",
		FollowUp: &ClusterFollowUp{KeepConnection: true},
	}); err != nil {
		t.Fatal(err)
	}
	var merges, completes, finishes int
	tr.SetOnMergeFn(func(PRInfo) { merges++ })
	tr.SetOnCompleteFn(func(PRInfo, string) { completes++ })
	tr.SetFinishFn(func(_ context.Context, pr PRInfo, status string) error {
		finishes++
		if status != "merged" {
			t.Errorf("finish status = %q, want merged", status)
		}
		if finishes < 3 {
			return errors.New("label update failed")
		}
		return nil
	})

	ctx := context.Background()
	tr.PollOnce(ctx)
	got, _ := tr.GetPR(ctx, 3)
	if got == nil {
		t.Fatal("a failed finish dropped the PR; it must stay tracked to try again")
	}
	if got.LastStatus != "merged" || got.FinishAttempts != 1 {
		t.Fatalf("after one failed finish: status=%q attempts=%d, want merged/1", got.LastStatus, got.FinishAttempts)
	}
	// While it waits, the kept connection stays protected...
	kept, _ := tr.KeptConnections(ctx)
	if kept["keep-me"].ReleasePR != 3 {
		t.Fatal("a merged remove that still has to finish must keep protecting its connection")
	}
	// ...and it is not listed as a pending pull request.
	if list, _ := tr.ListPRs(ctx, "", "", "", ""); len(list) != 0 {
		t.Fatalf("a merged PR waiting for its finish step is listed as pending: %+v", list)
	}

	tr.PollOnce(ctx)
	tr.PollOnce(ctx)
	if got, _ := tr.GetPR(ctx, 3); got != nil {
		t.Fatalf("finish worked on the third try but the PR is still tracked: %+v", got)
	}
	if merges != 1 || completes != 1 || finishes != 3 {
		t.Fatalf("merges=%d completes=%d finishes=%d, want 1/1/3", merges, completes, finishes)
	}
}

// R1/R4: a finish that never works must not loop forever. After the cap
// the tracker logs it, writes an audit entry, and drops the pull request.
func TestPollOnce_FinishGivesUpAfterTheCap(t *testing.T) {
	gp := &hookGitProvider{statuses: map[int]string{4: "closed"}}
	tr, events := newTestTracker(gp)
	trackOpen(t, tr, 4, "gone-cluster", OpAdoptCluster)
	tr.SetMaxFinishAttempts(3)
	finishes := 0
	tr.SetFinishFn(func(context.Context, PRInfo, string) error {
		finishes++
		return errors.New("cluster is gone")
	})

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		tr.PollOnce(ctx)
		if got, _ := tr.GetPR(ctx, 4); got == nil {
			t.Fatalf("dropped after %d tries, the cap is 3", i+1)
		}
	}
	tr.PollOnce(ctx)
	if got, _ := tr.GetPR(ctx, 4); got != nil {
		t.Fatalf("still tracked after the cap: %+v", got)
	}
	tr.PollOnce(ctx)
	if finishes != 3 {
		t.Fatalf("finish ran %d times, want exactly 3", finishes)
	}
	var gaveUp bool
	for _, e := range *events {
		if e.Event == "pr_finish_gave_up" {
			gaveUp = true
			if e.Level != "error" || e.Result != "failure" {
				t.Fatalf("give-up audit entry = %+v, want level error, result failure", e)
			}
		}
	}
	if !gaveUp {
		t.Fatalf("no pr_finish_gave_up audit entry; got %+v", *events)
	}
}

// R2: a pull request seen merged by a refresh and by the poll loop runs
// its hooks once.
func TestPollSinglePR_ThenPollOnce_HooksRunOnce(t *testing.T) {
	gp := &hookGitProvider{statuses: map[int]string{5: "merged"}}
	tr, _ := newTestTracker(gp)
	trackOpen(t, tr, 5, "a", OpRegisterCluster)
	var merges, finishes int
	tr.SetOnMergeFn(func(PRInfo) { merges++ })
	tr.SetFinishFn(func(context.Context, PRInfo, string) error {
		finishes++
		return errors.New("not yet")
	})
	ctx := context.Background()
	got, err := tr.PollSinglePR(ctx, 5)
	if err != nil || got == nil || got.LastStatus != "merged" {
		t.Fatalf("PollSinglePR = %+v, %v", got, err)
	}
	if again, err := tr.PollSinglePR(ctx, 5); err != nil || again == nil || again.LastStatus != "merged" {
		t.Fatalf("second PollSinglePR = %+v, %v", again, err)
	}
	tr.PollOnce(ctx)
	if merges != 1 {
		t.Fatalf("merge hook ran %d times, want 1", merges)
	}
	if finishes != 2 {
		t.Fatalf("finish ran %d times, want 2 (once on the refresh, once on the retry)", finishes)
	}
}
