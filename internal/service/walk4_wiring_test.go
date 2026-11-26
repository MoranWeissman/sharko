package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// walk4Repo is a v4 repo with podinfo 6.8.0 in the catalog and three
// clusters: one following the catalog, one on an older pin, one that was
// upgraded on its own to 6.9.0.
func walk4Repo(t *testing.T) *fakeGitProvider {
	t.Helper()
	cluster := func(name, pin string) []byte {
		body, err := models.SaveClusterAddons(models.ClusterAddonsSpec{
			Cluster: name,
			Addons:  map[string]models.ClusterAddonsAddon{"podinfo": {Enabled: true, Version: pin}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	catalogBody, err := config.SaveAddonCatalog(config.AddonCatalogSpec{
		Addons: map[string]config.AddonCatalogEntry{
			"podinfo": {RepoURL: "https://stefanprodan.github.io/podinfo", Chart: "podinfo", Version: "6.8.0", Namespace: "podinfo"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fakeGitProvider{files: map[string][]byte{
		orchestrator.EnginePinPath:    []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
		config.AddonCatalogPath:       catalogBody,
		"cluster-addons/same.yaml":    cluster("same", ""),
		"cluster-addons/older.yaml":   cluster("older", "6.7.1"),
		"cluster-addons/newer.yaml":   cluster("newer", "6.9.0"),
		"cluster-addons/missing.yaml": cluster("missing", "99.0.0"),
	}}
}

// walk4Argo answers ArgoCD's application list: "missing" asks for a chart
// version that does not exist while 6.8.0 keeps running.
func walk4Argo(t *testing.T) *argocd.Client {
	t.Helper()
	app := func(name, target, deployed, syncStatus string, conditions []map[string]interface{}) map[string]interface{} {
		status := map[string]interface{}{
			"sync":   map[string]interface{}{"status": syncStatus},
			"health": map[string]interface{}{"status": "Healthy"},
		}
		if deployed != "" {
			status["history"] = []map[string]interface{}{{"id": 1, "revisions": []string{"abc123", deployed}}}
		}
		if conditions != nil {
			status["conditions"] = conditions
		}
		return map[string]interface{}{
			"metadata": map[string]interface{}{"name": "podinfo-" + name, "namespace": "argocd"},
			"spec": map[string]interface{}{"sources": []map[string]interface{}{
				{"repoURL": "https://git.example/repo", "targetRevision": "main", "ref": "values"},
				{"repoURL": "https://stefanprodan.github.io/podinfo", "chart": "podinfo", "targetRevision": target},
			}},
			"status": status,
		}
	}
	apps := map[string]interface{}{"items": []map[string]interface{}{
		app("same", "6.8.0", "6.8.0", "Synced", nil),
		app("older", "6.7.1", "6.7.1", "Synced", nil),
		app("newer", "6.9.0", "6.9.0", "Synced", nil),
		app("missing", "99.0.0", "6.8.0", "Unknown", []map[string]interface{}{{"type": "ComparisonError", "message": "chart not found"}}),
	}}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apps)
	}))
	t.Cleanup(ts.Close)
	return argocd.NewClient(ts.URL, "fake-token", false)
}

// The version matrix feeds the Addons page, the Dashboard "behind" count and
// the bell. A cluster on a NEWER version than the catalog is marked ahead,
// so none of them calls it "behind".
func TestGetVersionMatrix_V4_MarksAClusterAheadOfTheCatalog(t *testing.T) {
	resp, err := NewAddonService("").GetVersionMatrix(context.Background(), walk4Repo(t), walk4Argo(t))
	if err != nil {
		t.Fatalf("GetVersionMatrix: %v", err)
	}
	if len(resp.Addons) != 1 {
		t.Fatalf("expected one addon row, got %d", len(resp.Addons))
	}
	cells := resp.Addons[0].Cells
	for cluster, want := range map[string]struct{ drift, ahead bool }{
		"same":  {false, false},
		"older": {true, false},
		"newer": {true, true},
	} {
		got := cells[cluster]
		if got.DriftFromCatalog != want.drift || got.AheadOfCatalog != want.ahead {
			t.Errorf("%s: drift=%v ahead=%v, want drift=%v ahead=%v", cluster, got.DriftFromCatalog, got.AheadOfCatalog, want.drift, want.ahead)
		}
	}
}

// The addon page's per-cluster "deployed version" is what ArgoCD really
// deployed, not the version that was asked for.
func TestGetAddonDetail_V4_DeployedVersionIsWhatRuns(t *testing.T) {
	resp, err := NewAddonService("").GetAddonDetail(context.Background(), "podinfo", walk4Repo(t), walk4Argo(t))
	if err != nil {
		t.Fatalf("GetAddonDetail: %v", err)
	}
	var found bool
	for _, app := range resp.Addon.Applications {
		if app.ClusterName != "missing" {
			continue
		}
		found = true
		if app.ConfiguredVersion != "99.0.0" {
			t.Errorf("configured version = %q, want 99.0.0 (what Git asks for)", app.ConfiguredVersion)
		}
		if app.DeployedVersion != "6.8.0" {
			t.Errorf("deployed version = %q, want 6.8.0 — 99.0.0 does not exist and never ran", app.DeployedVersion)
		}
	}
	if !found {
		t.Fatalf("cluster \"missing\" not in the response: %+v", resp.Addon.Applications)
	}
}
