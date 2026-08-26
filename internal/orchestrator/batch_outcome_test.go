package orchestrator

// batch_outcome_test.go — R2-9.
//
// The batch response carried `total`, `succeeded`, `failed` and `partial`,
// and `failed` deliberately counts every cluster that did not FULLY succeed,
// partials included. So there was no accurate "failed outright" number on the
// wire at all: a client had to subtract to get it, and nothing said so.
//
// `outcome` carries the three accurate counts. These tests drive the REAL
// RegisterClusterBatch with the REAL fixtures, so the counts are checked
// against what the orchestrator genuinely produced rather than against a
// hand-built struct.

import (
	"context"
	"fmt"
	"testing"

	"github.com/MoranWeissman/sharko/internal/providers"
)

func TestBatchOutcome_IsAccurateOnEveryShapeTheFixturesProduce(t *testing.T) {
	cases := []struct {
		what      string
		setup     func() (*Orchestrator, []RegisterClusterRequest)
		completed int
		partly    int
		failed    int
	}{
		{
			what: "every cluster finished",
			setup: func() (*Orchestrator, []RegisterClusterRequest) {
				creds := &mockCredProvider{creds: map[string]*providers.Kubeconfig{
					"prod-eu": {Server: "https://eu.example.com:6443", CAData: []byte("ca"), Token: "tok"},
					"prod-us": {Server: "https://us.example.com:6443", CAData: []byte("ca"), Token: "tok"},
				}}
				orch := New(nil, creds, newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
				return orch, []RegisterClusterRequest{
					{Name: "prod-eu", Addons: map[string]bool{"monitoring": true}},
					{Name: "prod-us", Addons: map[string]bool{"monitoring": true}},
				}
			},
			completed: 2,
		},
		{
			what: "every cluster stopped part-way — a pull request merged for each one",
			setup: func() (*Orchestrator, []RegisterClusterRequest) {
				git := newMockGitProvider()
				git.mergeErr = fmt.Errorf("merge conflict")
				creds := &mockCredProvider{creds: map[string]*providers.Kubeconfig{
					"prod-eu": {Server: "https://eu.example.com:6443", CAData: []byte("ca"), Token: "tok"},
					"prod-us": {Server: "https://us.example.com:6443", CAData: []byte("ca"), Token: "tok"},
				}}
				orch := New(nil, creds, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
				return orch, []RegisterClusterRequest{
					{Name: "prod-eu", Addons: map[string]bool{"monitoring": true}},
					{Name: "prod-us", Addons: map[string]bool{"monitoring": true}},
				}
			},
			partly: 2,
		},
		{
			what: "one stopped part-way, one never got off the ground",
			setup: func() (*Orchestrator, []RegisterClusterRequest) {
				git := newMockGitProvider()
				git.mergeErr = fmt.Errorf("merge conflict")
				creds := &mockCredProvider{creds: map[string]*providers.Kubeconfig{
					"prod-eu": {Server: "https://eu.example.com:6443", CAData: []byte("ca"), Token: "tok"},
				}}
				orch := New(nil, creds, newMockArgocd(), git, autoMergeGitOps(), defaultPaths(), nil)
				return orch, []RegisterClusterRequest{
					{Name: "prod-eu", Addons: map[string]bool{"monitoring": true}},
					{Name: "prod-us", Addons: map[string]bool{"nonexistent-addon": true}},
				}
			},
			partly: 1, failed: 1,
		},
		{
			what: "every cluster failed outright",
			setup: func() (*Orchestrator, []RegisterClusterRequest) {
				orch := New(nil, &mockCredProvider{creds: map[string]*providers.Kubeconfig{}},
					newMockArgocd(), newMockGitProvider(), autoMergeGitOps(), defaultPaths(), nil)
				return orch, []RegisterClusterRequest{
					{Name: "prod-eu", Addons: map[string]bool{"nonexistent-addon": true}},
					{Name: "prod-us", Addons: map[string]bool{"nonexistent-addon": true}},
				}
			},
			failed: 2,
		},
	}

	for _, c := range cases {
		t.Run(c.what, func(t *testing.T) {
			orch, reqs := c.setup()
			result := orch.RegisterClusterBatch(context.Background(), reqs)

			// If the fixture stopped producing the shape, the rest of this
			// test proves nothing — say so rather than passing quietly.
			got := map[string]int{}
			for _, r := range result.Results {
				got[r.Status]++
			}
			if got["success"] != c.completed || got["partial"] != c.partly || got["failed"] != c.failed {
				t.Fatalf("the fixture produced %v, not %d success / %d partial / %d failed — "+
					"it no longer produces the shape this case exists for",
					got, c.completed, c.partly, c.failed)
			}

			o := result.Outcome
			if o.Completed != c.completed {
				t.Errorf("outcome.completed = %d, want %d", o.Completed, c.completed)
			}
			if o.PartlyCompleted != c.partly {
				t.Errorf("outcome.partly_completed = %d, want %d", o.PartlyCompleted, c.partly)
			}
			if o.Failed != c.failed {
				t.Errorf("outcome.failed = %d, want %d — this is the count of clusters where "+
					"NOTHING landed, which the wire never carried before", o.Failed, c.failed)
			}
			if o.Unrecognized != 0 {
				t.Errorf("outcome.unrecognized = %d, want 0", o.Unrecognized)
			}
			if o.Total != len(reqs) {
				t.Errorf("outcome.total = %d, want %d", o.Total, len(reqs))
			}

			// The older counters must not have moved. `failed` on the wire
			// still means "did not fully succeed" and still decides the 207.
			if result.Failed != c.partly+c.failed {
				t.Errorf("failed = %d, want %d — the wire meaning is deliberately unchanged: "+
					"hard failures AND partials", result.Failed, c.partly+c.failed)
			}
			if result.Succeeded+result.Failed != result.Total {
				t.Errorf("succeeded(%d) + failed(%d) != total(%d)", result.Succeeded, result.Failed, result.Total)
			}
			if result.Partial != c.partly {
				t.Errorf("partial = %d, want %d", result.Partial, c.partly)
			}

			// And the accurate trio must agree with them.
			if o.Completed != result.Succeeded {
				t.Errorf("outcome.completed(%d) and succeeded(%d) disagree", o.Completed, result.Succeeded)
			}
			if o.Failed != result.HardFailed() {
				t.Errorf("outcome.failed(%d) and HardFailed()(%d) disagree — the accurate count "+
					"and the subtraction clients had to do must give the same answer",
					o.Failed, result.HardFailed())
			}
		})
	}
}

// TestBatchOutcome_SummarizeIsARecountNotAnIncrement. Summarize is called by
// the orchestrator and again by the handler, deliberately, so the wire is
// right even if a future path builds a result some other way. That is only
// safe while it recounts.
func TestBatchOutcome_SummarizeIsARecountNotAnIncrement(t *testing.T) {
	r := &BatchResult{Results: []RegisterClusterResult{
		{Status: "success"}, {Status: "partial"}, {Status: "failed"},
	}}
	first := r.Summarize()
	second := r.Summarize()
	if first != second {
		t.Fatalf("calling Summarize twice gave %+v then %+v — it is adding, not recounting, "+
			"so the counts grow every time somebody asks for them", first, second)
	}
	if first.Completed != 1 || first.PartlyCompleted != 1 || first.Failed != 1 {
		t.Fatalf("counted %+v", first)
	}
}

// TestAdoptOutcome_SummarizeIsARecountNotAnIncrement — same for the adopt
// result, whose response body carried no aggregate counts at all before.
func TestAdoptOutcome_SummarizeIsARecountNotAnIncrement(t *testing.T) {
	r := &AdoptClustersResult{Results: []AdoptClusterResult{
		{Name: "a", Status: "success"},
		{Name: "b", Status: "partial"},
		{Name: "c", Status: "failed"},
		{Name: "d", Status: "skipped"},
	}}
	first := r.Summarize()
	if first != r.Summarize() {
		t.Fatal("Summarize is adding rather than recounting")
	}
	if first.Completed != 1 || first.PartlyCompleted != 1 || first.Failed != 1 || first.Unrecognized != 1 {
		t.Fatalf("counted %+v — \"skipped\" must land in unrecognized, never on the completed side", first)
	}
	if first != r.Outcome {
		t.Fatal("Summarize did not store what it returned")
	}
}
