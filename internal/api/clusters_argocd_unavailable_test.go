package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// argoListFailingSrv answers every ArgoCD call normally except the cluster
// list, which fails the way the test asks: a status code, or a dropped
// connection (status 0).
func argoListFailingSrv(t *testing.T, status int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/clusters", func(w http.ResponseWriter, r *http.Request) {
		switch status {
		case http.StatusOK:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": []map[string]interface{}{
				{"name": "prod-eu", "server": "https://prod-eu.example.invalid", "info": map[string]interface{}{"connectionState": map[string]string{"status": "Successful"}}},
				{"name": "argo-only", "server": "https://argo-only.example.invalid"},
			}})
		case 0:
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("test server cannot drop a connection")
			}
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
		default:
			w.WriteHeader(status)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"items": []interface{}{}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestListClusters_WhenArgoCDCannotBeRead pins ep4 serious 2: when ArgoCD
// refuses Sharko or can't be reached, the clusters in Git stay Sharko's
// (managed: true, ArgoCD state "unknown"), none is counted as ArgoCD-only,
// and the answer says which of the two it was.
func TestListClusters_WhenArgoCDCannotBeRead(t *testing.T) {
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
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status %d, want 200 (body=%s)", w.Code, w.Body.String())
			}
			var resp models.ClustersResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decoding: %v", err)
			}
			if resp.ArgocdUnavailableReason != tc.wantReason {
				t.Errorf("argocd_unavailable_reason = %q, want %q", resp.ArgocdUnavailableReason, tc.wantReason)
			}
			if len(resp.Clusters) != 2 {
				t.Fatalf("got %d clusters, want the 2 from Git: %+v", len(resp.Clusters), resp.Clusters)
			}
			for _, c := range resp.Clusters {
				if !c.Managed {
					t.Errorf("cluster %q is not managed — the page would count it as ArgoCD-only", c.Name)
				}
				if c.ConnectionStatus != "unknown" {
					t.Errorf("cluster %q connection_status = %q, want \"unknown\"", c.Name, c.ConnectionStatus)
				}
			}
			if resp.HealthStats == nil || resp.HealthStats.NotInGit != 0 || resp.HealthStats.TotalInGit != 2 {
				t.Errorf("health stats = %+v, want 2 in Git and 0 ArgoCD-only", resp.HealthStats)
			}
			if len(resp.OrphanRegistrations) != 0 {
				t.Errorf("orphans = %+v, want none", resp.OrphanRegistrations)
			}
		})
	}

	t.Run("ArgoCD answers", func(t *testing.T) {
		argo := argoListFailingSrv(t, http.StatusOK)
		gp := &reconcileFakeGP{managedYAML: []byte("clusters:\n- name: prod-eu\n  labels: {}\n")}
		_, router := reconcileTestServer(t, gp, argo.URL)

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/clusters", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status %d (body=%s)", w.Code, w.Body.String())
		}
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(w.Body.Bytes(), &raw)
		if _, present := raw["argocd_unavailable_reason"]; present {
			t.Errorf("argocd_unavailable_reason is present on a good read: %s", raw["argocd_unavailable_reason"])
		}
		var resp models.ClustersResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		for _, c := range resp.Clusters {
			if c.Name == "prod-eu" && (!c.Managed || c.ConnectionStatus != "Successful") {
				t.Errorf("prod-eu = managed %v, status %q; want managed and Successful", c.Managed, c.ConnectionStatus)
			}
		}
	})
}
