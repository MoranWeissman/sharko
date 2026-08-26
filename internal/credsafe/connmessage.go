package credsafe

// connmessage.go — the two fixed sentences Sharko says when it cannot build a
// usable Git or ArgoCD client out of the saved connection (B1).
//
// # Why these live here and not in internal/api
//
// They were born in internal/api/init.go (B4), which was the right place when
// exactly two handlers said them. B1 routes sixty-four more call sites and
// four sites in internal/remediation through the same answer, and
// internal/remediation cannot import internal/api. A second copy of a fixed
// sentence is how two sentences drift apart, so both moved down here instead.
//
// credsafe is the right floor for them. The package already owns the question
// "what is Sharko allowed to say out loud about credential material", and
// repourl.go in this package already established that a Git repository URL IS
// credential material — it is routinely written with the token inside it. The
// error these sentences replace is, in the worst case, exactly that URL:
// internal/service/connection.go's deriveGiteaBaseURL calls url.Parse on the
// saved repo URL, and net/url's error value quotes in full the string it
// failed on. That error is wrapped twice and handed back by
// GetActiveGitProvider, so every handler that appended err.Error() to a 502
// was handing the repository's access token to any signed-in caller.
//
// # Why the sentence never varies with the cause
//
// Same rule as Message above: a sentence that changed with the cause would be
// a channel back to the cause. Every reason the build can fail — no active
// connection saved, no token, a repo URL Sharko cannot read, a config store
// that would not answer — gets this one sentence, because the operator's next
// move is the same in all of them: go to Settings and look at the connection.
// What tells two failures apart is the server-side log line, found by the
// request id.
//
// # Why there are two of them and not one
//
// The Git half and the ArgoCD half send an operator to two different fields on
// the same screen. Collapsing them would make the message safe and useless at
// the same time, which is the trade B4 already refused for the 401/403 pair.
const (
	// NoActiveGitConnectionMessage is said when Sharko cannot build a usable
	// Git provider for the active connection.
	NoActiveGitConnectionMessage = "Sharko has no usable Git connection. Open Settings and check the active connection: the Git provider, the repository it points at, and the access token."

	// NoActiveArgocdConnectionMessage is the same thing for the ArgoCD side of
	// the active connection.
	NoActiveArgocdConnectionMessage = "Sharko has no usable ArgoCD connection. Open Settings and check the active connection: the ArgoCD server address and the ArgoCD token."
)

// ErrNoActiveGitConnection and ErrNoActiveArgocdConnection are the error form
// of the same two sentences, for the handful of call sites that RETURN an
// error instead of writing a response.
//
// They carry the sentence and nothing else — no %w wrap of the cause. That is
// deliberate and it is the point: a wrapped cause is exactly what the sixty-four
// sites were leaking, and every consumer of these two values does the same
// thing with them (prints them at a boundary or logs them). Keeping the cause
// reachable would buy nothing and would put the token one fmt.Errorf away from
// coming back. The cause is named on the server-side log line at the point of
// failure instead.
var (
	ErrNoActiveGitConnection    = constError(NoActiveGitConnectionMessage)
	ErrNoActiveArgocdConnection = constError(NoActiveArgocdConnectionMessage)
)

// constError is a string that is an error. It has no cause and no Unwrap, so
// there is nothing underneath it for a later fmt.Errorf to drag back out.
type constError string

func (e constError) Error() string { return string(e) }
