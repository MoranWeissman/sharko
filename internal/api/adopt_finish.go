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
	"log/slog"
	"strconv"
	"time"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// Outcomes of FinishAdoptOnPRComplete, for tests and the log.
const (
	adoptFinishIgnored  = "ignored"
	adoptFinishAdopted  = "adopted"
	adoptFinishReleased = "released"
	adoptFinishFailed   = "failed"
)

// FinishAdoptOnPRComplete runs when a tracked pull request reaches merged
// or closed. It acts only on adopt pull requests whose number matches the
// waiting mark on the cluster's ArgoCD Secret, so a take over pull request
// (it shares the operation code) or an old adopt pull request is left
// alone.
//
//   - merged: Sharko takes ownership of the connection (the old labels are
//     kept), marks it adopted, and takes the waiting mark off.
//   - closed: Sharko takes the waiting mark off and changes nothing else.
func (s *Server) FinishAdoptOnPRComplete(ctx context.Context, pr prtracker.PRInfo, status string) string {
	if pr.Operation != prtracker.OpAdoptCluster || pr.Cluster == "" || s.argoSecretManager == nil {
		return adoptFinishIgnored
	}
	if status != "merged" && status != "closed" {
		return adoptFinishIgnored
	}
	name := pr.Cluster
	mark, err := s.argoSecretManager.GetAnnotation(ctx, name, argosecrets.AnnotationAdoptPendingPR)
	if err != nil {
		slog.Warn("adopt finish: could not read the cluster's ArgoCD connection", "cluster", name, "pr", pr.PRID, "error", err)
		return adoptFinishFailed
	}
	if mark == "" || mark != strconv.Itoa(pr.PRID) {
		return adoptFinishIgnored
	}

	if status == "closed" {
		if _, err := s.argoSecretManager.RemoveAnnotation(ctx, name, argosecrets.AnnotationAdoptPendingPR); err != nil {
			slog.Warn("adopt finish: pull request closed, but the waiting mark could not be taken off", "cluster", name, "pr", pr.PRID, "error", err)
			return adoptFinishFailed
		}
		slog.Info("adopt finish: pull request closed without merging — took the waiting mark off, changed nothing else", "cluster", name, "pr", pr.PRID)
		s.recordAdoptFinish(name, pr.PRID, "cluster_adopt_released", "success")
		return adoptFinishReleased
	}

	swap, err := s.argoSecretManager.TakeOverClusterSecret(
		ctx, name, true, time.Now().UTC().Format(time.RFC3339), takeoverLegacyLabelKey)
	if err != nil || !swap.Found {
		// The mark stays, so running adopt again (or the next poll of a
		// re-opened pull request) can finish the job.
		slog.Error("adopt finish: pull request merged, but Sharko could not take ownership of the ArgoCD connection — run adopt again",
			"cluster", name, "pr", pr.PRID, "error", err, "found", swap.Found)
		s.recordAdoptFinish(name, pr.PRID, "cluster_adopt_finish_failed", "failure")
		return adoptFinishFailed
	}
	if err := s.argoSecretManager.SetAnnotation(ctx, name, orchestrator.AnnotationAdopted, "true"); err != nil {
		slog.Error("adopt finish: took ownership, but could not mark the connection adopted", "cluster", name, "pr", pr.PRID, "error", err)
		s.recordAdoptFinish(name, pr.PRID, "cluster_adopt_finish_failed", "failure")
		return adoptFinishFailed
	}
	if _, err := s.argoSecretManager.RemoveAnnotation(ctx, name, argosecrets.AnnotationAdoptPendingPR); err != nil {
		// Harmless: the cluster is adopted. The mark is ignored from now on
		// because no open adopt pull request has its number.
		slog.Warn("adopt finish: adopted, but the waiting mark could not be taken off", "cluster", name, "pr", pr.PRID, "error", err)
	}
	if s.clusterRecon != nil {
		s.clusterRecon.Trigger()
	}
	slog.Info("adopt finish: pull request merged — Sharko owns the ArgoCD connection and marked it adopted", "cluster", name, "pr", pr.PRID)
	s.recordAdoptFinish(name, pr.PRID, "cluster_adopted", "success")
	return adoptFinishAdopted
}

// recordAdoptFinish writes an audit entry for the background finish step.
// There is no HTTP request here, so audit.Enrich cannot be used.
func (s *Server) recordAdoptFinish(cluster string, prID int, event, result string) {
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
		Action:    "adopt-finish",
		Resource:  "cluster:" + cluster,
		Source:    "pr-tracker",
		Result:    result,
		Event:     event,
		Detail:    "pull request #" + strconv.Itoa(prID),
	})
}
