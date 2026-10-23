package api

// adopt_finish.go — finish an adopt when its pull request is merged, or
// take its mark off when the pull request is closed without merging.
//
// An adopt that opens a pull request changes nothing on ArgoCD's
// connection except one harmless annotation: sharko.dev/adopt-pending-pr,
// set to the pull request number. Before this, the adopt answer promised
// "ownership and the adopted annotation will be set after the PR is
// merged", but nothing ever did it — the cluster stayed half adopted
// (episode 3 dry run, design call 2).

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/lifecycleevents"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// Outcomes of FinishAdoptOnPRComplete, for tests and the log.
const (
	adoptFinishIgnored   = "ignored"
	adoptFinishAdopted   = "adopted"
	adoptFinishTakenOver = "taken_over"
	adoptFinishReleased  = "released"
	adoptFinishGone      = "connection_gone"
	adoptFinishFailed    = "failed"
)

// FinishAdoptOnPRComplete finishes an adopt or a take over when its pull
// request reaches merged or closed. FinishTrackedPR calls it; an error
// keeps the pull request tracked so it is tried again (EP3 dry run round R,
// R4 and R5).
//
// Which one it finishes comes from the tracked pull request
// (FollowUp.Finish), not from the mark on the Secret, so an adopt merged
// before its mark was written still finishes. A pull request tracked
// before this field existed is finished only when its number matches the
// adopt waiting mark, as before.
//
//   - merged: Sharko takes ownership of the connection (an adopt keeps the
//     old labels and marks it adopted; a take over keeps or drops them as
//     the person asked), and takes this pull request's waiting mark off.
//     A connection that is gone counts as done.
//   - closed: Sharko takes this pull request's waiting mark off and
//     changes nothing else.
func (s *Server) FinishAdoptOnPRComplete(ctx context.Context, pr prtracker.PRInfo, status string) (string, error) {
	if pr.Operation != prtracker.OpAdoptCluster || pr.Cluster == "" || s.argoSecretManager == nil {
		return adoptFinishIgnored, nil
	}
	if status != "merged" && status != "closed" {
		return adoptFinishIgnored, nil
	}
	name := pr.Cluster
	prID := strconv.Itoa(pr.PRID)

	kind := ""
	if pr.FollowUp != nil {
		kind = pr.FollowUp.Finish
	}
	if kind != prtracker.FinishAdopt && kind != prtracker.FinishTakeover {
		mark, err := s.argoSecretManager.GetAnnotation(ctx, name, argosecrets.AnnotationAdoptPendingPR)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return adoptFinishIgnored, nil
			}
			slog.Warn("adopt finish: could not read the cluster's ArgoCD connection", "cluster", name, "pr", pr.PRID, "error", err)
			return adoptFinishFailed, err
		}
		if mark == "" || mark != prID {
			return adoptFinishIgnored, nil
		}
		kind = prtracker.FinishAdopt
	}
	markKey := argosecrets.AnnotationAdoptPendingPR
	what := "adopt"
	if kind == prtracker.FinishTakeover {
		markKey = argosecrets.AnnotationTakeoverPendingPR
		what = "take over"
	}

	if status == "closed" {
		removed, err := s.argoSecretManager.RemoveAnnotationIfValue(ctx, name, markKey, prID)
		if err != nil {
			slog.Warn("adopt finish: pull request closed, but the waiting mark could not be taken off", "cluster", name, "pr", pr.PRID, "kind", what, "error", err)
			return adoptFinishFailed, err
		}
		slog.Info("adopt finish: pull request closed without merging — took the waiting mark off, changed nothing else", "cluster", name, "pr", pr.PRID, "kind", what)
		if removed {
			s.recordAdoptFinish(name, pr.PRID, kind, "cluster_"+kind+"_released", "success")
		}
		return adoptFinishReleased, nil
	}

	detail, err := s.argoSecretManager.GetClusterSecretDetail(ctx, name)
	if err != nil {
		slog.Warn("adopt finish: could not read the cluster's ArgoCD connection", "cluster", name, "pr", pr.PRID, "error", err)
		return adoptFinishFailed, err
	}
	if !detail.Found {
		slog.Warn("adopt finish: pull request merged, but the cluster's ArgoCD connection is gone, so there is nothing to take ownership of",
			"cluster", name, "pr", pr.PRID, "kind", what)
		s.recordAdoptFinish(name, pr.PRID, kind, "cluster_"+kind+"_finish_failed", "failure")
		return adoptFinishGone, nil
	}

	preserve := true
	if kind == prtracker.FinishTakeover {
		preserve = pr.FollowUp.PreserveLegacyLabels
	}
	swap, err := s.argoSecretManager.TakeOverClusterSecret(
		ctx, name, preserve, time.Now().UTC().Format(time.RFC3339), takeoverLegacyLabelKey)
	if err == nil && !swap.Found {
		err = errors.New("the connection disappeared before the swap")
	}
	if err != nil {
		// The mark stays and the tracker tries again.
		slog.Error("adopt finish: pull request merged, but Sharko could not take ownership of the ArgoCD connection — Sharko tries again",
			"cluster", name, "pr", pr.PRID, "kind", what, "error", err)
		s.recordAdoptFinish(name, pr.PRID, kind, "cluster_"+kind+"_finish_failed", "failure")
		return adoptFinishFailed, err
	}
	outcome := adoptFinishTakenOver
	if kind == prtracker.FinishAdopt {
		if err := s.argoSecretManager.SetAnnotation(ctx, name, orchestrator.AnnotationAdopted, "true"); err != nil {
			slog.Error("adopt finish: took ownership, but could not mark the connection adopted", "cluster", name, "pr", pr.PRID, "error", err)
			s.recordAdoptFinish(name, pr.PRID, kind, "cluster_adopt_finish_failed", "failure")
			return adoptFinishFailed, err
		}
		outcome = adoptFinishAdopted
	}
	if _, err := s.argoSecretManager.RemoveAnnotationIfValue(ctx, name, markKey, prID); err != nil {
		// Harmless: ownership is done. The waiting-marks pass takes it off
		// later, because this pull request is merged.
		slog.Warn("adopt finish: adopted, but the waiting mark could not be taken off", "cluster", name, "pr", pr.PRID, "kind", what, "error", err)
	}
	if s.clusterRecon != nil {
		s.clusterRecon.Trigger()
	}
	slog.Info("adopt finish: pull request merged — Sharko owns the ArgoCD connection", "cluster", name, "pr", pr.PRID, "kind", what)
	event := "cluster_adopted"
	if kind == prtracker.FinishTakeover {
		event = lifecycleevents.ClusterTakenOver
	}
	s.recordAdoptFinish(name, pr.PRID, kind, event, "success")
	return outcome, nil
}

// recordAdoptFinish writes an audit entry for the background finish step.
// There is no HTTP request here, so audit.Enrich cannot be used.
func (s *Server) recordAdoptFinish(cluster string, prID int, kind, event, result string) {
	if s.auditLog == nil {
		return
	}
	level := "info"
	if result != "success" {
		level = "error"
	}
	s.auditLog.Add(audit.Entry{
		Timestamp: time.Now().UTC(),
		Level:     level,
		User:      "sharko",
		Action:    kind + "-finish",
		Resource:  "cluster:" + cluster,
		Source:    "pr-tracker",
		Result:    result,
		Event:     event,
		Detail:    "pull request #" + strconv.Itoa(prID),
	})
}
