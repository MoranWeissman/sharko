package credsafe

// sourcelabel.go — what Sharko is allowed to say out loud about the address
// of a catalog source, anywhere outside the process.
//
// # The rule: the address is sensitive because of WHAT IT IS, not what is in it
//
// An operator points Sharko at extra catalogs with SHARKO_CATALOG_URLS, and a
// private catalog is addressed by writing a token into the address's own path
// — that variable exists so such an address need not be committed to Git. So
// a credential inside one of these strings is the expected shape, not an
// accident.
//
// An earlier version of this file asked the URL grammar in repourl.go a
// question about each address: can it vouch that this one carries no
// credential? That question has been withdrawn for this field, because the
// documented private-catalog shape puts the token in the PATH —
// /private/<token>/catalog.yaml — and no grammar can tell that apart from
// /private/stable/catalog.yaml. An address that LOOKS clean can still be the
// key to someone's private catalog. So nothing inspects the address any more:
// the configured catalog source address is treated as sensitive, full stop.
//
// Sharko keeps the raw address inside the process, where it is needed — for
// the fetch itself, and as the key that tells one source's snapshot from
// another's. On every outward surface — a metric label, a log field, an API
// response, an audit record — the address is replaced by one fixed word.
//
// # Why the answer is a fixed word and not a short hash
//
// A short hash looks harmless: it is one-way, and it tells one source apart
// from another. But it is also the same value every time for the same
// address. Someone who has a list of possible tokens can hash each candidate
// address themselves and see which one matches what Sharko publishes. That
// turns the published value into a way of checking guesses offline, which is
// exactly what must not be handed out. The same objection covers a length, a
// first-few-characters, and any mask whose width follows the address.
//
// # What that costs, said plainly so nobody is surprised by it
//
// Every configured source gets the SAME word. On a metric that means they
// share one line: three sources failing produce one series counting three,
// not three series counting one each. The count is still true — it is the
// whole truth across all of them together — but it does not say how many
// separate addresses are behind it. On the sources page and next to each
// third-party addon, the operator sees the same word on every row and cannot
// tell one configured source from another. The metric Help text and the
// operator documentation both say so, because an operator reading a
// dashboard would otherwise think one source failed when three did.

// RedactedSourceLabel is the fixed word Sharko publishes in place of a
// catalog source address. It is the same for every address: it is not
// derived from the address in any way, so nothing about the address can be
// worked back out of it, and every configured source shares it.
const RedactedSourceLabel = "redacted"

// PublicSourceLabel is the one way a catalog source address becomes text on
// any outward surface — a metric label, a log field, an API response, an
// audit record.
//
// It takes no argument on purpose. There is no question left to ask about
// the address, so handing it in would only tempt a future caller to look at
// it. The answer is always the fixed word, and the compiler — not a
// reviewer's attention — finds any call site that still thinks the address
// matters.
func PublicSourceLabel() string { return RedactedSourceLabel }
