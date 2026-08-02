package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestProbeManifestAt_Found — the registry answers the manifest GET
// directly with 200 (no auth challenge, e.g. a self-hosted registry with
// anonymous pull enabled). The probe must succeed with no error.
func TestProbeManifestAt_Found(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/org/sharko-engine/manifests/0.4.0" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"schemaVersion":2}`))
	}))
	defer srv.Close()

	err := probeManifestAt(context.Background(), srv.URL, "org/sharko-engine", "0.4.0", "sharko-engine", "example.test/org")
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

// TestProbeManifestAt_RequiresAnonymousToken — the registry challenges the
// first manifest GET with a 401 + WWW-Authenticate Bearer header (the GHCR
// shape), serves a token at the named realm with no credentials required,
// and only returns 200 once the retried request carries that token. This
// pins the anonymous bearer-token round trip GHCR (and most public OCI
// registries) require even for public content.
func TestProbeManifestAt_RequiresAnonymousToken(t *testing.T) {
	const wantToken = "anon-token-xyz"
	var manifestHitsWithToken, manifestHitsWithoutToken int

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("service"); got != "test-registry" {
			t.Errorf("token request missing expected service param, got %q", got)
		}
		if got := r.URL.Query().Get("scope"); got != "repository:org/sharko-engine:pull" {
			t.Errorf("token request missing expected scope param, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":%q}`, wantToken)
	})

	var srv *httptest.Server
	mux.HandleFunc("/v2/org/sharko-engine/manifests/0.4.0", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+wantToken {
			manifestHitsWithoutToken++
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(
				`Bearer realm="%s/token",service="test-registry",scope="repository:org/sharko-engine:pull"`, srv.URL))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		manifestHitsWithToken++
		w.WriteHeader(http.StatusOK)
	})

	srv = httptest.NewServer(mux)
	defer srv.Close()

	err := probeManifestAt(context.Background(), srv.URL, "org/sharko-engine", "0.4.0", "sharko-engine", "example.test/org")
	if err != nil {
		t.Fatalf("expected success after token round trip, got: %v", err)
	}
	if manifestHitsWithoutToken != 1 {
		t.Errorf("expected exactly 1 unauthenticated manifest request, got %d", manifestHitsWithoutToken)
	}
	if manifestHitsWithToken != 1 {
		t.Errorf("expected exactly 1 authenticated manifest request, got %d", manifestHitsWithToken)
	}
}

// TestProbeManifestAt_NotFound — a confirmed 404 must produce the
// "not published" error naming chart, version, and registry, and must be
// distinguishable from a registry-unreachable failure (no "unreachable" or
// "timed out" wording).
func TestProbeManifestAt_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	err := probeManifestAt(context.Background(), srv.URL, "org/sharko-engine", "0.2.0", "sharko-engine", "ghcr.io/example-org")
	if err == nil {
		t.Fatal("expected an error for a 404 manifest response")
	}
	for _, want := range []string{"0.2.0", "sharko-engine", "ghcr.io/example-org", "not published", "publish it or pin a published version"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing expected substring %q", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "unreachable") || strings.Contains(err.Error(), "timed out") {
		t.Errorf("404 error should not claim the registry was unreachable: %v", err)
	}
}

// TestProbeManifestAt_RegistryError — a non-2xx, non-404 status (e.g. a
// registry-side 500) must produce the honest "could not confirm /
// unreachable" error rather than silently succeeding or claiming
// not-published.
func TestProbeManifestAt_RegistryError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := probeManifestAt(context.Background(), srv.URL, "org/sharko-engine", "0.4.0", "sharko-engine", "ghcr.io/example-org")
	if err == nil {
		t.Fatal("expected an error for a 500 manifest response")
	}
	for _, want := range []string{"0.4.0", "sharko-engine", "ghcr.io/example-org", "unreachable or timed out"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing expected substring %q", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "not published") {
		t.Errorf("500 error should not claim the version is confirmed not-published: %v", err)
	}
}

// TestProbeManifestAt_Timeout — an unresponsive registry must produce the
// honest "unreachable or timed out" refusal, not a false "not published."
func TestProbeManifestAt_Timeout(t *testing.T) {
	unreachable := "http://127.0.0.1:1" // nothing listens here — connection refused, fast and deterministic

	err := probeManifestAt(context.Background(), unreachable, "org/sharko-engine", "0.4.0", "sharko-engine", "ghcr.io/example-org")
	if err == nil {
		t.Fatal("expected an error for an unreachable registry")
	}
	if !strings.Contains(err.Error(), "unreachable or timed out") {
		t.Errorf("expected honest unreachable wording, got: %v", err)
	}
	if strings.Contains(err.Error(), "not published") {
		t.Errorf("connection failure should not claim the version is confirmed not-published: %v", err)
	}
}

// TestProbeManifestAt_ContextDeadline confirms the probe's own timeout
// (chartProbeTimeout) bounds the request even when the caller's context
// has no deadline — a slow/hanging registry can't stall init forever.
func TestProbeManifestAt_ContextDeadline(t *testing.T) {
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-blocked // never respond until the test unblocks the handler
	}))
	// One defer, explicit order inside it: close(blocked) MUST run before
	// srv.Close(). httptest.Server.Close() blocks until every in-flight
	// handler goroutine returns; the handler above is parked on <-blocked,
	// so closing the server first would deadlock forever waiting for a
	// handler that can never return. Two separate `defer` statements would
	// run LIFO (srv.Close() first, since it would be registered last) and
	// hit exactly that deadlock — this single defer makes the order
	// unambiguous.
	defer func() {
		close(blocked)
		srv.Close()
	}()

	start := time.Now()
	err := probeManifestAt(context.Background(), srv.URL, "org/sharko-engine", "0.4.0", "sharko-engine", "ghcr.io/example-org")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed > chartProbeTimeout+2*time.Second {
		t.Errorf("probe took %v, expected it to be bounded by chartProbeTimeout=%v", elapsed, chartProbeTimeout)
	}
}

func TestSplitOCIRegistryHost(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantHost   string
		wantPrefix string
	}{
		{"host+prefix", "ghcr.io/moranweissman/sharko", "ghcr.io", "moranweissman/sharko"},
		{"host only", "ghcr.io", "ghcr.io", ""},
		{"oci scheme stripped", "oci://ghcr.io/moranweissman/sharko", "ghcr.io", "moranweissman/sharko"},
		{"trailing slash trimmed", "ghcr.io/moranweissman/sharko/", "ghcr.io", "moranweissman/sharko"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, prefix := splitOCIRegistryHost(tt.in)
			if host != tt.wantHost || prefix != tt.wantPrefix {
				t.Errorf("splitOCIRegistryHost(%q) = (%q, %q), want (%q, %q)",
					tt.in, host, prefix, tt.wantHost, tt.wantPrefix)
			}
		})
	}
}

func TestParseBearerChallenge(t *testing.T) {
	header := `Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:org/sharko-engine:pull"`
	realm, params, ok := parseBearerChallenge(header)
	if !ok {
		t.Fatal("expected ok=true for a well-formed challenge")
	}
	if realm != "https://ghcr.io/token" {
		t.Errorf("realm = %q, want %q", realm, "https://ghcr.io/token")
	}
	if params["service"] != "ghcr.io" {
		t.Errorf("service = %q, want %q", params["service"], "ghcr.io")
	}
	if params["scope"] != "repository:org/sharko-engine:pull" {
		t.Errorf("scope = %q, want %q", params["scope"], "repository:org/sharko-engine:pull")
	}

	if _, _, ok := parseBearerChallenge("Basic realm=\"x\""); ok {
		t.Error("expected ok=false for a non-Bearer challenge")
	}
	if _, _, ok := parseBearerChallenge(""); ok {
		t.Error("expected ok=false for an empty challenge")
	}
}

// TestProbeEnginePinChart_UsesEffectiveRegistry confirms
// probeEnginePinChart passes the SAME registry/chart/version the pin
// itself would be written with — the whole point of Story A1 is that the
// probe and the pin can never disagree.
func TestProbeEnginePinChart_UsesEffectiveRegistry(t *testing.T) {
	orch := New(nil, nil, nil, nil, GitOpsConfig{}, RepoPathsConfig{}, nil)

	var gotRegistry, gotChart, gotVersion string
	orch.SetChartProbe(func(_ context.Context, registryURL, chart, version string) error {
		gotRegistry, gotChart, gotVersion = registryURL, chart, version
		return nil
	})

	if err := orch.probeEnginePinChart(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotRegistry != DefaultEngineChartRepoURL {
		t.Errorf("registry = %q, want default %q", gotRegistry, DefaultEngineChartRepoURL)
	}
	if gotChart == "" || gotVersion == "" {
		t.Errorf("expected non-empty chart/version, got chart=%q version=%q", gotChart, gotVersion)
	}
}

// TestProbeEnginePinChart_CustomRegistry confirms a configured
// GitOpsConfig.EngineChartRepoURL overrides the default and is what gets
// probed — not silently ignored.
func TestProbeEnginePinChart_CustomRegistry(t *testing.T) {
	orch := New(nil, nil, nil, nil,
		GitOpsConfig{EngineChartRepoURL: "registry.example.test/org"},
		RepoPathsConfig{}, nil)

	var gotRegistry string
	orch.SetChartProbe(func(_ context.Context, registryURL, _, _ string) error {
		gotRegistry = registryURL
		return nil
	})

	if err := orch.probeEnginePinChart(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotRegistry != "registry.example.test/org" {
		t.Errorf("registry = %q, want %q", gotRegistry, "registry.example.test/org")
	}
}

// TestSetChartProbe_NilRestoresDefault confirms passing nil to
// SetChartProbe restores the real network probe rather than leaving the
// field nil (which would panic probeEnginePinChart's fallback path is
// defensive, but SetChartProbe(nil) must never leave a caller silently
// skipping the check).
func TestSetChartProbe_NilRestoresDefault(t *testing.T) {
	orch := New(nil, nil, nil, nil, GitOpsConfig{}, RepoPathsConfig{}, nil)
	orch.SetChartProbe(func(_ context.Context, _, _, _ string) error { return errors.New("stub") })
	orch.SetChartProbe(nil)
	if orch.chartProbeFn == nil {
		t.Fatal("SetChartProbe(nil) left chartProbeFn nil")
	}
}
