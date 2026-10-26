package clusterreconciler

// token_renewal.go — keep eks-token connections alive (ep4 serious 1).
//
// An eks-token connection is written with a bearer token Sharko makes at
// write time (ConnectionCredentialSpecForWrite). That token lasts 15
// minutes. createOne only runs for a MISSING Secret, so before this file a
// Secret that already existed kept its first token forever, and ArgoCD lost
// the cluster 15 minutes after every write.
//
// The rules this file keeps:
//
//   - It runs only from the normal write pass (reconcileDiff). The check
//     pass, every read path and every API answer never reach it.
//   - It mints only for a cluster whose git record says credsSource
//     eks-token, that is not self-managed, and whose live Secret is labelled
//     as Sharko's (exactly) and is not adopted.
//   - It mints only when the live token is about 10 minutes old or more, read
//     from the date inside the token itself (so a restart changes nothing),
//     and only when the live config is exactly the bearerToken shape Sharko
//     writes. Anything else is not a renewal; it is left alone.
//   - One mint per renewal. A failed renewal waits renewalRetryAfter before
//     the next try, so a broken backend is not asked every 30 seconds.
//   - The token, and every error from the backend or from reading the token,
//     never reaches a log line, an audit entry, a reconcile record or an
//     answer. Errors are logged by fixed class only.
//   - Only data.config changes, through argosecrets.RenewOwnedBearerToken,
//     which re-reads the Secret and checks ownership again right before the
//     Update.
//   - A renewal is not a repair: it writes no "repaired" activity line.
//   - A failed renewal marks the cluster (TokenRenewalFailing), and the
//     cluster list shows the connection as failing instead of "Connected".

import (
	"context"
	"errors"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/logging"
	"github.com/MoranWeissman/sharko/internal/models"
)

const (
	// renewTokenAfter is how old an EKS token may get before the write pass
	// replaces it. Tokens last 15 minutes; 10 leaves room for a missed tick
	// or two and a retry.
	renewTokenAfter = 10 * time.Minute

	// renewalRetryAfter is how long a failed renewal waits before the next
	// try.
	renewalRetryAfter = 2 * time.Minute
)

// TokenRenewalFailedSentence is what the cluster's last-reconcile message
// says while a renewal is failing. Fixed text — no error words ever.
const TokenRenewalFailedSentence = "Sharko could not get a new sign-in token for this cluster, so ArgoCD will lose its connection to it soon. Check that Sharko can reach your secrets provider and AWS, then click Refresh."

// tokenRenewalState is one cluster's renewal trouble. It exists only while a
// renewal is failing.
type tokenRenewalState struct {
	since     time.Time
	nextRetry time.Time
}

// TokenRenewalFailing reports whether the last token renewal for this
// cluster failed and no renewal has worked since.
func (r *Reconciler) TokenRenewalFailing(name string) bool {
	if r == nil {
		return false
	}
	r.tokenRenewalMu.Lock()
	defer r.tokenRenewalMu.Unlock()
	_, failing := r.tokenRenewal[name]
	return failing
}

func (r *Reconciler) clearTokenRenewalTrouble(name string) {
	r.tokenRenewalMu.Lock()
	defer r.tokenRenewalMu.Unlock()
	delete(r.tokenRenewal, name)
}

// markTokenRenewalTrouble records a failed renewal and returns true when this
// is the first failure of a new run of failures.
func (r *Reconciler) markTokenRenewalTrouble(name string, now time.Time) bool {
	r.tokenRenewalMu.Lock()
	defer r.tokenRenewalMu.Unlock()
	if r.tokenRenewal == nil {
		r.tokenRenewal = make(map[string]tokenRenewalState)
	}
	prev, had := r.tokenRenewal[name]
	since := now
	if had {
		since = prev.since
	}
	r.tokenRenewal[name] = tokenRenewalState{since: since, nextRetry: now.Add(renewalRetryAfter)}
	return !had
}

func (r *Reconciler) tokenRenewalWaiting(name string, now time.Time) bool {
	r.tokenRenewalMu.Lock()
	defer r.tokenRenewalMu.Unlock()
	st, had := r.tokenRenewal[name]
	return had && now.Before(st.nextRetry)
}

// renewEKSTokenIfOld is called by reconcileDiff for every cluster that is in
// git and has a Sharko-labelled Secret. It does nothing unless every rule in
// the file comment holds.
func (r *Reconciler) renewEKSTokenIfOld(ctx context.Context, entry models.ManagedClusterEntry, live *corev1.Secret) {
	name := entry.Name
	if entry.CredsSource != models.CredsSourceEKSToken || entry.UserManagedConnection() ||
		live == nil || live.Labels[argosecrets.LabelManagedBy] != argosecrets.ManagedByValue ||
		argosecrets.IsAdopted(live.Annotations) {
		r.clearTokenRenewalTrouble(name)
		return
	}
	oldToken, shapeOK, issuedAt, dateOK := argosecrets.EKSBearerTokenFacts(live.Data["config"])
	if !shapeOK {
		// Not the shape Sharko writes for an EKS token (a cert pair, an exec
		// config, a hand edit). Not a renewal; Repair owns that.
		r.clearTokenRenewalTrouble(name)
		return
	}
	now := r.now()
	if dateOK && now.Sub(issuedAt) < renewTokenAfter {
		r.clearTokenRenewalTrouble(name)
		return
	}
	if r.tokenRenewalWaiting(name, now) {
		return
	}

	log := logging.LoggerFromContext(ctx)
	fail := func(class string, reason audit.Reason) {
		// class is a fixed word from this file. No error value is logged:
		// the backend's words and anything parsed out of a token stay here.
		log.Warn("[clusterreconciler] connection token renewal failed — nothing was written",
			"cluster", name, "namespace", r.namespace, "stage", class)
		if r.markTokenRenewalTrouble(name, now) {
			r.audit(audit.Entry{
				Level:     "warn",
				Event:     "cluster_connection_token_renewal_failed",
				User:      "sharko",
				Action:    "renew_connection_token",
				Resource:  "cluster:" + name,
				Source:    "reconciler",
				Result:    "failure",
				Changes:   audit.ChangesNone,
				Reason:    reason,
				RequestID: logging.RequestID(ctx),
			})
		}
	}

	// The one mint.
	spec, err := r.ConnectionCredentialSpecForWrite(entry)
	if err != nil {
		fail("get_credentials", audit.ReasonCredentials)
		return
	}
	if spec.Token == "" || spec.Server != string(live.Data["server"]) {
		// The backend now describes a different connection. That is drift
		// for Repair, not a token renewal.
		fail("connection_changed", audit.ReasonInvalidData)
		return
	}
	// Prove only the token differs: the same spec carrying the OLD token must
	// build exactly the live config.
	oldSpec := spec
	oldSpec.Token = oldToken
	expected, err := argosecrets.BuildSecretConfigJSON(oldSpec)
	if err != nil || expected != string(live.Data["config"]) {
		fail("connection_changed", audit.ReasonInvalidData)
		return
	}
	fresh, err := argosecrets.BuildSecretConfigJSON(spec)
	if err != nil {
		fail("build_config", audit.ReasonInvalidData)
		return
	}
	mgr := argosecrets.NewManager(r.deps.ArgoClient, r.namespace)
	if err := mgr.RenewOwnedBearerToken(ctx, name, expected, fresh); err != nil {
		stage := "write"
		switch {
		case errors.Is(err, argosecrets.ErrRepairOwnershipChanged):
			stage = "ownership_changed"
		case errors.Is(err, argosecrets.ErrRenewalConfigChanged),
			errors.Is(err, argosecrets.ErrRepairSecretChangedUnderneath):
			stage = "changed_underneath"
		case errors.Is(err, argosecrets.ErrRepairSecretMissing):
			stage = "secret_missing"
		}
		fail(stage, audit.Classify(err))
		return
	}

	recordWrite("updated")
	r.clearTokenRenewalTrouble(name)
	log.Info("[clusterreconciler] connection token renewed",
		"cluster", name, "namespace", r.namespace)
}
