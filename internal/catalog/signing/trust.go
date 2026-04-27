// Package signing implements per-entry cosign-keyless verification.
// trust.go owns the SHARKO_CATALOG_TRUSTED_IDENTITIES parser (V123-2.3).
//
// The parser is a startup-time helper — operators set the env var, Sharko
// validates and compiles every regex pattern, and the resulting
// sources.TrustPolicy is handed to the verifier built in V123-2.2. The
// posture mirrors the V123-1.1 SHARKO_CATALOG_URLS parser: validate hard
// at startup so the operator notices a broken policy immediately rather
// than later when an entry mysteriously refuses to verify.
package signing

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/MoranWeissman/sharko/internal/catalog/sources"
)

// trustLogger is overridable for tests. Production uses
// slog.Default().With("component", "catalog-trust-policy") on every call;
// tests swap in a recording handler via SetTrustLoggerForTest to assert
// on emitted warnings.
var trustLogger = func() *slog.Logger {
	return slog.Default().With("component", "catalog-trust-policy")
}

// SetTrustLoggerForTest overrides the package-level trustLogger function
// so a test can install a recording handler. Returns a cleanup function
// that restores the production logger; tests should defer the cleanup.
//
// Test-only — production callers never invoke this. The override is the
// minimal seam needed to assert on slog.Warn output without wiring the
// logger through the LoadTrustPolicyFromEnv signature (which would be a
// gratuitous public API change for a defense-in-depth diagnostic).
func SetTrustLoggerForTest(l *slog.Logger) func() {
	prev := trustLogger
	trustLogger = func() *slog.Logger { return l }
	return func() { trustLogger = prev }
}

// EnvTrustedIdentities is the env var operators set to override or extend
// the default trusted-identity regex list.
const EnvTrustedIdentities = "SHARKO_CATALOG_TRUSTED_IDENTITIES"

// DefaultsToken is the literal placeholder operators include in the env
// var to expand to DefaultTrustedIdentities at the matching position.
// Case-sensitive: `<defaults>` matches; `<DEFAULTS>` does not.
const DefaultsToken = "<defaults>"

// DefaultTrustedIdentities is the list of regex patterns that match
// Sigstore cert SANs Sharko trusts out of the box:
//
//   - CNCF org workflows (any project, any workflow file). Sharko's
//     positioning targets CNCF-curated addons, so trusting any signed
//     CNCF workflow is a reasonable conservative default.
//   - Sharko's own release workflow (signs the embedded catalog under
//     V123-2.5). Without this default, fresh installs would see the
//     embedded catalog as Unverified once the release pipeline starts
//     signing entries.
//
// Operators include the literal token "<defaults>" in
// SHARKO_CATALOG_TRUSTED_IDENTITIES to keep these while adding their own.
// To opt out entirely, set the env var to a regex that matches nothing
// (`^$`) — see LoadTrustPolicyFromEnv.
var DefaultTrustedIdentities = []string{
	`^https://github\.com/cncf/.*/\.github/workflows/.*$`,
	`^https://github\.com/MoranWeissman/sharko/\.github/workflows/release\.yml@refs/tags/v.*$`,
}

// LoadTrustPolicyFromEnv reads SHARKO_CATALOG_TRUSTED_IDENTITIES, expands
// the <defaults> token at each matching position, validates that every
// pattern compiles as a Go regexp, and returns the canonical
// sources.TrustPolicy.
//
// Behaviour:
//
//   - Env unset OR empty -> defaults only (the conservative fallback).
//   - "<defaults>" token -> expanded inline at that position; preserves
//     declaration order so the first-match-wins semantics in the verifier
//     stay deterministic. Multiple "<defaults>" tokens expand at each
//     occurrence (rare but well-defined).
//   - Any pattern fails to compile -> non-nil error (intended fatal at
//     startup; same posture as the V123-1.1 SHARKO_CATALOG_URLS parser).
//   - Operators who literally want "trust nothing" set the env to "^$"
//     (a regex that matches no string).
//
// The returned TrustPolicy.Identities slice is the raw pattern strings —
// the verifier compiles them again per-call (cheap, and keeps the env
// reload story simple). The double compile here is the validation pass;
// rejecting bad input at startup is the only useful contract.
func LoadTrustPolicyFromEnv() (sources.TrustPolicy, error) {
	raw := strings.TrimSpace(os.Getenv(EnvTrustedIdentities))
	var patterns []string
	if raw == "" {
		patterns = append(patterns, DefaultTrustedIdentities...)
	} else {
		for _, piece := range strings.Split(raw, ",") {
			p := strings.TrimSpace(piece)
			if p == "" {
				// Tolerate stray commas (e.g. "a,,b" or trailing ",").
				continue
			}
			if p == DefaultsToken {
				patterns = append(patterns, DefaultTrustedIdentities...)
				continue
			}
			patterns = append(patterns, p)
		}
	}
	for _, p := range patterns {
		if _, err := regexp.Compile(p); err != nil {
			return sources.TrustPolicy{}, fmt.Errorf(
				"%s: invalid regex %q: %w", EnvTrustedIdentities, p, err)
		}
	}

	// V123-PR-B (H6): defense-in-depth warning. cosign-style identity
	// matching is regexp.MatchString, which is *substring* by default —
	// `github.com/myorg/` is a perfectly valid pattern that trusts ANY
	// SAN containing that substring (e.g. an attacker's
	// `https://attacker.example.com/?fake=github.com/myorg/`). That may
	// be intentional (operator wants org-wide trust) or it may be a
	// mistake. We do NOT auto-anchor — that would silently change the
	// operator's trust posture; we just emit a startup warning so the
	// behaviour is explicit. Defaults are already anchored
	// (`^...$`) so they never trigger the warning, regardless of how
	// many times an operator includes <defaults>.
	defaults := make(map[string]struct{}, len(DefaultTrustedIdentities))
	for _, d := range DefaultTrustedIdentities {
		defaults[d] = struct{}{}
	}
	logger := trustLogger()
	for _, p := range patterns {
		if _, isDefault := defaults[p]; isDefault {
			continue
		}
		if !strings.HasPrefix(p, "^") || !strings.HasSuffix(p, "$") {
			logger.Warn(
				fmt.Sprintf(
					"trust policy pattern is not fully anchored — '%s'. "+
						"cosign-style identity matching is regexp.MatchString (substring); "+
						"add ^ and $ unless substring matching is intentional",
					p,
				),
				"pattern", p,
			)
		}
	}

	return sources.TrustPolicy{Identities: patterns}, nil
}
