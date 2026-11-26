// chart_version_seams.go — the two chart-repository lookups an upgrade and a
// catalog add need before they open a pull request, and the one place the
// errors of an upgrade are turned into an HTTP answer.
//
// The orchestrator owns the RULES (chart_version_check.go,
// values_follow_defaults.go). This file owns the network calls behind them,
// the same split catalog_v4_values_fetch.go uses.
package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/MoranWeissman/sharko/internal/helm"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// chartVersionExists is the real orchestrator.ChartVersionCheckFn.
//
// A chart repository that answers but does not list this chart at all is the
// same answer as "the version is not there": Sharko read the repository and
// what was asked for is not in it. Only a repository Sharko could not read is
// an error. An oci:// registry cannot be listed, so it is "not checkable".
func chartVersionExists(ctx context.Context, repoURL, chart, version string) (found, checkable bool, err error) {
	checkCtx, cancel := context.WithTimeout(ctx, chartValuesFetchTimeout)
	defer cancel()
	_, found, err = helm.NewFetcher().HasVersion(checkCtx, repoURL, chart, version)
	if err != nil {
		if errors.Is(err, helm.ErrOCIVersionCheckUnsupported) {
			return false, false, nil
		}
		if strings.Contains(err.Error(), "not found in repo") {
			return false, true, nil
		}
		return false, true, err
	}
	return found, true, nil
}

// chartDefaults is the real orchestrator.ChartDefaultsFn: a chart version's
// own values.yaml, untouched (no AI pass, no rewriting).
func chartDefaults(ctx context.Context, repoURL, chart, version string) ([]byte, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, chartValuesFetchTimeout)
	defer cancel()
	raw, err := helm.NewFetcher().FetchValues(fetchCtx, repoURL, chart, version)
	if err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

// wireChartChecks gives an orchestrator the two lookups. Tests replace them
// per Server (chartVersionCheckFn / chartDefaultsFn) so no test reaches the
// network.
func (s *Server) wireChartChecks(orch *orchestrator.Orchestrator) {
	check := s.chartVersionCheckFn
	if check == nil {
		check = chartVersionExists
	}
	defaults := s.chartDefaultsFn
	if defaults == nil {
		defaults = chartDefaults
	}
	orch.SetChartVersionCheck(check)
	orch.SetChartDefaultsFetcher(defaults)
}

// Codes the UI and CLI can branch on for an upgrade that was refused.
const (
	CodeVersionNotFound     = "version_not_found"
	CodeChartRepoUnreadable = "chart_repo_unreadable"
	CodeNothingToChange     = "nothing_to_change"
)

// writeUpgradeError maps an upgrade's errors onto honest status codes.
//
// Before this, every upgrade error was 502 "bad gateway" — a typo in a
// version, a cluster that does not run the addon, an addon that is not in
// the catalog. Those are the caller's to fix and are 4xx. 502 is kept for
// what it means: something Sharko talks to did not answer.
func writeUpgradeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, orchestrator.ErrChartVersionNotFound):
		writeCodedError(w, http.StatusBadRequest, CodeVersionNotFound, err.Error(), nil)
	case errors.Is(err, orchestrator.ErrChartVersionLookupFailed):
		writeCodedError(w, http.StatusBadGateway, CodeChartRepoUnreadable, err.Error(), nil)
	case errors.Is(err, orchestrator.ErrUpgradeNothingToDo):
		writeCodedError(w, http.StatusBadRequest, CodeNothingToChange, err.Error(), nil)
	case errors.Is(err, orchestrator.ErrUpgradeRequestInvalid):
		writeCodedError(w, http.StatusBadRequest, CodeInvalidRequest, err.Error(), nil)
	case errors.Is(err, orchestrator.ErrV4AddonNotInCatalog):
		writeCodedError(w, http.StatusNotFound, CodeNotInCatalog, err.Error(), nil)
	case orchestrator.IsV4RepoUnsupported(err), errors.Is(err, orchestrator.ErrMixedRepoLayout):
		writeCodedError(w, http.StatusConflict, CodeRepoLayout, err.Error(), nil)
	case errors.Is(err, orchestrator.ErrCatalogFileEmpty):
		writeCodedError(w, http.StatusUnprocessableEntity, CodeEmptyCatalogFile, err.Error(), nil)
	case err.Error() == "confirmation required: set yes: true in request body":
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}
