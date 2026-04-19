package api

import (
	"testing"
)

// TestInferCluster covers the title/branch heuristics used to label merged
// PRs in the dashboard's Merged tab. These come up only for display; the
// underlying PR lookup is by ID so a wrong inference here is cosmetic.
func TestInferCluster(t *testing.T) {
	cases := []struct {
		name   string
		title  string
		branch string
		want   string
	}{
		{
			name:   "extracts cluster from 'on cluster <name>' suffix",
			title:  "Update prometheus overrides on cluster prod-eu",
			branch: "sharko/values-prometheus-prod-eu",
			want:   "prod-eu",
		},
		{
			name:   "extracts cluster from middle of title",
			title:  "Register cluster staging-1 with default addons",
			branch: "sharko/register-staging-1",
			want:   "staging-1",
		},
		{
			name:   "falls back to last branch segment",
			title:  "Some unrelated title",
			branch: "sharko/register-prod",
			want:   "prod",
		},
		{
			name:   "returns empty when nothing matches",
			title:  "Bump dependency",
			branch: "feature/bump-deps",
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := inferCluster(tc.title, tc.branch)
			if got != tc.want {
				t.Errorf("inferCluster(%q, %q) = %q, want %q", tc.title, tc.branch, got, tc.want)
			}
		})
	}
}

// TestInferAddon documents the second-word heuristic for addon names. We
// deliberately skip leading verbs that don't carry an addon (Register,
// Deregister, Adopt, Init) so cluster lifecycle PRs don't end up tagged
// with a stray "cluster" addon name.
func TestInferAddon(t *testing.T) {
	cases := []struct {
		title string
		want  string
	}{
		{title: "Update prometheus overrides on cluster prod", want: "prometheus"},
		{title: "Upgrade external-dns to 1.14.0", want: "external-dns"},
		{title: "Register cluster prod", want: ""},
		{title: "Adopt clusters batch-1", want: ""},
		{title: "single-word", want: ""},
	}
	for _, tc := range cases {
		got := inferAddon(tc.title, "")
		if got != tc.want {
			t.Errorf("inferAddon(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}

// TestInferOperation documents the verb-extraction used to label the PR
// category in the dashboard. Not part of any contract — purely cosmetic.
func TestInferOperation(t *testing.T) {
	if got := inferOperation("Upgrade external-dns to 1.14.0", ""); got != "upgrade" {
		t.Errorf("inferOperation upgrade: got %q", got)
	}
	if got := inferOperation("", ""); got != "" {
		t.Errorf("inferOperation empty: got %q", got)
	}
}
