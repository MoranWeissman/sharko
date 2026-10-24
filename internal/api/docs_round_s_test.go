package api

// docs_round_s_test.go — pins for the docs lines fixed in EP3 dry run round
// S (S23 and S25): the user guide no longer talks about a per-cluster
// "values file" (a v4 repo has none), the deploy check line lists "Not
// needed", drop-legacy-labels takes "a,b", `sharko pr wait` works after a
// fast merge, and the adopt mark failure has its new plain sentence.

import (
	"strings"
	"testing"
)

func TestDocsEp3RoundSPins(t *testing.T) {
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	guide := flat(readDocForKindPins(t, "docs/user-guide.md"))
	commands := flat(readDocForKindPins(t, "docs/site/cli/commands.md"))
	clusters := flat(readDocForKindPins(t, "docs/site/user-guide/clusters.md"))
	vocab := flat(readDocForKindPins(t, "docs/site/user-guide/status-vocabulary.md"))
	adoptFailed := flat(readDocForKindPins(t, "docs/site/operator/adopt-cluster-entry-write-failed.md"))
	const fastMerge = "If the pull request already merged, `sharko pr wait` says so at once and exits 0."

	must := []struct{ name, doc, text string }{
		{"user-guide register", guide, "registers the cluster in ArgoCD, adds it to `managed-clusters.yaml`, and commits to Git as a pull request."},
		{"user-guide remove all", guide, "deletes the cluster's files (`cluster-addons/<cluster>.yaml` and `values/clusters/<cluster>/`) via PR"},
		{"user-guide remove none", guide, "Only removes the `managed-clusters.yaml` entry. The cluster's other files and its ArgoCD secret are kept."},
		{"user-guide disable", guide, "This opens a pull request that sets the addon to `enabled: false` in `cluster-addons/prod-eu.yaml`."},
		{"user-guide disable endpoint", guide, "-X DELETE https://sharko.your-cluster.com/api/v1/v4/clusters/prod-eu/addons/monitoring"},
		{"user-guide per-cluster upgrade", guide, "Changes the addon's version only in that cluster's `cluster-addons/<cluster>.yaml`. Other clusters keep their own version."},
		{"user-guide pr wait fast merge", guide, "If the pull request already merged (for example with auto-merge), `sharko pr wait` says so at once and exits 0."},
		{"user-guide already open", guide, "If a pull request for the cluster is already open, Sharko returns that one and does not open a second one."},
		{"commands.md drop-legacy-labels --acknowledge", commands,
			"| `--acknowledge <id>` | Warning id to acknowledge. Repeat the flag, or give several ids split by commas (`--acknowledge a,b`) |"},
		{"status-vocabulary deploy check line", vocab,
			"**Not needed** (\"ArgoCD already confirms it can reach this cluster, so no deploy check was needed.\"), or **Not run**"},
		{"status-vocabulary information line", vocab, "A line that says **Not needed** or **Not run** is only shown for information."},
		{"adopt failed page mark row", adoptFailed,
			"| `partial` | `The adoption pull request is merged, but Sharko could not mark the cluster's ArgoCD connection as adopted. ...` | Almost done: only the last mark on the Secret is missing. Adopt again to finish |"},
	}
	for _, m := range must {
		if !strings.Contains(m.doc, m.text) {
			t.Errorf("%s: missing the exact sentence %q", m.name, m.text)
		}
	}
	if n := strings.Count(commands, fastMerge); n != 1 {
		t.Errorf("commands.md: want the fast merge sentence once, found it %d times", n)
	}
	if n := strings.Count(clusters, fastMerge); n != 2 {
		t.Errorf("clusters.md: want the fast merge sentence twice (remove and adopt), found it %d times", n)
	}

	banned := []struct{ name, doc, text string }{
		{"user-guide old register", guide, "creates a values file"},
		{"user-guide old remove", guide, "deletes values file via PR"},
		{"user-guide old remove none", guide, "Values file and ArgoCD secret are kept."},
		{"user-guide old disable", guide, "Updates values file"},
		{"user-guide old disable none", guide, "Updates the values file only."},
		{"user-guide old disable endpoint", guide, "/api/v1/clusters/prod-eu/addons/monitoring"},
		{"user-guide old per-cluster upgrade", guide, "cluster's values file"},
		{"user-guide old idempotent", guide, "(idempotent retry)"},
		{"commands.md old drop-legacy-labels row", commands, "| `--acknowledge <id>` | Warning id to acknowledge (repeatable) |"},
		{"adopt failed page old row", adoptFailed, "Adoption PR merged but failed to set adopted annotation on ArgoCD secret`"},
	}
	for _, b := range banned {
		if strings.Contains(b.doc, b.text) {
			t.Errorf("%s: the old wrong sentence is back: %q", b.name, b.text)
		}
	}
}
