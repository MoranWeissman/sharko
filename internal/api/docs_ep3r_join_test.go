package api

// docs_ep3r_join_test.go — pins for the docs lines fixed when the round R
// server, UI and docs branches were joined (episode 3 dry run).
//
//   - The take over --acknowledge flag takes ids split by commas as well as
//     repeated flags (cmd/sharko/takeover.go). The docs said "(repeatable)"
//     only.
//   - A cluster verified only by ArgoCD shows "Not needed" in the status
//     popover, not "Not run" (ui/src/components/ClusterStatusSummary.tsx).
//   - A removal that keeps the ArgoCD connection puts the
//     sharko.dev/remove-keep-pending-pr mark on it while the pull request
//     waits. The docs did not say so.
//   - While a pull request waits, the command line prints "PR opened,
//     waiting for merge: <link>" (cmd/sharko/waiting.go). It prints the link,
//     not the number, so the docs must say where the number for
//     `sharko pr wait` comes from. The CLI side of this pin, which checks the
//     printed line itself, is TestWaitingForMergeLine_PrintsTheLinkNotTheNumber
//     in cmd/sharko.

import (
	"strings"
	"testing"
)

func TestDocsEp3RoundRJoinPins(t *testing.T) {
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	commands := flat(readDocForKindPins(t, "docs/site/cli/commands.md"))
	vocab := flat(readDocForKindPins(t, "docs/site/user-guide/status-vocabulary.md"))
	reconciler := flat(readDocForKindPins(t, "docs/site/operator/cluster-reconciler.md"))
	clusters := flat(readDocForKindPins(t, "docs/site/user-guide/clusters.md"))
	const prNumber = "The command prints the pull request link, not its number. The number is the last part of the link: for a link that ends in `/pull/12`, run `sharko pr wait 12`."

	must := []struct{ name, doc, text string }{
		{"commands.md takeover --acknowledge row", commands,
			"| `--acknowledge <id>` | Finding id to acknowledge. Repeat the flag, or give several ids split by commas (`--acknowledge a,b`) |"},
		{"commands.md remove-cluster keep mark", commands,
			"When the removal keeps the ArgoCD connection, the connection gets a `sharko.dev/remove-keep-pending-pr` mark while the pull request waits, so nothing deletes it in the meantime."},
		{"status-vocabulary.md ArgoCD-only paragraph", vocab,
			"the popover line says **Not needed**, because ArgoCD already confirms it can reach the cluster and no test app had to be deployed."},
		{"status-vocabulary.md Not needed row", vocab,
			"| **Not needed** | Neutral | ArgoCD already confirms it can reach this cluster, so no deploy check was needed. |"},
		{"status-vocabulary.md Not run row", vocab,
			"| **Not run** | Neutral | No deploy check has run for this cluster. |"},
		{"status-vocabulary.md Not known row", vocab,
			"| **Not known** | Neutral | Sharko got a deploy check result this page does not know how to show."},
		{"cluster-reconciler.md keep mark", reconciler,
			"While the annotation is on the Secret, the reconciler never deletes the Secret and never lists it as left over, even after the cluster is gone from `managed-clusters.yaml`."},
		{"cluster-reconciler.md keep mark on merge", reconciler,
			"When the pull request is merged, Sharko takes off its label and this annotation together, and the Secret stays in ArgoCD. If the pull request is closed without merging, Sharko takes off only the annotation and changes nothing else."},
	}
	if n := strings.Count(commands, prNumber); n != 1 {
		t.Errorf("commands.md: want the pr wait number sentence once, found it %d times: %q", n, prNumber)
	}
	if n := strings.Count(clusters, prNumber); n != 2 {
		t.Errorf("clusters.md: want the pr wait number sentence twice (remove and adopt), found it %d times: %q", n, prNumber)
	}
	for _, m := range must {
		if !strings.Contains(m.doc, m.text) {
			t.Errorf("%s: missing the exact sentence %q", m.name, m.text)
		}
	}

	banned := []struct{ name, doc, text string }{
		{"commands.md old takeover --acknowledge row", commands,
			"| `--acknowledge <id>` | Finding id to acknowledge (repeatable) |"},
		{"commands.md old pr wait line", commands,
			"for example with `sharko pr wait <PR number>`"},
		{"clusters.md old pr wait line", clusters,
			"for example with `sharko pr wait <PR number>`"},
		{"status-vocabulary.md old Not run claim", vocab,
			"but the popover line says **Not run**, because no test app was deployed"},
	}
	for _, b := range banned {
		if strings.Contains(b.doc, b.text) {
			t.Errorf("%s: the old wrong sentence is back: %q", b.name, b.text)
		}
	}
}
