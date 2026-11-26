// Package orchestrator — "does this chart version exist?", asked BEFORE a
// pull request is opened.
//
// WHY (pre-release walk 4, "Sharko accepts a version that does not exist and
// then shows it as running"; walk 1 L11). An upgrade to 99.0.0, or to "abc",
// used to pass the preview and open a real pull request. Only after the merge
// did ArgoCD say the chart version was not found, and nothing in Sharko said
// why. The same hole was in add-to-catalog: a version that was never released
// went into catalog.yaml, and the values file blamed "network or registry
// error" for a download that failed because the version is not there.
//
// THE THREE ANSWERS a person can get, kept apart on purpose:
//
//  1. The version is in the chart repository: go on.
//  2. Sharko read the chart repository and the version is NOT in it: refuse,
//     name the version, say it was not found (ErrChartVersionNotFound).
//  3. Sharko could not read the chart repository: say THAT, and never blame
//     the version (ErrChartVersionLookupFailed).
//
// A chart repository Sharko has no way to list (an oci:// registry) is not
// checked at all — that is the long-standing "unknown, never an error" rule
// for those (helm.ErrOCIVersionCheckUnsupported).
package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// ChartVersionCheckFn answers whether repoURL publishes chart at version.
//
//	found == true              the version is there
//	found == false, err == nil the repository was read and the version is not in it
//	err != nil                 the repository could not be read
//
// checkable == false means "this kind of repository cannot be listed" (OCI);
// the caller then skips the check.
type ChartVersionCheckFn func(ctx context.Context, repoURL, chart, version string) (found, checkable bool, err error)

// SetChartVersionCheck wires in the chart-repository lookup. Optional: with
// none wired (every orchestrator unit test that does not care) the check is
// skipped, the same nil-means-off rule SetChartValuesFetcher follows.
func (o *Orchestrator) SetChartVersionCheck(fn ChartVersionCheckFn) {
	o.chartVersionCheckFn = fn
}

// ChartDefaultsFn returns a chart version's own values.yaml, untouched.
// Used to tell which settings in a seeded values file are still the old
// chart's defaults (values_follow_defaults.go).
type ChartDefaultsFn func(ctx context.Context, repoURL, chart, version string) ([]byte, error)

// SetChartDefaultsFetcher wires in the chart values.yaml download used when
// an upgrade compares the old and new chart defaults. Optional; nil means
// the comparison is skipped and the upgrade says so.
func (o *Orchestrator) SetChartDefaultsFetcher(fn ChartDefaultsFn) {
	o.chartDefaultsFn = fn
}

var (
	// ErrChartVersionNotFound: the chart repository was read and does not
	// have this version. The caller's mistake — a 400-class answer.
	ErrChartVersionNotFound = errors.New("chart version not found")
	// ErrChartVersionLookupFailed: the chart repository could not be read.
	// Not the caller's mistake — a 502-class answer.
	ErrChartVersionLookupFailed = errors.New("chart repository could not be read")
)

// ChartVersionNotFoundMessage is the sentence a person reads when they ask
// for a version the chart repository does not have.
func ChartVersionNotFoundMessage(addon, version, repoURL string) string {
	return fmt.Sprintf(
		"Version %s of %s was not found in its chart repository (%s). Nothing was changed. Pick a version that the repository lists.",
		version, addon, credsafe.SafeRepoURL(repoURL))
}

// ChartVersionLookupFailedMessage is the sentence a person reads when Sharko
// could not ask the chart repository. It does not say the version is wrong,
// because Sharko does not know that.
func ChartVersionLookupFailedMessage(addon, version, repoURL string) string {
	return fmt.Sprintf(
		"Sharko could not reach the chart repository of %s (%s), so it could not check that version %s exists. Nothing was changed. Try again in a moment.",
		addon, credsafe.SafeRepoURL(repoURL), version)
}

// checkChartVersion runs the lookup and returns nil, an
// ErrChartVersionNotFound or an ErrChartVersionLookupFailed. The error text
// is the finished sentence.
func (o *Orchestrator) checkChartVersion(ctx context.Context, addon, repoURL, chart, version string) error {
	if o.chartVersionCheckFn == nil {
		return nil
	}
	found, checkable, err := o.chartVersionCheckFn(ctx, repoURL, chart, version)
	if !checkable {
		return nil
	}
	if err != nil {
		// The cause goes to the log in the safe form only: the raw error of
		// a chart download repeats the whole address, sign-in details and
		// all.
		slog.Warn("chart version check: could not read the chart repository",
			"addon", addon, "chart", chart, "version", version,
			"repo", credsafe.SafeRepoURL(repoURL), "cause", credsafe.LogClass(err))
		return newUserFacingError(ErrChartVersionLookupFailed, ChartVersionLookupFailedMessage(addon, version, repoURL))
	}
	if !found {
		return newUserFacingError(ErrChartVersionNotFound, ChartVersionNotFoundMessage(addon, version, repoURL))
	}
	return nil
}

// userFacingError is an error whose text is a finished sentence for a
// person, with a sentinel behind it for the API layer to pick a status
// from. errors.Is(err, kind) works; err.Error() is the sentence alone, with
// no "sentinel: " prefix in front of it.
type userFacingError struct {
	kind error
	msg  string
}

func (e *userFacingError) Error() string { return e.msg }
func (e *userFacingError) Unwrap() error { return e.kind }

func newUserFacingError(kind error, msg string) error {
	return &userFacingError{kind: kind, msg: msg}
}
