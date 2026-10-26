package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// TestFleetStatus_WhenArgoCDCannotBeRead pins ep4 T1: when ArgoCD refuses
// Sharko's login or cannot be reached, the fleet status must not count the
// clusters as disconnected. It says ArgoCD is unavailable, says why, and keeps
// the number of clusters from Git with no health counted.
func TestFleetStatus_WhenArgoCDCannotBeRead(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		wantReason string
	}{
		{"ArgoCD refuses the login (401)", http.StatusUnauthorized, models.ArgocdUnavailableAuthRefused},
		{"ArgoCD refuses the request (403)", http.StatusForbidden, models.ArgocdUnavailableAuthRefused},
		{"ArgoCD fails (500)", http.StatusInternalServerError, models.ArgocdUnavailableUnreachable},
		{"ArgoCD drops the connection", 0, models.ArgocdUnavailableUnreachable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			argo := argoListFailingSrv(t, tc.status)
			gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n- name: prod-us\n  labels: {}\n")}
			_, router := reconcileTestServer(t, gp, argo.URL)

			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/fleet/status", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status %d, want 200 (body=%s)", w.Code, w.Body.String())
			}
			var body fleetStatusResponse
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decoding: %v", err)
			}
			if !body.ArgoUnavailable {
				t.Error("argo_unavailable = false, want true while ArgoCD cannot be read")
			}
			if body.ArgoUnavailableReason != tc.wantReason {
				t.Errorf("argo_unavailable_reason = %q, want %q", body.ArgoUnavailableReason, tc.wantReason)
			}
			if body.DisconnectedClusters != 0 {
				t.Errorf("disconnected_clusters = %d, want 0: the clusters are not disconnected, ArgoCD is just not answering", body.DisconnectedClusters)
			}
			if body.HealthyClusters != 0 || body.DegradedClusters != 0 {
				t.Errorf("healthy %d / degraded %d, want 0 / 0: health is not known", body.HealthyClusters, body.DegradedClusters)
			}
			if body.TotalClusters != 2 || len(body.Clusters) != 2 {
				t.Errorf("total_clusters = %d (%d listed), want the 2 from Git", body.TotalClusters, len(body.Clusters))
			}
			if body.TotalDeployments != 0 || body.DegradedDeployments != 0 {
				t.Errorf("deployments %d (degraded %d), want 0: addon numbers come from ArgoCD", body.TotalDeployments, body.DegradedDeployments)
			}
		})
	}

	t.Run("ArgoCD answers", func(t *testing.T) {
		argo := argoListFailingSrv(t, http.StatusOK)
		gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n")}
		_, router := reconcileTestServer(t, gp, argo.URL)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/fleet/status", nil))
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
			t.Fatalf("decoding: %v (body=%s)", err, w.Body.String())
		}
		if _, present := raw["argo_unavailable_reason"]; present {
			t.Errorf("argo_unavailable_reason is present on a good read: %s", raw["argo_unavailable_reason"])
		}
		var body fleetStatusResponse
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body.ArgoUnavailable || body.HealthyClusters != 1 {
			t.Errorf("argo_unavailable %v, healthy %d; want false and 1", body.ArgoUnavailable, body.HealthyClusters)
		}
	})
}
