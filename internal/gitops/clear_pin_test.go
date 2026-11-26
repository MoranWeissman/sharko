package gitops

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

func TestClearClusterAddonsAddonVersion(t *testing.T) {
	prune := true
	body, err := models.SaveClusterAddons(models.ClusterAddonsSpec{
		Cluster: "spoke-1",
		Addons: map[string]models.ClusterAddonsAddon{
			"podinfo":  {Enabled: true, Version: "6.7.1", Settings: &models.ClusterAddonsAddonSettings{Namespace: "apps", Prune: &prune}},
			"reloader": {Enabled: true, Version: "2.2.21"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := ClearClusterAddonsAddonVersion(body, "podinfo")
	if err != nil {
		t.Fatalf("ClearClusterAddonsAddonVersion: %v", err)
	}
	spec, err := models.LoadClusterAddons(out)
	if err != nil {
		t.Fatal(err)
	}
	got := spec.Addons["podinfo"]
	if got.Version != "" {
		t.Errorf("pin = %q, want none", got.Version)
	}
	if !got.Enabled || got.Settings == nil || got.Settings.Namespace != "apps" {
		t.Errorf("everything else in the entry must stay: %+v", got)
	}
	if spec.Addons["reloader"].Version != "2.2.21" {
		t.Errorf("another addon's pin was touched: %+v", spec.Addons["reloader"])
	}
	if _, err := ClearClusterAddonsAddonVersion(body, "nosuchaddon"); err == nil {
		t.Error("an addon with no entry must be an error")
	}
}

// An upgrade for all clusters takes the version out of each cluster file.
// That used to drop the comments at the top of the file.
func TestClearClusterAddonsAddonVersion_KeepsTheCommentsAtTheTop(t *testing.T) {
	body, err := models.SaveClusterAddons(models.ClusterAddonsSpec{
		Cluster: "spoke-1",
		Addons:  map[string]models.ClusterAddonsAddon{"podinfo": {Enabled: true, Version: "6.7.1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	const header = "# Which approved addons run on spoke-1.\n# Edit through Sharko or by hand.\n"
	out, err := ClearClusterAddonsAddonVersion(append([]byte(header), body...), "podinfo")
	if err != nil {
		t.Fatalf("ClearClusterAddonsAddonVersion: %v", err)
	}
	if !strings.HasPrefix(string(out), header) {
		t.Errorf("the comments at the top of the file were dropped:\n%s", out)
	}
	spec, err := models.LoadClusterAddons(out)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Addons["podinfo"].Version != "" {
		t.Errorf("pin = %q, want none", spec.Addons["podinfo"].Version)
	}
}
