package signing

import (
	"os"
	"strings"
	"testing"
)

// TestLoadTrustPolicy_UnsetUsesDefaults — env var truly unset → defaults
// only. We explicitly Unsetenv first because t.Setenv("X", "") sets the
// var to the empty string rather than removing it; this case is about
// the "var is not in the environment at all" path. Case 2 covers the
// "var is set to empty string" path.
func TestLoadTrustPolicy_UnsetUsesDefaults(t *testing.T) {
	if v, ok := os.LookupEnv(EnvTrustedIdentities); ok {
		if err := os.Unsetenv(EnvTrustedIdentities); err != nil {
			t.Fatalf("unsetenv: %v", err)
		}
		t.Cleanup(func() { _ = os.Setenv(EnvTrustedIdentities, v) })
	}
	pol, err := LoadTrustPolicyFromEnv()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got, want := len(pol.Identities), len(DefaultTrustedIdentities); got != want {
		t.Fatalf("got %d identities, want %d (defaults)", got, want)
	}
	for i, want := range DefaultTrustedIdentities {
		if pol.Identities[i] != want {
			t.Errorf("identity[%d] = %q, want %q", i, pol.Identities[i], want)
		}
	}
}

// TestLoadTrustPolicy_EmptyUsesDefaults — env var set to empty string
// (e.g., `SHARKO_CATALOG_TRUSTED_IDENTITIES=`) → also defaults. Both
// "unset" and "empty" collapse to the conservative fallback; the
// `^$` regex is the documented "trust nothing" escape hatch.
func TestLoadTrustPolicy_EmptyUsesDefaults(t *testing.T) {
	t.Setenv(EnvTrustedIdentities, "")
	pol, err := LoadTrustPolicyFromEnv()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got, want := len(pol.Identities), len(DefaultTrustedIdentities); got != want {
		t.Fatalf("got %d identities, want %d (defaults)", got, want)
	}
	for i, want := range DefaultTrustedIdentities {
		if pol.Identities[i] != want {
			t.Errorf("identity[%d] = %q, want %q", i, pol.Identities[i], want)
		}
	}
}

// TestLoadTrustPolicy_DefaultsToken — env var is exactly the magic
// token → defaults expanded in-place (same outcome as unset/empty,
// but reached via the explicit-opt-in path).
func TestLoadTrustPolicy_DefaultsToken(t *testing.T) {
	t.Setenv(EnvTrustedIdentities, DefaultsToken)
	pol, err := LoadTrustPolicyFromEnv()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got, want := len(pol.Identities), len(DefaultTrustedIdentities); got != want {
		t.Fatalf("got %d identities, want %d", got, want)
	}
	for i, want := range DefaultTrustedIdentities {
		if pol.Identities[i] != want {
			t.Errorf("identity[%d] = %q, want %q", i, pol.Identities[i], want)
		}
	}
}

// TestLoadTrustPolicy_DefaultsTokenPlusCustom — `<defaults>,custom`
// expands to `default0, default1, custom`. Order matters: the
// verifier's first-match-wins iteration is keyed off this slice, so
// we assert position explicitly.
func TestLoadTrustPolicy_DefaultsTokenPlusCustom(t *testing.T) {
	const custom = `^https://github\.com/acme/.*$`
	t.Setenv(EnvTrustedIdentities, DefaultsToken+","+custom)
	pol, err := LoadTrustPolicyFromEnv()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	wantLen := len(DefaultTrustedIdentities) + 1
	if got := len(pol.Identities); got != wantLen {
		t.Fatalf("got %d identities, want %d", got, wantLen)
	}
	for i, want := range DefaultTrustedIdentities {
		if pol.Identities[i] != want {
			t.Errorf("identity[%d] = %q, want %q", i, pol.Identities[i], want)
		}
	}
	if got := pol.Identities[len(DefaultTrustedIdentities)]; got != custom {
		t.Errorf("trailing identity = %q, want %q", got, custom)
	}
}

// TestLoadTrustPolicy_CustomPlusDefaults — `custom,<defaults>` expands
// to `custom, default0, default1`. Order is preserved exactly as the
// operator wrote it; the parser does not reorder for "defaults always
// first" or anything similar.
func TestLoadTrustPolicy_CustomPlusDefaults(t *testing.T) {
	const custom = `^https://github\.com/acme/.*$`
	t.Setenv(EnvTrustedIdentities, custom+","+DefaultsToken)
	pol, err := LoadTrustPolicyFromEnv()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	wantLen := len(DefaultTrustedIdentities) + 1
	if got := len(pol.Identities); got != wantLen {
		t.Fatalf("got %d identities, want %d", got, wantLen)
	}
	if pol.Identities[0] != custom {
		t.Errorf("identity[0] = %q, want %q", pol.Identities[0], custom)
	}
	for i, want := range DefaultTrustedIdentities {
		if pol.Identities[i+1] != want {
			t.Errorf("identity[%d] = %q, want %q", i+1, pol.Identities[i+1], want)
		}
	}
}

// TestLoadTrustPolicy_OnlyCustomNoDefaults — single custom regex (no
// `<defaults>` token) → ONLY the custom regex is active. Defaults are
// NOT silently merged. This is the "operator overrides entirely"
// path; opting out of defaults must be an explicit choice.
func TestLoadTrustPolicy_OnlyCustomNoDefaults(t *testing.T) {
	const custom = `^https://github\.com/acme/.*$`
	t.Setenv(EnvTrustedIdentities, custom)
	pol, err := LoadTrustPolicyFromEnv()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got := len(pol.Identities); got != 1 {
		t.Fatalf("got %d identities, want 1", got)
	}
	if pol.Identities[0] != custom {
		t.Errorf("identity[0] = %q, want %q", pol.Identities[0], custom)
	}
	// Defensive: make sure neither default leaked in.
	for _, def := range DefaultTrustedIdentities {
		for _, got := range pol.Identities {
			if got == def {
				t.Errorf("default %q leaked into custom-only policy", def)
			}
		}
	}
}

// TestLoadTrustPolicy_TrimsWhitespace — the parser must trim
// leading/trailing whitespace around each comma-separated piece AND
// around the magic token. Operators copy-paste from YAML / shell
// heredocs; tolerating padding spares them a "why doesn't my regex
// work" debugging session.
func TestLoadTrustPolicy_TrimsWhitespace(t *testing.T) {
	const custom = `^https://github\.com/acme/.*$`
	t.Setenv(EnvTrustedIdentities,
		"  "+custom+"  ,  "+DefaultsToken+"  ")
	pol, err := LoadTrustPolicyFromEnv()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	wantLen := len(DefaultTrustedIdentities) + 1
	if got := len(pol.Identities); got != wantLen {
		t.Fatalf("got %d identities, want %d", got, wantLen)
	}
	for _, p := range pol.Identities {
		if strings.TrimSpace(p) != p {
			t.Errorf("identity %q has untrimmed whitespace", p)
		}
	}
	if pol.Identities[0] != custom {
		t.Errorf("identity[0] = %q, want %q", pol.Identities[0], custom)
	}
	for i, want := range DefaultTrustedIdentities {
		if pol.Identities[i+1] != want {
			t.Errorf("identity[%d] = %q, want %q", i+1, pol.Identities[i+1], want)
		}
	}
}

// TestLoadTrustPolicy_InvalidRegexErrors — a malformed pattern is a
// fatal startup error. The error must mention the offending pattern
// so the operator can find and fix it without grepping logs.
func TestLoadTrustPolicy_InvalidRegexErrors(t *testing.T) {
	const bad = "[unbalanced"
	t.Setenv(EnvTrustedIdentities, bad)
	pol, err := LoadTrustPolicyFromEnv()
	if err == nil {
		t.Fatalf("expected error for bad regex, got nil (policy %+v)", pol)
	}
	if !strings.Contains(err.Error(), bad) {
		t.Errorf("error %q does not mention offending pattern %q", err.Error(), bad)
	}
	if !strings.Contains(err.Error(), EnvTrustedIdentities) {
		t.Errorf("error %q does not mention env var name %q", err.Error(), EnvTrustedIdentities)
	}
}
