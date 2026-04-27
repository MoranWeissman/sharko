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
