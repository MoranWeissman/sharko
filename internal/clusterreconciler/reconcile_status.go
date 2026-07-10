package clusterreconciler

import (
	"fmt"
	"time"
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

// ClusterReconcileRecord is the last known reconcile outcome for one
// cluster. Message is a plain-English explanation of what happened —
// populated on Failed and Skipped, empty on Succeeded UNLESS label-fight
// detection (V2-cleanup-89.5) has found a sustained revert pattern on a
// self-managed connection's Secret: that case stays Succeeded (Sharko is
// still successfully re-applying its labels every tick) but Message
// carries the fight warning — see recordFightCheck.
type ClusterReconcileRecord struct {
	Time    time.Time
	Outcome ReconcileOutcome
	Message string
}

// recordReconcile stores the outcome of the most recent reconcile attempt
// for a single cluster, overwriting any previous record. Uses the
// reconciler's clock seam (r.now()) rather than time.Now() directly so
// tests can drive a deterministic timestamp the same way they do for the
// registration-pending grace window.
func (r *Reconciler) recordReconcile(name string, outcome ReconcileOutcome, message string) {
	r.lastReconcileMu.Lock()
	defer r.lastReconcileMu.Unlock()
	if r.lastReconcile == nil {
		r.lastReconcile = make(map[string]ClusterReconcileRecord)
	}
	r.lastReconcile[name] = ClusterReconcileRecord{
		Time:    r.now(),
		Outcome: outcome,
		Message: message,
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
	r.lastReconcileMu.RLock()
	names := make([]string, 0, len(r.lastReconcile))
	for name := range r.lastReconcile {
		names = append(names, name)
	}
	r.lastReconcileMu.RUnlock()

	msg := "reconciler pass aborted: " + reason
	for _, name := range names {
		r.recordReconcile(name, OutcomeFailed, msg)
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
	}
	return warning
}
