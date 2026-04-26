// Loader + verifier integration tests (V123-2.6).
//
// These exercise catalog.LoadBytesWithVerifier end-to-end with a
// VerifyEntryFunc closure: the loader sees real CatalogEntry YAML, walks
// every entry, decides which ones have a Signature.Bundle URL, and
// invokes the closure exactly when the contract says it should. The
// suite asserts the contract from the loader's perspective:
//
//   - signed entries that verify    → Verified=true, identity populated
//   - signed entries that mismatch  → Verified=false, identity empty
//   - signed entries with untrusted SAN → Verified=false, identity empty
//   - unsigned entries              → Verified=false AND verifyFn never invoked
//   - infra error from verifyFn     → Verified=false, LoadBytesWithVerifier
//                                     does NOT return an error
//
// We don't construct real Sigstore bundle JSON inside the loader test —
// V123-2.5 already proved the orchestration around real signing using a
// fakeSigner, and bundle JSON synthesis from a *ca.TestEntity requires
// hand-rolling protobundle.Bundle assembly which is out-of-scope for
// V123-2.6 (the brief explicitly allows "spy VerifyEntryFunc wrapping
// the real one" for the passthrough check). Here we wire a spy verifier
// that consults a per-entry routing table to return canned outcomes,
// PLUS a request-counter httptest.Server hosting bundle bytes so the
// "verifier never invoked" case is also observable at the HTTP layer.
package signing

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/MoranWeissman/sharko/internal/catalog"
)

// canonicalEntry is a minimal CatalogEntry that satisfies the loader's
// validateEntry rules. Kept tiny so test cases are readable.
func canonicalEntry(name string, sig *catalog.Signature) catalog.CatalogEntry {
	return catalog.CatalogEntry{
		Name:             name,
		Description:      "test fixture entry " + name,
		Chart:            name,
		Repo:             "https://charts.example.com/" + name,
		DefaultNamespace: name,
		Maintainers:      []string{"test"},
		License:          "Apache-2.0",
		Category:         "observability",
		CuratedBy:        []string{"cncf-graduated"},
		Signature:        sig,
	}
}

// signedCorpus is the marshaled YAML payload + the per-entry routing
// table the spy verifier consults + the bundle-host server stats.
type signedCorpus struct {
	payload  []byte
	hits     map[string]*atomic.Int64 // entry name → bundle-URL hit count
	server   *httptest.Server
	bundleOf map[string]string // entry name → bundle URL (when signed)
}

// outcome is what the spy verifier returns for a given entry name.
type outcome struct {
	verified bool
	issuer   string
	err      error
}

// newSignedCorpus builds a YAML payload from `entries`, hosts a per-name
// `.bundle` route on a local httptest.Server (returning a constant byte
// payload so HTTP wiring is exercised end-to-end), and stamps each
// signed entry's Signature.Bundle URL to the host. `sign[name]==true`
// means the entry should carry a Signature pointing at the test server;
// false (or absent) means leave Signature nil.
func newSignedCorpus(t *testing.T, entries []catalog.CatalogEntry, sign map[string]bool) *signedCorpus {
	t.Helper()

	corpus := &signedCorpus{
		hits:     make(map[string]*atomic.Int64, len(entries)),
		bundleOf: make(map[string]string, len(entries)),
	}
	for _, e := range entries {
		corpus.hits[e.Name] = new(atomic.Int64)
	}

	mux := http.NewServeMux()
	for _, e := range entries {
		name := e.Name
		mux.HandleFunc("/"+name+".bundle", func(w http.ResponseWriter, r *http.Request) {
			corpus.hits[name].Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"fake":"bundle for ` + name + `"}`))
		})
	}
	// httptest.NewTLSServer satisfies the loader's validateEntry rule
	// that signature.bundle must be an https:// URL. The spy verifier
	// uses the server's own client (which trusts the test cert) when
	// it pings the URL to increment the hits counter.
	corpus.server = httptest.NewTLSServer(mux)
	t.Cleanup(corpus.server.Close)

	stamped := make([]catalog.CatalogEntry, len(entries))
	for i, e := range entries {
		stamped[i] = e
		if sign[e.Name] {
			url := corpus.server.URL + "/" + e.Name + ".bundle"
			stamped[i].Signature = &catalog.Signature{Bundle: url}
			corpus.bundleOf[e.Name] = url
		}
	}

	type yamlRoot struct {
		Addons []catalog.CatalogEntry `yaml:"addons"`
	}
	out, err := yaml.Marshal(yamlRoot{Addons: stamped})
	if err != nil {
		t.Fatalf("marshal corpus: %v", err)
	}
	corpus.payload = out
	return corpus
}

// verifierWithLookup returns a VerifyEntryFunc whose outcome is keyed
// off the bundle URL. Each call also pings the bundle URL via the
// supplied http.Client so the per-entry hits counter increments — that
// way "verifier was invoked" is observable both at the closure layer
// (via callCount) AND at the HTTP layer (via corpus.hits[name]).
func verifierWithLookup(
	corpus *signedCorpus,
	outcomes map[string]outcome,
	callCount *atomic.Int64,
) catalog.VerifyEntryFunc {
	return func(ctx context.Context, _ []byte, bundleURL string) (bool, string, error) {
		callCount.Add(1)

		// Touch the URL so the hits counter on the corpus reflects
		// that the loader actually surfaced this URL to the verifier.
		// Use the corpus server's own client so connection reuse and
		// timeouts mirror what the production verifier does.
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, bundleURL, nil)
		resp, herr := corpus.server.Client().Do(req)
		if herr != nil {
			// Defensive — the test should never hit this path.
			return false, "", fmt.Errorf("spy fetch: %w", herr)
		}
		_ = resp.Body.Close()

		// Look up the canned outcome by entry name (parsed off the
		// URL — the route is /<name>.bundle).
		name := strings.TrimSuffix(strings.TrimPrefix(bundleURL, corpus.server.URL+"/"), ".bundle")
		if oc, ok := outcomes[name]; ok {
			return oc.verified, oc.issuer, oc.err
		}
		return false, "", fmt.Errorf("no outcome configured for %q", name)
	}
}

// TestLoadBytesWithVerifier_Integration is the end-to-end loader +
// verifier contract. Five sub-cases drive every documented branch of
// LoadBytesWithVerifier (catalog/loader.go lines ~280-447).
func TestLoadBytesWithVerifier_Integration(t *testing.T) {
	t.Run("signed_passes", func(t *testing.T) {
		entries := []catalog.CatalogEntry{canonicalEntry("alpha", nil)}
		corpus := newSignedCorpus(t, entries, map[string]bool{"alpha": true})
		var calls atomic.Int64
		fn := verifierWithLookup(corpus, map[string]outcome{
			"alpha": {verified: true, issuer: "ci@example.com"},
		}, &calls)

		cat, err := catalog.LoadBytesWithVerifier(context.Background(), corpus.payload, fn)
		if err != nil {
			t.Fatalf("LoadBytesWithVerifier: %v", err)
		}
		got, ok := cat.Get("alpha")
		if !ok {
			t.Fatal("entry alpha not loaded")
		}
		if !got.Verified {
			t.Errorf("alpha.Verified = false, want true")
		}
		if got.SignatureIdentity != "ci@example.com" {
			t.Errorf("alpha.SignatureIdentity = %q, want %q", got.SignatureIdentity, "ci@example.com")
		}
		if calls.Load() != 1 {
			t.Errorf("verifyFn calls = %d, want 1", calls.Load())
		}
		if hits := corpus.hits["alpha"].Load(); hits != 1 {
			t.Errorf("bundle URL hits for alpha = %d, want 1", hits)
		}
	})

	t.Run("signed_mismatch", func(t *testing.T) {
		entries := []catalog.CatalogEntry{canonicalEntry("beta", nil)}
		corpus := newSignedCorpus(t, entries, map[string]bool{"beta": true})
		var calls atomic.Int64
		fn := verifierWithLookup(corpus, map[string]outcome{
			// Mismatch outcome: (false, "", nil) — the SidecarVerifier
			// contract for "signature doesn't match payload."
			"beta": {verified: false, issuer: "", err: nil},
		}, &calls)

		cat, err := catalog.LoadBytesWithVerifier(context.Background(), corpus.payload, fn)
		if err != nil {
			t.Fatalf("LoadBytesWithVerifier: %v", err)
		}
		got, _ := cat.Get("beta")
		if got.Verified {
			t.Error("beta.Verified = true on sig mismatch, want false")
		}
		if got.SignatureIdentity != "" {
			t.Errorf("beta.SignatureIdentity = %q, want empty", got.SignatureIdentity)
		}
	})

	t.Run("signed_untrusted", func(t *testing.T) {
		entries := []catalog.CatalogEntry{canonicalEntry("gamma", nil)}
		corpus := newSignedCorpus(t, entries, map[string]bool{"gamma": true})
		var calls atomic.Int64
		fn := verifierWithLookup(corpus, map[string]outcome{
			// Untrusted-identity outcome — surfaces identically to
			// mismatch on the wire (the design defers the
			// verification_state enum to v1.24+; today both branches
			// land as Verified=false).
			"gamma": {verified: false, issuer: "", err: nil},
		}, &calls)

		cat, err := catalog.LoadBytesWithVerifier(context.Background(), corpus.payload, fn)
		if err != nil {
			t.Fatalf("LoadBytesWithVerifier: %v", err)
		}
		got, _ := cat.Get("gamma")
		if got.Verified {
			t.Error("gamma.Verified = true on untrusted identity, want false")
		}
		if got.SignatureIdentity != "" {
			t.Errorf("gamma.SignatureIdentity = %q, want empty", got.SignatureIdentity)
		}
	})

	t.Run("unsigned_passthrough", func(t *testing.T) {
		// Two entries: one signed (drives a verifier hit), one
		// unsigned (must NOT drive one). After the load, the signed
		// entry's URL has 1 hit and the unsigned entry's URL has 0.
		entries := []catalog.CatalogEntry{
			canonicalEntry("delta", nil),   // will be signed
			canonicalEntry("epsilon", nil), // will NOT be signed
		}
		corpus := newSignedCorpus(t, entries, map[string]bool{"delta": true})
		var calls atomic.Int64
		fn := verifierWithLookup(corpus, map[string]outcome{
			"delta": {verified: true, issuer: "ci@example.com"},
			// no entry for epsilon — verifier MUST NOT be called for it
		}, &calls)

		cat, err := catalog.LoadBytesWithVerifier(context.Background(), corpus.payload, fn)
		if err != nil {
			t.Fatalf("LoadBytesWithVerifier: %v", err)
		}
		// Signed entry verifies normally.
		delta, ok := cat.Get("delta")
		if !ok {
			t.Fatal("delta missing from loaded catalog")
		}
		if !delta.Verified {
			t.Errorf("delta.Verified = false, want true (signed + spy says ok)")
		}
		// Unsigned entry passes through with Verified=false.
		eps, ok := cat.Get("epsilon")
		if !ok {
			t.Fatal("epsilon missing from loaded catalog")
		}
		if eps.Verified {
			t.Error("epsilon.Verified = true on unsigned entry, want false")
		}
		if eps.SignatureIdentity != "" {
			t.Errorf("epsilon.SignatureIdentity = %q, want empty", eps.SignatureIdentity)
		}
		// Verifier callback must have run exactly once (delta), not twice.
		if calls.Load() != 1 {
			t.Errorf("verifyFn calls = %d, want 1 (verifier MUST NOT be invoked for unsigned entry)", calls.Load())
		}
		// Bundle URL for the signed entry was hit; the unsigned entry
		// has no Signature and so no URL to hit (sanity check on the
		// counter map — the unsigned entry's name is still keyed in
		// hits because newSignedCorpus pre-populates from the input
		// list, but its registered HTTP route was never reached).
		if hits := corpus.hits["delta"].Load(); hits != 1 {
			t.Errorf("hits[delta] = %d, want 1", hits)
		}
		if hits := corpus.hits["epsilon"].Load(); hits != 0 {
			t.Errorf("hits[epsilon] = %d, want 0 (unsigned entry must not be fetched)", hits)
		}
	})

	t.Run("infra_error_tolerated", func(t *testing.T) {
		// The verifier returns a non-nil error to signal an
		// infrastructure failure (network fetch, malformed bundle).
		// LoadBytesWithVerifier must NOT propagate this — the entry
		// loads with Verified=false and the load returns nil error.
		entries := []catalog.CatalogEntry{canonicalEntry("zeta", nil)}
		corpus := newSignedCorpus(t, entries, map[string]bool{"zeta": true})
		var calls atomic.Int64
		fn := verifierWithLookup(corpus, map[string]outcome{
			"zeta": {verified: false, issuer: "", err: fmt.Errorf("simulated bundle fetch 500")},
		}, &calls)

		cat, err := catalog.LoadBytesWithVerifier(context.Background(), corpus.payload, fn)
		if err != nil {
			t.Fatalf("LoadBytesWithVerifier returned %v on infra error; want nil (transient outage must not blackhole the catalog)", err)
		}
		got, ok := cat.Get("zeta")
		if !ok {
			t.Fatal("zeta missing from loaded catalog despite infra error tolerance")
		}
		if got.Verified {
			t.Error("zeta.Verified = true after infra error, want false")
		}
		if got.SignatureIdentity != "" {
			t.Errorf("zeta.SignatureIdentity = %q, want empty after infra error", got.SignatureIdentity)
		}
	})
}
