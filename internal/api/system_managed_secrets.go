package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
	"github.com/MoranWeissman/sharko/internal/authz"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/models"
)

// system_managed_secrets.go — GET /api/v1/system/managed-secrets. Visibility
// only: every secret Sharko manages, in one call, built entirely from data
// the server already read for the cluster list and the two reconcilers'
// in-memory state.
//
// Sharko manages two kinds of secret, each with its own engine:
//
//   - cluster connection secrets — the ArgoCD cluster Secret per managed
//     cluster (internal/clusterreconciler), a 30s tick + a trigger after
//     every merge.
//   - addon values secrets — pushed into remote clusters
//     (internal/secrets), a 5-minute poll.
//
// No per-row Kubernetes call: at 50 clusters that would be a fan-out storm.
// Every fact here comes from a listing read the server already performs
// (s.clusterSvc.ListClusters — the same call handleListClusters makes),
// each reconciler's own in-memory stats, or the audit log (already an
// in-memory ring buffer — s.AuditLog().List(0)). Where a fact genuinely
// isn't available, the field is left empty and the response says so via
// the "unknown" state / omitted timestamp — never an invented or
// approximated one.

// connectionSecretSource is what a cluster connection secret follows: git.
// Git holds the cluster's addon labels, and those labels are the content
// this secret is reconciled against. Named rather than repeated as a
// string literal so the row builder and any test read the same word.
const connectionSecretSource = "git"

// managedSecretsResponse is the response body for GET /api/v1/system/managed-secrets.
type managedSecretsResponse struct {
	ClusterConnectionSecrets []connectionSecretRow  `json:"cluster_connection_secrets"`
	AddonValuesSecrets       []addonValuesSecretRow `json:"addon_values_secrets"`
	// Rows (P3-E, E2) is both per-kind arrays merged into one list, shaped
	// like what the System page's table renders, worst-first ordered, and
	// the only part of this response that honors this request's filter
	// (?cluster=, ?addon=, ?state=, ?kind=, ?source=) and paging (?page=,
	// ?per_page=) query params — see buildManagedSecretRows and
	// filterManagedSecretRows. ClusterConnectionSecrets/AddonValuesSecrets
	// above stay full and unfiltered on every request: this field is
	// strictly additive, so an existing caller reading the two per-kind
	// arrays sees no change at all.
	Rows    []managedSecretRow    `json:"rows"`
	Engines managedSecretsEngines `json:"engines"`
	// OrphanedSecrets (leftover-secrets S1) is every leftover values secret
	// the addon-values engine's own scan passes have found: a live secret
	// carrying BOTH Sharko's managed-by label AND the sharko.dev/addon
	// provenance annotation, that no current desired-state plan claims any
	// more. Built purely from SecretReconciler.OrphanedSecrets() — an
	// in-memory read, no K8s call of its own, and not gated on a live
	// Git/ArgoCD connection the way ClusterConnectionSecrets/
	// AddonValuesSecrets are (the scan that found these already ran
	// separately, on the engine's own schedule). Also merged into Rows
	// (State "orphaned", Kind "values") like every other row kind.
	OrphanedSecrets []orphanedSecretRow `json:"orphaned_secrets"`
	// AddonValuesSecretSource is the real, human-readable name of the
	// backend addon-values secrets are compared against — "AWS Secrets
	// Manager", "a Kubernetes Secret", etc, or the generic "secrets store"
	// fallback when the configured backend has no recognizable product name
	// (unconfigured, a demo/stub backend, or an unimplemented type). Never
	// "the vault" — that reads as HashiCorp Vault to every DevOps reader,
	// which is misleading unless the backend genuinely IS Vault.
	//
	// KEPT for the whole-page copy that genuinely is about the page and not
	// about one row (the "Refresh all" hint). It is NOT what a row reads
	// anymore: every row now carries its own Source field (S1), so a reader
	// filtering, sorting or grouping by backend is reading a per-row fact,
	// and a future server that resolves a different backend per addon does
	// not need a response-shape change to say so.
	AddonValuesSecretSource string `json:"addon_values_secret_source"`
}

// connectionSecretRow is one row of cluster_connection_secrets — one row
// per managed cluster.
type connectionSecretRow struct {
	Cluster         string `json:"cluster"`
	SecretNamespace string `json:"secret_namespace,omitempty"`
	SecretName      string `json:"secret_name,omitempty"`
	// State is one of "in_sync", "out_of_sync", "missing", or "unknown" —
	// see connectionSecretState for exactly how each is derived. A FAILED
	// check (P1-B B1) renders as "unknown", never "out_of_sync": Sharko
	// could not look, which is a different fact from "Sharko looked and it
	// differs". See LastCheckError below for the two-facts shape this
	// implies (ArgoCD's own sync-state / last-operation-result split).
	State string `json:"state"`
	// Source (S1) names, per row, which store this secret's content is
	// compared against. A connection secret always follows git — git holds
	// the addon labels this secret is built from — so this is the constant
	// "git" here, and it is stated per row rather than assumed by the UI so
	// both kinds of row answer the same question in the same field.
	Source string `json:"source"`
	// LastChecked is RFC3339, or "" when the reconciler has never
	// processed this cluster on this server instance.
	LastChecked string `json:"last_checked,omitempty"`
	// LastRepaired / LastRepairedDetail come from a matching audit entry
	// (Resource "cluster:<name>", a repair event, Result "success") — an
	// honest per-row join, not an approximation. Empty when no such entry
	// exists in the audit log's retained window.
	LastRepaired       string `json:"last_repaired,omitempty"`
	LastRepairedDetail string `json:"last_repaired_detail,omitempty"`
	// LastCheckError (P1-B B1) is a safe, canned sentence describing why the
	// last check didn't finish — set only when this cluster's most recent
	// reconcile record is OutcomeFailed. Mirrors addonValuesSecretRow's
	// field of the same name exactly: distinct from State == "out_of_sync"
	// (which claims Sharko actually compared the secret and found a
	// mismatch), this field exists so the UI can say plainly "the last
	// check failed: …" instead of implying drift when the truth is the
	// check itself never finished. Always the mapped output of
	// clusterreconciler.FailureSentence — NEVER the reconciler's raw
	// record text.
	LastCheckError string `json:"last_check_error,omitempty"`

	// ComparedRevision (P2-C1) is the full branch head commit SHA the pass
	// that produced this row's state read git at. Empty when the active
	// git provider cannot say — never a guessed or stale value. The UI
	// shows the first 7 characters on the row/panel and the full value on
	// hover.
	ComparedRevision string `json:"compared_revision,omitempty"`
	// ComparedPath (P2-C1) is the exact managed-clusters file path this
	// row's state was compared against.
	ComparedPath string `json:"compared_path,omitempty"`
	// AppliedRevision (P2-C1) is the full commit SHA the last SUCCESSFUL
	// WRITE to this cluster's secret was built from — empty until this
	// server instance has ever successfully written it.
	AppliedRevision string `json:"applied_revision,omitempty"`
	// SelfHeals (P2-C3) reports whether Sharko will repair THIS row on its
	// own, without a human clicking Sync — derived from the real rule (see
	// connectionSelfHeals): self-managed connections and v4 repos always
	// heal; a v3 repo's managed clusters heal only when the
	// managed_cluster_self_heal setting is on.
	SelfHeals bool `json:"self_heals"`
	// DriftSource (P2-C6) names which side moved for an out_of_sync row:
	// "git" (the intent commit changed since the last successful write —
	// ComparedRevision != AppliedRevision) or "cluster" (the revisions
	// agree but the live secret still differs — something changed it
	// outside git). Empty when the row isn't out_of_sync, or when either
	// revision is unknown — never a guess.
	DriftSource string `json:"drift_source,omitempty"`
	// FightCount (P2-D D3) is the connection reconciler's label-fight
	// counter for this cluster — how many consecutive ticks something else
	// has reverted Sharko's own write on this cluster's self-managed ArgoCD
	// secret. 0/omitted for every cluster with no fight in progress
	// (including every Sharko-managed, non-self-managed cluster, which has
	// no fight concept). The UI shows a quiet row warning at 3 or more.
	FightCount int `json:"fight_count,omitempty"`
}

// addonValuesSecretRow is one row of addon_values_secrets — one row per
// cluster+addon pair that has both a secret definition and the addon
// enabled on that cluster.
type addonValuesSecretRow struct {
	Cluster         string `json:"cluster"`
	Addon           string `json:"addon"`
	SecretName      string `json:"secret_name,omitempty"`
	SecretNamespace string `json:"secret_namespace,omitempty"`
	// State is one of "in_sync", "out_of_sync", "missing", "foreign", or
	// "unknown" — derived here from the addon-values reconciler's per-item
	// outcome (see addonValuesSecretRowState) rather than its own
	// per-cluster record. "foreign" (P1-A) means a secret with this name is
	// on the cluster and Sharko did not create it, so Sharko will not
	// change or remove it.
	// Compared against the vault (the secrets provider), NOT git — git only
	// holds a pointer to where the value lives (S3(a) honesty lock).
	State string `json:"state"`
	// Source (S1) names, per row, the real backend this secret's value
	// comes from and is compared against — "AWS Secrets Manager", "a
	// Kubernetes Secret", ..., or the honest "secrets store" fallback when
	// the server cannot name a product. Per row, not per response: the row
	// is what a reader filters, sorts and groups by, so the fact has to
	// live on the row.
	Source string `json:"source"`
	// LastChecked is RFC3339, or "" when the addon-values reconciler has
	// never processed this cluster+addon pair on this server instance —
	// an in-memory read on s.secretReconciler (internal/secrets), the
	// same per-row-state pattern connectionSecretRow.LastChecked reads
	// from the cluster-connection reconciler.
	LastChecked string `json:"last_checked,omitempty"`
	// LastRepaired / LastRepairedDetail come from a matching audit entry
	// (Resource "cluster:<name>/addon:<addon>", an addon_secret_created
	// or addon_secret_updated event, Result "success") — the same honest
	// per-row join connectionSecretRow.LastRepaired uses. Empty when no
	// such entry exists in the audit log's retained window.
	LastRepaired       string `json:"last_repaired,omitempty"`
	LastRepairedDetail string `json:"last_repaired_detail,omitempty"`
	// LastCheckError (S8) is a safe, canned sentence describing why the
	// last check didn't finish — set only when the reconciler's per-item
	// record carries an error (a failed check, NOT a real "this drifted"
	// finding). Distinct from State=="out_of_sync", which claims Sharko
	// actually compared the secret to its source and found a mismatch:
	// this field exists so the UI can say plainly "the last check
	// failed: …" instead of implying drift when the truth is the check
	// itself never completed. Always the mapped output of
	// addonValuesSecretCheckFailureSentence — NEVER the reconciler's raw
	// error text (see that function's doc comment for why).
	LastCheckError string `json:"last_check_error,omitempty"`

	// SelfHeals (P2-C3) reports whether Sharko will repair THIS row on its
	// own: true for every values row except a foreign one (P1-A's
	// ownership gate means Sharko never touches a secret it did not
	// create, so it can never self-heal one either).
	SelfHeals bool `json:"self_heals"`
	// ConsecutiveFailures (P2-D D3) is the values reconciler's per-item
	// consecutive-failure count for this cluster+addon pair — how many
	// passes in a row this item's check or write attempt itself failed
	// (never for a legitimate finding like out_of_sync or missing).
	// 0/omitted when the last attempt succeeded or the pair has never been
	// checked. The UI shows a quiet row warning at 3 or more.
	ConsecutiveFailures int `json:"consecutive_failures,omitempty"`
}

// orphanedSecretRow (leftover-secrets S1) is one row of orphaned_secrets —
// one row per leftover values secret the addon-values engine's own scan
// found: on the cluster, carrying Sharko's labels, but claimed by nothing
// in the current desired-state plan.
type orphanedSecretRow struct {
	Cluster         string `json:"cluster"`
	SecretName      string `json:"secret_name,omitempty"`
	SecretNamespace string `json:"secret_namespace,omitempty"`
	// Addon is the addon name read off the secret's own provenance
	// annotation at scan time — omitted only if a future scan somehow
	// produces a record with an empty Addon, which the scan itself never
	// does (a secret with no addon annotation is never reported in the
	// first place — see internal/secrets/orphans.go's header comment).
	Addon string `json:"addon,omitempty"`
	// State is always the literal "orphaned" — its own row kind carries no
	// other state; the field exists so a caller reading Rows (which merges
	// every kind onto one shape) never has to special-case this kind to
	// find its state.
	State string `json:"state"`
	// Source (S1) names the real backend addon-values secrets are compared
	// against — same field, same meaning, as addonValuesSecretRow.Source.
	Source string `json:"source"`
	// LastChecked is RFC3339 — when the scan that most recently confirmed
	// this secret is still an orphan ran. Never omitted in practice: a
	// record only exists because a scan found it.
	LastChecked string `json:"last_checked,omitempty"`
}

// managedSecretRow (P3-E, E2) is one row of the merged Rows array — one
// connectionSecretRow or addonValuesSecretRow, tagged with Kind and
// reshaped onto a single flat struct so a caller renders one table instead
// of merging two arrays itself (the merge the System page's own UI already
// does in the browser — see ManagedSecrets.tsx's UnifiedRow). A field that
// doesn't apply to this row's Kind is left empty/omitted exactly as it
// already is on the source per-kind row — nothing here is invented:
//
//   - "connection" rows carry ComparedRevision/AppliedRevision/ComparedPath/
//     DriftSource/FightCount (connectionSecretRow's own P2-C1/P2-C6/P2-D
//     fields); Addon and ConsecutiveFailures are always empty/zero — a
//     connection secret has no addon and no per-item consecutive-failure
//     counter (that's FightCount's job on this kind).
//   - "values" rows carry Addon and ConsecutiveFailures; ComparedRevision/
//     AppliedRevision/ComparedPath/DriftSource/FightCount are always empty/
//     zero — the values engine compares against the vault, not git, so it
//     has no commit-revision or drift-blame facts to report this lane (see
//     connectionDriftSource's doc comment on why values rows skip drift
//     blame entirely).
//
// Name/Namespace are the k8s Secret's own name/namespace (SecretName/
// SecretNamespace on the source row) — the identity a portal groups or
// links by, same as the page's own row grouping.
type managedSecretRow struct {
	// Kind is "connection" or "values" — which engine/array this row came
	// from. Always present.
	Kind      string `json:"kind"`
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Cluster   string `json:"cluster"`
	Addon     string `json:"addon,omitempty"`
	Source    string `json:"source"`
	State     string `json:"state"`

	ComparedRevision string `json:"compared_revision,omitempty"`
	AppliedRevision  string `json:"applied_revision,omitempty"`
	ComparedPath     string `json:"compared_path,omitempty"`
	DriftSource      string `json:"drift_source,omitempty"`

	SelfHeals           bool `json:"self_heals"`
	FightCount          int  `json:"fight_count,omitempty"`
	ConsecutiveFailures int  `json:"consecutive_failures,omitempty"`

	LastChecked    string `json:"last_checked,omitempty"`
	LastCheckError string `json:"last_check_error,omitempty"`
	LastRepaired   string `json:"last_repaired,omitempty"`
}

// managedSecretKindConnection / managedSecretKindValues are the two Kind
// values a managedSecretRow can carry — named constants so the merge, the
// ?kind= filter, and any test that pins one all read the same literal.
const (
	managedSecretKindConnection = "connection"
	managedSecretKindValues     = "values"
)

// managedSecretStateRank is the worst-first order the System page's own
// StatusMark.statusSortRank uses (ui/src/components/resource/StatusMark.tsx)
// — reordered for G3 (gitops-proud P4-G) and again for leftover-secrets S1:
// missing first (nothing exists to Sync onto — a harder stop than a wrong
// value Sync can overwrite), then out_of_sync (a real, confirmed mismatch),
// then orphaned (a real, confirmed leftover — Sharko found a secret nothing
// claims any more; ranked right after the two real comparisons, ahead of
// foreign, because an orphan has a concrete fix Sharko can offer — delete
// it — where a foreign row has none), then foreign (a boundary worth
// knowing about but not damage), then a FAILED check (see
// managedSecretCheckFailedRank below), then a genuinely never-checked row,
// in_sync last. Kept in exact lockstep with the UI table on purpose: the
// merged Rows array is meant to read in the same order the page itself
// renders, not a server-invented ranking.
var managedSecretStateRank = map[string]int{
	"missing":     0,
	"out_of_sync": 1,
	"orphaned":    2,
	"foreign":     3,
	// "unknown" WITHOUT a check error ("not checked yet") ranks here — a
	// FAILED check outranks it by one slot, see managedSecretCheckFailedRank.
	"unknown": 5,
	"in_sync": 6,
}

// managedSecretCheckFailedRank is where a row whose last check genuinely
// FAILED ranks — one slot ahead of a row that has simply never been looked
// at, even though both carry the exact same "unknown" state word. A failed
// check is Sharko attempting and hitting a wall; a never-checked row is
// Sharko not having gotten to it yet — the first is the nearer, more
// actionable "Sharko doesn't know".
const managedSecretCheckFailedRank = 4

// managedSecretStateSortRank returns a row's sort weight. A state the rank
// table doesn't recognize reads as "unknown" — the same fallback
// toResourceStatus uses in the UI for a state string it doesn't know, never
// a crash or a silent last-place drop. hasCheckError promotes an "unknown"
// (or unrecognized) row to managedSecretCheckFailedRank — pass
// row.LastCheckError != "".
func managedSecretStateSortRank(state string, hasCheckError bool) int {
	rank, ok := managedSecretStateRank[state]
	if !ok {
		rank = managedSecretStateRank["unknown"]
	}
	if hasCheckError && (state == "unknown" || !ok) {
		return managedSecretCheckFailedRank
	}
	return rank
}

// buildManagedSecretRows merges the three already-built per-kind arrays
// into one flat, worst-first-sorted list (E2, extended for leftover-secrets
// S1). Called AFTER buildConnectionSecretRows/buildAddonValuesSecretRows/
// buildOrphanedSecretRows so it never recomputes state or does any I/O of
// its own — a pure reshape + sort.
func buildManagedSecretRows(conn []connectionSecretRow, values []addonValuesSecretRow, orphaned []orphanedSecretRow) []managedSecretRow {
	rows := make([]managedSecretRow, 0, len(conn)+len(values)+len(orphaned))
	for _, r := range conn {
		rows = append(rows, managedSecretRow{
			Kind:             managedSecretKindConnection,
			Name:             r.SecretName,
			Namespace:        r.SecretNamespace,
			Cluster:          r.Cluster,
			Source:           r.Source,
			State:            r.State,
			ComparedRevision: r.ComparedRevision,
			AppliedRevision:  r.AppliedRevision,
			ComparedPath:     r.ComparedPath,
			DriftSource:      r.DriftSource,
			SelfHeals:        r.SelfHeals,
			FightCount:       r.FightCount,
			LastChecked:      r.LastChecked,
			LastCheckError:   r.LastCheckError,
			LastRepaired:     r.LastRepaired,
		})
	}
	for _, r := range values {
		rows = append(rows, managedSecretRow{
			Kind:                managedSecretKindValues,
			Name:                r.SecretName,
			Namespace:           r.SecretNamespace,
			Cluster:             r.Cluster,
			Addon:               r.Addon,
			Source:              r.Source,
			State:               r.State,
			SelfHeals:           r.SelfHeals,
			ConsecutiveFailures: r.ConsecutiveFailures,
			LastChecked:         r.LastChecked,
			LastCheckError:      r.LastCheckError,
			LastRepaired:        r.LastRepaired,
		})
	}
	for _, r := range orphaned {
		rows = append(rows, managedSecretRow{
			Kind:      managedSecretKindValues,
			Name:      r.SecretName,
			Namespace: r.SecretNamespace,
			Cluster:   r.Cluster,
			Addon:     r.Addon,
			Source:    r.Source,
			State:     r.State,
			// An orphan never self-heals — there is no plan claiming it, so
			// there is nothing for the engine to converge it back toward.
			// The only future it has is the operator-gated delete.
			SelfHeals:   false,
			LastChecked: r.LastChecked,
		})
	}
	sortManagedSecretRowsWorstFirst(rows)
	return rows
}

// sortManagedSecretRowsWorstFirst orders rows worst-state-first
// (managedSecretStateSortRank), tie-broken by cluster, then addon, then
// name — a stable, deterministic order so paging through the same
// unchanged data never reshuffles a row across pages.
func sortManagedSecretRowsWorstFirst(rows []managedSecretRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		ri := managedSecretStateSortRank(rows[i].State, rows[i].LastCheckError != "")
		rj := managedSecretStateSortRank(rows[j].State, rows[j].LastCheckError != "")
		if ri != rj {
			return ri < rj
		}
		if rows[i].Cluster != rows[j].Cluster {
			return rows[i].Cluster < rows[j].Cluster
		}
		if rows[i].Addon != rows[j].Addon {
			return rows[i].Addon < rows[j].Addon
		}
		return rows[i].Name < rows[j].Name
	})
}

// managedSecretRowFilters holds the E1 query-param filters — cluster,
// addon, state, kind, source. An empty field means "no filter on this
// field." Applied only to the merged Rows array (E2); the two per-kind
// arrays are never filtered, keeping them additive/backward-compatible
// (see managedSecretsResponse.Rows's doc comment).
type managedSecretRowFilters struct {
	Cluster string
	Addon   string
	State   string
	Kind    string
	Source  string
}

// parseManagedSecretRowFilters reads ?cluster=, ?addon=, ?state=, ?kind=,
// ?source= off the request.
func parseManagedSecretRowFilters(r *http.Request) managedSecretRowFilters {
	q := r.URL.Query()
	return managedSecretRowFilters{
		Cluster: q.Get("cluster"),
		Addon:   q.Get("addon"),
		State:   q.Get("state"),
		Kind:    q.Get("kind"),
		Source:  q.Get("source"),
	}
}

// filterManagedSecretRows applies every non-empty filter, AND-joined,
// exact-match against the row's own field — case-honest (never folds or
// normalizes case, on either side). This mirrors the house pattern the
// existing filterClusters/filterAddons already use for a recognized filter
// field whose value simply doesn't occur in the data: a value that matches
// no row (an unrecognized ?state= or ?kind=, a typo'd cluster name, ...)
// yields a documented EMPTY result, never a 400 — there is nothing invalid
// about the request, only nothing left after filtering.
func filterManagedSecretRows(rows []managedSecretRow, f managedSecretRowFilters) []managedSecretRow {
	if f.Cluster == "" && f.Addon == "" && f.State == "" && f.Kind == "" && f.Source == "" {
		return rows
	}
	result := make([]managedSecretRow, 0, len(rows))
	for _, row := range rows {
		if f.Cluster != "" && row.Cluster != f.Cluster {
			continue
		}
		if f.Addon != "" && row.Addon != f.Addon {
			continue
		}
		if f.State != "" && row.State != f.State {
			continue
		}
		if f.Kind != "" && row.Kind != f.Kind {
			continue
		}
		if f.Source != "" && row.Source != f.Source {
			continue
		}
		result = append(result, row)
	}
	return result
}

// managedSecretsEngineInfo reports one reconciler's cadence + health.
type managedSecretsEngineInfo struct {
	// Wired is false when this server instance has no such reconciler
	// running at all (out-of-cluster, or no credentials provider) — every
	// other field stays zero/empty in that case, which is different from
	// "wired but never run yet".
	Wired bool `json:"wired"`
	// IntervalSeconds is the configured tick cadence, 0 when not wired.
	IntervalSeconds int `json:"interval_seconds,omitempty"`
	// LastRun is RFC3339, "" when wired but no tick has completed yet.
	LastRun string `json:"last_run,omitempty"`
	// LastError is the most recent failure message, "" when the last known
	// state has no error.
	LastError string `json:"last_error,omitempty"`
	// LastErrorCluster names the cluster the LastError message is ABOUT.
	// Cluster-connection: the per-cluster record LastError itself reads off
	// (clusterreconciler.Reconciler.LastError). Addon-values (P1-B B2): the
	// first failing cluster+addon pair from the most recent reconcile run
	// (secrets.Reconciler.LastErrorCluster). Empty when there is no current
	// error, or when the failure was plan-level (reading the catalog or the
	// managed-clusters file itself failed, before any per-item work
	// started) — there is no cluster to name in that case.
	LastErrorCluster string `json:"last_error_cluster,omitempty"`
	// LastErrorAt is the RFC3339 timestamp of LastError — an error with no
	// "since when" is not actionable. Empty exactly when LastError is empty.
	LastErrorAt string `json:"last_error_at,omitempty"`
	// Enabled (gitops-proud P4-I, D2) is true unless an admin has switched
	// this engine off via its settings toggle. Distinct from Wired: Wired
	// asks "does this server process have the reconciler object at all",
	// Enabled asks "is it allowed to run its passes right now". The
	// cluster-connection engine has no off switch on purpose (it is
	// Sharko's own job) and always reports true here. Defaults to true so
	// an install that never touches the setting, or a response built
	// before this field existed, reads as "on" rather than "off".
	Enabled bool `json:"enabled"`
}

type managedSecretsEngines struct {
	ClusterConnection managedSecretsEngineInfo `json:"cluster_connection"`
	AddonValues       managedSecretsEngineInfo `json:"addon_values"`
}

// connectionSecretRepairDetail maps the clusterreconciler audit Events that
// represent an actual write ("Sharko fixed something on this cluster's
// secret") to a short plain-English description of what changed. Only
// Result=="success" entries with one of these Events count as a repair —
// everything else (skips, failures, the fleet-wide "reconcile triggered"
// nudge) is not "we changed the secret".
var connectionSecretRepairDetail = map[string]string{
	"cluster_secret_create":            "secret created",
	"cluster_secret_managed_self_heal": "labels corrected",
	"cluster_secret_user_label_sync":   "labels synced onto your secret",
}

// addonValuesSecretRepairDetail maps the internal/secrets reconciler's
// per-item audit Events (wired in cmd/sharko/serve.go's SetItemAuditFunc
// callback) to a short plain-English description of what changed — the
// same canned-detail pattern connectionSecretRepairDetail uses, never
// free-form text. Only Result=="success" entries with one of these Events
// count as a repair — an unchanged check never produces an audit entry at
// all (see internal/secrets.Reconciler's item-audit callback), so there is
// nothing else to exclude here.
var addonValuesSecretRepairDetail = map[string]string{
	"addon_secret_created": "secret created",
	"addon_secret_updated": "secret updated",
}

// handleGetManagedSecrets godoc
//
// @Summary Get every secret Sharko manages
// @Description Aggregates cluster-connection secrets (the ArgoCD cluster Secret per managed cluster) and addon-values secrets (pushed into remote clusters), plus each reconciler engine's cadence, last run, and last error. Built entirely from data already read for the cluster list and the two reconcilers' in-memory stats — no per-row Kubernetes call. A fact the server cannot currently determine is left empty/unknown rather than approximated. The response also carries a merged, worst-state-first `rows` array (both kinds flattened onto one shape, P3-E) — the only part of the response that honors the query params below. The two per-kind arrays above are always returned in full, unfiltered and unpaginated, for backward compatibility. An unrecognized filter value (e.g. an unknown state or kind) matches no row rather than returning an error.
// @Tags system
// @Produce json
// @Security BearerAuth
// @Param cluster query string false "Rows filter: exact cluster name match"
// @Param addon query string false "Rows filter: exact addon name match (values rows only — connection rows have no addon and never match a non-empty value)"
// @Param state query string false "Rows filter: exact state match (in_sync, out_of_sync, missing, unknown, foreign, orphaned)"
// @Param kind query string false "Rows filter: exact kind match (connection, values)"
// @Param source query string false "Rows filter: exact source match (e.g. git, AWS Secrets Manager)"
// @Param page query int false "Rows paging: page number, default 1"
// @Param per_page query int false "Rows paging: items per page, default 20, max 100"
// @Success 200 {object} managedSecretsResponse "Managed secrets summary"
// @Failure 403 {object} map[string]interface{} "Forbidden"
// @Router /system/managed-secrets [get]
func (s *Server) handleGetManagedSecrets(w http.ResponseWriter, r *http.Request) {
	if !authz.RequireWithResponse(w, r, "managed-secrets.list") {
		return
	}

	resp := managedSecretsResponse{
		ClusterConnectionSecrets: []connectionSecretRow{},
		AddonValuesSecrets:       []addonValuesSecretRow{},
		OrphanedSecrets:          []orphanedSecretRow{},
		Rows:                     []managedSecretRow{},
	}

	// The engines section never depends on a live Git/ArgoCD connection —
	// build it first so a connection outage still reports engine health.
	//
	// L14 (code review): both settings this endpoint needs — self-heal and
	// addon-values-engine-enabled — are read ONCE here via
	// GetManagedSecretsSettings, which does a single ConfigMap Read for
	// both, and the two bools are threaded down from here instead of
	// addonValuesEngineInfo/buildConnectionSecretRows each reading the
	// settings store independently. Before this, those two calls meant TWO
	// separate live K8s API GETs of the identical ConfigMap object on every
	// hit of this endpoint (this page's own 30-second auto-refresh, times
	// however many tabs have it open).
	managedSecretsSettings := s.settingsStore.GetManagedSecretsSettings(r.Context())
	resp.Engines.ClusterConnection = s.clusterConnectionEngineInfo()
	resp.Engines.AddonValues = s.addonValuesEngineInfo(r.Context(), managedSecretsSettings.AddonValuesEngineEnabled)
	resp.AddonValuesSecretSource = s.addonValuesSecretSourceLabel()
	// Orphaned secrets (leftover-secrets S1) are a pure in-memory read on
	// the reconciler's OWN scan results — no K8s call, no dependency on a
	// live Git/ArgoCD connection, so this is built here alongside the
	// engines section rather than gated behind the cluster-list read below.
	resp.OrphanedSecrets = s.buildOrphanedSecretRows()

	gp, gpErr := s.connSvc.GetActiveGitProvider()
	ac, acErr := s.connSvc.GetActiveArgocdClient()
	if gpErr != nil || acErr != nil {
		// Degrade, never 500: the two tables need the cluster list, which
		// needs a live Git+ArgoCD connection. The engines section above
		// still stands on its own.
		s.respondManagedSecrets(w, r, resp)
		return
	}

	listResp, err := s.clusterSvc.ListClusters(r.Context(), gp, ac)
	if err != nil {
		s.respondManagedSecrets(w, r, resp)
		return
	}

	// Per-cluster reconcile outcomes are an in-memory lookup on
	// s.clusterRecon — no extra ArgoCD/K8s call, the same enrichment
	// handleListClusters already applies to every cluster it returns.
	for i := range listResp.Clusters {
		applyLastReconcile(&listResp.Clusters[i], s.clusterRecon)
	}

	// P3-F1: the audit ring is walked ONCE here, into a per-resource index
	// both row builders then read in O(1). It used to be walked once per
	// row — see buildRepairIndex's own comment.
	repairs := buildRepairIndex(s.AuditLog().List(0))

	resp.ClusterConnectionSecrets = s.buildConnectionSecretRows(r.Context(), listResp.Clusters, repairs, managedSecretsSettings.SelfHealEnabled)
	// M4 (code review): resp.Engines.AddonValues.Enabled is already computed
	// above (addonValuesEngineInfo) — reused here instead of re-reading the
	// settings store a second time, so the row-level SelfHeals promise and
	// the engine strip can never disagree about whether the engine is on.
	resp.AddonValuesSecrets = s.buildAddonValuesSecretRows(listResp.Clusters, repairs, resp.Engines.AddonValues.Enabled)

	s.respondManagedSecrets(w, r, resp)
}

// respondManagedSecrets finishes building the response and writes it: merges
// the two per-kind arrays into resp.Rows (E2), applies this request's E1
// filters and paging to Rows only — the two per-kind arrays are returned
// exactly as the caller built them, full and unfiltered — sets the
// pagination headers (X-Total-Count/X-Page/X-Per-Page, the same envelope
// parsePagination/setPaginationHeaders already give every other list
// endpoint), and writes 200. Called from every return path in
// handleGetManagedSecrets, including the two "degrade, never 500" early
// returns, so a caller paging through this endpoint sees the same header
// contract regardless of which path produced the response.
func (s *Server) respondManagedSecrets(w http.ResponseWriter, r *http.Request, resp managedSecretsResponse) {
	rows := buildManagedSecretRows(resp.ClusterConnectionSecrets, resp.AddonValuesSecrets, resp.OrphanedSecrets)
	rows = filterManagedSecretRows(rows, parseManagedSecretRowFilters(r))

	p := parsePagination(r)
	setPaginationHeaders(w, len(rows), p)
	resp.Rows = applyPagination(rows, p)

	writeJSON(w, http.StatusOK, resp)
}

// buildConnectionSecretRows projects one row per Sharko-managed cluster.
// Discovered/orphan clusters (Managed == false) are excluded — Sharko has
// no connection secret of its own to report for a cluster it didn't
// register.
// selfHealOn is the managed_cluster_self_heal setting (P2-C3) — a single,
// repo-wide server setting, not a per-cluster fact. L14 (code review): the
// CALLER reads it once per request (GetManagedSecretsSettings, alongside
// the addon-values engine's own setting, in one ConfigMap Read) and passes
// it down here instead of this function reading the settings store itself
// — this used to be its own s.settingsStore.IsManagedClusterSelfHealEnabled
// call, a second live K8s API GET of the same ConfigMap object
// handleGetManagedSecrets's addonValuesEngineInfo call also reads.
func (s *Server) buildConnectionSecretRows(ctx context.Context, clusters []models.Cluster, repairs repairIndex, selfHealOn bool) []connectionSecretRow {
	_, ns, _ := s.k8sClientAndNamespace()
	v3SelfHealOn := selfHealOn

	rows := make([]connectionSecretRow, 0, len(clusters))
	for _, c := range clusters {
		if !c.Managed {
			continue
		}
		row := connectionSecretRow{Cluster: c.Name, Source: connectionSecretSource}
		if ns != "" {
			// The Secret's Name always equals the cluster's Name (see
			// argosecrets.Manager.Ensure) — same deterministic fact
			// applyManagedSecretFields uses for the per-cluster page.
			row.SecretNamespace = ns
			row.SecretName = c.Name
		}
		row.State, row.LastChecked = connectionSecretState(c.LastReconcile)
		row.LastCheckError = connectionSecretCheckError(c.LastReconcile)
		if c.LastReconcile != nil {
			row.ComparedRevision = c.LastReconcile.ComparedRevision
			row.ComparedPath = c.LastReconcile.ComparedPath
			row.AppliedRevision = c.LastReconcile.AppliedRevision
			row.FightCount = c.LastReconcile.FightCount
		}
		row.SelfHeals = connectionSelfHeals(c, row.ComparedPath, v3SelfHealOn)
		row.DriftSource = connectionDriftSource(row.State, row.ComparedRevision, row.AppliedRevision)

		if rep, ok := repairs.lastConnectionSecretRepair(c.Name); ok {
			row.LastRepaired = rep.At.UTC().Format(time.RFC3339)
			row.LastRepairedDetail = rep.Detail
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Cluster < rows[j].Cluster })
	return rows
}

// connectionSelfHeals derives the real self-heal rule for a connection row
// (P2-C3): which future does this cluster's out-of-sync secret actually
// have.
//
//   - A self-managed connection (c.ConnectionManagedBy == "user") always
//     heals: syncSelfManaged re-applies addon labels onto the user's
//     secret every tick, unconditionally — see reconciler.go's doc comment
//     on that function.
//   - A v4 repo (comparedPath == clusterreconciler.V4ManagedClustersPath)
//     always heals: applyV4AddonLabels converges the addons.sharko.dev/
//     labels every tick, with no setting gate — that is how enable/disable
//     take effect on a v4 repo.
//   - A v3, Sharko-managed cluster heals only when the LIVE
//     managed_cluster_self_heal setting is on (V3 GF1, opt-in, default
//     off) — reflected here verbatim, not re-derived.
//
// comparedPath being empty (this cluster has never been checked on this
// server instance) falls through to the v3SelfHealOn answer: a real,
// grounded server setting is a better default than an invented tri-state.
func connectionSelfHeals(c models.Cluster, comparedPath string, v3SelfHealOn bool) bool {
	if c.ConnectionManagedBy == "user" {
		return true
	}
	if comparedPath == clusterreconciler.V4ManagedClustersPath {
		return true
	}
	return v3SelfHealOn
}

// connectionDriftSource names which side moved for an out_of_sync
// connection row (P2-C6): compare the commit Sharko last CHECKED against
// (comparedRevision) to the commit the last SUCCESSFUL WRITE was built
// from (appliedRevision).
//
//   - Different → "git": a newer commit changed the desired state since
//     the last successful write, and the live secret hasn't caught up yet.
//   - Equal → "cluster": the desired state hasn't moved since the last
//     write, yet the live secret still differs — something changed it
//     outside git.
//   - Either revision unknown, or the row isn't out_of_sync → "" (say
//     nothing rather than guess; values rows skip drift blame entirely
//     this lane — their intent has no commit to compare against).
func connectionDriftSource(state, comparedRevision, appliedRevision string) string {
	if state != "out_of_sync" {
		return ""
	}
	if comparedRevision == "" || appliedRevision == "" {
		return ""
	}
	if comparedRevision != appliedRevision {
		return "git"
	}
	return "cluster"
}

// connectionSecretState derives the row's honest state + last-checked
// timestamp from the reconciler's per-cluster record. Every branch here is
// grounded in a real, already-recorded fact — nothing is guessed:
//
//   - rec == nil: the reconciler has never processed this cluster on this
//     server instance (fresh startup, no reconciler wired, or a
//     registration PR that hasn't merged yet) -> "unknown".
//   - Outcome "skipped" with the exact self-managed "not created yet"
//     message -> "missing": the reconciler itself observed the secret
//     doesn't exist.
//   - Outcome "succeeded" with no label drift -> "in_sync".
//   - Outcome "succeeded" WITH label drift (self-heal off, or a
//     self-managed fight in progress) -> "out_of_sync": Sharko compared and
//     found a real mismatch.
//   - Outcome "failed" -> "unknown" (P1-B B1, finding #120). A failed check
//     means Sharko does not know whether the secret matches — that is a
//     different fact from "Sharko looked and it differs", and wearing
//     out_of_sync's badge told an ArgoCD-literate reader a real comparison
//     had happened when it hadn't. LastCheckError (connectionSecretRow)
//     carries the canned reason alongside this state — see
//     connectionSecretCheckError.
//   - Any other "skipped" reason (a git read failure that left labels
//     untouched, a same-name unlabeled Secret in Adopt territory, etc.) ->
//     "unknown" — collapsing these into "in sync" or "out of sync" would
//     overclaim; the reconciler deliberately took no position this tick.
//
// HONEST TRADE-OFF, on purpose: ClusterReconcileRecord holds only the LAST
// outcome — a cluster whose last SUCCESSFUL check found real drift
// (out_of_sync), followed by a check that itself FAILED, loses the "it was
// out of sync" fact the moment the failed record overwrites it. This
// function does not invent memory to paper over that: state = unknown +
// LastCheckError's reason is the honest rendering of what Sharko can
// currently prove, not a guess at what was true before the check failed.
// Preserving the prior verdict across a failed check would need the record
// to carry more than one outcome, which is out of this lane's scope.
func connectionSecretState(rec *models.ClusterLastReconcile) (state, lastChecked string) {
	if rec == nil {
		return "unknown", ""
	}
	lastChecked = rec.Time
	switch {
	// M8 (code review): matched against RawMessage, not Message — Message
	// is now the FailureSentence-mapped, safe-for-a-browser text
	// (applyLastReconcile in clusters_reconcile.go), which no longer
	// carries this exact sentinel string verbatim. RawMessage still does;
	// SelfManagedSecretNotCreatedMessage is a fixed sentence this package
	// itself writes, never wrapped error text, so comparing against it
	// directly is safe.
	case rec.Outcome == string(clusterreconciler.OutcomeSkipped) && rec.RawMessage == clusterreconciler.SelfManagedSecretNotCreatedMessage:
		return "missing", lastChecked
	case rec.Outcome == string(clusterreconciler.OutcomeSucceeded) && rec.LabelDrift == nil:
		return "in_sync", lastChecked
	case rec.Outcome == string(clusterreconciler.OutcomeSucceeded):
		return "out_of_sync", lastChecked
	default: // OutcomeFailed and any other "skipped" reason
		return "unknown", lastChecked
	}
}

// connectionSecretCheckError reports the canned reason (P1-B B1) a
// connection row's last check didn't finish — set only when the
// reconciler's per-cluster record is OutcomeFailed. Mirrors
// addonValuesRowLastCheckError's shape: mapped through
// clusterreconciler.FailureSentence, NEVER the record's raw Message text
// (see that function's doc comment on why — the same S8/#123 concern, one
// stampAbortedTick call away from leaking a raw git or Kubernetes error
// into every cluster's row at once).
func connectionSecretCheckError(rec *models.ClusterLastReconcile) string {
	if rec == nil || rec.Outcome != string(clusterreconciler.OutcomeFailed) {
		return ""
	}
	// M8 (code review): mapped from RawMessage, not Message. Message is
	// already FailureSentence-mapped by applyLastReconcile — running an
	// already-mapped sentence through FailureSentence a second time matches
	// none of its fixed-prefix cases (the canned sentences don't contain
	// the raw stage prefixes) and collapses every specific reason into the
	// generic "The last check didn't finish" fallback. RawMessage still
	// carries the one-and-only-mapping-needed raw text.
	return clusterreconciler.FailureSentence(rec.RawMessage)
}

// secretRepair is one row's "last repaired" fact: when Sharko last
// successfully wrote this secret, and the canned description of what
// changed.
type secretRepair struct {
	At     time.Time
	Detail string
}

// repairIndex is the whole page's "last repaired" answer, precomputed.
//
// P3-F1 — WHY THIS EXISTS. The two lookups below used to each walk the
// entire audit ring PER ROW: the comment on the old
// lastConnectionSecretRepair claimed "one scan for the whole page", but it
// was called once per cluster and once per cluster+addon pair, so a page
// with 50 clusters and 200 values rows walked a 1000-entry ring 250 times
// — a quarter of a million comparisons to fill in a column. Now the ring
// is walked ONCE, into two maps, and every row is an O(1) lookup.
//
// Connection and values repairs are kept in SEPARATE maps rather than one,
// even though their resource strings ("cluster:prod-eu" vs
// "cluster:prod-eu/addon:datadog") can't collide today. One map would mean
// a values event landing on a connection-shaped resource — a bug, a
// rename, anything — could silently fill in the wrong row's "last
// repaired". Two maps make that impossible by construction rather than by
// the resource strings happening to stay different.
type repairIndex struct {
	connection map[string]secretRepair
	values     map[string]secretRepair
}

// buildRepairIndex walks the audit entries ONCE, newest-first (the order
// audit.Log.List returns), and keeps the FIRST match per resource — which
// is therefore the most recent one, the same answer the old per-row scans
// returned.
func buildRepairIndex(entries []audit.Entry) repairIndex {
	idx := repairIndex{
		connection: make(map[string]secretRepair),
		values:     make(map[string]secretRepair),
	}
	for _, e := range entries {
		if e.Result != "success" || e.Resource == "" {
			continue
		}
		if d, isRepair := connectionSecretRepairDetail[e.Event]; isRepair {
			if _, seen := idx.connection[e.Resource]; !seen {
				idx.connection[e.Resource] = secretRepair{At: e.Timestamp, Detail: d}
			}
			continue
		}
		if d, isRepair := addonValuesSecretRepairDetail[e.Event]; isRepair {
			if _, seen := idx.values[e.Resource]; !seen {
				idx.values[e.Resource] = secretRepair{At: e.Timestamp, Detail: d}
			}
		}
	}
	return idx
}

// lastConnectionSecretRepair reads one cluster's most recent successful
// connection-secret write out of the prebuilt index. audit.Entry.Resource
// already carries "cluster:<name>" for exactly these events — see
// internal/clusterreconciler/reconciler.go's r.audit calls.
func (idx repairIndex) lastConnectionSecretRepair(clusterName string) (secretRepair, bool) {
	r, ok := idx.connection["cluster:"+clusterName]
	return r, ok
}

// buildAddonValuesSecretRows projects one row per cluster+addon pair that
// has both a registered secret definition AND the addon enabled on that
// cluster (via the cluster's addon labels — already present on the Cluster
// model from the same listing read, no extra I/O).
func (s *Server) buildAddonValuesSecretRows(clusters []models.Cluster, repairs repairIndex, engineEnabled bool) []addonValuesSecretRow {
	// Resolved once per request and stamped onto every row it applies to
	// (S1). One addon-secret backend serves the whole server today, so
	// every values row currently gets the same answer — but the ANSWER
	// lives on the row, which is what a reader groups, filters and sorts
	// by, and what a future per-addon backend would vary.
	source := s.addonValuesSecretSourceLabel()

	s.addonSecretDefsMu.RLock()
	defNames := make([]string, 0, len(s.addonSecretDefs))
	defs := make(map[string]struct{ SecretName, Namespace string }, len(s.addonSecretDefs))
	for name, def := range s.addonSecretDefs {
		defNames = append(defNames, name)
		defs[name] = struct{ SecretName, Namespace string }{def.SecretName, def.Namespace}
	}
	s.addonSecretDefsMu.RUnlock()

	if len(defs) == 0 {
		return []addonValuesSecretRow{}
	}
	sort.Strings(defNames)

	rows := make([]addonValuesSecretRow, 0)
	for _, c := range clusters {
		if !c.Managed {
			continue
		}
		for _, addonName := range defNames {
			if !models.AddonLabelEnabled(c.Labels[addonName]) {
				continue
			}
			def := defs[addonName]
			state := addonValuesSecretRowState(s.secretReconciler, c.Name, addonName)
			row := addonValuesSecretRow{
				Cluster:         c.Name,
				Addon:           addonName,
				SecretName:      def.SecretName,
				SecretNamespace: def.Namespace,
				Source:          source,
				State:           state,
				LastCheckError:  addonValuesRowLastCheckError(s.secretReconciler, c.Name, addonName),
				// P2-C3: the values engine always repairs what it owns
				// (P1-A's ownership gate) — the one exception is a foreign
				// row, which Sharko will never touch.
				//
				// M4 (code review): a row must not also promise self-heal
				// when an admin has switched the engine off (Settings ->
				// Addon Values Engine) — the locked decision that single-row
				// Check/Sync stay ungated is not a promise that the periodic
				// pass will also fix this row, and with the engine off there
				// is no periodic pass.
				SelfHeals: engineEnabled && state != "foreign",
			}
			if s.secretReconciler != nil {
				if checkedAt, ok := s.secretReconciler.LastItemChecked(c.Name, addonName); ok {
					row.LastChecked = checkedAt.UTC().Format(time.RFC3339)
				}
				if count, ok := s.secretReconciler.LastItemConsecutiveFailures(c.Name, addonName); ok {
					row.ConsecutiveFailures = count
				}
			}
			if rep, ok := repairs.lastAddonValuesSecretRepair(c.Name, addonName); ok {
				row.LastRepaired = rep.At.UTC().Format(time.RFC3339)
				row.LastRepairedDetail = rep.Detail
			}
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Cluster != rows[j].Cluster {
			return rows[i].Cluster < rows[j].Cluster
		}
		return rows[i].Addon < rows[j].Addon
	})
	return rows
}

// addonValuesSecretRowState derives one addon-values row's honest state
// from the reconciler's per-item outcome (SecretReconciler.LastItemOutcome
// — populated by the periodic pass AND by a single-item Refresh/Sync, S4).
// Mirrors connectionSecretState's shape but off a different, flatter
// vocabulary (internal/secrets.ItemOutcome has no drift sub-structure):
//
//   - recon == nil or never checked -> "unknown": nothing to report yet.
//   - "unchanged"/"created"/"updated" -> "in_sync": the secret matches its
//     source as of the last check or write.
//   - "out_of_sync" (a Refresh found a mismatch — a REAL comparison, Sharko
//     looked and it differs) -> "out_of_sync".
//   - "error" (P1-B symmetry fix, same #120 bug this lane already fixed on
//     the connection side): a check or push attempt itself FAILED — Sharko
//     does not know whether the secret matches, which is a different fact
//     from "compared and it differs" -> "unknown", never "out_of_sync".
//     LastCheckError (addonValuesRowLastCheckError) carries the canned
//     reason alongside this state, unchanged by this fix.
//   - "missing" (a Refresh found no Secret at all) -> "missing".
//   - "skipped" (the catalog's push definition is incomplete) -> "unknown"
//     — the reconciler deliberately took no position, same as
//     connectionSecretState's own catch-all.
func addonValuesSecretRowState(recon SecretReconciler, cluster, addon string) string {
	if recon == nil {
		return "unknown"
	}
	outcome, ok := recon.LastItemOutcome(cluster, addon)
	if !ok {
		return "unknown"
	}
	switch outcome {
	case "unchanged", "created", "updated":
		return "in_sync"
	case "out_of_sync":
		return "out_of_sync"
	case "missing":
		return "missing"
	case "foreign":
		// P1-A: a secret with this name is on the cluster and Sharko did
		// not create it. Its own state, on purpose — it is neither drift
		// (nothing to correct) nor damage (nothing is broken) nor unknown
		// (Sharko looked and knows exactly what it found). It is a boundary,
		// and Sharko stays on its side of it.
		return "foreign"
	default: // "error", "skipped"
		// "error": a failed check is not drift (P1-B). "skipped": the
		// reconciler deliberately took no position — both collapse to the
		// same honest "Sharko does not know" state.
		return "unknown"
	}
}

// addonValuesRowLastCheckError reads the reconciler's RAW per-item error
// (S8) and maps it through addonValuesSecretCheckFailureSentence before it
// ever reaches the row struct — the JSON response never carries the raw
// string. recon == nil or no recorded error both return "" (the common
// case: nothing to report).
func addonValuesRowLastCheckError(recon SecretReconciler, cluster, addon string) string {
	if recon == nil {
		return ""
	}
	raw, ok := recon.LastItemError(cluster, addon)
	if !ok {
		return ""
	}
	return addonValuesSecretCheckFailureSentence(raw)
}

// addonValuesSecretCheckFailureSentence maps a secrets-reconciler item
// error to a safe, canned sentence (S8's chosen route — see the security
// note on secrets.Reconciler.LastItemError). The reconciler wraps
// secrets-provider errors verbatim
// (internal/secrets/reconciler.go's reconcileSecret, e.g.
// `fmt.Errorf("fetching %q from provider: %w", providerPath, err)`) — a
// misbehaving provider SDK could in principle echo a fragment of a value
// inside its own error text, and this project has had a near-miss on
// exactly that kind of leak before. Rather than trust every string that
// could reach this function, categorize by which STAGE of the check
// failed — never render the wrapped error's own text back to the browser —
// and return one fixed, pre-written sentence per stage. Same "canned
// detail, not the raw string" choice connectionSecretRepairDetail already
// makes for repair events, just applied to failures instead of successes.
//
// The match order matters: "the secret definition in the catalog has no"
// (the MissingFields skip, always this project's own message, never a
// wrapped provider error) is checked before the provider/network cases so
// a catalog-authoring mistake reads as exactly that, not a vault outage.
func addonValuesSecretCheckFailureSentence(errMsg string) string {
	switch {
	case errMsg == "":
		return ""
	// H1 (code review): CheckOne (internal/api/addon_secret_single.go's
	// handleRefreshAddonValuesSecret) now routes its error through this
	// function too, not just LastItemError — so the two extra shapes
	// findWork can return (no Git connection configured, and the catalog
	// itself couldn't be read) need their own cases here, checked before the
	// generic stage cases below so they don't fall through to the vaguer
	// default.
	case strings.Contains(errMsg, "no Git connection is configured"):
		return "Sharko has no Git connection configured — there is nothing to check."
	case strings.Contains(errMsg, "could not read the addon catalog or managed clusters list"):
		return "Sharko couldn't read the addon catalog or managed-clusters file in git. Check that Sharko can reach your git host, then try again."
	case strings.Contains(errMsg, "no addon-values secret is defined for"):
		return "No addon-values secret is defined for this cluster and addon — check that the cluster is registered, the addon is enabled on it, and the addon's catalog entry defines a secret to push."
	case strings.Contains(errMsg, "the secret definition in the catalog has no"):
		return "The secret definition in the catalog is incomplete — fill in the missing fields."
	case strings.Contains(errMsg, "skip certificate checks"):
		// remoteclient.ErrUnverifiedDestination (task #152 lane C), asked
		// early in checkWork/reconcileSecret before any value is fetched —
		// a deliberate refusal, not a failed check. This case was missing
		// here (loose end from lane C — the sibling engine-level mappers,
		// clusterreconciler.FailureSentence and secrets.FailureSentence,
		// already had it; this per-row one didn't, so a refused row fell
		// through to the generic "last check didn't finish" default
		// instead of the real, safe reason). Checked before the "provider"
		// case below on purpose: it must win even though the wrapping text
		// ("fetching %q from provider: %w") also contains the word
		// "provider".
		return "This cluster's connection is set up to skip certificate checks, so Sharko refused to send it a secret."
	case strings.Contains(errMsg, "secret path refused"):
		// internal/providers.ErrSecretPathRefused (task #152 story B) — the
		// AWS-prefix and Kubernetes-namespace boundary refusals. Also a
		// deliberate refusal, not a failure, and also safe to show verbatim
		// (see secretPathRefusedSentence's doc comment: the refusal names
		// only the refused path and the configured prefix/namespace, both
		// Git/config metadata, never secret material). Checked before
		// "provider" below for the same reason as the TLS case above.
		return secretPathRefusedSentence(errMsg)
	case strings.Contains(errMsg, "getting credentials"):
		return "Sharko couldn't get credentials for this cluster."
	case strings.Contains(errMsg, "connecting to cluster"):
		return "Sharko couldn't connect to this cluster."
	case strings.Contains(errMsg, "provider"):
		return "Sharko couldn't fetch this secret's value from the vault."
	case strings.Contains(errMsg, "existing secret"):
		return "Sharko couldn't read the existing secret on this cluster."
	case strings.Contains(errMsg, "creating secret"):
		return "Sharko couldn't create this secret on the cluster."
	case strings.Contains(errMsg, "updating secret"):
		return "Sharko couldn't update this secret on the cluster."
	default:
		return "The last check didn't finish."
	}
}

// secretPathRefusedSentence extracts the safe, canned sentence
// internal/providers/boundary.go's boundary-refusal constructors
// (awsNoPrefixRefusal, awsOutsidePrefixRefusal, k8sNoNamespaceRefusal,
// k8sOutsideNamespaceRefusal) produce, dropping the "fetching ... from ...
// provider:" wrapper reconcileSecret/checkWork/syncWork add around it
// (internal/secrets/reconciler.go) — so a row shows the provider's own
// words, the exact text an operator already sees at every other door (task
// #152 story B's "three-door parity" requirement). Safe to show verbatim:
// boundary.go's own doc comment says these sentences name only the refused
// path and the configured boundary — Git/config metadata, never secret
// material. Falls back to the full wrapped string if the marker somehow
// isn't found (defensive only — every boundary.go constructor wraps
// ErrSecretPathRefused with fmt.Errorf("%w: ...", ...), so the marker is
// always present in practice).
func secretPathRefusedSentence(errMsg string) string {
	const marker = "secret path refused: "
	if idx := strings.Index(errMsg, marker); idx != -1 {
		return errMsg[idx+len(marker):]
	}
	return errMsg
}

// addonValuesSecretSyncFailureSentence is addonValuesSecretCheckFailureSentence's
// sync-side twin (H1, code review). handleSyncAddonValuesSecret
// (internal/api/addon_secret_single.go) used to pass SyncOne's error
// straight through to writeError as err.Error() — the same raw-reconciler-
// text leak CheckOne had, plus SyncOne can additionally fail on the two
// write-only stages (creating/updating a secret) that a pure check never
// reaches. Kept as its own function, not a shared one, because the wording
// says "sync", not "check" — a person who clicked Sync and got told "the
// last CHECK didn't finish" would be looking at the wrong verb for the
// button they pressed.
func addonValuesSecretSyncFailureSentence(errMsg string) string {
	switch {
	case errMsg == "":
		return ""
	case strings.Contains(errMsg, "Someone else created this one"):
		// Passed through UNCHANGED, deliberately — the one exception to
		// "never echo the raw string" in this function. secrets.
		// ErrForeignSecret is not wrapped provider/K8s error text; it is
		// Sharko's own fixed, safe, complete sentence, and demo mode's
		// demoForeignRefusal (internal/demo/addon_values_reconciler.go) is
		// kept word-for-word identical to it on purpose — a Sync refusal on
		// a foreign secret must read exactly the same whether it came from
		// the real engine or the demo one. Rewording it here would break
		// that pinned contract for no safety gain.
		return errMsg
	case strings.Contains(errMsg, "no Git connection is configured"):
		return "Sharko has no Git connection configured — there is nothing to sync."
	case strings.Contains(errMsg, "could not read the addon catalog or managed clusters list"):
		return "Sharko couldn't read the addon catalog or managed-clusters file in git. Check that Sharko can reach your git host, then try again."
	case strings.Contains(errMsg, "no addon-values secret is defined for"):
		return "No addon-values secret is defined for this cluster and addon — check that the cluster is registered, the addon is enabled on it, and the addon's catalog entry defines a secret to push."
	case strings.Contains(errMsg, "the secret definition in the catalog has no"):
		return "The secret definition in the catalog is incomplete — fill in the missing fields."
	case strings.Contains(errMsg, "skip certificate checks"):
		// remoteclient.ErrUnverifiedDestination (task #152 lane C) — same
		// loose end as addonValuesSecretCheckFailureSentence's own case
		// above, missing here too (SyncOne drives the same reconcileSecret
		// refusal CheckOne's checkWork does). Same sentence as the check
		// twin, deliberately: the refusal names Sharko's own fixed reason,
		// not a "check" or "sync" verb, so there's nothing to say
		// differently between the two doors.
		return "This cluster's connection is set up to skip certificate checks, so Sharko refused to send it a secret."
	case strings.Contains(errMsg, "secret path refused"):
		// internal/providers.ErrSecretPathRefused (task #152 story B) — see
		// secretPathRefusedSentence's doc comment. Checked before
		// "provider" below for the same reason as the TLS case above.
		return secretPathRefusedSentence(errMsg)
	case strings.Contains(errMsg, "getting credentials"):
		return "Sharko couldn't get credentials for this cluster."
	case strings.Contains(errMsg, "connecting to cluster"):
		return "Sharko couldn't connect to this cluster."
	case strings.Contains(errMsg, "provider"):
		return "Sharko couldn't fetch this secret's value from the vault."
	case strings.Contains(errMsg, "existing secret"):
		return "Sharko couldn't read the existing secret on this cluster."
	case strings.Contains(errMsg, "creating secret"):
		return "Sharko couldn't create this secret on the cluster."
	case strings.Contains(errMsg, "updating secret"):
		return "Sharko couldn't update this secret on the cluster."
	default:
		return "The last sync didn't finish."
	}
}

// lastAddonValuesSecretRepair reads one cluster+addon pair's most recent
// successful values-secret write out of the prebuilt index. Resource
// "cluster:<name>/addon:<addon>" is written by internal/secrets.
// Reconciler's per-item audit callback (wired in cmd/sharko/serve.go) for
// exactly the events in addonValuesSecretRepairDetail.
func (idx repairIndex) lastAddonValuesSecretRepair(clusterName, addonName string) (secretRepair, bool) {
	r, ok := idx.values[fmt.Sprintf("cluster:%s/addon:%s", clusterName, addonName)]
	return r, ok
}

// buildOrphanedSecretRows projects the addon-values engine's in-memory
// orphan-scan snapshot (leftover-secrets S1) into the response shape — no
// K8s call, no dependency on a live Git/ArgoCD connection: this reads what
// the reconciler's own scan passes already found (internal/secrets/
// orphans.go), the same "list is a pure in-memory read" rule
// buildAddonValuesSecretRows follows for the reconciler's item records.
func (s *Server) buildOrphanedSecretRows() []orphanedSecretRow {
	rows := make([]orphanedSecretRow, 0)
	if s.secretReconciler == nil {
		return rows
	}
	source := s.addonValuesSecretSourceLabel()
	for _, o := range s.secretReconciler.OrphanedSecrets() {
		rows = append(rows, orphanedSecretRow{
			Cluster:         o.Cluster,
			SecretName:      o.Name,
			SecretNamespace: o.Namespace,
			Addon:           o.Addon,
			State:           "orphaned",
			Source:          source,
			LastChecked:     o.LastChecked.UTC().Format(time.RFC3339),
		})
	}
	return rows
}

// clusterConnectionEngineInfo reports the cluster-secret reconciler's own
// cadence + health — an in-memory read on s.clusterRecon, never a K8s call.
func (s *Server) clusterConnectionEngineInfo() managedSecretsEngineInfo {
	if s.clusterRecon == nil {
		return managedSecretsEngineInfo{}
	}
	info := managedSecretsEngineInfo{
		Wired:           true,
		IntervalSeconds: int(s.clusterRecon.TickInterval().Seconds()),
		// This engine has no off switch, on purpose (gitops-proud P4-I,
		// D2 — it is Sharko's own job, never something another tool might
		// already be doing) — always reports enabled.
		Enabled: true,
	}
	if t := s.clusterRecon.LastRunTime(); !t.IsZero() {
		info.LastRun = t.UTC().Format(time.RFC3339)
	}
	if cluster, msg, at, ok := s.clusterRecon.LastError(); ok {
		info.LastError = msg
		info.LastErrorCluster = cluster
		info.LastErrorAt = at.UTC().Format(time.RFC3339)
	}
	return info
}

// addonValuesSecretSourceLabel names the real backend addon-values secrets
// are compared against, from the same config the addon-secret provider
// factory itself reads (s.addonSecretCfg) — never a fixed, possibly-wrong
// product name. Types with no dedicated backend implementation today
// ("vault" — the doc comment on AddonSecretProviderConfig.Type notes "future;
// current code has no vault factory") or no config at all fall back to the
// generic, lowercase, article-free "secrets store" — honest about not
// knowing, rather than guessing a product name Sharko isn't actually using.
//
// G2 (gitops-proud P4-G): "demo" gets its OWN name here, not the generic
// fallback — demo mode is a real, known backend (internal/demo/setup.go
// sets this exact Type on every SetupDemoServer call), not an unimplemented
// or misconfigured one, so `make demo-big`'s rows earn the same courtesy
// every real provider above already gets, instead of showing the same
// "secrets store" fallback on every single row. This is demo-scoped by
// construction: the "demo" case only ever matches when demo mode itself set
// the Type, so no production path is inventing a name here.
func (s *Server) addonValuesSecretSourceLabel() string {
	cfg := s.addonSecretCfg()
	if cfg == nil {
		return "secrets store"
	}
	switch cfg.Type {
	case "aws-sm", "aws-secrets-manager":
		return "AWS Secrets Manager"
	case "k8s-secrets", "kubernetes":
		return "a Kubernetes Secret"
	case "gcp", "gcp-sm", "google-secret-manager":
		return "Google Secret Manager"
	case "azure", "azure-kv", "azure-key-vault":
		return "Azure Key Vault"
	case "demo":
		return "the demo secrets store"
	default:
		return "secrets store"
	}
}

// addonValuesEngineInfo reports the addon-values reconciler's own cadence +
// health — an in-memory read on s.secretReconciler, never a K8s call.
//
// LastError is ALREADY the mapped, canned sentence (secrets.Reconciler.
// LastError applies secrets.FailureSentence before returning — P1-B B2).
// LastErrorCluster/LastErrorAt (new here) mirror the cluster-connection
// engine's fields added by #716, so the page's red line for this engine can
// name a cluster and a time exactly like the connection one does — and so
// its click-to-filter behaviour (EngineStat in ManagedSecrets.tsx, already
// generic over both engines) has something to filter to.
// engineEnabled is the addon_values_engine_enabled setting (gitops-proud
// P4-I, D2) — a single, repo-wide server setting, not a per-cluster fact.
// L14 (code review): the CALLER reads it once per request
// (GetManagedSecretsSettings, alongside managed_cluster_self_heal, in one
// ConfigMap Read) and passes it down here instead of this function reading
// the settings store itself — this used to be its own
// s.settingsStore.IsAddonValuesEngineEnabled call, a second live K8s API
// GET of the same ConfigMap object buildConnectionSecretRows's self-heal
// read also hits.
func (s *Server) addonValuesEngineInfo(ctx context.Context, engineEnabled bool) managedSecretsEngineInfo {
	if s.secretReconciler == nil {
		return managedSecretsEngineInfo{}
	}
	info := managedSecretsEngineInfo{
		Wired:           true,
		IntervalSeconds: int(s.secretReconciler.Interval().Seconds()),
		LastError:       s.secretReconciler.LastError(),
		Enabled:         engineEnabled,
	}
	if t := s.secretReconciler.LastRunTime(); !t.IsZero() {
		info.LastRun = t.UTC().Format(time.RFC3339)
	}
	if info.LastError != "" {
		info.LastErrorCluster = s.secretReconciler.LastErrorCluster()
		if at := s.secretReconciler.LastErrorAt(); !at.IsZero() {
			info.LastErrorAt = at.UTC().Format(time.RFC3339)
		}
	}
	return info
}
