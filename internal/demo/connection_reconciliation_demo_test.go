package demo

// connection_reconciliation_demo_test.go — the new reconciliation endpoint
// must answer correctly on a demo server. Project lesson: the previous
// round's only release blocker was a demo-mode 500 nobody tested — every new
// endpoint gets a router-level demo test from day one.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/api"
)

func TestBigEstate_ConnectionReconciliation_Answers(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)

	// Pick real managed clusters the demo estate reports on its own fleet
	// page — those are exactly the ones the endpoint must answer for.
	body := getManagedSecrets(t, router, token)
	if len(body.ClusterConnectionSecrets) == 0 {
		t.Fatal("the big demo estate reported no connection rows to test against")
	}

	validStates := map[string]bool{"synced": true, "out_of_sync": true, "blocked": true, "unknown": true}
	validScopes := map[string]bool{"full": true, "partial": true, "labels_only": true, "none": true}

	tested := 0
	for _, row := range body.ClusterConnectionSecrets {
		if tested == 5 {
			break // a spread is enough; the estate is large
		}
		tested++

		req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+row.Cluster+"/connection-reconciliation", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rw := httptest.NewRecorder()
		router.ServeHTTP(rw, req)

		if rw.Code != http.StatusOK {
			t.Fatalf("GET connection-reconciliation for %q = %d, want 200; body = %s", row.Cluster, rw.Code, rw.Body.String())
		}
		raw := rw.Body.String()
		if strings.Contains(raw, "0001-01-01") {
			t.Fatalf("%q: response leaks Go's zero time: %s", row.Cluster, raw)
		}

		var resp struct {
			Cluster        string `json:"cluster"`
			ManagementMode string `json:"management_mode"`
			ManagedScope   string `json:"managed_scope"`
			ModeStatement  string `json:"mode_statement"`
			Sync           struct {
				State             string `json:"state"`
				VerificationScope string `json:"verification_scope"`
			} `json:"sync"`
			Health struct {
				State string `json:"state"`
			} `json:"health"`
			ValuesNeverReturned bool `json:"values_never_returned"`
		}
		if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%q: decode: %v; body = %s", row.Cluster, err, raw)
		}
		if resp.Cluster != row.Cluster {
			t.Errorf("cluster = %q, want %q", resp.Cluster, row.Cluster)
		}
		if !validStates[resp.Sync.State] {
			t.Errorf("%q: sync.state = %q, not one of the four durable words", row.Cluster, resp.Sync.State)
		}
		if !validScopes[resp.Sync.VerificationScope] {
			t.Errorf("%q: verification_scope = %q, not a valid scope", row.Cluster, resp.Sync.VerificationScope)
		}
		if resp.Sync.State == "synced" && resp.Sync.VerificationScope != "full" {
			t.Errorf("%q: INVARIANT VIOLATED in demo mode: synced with verification_scope=%q", row.Cluster, resp.Sync.VerificationScope)
		}
		if resp.ModeStatement == "" {
			t.Errorf("%q: mode_statement is empty", row.Cluster)
		}
		if !resp.ValuesNeverReturned {
			t.Errorf("%q: values_never_returned must be true", row.Cluster)
		}
	}
}
