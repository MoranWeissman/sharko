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
// # A marked error SAYS the safe sentence. That is the whole point.
//
// Mark wraps an error and the wrapper's Error() returns the one fixed safe
// sentence — not the original text. So a boundary that forgets to ask cannot
// leak: the default answer is already the safe one. err.Error(), "%v", "%s",
// slog's error field, a log line somebody adds next year — all of them get the
// sentence.
//
// The real cause is NOT thrown away. It stays reachable through Unwrap, so
// errors.Is and errors.As still find everything underneath: a typed
// *providers.ArgoCDProviderError, a Kubernetes StatusError, the not-found
// marker below. What is gone is the ability to get at the original WORDS by
// accident.
//
// # Never match on text
//
// Is uses errors.Is against a sentinel. It never looks at an error's words.
// A filter that matched on words would silently stop matching the day a
// backend rephrased its errors, which is exactly how this bug class comes
// back. That also means a git or Kubernetes error that WRAPS a credentials
// error with %w is still caught — the marker travels with the cause.
//
// The same rule applies to callers. Nothing on a credential path may read a
// marked error's words to decide anything. When a caller needs to know WHY the
// fetch failed, the provider says so with a type: see ErrNotFound.
package credsafe

import "errors"

// ErrCredentialProvider is the marker. It is never returned on its own and
// its text is never shown to anybody; it exists so errors.Is can answer
// "did this come from a credentials backend?" without reading any words.
var ErrCredentialProvider = errors.New("credentials backend failure")

// ErrNotFound is the second marker: the credentials are simply not there.
//
// It exists because one caller genuinely needs to tell "the secret is missing"
// apart from every other credential failure — the cluster-test handler offers
// the operator a list of similarly-named secrets to pick from, and that offer
// only makes sense when the thing really is absent. That used to be decided by
// looking for the words "not found" in the error text, which is wrong twice
// over: it stops working the day a backend rephrases itself, and an
// AccessDenied whose message happens to contain "not found" got the same
// treatment as a genuinely missing secret.
//
// So the provider says it with a type instead, at the point where it actually
// knows: the AWS SDK returned ResourceNotFoundException, or
// apierrors.IsNotFound was true on the Kubernetes read. Nowhere else may
// infer it.
var ErrNotFound = errors.New("credentials not found at the configured source")

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
// The returned error's Error() is the fixed safe sentence — Message — and
// nothing else. The original cause stays reachable through Unwrap, so
// errors.Is and errors.As keep finding every typed error underneath; only the
// original WORDS stop being reachable by accident.
//
// nil in, nil out. Marking an ALREADY-marked error returns it unchanged.
//
// # Why "already marked" means the outermost error, not anywhere in the chain
//
// A mark is usually put on deep — the ArgoCD provider marks the Secret list
// every credential-serving method goes through, so no new method can be added
// without inheriting it — and then Sharko's own prefixes get wrapped around it
// on the way out ("listing argocd cluster secrets in namespace %q: %w").
//
// If Mark skipped whenever a mark existed somewhere below, the error leaving the
// exported boundary would be one of those fmt.Errorf wrappers, and its Error()
// would be Sharko's prefixes followed by the sentence. That text is safe — the
// backend's own words are already gone — but it is not the ONE fixed sentence,
// and "the same sentence for every failure" is the property that stops the
// sentence being a channel back to the cause.
//
// So Mark wraps again unless err is itself the wrapper. The boundary gets the
// last word, the chain underneath is untouched, and Mark(Mark(x)) is still
// Mark(x).
func Mark(err error) error {
	if err == nil {
		return nil
	}
	if _, alreadyOutermost := err.(*marked); alreadyOutermost {
		return err
	}
	return &marked{cause: err}
}

// MarkNotFound tags err as "the credentials are simply not there".
//
// Call it only where that is KNOWN — the backend said the secret does not
// exist. Never where it is merely likely: an access denial, a timeout or a
// parse failure must not carry this marker, because a caller acts on it.
//
// It composes with Mark in either order; the markers both travel through
// Unwrap, so a boundary that calls Mark on top of this keeps both answers.
//
// nil in, nil out. Marking twice is the same as marking once.
func MarkNotFound(err error) error {
	if err == nil {
		return nil
	}
	if IsNotFound(err) {
		return err
	}
	return &notFound{cause: err}
}

// Is reports whether err, or anything it wraps, came from a credentials
// backend.
func Is(err error) bool {
	return errors.Is(err, ErrCredentialProvider)
}

// IsNotFound reports whether err, or anything it wraps, says the credentials
// were not found at the configured source.
//
// This is the ONLY sanctioned way to ask that question. A substring search for
// "not found" on a credential path is a bug, and after Mark there are no words
// left to search anyway.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// Sentence is what a public boundary should say about err: the fixed safe
// sentence when the error came from a credentials backend, and the error's own
// text otherwise.
//
// Errors that did not come from a credentials backend are deliberately left
// alone. Blanket-redacting a git or Kubernetes error would empty out the audit
// trail and the operator-facing messages for no gain — those errors are a
// different risk. The only thing that changes is the credentials case.
//
// Since Mark now makes Error() say the sentence anyway, Sentence is belt and
// braces rather than the only guard. Keep calling it at boundaries: it is what
// makes the intent readable, and it covers the case where a git error WRAPS a
// credentials error, whose own outer words would otherwise come through.
func Sentence(err error) string {
	if err == nil {
		return ""
	}
	if Is(err) {
		return Message
	}
	return err.Error()
}

// Cause returns the real error underneath the credentials marker, or err
// unchanged when there is no marker anywhere in the chain.
//
// THIS IS FOR CLASSIFICATION ONLY, AND FOR NOTHING ELSE. The one legitimate
// use is a fixed lookup table that reads an error to pick one of a small set of
// Sharko-written outcomes and echoes no part of it — verify.ClassifyError and
// verify.AssumeRoleHint are the two, and they call it themselves so no caller
// has to remember.
//
// Using it to build a message, a log line, an API field or an audit entry is
// exactly the leak this package exists to stop. If you want words for a person,
// call Sentence.
//
// # It searches the whole chain, and what that costs
//
// A mark is usually applied deep — the ArgoCD provider marks its Secret list,
// and two layers of fmt.Errorf(..., %w) sit on top of it by the time the error
// leaves GetCredentials. So a plain type assertion on the outermost error finds
// nothing; this walks the tree.
//
// What comes back is the error the mark was put on, WITHOUT the Sharko-authored
// prefixes that were wrapped around it afterwards. That is the right answer for
// a classifier: every phrase the classification tables match on ("connection
// refused", "x509", "AssumeRole", "unauthorized", …) belongs to the upstream
// error, not to Sharko's wrapping.
func Cause(err error) error {
	if err == nil {
		return nil
	}
	if found := findMarked(err); found != nil {
		return Cause(found.cause)
	}
	return err
}

// findMarked walks err's tree — depth first, both Unwrap shapes — and returns
// the first *marked it meets, or nil.
func findMarked(err error) *marked {
	switch e := err.(type) {
	case *marked:
		return e
	case interface{ Unwrap() error }:
		return findMarked(e.Unwrap())
	case interface{ Unwrap() []error }:
		for _, inner := range e.Unwrap() {
			if found := findMarked(inner); found != nil {
				return found
			}
		}
	}
	return nil
}

// marked is Mark's wrapper.
//
// Error() says the fixed safe sentence. Unwrap returns TWO errors: the real
// cause, so errors.Is / errors.As keep reaching everything underneath, and the
// marker, so Is answers yes. Go's errors package walks both branches of a
// multi-error Unwrap.
type marked struct {
	cause error
}

func (m *marked) Error() string { return Message }

func (m *marked) Unwrap() []error { return []error{m.cause, ErrCredentialProvider} }

// notFound is MarkNotFound's wrapper.
//
// Error() passes the cause's text through unchanged. This wrapper is not the
// safety boundary — Mark is — and a not-found error that never reaches a
// credentials boundary (a test double, a future non-credential use) should not
// start claiming to be about credentials. What matters here is that ErrNotFound
// is reachable through Unwrap, including from under Mark.
type notFound struct {
	cause error
}

func (e *notFound) Error() string { return e.cause.Error() }

func (e *notFound) Unwrap() []error { return []error{e.cause, ErrNotFound} }
