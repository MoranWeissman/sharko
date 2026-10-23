package demo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/api"
)

// EP3-DRYRUN-FIXES call 4: the leftovers list now needs the
// registration-pending mark. The big demo estate's Sharko-owned
// Argo-only cluster must still show there, so the demo keeps showing the
// "registration that never finished" case.
func TestBigEstate_OrphanRegistration_StillShows(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("GET /clusters status = %d; body = %s", rw.Code, rw.Body.String())
	}
	var body struct {
		OrphanRegistrations []struct {
			ClusterName string `json:"cluster_name"`
		} `json:"orphan_registrations"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.OrphanRegistrations) == 0 {
		t.Fatal("the demo's leftover registration no longer shows on the clusters list")
	}
}
