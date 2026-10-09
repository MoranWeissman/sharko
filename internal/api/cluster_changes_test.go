package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/changelog"
	"github.com/MoranWeissman/sharko/internal/models"
)

// TestHandleGetClusterChanges_ArgoCDUnavailable_ReturnsUnknownOutcome
// covers the graceful-degradation contract: when there is no active
// ArgoCD connection (newTestServer's connSvc has nothing configured), the
// endpoint must still return 200 with every recorded entry, each carrying
// deploy_outcome "unknown" — never a 5xx just because ArgoCD is
// unreachable/unconfigured.
func TestHandleGetClusterChanges_ArgoCDUnavailable_ReturnsUnknownOutcome(t *testing.T) {
	srv := newTestServer()
	srv.ChangeLogStore().Record(changelog.Entry{
		Operation:   "addon enable",
		Addon:       "cert-manager",
		Cluster:     "prod-eu",
		PRID:        42,
		PRUrl:       "https://example.invalid/pr/42",
		OpenedAt:    time.Now().Add(-time.Hour),
		CompletedAt: time.Now(),
		Status:      changelog.StatusMerged,
	})

	router := NewRouter(srv, nil)
	req := httptest.NewRequest("GET", "/api/v1/clusters/prod-eu/changes", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		ClusterName string `json:"cluster_name"`
		Changes     []struct {
			PRID          int    `json:"pr_id"`
			Status        string `json:"status"`
			DeployOutcome string `json:"deploy_outcome"`
		} `json:"changes"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body.ClusterName != "prod-eu" {
		t.Errorf("expected cluster_name prod-eu, got %s", body.ClusterName)
	}
	if len(body.Changes) != 1 {
		t.Fatalf("expected 1 change entry, got %d", len(body.Changes))
	}
	if body.Changes[0].PRID != 42 {
		t.Errorf("expected pr_id 42, got %d", body.Changes[0].PRID)
	}
	if body.Changes[0].DeployOutcome != "unknown" {
		t.Errorf("expected deploy_outcome unknown when ArgoCD is unavailable, got %s", body.Changes[0].DeployOutcome)
	}
}

// TestHandleGetClusterChanges_NoEntries_ReturnsEmptyList asserts a cluster
// with no recorded changes gets a 200 with an empty (not null) list rather
// than a 404 — this is a log, absence of entries is a normal state.
func TestHandleGetClusterChanges_NoEntries_ReturnsEmptyList(t *testing.T) {
	srv := newTestServer()
	router := NewRouter(srv, nil)

	req := httptest.NewRequest("GET", "/api/v1/clusters/never-touched/changes", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	changes, ok := body["changes"].([]interface{})
	if !ok {
		t.Fatalf("expected changes to decode as a list, got %T", body["changes"])
	}
	if len(changes) != 0 {
		t.Errorf("expected 0 changes, got %d", len(changes))
	}
}

// TestDeployOutcomeFor covers the read-time join logic directly: given an
// observability overview snapshot, map an addon+cluster pair to the coarse
// healthy/failed/unknown outcome the API surfaces.
func TestDeployOutcomeFor(t *testing.T) {
	overview := &models.ObservabilityOverviewResponse{
		AddonHealth: []models.AddonHealthDetail{
			{
				AddonName: "cert-manager",
				Clusters: []models.AddonClusterHealth{
					{ClusterName: "prod-eu", Health: "Healthy"},
					{ClusterName: "prod-us", Health: "Degraded"},
					{ClusterName: "staging", Health: "Progressing"},
				},
			},
		},
	}

	cases := []struct {
		name     string
		overview *models.ObservabilityOverviewResponse
		addon    string
		cluster  string
		want     string
	}{
		{"nil overview (argocd unavailable)", nil, "cert-manager", "prod-eu", "unknown"},
		{"healthy addon+cluster", overview, "cert-manager", "prod-eu", "healthy"},
		{"degraded addon+cluster", overview, "cert-manager", "prod-us", "failed"},
		{"other health value", overview, "cert-manager", "staging", "unknown"},
		{"addon not present", overview, "unknown-addon", "prod-eu", "unknown"},
		{"cluster not present for addon", overview, "cert-manager", "does-not-exist", "unknown"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deployOutcomeFor(tc.overview, tc.addon, tc.cluster)
			if got != tc.want {
				t.Errorf("deployOutcomeFor(%s, %s) = %q, want %q", tc.addon, tc.cluster, got, tc.want)
			}
		})
	}
}

// TestHandleGetClusterChanges_MissingClusterName covers the 400 guard —
// mirrors handleGetClusterHistory's contract for a blank {name}.
func TestHandleGetClusterChanges_MissingClusterName(t *testing.T) {
	srv := newTestServer()

	req := httptest.NewRequest("GET", "/api/v1/clusters//changes", nil)
	req.SetPathValue("name", "")
	w := httptest.NewRecorder()

	srv.handleGetClusterChanges(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing cluster name, got %d", w.Code)
	}
}
