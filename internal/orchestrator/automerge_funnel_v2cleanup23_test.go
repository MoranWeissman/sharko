// V2-cleanup-23 coverage: the shared prMeta builder threads the per-request
// auto-merge choice through EVERY write path that opens a PR. Before this
// bundle only ~7 of ~16 paths honored the override, so behavior drifted
// ("sometimes auto-merged, sometimes not"). This table test asserts the
// uniform contract for every PR-opening orchestrator operation:
//
//   - an explicit AutoMerge=true  -> PR merged + source branch deleted,
//     even when the connection default is false;
//   - an explicit AutoMerge=false -> PR opened, NOT merged, branch kept,
//     even when the connection default is true;
//   - AutoMerge=nil               -> falls back to the connection default
//     (tested with both a true and a false connection default).
//
// The mock git provider only calls DeleteBranch after a successful merge
// (see commitChangesWithMeta), so a non-empty deletedBranches slice is a
// faithful proxy for "the PR was merged".
//
// Operations covered here (the gaps closed by V2-cleanup-23):
//   upgrade-global, upgrade-cluster, upgrade-batch, set-global-values,
//   set-cluster-values, configure-addon, unadopt-cluster, enable-addon,
//   remove-addon.
// Plus a regression pass over the already-correct paths:
//   register-cluster, add-addon, adopt-cluster, update-cluster.
// (remove-cluster + disable-addon are covered in
// remove_disable_automerge_test.go and stay green as regression.)
package orchestrator

import (
	"context"
	"testing"
)

// autoMergeMatrix is the canonical 4-row override/default precedence matrix
// reused by every per-op sub-test below.
type autoMergeRow struct {
	name           string
	connAutoMerge  bool
	override       *bool
	wantMerged     bool
	wantBranchGone bool
}

func autoMergeMatrix() []autoMergeRow {
	return []autoMergeRow{
		{name: "override_true_beats_conn_false", connAutoMerge: false, override: boolPtr(true), wantMerged: true, wantBranchGone: true},
		{name: "override_false_beats_conn_true", connAutoMerge: true, override: boolPtr(false), wantMerged: false, wantBranchGone: false},
		{name: "override_nil_falls_back_to_conn_true", connAutoMerge: true, override: nil, wantMerged: true, wantBranchGone: true},
		{name: "override_nil_falls_back_to_conn_false", connAutoMerge: false, override: nil, wantMerged: false, wantBranchGone: false},
	}
}

// adoptedManagedClusters seeds an adopted cluster so UnadoptCluster reaches
// the PR path (it requires the adopted annotation on the ArgoCD secret).
const adoptedManagedClusters = "clusters:\n  - name: prod-eu\n    labels:\n      cert-manager: true\n"

// runAutoMergeFunnelOp drives one operation through the full matrix. The
// seedFn pre-populates git.files for that op; the runFn invokes the op with
// the row's override and returns the GitResult the op produced.
func runAutoMergeFunnelOp(
	t *testing.T,
	seedFn func(git *mockGitProvider, argo *mockArgocd),
	runFn func(o *Orchestrator, override *bool) (*GitResult, error),
) {
	t.Helper()
	for _, tt := range autoMergeMatrix() {
		t.Run(tt.name, func(t *testing.T) {
			git := newMockGitProvider()
			argo := newMockArgocd()
			if seedFn != nil {
				seedFn(git, argo)
			}
			cfg := defaultGitOps()
			cfg.PRAutoMerge = tt.connAutoMerge
			orch := New(nil, defaultCreds(), argo, git, cfg, defaultPaths(), nil)

			result, err := runFn(orch, tt.override)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected a git result (PR should have been created)")
			}
			if result.Merged != tt.wantMerged {
				t.Errorf("Merged=%v, want %v", result.Merged, tt.wantMerged)
			}
			if len(git.prs) != 1 {
				t.Errorf("expected exactly one PR created, got %d", len(git.prs))
			}
			branchDeleted := len(git.deletedBranches) > 0
			if branchDeleted != tt.wantBranchGone {
				t.Errorf("branch deleted=%v, want %v (deletedBranches=%v)", branchDeleted, tt.wantBranchGone, git.deletedBranches)
			}
		})
	}
}

// ---- Gaps closed by V2-cleanup-23 ----

func TestUpgradeAddonGlobal_AutoMergeOverride(t *testing.T) {
	runAutoMergeFunnelOp(t,
		nil, // default seed catalog already contains cert-manager
		func(o *Orchestrator, override *bool) (*GitResult, error) {
			return o.UpgradeAddonGlobal(context.Background(), "cert-manager", "1.15.0", override)
		})
}

func TestUpgradeAddonCluster_AutoMergeOverride(t *testing.T) {
	runAutoMergeFunnelOp(t,
		func(git *mockGitProvider, _ *mockArgocd) {
			git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte(certManagerValuesFile)
		},
		func(o *Orchestrator, override *bool) (*GitResult, error) {
			return o.UpgradeAddonCluster(context.Background(), "cert-manager", "prod-eu", "1.15.0", override)
		})
}

func TestUpgradeAddons_Batch_AutoMergeOverride(t *testing.T) {
	runAutoMergeFunnelOp(t,
		nil,
		func(o *Orchestrator, override *bool) (*GitResult, error) {
			return o.UpgradeAddons(context.Background(), map[string]string{"cert-manager": "1.15.0"}, override)
		})
}

func TestSetGlobalAddonValues_AutoMergeOverride(t *testing.T) {
	runAutoMergeFunnelOp(t,
		nil,
		func(o *Orchestrator, override *bool) (*GitResult, error) {
			return o.SetGlobalAddonValues(context.Background(), "cert-manager", "replicaCount: 2\n", override)
		})
}

func TestSetClusterAddonValues_AutoMergeOverride(t *testing.T) {
	runAutoMergeFunnelOp(t,
		func(git *mockGitProvider, _ *mockArgocd) {
			git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte(certManagerValuesFile)
		},
		func(o *Orchestrator, override *bool) (*GitResult, error) {
			return o.SetClusterAddonValues(context.Background(), "prod-eu", "cert-manager", "replicaCount: 2\n", override, false)
		})
}

func TestConfigureAddon_AutoMergeOverride(t *testing.T) {
	runAutoMergeFunnelOp(t,
		nil,
		func(o *Orchestrator, override *bool) (*GitResult, error) {
			return o.ConfigureAddon(context.Background(), ConfigureAddonRequest{
				Name:      "cert-manager",
				Version:   "1.15.0",
				AutoMerge: override,
			})
		})
}

func TestEnableAddon_AutoMergeOverride(t *testing.T) {
	runAutoMergeFunnelOp(t,
		func(git *mockGitProvider, _ *mockArgocd) {
			git.files["configuration/managed-clusters.yaml"] = []byte(managedClustersWithCertManager)
			git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte(certManagerValuesFile)
		},
		func(o *Orchestrator, override *bool) (*GitResult, error) {
			res, err := o.EnableAddon(context.Background(), EnableAddonRequest{
				Cluster:   "prod-eu",
				Addon:     "cert-manager",
				Yes:       true,
				AutoMerge: override,
			})
			if res == nil {
				return nil, err
			}
			return res.Git, err
		})
}

func TestRemoveAddon_AutoMergeOverride(t *testing.T) {
	runAutoMergeFunnelOp(t,
		func(git *mockGitProvider, _ *mockArgocd) {
			git.files["configuration/addons-global-values/cert-manager.yaml"] = []byte("replicaCount: 1\n")
		},
		func(o *Orchestrator, override *bool) (*GitResult, error) {
			return o.RemoveAddon(context.Background(), RemoveAddonRequest{Name: "cert-manager", AutoMerge: override})
		})
}

func TestUnadoptCluster_AutoMergeOverride(t *testing.T) {
	runAutoMergeFunnelOp(t,
		func(git *mockGitProvider, _ *mockArgocd) {
			git.files["configuration/managed-clusters.yaml"] = []byte(adoptedManagedClusters)
			git.files["configuration/addons-clusters-values/prod-eu.yaml"] = []byte(certManagerValuesFile)
		},
		func(o *Orchestrator, override *bool) (*GitResult, error) {
			// No argoSecretManager wired (nil) -> the adopted-annotation
			// check is skipped, so the flow reaches the PR path directly.
			res, err := o.UnadoptCluster(context.Background(), "prod-eu", UnadoptClusterRequest{
				Yes:       true,
				AutoMerge: override,
			})
			if res == nil {
				return nil, err
			}
			return res.Git, err
		})
}

// ---- Regression: the already-correct paths must keep honoring the override ----

func TestUpdateClusterAddons_AutoMergeOverride_Regression(t *testing.T) {
	runAutoMergeFunnelOp(t,
		func(git *mockGitProvider, _ *mockArgocd) {
			git.files["configuration/managed-clusters.yaml"] = []byte(managedClustersWithCertManager)
		},
		func(o *Orchestrator, override *bool) (*GitResult, error) {
			res, err := o.UpdateClusterAddons(context.Background(), "prod-eu", "https://k8s.example.com:6443", "", map[string]bool{"cert-manager": true}, override, false)
			if res == nil {
				return nil, err
			}
			return res.Git, err
		})
}
