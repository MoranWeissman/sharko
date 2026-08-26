package orchestrator

import (
	"context"

	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/fanout"
)

// MaxBatchSize is the maximum number of clusters that can be registered in a single batch.
const MaxBatchSize = 10

// BatchResult holds the aggregate result of a batch cluster registration.
//
// A cluster can come back three ways, not two. It can succeed. It can fail
// before anything happened. Or it can come back "partial": the pull request
// landed, the cluster was registered, its Secrets were written — and then a
// later step did not finish. Nothing is rolled back, so a partial leaves real
// changes behind in Git and in the operator's cluster.
//
// This type used to carry only Succeeded and Failed, so a partial had to be
// forced into one of them, and it was counted as a failure. A batch where
// every cluster came back partial therefore looked, to anything reading these
// counters, exactly like a batch where nothing at all had happened.
type BatchResult struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	// Failed has always meant "did not fully succeed" on the wire — hard
	// failures AND partials. POST /api/v1/clusters/batch is a stable
	// endpoint and the HTTP status code is derived from this field, so its
	// meaning is deliberately left alone. Read HardFailed for the count of
	// clusters where genuinely nothing landed.
	Failed int `json:"failed"`
	// Partial is how many of Failed were partials — clusters that changed
	// something real before stopping. It is a SUBSET of Failed, not a
	// separate bucket, so Succeeded + Failed still equals Total.
	Partial int `json:"partial"`
	// Outcome is the accurate trio the wire never used to carry: how many
	// clusters fully completed, how many stopped part-way, and how many
	// failed outright with nothing landed. The older counters above are
	// untouched — `failed` still means "did not fully succeed" and still
	// decides the HTTP status — so a client that reads them keeps working,
	// and a client that wants the truth about outright failures no longer
	// has to do arithmetic to get it.
	//
	// Counted from results[].status by fanout.Count, which is the same
	// function the audit trail, the printed summary and the CLI's exit code
	// read. One count, four surfaces, no disagreement.
	Outcome fanout.Outcome          `json:"outcome"`
	Results []RegisterClusterResult `json:"results"`
}

// Summarize recounts the accurate trio from the per-cluster results and
// stores it on Outcome. Safe to call more than once — it is a recount, never
// an increment, so a caller can insist on a fresh answer without having to
// know who already called it.
func (r *BatchResult) Summarize() fanout.Outcome {
	statuses := make([]string, 0, len(r.Results))
	for _, res := range r.Results {
		statuses = append(statuses, res.Status)
	}
	r.Outcome = fanout.Count(statuses)
	return r.Outcome
}

// HardFailed is the number of clusters where nothing landed at all: no pull
// request, no Secret, no registration. It is Failed minus the partials.
func (r *BatchResult) HardFailed() int { return r.Failed - r.Partial }

// AnythingApplied reports whether the batch changed anything real anywhere. A
// partial counts, because a partial is not a rollback — the work it did do is
// still there.
func (r *BatchResult) AnythingApplied() bool { return r.Succeeded > 0 || r.Partial > 0 }

// RegisterClusterBatch registers multiple clusters sequentially.
// One failed cluster does not stop the rest from being processed.
func (o *Orchestrator) RegisterClusterBatch(ctx context.Context, requests []RegisterClusterRequest) *BatchResult {
	result := &BatchResult{Total: len(requests)}
	for _, req := range requests {
		clusterResult, err := o.RegisterCluster(ctx, req)
		if err != nil {
			result.Failed++
			// PUBLIC BOUNDARY. RegisterClusterResult.Error goes into the batch
			// response body. A credentials-backend failure gets the fixed
			// sentence; a git or ArgoCD failure keeps its text.
			result.Results = append(result.Results, RegisterClusterResult{
				Status:  "failed",
				Cluster: ClusterResult{Name: req.Name},
				Error:   credsafe.Sentence(err),
			})
			continue
		}
		if clusterResult.Status == "partial" {
			// Counted in BOTH: Partial records what really happened, Failed
			// keeps the wire meaning and the 207 status code exactly as they
			// have always been.
			result.Partial++
			result.Failed++
		} else {
			result.Succeeded++
		}
		result.Results = append(result.Results, *clusterResult)
	}
	result.Summarize()
	return result
}
