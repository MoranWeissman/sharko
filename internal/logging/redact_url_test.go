package logging

// redact_url_test.go — the redactor's blind spot, and the detector that closes
// it (B14).
//
// The three detectors that were here could not see a credential inside a plain
// STRING value under a key nobody thought to call sensitive. "repo" is such a
// key, and internal/catalog logged the whole chart repository address under it
// — token and all. internal/advisories does the same under "url".
//
// Patching those two call sites would have left the SHAPE open for the third.
// So the fix is a fourth detector at the sink, and these tests are about the
// detector, not about the two lines that prompted it.

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

const redactURLSentinel = "M2VT-log-url-token-sentinel-6h4j8n-never-leaves-the-server-b9f3"

// captureRedacted logs one attr through the REAL handler chain `sharko serve`
// installs and returns what was written.
func captureRedacted(t *testing.T, key, value string) string {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(NewRedactHandler(
		slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
	))
	logger.Info("a line", key, value)
	out := buf.String()
	if !strings.Contains(out, `"msg":"a line"`) {
		t.Fatalf("the log line was never written, so this test swept nothing:\n%s", out)
	}
	return out
}

// TestRedact_CredentialInURL_UnderAnInnocentKey is the leak itself: the key is
// "repo", which is on no sensitive-name list and never will be.
func TestRedact_CredentialInURL_UnderAnInnocentKey(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		value string
		// want is typed out here, never read from the code.
		want string
	}{
		{
			"token in the username position, under repo",
			"repo",
			"https://" + redactURLSentinel + "@charts.example/org/repo",
			"https://charts.example/org/repo",
		},
		{
			"token in the password position, under url",
			"url",
			"https://x-access-token:" + redactURLSentinel + "@charts.example/org/repo",
			"https://charts.example/org/repo",
		},
		{
			"token in the username position of an oci registry",
			"registry",
			"oci://" + redactURLSentinel + "@registry.example/charts",
			"oci://registry.example/charts",
		},
		// BF1. The three above all end their credential at an "@". This
		// one does not, and that is exactly why it used to walk straight
		// through: the sink asked whether the value contained an "@" and
		// nothing else, so an address carrying its token as a query
		// parameter never reached the stripper that exists to remove
		// query parameters.
		{
			"token as a query parameter, under repo",
			"repo",
			"https://charts.example/org/repo?access_token=" + redactURLSentinel,
			"https://charts.example/org/repo",
		},
		{
			"token as a query parameter alongside an ordinary one",
			"url",
			"https://charts.example/index.yaml?ref=main&private_token=" + redactURLSentinel,
			"https://charts.example/index.yaml",
		},
		{
			"token in the fragment",
			"repo",
			"https://charts.example/org/repo#" + redactURLSentinel,
			"https://charts.example/org/repo",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := captureRedacted(t, tc.key, tc.value)
			if strings.Contains(out, redactURLSentinel) {
				t.Errorf("the log line carries the token under key %q:\n%s", tc.key, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("the log line no longer names the repository at all — an operator cannot tell which one this was.\nwant it to contain: %q\ngot:\n%s", tc.want, out)
			}
		})
	}
}

// TestRedact_OrdinaryURLsAreLeftExactlyAsWritten is the other half, and the
// reason this detector is safe to put at the sink: it fires on ONE structural
// fact, and credsafe is the only place that says what that fact is — credsafe
// read the value all the way through and found a place a credential can live:
// a userinfo section, a query, or a fragment.
//
// A value credsafe read and found none of those in is untouched, and so is
// anything with none of "@", "?" or "#" anywhere in it, which is most log
// text. What is NOT here any more is a URL with a plain query string on it.
// "?ref=main" used to be left alone, on the reasoning that only a userinfo
// section carries credentials. It does not: "?access_token=..." is a real
// shape, credsafe has always said so, and the sink was reading half the rule.
// So a query and a fragment now go the same way they go everywhere else in
// Sharko — see TestRedact_QueryAndFragmentGoEvenWhenTheyLookHarmless.
func TestRedact_OrdinaryURLsAreLeftExactlyAsWritten(t *testing.T) {
	for _, value := range []string{
		"https://charts.example/org/repo",
		"https://charts.example:8443/org/repo",
		"oci://registry.example/charts",
		"charts.example/org/repo",
		"localhost:8080",
		"github.com/org/repo@v1",
		"*url.Error",
		"a message with no credential characters in it at all",
	} {
		t.Run(value, func(t *testing.T) {
			out := captureRedacted(t, "repo", value)
			if !strings.Contains(out, jsonEscaped(value)) {
				t.Errorf("the value was rewritten, and it carried no credential:\nwrote: %q\ngot:\n%s", value, out)
			}
		})
	}
}

// TestRedact_ValuesCredsafeCannotReadAreBlanked is the BF12 half of the same
// question, and it is the one that used to go the wrong way.
//
// A string the classifier cannot read is precisely the one where a token could
// be anywhere in it, so the sink writes the placeholder instead of the value.
// The scp-style Git remote is the shape this was ruled on: Sharko does not
// support it, net/url cannot parse it, and it used to be printed as written.
func TestRedact_ValuesCredsafeCannotReadAreBlanked(t *testing.T) {
	for _, value := range []string{
		"git@charts.example:org/repo.git",
		"not a url at all, just words with an @ in them",
		// The BF12 disclosure in miniature: a credential AND a port net/url
		// refuses. It used to be printed in full because the parse failed.
		"https://" + redactURLSentinel + "@charts.example:notaport/org/repo",
	} {
		t.Run(value, func(t *testing.T) {
			out := captureRedacted(t, "repo", value)
			if strings.Contains(out, jsonEscaped(value)) {
				t.Errorf("a value credsafe could not read was printed as written, so a token inside one "+
					"would be printed too:\n%s", out)
			}
			if !strings.Contains(out, redactedPlaceholder) {
				t.Errorf("the value was neither printed nor blanked — the sink wrote something else:\n%s", out)
			}
		})
	}
}

// TestRedact_AnEmailKeepsOnlyItsDomain records a widening BF12 brought with it,
// so it is a decision rather than a surprise.
//
// "somebody@example.com" reads as user information in front of a host, which
// is the same shape a bare token in the username position has. The sink cannot
// tell those apart without guessing at what text looks secret, which is the one
// thing this package refuses to do, so the local part goes.
func TestRedact_AnEmailKeepsOnlyItsDomain(t *testing.T) {
	out := captureRedacted(t, "author", "somebody@example.com")
	if strings.Contains(out, "somebody@") {
		t.Errorf("the local part survived, so a bare token written the same way would too:\n%s", out)
	}
	if !strings.Contains(out, "example.com") {
		t.Errorf("the domain went as well, so the log line no longer says anything at all:\n%s", out)
	}
}

// jsonEscaped is what slog's JSON handler writes for a string. Only the two
// characters these fixtures can contain are handled — a bigger escaper here
// would be a second JSON encoder to get wrong.
func jsonEscaped(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// TestRedact_SensitiveKeyStillWinsOverTheURLDetector keeps the ordering
// explicit: a value under a key that is already known-sensitive is blanked
// outright, not rewritten into a readable address.
func TestRedact_SensitiveKeyStillWinsOverTheURLDetector(t *testing.T) {
	out := captureRedacted(t, "token", "https://"+redactURLSentinel+"@charts.example/org/repo")
	if strings.Contains(out, redactURLSentinel) {
		t.Errorf("the log line carries the token:\n%s", out)
	}
	if !strings.Contains(out, redactedPlaceholder) {
		t.Errorf("a value under a known-sensitive key must be blanked outright, not rewritten:\n%s", out)
	}
	if strings.Contains(out, "charts.example") {
		t.Errorf("a value under a known-sensitive key must not be partly echoed back:\n%s", out)
	}
}

// TestRedact_QueryAndFragmentGoEvenWhenTheyLookHarmless is the cost of the fix,
// written down rather than discovered later (BF1).
//
// The sink cannot tell "?ref=main" from "?access_token=...". Nothing can,
// without scanning the text for things that look secret, which is the one
// approach credsafe refuses — it fails on the first shape nobody predicted.
// So both go. An operator loses a query string from a log line; the
// alternative is a token in one.
func TestRedact_QueryAndFragmentGoEvenWhenTheyLookHarmless(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"https://charts.example/index.yaml?ref=main", "https://charts.example/index.yaml"},
		{"https://charts.example:8443/org/repo#frag", "https://charts.example:8443/org/repo"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			out := captureRedacted(t, "repo", tc.value)
			if !strings.Contains(out, jsonEscaped(tc.want)) {
				t.Errorf("want the address kept as %q, got:\n%s", tc.want, out)
			}
			if strings.Contains(out, jsonEscaped(tc.value)) {
				t.Errorf("the query or fragment survived, so the same path would carry a token:\n%s", out)
			}
		})
	}
}
