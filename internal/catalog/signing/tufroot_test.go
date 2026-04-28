package signing

import (
	"context"
	"os"
	"testing"
)

// TestLoadProductionTrustedRoot_Smoke exercises the canonical
// production trust-root loader against the real public-good Sigstore
// TUF mirror. It is the only direct coverage that the B1 wiring (V123-2.4)
// produces a non-nil root.TrustedMaterial without erroring — which is
// exactly what cmd/sharko/serve.go relies on at boot.
//
// The test reaches out to https://tuf-repo-cdn.sigstore.dev. CI from
// GitHub-hosted runners can talk to it; air-gapped CI cannot. The
// SHARKO_SKIP_TUF_NETWORK env var is the documented escape hatch — set
// it to "1" (or any non-empty value) to skip. It is NOT skipped by
// default; the whole point is to assert the wiring against a live
// mirror at least once per CI run.
func TestLoadProductionTrustedRoot_Smoke(t *testing.T) {
	if os.Getenv("SHARKO_SKIP_TUF_NETWORK") != "" {
		t.Skip("SHARKO_SKIP_TUF_NETWORK is set; skipping network-dependent TUF smoke test")
	}

	tm, err := LoadProductionTrustedRoot(context.Background())
	if err != nil {
		t.Fatalf("LoadProductionTrustedRoot: %v", err)
	}
	if tm == nil {
		t.Fatal("LoadProductionTrustedRoot returned nil TrustedMaterial with no error")
	}
}

// TestResolveTUFCachePath_Default — with the env var unset, the helper
// must return the container-safe default. Pin the contract: every Linux
// container distro must be able to write to this path on first boot.
func TestResolveTUFCachePath_Default(t *testing.T) {
	t.Setenv(tufCacheEnvVar, "")

	got := resolveTUFCachePath()
	if got != defaultTUFCachePath {
		t.Fatalf("resolveTUFCachePath() = %q, want %q", got, defaultTUFCachePath)
	}
}

// TestResolveTUFCachePath_EnvOverride — operators who want persistence
// across container restarts set SHARKO_SIGSTORE_TUF_CACHE to a mounted
// volume path. The helper must honor that value verbatim.
func TestResolveTUFCachePath_EnvOverride(t *testing.T) {
	const want = "/var/lib/sharko/sigstore-tuf"
	t.Setenv(tufCacheEnvVar, want)

	got := resolveTUFCachePath()
	if got != want {
		t.Fatalf("resolveTUFCachePath() = %q, want %q", got, want)
	}
}

// TestResolveTUFCachePath_EnvWhitespaceFallsBackToDefault — a
// whitespace-only env value is operator error (likely a stray quote in
// a Helm values file). Treat it as "unset" rather than trying to mkdir
// a literal whitespace directory.
func TestResolveTUFCachePath_EnvWhitespaceFallsBackToDefault(t *testing.T) {
	t.Setenv(tufCacheEnvVar, "   \t  ")

	got := resolveTUFCachePath()
	if got != defaultTUFCachePath {
		t.Fatalf("resolveTUFCachePath() = %q, want default %q", got, defaultTUFCachePath)
	}
}
