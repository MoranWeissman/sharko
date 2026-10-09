// showcase.go — pins one exemplar of every UI-visible cluster/addon state
// onto specific, deterministic clusters and addons in a generated estate,
// so the maintainer's walk can find every state on purpose instead of
// hoping the random fractions produced it (maintainer scope addition,
// folded into S1).
//
// State vocabularies enumerated from the code (not guessed) — this is the
// full list applyStateCoverage pins at least one exemplar of:
//
// Cluster states:
//   - ConnectionStatus (internal/service/cluster.go): "Successful" (the
//     random-fraction default), "Failed" (disconnected — random fraction
//     PLUS 4 dedicated connectivity-zoo clusters below), "missing" (a
//     git-defined cluster ArgoCD has no matching cluster Secret for —
//     line ~224), "not_in_git" (an ArgoCD-only cluster with no git entry
//     — line ~240, via GeneratedEstate.ArgoOnlyClusters).
//   - ConnectivityStatus (internal/api/connectivity_status.go
//     computeConnectivityVerdictAt): "verified_argocd" (default for any
//     ConnectionStatus=="Successful" cluster), "verified_check",
//     "check_pending", "check_failed", "" (disconnected with no
//     connectivity-check app at all).
//   - DerivedHealthStatus (same file, computeDerivedHealth): "healthy",
//     "reachable", "unknown".
//   - SharkoStatus, the persisted 5-state model (internal/observations/
//     types.go): Unknown, Connected, Verified, Operational, Unreachable —
//     via GeneratedEstate.ObservationSeeds feeding a fake observations.Store.
//   - ConnectionManagedBy (models.ManagedClusterEntry): "" (Sharko-managed,
//     the default) vs "user" (self-managed connection).
//   - AlreadyManagedBySharko (internal/api/clusters_reconcile.go
//     applyManagedSecretFields): true (owned — the default once the fake
//     cluster reconciler is wired) vs false (a REGISTERED cluster whose
//     fake ArgoCD-secret is deliberately unlabeled — "takeover-eligible,
//     git-defined but not owned").
//   - Orphan vs plain not_in_git (internal/api/clusters_orphans.go
//     resolveOrphanRegistrations): an ArgoOnlyCluster with a LABELED fake
//     secret surfaces as an orphan; one with an UNLABELED secret surfaces
//     as plain not_in_git.
//   - A cluster with zero addons (the connectivity-check-only case).
//   - Unregistered/discovered candidate + pending registration: already
//     produced by GenerateEstate's UnregisteredClusters + GitPRs — no
//     extra pinning needed here.
//
// Addon application states (per ArgoCD Application):
//   - Health (the UI's Observability.tsx color map): Healthy, Degraded,
//     Progressing, Missing, Suspended, Unknown.
//   - Sync: Synced, OutOfSync.
//   - Behind-catalog vs on-version — the random behindCatalogFraction
//     PLUS one deterministic exemplar.
//   - PR-flow "ghost pending" states — an open addon-enable PR for an
//     addon not yet on a cluster, an open addon-disable PR for one that
//     is, an open addon-upgrade PR for one that is — pinned in
//     generateTrackedPRs (see that function's doc comment), not here.
package demo

import (
	"fmt"
	"math/rand"
	"time"
)

// clusterCycler returns a stateful "next index" function that walks 0..n-1
// once each before repeating — so a handful of showcase.go pinning calls on
// a small n (an unusually tiny custom --demo-clusters config) degrade to
// reusing clusters rather than panicking on an out-of-range index.
func clusterCycler(n int) func() int {
	i := 0
	return func() int {
		v := i % n
		i++
		return v
	}
}

// clusterIndexByName finds estate.Clusters' index for name, or -1.
func clusterIndexByName(estate *GeneratedEstate, name string) int {
	for i := range estate.Clusters {
		if estate.Clusters[i].Name == name {
			return i
		}
	}
	return -1
}

// setCell overrides one (cluster, addon) deployment cell — used to pin
// exemplar app states. Creates the addon-membership entry and the
// deployment-detail entry if either is missing.
func setCell(estate *GeneratedEstate, rng *rand.Rand, now time.Time, clusterName, addonName, version, health, sync string) {
	if i := clusterIndexByName(estate, clusterName); i >= 0 {
		if estate.Clusters[i].Addons == nil {
			estate.Clusters[i].Addons = map[string]string{}
		}
		estate.Clusters[i].Addons[addonName] = version
	}
	cells, ok := estate.Deployments[clusterName]
	if !ok || cells == nil {
		cells = map[string]deploymentInfo{}
		estate.Deployments[clusterName] = cells
	}
	cells[addonName] = deploymentInfo{Health: health, Sync: sync, History: generateHistory(rng, now)}
}

// forceAllUnhealthy overrides every existing cell on clusterName to
// Degraded/OutOfSync, guaranteeing clusterHasHealthyAddon returns false —
// the precondition every non-Operational SharkoStatus/connectivity showcase
// below needs (a genuinely healthy addon would otherwise upstage them via
// computeDerivedHealth's "healthy" priority-1 rule).
func forceAllUnhealthy(estate *GeneratedEstate, clusterName string) {
	cells := estate.Deployments[clusterName]
	for addonName, cell := range cells {
		cell.Health = "Degraded"
		cell.Sync = "OutOfSync"
		cells[addonName] = cell
	}
}

// generateArgoOnlyClusterNames produces names for GeneratedEstate.
// ArgoOnlyClusters, using a "legacy"/"shadow" prefix pool distinct from
// both the registered (prod/staging/dev) and unregistered-candidate
// (dr/sandbox) pools, so an ArgoCD-only cluster never collides in name
// with a cluster that IS registered.
func generateArgoOnlyClusterNames(n int, rng *rand.Rand) []genClusterName {
	type combo struct{ prefix, regionTag string }
	var combos []combo
	for _, p := range []string{"legacy", "shadow"} {
		for _, r := range []string{"eu", "us", "asia"} {
			combos = append(combos, combo{p, r})
		}
	}
	rng.Shuffle(len(combos), func(i, j int) { combos[i], combos[j] = combos[j], combos[i] })

	counts := make(map[combo]int, len(combos))
	out := make([]genClusterName, 0, n)
	for i := 0; len(out) < n; i++ {
		c := combos[i%len(combos)]
		counts[c]++
		out = append(out, genClusterName{
			Name:   fmt.Sprintf("%s-%s-%d", c.prefix, c.regionTag, counts[c]),
			Env:    "unmanaged",
			Region: shortRegionToFull[c.regionTag],
		})
	}
	return out
}

// applyStateCoverage pins one exemplar of every enumerated state (see the
// package doc comment above) onto specific clusters/addons in estate,
// mutating it in place. Best-effort against small estates: clusterCycler
// wraps around rather than panicking, so a tiny custom ScaleConfig degrades
// to reusing a handful of clusters across showcases instead of failing.
func applyStateCoverage(estate *GeneratedEstate, rng *rand.Rand, now time.Time) {
	n := len(estate.Clusters)
	m := len(estate.Addons)
	if n == 0 || m == 0 {
		return
	}

	next := clusterCycler(n)
	name := func(i int) string { return estate.Clusters[i].Name }

	// 1. Zero-addon cluster (connectivity-check-only case) + SharkoStatus
	// "Unknown" (a seeded-but-never-tested observation — Stage "" falls
	// through ComputeStatus's default case).
	iZero := next()
	estate.Clusters[iZero].Addons = map[string]string{}
	estate.Deployments[name(iZero)] = map[string]deploymentInfo{}
	estate.ObservationSeeds = append(estate.ObservationSeeds,
		ObservationSeed{ClusterName: name(iZero), Stage: "", Success: true})

	// 2. SharkoStatus "Connected" (stage1 test succeeded, no healthy addon).
	iConnected := next()
	forceAllUnhealthy(estate, name(iConnected))
	estate.ObservationSeeds = append(estate.ObservationSeeds,
		ObservationSeed{ClusterName: name(iConnected), Stage: "stage1", Success: true})

	// 3. SharkoStatus "Verified" (stage2 test succeeded, no healthy addon).
	iVerified := next()
	forceAllUnhealthy(estate, name(iVerified))
	estate.ObservationSeeds = append(estate.ObservationSeeds,
		ObservationSeed{ClusterName: name(iVerified), Stage: "stage2", Success: true})

	// 4. SharkoStatus "Unreachable" (last test failed, no healthy addon).
	iUnreachable := next()
	forceAllUnhealthy(estate, name(iUnreachable))
	estate.ObservationSeeds = append(estate.ObservationSeeds,
		ObservationSeed{ClusterName: name(iUnreachable), Stage: "stage1", Success: false})

	// 5. SharkoStatus "Operational" (a genuinely healthy addon wins
	// regardless of test outcome/stage).
	iOperational := next()
	setCell(estate, rng, now, name(iOperational), estate.Addons[0].Name, estate.Addons[0].CatalogVersion, "Healthy", "Synced")
	estate.ObservationSeeds = append(estate.ObservationSeeds,
		ObservationSeed{ClusterName: name(iOperational), Stage: "stage1", Success: true})

	// 6. Self-managed connection (connectionManagedBy: user).
	iSelfManaged := next()
	estate.SelfManagedClusterNames = append(estate.SelfManagedClusterNames, name(iSelfManaged))

	// 7. "missing" ArgoCD secret — registered in git, ArgoCD has no
	// matching cluster entry at all.
	iMissing := next()
	estate.MissingClusterNames = append(estate.MissingClusterNames, name(iMissing))

	// 8. Takeover-eligible — registered AND known to ArgoCD, but the fake
	// ArgoCD-connection Secret is deliberately unlabeled (a foreign/
	// pre-existing Secret Sharko does not own yet).
	iTakeover := next()
	estate.ForeignSecretClusterNames = append(estate.ForeignSecretClusterNames, name(iTakeover))

	// 9-12. Connectivity-status zoo: 4 dedicated disconnected clusters,
	// each exercising a distinct computeConnectivityVerdictAt outcome.
	iVerifiedCheck := next()
	estate.Clusters[iVerifiedCheck].ConnStatus = "Failed"
	forceAllUnhealthy(estate, name(iVerifiedCheck))
	estate.ConnectivityCheckApps = append(estate.ConnectivityCheckApps, ConnectivityCheckApp{
		ClusterName: name(iVerifiedCheck), Health: "Healthy", Sync: "Synced",
		CreatedAt: now.Add(-48 * time.Hour).UTC().Format(time.RFC3339),
	})

	iCheckPending := next()
	estate.Clusters[iCheckPending].ConnStatus = "Failed"
	forceAllUnhealthy(estate, name(iCheckPending))
	estate.ConnectivityCheckApps = append(estate.ConnectivityCheckApps, ConnectivityCheckApp{
		ClusterName: name(iCheckPending), Health: "Progressing", Sync: "OutOfSync",
		CreatedAt: now.Add(-2 * time.Minute).UTC().Format(time.RFC3339),
	})

	iCheckFailed := next()
	estate.Clusters[iCheckFailed].ConnStatus = "Failed"
	forceAllUnhealthy(estate, name(iCheckFailed))
	estate.ConnectivityCheckApps = append(estate.ConnectivityCheckApps, ConnectivityCheckApp{
		ClusterName: name(iCheckFailed), Health: "Degraded", Sync: "OutOfSync",
		CreatedAt: now.Add(-48 * time.Hour).UTC().Format(time.RFC3339),
	})

	iPlainDisconnected := next()
	estate.Clusters[iPlainDisconnected].ConnStatus = "Failed"
	forceAllUnhealthy(estate, name(iPlainDisconnected))
	// Deliberately no connectivity-check app here — this is the
	// ConnectivityStatus=="" / DerivedHealthStatus=="unknown" exemplar.

	// 13. Deterministic behind-catalog exemplar (on top of the
	// probabilistic behindCatalogFraction already applied per-cell).
	iBehind := next()
	behindAddon := estate.Addons[0]
	setCell(estate, rng, now, name(iBehind), behindAddon.Name, olderVersion(behindAddon.CatalogVersion, rng), "Healthy", "OutOfSync")

	// 14-16. Rare ArgoCD health-value zoo: Missing, Suspended, Unknown —
	// real ArgoCD health values the UI (Observability.tsx) renders with
	// their own distinct color, that the random Healthy/Degraded/
	// Progressing split above never produces.
	iRareHealth := next()
	rare := []struct{ health, sync string }{
		{"Missing", "OutOfSync"},
		{"Suspended", "Synced"},
		{"Unknown", "OutOfSync"},
	}
	for k, r := range rare {
		a := estate.Addons[k%m]
		setCell(estate, rng, now, name(iRareHealth), a.Name, a.CatalogVersion, r.health, r.sync)
	}

	// 17-18. Orphan vs plain not_in_git — two ArgoCD-only clusters (no git
	// entry at all), distinguished purely by whether their fake
	// ArgoCD-connection Secret carries the sharko ownership label.
	argoOnlyNames := generateArgoOnlyClusterNames(2, rng)
	for i, n := range argoOnlyNames {
		c := Cluster{
			Name:       n.Name,
			Server:     "https://k8s." + n.Name + ".demo.internal",
			Region:     n.Region,
			Env:        n.Env,
			K8sVersion: generatedK8sVersions[rng.Intn(len(generatedK8sVersions))],
			ConnStatus: "Successful",
		}
		estate.ArgoOnlyClusters = append(estate.ArgoOnlyClusters, ArgoOnlyCluster{
			Cluster:     c,
			SharkoOwned: i == 0, // first = orphan (labeled), second = plain not_in_git (unlabeled)
		})
	}
}
