package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
)

// TestGetOverview_NoPerAppGetApplicationCalls_PerfM2 is the regression test
// for the observability N+1 fix: GetOverview used to call
// ac.GetApplication(ctx, app.Name) once per addon app after already
// listing them, even though ac.ListApplicationsSummary is a plain alias
// for ac.ListApplications (internal/argocd/client.go) and both decode the
// exact same JSON shape — history and resources included. The stub ArgoCD
// server below serves History/Resources data ONLY from the list endpoint
// and fails the test if the per-application GET endpoint is ever hit,
// proving GetOverview now gets everything it needs from the one list call.
func TestGetOverview_NoPerAppGetApplicationCalls_PerfM2(t *testing.T) {
	const appName = "metrics-server-prod-eu"

	appJSON := `{
		"metadata": {"name": "` + appName + `"},
		"spec": {
			"project": "default",
			"source": {"repoURL": "https://charts.example.com", "chart": "metrics-server", "targetRevision": "3.1.0"},
			"destination": {"server": "https://prod-eu.example.com", "name": "prod-eu"}
		},
		"status": {
			"sync": {"status": "Synced"},
			"health": {"status": "Healthy", "lastTransitionTime": "2026-08-01T00:00:00Z"},
			"reconciledAt": "2026-08-01T00:00:00Z",
			"history": [
				{"id": 1, "deployedAt": "2026-08-01T00:01:00Z", "deployStartedAt": "2026-08-01T00:00:30Z", "revision": "abc123"}
			],
			"resources": [
				{"kind": "Deployment", "name": "metrics-server", "namespace": "kube-system", "status": "Synced", "health": {"status": "Healthy"}}
			]
		}
	}`

	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/api/v1/clusters", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"name":"prod-eu","server":"https://prod-eu.example.com","info":{"connectionState":{"status":"Successful"}}}]}`))
	})
	mux.HandleFunc("/api/v1/applications", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[` + appJSON + `]}`))
	})
	mux.HandleFunc("/api/v1/applications/"+appName, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("GetOverview called GetApplication(%q) — the N+1 fix removed this per-app round trip; ListApplicationsSummary already carries history/resources", appName)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ac := argocd.NewClient(srv.URL, "test-token", true)
	obsSvc := NewObservabilityService(NewClusterService(""))

	overview, err := obsSvc.GetOverview(t.Context(), ac, nil)
	if err != nil {
		t.Fatalf("GetOverview: %v", err)
	}

	if len(overview.AddonHealth) != 1 {
		t.Fatalf("expected 1 addon in AddonHealth, got %d: %+v", len(overview.AddonHealth), overview.AddonHealth)
	}
	addon := overview.AddonHealth[0]
	if addon.AddonName != "metrics-server" {
		t.Errorf("AddonName = %q, want %q", addon.AddonName, "metrics-server")
	}
	if len(addon.Clusters) != 1 {
		t.Fatalf("expected 1 cluster entry, got %d", len(addon.Clusters))
	}
	ch := addon.Clusters[0]
	if ch.ClusterName != "prod-eu" {
		t.Errorf("ClusterName = %q, want %q", ch.ClusterName, "prod-eu")
	}
	// Resource/history data must have come through even without any
	// per-app GetApplication call.
	if ch.ResourceCount != 1 || ch.HealthyResources != 1 {
		t.Errorf("ResourceCount/HealthyResources = %d/%d, want 1/1 (from the list response's resources)", ch.ResourceCount, ch.HealthyResources)
	}
	if ch.LastDeployTime == "" {
		t.Errorf("LastDeployTime is empty, want a value derived from the list response's history")
	}

	if len(overview.RecentSyncs) != 1 {
		t.Errorf("expected 1 recent sync entry (from history), got %d", len(overview.RecentSyncs))
	}
}
