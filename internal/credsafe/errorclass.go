package credsafe

// errorclass.go — what Sharko is allowed to say about an error in a LOG line
// (B9).
//
// # The log was the last place the raw words still went
//
// Five stories closed this leak on response bodies. The log was left, and a
// log feels less exposed than an API reply — it is not. It is shipped to
// whatever collector the operator runs, kept for months, and read by more
// people than the API. The standing rule makes no exception for it.
//
// The carrier is the same one as everywhere else. A Git repository address is
// commonly written with the password inside it —
//
//	https://x-access-token:<token>@host/org/repo.git
//
// — and providers quote the address they failed on, verbatim, inside their
// error text. net/url's own parse error quotes in full the string it refused.
// So `slog.Error("...", "error", err)` is a token in the log, and it looks at
// the call site like nothing more than good hygiene.
//
// # Classified by TYPE, never by reading the words
//
// LogClass never calls err.Error(). It cannot: there is no branch in it that
// looks at an error's message. Everything it reports comes from one of two
// places, and both are decided at compile time:
//
//   - sentinel and interface probes — errors.Is against a fixed set of stdlib
//     sentinels, and the two stdlib error interfaces (net.Error's Timeout,
//     and this package's own credentials mark);
//   - the Go TYPE NAMES of the error chain, via %T.
//
// A Go type name is written in source by a programmer. No value, no header, no
// remote server and no attacker can put text into one. That is what makes the
// chain safe to print, and it is also the single most useful thing an operator
// gets out of a stripped error: `*url.Error` says the address would not parse,
// `*net.DNSError` says the name would not resolve, `*os.SyscallError` says the
// dial was refused. Those are the answers people actually want from a log.
//
// A text scan for "things that look like secrets" was the alternative and is
// banned. It fails on the first shape nobody predicted, and the whole point of
// this package is that classification is by type.
//
// # Where the words go instead
//
// Nowhere. There is no debug sink that keeps them. A "raw log for debugging"
// would have to live somewhere, and everywhere it could live is shipped off
// the machine — stdout is the container log, a file is collected, an in-memory
// ring is served over the API. Since no such place can be proved private, the
// text is not written at all. What failed is named at the point of failure, by
// the code that knows what it was doing, in words Sharko wrote.
//
// # It is applied at the sink, not at the call site
//
// LogClass is called from internal/logging's RedactHandler, which every log
// record in the process passes through. That is deliberate: a call-site fix
// protects the eighty-eight lines that exist today and nothing written
// tomorrow. Wrapping the sink means a new `slog.Error(..., "error", err)`
// added next year is safe without its author knowing this file exists.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"strings"
	"syscall"
)

// LogClassUnclassified is what LogClass says when it recognises nothing about
// an error beyond its type chain. It is deliberately dull: "Sharko does not
// know what this is" is an honest answer, and better than a guess.
const LogClassUnclassified = "unclassified"

// logClassMaxDepth bounds the unwrap walk. An error chain longer than this is
// either pathological or a cycle somebody built by accident, and a log line is
// not the place to find out.
const logClassMaxDepth = 12

// errorSentinel pairs a fixed standard-library error value with the two names
// Sharko is allowed to say about it: the short one a log line carries, and the
// plain sentence a person reads.
//
// Both names are written here in source. Neither is derived from the error's
// message, so neither can carry an address, a token, or anything else that
// arrived from outside the process. Keeping them in ONE table is what stops
// the log wording and the on-screen wording drifting apart, and stops a new
// sentinel being added to one and forgotten in the other.
type errorSentinel struct {
	sentinel error
	logName  string
	plain    string
}

var errorSentinels = []errorSentinel{
	{context.DeadlineExceeded, "deadline-exceeded", "it ran out of time"},
	{context.Canceled, "canceled", "it was cancelled"},
	{io.EOF, "eof", "the other side closed the connection"},
	{io.ErrUnexpectedEOF, "unexpected-eof", "the reply stopped in the middle"},
	{fs.ErrNotExist, "file-not-found", "a file it needed is not there"},
	{fs.ErrPermission, "file-permission-denied", "a file it needed cannot be read"},
	{syscall.ECONNREFUSED, "connection-refused", "the connection was refused"},
	{syscall.ECONNRESET, "connection-reset", "the connection was reset"},
	{syscall.EHOSTUNREACH, "host-unreachable", "the host cannot be reached"},
	{syscall.ENETUNREACH, "network-unreachable", "the network cannot be reached"},
}

// LogClass returns a description of err that is safe to write to a log.
//
// It never reads err.Error(). The result is built only from sentinel matches,
// stdlib interface probes and Go type names — all of which are fixed at
// compile time and cannot carry data from a provider, a repository URL, an
// HTTP response or a credentials backend.
//
// Returns "" for a nil error so a caller can tell "no error" apart from "an
// error Sharko could not classify".
func LogClass(err error) string {
	if err == nil {
		return ""
	}

	var facts []string
	add := func(s string) {
		for _, seen := range facts {
			if seen == s {
				return
			}
		}
		facts = append(facts, s)
	}

	// This package's own mark: the error came out of a credentials backend,
	// which is the class whose words are most dangerous of all.
	if Is(err) {
		add("credentials-backend")
	}
	if errors.Is(err, ErrNotFound) {
		add("not-found")
	}

	// stdlib sentinels. Each is a fixed value in the standard library.
	for _, probe := range errorSentinels {
		if errors.Is(err, probe.sentinel) {
			add(probe.logName)
		}
	}

	// Interface probes. Timeout() is a method on net.Error; calling it reads
	// no message.
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		add("timeout")
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		add("dns")
	}

	// *url.Error is deliberately NOT special-cased. Its useful field (Op) is
	// already implied by its position in the chain, and its other two fields
	// are the URL itself — the exact thing that must not be printed. It shows
	// up in the type chain below, which is all an operator needs to know that
	// an address would not parse.

	if len(facts) == 0 {
		facts = append(facts, LogClassUnclassified)
	}
	if chain := logClassTypeChain(err); chain != "" {
		facts = append(facts, "chain="+chain)
	}
	return strings.Join(facts, " ")
}

// logClassTypeChain walks err's Unwrap chain and reports the Go type name at
// each level, deepest last.
//
// %T on an error value prints its type, which is a symbol a programmer wrote
// in source. It is not derived from the error's message, its fields, or
// anything that arrived over a wire.
//
// *fmt.wrapError is dropped: it is what fmt.Errorf("...: %w") produces, it
// appears at nearly every level of nearly every chain, and it says nothing
// about what went wrong. Consecutive repeats collapse for the same reason.
func logClassTypeChain(err error) string {
	var names []string
	for depth := 0; err != nil && depth < logClassMaxDepth; depth++ {
		name := fmt.Sprintf("%T", err)
		if name != "*fmt.wrapError" && (len(names) == 0 || names[len(names)-1] != name) {
			names = append(names, name)
		}
		switch u := err.(type) {
		case interface{ Unwrap() error }:
			err = u.Unwrap()
		case interface{ Unwrap() []error }:
			// A joined error. Take the first branch only: a log line wants a
			// shape, not a tree, and every branch is reported by type anyway
			// if it is the one that matters.
			joined := u.Unwrap()
			if len(joined) == 0 {
				err = nil
				break
			}
			err = joined[0]
		default:
			err = nil
		}
	}
	return strings.Join(names, ">")
}
