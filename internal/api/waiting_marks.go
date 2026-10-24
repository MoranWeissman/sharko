package api

// waiting_marks.go — make sure every waiting mark has something that
// finishes it (EP3 dry run rounds R and S: R4, S8, S9, S10, S11, S20).
//
// Adopt, take over, and a removal or un-adopt that keeps the connection
// each put a mark on the cluster's ArgoCD connection. The mark names the
// pull request, its repository and the choices the finish needs. The PR
// tracker finishes the work when the pull request merges or closes. When
// the tracker lost that pull request (its record was lost, or the pull
// request was made before tracking), this pass tracks it again from the
// mark, so the tracker finishes it under its one retry rule: the same gap
// between tries, the same limit, the same single give-up. The pass itself
// never finishes anything and never deletes anything.
//
// A keep mark that never got its pull request number ("pending") is
// settled once it is old enough: it gets the number of the removal the
// tracker still has for the cluster, or it comes off when the cluster is
// still in Git, or the connection is handed back (S9).

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
	"github.com/MoranWeissman/sharko/internal/orchestrator"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// waitingMarkPendingMaxAge is how old a keep mark with no pull request
// number must be before the pass settles it. The request that wrote it
// writes the number within seconds; a mark this old was left behind.
const waitingMarkPendingMaxAge = 10 * time.Minute

// waitingMarkSeen remembers, in memory, when the pass first saw a keep mark
// with no pull request number and no written time (older marks). A
// restart starts the wait again, which only makes it wait longer.
type waitingMarkSeen struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// firstSeen returns when key was first seen, recording now if never.
func (w *waitingMarkSeen) firstSeen(key string, now time.Time) time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seen == nil {
		w.seen = map[string]time.Time{}
	}
	if t, ok := w.seen[key]; ok {
		return t
	}
	w.seen[key] = now
	return now
}

func (w *waitingMarkSeen) forget(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.seen, key)
}

// waitingMarkPR is the tracked pull request a mark stands for. The
// choices come from the mark: the take over label choice (an older mark
// without it keeps the old labels, which takes nothing off), and whether a
// keep mark is a removal or an un-adopt. A kept connection is only handed
// back (cleanup none).
func waitingMarkPR(key, cluster string, m models.PendingPRMark) prtracker.PRInfo {
	pr := prtracker.PRInfo{PRID: m.PR, Cluster: cluster, Source: "waiting-mark"}
	switch key {
	case argosecrets.AnnotationAdoptPendingPR:
		pr.Operation = prtracker.OpAdoptCluster
		pr.FollowUp = &prtracker.ClusterFollowUp{Finish: prtracker.FinishAdopt}
	case argosecrets.AnnotationTakeoverPendingPR:
		keep := true
		if m.KeepOldLabels != nil {
			keep = *m.KeepOldLabels
		}
		pr.Operation = prtracker.OpAdoptCluster
		pr.FollowUp = &prtracker.ClusterFollowUp{Finish: prtracker.FinishTakeover, PreserveLegacyLabels: keep}
	case argosecrets.AnnotationRemoveKeepPendingPR:
		pr.Operation = prtracker.OpRemoveCluster
		pr.FollowUp = &prtracker.ClusterFollowUp{Cleanup: "none", KeepConnection: true, CredentialKey: cluster}
		if m.Op == models.PendingMarkOpUnadopt {
			pr.Operation = prtracker.OpUnadoptCluster
			pr.FollowUp = &prtracker.ClusterFollowUp{KeepConnection: true}
		}
	}
	return pr
}

// FinishWaitingClusterMarks is the pass the cluster reconciler runs on its
// tick (clusterreconciler.Deps.WaitingMarksFn). It does nothing until the
// k8s client, the PR tracker and a Git connection are all there.
func (s *Server) FinishWaitingClusterMarks(ctx context.Context) {
	k8s, ns, ok := s.k8sClientAndNamespace()
	if !ok || s.prTracker == nil || s.connSvc == nil {
		return
	}
	git, err := s.connSvc.GetActiveGitProvider()
	if err != nil || git == nil {
		return
	}
	repo := ""
	if conn, connErr := s.connSvc.GetActiveConnection(); connErr == nil && conn != nil {
		repo = conn.Git.RepoKey()
	}
	s.finishWaitingMarks(ctx, k8s, ns, git, repo)
}

// finishWaitingMarks looks at every Secret in the namespace, not only the
// ones that still carry ArgoCD's cluster label, so no mark is missed
// (S20). repo is the active Git connection's repository key ("" when not
// known).
func (s *Server) finishWaitingMarks(ctx context.Context, k8s kubernetes.Interface, ns string, git gitprovider.GitProvider, repo string) {
	list, err := k8s.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		slog.Warn("waiting marks: could not list the Secrets; Sharko tries again on the next pass",
			"namespace", ns, "error", err)
		return
	}
	for i := range list.Items {
		sec := &list.Items[i]
		for _, key := range argosecrets.PendingPRMarks {
			value, has := sec.Annotations[key]
			if !has {
				continue
			}
			cluster := argosecrets.ClusterNameOf(sec)
			m, ok := models.ParsePendingPRMark(value)
			if !ok {
				slog.Info("waiting marks: a waiting mark Sharko cannot read is left alone",
					"cluster", cluster, "mark", key)
				continue
			}
			if m.PR == 0 {
				if key == argosecrets.AnnotationRemoveKeepPendingPR {
					s.settlePendingKeepMark(ctx, git, sec.Namespace+"/"+sec.Name, cluster, m)
				}
				continue
			}
			s.trackWaitingMark(ctx, git, repo, key, cluster, m)
		}
	}
}

// trackWaitingMark makes sure the tracker has the pull request a numbered
// mark names. A mark for another repository than the active Git connection
// is left alone: the same number there is another pull request (S11).
func (s *Server) trackWaitingMark(ctx context.Context, git gitprovider.GitProvider, repo, key, cluster string, m models.PendingPRMark) {
	if m.Repo != "" && repo != "" && !models.RepoKeysMatch(m.Repo, repo) {
		slog.Info("waiting marks: the mark's pull request is in another repository than the active Git connection; left alone",
			"cluster", cluster, "mark", key, "pr", m.PR, "mark_repo", m.Repo, "active_repo", repo)
		return
	}
	tracked, err := s.prTracker.GetPR(ctx, m.PR)
	if err != nil {
		slog.Warn("waiting marks: could not read the tracked pull requests; Sharko tries again on the next pass",
			"cluster", cluster, "mark", key, "pr", m.PR, "error", err)
		return
	}
	if tracked != nil {
		// The tracker has it (open, finishing, or given up) and deals
		// with it under its own retry rule.
		return
	}
	pr := waitingMarkPR(key, cluster, m)
	pr.CreatedAt = time.Now()
	status, err := git.GetPullRequestStatus(ctx, m.PR)
	switch {
	case errors.Is(err, gitprovider.ErrPullRequestNotFound):
		// Handled as closed (S10); the hooks write the "gone" audit entry.
		pr.LastStatus, pr.Gone, pr.HooksPending = "closed", true, true
	case err != nil:
		slog.Warn("waiting marks: could not ask Git about the mark's pull request; Sharko tries again on the next pass",
			"cluster", cluster, "mark", key, "pr", m.PR, "error", err)
		return
	case status == "merged" || status == "closed":
		pr.LastStatus = status
	default:
		// Still open: track it, and the tracker sees it merge or close.
		pr.LastStatus = "open"
	}
	if err := s.prTracker.TrackPR(ctx, pr); err != nil {
		slog.Warn("waiting marks: could not track the mark's pull request again; Sharko tries again on the next pass",
			"cluster", cluster, "mark", key, "pr", m.PR, "error", err)
		return
	}
	slog.Info("waiting marks: tracking a pull request again from its waiting mark",
		"cluster", cluster, "mark", key, "pr", m.PR, "status", pr.LastStatus)
	s.prTracker.FinishNow(ctx, m.PR)
}

// settlePendingKeepMark settles a keep mark that never got its pull request
// number, once it is waitingMarkPendingMaxAge old (S9). It never deletes
// the connection:
//
//   - the tracker still has an unfinished removal or un-adopt that keeps
//     this connection: the mark gets that pull request's number;
//   - else the cluster is still in Git (the removal never happened): the
//     mark comes off;
//   - else the cluster is gone from Git: the connection is handed back
//     (Sharko's label and the mark come off).
func (s *Server) settlePendingKeepMark(ctx context.Context, git gitprovider.GitProvider, seenKey, cluster string, m models.PendingPRMark) {
	if s.argoSecretManager == nil {
		return
	}
	now := time.Now()
	since, known := m.WrittenAt()
	if !known {
		since = s.waitingMarks.firstSeen(seenKey, now)
	}
	if now.Sub(since) < waitingMarkPendingMaxAge {
		return
	}
	key := argosecrets.AnnotationRemoveKeepPendingPR

	waiting, err := s.prTracker.ListPRsFiltered(ctx, "", cluster, "", "",
		[]string{prtracker.OpRemoveCluster, prtracker.OpUnadoptCluster})
	if err != nil {
		slog.Warn("waiting marks: could not read the tracked pull requests; Sharko tries again on the next pass",
			"cluster", cluster, "mark", key, "error", err)
		return
	}
	sort.Slice(waiting, func(i, j int) bool { return waiting[i].PRID > waiting[j].PRID })
	for _, pr := range waiting {
		if pr.FollowUp == nil || !pr.FollowUp.KeepConnection {
			continue
		}
		op := models.PendingMarkOpRemove
		if pr.Operation == prtracker.OpUnadoptCluster {
			op = models.PendingMarkOpUnadopt
		}
		numbered := models.PendingPRMark{PR: pr.PRID, Repo: models.RepoKeyFromPRURL(pr.PRUrl), Op: op,
			At: now.UTC().Format(time.RFC3339)}
		if _, err := s.argoSecretManager.ReplaceAnnotationIfValue(ctx, cluster, key, "pending", numbered.String()); err != nil {
			slog.Warn("waiting marks: could not write the pull request number into a keep mark; Sharko tries again on the next pass",
				"cluster", cluster, "pr", pr.PRID, "error", err)
			return
		}
		s.waitingMarks.forget(seenKey)
		slog.Info("waiting marks: a keep mark with no pull request number now names the removal still waiting",
			"cluster", cluster, "pr", pr.PRID)
		return
	}

	orch := orchestrator.New(&s.gitMu, nil, nil, git, s.gitopsConfig(), s.repoPaths, nil)
	inGit, err := orch.ClusterInRegistry(ctx, cluster)
	if err != nil {
		slog.Warn("waiting marks: could not read the cluster list to settle a keep mark; Sharko tries again on the next pass",
			"cluster", cluster, "error", err)
		return
	}
	if inGit {
		if _, err := s.argoSecretManager.RemoveAnnotationIfValue(ctx, cluster, key, "pending"); err != nil {
			slog.Warn("waiting marks: could not take an old keep mark off; Sharko tries again on the next pass",
				"cluster", cluster, "error", err)
			return
		}
		s.waitingMarks.forget(seenKey)
		slog.Info("waiting marks: took off a keep mark whose removal never got a pull request; the cluster is still in Git",
			"cluster", cluster)
		return
	}
	if _, err := s.argoSecretManager.ReleaseKeptConnection(ctx, cluster, 0); err != nil {
		slog.Warn("waiting marks: could not hand back a kept connection; Sharko tries again on the next pass",
			"cluster", cluster, "error", err)
		return
	}
	s.waitingMarks.forget(seenKey)
	slog.Info("waiting marks: handed back a kept connection whose removal never got a pull request number; the cluster is gone from Git",
		"cluster", cluster)
}
