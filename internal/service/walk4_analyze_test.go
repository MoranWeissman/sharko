package service

import (
	"context"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
)

// Pre-release walk 4 finding 11: Analyze showed a red "Conflicts Found" for
// values the generated file copied from the chart's own defaults ("image.tag
// yours: 6.7.1 old: 6.7.1 new: 6.8.0"). Nobody chose those. An all-clusters
// upgrade moves them with the chart, so they are listed apart and only a
// setting somebody really changed is a conflict.
func TestCheckUpgrade_V4_CopiedDefaultsAreNotConflicts(t *testing.T) {
	const addon = "podinfo"
	oldValues := "image:\n  tag: 6.7.1\nlogLevel: info\nfaults:\n  delay: false\n"
	newValues := "image:\n  tag: 6.8.0\nlogLevel: warn\nfaults:\n  delay: true\n"
	helmSrv := newHelmServerWithTarballs(t, addon, map[string]string{"6.7.1": oldValues, "6.8.0": newValues})

	generated := string(orchestrator.GenerateGlobalValuesFileV4(addon, addon, "6.7.1", helmSrv.URL, []byte(oldValues), false, false))
	// A person changed one setting; the rest is still the chart's copy.
	edited := replaceOnce(t, generated, "logLevel: info", "logLevel: debug")

	gp := &fakeGitProvider{files: map[string][]byte{
		orchestrator.EnginePinPath:   []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
		config.AddonCatalogPath:      v4DeltaYAML(t, addon, helmSrv.URL, addon, "6.7.1"),
		"values/global/podinfo.yaml": []byte(edited),
	}}

	resp, err := newTestUpgradeSvc(nil).CheckUpgrade(context.Background(), addon, "6.8.0", gp)
	if err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}

	conflicts := map[string]string{}
	for _, c := range resp.Conflicts {
		conflicts[c.Path] = c.ConfiguredValue
	}
	if _, bad := conflicts["image.tag"]; bad {
		t.Errorf("image.tag is the chart's own copied default — it must not be a conflict: %+v", resp.Conflicts)
	}
	if _, bad := conflicts["faults.delay"]; bad {
		t.Errorf("faults.delay was never changed — it must not be a conflict: %+v", resp.Conflicts)
	}
	if conflicts["logLevel"] != "debug" {
		t.Errorf("logLevel was changed by a person and the default moved too — that IS a conflict: %+v", resp.Conflicts)
	}

	follows := map[string]string{}
	for _, f := range resp.FollowsDefault {
		follows[f.Path] = f.OldValue + ">" + f.NewValue
	}
	if follows["image.tag"] != "6.7.1>6.8.0" || follows["faults.delay"] != "false>true" {
		t.Errorf("follows_default = %v, want image.tag 6.7.1>6.8.0 and faults.delay false>true", follows)
	}
	if _, bad := follows["logLevel"]; bad {
		t.Errorf("a setting a person changed must not be listed as following the chart: %v", follows)
	}
}

// A values file a person wrote (no Sharko header) is not touched by an
// upgrade, so everything in it that the chart changed stays a conflict.
func TestCheckUpgrade_V4_HandWrittenFileKeepsItsConflicts(t *testing.T) {
	const addon = "podinfo"
	oldValues := "image:\n  tag: 6.7.1\n"
	newValues := "image:\n  tag: 6.8.0\n"
	helmSrv := newHelmServerWithTarballs(t, addon, map[string]string{"6.7.1": oldValues, "6.8.0": newValues})
	gp := &fakeGitProvider{files: map[string][]byte{
		orchestrator.EnginePinPath:   []byte("apiVersion: argoproj.io/v1alpha1\nkind: Application\n"),
		config.AddonCatalogPath:      v4DeltaYAML(t, addon, helmSrv.URL, addon, "6.7.1"),
		"values/global/podinfo.yaml": []byte("image:\n  tag: 6.7.1\n"),
	}}
	resp, err := newTestUpgradeSvc(nil).CheckUpgrade(context.Background(), addon, "6.8.0", gp)
	if err != nil {
		t.Fatalf("CheckUpgrade: %v", err)
	}
	if len(resp.Conflicts) != 1 || resp.Conflicts[0].Path != "image.tag" {
		t.Errorf("expected image.tag to stay a conflict in a hand-written file, got %+v", resp.Conflicts)
	}
	if len(resp.FollowsDefault) != 0 {
		t.Errorf("nothing follows the chart in a hand-written file: %+v", resp.FollowsDefault)
	}
}

func replaceOnce(t *testing.T, s, old, new string) string {
	t.Helper()
	i := indexOf(s, old)
	if i < 0 {
		t.Fatalf("fixture: %q not found in:\n%s", old, s)
	}
	return s[:i] + new + s[i+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
