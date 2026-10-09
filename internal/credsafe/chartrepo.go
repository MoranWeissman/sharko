package credsafe

// chartrepo.go — what Sharko is allowed to say when a call OUT to a chart
// repository, a catalog source or the configured AI provider fails (B13).
//
// # The carrier is Go's own error text, and it keeps the token
//
// net/http returns a *url.Error when a request cannot be made. Its Error()
// runs the address through net/url's stripPassword, and stripPassword replaces
// the PASSWORD only — it keeps the username. Measured, not assumed:
//
//	https://ghp_TOKEN@host/index.yaml
//	  -> Get "https://ghp_TOKEN@host/index.yaml": dial tcp ...
//
//	https://x-access-token:ghp_TOKEN@host/index.yaml
//	  -> Get "https://x-access-token:***@host/index.yaml": dial tcp ...
//
//	https://host/index.yaml?access_token=ghp_TOKEN
//	  -> Get "https://host/index.yaml?access_token=ghp_TOKEN": dial tcp ...
//
// A token is normally written in the USERNAME position, and a query parameter
// is a real shape too. Two of those three spellings hand the token back whole.
// So `writeError(w, 500, fmt.Sprintf("listing chart versions: %v", err))` is
// the repository's access token in a response body, and at the call site it
// looks like nothing more than a helpful error message.
//
// The address it dials is not the caller's own. It is the repoURL stored in
// the operator's catalog, so this is somebody else's saved secret being handed
// to whoever happens to be signed in — including a viewer, and including the
// browser's "Ask AI" button, which pastes the error text into a prompt and
// sends it to a third-party model.
//
// # A fixed sentence per operation, and no parameter for anything else
//
// These are constants. writeChartRepoError in internal/api picks one by a
// typed operation value, and takes no string and no error text — passing an
// error's words is a compile error, not something a reviewer has to catch.
//
// Each sentence names the operation that failed and the kind of thing it was
// talking to. That is what an operator needs: which step broke. WHY it broke
// is carried by the HTTP status, which is still classified from the error's
// TYPE — 502 unreachable, 504 timed out, 429 rate-limited — so the one
// distinction an operator acts on survives the sentence being fixed.

// The chart-repository sentences. Each is a complete sentence, in the
// operator's terms, naming the step that failed and nothing about the address.
const (
	// ChartRepoListVersionsMessage — fetching <repo>/index.yaml to list a
	// chart's published versions.
	ChartRepoListVersionsMessage = "Sharko could not read the list of versions from the chart repository."

	// ChartRepoFetchValuesMessage — downloading a chart to read its
	// values.yaml.
	ChartRepoFetchValuesMessage = "Sharko could not download the chart from its repository to read its values."

	// ChartRepoUpgradeCheckMessage — the upgrade impact analysis, which reads
	// both the old and the new chart from the repository.
	ChartRepoUpgradeCheckMessage = "Sharko could not work out what this upgrade changes, because it could not read the chart from its repository."

	// ChartRepoRecommendationsMessage — the next-patch / next-minor / latest
	// suggestions, which need the repository's version list.
	ChartRepoRecommendationsMessage = "Sharko could not suggest upgrade versions, because it could not read the list of versions from the chart repository."

	// ChartRepoAISummaryMessage — the configured AI provider. Its base URL is
	// operator-supplied too, so the same error text carries the same risk.
	ChartRepoAISummaryMessage = "Sharko could not get a written summary from the configured AI provider."
)
