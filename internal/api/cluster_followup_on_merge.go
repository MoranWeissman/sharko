package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"
	"github.com/MoranWeissman/sharko/internal/remoteclient"
)

// errFinishNoGit is returned when the finish step has no Git connection to
// check the cluster is really gone from Git. The tracker tries again.
var errFinishNoGit = errors.New("no usable Git connection to check the cluster list")

// FinishTrackedPR is the tracker's finish step (prtracker.SetFinishFn). It
// runs after a tracked pull request merged or was closed, after the merge
// and complete hooks. A returned error keeps the pull request tracked, and
// the tracker runs this again on its next poll, up to its cap (EP3 dry run
// round R, R1 and R4). Every branch is safe to run twice.
//
//   - remove or un-adopt, merged: FinishClusterFollowUpOnMerge.
//   - remove, closed: nothing changed, so only this pull request's keep
//     mark comes off.
//   - adopt and take over (one operation code): FinishAdoptOnPRComplete.
func (s *Server) FinishTrackedPR(ctx context.Context, pr prtracker.PRInfo, status string) error {
	switch pr.Operation {
	case prtracker.OpRemoveCluster, prtracker.OpUnadoptCluster:
		if status == "merged" {
			return s.FinishClusterFollowUpOnMerge(ctx, pr)
		}
		return s.dropKeepMark(ctx, pr)
	case prtracker.OpAdoptCluster:
		_, err := s.FinishAdoptOnPRComplete(ctx, pr, status)
		return err
	}
	return nil
}

// FinishClusterFollowUpOnMerge finishes a cluster removal or un-adopt whose
// pull request a person merged after the request returned (EP3-DRYRUN-FIXES
// calls 1 and 9: nothing that cuts a cluster off happens before its pull
// request is merged). FinishTrackedPR calls it.
//
// Every other operation, and a remove or un-adopt pull request tracked
// before this hook existed (no FollowUp: its ArgoCD side already ran at
// request time), is ignored.
//
// It returns an error when it could not finish (no Git connection, the
// cluster list could not be read, or a step failed), so the tracker keeps
// the pull request and tries again. Meanwhile the remove-keep mark keeps a
// kept connection safe (R1).
//
// It leaves the connection alone, and only takes this pull request's keep
// mark off, when the cluster is back in Git or a new registration of the
// same name is waiting (R3): the Secret then belongs to the new
// registration.
func (s *Server) FinishClusterFollowUpOnMerge(ctx context.Context, pr prtracker.PRInfo) error {
	if pr.Operation != prtracker.OpRemoveCluster && pr.Operation != prtracker.OpUnadoptCluster {
		return nil
	}
	if pr.FollowUp == nil || pr.Cluster == "" {
		return nil
	}
	followUp := followUpFromTracker(pr.FollowUp)
	// The finish only takes off this pull request's own keep mark (EP3
	// dry run round S, S4).
	followUp.PRID = pr.PRID

	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil || git == nil {
		// The error value is not logged: on the Git side the reason the
		// client could not be built may be the repository token.
		slog.Warn("after-merge cluster step waits: no usable Git connection",
			"cluster", pr.Cluster, "operation", pr.Operation, "pr_id", pr.PRID)
		return errFinishNoGit
	}
	var ac orchestrator.ArgocdClient
	if client, acErr := s.connSvc.GetActiveArgocdClient(); acErr == nil && client != nil {
		ac = client
	}

	orch := orchestrator.New(&s.gitMu, s.credProvider(), ac, git, s.gitopsConfig(), s.repoPaths, nil)
	orch.SetSecretManagement(s.addonSecretDefs, s.secretFetcher, remoteclient.NewClientFromKubeconfig)
	// The same credentials router the request path uses, so a cluster
	// whose connection lives only in ArgoCD is reached too (R13).
	orch.SetCredsRouter(s.credsRouter())
	if s.argoSecretManager != nil {
		roleARN := ""
		if s.addonSecretCfg() != nil {
			roleARN = s.addonSecretCfg().RoleARN
		}
		orch.SetArgoSecretManager(&argoManagerAdapter{mgr: s.argoSecretManager}, roleARN)
	}

	inGit, err := orch.ClusterInRegistry(ctx, pr.Cluster)
	if err != nil {
		slog.Error("after-merge cluster step waits: Sharko could not read the cluster list to check the cluster is really gone from Git",
			"cluster", pr.Cluster, "operation", pr.Operation, "pr_id", pr.PRID, "error", err)
		return fmt.Errorf("reading the cluster list: %w", err)
	}
	if inGit {
		slog.Info("after-merge cluster step skipped: the cluster is in Git again, so its connection is left alone",
			"cluster", pr.Cluster, "operation", pr.Operation, "pr_id", pr.PRID)
		return s.dropKeepMark(ctx, pr)
	}
	waiting, err := s.newRegistrationWaiting(ctx, pr.Cluster)
	if err != nil {
		slog.Error("after-merge cluster step waits: Sharko could not check whether the cluster is being registered again",
			"cluster", pr.Cluster, "operation", pr.Operation, "pr_id", pr.PRID, "error", err)
		return fmt.Errorf("checking for a new registration: %w", err)
	}
	if waiting {
		slog.Info("after-merge cluster step skipped: the cluster is being registered again, so its connection is left alone",
			"cluster", pr.Cluster, "operation", pr.Operation, "pr_id", pr.PRID)
		return s.dropKeepMark(ctx, pr)
	}

	var finishErr error
	switch pr.Operation {
	case prtracker.OpRemoveCluster:
		res := orch.FinishRemoveAfterMerge(ctx, pr.Cluster, *followUp)
		if res.Status != "success" {
			slog.Error("finishing a cluster removal after its pull request merged did not fully work",
				"cluster", pr.Cluster, "pr_id", pr.PRID, "failed_step", res.FailedStep)
			finishErr = fmt.Errorf("removal step %q did not work", res.FailedStep)
		} else {
			slog.Info("cluster removal finished after its pull request merged",
				"cluster", pr.Cluster, "pr_id", pr.PRID)
		}
	case prtracker.OpUnadoptCluster:
		if err := orch.FinishUnadoptAfterMerge(ctx, pr.Cluster, *followUp); err != nil {
			slog.Error("finishing an un-adopt after its pull request merged failed",
				"cluster", pr.Cluster, "pr_id", pr.PRID, "error", err)
			finishErr = fmt.Errorf("un-adopt: %w", err)
		} else {
			slog.Info("un-adopt finished after its pull request merged",
				"cluster", pr.Cluster, "pr_id", pr.PRID)
		}
	}
	if s.clusterRecon != nil {
		s.clusterRecon.Trigger()
	}
	return finishErr
}

// newRegistrationWaiting reports whether a new registration of the cluster
// is on its way: its ArgoCD Secret carries the registration-pending mark,
// or the tracker has an open register pull request for it (R3).
func (s *Server) newRegistrationWaiting(ctx context.Context, cluster string) (bool, error) {
	if s.argoSecretManager != nil {
		detail, err := s.argoSecretManager.GetClusterSecretDetail(ctx, cluster)
		if err != nil {
			return false, err
		}
		if detail.Found {
			if _, ok := models.RegistrationPendingValue(detail.Annotations); ok {
				return true, nil
			}
		}
	}
	if s.prTracker != nil {
		open, err := s.prTracker.ListPRsFiltered(ctx, "open", cluster, "", "", []string{prtracker.OpRegisterCluster})
		if err != nil {
			return false, err
		}
		if len(open) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// dropKeepMark takes this pull request's remove-keep mark off the
// cluster's Secret. A mark with another value (a newer removal) stays.
func (s *Server) dropKeepMark(ctx context.Context, pr prtracker.PRInfo) error {
	if s.argoSecretManager == nil || (pr.Operation != prtracker.OpRemoveCluster && pr.Operation != prtracker.OpUnadoptCluster) || pr.Cluster == "" {
		return nil
	}
	if _, err := s.argoSecretManager.RemoveAnnotationIfValue(ctx, pr.Cluster,
		argosecrets.AnnotationRemoveKeepPendingPR, strconv.Itoa(pr.PRID)); err != nil {
		slog.Warn("could not take a removal's keep mark off the cluster's ArgoCD connection; Sharko tries again",
			"cluster", pr.Cluster, "pr_id", pr.PRID, "error", err)
		return fmt.Errorf("taking the keep mark off: %w", err)
	}
	return nil
}
