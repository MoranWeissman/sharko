package main

import (
	"github.com/MoranWeissman/sharko/internal/catalog/signing"
	"github.com/MoranWeissman/sharko/internal/catalog/sources"
)

// catalog_trust.go owns the one decision that separates Sharko's own
// catalogue from everybody else's.
//
// Two catalogues reach the same verifier and they are not the same kind of
// thing:
//
//   - The EMBEDDED catalogue is signed by Sharko's own release workflow,
//     from the same commit this binary was built from. It can therefore be
//     held to a much stricter rule than "a trusted identity signed
//     something": the certificate must claim the exact commit this build
//     was released from. That is what stops a genuine, fully valid
//     signature made from a different commit — an older release's
//     catalogue, or one signed from a later commit on `main` — being
//     accepted as this release's catalogue.
//
//   - A THIRD-PARTY catalogue named in SHARKO_CATALOG_URLS or in
//     configuration/marketplace-sources.yaml is signed by its own
//     publisher, from its own repository, at its own commit. That commit
//     has no relationship to Sharko's release commit and never will, so
//     applying the same rule there would refuse every third-party
//     signature there has ever been. Its behaviour must not change.
//
// The separation is structural rather than a rule somebody has to
// remember. buildCatalogTrustPolicies returns TWO values and the caller
// must choose; the embedded one is the only one that goes through
// signing.EmbeddedCatalogTrustPolicy, which is the only place in the
// codebase that can set RequireReleaseCommit. A future edit that wired the
// embedded policy into the fetcher would have to pass the wrong one of two
// clearly named return values, and TestCatalogTrustPolicies_ThirdPartyIsUnbound
// plus TestServeWiring_KeepsTheTwoPoliciesApart fail if it does.

// catalogTrustPolicies carries the two policies apart so a call site
// cannot pick up the wrong one by taking whatever is in scope.
type catalogTrustPolicies struct {
	// Embedded gates Sharko's own //go:embed catalogue. Carries the
	// release-commit binding.
	Embedded sources.TrustPolicy
	// ThirdParty gates every catalogue fetched from a configured URL. It
	// is the shipped policy with nothing added — identical to what
	// signing.LoadTrustPolicyFromEnv returns.
	ThirdParty sources.TrustPolicy
	// BuildCommit is the release commit this binary was stamped with, as
	// it was read, before any validation. Reported so the startup log and
	// the tests can say what the build actually carried rather than
	// inferring it from the policy.
	BuildCommit string
	// ReleaseStamped says whether BuildCommit is a full commit hash, i.e.
	// whether this binary can bind its own catalogue to a release at all.
	// False for a development build and for a container image built
	// without the COMMIT build argument.
	ReleaseStamped bool
}

// buildCatalogTrustPolicies is the shipped wiring. cmd/sharko/serve.go
// calls exactly this function and uses exactly these two values, so a test
// that calls it is exercising the runtime default policy rather than a
// hand-built one.
//
// The release commit comes from the package-level `commit` variable in
// root.go, which the release pipeline sets with `-X main.commit`. It is a
// parameter rather than a direct read so a test can drive the
// wrong-commit and missing-stamp cases without rebuilding the binary;
// serve.go passes the real variable and nothing else does.
func buildCatalogTrustPolicies(buildCommit string) (catalogTrustPolicies, error) {
	base, err := signing.LoadTrustPolicyFromEnv()
	if err != nil {
		return catalogTrustPolicies{}, err
	}
	return catalogTrustPolicies{
		Embedded:       signing.EmbeddedCatalogTrustPolicy(base, buildCommit),
		ThirdParty:     base,
		BuildCommit:    buildCommit,
		ReleaseStamped: signing.IsFullCommitSHA(buildCommit),
	}, nil
}
