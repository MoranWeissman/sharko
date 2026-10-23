package api

import (
	"context"
	"log/slog"

	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

// FinishClusterFollowUpOnMerge is the pull-request-merge hook that finishes
// a cluster removal or un-adopt whose pull request a person merged after
// the request returned (EP3-DRYRUN-FIXES calls 1 and 9: nothing that cuts a
// cluster off happens before its pull request is merged). It is wired in
// cmd/sharko/serve.go's prTracker.SetOnMergeFn and runs there
// synchronously, before the tracker drops the pull request — the cluster
// reconciler keeps a "keep the connection" Secret safe for exactly as long
// as the pull request is tracked (prtracker.Tracker.KeptConnections).
//
// Every other operation, and a remove or un-adopt pull request tracked
// before this hook existed (no FollowUp: its ArgoCD side already ran at
// request time), is ignored. It does nothing when the cluster is back in
// Git (registered again under the same name) or when Sharko cannot read
// the cluster list to tell. Safe to run twice: an auto-merged pull request
// was already finished at request time, and the tracker's later poll of
// the same pull request runs this again as a no-op.
//
// Errors are logged, never returned: the merge already happened.
func (s *Server) FinishClusterFollowUpOnMerge(ctx context.Context, pr prtracker.PRInfo) {
	if pr.Operation != prtracker.OpRemoveCluster && pr.Operation != prtracker.OpUnadoptCluster {
		return
	}
	if pr.FollowUp == nil || pr.Cluster == "" {
		return
	}
	followUp := followUpFromTracker(pr.FollowUp)

	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil || git == nil {
		// The error value is not logged: on the Git side the reason the
		// client could not be built may be the repository token.
		slog.Warn("after-merge cluster step skipped: no usable Git connection",
			"cluster", pr.Cluster, "operation", pr.Operation, "pr_id", pr.PRID)
		return
	}
	var ac orchestrator.ArgocdClient
	if client, acErr := s.connSvc.GetActiveArgocdClient(); acErr == nil && client != nil {
		ac = client
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, git, s.gitopsConfig(), s.repoPaths, nil)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	if s.argoSecretManager != nil {
		roleARN := ""
		if s.addonSecretCfg() != nil {
			roleARN = s.addonSecretCfg().RoleARN
		}
		orch.SetArgoSecretManager(&argoManagerAdapter{mgr: s.argoSecretManager}, roleARN)
	}

	inGit, err := orch.ClusterInRegistry(ctx, pr.Cluster)
	if err != nil {
		slog.Error("after-merge cluster step skipped: Sharko could not read the cluster list to check the cluster is really gone from Git",
			"cluster", pr.Cluster, "operation", pr.Operation, "pr_id", pr.PRID, "error", err)
		return
	}
	if inGit {
		slog.Info("after-merge cluster step skipped: the cluster is in Git again, so its connection is left alone",
			"cluster", pr.Cluster, "operation", pr.Operation, "pr_id", pr.PRID)
		return
	}

	switch pr.Operation {
	case prtracker.OpRemoveCluster:
		res := orch.FinishRemoveAfterMerge(ctx, pr.Cluster, *followUp)
		if res.Status != "success" {
			slog.Error("finishing a cluster removal after its pull request merged did not fully work",
				"cluster", pr.Cluster, "pr_id", pr.PRID, "failed_step", res.FailedStep)
		} else {
			slog.Info("cluster removal finished after its pull request merged",
				"cluster", pr.Cluster, "pr_id", pr.PRID)
		}
	case prtracker.OpUnadoptCluster:
		if err := orch.FinishUnadoptAfterMerge(ctx, pr.Cluster, *followUp); err != nil {
			slog.Error("finishing an un-adopt after its pull request merged failed",
				"cluster", pr.Cluster, "pr_id", pr.PRID, "error", err)
		} else {
			slog.Info("un-adopt finished after its pull request merged",
				"cluster", pr.Cluster, "pr_id", pr.PRID)
		}
	}
	if s.clusterRecon != nil {
		s.clusterRecon.Trigger()
	}
}
