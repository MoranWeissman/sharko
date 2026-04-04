package orchestrator

import "context"

// MaxBatchSize is the maximum number of clusters that can be registered in a single batch.
const MaxBatchSize = 10

// BatchResult holds the aggregate result of a batch cluster registration.
type BatchResult struct {
	Total     int                      `json:"total"`
	Succeeded int                      `json:"succeeded"`
	Failed    int                      `json:"failed"`
	Results   []RegisterClusterResult  `json:"results"`
}

// RegisterClusterBatch registers multiple clusters sequentially.
// One failed cluster does not stop the rest from being processed.
func (o *Orchestrator) RegisterClusterBatch(ctx context.Context, requests []RegisterClusterRequest) *BatchResult {
	result := &BatchResult{Total: len(requests)}
	for _, req := range requests {
		clusterResult, err := o.RegisterCluster(ctx, req)
		if err != nil {
			result.Failed++
			result.Results = append(result.Results, RegisterClusterResult{
				Status:  "failed",
				Cluster: ClusterResult{Name: req.Name},
				Error:   err.Error(),
			})
			continue
		}
		if clusterResult.Status == "partial" {
			result.Failed++
		} else {
			result.Succeeded++
		}
		result.Results = append(result.Results, *clusterResult)
	}
	return result
}
