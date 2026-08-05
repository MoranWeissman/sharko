package clusterreconciler

// check_pass.go — the READ-ONLY pass (P1-A A2).
//
// Until this file existed, the connection engine had exactly one pass, and
// that pass wrote: it created missing cluster secrets, deleted orphans,
// stamped labels and stripped annotations. The page's "Refresh" button fired
// that pass while its help text said it "checks". This is the other half of
// the pair, so the two words on the page can mean two different things:
//
//   Refresh — read git, read the live secrets, compare, write down what was
//             found. Nothing on any cluster changes. That is this file.
//   Sync    — act on what was found. That is the write pass (pollOnce), and
//             it is untouched.
//
// SCOPE LINE, on purpose: the 30-second loop and the post-merge trigger keep
// firing the WRITE pass exactly as before. That loop is the GitOps agent
// doing its job — continuously reconciling — and nothing here changes its
// behaviour. Only the human-driven button moved.
//
// The check reuses the write pass's own building blocks — readDesiredState,
// listManagedSecrets, desiredAddonLabels, labelsMatch, computeLabelDrift,
// recordReconcile — so a check and a repair can never disagree about what
// they are looking at. What the check does NOT reuse is every function whose
// name is a verb: createOne, deleteOne, selfHealManagedCluster,
// syncSelfManaged, syncConnectivityCheckLabel, clearRegistrationPending.
// None of them is called from this file, and none of them ever should be.

import (
	"context"
	"errors"

	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TriggerCheck requests an immediate READ-ONLY check pass. Never blocks: if
// a check is already queued, this call is a no-op (the pending one covers
// it). Safe to call before Start — the buffered channel holds the request
// until the goroutine drains it.
//
// Separate from Trigger() by design. Trigger() is the write nudge the PR
// tracker fires after a merge; this is the "somebody clicked Refresh" nudge.
// Wiring one to the other would put the page back where it started.
func (r *Reconciler) TriggerCheck() {
	select {
	case r.checkCh <- struct{}{}:
	default:
	}
}

// checkOnce is the check pass body — read, compare, record. It performs no
// create, update, patch or delete against Kubernetes. Only Get and List.
//
// Invoked via r.checkFn (per-instance test seam set up in New) for the same
// reason pollOnce is.
func (r *Reconciler) checkOnce(ctx context.Context) {
	log := logging.LoggerFromContext(ctx)

	// Same dependency preconditions as the write pass, minus the vault: a
	// check never builds a secret payload, so it never needs credentials.
	if r.deps.GitProvider == nil {
		log.Warn("[clusterreconciler] no GitProvider getter configured, skipping check")
		return
	}
	gp := r.deps.GitProvider()
	if gp == nil {
		log.Debug("[clusterreconciler] no active git provider, skipping check",
			"managed_clusters_path", r.managedClustersPath)
		return
	}
	if r.deps.ArgoClient == nil {
		log.Warn("[clusterreconciler] no ArgoClient (k8s clientset) configured, skipping check")
		return
	}

	// P2-D D1: same "only count a real attempt" stance pollOnce takes —
	// timing starts once every dependency precondition above has passed.
	start := r.now()

	// P2-C1: fetch the branch head SHA once for this check pass, same as
	// the write pass — this is what lets a Refresh click's rows say WHICH
	// commit they were just compared against.
	revision := r.fetchComparedRevision(ctx, gp)

	spec, v4, _, readPath, err := r.readDesiredState(ctx, gp)
	if err != nil {
		var rdErr *desiredStateReadError
		if !errors.As(err, &rdErr) {
			rdErr = &desiredStateReadError{Kind: desiredStateReadKindGit, Path: r.managedClustersPath, Err: err}
		}
		log.Error("[clusterreconciler] check pass could not read the desired state from git — nothing was compared",
			"path", rdErr.Path, "branch", r.branch, "kind", string(rdErr.Kind), "error", rdErr.Err)
		// Same stance the write pass takes on an aborted pass: every known
		// cluster's record is stamped so a reader sees the abort instead of
		// a stale success quietly ageing. (How that then RENDERS — "the
		// check failed" versus "these all drifted" — is lane P1-B's job for
		// both passes at once, not something to fork here.) stampAbortedTick
		// also clears the pass-compared facts (P2-C1) — an aborted check has
		// nothing honest to say about which commit it compared against.
		r.stampAbortedTick(string(rdErr.Kind) + " failed: " + rdErr.Err.Error())
		r.recordRunMetrics(engineClusterConnection, start, "failure", 0)
		r.recordStateGauges()
		return
	}
	r.setPassCompared(revision, readPath)

	existing, err := r.listManagedSecrets(ctx)
	if err != nil {
		log.Error("[clusterreconciler] check pass could not list the ArgoCD cluster secrets — nothing was compared",
			"namespace", r.namespace, "error", err)
		r.stampAbortedTick("listing ArgoCD cluster secrets failed: " + err.Error())
		r.recordRunMetrics(engineClusterConnection, start, "failure", 0)
		r.recordStateGauges()
		return
	}

	desired := make(map[string]models.ManagedClusterEntry)
	if spec != nil {
		for _, c := range spec.Clusters {
			if c.Name == "" {
				continue
			}
			desired[c.Name] = c
		}
	}

	for name, entry := range desired {
		desiredLabels := desiredAddonLabels(entry, v4.labelsFor(name))

		// A v4 repo whose assignment file for this cluster could not be read
		// this pass: Sharko does not know which addons should be on, so it
		// has no honest comparison to make. Say that, do not guess.
		if v4 != nil && !v4.desiredKnown(name) {
			r.recordReconcile(name, OutcomeSkipped,
				"Sharko couldn't read this cluster's addon assignment file in git, so it can't say whether the cluster secret matches.", nil)
			continue
		}

		if entry.UserManagedConnection() {
			// The user owns this secret, so it never carries Sharko's label
			// and never appears in the listManagedSecrets result — read it
			// by name instead. An unlabeled secret here is the normal,
			// expected shape, not a boundary.
			r.checkOneSecret(name, desiredLabels, r.getSecretForCheck(ctx, name), true, v4 != nil)
			continue
		}

		secret, present := existing[name]
		if !present {
			// Not one of Sharko's. Either nothing is there at all, or a
			// same-name secret exists that somebody else created.
			r.checkOneSecret(name, desiredLabels, r.getSecretForCheck(ctx, name), false, v4 != nil)
			continue
		}
		r.recordCompared(name, desiredLabels, secret, v4 != nil)
	}

	// Prune against the same union the write pass uses (desired plus what
	// was observed live), so a cluster nobody knows about any more stops
	// being reported. Record bookkeeping only — no cluster is touched.
	known := make(map[string]struct{}, len(desired)+len(existing))
	for name := range desired {
		known[name] = struct{}{}
	}
	for name := range existing {
		known[name] = struct{}{}
	}
	r.pruneStaleReconcileRecords(known)

	log.Info("[clusterreconciler] check pass complete — nothing was written",
		"namespace", r.namespace, "clusters_in_git", len(desired), "sharko_owned_secrets_live", len(existing))

	// P2-D D1: a completed check pass never writes, so it has no "partial"
	// concept the way the write pass does (stats.Errors) — reaching this
	// line means the pass read git and listed the live secrets successfully,
	// so "success" always applies here. A per-cluster read failure inside
	// checkOneSecret still shows up as that cluster's own OutcomeFailed
	// record (and so in sharko_managed_secrets_state's "unknown" bucket and
	// sharko_reconciler_item_failures_total) without downgrading the whole
	// pass's outcome — the same relationship stats.Errors has to pollOnce's
	// "partial" outcome.
	r.recordRunMetrics(engineClusterConnection, start, "success", len(desired))
	r.recordStateGauges()
}

// secretReadResult is what one read-by-name came back with: the secret, or
// the reason there isn't one.
type secretReadResult struct {
	secret   *corev1.Secret
	notFound bool
	err      error
}

// getSecretForCheck reads one cluster secret by name. A read, never a write.
func (r *Reconciler) getSecretForCheck(ctx context.Context, name string) secretReadResult {
	secret, err := r.deps.ArgoClient.CoreV1().Secrets(r.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return secretReadResult{notFound: true}
		}
		return secretReadResult{err: err}
	}
	return secretReadResult{secret: secret}
}

// checkOneSecret turns one read-by-name into one honest record.
//
// selfManaged flips two of the answers: the user owns that secret, so its
// absence is "waiting for you to create it" rather than "Sharko hasn't
// created it", and the missing ownership label is the expected shape rather
// than somebody else's secret in Sharko's way.
func (r *Reconciler) checkOneSecret(name string, desiredLabels map[string]string, read secretReadResult, selfManaged, v4Repo bool) {
	switch {
	case read.err != nil:
		r.recordReconcile(name, OutcomeFailed,
			"Sharko couldn't read this cluster's ArgoCD secret to check it: "+read.err.Error(), nil)
	case read.notFound && selfManaged:
		r.recordReconcile(name, OutcomeSkipped, SelfManagedSecretNotCreatedMessage, nil)
	case read.notFound:
		r.recordReconcile(name, OutcomeSkipped, ManagedSecretNotCreatedMessage, nil)
	case !selfManaged && !IsManagedBySharko(read.secret):
		r.recordReconcile(name, OutcomeSkipped, UnlabeledSecretExistsMessage, nil)
	default:
		r.recordCompared(name, desiredLabels, read.secret, v4Repo)
	}
}

// recordCompared writes down the result of comparing one live secret's addon
// labels against what git asks for — the exact comparison the write pass
// makes before it decides whether to repair anything, minus the repair.
func (r *Reconciler) recordCompared(name string, desiredLabels map[string]string, secret *corev1.Secret, v4Repo bool) {
	inSync := labelsMatch(desiredLabels, secret.Labels)
	if inSync && v4Repo && hasStaleV4AddonLabels(desiredLabels, secret.Labels) {
		inSync = false
	}
	if inSync {
		r.recordReconcile(name, OutcomeSucceeded, "cluster Secret present; labels verified", nil)
		return
	}
	r.recordReconcile(name, OutcomeSucceeded, "cluster Secret present",
		computeLabelDrift(desiredLabels, secret.Labels, annotationsOf(secret)))
}
