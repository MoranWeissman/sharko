package envreg

// httpport_test.go — the listen port, branch by branch.
//
// The rule has three branches for the deprecated name alone, and each one
// exists because the other two would get that case wrong:
//
//	a URI Kubernetes injected  → ignored in silence
//	a whole number             → used, with a deprecation warning
//	anything else              → the server does not start
//
// plus the two-name cases on top of that. They are tested separately
// rather than through one table with a "wantErr bool", because "stops the
// server" and "ignored in silence" are opposite answers to the same
// input shape and a shared assertion tends to blur them.

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"
)

// theFallback is a value that is not any default in the code, so a test
// that gets it back knows the fallback was used and did not merely
// coincide with 8080.
const theFallback = 9999

// setPortEnv puts both names into a known state — set to something, or
// genuinely absent — and clears the recorded deprecations so each test
// starts from nothing.
func setPortEnv(t *testing.T, canonical, legacy *string) {
	t.Helper()
	setOrUnset(t, HTTPPort, canonical)
	setOrUnset(t, LegacyHTTPPort, legacy)
	resetForTest()
}

// setOrUnset sets a variable, or removes it entirely when value is nil.
// The leading t.Setenv is what registers the restore, including the case
// where the variable was not there to begin with.
func setOrUnset(t *testing.T, name string, value *string) {
	t.Helper()
	t.Setenv(name, "")
	if value == nil {
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unsetting %s: %v", name, err)
		}
		return
	}
	t.Setenv(name, *value)
}

func strptr(s string) *string { return &s }

func TestResolveHTTPPort_ValidCanonicalValue(t *testing.T) {
	for _, value := range []string{"8080", "1", "65535", "80"} {
		t.Run(value, func(t *testing.T) {
			setPortEnv(t, strptr(value), nil)

			port, err := ResolveHTTPPort(theFallback)
			if err != nil {
				t.Fatalf("%s=%q was refused: %v", HTTPPort, value, err)
			}
			if got, want := port, atoiForTest(t, value); got != want {
				t.Errorf("port = %d, want %d", got, want)
			}
			if d := PendingDeprecations(); len(d) != 0 {
				t.Errorf("the canonical setting warned about a deprecation: %v", d)
			}
		})
	}
}

func TestResolveHTTPPort_NothingSetUsesTheFallback(t *testing.T) {
	setPortEnv(t, nil, nil)

	port, err := ResolveHTTPPort(theFallback)
	if err != nil {
		t.Fatalf("unset should not be an error: %v", err)
	}
	if port != theFallback {
		t.Errorf("port = %d, want the fallback %d", port, theFallback)
	}
}

func TestResolveHTTPPort_EmptyIsAbsenceNotNonsense(t *testing.T) {
	// An unset Helm value renders as "". Treating that as a bad port
	// would fail the boot of an install that configured nothing.
	for name, env := range map[string]struct{ canonical, legacy *string }{
		"canonical empty": {canonical: strptr("")},
		"legacy empty":    {legacy: strptr("")},
		"both empty":      {canonical: strptr(""), legacy: strptr("")},
		"whitespace only": {canonical: strptr("   ")},
	} {
		t.Run(name, func(t *testing.T) {
			setPortEnv(t, env.canonical, env.legacy)

			port, err := ResolveHTTPPort(theFallback)
			if err != nil {
				t.Fatalf("an empty value should be treated as unset, got: %v", err)
			}
			if port != theFallback {
				t.Errorf("port = %d, want the fallback %d", port, theFallback)
			}
		})
	}
}

// TestResolveHTTPPort_OutOfRange covers both ends. Zero is refused even
// though it parses, because to the kernel it means "any free port" and
// the server would end up listening somewhere nobody can predict.
func TestResolveHTTPPort_OutOfRange(t *testing.T) {
	for _, value := range []string{"0", "65536", "-1", "99999999999999999999"} {
		t.Run(value, func(t *testing.T) {
			setPortEnv(t, strptr(value), nil)

			_, err := ResolveHTTPPort(theFallback)
			if err == nil {
				t.Fatalf("%s=%q was accepted; it is not a usable TCP port", HTTPPort, value)
			}
			assertNamesSettingNotValue(t, err, HTTPPort, value)
		})
	}
}

// TestResolveHTTPPort_TrailingRubbishIsRefused is the original defect,
// pinned by its exact input.
//
// fmt.Sscanf(envPort, "%d", &port) reads "80x" as 80 and reports success
// for it. Anything that makes this test pass by returning 80 has put the
// defect back.
func TestResolveHTTPPort_TrailingRubbishIsRefused(t *testing.T) {
	for _, value := range []string{"80x", "8080 abc", "abc", "8.0", "0x1f50", "8_080", "eighty"} {
		t.Run(value, func(t *testing.T) {
			setPortEnv(t, strptr(value), nil)

			port, err := ResolveHTTPPort(theFallback)
			if err == nil {
				t.Fatalf("%s=%q was accepted and became port %d. A value that is not a whole number "+
					"must stop the server, never be partially read and never fall back silently.",
					HTTPPort, value, port)
			}
			assertNamesSettingNotValue(t, err, HTTPPort, value)
		})
	}
}

// TestResolveHTTPPort_ServiceLinkValueIsIgnored is the case the whole
// deprecation dance exists for.
//
// Kubernetes writes SHARKO_PORT=tcp://<clusterIP>:<port> into Sharko's own
// Pod. No operator did that, so it is not configuration: not a value, not
// an error, and not something to warn about.
func TestResolveHTTPPort_ServiceLinkValueIsIgnored(t *testing.T) {
	for _, value := range []string{
		"tcp://10.96.35.88:80",
		"tcp://10.96.35.88:8080",
		"udp://10.96.35.88:53",
		"sctp://10.96.35.88:9999",
	} {
		t.Run(value, func(t *testing.T) {
			setPortEnv(t, nil, strptr(value))

			port, err := ResolveHTTPPort(theFallback)
			if err != nil {
				t.Fatalf("a value Kubernetes injected must be ignored, not refused: %v", err)
			}
			if port != theFallback {
				t.Errorf("port = %d, want the fallback %d — the injected URI was read as configuration", port, theFallback)
			}
			if d := PendingDeprecations(); len(d) != 0 {
				t.Errorf("a variable Kubernetes wrote produced a deprecation warning: %v. Every "+
					"ordinary install would be told to stop setting something nobody set.", d)
			}
		})
	}
}

// TestResolveHTTPPort_ServiceLinkValueIgnoredEvenWithCanonicalSet is the
// combination a real install actually has: the chart sets
// SHARKO_HTTP_PORT and Kubernetes injects SHARKO_PORT. If the injected
// value counted as a disagreeing second opinion, every install would
// refuse to boot.
func TestResolveHTTPPort_ServiceLinkValueIgnoredEvenWithCanonicalSet(t *testing.T) {
	setPortEnv(t, strptr("8080"), strptr("tcp://10.96.35.88:80"))

	port, err := ResolveHTTPPort(theFallback)
	if err != nil {
		t.Fatalf("the shipped chart's own combination failed to start: %v", err)
	}
	if port != 8080 {
		t.Errorf("port = %d, want 8080", port)
	}
	if d := PendingDeprecations(); len(d) != 0 {
		t.Errorf("the shipped chart's own combination warns about a deprecation: %v", d)
	}
}

// TestResolveHTTPPort_NumericLegacyNameWorksAndWarns is the compatibility
// promise: an operator who set SHARKO_PORT before keeps working.
func TestResolveHTTPPort_NumericLegacyNameWorksAndWarns(t *testing.T) {
	setPortEnv(t, nil, strptr("9090"))

	port, err := ResolveHTTPPort(theFallback)
	if err != nil {
		t.Fatalf("the deprecated name should still work: %v", err)
	}
	if port != 9090 {
		t.Errorf("port = %d, want 9090", port)
	}

	deprecations := PendingDeprecations()
	if len(deprecations) != 1 {
		t.Fatalf("got %d deprecation(s), want exactly 1: %v", len(deprecations), deprecations)
	}
	if deprecations[0].Alias != LegacyHTTPPort || deprecations[0].Canonical != HTTPPort {
		t.Errorf("deprecation = %+v, want alias %s → canonical %s",
			deprecations[0], LegacyHTTPPort, HTTPPort)
	}

	// And the warning that reaches the log names both settings and
	// carries no value at all.
	var buf bytes.Buffer
	WarnDeprecated(slog.New(slog.NewJSONHandler(&buf, nil)))
	record := map[string]any{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("the warning is not one JSON record: %v\n%s", err, buf.String())
	}
	if record["setting"] != LegacyHTTPPort {
		t.Errorf("setting = %v, want %s", record["setting"], LegacyHTTPPort)
	}
	if record["use_instead"] != HTTPPort {
		t.Errorf("use_instead = %v, want %s", record["use_instead"], HTTPPort)
	}
	if strings.Contains(buf.String(), "9090") {
		t.Errorf("the deprecation warning printed the value:\n%s", buf.String())
	}
}

// TestResolveHTTPPort_InvalidLegacyNameStopsStartup — "80x" in the old
// name is still an operator saying something wrong, and is refused for
// the same reason as in the new name.
func TestResolveHTTPPort_InvalidLegacyNameStopsStartup(t *testing.T) {
	for _, value := range []string{"80x", "abc", "0", "65536"} {
		t.Run(value, func(t *testing.T) {
			setPortEnv(t, nil, strptr(value))

			port, err := ResolveHTTPPort(theFallback)
			if err == nil {
				t.Fatalf("%s=%q was accepted and became port %d", LegacyHTTPPort, value, port)
			}
			// The error must name the variable the operator really set,
			// not the one they have never heard of.
			assertNamesSettingNotValue(t, err, LegacyHTTPPort, value)
		})
	}
}

func TestResolveHTTPPort_BothSetAndAgreeing(t *testing.T) {
	setPortEnv(t, strptr("8080"), strptr("8080"))

	port, err := ResolveHTTPPort(theFallback)
	if err != nil {
		t.Fatalf("two settings that agree should not be a conflict: %v", err)
	}
	if port != 8080 {
		t.Errorf("port = %d, want 8080", port)
	}
	if d := PendingDeprecations(); len(d) != 1 {
		t.Errorf("got %d deprecation(s), want 1 — the old name is in use and should still be reported: %v", len(d), d)
	}
}

func TestResolveHTTPPort_BothSetAndDisagreeingStopsStartup(t *testing.T) {
	setPortEnv(t, strptr("8080"), strptr("9090"))

	_, err := ResolveHTTPPort(theFallback)
	if err == nil {
		t.Fatal("two settings giving different ports were not refused. Preferring one of them " +
			"silently is how an operator ends up certain they changed a port they did not.")
	}
	message := err.Error()
	for _, name := range []string{HTTPPort, LegacyHTTPPort} {
		if !strings.Contains(message, name) {
			t.Errorf("the conflict error does not name %s: %q", name, message)
		}
	}
	for _, value := range []string{"8080", "9090"} {
		if strings.Contains(message, value) {
			t.Errorf("the conflict error repeats the value %q: %q", value, message)
		}
	}
}

// TestLookupAndResolveHTTPPortAgree drives the generic resolver and the
// port resolver over the same environments.
//
// They share resolveAliasPair, which is the point — but "they share a
// function" is a claim about today's code, and this is the assertion that
// keeps it true. Startup runs BOTH: envreg.Validate() calls Lookup for
// every production setting before ResolveHTTPPort ever runs, so a
// disagreement means one of them fails the boot while the other is happy.
func TestLookupAndResolveHTTPPortAgree(t *testing.T) {
	for name, env := range map[string]struct{ canonical, legacy *string }{
		"neither set":            {},
		"canonical only":         {canonical: strptr("8080")},
		"legacy numeric only":    {legacy: strptr("9090")},
		"legacy service link":    {legacy: strptr("tcp://10.96.35.88:80")},
		"both, service link":     {canonical: strptr("8080"), legacy: strptr("tcp://10.96.35.88:80")},
		"both, agreeing":         {canonical: strptr("8080"), legacy: strptr("8080")},
		"both, disagreeing":      {canonical: strptr("8080"), legacy: strptr("9090")},
		"legacy trailing rubbis": {legacy: strptr("80x")},
	} {
		t.Run(name, func(t *testing.T) {
			setPortEnv(t, env.canonical, env.legacy)
			lookupValue, lookupPresent, lookupErr := Lookup(HTTPPort)
			lookupDeprecations := len(PendingDeprecations())

			setPortEnv(t, env.canonical, env.legacy)
			_, resolveErr := ResolveHTTPPort(theFallback)
			resolveDeprecations := len(PendingDeprecations())

			// A conflict is the only thing Lookup can refuse. When it
			// does, ResolveHTTPPort must refuse too — otherwise the boot
			// fails in one place and succeeds in the other depending on
			// which runs first.
			if (lookupErr != nil) && (resolveErr == nil) {
				t.Errorf("Lookup refused this environment (%v) but ResolveHTTPPort accepted it", lookupErr)
			}
			if lookupDeprecations != resolveDeprecations {
				t.Errorf("Lookup recorded %d deprecation(s) and ResolveHTTPPort recorded %d — "+
					"the two are no longer applying the same alias rule",
					lookupDeprecations, resolveDeprecations)
			}
			// And when both are happy and a value really was set, the
			// number Lookup hands back is the port ResolveHTTPPort chose.
			if lookupErr == nil && resolveErr == nil && lookupPresent {
				wanted, convErr := strconv.Atoi(strings.TrimSpace(lookupValue))
				if convErr == nil {
					setPortEnv(t, env.canonical, env.legacy)
					got, portErr := ResolveHTTPPort(theFallback)
					if portErr != nil {
						t.Fatalf("ResolveHTTPPort refused a value Lookup accepted: %v", portErr)
					}
					if got != wanted {
						t.Errorf("Lookup resolved to %d and ResolveHTTPPort chose %d", wanted, got)
					}
				}
			}
		})
	}
}

// TestIsServiceLinkValue pins what counts as "Kubernetes wrote this".
//
// The false cases matter more than the true ones: every value that is
// wrongly called a service link is a value silently thrown away.
func TestIsServiceLinkValue(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"tcp://10.96.35.88:80", true},
		{"udp://10.96.35.88:53", true},
		{"sctp://10.96.35.88:9999", true},
		{"tcp://[fd00::1]:80", true},

		{"8080", false},
		{"", false},
		{"80x", false},
		{"tcp://10.96.35.88", false},     // no port
		{"http://10.96.35.88:80", false}, // not a protocol a Service port can have
		{"tcp://10.96.35.88:80/path", false},
		{" tcp://10.96.35.88:80", false},
		{"tcp://10.96.35.88:80 ", false},
		{"TCP://10.96.35.88:80", false},
	} {
		if got := isServiceLinkValue(tc.value); got != tc.want {
			t.Errorf("isServiceLinkValue(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------

// assertNamesSettingNotValue is this project's error rule, as an
// assertion: an error about configuration names the SETTING and never
// repeats what was in it. Values reach logs and issue trackers.
func assertNamesSettingNotValue(t *testing.T, err error, setting, value string) {
	t.Helper()
	message := err.Error()
	if !strings.Contains(message, setting) {
		t.Errorf("the error does not name %s, so the operator cannot tell what to fix: %q", setting, message)
	}
	// The message states the allowed range, so a one- or two-character
	// value can appear in it innocently ("1", "0" inside "8080" and so
	// on). Anything longer has no business being there.
	if len(value) >= 3 && strings.Contains(message, value) {
		t.Errorf("the error repeats the offending value %q: %q", value, message)
	}
}

func atoiForTest(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		t.Fatalf("test fixture %q is not a number: %v", s, err)
	}
	return n
}
