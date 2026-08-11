// Package credsafe is the one place that decides whether an error came from
// a credentials backend, and the one place that owns the sentence Sharko says
// out loud when one did.
//
// # Why this exists
//
// A credentials backend's own error text is not safe to pass on. An AWS SDK
// error can carry credential material in its message — a wrapped presigned
// URL, a token fragment, a credential a provider chain put into its text — and
// the same is true of any future backend. So that text must not reach an API
// response, a log line, an audit entry, a Kubernetes event, or CLI output.
//
// # How it works, and why it is a marker and not a rewrite
//
// Mark wraps an error WITHOUT changing what Error() says. That is deliberate.
// Inside the process, callers still see the original text and the original
// typed errors: fmt.Errorf(... %w ...) chains keep working, errors.As still
// finds *providers.ArgoCDProviderError, and the "not found" substring check
// the cluster-test handler uses to offer secret-name suggestions still
// matches. Nothing about a function's contract with its internal callers
// changes. What changes is that every public boundary can now ASK "did this
// come from a credentials backend?" and answer with a fixed sentence instead
// of the text.
//
// # Never match on text
//
// Is uses errors.Is against a sentinel. It never looks at an error's words.
// A filter that matched on words would silently stop matching the day a
// backend rephrased its errors, which is exactly how this bug class comes
// back. That also means a git or Kubernetes error that WRAPS a credentials
// error with %w is still caught — the marker travels with the cause.
package credsafe

import "errors"

// ErrCredentialProvider is the marker. It is never returned on its own and
// its text is never shown to anybody; it exists so errors.Is can answer
// "did this come from a credentials backend?" without reading any words.
var ErrCredentialProvider = errors.New("credentials backend failure")

// Message is the fixed sentence that replaces a credentials backend's own
// error text at every public boundary.
//
// It is the SAME sentence for every underlying failure on purpose. A sentence
// that changed with the cause would be a channel back to the cause, and a
// caller could learn about the credential by watching which sentence came
// back. What tells two failures apart is the server-side log line — cluster,
// region, and which step failed — found by the request id.
const Message = "Sharko could not read this cluster's sign-in details from the configured credentials source. The server log for this request id says which step failed."

// Mark tags err as having come from a credentials backend.
//
// The returned error says exactly what err said — Error() is unchanged — so
// internal callers, %w chains, errors.As on typed provider errors, and
// existing substring checks all behave as before. Only the marker is added.
//
// nil in, nil out. Marking twice is the same as marking once.
func Mark(err error) error {
	if err == nil {
		return nil
	}
	if Is(err) {
		return err
	}
	return &marked{cause: err}
}

// Is reports whether err, or anything it wraps, came from a credentials
// backend.
func Is(err error) bool {
	return errors.Is(err, ErrCredentialProvider)
}

// Sentence is what a public boundary should say about err: the fixed safe
// sentence when the error came from a credentials backend, and the error's own
// text otherwise.
//
// Errors that did not come from a credentials backend are deliberately left
// alone. Blanket-redacting a git or Kubernetes error would empty out the audit
// trail and the operator-facing messages for no gain — those errors are a
// different risk. The only thing that changes is the credentials case.
func Sentence(err error) string {
	if err == nil {
		return ""
	}
	if Is(err) {
		return Message
	}
	return err.Error()
}

// marked is Mark's wrapper.
//
// Unwrap returns TWO errors: the real cause, so errors.Is / errors.As keep
// reaching everything underneath, and the marker, so Is answers yes. Go's
// errors package walks both branches of a multi-error Unwrap.
type marked struct {
	cause error
}

func (m *marked) Error() string { return m.cause.Error() }

func (m *marked) Unwrap() []error { return []error{m.cause, ErrCredentialProvider} }
