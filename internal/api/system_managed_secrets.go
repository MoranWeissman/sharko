package api

import (
	"net/http"
	"sort"
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

// managedSecretsResponse is the response body for GET /api/v1/system/managed-secrets.
type managedSecretsResponse struct {
	ClusterConnectionSecrets []connectionSecretRow  `json:"cluster_connection_secrets"`
	AddonValuesSecrets       []addonValuesSecretRow `json:"addon_values_secrets"`
	Engines                  managedSecretsEngines  `json:"engines"`
}

// connectionSecretRow is one row of cluster_connection_secrets — one row
// per managed cluster.
type connectionSecretRow struct {
	Cluster         string `json:"cluster"`
	SecretNamespace string `json:"secret_namespace,omitempty"`
	SecretName      string `json:"secret_name,omitempty"`
	// State is one of "in_sync", "out_of_sync", "missing", or "unknown" —
	// see connectionSecretState for exactly how each is derived.
	State string `json:"state"`
	// LastChecked is RFC3339, or "" when the reconciler has never
	// processed this cluster on this server instance.
	LastChecked string `json:"last_checked,omitempty"`
	// LastRepaired / LastRepairedDetail come from a matching audit entry
	// (Resource "cluster:<name>", a repair event, Result "success") — an
	// honest per-row join, not an approximation. Empty when no such entry
	// exists in the audit log's retained window.
	LastRepaired       string `json:"last_repaired,omitempty"`
	LastRepairedDetail string `json:"last_repaired_detail,omitempty"`
}

// addonValuesSecretRow is one row of addon_values_secrets — one row per
// cluster+addon pair that has both a secret definition and the addon
// enabled on that cluster.
//
// LastChecked/LastRepaired are deliberately absent from this struct: the
// addon-values reconciler (internal/secrets) only keeps an aggregate,
// pass-level stat (created/updated counts for the whole pass) — it has no
// per-secret timestamp to report, and its audit trail summarizes the same
// way (one "secret_push" entry per pass, not one per secret). Per-row
// history is a known gap here; the engine-level addon_values figures below
// are the closest honest signal. See the story report for this gap.
type addonValuesSecretRow struct {
	Cluster         string `json:"cluster"`
	Addon           string `json:"addon"`
	SecretName      string `json:"secret_name,omitempty"`
	SecretNamespace string `json:"secret_namespace,omitempty"`
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

	resp.ClusterConnectionSecrets = s.buildConnectionSecretRows(listResp.Clusters, auditEntries)
	resp.AddonValuesSecrets = s.buildAddonValuesSecretRows(listResp.Clusters)

	writeJSON(w, http.StatusOK, resp)
}

// buildConnectionSecretRows projects one row per Sharko-managed cluster.
// Discovered/orphan clusters (Managed == false) are excluded — Sharko has
// no connection secret of its own to report for a cluster it didn't
// register.
func (s *Server) buildConnectionSecretRows(clusters []models.Cluster, auditEntries []audit.Entry) []connectionSecretRow {
	_, ns, _ := s.k8sClientAndNamespace()

	rows := make([]connectionSecretRow, 0, len(clusters))
	for _, c := range clusters {
		if !c.Managed {
			continue
		}
		row := connectionSecretRow{Cluster: c.Name}
		if ns != "" {
			// The Secret's Name always equals the cluster's Name (see
			// argosecrets.Manager.Ensure) — same deterministic fact
			// applyManagedSecretFields uses for the per-cluster page.
			row.SecretNamespace = ns
			row.SecretName = c.Name
		}
		row.State, row.LastChecked = connectionSecretState(c.LastReconcile)

		if at, detail, ok := lastConnectionSecretRepair(auditEntries, c.Name); ok {
			row.LastRepaired = at.UTC().Format(time.RFC3339)
			row.LastRepairedDetail = detail
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Cluster < rows[j].Cluster })
	return rows
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
//     self-managed fight in progress), or Outcome "failed" -> "out_of_sync".
//   - Any other "skipped" reason (a git read failure that left labels
//     untouched, a same-name unlabeled Secret in Adopt territory, etc.) ->
//     "unknown" — collapsing these into "in sync" or "out of sync" would
//     overclaim; the reconciler deliberately took no position this tick.
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
	case rec.Outcome == string(clusterreconciler.OutcomeSucceeded), rec.Outcome == string(clusterreconciler.OutcomeFailed):
		return "out_of_sync", lastChecked
	default:
		return "unknown", lastChecked
	}
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
func (s *Server) buildAddonValuesSecretRows(clusters []models.Cluster) []addonValuesSecretRow {
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
			rows = append(rows, addonValuesSecretRow{
				Cluster:         c.Name,
				Addon:           addonName,
				SecretName:      def.SecretName,
				SecretNamespace: def.Namespace,
			})
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
	if msg, _, ok := s.clusterRecon.LastError(); ok {
		info.LastError = msg
	}
	return info
}

// addonValuesEngineInfo reports the addon-values reconciler's own cadence +
// health — an in-memory read on s.secretReconciler, never a K8s call.
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
	return info
}
