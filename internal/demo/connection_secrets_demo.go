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

	"github.com/MoranWeissman/sharko/internal/api"
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

// demoOlderBranchHeadSHA is a second, distinct, obviously-fake commit SHA
// (P2-C4/C6) — the "applied" revision for the one seeded row that shows the
// "git moved" drift blame: its last successful write was built from THIS
// commit, while the current pass compared against demoBranchHeadSHA
// (mock_git.go), so the two disagree exactly the way a real cluster looks
// after a newer commit changes the desired state.
const demoOlderBranchHeadSHA = "beefbeef0102030405060708090a0b0c0d0e0f1"

// demoClusterReconcileSeed is one cluster's chosen reconcile outcome — see
// buildDemoReconcileSeeds.
type demoClusterReconcileSeed struct {
	cluster string
	outcome clusterreconciler.ReconcileOutcome
	message string
	drift   *clusterreconciler.LabelDrift

	// comparedPath (P2-C1/C3) alternates between the v3 and v4
	// managed-clusters path per cluster so the page shows a genuine
	// self_heals spread (a v4 row always heals; a v3 row heals only when
	// the — off by default, and off in demo mode, since no settings store
	// is wired out-of-cluster — managed_cluster_self_heal setting is on).
	comparedPath string
	// appliedRevision (P2-C1/C6) is "" for a cluster whose secret has
	// never been successfully written (the two special cases below), or
	// one of the two fixed demo SHAs for everything else — see
	// buildDemoReconcileSeeds for which row gets which.
	appliedRevision string

	// ── The canonical connection answer (B5) ──────────────────────────────
	// Since the fleet row's state IS the canonical reconciliation answer,
	// the demo has to seed that too — demo clusters do not exist, so no
	// real comparison can ever run against them. These fields carry the
	// FACTS; the display words are derived by the server from them, through
	// the same functions the real path uses.
	managementMode    string
	syncState         string
	verificationScope string
	approvalRequired  bool
	liveSecretMissing bool
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
//     no drift (in sync), roughly 1 in 12 succeeds WITH label drift where
//     git moved since the last write (out of sync, drift blame "git"),
//     roughly 1 in 12 succeeds WITH label drift where git has NOT moved
//     (out of sync, drift blame "cluster" — P2-C6), and roughly 1 in 12
//     fails outright (unknown via a different honest reason).
//
// comparedPath (P2-C1/C3) alternates v4/v3 per cluster (demoComparedPath)
// so the self_heals column shows a real spread: a v4 row always heals
// itself; a v3 row only heals when managed_cluster_self_heal is on, and no
// settings store is wired in demo mode (out-of-cluster), so every v3 row
// reads self_heals=false — the honest, grounded default the same code path
// falls back to in production when it doesn't know.
func buildDemoReconcileSeeds(estate *GeneratedEstate) []demoClusterReconcileSeed {
	special := make(map[string]bool)
	seeds := make([]demoClusterReconcileSeed, 0, len(estate.Clusters))

	if len(estate.SelfManagedClusterNames) > 0 {
		name := estate.SelfManagedClusterNames[0]
		special[name] = true
		seeds = append(seeds, demoClusterReconcileSeed{
			cluster:      name,
			outcome:      clusterreconciler.OutcomeSkipped,
			message:      clusterreconciler.SelfManagedSecretNotCreatedMessage,
			comparedPath: demoComparedPath(0),
			// Never successfully written — the secret doesn't exist yet.
			managementMode:    api.DemoManagementModeSelfManaged,
			syncState:         api.DemoSyncStateOutOfSync,
			verificationScope: api.DemoVerificationScopeNone,
			liveSecretMissing: true,
		})
	}
	if len(estate.ForeignSecretClusterNames) > 0 {
		name := estate.ForeignSecretClusterNames[0]
		special[name] = true
		seeds = append(seeds, demoClusterReconcileSeed{
			cluster:      name,
			outcome:      clusterreconciler.OutcomeSkipped,
			message:      "an unlabeled secret with this name already exists on this cluster — this looks like an existing installation, not something Sharko created. Register it as an adopted cluster instead of overwriting it.",
			comparedPath: demoComparedPath(1),
			// Never successfully written — it isn't Sharko's secret.
			managementMode:    api.DemoManagementModeForeignOwned,
			syncState:         api.DemoSyncStateBlocked,
			verificationScope: api.DemoVerificationScopeNone,
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
		path := demoComparedPath(i)
		switch i % 12 {
		case 0:
			seeds = append(seeds, demoClusterReconcileSeed{
				cluster:      c.Name,
				outcome:      clusterreconciler.OutcomeFailed,
				message:      "could not update this cluster's ArgoCD connection secret — the last attempt to reach its Kubernetes API timed out.",
				comparedPath: path,
				// The pass's git read still succeeded (only the write
				// failed) but this cluster has never had a successful
				// write on this server instance yet.
				managementMode:    api.DemoManagementModeSharkoManaged,
				syncState:         api.DemoSyncStateUnknown,
				verificationScope: api.DemoVerificationScopeNone,
			})
		case 1:
			// Drift blame "git" (P2-C6): the last successful write was
			// built from an OLDER commit than the one this pass just
			// compared against — a newer commit changed the desired state
			// since then.
			seeds = append(seeds, demoClusterReconcileSeed{
				cluster:         c.Name,
				outcome:         clusterreconciler.OutcomeSucceeded,
				drift:           &clusterreconciler.LabelDrift{Changed: []string{"an addon label"}},
				comparedPath:    path,
				appliedRevision: demoOlderBranchHeadSHA,
				// A newer commit changed connection details, so this one is
				// approval-gated: Sharko never rewrites connection details
				// or credential material by itself.
				managementMode:    api.DemoManagementModeSharkoManaged,
				syncState:         api.DemoSyncStateOutOfSync,
				verificationScope: api.DemoVerificationScopeFull,
				approvalRequired:  true,
			})
		case 2:
			// Drift blame "cluster" (P2-C6): the last successful write was
			// built from the SAME commit this pass just compared against
			// — git hasn't moved, so something changed the live secret
			// outside git.
			seeds = append(seeds, demoClusterReconcileSeed{
				cluster:         c.Name,
				outcome:         clusterreconciler.OutcomeSucceeded,
				drift:           &clusterreconciler.LabelDrift{Changed: []string{"an addon label"}},
				comparedPath:    path,
				appliedRevision: demoBranchHeadSHA,
				// Addon labels only — nothing here needs an admin.
				managementMode:    api.DemoManagementModeSharkoManaged,
				syncState:         api.DemoSyncStateOutOfSync,
				verificationScope: api.DemoVerificationScopeFull,
			})
		default:
			seeds = append(seeds, demoClusterReconcileSeed{
				cluster:         c.Name,
				outcome:         clusterreconciler.OutcomeSucceeded,
				comparedPath:    path,
				appliedRevision: demoBranchHeadSHA,
				// Every field Sharko owns was compared and matched — the one
				// combination that may honestly read "Connection synced".
				managementMode:    api.DemoManagementModeSharkoManaged,
				syncState:         api.DemoSyncStateSynced,
				verificationScope: api.DemoVerificationScopeFull,
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
		// P2-C1/C4: every pass — real or seeded — reads git at the SAME
		// branch head, so every row's ComparedRevision is demoBranchHeadSHA.
		// ComparedPath and AppliedRevision are per-seed (see
		// buildDemoReconcileSeeds's doc comment).
		recon.SeedReconcileRevisionForDemo(seed.cluster, demoBranchHeadSHA, seed.comparedPath, seed.appliedRevision)
	}
}

// applyDemoConnectionChecks seeds each cluster's CANONICAL connection answer
// — the fact the fleet row's state, headline and verification indicator are
// now derived from (B5). Called wherever applyDemoReconcileSeeds is, with the
// same "at", so a Refresh click re-stamps when Sharko "looked" without moving
// any row's state, exactly as a real read-only check does.
//
// The demo supplies facts only. The server derives the display words from
// them through the same functions the real path uses, and applies the same
// fail-closed invariant — so no seed can produce a green row that the facts
// do not support.
func applyDemoConnectionChecks(srv *api.Server, seeds []demoClusterReconcileSeed, at time.Time) {
	checks := make([]api.DemoConnectionCheck, 0, len(seeds))
	for i, seed := range seeds {
		if seed.managementMode == "" {
			continue
		}
		checks = append(checks, api.DemoConnectionCheck{
			Cluster:           seed.cluster,
			ManagementMode:    seed.managementMode,
			SyncState:         seed.syncState,
			VerificationScope: seed.verificationScope,
			ApprovalRequired:  seed.approvalRequired,
			LiveSecretMissing: seed.liveSecretMissing,
			CheckedAt:         at.Add(-connectionAgeOffsets[i%len(connectionAgeOffsets)]),
		})
	}
	srv.SeedConnectionChecksForDemo(checks)
}

// demoComparedPath (P2-C1/C3) alternates the demo's managed-clusters file
// path per cluster index — even indices read as a v4 repo
// (V4ManagedClustersPath, always self-heals), odd indices as a v3 repo
// (DefaultManagedClustersPath, self-heals only when the setting is on —
// off in demo mode). A real repo is one layout or the other for its whole
// estate; this alternation exists only so the demo's self_heals column
// shows both true and false instead of every row reading the same way.
func demoComparedPath(i int) string {
	if i%2 == 0 {
		return clusterreconciler.V4ManagedClustersPath
	}
	return clusterreconciler.DefaultManagedClustersPath
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
