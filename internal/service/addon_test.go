package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moran/argocd-addons-platform/internal/argocd"
	"github.com/moran/argocd-addons-platform/internal/gitprovider"
)

// fakeGitProvider implements gitprovider.GitProvider for testing.
type fakeGitProvider struct {
	files map[string][]byte
}

func (f *fakeGitProvider) GetFileContent(_ context.Context, path, _ string) ([]byte, error) {
	return f.files[path], nil
}

func (f *fakeGitProvider) ListDirectory(_ context.Context, _, _ string) ([]string, error) {
	return nil, nil
}

func (f *fakeGitProvider) ListPullRequests(_ context.Context, _ string) ([]gitprovider.PullRequest, error) {
	return nil, nil
}

func (f *fakeGitProvider) TestConnection(_ context.Context) error {
	return nil
}

func (f *fakeGitProvider) CreateBranch(_ context.Context, _, _ string) error {
	return nil
}

func (f *fakeGitProvider) CreateOrUpdateFile(_ context.Context, _ string, _ []byte, _, _ string) error {
	return nil
}

func (f *fakeGitProvider) DeleteFile(_ context.Context, _, _, _ string) error {
	return nil
}

func (f *fakeGitProvider) CreatePullRequest(_ context.Context, _, _, _, _ string) (*gitprovider.PullRequest, error) {
	return nil, nil
}

func (f *fakeGitProvider) MergePullRequest(_ context.Context, _ int) error {
	return nil
}

func (f *fakeGitProvider) DeleteBranch(_ context.Context, _ string) error {
	return nil
}

func TestGetVersionMatrix(t *testing.T) {
	clusterAddonsYAML := []byte(`
clusters:
  - name: cluster-a
    labels:
      ingress-nginx: enabled
      cert-manager: enabled
      cert-manager-version: "1.15.0"
      external-dns: disabled
  - name: cluster-b
    labels:
      ingress-nginx: enabled
      cert-manager: enabled
`)

	addonsCatalogYAML := []byte(`
applicationsets:
  - appName: cert-manager
    repoURL: https://charts.jetstack.io
    chart: cert-manager
    version: "1.14.0"
    namespace: cert-manager
  - appName: ingress-nginx
    repoURL: https://kubernetes.github.io/ingress-nginx
    chart: ingress-nginx
    version: "4.10.0"
    namespace: ingress-nginx
  - appName: external-dns
    repoURL: https://kubernetes-sigs.github.io/external-dns
    chart: external-dns
    version: "1.14.0"
    namespace: external-dns
`)

	// Fake ArgoCD server returning applications
	argoApps := map[string]interface{}{
		"items": []map[string]interface{}{
			{
				"metadata": map[string]interface{}{"name": "cert-manager-cluster-a", "namespace": "argocd"},
				"spec": map[string]interface{}{
					"project": "default",
					"source":  map[string]interface{}{"repoURL": "https://charts.jetstack.io", "targetRevision": "1.15.0", "chart": "cert-manager"},
					"destination": map[string]interface{}{"server": "https://cluster-a", "namespace": "cert-manager"},
				},
				"status": map[string]interface{}{
					"sync":   map[string]interface{}{"status": "Synced"},
					"health": map[string]interface{}{"status": "Healthy"},
				},
			},
			{
				"metadata": map[string]interface{}{"name": "ingress-nginx-cluster-a", "namespace": "argocd"},
				"spec": map[string]interface{}{
					"project": "default",
					"source":  map[string]interface{}{"repoURL": "https://kubernetes.github.io/ingress-nginx", "targetRevision": "4.10.0", "chart": "ingress-nginx"},
					"destination": map[string]interface{}{"server": "https://cluster-a", "namespace": "ingress-nginx"},
				},
				"status": map[string]interface{}{
					"sync":   map[string]interface{}{"status": "Synced"},
					"health": map[string]interface{}{"status": "Degraded"},
				},
			},
			{
				"metadata": map[string]interface{}{"name": "cert-manager-cluster-b", "namespace": "argocd"},
				"spec": map[string]interface{}{
					"project": "default",
					"source":  map[string]interface{}{"repoURL": "https://charts.jetstack.io", "targetRevision": "1.14.0", "chart": "cert-manager"},
					"destination": map[string]interface{}{"server": "https://cluster-b", "namespace": "cert-manager"},
				},
				"status": map[string]interface{}{
					"sync":   map[string]interface{}{"status": "Synced"},
					"health": map[string]interface{}{"status": "Healthy"},
				},
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(argoApps)
	}))
	defer ts.Close()

	gp := &fakeGitProvider{
		files: map[string][]byte{
			"configuration/cluster-addons.yaml": clusterAddonsYAML,
			"configuration/addons-catalog.yaml": addonsCatalogYAML,
		},
	}

	ac := argocd.NewClient(ts.URL, "fake-token", false)
	svc := NewAddonService()

	resp, err := svc.GetVersionMatrix(context.Background(), gp, ac)
	if err != nil {
		t.Fatalf("GetVersionMatrix returned error: %v", err)
	}

	// Verify clusters are sorted
	if len(resp.Clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d", len(resp.Clusters))
	}
	if resp.Clusters[0] != "cluster-a" || resp.Clusters[1] != "cluster-b" {
		t.Errorf("expected clusters [cluster-a, cluster-b], got %v", resp.Clusters)
	}

	// Verify addons are sorted by name
	if len(resp.Addons) != 3 {
		t.Fatalf("expected 3 addons, got %d", len(resp.Addons))
	}
	if resp.Addons[0].AddonName != "cert-manager" {
		t.Errorf("expected first addon to be cert-manager, got %s", resp.Addons[0].AddonName)
	}
	if resp.Addons[1].AddonName != "external-dns" {
		t.Errorf("expected second addon to be external-dns, got %s", resp.Addons[1].AddonName)
	}
	if resp.Addons[2].AddonName != "ingress-nginx" {
		t.Errorf("expected third addon to be ingress-nginx, got %s", resp.Addons[2].AddonName)
	}

	// Check cert-manager on cluster-a: version override 1.15.0, drift = true, health = Healthy
	cmA := resp.Addons[0].Cells["cluster-a"]
	if cmA.Version != "1.15.0" {
		t.Errorf("cert-manager cluster-a version: expected 1.15.0, got %s", cmA.Version)
	}
	if !cmA.DriftFromCatalog {
		t.Error("cert-manager cluster-a should have drift_from_catalog=true")
	}
	if cmA.Health != "Healthy" {
		t.Errorf("cert-manager cluster-a health: expected Healthy, got %s", cmA.Health)
	}

	// Check cert-manager on cluster-b: no override, no drift, health = Healthy
	cmB := resp.Addons[0].Cells["cluster-b"]
	if cmB.Version != "1.14.0" {
		t.Errorf("cert-manager cluster-b version: expected 1.14.0, got %s", cmB.Version)
	}
	if cmB.DriftFromCatalog {
		t.Error("cert-manager cluster-b should have drift_from_catalog=false")
	}
	if cmB.Health != "Healthy" {
		t.Errorf("cert-manager cluster-b health: expected Healthy, got %s", cmB.Health)
	}

	// Check external-dns on cluster-a: disabled
	edA := resp.Addons[1].Cells["cluster-a"]
	if edA.Health != "not_enabled" {
		t.Errorf("external-dns cluster-a health: expected not_enabled, got %s", edA.Health)
	}

	// Check external-dns on cluster-b: no label, should not exist
	if _, exists := resp.Addons[1].Cells["cluster-b"]; exists {
		t.Error("external-dns should not have an entry for cluster-b (no label)")
	}

	// Check ingress-nginx on cluster-a: health = Degraded
	inA := resp.Addons[2].Cells["cluster-a"]
	if inA.Health != "Degraded" {
		t.Errorf("ingress-nginx cluster-a health: expected Degraded, got %s", inA.Health)
	}

	// Check ingress-nginx on cluster-b: enabled but no ArgoCD app -> missing
	inB := resp.Addons[2].Cells["cluster-b"]
	if inB.Health != "missing" {
		t.Errorf("ingress-nginx cluster-b health: expected missing, got %s", inB.Health)
	}
}
