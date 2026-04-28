package signing

import (
	"context"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/testing/ca"

	"github.com/MoranWeissman/sharko/internal/catalog/sources"
)

// recordingTrustHandler captures every slog.Record handed to it so the
// V123-PR-B (H6) anchor-warning tests can assert on warning count +
// content. Mirrors the recordedLogger pattern in verify_test.go but lives
// here so trust_test.go is self-contained.
type recordingTrustHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingTrustHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *recordingTrustHandler) Handle(_ context.Context, rec slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, rec)
	return nil
}

func (h *recordingTrustHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *recordingTrustHandler) WithGroup(_ string) slog.Handler      { return h }

// warnings returns just the WARN-level records. The anchor diagnostic is
// emitted at WARN so we filter for it explicitly — drop INFO/DEBUG/ERROR
// noise the package may emit later.
func (h *recordingTrustHandler) warnings() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []slog.Record
	for _, r := range h.records {
		if r.Level == slog.LevelWarn {
			out = append(out, r)
		}
	}
	return out
}

// patternAttr extracts the `pattern` structured attr off a record. Used
// by the table-driven tests to assert WHICH pattern triggered the warning,
// not just that a warning of some shape was emitted.
func patternAttr(rec slog.Record) string {
	var got string
	rec.Attrs(func(a slog.Attr) bool {
		if a.Key == "pattern" {
			got = a.Value.String()
			return false
		}
		return true
	})
	return got
}

// installRecordingTrustLogger wires the package's trustLogger to a fresh
// recording handler and returns the handler + a cleanup. Caller defers
// cleanup() so the swap is contained to its test.
func installRecordingTrustLogger(t *testing.T) *recordingTrustHandler {
	t.Helper()
	h := &recordingTrustHandler{}
	cleanup := SetTrustLoggerForTest(slog.New(h))
	t.Cleanup(cleanup)
	return h
}

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

// TestLoadTrustPolicy_NoMatchRejects — V123-2.6 explicit "no match"
// leg of the trust-policy table-driven AC. The other trust_test.go
// cases cover the parser's expansion / validation behaviour; this one
// exercises the runtime rejection of a signature whose subject doesn't
// match any policy regex.
//
// Setup: build a TrustPolicy whose only pattern matches the
// reserved-by-RFC-6761 `example.invalid` TLD (a regex that genuinely
// cannot match a real Sigstore SAN). Sign a payload via VirtualSigstore
// with a normal SAN, run the verifier, and assert:
//
//   - public return is (false, "", nil)
//   - log reason contains "not in trust policy" (proves the
//     untrusted-identity branch was taken, not the sig-mismatch branch)
//
// This is the third leg of the AC's table-driven trust-policy
// requirement: default-regex hits, custom-regex hits, no-match-rejects.
// The first two are implicit in the existing parser tests + the
// happy-path verifier test; the third was missing as a standalone case
// at the regex-match layer.
func TestLoadTrustPolicy_NoMatchRejects(t *testing.T) {
	vs, err := ca.NewVirtualSigstore()
	if err != nil {
		t.Fatalf("NewVirtualSigstore: %v", err)
	}

	// SAN that any reasonable Sigstore signer would produce. The
	// trust policy below cannot match it.
	const realSubject = "https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@refs/tags/v1.23.0"
	payload := []byte("payload signed by a legitimate identity")
	entity, err := vs.Sign(realSubject, "https://oidc.example.com", payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Trust policy that intentionally does not match the SAN above.
	// example.invalid is RFC-6761-reserved and will never appear in a
	// real Sigstore subject — choosing it removes any chance of
	// accidental overlap if a future test fixture changes.
	policy := sources.TrustPolicy{
		Identities: []string{`^https://example\.invalid/.*$`},
	}

	v, rec := withRecordedLogger(t, vs)
	verified, issuer, verr := v.verifyEntity(
		context.Background(),
		entity,
		payload,
		policy,
		"https://example.invalid/x.bundle",
	)
	if verr != nil {
		t.Fatalf("verifyEntity: unexpected err: %v (no-match must NOT be err)", verr)
	}
	if verified {
		t.Errorf("expected verified=false on no-match policy")
	}
	if issuer != "" {
		t.Errorf("expected empty issuer on no-match, got %q", issuer)
	}
	if got := rec.LastReason(); !strings.Contains(got, "not in trust policy") {
		t.Errorf("expected log reason to contain %q (untrusted-identity branch); got %q",
			"not in trust policy", got)
	}
}

// --- V123-PR-B / H6: unanchored-pattern warning ---------------------------
//
// LoadTrustPolicyFromEnv emits a slog.Warn for every operator-supplied
// pattern that lacks both `^` and `$` anchors. Defaults never warn (they
// are already anchored and explicitly skipped). The behaviour is
// defense-in-depth: cosign-style identity matching is substring-based by
// default, and an unanchored regex like `github.com/myorg/` trusts any
// SAN that *contains* that substring — including hostile attacker URLs
// that embed the trusted prefix as a query parameter. We don't auto-wrap
// (that would silently change operator intent); we surface the trade-off.

// TestLoadTrustPolicy_UnanchoredCustomEmitsWarning — a single unanchored
// operator pattern triggers exactly one warning, naming the offending
// pattern in both the message and the structured `pattern` attr.
func TestLoadTrustPolicy_UnanchoredCustomEmitsWarning(t *testing.T) {
	h := installRecordingTrustLogger(t)

	const unanchored = `github\.com/myorg/`
	t.Setenv(EnvTrustedIdentities, unanchored)
	pol, err := LoadTrustPolicyFromEnv()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(pol.Identities) != 1 || pol.Identities[0] != unanchored {
		t.Fatalf("policy did not preserve operator pattern: %+v", pol.Identities)
	}

	warns := h.warnings()
	if len(warns) != 1 {
		t.Fatalf("expected exactly 1 anchor warning, got %d", len(warns))
	}
	rec := warns[0]
	if got := rec.Message; !strings.Contains(got, "not fully anchored") {
		t.Errorf("warning message lacks 'not fully anchored' marker: %q", got)
	}
	if got := rec.Message; !strings.Contains(got, "regexp.MatchString") {
		t.Errorf("warning message lacks regexp.MatchString explanation: %q", got)
	}
	if got := rec.Message; !strings.Contains(got, unanchored) {
		t.Errorf("warning message does not mention offending pattern %q: %q", unanchored, got)
	}
	if got := patternAttr(rec); got != unanchored {
		t.Errorf("structured pattern attr = %q, want %q", got, unanchored)
	}
}

// TestLoadTrustPolicy_AnchoredCustomNoWarning — a fully-anchored operator
// pattern (^...$) does NOT warn. This is the "operator did the right
// thing" path; we must not nag them.
func TestLoadTrustPolicy_AnchoredCustomNoWarning(t *testing.T) {
	h := installRecordingTrustLogger(t)

	const anchored = `^https://github\.com/acme/.*$`
	t.Setenv(EnvTrustedIdentities, anchored)
	if _, err := LoadTrustPolicyFromEnv(); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got := len(h.warnings()); got != 0 {
		t.Errorf("expected no warnings for anchored pattern, got %d: %+v", got, h.warnings())
	}
}

// TestLoadTrustPolicy_DefaultsExpansionNoWarning — neither <defaults>
// expansion nor the unset/empty fallback should warn. The default
// patterns are already anchored AND explicitly skipped in the warning
// loop (so a future un-anchored default would NOT regress the diagnostic
// — but every current default IS anchored, so this is double belt-and-
// braces).
func TestLoadTrustPolicy_DefaultsExpansionNoWarning(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		h := installRecordingTrustLogger(t)
		if v, ok := os.LookupEnv(EnvTrustedIdentities); ok {
			if err := os.Unsetenv(EnvTrustedIdentities); err != nil {
				t.Fatalf("unsetenv: %v", err)
			}
			t.Cleanup(func() { _ = os.Setenv(EnvTrustedIdentities, v) })
		}
		if _, err := LoadTrustPolicyFromEnv(); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got := len(h.warnings()); got != 0 {
			t.Errorf("expected no warnings for unset env, got %d: %+v", got, h.warnings())
		}
	})

	t.Run("defaults_token", func(t *testing.T) {
		h := installRecordingTrustLogger(t)
		t.Setenv(EnvTrustedIdentities, DefaultsToken)
		if _, err := LoadTrustPolicyFromEnv(); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got := len(h.warnings()); got != 0 {
			t.Errorf("expected no warnings for <defaults> token, got %d: %+v", got, h.warnings())
		}
	})
}

// TestLoadTrustPolicy_MixedAnchoredUnanchored — `<defaults>,unanchored,
// ^anchored$` produces exactly one warning (for `unanchored`), in
// declaration order. Defaults stay quiet; the anchored custom pattern
// stays quiet; only the unanchored custom pattern triggers.
func TestLoadTrustPolicy_MixedAnchoredUnanchored(t *testing.T) {
	h := installRecordingTrustLogger(t)

	const unanchored = `github\.com/loose-org/`
	const anchored = `^https://github\.com/strict-org/.*$`
	t.Setenv(EnvTrustedIdentities, DefaultsToken+","+unanchored+","+anchored)
	pol, err := LoadTrustPolicyFromEnv()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got, want := len(pol.Identities), len(DefaultTrustedIdentities)+2; got != want {
		t.Fatalf("got %d identities, want %d", got, want)
	}

	warns := h.warnings()
	if len(warns) != 1 {
		t.Fatalf("expected exactly 1 warning (unanchored only), got %d: %+v", len(warns), warns)
	}
	if got := patternAttr(warns[0]); got != unanchored {
		t.Errorf("warning's pattern attr = %q, want %q", got, unanchored)
	}
}

// --- V123-PR-E: workflow_run cert SAN regression pin --------------------
//
// rc.2 cut clean and signatures cryptographically verified, but every entry
// was rejected with `signature verified but identity not in trust policy`
// because the default Sharko regex anchored to `refs/tags/v.*` while the
// actual cosign cert SAN is
// `release.yml@refs/heads/main` (Fulcio encodes `job_workflow_ref`, the
// workflow file's ref, not the triggering tag's ref). Tag-context is
// enforced by the `if: startsWith(workflow_run.head_branch, 'v')` guard in
// release.yml. These two tests pin the corrected regex so a future "fix"
// back to `refs/tags/v.*` fails immediately.

// TestDefaultTrustedIdentities_MatchesWorkflowRunSAN — the Sharko default
// must match the actual cert SAN that Fulcio mints for our workflow_run-
// triggered release.yml. Asserts exactly one default pattern matches the
// canonical SAN string (the Sharko default; the CNCF default cannot match
// a sharko URL).
func TestDefaultTrustedIdentities_MatchesWorkflowRunSAN(t *testing.T) {
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
	const realSAN = "https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@refs/heads/main"
	matches := 0
	for _, p := range pol.Identities {
		re := regexp.MustCompile(p)
		if re.MatchString(realSAN) {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("expected exactly 1 default pattern to match SAN %q, got %d (defaults=%v)",
			realSAN, matches, pol.Identities)
	}
}

// TestDefaultTrustedIdentities_RejectsNonMainBranch — the corrected default
// must remain anchored to `main`, NOT `refs/heads/.*`. A SAN whose ref is
// any other branch (e.g. a feature branch CI run) must NOT match any
// Sharko default. (The CNCF default cannot match a sharko URL anyway, so
// this effectively asserts the Sharko regex anchors to `main$`.)
func TestDefaultTrustedIdentities_RejectsNonMainBranch(t *testing.T) {
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
	const featureBranchSAN = "https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@refs/heads/feature-branch"
	for _, p := range pol.Identities {
		re := regexp.MustCompile(p)
		if re.MatchString(featureBranchSAN) {
			t.Errorf("default pattern %q unexpectedly matched non-main SAN %q",
				p, featureBranchSAN)
		}
	}
}

// TestLoadTrustPolicy_PartiallyAnchored — patterns missing ONE anchor
// (only `^` or only `$`) still warn. "Fully anchored" requires both;
// `^foo` matches anything starting with foo, `bar$` matches anything
// ending with bar — both can be exploited the same way an unanchored
// substring can be.
func TestLoadTrustPolicy_PartiallyAnchored(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
	}{
		{"start_only", `^github\.com/myorg/`},
		{"end_only", `github\.com/myorg/.*$`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := installRecordingTrustLogger(t)
			t.Setenv(EnvTrustedIdentities, tc.pattern)
			if _, err := LoadTrustPolicyFromEnv(); err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			warns := h.warnings()
			if len(warns) != 1 {
				t.Fatalf("expected 1 warning for partially-anchored pattern, got %d", len(warns))
			}
			if got := patternAttr(warns[0]); got != tc.pattern {
				t.Errorf("warning pattern attr = %q, want %q", got, tc.pattern)
			}
		})
	}
}
