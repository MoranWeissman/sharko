package clusterreconciler

// connection_drift_notice.go — R3-5, so a person learns a connection drifted
// without opening a page.
//
// # What this detects, and why it is free
//
// The pass ALREADY holds everything this needs. listManagedSecrets fetched every
// Sharko-owned connection Secret at the top of the pass, and desiredAddonLabels
// computed what git wants. So the checks below run over data that is already in
// memory: no extra Kubernetes call, no git call, and — this is the important one
// — no secrets-backend call.
//
// That last point is why the checks are the ones they are. The tempting check is
// "does data.server still match the backend?", and it is exactly the one that
// cannot go here: answering it means a stored-facts read per cluster per pass. On
// a fleet of two hundred clusters at the 30-second tick that is 200 backend reads
// every 30 seconds, roughly 576,000 a day, for a question a person asks a few
// times a week. The zero-mint guarantee would hold (the stored-facts read never
// mints), but the cost and the noise would not be worth it, and a backend hiccup
// would start writing check_failed records for clusters nobody asked about.
//
// So this detects the drift that is visible for free — a connection that is
// structurally broken or has lost its ownership marker — and the full
// field-by-field comparison stays where a person asks for it, on the endpoint.
// What this gives up is honestly named in driftNoticeUncheckedNote.
//
// # It only ever reads
//
// Nothing here writes. There is no Secret write, no git write, and no repair on
// any path — a background pass that repaired on its own is not what was asked
// for, and self-heal already owns that decision separately, under its own
// setting. This records a fact and emits at most one Kubernetes event per drift
// episode.
//
// # check_failed stays check_failed
//
// This never touches a cluster whose record says the pass could not finish. A
// cluster Sharko could not read is not a cluster Sharko can call drifted, and it
// is certainly not one Sharko can call fine. Only a cluster whose Secret was
// genuinely read this pass is examined.

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/events"
	"github.com/MoranWeissman/sharko/internal/logging"
)

// driftNoticeUncheckedNote says plainly what the background pass does NOT check,
// so a quiet background pass is never mistaken for a clean full comparison.
//
// It is a fixed sentence and carries nothing derived from any value.
const driftNoticeUncheckedNote = "Sharko's background pass checks that this connection is structurally intact and still Sharko's. It does not compare the stored sign-in details on a timer — open the cluster's connection check for that."

// ConnectionShapeProblem is one thing wrong with a connection Secret that was
// visible without reading anything extra.
type ConnectionShapeProblem string

const (
	// ShapeProblemOwnershipLabelMissing — the Secret is one Sharko is supposed
	// to own, and the ownership marker is gone. Something stripped it. Left
	// alone it drops out of listManagedSecrets on a later pass and stops being
	// reconciled at all, silently.
	ShapeProblemOwnershipLabelMissing ConnectionShapeProblem = "ownership_label_missing"

	// ShapeProblemSecretTypeLabelMissing — ArgoCD selects cluster Secrets on
	// argocd.argoproj.io/secret-type=cluster. Without it ArgoCD does not see
	// the connection at all, however correct the rest of it is.
	ShapeProblemSecretTypeLabelMissing ConnectionShapeProblem = "secret_type_label_missing"

	// ShapeProblemConnectionDataKeyMissing — one of the three data keys a
	// connection needs (name, server, config) is absent. ArgoCD cannot use the
	// connection.
	ShapeProblemConnectionDataKeyMissing ConnectionShapeProblem = "connection_data_key_missing"

	// ShapeProblemNameMismatch — data.name is not the cluster's name. ArgoCD
	// keys its own view on that value, so a mismatch makes the connection
	// answer to a different name than the one Sharko manages.
	ShapeProblemNameMismatch ConnectionShapeProblem = "connection_name_mismatch"
)

// connectionDriftState is the per-cluster record of whether a drift notice has
// already been given for the CURRENT episode.
//
// In memory only, same stance as every other per-cluster fact in this package: a
// restart just means the next pass re-notices, which delays nothing that matters.
type connectionDriftState struct {
	// problems is the sorted problem set the last pass saw, so a drift that
	// CHANGES (a second thing breaks) is treated as a new episode and gets its
	// own event rather than being silenced by the first one.
	problems string
	// eventEmitted is true once an event has gone out for this exact problem
	// set. Cleared when the connection comes good again, so a later episode
	// gets its own event.
	eventEmitted bool
}

// detectConnectionShapeDrift examines one already-fetched connection Secret and
// returns what is wrong with it, sorted. Empty means nothing visible is wrong.
//
// Pure: no client, no network, no clock. It reads the object it is handed and
// nothing else, which is what makes it free to run on every cluster every pass.
func detectConnectionShapeDrift(name string, secret *corev1.Secret) []ConnectionShapeProblem {
	if secret == nil {
		return nil
	}
	var problems []ConnectionShapeProblem

	if secret.Labels[argosecrets.LabelManagedBy] != argosecrets.ManagedByValue {
		problems = append(problems, ShapeProblemOwnershipLabelMissing)
	}
	if secret.Labels[argosecrets.LabelSecretType] != "cluster" {
		problems = append(problems, ShapeProblemSecretTypeLabelMissing)
	}
	for _, key := range []string{"name", "server", "config"} {
		if len(secret.Data[key]) == 0 {
			problems = append(problems, ShapeProblemConnectionDataKeyMissing)
			break
		}
	}
	if live := string(secret.Data["name"]); live != "" && live != name {
		problems = append(problems, ShapeProblemNameMismatch)
	}

	sort.Slice(problems, func(i, j int) bool { return problems[i] < problems[j] })
	return problems
}

// noticeConnectionShapeDrift records what the pass saw about one connection's
// shape and emits at most one Kubernetes event per drift episode.
//
// READ-ONLY. It writes nothing to the cluster and never repairs. An adopted or
// self-managed connection is skipped entirely — the ownership and secret-type
// labels are deliberately absent on a guest connection, so reporting them as
// drift would be reporting Sharko's own correct behaviour as a fault.
func (r *Reconciler) noticeConnectionShapeDrift(ctx context.Context, name string, secret *corev1.Secret, selfManaged bool) {
	if secret == nil {
		return
	}
	if selfManaged || argosecrets.IsAdopted(secret.Annotations) {
		// Guest connection: Sharko does not stamp these labels and does not own
		// the connection data, so none of the checks above apply.
		r.clearConnectionDriftNotice(name)
		return
	}

	problems := detectConnectionShapeDrift(name, secret)
	if len(problems) == 0 {
		r.clearConnectionDriftNotice(name)
		return
	}

	key := fmt.Sprint(problems)

	r.driftNoticeMu.Lock()
	if r.driftNotice == nil {
		r.driftNotice = make(map[string]connectionDriftState)
	}
	state := r.driftNotice[name]
	newEpisode := state.problems != key
	if newEpisode {
		state = connectionDriftState{problems: key}
	}
	shouldEmit := !state.eventEmitted
	if shouldEmit {
		state.eventEmitted = true
	}
	r.driftNotice[name] = state
	r.driftNoticeMu.Unlock()

	if !shouldEmit {
		return
	}

	logging.LoggerFromContext(ctx).Warn(
		"[clusterreconciler] this cluster's ArgoCD connection has drifted from the shape Sharko writes — nothing was changed; a repair is a separate, asked-for action",
		"cluster", name, "namespace", r.namespace, "problems", problems)

	if r.eventRecorder != nil {
		r.eventRecorder.Eventf(
			events.ReasonDriftDetected,
			"Cluster %s: its ArgoCD connection no longer looks the way Sharko writes it (%s). Sharko changed nothing. Run the connection check to see the detail, and repair it from there. %s",
			events.EventTypeWarning,
			name, problemSentence(problems), driftNoticeUncheckedNote,
		)
	}
}

// problemSentence renders the problem set in plain words. Fixed phrases only —
// nothing here is derived from a value on the Secret.
func problemSentence(problems []ConnectionShapeProblem) string {
	words := make([]string, 0, len(problems))
	for _, p := range problems {
		switch p {
		case ShapeProblemOwnershipLabelMissing:
			words = append(words, "its Sharko ownership label is gone")
		case ShapeProblemSecretTypeLabelMissing:
			words = append(words, "ArgoCD's cluster-Secret label is gone, so ArgoCD cannot see it")
		case ShapeProblemConnectionDataKeyMissing:
			words = append(words, "part of the connection detail is missing")
		case ShapeProblemNameMismatch:
			words = append(words, "the name inside the connection is not this cluster's name")
		}
	}
	if len(words) == 0 {
		return "no detail"
	}
	out := words[0]
	for _, w := range words[1:] {
		out += "; " + w
	}
	return out
}

// clearConnectionDriftNotice forgets a cluster's drift-notice state, so a later
// drift episode gets its own event.
//
// Called when a pass sees a healthy connection and when a repair succeeds — the
// drift a repair just corrected is not drift any more.
func (r *Reconciler) clearConnectionDriftNotice(name string) {
	if r == nil {
		return
	}
	r.driftNoticeMu.Lock()
	delete(r.driftNotice, name)
	r.driftNoticeMu.Unlock()
}

// ConnectionShapeProblems reports what this server last noticed about one
// cluster's connection shape, for a caller that wants to show it. Empty means
// nothing is wrong, or no pass has looked yet.
func (r *Reconciler) ConnectionShapeProblems(name string) string {
	if r == nil {
		return ""
	}
	r.driftNoticeMu.Lock()
	defer r.driftNoticeMu.Unlock()
	return r.driftNotice[name].problems
}
