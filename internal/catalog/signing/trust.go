// Package signing implements per-entry cosign-keyless verification.
// trust.go owns the SHARKO_CATALOG_TRUSTED_IDENTITIES parser.
//
// The parser is a startup-time helper — operators set the env var,
// Sharko validates and compiles every regex pattern, and the resulting
// sources.TrustPolicy is handed to the verifier. Validation runs hard
// at startup so the operator notices a broken policy immediately
// rather than later when an entry mysteriously refuses to verify.
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

// EnvTrustedWorkflowRef is the env var operators set to override the
// default workflow_ref claim assertion (defense-in-depth layered on
// top of EnvTrustedIdentities). The verifier compares the configured
// regex against the cert's GitHub workflow_ref claim AFTER the SAN
// regex check passes. Unset / empty falls back to
// DefaultTrustedWorkflowRef.
const EnvTrustedWorkflowRef = "SHARKO_CATALOG_TRUSTED_WORKFLOW_REF"

// DefaultTrustedWorkflowRef is the conservative default regex applied
// to the cert's GitHub workflow_ref claim. Sharko's own release.yml is
// gated to only sign on tag refs, and Fulcio records that ref as the
// cert's workflow_ref extension. Anchoring the default to
// `^refs/tags/v.*$` cryptographically asserts the already-policy
// behaviour: an attacker who matched the SAN regex must ALSO have come
// from a tag-built workflow, not a feature-branch CI run whose trigger
// guard was bypassed.
//
// Operators with non-tag-driven release pipelines override via
// EnvTrustedWorkflowRef.
const DefaultTrustedWorkflowRef = `^refs/tags/v.*$`

// EmbeddedCatalogWorkflowRef is the workflow_ref claim assertion applied
// to Sharko's OWN embedded catalogue, and to nothing else.
//
// Why it differs from DefaultTrustedWorkflowRef. That default says
// `^refs/tags/v.*$`, and for a `workflow_run`-triggered workflow no
// certificate can ever satisfy it. Fulcio stamps workflow_ref from the
// ref the workflow FILE lives on, which for this trigger is always
// `refs/heads/main`, never the tag being built. Measured on the
// certificates inside all 45 published v4.0.1 bundles: every one carries
// workflow_ref `refs/heads/main`. The two shipped defaults are therefore
// mutually unsatisfiable for Sharko's own signatures, and the shipped
// runtime marks every entry of its own catalogue unverified as a result.
// TestShippedDefaults_AreMutuallySatisfiable pins that this can never
// come back.
//
// DefaultTrustedWorkflowRef is deliberately NOT changed. It governs
// third-party catalogues, whose publishers do sign from tags, and
// widening or moving it would change documented behaviour for every
// operator. The narrow fix is to apply the ref Sharko's own workflow
// actually mints, to Sharko's own catalogue only.
//
// This assertion is now the weaker of the two claim checks on the
// embedded path: it repeats what DefaultTrustedIdentities' Sharko
// pattern already pins (that pattern ends `@refs/heads/main`). The real
// control is the release-commit binding — see
// EmbeddedCatalogTrustPolicy. The tag-shaped assertion is not dropped so
// much as replaced by a stricter one landing in the same change: instead
// of "came from some tag" the certificate must now claim the exact
// commit this build was released from.
const EmbeddedCatalogWorkflowRef = `^refs/heads/main$`

// commitSHALen is the length of a full git SHA-1 commit hash in hex.
// Sharko compares full hashes only — never a prefix. A prefix comparison
// is a weaker check (two different commits can share a prefix, and a
// short hash is not a stable identifier as a repository grows), and the
// values Sharko has on both sides are full, so there is nothing to gain
// by loosening it.
const commitSHALen = 40

// IsFullCommitSHA reports whether s is exactly 40 hexadecimal characters
// — the shape of a full git commit hash. Case-insensitive on input;
// callers lowercase before storing.
//
// Everything that is NOT a full commit hash is treated the same way: the
// literal `dev` that cmd/sharko/root.go declares and the Dockerfile
// defaults to, the short hash the local `make build` target stamps, and
// the empty string. None of them identifies a release, so none of them
// can stand in for one.
func IsFullCommitSHA(s string) bool {
	if len(s) != commitSHALen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// EmbeddedCatalogTrustPolicy returns the trust policy for Sharko's OWN
// embedded catalogue: the base policy, plus the release-commit binding,
// plus the workflow_ref claim Sharko's own release workflow actually
// mints.
//
// This function is the structural boundary the whole design rests on.
// RequireReleaseCommit can only be turned on here, so a catalogue that
// does not come through this constructor cannot be subjected to the
// release-commit requirement. cmd/sharko/serve.go hands the result to
// the embedded-catalogue loader and hands the UNMODIFIED base policy to
// the third-party fetcher; TestThirdPartyPolicy_NeverCarriesTheCommitRequirement
// and the wiring test in cmd/sharko pin both halves.
//
// buildCommit is the binary's own build-stamped release commit — the
// value the release pipeline writes with `-X main.commit`. Why that
// value, and why it is independent of the certificate:
//
//   - It is fixed at LINK time by the release job from the tag being
//     released. Nothing the certificate says can change it, so comparing
//     the certificate's claim against it is a real comparison and not the
//     certificate being compared with itself.
//   - It is NOT the current tip of `main`. A release commit is normally
//     an ancestor of `main` by the time anyone runs the binary — as of
//     this writing v4.0.1's commit sits 57 commits behind `main` — and
//     using the tip would refuse the genuine catalogue of every release
//     that is not the newest commit in the repository.
//   - It travels with the artifact. A binary carries the commit it was
//     built from wherever it is copied, with no configuration and no
//     network.
//
// When buildCommit is not a full commit hash — a `go build` with no
// ldflags, `make build`'s short hash, or a container image whose COMMIT
// build argument was not supplied — ReleaseCommit is left empty while
// RequireReleaseCommit stays TRUE. That combination is a refusal with a
// named reason, not a skip: see assertReleaseCommit.
//
// The operator's own SHARKO_CATALOG_TRUSTED_WORKFLOW_REF setting still
// wins. If they set it, it is honoured here too — an operator who has
// deliberately configured a workflow_ref policy does not get it silently
// replaced.
func EmbeddedCatalogTrustPolicy(base sources.TrustPolicy, buildCommit string) sources.TrustPolicy {
	p := base
	// Copy the slice so a later edit to one policy cannot reach the other.
	p.Identities = append([]string(nil), base.Identities...)

	if strings.TrimSpace(os.Getenv(EnvTrustedWorkflowRef)) == "" {
		p.WorkflowRef = EmbeddedCatalogWorkflowRef
	}

	p.RequireReleaseCommit = true
	p.ReleaseCommit = ""
	if IsFullCommitSHA(buildCommit) {
		p.ReleaseCommit = strings.ToLower(buildCommit)
	}
	return p
}

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
//   - Sharko's own release workflow (signs the embedded catalog).
//     Without this default, fresh installs would see the embedded
//     catalog as Unverified once the release pipeline starts signing
//     entries.
//
// Important — why the Sharko default ends in `@refs/heads/main` rather
// than the triggering tag's ref: Sigstore Fulcio mints certs whose SAN
// reflects the GitHub Actions OIDC `job_workflow_ref` claim, which is
// the ref where the workflow YAML *file* lives at job start. release.yml
// runs as a `workflow_run`-triggered job, so its `job_workflow_ref` is
// always `refs/heads/main` — the actual triggering tag is encoded in
// other OIDC claims (`ref`, `event_name`), not in the SAN. Tag-only
// invocation is enforced by the
// `if: startsWith(github.event.workflow_run.head_branch, 'v')` guard in
// release.yml, NOT by the cert SAN. Anchoring this default to `main`
// (rather than e.g. `refs/heads/.*`) keeps it tight: only signatures
// produced by the workflow file as it lives on `main` are trusted.
//
// Operators include the literal token "<defaults>" in
// SHARKO_CATALOG_TRUSTED_IDENTITIES to keep these while adding their own.
// To opt out entirely, set the env var to a regex that matches nothing
// (`^$`) — see LoadTrustPolicyFromEnv.
var DefaultTrustedIdentities = []string{
	`^https://github\.com/cncf/.*/\.github/workflows/.*$`,
	`^https://github\.com/MoranWeissman/sharko/\.github/workflows/release\.yml@refs/heads/main$`,
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
//     startup; same posture as the SHARKO_CATALOG_URLS parser).
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
			// BF1: the pattern used to be formatted into the MESSAGE as
			// well as passed as an attribute. A log message goes to the
			// collector exactly as assembled — the redaction sink rewrites
			// attributes and never touches the message — so an operator's
			// own SHARKO_CATALOG_TRUSTED_IDENTITIES text was reaching the
			// log by the one route nothing inspects. It is still reported,
			// once, as the attribute it always also was.
			logger.Warn(
				"trust policy pattern is not fully anchored. "+
					"cosign-style identity matching is regexp.MatchString (substring); "+
					"add ^ and $ unless substring matching is intentional",
				"pattern", p,
			)
		}
	}

	// Load the workflow_ref claim assertion. Unset / empty falls back
	// to DefaultTrustedWorkflowRef — secure default. An
	// operator who explicitly wants to disable the claim assertion (e.g.
	// to verify entries signed by a non-GitHub-Actions issuer that
	// doesn't mint a workflow_ref extension at all) sets the env var to
	// ".*" — a regex that matches everything, INCLUDING the empty claim
	// extracted from a non-GHA cert.
	//
	// Validation parity with Identities: the regex is compiled here so
	// a malformed pattern fails at startup, not later inside the
	// verifier. The verifier compiles it again per-call (matches the
	// Identities posture).
	workflowRef := strings.TrimSpace(os.Getenv(EnvTrustedWorkflowRef))
	if workflowRef == "" {
		workflowRef = DefaultTrustedWorkflowRef
	}
	if _, err := regexp.Compile(workflowRef); err != nil {
		return sources.TrustPolicy{}, fmt.Errorf(
			"%s: invalid regex %q: %w", EnvTrustedWorkflowRef, workflowRef, err)
	}

	return sources.TrustPolicy{
		Identities:  patterns,
		WorkflowRef: workflowRef,
	}, nil
}
