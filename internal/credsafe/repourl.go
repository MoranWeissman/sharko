package credsafe

// repourl.go — the second kind of credential material this package is asked
// about: an address that can carry a token inside it.
//
// # Why an address belongs in this package
//
// The rest of credsafe is about a credentials backend's ERROR TEXT. This is
// about a value that is not an error at all, and it is here for the same
// reason: it is credential material, and the package's job is owning what
// Sharko is allowed to say out loud about credential material. Two places
// deciding that independently is how one of them ends up wrong.
//
// A Git repository address is routinely written with the credential inside it:
//
//	https://x-access-token:<token>@github.example/org/repo.git
//	https://<personal-access-token>@github.example/org/repo.git
//	https://user:<password>@gitea.example/org/repo.git
//
// Whatever created the address — an operator pasting it into ArgoCD, a CI job
// building it from a secret — the token travels with it. So any code that
// echoes an address into a response body, a log line, an audit entry or an
// operation message is echoing a secret, and it never looks like one at the
// call site: it looks like a helpful "which repo failed?".
//
// # Why the answer has three states and not two (BF12)
//
// This file used to answer with a bool, and the bool said "no credential" in
// three different situations: the string held none of "@", "?" or "#"; net/url
// refused to parse it; or the parse produced no scheme and no host. Only the
// first of those is a real answer. The other two are "I could not tell", and
// spelling "I could not tell" as false meant every caller read it as safe.
//
// That failed OPEN, and it failed open in production. A scheme-less address
// such as user:PASSWORD@git.example/o/r.git was declared credential-free and
// written into a Kubernetes Deployment in the clear, and an address whose port
// was not a number was declared credential-free, saved to disk, and then
// quoted back on a terminal by the transport error it caused.
//
// So the answer is now AddressVerdict, with three states, and its ZERO VALUE
// is the unsafe one. A caller may act on an address only after explicitly
// receiving AddressCredentialFree. There is no bool, no IsSafe() helper and no
// wrapper that turns three states back into two, because that collapse is
// exactly the defect.
//
// # The grammar: exactly two shapes, and nothing else (BF13)
//
// The way an address was read used to let a MISTYPED scheme through. "//" was
// put in front of whatever had been written, and net/url then read the broken
// scheme fragment as the AUTHORITY. The whole of the operator's user
// information landed in the PATH, where an "@" is ordinary and allowed, so the
// address came back credential-free and was handed on exactly as written.
//
// There is no one way to mistype a scheme, so there is no list of mistyped
// schemes here and there must never be one. Some of them are turned away
// because what is left still carries the colon and reads as a host with an
// empty port — one slash instead of two, a digit in front, an underscore in
// the middle, all end up there. The one where the COLON is left out does not:
// "https//git.example/o/r" leaves "https", which is a perfectly good host
// name, and that shape went on being accepted after the other three were
// closed (BF13-6). It is refused now by a rule about the SHAPE — a second "//"
// straight after the authority, in an address that had no "//" of its own —
// rather than by anything that recognises the text of a scheme.
//
// So what there is instead is a grammar with exactly two accepted shapes, and
// everything that is not one of them is refused:
//
//	Form 1 — a hierarchical URL whose valid scheme starts at the very first
//	         byte and is followed by "://".
//	Form 2 — no scheme at all: a network reference that becomes a valid
//	         authority, plus an optional path, when it is read with a SINGLE
//	         leading "//".
//
// An address already written with its own leading "//" is form 2 as it
// stands and is read as it stands. Putting a second "//" in front of it would
// turn "////user:pw@host" into a path and make the user information vanish.
//
// Form 2 is what makes "localhost:8080" work. Parsed bare, net/url reads that
// as scheme "localhost" with the opaque part "8080" — a reading that names no
// host and shows no user information, so it looks credential-free while
// proving nothing. Read as "//localhost:8080" it is a host and a port, which
// is what an operator meant.
//
// # Scheme validity is SYNTAX, not a list of protocols
//
// A valid scheme is one letter followed by any number of letters, digits,
// "+", "-" or ".". That is all. This package is a generic safety boundary and
// must be able to say something honest about oci://, git+ssh://, file:// and
// ftp:// on structure alone. Which protocols a particular Sharko feature
// supports is that feature's business, decided next to the transport that has
// to speak them, and writing a protocol list in here would put the two rules
// in two places — which is how the first version of this file went wrong.
//
// # What has to hold for either form
//
// All of these, or the address is refused:
//
//   - a valid, non-empty host;
//   - no user information, INCLUDING an empty user information section, so
//     "https://@git.example/o/r" is refused as well;
//   - no query string;
//   - no fragment;
//   - no leading or trailing whitespace, and no raw control character
//     anywhere in the address;
//   - no malformed, ambiguous or opaque reading;
//   - no port that is empty, not made of digits, or above 65535.
//
// "A valid host" here means a host this grammar could read whole: non-empty,
// and with no percent escape smuggling user information through it. It does
// NOT mean a name that is valid DNS. Sharko does not check DNS label lengths,
// leading hyphens, unusual numeric IP spellings or internationalised names,
// and this comment must not claim it does.
//
// # Percent signs are judged by where they sit, never by what they spell
//
//   - a percent escape must not be able to carry user information through an
//     unbracketed authority or hostname, so a "%" that survives into an
//     unbracketed host is refused;
//   - "%" is not banned outright: a bracketed IPv6 zone identifier is written
//     "[fe80::1%25eth0]" and needs one;
//   - a well-formed percent escape inside an ordinary path stays path data.
//
// # Order matters, and the order is: read it whole, THEN look for a credential
//
// Every structural check above runs BEFORE the question "is there a credential
// in it". That is deliberate. It means "carries a credential" is only ever
// returned for an address that is otherwise entirely readable, which is what
// gives the display helper something worth stripping. It is NOT on its own a
// promise that what is left after the strip is a valid, credential-free
// address — the strip builds a new string, and this file used to claim the
// promise anyway. So the display helper asks the grammar about that new string
// too, and shows nothing unless the answer is credential-free. An address that
// is both unreadable and credential-bearing comes back as unreadable, and
// nothing of it is shown at all.
//
// # What is refused, in plain words
//
// User information in the authority, any query, any fragment — and anything
// that cannot be read whole. A non-numeric port fails to parse, so it is
// refused before it can be saved and long before it reaches http.NewRequest.
//
// An "@" inside a path is not user information: github.com/org/repo@v1 reads
// as host github.com and path /org/repo@v1, and it stays valid.
//
// The scp-style Git remote git@github.com:org/repo.git cannot be read at all
// (":org" is not a port), so it is refused. Sharko has never supported that
// shape — every address ParseRepoURL accepts is an https one — so refusing it
// costs no working installation, and in a log line it is blanked rather than
// printed. Support for it is not being added.
//
// A query is refused even when it looks harmless. "?ref=main" has no token in
// it and this file cannot tell the difference; classification here is
// structural and never a scan for text that looks secret. The cost is a
// stripped "?ref=main" in a log line. The alternative cost is a token in one.

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// credentialBearingRunes are the three characters a credential needs in order
// to be somewhere in an address: "@" ends a userinfo section, "?" starts a
// query and "#" starts a fragment. A string with none of them has no place to
// hide one.
const credentialBearingRunes = "@?#"

// ContainsCredentialCarrierRune reports whether a string contains any of the
// three characters a credential needs in order to be somewhere in an address.
//
// This is NOT a safety verdict and must never be used as one. It answers one
// narrow question — "is there even a place in this text for user information,
// a query or a fragment?" — and the only thing a false answer licenses is
// leaving a value alone that provably has none of those three parts. Whether
// an address may be used, saved or shown is ClassifyAddress's decision, and
// ClassifyAddress alone.
//
// It exists because the log sink runs over every string in every log line, and
// most of those are ordinary words. Asking this first keeps the sink silent on
// text that could not carry a credential if it tried, without giving the sink
// a character rule of its own to get wrong.
//
// Since BF13 the classifier does not use it at all. ClassifyAddress is purely
// a grammar now, and this stayed a character test, so mixing the two would put
// a character rule back inside a structural decision — which is where the last
// two holes were. The log sink is its only caller, and its only job there is
// the cheap "there is provably nothing to be about" gate described above.
func ContainsCredentialCarrierRune(s string) bool {
	return strings.ContainsAny(s, credentialBearingRunes)
}

// AddressVerdict is what credsafe will say about one address.
//
// The zero value is AddressUnclassifiable on purpose. A struct field nobody
// filled in, a variable declared and not assigned, a future state this build
// has never heard of — all of them read as "do not use this", which is the
// only default that cannot turn a bug into a printed credential.
type AddressVerdict int

const (
	// AddressUnclassifiable means Sharko could not read the address well
	// enough to say anything about it: it did not parse, it parsed only as
	// an opaque reference, or it has no authority to inspect and holds one
	// of the three characters a credential can hide behind. Callers refuse.
	//
	// This is the zero value, so an unset verdict is a refusal.
	AddressUnclassifiable AddressVerdict = iota

	// AddressCredentialFree means Sharko read the whole address and there
	// is no user information, no query and no fragment in it. This is the
	// ONE state that lets a caller go ahead.
	AddressCredentialFree

	// AddressCarriesCredential means Sharko read the address and it has
	// somewhere in it for a credential to sit — user information before an
	// "@", a query, or a fragment. It never means a secret was recognised;
	// this package does not guess at what text looks secret.
	AddressCarriesCredential
)

// String names the verdict for a programmer reading a test failure. It never
// names or quotes the address it came from.
func (v AddressVerdict) String() string {
	switch v {
	case AddressCredentialFree:
		return "credential-free"
	case AddressCarriesCredential:
		return "carries a credential"
	case AddressUnclassifiable:
		return "unclassifiable"
	default:
		return "unclassifiable (unknown verdict)"
	}
}

// maxPort is the largest port number there is. A port above this is out of
// range and the address is refused.
const maxPort = 65535

// schemeRe matches form 1: a syntactically valid hierarchical scheme starting
// at the very first byte and followed by "://".
//
// A scheme is one letter followed by any number of letters, digits, "+", "-"
// or ".". Validity here is SYNTAX only. There is deliberately no list of
// allowed protocols: this package is a generic safety boundary and has to
// classify oci://, git+ssh://, file:// and ftp:// on structure alone, exactly
// as it classifies https://. A feature whose transport only speaks certain
// protocols restricts them next to that transport, not here.
var schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*://`)

// addressReading is one address, read the one way credsafe is willing to read
// it, together with how it was written so it can be rendered back the same way.
type addressReading struct {
	u *url.URL
	// explicit is true when the address carried its own "<scheme>://".
	explicit bool
	// network is true when the address was written starting with "//" and
	// therefore already was a network-path reference.
	network bool
}

// readAddress applies the grammar to raw exactly once and returns both the
// reading and the verdict. Every question anyone asks about an address is
// answered from this one call, so what is judged and what is shown can never
// come apart.
//
// The structural checks all run BEFORE the credential question, so
// AddressCarriesCredential is only ever returned for an address that is
// otherwise readable whole. That is what gives SafeRepoURL something worth
// stripping; what makes the strip safe is that SafeRepoURL then puts its own
// result back through this same function and shows it only on a
// credential-free answer.
//
// When the verdict is not AddressCredentialFree or AddressCarriesCredential,
// the returned reading is the zero value and holds no url.URL at all, so there
// is nothing for a caller to accidentally render.
func readAddress(raw string) (addressReading, AddressVerdict) {
	var nothing addressReading

	if raw == "" {
		// "Not configured yet" is a different condition and belongs to the
		// caller, in the caller's own words. It is not a safe address.
		return nothing, AddressUnclassifiable
	}

	// Whitespace at either end, and any raw control character anywhere. A
	// raw newline is the shape that gets through when a reader stops at the
	// first line and treats what follows as a separate, safe value.
	if strings.TrimSpace(raw) != raw {
		return nothing, AddressUnclassifiable
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return nothing, AddressUnclassifiable
		}
	}

	// How is this address written? There are three branches here, and the
	// third one is not a catch-all that accepts what the first two did not:
	// it is how form 2 is READ. An address with no scheme and no "//" of its
	// own is what form 2 looks like when an operator types it, so a single
	// "//" is put in front of it and it is then held to every check below,
	// exactly like the other two. Nothing is waved through for having reached
	// this branch, and the extra check right after the parse is there because
	// this branch is the one that has to be sure the operator's first segment
	// really was meant as a host.
	var reading addressReading
	var toParse string
	switch {
	case schemeRe.MatchString(raw):
		reading.explicit = true
		toParse = raw
	case strings.HasPrefix(raw, "//"):
		// Already a network-path reference. Adding another "//" would make
		// "////user:pw@host" parse as a path and the user information would
		// silently disappear.
		reading.network = true
		toParse = raw
	default:
		toParse = "//" + raw
	}

	u, err := url.Parse(toParse)
	if err != nil || u == nil {
		// A string net/url cannot read is precisely the case where a token
		// could be anywhere in it. It is never credential-free.
		return nothing, AddressUnclassifiable
	}
	if u.Opaque != "" {
		// An opaque reading names no host and shows no user information, so
		// it would look credential-free while proving nothing.
		return nothing, AddressUnclassifiable
	}
	reading.u = u

	// A valid, non-empty host. This is the check the mistyped-scheme leak
	// walked around: with the scheme broken, the fragment of it became the
	// authority and the operator's user information became path text.
	host := u.Hostname()
	if host == "" {
		return nothing, AddressUnclassifiable
	}

	// A second "//" straight after the authority, in the one branch where the
	// first "//" was credsafe's own doing.
	//
	// An address written with no scheme is a host, an optional port and an
	// optional path, and a path in it never opens a second authority. When a
	// second "//" is there, what the operator wrote was a scheme with its
	// colon left out — "https//git.example/o/r" — and the first segment is
	// that scheme rather than a host anyone meant to reach. Every other way
	// of mistyping a scheme is turned away by a check further down, because
	// what is left of the scheme still carries its colon and reads as a host
	// with an empty port. This one does not: "https" on its own is a
	// perfectly good host name, so the whole of the operator's user
	// information lands in the path, where an "@" is ordinary and allowed for
	// the sake of github.com/org/repo@v1, and the address came back
	// credential-free and was handed on exactly as written.
	//
	// The other two branches are deliberately left alone. There the operator
	// wrote the "//" that opens the authority themselves, so where the
	// authority ends is not in doubt and a doubled slash after it is ordinary
	// path text.
	if !reading.explicit && !reading.network && strings.HasPrefix(u.EscapedPath(), "//") {
		return nothing, AddressUnclassifiable
	}

	// Percent escapes are judged by where they sit. Inside brackets a "%" is
	// how an IPv6 zone identifier has to be written, and net/url has already
	// checked that spelling. Outside brackets there is no legitimate reason
	// for one to survive into a hostname, and an escape that does is a way to
	// write user information the authority reader will not recognise as user
	// information.
	if !strings.HasPrefix(u.Host, "[") && strings.Contains(host, "%") {
		return nothing, AddressUnclassifiable
	}

	// The port. net/url accepts a bare ":" with nothing after it and accepts
	// any number of digits, so neither "empty" nor "out of range" is caught
	// by parsing and both are checked here.
	if port, written := portSection(u.Host); written {
		if port == "" {
			return nothing, AddressUnclassifiable
		}
		n := 0
		for _, c := range port {
			if c < '0' || c > '9' {
				return nothing, AddressUnclassifiable
			}
			n = n*10 + int(c-'0')
			if n > maxPort {
				return nothing, AddressUnclassifiable
			}
		}
	}

	// The address has now been read whole. Only at this point is it worth
	// asking whether there is anywhere in it for a credential to sit.
	if u.User != nil {
		// Non-nil covers the empty section too: "https://@git.example/o/r"
		// has a user information section, and the rule refuses the section
		// being there at all, whatever is written inside it.
		return reading, AddressCarriesCredential
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" {
		return reading, AddressCarriesCredential
	}
	return reading, AddressCredentialFree
}

// portSection returns what is written after the host's ":", and whether a ":"
// was written at all.
//
// url.URL.Port() cannot answer this: it returns "" both for "git.example",
// which has no port, and for "git.example:", whose port is empty — and those
// are opposite answers. So the two are told apart here, on the authority text.
func portSection(hostport string) (port string, written bool) {
	if strings.HasPrefix(hostport, "[") {
		closing := strings.LastIndex(hostport, "]")
		if closing < 0 {
			// url.Parse refuses an unclosed bracket, so this is
			// unreachable; saying "no port written" keeps it harmless if
			// it ever stops being.
			return "", false
		}
		rest := hostport[closing+1:]
		if rest == "" {
			return "", false
		}
		return strings.TrimPrefix(rest, ":"), true
	}
	colon := strings.LastIndex(hostport, ":")
	if colon < 0 {
		return "", false
	}
	return hostport[colon+1:], true
}

// ClassifyAddress is the ONE decision about whether an address may be used,
// saved or shown as it stands.
//
// It returns AddressCredentialFree only for an address the grammar read whole
// as one of its two accepted shapes, with no user information, no query and no
// fragment in it. Everything else — a credential position that exists, a shape
// the grammar does not accept, a string it could not read — comes back as a
// refusal, and the refusal is the zero value so a caller that forgets to look
// still refuses.
func ClassifyAddress(raw string) AddressVerdict {
	_, verdict := readAddress(raw)
	return verdict
}

// SafeRepoURL returns the part of an address that is safe to show a person:
// scheme, host, port and path, with any embedded credential removed.
//
// It returns "" whenever the grammar could not read the address whole.
// Callers must then say nothing about the address rather than falling back to
// the original string.
//
// # The original is returned only after an explicit safe answer
//
// There is exactly one branch below that hands raw back, and it is reached
// only on AddressCredentialFree — an explicit, positive verdict from the
// grammar. An address the grammar refused, for any reason, never leaves here
// as it was written. That is the whole of BF13: the previous version handed
// the operator's string straight back whenever it happened to reach the end
// of a permissive read.
//
// # And the stripped result is checked too
//
// The other branch below builds a NEW string: the address re-rendered by
// net/url with the credential positions cleared. That string is not the one
// the grammar read, so it gets read as well, and it leaves here only on an
// explicit credential-free answer of its own. This used to be a claim in a
// comment — "what is left once they are cleared is itself a valid,
// credential-free address" — with nothing checking it.
//
// It reads the address through readAddress, the same single reading
// ClassifyAddress uses, so what is shown and what is judged can never come
// apart. Whenever ClassifyAddress says AddressCredentialFree this returns a
// non-empty string, and whenever it says AddressUnclassifiable this returns "".
func SafeRepoURL(raw string) string {
	r, verdict := readAddress(raw)
	switch verdict {
	case AddressCredentialFree:
		// The grammar read the whole address and found no credential in it,
		// so the operator sees exactly the text they wrote. It is handed
		// back byte for byte rather than re-rendered from the parse: a
		// re-render is a second reading, and two readings of one value is
		// the shape of defect this file exists to prevent.
		return raw
	case AddressCarriesCredential:
		// Everything except the credential positions already passed the
		// grammar, so clearing them ought to leave a valid, credential-free
		// address — but "ought to" is not the same as "does", and this file
		// used to say it as though it were. What is returned here is not the
		// address the grammar read: it is a NEW string, re-rendered by
		// net/url and then trimmed, and no one had ever asked the grammar
		// about that new string. So it is asked, below, and nothing leaves
		// here without a credential-free answer of its own.
		u := *r.u
		u.User = nil
		u.RawQuery = ""
		u.ForceQuery = false
		u.Fragment = ""
		u.RawFragment = ""
		out := u.String()
		if !r.explicit && !r.network {
			// The "//" was credsafe's own doing, so it comes back off and
			// the operator sees the address the way they wrote it.
			out = strings.TrimPrefix(out, "//")
		}
		if ClassifyAddress(out) != AddressCredentialFree {
			// The strip produced something the grammar will not vouch for.
			// Nothing has been established about it, so nothing of it is
			// shown — the same answer an unreadable address gets.
			return ""
		}
		return out
	default:
		// AddressUnclassifiable, and any verdict this build has never heard
		// of. Nothing about the value has been established, so nothing about
		// the value is shown.
		return ""
	}
}

// # When there is nothing safe to name (B14)
//
// SafeRepoURL returning "" is right for a field: an empty repository column
// says "Sharko declined to say". It is wrong in the middle of a sentence. The
// version-freshness screens build whole sentences around the address —
// "Sharko could not read the version index at <repo>" — and an empty string
// there produces a sentence that stops mid-air.
//
// So sentence builders call SafeRepoURLPhrase instead. It is SafeRepoURL with
// one difference: when SafeRepoURL cannot vouch for any part of the string, it
// says "the chart repository" rather than nothing. The sentence stays whole and
// still tells the operator which step failed, and the raw string still never
// travels. It is not a second classifier — the decision is still SafeRepoURL's.
const UnnamedRepoPhrase = "the chart repository"

// SafeRepoURLPhrase returns the safe part of a repository address for use
// INSIDE a sentence, falling back to UnnamedRepoPhrase when SafeRepoURL has
// nothing it can vouch for. It never returns the empty string.
func SafeRepoURLPhrase(raw string) string {
	if safe := SafeRepoURL(raw); safe != "" {
		return safe
	}
	return UnnamedRepoPhrase
}
