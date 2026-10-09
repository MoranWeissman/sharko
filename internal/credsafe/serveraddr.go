package credsafe

// serveraddr.go — the same rule as repourl_supported.go, applied to the OTHER
// address Sharko stores on an operator's disk: the Sharko server address the
// CLI keeps in its config file (~/.sharko/config, `server:`) and the one the
// --server flag can supply.
//
// # Why this address needed the rule too
//
// A repository address is obviously credential material, and the package
// already refuses to save one that carries a credential. The CLI's server
// address looked different because it points at Sharko itself rather than at
// a Git host — so nobody thought of it as a place a secret could live.
//
// It is exactly the same shape of value. An operator can write
// https://user:token@sharko.example into it, a script can build it from a
// secret, and once it is in the config file the CLI hands it to Go's HTTP
// client. Go's own transport error then quotes the address back: it masks the
// PASSWORD half of the userinfo and nothing else, so a credential written in
// the USERNAME position — which is how a bare token is normally carried — came
// straight back out on the terminal. Commands that additionally printed the
// resolved address themselves put every part of it on screen, including the
// half Go had just masked on the same line.
//
// # It is not a second classifier
//
// Everything below delegates. ClassifyAddress decides whether an address may
// be used at all, and SafeRepoURL decides which part of an address may be
// shown. Both live in repourl.go and both are used here unchanged. There is
// one safe verdict and this file waits to be handed it; every other answer,
// including the zero value, is a refusal. This file adds only the WORDS — a
// refusal sentence written for someone configuring a CLI rather than someone
// editing a catalog — and the error type that carries which setting is at
// fault.
//
// Writing the structural test out a second time is the specific mistake this
// package exists to prevent: the copy that only knew about "@" is how an
// address carrying its token as a query parameter walked past a check that
// was supposed to catch it.

// UnsupportedServerAddressMessage is the one sentence Sharko says when it will
// not use a configured server address.
//
// # What it deliberately does not say
//
// It does not claim a credential was found, because the test underneath cannot
// know that. ClassifyAddress is structural: it asks whether the address has
// somewhere for a credential to sit — user information in the authority, a
// query string, a fragment — never whether the text sitting there looks
// secret. It also refuses an address it cannot read at all, which is the other
// half of the same sentence. So the sentence states the RULE, which is always
// true, rather than an accusation, which would be a guess. The price is that an
// ordinary "?ref=main" is refused as well, and that is the right side to be
// wrong on.
//
// It also carries nothing of the value: not the address, not a piece of it,
// not its length, not a mask of it. A refusal message is written for one
// screen and then travels — into a shell history, a CI log, a bug report — so
// the rule that the value must not travel applies to the refusal too.
const UnsupportedServerAddressMessage = "The Sharko server address must be one Sharko can read in full: a host, an optional port, and an optional path. User information in the address, a query string, and a fragment are all refused, and so is an address Sharko cannot read. Use a credential-free base URL, then run 'sharko login' again to save it."

// UnsupportedServerAddressError is the refusal, as a type.
//
// Callers decide what to do by asking errors.Is / errors.As for this, never by
// reading the sentence. Matching on message text is how a reworded sentence
// silently switches a safety check off.
//
// Setting names WHICH configuration the operator has to go and fix — the
// --server flag, or the server field in the CLI config file. It is always a
// name a programmer wrote in this repository, never anything derived from the
// value.
type UnsupportedServerAddressError struct {
	// Setting is the configuration that holds the unusable address, e.g.
	// "--server" or "the server field in the Sharko CLI config file".
	// Empty when the caller has only one address in play.
	Setting string
}

func (e *UnsupportedServerAddressError) Error() string {
	if e.Setting != "" {
		return e.Setting + " — " + UnsupportedServerAddressMessage
	}
	return UnsupportedServerAddressMessage
}

// Is lets errors.Is(err, ErrServerAddressUnsupported) answer true for every
// setting-tagged refusal, so a caller that only wants to know "was this
// refused for this reason" does not have to know about the type.
func (e *UnsupportedServerAddressError) Is(target error) bool {
	return target == ErrServerAddressUnsupported
}

// ErrServerAddressUnsupported is the bare, untagged form of the refusal, and
// the sentinel every tagged one matches.
var ErrServerAddressUnsupported error = &UnsupportedServerAddressError{}

// ValidateServerAddress is the ONE rule about what the CLI may use, save, or
// show as a Sharko server address.
//
// Every place that reads the address, writes it, or is about to dial it calls
// this. There is no second copy.
//
// An empty address is not this function's business — "not configured yet" is a
// different condition and the caller says so in its own words.
func ValidateServerAddress(raw string) error {
	return ValidateServerAddressAt("", raw)
}

// ValidateServerAddressAt is ValidateServerAddress with the name of the
// setting the address came from, so the refusal can point at it.
func ValidateServerAddressAt(setting, raw string) error {
	if raw == "" {
		return nil
	}
	// One state lets the address through, and it has to be handed over
	// explicitly. "Was not identified as unsafe" is not the same answer and
	// is not accepted here.
	switch ClassifyAddress(raw) {
	case AddressCredentialFree:
		return nil
	case AddressCarriesCredential, AddressUnclassifiable:
		return &UnsupportedServerAddressError{Setting: setting}
	default:
		// A verdict this build has never heard of is not a safe one.
		return &UnsupportedServerAddressError{Setting: setting}
	}
}

// UnnamedServerPhrase is what a sentence says instead of the server address
// when there is no part of the address Sharko can vouch for.
const UnnamedServerPhrase = "the configured Sharko server"

// SafeServerAddressPhrase returns the part of a Sharko server address that is
// safe to show a person, for use INSIDE a sentence. It never returns the empty
// string: when there is nothing it can vouch for, the sentence says
// UnnamedServerPhrase and stays whole.
//
// The decision is SafeRepoURL's — this is the same stripper with wording that
// fits a server rather than a chart repository. Callers that have already
// passed ValidateServerAddress still go through it, so that a bug upstream
// cannot turn into a printed credential.
func SafeServerAddressPhrase(raw string) string {
	if safe := SafeRepoURL(raw); safe != "" {
		return safe
	}
	return UnnamedServerPhrase
}
