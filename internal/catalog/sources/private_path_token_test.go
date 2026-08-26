package sources

// private_path_token_test.go — the positive proof that the documented
// private-catalog feature actually works.
//
// internal/config/catalog_sources.go documents, in as many words, that a
// private catalog is addressed by writing an auth token into the address's
// own path — https://host/private/<token>/catalog.yaml — and that
// SHARKO_CATALOG_URLS exists precisely so such an address need not be
// committed to Git. Everything else in this story is about keeping that
// token inside the process; this test is the other half of the bargain:
// the token-in-the-path really does authenticate, driven through the REAL
// fetcher against a server that genuinely refuses requests without it.
//
// Both halves are asserted. A test that only proved the happy path could
// pass against a server that ignores the token entirely — which would
// mean the "private" catalog was public all along.
//
// If this test cannot pass, that is a release blocker, not a thing to
// work around: the documented promise would be false.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
)

func TestPrivatePathTokenCatalog_RefusedWithoutToken_ServedWithIt(t *testing.T) {
	// Synthetic token only. Nothing here is real or derived from anything
	// real.
	const token = "bf14r2-private-path-synthetic-not-a-real-token"
	const tokenPath = "/private/" + token + "/catalog.yaml"

	var tokenlessRefusals atomic.Int32
	var tokenServes atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == tokenPath {
			tokenServes.Add(1)
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write([]byte(validCatalogYAML))
			return
		}
		// The same file without the token segment — and anything else —
		// is refused. This is what makes the catalog "private".
		tokenlessRefusals.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	tokenURL := srv.URL + tokenPath
	tokenlessURL := srv.URL + "/private/catalog.yaml"

	// Through the REAL door: the env vars, parsed by the real loader. The
	// test server binds loopback, so the SSRF guard must be lifted the
	// way an operator would lift it — and the test asserts the lift is
	// really in place before any fetch runs, because a fetch that never
	// happened proves nothing.
	t.Setenv(config.EnvCatalogURLs, tokenURL+","+tokenlessURL)
	t.Setenv(config.EnvCatalogAllowPrivate, "true")
	if got := os.Getenv(config.EnvCatalogAllowPrivate); got != "true" {
		t.Fatalf("%s = %q, want \"true\" — without it the loopback test server is unreachable and nothing below runs", config.EnvCatalogAllowPrivate, got)
	}

	cfg, err := config.LoadCatalogSourcesFromEnv()
	if err != nil {
		t.Fatalf("LoadCatalogSourcesFromEnv refused the documented private-catalog shape: %v — if this cannot parse, the promise in internal/config/catalog_sources.go is false", err)
	}
	if !cfg.AllowPrivate {
		t.Fatalf("cfg.AllowPrivate = false after setting %s=true — the real door did not open", config.EnvCatalogAllowPrivate)
	}
	if len(cfg.Sources) != 2 {
		t.Fatalf("len(cfg.Sources) = %d, want 2 — the two configured addresses have to survive parsing for both halves of this proof to run", len(cfg.Sources))
	}
	// The token must survive canonicalization byte-for-byte: it IS the
	// credential, and a canonicalizer that rewrote it would break the
	// documented feature.
	var canonToken, canonTokenless string
	for _, src := range cfg.Sources {
		switch src.URL {
		case tokenURL:
			canonToken = src.URL
		case tokenlessURL:
			canonTokenless = src.URL
		}
	}
	if canonToken == "" || canonTokenless == "" {
		t.Fatalf("the canonicalized sources %v do not carry the two configured addresses — the path token did not survive parsing", cfg.Sources)
	}

	// The REAL fetcher, not a hand-rolled HTTP call. Only the HTTP client
	// is swapped, so the TLS test certificate is trusted.
	f := NewFetcher(cfg, nil, nil)
	f.SetHTTPClientForTest(srv.Client())
	f.ForceRefresh(t.Context())

	snaps := f.Snapshots()

	// Half one: without the token the server refused, and the fetcher
	// recorded the refusal.
	if tokenlessRefusals.Load() == 0 {
		t.Fatalf("NOT EXERCISED | the server never refused a tokenless request, so nothing here proved the catalog is private")
	}
	tokenless, ok := snaps[canonTokenless]
	if !ok {
		t.Fatalf("no snapshot for the tokenless address — its fetch never ran, so its refusal was never observed")
	}
	if tokenless.Status != StatusFailed {
		t.Errorf("tokenless fetch status = %q, want %q — a request without the documented path token must be refused", tokenless.Status, StatusFailed)
	}
	if len(tokenless.Entries) != 0 {
		t.Errorf("the tokenless fetch yielded %d entries — the server handed out the private catalog without the token", len(tokenless.Entries))
	}

	// Half two: with the token in the path, the fetch succeeds and yields
	// entries.
	if tokenServes.Load() == 0 {
		t.Fatalf("NOT EXERCISED | the server never served the token-bearing path, so the happy half of this proof never ran")
	}
	withToken, ok := snaps[canonToken]
	if !ok {
		t.Fatalf("no snapshot for the token-bearing address — its fetch never ran")
	}
	if withToken.Status != StatusOK {
		t.Errorf("token-bearing fetch status = %q, want %q — private-catalog authentication is not working, which is a release blocker (err: %v)", withToken.Status, StatusOK, withToken.LastErr)
	}
	if len(withToken.Entries) != 2 {
		t.Errorf("token-bearing fetch yielded %d entries, want 2 — a success that served no entries is not the documented feature", len(withToken.Entries))
	}
}
