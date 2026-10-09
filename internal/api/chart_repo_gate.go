package api

// chart_repo_gate.go — the ONE place that answers a caller whose request
// needed something fetched over HTTP from a chart repository, a catalog
// source, or the configured AI provider, and could not get it (B13).
//
// # What was here before
//
// Ten handlers each wrote their own version of the same line: the error value
// from the outbound call, formatted straight into the response body. Two of
// them were named in the report; the other eight were found by asking the
// question the report's own reasoning implies — which OTHER responses are
// built from an error that could have come from an outbound HTTP call — and
// they leak exactly the same way.
//
// The error is a *url.Error, and net/url's stripPassword keeps the username.
// A token in a repository address is normally written in the username
// position, so the token came back whole in a 500 body. See
// internal/credsafe/chartrepo.go for the measured proof of that behaviour and
// for the sentences this file writes instead.
//
// # Why this cannot be misused
//
// writeChartRepoError takes an error, and it takes an operation — but the
// operation is a chartRepoOp, not a string. There is no parameter anywhere in
// the signature that an error's words can travel through: fmt.Sprintf-ing one
// into the body would have to go somewhere, and there is nowhere. The error is
// used for exactly one thing, and it is not a thing that reads its message.
//
// # The 502/504 distinction is kept, and kept where it already lived
//
// The previous story left this leak alone because killing it inside
// internal/helm.getIndex would mean dropping the %w chain that
// classifyUpstreamError walks to decide 502 (unreachable) from 504 (timed
// out), and an operator acts on that difference.
//
// That trade-off is not real, because it assumed the fix had to be at the
// source. The wrapped error is exactly what should keep travelling — it is the
// only thing that can carry the type information. What must stop is the
// FORMATTING of it into a body. So the chain is left completely intact, the
// handler still hands the whole wrapped error to classifyUpstreamError, and
// the classification is bit-for-bit the one it always was:
//
//	errors.Is ECONNREFUSED / errors.As *net.DNSError  -> 502
//	errors.As *url.Error with Timeout()               -> 504
//	rate-limit                                        -> 429
//	anything else                                     -> 500
//
// Every one of those branches is a TYPE or sentinel probe, the way
// credsafe.LogClass does it. None of them needs the message in the body, which
// is what makes "classify from the error, say a fixed sentence" possible at
// all. Nothing about the distinction is degraded; the operator gets the same
// status code, from the same function, off the same error value.
//
// # The log
//
// The error IS logged, and that is safe since B9: every record in the process
// goes through internal/logging's RedactHandler, which replaces an error value
// with credsafe.LogClass(err) — a description built only from sentinels and Go
// type names, never from the message. So the log says
// "chain=*fmt.wrapError>*url.Error>*net.OpError" and the operator learns the
// address would not dial, without the address.
//
// # Which sites route through here
//
// chart_repo_gate_guard_test.go holds the list, by file and function, and
// fails both ways: a new site that does not route through here, and a listed
// site that has gone away.

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// chartRepoOp names the outbound operation that failed. It is a type, not a
// string, so the set of things this file can say is fixed at compile time and
// a caller cannot widen it by passing different text.
type chartRepoOp int

const (
	// chartRepoListVersions — reading <repo>/index.yaml for a chart's
	// published version list.
	chartRepoListVersions chartRepoOp = iota

	// chartRepoFetchValues — downloading a chart archive to read its
	// values.yaml.
	chartRepoFetchValues

	// chartRepoUpgradeCheck — the upgrade impact analysis, which reads both
	// the current and the target chart.
	chartRepoUpgradeCheck

	// chartRepoRecommendations — the next-patch / next-minor / latest
	// suggestions.
	chartRepoRecommendations

	// chartRepoAISummary — the configured AI provider, whose base URL is
	// operator-supplied and carries the same risk.
	chartRepoAISummary
)

// chartRepoMessages maps each operation onto its fixed sentence. Keeping the
// sentences in internal/credsafe rather than here is the same rule the
// connection gate follows: what Sharko is allowed to say about credential
// material is decided in one package.
var chartRepoMessages = map[chartRepoOp]string{
	chartRepoListVersions:    credsafe.ChartRepoListVersionsMessage,
	chartRepoFetchValues:     credsafe.ChartRepoFetchValuesMessage,
	chartRepoUpgradeCheck:    credsafe.ChartRepoUpgradeCheckMessage,
	chartRepoRecommendations: credsafe.ChartRepoRecommendationsMessage,
	chartRepoAISummary:       credsafe.ChartRepoAISummaryMessage,
}

// chartRepoOpNames are the log-side names of the operations. They are written
// here in source, so like a Go type name nothing from a wire can get into one.
var chartRepoOpNames = map[chartRepoOp]string{
	chartRepoListVersions:    "list_chart_versions",
	chartRepoFetchValues:     "fetch_chart_values",
	chartRepoUpgradeCheck:    "check_upgrade",
	chartRepoRecommendations: "upgrade_recommendations",
	chartRepoAISummary:       "ai_summary",
}

// writeChartRepoError answers a request whose outbound fetch failed.
//
// The status comes from classifyUpstreamError, so 502-versus-504 is exactly
// what it was. The body is the fixed sentence for op. The error's words reach
// neither.
func writeChartRepoError(w http.ResponseWriter, r *http.Request, op chartRepoOp, err error) {
	// Sharko refusing the address is not an upstream failure and must not be
	// reported as one. Nothing was contacted, nothing went wrong out there —
	// the catalog entry names an address the technical preview does not
	// support, and the operator can fix that. So it gets a 422 and the rule
	// itself, rather than a gateway error telling them to check a network
	// that was never used.
	var unsupported *credsafe.UnsupportedRepoURLError
	if errors.As(err, &unsupported) {
		slog.Warn("chart repository address is not supported",
			"op", chartRepoOpNames[op],
			"method", methodForLog(r),
			"route", routeForLog(r),
			"status", http.StatusUnprocessableEntity)
		writeError(w, http.StatusUnprocessableEntity,
			chartRepoMessages[op]+" "+credsafe.UnsupportedRepoURLMessage)
		return
	}

	status := classifyUpstreamError(err)
	slog.Warn("outbound fetch failed",
		"op", chartRepoOpNames[op],
		"method", methodForLog(r),
		"route", routeForLog(r),
		"status", status,
		"error", err)
	writeError(w, status, chartRepoMessages[op])
}
