// tufroot.go owns the production trust-root loader (V123-2.4 + B1
// blocker fix). The loader fetches the Sigstore public-good
// `trusted_root.json` via the standard TUF client and parses it into a
// root.TrustedMaterial that the verifier accepts via WithTrustedMaterial.
//
// Why this lives here:
//   - cmd/sharko/serve.go must construct the verifier with a real trust
//     root or every signed catalog entry will surface Verified=false
//     against the fail-closed staticTrust{} default. Pre-V123-2.4 the
//     verifier was constructed without WithTrustedMaterial — that wiring
//     is the B1 BLOCKER from .bmad/output/v1.23-PRE-TAG-TODO.md.
//   - serve.go must NOT learn the TUF API. Keeping the helper one
//     function call wide (`signing.LoadProductionTrustedRoot(ctx)`)
//     means the call site in serve.go stays a one-liner and the TUF
//     coupling is contained inside the signing package.
//
// Failure mode:
//   - TUF fetches reach out to https://tuf-repo-cdn.sigstore.dev. An
//     air-gapped Sharko deployment cannot reach that host. The caller
//     in serve.go is responsible for treating any error as warn-not-fatal
//     so the air-gapped boot path stays alive — the verifier just falls
//     back to its fail-closed staticTrust{} default and operators see
//     Verified=false on every signed entry, which is the correct
//     conservative outcome.
//   - This helper itself returns the error verbatim; it is the caller's
//     job to decide fatal vs. warn. Wrapping that decision into the
//     loader would force the same policy on every future caller.
package signing

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
)

// tufCacheEnvVar is the operator-facing override for the on-disk TUF
// cache path. Empty (or unset) keeps the container-safe default.
const tufCacheEnvVar = "SHARKO_SIGSTORE_TUF_CACHE"

// defaultTUFCachePath is the container-safe fallback for the on-disk
// TUF cache. Sigstore-go's stock default (`$HOME/.sigstore/tuf`) is the
// classic shape that fails inside Sharko's Docker image — the runtime
// user has no `$HOME`, so the path resolves to `/.sigstore` and the
// kernel rejects the mkdir. `/tmp` is writable on every Linux container
// distro (including distroless and read-only-root setups where `/tmp`
// is the canonical writable mount). Operators who want persistence can
// point SHARKO_SIGSTORE_TUF_CACHE at a mounted volume.
const defaultTUFCachePath = "/tmp/sigstore-tuf"

// resolveTUFCachePath picks the on-disk TUF cache directory.
//
// Order:
//  1. SHARKO_SIGSTORE_TUF_CACHE if set to a non-whitespace value.
//  2. defaultTUFCachePath (`/tmp/sigstore-tuf`) otherwise.
//
// Whitespace-only env values are treated as "unset" so an operator who
// sets `SHARKO_SIGSTORE_TUF_CACHE=" "` doesn't end up trying to write
// to a literal whitespace directory.
func resolveTUFCachePath() string {
	if v := strings.TrimSpace(os.Getenv(tufCacheEnvVar)); v != "" {
		return v
	}
	return defaultTUFCachePath
}

// LoadProductionTrustedRoot fetches the Sigstore public-good
// `trusted_root.json` via TUF and parses it into a root.TrustedMaterial
// suitable for signing.WithTrustedMaterial.
//
// Cache path defaults to `/tmp/sigstore-tuf` — a writable location in
// every Linux container including distroless and read-only-root setups.
// Operators who want persistence across container restarts can point
// `SHARKO_SIGSTORE_TUF_CACHE` at a mounted volume. The repository base
// URL is the public good mirror. The first call from a fresh cache
// reaches out to https://tuf-repo-cdn.sigstore.dev to pull the root
// metadata + the trusted_root.json target; subsequent calls re-use the
// on-disk cache subject to the TUF metadata expiry.
//
// The ctx parameter is accepted for API symmetry with the rest of the
// signing package's loaders. The underlying tuf.New + GetTarget calls
// in sigstore-go v1.1.4 do not honor ctx for cancellation, so a hung
// fetch will be bounded by the underlying TUF client's HTTP timeouts
// rather than ctx. This is acceptable for startup-time use; callers
// that want hard cancellation can wrap this in a goroutine with
// context.AfterFunc / select.
func LoadProductionTrustedRoot(ctx context.Context) (root.TrustedMaterial, error) {
	_ = ctx // reserved for future use; see godoc above

	opts := tuf.DefaultOptions()
	opts.CachePath = resolveTUFCachePath()
	client, err := tuf.New(opts)
	if err != nil {
		return nil, fmt.Errorf("tuf.New: %w", err)
	}

	trustedRootJSON, err := client.GetTarget("trusted_root.json")
	if err != nil {
		return nil, fmt.Errorf("tuf GetTarget(trusted_root.json): %w", err)
	}

	tr, err := root.NewTrustedRootFromJSON(trustedRootJSON)
	if err != nil {
		return nil, fmt.Errorf("parse trusted_root.json: %w", err)
	}
	return tr, nil
}
