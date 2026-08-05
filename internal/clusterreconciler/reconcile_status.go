package clusterreconciler

import (
	"fmt"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/metrics"
)

// reconcile_status.go — per-cluster reconcile visibility (V2-cleanup-89.4).
//
// Before this, a reconcile failure for a single cluster (a vault fetch
// error, a K8s API rejection, a self-managed connection still waiting on
// the user) went to slog + the audit log only. An operator looking at one
// cluster had no way to tell whether the last reconcile attempt for THAT
// cluster succeeded, failed, or was deliberately skipped — ArgoCD shows a
// failed apply; Sharko showed nothing. This file adds an in-memory,
// thread-safe last-outcome record per cluster name, and a getter the API
// layer uses to project it onto the cluster read model
// (models.Cluster.LastReconcile).
//
// Deliberately in-memory only, never persisted: this is derived,
// self-healing state exactly like the reconcile loop itself — every tick
// recomputes the outcome for every cluster it touches, so a server
// restart losing the history is a non-issue (the next tick, at most
// DefaultTickInterval later, repopulates it).

// ReconcileOutcome classifies the result of the most recent reconcile
// attempt for a single cluster.
type ReconcileOutcome string

const (
	// OutcomeSucceeded means the cluster's ArgoCD cluster Secret is in sync
	// with managed-clusters.yaml as of this tick — created just now,
	// already up to date from a previous tick, or (self-managed
	// connections) addon labels synced onto the user's Secret.
	OutcomeSucceeded ReconcileOutcome = "succeeded"

	// OutcomeFailed means this tick attempted a write for this cluster and
	// it failed — a vault credential fetch, building the Secret payload, or
	// the ArgoCD cluster Secret K8s API call itself.
	OutcomeFailed ReconcileOutcome = "failed"

	// OutcomeSkipped means this tick deliberately did not write anything
	// for this cluster — not an error, but not "in sync and done" either.
	// Covers: an unlabeled same-name Secret already exists (Adopt
	// territory), or a self-managed connection's Secret hasn't been
	// created by the user yet.
	OutcomeSkipped ReconcileOutcome = "skipped"
)

// LabelDrift holds the concrete label difference between git-desired and
// live cluster-secret labels for a Sharko-managed cluster (V3 G1 — drift
// detection for the managed path). nil when labels are in sync, or when the
// cluster is self-managed (user owns the Secret — syncSelfManaged already
// self-heals labels every tick, no OutOfSync concept needed there).
//
// Only Sharko's own addon-label keys are compared (same scope as
// desiredAddonLabels + the labelsMatch check at reconciler.go:610) —
// ownership bookkeeping labels (managed-by, connectivity-check) and other
// unrelated labels are excluded by design.
type LabelDrift struct {
	// Added: keys present in git-desired but missing from live Secret
	Added []string `json:"added,omitempty"`
	// Removed: keys present on live Secret but missing from git-desired
	Removed []string `json:"removed,omitempty"`
	// Changed: keys present in both but with differing values
	Changed []string `json:"changed,omitempty"`
}

// ClusterReconcileRecord is the last known reconcile outcome for one
// cluster. Message is a plain-English explanation of what happened —
// populated on Failed and Skipped, empty on Succeeded UNLESS label-fight
// detection (V2-cleanup-89.5) has found a sustained revert pattern on a
// self-managed connection's Secret: that case stays Succeeded (Sharko is
// still successfully re-applying its labels every tick) but Message
// carries the fight warning — see recordFightCheck.
//
// LabelDrift (V3 G1) carries the git-vs-live label comparison for
// Sharko-managed clusters; nil when labels are in sync or for self-managed
// connections (which already self-heal every tick).
type ClusterReconcileRecord struct {
	Time       time.Time
	Outcome    ReconcileOutcome
	Message    string
	LabelDrift *LabelDrift

	// ComparedRevision (P2-C1) is the branch head commit SHA the PASS that
	// produced this record read git at — fetched once per pass (pollOnce
	// or checkOnce), never per cluster, since every cluster processed in
	// the same pass reads the same managed-clusters file at the same git
	// state. Empty when the active provider does not implement
	// gitprovider.BranchRevisioner, or the call failed, or the pass
	// aborted before reaching any per-cluster work — never a guessed or
	// stale value (see Reconciler.fetchComparedRevision).
	ComparedRevision string
	// ComparedPath (P2-C1) is the exact managed-clusters file path this
	// pass read from git: DefaultManagedClustersPath (v3) or
	// V4ManagedClustersPath (v4) — see readDesiredState's v3→v4 fallback.
	// Same pass-level, not per-cluster, reasoning as ComparedRevision.
	ComparedPath string
	// AppliedRevision (P2-C1) is the branch head commit SHA the LAST
	// SUCCESSFUL WRITE to this cluster's ArgoCD secret was built from —
	// the "applied" half of the generation/observedGeneration pair.
	// Stamped ONLY at the moment a real Kubernetes write succeeds
	// (createOne, a changed selfHealManagedCluster, a changed
	// syncSelfManaged); a check pass, a no-op tick, or a failed write
	// carries the PREVIOUS value forward untouched — see
	// Reconciler.stampAppliedRevision. Empty until this server instance
	// has ever successfully written this cluster's secret.
	AppliedRevision string
}

// recordReconcile stores the outcome of the most recent reconcile attempt
// for a single cluster, overwriting any previous record. Uses the
// reconciler's clock seam (r.now()) rather than time.Now() directly so
// tests can drive a deterministic timestamp the same way they do for the
// registration-pending grace window.
//
// drift (V3 G1) carries the label difference for Sharko-managed clusters;
// nil when labels are in sync or for self-managed connections.
func (r *Reconciler) recordReconcile(name string, outcome ReconcileOutcome, message string, drift *LabelDrift) {
	// Read the pass-level compared facts and this cluster's persisted
	// applied-revision BEFORE taking lastReconcileMu — currentPassCompared
	// and appliedRevisionFor each take their own, separate lock (P2-C1).
	comparedRevision, comparedPath := r.currentPassCompared()
	appliedRevision := r.appliedRevisionFor(name)

	r.lastReconcileMu.Lock()
	defer r.lastReconcileMu.Unlock()
	if r.lastReconcile == nil {
		r.lastReconcile = make(map[string]ClusterReconcileRecord)
	}
	r.lastReconcile[name] = ClusterReconcileRecord{
		Time:             r.now(),
		Outcome:          outcome,
		Message:          message,
		LabelDrift:       drift,
		ComparedRevision: comparedRevision,
		ComparedPath:     comparedPath,
		AppliedRevision:  appliedRevision,
	}
	// P2-D D2: recordReconcile is the single choke point every Failed
	// outcome in this package passes through — whether it is one cluster's
	// own write failing, or every known cluster being stamped at once by
	// stampAbortedTick — so it is also the single place to count per-item
	// failures. reason is a small fixed set (classifyFailureReason), never
	// the raw message text, so this counter's cardinality never grows with
	// the variety of underlying errors.
	if outcome == OutcomeFailed {
		metrics.ReconcilerItemFailures.WithLabelValues(engineClusterConnection, classifyFailureReason(message)).Inc()
	}
}

// classifyFailureReason buckets a ClusterReconcileRecord's Message into the
// small fixed reason vocabulary sharko_reconciler_item_failures_total uses
// (P2-D D2). Mirrors FailureSentence's own stage-matching order and prefixes
// exactly (failure_sentence.go) — same classification, a short metric-label
// code instead of a sentence for a person.
func classifyFailureReason(msg string) string {
	switch {
	case strings.Contains(msg, "git_read failed"), strings.Contains(msg, "git read failed"):
		return "git_read"
	case strings.Contains(msg, "schema_validation failed"), strings.Contains(msg, "schema validation failed"):
		return "schema_validation"
	case strings.Contains(msg, "reconciler pass aborted"):
		return "cluster_unreachable"
	case strings.Contains(msg, "credentials"):
		return "credentials"
	case strings.Contains(msg, "check whether an ArgoCD cluster secret already exists"),
		strings.Contains(msg, "read this cluster's ArgoCD secret to check it"),
		strings.Contains(msg, "re-read the cluster Secret"):
		return "cluster_unreachable"
	case strings.Contains(msg, "couldn't build"),
		strings.Contains(msg, "couldn't create"),
		strings.Contains(msg, "couldn't sync"),
		strings.Contains(msg, "couldn't converge"),
		strings.Contains(msg, "couldn't remove"):
		return "write_failed"
	default:
		return "other"
	}
}

// LastReconcile returns the most recently recorded reconcile outcome for
// the named cluster. ok is false when no reconcile has ever run for this
// cluster on this server instance — a fresh startup before the first tick,
// or a cluster whose registration PR hasn't merged into
// managed-clusters.yaml yet. Safe to call concurrently with the reconcile
// goroutine; this is the read side the API layer polls per request.
func (r *Reconciler) LastReconcile(name string) (ClusterReconcileRecord, bool) {
	r.lastReconcileMu.RLock()
	defer r.lastReconcileMu.RUnlock()
	rec, ok := r.lastReconcile[name]
	return rec, ok
}

// TickInterval returns the reconciler's configured poll cadence — how often
// the background loop ticks, independent of Trigger() nudges (System-page
// managed-secrets summary).
func (r *Reconciler) TickInterval() time.Duration {
	return r.tickInterval
}

// LastRunTime returns the most recent timestamp recorded across every
// per-cluster reconcile record. Every cluster touched in the same tick gets
// the same r.now() stamp (see recordReconcile), so this is exactly the last
// full pass's timestamp whenever at least one cluster has been reconciled
// on this server instance. Returns the zero time when no reconcile has ever
// recorded a per-cluster outcome (fresh startup, or the reconciler has
// never processed any cluster).
func (r *Reconciler) LastRunTime() time.Time {
	r.lastReconcileMu.RLock()
	defer r.lastReconcileMu.RUnlock()
	var latest time.Time
	for _, rec := range r.lastReconcile {
		if rec.Time.After(latest) {
			latest = rec.Time
		}
	}
	return latest
}

// recordWrite marks one actual Kubernetes write this pass made (P2-D
// D1/D2) — kind is "created" | "updated" | "deleted". Bumps both the
// legacy-shaped items-changed counter (declared since before this lane,
// never written until now) and the new writes_total counter, which share
// the same (engine, kind) shape on purpose — every write call site in this
// package calls this exactly once per actual write, right next to the
// existing reconcileStats counter it already bumped.
func recordWrite(kind string) {
	metrics.ReconcilerItemsChanged.WithLabelValues(engineClusterConnection, kind).Inc()
	metrics.ReconcilerWrites.WithLabelValues(engineClusterConnection, kind).Inc()
}

// recordRunMetrics is the single point both pollOnce (write pass) and
// checkOnce (check pass) call at the end of a completed run — abort branches
// included, with itemsChecked 0 (P2-D D1). engine is always
// engineClusterConnection for this package; taken as a parameter (rather
// than hardcoded) only so a test can call this against a throwaway engine
// label without polluting the real one's series.
//
// outcome is "success" | "partial" | "failure" — never anything else, kept
// tiny per the sprint plan's label-cardinality rule.
func (r *Reconciler) recordRunMetrics(engine string, start time.Time, outcome string, itemsChecked int) {
	metrics.ReconcilerRuns.WithLabelValues(engine, outcome).Inc()
	metrics.ReconcilerDuration.WithLabelValues(engine).Observe(r.now().Sub(start).Seconds())
	now := float64(r.now().Unix())
	metrics.ReconcilerLastRun.WithLabelValues(engine).Set(now)
	if outcome == "success" {
		metrics.ReconcilerLastSuccess.WithLabelValues(engine).Set(now)
	}
	if itemsChecked > 0 {
		metrics.ReconcilerItemsChecked.WithLabelValues(engine).Add(float64(itemsChecked))
	}
}

// managedSecretsMetricStates is every state sharko_managed_secrets_state can
// report for this engine (P2-D D1/D4) — written every pass, in full, so a
// state that just emptied out reports 0 instead of leaving a stale nonzero
// value behind. Matches the vocabulary internal/api's connectionSecretState
// renders per row.
var managedSecretsMetricStates = []string{"in_sync", "out_of_sync", "missing", "unknown"}

// metricStateFromRecord buckets one cluster's reconcile record into the
// sharko_managed_secrets_state vocabulary (P2-D D1). Deliberately mirrors
// internal/api/system_managed_secrets.go's connectionSecretState rule
// (self-managed "not created yet" -> missing; succeeded + no drift ->
// in_sync; succeeded + drift -> out_of_sync; anything else -> unknown) —
// api cannot be imported here (it imports this package), so the two are
// kept in sync by hand; a future package split could hoist the shared rule
// out from under both, out of scope for this lane.
func metricStateFromRecord(rec ClusterReconcileRecord) string {
	switch {
	case rec.Outcome == OutcomeSkipped && rec.Message == SelfManagedSecretNotCreatedMessage:
		return "missing"
	case rec.Outcome == OutcomeSucceeded && rec.LabelDrift == nil:
		return "in_sync"
	case rec.Outcome == OutcomeSucceeded:
		return "out_of_sync"
	default:
		return "unknown"
	}
}

// recordStateGauges recomputes sharko_managed_secrets_state from the
// CURRENT full lastReconcile map (every cluster this server instance
// currently knows about, not just the clusters this one pass touched) and
// sets every known state bucket (P2-D D1/D4).
func (r *Reconciler) recordStateGauges() {
	r.lastReconcileMu.RLock()
	counts := make(map[string]int, len(managedSecretsMetricStates))
	for _, rec := range r.lastReconcile {
		counts[metricStateFromRecord(rec)]++
	}
	r.lastReconcileMu.RUnlock()

	for _, state := range managedSecretsMetricStates {
		metrics.ManagedSecretsState.WithLabelValues(engineClusterConnection, state).Set(float64(counts[state]))
	}
}

// fightGaugeThreshold is the "is this item worth surfacing as a fight"
// bar (P2-D D3) — the row warning in ui/src/views/ManagedSecrets.tsx and
// sharko_reconciler_fights both use this same number. Deliberately
// SEPARATE from fightRevertThreshold (2): that constant decides when
// recordFightCheck's internal warning text starts (a lower, earlier bar,
// unchanged by this lane per the "no backoff/detection changes" scope
// guard); this one decides when a fight is worth a person's attention on
// the page and in Grafana.
const fightGaugeThreshold = 3

// FightCount returns the number of CONSECUTIVE reverted ticks currently
// recorded for the named self-managed cluster's label-fight detector
// (P2-D D3) — the same counter recordFightCheck advances, exposed for the
// managed-secrets API response's fight_count field. 0 when the cluster has
// no fight-tracking state at all (never a self-managed connection, or no
// tick has run for it yet).
func (r *Reconciler) FightCount(name string) int {
	r.fightMu.Lock()
	defer r.fightMu.Unlock()
	return r.fightState[name].reverts
}

// recordFightGauge recomputes sharko_reconciler_fights from the current
// fightState map (P2-D D3) — the count of clusters whose revert streak has
// reached fightGaugeThreshold or more.
func (r *Reconciler) recordFightGauge() {
	r.fightMu.Lock()
	count := 0
	for _, state := range r.fightState {
		if state.reverts >= fightGaugeThreshold {
			count++
		}
	}
	r.fightMu.Unlock()
	metrics.ReconcilerFights.WithLabelValues(engineClusterConnection).Set(float64(count))
}

// LastError returns the cluster name, message, and timestamp of the most
// recent Failed per-cluster reconcile record. ok is false when no cluster
// currently has a Failed outcome recorded (a clean fleet, or no reconcile
// has run yet). This is a real, grounded signal — the same per-cluster
// failure a cluster's own detail page would show — not an engine-wide abort
// reason (the reconciler doesn't track one separately; see
// stampAbortedTick, which stamps the SAME per-cluster records this reads).
//
// cluster is returned (not just discarded) so a caller — the managed-secrets
// summary, in particular — can say WHICH cluster's connection secret is
// failing and let a reader jump straight to it, instead of a message with
// no subject.
//
// message is ALWAYS the mapped output of FailureSentence (P1-B B2) — never
// the record's raw Message text. The raw text still reaches the server log
// at every call site that records a Failed outcome; this is the one place
// that decides what the API (and so the browser) gets to see.
func (r *Reconciler) LastError() (cluster, message string, at time.Time, ok bool) {
	r.lastReconcileMu.RLock()
	defer r.lastReconcileMu.RUnlock()
	var rawMessage string
	for name, rec := range r.lastReconcile {
		if rec.Outcome == OutcomeFailed && rec.Time.After(at) {
			at = rec.Time
			cluster = name
			rawMessage = rec.Message
			ok = true
		}
	}
	if ok {
		message = FailureSentence(rawMessage)
	}
	return cluster, message, at, ok
}

// SelfManagedSecretNotCreatedMessage is the exact ClusterReconcileRecord.Message
// recorded when a self-managed connection's ArgoCD cluster Secret does not
// exist yet — Sharko is waiting for the user to create it (see
// syncSelfManaged's `!found` branch in reconciler.go). Exported as a single
// source of truth so read-model code (the System page's managed-secrets
// summary) can recognize this specific, well-defined skip reason as
// "the secret is missing" without fuzzy-matching a duplicated string.
const SelfManagedSecretNotCreatedMessage = "Waiting for you to create this cluster's ArgoCD cluster secret — this connection is self-managed, so Sharko only syncs addon labels onto it once it exists."

// ManagedSecretNotCreatedMessage is the exact ClusterReconcileRecord.Message
// recorded when a Sharko-managed cluster has no ArgoCD cluster secret on the
// cluster at all — the check-only pass found nothing there (CheckOnce), or a
// one-time "Sync" was asked for a secret that has never existed
// (ResyncClusterLabels). The write pass never records it: that pass creates
// the secret instead of reporting on it.
//
// Exported for the same reason as the self-managed message above: the read
// model recognises this exact, well-defined reason as "the secret is
// missing" without fuzzy-matching a duplicated string.
const ManagedSecretNotCreatedMessage = "This cluster's ArgoCD secret has not been created yet, so there is nothing to sync onto."

// UnlabeledSecretExistsMessage is recorded by the check-only pass when a
// secret with the cluster's name already sits in the ArgoCD namespace
// without Sharko's ownership label. Sharko takes no position on it — it is
// somebody else's, and adopting it is a separate, deliberate action.
const UnlabeledSecretExistsMessage = "A secret with this cluster's name already exists and Sharko did not create it — adopt the cluster instead of overwriting it."

// pruneStaleReconcileRecords removes lastReconcile and fightState entries
// for cluster names that were neither desired (in managed-clusters.yaml)
// nor observed live in ArgoCD during this pass (V2-cleanup-90.2, fix M3).
// Call once at the end of a COMPLETED reconcileDiff pass; never call it on
// an aborted pass (see stampAbortedTick below) — an abort has no reliable
// "this pass's clusters" set to prune against.
//
// known MUST be the union of the desired set (managed-clusters.yaml
// entries) AND the existing set (sharko-labeled Secrets observed live in
// ArgoCD at the START of this pass, i.e. reconcileDiff's `existing` map)
// — NOT the desired set alone. This union is what makes the interaction
// with fix L10 (deleteOne now records an outcome for orphan cleanup) come
// out honest:
//
//   - An orphan whose delete SUCCEEDS this pass is still a member of
//     `existing` (that map is a snapshot taken before any delete runs), so
//     its freshly-written "orphaned Secret removed" record survives THIS
//     pass's prune. The FOLLOWING pass re-lists ArgoCD, no longer finds
//     the (now actually deleted) Secret, so the name drops out of both
//     desired and existing — the record is pruned one pass later.
//   - An orphan whose delete FAILS this pass is likewise a member of
//     `existing` this pass, so its "failed" record survives this pass's
//     prune too. Next pass the Secret is STILL live (the delete never
//     happened), so `existing` includes it again — the record survives
//     again. It keeps surviving for as long as the orphan itself does.
//
// Pruning against the desired set alone would erase both records in the
// SAME pass they were written — an orphan is by definition absent from
// managed-clusters.yaml — which would make deleteOne's recordReconcile
// calls invisible to any caller and silently defeat fix L10.
func (r *Reconciler) pruneStaleReconcileRecords(known map[string]struct{}) {
	r.lastReconcileMu.Lock()
	for name := range r.lastReconcile {
		if _, ok := known[name]; !ok {
			delete(r.lastReconcile, name)
		}
	}
	r.lastReconcileMu.Unlock()

	r.fightMu.Lock()
	for name := range r.fightState {
		if _, ok := known[name]; !ok {
			delete(r.fightState, name)
		}
	}
	r.fightMu.Unlock()

	// P2-C1: a cluster that has dropped out of both git and the live
	// ArgoCD Secret listing has nothing left to say "the last successful
	// write was built from commit X" ABOUT — prune its persisted applied
	// revision the same pass lastReconcile/fightState prune theirs, so a
	// removed-then-reused cluster name never inherits a stale ghost value.
	r.appliedRevMu.Lock()
	for name := range r.appliedRevision {
		if _, ok := known[name]; !ok {
			delete(r.appliedRevision, name)
		}
	}
	r.appliedRevMu.Unlock()
}

// stampAbortedTick records OutcomeFailed for every cluster currently known
// to the reconcile-record map when a pass aborts BEFORE reaching the
// per-cluster work — a git read failure, a schema-validation rejection, or
// a failed ArgoCD Secret listing (V2-cleanup-90.2, fix M5a). Without this,
// an aborted pass leaves every cluster's last-known record untouched and
// silently aging: an operator watching one cluster's LastReconcile would
// see a stale "succeeded" from minutes or hours ago with no signal that
// the reconciler has since been failing to even read its source of truth.
//
// Only stamps clusters ALREADY in the record map — an abort has no
// desired/existing set to draw new cluster names from, so a cluster this
// server instance has never reconciled stays absent (matches
// LastReconcile's existing "ok=false means never reconciled" contract).
// Deliberately does NOT prune: an aborted pass has no reliable "this
// pass's clusters" set to prune against (see pruneStaleReconcileRecords).
func (r *Reconciler) stampAbortedTick(reason string) {
	// An aborted pass never completed a real git-vs-live comparison, so it
	// has nothing honest to say about WHICH commit was compared (P2-C1) —
	// clear the pass-level compared facts before stamping so every record
	// written below carries empty ComparedRevision/ComparedPath rather than
	// a stale value left over from the last pass that DID complete.
	// AppliedRevision is untouched: a past successful write is still a past
	// successful write, whatever THIS pass's abort reason is.
	r.setPassCompared("", "")

	r.lastReconcileMu.RLock()
	names := make([]string, 0, len(r.lastReconcile))
	for name := range r.lastReconcile {
		names = append(names, name)
	}
	r.lastReconcileMu.RUnlock()

	msg := "reconciler pass aborted: " + reason
	for _, name := range names {
		r.recordReconcile(name, OutcomeFailed, msg, nil)
	}
}

// --- Label-fight detection (V2-cleanup-89.5) ---
//
// A self-managed connection's ArgoCD cluster Secret is the user's — Sharko
// only ever merges its own addon-label keys onto it via
// argosecrets.Manager.SyncLabelsOnly (syncSelfManaged calls this every
// tick). If that same Secret is ALSO rendered from Git by a separate
// ArgoCD Application, two things can go wrong: that Application's
// `syncOptions: [Replace=true]` wipes Sharko's labels on every one of ITS
// syncs, or its manifest defines one of Sharko's addon-label keys with a
// conflicting value and self-heal reverts Sharko's write. Either way the
// symptom is the same from Sharko's side: a label Sharko wrote keeps
// coming back different on a later tick, even though Sharko never stopped
// wanting the value it originally wrote.
//
// The detector below tells that apart from the ordinary, expected case of
// git itself changing what Sharko wants for a key (e.g. an addon toggled
// off in managed-clusters.yaml) — a value changing because git drove the
// change is not a fight, it's Sharko doing its job.

// clusterFightState is the per-cluster label-fight tracking state for one
// self-managed cluster. Held in Reconciler.fightState, guarded by
// Reconciler.fightMu.
type clusterFightState struct {
	// lastWanted is the addon label key/value pairs Sharko computed as
	// desired the last time syncSelfManaged ran for this cluster,
	// regardless of whether a write actually occurred that tick (a Secret
	// already converged to the desired value still updates this baseline —
	// there is nothing new to compare against on the next tick either
	// way). nil before the first tick has ever run for this cluster.
	lastWanted map[string]string
	// reverts is the number of CONSECUTIVE ticks in which recordFightCheck
	// observed at least one key Sharko still wanted unchanged (i.e. git did
	// NOT move it) come back with a live value different from what Sharko
	// wrote last time. Reset to 0 the instant a tick shows no revert.
	reverts int
}

// resetFightStreak clears all label-fight tracking state for one cluster
// (V2-cleanup-90.2, fix M1). Call this when a self-managed connection's
// label write itself failed this tick.
//
// recordFightCheck runs BEFORE the write and unconditionally advances
// lastWanted to the value Sharko is ABOUT to write, on the assumption the
// write will land. When the write then fails, that assumption breaks: the
// live Secret still holds whatever was there before, but lastWanted now
// claims Sharko already achieved the new value. On the NEXT tick, comparing
// the (unchanged) live Secret against that stale lastWanted baseline looks
// exactly like an external actor reverting Sharko's write — a false "fight"
// signal caused entirely by Sharko's own failed write, and one that repeats
// (and escalates past fightRevertThreshold) on a second consecutive
// failure.
//
// Clearing the cluster's fight state entirely — rather than adding a
// separate "skip the next comparison" flag — is the cleaner shape: it
// reuses the SAME nil-check (`state.lastWanted != nil`) recordFightCheck
// already treats as "no baseline yet, nothing to compare" for a cluster's
// very first tick, so no new field or branch is needed. The cost is that a
// real, still-ongoing fight's revert streak also resets on a Sharko-side
// write failure; that trade is acceptable because a genuine fight simply
// re-accumulates reverts starting from the next tick where Sharko's write
// actually lands — at worst delaying the warning by one
// fightRevertThreshold's worth of ticks (~60s). The alternative (a false
// alarm baked into every write-failure streak) is strictly worse.
func (r *Reconciler) resetFightStreak(name string) {
	r.fightMu.Lock()
	defer r.fightMu.Unlock()
	delete(r.fightState, name)
}

// fightRevertThreshold is the number of consecutive reverted ticks needed
// before recordFightCheck starts returning a non-empty warning. A single
// reverted tick is not enough to conclude a fight — the other
// Application's sync landing between Sharko's read and write on one
// unlucky tick would trip that — but two or more IN A ROW, at the
// reconciler's ~30s cadence, means something is durably reasserting a
// different value.
const fightRevertThreshold = 2

// recordFightCheck compares the live label values observed on this tick
// (BEFORE this tick's write, i.e. what was on the Secret coming in) against
// what Sharko itself last wanted to write for this cluster, and updates the
// per-cluster revert-streak counter. Returns a non-empty plain-English
// warning once the streak reaches fightRevertThreshold; empty otherwise.
//
// desired is the FULL set of addon labels Sharko wants to write THIS tick
// (already normalized to the canonical vocabulary). observedLive is the
// live Secret's label map as read before this tick's write — pass nil when
// the Secret does not exist yet (nothing to have reverted against).
//
// False-positive guard: for each key in the previous tick's lastWanted,
// the key only counts toward a revert if desired ALSO still wants that
// exact same value this tick (git has not changed Sharko's own intent for
// that key since the last write). If git moved the desired value, a
// mismatch against the OLD lastWanted is expected and is skipped —
// otherwise every ordinary addon toggle would look identical to a fight.
func (r *Reconciler) recordFightCheck(name string, desired, observedLive map[string]string) (warning string) {
	r.fightMu.Lock()
	defer r.fightMu.Unlock()
	if r.fightState == nil {
		r.fightState = make(map[string]clusterFightState)
	}
	state := r.fightState[name]

	revertedThisTick := false
	if state.lastWanted != nil && observedLive != nil {
		for key, wantedLast := range state.lastWanted {
			wantedNow, stillWanted := desired[key]
			if !stillWanted || wantedNow != wantedLast {
				// git changed (or dropped) what Sharko wants for this key
				// since the last tick — not a revert signal, regardless of
				// what the live Secret currently holds for it.
				continue
			}
			if observedLive[key] != wantedLast {
				revertedThisTick = true
				break
			}
		}
	}

	if revertedThisTick {
		state.reverts++
	} else {
		state.reverts = 0
	}

	// Snapshot desired as the new baseline for the NEXT tick's comparison.
	// Copy rather than alias — desired is the caller's normalizeLabels
	// output and must not be mutated by a later tick through this map.
	snapshot := make(map[string]string, len(desired))
	for k, v := range desired {
		snapshot[k] = v
	}
	state.lastWanted = snapshot
	r.fightState[name] = state

	if state.reverts >= fightRevertThreshold {
		warning = fmt.Sprintf(
			"something else keeps overwriting Sharko's addon labels on this cluster's self-managed ArgoCD secret (reverted %d checks in a row) — likely the ArgoCD application that renders this secret from Git fighting with Sharko over it. Sharko will keep re-applying its labels every tick; see https://sharko.readthedocs.io/en/latest/operator/self-managed-connections/.",
			state.reverts,
		)
		// TODO(V3 E1 follow-up): Emit events.ReasonDriftDetected Warning event
		// here once the reconciler has access to an EventRecorder. The event
		// should be emitted at most once per fight (not every tick) — track
		// state.fightEventEmitted to avoid spam. Clear the flag when reverts
		// drops back to 0.
	}
	return warning
}
