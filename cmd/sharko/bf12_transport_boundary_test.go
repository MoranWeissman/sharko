package main

// bf12_transport_boundary_test.go — BF12-2.
//
// # What went wrong
//
// `apiRequest` builds an HTTP request and then sends it. BF10 fixed the SEND:
// a failure there comes back from net/http as a *url.Error, that type prints
// the address it was dialling, and it masks only the password half of any user
// information — so a token written in the username position came straight back
// out. What BF10 did not fix was the BUILD, two lines above, which wrapped the
// same kind of error under "%w". On that path net/http masks nothing at all,
// not even the password half, so `sharko version` printed a username AND a
// password in the clear.
//
// # Why this file still has work to do after BF12-1
//
// BF12-1 made the address check fail closed, so an address with user
// information, a query or a fragment is refused before anything is built. It
// is tempting to conclude that the build error can no longer be reached. It
// can:
//
//	[::1]:8080
//
// is a scheme-less IPv6 endpoint. The product owner explicitly preserved that
// shape — it is credential-free and Sharko must keep accepting it — and
// net/url cannot build a request from it once Sharko has appended the API
// path ("first path segment in URL cannot contain colon"). The build error
// fires, and under a "%w" it reproduced the operator's address verbatim
// inside Go's own words.
//
// So the tests below drive the real command tree with exactly that shape and
// prove that Go's words never appear in Sharko's output. Both scenarios are
// covered: the address that cannot be BUILT into a request, and the address
// that builds fine and then cannot be REACHED.
//
// # How a clean output is proved to be really clean
//
// A search that has never been shown to find anything proves nothing when it
// finds nothing. Every check below first runs the SAME search against the raw
// Go error, and fails at setup if the phrase is not there.

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// The two addresses, and what Go says about each.
// ---------------------------------------------------------------------------

const (
	// bf12UnbuildableAddress is credential-free, is accepted by the address
	// check on purpose, and cannot be built into a request once the API
	// path is appended. The path segment is a sentinel so the test can tell
	// the operator's own value apart from anything else in the output.
	bf12UnbuildableAddress = "[::1]:8080/BF12SENTINELPATHZZ"

	// bf12UnreachableAddress builds fine and then goes nowhere: ".invalid"
	// is reserved by the RFCs and never resolves.
	bf12UnreachableAddress = "https://bf12sentinelhostzz.invalid"

	// bf12PathSentinel and bf12HostSentinel are the parts of those two
	// addresses that must never turn up inside a failure sentence.
	bf12PathSentinel = "BF12SENTINELPATHZZ"
	bf12HostSentinel = "bf12sentinelhostzz"
)

// bf12GoTransportWords are phrases that only ever appear because Go's own
// error text was let through. Sharko never writes any of them.
//
// The last three are the Go TYPE NAMES that credsafe.LogClass appends. They
// belong in a log line and not in a sentence a person reads, which is the
// other half of what BF12-2 fixed.
var bf12GoTransportWords = []string{
	`parse "`,
	"first path segment in URL cannot contain colon",
	"no such host",
	"dial tcp",
	"lookup ",
	"*url.Error",
	"*net.DNSError",
	"chain=",
}

// bf12ProvePhrasePresent is the positive control. It asserts the phrase IS in
// the raw Go error, using the same search that is about to be run against
// Sharko's output.
func bf12ProvePhrasePresent(t *testing.T, what, haystack, phrase string) {
	t.Helper()
	if !strings.Contains(haystack, phrase) {
		t.Fatalf("positive control failed: %s does not contain %q, so finding it absent "+
			"from Sharko's output would prove nothing.\n\ngot: %s", what, phrase, haystack)
	}
}

// bf12ProveClean scans everything one command wrote for Go's own words.
//
// It does NOT forbid the address itself here, and that is deliberate. Some
// commands show the address on purpose — `sharko version` prints
// "Server: <address>" — and they show it through credsafe, which is the whole
// point of BF10's display fix. What must never appear is Go's text, because
// the only way Go's text gets onto a screen is a raw error being wrapped or
// quoted, and Go's text is where the address comes back UNVOUCHED-FOR: with
// user information in it, with only the password half masked, or, on the build
// path, with nothing masked at all.
func bf12ProveClean(t *testing.T, what, haystack string) {
	t.Helper()
	for _, phrase := range bf12GoTransportWords {
		if strings.Contains(haystack, phrase) {
			t.Errorf("%s repeats Go's own transport text %q — the error was wrapped or "+
				"quoted instead of described:\n\n%s", what, phrase, haystack)
		}
	}
}

// bf12ProveErrorCarriesNoAddress checks the returned ERROR, which is the thing
// the transport boundary produces and the thing that travels: into a shell
// script, a CI log, a bug report. Sharko's own sentence names the method and
// the API path and nothing else, so no part of the operator's address belongs
// in it.
func bf12ProveErrorCarriesNoAddress(t *testing.T, what string, err error, addressParts []string) {
	t.Helper()
	if err == nil {
		return
	}
	for _, part := range addressParts {
		if strings.Contains(err.Error(), part) {
			t.Errorf("the error from %s reproduces the operator's address (%q):\n\n%v",
				what, part, err)
		}
	}
}

// ---------------------------------------------------------------------------
// The address that cannot be built into a request.
// ---------------------------------------------------------------------------

// TestBuildFailure_NeverReproducesTheAddress is the direct guard on
// cmd/sharko/client.go's http.NewRequest branch.
//
// It drives every command that reaches the server, taken from the same walk of
// the real command tree that the rest of the package uses, so a command added
// next year is covered without anybody remembering this file exists.
func TestBuildFailure_NeverReproducesTheAddress(t *testing.T) {
	// Positive control, on the real thing: Go's build error really does
	// contain both the phrase and the operator's path.
	_, rawErr := http.NewRequest(http.MethodGet, bf12UnbuildableAddress+"/api/v1/health", nil)
	if rawErr == nil {
		t.Fatal("net/http built a request from " + bf12UnbuildableAddress + " — this test was " +
			"written around the fact that it cannot, and now proves nothing")
	}
	bf12ProvePhrasePresent(t, "Go's own build error", rawErr.Error(),
		"first path segment in URL cannot contain colon")
	bf12ProvePhrasePresent(t, "Go's own build error", rawErr.Error(), bf12PathSentinel)

	bf12RunEveryServerReachingCommand(t, bf12UnbuildableAddress, []string{bf12PathSentinel})
}

// ---------------------------------------------------------------------------
// The address that builds and then goes nowhere.
// ---------------------------------------------------------------------------

// TestTransportFailure_NeverReproducesTheAddress is the guard on the
// client.Do branch, which BF10 fixed and which must stay fixed.
func TestTransportFailure_NeverReproducesTheAddress(t *testing.T) {
	req, buildErr := http.NewRequest(http.MethodGet, bf12UnreachableAddress+"/api/v1/health", nil)
	if buildErr != nil {
		t.Fatalf("the unreachable address could not even be built, so this test would be "+
			"exercising the wrong branch: %v", buildErr)
	}
	_, rawErr := http.DefaultClient.Do(req)
	if rawErr == nil {
		t.Fatal("the address reserved for never resolving resolved — refusing to draw any " +
			"conclusion from a clean output")
	}
	bf12ProvePhrasePresent(t, "Go's own transport error", rawErr.Error(), bf12HostSentinel)

	bf12RunEveryServerReachingCommand(t, bf12UnreachableAddress, []string{bf12HostSentinel})
}

// ---------------------------------------------------------------------------
// The walk.
// ---------------------------------------------------------------------------

// bf12SkippedCommands are commands this file does not drive, each with the
// reason. Every key is checked against the walk, so a stale entry fails.
var bf12SkippedCommands = map[string]string{
	"sharko pr wait": "its poll loop treats a transport failure as transient and calls " +
		"os.Exit(2) when the deadline passes. os.Exit from inside a test kills the whole " +
		"test binary with no line saying which command did it. The os.Exit is a known " +
		"defect recorded elsewhere; it is not BF12-2's to fix.",
}

// bf12WriteRawConfig writes a config file by hand, with the address QUOTED.
//
// It exists instead of the package's own writeRawConfig for one reason, and
// the reason is a bug this test had until it was caught: an IPv6 endpoint
// written into YAML unquoted —
//
//	server: [::1]:8080/...
//
// — is not a string to a YAML parser, it is a flow sequence. The CLI then
// failed to load its config and never got anywhere near building a request, so
// every assertion below passed while proving nothing. Quoting the value is
// what makes the address arrive at the code under test.
func bf12WriteRawConfig(t *testing.T, dir, server, token string) string {
	t.Helper()
	body := fmt.Sprintf("server: %s\ntoken: %s\n", strconv.Quote(server), strconv.Quote(token))
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("writing the config fixture: %v", err)
	}

	// The fixture must actually load, or everything below is vacuous. This
	// is the check whose absence made the first version of this file pass
	// with a broken config file.
	loaded, err := loadConfig()
	if err != nil {
		t.Fatalf("the config fixture this test wrote does not load, so no command would "+
			"reach the code under test: %v\n\nfile:\n%s", err, body)
	}
	if loaded.Server != server {
		t.Fatalf("the config fixture round-tripped to %q, not %q — the address would not "+
			"arrive at the code under test", loaded.Server, server)
	}
	return body
}

// bf12RunEveryServerReachingCommand drives every command classified as
// reaching the server with the given address saved in an isolated config, and
// proves nothing in the output repeats Go's words or the address.
func bf12RunEveryServerReachingCommand(t *testing.T, address string, addressParts []string) {
	t.Helper()

	// Isolation first, and it fails at setup rather than after the fact.
	// isolatedConfigDir points SHARKO_CONFIG_DIR at a throwaway directory
	// and PROVES the override took effect before anything runs.
	dir := isolatedConfigDir(t)
	onDisk := bf12WriteRawConfig(t, dir, address, "test-token")
	for _, part := range addressParts {
		bf12ProvePhrasePresent(t, "the config file this test wrote", onDisk, part)
	}

	walked := walkCommandTree()
	if len(walked) == 0 {
		t.Fatal("the command tree walk found no commands at all — everything below would " +
			"pass vacuously")
	}
	inTree := map[string]bool{}
	for _, path := range walked {
		inTree[path] = true
	}
	for path := range bf12SkippedCommands {
		if !inTree[path] {
			t.Errorf("stale skip %q: it is skipped here but no longer exists in the command tree", path)
		}
	}

	exercised := 0
	for _, path := range walked {
		row, ok := classifiedCommands[path]
		if !ok || row.kind != reachesServer {
			continue
		}
		if _, skipped := bf12SkippedCommands[path]; skipped {
			continue
		}
		exercised++
		t.Run(path, func(t *testing.T) {
			output, err := runCommand(t, path, row.argv)
			combined := output
			if err != nil {
				combined += "\n" + err.Error()
			}
			bf12ProveClean(t, "command "+path, combined)
			bf12ProveErrorCarriesNoAddress(t, "command "+path, err, addressParts)
		})
	}

	// Zero exercised commands is a broken walk, not a clean sweep.
	if exercised == 0 {
		t.Fatal("no server-reaching command was exercised — this test proved nothing")
	}
}
