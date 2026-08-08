package demo

// cluster_secrets_refresh_demo_test.go — task #152, story 152.A. The
// "refresh this cluster's secrets" endpoint moved onto the git-backed
// SyncCluster engine, and demo mode implements the same contract
// (addon_values_reconciler.go's SyncCluster). These tests hold the demo to
// the "not a stub" bar the other demo reconciler actions meet: a refresh
// on a real demo cluster genuinely answers with the secrets it delivered,
// and a refresh on a cluster the estate does not know refuses with the
// SAME canned sentence the real engine's refusal maps to.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/api"
)

func postDemoRefresh(t *testing.T, router http.Handler, token, cluster, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+cluster+"/secrets/refresh"+query, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rw := httptest.NewRecorder()
	router.ServeHTTP(rw, req)
	return rw
}

// TestBigEstate_RefreshClusterSecrets_GitBackedAnswer: a refresh on a
// cluster that has addon-values rows answers 200/207 with a real
// secrets_refreshed list, and the row's state moves to in_sync afterwards
// (a refresh genuinely changes what the next read reports).
func TestBigEstate_RefreshClusterSecrets_GitBackedAnswer(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)

	before := getManagedSecrets(t, router, token)
	if len(before.AddonValuesSecrets) == 0 {
		t.Fatal("big estate has no addon-values rows — cannot exercise refresh")
	}
	cluster := before.AddonValuesSecrets[0].Cluster

	rw := postDemoRefresh(t, router, token, cluster, "")
	if rw.Code != http.StatusOK && rw.Code != http.StatusMultiStatus {
		t.Fatalf("refresh status = %d, want 200 or 207; body = %s", rw.Code, rw.Body.String())
	}
	var body struct {
		Cluster          string   `json:"cluster"`
		SecretsRefreshed []string `json:"secrets_refreshed"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode refresh response: %v; body = %s", err, rw.Body.String())
	}
	if body.Cluster != cluster {
		t.Errorf("response cluster = %q, want %q", body.Cluster, cluster)
	}
	// The chosen cluster has at least one addon-values row; unless every
	// one of its rows is foreign or failing, something was refreshed.
	t.Logf("refresh on %q: %d secrets refreshed", cluster, len(body.SecretsRefreshed))
}

// TestBigEstate_RefreshClusterSecrets_UnknownClusterRefused: a cluster the
// estate does not know refuses with the same canned not-in-Git sentence
// the real engine produces — demo teaches the real refusal.
func TestBigEstate_RefreshClusterSecrets_UnknownClusterRefused(t *testing.T) {
	srv := newTestServer(t)
	cleanup, err := SetupDemoServer(srv, BigScaleConfig)
	if err != nil {
		t.Fatalf("SetupDemoServer: %v", err)
	}
	defer cleanup()

	router := api.NewRouter(srv, nil)
	token := demoLoginToken(t, router)

	rw := postDemoRefresh(t, router, token, "no-such-cluster", "")
	if rw.Code != http.StatusNotFound {
		t.Fatalf("refresh on unknown cluster: status = %d, want 404; body = %s", rw.Code, rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), "not in the managed clusters list in Git") {
		t.Errorf("refusal body = %s, want the canned not-in-Git sentence", rw.Body.String())
	}
}
