package demo

import (
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/observations"
	"github.com/MoranWeissman/sharko/internal/prtracker"
	"github.com/MoranWeissman/sharko/internal/verify"
)

// TestBigPresetStateCoverage is the maintainer's explicit ask: for the big
// preset, every enumerated cluster state and app state appears at least
// once. This asserts the GENERATOR's output — the inputs that feed the
// real derivation logic in internal/api/connectivity_status.go,
// internal/observations, and internal/api/clusters_orphans.go — rather
// than re-running that (unexported, cross-package) derivation itself. See
// showcase.go's package doc comment for the full enumeration this mirrors.
func TestBigPresetStateCoverage(t *testing.T) {
	estate, err := GenerateEstate(BigScaleConfig)
	if err != nil {
		t.Fatalf("GenerateEstate(BigScaleConfig): %v", err)
	}

	// --- Cluster-level ConnectionStatus values ---
	connStatuses := map[string]bool{}
	for _, c := range estate.Clusters {
		connStatuses[c.ConnStatus] = true
	}
	for _, want := range []string{"Successful", "Failed"} {
		if !connStatuses[want] {
			t.Errorf("no registered cluster has ConnStatus %q", want)
		}
	}

	if len(estate.MissingClusterNames) == 0 {
		t.Error("no cluster designated for the \"missing ArgoCD secret\" state")
	}
	if len(estate.ArgoOnlyClusters) < 2 {
		t.Fatalf("ArgoOnlyClusters = %d, want >= 2 (one orphan-eligible, one plain not_in_git)", len(estate.ArgoOnlyClusters))
	}
	var sawOrphanEligible, sawPlainNotInGit bool
	for _, ao := range estate.ArgoOnlyClusters {
		if ao.SharkoOwned {
			sawOrphanEligible = true
		} else {
			sawPlainNotInGit = true
		}
	}
	if !sawOrphanEligible {
		t.Error("no ArgoOnlyCluster with a labeled (sharko-owned) secret — orphan-registration bucket unreachable")
	}
	if !sawPlainNotInGit {
		t.Error("no ArgoOnlyCluster with an unlabeled secret — plain not_in_git bucket unreachable")
	}

	if len(estate.ForeignSecretClusterNames) == 0 {
		t.Error("no registered cluster with an unlabeled (foreign) secret — takeover-eligible state unreachable")
	}
	for _, name := range estate.ForeignSecretClusterNames {
		for _, missing := range estate.MissingClusterNames {
			if name == missing {
				t.Errorf("cluster %s is in both ForeignSecretClusterNames and MissingClusterNames — mutually exclusive states", name)
			}
		}
	}

	if len(estate.SelfManagedClusterNames) == 0 {
		t.Error("no cluster designated connectionManagedBy: user — self-managed-connection state unreachable")
	}

	// --- Zero-addon cluster (connectivity-check-only case) ---
	var sawZeroAddon bool
	for _, c := range estate.Clusters {
		if len(c.Addons) == 0 {
			sawZeroAddon = true
			break
		}
	}
	if !sawZeroAddon {
		t.Error("no registered cluster has zero addons")
	}

	// --- ConnectivityStatus verdict shapes (verified_check / check_pending / check_failed) ---
	var sawVerifiedCheckShape, sawCheckPendingShape, sawCheckFailedShape bool
	now := time.Now()
	for _, app := range estate.ConnectivityCheckApps {
		switch {
		case app.Health == "Healthy" && app.Sync == "Synced":
			sawVerifiedCheckShape = true
		case app.Health == "Degraded":
			sawCheckFailedShape = true
		default:
			// check_pending requires a recent CreatedAt (< the 10-minute
			// escalation window) and no honest-failure signal.
			created, err := time.Parse(time.RFC3339, app.CreatedAt)
			if err == nil && now.Sub(created) < 10*time.Minute && app.Health != "Degraded" {
				sawCheckPendingShape = true
			}
		}
	}
	if !sawVerifiedCheckShape {
		t.Error("no connectivity-check app shaped for the verified_check verdict (Healthy+Synced)")
	}
	if !sawCheckPendingShape {
		t.Error("no connectivity-check app shaped for the check_pending verdict (recent, not yet healthy, no failure signal)")
	}
	if !sawCheckFailedShape {
		t.Error("no connectivity-check app shaped for the check_failed verdict (Degraded)")
	}

	// A disconnected cluster with NO connectivity-check app at all covers
	// the ConnectivityStatus=="" / DerivedHealthStatus=="unknown" case.
	checkedClusters := map[string]bool{}
	for _, app := range estate.ConnectivityCheckApps {
		checkedClusters[app.ClusterName] = true
	}
	var sawPlainDisconnected bool
	for _, c := range estate.Clusters {
		if c.ConnStatus == "Failed" && !checkedClusters[c.Name] {
			sawPlainDisconnected = true
			break
		}
	}
	if !sawPlainDisconnected {
		t.Error("no disconnected cluster without a connectivity-check app — ConnectivityStatus=='' case unreachable")
	}

	// --- SharkoStatus 5-state model, via the real ComputeStatus function ---
	seenStatus := map[observations.ClusterStatus]bool{}
	healthyAddonCluster := map[string]bool{}
	for clusterName, cells := range estate.Deployments {
		for _, cell := range cells {
			if cell.Health == "Healthy" && cell.Sync == "Synced" {
				healthyAddonCluster[clusterName] = true
			}
		}
	}
	for _, seed := range estate.ObservationSeeds {
		obs := &observations.Observation{LastTestStage: seed.Stage}
		result := verify.Result{Success: seed.Success}
		outcome := "success"
		if !seed.Success {
			outcome = "failure"
		}
		obs.LastTestOutcome = outcome
		_ = result
		status := observations.ComputeStatus(obs, healthyAddonCluster[seed.ClusterName]).Status
		seenStatus[status] = true
	}
	for _, want := range []observations.ClusterStatus{
		observations.StatusUnknown,
		observations.StatusConnected,
		observations.StatusVerified,
		observations.StatusOperational,
		observations.StatusUnreachable,
	} {
		if !seenStatus[want] {
			t.Errorf("no ObservationSeed produces SharkoStatus %q", want)
		}
	}

	// --- Addon application health/sync coverage ---
	seenHealth := map[string]bool{}
	seenSync := map[string]bool{}
	var sawBehindCatalog bool
	catalogVersion := make(map[string]string, len(estate.Addons))
	for _, a := range estate.Addons {
		catalogVersion[a.Name] = a.CatalogVersion
	}
	for _, c := range estate.Clusters {
		for addonName, version := range c.Addons {
			if version != "" && version != catalogVersion[addonName] {
				sawBehindCatalog = true
			}
		}
	}
	for _, cells := range estate.Deployments {
		for _, cell := range cells {
			seenHealth[cell.Health] = true
			seenSync[cell.Sync] = true
		}
	}
	for _, want := range []string{"Healthy", "Degraded", "Progressing", "Missing", "Suspended", "Unknown"} {
		if !seenHealth[want] {
			t.Errorf("no addon app cell has health %q", want)
		}
	}
	for _, want := range []string{"Synced", "OutOfSync"} {
		if !seenSync[want] {
			t.Errorf("no addon app cell has sync %q", want)
		}
	}
	if !sawBehindCatalog {
		t.Error("no addon cell is behind the catalog version")
	}

	// --- PR-flow ghost states: open enable/disable/upgrade, each with
	// membership-honest semantics ---
	seenOpenOp := map[string]bool{}
	for _, pr := range estate.TrackedPRs {
		if pr.LastStatus != "open" {
			continue
		}
		seenOpenOp[pr.Operation] = true

		var cluster *Cluster
		for i := range estate.Clusters {
			if estate.Clusters[i].Name == pr.Cluster {
				cluster = &estate.Clusters[i]
				break
			}
		}
		if cluster == nil {
			continue // PR targets an unregistered/argo-only cluster — not a membership case we validate here.
		}
		_, enabled := cluster.Addons[pr.Addon]
		switch pr.Operation {
		case prtracker.OpAddonEnable:
			if enabled {
				t.Errorf("open enable PR for %s on %s targets an addon that's already enabled", pr.Addon, pr.Cluster)
			}
		case prtracker.OpAddonDisable, prtracker.OpAddonUpgrade:
			if !enabled {
				t.Errorf("open %s PR for %s on %s targets an addon that isn't enabled", pr.Operation, pr.Addon, pr.Cluster)
			}
		}
	}
	for _, want := range []string{prtracker.OpAddonEnable, prtracker.OpAddonDisable, prtracker.OpAddonUpgrade} {
		if !seenOpenOp[want] {
			t.Errorf("no open tracked PR with operation %q", want)
		}
	}
}
