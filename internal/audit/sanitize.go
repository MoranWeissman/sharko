package audit

// sanitize.go — unsafe text never ENTERS the audit log, and no caller has a say
// in it.
//
// # Why the fix is here and not at the read side
//
// Entry.Error and Entry.Detail both go out over the wire. GET /api/v1/audit
// returns whole entries, GET /api/v1/audit/stream marshals each entry straight
// off the subscriber channel, and audit.list is open to the VIEWER role — the
// lowest one there is. So the audit log is a read-by-anyone surface.
//
// Hiding text on the way out would have been the wrong fix twice over: it would
// have needed doing separately in the list handler and the stream handler (and
// the stream would have been missed — it marshals the raw entry), and the
// unsafe text would still be sitting in memory for the next reader somebody
// adds. So the entry that gets STORED is already safe.
//
// # What changed, and why the old version was not enough
//
// This function used to read a caller-supplied bool:
//
//	if entry.CredentialFailure { entry.Error = credsafe.Message; entry.Detail = "" }
//
// Everything about that was conditional on the caller. Seventeen sites set the
// flag and sixteen decided by running credsafe.Is on a live error, so an entry
// was safe only if some upstream author had remembered to mark that error.
// Two sites recorded an ArgoCD error that nothing marks, so the flag was
// guaranteed false and the raw text was guaranteed to be stored. And because
// Detail was only cleared INSIDE the if, any writer that set Detail without
// setting the flag shipped its text verbatim.
//
// Now:
//
//   - Error is rebuilt from Reason on EVERY entry. Not "when the caller says
//     so" — always. Whatever was in the field beforehand is not read.
//   - Error is a SafeText, which no package outside this one can construct, so
//     there was nothing in the field beforehand in the first place.
//   - Reason is an enum, validated here against the catalog. A caller cannot
//     put a secret in an enum.
//   - Detail is decided by the sink's own reading of that validated enum, not
//     by a flag the caller passed in.
//
// The result: an unmarked provider error and a marked credentials error get the
// same treatment, because neither of them reaches this function. Only a
// category does.
//
// # Idempotency, without comparing any message text
//
// sanitize(sanitize(e)) == sanitize(e), and nothing here looks at a sentence to
// decide that. Error is a pure function of Reason — sentenceFor is a map lookup
// — and sanitize does not modify Reason except to blank an invalid one, which
// is itself idempotent. Run it twice and the same lookup happens twice.
//
// That is why "already safe" is not a question this code has to answer. There
// is no state to detect: the output does not depend on the input's Error field
// at all.
//
// # The useful category survives
//
// An entry still says what kind of thing went wrong — Reason is on the wire,
// and Error is a whole sentence naming the category and pointing at the server
// log. Together with Event, Action, Resource, Result and Changes, a reader
// learns which operation failed, on what, and what sort of failure it was. What
// is gone is only the backend's own words, which go to the server log at every
// call site and stay there.

// sanitize returns entry ready to store.
//
// It is called by Add before the ring append AND before the SSE fan-out, so one
// pass covers the table and the live stream.
func sanitize(entry Entry) Entry {
	// A reason nobody defined tells a reader nothing and could be anything, so
	// it does not survive. This runs before the sentence lookup so an invented
	// reason cannot select a sentence either.
	if !entry.Reason.Valid() {
		entry.Reason = ""
	}

	// UNCONDITIONAL. Every entry, every time, from the catalog and from
	// nothing else. There is no branch here that can be taken by a caller, and
	// no path on which the incoming Error value is read.
	entry.Error = sentenceFor(entry.Reason)

	// Also unconditional: the sink asks its own validated enum whether this
	// category of failure is allowed a free-text detail. The old version asked
	// the caller's bool, and only inside an if — which is how a Detail writer
	// that set no flag shipped its text.
	if entry.Reason.hidesDetail() {
		entry.Detail = ""
	}

	return entry
}
