// connection_secrets_demo.go — deterministic connection-secret ("cluster
// connection secret") state seeding for a generated demo estate (S4/S5).
//
// Uses direct seeding (clusterreconciler.Reconciler.SeedReconcileRecordForDemo)
// rather than actually running the reconciler against the demo's fake
// Kubernetes clientset — see that method's doc comment for why: getting a
// real tick to land on a CHOSEN state needs the fake Secrets, the
// git-rendered desired addon labels, and the self-heal setting to all agree,
// which is a lot of moving parts to coordinate just to show four states a
// direct, honest record write can produce just as legitimately.
package demo

import (
	"sort"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
)

// connectionAgeOffsets is the "how long ago was this cluster last
// reconciled" spread — always relative to the "at" a caller passes in
// (server start, or a fresh Refresh click), never a fixed calendar date.
var connectionAgeOffsets = []time.Duration{
	2 * time.Minute,
	9 * time.Minute,
	18 * time.Minute,
	26 * time.Minute,
	31 * time.Minute,
	40 * time.Minute,
	55 * time.Minute,
	1*time.Hour + 15*time.Minute,
	1*time.Hour + 48*time.Minute,
	2*time.Hour + 30*time.Minute,
}

// repairHistoryOffsets spreads seeded repair audit entries (S5) across the
// last few days — deterministic, always relative to "now", shared by both
// the connection-secret and addon-values-secret repair seeding.
var repairHistoryOffsets = []time.Duration{
	18 * time.Hour,
	1*24*time.Hour + 6*time.Hour,
	2*24*time.Hour + 3*time.Hour,
	3*24*time.Hour + 14*time.Hour,
}

// demoClusterReconcileSeed is one cluster's chosen reconcile outcome — see
// buildDemoReconcileSeeds.
type demoClusterReconcileSeed struct {
	cluster string
	outcome clusterreconciler.ReconcileOutcome
	message string
	drift   *clusterreconciler.LabelDrift
}

// buildDemoReconcileSeeds works out, once and deterministically, which
// state every registered cluster in estate should show on the Managed
// Secrets page's cluster_connection_secrets table — a real mix instead of
// 50x unknown, lined up with the estate's own pinned exemplars:
//
//   - estate.SelfManagedClusterNames[0] (if any) — the ONE case
//     connectionSecretState can honestly call "missing": a self-managed
//     connection whose Secret the user hasn't created yet
//     (clusterreconciler.SelfManagedSecretNotCreatedMessage is the exact
//     message that state requires — see connectionSecretState in
//     internal/api/system_managed_secrets.go).
//   - estate.ForeignSecretClusterNames[0] (if any) — a same-name Secret
//     already exists but isn't Sharko's (Adopt territory); the reconciler
//     deliberately takes no position on it, which reads as "unknown".
//   - everything else cycles deterministically: most clusters succeed with
//     no drift (in sync), roughly 1 in 12 succeeds WITH label drift (out of
//     sync — self-heal caught up to a point but a difference remains), and
//     roughly 1 in 12 fails outright (out of sync via a different honest
//     reason).
func buildDemoReconcileSeeds(estate *GeneratedEstate) []demoClusterReconcileSeed {
	special := make(map[string]bool)
	seeds := make([]demoClusterReconcileSeed, 0, len(estate.Clusters))

	if len(estate.SelfManagedClusterNames) > 0 {
		name := estate.SelfManagedClusterNames[0]
		special[name] = true
		seeds = append(seeds, demoClusterReconcileSeed{
			cluster: name,
			outcome: clusterreconciler.OutcomeSkipped,
			message: clusterreconciler.SelfManagedSecretNotCreatedMessage,
		})
	}
	if len(estate.ForeignSecretClusterNames) > 0 {
		name := estate.ForeignSecretClusterNames[0]
		special[name] = true
		seeds = append(seeds, demoClusterReconcileSeed{
			cluster: name,
			outcome: clusterreconciler.OutcomeSkipped,
			message: "an unlabeled secret with this name already exists on this cluster — this looks like an existing installation, not something Sharko created. Register it as an adopted cluster instead of overwriting it.",
		})
	}

	clusters := make([]Cluster, len(estate.Clusters))
	copy(clusters, estate.Clusters)
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].Name < clusters[j].Name })

	i := 0
	for _, c := range clusters {
		if special[c.Name] {
			continue
		}
		switch i % 12 {
		case 0:
			seeds = append(seeds, demoClusterReconcileSeed{
				cluster: c.Name,
				outcome: clusterreconciler.OutcomeFailed,
				message: "could not update this cluster's ArgoCD connection secret — the last attempt to reach its Kubernetes API timed out.",
			})
		case 1:
			seeds = append(seeds, demoClusterReconcileSeed{
				cluster: c.Name,
				outcome: clusterreconciler.OutcomeSucceeded,
				drift:   &clusterreconciler.LabelDrift{Changed: []string{"an addon label"}},
			})
		default:
			seeds = append(seeds, demoClusterReconcileSeed{
				cluster: c.Name,
				outcome: clusterreconciler.OutcomeSucceeded,
			})
		}
		i++
	}

	return seeds
}

// applyDemoReconcileSeeds writes every seed into recon's in-memory record,
// stamped relative to "at" — called once at demo startup, and again (with a
// fresh "at") from the demo's reconcilerTrigger, so a Refresh click
// genuinely updates every row's last-checked time (S3). Fleet-wide on
// purpose — it mirrors the real POST /clusters/{name}/reconcile trigger,
// which has always nudged a full pass rather than one scoped cluster (see
// handleReconcileCluster's own doc comment in internal/api/clusters_reconcile.go).
func applyDemoReconcileSeeds(recon *clusterreconciler.Reconciler, seeds []demoClusterReconcileSeed, at time.Time) {
	for i, seed := range seeds {
		checkedAt := at.Add(-connectionAgeOffsets[i%len(connectionAgeOffsets)])
		recon.SeedReconcileRecordForDemo(seed.cluster, seed.outcome, seed.message, checkedAt, seed.drift)
	}
}

// seedDemoConnectionRepairHistory (S5) records a past repair audit entry
// for a deterministic subset of the clusters that actually succeeded a
// write (Outcome succeeded, no drift — a genuinely fixed cluster), using
// the exact resource shape and event names
// lastConnectionSecretRepair (internal/api/system_managed_secrets.go) joins
// on: Resource "cluster:<name>", Result "success", one of
// cluster_secret_create / cluster_secret_managed_self_heal /
// cluster_secret_user_label_sync. Spread over the last few days,
// deterministic — never invented per-request.
func seedDemoConnectionRepairHistory(auditLog *audit.Log, seeds []demoClusterReconcileSeed, now time.Time) {
	if auditLog == nil {
		return
	}
	events := []string{"cluster_secret_create", "cluster_secret_managed_self_heal", "cluster_secret_user_label_sync"}
	picked := 0
	for i, seed := range seeds {
		if seed.outcome != clusterreconciler.OutcomeSucceeded || seed.drift != nil {
			continue // only a cleanly in-sync cluster has an honest "it was fixed and stayed fixed" story
		}
		if i%6 != 0 {
			continue // a SUBSET, not every cluster
		}
		auditLog.Add(audit.Entry{
			Level:     "info",
			Event:     events[picked%len(events)],
			User:      "sharko",
			Action:    "reconcile",
			Resource:  "cluster:" + seed.cluster,
			Source:    "reconciler",
			Result:    "success",
			Timestamp: now.Add(-repairHistoryOffsets[picked%len(repairHistoryOffsets)]),
		})
		picked++
	}
}
