package demo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/api"
)

// secrets_status_demo_test.go pins the W3-6b review blocker: GET
// /api/v1/secrets/status type-asserted the reconciler's stats to
// secrets.ReconcileStats, but demo mode wires demoAddonValuesReconciler,
// whose GetStats() returned a private look-alike type — so every demo
// server answered 500 on this endpoint, and nothing caught it. The
// api.SecretReconciler boundary is now compile-time enforced (GetStats()
// secrets.ReconcileStats on the interface itself), so this test exercises
// the demo reconciler through the real handler rather than re-asserting a
// type by hand.
func TestBigEstate_SecretsStatus_DemoReconciler(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/secrets/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("GET secrets/status status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}

	rawBody := rw.Body.String()
	if strings.Contains(rawBody, "0001-01-01") {
		t.Fatalf("response body leaks Go's zero time: %s", rawBody)
	}

	var resp api.ReconcileStatusResponse
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode secrets/status response: %v; body = %s", err, rawBody)
	}

	// The demo addon-values reconciler always seeds a non-zero lastRun at
	// construction (newDemoAddonValuesReconciler sets it to "just under two
	// minutes ago") — so a healthy response must carry last_run, not omit
	// it. If a future demo estate ever seeds a genuinely zero lastRun, this
	// assertion is the one to flip to "must be empty" for that case — the
	// point pinned here is that LastRun flows through GetStats honestly
	// either way, never as the fabricated Go zero-time string.
	if resp.LastRun == "" {
		t.Fatalf("expected last_run to be present for the demo reconciler's seeded lastRun, got empty; body = %s", rawBody)
	}

	if resp.Checked <= 0 {
		t.Fatalf("expected Checked > 0 on the big generated estate, got %d; body = %s", resp.Checked, rawBody)
	}
}
