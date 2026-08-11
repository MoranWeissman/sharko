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
// # Classification is by type, never by words
//
// credsafe.Is walks the error chain with errors.Is against a sentinel. It never
// reads an error's words. A word filter would silently stop matching the day a
// backend rephrased its errors — which is exactly how this bug class comes
// back — and it would miss a git or Kubernetes error that WRAPS a credentials
// error, which the marker travels through because the wrap uses %w.
//
// # What is deliberately NOT touched
//
// An error that did not come from a credentials backend keeps its text. Git and
// Kubernetes errors are a different risk, and blanket-redacting them would gut
// the audit trail for no gain. The only thing that changes is the credentials
// case — and a git error that wraps a credentials error IS the credentials
// case, because that is what the marker says.

// sanitize returns entry with credentials-backend error text replaced by the
// one fixed safe sentence, and with the typed cause cleared either way.
//
// The cause is cleared even when the entry is safe: a typed error hanging off a
// stored entry is a leak waiting for the next person who prints it with %+v,
// and json:"-" only hides it from one reader.
func sanitize(entry Entry) Entry {
	if credsafe.Is(entry.Cause) {
		if entry.Error != "" {
			entry.Error = credsafe.Message
		}
		if entry.Detail != "" {
			entry.Detail = credsafe.Message
		}
	}
	entry.Cause = nil
	return entry
}
