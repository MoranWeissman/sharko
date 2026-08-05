package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/MoranWeissman/sharko/internal/audit"
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
	Engines                  managedSecretsEngines  `json:"engines"`
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
// @Description Aggregates cluster-connection secrets (the ArgoCD cluster Secret per managed cluster) and addon-values secrets (pushed into remote clusters), plus each reconciler engine's cadence, last run, and last error. Built entirely from data already read for the cluster list and the two reconcilers' in-memory stats — no per-row Kubernetes call. A fact the server cannot currently determine is left empty/unknown rather than approximated. Any authenticated user may read this, the same convention as /providers, /config, and /secrets/status.
// @Tags system
// @Produce json
// @Security BearerAuth
// @Success 200 {object} managedSecretsResponse "Managed secrets summary"
// @Router /system/managed-secrets [get]
func (s *Server) handleGetManagedSecrets(w http.ResponseWriter, r *http.Request) {
	resp := managedSecretsResponse{
		ClusterConnectionSecrets: []connectionSecretRow{},
		AddonValuesSecrets:       []addonValuesSecretRow{},
	}

	// The engines section never depends on a live Git/ArgoCD connection —
	// build it first so a connection outage still reports engine health.
	resp.Engines.ClusterConnection = s.clusterConnectionEngineInfo()
	resp.Engines.AddonValues = s.addonValuesEngineInfo()
	resp.AddonValuesSecretSource = s.addonValuesSecretSourceLabel()

	gp, gpErr := s.connSvc.GetActiveGitProvider()
	ac, acErr := s.connSvc.GetActiveArgocdClient()
	if gpErr != nil || acErr != nil {
		// Degrade, never 500: the two tables need the cluster list, which
		// needs a live Git+ArgoCD connection. The engines section above
		// still stands on its own.
		writeJSON(w, http.StatusOK, resp)
		return
	}

	listResp, err := s.clusterSvc.ListClusters(r.Context(), gp, ac)
	if err != nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Per-cluster reconcile outcomes are an in-memory lookup on
	// s.clusterRecon — no extra ArgoCD/K8s call, the same enrichment
	// handleListClusters already applies to every cluster it returns.
	for i := range listResp.Clusters {
		applyLastReconcile(&listResp.Clusters[i], s.clusterRecon)
	}

	auditEntries := s.AuditLog().List(0)

	resp.ClusterConnectionSecrets = s.buildConnectionSecretRows(r.Context(), listResp.Clusters, auditEntries)
	resp.AddonValuesSecrets = s.buildAddonValuesSecretRows(listResp.Clusters, auditEntries)

	writeJSON(w, http.StatusOK, resp)
}

// buildConnectionSecretRows projects one row per Sharko-managed cluster.
// Discovered/orphan clusters (Managed == false) are excluded — Sharko has
// no connection secret of its own to report for a cluster it didn't
// register.
func (s *Server) buildConnectionSecretRows(ctx context.Context, clusters []models.Cluster, auditEntries []audit.Entry) []connectionSecretRow {
	_, ns, _ := s.k8sClientAndNamespace()

	// The managed_cluster_self_heal setting is read ONCE per request (P2-C3)
	// — it is a single, repo-wide server setting, not a per-cluster fact,
	// so there is no reason to ask the settings store once per row. A nil
	// settingsStore (out-of-cluster mode) reads as "off", matching
	// clusterreconciler.Deps.SelfHealFn's own "nil means default OFF"
	// contract.
	v3SelfHealOn := s.settingsStore != nil && s.settingsStore.IsManagedClusterSelfHealEnabled(ctx)

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
		}
		row.SelfHeals = connectionSelfHeals(c, row.ComparedPath, v3SelfHealOn)
		row.DriftSource = connectionDriftSource(row.State, row.ComparedRevision, row.AppliedRevision)

		if at, detail, ok := lastConnectionSecretRepair(auditEntries, c.Name); ok {
			row.LastRepaired = at.UTC().Format(time.RFC3339)
			row.LastRepairedDetail = detail
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
	case rec.Outcome == string(clusterreconciler.OutcomeSkipped) && rec.Message == clusterreconciler.SelfManagedSecretNotCreatedMessage:
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
	return clusterreconciler.FailureSentence(rec.Message)
}

// lastConnectionSecretRepair scans the audit log (newest-first, per
// audit.Log.List) for the most recent successful write to this cluster's
// connection secret. This is an existing query pattern (audit.Entry.Resource
// already carries "cluster:<name>" for exactly these events — see
// internal/clusterreconciler/reconciler.go's r.audit calls) read once for
// the whole page rather than filtered per-cluster, so 50 clusters cost one
// scan of the in-memory ring buffer, not 50.
func lastConnectionSecretRepair(entries []audit.Entry, clusterName string) (at time.Time, detail string, ok bool) {
	want := "cluster:" + clusterName
	for _, e := range entries {
		if e.Resource != want || e.Result != "success" {
			continue
		}
		if d, isRepair := connectionSecretRepairDetail[e.Event]; isRepair {
			return e.Timestamp, d, true
		}
	}
	return time.Time{}, "", false
}

// buildAddonValuesSecretRows projects one row per cluster+addon pair that
// has both a registered secret definition AND the addon enabled on that
// cluster (via the cluster's addon labels — already present on the Cluster
// model from the same listing read, no extra I/O).
func (s *Server) buildAddonValuesSecretRows(clusters []models.Cluster, auditEntries []audit.Entry) []addonValuesSecretRow {
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
				SelfHeals: state != "foreign",
			}
			if s.secretReconciler != nil {
				if checkedAt, ok := s.secretReconciler.LastItemChecked(c.Name, addonName); ok {
					row.LastChecked = checkedAt.UTC().Format(time.RFC3339)
				}
			}
			if at, detail, ok := lastAddonValuesSecretRepair(auditEntries, c.Name, addonName); ok {
				row.LastRepaired = at.UTC().Format(time.RFC3339)
				row.LastRepairedDetail = detail
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
	case strings.Contains(errMsg, "the secret definition in the catalog has no"):
		return "The secret definition in the catalog is incomplete — fill in the missing fields."
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

// lastAddonValuesSecretRepair scans the audit log (newest-first, per
// audit.Log.List) for the most recent successful write to this
// cluster+addon's values secret. Mirrors lastConnectionSecretRepair's
// one-scan-for-the-whole-page shape; Resource "cluster:<name>/addon:<addon>"
// is written by internal/secrets.Reconciler's per-item audit callback
// (wired in cmd/sharko/serve.go) for exactly the events in
// addonValuesSecretRepairDetail.
func lastAddonValuesSecretRepair(entries []audit.Entry, clusterName, addonName string) (at time.Time, detail string, ok bool) {
	want := fmt.Sprintf("cluster:%s/addon:%s", clusterName, addonName)
	for _, e := range entries {
		if e.Resource != want || e.Result != "success" {
			continue
		}
		if d, isRepair := addonValuesSecretRepairDetail[e.Event]; isRepair {
			return e.Timestamp, d, true
		}
	}
	return time.Time{}, "", false
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
// current code has no vault factory"), no config at all, or a non-production
// type (the demo server's synthetic "demo" backend) fall back to the
// generic, lowercase, article-free "secrets store" — honest about not
// knowing, rather than guessing a product name Sharko isn't actually using.
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
func (s *Server) addonValuesEngineInfo() managedSecretsEngineInfo {
	if s.secretReconciler == nil {
		return managedSecretsEngineInfo{}
	}
	info := managedSecretsEngineInfo{
		Wired:           true,
		IntervalSeconds: int(s.secretReconciler.Interval().Seconds()),
		LastError:       s.secretReconciler.LastError(),
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
