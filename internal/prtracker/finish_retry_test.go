package prtracker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/gitprovider"
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

// testClock is a clock the test moves by hand.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock(tr *Tracker) *testClock {
	c := &testClock{t: time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)}
	tr.SetClock(c.now)
	return c
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) step(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

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

	clock := newTestClock(tr)
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
	// ...and it is listed as still finishing (EP3 dry run round S, S21).
	if list, _ := tr.ListPRs(ctx, "", "", "", ""); len(list) != 1 || ListStatus(list[0]) != StatusFinishing {
		t.Fatalf("a merged PR waiting for its finish step must be listed as finishing: %+v", list)
	}

	clock.step(FinishRetryGap)
	tr.PollOnce(ctx)
	clock.step(FinishRetryGap)
	tr.PollOnce(ctx)
	if got, _ := tr.GetPR(ctx, 3); got != nil {
		t.Fatalf("finish worked on the third try but the PR is still tracked: %+v", got)
	}
	if merges != 1 || completes != 1 || finishes != 3 {
		t.Fatalf("merges=%d completes=%d finishes=%d, want 1/1/3", merges, completes, finishes)
	}
}

// S10/S39: a finish that never works must not loop forever, and the
// tries are spread out. With the real limit and the real gap, polls that
// come sooner than the gap (a Trigger, a refresh) do not run it, and after
// the last try the entry is kept as given up: one error audit entry, and
// no second round of tries.
func TestPollOnce_FinishGivesUpAfterTheRealLimit_RoundS(t *testing.T) {
	gp := &hookGitProvider{statuses: map[int]string{4: "closed"}}
	tr, events := newTestTracker(gp)
	clock := newTestClock(tr)
	trackOpen(t, tr, 4, "gone-cluster", OpAdoptCluster)
	finishes := 0
	tr.SetFinishFn(func(context.Context, PRInfo, string) error {
		finishes++
		return errors.New("cluster is gone")
	})

	ctx := context.Background()
	for i := 1; i < DefaultMaxFinishAttempts; i++ {
		tr.PollOnce(ctx)
		// Polls that come right away do not use up tries.
		tr.PollOnce(ctx)
		tr.PollOnce(ctx)
		if finishes != i {
			t.Fatalf("after %d gaps the finish ran %d times, want %d: tries must wait %s", i-1, finishes, i, FinishRetryGap)
		}
		got, _ := tr.GetPR(ctx, 4)
		if got == nil || got.GaveUp {
			t.Fatalf("gave up after %d tries, the limit is %d: %+v", i, DefaultMaxFinishAttempts, got)
		}
		clock.step(FinishRetryGap)
	}
	tr.PollOnce(ctx)
	if finishes != DefaultMaxFinishAttempts {
		t.Fatalf("finish ran %d times, want %d", finishes, DefaultMaxFinishAttempts)
	}
	got, _ := tr.GetPR(ctx, 4)
	if got == nil || !got.GaveUp {
		t.Fatalf("after the last try the entry must be kept as given up, got %+v", got)
	}
	// No second round of tries, however long it waits.
	for i := 0; i < 5; i++ {
		clock.step(time.Hour)
		tr.PollOnce(ctx)
	}
	if finishes != DefaultMaxFinishAttempts {
		t.Fatalf("finish ran %d times after giving up, want it to stop at %d", finishes, DefaultMaxFinishAttempts)
	}
	gaveUp := 0
	for _, e := range *events {
		if e.Event == "pr_finish_gave_up" {
			gaveUp++
			if e.Level != "error" || e.Result != "failure" {
				t.Fatalf("give-up audit entry = %+v, want level error, result failure", e)
			}
		}
	}
	if gaveUp != 1 {
		t.Fatalf("got %d pr_finish_gave_up audit entries, want exactly 1; all: %+v", gaveUp, *events)
	}
	// A given-up entry is not shown as still finishing, and goes after a while.
	if list, _ := tr.ListPRs(ctx, "", "", "", ""); len(list) != 0 {
		t.Fatalf("a given-up entry is listed: %+v", list)
	}
	clock.step(GaveUpKeepFor + time.Hour)
	tr.PollOnce(ctx)
	if got, _ := tr.GetPR(ctx, 4); got != nil {
		t.Fatalf("a given-up entry is kept past %s: %+v", GaveUpKeepFor, got)
	}
}

// S10: the give-up line is true for every kind of pull request. A removal
// with cleanup=all has no mark, so the line must not say one protects the
// connection.
func TestGaveUpLogMessage_TrueForEveryKind_RoundS(t *testing.T) {
	want := "[prtracker] Sharko gave up finishing a pull request: every try failed. The work after the merge or close is not done. Look at the cluster and its ArgoCD connection, and finish it by hand."
	if GaveUpLogMessage != want {
		t.Fatalf("GaveUpLogMessage = %q, want %q", GaveUpLogMessage, want)
	}
	for _, banned := range []string{"mark", "nothing gets deleted", "GAVE UP"} {
		if strings.Contains(GaveUpLogMessage, banned) {
			t.Fatalf("GaveUpLogMessage says %q, which is not true for every kind", banned)
		}
	}
}

// S12: Sharko stopped right after it saved "merged" and before the hooks
// ran. The entry is stored with HooksPending. The next poll (a restart's
// first poll) runs the hooks, once, and then the finish.
func TestPollOnce_RunsHooksAStopSkipped_RoundS(t *testing.T) {
	gp := &hookGitProvider{statuses: map[int]string{}}
	tr, events := newTestTracker(gp)
	ctx := context.Background()
	if err := tr.TrackPR(ctx, PRInfo{
		PRID: 6, Cluster: "c", Operation: OpRegisterCluster, LastStatus: "merged", HooksPending: true,
	}); err != nil {
		t.Fatal(err)
	}
	var merges, completes, finishes int
	tr.SetOnMergeFn(func(PRInfo) { merges++ })
	tr.SetOnCompleteFn(func(PRInfo, string) { completes++ })
	tr.SetFinishFn(func(context.Context, PRInfo, string) error {
		finishes++
		if finishes == 1 {
			return errors.New("not yet")
		}
		return nil
	})
	tr.ReconcileOnStartup(ctx)
	if merges != 1 || completes != 1 || finishes != 1 {
		t.Fatalf("after the restart poll: merges=%d completes=%d finishes=%d, want 1/1/1", merges, completes, finishes)
	}
	tr.SetClock(func() time.Time { return time.Now().Add(FinishRetryGap + time.Second) })
	tr.PollOnce(ctx)
	if merges != 1 || completes != 1 || finishes != 2 {
		t.Fatalf("after the retry: merges=%d completes=%d finishes=%d, want 1/1/2 (hooks run once)", merges, completes, finishes)
	}
	n := 0
	for _, e := range *events {
		if e.Event == "pr_merged" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("pr_merged audit entries = %d, want 1", n)
	}
}

// S12: claiming the merged status saves HooksPending, so a stop before the
// hooks ran is seen on restart.
func TestClaimTerminal_SavesHooksPending_RoundS(t *testing.T) {
	gp := &hookGitProvider{statuses: map[int]string{}}
	tr, _ := newTestTracker(gp)
	ctx := context.Background()
	trackOpen(t, tr, 9, "c", OpRegisterCluster)
	if _, _, claimed, err := tr.claimTerminal(ctx, 9, "merged", false); err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}
	got, _ := tr.GetPR(ctx, 9)
	if got == nil || !got.HooksPending {
		t.Fatalf("after the claim HooksPending must be saved, got %+v", got)
	}
}

// S10: Git says an open pull request is gone. It is handled as closed, so
// the finish runs (a waiting mark comes off) instead of the entry being
// dropped with the work never done.
func TestPollOnce_GonePR_RunsTheFinishAsClosed_RoundS(t *testing.T) {
	gp := &mockGitProvider{statusErrs: map[int]error{
		11: fmt.Errorf("get pull request #11: %w", gitprovider.ErrPullRequestNotFound),
	}}
	tr, events := newTestTracker(gp)
	ctx := context.Background()
	trackOpen(t, tr, 11, "c", OpAdoptCluster)
	var gotStatus string
	tr.SetFinishFn(func(_ context.Context, _ PRInfo, status string) error {
		gotStatus = status
		return nil
	})
	tr.PollOnce(ctx)
	if gotStatus != "closed" {
		t.Fatalf("finish status = %q, want closed for a pull request Git says is gone", gotStatus)
	}
	if got, _ := tr.GetPR(ctx, 11); got != nil {
		t.Fatalf("still tracked after the finish worked: %+v", got)
	}
	if len(*events) != 1 || (*events)[0].Event != "pr_gone" {
		t.Fatalf("want one pr_gone audit entry, got %+v", *events)
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
	tr.SetClock(func() time.Time { return time.Now().Add(FinishRetryGap + time.Second) })
	tr.PollOnce(ctx)
	if merges != 1 {
		t.Fatalf("merge hook ran %d times, want 1", merges)
	}
	if finishes != 2 {
		t.Fatalf("finish ran %d times, want 2 (once on the refresh, once on the retry)", finishes)
	}
}

// S39: the real poll loop. Many Trigger calls in a row (what a failed
// finish used to cause through the reconciler) do not burn the tries:
// the finish runs once until the gap has passed.
func TestStartLoop_TriggersDoNotBurnTheTries_RoundS(t *testing.T) {
	gp := &hookGitProvider{statuses: map[int]string{12: "merged"}}
	tr, _ := newTestTracker(gp)
	newTestClock(tr)
	trackOpen(t, tr, 12, "c", OpRemoveCluster)
	var mu sync.Mutex
	finishes := 0
	tr.SetFinishFn(func(context.Context, PRInfo, string) error {
		mu.Lock()
		finishes++
		mu.Unlock()
		return errors.New("not yet")
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr.Start(ctx)
	defer tr.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tr.Trigger()
		time.Sleep(2 * time.Millisecond)
	}
	mu.Lock()
	got := finishes
	mu.Unlock()
	if got != 1 {
		t.Fatalf("the finish ran %d times in 2 s of triggers, want 1: the tries must wait %s", got, FinishRetryGap)
	}
}
