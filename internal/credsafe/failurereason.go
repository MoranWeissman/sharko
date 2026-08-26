package credsafe

// failurereason.go — what Sharko is allowed to say about an error in a
// sentence a PERSON reads (BF12-2).
//
// # Why this exists next to LogClass and is not LogClass
//
// LogClass answers a log collector's question. It is deliberately terse and it
// ends with the Go TYPE NAMES of the error chain, because to somebody reading
// a log at three in the morning "*net.DNSError" is the answer.
//
// Two places were calling it for a sentence printed on an operator's terminal
// instead, and there the type chain is the wrong answer twice over. It teaches
// a person nothing — "did not complete (unclassified chain=*url.Error>
// *errors.errorString)" is not English — and it puts Sharko's internal shape
// on a screen that ends up in a screenshot, a ticket and a forum post.
//
// So the terse form stays where it belongs and this is the form for people. It
// is the SAME evidence: the same sentinel table (errorSentinels in
// errorclass.go), the same interface probes, and nothing at all from
// err.Error(). One table, two wordings, so the two cannot drift apart and a
// sentinel added to one cannot be forgotten in the other.
//
// # The one thing it must never do
//
// It must never reproduce an address. Every construction and transport failure
// in Go comes back as a *url.Error, and that type prints the whole address it
// was handed: for a failure to build the request, with nothing masked at all,
// not even the password half. That is the leak BF12 exists to close, and it is
// why no branch below reads a message.

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// PlainFailureUnknown is what PlainFailureReason says when it recognises
// nothing about an error. Saying so plainly is better than a guess, and much
// better than quoting the error to fill the gap.
const PlainFailureUnknown = "Sharko could not tell why"

// PlainFailureAddressUnusable is the reason given when the only thing known
// about a failure is that Go could not use the address it was handed. It is
// the shape of the failure, never the address.
const PlainFailureAddressUnusable = "Sharko could not use that address"

// PlainFailureReason returns a short plain-English description of why an
// operation failed, for a sentence somebody reads on a screen.
//
// It never calls err.Error(). Every word it returns is written in this file or
// in the sentinel table in errorclass.go, so no address, token, response body
// or provider message can travel inside it. There are no Go type names in it
// either.
//
// Returns "" for a nil error, so a caller can tell "nothing went wrong" apart
// from "something went wrong and Sharko could not name it".
func PlainFailureReason(err error) string {
	if err == nil {
		return ""
	}

	var reasons []string
	add := func(s string) {
		for _, seen := range reasons {
			if seen == s {
				return
			}
		}
		reasons = append(reasons, s)
	}

	// This package's own mark: the error came out of a credentials backend,
	// whose words are the most dangerous of all to repeat.
	if Is(err) {
		add("the credentials backend refused it")
	}
	if errors.Is(err, ErrNotFound) {
		add("it was not there")
	}

	for _, probe := range errorSentinels {
		if errors.Is(err, probe.sentinel) {
			add(probe.plain)
		}
	}

	// Interface probes. Calling Timeout() reads no message.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		add("it timed out")
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		add("the name did not resolve")
	}

	if len(reasons) == 0 {
		// Nothing above recognised it. A *url.Error at this point is the
		// address itself being unusable — a port that is not a number, a
		// shape net/url will not accept — which is worth saying, because
		// it tells the operator to go and look at the setting rather than
		// at the network. The TYPE says that much; the value stays here.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			return PlainFailureAddressUnusable
		}
		return PlainFailureUnknown
	}
	return strings.Join(reasons, ", ")
}
