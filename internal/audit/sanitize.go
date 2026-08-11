package audit

import "github.com/MoranWeissman/sharko/internal/credsafe"

// sanitize.go — unsafe text never ENTERS the audit log.
//
// # Why the fix is here and not at the read side
//
// Entry.Error and Entry.Detail are both public. GET /api/v1/audit returns whole
// entries, GET /api/v1/audit/stream marshals each entry straight off the
// subscriber channel, and audit.list is open to the VIEWER role — the lowest
// one there is. So the audit log is a read-by-anyone surface, and a
// credentials-backend error's text does not belong in it.
//
// Hiding the text on the way out would have been the wrong fix twice over: it
// would have needed doing separately in the list handler and the stream
// handler (and the stream would have been missed — it marshals the raw entry),
// and the unsafe text would still be sitting in memory for the next reader
// somebody adds. So the entry that gets STORED is already safe.
//
// # Classification happens at the call site, never here, and never by words
//
// The decision arrives as Entry.CredentialFailure, a bool the credential
// boundary set by calling credsafe.Is on the live typed error. credsafe.Is
// walks the chain with errors.Is against a sentinel and never reads an error's
// words. A word filter would silently stop matching the day a backend rephrased
// its errors — which is exactly how this bug class comes back — and it would
// miss a git or Kubernetes error that WRAPS a credentials error, which the
// marker travels through because the wrap uses %w.
//
// # What is deliberately NOT touched
//
// An entry that is not about a credentials-backend failure keeps its text. Git
// and Kubernetes errors are a different risk, and blanket-redacting them would
// gut the audit trail for no gain. The only thing that changes is the
// credentials case — and a git error that wraps a credentials error IS the
// credentials case, because that is what the marker says and what the call site
// asked credsafe.Is about.

// sanitize returns entry ready to store: for a credentials-backend failure,
// Error is the one fixed safe sentence and Detail is EMPTY. The flag is cleared
// either way.
//
// DETAIL IS EMPTIED, NOT REPLACED. Putting the fixed sentence in both fields
// said the same thing twice and made the row look like it carried two separate
// pieces of information when it carried one. The single answer lives in Error.
//
// The flag is cleared even when the entry is safe, so a stored entry says
// nothing about how it was classified — and so a future reader that starts
// trusting the field cannot be fed a stale true.
func sanitize(entry Entry) Entry {
	if entry.CredentialFailure {
		entry.Error = credsafe.Message
		entry.Detail = ""
	}
	entry.CredentialFailure = false
	return entry
}
