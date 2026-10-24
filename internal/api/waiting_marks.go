package api

// waiting_marks.go — finish a waiting mark whose pull request the tracker
// no longer has (EP3 dry run round R, R4).
//
// Adopt, take over and a removal that keeps the connection each put a mark
// with the pull request number on the cluster's ArgoCD connection. The PR
// tracker finishes the work when the pull request merges or closes. When
// the tracker lost that pull request (it gave up after its tries, or its
// record was lost), the mark stays on and nothing would ever finish the
// work. This pass looks at every such mark, asks Git about its pull
// request, and finishes it the same way the tracker does.

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/prtracker"
)

// waitingMarkMaxTries is how many times the pass tries one mark whose
// pull request it cannot read or cannot finish. Then it logs an error once
// and leaves the mark on, so the connection stays protected.
const waitingMarkMaxTries = 20

// waitingMarkTries counts the failed tries per mark, in memory. A restart
// starts the count again, which is fine: the mark only protects.
type waitingMarkTries struct {
	mu    sync.Mutex
	tries map[string]int
}

// failed counts one failed try and says whether that was the last one.
func (w *waitingMarkTries) failed(key string) (count int, last bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.tries == nil {
		w.tries = map[string]int{}
	}
	w.tries[key]++
	return w.tries[key], w.tries[key] == waitingMarkMaxTries
}

func (w *waitingMarkTries) givenUp(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.tries[key] >= waitingMarkMaxTries
}

func (w *waitingMarkTries) done(key string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.tries, key)
}

// waitingMarkPR is the tracked pull request a mark stands for. For a take
// over, the person's choice about the old labels was only in the lost
// record, so the pass keeps them (it takes nothing off). For a kept
// connection, it only hands the connection back (cleanup none).
func waitingMarkPR(key, cluster string, prID int) prtracker.PRInfo {
	pr := prtracker.PRInfo{PRID: prID, Cluster: cluster}
	switch key {
	case argosecrets.AnnotationAdoptPendingPR:
		pr.Operation = prtracker.OpAdoptCluster
		pr.FollowUp = &prtracker.ClusterFollowUp{Finish: prtracker.FinishAdopt}
	case argosecrets.AnnotationTakeoverPendingPR:
		pr.Operation = prtracker.OpAdoptCluster
		pr.FollowUp = &prtracker.ClusterFollowUp{Finish: prtracker.FinishTakeover, PreserveLegacyLabels: true}
	case argosecrets.AnnotationRemoveKeepPendingPR:
		pr.Operation = prtracker.OpRemoveCluster
		pr.FollowUp = &prtracker.ClusterFollowUp{Cleanup: "none", KeepConnection: true, CredentialKey: cluster}
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
	s.finishWaitingMarks(ctx, k8s, ns, git)
}

func (s *Server) finishWaitingMarks(ctx context.Context, k8s kubernetes.Interface, ns string, git gitprovider.GitProvider) {
	list, err := k8s.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{
		LabelSelector: argosecrets.LabelSecretType + "=cluster",
	})
	if err != nil {
		slog.Warn("waiting marks: could not list the ArgoCD cluster Secrets; Sharko tries again on the next pass",
			"namespace", ns, "error", err)
		return
	}
	for i := range list.Items {
		sec := &list.Items[i]
		cluster := argosecrets.ClusterNameOf(sec)
		for _, key := range argosecrets.PendingPRMarks {
			// Only a pull request number counts. "pending" is the value
			// before the pull request exists, while the request that
			// wrote it is still running.
			prID, convErr := strconv.Atoi(strings.TrimSpace(sec.Annotations[key]))
			if convErr != nil || prID <= 0 {
				continue
			}
			s.finishOneWaitingMark(ctx, git, key, cluster, prID)
		}
	}
}

func (s *Server) finishOneWaitingMark(ctx context.Context, git gitprovider.GitProvider, key, cluster string, prID int) {
	tryKey := key + "|" + cluster + "|" + strconv.Itoa(prID)
	if s.waitingMarks.givenUp(tryKey) {
		return
	}
	tracked, err := s.prTracker.GetPR(ctx, prID)
	if err != nil {
		slog.Warn("waiting marks: could not read the tracked pull requests; Sharko tries again on the next pass",
			"cluster", cluster, "mark", key, "pr", prID, "error", err)
		return
	}
	if tracked != nil {
		// The tracker still has it and finishes it itself.
		return
	}
	status, err := git.GetPullRequestStatus(ctx, prID)
	if err == nil && status != "merged" && status != "closed" {
		// Still open (or a state Sharko does not act on): leave the mark.
		return
	}
	if err == nil {
		err = s.FinishTrackedPR(ctx, waitingMarkPR(key, cluster, prID), status)
		if err == nil {
			s.waitingMarks.done(tryKey)
			slog.Info("waiting marks: finished a pull request the tracker no longer had",
				"cluster", cluster, "mark", key, "pr", prID, "status", status)
			return
		}
	}
	count, last := s.waitingMarks.failed(tryKey)
	if last {
		slog.Error("waiting marks: Sharko gave up finishing this pull request; the mark stays on the cluster's ArgoCD connection, so nothing deletes it — look at the pull request and the connection by hand",
			"cluster", cluster, "mark", key, "pr", prID, "tries", count, "error", err)
		return
	}
	slog.Warn("waiting marks: could not finish a pull request the tracker no longer had; Sharko tries again on the next pass",
		"cluster", cluster, "mark", key, "pr", prID, "tries", count, "error", err)
}
