// catalog_v4_values_fetch.go — the ChartValuesFetcherFn wiring for
// AddToCatalog's v4 smart-values generation (v4 smartvalues wave).
//
// AddToCatalog (internal/orchestrator/catalog_ops.go) calls this once per
// addon it is about to scaffold values/global/<addon>.yaml for, with the
// entry's FINAL chart+version already resolved (including the
// from_marketplace "fill in the newest version" case). This file owns the
// one thing the orchestrator package deliberately does not: the HTTP
// fetch and the AI annotate call. Both mirror the existing v3 AddAddon
// door (internal/api/addons_write.go's handleAddAddon) exactly — same
// fetcher, same AnnotateValues call, same secret-leak handling — so this
// is a new WIRING site, not a new AI call site.
package api

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/MoranWeissman/sharko/internal/helm"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// chartValuesFetchTimeout bounds the upstream chart download the same way
// every other chart-metadata read in this package does (catalog_versions.go,
// catalog_remote.go, catalog_readme.go, catalog_repo_charts.go,
// catalog_validate.go, catalog_search.go, catalog_project_readme.go all use
// 8s) — a values.yaml fetch is the same class of call (one HTTP GET to a
// chart repo or registry) and gets the same budget.
const chartValuesFetchTimeout = 8 * time.Second

// fetchChartValuesForV4Catalog is the orchestrator.ChartValuesFetcherFn
// wired into AddToCatalog. A fetch failure is returned verbatim — the
// orchestrator's v4GenerateGlobalValues falls back to the honest
// fetch-failed stub, so the add never fails because of this. AI
// annotation runs only when a provider is configured and the "annotate
// on seed" toggle is effectively on, exactly the v3 AddAddon rule
// (s.aiClient.AnnotateOnSeedEnabled()); a secret-leak block downgrades to
// heuristic-only output (never fails the add) and gets its own audit
// entry, same as the v3 door.
func (s *Server) fetchChartValuesForV4Catalog(ctx context.Context, addonName, repoURL, chart, version string) (orchestrator.ChartValuesFetchResult, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, chartValuesFetchTimeout)
	defer cancel()

	raw, err := helm.NewFetcher().FetchValues(fetchCtx, repoURL, chart, version)
	if err != nil {
		return orchestrator.ChartValuesFetchResult{}, err
	}

	result := orchestrator.ChartValuesFetchResult{UpstreamValues: []byte(raw)}

	if s.aiClient == nil || !s.aiClient.AnnotateOnSeedEnabled() {
		return result, nil
	}

	annRes, annErr := orchestrator.AnnotateValues(ctx, result.UpstreamValues, chart, version, s.aiClient)
	if annErr != nil {
		var secretBlock *orchestrator.SecretLeakError
		if errors.As(annErr, &secretBlock) {
			slog.Warn("catalog add: ai annotate hard-blocked by secret guard; proceeding with heuristic-only",
				"request_id", logging.RequestID(ctx),
				"addon", addonName, "chart", chart, "version", version,
				"matches", len(secretBlock.Matches),
			)
			s.emitSecretLeakAuditBlock(ctx, "catalog_add", addonName, chart, version, secretBlock.Matches)
		}
		// Every other error class is already logged inside AnnotateValues
		// and returns the original bytes with a SkipReason — nothing
		// further to do here.
	}
	if annRes.SkipReason == "" {
		result.UpstreamValues = annRes.AnnotatedYAML
		result.AIAnnotated = true
		result.ExtraClusterSpecificPaths = annRes.AdditionalClusterPaths
	}
	return result, nil
}
