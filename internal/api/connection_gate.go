package api

// connection_gate.go — the ONE place that answers a caller who asked for the
// active Git or ArgoCD client and did not get one (B1).
//
// # What was here before
//
// Sixty-four handlers in this package each wrote their own version of the same
// two lines: a short prefix of Sharko's own naming which half of the
// connection was missing, then err.Error() appended to it, written out as a
// 502. (The exact prefixes are not repeated here — they are banned wording
// now, and banned_wording_sweep_test.go would find them in this comment just
// as readily as in a shipped string, which is the point of the sweep.)
//
// err there comes from building a client out of the saved connection, and one
// of the ways that fails is net/url refusing the repository URL. net/url's
// error value quotes in full the string it failed on, and a Git repository URL
// is routinely written with the token inside it
// (https://x-access-token:<token>@host/org/repo.git). So one unreadable repo
// URL in the saved connection put the repository's access token into a 502
// body that any signed-in caller at any permission level could ask for — from
// sixty-four different endpoints, including read-only ones.
//
// B4 fixed exactly this on two endpoints. This is the same fix for the other
// sixty-four, and it is one shared fix rather than sixty-four hand edits
// because they were one bug, not sixty-four bugs.
//
// # Why these two functions cannot be misused
//
// Neither takes an error, and neither takes a string. There is no parameter
// into which any caller could put an error's words — passing one is a compile
// error, not a lint warning, not a review catch. The sentence is a constant in
// internal/credsafe and is the same for every caller and every cause.
//
// The log line is the same shape: it names the HTTP method and the ROUTE
// PATTERN (net/http's own server-authored template, e.g.
// "POST /api/v1/clusters/{name}/adopt"), never the request path, so no
// caller-supplied text reaches the log either. The error value is not logged.
// This branch is reached when Sharko could not build a client, and on the Git
// side the reason it could not may BE the token — so there is no version of
// "just log it for debugging" that is safe here. What failed is named at the
// point of failure, inside internal/service.
//
// # Which sites route through here
//
// connection_gate_guard_test.go holds the list, by file and function, and
// fails both ways: a new site that does not route through here, and a listed
// site that has gone away.

import (
	"log/slog"
	"net/http"

	"github.com/MoranWeissman/sharko/internal/credsafe"
)

// writeNoActiveGitConnection answers a request that needed the active Git
// provider and could not have one.
//
// It writes 502 with the one fixed sentence and logs which route asked. It
// accepts nothing that could carry an error's text.
func writeNoActiveGitConnection(w http.ResponseWriter, r *http.Request) {
	slog.Warn("no usable Git connection for the active connection",
		"method", methodForLog(r), "route", routeForLog(r))
	writeError(w, http.StatusBadGateway, credsafe.NoActiveGitConnectionMessage)
}

// writeNoActiveArgocdConnection is the same for the ArgoCD half.
func writeNoActiveArgocdConnection(w http.ResponseWriter, r *http.Request) {
	slog.Warn("no usable ArgoCD connection for the active connection",
		"method", methodForLog(r), "route", routeForLog(r))
	writeError(w, http.StatusBadGateway, credsafe.NoActiveArgocdConnectionMessage)
}

// The 503 pair. Fifteen more sites — the ones B1's brief did not name, because
// they carried no prefix at all and so no wording sweep could ever have found
// them — answered the SAME failure with
// writeError(w, http.StatusServiceUnavailable, err.Error()): the same error
// value, the same leak, a bare body and a different status code.
//
// The status code is left exactly as each site had it. B1 is about what Sharko
// SAYS, not about which code it says it under; 502 and 503 are a contract the
// UI and the end-to-end tests already depend on, and quietly re-coding fifteen
// endpoints inside a security fix is how a security fix becomes a regression.
// Hence two more functions rather than a status argument: still no parameter
// anywhere that an error's words could travel through.

// writeNoActiveGitConnectionUnavailable is writeNoActiveGitConnection with the
// 503 those fifteen sites already returned.
func writeNoActiveGitConnectionUnavailable(w http.ResponseWriter, r *http.Request) {
	slog.Warn("no usable Git connection for the active connection",
		"method", methodForLog(r), "route", routeForLog(r), "status", http.StatusServiceUnavailable)
	writeError(w, http.StatusServiceUnavailable, credsafe.NoActiveGitConnectionMessage)
}

// writeNoActiveArgocdConnectionUnavailable is the ArgoCD half of the same.
func writeNoActiveArgocdConnectionUnavailable(w http.ResponseWriter, r *http.Request) {
	slog.Warn("no usable ArgoCD connection for the active connection",
		"method", methodForLog(r), "route", routeForLog(r), "status", http.StatusServiceUnavailable)
	writeError(w, http.StatusServiceUnavailable, credsafe.NoActiveArgocdConnectionMessage)
}

// routeForLog returns net/http's matched route pattern — the template the
// server itself registered, not anything the caller sent. It is empty when the
// handler was called directly rather than through the mux (which is what most
// tests do), and empty is fine: the method plus the message already say which
// half failed.
func routeForLog(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Pattern
}

// methodForLog returns the request method, or empty for a nil request. Both
// helpers are nil-safe so a non-handler caller cannot panic its way past the
// gate.
func methodForLog(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Method
}
