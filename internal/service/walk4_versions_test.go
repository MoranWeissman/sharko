package service

import (
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/models"
)

// Pre-release walk 4: a cluster that was upgraded ahead of the catalog was
// called "behind" and offered "Downgrade".
func TestVersionIsNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"6.8.0", "6.7.1", true},
		{"6.7.1", "6.8.0", false},
		{"6.7.1", "6.7.1", false},
		{"v1.16.3", "1.16.2", true},
		{"3.0.0-beta.6", "3.0.0", false},
		{"3.0.0", "3.0.0-beta.6", true},
		{"abc", "6.7.1", false},
		{"99.0.0", "abc", false},
	}
	for _, tc := range cases {
		if got := versionIsNewer(tc.a, tc.b); got != tc.want {
			t.Errorf("versionIsNewer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// Walk 4: the cluster page said "Running 99.0.0 ... Error" with no reason
// anywhere. The reason names the version that could not be loaded and the
// one that is still running.
func TestVersionNotLoadedIssue(t *testing.T) {
	app := models.ArgocdApplication{
		SourceTargetRevision: "99.0.0",
		SyncedRevision:       "6.9.0",
		Conditions: []models.AppCondition{{
			Type:    "ComparisonError",
			Message: `chart "podinfo" version "99.0.0" not found in https://charts.example/repo?access_token=hunter2 repository`,
		}},
	}
	got := versionNotLoadedIssue(app, "99.0.0")
	want := "ArgoCD could not load version 99.0.0. Check that this version exists in the chart repository. Version 6.9.0 is still running."
	if got != want {
		t.Errorf("issue =\n  %q\nwant\n  %q", got, want)
	}
	if strings.Contains(got, "hunter2") || strings.Contains(got, "charts.example") {
		t.Errorf("ArgoCD's own text leaked into the sentence: %q", got)
	}

	app.SyncedRevision = ""
	if got, want := versionNotLoadedIssue(app, "99.0.0"), "ArgoCD could not load version 99.0.0. Check that this version exists in the chart repository. Nothing is running yet."; got != want {
		t.Errorf("issue =\n  %q\nwant\n  %q", got, want)
	}

	// No comparison error, or the declared version is what runs: no issue.
	if got := versionNotLoadedIssue(models.ArgocdApplication{SyncedRevision: "6.8.0"}, "6.9.0"); got != "" {
		t.Errorf("a rollout in progress is not an error: %q", got)
	}
	app.SyncedRevision = "99.0.0"
	if got := versionNotLoadedIssue(app, "99.0.0"); got != "" {
		t.Errorf("declared == running must not raise the issue: %q", got)
	}
}

func TestDeclaredVersionOf(t *testing.T) {
	if got := declaredVersionOf(models.AddonComparisonStatus{GitVersion: "1.0.0"}); got != "1.0.0" {
		t.Errorf("got %q", got)
	}
	if got := declaredVersionOf(models.AddonComparisonStatus{GitVersion: "1.0.0", EnvironmentVersion: "1.1.0"}); got != "1.1.0" {
		t.Errorf("got %q", got)
	}
	if got := declaredVersionOf(models.AddonComparisonStatus{GitVersion: "1.0.0", HasVersionOverride: true, CustomVersion: "1.2.0"}); got != "1.2.0" {
		t.Errorf("got %q", got)
	}
}
