package service

import (
	"context"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// These tests cover two reads that used to look only at the v3 files:
// GetAddonValues (the addon page's global values) and the observability
// "missing resource limits" alerts. On a v4 repo (one with the engine pin)
// both found nothing: GetAddonValues failed with "file not found", and the
// alerts list was always empty. A v4 repo keeps these files at catalog.yaml
// and values/global/<addon>.yaml.

// strictFakeGit is fakeGitProvider, except a missing file returns
// gitprovider.ErrFileNotFound the way the real providers do. The plain
// fake returns empty content with no error, which hides "no values file".
type strictFakeGit struct{ *fakeGitProvider }

func (f strictFakeGit) GetFileContent(ctx context.Context, path, ref string) ([]byte, error) {
	data, ok := f.files[path]
	if !ok {
		return nil, gitprovider.ErrFileNotFound
	}
	return data, nil
}

const testEnginePin = "apiVersion: argoproj.io/v1alpha1\nkind: Application\n"

func v4CatalogBytes(t *testing.T, names ...string) []byte {
	t.Helper()
	spec := config.AddonCatalogSpec{Addons: map[string]config.AddonCatalogEntry{}}
	for _, n := range names {
		spec.Addons[n] = config.AddonCatalogEntry{RepoURL: "https://charts.example.com", Chart: n, Version: "1.0.0"}
	}
	data, err := config.SaveAddonCatalog(spec)
	if err != nil {
		t.Fatalf("SaveAddonCatalog: %v", err)
	}
	return data
}

func TestGetAddonValues_V4Repo_ReadsValuesGlobal(t *testing.T) {
	gp := &fakeGitProvider{files: map[string][]byte{
		orchestrator.EnginePinPath:                  []byte(testEnginePin),
		orchestrator.V4GlobalValuesDir + "/cm.yaml": []byte("replicaCount: 2\n"),
	}}
	resp, err := NewAddonService("").GetAddonValues(context.Background(), "cm", strictFakeGit{gp})
	if err != nil {
		t.Fatalf("GetAddonValues on a v4 repo returned error: %v", err)
	}
	if resp.ValuesYAML != "replicaCount: 2\n" {
		t.Errorf("ValuesYAML = %q, want the values/global/cm.yaml content", resp.ValuesYAML)
	}
}

func TestGetAddonValues_V3Repo_StillReadsConfigurationPath(t *testing.T) {
	gp := &fakeGitProvider{files: map[string][]byte{
		"configuration/addons-global-values/cm.yaml": []byte("v3: true\n"),
		// A v4-path file with no engine pin must be ignored.
		orchestrator.V4GlobalValuesDir + "/cm.yaml": []byte("v4: true\n"),
	}}
	resp, err := NewAddonService("").GetAddonValues(context.Background(), "cm", strictFakeGit{gp})
	if err != nil {
		t.Fatalf("GetAddonValues on a v3 repo returned error: %v", err)
	}
	if resp.ValuesYAML != "v3: true\n" {
		t.Errorf("ValuesYAML = %q, want the v3 file content", resp.ValuesYAML)
	}
}

func deployedGroups(names ...string) []models.AddonGroupHealth {
	out := make([]models.AddonGroupHealth, 0, len(names))
	for _, n := range names {
		out = append(out, models.AddonGroupHealth{AddonName: n, TotalApps: 1})
	}
	return out
}

func TestCheckResourceAlerts_V4Repo(t *testing.T) {
	gp := &fakeGitProvider{files: map[string][]byte{
		orchestrator.EnginePinPath:                            []byte(testEnginePin),
		config.AddonCatalogPath:                               v4CatalogBytes(t, "no-limits", "has-limits", "no-values-file", "not-deployed"),
		orchestrator.V4GlobalValuesDir + "/no-limits.yaml":    []byte("replicaCount: 1\n"),
		orchestrator.V4GlobalValuesDir + "/has-limits.yaml":   []byte("resources:\n  limits:\n    cpu: 100m\n"),
		orchestrator.V4GlobalValuesDir + "/not-deployed.yaml": []byte("replicaCount: 1\n"),
	}}
	svc := NewObservabilityService(nil)
	alerts := svc.checkResourceAlerts(context.Background(), strictFakeGit{gp},
		deployedGroups("no-limits", "has-limits", "no-values-file"))

	if len(alerts) != 1 {
		t.Fatalf("got %d alerts (%+v), want exactly 1 for no-limits", len(alerts), alerts)
	}
	if alerts[0].AddonName != "no-limits" || alerts[0].AlertType != "missing_limits" {
		t.Errorf("alert = %+v, want no-limits / missing_limits", alerts[0])
	}
	if alerts[0].Details != "No resource requests/limits configured in global values" {
		t.Errorf("alert details = %q", alerts[0].Details)
	}
}

func TestCheckResourceAlerts_V3Repo_Unchanged(t *testing.T) {
	gp := &fakeGitProvider{files: map[string][]byte{
		"configuration/addons-catalog.yaml":                 []byte("applicationsets:\n  - name: no-limits\n    repoURL: https://charts.example.com\n    chart: no-limits\n    version: 1.0.0\n"),
		"configuration/addons-global-values/no-limits.yaml": []byte("replicaCount: 1\n"),
	}}
	svc := NewObservabilityService(nil)
	alerts := svc.checkResourceAlerts(context.Background(), strictFakeGit{gp}, deployedGroups("no-limits"))
	if len(alerts) != 1 || alerts[0].AddonName != "no-limits" {
		t.Fatalf("v3 alerts = %+v, want one alert for no-limits", alerts)
	}
}
