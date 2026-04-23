// Integration test for V123-1.9 — drives the full third-party catalog
// loop end-to-end inside the `sources` package: HTTPS loopback server →
// Fetcher.ForceRefresh → SourceSnapshot → Merge(embedded, snapshots) →
// MergedCatalog. Stays in-package to avoid the internal/api ↔ sources
// import cycle (handler-level coverage is provided by V123-1.4/1.5/1.6).
//
// Scope:
//   - Loopback TLS server is trusted via srv.Client() on the fetcher
//     (Gotcha #2 from the story brief).
//   - AllowPrivate=true on the config (Gotcha #1) — the runtime SSRF
//     guard would otherwise reject 127.0.0.1.
//   - ForceRefresh (not Start + sleep) keeps timing deterministic.
//   - A synthetic embedded slice that collides on "cert-manager"
//     exercises the embedded-wins rule end-to-end.
//   - Asserts every MergedEntry carries the correct Origin.
package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/catalog"
	"github.com/MoranWeissman/sharko/internal/config"
)

// TestIntegration_FullLoop_FetcherToMergedCatalog verifies the complete
// third-party catalog pipeline: HTTPS server → fetcher → snapshot →
// merger → final in-memory catalog with source attribution.
//
// The test asserts:
//
//  1. The fetcher trusts the test server's self-signed TLS cert via
//     srv.Client() and parses three entries into a StatusOK snapshot.
//  2. Merge overlays the snapshot under an embedded slice that
//     collides on "cert-manager". Embedded wins; the merged output
//     carries 4 entries (alpha, beta, cert-manager, embedded-only).
//  3. Exactly one Conflict{Name: "cert-manager", Reason:
//     ReasonEmbeddedWins, Winner: OriginEmbedded}.
//  4. Every MergedEntry has a non-empty Origin matching the expected
//     source (third-party entries → srv URL, embedded entries → the
//     OriginEmbedded sentinel).
func TestIntegration_FullLoop_FetcherToMergedCatalog(t *testing.T) {
	// Third-party YAML with three entries. All required fields present
	// so catalog.LoadBytes's validateEntry accepts them. cert-manager
	// is the collision target (embedded declares it too).
	const yaml = `addons:
  - name: alpha
    description: Alpha test addon.
    chart: alpha
    repo: https://example.com/charts
    default_namespace: alpha
    default_sync_wave: 10
    maintainers: ["alpha-team"]
    license: Apache-2.0
    category: observability
    curated_by: [cncf-sandbox]
  - name: beta
    description: Beta test addon.
    chart: beta
    repo: https://example.com/charts
    default_namespace: beta
    default_sync_wave: 10
    maintainers: ["beta-team"]
    license: MIT
    category: networking
    curated_by: [cncf-sandbox]
  - name: cert-manager
    description: Third-party cert-manager (will be shadowed by embedded).
    chart: cert-manager
    repo: https://example.com/charts
    default_namespace: cert-manager
    default_sync_wave: 10
    maintainers: ["third-party"]
    license: Apache-2.0
    category: security
    curated_by: [cncf-graduated]
`

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte(yaml))
	}))
	t.Cleanup(srv.Close)

	sourceURL := srv.URL + "/catalog.yaml"
	cfg := &config.CatalogSourcesConfig{
		Sources:         []config.CatalogSource{{URL: sourceURL}},
		RefreshInterval: 1 * time.Hour, // long — we force one refresh
		AllowPrivate:    true,          // required for 127.0.0.1 loopback (Gotcha #1)
	}
	f := NewFetcher(cfg, nil, nil)
	// srv.Client() already trusts the self-signed test CA.
	f.SetHTTPClientForTest(srv.Client())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f.ForceRefresh(ctx)

	// --- Phase 1: fetcher state ---

	snaps := f.Snapshots()
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d (keys=%v)", len(snaps), keysOf(snaps))
	}
	snap := snaps[sourceURL]
	if snap == nil {
		t.Fatalf("expected snapshot keyed by %q, got keys=%v", sourceURL, keysOf(snaps))
	}
	if snap.Status != StatusOK {
		t.Fatalf("snapshot status = %q, want %q (LastErr=%v)", snap.Status, StatusOK, snap.LastErr)
	}
	if got := len(snap.Entries); got != 3 {
		t.Fatalf("snapshot entries = %d, want 3", got)
	}

	// --- Phase 2: merge with a colliding embedded slice ---

	// Synthetic embedded entries. mkEntry from merger_test.go guarantees
	// all schema-required fields — we just override Name here.
	embedded := []catalog.CatalogEntry{
		mkEntry("cert-manager"),  // collides with third-party
		mkEntry("embedded-only"), // no collision
	}
	merged := Merge(embedded, []*SourceSnapshot{snap})

	// 4 entries: alpha, beta, cert-manager (embedded wins), embedded-only.
	if got := len(merged.Entries); got != 4 {
		t.Errorf("merged entries = %d, want 4 (names=%v)", got, entryNamesFor(merged.Entries))
	}

	// Exactly one conflict — cert-manager.
	if len(merged.Conflicts) != 1 {
		t.Fatalf("merged conflicts = %d, want 1 (conflicts=%+v)", len(merged.Conflicts), merged.Conflicts)
	}
	conflict := merged.Conflicts[0]
	if conflict.Name != "cert-manager" {
		t.Errorf("conflict name = %q, want cert-manager", conflict.Name)
	}
	if conflict.Winner != OriginEmbedded {
		t.Errorf("conflict winner = %q, want %q", conflict.Winner, OriginEmbedded)
	}
	if conflict.Reason != ReasonEmbeddedWins {
		t.Errorf("conflict reason = %q, want %q", conflict.Reason, ReasonEmbeddedWins)
	}
	if len(conflict.Losers) != 1 || conflict.Losers[0] != sourceURL {
		t.Errorf("conflict losers = %v, want [%s]", conflict.Losers, sourceURL)
	}

	// --- Phase 3: every entry carries the correct Origin ---

	wantOrigin := map[string]string{
		"alpha":         sourceURL,
		"beta":          sourceURL,
		"cert-manager":  OriginEmbedded,
		"embedded-only": OriginEmbedded,
	}
	seen := make(map[string]bool, len(wantOrigin))
	for _, e := range merged.Entries {
		want, ok := wantOrigin[e.Name]
		if !ok {
			t.Errorf("unexpected entry in merged output: %q", e.Name)
			continue
		}
		if e.Origin != want {
			t.Errorf("entry %q: Origin = %q, want %q", e.Name, e.Origin, want)
		}
		if e.Overridden {
			// The surviving cert-manager is the embedded one (winner) —
			// never Overridden. Losers are dropped from Entries
			// entirely, so no surviving entry should be flagged.
			t.Errorf("entry %q: Overridden=true, want false", e.Name)
		}
		seen[e.Name] = true
	}
	for name := range wantOrigin {
		if !seen[name] {
			t.Errorf("expected entry %q missing from merged output", name)
		}
	}
}

// entryNamesFor is a local helper so the integration test can log the
// names of merged entries without depending on the unexported
// entryNames helper in merger_test.go (which is in the same package and
// would be shadowed if tests ever run in different orders). Naming it
// distinctly avoids an accidental duplicate-symbol collision across test
// files.
func entryNamesFor(entries []MergedEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}
